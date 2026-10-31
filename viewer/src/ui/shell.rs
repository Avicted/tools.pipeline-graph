//! What surrounds a screen: the frame (panels, top bar, navigation, windows), keyboard
//! shortcuts, background work, and the small widgets they share.

use super::app::APP_NAME;
use super::job::Job;
use super::theme::{self, Mode, Palette};
use super::util;
use super::workspace::{LineageMode, Screen, Workspace};
use crate::core::model::Model;
use crate::core::scene::Scene;
use egui::{Align, Color32, Key, Layout, RichText, Sense};
use std::path::PathBuf;
use std::time::Duration;

impl Workspace {
    // ---- frame ----

    pub fn frame(&mut self, ctx: &egui::Context, pal: &Palette, mode: &mut Mode, open: &mut Option<PathBuf>) {
        self.refresh_lineage();
        self.poll_background(ctx);
        self.sync_title(ctx);
        self.shortcuts(ctx, mode);
        if std::mem::take(&mut self.theme_toggle) {
            *mode = if *mode == Mode::Dark { Mode::Light } else { Mode::Dark };
        }

        egui::TopBottomPanel::top("top")
            .exact_height(46.0)
            .frame(egui::Frame::new().fill(pal.panel).inner_margin(egui::Margin::symmetric(12, 8)).stroke(theme::stroke(1.0, pal.line)))
            .show(ctx, |ui| {
                self.top_bar(ui, pal, mode, open);
            });
        if self.show_nav {
            egui::SidePanel::left("nav")
                .exact_width(236.0)
                .resizable(false)
                .frame(
                    egui::Frame::new().fill(pal.panel).inner_margin(egui::Margin::symmetric(10, 10)).stroke(theme::stroke(1.0, pal.line)),
                )
                .show(ctx, |ui| {
                    self.nav_panel(ui, pal);
                });
        }
        let want_inspector = self.show_inspector && self.sel.is_some();
        if want_inspector {
            egui::SidePanel::right("inspector")
                .default_width(400.0)
                .width_range(300.0..=760.0)
                .resizable(true)
                .frame(
                    egui::Frame::new().fill(pal.panel).inner_margin(egui::Margin::symmetric(14, 12)).stroke(theme::stroke(1.0, pal.line)),
                )
                .show(ctx, |ui| {
                    self.show_inspector(ui, pal);
                });
        }
        egui::CentralPanel::default().frame(egui::Frame::new().fill(pal.bg)).show(ctx, |ui| match self.screen {
            Screen::Snapshot => self.show_snapshot(ui, pal),
            Screen::Table => self.show_table(ui, pal),
            Screen::Map => self.show_map(ui, pal),
            Screen::Prod => self.show_prod(ui, pal),
            Screen::Images => self.show_images(ui, pal),
            Screen::Compare => self.show_compare(ui, pal),
        });
        self.show_palette(ctx, pal);
        self.windows(ctx, pal);
        self.toasts(ctx, pal);
        self.record_history();
        self.drive_shots(ctx);
    }

    fn poll_background(&mut self, ctx: &egui::Context) {
        if let Some(job) = &self.reload {
            if let Some(r) = job.poll_result() {
                self.reload = None;
                match r {
                    Ok(m) => {
                        let (sel_id, screen, table_q) =
                            (self.sel.map(|v| self.m.g.node_id(v).to_string()), self.screen, self.table.query.clone());
                        // A comparison is indexed by the nodes of the graph it was made against, and a job
                        // still running holds the old model: keep the choice of snapshot, redo the rest.
                        let mut compare = std::mem::take(&mut self.compare);
                        let again = compare.old_path.clone();
                        compare.diff = None;
                        compare.job = None;
                        let mut fresh = Workspace::new(m);
                        fresh.compare = compare;
                        fresh.compare.discover(&fresh.m.path);
                        if let Some(p) = again {
                            fresh.start_compare(ctx, p);
                        }
                        fresh.screen = screen;
                        fresh.table.query = table_q;
                        fresh.table.dirty = true;
                        fresh.sel = sel_id.and_then(|id| fresh.m.g.node_by_id(&id));
                        fresh.toast(format!("Reloaded in {} ms", fresh.m.load_ms));
                        *self = fresh;
                    }
                    Err(e) => self.toast(format!("Reload failed: {e}")),
                }
            }
        }
        self.poll_compare();
        self.poll_layouts();
    }

    /// File → Save as: a copy of the open graph.json (the viewer never changes it).
    pub fn save_copy(&mut self) {
        let src = self.m.path.clone();
        let name = src.file_name().map_or_else(|| "graph.json".into(), |n| n.to_string_lossy().into_owned());
        let Some(dest) = rfd::FileDialog::new().add_filter("pipeline-graph", &["json"]).set_file_name(name).save_file() else {
            return;
        };
        match std::fs::copy(&src, &dest) {
            Ok(_) => self.toast(format!("Saved a copy to {}", dest.display())),
            Err(e) => self.toast(format!("Save failed: {e}")),
        }
    }

    pub fn start_reload(&mut self, ctx: &egui::Context) {
        let p = self.m.path.clone();
        self.reload = Some(Job::spawn(ctx, move || Model::load(&p)));
        self.toast("Reloading…");
    }

    fn shortcuts(&mut self, ctx: &egui::Context, mode: &mut Mode) {
        let typing = ctx.wants_keyboard_input();
        let (cmd_k, slash, esc, back, fwd) = ctx.input(|i| {
            (
                i.key_pressed(Key::K) && i.modifiers.command,
                i.key_pressed(Key::Slash) && !i.modifiers.any(),
                i.key_pressed(Key::Escape),
                i.key_pressed(Key::ArrowLeft) && i.modifiers.alt,
                i.key_pressed(Key::ArrowRight) && i.modifiers.alt,
            )
        });
        if cmd_k {
            self.palette.toggle();
            return;
        }
        if self.palette.open {
            return;
        }
        if back {
            self.back();
        }
        if fwd {
            self.forward();
        }
        if esc {
            if self.show_help {
                self.show_help = false;
            } else if self.show_warnings {
                self.show_warnings = false;
            } else if typing {
                // let the text field release focus
            } else if self.path_to.is_some() {
                self.path_to = None;
            } else if self.mode != LineageMode::Off {
                self.mode = LineageMode::Off;
            } else if self.sel.is_some() {
                if let Scene::Focus(r) = self.scene {
                    if self.screen == Screen::Map && self.sel != Some(r) {
                        self.sel = Some(r);
                        return;
                    }
                }
                self.select(None);
            } else if let (Screen::Map, Scene::Focus(_) | Scene::Repo(_)) = (self.screen, &self.scene) {
                self.scene = if self.m.g.len() > 1500 { Scene::Repos } else { Scene::All };
                self.canvas.fit_pending = true;
            }
        }
        if typing {
            return;
        }
        if slash {
            match self.screen {
                Screen::Table => self.table.focus_filter = true,
                Screen::Images => self.images.focus = true,
                _ => self.palette.toggle(),
            }
        }
        let pressed = |k: Key| ctx.input(|i| i.key_pressed(k) && !i.modifiers.any());
        for (k, s) in [
            (Key::Num1, Screen::Snapshot),
            (Key::Num2, Screen::Table),
            (Key::Num3, Screen::Map),
            (Key::Num4, Screen::Prod),
            (Key::Num5, Screen::Images),
            (Key::Num6, Screen::Compare),
        ] {
            if pressed(k) {
                self.go(s);
            }
        }
        if pressed(Key::Tab) {
            self.go(if self.screen == Screen::Table { Screen::Map } else { Screen::Table });
        }
        if pressed(Key::B) {
            self.set_mode(LineageMode::Down);
        }
        if pressed(Key::D) {
            self.set_mode(LineageMode::Up);
        }
        if pressed(Key::I) {
            self.set_mode(LineageMode::Both);
        }
        if pressed(Key::F) {
            self.canvas.fit_all = true;
        }
        if pressed(Key::T) {
            *mode = if *mode == Mode::Dark { Mode::Light } else { Mode::Dark };
        }
        if pressed(Key::L) {
            let l = self.link();
            self.copy(ctx, l, "Link");
        }
        if pressed(Key::R) {
            self.start_reload(ctx);
        }
        // Ctrl+C copies selected text when there is some, otherwise the selected node's id.
        let copy = ctx.input(|i| i.events.iter().any(|e| matches!(e, egui::Event::Copy)));
        if copy && !egui::text_selection::LabelSelectionState::load(ctx).has_selection() {
            if let Some(v) = self.sel {
                let id = self.ci_id(v);
                self.copy(ctx, id, "Id");
            }
        }
        if ctx.input(|i| i.key_pressed(Key::Questionmark)) {
            self.show_help = !self.show_help;
        }
        if pressed(Key::Enter) {
            if let Some(v) = self.sel {
                if self.screen == Screen::Table {
                    self.open_focus(v);
                }
            }
        }
    }

    fn top_bar(&mut self, ui: &mut egui::Ui, pal: &Palette, mode: &mut Mode, open: &mut Option<PathBuf>) {
        ui.horizontal_centered(|ui| {
            ui.style_mut().interaction.selectable_labels = false;
            if ui.add(egui::Button::new("☰").frame(false)).on_hover_text("Show or hide the sidebar").clicked() {
                self.show_nav = !self.show_nav;
            }
            ui.add_enabled_ui(self.history.can_back(), |ui| {
                if ui.add(egui::Button::new("◀").frame(false)).on_hover_text("Back (Alt+←)").clicked() {
                    self.back();
                }
            });
            ui.add_enabled_ui(self.history.can_forward(), |ui| {
                if ui.add(egui::Button::new("▶").frame(false)).on_hover_text("Forward (Alt+→)").clicked() {
                    self.forward();
                }
            });
            brand(ui, pal, 16.0);
            ui.label(RichText::new("/").color(pal.muted));
            ui.label(RichText::new(self.screen.title()).size(15.0));
            let g = &self.m.g;
            let gen = g.s(g.generated);
            ui.label(
                RichText::new(format!("   {} · {} repos", if gen.is_empty() { "undated snapshot" } else { gen }, g.repos.len()))
                    .small()
                    .color(pal.muted),
            );
            if self.reload.is_some() {
                ui.spinner();
            }
            ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                if ui.add(egui::Button::new("?").min_size(egui::vec2(28.0, 28.0))).on_hover_text("Keyboard shortcuts").clicked() {
                    self.show_help = !self.show_help;
                }
                let icon = if *mode == Mode::Dark { "☀" } else { "☾" };
                if ui.add(egui::Button::new(icon).min_size(egui::vec2(28.0, 28.0))).on_hover_text("Light / dark (T)").clicked() {
                    *mode = if *mode == Mode::Dark { Mode::Light } else { Mode::Dark };
                }
                let b = egui::Button::new(RichText::new("🔍  Search or run a command…      Ctrl K").color(pal.muted))
                    .min_size(egui::vec2(300.0, 28.0));
                if ui.add(b).clicked() {
                    self.palette.toggle();
                }
                let tip = "Open another graph.json";
                if ui.add(egui::Button::new("Open…").min_size(egui::vec2(28.0, 28.0))).on_hover_text(tip).clicked() {
                    if let Some(p) = rfd::FileDialog::new().add_filter("pipeline-graph", &["json"]).pick_file() {
                        *open = Some(p);
                    }
                }
                if self.screen != Screen::Compare
                    && ui
                        .add(
                            egui::Button::new(if self.show_inspector { "Details ▸" } else { "◂ Details" }).min_size(egui::vec2(28.0, 28.0)),
                        )
                        .clicked()
                {
                    self.show_inspector = !self.show_inspector;
                }
            });
        });
    }

    fn nav_panel(&mut self, ui: &mut egui::Ui, pal: &Palette) {
        let m = self.m.clone();
        let (g, d) = (&m.g, &m.d);
        ui.spacing_mut().item_spacing.y = 2.0;
        section(ui, pal, "VIEWS");
        for (i, s) in Screen::ALL.into_iter().enumerate() {
            let count = match s {
                Screen::Snapshot => None,
                Screen::Table => Some(d.totals.builds + d.totals.images + d.totals.externals),
                Screen::Map => Some(d.totals.builds + d.totals.images + d.totals.externals),
                Screen::Prod => Some(d.repos.iter().filter(|r| r.vcs > 0).count() as u32),
                Screen::Images => Some(d.totals.images),
                Screen::Compare => self.compare.diff.as_ref().map(|x| x.added + x.changed + x.removed.len() as u32),
            };
            if nav_item(ui, pal, &format!("{}  {}", i + 1, s.title()), count.map(util::num), self.screen == s) {
                self.go(s);
            }
        }
        ui.add_space(10.0);
        section(ui, pal, "COMMIT → PROD");
        egui::ScrollArea::vertical().id_salt("nav-scroll").auto_shrink([false, false]).show(ui, |ui| {
            for (i, r) in d.repos.iter().enumerate() {
                if r.vcs == 0 {
                    continue;
                }
                let sel = self.screen == Screen::Prod && self.prod_repo == i;
                if nav_item(ui, pal, g.s(r.name), Some(r.longest.to_string()), sel) {
                    self.prod_repo = i;
                    self.go(Screen::Prod);
                }
            }
            ui.add_space(10.0);
            section(ui, pal, "REPOSITORIES");
            for r in d.repos.iter() {
                let n = r.builds + r.images;
                if nav_item(ui, pal, g.s(r.name), Some(util::num(n)), false) {
                    let q = format!("repo:{}", g.s(r.name));
                    self.filter_table(&q);
                }
            }
            ui.add_space(12.0);
            if !g.warnings.is_empty() {
                let b = egui::Button::new(
                    RichText::new(format!("⚠ {} analysis warning{}", g.warnings.len(), if g.warnings.len() == 1 { "" } else { "s" }))
                        .color(pal.warn),
                )
                .fill(util_tint(pal.warn, 24))
                .stroke(theme::stroke(1.0, pal.warn));
                if ui.add_sized([ui.available_width(), 30.0], b).clicked() {
                    self.show_warnings = true;
                }
            }
        });
    }

    fn windows(&mut self, ctx: &egui::Context, pal: &Palette) {
        if self.show_help {
            self.help_overlay(ctx, pal);
        }
        if self.show_warnings {
            let mut open = true;
            let m = self.m.clone();
            egui::Window::new("Analysis warnings")
                .open(&mut open)
                .default_size([760.0, 420.0])
                .anchor(egui::Align2::CENTER_CENTER, [0.0, 0.0])
                .show(ctx, |ui| {
                    ui.label(RichText::new("Everything the analysis could not resolve, and anything in this file the viewer had to repair. Nothing is dropped silently.").color(pal.muted));
                    ui.add(egui::TextEdit::singleline(&mut self.warn_filter).hint_text("Filter…").desired_width(f32::INFINITY));
                    let f = self.warn_filter.to_lowercase();
                    egui::ScrollArea::vertical().show(ui, |ui| {
                        for w in &m.g.warnings {
                            let w = m.g.s(*w);
                            if f.is_empty() || w.to_lowercase().contains(&f) {
                                ui.horizontal_wrapped(|ui| {
                                    ui.label(RichText::new("⚠").color(pal.warn));
                                    ui.label(w);
                                });
                            }
                        }
                    });
                });
            self.show_warnings = open;
        }
    }

    fn toasts(&mut self, ctx: &egui::Context, pal: &Palette) {
        let Some(msg) = self.live_toast().map(str::to_owned) else { return };
        egui::Area::new(egui::Id::new("toast")).anchor(egui::Align2::CENTER_BOTTOM, [0.0, -24.0]).order(egui::Order::Foreground).show(
            ctx,
            |ui| {
                egui::Frame::new().fill(pal.card_sel).corner_radius(8).inner_margin(egui::Margin::symmetric(14, 8)).show(ui, |ui| {
                    ui.label(RichText::new(&msg).color(pal.card_sel_text));
                });
            },
        );
        ctx.request_repaint_after(Duration::from_millis(300));
    }
}

pub fn util_tint(c: Color32, alpha: u8) -> Color32 {
    theme::with_alpha(c, alpha)
}

/// "badger-works/" muted, then "pipeline-graph" bold.
pub(super) fn brand(ui: &mut egui::Ui, pal: &Palette, size: f32) {
    let (org, name) = APP_NAME.split_once('/').unwrap_or(("", APP_NAME));
    let gap = std::mem::replace(&mut ui.spacing_mut().item_spacing.x, 0.0);
    ui.label(RichText::new(format!("{org}/")).size(size).color(pal.muted));
    ui.spacing_mut().item_spacing.x = gap;
    ui.label(RichText::new(name).size(size).strong());
}

fn section(ui: &mut egui::Ui, pal: &Palette, title: &str) {
    ui.add_space(4.0);
    ui.label(RichText::new(title).small().strong().color(pal.muted));
    ui.add_space(2.0);
}

/// A row in the sidebar; true when clicked.
fn nav_item(ui: &mut egui::Ui, pal: &Palette, label: &str, count: Option<String>, active: bool) -> bool {
    let h = 28.0;
    let (rect, resp) = ui.allocate_exact_size(egui::vec2(ui.available_width(), h), Sense::click());
    let hovered = resp.hovered();
    if active {
        ui.painter().rect_filled(rect, 7, pal.accent);
    } else if hovered {
        ui.painter().rect_filled(rect, 7, util_tint(pal.muted, 36));
    }
    let fg = if active { Color32::WHITE } else { pal.text };
    ui.painter().text(rect.left_center() + egui::vec2(10.0, 0.0), egui::Align2::LEFT_CENTER, label, egui::FontId::proportional(14.0), fg);
    if let Some(c) = count {
        ui.painter().text(
            rect.right_center() - egui::vec2(10.0, 0.0),
            egui::Align2::RIGHT_CENTER,
            c,
            egui::FontId::proportional(12.0),
            if active { Color32::WHITE } else { pal.muted },
        );
    }
    resp.clicked()
}
