//! The right-hand panel: everything known about the selected node, in the
//! order a reviewer asks: what it is, what triggers it, what it runs, where.

use super::app::{LineageMode, Workspace};
use super::theme::{self, Palette};
use super::util;
use crate::core::derive::{classify, Class};
use crate::core::diff::Change;
use crate::core::graph::{Ek, Kind};
use crate::core::model::Model;
use egui::{Color32, RichText};

#[derive(Default)]
struct Acts {
    jump: Option<u32>,
    focus: Option<u32>,
    copy: Option<(String, &'static str)>,
    url: Option<String>,
    mode: Option<LineageMode>,
    route: bool,
}

fn link(ui: &mut egui::Ui, m: &Model, pal: &Palette, v: u32, acts: &mut Acts) {
    let g = &m.g;
    ui.horizontal(|ui| {
        let r = util::link(ui, RichText::new(g.node_label(v)).color(pal.accent));
        if r.clicked() {
            acts.jump = Some(v);
        }
        if r.double_clicked() {
            acts.focus = Some(v);
        }
        r.on_hover_text(g.node_id(v));
        let repo = g.node_repo(v);
        if !repo.is_empty() {
            ui.label(RichText::new(repo).small().color(pal.muted));
        }
    });
}

fn copy_btn(ui: &mut egui::Ui, acts: &mut Acts, text: &str, what: &'static str) {
    if ui.add(egui::Button::new(RichText::new("Copy").small()).small().frame(true)).on_hover_text("Copy to clipboard").clicked() {
        acts.copy = Some((text.to_string(), what));
    }
}

fn heading(ui: &mut egui::Ui, pal: &Palette, text: &str, n: Option<usize>, color: Option<Color32>) {
    ui.add_space(8.0);
    ui.horizontal(|ui| {
        if let Some(c) = color {
            let (r, _) = ui.allocate_exact_size(egui::vec2(10.0, 10.0), egui::Sense::hover());
            ui.painter().rect_filled(r, 3, c);
        }
        ui.label(RichText::new(text.to_uppercase()).small().strong().color(pal.muted));
        if let Some(n) = n {
            ui.label(RichText::new(format!("({n})")).small().color(pal.muted));
        }
    });
}

fn code(ui: &mut egui::Ui, pal: &Palette, text: &str) {
    egui::Frame::new()
        .fill(if pal.dark { Color32::from_rgb(0x0E, 0x10, 0x14) } else { Color32::from_rgb(0x1A, 0x1D, 0x24) })
        .corner_radius(8)
        .inner_margin(egui::Margin::symmetric(10, 8))
        .show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.add(egui::Label::new(RichText::new(text.trim_end()).monospace().color(Color32::from_rgb(0xE5, 0xE7, 0xEB))).wrap());
        });
}

/// One level of the make tree, built lazily: closed branches cost nothing.
fn make_tree(ui: &mut egui::Ui, m: &Model, pal: &Palette, v: u32, depth: usize, acts: &mut Acts, path: &mut Vec<u32>) {
    let g = &m.g;
    let kids: Vec<u32> = g.full.out.es(v).iter().filter(|&&e| g.full.kind[e as usize].is_make()).map(|&e| g.full.to[e as usize]).collect();
    let recipe = g.steps(v).first().map(|s| util::first_line(g.s(s.summary), 70)).unwrap_or_default();
    let undefined = g.s(g.sub[v as usize]).ends_with("(no rule)");
    let title = RichText::new(format!("make {}", g.node_label(v))).monospace();
    if kids.is_empty() || depth >= 8 || path.contains(&v) {
        ui.horizontal(|ui| {
            ui.add_space(14.0);
            if util::link(ui, title.color(pal.accent)).clicked() {
                acts.jump = Some(v);
            }
            if undefined {
                ui.label(RichText::new("undefined").small().color(pal.warn));
            } else if !recipe.is_empty() {
                ui.label(RichText::new(recipe).small().color(pal.muted));
            }
        });
        return;
    }
    path.push(v);
    let id = ui.make_persistent_id(("mk", v, depth, path.len()));
    egui::collapsing_header::CollapsingState::load_with_default_open(ui.ctx(), id, depth < 1)
        .show_header(ui, |ui| {
            if util::link(ui, title.color(pal.accent)).clicked() {
                acts.jump = Some(v);
            }
            if !recipe.is_empty() {
                ui.label(RichText::new(recipe).small().color(pal.muted));
            }
        })
        .body(|ui| {
            for k in kids.iter().take(60) {
                make_tree(ui, m, pal, *k, depth + 1, acts, path);
            }
            if kids.len() > 60 {
                ui.label(RichText::new(format!("… {} more", kids.len() - 60)).small().color(pal.muted));
            }
        });
    path.pop();
}

impl Workspace {
    pub fn show_inspector(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        let m = self.m.clone();
        let mut acts = Acts::default();
        let Some(v) = self.sel else {
            ui.add_space(30.0);
            ui.vertical_centered(|ui| {
                ui.label(RichText::new("Nothing selected").heading().color(pal.muted));
                ui.add_space(6.0);
                ui.label(RichText::new("Pick a job, image or make target to see what triggers it, what it runs, the image it runs in, and what breaks if it does.").color(pal.muted));
            });
            return;
        };
        let (g, d) = (&m.g, &m.d);
        let vi = v as usize;
        egui::ScrollArea::vertical().auto_shrink([false, false]).show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.horizontal_wrapped(|ui| {
                ui.label(RichText::new(g.node_label(v)).heading().strong());
            });
            ui.horizontal_wrapped(|ui| {
                let k = theme::kind_color(g.kind[vi], g.composite[vi], pal.dark);
                ui.label(RichText::new(format!(" {} ", g.kind_name(v))).small().color(k).background_color(theme::with_alpha(k, 36)));
                let c = d.class[vi];
                if c != Class::None {
                    let reason = classify(g, v).1;
                    ui.label(
                        RichText::new(format!(" {} ", c.name()))
                            .small()
                            .color(Color32::WHITE)
                            .background_color(theme::class_color(c, pal.dark)),
                    )
                    .on_hover_text(format!("Stage “{}”: {reason}", c.name()));
                }
                ui.label(RichText::new(g.node_repo(v)).color(pal.muted));
                let p = g.s(g.project[vi]);
                if !p.is_empty() {
                    ui.label(RichText::new(format!("· {p}")).color(pal.muted));
                }
            });
            ui.add_space(6.0);
            egui::Grid::new("meta").num_columns(2).spacing([10.0, 4.0]).show(ui, |ui| {
                let sub = g.s(g.sub[vi]);
                if !sub.is_empty() {
                    ui.label(RichText::new(if g.kind[vi] == Kind::Build { g.id_name(v) } else { "Where".to_string() }).color(pal.muted));
                    ui.horizontal(|ui| {
                        ui.label(RichText::new(sub).monospace());
                        copy_btn(ui, &mut acts, sub, "Id");
                    });
                    ui.end_row();
                }
                if let Some(sys) = g.system_of(v).filter(|x| !g.s(x.title).is_empty()) {
                    ui.label(RichText::new("System").color(pal.muted));
                    ui.label(g.s(sys.title));
                    ui.end_row();
                }
                let src = g.s(g.source[vi]);
                if !src.is_empty() {
                    ui.label(RichText::new("Source").color(pal.muted));
                    ui.horizontal(|ui| {
                        let repo = g.repos.iter().find(|r| r.name == g.repo[vi]);
                        let url = repo.and_then(|r| util::source_url(g.s(r.url), g.s(r.commit), src));
                        match url {
                            Some(u) => {
                                // the hover shows the address itself: what is opened is never a surprise
                                if ui.link(RichText::new(src).monospace()).on_hover_text(format!("Open {u}")).clicked() {
                                    acts.url = Some(u);
                                }
                            }
                            None => {
                                ui.label(RichText::new(src).monospace());
                            }
                        }
                        copy_btn(ui, &mut acts, src, "Source");
                    });
                    ui.end_row();
                }
                if let Some(r) = g.repos.iter().find(|r| r.name == g.repo[vi]) {
                    if !g.s(r.commit).is_empty() {
                        ui.label(RichText::new("Commit").color(pal.muted));
                        ui.horizontal(|ui| match util::commit_url(g.s(r.url), g.s(r.commit)) {
                            Some(u) => {
                                if ui.link(RichText::new(util::short(g.s(r.commit))).monospace()).on_hover_text(format!("Open {u}")).clicked() {
                                    acts.url = Some(u);
                                }
                            }
                            None => {
                                ui.label(RichText::new(util::short(g.s(r.commit))).monospace());
                            }
                        });
                        ui.end_row();
                    }
                }
            });
            ui.add_space(6.0);
            ui.horizontal_wrapped(|ui| {
                if ui.button("Copy link").clicked() {
                    acts.copy = Some((String::new(), "Link"));
                }
                if ui.button("Lineage").on_hover_text("Open this lineage on its own (Enter)").clicked() {
                    acts.focus = Some(v);
                }
                if ui
                    .button("Route to…")
                    .on_hover_text("Shift+click a second job on the map to keep only the route between the two")
                    .clicked()
                {
                    acts.route = true;
                }
            });
            ui.add_space(4.0);
            ui.horizontal_wrapped(|ui| {
                let (up, down) = (d.up[vi], d.down[vi]);
                for (n, label, col, mode, tip) in [
                    (
                        down,
                        "breaks",
                        pal.down,
                        LineageMode::Down,
                        "Jobs and images downstream that go stale if this one fails: its blast radius. Click to keep only them on the map (B).",
                    ),
                    (
                        up,
                        "needs",
                        pal.up,
                        LineageMode::Up,
                        "Jobs and images upstream that must run first for this one to get fresh input. Click to keep only them on the map (D).",
                    ),
                ] {
                    let b = egui::Button::new(RichText::new(format!("{} {label}", util::num(n))).color(col))
                        .stroke(super::theme::stroke(1.0, col))
                        .fill(theme::with_alpha(col, 22));
                    if util::hint(ui.add(b), &format!("{} {label}", util::num(n)), tip).clicked() {
                        acts.mode = Some(mode);
                    }
                }
            });

            if let (true, Some(df)) = (self.compare.overlay, self.compare.diff.as_ref()) {
                let st = df.state[vi];
                if st != Change::Same {
                    heading(
                        ui,
                        pal,
                        if st == Change::Added { "New since the compared snapshot" } else { "Changed since the compared snapshot" },
                        None,
                        Some(if st == Change::Added { pal.added } else { pal.changed }),
                    );
                    for n in df.notes.get(&v).into_iter().flatten() {
                        ui.label(RichText::new(format!("• {n}")).small());
                    }
                }
            }

            // triggers
            let trig = g.triggers(v);
            let chained: Vec<u32> =
                g.flow.inn.es(v).iter().filter(|&&e| g.flow.kind[e as usize] == Ek::Trigger).map(|&e| g.flow.from[e as usize]).collect();
            if g.kind[vi] == Kind::Build {
                heading(ui, pal, "Triggers", Some(trig.len().max(chained.len())), None);
                if trig.is_empty() && chained.is_empty() {
                    ui.label(RichText::new("No trigger of its own: it only runs as a dependency or by hand.").small().color(pal.muted));
                }
                for t in trig {
                    if g.s(t.ty) == "finishBuild" {
                        continue;
                    }
                    ui.horizontal(|ui| {
                        ui.label(RichText::new(g.s(t.ty)).strong());
                        ui.label(RichText::new(g.s(t.detail)).monospace().small().color(pal.muted));
                    });
                }
                for c in &chained {
                    ui.horizontal(|ui| {
                        ui.label(RichText::new("after").strong());
                        link(ui, &m, pal, *c, &mut acts);
                    });
                }
            }

            // container
            let mut cont: Vec<u32> = vec![];
            for &e in g.full.inn.es(v) {
                if g.full.kind[e as usize] == Ek::RunsIn {
                    cont.push(g.full.from[e as usize]);
                }
            }
            if g.kind[vi] != Kind::Image && !cont.is_empty() {
                heading(ui, pal, "Runs in container", Some(cont.len()), None);
                for c in cont {
                    link(ui, &m, pal, c, &mut acts);
                    let mut chain = vec![];
                    let mut cur = c;
                    while let Some(&e) = g.flow.inn.es(cur).iter().find(|&&e| g.flow.kind[e as usize] == Ek::Derives) {
                        cur = g.flow.from[e as usize];
                        chain.push(cur);
                        if chain.len() > 8 {
                            break;
                        }
                    }
                    for b in chain {
                        ui.horizontal(|ui| {
                            ui.add_space(14.0);
                            ui.label(RichText::new("built on").small().color(pal.muted));
                            link(ui, &m, pal, b, &mut acts);
                        });
                    }
                }
            }
            if g.kind[vi] == Kind::Image {
                if let Some((_, base)) = g.params(v).iter().find(|(k, _)| g.s(*k) == "from") {
                    heading(ui, pal, "Built from (third-party)", None, None);
                    ui.label(RichText::new(g.s(*base)).monospace());
                }
            }

            // relations
            let net = g.net_for(v);
            relations(ui, &m, pal, v, net, false, &mut acts);
            relations(ui, &m, pal, v, net, true, &mut acts);

            // the make tree a job runs
            if g.kind[vi] == Kind::Build {
                let roots: Vec<u32> =
                    g.full.out.es(v).iter().filter(|&&e| g.full.kind[e as usize] == Ek::Runs).map(|&e| g.full.to[e as usize]).collect();
                if !roots.is_empty() {
                    heading(ui, pal, "Make targets it runs", Some(roots.len()), None);
                    let mut path = vec![];
                    for r in roots {
                        make_tree(ui, &m, pal, r, 0, &mut acts, &mut path);
                    }
                }
            }

            // steps
            let steps = g.steps(v);
            if !steps.is_empty() {
                heading(ui, pal, if g.kind[vi] == Kind::Make { "Recipe" } else { "Steps" }, Some(steps.len()), None);
                for (i, s) in steps.iter().enumerate() {
                    ui.horizontal(|ui| {
                        let name = g.s(s.name);
                        ui.label(RichText::new(format!("{}. {}", i + 1, if name.is_empty() { g.s(s.summary) } else { name })).strong());
                        if !g.s(s.ty).is_empty() {
                            ui.label(RichText::new(g.s(s.ty)).small().color(pal.muted));
                        }
                        if !g.s(s.workdir).is_empty() {
                            ui.label(RichText::new(format!("in {}", g.s(s.workdir))).small().monospace().color(pal.muted));
                        }
                    });
                    let script = g.s(s.script);
                    if !script.is_empty() {
                        code(ui, pal, script);
                        copy_btn(ui, &mut acts, script, "Script");
                    } else if !g.s(s.summary).is_empty() {
                        code(ui, pal, g.s(s.summary));
                    }
                }
            }
            let params = g.params(v);
            let params: Vec<_> = params.iter().filter(|(k, _)| g.s(*k) != "from").collect();
            if !params.is_empty() {
                heading(ui, pal, "Parameters", Some(params.len()), None);
                egui::Grid::new("params").num_columns(2).spacing([14.0, 3.0]).striped(true).show(ui, |ui| {
                    for (k, val) in params {
                        ui.label(RichText::new(g.s(*k)).monospace().small());
                        ui.label(RichText::new(g.s(*val)).monospace().small());
                        ui.end_row();
                    }
                });
            }
            let arts = g.artifacts(v);
            if !arts.is_empty() {
                heading(ui, pal, "Artifact rules", Some(arts.len()), None);
                for a in arts {
                    ui.label(RichText::new(g.s(*a)).monospace().small());
                }
            }
        });

        // apply what was asked for
        if let Some((text, what)) = acts.copy.take() {
            let text = if what == "Link" { self.link() } else { text };
            let ctx = ui.ctx().clone();
            self.copy(&ctx, text, what);
        }
        if let Some(u) = acts.url.take() {
            ui.ctx().open_url(egui::OpenUrl::new_tab(u));
        }
        if let Some(mode) = acts.mode {
            self.set_mode(mode);
            if self.screen != super::app::Screen::Map && self.screen != super::app::Screen::Prod {
                self.go(super::app::Screen::Map);
                self.scene = crate::core::scene::Scene::Focus(v);
            }
        }
        if let Some(f) = acts.focus {
            self.open_focus(f);
        } else if let Some(j) = acts.jump {
            self.open_node(j);
        }
        if acts.route {
            self.toast("Shift+click another job on the map");
            if self.screen != super::app::Screen::Map && self.screen != super::app::Screen::Prod {
                self.open_focus(v);
            }
        }
    }
}

/// Direct neighbours, grouped by the kind of link.
fn relations(ui: &mut egui::Ui, m: &Model, pal: &Palette, v: u32, net: &crate::core::graph::Net, downstream: bool, acts: &mut Acts) {
    let g = &m.g;
    let adj = net.adj(downstream);
    let es = adj.es(v);
    // make internals are shown as the make tree on builds
    let skip_make = g.kind[v as usize] == Kind::Build;
    let mut groups: Vec<(Ek, Vec<u32>)> = vec![];
    for &e in es {
        let k = net.kind[e as usize];
        if skip_make && k.is_make() {
            continue;
        }
        let other = if downstream { net.to[e as usize] } else { net.from[e as usize] };
        match groups.iter_mut().find(|(gk, _)| *gk == k) {
            Some((_, l)) => {
                if !l.contains(&other) {
                    l.push(other)
                }
            }
            None => groups.push((k, vec![other])),
        }
    }
    let n: usize = groups.iter().map(|(_, l)| l.len()).sum();
    if n == 0 {
        return;
    }
    heading(
        ui,
        pal,
        if downstream { "Directly needed by" } else { "Directly needs" },
        Some(n),
        Some(if downstream { pal.down } else { pal.up }),
    );
    groups.sort_by_key(|(k, _)| *k);
    for (k, list) in groups {
        let st = theme::edge_style(k, pal.dark);
        ui.horizontal(|ui| {
            let (r, _) = ui.allocate_exact_size(egui::vec2(26.0, 10.0), egui::Sense::hover());
            ui.painter().line_segment([r.left_center(), r.right_center()], super::theme::stroke(st.width.max(1.5), st.color));
            ui.label(RichText::new(k.name()).small().color(pal.muted));
        });
        for o in list.iter().take(40) {
            ui.horizontal(|ui| {
                ui.add_space(10.0);
                link(ui, m, pal, *o, acts);
            });
        }
        if list.len() > 40 {
            ui.label(RichText::new(format!("   … {} more", list.len() - 40)).small().color(pal.muted));
        }
    }
}
