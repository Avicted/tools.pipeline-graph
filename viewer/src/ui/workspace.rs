//! The open graph and where you are in it: which screen, what is selected, the lineage of the
//! selection, the history behind Back and Forward, and the deep link that spells all of it.

use super::app::window_title;
use super::canvas::{CanvasState, LayoutCache};
use super::compare::CompareState;
use super::images::ImagesState;
use super::job::Job;
use super::palette::PaletteState;
use super::shots::ShotRun;
use super::table::TableState;
use super::util;
use crate::core::graph::{Bits, Scratch};
use crate::core::model::Model;
use crate::core::scene::Scene;
use egui::RichText;
use std::sync::Arc;
use std::time::{Duration, Instant};

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

/// Where Back and Forward go: the places visited, the one we are at, and whether the last
/// change of place was a Back or Forward (which must not be recorded as a new visit).
#[derive(Default)]
pub struct History {
    places: Vec<Nav>,
    at: usize,
    replaying: bool,
}

/// How many places Back remembers.
const HISTORY_LIMIT: usize = 200;
/// How long a toast stays up.
const TOAST_MS: u64 = 2200;

impl History {
    fn starting_at(first: Nav) -> History {
        History { places: vec![first], at: 0, replaying: false }
    }

    pub fn can_back(&self) -> bool {
        self.at > 0
    }

    pub fn can_forward(&self) -> bool {
        self.at + 1 < self.places.len()
    }

    /// Moves one place back or forward and returns it, or None at either end.
    fn step(&mut self, back: bool) -> Option<Nav> {
        if (back && !self.can_back()) || (!back && !self.can_forward()) {
            return None;
        }
        self.at = if back { self.at - 1 } else { self.at + 1 };
        self.replaying = true;
        Some(self.places[self.at].clone())
    }

    fn record(&mut self, here: Nav) {
        if self.replaying {
            self.replaying = false;
            return;
        }
        if self.places[self.at] != here {
            self.places.truncate(self.at + 1);
            self.places.push(here);
            if self.places.len() > HISTORY_LIMIT {
                self.places.remove(0);
            }
            self.at = self.places.len() - 1;
        }
    }
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
    pub help_tab: usize,
    title: String,
    pub show_warnings: bool,
    pub warn_filter: String,
    pub scratch: Scratch,
    pub history: History,
    toast: Option<(String, Instant)>,
    pub reload: Option<Job<Result<Model, String>>>,
    pub canvas_menu_target: Option<u32>,
    pub theme_toggle: bool,
    pub shot: Option<ShotRun>,
}

impl Workspace {
    pub fn new(m: Model) -> Workspace {
        let n = m.g.len();
        let scene = Scene::first_for(m.g.len());
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
            scene_limit: crate::core::scene::DEFAULT_LIMIT,
            images: ImagesState::default(),
            compare: CompareState::default(),
            palette: PaletteState::default(),
            show_inspector: true,
            show_nav: true,
            show_help: false,
            help_tab: 0,
            title: String::new(),
            show_warnings: false,
            warn_filter: String::new(),
            scratch: Scratch::new(n),
            history: History::default(),
            toast: None,
            reload: None,
            canvas_menu_target: None,
            theme_toggle: false,
            shot: None,
        };
        let mut ws = ws;
        ws.compare.discover(&ws.m.path);
        ws.prod_repo = ws.m.best_prod_repo();
        ws.history = History::starting_at(ws.nav());
        ws
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
        if let Some(n) = self.history.step(true) {
            self.apply_nav(n);
        }
    }

    pub fn forward(&mut self) {
        if let Some(n) = self.history.step(false) {
            self.apply_nav(n);
        }
    }

    /// Called once per frame: remembers the place we are at, unless we only just got there by
    /// going back or forward.
    pub(super) fn record_history(&mut self) {
        let here = self.nav();
        self.history.record(here);
    }

    pub fn dismiss_toast(&mut self) {
        self.toast = None;
    }

    /// The message to show now, or None once it has been up for `TOAST_MS`.
    pub fn live_toast(&mut self) -> Option<&str> {
        if self.toast.as_ref().is_some_and(|(_, t)| t.elapsed() > Duration::from_millis(TOAST_MS)) {
            self.toast = None;
        }
        self.toast.as_ref().map(|(m, _)| m.as_str())
    }

    /// Keeps the window title in step with the screen and the graph.
    pub(super) fn sync_title(&mut self, ctx: &egui::Context) {
        let title = window_title(self.screen, &self.m.path);
        if title != self.title {
            ctx.send_viewport_cmd(egui::ViewportCommand::Title(title.clone()));
            self.title = title;
        }
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

    pub(super) fn refresh_lineage(&mut self) {
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
        let mut l = format!("pipeline-graph://{}", self.screen.slug());
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
        let rest = link.strip_prefix("pipeline-graph://").unwrap_or(link);
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
}
