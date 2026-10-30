//! The command palette: jump to any node, or run any command, from the keyboard.

use super::app::{LineageMode, Screen, Workspace};
use super::canvas::Scene;
use super::theme::Palette;
use crate::core::search::{fuzzy_rank, Query};
use egui::{Key, Modifiers, RichText, Sense};

#[derive(Default)]
pub struct PaletteState {
    pub open: bool,
    pub query: String,
    cursor: usize,
    fresh: bool,
}

impl PaletteState {
    pub fn toggle(&mut self) {
        self.open = !self.open;
        if self.open {
            self.query.clear();
            self.cursor = 0;
            self.fresh = true;
        }
    }
}

#[derive(Clone)]
enum Cmd {
    Screen(Screen),
    Theme,
    Fit,
    CopyLink,
    Reload,
    Warnings,
    Help,
    ClearSelection,
    Mode(LineageMode),
    Focus,
    Scene(Scene),
    Snapshot(usize),
}

#[derive(Clone)]
enum Item {
    Node(u32),
    Cmd(String, Cmd),
}

impl Workspace {
    fn commands(&self) -> Vec<(String, Cmd)> {
        let mut c: Vec<(String, Cmd)> = Screen::ALL.iter().map(|&s| (format!("Go to {}", s.title()), Cmd::Screen(s))).collect();
        c.push(("Map: repositories".into(), Cmd::Scene(Scene::Repos)));
        c.push(("Map: all jobs".into(), Cmd::Scene(Scene::All)));
        c.push(("Fit the map to the window".into(), Cmd::Fit));
        c.push(("Copy a link to this view".into(), Cmd::CopyLink));
        c.push(("Toggle light / dark".into(), Cmd::Theme));
        c.push(("Reload graph.json".into(), Cmd::Reload));
        c.push(("Show analysis warnings".into(), Cmd::Warnings));
        c.push(("Keyboard shortcuts".into(), Cmd::Help));
        if self.sel.is_some() {
            c.push(("Open the selection's lineage on its own".into(), Cmd::Focus));
            c.push(("Keep only what the selection breaks".into(), Cmd::Mode(LineageMode::Down)));
            c.push(("Keep only what the selection needs".into(), Cmd::Mode(LineageMode::Up)));
            c.push(("Keep the selection's whole lineage".into(), Cmd::Mode(LineageMode::Both)));
            c.push(("Clear the selection".into(), Cmd::ClearSelection));
        }
        for (i, (label, _)) in self.compare.snapshots.iter().enumerate().take(8) {
            c.push((format!("Compare with {label}"), Cmd::Snapshot(i)));
        }
        c
    }

    fn run(&mut self, ctx: &egui::Context, cmd: Cmd, mode_toggle: &mut bool) {
        match cmd {
            Cmd::Screen(s) => self.go(s),
            Cmd::Theme => *mode_toggle = true,
            Cmd::Fit => self.canvas.fit_all = true,
            Cmd::CopyLink => {
                let l = self.link();
                self.copy(ctx, l, "Link");
            }
            Cmd::Reload => self.start_reload(ctx),
            Cmd::Warnings => self.show_warnings = true,
            Cmd::Help => self.show_help = true,
            Cmd::ClearSelection => self.select(None),
            Cmd::Mode(m) => {
                self.set_mode(m);
                if self.screen != Screen::Map && self.screen != Screen::Prod {
                    if let Some(v) = self.sel {
                        self.open_focus(v);
                    }
                }
            }
            Cmd::Focus => {
                if let Some(v) = self.sel {
                    self.open_focus(v);
                }
            }
            Cmd::Scene(s) => {
                self.scene = s;
                self.screen = Screen::Map;
                self.canvas.fit_pending = true;
            }
            Cmd::Snapshot(i) => {
                if let Some((_, p)) = self.compare.snapshots.get(i).cloned() {
                    self.start_compare(ctx, p);
                    self.screen = Screen::Compare;
                }
            }
        }
    }

    pub fn show_palette(&mut self, ctx: &egui::Context, pal: &Palette) {
        if !self.palette.open {
            return;
        }
        let m = self.m.clone();
        let (g, d) = (&m.g, &m.d);
        let cmds_only = self.palette.query.starts_with('>');
        let text = self.palette.query.trim_start_matches('>').trim().to_string();
        let mut items: Vec<Item> = vec![];
        if !cmds_only && !text.is_empty() {
            let q = Query::parse(&text);
            for h in m.ix.search(g, d, &q).into_iter().take(40) {
                items.push(Item::Node(h.node));
            }
        }
        let cmds = self.commands();
        if text.is_empty() {
            items.extend(cmds.iter().map(|(l, c)| Item::Cmd(l.clone(), c.clone())));
        } else {
            let ranked = fuzzy_rank(&text, cmds.iter().enumerate().map(|(i, (l, _))| (i, l.as_str())));
            let cap = if cmds_only { 20 } else { 5 };
            for (i, _) in ranked.into_iter().take(cap) {
                items.push(Item::Cmd(cmds[i].0.clone(), cmds[i].1.clone()));
            }
        }
        // keys
        let (down, up, enter, shift_enter, esc) = ctx.input_mut(|i| {
            (
                i.consume_key(Modifiers::NONE, Key::ArrowDown),
                i.consume_key(Modifiers::NONE, Key::ArrowUp),
                i.consume_key(Modifiers::NONE, Key::Enter),
                i.consume_key(Modifiers::SHIFT, Key::Enter),
                i.consume_key(Modifiers::NONE, Key::Escape),
            )
        });
        if down && !items.is_empty() {
            self.palette.cursor = (self.palette.cursor + 1) % items.len();
        }
        if up && !items.is_empty() {
            self.palette.cursor = (self.palette.cursor + items.len() - 1) % items.len();
        }
        self.palette.cursor = self.palette.cursor.min(items.len().saturating_sub(1));
        let mut chosen: Option<(Item, bool)> = None;
        if enter || shift_enter {
            chosen = items.get(self.palette.cursor).cloned().map(|i| (i, shift_enter));
        }
        if esc {
            self.palette.open = false;
            return;
        }

        egui::Area::new(egui::Id::new("palette-backdrop")).order(egui::Order::Middle).fixed_pos(egui::pos2(0.0, 0.0)).show(ctx, |ui| {
            let r = ctx.screen_rect();
            let (rect, resp) = ui.allocate_exact_size(r.size(), Sense::click());
            ui.painter().rect_filled(rect, 0, egui::Color32::from_black_alpha(if pal.dark { 120 } else { 60 }));
            if resp.clicked() {
                self.palette.open = false;
            }
        });
        egui::Area::new(egui::Id::new("palette")).order(egui::Order::Foreground).anchor(egui::Align2::CENTER_TOP, [0.0, 70.0]).show(
            ctx,
            |ui| {
                egui::Frame::popup(ui.style()).corner_radius(12).inner_margin(12).show(ui, |ui| {
                    ui.set_width(680.0);
                    let te = egui::TextEdit::singleline(&mut self.palette.query)
                        .hint_text("Search jobs, images, scripts, parameters — or type > for commands")
                        .desired_width(f32::INFINITY)
                        .font(egui::TextStyle::Heading)
                        .margin(egui::Margin::symmetric(10, 8));
                    let r = ui.add(te);
                    if self.palette.fresh {
                        r.request_focus();
                        self.palette.fresh = false;
                    }
                    if r.changed() {
                        self.palette.cursor = 0;
                    }
                    ui.add_space(6.0);
                    if items.is_empty() {
                        ui.label(RichText::new(if text.is_empty() { "Nothing to show" } else { "No match" }).color(pal.muted));
                    }
                    egui::ScrollArea::vertical().max_height(420.0).auto_shrink([false, true]).show(ui, |ui| {
                        for (i, it) in items.iter().enumerate() {
                            let active = i == self.palette.cursor;
                            let h = 30.0;
                            let (rect, resp) = ui.allocate_exact_size(egui::vec2(ui.available_width(), h), Sense::click());
                            if active {
                                ui.painter().rect_filled(rect, 6, pal.accent);
                                ui.scroll_to_rect(rect, None);
                            } else if resp.hovered() {
                                ui.painter().rect_filled(rect, 6, super::theme::with_alpha(pal.muted, 40));
                            }
                            let fg = if active { egui::Color32::WHITE } else { pal.text };
                            let sub = if active { egui::Color32::from_white_alpha(190) } else { pal.muted };
                            match it {
                                Item::Node(v) => {
                                    let kind = match g.kind[*v as usize] {
                                        crate::core::graph::Kind::Build => "job",
                                        crate::core::graph::Kind::Image => "image",
                                        crate::core::graph::Kind::Make => "make",
                                        crate::core::graph::Kind::External => "external",
                                    };
                                    ui.painter().text(
                                        rect.left_center() + egui::vec2(10.0, 0.0),
                                        egui::Align2::LEFT_CENTER,
                                        g.node_label(*v),
                                        egui::FontId::proportional(14.0),
                                        fg,
                                    );
                                    ui.painter().text(
                                        rect.right_center() - egui::vec2(10.0, 0.0),
                                        egui::Align2::RIGHT_CENTER,
                                        format!("{kind} · {}", g.node_repo(*v)),
                                        egui::FontId::proportional(12.0),
                                        sub,
                                    );
                                }
                                Item::Cmd(label, _) => {
                                    ui.painter().text(
                                        rect.left_center() + egui::vec2(10.0, 0.0),
                                        egui::Align2::LEFT_CENTER,
                                        format!("›  {label}"),
                                        egui::FontId::proportional(14.0),
                                        fg,
                                    );
                                }
                            }
                            if resp.clicked() {
                                chosen = Some((it.clone(), false));
                            }
                        }
                    });
                    ui.add_space(4.0);
                    ui.label(RichText::new("↑↓ move   ↵ open   ⇧↵ lineage on its own   esc close").small().color(pal.muted));
                });
            },
        );

        if let Some((item, focus)) = chosen {
            self.palette.open = false;
            match item {
                Item::Node(v) => {
                    if focus {
                        self.open_focus(v);
                    } else {
                        self.open_node(v);
                    }
                }
                Item::Cmd(_, c) => {
                    let mut toggle = false;
                    self.run(ctx, c, &mut toggle);
                    if toggle {
                        self.theme_toggle = true;
                    }
                }
            }
        }
    }
}
