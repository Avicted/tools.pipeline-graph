//! The map: scenes (what is on the canvas), their cached layouts, and the
//! painter that draws only what is on screen, with level of detail.

use super::app::{LineageMode, Model, Screen, Workspace};
use super::job::Job;
use super::theme::{self, Palette};
use crate::core::derive::{Class, ENTRY_CHAINED, ENTRY_MANUAL, ENTRY_SCHEDULE, ENTRY_VCS, NONE};
use crate::core::diff::Change;
use crate::core::graph::{Ek, Kind, Scratch};
use crate::core::layout::{self, Opts};
use crate::core::spatial::{Grid, Rect};
use egui::epaint::CubicBezierShape;
use egui::text::{LayoutJob, TextFormat, TextWrapping};
use egui::{Align2, Color32, CursorIcon, FontId, Painter, Pos2, RichText, Sense, Shape, Stroke, Vec2};
use std::collections::HashMap;
use std::sync::Arc;

/// What the canvas shows.
#[derive(Clone, PartialEq, Eq, Hash, Debug)]
pub enum Scene {
    /// One box per repository, with counted links between them.
    Repos,
    /// A repository's jobs and what touches them from outside.
    Repo(u32),
    /// Every job and image, in repository lanes.
    All,
    /// One node's upstream and downstream, laid out on its own.
    Focus(u32),
    /// Everything downstream of a repository's commit-triggered jobs.
    Flow(u32),
}

pub struct SNode {
    /// A graph node, or in `Scene::Repos` a repository index.
    pub gid: u32,
    pub rect: Rect,
}

pub struct SEdge {
    pub a: u32,
    pub b: u32,
    pub kind: Ek,
    pub pts: Vec<[f32; 2]>,
    pub bbox: Rect,
    pub weight: u32,
}

pub struct SceneLayout {
    pub nodes: Vec<SNode>,
    pub edges: Vec<SEdge>,
    pub lanes: Vec<(String, Rect)>,
    pub w: f32,
    pub h: f32,
    ngrid: Grid,
    egrid: Grid,
    pub index_of: HashMap<u32, u32>,
    pub aggregated: bool,
    /// Title and sub line of aggregated nodes.
    pub agg_text: Vec<(String, String)>,
    pub kinds: Vec<Ek>,
}

fn card_size(m: &Model, v: u32) -> (f32, f32) {
    let g = &m.g;
    let title = g.node_label(v).chars().count() as f32;
    let sub = g.s(g.sub[v as usize]).chars().count() as f32;
    let w = (title * 7.2 + 32.0).max(sub * 5.6 + 28.0).clamp(150.0, 250.0);
    let h = match g.kind[v as usize] {
        Kind::Build | Kind::External => 58.0,
        _ => 46.0,
    };
    (w, h)
}

pub fn build_scene(m: &Model, scene: &Scene) -> SceneLayout {
    let (g, d) = (&m.g, &m.d);
    let n_repo = g.repos.len() as u32;
    let mut members: Vec<u32> = vec![];
    let mut edges: Vec<(u32, u32, Ek, u32)> = vec![]; // (scene a, scene b, kind, weight)
    let mut sizes: Vec<(f32, f32)> = vec![];
    let mut lanes_of: Vec<u32> = vec![];
    let mut agg_text = vec![];
    let mut lane_names: Vec<String> = vec![];
    let mut with_lanes = true;
    let mut aggregated = false;
    let mut index_of: HashMap<u32, u32> = HashMap::new();

    let take = |members: &mut Vec<u32>, index_of: &mut HashMap<u32, u32>, v: u32| {
        if let std::collections::hash_map::Entry::Vacant(e) = index_of.entry(v) {
            e.insert(members.len() as u32);
            members.push(v);
        }
    };
    let mut sc = Scratch::new(g.len());
    match scene {
        Scene::Repos => {
            aggregated = true;
            with_lanes = false;
            let mut counts: HashMap<(u32, u32), u32> = HashMap::new();
            let ext = n_repo;
            let rep = |v: u32| if d.repo_of[v as usize] == NONE { ext } else { d.repo_of[v as usize] };
            for e in 0..g.flow.len() {
                let (a, b) = (rep(g.flow.from[e]), rep(g.flow.to[e]));
                if a != b {
                    *counts.entry((a, b)).or_default() += 1;
                }
            }
            let mut used = vec![false; n_repo as usize + 1];
            for &(a, b) in counts.keys() {
                used[a as usize] = true;
                used[b as usize] = true;
            }
            for (i, r) in d.repos.iter().enumerate() {
                if r.builds + r.images > 0 || used[i] {
                    index_of.insert(i as u32, members.len() as u32);
                    members.push(i as u32);
                    agg_text.push((g.s(r.name).to_string(), format!("{} jobs · {} images · {} make", r.builds, r.images, r.makes)));
                    sizes.push((250.0, 74.0));
                }
            }
            if used[ext as usize] || d.totals.externals > 0 {
                index_of.insert(ext, members.len() as u32);
                members.push(ext);
                agg_text.push(("External".into(), format!("{} builds outside the analysed repos", d.totals.externals)));
                sizes.push((250.0, 74.0));
            }
            let mut keys: Vec<_> = counts.into_iter().collect();
            keys.sort();
            for ((a, b), w) in keys {
                if let (Some(&sa), Some(&sb)) = (index_of.get(&a), index_of.get(&b)) {
                    edges.push((sa, sb, Ek::Other, w));
                }
            }
            lanes_of = vec![0; members.len()];
            lane_names = vec![String::new()];
        }
        Scene::All | Scene::Repo(_) => {
            if let Scene::Repo(r) = scene {
                for v in 0..g.len() as u32 {
                    if g.kind[v as usize] != Kind::Make && d.repo_of[v as usize] == *r {
                        take(&mut members, &mut index_of, v);
                    }
                }
                let own = members.clone();
                for v in own {
                    for &w in g.flow.out.nb(v).iter().chain(g.flow.inn.nb(v)) {
                        take(&mut members, &mut index_of, w);
                    }
                }
            } else {
                for v in 0..g.len() as u32 {
                    if g.kind[v as usize] != Kind::Make {
                        take(&mut members, &mut index_of, v);
                    }
                }
            }
        }
        Scene::Focus(v) => {
            with_lanes = false;
            let net = g.net_for(*v);
            let up = sc.reach(net, &[*v], false);
            let down = sc.reach(net, &[*v], true);
            for x in up.iter().chain(down.iter()) {
                take(&mut members, &mut index_of, x);
            }
            members.sort_unstable();
            index_of.clear();
            for (i, &x) in members.iter().enumerate() {
                index_of.insert(x, i as u32);
            }
        }
        Scene::Flow(r) => {
            with_lanes = false;
            let roots: Vec<u32> = (0..g.len() as u32)
                .filter(|&v| g.kind[v as usize] == Kind::Build && d.repo_of[v as usize] == *r && d.entry[v as usize] & ENTRY_VCS != 0)
                .collect();
            let down = sc.reach(&g.flow, &roots, true);
            for x in down.iter() {
                take(&mut members, &mut index_of, x);
            }
        }
    }

    if !aggregated {
        let net = match scene {
            Scene::Focus(v) => g.net_for(*v),
            _ => &g.flow,
        };
        for (i, &v) in members.iter().enumerate() {
            sizes.push(card_size(m, v));
            lanes_of.push(if with_lanes {
                let r = d.repo_of[v as usize];
                if r == NONE {
                    n_repo
                } else {
                    r
                }
            } else {
                0
            });
            for &e in net.out.es(v) {
                let w = net.to[e as usize];
                if let Some(&j) = index_of.get(&w) {
                    edges.push((i as u32, j, net.kind[e as usize], 1));
                }
            }
        }
        lane_names = if with_lanes {
            g.repos.iter().map(|r| g.s(r.name).to_string()).chain(std::iter::once("External".to_string())).collect()
        } else {
            vec![String::new()]
        };
    }

    let pairs: Vec<(u32, u32)> = edges.iter().map(|e| (e.0, e.1)).collect();
    let mut opts = Opts { layer_gap: 64.0, node_gap: 14.0, margin: 20.0, lane_gap: 12.0, ..Opts::default() };
    opts.top_offset = if with_lanes { 8.0 } else { 0.0 };
    if with_lanes {
        opts.lane_header = 8.0;
        opts.lane_pad = 16.0;
        opts.lane_top = 56.0;
    } else {
        opts.lane_header = 0.0;
        opts.lane_pad = 8.0;
    }
    let out = layout::compute(&layout::Input { size: &sizes, lane: &lanes_of, lane_names: &lane_names, edges: &pairs }, &opts);

    let mut ngrid = Grid::new(out.w, out.h);
    for (i, r) in out.boxes.iter().enumerate() {
        ngrid.insert(i as u32, r);
    }
    let mut egrid = Grid::new(out.w, out.h);
    let mut sedges = Vec::with_capacity(edges.len());
    let mut kinds: Vec<Ek> = vec![];
    for (i, e) in edges.iter().enumerate() {
        let pts = out.routes[i].clone();
        if pts.len() < 2 {
            continue;
        }
        let (mut x0, mut y0, mut x1, mut y1) = (f32::MAX, f32::MAX, f32::MIN, f32::MIN);
        for p in &pts {
            x0 = x0.min(p[0]);
            y0 = y0.min(p[1]);
            x1 = x1.max(p[0]);
            y1 = y1.max(p[1]);
        }
        let bbox = Rect { x0, y0, x1, y1 };
        egrid.insert(sedges.len() as u32, &bbox);
        if !kinds.contains(&e.2) {
            kinds.push(e.2);
        }
        sedges.push(SEdge { a: e.0, b: e.1, kind: e.2, pts, bbox, weight: e.3 });
    }
    let lanes = if with_lanes { out.lanes.iter().map(|(i, r)| (lane_names[*i as usize].clone(), *r)).collect() } else { vec![] };
    SceneLayout {
        nodes: members.iter().zip(&out.boxes).map(|(&gid, &rect)| SNode { gid, rect }).collect(),
        edges: sedges,
        lanes,
        w: out.w,
        h: out.h,
        ngrid,
        egrid,
        index_of,
        aggregated,
        agg_text,
        kinds,
    }
}

#[derive(Default)]
pub struct LayoutCache {
    map: HashMap<Scene, Arc<SceneLayout>>,
    order: Vec<Scene>,
    pending: HashMap<Scene, Job<Arc<SceneLayout>>>,
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
        let done: Vec<Scene> = self
            .layouts
            .pending
            .iter()
            .filter_map(|(s, j)| j.poll().map(|l| (s.clone(), l)))
            .map(|(s, l)| {
                self.layouts.map.insert(s.clone(), l);
                self.layouts.order.retain(|x| *x != s);
                self.layouts.order.push(s.clone());
                s
            })
            .collect();
        for s in done {
            self.layouts.pending.remove(&s);
        }
        while self.layouts.order.len() > 6 {
            let old = self.layouts.order.remove(0);
            self.layouts.map.remove(&old);
        }
    }

    pub fn layouts_idle(&self) -> bool {
        self.layouts.pending.is_empty()
    }

    pub fn layout_for(&mut self, ctx: &egui::Context, scene: &Scene) -> Option<Arc<SceneLayout>> {
        if let Some(l) = self.layouts.map.get(scene) {
            return Some(l.clone());
        }
        if !self.layouts.pending.contains_key(scene) {
            let m = self.m.clone();
            let s = scene.clone();
            self.layouts.pending.insert(scene.clone(), Job::spawn(ctx, move || Arc::new(build_scene(&m, &s))));
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
                let big = self.m.g.len() > 1500;
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
        let (g, d) = (&m.g, &m.d);
        let size = ui.available_size();
        let (resp, painter) = ui.allocate_painter(size, Sense::click_and_drag());
        let rect = resp.rect;
        painter.rect_filled(rect, 0, pal.bg);
        let painter = painter.with_clip_rect(rect);
        let origin = rect.min.to_vec2();
        let now = ui.input(|i| i.time);
        self.canvas.view = rect.size();

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
        let (k, pan) = (self.canvas.k, self.canvas.pan);
        let to_screen = |p: [f32; 2]| Pos2::new(origin.x + pan.x + p[0] * k, origin.y + pan.y + p[1] * k);
        let view = Rect { x0: -pan.x / k, y0: -pan.y / k, x1: (rect.width() - pan.x) / k, y1: (rect.height() - pan.y) / k };

        // what is visible
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
        lay.ngrid.query(&view, &mut cv.seen_n, cv.stamp, &mut cv.vis_n);
        lay.egrid.query(&view, &mut cv.seen_e, cv.stamp, &mut cv.vis_e);
        cv.vis_n.retain(|&i| lay.nodes[i as usize].rect.intersects(&view));
        cv.vis_e.retain(|&i| lay.edges[i as usize].bbox.intersects(&view));
        cv.vis_n.sort_unstable();
        cv.vis_e.sort_unstable();
        let (vis_n, vis_e) = (std::mem::take(&mut cv.vis_n), std::mem::take(&mut cv.vis_e));

        // pointer
        let hover_pos = resp.hover_pos();
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

        let lin = self.lin.as_ref();
        let dim_on = lin.is_some() && self.screen != Screen::Compare;
        let allowed = |gid: u32| -> bool {
            let Some(l) = lin else { return true };
            if let Some(r) = &l.route {
                return r.set.get(gid);
            }
            match self.mode {
                LineageMode::Off => true,
                LineageMode::Both => l.up.get(gid) || l.down.get(gid),
                LineageMode::Down => l.down.get(gid),
                LineageMode::Up => l.up.get(gid),
            }
        };
        let hide_others = lin.is_some() && (self.mode != LineageMode::Off || lin.is_some_and(|l| l.route.is_some()));
        let diff = if self.compare.overlay { self.compare.diff.as_ref() } else { None };

        // lanes
        let lane_font = FontId::proportional((12.0 * k.max(0.6)).clamp(11.0, 15.0));
        for (i, (name, r)) in lay.lanes.iter().enumerate() {
            if !r.intersects(&view) {
                continue;
            }
            let sr = egui::Rect::from_min_max(to_screen([r.x0, r.y0]), to_screen([r.x1, r.y1]));
            painter.rect_filled(sr, 8, if i % 2 == 0 { pal.lane } else { pal.lane_alt });
            painter.rect_stroke(sr, 8, super::theme::stroke(1.0, pal.line), egui::StrokeKind::Inside);
            // the tag stays in view while its lane is scrolled
            let x = (sr.min.x + 8.0).max(rect.min.x + 8.0).min(sr.max.x - 60.0);
            let galley = painter.layout_no_wrap(name.clone(), lane_font.clone(), Color32::WHITE);
            let tag = egui::Rect::from_min_size(Pos2::new(x, sr.min.y + 6.0), galley.size() + Vec2::new(14.0, 6.0));
            let col = if name == "External" { pal.warn } else { Color32::from_rgb(0x47, 0x4F, 0x5E) };
            painter.rect_filled(tag, 5, col);
            painter.galley(tag.min + Vec2::new(7.0, 3.0), galley, Color32::WHITE);
        }

        // edges
        let hit_dist = 6.0f32;
        let mut hover_edge: Option<u32> = None;
        for &ei in &vis_e {
            let e = &lay.edges[ei as usize];
            let (ga, gb) = (lay.nodes[e.a as usize].gid, lay.nodes[e.b as usize].gid);
            if !lay.aggregated && hide_others && !(allowed(ga) && allowed(gb)) {
                continue;
            }
            let mut st = theme::edge_style(e.kind, pal.dark);
            let mut color = st.color;
            let mut width = st.width * k.clamp(0.7, 1.2);
            let mut lit = false;
            if lay.aggregated {
                color = pal.muted;
                width = (1.5 + (e.weight as f32).ln_1p()).min(7.0);
                st.dash = None;
            }
            if let Some(l) = lin {
                if !lay.aggregated {
                    let on_route = l.route.as_ref().map(|r| r.set.get(ga) && r.set.get(gb));
                    let up = l.up.get(ga) && l.up.get(gb);
                    let down = l.down.get(ga) && l.down.get(gb);
                    match on_route {
                        Some(true) => {
                            lit = true;
                            color = pal.accent;
                        }
                        Some(false) => {}
                        None if up => {
                            lit = true;
                            color = pal.up;
                        }
                        None if down => {
                            lit = true;
                            color = pal.down;
                        }
                        None => {}
                    }
                    if lit {
                        width += 1.0;
                    } else if dim_on {
                        color = theme::with_alpha(color, 36);
                    }
                }
            }
            if hover_node.is_none() && !lay.aggregated {
                if let Some(p) = hover_pos {
                    if near_polyline(&e.pts, p, &to_screen, hit_dist) {
                        hover_edge = Some(ei);
                        width += 0.8;
                    }
                }
            }
            draw_edge(&painter, e, &to_screen, color, width, st.dash.filter(|_| k > 0.2), k);
            if lay.aggregated && k > 0.25 {
                let mid = to_screen(e.pts[e.pts.len() / 2]);
                let mid = if e.pts.len() % 2 == 0 {
                    (mid.to_vec2() + to_screen(e.pts[e.pts.len() / 2 - 1]).to_vec2()).to_pos2() / 1.0
                } else {
                    mid
                };
                let _ = mid;
                let a = to_screen(e.pts[0]);
                let b = to_screen(*e.pts.last().unwrap());
                let c = Pos2::new((a.x + b.x) / 2.0, (a.y + b.y) / 2.0);
                let gal = painter.layout_no_wrap(e.weight.to_string(), FontId::proportional(12.0), pal.text);
                let r = egui::Rect::from_center_size(c, gal.size() + Vec2::new(10.0, 4.0));
                painter.rect_filled(r, 8, pal.card);
                painter.rect_stroke(r, 8, super::theme::stroke(1.0, pal.line), egui::StrokeKind::Inside);
                painter.galley(r.min + Vec2::new(5.0, 2.0), gal, pal.text);
            }
        }

        // nodes
        let sel = self.sel;
        for &i in &vis_n {
            let n = &lay.nodes[i as usize];
            let sr = egui::Rect::from_min_max(to_screen([n.rect.x0, n.rect.y0]), to_screen([n.rect.x1, n.rect.y1]));
            if lay.aggregated {
                draw_repo_card(&painter, sr, &lay.agg_text[i as usize], pal, hover_node == Some(i), k);
                continue;
            }
            let v = n.gid;
            if hide_others && !allowed(v) {
                continue;
            }
            let related = lin.map(|l| (l.root == v, l.up.get(v), l.down.get(v), l.route.as_ref().is_some_and(|r| r.set.get(v))));
            let change = diff.map(|df| df.state[v as usize]).unwrap_or(Change::Same);
            let vis = CardVis {
                selected: sel == Some(v),
                hovered: hover_node == Some(i),
                up: related.is_some_and(|r| r.1 && !r.0),
                down: related.is_some_and(|r| r.2 && !r.0),
                on_route: related.is_some_and(|r| r.3 && !r.0),
                dim: dim_on && related.is_some_and(|r| !r.0 && !r.1 && !r.2 && !r.3),
                change,
            };
            draw_card(&painter, sr, v, m.as_ref(), pal, &vis, k);
        }
        self.canvas.vis_n = vis_n;
        self.canvas.vis_e = vis_e;

        // pointer actions
        if hover_node.is_some() {
            ui.output_mut(|o| o.cursor_icon = CursorIcon::PointingHand);
        }
        if let (Some(ei), Some(p)) = (hover_edge, hover_pos) {
            let e = &lay.edges[ei as usize];
            let (a, b) = (lay.nodes[e.a as usize].gid, lay.nodes[e.b as usize].gid);
            let text = format!("{}  →  {}\n{}", g.node_label(a), g.node_label(b), e.kind.name());
            egui::show_tooltip_at_pointer(ui.ctx(), ui.layer_id(), egui::Id::new("edge-tip"), |ui: &mut egui::Ui| {
                ui.label(text);
            });
            let _ = p;
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
            ui.label(RichText::new(g.node_label(v)).strong());
            ui.separator();
            if ui.button("Open lineage on its own").clicked() {
                self.open_focus(v);
                ui.close_menu();
            }
            for (label, mode) in [
                ("Keep only what it breaks", LineageMode::Down),
                ("Keep only what it needs", LineageMode::Up),
                ("Keep both", LineageMode::Both),
            ] {
                if ui.button(label).clicked() {
                    self.select(Some(v));
                    self.mode = mode;
                    ui.close_menu();
                }
            }
            ui.separator();
            if ui.button("Copy id").clicked() {
                let id = g.node_id(v).to_string();
                self.copy(ui.ctx(), id, "Id");
                ui.close_menu();
            }
            if ui.button("Copy link").clicked() {
                self.select(Some(v));
                let l = self.link();
                self.copy(ui.ctx(), l, "Link");
                ui.close_menu();
            }
        });

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

        // legend
        if !lay.aggregated && !lay.kinds.is_empty() {
            let mut y = rect.max.y - 14.0;
            let mut kinds = lay.kinds.clone();
            kinds.sort();
            for kd in kinds.iter().rev() {
                let st = theme::edge_style(*kd, pal.dark);
                let (x0, x1) = (rect.min.x + 14.0, rect.min.x + 44.0);
                match st.dash {
                    Some((dl, gap)) => {
                        painter.extend(Shape::dashed_line(
                            &[Pos2::new(x0, y), Pos2::new(x1, y)],
                            super::theme::stroke(st.width.max(1.5), st.color),
                            dl.min(5.0),
                            gap.min(3.0),
                        ));
                    }
                    None => {
                        painter.line_segment([Pos2::new(x0, y), Pos2::new(x1, y)], super::theme::stroke(st.width.max(1.5), st.color));
                    }
                }
                painter.text(Pos2::new(x1 + 8.0, y), Align2::LEFT_CENTER, kd.name(), FontId::proportional(11.5), pal.muted);
                y -= 17.0;
            }
        }
        let _ = Class::None;
        let _ = (ENTRY_CHAINED, ENTRY_MANUAL, ENTRY_SCHEDULE);
        let _ = d;
    }

    fn start_anim(&mut self, to_pan: Vec2, to_k: f32, now: f64) {
        let c = &mut self.canvas;
        c.anim = Some(Anim { from_pan: c.pan, from_k: c.k, to_pan, to_k, t0: now });
    }
}

struct CardVis {
    selected: bool,
    hovered: bool,
    up: bool,
    down: bool,
    on_route: bool,
    dim: bool,
    change: Change,
}

fn fit_text(painter: &Painter, text: &str, font: FontId, color: Color32, max_w: f32) -> Arc<egui::Galley> {
    let mut job = LayoutJob::single_section(text.to_owned(), TextFormat::simple(font, color));
    job.wrap = TextWrapping { max_width: max_w.max(8.0), max_rows: 1, break_anywhere: true, overflow_character: Some('…') };
    painter.layout_job(job)
}

fn pill(painter: &Painter, left_top: Pos2, text: &str, fg: Color32, bg: Color32) -> f32 {
    let galley = painter.layout_no_wrap(text.to_owned(), FontId::proportional(10.5), fg);
    let r = egui::Rect::from_min_size(left_top, galley.size() + Vec2::new(10.0, 3.0));
    painter.rect_filled(r, 4, bg);
    painter.galley(r.min + Vec2::new(5.0, 1.5), galley, fg);
    r.width() + 4.0
}

fn draw_card(painter: &Painter, r: egui::Rect, v: u32, m: &Model, pal: &Palette, vis: &CardVis, k: f32) {
    let (g, d) = (&m.g, &m.d);
    let dim = |c: Color32| if vis.dim { c.gamma_multiply(0.3) } else { c };
    let kind = g.kind[v as usize];
    let accent = theme::kind_color(kind, g.composite[v as usize], pal.dark);
    let fill = if vis.selected { pal.card_sel } else { pal.card };
    let text = if vis.selected { pal.card_sel_text } else { pal.text };
    let muted = if vis.selected { theme::with_alpha(pal.card_sel_text, 170) } else { pal.muted };
    let corner = (7.0 * k.clamp(0.4, 1.0)).round() as u8;
    // zoomed out, a card grows sideways (into the gap between columns) so its title stays readable
    let titles_only = (0.2..0.55).contains(&k);
    let mut title_gal = None;
    let mut r = r;
    if titles_only {
        let size = (14.0 * k).clamp(11.0, 15.0);
        let reach = r.width() + 0.9 * 64.0 * k;
        let gal = fit_text(painter, g.node_label(v), FontId::proportional(size), dim(text), reach - 18.0);
        let want = gal.size().x + 20.0;
        if want > r.width() {
            r = egui::Rect::from_center_size(r.center(), Vec2::new(want.min(reach), r.height()));
        }
        title_gal = Some(gal);
    }
    painter.rect_filled(r, corner, dim(fill));
    let mut stroke = super::theme::stroke(if vis.hovered { 1.8 } else { 1.0 }, if vis.hovered { accent } else { pal.line });
    let ring = if vis.on_route {
        Some(pal.accent)
    } else if vis.up {
        Some(pal.up)
    } else if vis.down {
        Some(pal.down)
    } else {
        None
    };
    if let Some(c) = ring {
        stroke = super::theme::stroke(2.2, c);
    }
    match vis.change {
        Change::Added => stroke = super::theme::stroke(2.6, pal.added),
        Change::Changed => stroke = super::theme::stroke(2.6, pal.changed),
        Change::Same => {}
    }
    painter.rect_stroke(r, corner, super::theme::stroke(stroke.width, dim(stroke.color)), egui::StrokeKind::Inside);
    if k < 0.2 {
        // far away: a coloured block is all that is legible
        painter.rect_filled(egui::Rect::from_min_size(r.min, Vec2::new(r.width().min(5.0), r.height())), 2, dim(accent));
        return;
    }
    let pad = 9.0 * k.clamp(0.7, 1.0);
    let bar = egui::Rect::from_min_size(r.min, Vec2::new(4.0 * k.clamp(0.6, 1.0), r.height()));
    painter.rect_filled(bar, corner, dim(accent));
    let clip = painter.with_clip_rect(r.shrink(1.0));
    let inner_w = r.width() - 2.0 * pad - 4.0;
    let label = g.node_label(v);
    if let Some(gal) = title_gal {
        let y = r.center().y - gal.size().y / 2.0;
        clip.galley(Pos2::new(r.min.x + pad + 4.0, y), gal, text);
        return;
    }
    let tsize = (14.0 * k).clamp(11.0, 17.0);
    let title = if kind == Kind::Make { format!("make {label}") } else { label.to_string() };
    let gal = fit_text(&clip, &title, FontId::proportional(tsize), dim(text), inner_w - 30.0);
    let mut y = r.min.y + 7.0 * k.clamp(0.7, 1.2);
    let th = gal.size().y;
    clip.galley(Pos2::new(r.min.x + pad + 4.0, y), gal, text);
    let down = d.down[v as usize];
    if k >= 0.75 && down > 0 && kind != Kind::Make {
        let c = if vis.selected { pal.card_sel_text } else { pal.down };
        clip.text(
            Pos2::new(r.max.x - pad, y + th / 2.0),
            Align2::RIGHT_CENTER,
            format!("↓{down}"),
            FontId::proportional(11.0 * k.clamp(0.8, 1.2)),
            dim(c),
        );
    }
    y += th + 1.0;
    let sub = if kind == Kind::Build && !g.s(g.project[v as usize]).is_empty() {
        format!("{} · {}", g.s(g.project[v as usize]), g.s(g.sub[v as usize]))
    } else {
        g.s(g.sub[v as usize]).to_string()
    };
    let ssize = (11.0 * k).clamp(9.5, 13.0);
    let gal = fit_text(&clip, &sub, FontId::proportional(ssize), dim(muted), inner_w);
    let sh = gal.size().y;
    clip.galley(Pos2::new(r.min.x + pad + 4.0, y), gal, muted);
    y += sh + 4.0 * k.clamp(0.8, 1.2);
    if k >= 0.8 && r.max.y - y > 12.0 {
        let mut x = r.min.x + pad + 4.0;
        let top = Pos2::new(x, y);
        let _ = top;
        let class = d.class[v as usize];
        if class != Class::None {
            x += pill(&clip, Pos2::new(x, y), class.name(), Color32::WHITE, dim(theme::class_color(class, pal.dark)));
        }
        let e = d.entry[v as usize];
        let ink = if vis.selected { pal.card_sel_text } else { pal.muted };
        let chip_bg = if vis.selected { theme::with_alpha(pal.card_sel_text, 40) } else { theme::with_alpha(pal.muted, 40) };
        for (bit, name) in [(ENTRY_VCS, "commit"), (ENTRY_SCHEDULE, "scheduled"), (ENTRY_MANUAL, "manual")] {
            if e & bit != 0 {
                x += pill(&clip, Pos2::new(x, y), name, dim(ink), chip_bg);
            }
        }
        if kind == Kind::Image {
            x += pill(&clip, Pos2::new(x, y), "image", dim(ink), chip_bg);
        }
        if d.no_test_up[v as usize] {
            x += pill(&clip, Pos2::new(x, y), "no test upstream", dim(pal.card), pal.changed);
        }
        let _ = x;
    }
    if vis.change != Change::Same {
        let c = if vis.change == Change::Added { pal.added } else { pal.changed };
        painter.circle_filled(r.right_top() + Vec2::new(-4.0, 4.0), 5.0, c);
    }
}

fn draw_repo_card(painter: &Painter, r: egui::Rect, text: &(String, String), pal: &Palette, hovered: bool, k: f32) {
    painter.rect_filled(r, 10, pal.card);
    painter.rect_stroke(
        r,
        10,
        super::theme::stroke(if hovered { 2.0 } else { 1.0 }, if hovered { pal.accent } else { pal.line }),
        egui::StrokeKind::Inside,
    );
    let clip = painter.with_clip_rect(r.shrink(1.0));
    let tsize = (17.0 * k).clamp(12.0, 22.0);
    clip.text(r.min + Vec2::new(14.0, r.height() * 0.32), Align2::LEFT_CENTER, &text.0, FontId::proportional(tsize), pal.text);
    if k > 0.35 {
        clip.text(
            r.min + Vec2::new(14.0, r.height() * 0.70),
            Align2::LEFT_CENTER,
            &text.1,
            FontId::proportional((12.0 * k).clamp(10.0, 14.0)),
            pal.muted,
        );
    }
}

fn bezier_pts(pts: &[Pos2]) -> Vec<[Pos2; 4]> {
    let mut out = vec![];
    for w in pts.windows(2) {
        let (a, b) = (w[0], w[1]);
        let dir = if b.x >= a.x { 1.0 } else { -1.0 };
        let dx = ((b.x - a.x).abs() / 2.0).max(30.0 * 0.0 + 1.0) * dir;
        out.push([a, Pos2::new(a.x + dx, a.y), Pos2::new(b.x - dx, b.y), b]);
    }
    out
}

fn draw_edge(
    painter: &Painter,
    e: &SEdge,
    to_screen: &impl Fn([f32; 2]) -> Pos2,
    color: Color32,
    width: f32,
    dash: Option<(f32, f32)>,
    k: f32,
) {
    let pts: Vec<Pos2> = e.pts.iter().map(|&p| to_screen(p)).collect();
    // the bend between ports needs a minimum reach so short hops still curve
    let mut segs = bezier_pts(&pts);
    for s in segs.iter_mut() {
        let dir = if s[3].x >= s[0].x { 1.0 } else { -1.0 };
        let dx = ((s[3].x - s[0].x).abs() / 2.0).max(30.0 * k) * dir;
        s[1] = Pos2::new(s[0].x + dx, s[0].y);
        s[2] = Pos2::new(s[3].x - dx, s[3].y);
    }
    match dash {
        Some((dl, gap)) => {
            let mut flat: Vec<Pos2> = vec![];
            for s in &segs {
                flat.extend(CubicBezierShape::from_points_stroke(*s, false, Color32::TRANSPARENT, Stroke::NONE).flatten(Some(0.6)));
            }
            painter.extend(Shape::dashed_line(&flat, super::theme::stroke(width, color), dl * k.clamp(0.7, 1.2), gap * k.clamp(0.7, 1.2)));
        }
        None => {
            for s in &segs {
                painter.add(Shape::CubicBezier(CubicBezierShape::from_points_stroke(
                    *s,
                    false,
                    Color32::TRANSPARENT,
                    super::theme::stroke(width, color),
                )));
            }
        }
    }
    if k > 0.3 {
        if let Some(last) = segs.last() {
            let tip = last[3];
            let dir = (tip - last[2]).normalized();
            let dir = if dir.x.is_nan() { Vec2::X } else { dir };
            let n = Vec2::new(-dir.y, dir.x);
            let s = 7.0 * k.clamp(0.7, 1.3);
            painter.add(Shape::convex_polygon(
                vec![tip, tip - dir * s + n * (s * 0.45), tip - dir * s - n * (s * 0.45)],
                color,
                Stroke::NONE,
            ));
        }
    }
}

fn near_polyline(pts: &[[f32; 2]], p: Pos2, to_screen: &impl Fn([f32; 2]) -> Pos2, dist: f32) -> bool {
    pts.windows(2).any(|w| {
        let (a, b) = (to_screen(w[0]), to_screen(w[1]));
        let ab = b - a;
        let len2 = ab.length_sq();
        let t = if len2 == 0.0 { 0.0 } else { ((p - a).dot(ab) / len2).clamp(0.0, 1.0) };
        (a + ab * t).distance(p) <= dist
    })
}

impl LayoutCache {
    pub fn get(&self, s: &Scene) -> Option<Arc<SceneLayout>> {
        self.map.get(s).cloned()
    }
}
