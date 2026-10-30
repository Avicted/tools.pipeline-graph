//! The reality snapshot: what exists, per repository, at a glance. Descriptive
//! only; every number opens the table that explains it.

use super::app::Workspace;
use super::theme::{self, Palette};
use super::util;
use crate::core::derive::{Class, RepoStats};
use crate::core::graph::Graph;
use egui::{Color32, RichText, Sense};

fn big_stat(ui: &mut egui::Ui, pal: &Palette, value: &str, label: &str, color: Color32, tip: &str) -> bool {
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
    util::hint(resp.on_hover_cursor(egui::CursorIcon::PointingHand), label, tip).clicked()
}

/// A coloured count and what it means; opens the table when clicked.
fn fact(ui: &mut egui::Ui, pal: &Palette, n: u32, text: &str, tone: Option<Color32>, tip: (&str, &str)) -> bool {
    let col = if n == 0 { pal.muted } else { tone.unwrap_or(pal.text) };
    ui.horizontal(|ui| {
        let num = RichText::new(util::num(n)).strong().color(col);
        let text = RichText::new(text).color(if n == 0 { pal.muted } else { pal.text });
        let (a, b) = if n > 0 { (util::link(ui, num), util::link(ui, text)) } else { (ui.label(num), ui.label(text)) };
        let a = util::hint(a, tip.0, tip.1);
        let b = util::hint(b, tip.0, tip.1);
        n > 0 && (a.clicked() || b.clicked())
    })
    .inner
}

/// A count in a pill: "7 on a commit".
fn chip(ui: &mut egui::Ui, pal: &Palette, n: u32, text: &str, tip: (&str, &str)) -> bool {
    let col = if n == 0 { pal.muted } else { pal.accent };
    let b = egui::Button::new(RichText::new(format!("{} {text}", util::num(n))).color(col))
        .fill(theme::with_alpha(col, 18))
        .stroke(theme::stroke(1.0, theme::with_alpha(col, 70)))
        .corner_radius(12);
    let r = ui.add_enabled(n > 0, b);
    util::hint(r, tip.0, tip.1).on_hover_cursor(egui::CursorIcon::PointingHand).clicked()
}

enum Look {
    Table(&'static str),
    Make,
    Warnings,
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
                    if big_stat(ui, pal, &util::num(t.builds), "pipeline jobs", pal.accent, &jobs_tip(g)) {
                        self.filter_table("kind:build");
                    }
                    if big_stat(
                        ui,
                        pal,
                        &util::num(t.images),
                        "docker images",
                        theme::kind_color(crate::core::graph::Kind::Image, false, pal.dark),
                        STAT_TIPS[1],
                    ) {
                        self.filter_table("kind:image");
                    }
                    if big_stat(
                        ui,
                        pal,
                        &util::num(t.makes),
                        "make targets",
                        theme::kind_color(crate::core::graph::Kind::Make, false, pal.dark),
                        STAT_TIPS[2],
                    ) {
                        self.table.kinds[2] = true;
                        self.filter_table("kind:make");
                    }
                    if big_stat(ui, pal, &util::num(t.containers), "images jobs run in", pal.up, STAT_TIPS[3]) {
                        self.filter_table("is:container");
                    }
                    if big_stat(ui, pal, &t.longest.to_string(), "longest chain", pal.down, STAT_TIPS[4]) {
                        if let Some(r) = (0..d.repos.len()).max_by_key(|&i| d.repos[i].longest) {
                            self.prod_repo = r;
                            self.go(super::app::Screen::Prod);
                        }
                    }
                    if big_stat(
                        ui,
                        pal,
                        &util::num(t.warnings),
                        "analysis warnings",
                        if t.warnings > 0 { pal.warn } else { pal.muted },
                        STAT_TIPS[5],
                    ) {
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
                self.worth_a_look(ui, pal);
                ui.add_space(14.0);
                ui.horizontal_wrapped(|ui| {
                    ui.label(RichText::new("How work starts").size(16.0).strong());
                    ui.add_space(8.0);
                    for (n, text, tip, q) in [
                        (t.vcs, "on a commit", TRIG_TIPS[0], "trig:vcs"),
                        (t.scheduled, "on a schedule", TRIG_TIPS[1], "trig:scheduled"),
                        (t.chained, "when another job finishes", TRIG_TIPS[2], "trig:chained"),
                        (t.manual, "by hand", TRIG_TIPS[3], "trig:manual"),
                    ] {
                        if chip(ui, pal, n, text, tip) {
                            self.filter_table(q);
                        }
                    }
                });
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
                    if util::link(ui, RichText::new(name).size(18.0).strong()).on_hover_text("List everything in this repository").clicked()
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
                    for (n, label, tip, trig) in [
                        (r.vcs, "on commit", TRIG_TIPS[0], "trig:vcs"),
                        (r.scheduled, "scheduled", TRIG_TIPS[1], "trig:scheduled"),
                        (r.chained, "chained", TRIG_TIPS[2], "trig:chained"),
                        (r.manual, "manual", TRIG_TIPS[3], "trig:manual"),
                    ] {
                        if n > 0
                            && util::hint(util::link(ui, RichText::new(format!("{n} {label}")).color(pal.accent)), tip.0, tip.1).clicked()
                        {
                            go = Some(q(trig));
                        }
                    }
                });
                if r.deploys_no_test > 0
                    && fact(
                        ui,
                        pal,
                        r.deploys_no_test,
                        &format!("of {} deploys with no test upstream", r.deploys),
                        Some(pal.changed),
                        LOOK_TIPS[0],
                    )
                {
                    go = Some(q("is:notest"));
                }
                if r.isolated > 0 && fact(ui, pal, r.isolated, "stand-alone", Some(pal.changed), LOOK_TIPS[1]) {
                    go = Some(q("is:isolated"));
                }
                if r.containers > 0 && fact(ui, pal, r.containers, "images jobs run in", None, ("Images jobs run in", STAT_TIPS[3])) {
                    go = Some(q("is:container"));
                }
                if r.warnings > 0 && fact(ui, pal, r.warnings, "analysis warnings", Some(pal.warn), ("Analysis warnings", STAT_TIPS[5])) {
                    self.show_warnings = true;
                    self.warn_filter = format!("{name}:");
                }
                if let Some(q) = go {
                    self.filter_table(&q);
                }
                if r.longest > 0 {
                    ui.add_space(6.0);
                    ui.horizontal_wrapped(|ui| {
                        let l = ui.label(RichText::new(format!("Longest commit → prod chain: {} jobs", r.longest)).color(pal.muted));
                        util::hint(l, "Longest commit → prod chain", STAT_TIPS[4]);
                        if ui.link("Show").clicked() {
                            self.prod_repo = i;
                            self.go(super::app::Screen::Prod);
                        }
                    });
                }
            });
        });
    }

    /// The facts a pipeline designer would want to check, the ones that occur first.
    fn worth_a_look(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        let t = self.m.d.totals.clone();
        let hubs = self.m.d.hubs.len() as u32;
        let busiest = self.m.d.hubs.first().map_or(0, |&v| self.m.d.down[v as usize]);
        let mut rows = vec![
            (
                t.cyclic,
                "jobs wait on each other in a cycle, so none of them has a start order".into(),
                LOOK_TIPS[4],
                Look::Table("is:cycle"),
            ),
            (
                hubs,
                format!("load-bearing jobs: the busiest has {} other jobs depending on it", util::num(busiest)),
                LOOK_TIPS[5],
                Look::Table("is:hub"),
            ),
            (t.unused, "images are never run in, derived from or consumed".into(), LOOK_TIPS[6], Look::Table("is:unused")),
            (
                t.deploys_no_test,
                format!("of {} deploy jobs have no test job anywhere upstream", t.deploys),
                LOOK_TIPS[0],
                Look::Table("is:notest"),
            ),
            (t.isolated, "jobs are stand-alone: nothing upstream or downstream".into(), LOOK_TIPS[1], Look::Table("is:isolated")),
            (t.undefined_make, "make targets are invoked but no makefile found has a rule for them".into(), LOOK_TIPS[2], Look::Make),
            (
                t.externals,
                "referenced builds are not defined in any analysed repository".into(),
                LOOK_TIPS[3],
                Look::Table("kind:external"),
            ),
            (
                t.warnings,
                "analysis warnings: references or files that could not be resolved".into(),
                ("Analysis warnings", STAT_TIPS[5]),
                Look::Warnings,
            ),
        ];
        rows.sort_by_key(|r| r.0 == 0);
        ui.horizontal(|ui| {
            ui.label(RichText::new("Worth a look").size(16.0).strong());
            ui.label(RichText::new("descriptive, not a verdict: hover for why each matters").small().color(pal.muted));
        });
        ui.add_space(4.0);
        let mut go = None;
        egui::Frame::new().fill(pal.card).corner_radius(10).stroke(theme::stroke(1.0, pal.line)).inner_margin(14).show(ui, |ui| {
            ui.set_width(ui.available_width().min(900.0));
            if rows.iter().all(|r| r.0 == 0) {
                ui.label(RichText::new("Nothing stands out in this snapshot.").color(pal.muted));
            }
            for (n, text, tip, look) in rows {
                let tone = match look {
                    Look::Warnings | Look::Table("kind:external" | "is:cycle") => Some(pal.warn),
                    Look::Table("is:hub") => None,
                    _ => Some(pal.changed),
                };
                if fact(ui, pal, n, &text, tone, tip) {
                    go = Some(look);
                }
            }
        });
        match go {
            Some(Look::Table(q)) => self.filter_table(q),
            Some(Look::Make) => {
                self.table.kinds[2] = true;
                self.filter_table("is:undefined");
            }
            Some(Look::Warnings) => self.show_warnings = true,
            None => {}
        }
    }
}

/// What the jobs headline number counts, in the words of the CI systems in the graph.
fn jobs_tip(g: &Graph) -> String {
    let jobs = g.jobs_name();
    let (first, rest) = jobs.split_at(jobs.chars().next().map_or(0, char::len_utf8));
    format!("{}{rest} defined in the analysed repositories. Click to list them.", first.to_uppercase())
}

/// What each other headline number counts and why it matters. Index 0 is `jobs_tip`.
const STAT_TIPS: [&str; 6] = [
    "",
    "Docker images built, pulled or run in, from Dockerfiles and CI steps. Click to list them.",
    "Make targets CI steps run, directly or through other targets. Click to list them.",
    "Images that job steps run inside, such as build containers. What is in them shapes every job that runs there. Click to list them.",
    "The most jobs one commit passes through on its way to a deploy. Every link is one more place to wait or fail. Click to open that chain.",
    "References, files and settings the analysis could not resolve. They are listed, never dropped silently. Click to read them.",
];

const TRIG_TIPS: [(&str, &str); 4] = [
    ("Start on a commit", "Jobs with a VCS trigger: every push to a watched branch starts them. Click to list them."),
    ("Run on a schedule", "Jobs with a schedule trigger. Their result can be up to one period old. Click to list them."),
    ("Chained", "Jobs started by a finish-build trigger when another job completes. Click to list them."),
    ("Started by hand", "No trigger of their own and no job starts them: someone has to. Click to list them."),
];

const LOOK_TIPS: [(&str, &str); 7] = [
    (
        "Deploys with no test upstream",
        "No job classified as a test runs anywhere before these deploys in their dependency chain. They may be tested in a way the analysis cannot see; worth confirming. Click to list them.",
    ),
    (
        "Stand-alone jobs",
        "Nothing they need and nothing needs them. Often leftovers, or wired up through something the analysis does not read. Click to list them.",
    ),
    (
        "Make targets with no rule",
        "A step runs make with a target no analysed makefile defines. The step fails, or the rule comes from a file outside the analysis. Click to list them.",
    ),
    (
        "Builds not found",
        "Dependencies or triggers point at build ids that no analysed repository defines: another repository, a deleted job, or a typo. Click to list them.",
    ),
    (
        "Jobs in a cycle",
        "Each of these needs another job in the group, directly or through others, that needs it back. Nothing can start first, so the layout and the “longest chain” leave them out of order. Click to list them.",
    ),
    (
        "Load-bearing jobs",
        "The ten jobs with the most other jobs transitively depending on them (at least three). When one of these breaks or slows down, everything downstream waits. Click to list them, or open one and press B to see what it breaks.",
    ),
    (
        "Images nothing uses",
        "Docker images that no job runs inside and no other image is built from, and nothing consumes. Often a leftover, or used by something outside the analysis. Click to list them.",
    ),
];
