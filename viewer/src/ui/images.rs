//! Images & layers: what is built on what, who builds it, who uses it, and
//! which jobs run inside it. Answers "who rebuilds if this base moves".

use super::app::Workspace;
use super::theme::{self, Palette};
use super::util;
use crate::core::graph::{Ek, Kind};
use egui::{RichText, Stroke};
use std::collections::HashMap;

#[derive(Default)]
pub struct ImagesState {
    pub query: String,
    pub focus: bool,
}

impl Workspace {
    pub fn show_images(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        let m = self.m.clone();
        let (g, d) = (&m.g, &m.d);
        let imgs: Vec<u32> = (0..g.len() as u32).filter(|&v| g.kind[v as usize] == Kind::Image).collect();
        let derived_parent = |v: u32| -> Option<u32> {
            g.flow.inn.es(v).iter().find(|&&e| g.flow.kind[e as usize] == Ek::Derives).map(|&e| g.flow.from[e as usize])
        };
        let children = |v: u32| -> Vec<u32> {
            g.flow.out.es(v).iter().filter(|&&e| g.flow.kind[e as usize] == Ek::Derives).map(|&e| g.flow.to[e as usize]).collect()
        };
        let from_of = |v: u32| -> String {
            g.params(v).iter().find(|(k, _)| g.s(*k) == "from").map(|(_, b)| g.s(*b).to_string()).unwrap_or_default()
        };

        // roots grouped by the third-party image they start from
        let mut groups: Vec<(String, Vec<u32>)> = vec![];
        let mut idx: HashMap<String, usize> = HashMap::new();
        for &v in &imgs {
            if derived_parent(v).is_some() {
                continue;
            }
            let base = from_of(v);
            let key = if base.is_empty() { "base not found in the analysed files".to_string() } else { base };
            let i = *idx.entry(key.clone()).or_insert_with(|| {
                groups.push((key.clone(), vec![]));
                groups.len() - 1
            });
            groups[i].1.push(v);
        }
        groups.sort_by(|a, b| (a.0.starts_with("base not"), &a.0).cmp(&(b.0.starts_with("base not"), &b.0)));

        let q = self.images.query.to_lowercase();
        // which images match, with their ancestors kept
        let mut keep: HashMap<u32, bool> = HashMap::new();
        fn matches(
            g: &crate::core::graph::Graph,
            children: &dyn Fn(u32) -> Vec<u32>,
            q: &str,
            v: u32,
            keep: &mut HashMap<u32, bool>,
            depth: usize,
        ) -> bool {
            if let Some(&k) = keep.get(&v) {
                return k;
            }
            keep.insert(v, false); // cycles stop here
            let own = g.node_label(v).to_lowercase().contains(q) || g.s(g.sub[v as usize]).to_lowercase().contains(q);
            let mut any = own;
            if depth < 40 {
                for c in children(v) {
                    any |= matches(g, children, q, c, keep, depth + 1);
                }
            }
            keep.insert(v, any);
            any
        }

        egui::Frame::new().inner_margin(egui::Margin::symmetric(24, 18)).show(ui, |ui| {
            ui.label(RichText::new("Images & layers").size(22.0).strong());
            ui.label(
                RichText::new(format!(
                    "{} images built by the pipelines · {} jobs and targets run inside an image · {} start from a third-party base",
                    imgs.len(),
                    d.container.iter().filter(|&&x| x).count(),
                    groups.iter().filter(|(k, _)| !k.starts_with("base not")).count()
                ))
                .color(pal.muted),
            );
            ui.add_space(8.0);
            let te = egui::TextEdit::singleline(&mut self.images.query)
                .hint_text("Filter images…   ( / )")
                .desired_width(360.0)
                .margin(egui::Margin::symmetric(10, 6));
            let r = ui.add(te);
            if self.images.focus {
                r.request_focus();
                self.images.focus = false;
            }
            ui.add_space(8.0);
        });
        let mut jump: Option<u32> = None;
        let mut focus: Option<u32> = None;
        egui::ScrollArea::vertical().auto_shrink([false, false]).show(ui, |ui| {
            egui::Frame::new().inner_margin(egui::Margin::symmetric(24, 0)).show(ui, |ui| {
                if imgs.is_empty() {
                    ui.label(RichText::new("No docker images were found being built. The analysis links `docker build`/`push` in scripts, make recipes and Dockerfiles.").color(pal.muted));
                }
                for (base, roots) in &groups {
                    let roots: Vec<u32> = if q.is_empty() { roots.clone() } else { roots.iter().copied().filter(|&r| matches(g, &children, &q, r, &mut keep, 0)).collect() };
                    if roots.is_empty() {
                        continue;
                    }
                    ui.horizontal(|ui| {
                        ui.label(RichText::new(base).monospace().strong());
                        ui.label(RichText::new(format!("({})", roots.len())).small().color(pal.muted));
                    });
                    for r in roots {
                        image_row(ui, &m, pal, r, 0, &q, &keep, &children, &mut jump, &mut focus, &mut vec![]);
                    }
                    ui.add_space(10.0);
                }
            });
        });
        if let Some(f) = focus {
            self.open_focus(f);
        } else if let Some(j) = jump {
            self.select(Some(j));
        }
    }
}

#[allow(clippy::too_many_arguments)]
fn image_row(
    ui: &mut egui::Ui,
    m: &super::app::Model,
    pal: &Palette,
    v: u32,
    depth: usize,
    q: &str,
    keep: &std::collections::HashMap<u32, bool>,
    children: &dyn Fn(u32) -> Vec<u32>,
    jump: &mut Option<u32>,
    focus: &mut Option<u32>,
    path: &mut Vec<u32>,
) {
    let (g, d) = (&m.g, &m.d);
    let vi = v as usize;
    let kids: Vec<u32> = children(v).into_iter().filter(|c| q.is_empty() || keep.get(c).copied().unwrap_or(false)).collect();
    let producers: Vec<u32> =
        g.flow.inn.es(v).iter().filter(|&&e| g.flow.kind[e as usize] == Ek::Produces).map(|&e| g.flow.from[e as usize]).collect();
    let users: Vec<u32> =
        g.flow.out.es(v).iter().filter(|&&e| g.flow.kind[e as usize] == Ek::Consumes).map(|&e| g.flow.to[e as usize]).collect();
    let runners: Vec<u32> =
        g.flow.out.es(v).iter().filter(|&&e| g.flow.kind[e as usize] == Ek::RunsIn).map(|&e| g.flow.to[e as usize]).collect();
    let accent = theme::kind_color(Kind::Image, false, pal.dark);
    let id = ui.make_persistent_id(("img", v, depth));
    let mut state = egui::collapsing_header::CollapsingState::load_with_default_open(ui.ctx(), id, depth < 1 && !kids.is_empty());
    if !q.is_empty() {
        state.set_open(true);
    }
    path.push(v);
    state
        .show_header(ui, |ui| {
            let r = ui.add(egui::Label::new(RichText::new(g.node_label(v)).strong().color(accent)).sense(egui::Sense::click()));
            if r.clicked() {
                *jump = Some(v);
            }
            if r.double_clicked() {
                *focus = Some(v);
            }
            r.on_hover_text(g.s(g.sub[vi]));
            ui.label(RichText::new(g.s(g.sub[vi])).small().monospace().color(pal.muted));
            for (n, what) in [(producers.len(), "built by"), (users.len(), "used by"), (runners.len(), "run in it")] {
                if n > 0 {
                    ui.label(RichText::new(format!("{n} {what}")).small().color(pal.muted));
                }
            }
            if !kids.is_empty() {
                ui.label(RichText::new(format!("{} derived", kids.len())).small().color(pal.accent));
            }
            if d.down[vi] > 0 {
                ui.label(RichText::new(format!("↓{} if it changes", d.down[vi])).small().color(pal.down));
            }
        })
        .body(|ui| {
            for (title, list) in [("Built by", &producers), ("Used by", &users), ("Jobs that run inside it", &runners)] {
                if list.is_empty() {
                    continue;
                }
                ui.label(RichText::new(title.to_uppercase()).small().strong().color(pal.muted));
                for &n in list.iter().take(30) {
                    ui.horizontal(|ui| {
                        ui.add_space(8.0);
                        if ui.add(egui::Label::new(RichText::new(g.node_label(n)).color(pal.accent)).sense(egui::Sense::click())).clicked()
                        {
                            *jump = Some(n);
                        }
                        ui.label(RichText::new(g.node_repo(n)).small().color(pal.muted));
                    });
                }
                if list.len() > 30 {
                    ui.label(RichText::new(format!("   … {} more", list.len() - 30)).small().color(pal.muted));
                }
            }
            if !kids.is_empty() {
                ui.add_space(4.0);
                ui.label(RichText::new(format!("DERIVED IMAGES ({})", kids.len())).small().strong().color(pal.muted));
                ui.painter().rect_stroke(ui.min_rect(), 0, Stroke::NONE, egui::StrokeKind::Inside);
                for c in kids {
                    if path.contains(&c) {
                        continue;
                    }
                    image_row(ui, m, pal, c, depth + 1, q, keep, children, jump, focus, path);
                }
            }
            let _ = util::num(0);
        });
    path.pop();
}
