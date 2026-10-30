//! The help overlay: every shortcut, grouped by what you want to do, and a legend
//! for reading the views. Opened with `?`, closed with Esc or a click outside.

use super::app::{Workspace, APP_NAME};
use super::theme::{self, Palette};
use crate::core::derive::Class;
use crate::core::graph::{Ek, Kind};
use egui::{Color32, RichText, Sense};

type Group = (&'static str, &'static [(&'static [&'static str], &'static str)]);

const SHORTCUTS: [&[Group]; 2] = [
    &[
        (
            "Find",
            &[
                (&["Ctrl", "K"], "Search jobs, images, repos and commands; start with > for commands only"),
                (&["/"], "Filter the table or the image list; elsewhere, open search"),
            ],
        ),
        (
            "Move around",
            &[
                (&["1 – 6"], "Snapshot, Jobs & images, Map, Commit → prod, Images, Compare"),
                (&["Tab"], "Switch between the table and the map"),
                (&["↑", "↓"], "Move through the table"),
                (&["Alt", "←", "→"], "Back and forward"),
            ],
        ),
        (
            "Explore a job",
            &[
                (&["Enter"], "Open the selected job's lineage on its own"),
                (&["Double-click"], "The same, from anywhere"),
                (&["B"], "Keep only what it breaks: everything downstream"),
                (&["D"], "Keep only what it needs: everything upstream"),
                (&["I"], "Keep both"),
                (&["Shift", "click"], "Keep only the route between two jobs"),
                (&["Esc"], "Step back: route, filter, selection, lineage"),
            ],
        ),
    ],
    &[
        ("Map", &[(&["F"], "Fit the map to the window"), (&["Scroll"], "Pan; hold Ctrl to zoom"), (&["Drag"], "Pan")]),
        (
            "Copy & share",
            &[
                (&["Drag", "Ctrl", "C"], "Select any text and copy it"),
                (&["Ctrl", "C"], "With no text selected: copy the selected job's id"),
                (&["Right-click"], "Copy a job's name, id, source or link, in the table and on the map"),
                (&["L"], "Copy a pgview:// link to exactly this view; open it with --goto"),
            ],
        ),
        ("App", &[(&["T"], "Light or dark"), (&["R"], "Reload graph.json from disk"), (&["?"], "This help")]),
    ],
];

const STAGES: [(Class, &str); 8] = [
    (Class::Build, "compiles or assembles"),
    (Class::Lint, "checks style and static rules"),
    (Class::Test, "runs tests"),
    (Class::Scan, "security, licence or CVE scanning"),
    (Class::Package, "builds images or archives"),
    (Class::Publish, "pushes artefacts or tags a release"),
    (Class::Deploy, "changes an environment"),
    (Class::Other, "none of the above"),
];

/// A key drawn as a key cap.
fn keycap(ui: &mut egui::Ui, pal: &Palette, key: &str) {
    let galley = ui.painter().layout_no_wrap(key.to_string(), egui::FontId::monospace(12.5), pal.text);
    let size = egui::vec2((galley.size().x + 14.0).max(24.0), 22.0);
    let (rect, _) = ui.allocate_exact_size(size, Sense::hover());
    let p = ui.painter();
    p.rect_filled(rect.translate(egui::vec2(0.0, 1.5)), 5, pal.line);
    p.rect(rect, 5, pal.card, theme::stroke(1.0, pal.line), egui::StrokeKind::Inside);
    p.galley(rect.center() - galley.size() / 2.0, galley, pal.text);
}

fn group_title(ui: &mut egui::Ui, pal: &Palette, title: &str) {
    ui.add_space(6.0);
    ui.label(RichText::new(title.to_uppercase()).small().strong().color(pal.muted));
    ui.add_space(4.0);
}

fn shortcuts(ui: &mut egui::Ui, pal: &Palette) {
    ui.columns(2, |cols| {
        for (col, groups) in cols.iter_mut().zip(SHORTCUTS) {
            col.spacing_mut().item_spacing.y = 7.0;
            for (title, rows) in groups {
                group_title(col, pal, title);
                for (keys, text) in *rows {
                    col.horizontal(|ui| {
                        ui.spacing_mut().item_spacing.x = 4.0;
                        ui.allocate_ui_with_layout(egui::vec2(150.0, 22.0), egui::Layout::left_to_right(egui::Align::Center), |ui| {
                            ui.set_min_width(150.0);
                            for k in *keys {
                                keycap(ui, pal, k);
                            }
                        });
                        ui.add(egui::Label::new(*text).wrap());
                    });
                }
                col.add_space(6.0);
            }
        }
    });
}

fn swatch(ui: &mut egui::Ui, c: Color32) {
    let (r, _) = ui.allocate_exact_size(egui::vec2(14.0, 14.0), Sense::hover());
    ui.painter().rect_filled(r, 4, c);
}

fn line_sample(ui: &mut egui::Ui, k: Ek, dark: bool) {
    let st = theme::edge_style(k, dark);
    let (r, _) = ui.allocate_exact_size(egui::vec2(44.0, 14.0), Sense::hover());
    let (a, b) = (r.left_center(), r.right_center() - egui::vec2(6.0, 0.0));
    let stroke = theme::stroke(st.width, st.color);
    match st.dash {
        Some((dl, gap)) => {
            ui.painter().extend(egui::Shape::dashed_line(&[a, b], stroke, dl, gap));
        }
        None => {
            ui.painter().line_segment([a, b], stroke);
        }
    }
    let tip = r.right_center();
    ui.painter().add(egui::Shape::convex_polygon(
        vec![tip, tip - egui::vec2(6.0, 3.5), tip - egui::vec2(6.0, -3.5)],
        st.color,
        egui::Stroke::NONE,
    ));
}

fn row(ui: &mut egui::Ui, mark: impl FnOnce(&mut egui::Ui), name: &str, text: &str, pal: &Palette) {
    ui.horizontal(|ui| {
        mark(ui);
        ui.label(RichText::new(name).strong());
        ui.add(egui::Label::new(RichText::new(text).color(pal.muted)).wrap());
    });
}

fn legend(ui: &mut egui::Ui, pal: &Palette, jobs: &str) {
    let dark = pal.dark;
    ui.columns(2, |cols| {
        let ui = &mut cols[0];
        ui.spacing_mut().item_spacing.y = 6.0;
        group_title(ui, pal, "Stage: what a job does");
        for (c, text) in STAGES {
            row(ui, |ui| swatch(ui, theme::class_color(c, dark)), c.name(), text, pal);
        }
        ui.label(RichText::new("Read from the job's name and steps; hover a stage badge to see why.").small().color(pal.muted));
        ui.add_space(6.0);
        group_title(ui, pal, "What a node is");
        let job = format!("one of the {jobs}");
        for (k, composite, name, text) in [
            (Kind::Build, false, "job", job.as_str()),
            (Kind::Build, true, "composite", "bundles other jobs and runs nothing itself"),
            (Kind::Image, false, "image", "a Docker image built, pulled or run in"),
            (Kind::Make, false, "make", "a make target a step runs"),
            (Kind::External, false, "external", "referenced, but not in any analysed repository"),
        ] {
            row(ui, |ui| swatch(ui, theme::kind_color(k, composite, dark)), name, text, pal);
        }

        let ui = &mut cols[1];
        ui.spacing_mut().item_spacing.y = 6.0;
        group_title(ui, pal, "Needs and breaks");
        row(ui, |ui| swatch(ui, pal.up), "needs", "everything upstream that must run first for this job to get fresh input", pal);
        row(ui, |ui| swatch(ui, pal.down), "breaks", "everything downstream that goes stale if this job fails: its blast radius", pal);
        ui.add_space(6.0);
        group_title(ui, pal, "Lines on the map");
        for k in Ek::ALL {
            ui.horizontal(|ui| {
                line_sample(ui, k, dark);
                ui.label(k.name());
            });
        }
        ui.add_space(6.0);
        group_title(ui, pal, "Since the compared snapshot");
        row(ui, |ui| swatch(ui, pal.added), "new", "appeared since the older snapshot", pal);
        row(ui, |ui| swatch(ui, pal.changed), "changed", "triggers, steps, images or links differ", pal);
        row(ui, |ui| swatch(ui, pal.removed), "removed", "was there before, gone now", pal);
    });
}

impl Workspace {
    pub fn help_overlay(&mut self, ctx: &egui::Context, pal: &Palette) {
        let width = (ctx.screen_rect().width() - 80.0).clamp(560.0, 900.0);
        let frame = egui::Frame::new()
            .fill(pal.panel)
            .stroke(theme::stroke(1.0, pal.line))
            .corner_radius(14)
            .inner_margin(egui::Margin::symmetric(26, 22))
            .shadow(ctx.style().visuals.window_shadow);
        let r = egui::Modal::new(egui::Id::new("help")).frame(frame).backdrop_color(Color32::from_black_alpha(120)).show(ctx, |ui| {
            ui.set_width(width);
            ui.horizontal(|ui| {
                ui.spacing_mut().item_spacing.x = 0.0;
                let (org, name) = APP_NAME.split_once('/').unwrap_or(("", APP_NAME));
                ui.label(RichText::new(format!("{org}/")).size(22.0).color(pal.muted));
                ui.label(RichText::new(name).size(22.0).strong());
                ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                    ui.spacing_mut().item_spacing.x = 6.0;
                    if ui.add(egui::Button::new("✕").frame(false)).on_hover_text("Close (Esc)").clicked() {
                        self.show_help = false;
                    }
                    ui.add_space(8.0);
                    for (i, name) in ["Reading the views", "Shortcuts"].into_iter().enumerate().map(|(i, n)| (1 - i, n)) {
                        ui.selectable_value(&mut self.help_tab, i, name);
                    }
                });
            });
            ui.label(
                RichText::new(
                    "What your CI configuration, Dockerfiles and makefiles say is built, at the analysed commits. \
                     Descriptive only: nothing is a verdict, and anything the analysis could not resolve is listed \
                     under analysis warnings.",
                )
                .color(pal.muted),
            );
            ui.add_space(10.0);
            ui.separator();
            let h = (ctx.screen_rect().height() - 240.0).clamp(240.0, 620.0);
            egui::ScrollArea::vertical().max_height(h).min_scrolled_height(h).show(ui, |ui| {
                if self.help_tab == 0 {
                    shortcuts(ui, pal);
                } else {
                    legend(ui, pal, &self.m.g.jobs_name());
                }
            });
        });
        if r.should_close() {
            self.show_help = false;
        }
    }
}
