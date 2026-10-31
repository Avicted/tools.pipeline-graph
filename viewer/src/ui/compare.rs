//! Compare two snapshots of the analysis: what was added, removed or changed.

use super::app::{Model, Screen, Workspace};
use super::job::Job;
use super::theme::Palette;
use super::util;
use crate::core::diff::{self, Diff};
use crate::core::graph::Graph;
use egui::RichText;
use std::path::{Path, PathBuf};
use std::sync::Arc;

#[derive(Default)]
pub struct CompareState {
    pub diff: Option<Arc<Diff>>,
    pub old_path: Option<PathBuf>,
    pub job: Option<Job<Result<Diff, String>>>,
    pub snapshots: Vec<(String, PathBuf)>,
    pub overlay: bool,
    pub error: Option<String>,
    tab: usize,
}

impl CompareState {
    /// Finds the dated copies pipeline-graph keeps next to graph.json.
    pub fn discover(&mut self, graph_path: &Path) {
        self.snapshots.clear();
        let Some(dir) = graph_path.parent().map(|p| p.join("history")) else { return };
        let Ok(rd) = std::fs::read_dir(dir) else { return };
        let mut v: Vec<(String, PathBuf)> = rd
            .filter_map(|e| e.ok())
            .map(|e| e.path())
            .filter(|p| p.extension().is_some_and(|x| x == "json"))
            .map(|p| (p.file_stem().map(|s| s.to_string_lossy().into_owned()).unwrap_or_default(), p))
            .collect();
        v.sort_by(|a, b| b.0.cmp(&a.0));
        self.snapshots = v;
    }
}

impl Workspace {
    pub fn start_compare(&mut self, ctx: &egui::Context, path: PathBuf) {
        let m: Arc<Model> = self.m.clone();
        let p = path.clone();
        self.compare.error = None;
        self.compare.old_path = Some(path);
        self.compare.job = Some(Job::spawn(ctx, move || {
            let old = Graph::load(&p).map_err(|e| e.to_string())?;
            Ok(diff::compare(&old, &m.g))
        }));
    }

    pub fn poll_compare(&mut self) {
        let Some(job) = &self.compare.job else { return };
        let Some(r) = job.poll_result() else { return };
        self.compare.job = None;
        match r {
            Ok(df) => {
                self.toast(format!("Compared: +{} new, −{} removed, ~{} changed", df.added, df.removed.len(), df.changed));
                self.compare.diff = Some(Arc::new(df));
                self.compare.overlay = true;
                self.table.dirty = true;
            }
            Err(e) => {
                self.compare.error = Some(e);
                self.compare.old_path = None;
            }
        }
    }

    pub fn show_compare(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        let m = self.m.clone();
        let g = &m.g;
        let ctx = ui.ctx().clone();
        egui::ScrollArea::vertical().auto_shrink([false, false]).show(ui, |ui| {
            egui::Frame::new().inner_margin(egui::Margin::symmetric(24, 18)).show(ui, |ui| {
                ui.label(RichText::new("What changed").size(22.0).strong());
                ui.label(
                    RichText::new(
                        "Compare this snapshot with an earlier one. Each run of pipeline-graph keeps a dated copy in out/history.",
                    )
                    .color(pal.muted),
                );
                ui.add_space(10.0);
                ui.horizontal_wrapped(|ui| {
                    let cur =
                        self.compare.old_path.as_ref().map(|p| p.file_stem().map(|s| s.to_string_lossy().into_owned()).unwrap_or_default());
                    egui::ComboBox::from_id_salt("snap")
                        .width(320.0)
                        .selected_text(cur.unwrap_or_else(|| "Choose an earlier snapshot…".into()))
                        .show_ui(ui, |ui| {
                            for (label, p) in self.compare.snapshots.clone() {
                                if ui.selectable_label(self.compare.old_path.as_ref() == Some(&p), &label).clicked() {
                                    self.start_compare(&ctx, p);
                                }
                            }
                            if self.compare.snapshots.is_empty() {
                                ui.label(RichText::new("No snapshots found next to graph.json").color(pal.muted));
                            }
                        });
                    if ui.button("Open file…").clicked() {
                        if let Some(p) = rfd::FileDialog::new().add_filter("pipeline-graph", &["json"]).pick_file() {
                            self.start_compare(&ctx, p);
                        }
                    }
                    if self.compare.diff.is_some() {
                        if ui.button("Clear").clicked() {
                            self.compare.diff = None;
                            self.compare.old_path = None;
                            self.table.dirty = true;
                        }
                        ui.checkbox(&mut self.compare.overlay, "Mark changes on the table and map");
                    }
                    if self.compare.job.is_some() {
                        ui.spinner();
                    }
                });
                if let Some(e) = &self.compare.error {
                    ui.colored_label(pal.removed, e);
                }
                let Some(df) = self.compare.diff.clone() else {
                    ui.add_space(16.0);
                    egui::Frame::new().fill(pal.card).corner_radius(10).stroke(super::theme::stroke(1.0, pal.line)).inner_margin(16).show(
                        ui,
                        |ui| {
                            ui.label("Nothing compared yet.");
                            ui.label(
                                RichText::new("Pick a snapshot above, or start the viewer with  --compare old/graph.json.")
                                    .color(pal.muted),
                            );
                        },
                    );
                    return;
                };
                ui.add_space(8.0);
                ui.label(RichText::new(format!("{}  →  {}", df.old_generated, df.new_generated)).color(pal.muted));
                if df.is_empty() {
                    ui.add_space(10.0);
                    ui.label(RichText::new("No difference: the two snapshots describe the same pipelines.").strong());
                    return;
                }
                ui.add_space(12.0);
                ui.horizontal_wrapped(|ui| {
                    ui.spacing_mut().item_spacing = egui::vec2(12.0, 12.0);
                    let tiles = [
                        (df.added as usize, "new", pal.added, 0),
                        (df.removed.len(), "removed", pal.removed, 1),
                        (df.changed as usize, "changed", pal.changed, 2),
                        (df.edges_added.len(), "links added", pal.added, 3),
                        (df.edges_removed.len(), "links gone", pal.removed, 3),
                    ];
                    for (n, label, col, tab) in tiles {
                        let (rect, resp) = ui.allocate_exact_size(egui::vec2(130.0, 64.0), egui::Sense::click());
                        let active = self.compare.tab == tab;
                        ui.painter().rect_filled(rect, 10, pal.card);
                        ui.painter().rect_stroke(
                            rect,
                            10,
                            super::theme::stroke(if active { 2.0 } else { 1.0 }, if active { col } else { pal.line }),
                            egui::StrokeKind::Inside,
                        );
                        ui.painter().text(
                            rect.left_top() + egui::vec2(12.0, 8.0),
                            egui::Align2::LEFT_TOP,
                            util::num(n as u32),
                            egui::FontId::proportional(24.0),
                            col,
                        );
                        ui.painter().text(
                            rect.left_bottom() + egui::vec2(12.0, -8.0),
                            egui::Align2::LEFT_BOTTOM,
                            label,
                            egui::FontId::proportional(12.5),
                            pal.muted,
                        );
                        if resp.clicked() {
                            self.compare.tab = tab;
                        }
                    }
                });

                if !df.repos.is_empty() {
                    ui.add_space(14.0);
                    ui.label(RichText::new("By repository").strong());
                    egui::Grid::new("repo-delta").num_columns(5).spacing([22.0, 4.0]).striped(true).show(ui, |ui| {
                        for h in ["repo", "new", "removed", "changed", "commit"] {
                            ui.label(RichText::new(h.to_uppercase()).small().strong().color(pal.muted));
                        }
                        ui.end_row();
                        for r in &df.repos {
                            ui.label(&r.repo);
                            ui.label(RichText::new(r.added.to_string()).color(if r.added > 0 { pal.added } else { pal.muted }));
                            ui.label(RichText::new(r.removed.to_string()).color(if r.removed > 0 { pal.removed } else { pal.muted }));
                            ui.label(RichText::new(r.changed.to_string()).color(if r.changed > 0 { pal.changed } else { pal.muted }));
                            let (o, n) = (util::short(&r.old_commit), util::short(&r.new_commit));
                            ui.label(
                                RichText::new(if o == n { n.to_string() } else { format!("{o} → {n}") })
                                    .monospace()
                                    .small()
                                    .color(if o == n { pal.muted } else { pal.changed }),
                            );
                            ui.end_row();
                        }
                    });
                }

                ui.add_space(14.0);
                ui.horizontal(|ui| {
                    for (i, name) in ["New", "Removed", "Changed", "Links"].iter().enumerate() {
                        ui.selectable_value(&mut self.compare.tab, i, *name);
                    }
                });
                ui.add_space(6.0);
                let mut open: Option<u32> = None;
                match self.compare.tab {
                    0 | 2 => {
                        let want = if self.compare.tab == 0 { diff::Change::Added } else { diff::Change::Changed };
                        let mut n = 0;
                        for v in 0..g.len() as u32 {
                            if df.state[v as usize] != want {
                                continue;
                            }
                            n += 1;
                            if n > 500 {
                                ui.label(RichText::new("… more, filter the table with “only changes”").color(pal.muted));
                                break;
                            }
                            let notes = df.notes.get(&v);
                            ui.horizontal(|ui| {
                                if util::link(ui, RichText::new(g.node_label(v)).color(pal.accent).strong()).clicked() {
                                    open = Some(v);
                                }
                                ui.label(RichText::new(format!("{} · {}", g.kind_name(v), g.node_repo(v))).small().color(pal.muted));
                            });
                            if want == diff::Change::Changed {
                                for note in notes.into_iter().flatten() {
                                    ui.label(RichText::new(format!("      • {note}")).small());
                                }
                            }
                        }
                        if n == 0 {
                            ui.label(RichText::new("None").color(pal.muted));
                        }
                    }
                    1 => {
                        for r in df.removed.iter().take(1000) {
                            ui.horizontal(|ui| {
                                ui.label(RichText::new(&r.label).strong().color(pal.removed));
                                ui.label(RichText::new(format!("{:?} · {}", r.kind, r.repo)).small().color(pal.muted));
                            });
                        }
                        if df.removed.is_empty() {
                            ui.label(RichText::new("None").color(pal.muted));
                        }
                    }
                    _ => {
                        for (title, list, col) in
                            [("Links added", &df.edges_added, pal.added), ("Links gone", &df.edges_removed, pal.removed)]
                        {
                            ui.label(RichText::new(format!("{} ({})", title.to_uppercase(), list.len())).small().strong().color(col));
                            for e in list.iter().take(300) {
                                let name = |id: &str| g.node_by_id(id).map_or_else(|| id.to_string(), |v| g.node_label(v).to_string());
                                ui.label(RichText::new(format!("{}  →  {}   ({})", name(&e.from), name(&e.to), e.kind.name())).small());
                            }
                            if list.len() > 300 {
                                ui.label(RichText::new(format!("… {} more", list.len() - 300)).small().color(pal.muted));
                            }
                            ui.add_space(6.0);
                        }
                    }
                }
                if let Some(v) = open {
                    self.screen = Screen::Table;
                    self.select(Some(v));
                    self.table.reveal = Some(v);
                }
            });
        });
    }
}
