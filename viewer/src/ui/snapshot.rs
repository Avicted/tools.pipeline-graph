//! The reality snapshot: what exists, per repository, at a glance. Descriptive
//! only; every number opens the table that explains it.

use super::app::Workspace;
use super::theme::{self, Palette};
use super::util;
use crate::core::derive::{Class, RepoStats};
use egui::{Color32, RichText, Sense};

fn big_stat(ui: &mut egui::Ui, pal: &Palette, value: &str, label: &str, color: Color32) -> bool {
    let size = egui::vec2(150.0, 74.0);
    let (rect, resp) = ui.allocate_exact_size(size, Sense::click());
    let hovered = resp.hovered();
    ui.painter().rect_filled(rect, 10, pal.card);
    ui.painter().rect_stroke(
        rect,
        10,
        super::theme::stroke(if hovered { 1.8 } else { 1.0 }, if hovered { color } else { pal.line }),
        egui::StrokeKind::Inside,
    );
    ui.painter().text(rect.left_top() + egui::vec2(14.0, 12.0), egui::Align2::LEFT_TOP, value, egui::FontId::proportional(28.0), color);
    ui.painter().text(
        rect.left_bottom() + egui::vec2(14.0, -10.0),
        egui::Align2::LEFT_BOTTOM,
        label,
        egui::FontId::proportional(12.5),
        pal.muted,
    );
    resp.on_hover_cursor(egui::CursorIcon::PointingHand).clicked()
}

/// A coloured count that opens the table when clicked.
fn fact(ui: &mut egui::Ui, pal: &Palette, n: u32, text: &str, tone: Option<Color32>) -> bool {
    let col = if n == 0 { pal.muted } else { tone.unwrap_or(pal.text) };
    let mut clicked = false;
    ui.horizontal(|ui| {
        let r = ui.add(egui::Label::new(RichText::new(util::num(n)).strong().color(col)).sense(if n > 0 {
            Sense::click()
        } else {
            Sense::hover()
        }));
        let t = ui.add(egui::Label::new(RichText::new(text).color(if n == 0 { pal.muted } else { pal.text })).sense(if n > 0 {
            Sense::click()
        } else {
            Sense::hover()
        }));
        if n > 0 && (r.clicked() || t.clicked()) {
            clicked = true;
        }
        if n > 0 {
            r.on_hover_cursor(egui::CursorIcon::PointingHand);
            t.on_hover_cursor(egui::CursorIcon::PointingHand);
        }
    });
    clicked
}

fn class_bar(ui: &mut egui::Ui, pal: &Palette, classes: &[u32; 8], width: f32) {
    let total: u32 = classes.iter().sum();
    let (rect, resp) = ui.allocate_exact_size(egui::vec2(width, 10.0), Sense::hover());
    if total == 0 {
        ui.painter().rect_filled(rect, 5, pal.line);
        return;
    }
    let mut x = rect.min.x;
    for c in Class::STAGES {
        let n = classes[c.idx()];
        if n == 0 {
            continue;
        }
        let w = rect.width() * n as f32 / total as f32;
        let seg = egui::Rect::from_min_size(egui::pos2(x, rect.min.y), egui::vec2(w, rect.height()));
        ui.painter().rect_filled(seg.shrink2(egui::vec2(0.5, 0.0)), 0, theme::class_color(c, pal.dark));
        x += w;
    }
    resp.on_hover_ui(|ui| {
        for c in Class::STAGES {
            let n = classes[c.idx()];
            if n > 0 {
                ui.horizontal(|ui| {
                    ui.label(RichText::new("■").color(theme::class_color(c, pal.dark)));
                    ui.label(format!("{} {}", n, c.name()));
                });
            }
        }
    });
}

impl Workspace {
    pub fn show_snapshot(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        let m = self.m.clone();
        let (g, d) = (&m.g, &m.d);
        let t = &d.totals;
        egui::ScrollArea::vertical().auto_shrink([false, false]).show(ui, |ui| {
            egui::Frame::new().inner_margin(egui::Margin::symmetric(24, 18)).show(ui, |ui| {
                ui.label(RichText::new("What is actually built").size(24.0).strong());
                let gen = g.s(g.generated);
                ui.label(
                    RichText::new(format!(
                        "{} · {} repositories · read in {} ms from {}",
                        if gen.is_empty() { "undated".to_string() } else { gen.to_string() },
                        g.repos.len(),
                        m.load_ms,
                        m.path.display()
                    ))
                    .color(pal.muted),
                );
                ui.add_space(14.0);
                ui.horizontal_wrapped(|ui| {
                    ui.spacing_mut().item_spacing = egui::vec2(12.0, 12.0);
                    if big_stat(ui, pal, &util::num(t.builds), "pipeline jobs", pal.accent) {
                        self.filter_table("kind:build");
                    }
                    if big_stat(
                        ui,
                        pal,
                        &util::num(t.images),
                        "docker images",
                        theme::kind_color(crate::core::graph::Kind::Image, false, pal.dark),
                    ) {
                        self.filter_table("kind:image");
                    }
                    if big_stat(
                        ui,
                        pal,
                        &util::num(t.makes),
                        "make targets",
                        theme::kind_color(crate::core::graph::Kind::Make, false, pal.dark),
                    ) {
                        self.table.kinds[2] = true;
                        self.filter_table("kind:make");
                    }
                    if big_stat(ui, pal, &util::num(t.containers), "images jobs run in", pal.up) {
                        self.filter_table("is:container");
                    }
                    if big_stat(ui, pal, &t.longest.to_string(), "longest chain", pal.down) {
                        if let Some(r) = (0..d.repos.len()).max_by_key(|&i| d.repos[i].longest) {
                            self.prod_repo = r;
                            self.go(super::app::Screen::Prod);
                        }
                    }
                    if big_stat(ui, pal, &util::num(t.warnings), "analysis warnings", if t.warnings > 0 { pal.warn } else { pal.muted }) {
                        self.show_warnings = true;
                    }
                });

                if let Some(df) = self.compare.diff.clone() {
                    ui.add_space(14.0);
                    egui::Frame::new().fill(pal.card).corner_radius(10).stroke(super::theme::stroke(1.0, pal.line)).inner_margin(14).show(
                        ui,
                        |ui| {
                            ui.horizontal_wrapped(|ui| {
                                ui.label(RichText::new("Since the compared snapshot").strong());
                                ui.label(RichText::new(format!("({})", df.old_generated)).color(pal.muted));
                                ui.label(RichText::new(format!("+{} new", df.added)).color(pal.added).strong());
                                ui.label(RichText::new(format!("−{} removed", df.removed.len())).color(pal.removed).strong());
                                ui.label(RichText::new(format!("~{} changed", df.changed)).color(pal.changed).strong());
                                if ui.link("Details").clicked() {
                                    self.go(super::app::Screen::Compare);
                                }
                            });
                        },
                    );
                }

                ui.add_space(18.0);
                ui.label(RichText::new("Across all repositories").size(16.0).strong());
                ui.add_space(4.0);
                egui::Frame::new().fill(pal.card).corner_radius(10).stroke(super::theme::stroke(1.0, pal.line)).inner_margin(14).show(
                    ui,
                    |ui| {
                        ui.set_width(ui.available_width().min(900.0));
                        if fact(ui, pal, t.vcs, "jobs start on a commit", None) {
                            self.filter_table("trig:vcs");
                        }
                        if fact(ui, pal, t.scheduled, "run on a schedule", None) {
                            self.filter_table("trig:scheduled");
                        }
                        if fact(ui, pal, t.chained, "start when another job finishes", None) {
                            self.filter_table("trig:chained");
                        }
                        if fact(ui, pal, t.manual, "are started by hand", None) {
                            self.filter_table("trig:manual");
                        }
                        ui.add_space(4.0);
                        if fact(
                            ui,
                            pal,
                            t.deploys_no_test,
                            &format!("of {} deploy jobs have no test job anywhere upstream", t.deploys),
                            Some(pal.changed),
                        ) {
                            self.filter_table("is:notest");
                        }
                        if fact(
                            ui,
                            pal,
                            t.isolated,
                            "jobs are stand-alone: nothing upstream or downstream, found by the analysis",
                            Some(pal.changed),
                        ) {
                            self.filter_table("is:isolated");
                        }
                        if fact(
                            ui,
                            pal,
                            t.undefined_make,
                            "make targets are invoked but have no rule in any makefile found",
                            Some(pal.changed),
                        ) {
                            self.table.kinds[2] = true;
                            self.filter_table("is:undefined");
                        }
                        if fact(ui, pal, t.externals, "referenced builds were not found in any analysed repository", Some(pal.warn)) {
                            self.filter_table("kind:external");
                        }
                    },
                );

                ui.add_space(18.0);
                ui.horizontal(|ui| {
                    ui.label(RichText::new("Repositories").size(16.0).strong());
                    ui.add_space(10.0);
                    for c in Class::STAGES {
                        ui.label(RichText::new("■").color(theme::class_color(c, pal.dark)));
                        ui.label(RichText::new(c.name()).small().color(pal.muted));
                    }
                });
                ui.add_space(6.0);
                let cols = (((ui.available_width() + 14.0) / (372.0 + 32.0 + 14.0)) as usize).max(1);
                for chunk in (0..d.repos.len()).collect::<Vec<_>>().chunks(cols) {
                    ui.horizontal_top(|ui| {
                        ui.spacing_mut().item_spacing = egui::vec2(14.0, 14.0);
                        for &i in chunk {
                            self.repo_card(ui, pal, i, &d.repos[i]);
                        }
                    });
                    ui.add_space(14.0);
                }
            });
        });
    }

    fn repo_card(&mut self, ui: &mut egui::Ui, pal: &Palette, i: usize, r: &RepoStats) {
        let m = self.m.clone();
        let g = &m.g;
        let name = g.s(r.name);
        let info = &g.repos[i];
        let delta = self.compare.diff.as_ref().and_then(|df| df.repos.iter().find(|x| x.repo == name).cloned());
        egui::Frame::new().fill(pal.card).corner_radius(12).stroke(super::theme::stroke(1.0, pal.line)).inner_margin(16).show(ui, |ui| {
            ui.set_width(372.0);
            ui.vertical(|ui| {
                ui.horizontal(|ui| {
                    if ui
                        .add(egui::Label::new(RichText::new(name).size(18.0).strong()).sense(Sense::click()))
                        .on_hover_cursor(egui::CursorIcon::PointingHand)
                        .clicked()
                    {
                        self.filter_table(&format!("repo:{name}"));
                    }
                    let c = g.s(info.commit);
                    if !c.is_empty() {
                        ui.label(RichText::new(util::short(c)).monospace().small().color(pal.muted));
                    }
                    if let Some(dl) = &delta {
                        if dl.added > 0 {
                            ui.label(RichText::new(format!("+{}", dl.added)).color(pal.added).strong());
                        }
                        if dl.removed > 0 {
                            ui.label(RichText::new(format!("−{}", dl.removed)).color(pal.removed).strong());
                        }
                        if dl.changed > 0 {
                            ui.label(RichText::new(format!("~{}", dl.changed)).color(pal.changed).strong());
                        }
                    }
                });
                ui.label(
                    RichText::new(format!(
                        "{} jobs · {} images · {} make targets",
                        util::num(r.builds),
                        util::num(r.images),
                        util::num(r.makes)
                    ))
                    .color(pal.muted),
                );
                ui.add_space(8.0);
                class_bar(ui, pal, &r.classes, 340.0);
                ui.add_space(8.0);
                let q = |extra: &str| format!("repo:{name} {extra}");
                let mut go: Option<String> = None;
                ui.horizontal_wrapped(|ui| {
                    for (n, label, trig) in [
                        (r.vcs, "on commit", "trig:vcs"),
                        (r.scheduled, "scheduled", "trig:scheduled"),
                        (r.chained, "chained", "trig:chained"),
                        (r.manual, "manual", "trig:manual"),
                    ] {
                        if n > 0
                            && ui
                                .add(egui::Label::new(RichText::new(format!("{n} {label}")).color(pal.accent)).sense(Sense::click()))
                                .on_hover_cursor(egui::CursorIcon::PointingHand)
                                .clicked()
                        {
                            go = Some(q(trig));
                        }
                    }
                });
                if r.deploys_no_test > 0
                    && fact(ui, pal, r.deploys_no_test, &format!("of {} deploys with no test upstream", r.deploys), Some(pal.changed))
                {
                    go = Some(q("is:notest"));
                }
                if r.isolated > 0 && fact(ui, pal, r.isolated, "stand-alone", Some(pal.changed)) {
                    go = Some(q("is:isolated"));
                }
                if r.containers > 0 && fact(ui, pal, r.containers, "images jobs run in", None) {
                    go = Some(q("is:container"));
                }
                if r.warnings > 0 && fact(ui, pal, r.warnings, "analysis warnings", Some(pal.warn)) {
                    self.show_warnings = true;
                    self.warn_filter = format!("{name}:");
                }
                if let Some(q) = go {
                    self.filter_table(&q);
                }
                if r.longest > 0 {
                    ui.add_space(6.0);
                    ui.horizontal_wrapped(|ui| {
                        ui.label(RichText::new(format!("Longest commit → prod chain: {} jobs", r.longest)).color(pal.muted));
                        if ui.link("Show").clicked() {
                            self.prod_repo = i;
                            self.go(super::app::Screen::Prod);
                        }
                    });
                }
            });
        });
    }
}
