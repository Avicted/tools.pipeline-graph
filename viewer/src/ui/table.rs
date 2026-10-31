//! The explorer table: every job, image and make target, sortable and filterable.

use super::theme::{self, Palette};
use super::util;
use super::workspace::Workspace;
use crate::core::derive::{Class, ENTRY_CHAINED, ENTRY_MANUAL, ENTRY_SCHEDULE, ENTRY_VCS};
use crate::core::diff::Change;
use crate::core::graph::{Ek, Kind};
use crate::core::search::Query;
use egui::{Color32, Key, RichText, Sense};
use egui_extras::{Column, TableBuilder};

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Col {
    Name,
    Repo,
    Stage,
    Needs,
    Breaks,
    Change,
}

pub struct TableState {
    pub query: String,
    pub by_relevance: bool,
    pub sort: Col,
    pub desc: bool,
    pub kinds: [bool; 4], // jobs, images, make targets, external
    pub class: Option<Class>,
    pub trig: u8,
    pub repo: Option<u32>,
    pub changes_only: bool,
    pub rows: Vec<u32>,
    pub dirty: bool,
    pub focus_filter: bool,
    pub reveal: Option<u32>,
    pub total: usize,
    max_down: u32,
}

impl Default for TableState {
    fn default() -> Self {
        TableState {
            query: String::new(),
            by_relevance: true,
            sort: Col::Breaks,
            desc: true,
            kinds: [true, true, false, true],
            class: None,
            trig: 0,
            repo: None,
            changes_only: false,
            rows: vec![],
            dirty: true,
            focus_filter: false,
            reveal: None,
            total: 0,
            max_down: 1,
        }
    }
}

fn cmp_ci(a: &str, b: &str) -> std::cmp::Ordering {
    a.bytes().map(|c| c.to_ascii_lowercase()).cmp(b.bytes().map(|c| c.to_ascii_lowercase()))
}

impl Workspace {
    pub fn recompute_rows(&mut self) {
        let m = self.m.clone();
        let (g, d) = (&m.g, &m.d);
        let t = &mut self.table;
        let q = Query::parse(&t.query);
        let hits = if q.is_empty() { None } else { Some(m.ix.search(g, d, &q)) };
        let ranked = hits.is_some() && t.by_relevance;
        let mut rows: Vec<u32> = match &hits {
            Some(h) => h.iter().map(|x| x.node).collect(),
            None => (0..g.len() as u32).collect(),
        };
        let diff = self.compare.diff.as_ref();
        rows.retain(|&v| {
            let vi = v as usize;
            let kind_ok = match g.kind[vi] {
                Kind::Build => t.kinds[0],
                Kind::Image => t.kinds[1],
                Kind::Make => t.kinds[2],
                Kind::External => t.kinds[3],
            };
            kind_ok
                && t.class.is_none_or(|c| d.class[vi] == c)
                && (t.trig == 0 || d.entry[vi] & t.trig != 0)
                && t.repo.is_none_or(|r| d.repo_of[vi] == r)
                && (!t.changes_only || diff.is_some_and(|df| df.state[vi] != Change::Same))
        });
        if !ranked {
            let (sort, desc) = (t.sort, t.desc);
            let state = |v: u32| diff.map_or(0, |df| df.state[v as usize] as u8);
            rows.sort_by(|&a, &b| {
                let (ai, bi) = (a as usize, b as usize);
                let o = match sort {
                    Col::Name => cmp_ci(g.node_label(a), g.node_label(b)),
                    Col::Repo => cmp_ci(g.node_repo(a), g.node_repo(b)),
                    Col::Stage => d.class[ai].idx().cmp(&d.class[bi].idx()),
                    Col::Needs => d.up[ai].cmp(&d.up[bi]),
                    Col::Breaks => d.down[ai].cmp(&d.down[bi]),
                    Col::Change => state(a).cmp(&state(b)),
                };
                (if desc { o.reverse() } else { o }).then(cmp_ci(g.node_label(a), g.node_label(b)))
            });
        }
        t.max_down = rows.iter().map(|&v| d.down[v as usize]).max().unwrap_or(1).max(1);
        t.total = g.len();
        t.rows = rows;
        t.dirty = false;
    }

    pub fn show_table(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        if self.table.dirty {
            self.recompute_rows();
        }
        let m = self.m.clone();
        let (g, d) = (&m.g, &m.d);
        let ctx = ui.ctx().clone();

        // keyboard: move through the rows
        if !ctx.wants_keyboard_input() && !self.palette.open {
            let (down, up, page_d, page_u) = ctx.input(|i| {
                (i.key_pressed(Key::ArrowDown), i.key_pressed(Key::ArrowUp), i.key_pressed(Key::PageDown), i.key_pressed(Key::PageUp))
            });
            let step: isize = if down {
                1
            } else if up {
                -1
            } else if page_d {
                10
            } else if page_u {
                -10
            } else {
                0
            };
            if step != 0 && !self.table.rows.is_empty() {
                let cur = self.sel.and_then(|s| self.table.rows.iter().position(|&r| r == s));
                let next = match cur {
                    Some(i) => (i as isize + step).clamp(0, self.table.rows.len() as isize - 1) as usize,
                    None => 0,
                };
                let v = self.table.rows[next];
                self.select(Some(v));
                self.table.reveal = Some(v);
            }
        }

        egui::Frame::new().inner_margin(egui::Margin::symmetric(16, 12)).show(ui, |ui| {
            ui.horizontal(|ui| {
                let te = egui::TextEdit::singleline(&mut self.table.query)
                    .hint_text("Filter jobs, repos, scripts, parameters…   try  repo:orders class:deploy trig:commit   ( / )")
                    .desired_width(520.0)
                    .margin(egui::Margin::symmetric(10, 6));
                let r = ui.add(te);
                if self.table.focus_filter {
                    r.request_focus();
                    self.table.focus_filter = false;
                }
                if r.changed() {
                    self.table.by_relevance = true;
                    self.table.dirty = true;
                }
                if !self.table.query.is_empty() && ui.small_button("×").clicked() {
                    self.table.query.clear();
                    self.table.dirty = true;
                }
                ui.label(
                    RichText::new(format!(
                        "{} of {}",
                        super::util::num(self.table.rows.len() as u32),
                        super::util::num(self.table.total as u32)
                    ))
                    .color(pal.muted),
                );
            });
            ui.add_space(6.0);
            ui.horizontal_wrapped(|ui| {
                let mut dirty = false;
                for (i, name) in ["Jobs", "Images", "Make targets", "External"].iter().enumerate() {
                    if ui.selectable_label(self.table.kinds[i], *name).clicked() {
                        self.table.kinds[i] = !self.table.kinds[i];
                        dirty = true;
                    }
                }
                ui.separator();
                for (bit, name) in
                    [(ENTRY_VCS, "on commit"), (ENTRY_SCHEDULE, "scheduled"), (ENTRY_CHAINED, "chained"), (ENTRY_MANUAL, "manual")]
                {
                    if ui.selectable_label(self.table.trig & bit != 0, name).clicked() {
                        self.table.trig ^= bit;
                        dirty = true;
                    }
                }
                ui.separator();
                egui::ComboBox::from_id_salt("stage")
                    .selected_text(self.table.class.map_or("any stage".to_string(), |c| c.name().to_string()))
                    .show_ui(ui, |ui| {
                        if ui.selectable_label(self.table.class.is_none(), "any stage").clicked() {
                            self.table.class = None;
                            dirty = true;
                        }
                        for c in Class::STAGES {
                            let n = d.totals.classes[c.idx()];
                            if ui.selectable_label(self.table.class == Some(c), format!("{}  ({n})", c.name())).clicked() {
                                self.table.class = Some(c);
                                dirty = true;
                            }
                        }
                    });
                egui::ComboBox::from_id_salt("repo")
                    .selected_text(self.table.repo.map_or("any repo".to_string(), |r| g.s(g.repos[r as usize].name).to_string()))
                    .show_ui(ui, |ui| {
                        if ui.selectable_label(self.table.repo.is_none(), "any repo").clicked() {
                            self.table.repo = None;
                            dirty = true;
                        }
                        for (i, r) in g.repos.iter().enumerate() {
                            if ui.selectable_label(self.table.repo == Some(i as u32), g.s(r.name)).clicked() {
                                self.table.repo = Some(i as u32);
                                dirty = true;
                            }
                        }
                    });
                if self.compare.diff.is_some() {
                    ui.separator();
                    if ui.selectable_label(self.table.changes_only, "only changes").clicked() {
                        self.table.changes_only = !self.table.changes_only;
                        dirty = true;
                    }
                }
                if dirty {
                    self.table.dirty = true;
                }
            });
        });
        if self.table.dirty {
            self.recompute_rows();
        }

        // make sure a revealed row is visible: drop filters that hide it
        let mut scroll_to = None;
        if let Some(v) = self.table.reveal.take() {
            match self.table.rows.iter().position(|&r| r == v) {
                Some(i) => scroll_to = Some(i),
                None => {
                    self.table.query.clear();
                    self.table.kinds = [true, true, true, true];
                    self.table.class = None;
                    self.table.trig = 0;
                    self.table.repo = None;
                    self.table.changes_only = false;
                    self.recompute_rows();
                    scroll_to = self.table.rows.iter().position(|&r| r == v);
                }
            }
        }

        let show_change = self.compare.diff.is_some() && self.compare.overlay;
        let rows = std::mem::take(&mut self.table.rows);
        let max_down = self.table.max_down as f32;
        let (sort, desc) = (self.table.sort, self.table.desc);
        let mut clicked: Option<u32> = None;
        let mut focus: Option<u32> = None;
        let mut new_sort: Option<Col> = None;
        let diff = self.compare.diff.clone();
        let sel = self.sel;

        egui::Frame::new().inner_margin(egui::Margin::symmetric(16, 0)).show(ui, |ui| {
            // A whole row is the click target; a selectable label would swallow the click.
            // Right-click a row to copy what is in it.
            ui.style_mut().interaction.selectable_labels = false;
            let mut tb = TableBuilder::new(ui)
                .striped(false)
                .resizable(true)
                .sense(Sense::click())
                .cell_layout(egui::Layout::left_to_right(egui::Align::Center))
                .column(Column::initial(250.0).at_least(160.0).clip(true))
                .column(Column::initial(120.0).at_least(70.0).clip(true))
                .column(Column::initial(72.0).at_least(56.0))
                .column(Column::initial(190.0).at_least(80.0).clip(true))
                .column(Column::initial(230.0).at_least(100.0).clip(true))
                .column(Column::initial(56.0).at_least(48.0))
                .column(if show_change { Column::initial(110.0).at_least(80.0) } else { Column::remainder().at_least(100.0) });
            if show_change {
                tb = tb.column(Column::remainder().at_least(70.0));
            }
            if let Some(i) = scroll_to {
                tb = tb.scroll_to_row(i, Some(egui::Align::Center));
            }
            let heads = [
                ("NAME", Col::Name),
                ("REPO", Col::Repo),
                ("STAGE", Col::Stage),
                ("TRIGGERS", Col::Name),
                ("IMAGES", Col::Name),
                ("NEEDS", Col::Needs),
                ("BREAKS", Col::Breaks),
                ("CHANGE", Col::Change),
            ];
            tb.header(26.0, |mut h| {
                for (i, (name, col)) in heads.iter().enumerate() {
                    if i == 7 && !show_change {
                        break;
                    }
                    h.col(|ui| {
                        let sortable = i != 3 && i != 4;
                        let arrow = if sortable && *col == sort {
                            if desc {
                                " ▼"
                            } else {
                                " ▲"
                            }
                        } else {
                            ""
                        };
                        let text = RichText::new(format!("{name}{arrow}")).small().strong().color(pal.muted);
                        let r = if sortable { util::link(ui, text) } else { ui.label(text) };
                        let (title, body) = HEAD_TIPS[i];
                        if util::hint(r, title, body).clicked() && sortable {
                            new_sort = Some(*col);
                        }
                    });
                }
            })
            .body(|body| {
                body.rows(42.0, rows.len(), |mut row| {
                    let v = rows[row.index()];
                    let vi = v as usize;
                    row.set_selected(sel == Some(v));
                    row.col(|ui| {
                        ui.vertical(|ui| {
                            ui.spacing_mut().item_spacing.y = 0.0;
                            ui.horizontal(|ui| {
                                ui.add(egui::Label::new(RichText::new(g.node_label(v)).strong()).truncate());
                                let tag = match g.kind[vi] {
                                    Kind::Image => Some("image"),
                                    Kind::Make => Some("make"),
                                    Kind::External => Some("external"),
                                    Kind::Build if g.composite[vi] => Some("composite"),
                                    _ => None,
                                };
                                if let Some(t) = tag {
                                    ui.label(RichText::new(t).small().color(theme::kind_color(g.kind[vi], g.composite[vi], pal.dark)));
                                }
                            });
                            ui.add(egui::Label::new(RichText::new(g.s(g.sub[vi])).small().color(pal.muted)).truncate());
                        });
                    });
                    row.col(|ui| {
                        ui.add(egui::Label::new(g.node_repo(v)).truncate());
                    });
                    row.col(|ui| {
                        let c = d.class[vi];
                        if c != Class::None {
                            ui.label(
                                RichText::new(format!(" {} ", c.name()))
                                    .small()
                                    .color(Color32::WHITE)
                                    .background_color(theme::class_color(c, pal.dark)),
                            );
                        }
                    });
                    row.col(|ui| {
                        ui.add(egui::Label::new(RichText::new(trigger_text(m.as_ref(), v)).small().color(pal.muted)).truncate());
                    });
                    row.col(|ui| {
                        ui.add(egui::Label::new(RichText::new(image_text(m.as_ref(), v)).small().color(pal.muted)).truncate());
                    });
                    row.col(|ui| {
                        ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                            if d.up[vi] > 0 {
                                ui.label(RichText::new(d.up[vi].to_string()).color(pal.up));
                            }
                        });
                    });
                    row.col(|ui| {
                        let n = d.down[vi];
                        if n > 0 {
                            ui.horizontal(|ui| {
                                let w = ((n as f32 / max_down).sqrt() * 58.0).max(3.0);
                                let (bar, _) = ui.allocate_exact_size(egui::vec2(w, 6.0), Sense::hover());
                                ui.painter().rect_filled(bar, 3, pal.down);
                                ui.label(n.to_string());
                            });
                        }
                    });
                    if show_change {
                        row.col(|ui| {
                            if let Some(df) = &diff {
                                match df.state[vi] {
                                    Change::Added => ui.label(RichText::new("new").color(pal.added).strong()),
                                    Change::Changed => ui.label(RichText::new("changed").color(pal.changed).strong()),
                                    Change::Same => ui.label(""),
                                };
                            }
                        });
                    }
                    let r = row.response().on_hover_cursor(egui::CursorIcon::PointingHand);
                    if r.clicked() {
                        clicked = Some(v);
                    }
                    r.context_menu(|ui| self.node_menu(ui, v));
                    if r.double_clicked() {
                        focus = Some(v);
                    }
                });
            });
        });
        self.table.rows = rows;
        if let Some(c) = new_sort {
            if self.table.sort == c && !self.table.by_relevance {
                self.table.desc = !self.table.desc;
            } else {
                self.table.sort = c;
                self.table.desc = matches!(c, Col::Needs | Col::Breaks | Col::Change);
            }
            self.table.by_relevance = false;
            self.table.dirty = true;
        }
        if let Some(v) = clicked {
            self.select(Some(v));
        }
        if let Some(v) = focus {
            self.open_focus(v);
        }
    }
}

pub fn trigger_text(m: &crate::core::model::Model, v: u32) -> String {
    let g = &m.g;
    let mut parts: Vec<String> = vec![];
    for t in g.triggers(v) {
        match g.s(t.ty) {
            "vcs" => parts.push("commit".into()),
            "schedule" => parts.push(format!("scheduled {}", g.s(t.detail))),
            "finishBuild" => {}
            other => parts.push(other.to_string()),
        }
    }
    let from: Vec<&str> = g
        .flow
        .inn
        .es(v)
        .iter()
        .filter(|&&e| g.flow.kind[e as usize] == Ek::Trigger)
        .map(|&e| g.node_label(g.flow.from[e as usize]))
        .collect();
    if !from.is_empty() {
        let more = if from.len() > 2 { format!(" +{}", from.len() - 2) } else { String::new() };
        parts.push(format!("after {}{more}", from[..from.len().min(2)].join(", ")));
    }
    parts.join(" · ")
}

pub fn image_text(m: &crate::core::model::Model, v: u32) -> String {
    let g = &m.g;
    let mut parts: Vec<String> = vec![];
    let label = |n: u32| g.node_label(n).to_string();
    let mut add = |verb: &str, nodes: Vec<u32>| {
        for n in nodes.iter().take(3) {
            parts.push(format!("{verb} {}", label(*n)));
        }
    };
    let by = |adj: &[u32], net: &crate::core::graph::Net, k: Ek, other_from: bool| -> Vec<u32> {
        adj.iter()
            .filter(|&&e| net.kind[e as usize] == k)
            .map(|&e| if other_from { net.from[e as usize] } else { net.to[e as usize] })
            .collect()
    };
    add("builds", by(g.flow.out.es(v), &g.flow, Ek::Produces, false));
    add("uses", by(g.flow.inn.es(v), &g.flow, Ek::Consumes, true));
    add("in", by(g.flow.inn.es(v), &g.flow, Ek::RunsIn, true));
    if g.kind[v as usize] == Kind::Image {
        add("from", by(g.flow.inn.es(v), &g.flow, Ek::Derives, true));
    }
    parts.join(" · ")
}

/// What each column says, for the header hover cards.
const HEAD_TIPS: [(&str, &str); 8] = [
    ("Name", "The job, image or make target, with its CI id or image reference below. Click to sort."),
    ("Repository", "Where it is defined. Click to sort."),
    ("Stage", "What the job does, read from its name and steps: build, lint, test, scan, package, publish, deploy. Click to sort."),
    ("Triggers", "What starts it: a commit, a schedule, another job finishing. Empty means it only runs as a dependency or by hand."),
    ("Images", "Images it builds, pushes or runs its steps in."),
    (
        "Needs (upstream)",
        "How many jobs and images must run before this one gets fresh input. A long tail here is a slow or fragile path to this job. Click to sort.",
    ),
    (
        "Breaks (downstream)",
        "How many jobs and images would not get fresh input if this one failed: its blast radius. Click to sort.",
    ),
    ("Change", "New or changed since the compared snapshot. Click to sort."),
];
