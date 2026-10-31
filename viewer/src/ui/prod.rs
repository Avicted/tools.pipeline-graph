//! Commit → prod: for one repository, everything its commits set in motion,
//! in dependency order, with each job's stage, trigger and container.

use super::app::Workspace;
use super::theme::{self, Palette};
use crate::core::derive::Class;
use crate::core::scene::Scene;
use egui::RichText;

impl Workspace {
    pub fn show_prod(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        let m = self.m.clone();
        let (g, d) = (&m.g, &m.d);
        let with_roots: Vec<usize> = (0..d.repos.len()).filter(|&i| d.repos[i].vcs > 0).collect();
        if with_roots.is_empty() {
            ui.add_space(40.0);
            ui.vertical_centered(|ui| {
                ui.label(RichText::new("No job is triggered by a commit").heading().color(pal.muted));
                ui.label(
                    RichText::new("Commit → prod follows the jobs that start on a commit. None were found in the analysed repositories.")
                        .color(pal.muted),
                );
            });
            return;
        }
        if !with_roots.contains(&self.prod_repo) {
            self.prod_repo = with_roots[0];
        }
        let r = &d.repos[self.prod_repo];
        egui::Frame::new().inner_margin(egui::Margin::symmetric(12, 8)).show(ui, |ui| {
            ui.horizontal_wrapped(|ui| {
                egui::ComboBox::from_id_salt("prod-repo").selected_text(RichText::new(g.s(r.name)).strong()).show_ui(ui, |ui| {
                    for &i in &with_roots {
                        if ui.selectable_label(self.prod_repo == i, g.s(d.repos[i].name)).clicked() {
                            self.prod_repo = i;
                            self.canvas.fit_pending = true;
                        }
                    }
                });
                ui.label(RichText::new(format!("{} commit-triggered job{}", r.vcs, if r.vcs == 1 { "" } else { "s" })).color(pal.muted));
                ui.label(RichText::new(format!("· longest chain {} jobs", r.longest)).color(pal.muted));
                if let Some(l) = self.layouts_for_prod() {
                    ui.separator();
                    let mut counts = [0u32; 8];
                    for n in &l.nodes {
                        if d.class[n.gid as usize] != Class::None {
                            counts[d.class[n.gid as usize].idx()] += 1;
                        }
                    }
                    for c in Class::STAGES {
                        let n = counts[c.idx()];
                        if n > 0 {
                            ui.label(
                                RichText::new(format!(" {} {} ", n, c.name()))
                                    .small()
                                    .color(egui::Color32::WHITE)
                                    .background_color(theme::class_color(c, pal.dark)),
                            );
                        }
                    }
                    let n = l.nodes.iter().filter(|n| d.no_test_up[n.gid as usize]).count();
                    if n > 0 {
                        ui.label(
                            RichText::new(format!("{n} deploy job{} with no test job upstream", if n == 1 { "" } else { "s" }))
                                .color(pal.changed),
                        );
                    }
                }
                ui.separator();
                self.lineage_chips(ui, pal);
            });
        });
        let scene = Scene::Flow(self.prod_repo as u32);
        self.canvas_view(ui, pal, &scene);
    }

    fn layouts_for_prod(&mut self) -> Option<std::sync::Arc<crate::core::scene::SceneLayout>> {
        let s = Scene::Flow(self.prod_repo as u32);
        self.layouts.get(&s, self.scene_limit)
    }
}
