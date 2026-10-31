//! The application: loading a graph, the welcome page, the menu bar and the window. What is
//! shown for an open graph is in `workspace` and `shell`.

use super::job::Job;
use super::shell::brand;
use super::shots::ShotRun;
use super::theme::{self, Mode, Palette};
use super::util;
use super::workspace::{Screen, Workspace};
use crate::core::model::Model;
use egui::RichText;
use std::path::{Path, PathBuf};

/// The product name, shown in the window title, the top bar and the help.
pub const APP_NAME: &str = "badger-works/pipeline-graph";

/// The window title: the screen, which graph is open, and the app.
pub fn window_title(screen: Screen, path: &Path) -> String {
    let dir = path.parent().and_then(Path::file_name).filter(|d| *d != "out" && *d != ".");
    let graph = dir.or_else(|| path.file_stem()).map(|s| s.to_string_lossy()).unwrap_or_default();
    format!("{} · {graph} — {APP_NAME}", screen.title())
}

pub enum Phase {
    Welcome(Option<String>),
    Loading(Job<Result<Model, String>>, PathBuf),
    Ready(Box<Workspace>),
}

pub struct Cli {
    pub path: Option<PathBuf>,
    pub compare: Option<PathBuf>,
    pub goto: Option<String>,
    pub shot: Option<PathBuf>,
    pub theme: Option<String>,
}

pub struct App {
    pub phase: Phase,
    mode: Mode,
    cli: Cli,
    applied_mode: Option<Mode>,
    show_about: bool,
}

impl App {
    pub fn new(cc: &eframe::CreationContext<'_>, cli: Cli) -> App {
        theme::install_fonts(&cc.egui_ctx);
        let mode = match cli.theme.as_deref() {
            Some("light") => Mode::Light,
            Some("dark") => Mode::Dark,
            _ if cc.egui_ctx.theme() == egui::Theme::Dark => Mode::Dark,
            _ => Mode::Light,
        };
        let mut app = App { phase: Phase::Welcome(None), mode, cli, applied_mode: None, show_about: false };
        let path = app.cli.path.clone().or_else(|| ["out/graph.json", "graph.json"].iter().map(PathBuf::from).find(|p| p.exists()));
        if let Some(p) = path {
            app.open(&cc.egui_ctx, p);
        }
        app
    }

    fn open(&mut self, ctx: &egui::Context, path: PathBuf) {
        let p = path.clone();
        self.phase = Phase::Loading(Job::spawn(ctx, move || Model::load(&p)), path);
    }
}

impl eframe::App for App {
    fn update(&mut self, ctx: &egui::Context, _frame: &mut eframe::Frame) {
        if self.applied_mode != Some(self.mode) {
            theme::apply(ctx, self.mode);
            self.applied_mode = Some(self.mode);
        }
        let pal = theme::palette(self.mode);
        let mut next: Option<Phase> = None;
        let mut open: Option<PathBuf> = None;
        let mut act = self.menu_bar(ctx, &pal);
        if let Some(p) = act.open.take() {
            open = Some(p);
        }
        if act.close {
            next = Some(Phase::Welcome(None));
        }
        if let (true, Phase::Ready(ws)) = (act.save_as, &mut self.phase) {
            ws.save_copy();
        }
        if let (true, Phase::Ready(ws)) = (act.reload, &mut self.phase) {
            ws.start_reload(ctx);
        }
        self.about(ctx, &pal);
        match &mut self.phase {
            Phase::Welcome(err) => {
                welcome(ctx, &pal, err.as_deref(), &mut open);
            }
            Phase::Loading(job, path) => {
                if let Some(r) = job.poll_result() {
                    next = Some(match r {
                        Ok(m) => {
                            let mut ws = Workspace::new(m);
                            if let Some(c) = self.cli.compare.take() {
                                ws.start_compare(ctx, c);
                                ws.screen = Screen::Compare;
                            }
                            if let Some(g) = self.cli.goto.take() {
                                ws.goto_link(&g);
                            }
                            ws.shot = self.cli.shot.take().map(ShotRun::new);
                            Phase::Ready(Box::new(ws))
                        }
                        Err(e) => Phase::Welcome(Some(e)),
                    });
                } else {
                    egui::CentralPanel::default().show(ctx, |ui| {
                        ui.centered_and_justified(|ui| {
                            ui.vertical_centered(|ui| {
                                ui.add_space(ui.available_height() / 2.0 - 40.0);
                                ui.spinner();
                                ui.label(format!("Reading {}…", path.display()));
                            });
                        });
                    });
                }
            }
            Phase::Ready(ws) => {
                let mut mode = self.mode;
                ws.frame(ctx, &pal, &mut mode, &mut open);
                self.mode = mode;
            }
        }
        ctx.input(|i| {
            if let Some(f) = i.raw.dropped_files.first().and_then(|f| f.path.clone()) {
                open = Some(f);
            }
        });
        if let Some(p) = open {
            self.open(ctx, p);
        }
        if let Some(n) = next {
            self.phase = n;
        }
    }

    /// A screenshot run sees no mouse or keyboard and always has focus, so where the
    /// pointer is and what the desktop does with focus never reach the pictures.
    fn raw_input_hook(&mut self, _ctx: &egui::Context, raw: &mut egui::RawInput) {
        let shooting = self.cli.shot.is_some() || matches!(&self.phase, Phase::Ready(ws) if ws.shot.is_some());
        if shooting {
            raw.focused = true;
            raw.dropped_files.clear();
            raw.hovered_files.clear();
            raw.events.retain(|e| matches!(e, egui::Event::Screenshot { .. }));
        }
    }
}

#[derive(Default)]
struct MenuAct {
    open: Option<PathBuf>,
    close: bool,
    save_as: bool,
    reload: bool,
}

impl App {
    /// The File and Help menus, and their shortcuts: Ctrl+O, Ctrl+W, Ctrl+Shift+S, Ctrl+Q.
    fn menu_bar(&mut self, ctx: &egui::Context, pal: &Palette) -> MenuAct {
        use egui::{Key, KeyboardShortcut, Modifiers};
        let (o, w, sa, q) = (
            KeyboardShortcut::new(Modifiers::COMMAND, Key::O),
            KeyboardShortcut::new(Modifiers::COMMAND, Key::W),
            KeyboardShortcut::new(Modifiers::COMMAND | Modifiers::SHIFT, Key::S),
            KeyboardShortcut::new(Modifiers::COMMAND, Key::Q),
        );
        let ready = matches!(self.phase, Phase::Ready(_));
        let mut act = MenuAct::default();
        let (mut do_open, mut do_close, mut do_save_as, mut do_quit) =
            ctx.input_mut(|i| (i.consume_shortcut(&o), i.consume_shortcut(&w), i.consume_shortcut(&sa), i.consume_shortcut(&q)));
        egui::TopBottomPanel::top("menubar")
            .frame(egui::Frame::new().fill(pal.panel).inner_margin(egui::Margin::symmetric(8, 2)).stroke(theme::stroke(1.0, pal.line)))
            .show(ctx, |ui| {
                egui::menu::bar(ui, |ui| {
                    ui.menu_button("File", |ui| {
                        let item = |ui: &mut egui::Ui, text: &str, sc: &KeyboardShortcut, on: bool| {
                            ui.add_enabled(on, egui::Button::new(text).shortcut_text(ctx.format_shortcut(sc))).clicked()
                        };
                        if item(ui, "Open…", &o, true) {
                            do_open = true;
                            ui.close_menu();
                        }
                        if item(ui, "Close", &w, ready) {
                            do_close = true;
                            ui.close_menu();
                        }
                        ui.separator();
                        ui.add_enabled(false, egui::Button::new("Save")).on_disabled_hover_text(
                            "The viewer only reads graph.json, so there is never anything to save. Use Save as… for a copy.",
                        );
                        if item(ui, "Save as…", &sa, ready) {
                            do_save_as = true;
                            ui.close_menu();
                        }
                        ui.separator();
                        if ui.add_enabled(ready, egui::Button::new("Reload").shortcut_text("R")).clicked() {
                            act.reload = true;
                            ui.close_menu();
                        }
                        ui.separator();
                        if item(ui, "Quit", &q, true) {
                            do_quit = true;
                            ui.close_menu();
                        }
                    });
                    ui.menu_button("Help", |ui| {
                        if ui.add_enabled(ready, egui::Button::new("Keyboard shortcuts and legend").shortcut_text("?")).clicked() {
                            if let Phase::Ready(ws) = &mut self.phase {
                                ws.show_help = true;
                            }
                            ui.close_menu();
                        }
                        if ui.button(format!("About {APP_NAME}")).clicked() {
                            self.show_about = true;
                            ui.close_menu();
                        }
                    });
                });
            });
        if do_open {
            act.open = util::graph_dialog().pick_file();
        }
        act.close = do_close && ready;
        act.save_as = do_save_as && ready;
        if do_quit {
            ctx.send_viewport_cmd(egui::ViewportCommand::Close);
        }
        act
    }

    fn about(&mut self, ctx: &egui::Context, pal: &Palette) {
        if !self.show_about {
            return;
        }
        let r = egui::Modal::new(egui::Id::new("about")).show(ctx, |ui| {
            ui.set_width(360.0);
            ui.vertical_centered(|ui| {
                brand(ui, pal, 24.0);
                ui.label(RichText::new(format!("pipeline-graph {}", env!("CARGO_PKG_VERSION"))).color(pal.muted));
                ui.add_space(8.0);
                ui.label("A native viewer for the graph.json that pipeline-graph writes: a snapshot of what your pipelines actually do.");
                ui.add_space(6.0);
                ui.label(
                    RichText::new("Read-only static analysis: nothing is executed and nothing talks to a CI server.").color(pal.muted),
                );
                ui.add_space(6.0);
                ui.label(RichText::new(format!("License: {}", env!("CARGO_PKG_LICENSE"))).small().color(pal.muted));
                ui.add_space(10.0);
                if ui.button("Close").clicked() {
                    self.show_about = false;
                }
            });
        });
        if r.should_close() {
            self.show_about = false;
        }
    }
}

fn welcome(ctx: &egui::Context, pal: &Palette, err: Option<&str>, open: &mut Option<PathBuf>) {
    egui::CentralPanel::default().show(ctx, |ui| {
        ui.vertical_centered(|ui| {
            ui.add_space(ui.available_height() * 0.28);
            ui.horizontal(|ui| {
                let w = ui.painter().layout_no_wrap(APP_NAME.to_string(), egui::FontId::proportional(34.0), pal.text).size().x;
                ui.add_space((ui.available_width() - w) / 2.0);
                brand(ui, pal, 34.0);
            });
            ui.label(RichText::new("A snapshot of what your pipelines actually do").color(pal.muted));
            ui.add_space(18.0);
            if let Some(e) = err {
                ui.colored_label(pal.removed, e);
                ui.add_space(10.0);
            }
            if ui.add(egui::Button::new(RichText::new("Open graph.json…").size(16.0)).min_size(egui::vec2(200.0, 36.0))).clicked() {
                if let Some(p) = util::graph_dialog().pick_file() {
                    *open = Some(p);
                }
            }
            ui.add_space(8.0);
            ui.label(
                RichText::new("or drop a file here, or run `pipeline-graph` first and start the viewer in the same directory")
                    .color(pal.muted),
            );
        });
    });
}
