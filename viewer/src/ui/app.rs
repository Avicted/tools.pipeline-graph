//! The application shell: loading, navigation, shortcuts, panels and deep links.

use super::canvas::{CanvasState, LayoutCache, Scene};
use super::compare::CompareState;
use super::images::ImagesState;
use super::job::Job;
use super::palette::PaletteState;
use super::shots::ShotRun;
use super::table::TableState;
use super::theme::{self, Mode, Palette};
use super::util;
use crate::core::derive::Derived;
use crate::core::graph::{Bits, Graph, Scratch};
use crate::core::search::SearchIndex;
use egui::{Align, Color32, Key, Layout, RichText, Sense};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

/// A loaded graph with everything derived from it.
pub struct Model {
    pub g: Graph,
    pub d: Derived,
    pub ix: SearchIndex,
    pub path: PathBuf,
    pub load_ms: u128,
}

impl Model {
    pub fn load(path: &Path) -> Result<Model, String> {
        let t = Instant::now();
        let g = Graph::load(path).map_err(|e| e.to_string())?;
        let d = Derived::compute(&g);
        let ix = SearchIndex::build(&g);
        Ok(Model { g, d, ix, path: path.to_path_buf(), load_ms: t.elapsed().as_millis() })
    }
}

/// The product name, shown in the window title, the top bar and the help.
pub const APP_NAME: &str = "badger-works/pipeline-graph";

/// The window title: the screen, which graph is open, and the app.
pub fn window_title(screen: Screen, path: &Path) -> String {
    let dir = path.parent().and_then(Path::file_name).filter(|d| *d != "out" && *d != ".");
    let graph = dir.or_else(|| path.file_stem()).map(|s| s.to_string_lossy()).unwrap_or_default();
    format!("{} · {graph} — {APP_NAME}", screen.title())
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Screen {
    Snapshot,
    Table,
    Map,
    Prod,
    Images,
    Compare,
}

impl Screen {
    pub const ALL: [Screen; 6] = [Screen::Snapshot, Screen::Table, Screen::Map, Screen::Prod, Screen::Images, Screen::Compare];

    pub fn title(self) -> &'static str {
        match self {
            Screen::Snapshot => "Snapshot",
            Screen::Table => "Jobs & images",
            Screen::Map => "Map",
            Screen::Prod => "Commit → prod",
            Screen::Images => "Images & layers",
            Screen::Compare => "Compare",
        }
    }

    pub fn slug(self) -> &'static str {
        match self {
            Screen::Snapshot => "snapshot",
            Screen::Table => "table",
            Screen::Map => "map",
            Screen::Prod => "prod",
            Screen::Images => "images",
            Screen::Compare => "compare",
        }
    }

    fn from_slug(s: &str) -> Option<Screen> {
        Screen::ALL.into_iter().find(|x| x.slug() == s)
    }
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum LineageMode {
    Off,
    Both,
    Down,
    Up,
}

/// What a selected job needs, what it breaks, and optionally the route between two jobs.
pub struct Lineage {
    pub root: u32,
    pub up: Bits,
    pub down: Bits,
    pub route: Option<Route>,
}

pub struct Route {
    pub set: Bits,
    pub order: Vec<u32>,
    pub downstream: bool, // the other end is downstream of the root
}

#[derive(Clone, PartialEq)]
struct Nav {
    screen: Screen,
    scene: Scene,
    prod_repo: usize,
    sel: Option<u32>,
}

pub struct Workspace {
    pub m: Arc<Model>,
    pub screen: Screen,
    pub sel: Option<u32>,
    pub path_to: Option<u32>,
    pub mode: LineageMode,
    pub lin: Option<Lineage>,
    lin_key: (Option<u32>, Option<u32>),
    pub scene: Scene,
    pub prod_repo: usize,
    pub table: TableState,
    pub canvas: CanvasState,
    pub layouts: LayoutCache,
    pub scene_limit: usize,
    pub images: ImagesState,
    pub compare: CompareState,
    pub palette: PaletteState,
    pub show_inspector: bool,
    pub show_nav: bool,
    pub show_help: bool,
    title: String,
    pub show_warnings: bool,
    pub warn_filter: String,
    pub scratch: Scratch,
    history: Vec<Nav>,
    hpos: usize,
    navigating: bool,
    toast: Option<(String, Instant)>,
    pub reload: Option<Job<Result<Model, String>>>,
    pub canvas_menu_target: Option<u32>,
    pub theme_toggle: bool,
    pub shot: Option<ShotRun>,
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
        let mut app = App { phase: Phase::Welcome(None), mode, cli, applied_mode: None };
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
        match &mut self.phase {
            Phase::Welcome(err) => {
                welcome(ctx, &pal, err.as_deref(), &mut open);
            }
            Phase::Loading(job, path) => {
                if let Some(r) = job.poll() {
                    next = Some(match r {
                        Ok(m) => {
                            let mut ws = Workspace::new(ctx, m);
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
                if let Some(p) = rfd::FileDialog::new().add_filter("pipeline-graph", &["json"]).pick_file() {
                    *open = Some(p);
                }
            }
            ui.add_space(8.0);
            ui.label(
                RichText::new("or drop a file here, or run `pipeline-graph` first and start pgview in the same directory").color(pal.muted),
            );
        });
    });
}

impl Workspace {
    pub fn new(ctx: &egui::Context, m: Model) -> Workspace {
        let n = m.g.len();
        let _ = ctx;
        let scene = if m.g.len() > 1500 { Scene::Repos } else { Scene::All };
        let ws = Workspace {
            m: Arc::new(m),
            screen: Screen::Snapshot,
            sel: None,
            path_to: None,
            mode: LineageMode::Off,
            lin: None,
            lin_key: (None, None),
            scene,
            prod_repo: 0,
            table: TableState::default(),
            canvas: CanvasState::default(),
            layouts: LayoutCache::default(),
            scene_limit: super::canvas::DEFAULT_LIMIT,
            images: ImagesState::default(),
            compare: CompareState::default(),
            palette: PaletteState::default(),
            show_inspector: true,
            show_nav: true,
            show_help: false,
            title: String::new(),
            show_warnings: false,
            warn_filter: String::new(),
            scratch: Scratch::new(n),
            history: vec![],
            hpos: 0,
            navigating: false,
            toast: None,
            reload: None,
            canvas_menu_target: None,
            theme_toggle: false,
            shot: None,
        };
        let mut ws = ws;
        ws.compare.discover(&ws.m.path);
        ws.prod_repo = ws.best_prod_repo();
        ws.history.push(ws.nav());
        ws
    }

    /// The repo with the longest commit → prod chain: the most useful one to open first.
    fn best_prod_repo(&self) -> usize {
        let d = &self.m.d;
        (0..d.repos.len()).max_by_key(|&i| d.repos[i].longest).unwrap_or(0)
    }

    fn nav(&self) -> Nav {
        Nav { screen: self.screen, scene: self.scene.clone(), prod_repo: self.prod_repo, sel: self.sel }
    }

    fn apply_nav(&mut self, n: Nav) {
        self.screen = n.screen;
        self.scene = n.scene;
        self.prod_repo = n.prod_repo;
        self.sel = n.sel;
        self.path_to = None;
        self.canvas.fit_pending = true;
    }

    pub fn back(&mut self) {
        if self.hpos > 0 {
            self.hpos -= 1;
            self.navigating = true;
            self.apply_nav(self.history[self.hpos].clone());
        }
    }

    pub fn forward(&mut self) {
        if self.hpos + 1 < self.history.len() {
            self.hpos += 1;
            self.navigating = true;
            self.apply_nav(self.history[self.hpos].clone());
        }
    }

    fn record_history(&mut self) {
        if self.navigating {
            self.navigating = false;
            return;
        }
        let n = self.nav();
        if self.history[self.hpos] != n {
            self.history.truncate(self.hpos + 1);
            self.history.push(n);
            if self.history.len() > 200 {
                self.history.remove(0);
            }
            self.hpos = self.history.len() - 1;
        }
    }

    pub fn dismiss_toast(&mut self) {
        self.toast = None;
    }

    pub fn toast(&mut self, s: impl Into<String>) {
        self.toast = Some((s.into(), Instant::now()));
    }

    pub fn copy(&mut self, ctx: &egui::Context, text: String, what: &str) {
        ctx.copy_text(text);
        self.toast(format!("{what} copied"));
    }

    /// What identifies `v` in the CI or registry: the CI id, image reference or make
    /// location, falling back to the graph id.
    pub fn ci_id(&self, v: u32) -> String {
        let g = &self.m.g;
        let sub = g.s(g.sub[v as usize]);
        if sub.is_empty() { g.node_id(v) } else { sub }.to_string()
    }

    /// The right-click menu of a job, image or make target, on the map and in the table.
    pub fn node_menu(&mut self, ui: &mut egui::Ui, v: u32) {
        let m = self.m.clone();
        let g = &m.g;
        ui.label(RichText::new(g.node_label(v)).strong());
        ui.separator();
        if ui.button("Open lineage on its own").clicked() {
            self.open_focus(v);
            ui.close_menu();
        }
        if matches!(self.screen, Screen::Map | Screen::Prod) {
            for (label, mode) in [
                ("Keep only what it breaks", LineageMode::Down),
                ("Keep only what it needs", LineageMode::Up),
                ("Keep both", LineageMode::Both),
            ] {
                if ui.button(label).clicked() {
                    self.select(Some(v));
                    self.mode = mode;
                    ui.close_menu();
                }
            }
        }
        ui.separator();
        let source = g.s(g.source[v as usize]).to_string();
        let mut copies = vec![("Copy name", g.node_label(v).to_string(), "Name"), ("Copy id", self.ci_id(v), "Id")];
        if !source.is_empty() {
            copies.push(("Copy source", source, "Source"));
        }
        for (label, text, what) in copies {
            if ui.button(label).clicked() {
                self.copy(ui.ctx(), text, what);
                ui.close_menu();
            }
        }
        if ui.button("Copy link").clicked() {
            self.select(Some(v));
            let l = self.link();
            self.copy(ui.ctx(), l, "Link");
            ui.close_menu();
        }
    }

    // ---- selection ----

    pub fn select(&mut self, v: Option<u32>) {
        if self.sel != v {
            self.path_to = None;
        }
        self.sel = v;
    }

    pub fn go(&mut self, s: Screen) {
        self.screen = s;
        if s == Screen::Map || s == Screen::Prod {
            self.canvas.fit_pending = true;
        }
    }

    /// Show `v` where it reads best: in the table when that is open, otherwise on the map.
    pub fn open_node(&mut self, v: u32) {
        self.select(Some(v));
        match self.screen {
            Screen::Map | Screen::Prod => {
                self.canvas.center_on = Some(v);
            }
            Screen::Table => {
                self.table.reveal = Some(v);
            }
            _ => {
                self.screen = Screen::Table;
                self.table.reveal = Some(v);
            }
        }
    }

    pub fn open_focus(&mut self, v: u32) {
        self.select(Some(v));
        self.scene = Scene::Focus(v);
        self.screen = Screen::Map;
        self.canvas.fit_pending = true;
    }

    /// Opens the table filtered by `query`.
    pub fn filter_table(&mut self, query: &str) {
        self.table.query = query.to_string();
        self.table.by_relevance = false;
        self.table.dirty = true;
        self.screen = Screen::Table;
    }

    pub fn set_mode(&mut self, m: LineageMode) {
        self.mode = if self.mode == m { LineageMode::Off } else { m };
        self.canvas.fit_pending = self.mode != LineageMode::Off && self.screen == Screen::Map;
    }

    fn refresh_lineage(&mut self) {
        let key = (self.sel, self.path_to);
        if key == self.lin_key {
            return;
        }
        self.lin_key = key;
        self.lin = self.sel.map(|v| {
            let m = self.m.clone();
            let net = m.g.net_for(v);
            let up = self.scratch.reach(net, &[v], false);
            let down = self.scratch.reach(net, &[v], true);
            let route = self.path_to.and_then(|t| {
                let (mut set, downstream) = if down.get(t) {
                    (self.scratch.reach(net, &[t], false), true)
                } else if up.get(t) {
                    (self.scratch.reach(net, &[t], true), false)
                } else {
                    return None;
                };
                set.intersect(if downstream { &down } else { &up });
                let mut order = self.scratch.order_from(net, v, downstream, &set);
                if !downstream {
                    order.reverse(); // earliest upstream job first
                }
                Some(Route { set, order, downstream })
            });
            Lineage { root: v, up, down, route }
        });
    }

    // ---- deep links ----

    pub fn link(&self) -> String {
        let mut l = format!("pgview://{}", self.screen.slug());
        let mut q: Vec<String> = vec![];
        if let Some(v) = self.sel {
            q.push(format!("node={}", util::encode(self.m.g.node_id(v))));
        }
        match (&self.screen, &self.scene) {
            (Screen::Map, Scene::Repos) => q.push("scene=repos".into()),
            (Screen::Map, Scene::All) => q.push("scene=all".into()),
            (Screen::Map, Scene::Repo(r)) => q.push(format!("scene=repo:{}", util::encode(self.m.g.s(self.m.g.repos[*r as usize].name)))),
            (Screen::Map, Scene::Focus(v)) => q.push(format!("scene=focus:{}", util::encode(self.m.g.node_id(*v)))),
            _ => {}
        }
        if self.screen == Screen::Prod {
            if let Some(r) = self.m.g.repos.get(self.prod_repo) {
                q.push(format!("repo={}", util::encode(self.m.g.s(r.name))));
            }
        }
        if self.screen == Screen::Table && !self.table.query.is_empty() {
            q.push(format!("q={}", util::encode(&self.table.query)));
        }
        if !q.is_empty() {
            l.push('?');
            l.push_str(&q.join("&"));
        }
        l
    }

    pub fn goto_link(&mut self, link: &str) {
        let rest = link.strip_prefix("pgview://").unwrap_or(link);
        let (screen, query) = rest.split_once('?').unwrap_or((rest, ""));
        if let Some(s) = Screen::from_slug(screen) {
            self.screen = s;
        }
        let g = &self.m.g;
        for kv in query.split('&') {
            let Some((k, v)) = kv.split_once('=') else { continue };
            let v = util::decode(v);
            match k {
                "node" => self.sel = g.node_by_id(&v),
                "q" => {
                    self.table.query = v;
                    self.table.dirty = true;
                }
                "repo" => {
                    if let Some(i) = g.repo_index(&v) {
                        self.prod_repo = i;
                    }
                }
                "scene" => {
                    self.scene = match v.split_once(':') {
                        None if v == "all" => Scene::All,
                        None => Scene::Repos,
                        Some(("repo", r)) => g.repo_index(r).map_or(Scene::Repos, |i| Scene::Repo(i as u32)),
                        Some(("focus", n)) => g.node_by_id(n).map_or(Scene::Repos, Scene::Focus),
                        _ => Scene::Repos,
                    }
                }
                _ => {}
            }
        }
        self.canvas.fit_pending = true;
        if let Some(v) = self.sel {
            self.canvas.center_on = Some(v);
        }
    }

    // ---- frame ----

    pub fn frame(&mut self, ctx: &egui::Context, pal: &Palette, mode: &mut Mode, open: &mut Option<PathBuf>) {
        self.refresh_lineage();
        self.poll_background(ctx);
        let title = window_title(self.screen, &self.m.path);
        if title != self.title {
            ctx.send_viewport_cmd(egui::ViewportCommand::Title(title.clone()));
            self.title = title;
        }
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
            if let Some(r) = job.poll() {
                self.reload = None;
                match r {
                    Ok(m) => {
                        let (sel_id, screen, table_q) =
                            (self.sel.map(|v| self.m.g.node_id(v).to_string()), self.screen, self.table.query.clone());
                        let compare = std::mem::take(&mut self.compare);
                        let mut fresh = Workspace::new(ctx, m);
                        fresh.compare = compare;
                        fresh.compare.discover(&fresh.m.path);
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
            ui.add_enabled_ui(self.hpos > 0, |ui| {
                if ui.add(egui::Button::new("◀").frame(false)).on_hover_text("Back (Alt+←)").clicked() {
                    self.back();
                }
            });
            ui.add_enabled_ui(self.hpos + 1 < self.history.len(), |ui| {
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
            for (i, r) in d.repos.iter().enumerate() {
                let n = r.builds + r.images;
                if nav_item(ui, pal, g.s(r.name), Some(util::num(n)), false) {
                    let q = format!("repo:{}", g.s(r.name));
                    self.filter_table(&q);
                    let _ = i;
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
            let mut open = true;
            egui::Window::new("Keyboard shortcuts")
                .open(&mut open)
                .collapsible(false)
                .resizable(false)
                .anchor(egui::Align2::CENTER_CENTER, [0.0, 0.0])
                .show(ctx, |ui| {
                    egui::Grid::new("help").num_columns(2).spacing([24.0, 6.0]).show(ui, |ui| {
                        for (k, v) in HELP {
                            ui.label(RichText::new(*k).monospace().color(pal.accent));
                            ui.label(*v);
                            ui.end_row();
                        }
                    });
                });
            self.show_help = open;
        }
        if self.show_warnings {
            let mut open = true;
            let m = self.m.clone();
            egui::Window::new("Analysis warnings")
                .open(&mut open)
                .default_size([760.0, 420.0])
                .anchor(egui::Align2::CENTER_CENTER, [0.0, 0.0])
                .show(ctx, |ui| {
                    ui.label(RichText::new("Everything the analysis could not resolve. Nothing is dropped silently.").color(pal.muted));
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
        let Some((msg, t)) = &self.toast else { return };
        if t.elapsed() > Duration::from_millis(2200) {
            self.toast = None;
            return;
        }
        egui::Area::new(egui::Id::new("toast")).anchor(egui::Align2::CENTER_BOTTOM, [0.0, -24.0]).order(egui::Order::Foreground).show(
            ctx,
            |ui| {
                egui::Frame::new().fill(pal.card_sel).corner_radius(8).inner_margin(egui::Margin::symmetric(14, 8)).show(ui, |ui| {
                    ui.label(RichText::new(msg).color(pal.card_sel_text));
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
fn brand(ui: &mut egui::Ui, pal: &Palette, size: f32) {
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

const HELP: &[(&str, &str)] = &[
    ("Ctrl/⌘ K", "Search nodes and run commands (type > for commands only)"),
    ("/", "Filter the table, or open search"),
    ("1 – 6", "Snapshot, Jobs & images, Map, Commit → prod, Images, Compare"),
    ("Tab", "Switch between the table and the map"),
    ("↑ ↓  Enter", "Move through the table; open the job's lineage"),
    ("B  D  I", "Keep only what a job breaks / needs / both"),
    ("Shift+click", "Show only the route between two jobs"),
    ("Double-click", "Open a job's lineage on its own"),
    ("F", "Fit the map to the window"),
    ("Esc", "Step back: route, filter, selection, lineage"),
    ("Alt ← →", "Back and forward"),
    ("L", "Copy a link to exactly this view"),
    ("T", "Light / dark"),
    ("R", "Reload graph.json from disk"),
];
