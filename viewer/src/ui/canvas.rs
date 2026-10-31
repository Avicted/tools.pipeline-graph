//! The map: scenes (what is on the canvas), their cached layouts, and the
//! painter that draws only what is on screen, with level of detail.

use super::job::{Job, Poll};
use super::paint::{draw_edges, draw_lanes, draw_legend, draw_nodes, Highlight};
use super::theme::Palette;
use super::workspace::{LineageMode, Screen, Workspace};
use crate::core::scene::{build_scene, Scene, SceneLayout, DEFAULT_LIMIT};
use crate::core::spatial::Rect;
use egui::{Color32, CursorIcon, Pos2, RichText, Sense, Vec2};
use std::collections::HashMap;
use std::sync::Arc;

/// How many finished layouts are kept for switching back and forth.
const LAYOUTS_KEPT: usize = 6;

#[derive(Default)]
pub struct LayoutCache {
    map: HashMap<(Scene, usize), Arc<SceneLayout>>,
    order: Vec<(Scene, usize)>,
    pending: HashMap<(Scene, usize), Job<Arc<SceneLayout>>>,
    /// Layouts whose thread died; not started again.
    failed: std::collections::HashSet<(Scene, usize)>,
}

#[derive(Clone, Copy)]
struct Anim {
    from_pan: Vec2,
    from_k: f32,
    to_pan: Vec2,
    to_k: f32,
    t0: f64,
}

#[derive(Default)]
pub struct CanvasState {
    pub pan: Vec2,
    pub k: f32,
    pub fit_pending: bool,
    /// Fit everything, however small that makes it (the Fit button).
    pub fit_all: bool,
    pub center_on: Option<u32>,
    fitted: Option<Scene>,
    limit_for: Option<Scene>,
    anim: Option<Anim>,
    seen_n: Vec<u32>,
    seen_e: Vec<u32>,
    stamp: u32,
    vis_n: Vec<u32>,
    vis_e: Vec<u32>,
    view: Vec2,
}

impl Workspace {
    pub fn poll_layouts(&mut self) {
        let mut done: Vec<(Scene, usize)> = vec![];
        let mut lost: Vec<(Scene, usize)> = vec![];
        for (key, job) in &self.layouts.pending {
            match job.poll() {
                Poll::Pending => {}
                Poll::Ready(l) => {
                    self.layouts.map.insert(key.clone(), l);
                    self.layouts.order.retain(|x| x != key);
                    self.layouts.order.push(key.clone());
                    done.push(key.clone());
                }
                Poll::Lost => lost.push(key.clone()),
            }
        }
        for key in done.iter().chain(&lost) {
            self.layouts.pending.remove(key);
        }
        if !lost.is_empty() {
            // remembered, or the next frame would start the same failing layout again
            self.layouts.failed.extend(lost);
            self.toast("The layout stopped unexpectedly; pick another view");
        }
        while self.layouts.order.len() > LAYOUTS_KEPT {
            let old = self.layouts.order.remove(0);
            self.layouts.map.remove(&old);
        }
    }

    pub fn layouts_idle(&self) -> bool {
        self.layouts.pending.is_empty()
    }

    pub fn layout_for(&mut self, ctx: &egui::Context, scene: &Scene) -> Option<Arc<SceneLayout>> {
        let key = (scene.clone(), self.scene_limit);
        if let Some(l) = self.layouts.map.get(&key) {
            return Some(l.clone());
        }
        if !self.layouts.pending.contains_key(&key) && !self.layouts.failed.contains(&key) {
            let m = self.m.clone();
            let (s, limit) = key.clone();
            self.layouts.pending.insert(key, Job::spawn(ctx, move || Arc::new(build_scene(&m, &s, limit))));
        }
        None
    }

    pub fn show_map(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        self.map_toolbar(ui, pal);
        let scene = self.scene.clone();
        self.canvas_view(ui, pal, &scene);
    }

    fn map_toolbar(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        egui::Frame::new().inner_margin(egui::Margin::symmetric(12, 8)).show(ui, |ui| {
            ui.horizontal(|ui| {
                let big = self.m.g.len() > crate::core::scene::BIG_GRAPH;
                if ui.selectable_label(self.scene == Scene::Repos, "Repositories").clicked() {
                    self.scene = Scene::Repos;
                    self.canvas.fit_pending = true;
                }
                if ui
                    .selectable_label(self.scene == Scene::All, "All jobs")
                    .on_hover_text(if big { "Large graph: laying out every job can take a moment" } else { "Every job and image" })
                    .clicked()
                {
                    self.scene = Scene::All;
                    self.canvas.fit_pending = true;
                }
                let m = self.m.clone();
                let cur = match &self.scene {
                    Scene::Repo(r) => m.g.s(m.g.repos[*r as usize].name).to_string(),
                    _ => "Repository".to_string(),
                };
                egui::ComboBox::from_id_salt("repo-scene").selected_text(cur).show_ui(ui, |ui| {
                    for (i, r) in m.g.repos.iter().enumerate() {
                        if ui.selectable_label(self.scene == Scene::Repo(i as u32), m.g.s(r.name)).clicked() {
                            self.scene = Scene::Repo(i as u32);
                            self.canvas.fit_pending = true;
                        }
                    }
                });
                if let Scene::Focus(v) = self.scene {
                    ui.separator();
                    ui.label(RichText::new(format!("Lineage of {}", m.g.node_label(v))).strong());
                    if ui.small_button("×").on_hover_text("Close (Esc)").clicked() {
                        self.scene = if big { Scene::Repos } else { Scene::All };
                        self.canvas.fit_pending = true;
                    }
                }
                ui.separator();
                self.lineage_chips(ui, pal);
            });
        });
    }

    pub fn lineage_chips(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        let has = self.sel.is_some();
        ui.add_enabled_ui(has, |ui| {
            for (mode, label, tip, col) in [
                (LineageMode::Down, "Breaks  B", "Keep only what depends on the selection (blast radius)", pal.down),
                (LineageMode::Up, "Needs  D", "Keep only what the selection depends on", pal.up),
                (LineageMode::Both, "Both  I", "Keep the whole lineage", pal.accent),
            ] {
                let on = self.mode == mode;
                let text = RichText::new(label).color(if on { Color32::WHITE } else { col });
                let b = egui::Button::new(text).fill(if on { col } else { Color32::TRANSPARENT }).stroke(super::theme::stroke(1.0, col));
                if ui.add(b).on_hover_text(tip).clicked() {
                    self.set_mode(mode);
                }
            }
        });
        if let Some(t) = self.path_to {
            let name = self.m.g.node_label(t).to_string();
            ui.label(RichText::new(format!("route to {name}")).color(pal.accent));
            if ui.small_button("×").clicked() {
                self.path_to = None;
            }
        }
    }

    fn zoom_by(&mut self, f: f32) {
        let c = self.canvas.view / 2.0;
        let k = (self.canvas.k * f).clamp(0.03, 3.0);
        self.canvas.pan = c - (c - self.canvas.pan) * (k / self.canvas.k.max(0.0001));
        self.canvas.k = k;
    }

    /// Draws a scene filling the remaining space and handles all pointer input on it.
    pub fn canvas_view(&mut self, ui: &mut egui::Ui, pal: &Palette, scene: &Scene) {
        if self.canvas.fitted.as_ref() != Some(scene) && self.canvas.limit_for.as_ref() != Some(scene) {
            self.scene_limit = DEFAULT_LIMIT;
        }
        self.canvas.limit_for = Some(scene.clone());
        let Some(lay) = self.layout_for(ui.ctx(), scene) else {
            ui.centered_and_justified(|ui| {
                ui.vertical_centered(|ui| {
                    ui.add_space(ui.available_height() / 2.0 - 30.0);
                    ui.spinner();
                    ui.label(RichText::new("Laying out…").color(pal.muted));
                });
            });
            return;
        };
        let m = self.m.clone();
        let size = ui.available_size();
        let (resp, painter) = ui.allocate_painter(size, Sense::click_and_drag());
        let rect = resp.rect;
        painter.rect_filled(rect, 0, pal.bg);
        let painter = painter.with_clip_rect(rect);
        let origin = rect.min.to_vec2();
        let now = ui.input(|i| i.time);
        self.canvas.view = rect.size();

        self.update_camera(ui, scene, &lay, rect, now);
        self.pan_and_zoom(ui, &resp, origin);
        let (k, pan) = (self.canvas.k, self.canvas.pan);
        let to_screen = |p: [f32; 2]| Pos2::new(origin.x + pan.x + p[0] * k, origin.y + pan.y + p[1] * k);
        let view = Rect { x0: -pan.x / k, y0: -pan.y / k, x1: (rect.width() - pan.x) / k, y1: (rect.height() - pan.y) / k };
        let (vis_n, vis_e) = self.collect_visible(&lay, &view);

        // pointer
        let hover_pos = if self.shot.is_some() { None } else { resp.hover_pos() };
        let hover_w = hover_pos.map(|p| [(p.x - origin.x - pan.x) / k, (p.y - origin.y - pan.y) / k]);
        let mut hover_node: Option<u32> = None;
        if let Some(w) = hover_w {
            for &i in vis_n.iter().rev() {
                if lay.nodes[i as usize].rect.contains(w[0], w[1]) {
                    hover_node = Some(i);
                    break;
                }
            }
        }

        let hl = Highlight::new(self.lin.as_ref(), self.mode, self.screen != Screen::Compare);
        let diff = if self.compare.overlay { self.compare.diff.as_deref() } else { None };
        draw_lanes(&painter, &lay, pal, &view, rect, k, &to_screen);
        let hover_edge = draw_edges(&painter, &lay, pal, k, &to_screen, &vis_e, &hl, hover_node, hover_pos);
        draw_nodes(&painter, &lay, &m, pal, k, &to_screen, &vis_n, &hl, self.sel, hover_node, diff);
        self.canvas.vis_n = vis_n;
        self.canvas.vis_e = vis_e;

        self.pointer_actions(ui, &resp, scene, &lay, hover_node, hover_edge, hover_pos);
        self.zoom_controls(ui, rect);
        self.limit_notice(ui, rect, &lay);
        draw_legend(&painter, &lay, pal, rect);
    }

    /// Moves the camera: to a node asked for, to fit the scene, or along a running animation.
    fn update_camera(&mut self, ui: &egui::Ui, scene: &Scene, lay: &SceneLayout, rect: egui::Rect, now: f64) {
        // what to look at: fit on request or when a scene is shown for the first time
        if self.canvas.k == 0.0 {
            self.canvas.k = 1.0;
        }
        if let Some(v) = self.canvas.center_on.take() {
            match lay.index_of.get(&v) {
                Some(&i) if !lay.aggregated => {
                    let r = lay.nodes[i as usize].rect;
                    let k = self.canvas.k.clamp(0.7, 1.4);
                    let (cx, cy) = r.center();
                    self.start_anim(rect.size() / 2.0 - Vec2::new(cx, cy) * k, k, now);
                }
                _ if self.screen == Screen::Map && !lay.aggregated => {
                    self.scene = Scene::Focus(v);
                }
                _ => {}
            }
        }
        if self.canvas.fit_pending || self.canvas.fit_all || self.canvas.fitted.as_ref() != Some(scene) {
            let first = self.canvas.fitted.as_ref() != Some(scene);
            let pure = self.canvas.fit_all;
            self.canvas.fit_pending = false;
            self.canvas.fit_all = false;
            self.canvas.fitted = Some(scene.clone());
            let pad = 40.0;
            let fit_k = ((rect.width() - pad) / lay.w.max(1.0)).min((rect.height() - pad) / lay.h.max(1.0)).clamp(0.03, 1.0);
            // a long chain fitted to the window is unreadable: keep cards legible and start at the interesting end
            let readable = matches!(scene, Scene::Focus(_) | Scene::Flow(_)) && !pure;
            let k = if readable { fit_k.max(0.62) } else { fit_k };
            let mut pan = (rect.size() - Vec2::new(lay.w, lay.h) * k) / 2.0;
            if readable && lay.w * k > rect.width() - pad {
                let focus_x = match scene {
                    Scene::Focus(root) => lay.index_of.get(root).map(|&i| lay.nodes[i as usize].rect.center().0 * k),
                    _ => None,
                };
                pan.x = match focus_x {
                    Some(x) => rect.width() / 2.0 - x,
                    None => pad / 2.0,
                };
                pan.x = pan.x.clamp(rect.width() - lay.w * k - pad / 2.0, pad / 2.0);
            }
            if first {
                self.canvas.pan = pan;
                self.canvas.k = k;
                self.canvas.anim = None;
            } else {
                self.start_anim(pan, k, now);
            }
        }
        if let Some(a) = self.canvas.anim {
            let t = (((now - a.t0) / 0.22) as f32).clamp(0.0, 1.0);
            let e = 1.0 - (1.0 - t).powi(3);
            self.canvas.pan = a.from_pan + (a.to_pan - a.from_pan) * e;
            self.canvas.k = a.from_k + (a.to_k - a.from_k) * e;
            if t >= 1.0 {
                self.canvas.anim = None;
            } else {
                ui.ctx().request_repaint();
            }
        }
    }

    fn pan_and_zoom(&mut self, ui: &egui::Ui, resp: &egui::Response, origin: Vec2) {
        // pan and zoom
        if resp.dragged_by(egui::PointerButton::Primary) || resp.dragged_by(egui::PointerButton::Middle) {
            self.canvas.pan += resp.drag_delta();
            self.canvas.anim = None;
        }
        if resp.hovered() {
            let (scroll, zoom) = ui.input(|i| (i.smooth_scroll_delta, i.zoom_delta()));
            if scroll != Vec2::ZERO {
                self.canvas.pan += scroll;
                self.canvas.anim = None;
            }
            if zoom != 1.0 {
                if let Some(p) = resp.hover_pos() {
                    let c = p.to_vec2() - origin;
                    let k = (self.canvas.k * zoom).clamp(0.03, 3.0);
                    self.canvas.pan = c - (c - self.canvas.pan) * (k / self.canvas.k);
                    self.canvas.k = k;
                    self.canvas.anim = None;
                }
            }
        }
    }

    /// The nodes and edges that intersect the view, in order, using the layout's grids.
    fn collect_visible(&mut self, lay: &SceneLayout, view: &Rect) -> (Vec<u32>, Vec<u32>) {
        let cv = &mut self.canvas;
        cv.stamp = cv.stamp.wrapping_add(1).max(1);
        if cv.seen_n.len() < lay.nodes.len() {
            cv.seen_n.resize(lay.nodes.len(), 0);
        }
        if cv.seen_e.len() < lay.edges.len() {
            cv.seen_e.resize(lay.edges.len(), 0);
        }
        cv.vis_n.clear();
        cv.vis_e.clear();
        lay.ngrid.query(view, &mut cv.seen_n, cv.stamp, &mut cv.vis_n);
        lay.egrid.query(view, &mut cv.seen_e, cv.stamp, &mut cv.vis_e);
        cv.vis_n.retain(|&i| lay.nodes[i as usize].rect.intersects(view));
        cv.vis_e.retain(|&i| lay.edges[i as usize].bbox.intersects(view));
        cv.vis_n.sort_unstable();
        cv.vis_e.sort_unstable();
        (std::mem::take(&mut cv.vis_n), std::mem::take(&mut cv.vis_e))
    }

    #[allow(clippy::too_many_arguments)]
    fn pointer_actions(
        &mut self,
        ui: &mut egui::Ui,
        resp: &egui::Response,
        scene: &Scene,
        lay: &SceneLayout,
        hover_node: Option<u32>,
        hover_edge: Option<u32>,
        hover_pos: Option<Pos2>,
    ) {
        let m = self.m.clone();
        let g = &m.g;
        if hover_node.is_some() {
            ui.output_mut(|o| o.cursor_icon = CursorIcon::PointingHand);
        }
        if let (Some(ei), Some(_)) = (hover_edge, hover_pos) {
            let e = &lay.edges[ei as usize];
            let (a, b) = (lay.nodes[e.a as usize].gid, lay.nodes[e.b as usize].gid);
            let text = format!("{}  →  {}\n{}", g.node_label(a), g.node_label(b), e.kind.name());
            egui::show_tooltip_at_pointer(ui.ctx(), ui.layer_id(), egui::Id::new("edge-tip"), |ui: &mut egui::Ui| {
                // Never wrap: a tooltip re-placed every frame under a moving pointer is sized
                // from the room left beside it and collapses to a sliver.
                ui.add(egui::Label::new(text).wrap_mode(egui::TextWrapMode::Extend));
            });
        }
        let shift = ui.input(|i| i.modifiers.shift);
        if resp.double_clicked() {
            if let Some(i) = hover_node {
                if !lay.aggregated {
                    self.open_focus(lay.nodes[i as usize].gid);
                }
            }
        } else if resp.clicked() {
            match hover_node {
                Some(i) if lay.aggregated => {
                    let r = lay.nodes[i as usize].gid;
                    if (r as usize) < g.repos.len() {
                        self.scene = Scene::Repo(r);
                        self.canvas.fit_pending = true;
                    }
                }
                Some(i) => {
                    let v = lay.nodes[i as usize].gid;
                    if shift && self.sel.is_some() && self.sel != Some(v) {
                        self.path_to = Some(v);
                    } else {
                        self.select(Some(v));
                    }
                }
                None => {
                    if let Scene::Focus(root) = scene {
                        self.sel = Some(*root);
                    } else if !shift {
                        self.select(None);
                    }
                }
            }
        }
        // right-click menu on a node
        if resp.secondary_clicked() {
            self.canvas_menu_target = hover_node.filter(|_| !lay.aggregated).map(|i| lay.nodes[i as usize].gid);
        }
        let target = self.canvas_menu_target;
        resp.context_menu(|ui| {
            let Some(v) = target else {
                if ui.button("Fit to window").clicked() {
                    self.canvas.fit_all = true;
                    ui.close_menu();
                }
                return;
            };
            self.node_menu(ui, v);
        });
    }

    fn zoom_controls(&mut self, ui: &mut egui::Ui, rect: egui::Rect) {
        // zoom controls
        egui::Area::new(egui::Id::new("zoom-controls"))
            .order(egui::Order::Foreground)
            .fixed_pos(rect.right_top() + Vec2::new(-176.0, 10.0))
            .show(ui.ctx(), |ui| {
                egui::Frame::popup(ui.style()).inner_margin(egui::Margin::symmetric(6, 4)).corner_radius(10).show(ui, |ui| {
                    ui.horizontal(|ui| {
                        if ui.small_button("−").clicked() {
                            self.zoom_by(0.8);
                        }
                        ui.label(format!("{:.0}%", self.canvas.k * 100.0));
                        if ui.small_button("+").clicked() {
                            self.zoom_by(1.25);
                        }
                        if ui.small_button("Fit").on_hover_text("Fit everything (F)").clicked() {
                            self.canvas.fit_all = true;
                        }
                    });
                });
            });
    }

    /// Says so when the scene was cut down to the nearest nodes, and offers more.
    fn limit_notice(&mut self, ui: &mut egui::Ui, rect: egui::Rect, lay: &SceneLayout) {
        // the scene was cut down to the nearest nodes
        if lay.total > lay.nodes.len() {
            egui::Area::new(egui::Id::new("scene-limit"))
                .order(egui::Order::Foreground)
                .fixed_pos(rect.left_top() + Vec2::new(12.0, 10.0))
                .show(ui.ctx(), |ui| {
                    egui::Frame::popup(ui.style()).inner_margin(egui::Margin::symmetric(10, 6)).corner_radius(10).show(ui, |ui| {
                        ui.horizontal(|ui| {
                            ui.label(format!(
                                "Showing the {} nearest of {} nodes",
                                super::util::num(lay.nodes.len() as u32),
                                super::util::num(lay.total as u32)
                            ));
                            if ui.button("Show more").on_hover_text("Three times as many; large layouts take a moment").clicked() {
                                self.scene_limit = (self.scene_limit * 3).min(lay.total.max(self.scene_limit));
                                self.canvas.fit_pending = true;
                            }
                        });
                    });
                });
        }
    }

    fn start_anim(&mut self, to_pan: Vec2, to_k: f32, now: f64) {
        let c = &mut self.canvas;
        if self.shot.is_some() {
            (c.pan, c.k, c.anim) = (to_pan, to_k, None);
            return;
        }
        c.anim = Some(Anim { from_pan: c.pan, from_k: c.k, to_pan, to_k, t0: now });
    }
}

impl LayoutCache {
    pub fn get(&self, s: &Scene, limit: usize) -> Option<Arc<SceneLayout>> {
        self.map.get(&(s.clone(), limit)).cloned()
    }
}
