//! Walks the whole interface on a real graph, headlessly, to catch panics:
//! every screen, every scene, the palette, filters and a comparison.

use pipeline_graph_viewer::core::graph::Kind;
use pipeline_graph_viewer::core::model::Model;
use pipeline_graph_viewer::core::scene::Scene;
use pipeline_graph_viewer::ui::app::{LineageMode, Screen};
use pipeline_graph_viewer::ui::headless::Rig;
use std::path::Path;

fn rig() -> Rig {
    Rig::new(Model::load(Path::new("testdata/acme-previous.json")).expect("fixture"))
}

#[test]
fn every_screen_draws() {
    let mut r = rig();
    for s in Screen::ALL {
        r.ws.go(s);
        r.settle();
        assert!(r.prims > 0, "{s:?} drew nothing");
    }
}

#[test]
fn every_scene_lays_out_and_draws() {
    let mut r = rig();
    let m = r.ws.m.clone();
    let busiest = (0..m.g.len() as u32).filter(|&v| m.g.kind[v as usize] == Kind::Build).max_by_key(|&v| m.d.down[v as usize]).unwrap();
    r.ws.screen = Screen::Map;
    for scene in [Scene::Repos, Scene::All, Scene::Repo(0), Scene::Focus(busiest), Scene::Flow(0)] {
        r.ws.scene = scene.clone();
        r.settle();
        assert!(r.prims > 0, "{scene:?} drew nothing");
    }
    // lineage views of a selection, in every mode, plus a route between two jobs
    r.ws.scene = Scene::All;
    r.ws.select(Some(busiest));
    for mode in [LineageMode::Down, LineageMode::Up, LineageMode::Both, LineageMode::Off] {
        r.ws.mode = mode;
        r.settle();
    }
    let other = (0..m.g.len() as u32).find(|&v| v != busiest && m.g.kind[v as usize] == Kind::Build && m.d.down[v as usize] > 0).unwrap();
    r.ws.path_to = Some(other);
    r.settle();
}

#[test]
fn table_filters_and_search_do_not_panic() {
    let mut r = rig();
    r.ws.screen = Screen::Table;
    for q in ["", "deploy", "repo:orders-service class:test", "trig:commit", "is:notest", "kind:make", "zzz nothing", "a=", "repo:"] {
        r.ws.table.query = q.into();
        r.ws.table.dirty = true;
        r.settle();
    }
    r.ws.table.query.clear();
    r.ws.table.kinds = [true, true, true, true];
    r.ws.table.dirty = true;
    r.settle();
    assert!(!r.ws.table.rows.is_empty());
    r.ws.palette.toggle();
    for q in ["deploy", ">theme", "", "xyzzy"] {
        r.ws.palette.query = q.into();
        r.settle();
    }
}

#[test]
fn deep_links_round_trip() {
    let mut r = rig();
    r.ws.screen = Screen::Table;
    r.ws.table.query = "repo:orders-service".into();
    let sel = r.ws.m.g.node_by_id("bt:orders-service/Image");
    r.ws.select(sel);
    let link = r.ws.link();
    assert!(link.starts_with("pipeline-graph://table?"), "{link}");
    let mut other = rig();
    other.ws.goto_link(&link);
    assert_eq!(other.ws.sel, sel);
    assert_eq!(other.ws.table.query, "repo:orders-service");
    assert_eq!(other.ws.screen, Screen::Table);
    other.settle();
}

#[test]
fn comparing_with_the_current_snapshot_marks_changes() {
    let mut r = rig();
    let g = std::path::PathBuf::from("testdata/acme-previous.json");
    r.ws.start_compare(&r.ctx.clone(), g);
    r.settle();
    let t = std::time::Instant::now();
    while r.ws.compare.diff.is_none() && t.elapsed().as_secs() < 10 {
        std::thread::sleep(std::time::Duration::from_millis(5));
        r.frame();
    }
    assert!(r.ws.compare.diff.as_ref().is_some_and(|d| d.is_empty()), "a snapshot compared with itself");
    r.ws.go(Screen::Compare);
    r.settle();
}

#[test]
fn help_draws_both_tabs() {
    let mut r = rig();
    r.ws.show_help = true;
    for tab in [0, 1] {
        r.ws.help_tab = tab;
        r.settle();
        assert!(r.ws.show_help, "help closed by itself");
    }
}

#[test]
fn node_menu_draws_and_ctrl_c_copies_the_selected_id() {
    let mut r = rig();
    let v = r.ws.m.g.node_by_id("bt:orders-service/Image").expect("fixture node");
    let _ = r.ctx.run(egui::RawInput::default(), |ctx| {
        egui::CentralPanel::default().show(ctx, |ui| r.ws.node_menu(ui, v));
    });
    r.ws.screen = Screen::Table;
    r.ws.select(Some(v));
    r.settle();
    let raw = egui::RawInput { events: vec![egui::Event::Copy], ..Default::default() };
    let pal = pipeline_graph_viewer::ui::theme::palette(pipeline_graph_viewer::ui::theme::Mode::Light);
    let (mut mode, mut open) = (pipeline_graph_viewer::ui::theme::Mode::Light, None);
    let out = r.ctx.run(raw, |ctx| r.ws.frame(ctx, &pal, &mut mode, &mut open));
    let want = r.ws.ci_id(v);
    assert!(
        out.platform_output.commands.iter().any(|c| matches!(c, egui::OutputCommand::CopyText(t) if *t == want)),
        "{:?}",
        out.platform_output.commands
    );
}

#[test]
fn the_window_title_names_the_screen_the_graph_and_the_app() {
    use pipeline_graph_viewer::ui::app::window_title;
    assert_eq!(window_title(Screen::Table, Path::new("out/acme/graph.json")), "Jobs & images · acme — badger-works/pipeline-graph");
    assert_eq!(window_title(Screen::Map, Path::new("out/graph.json")), "Map · graph — badger-works/pipeline-graph");
    assert_eq!(window_title(Screen::Snapshot, Path::new("graph.json")), "Snapshot · graph — badger-works/pipeline-graph");
}

/// graph.json with `extra` more jobs than the fixture, written to a fresh path.
fn grown_copy(tag: &str, extra: usize) -> std::path::PathBuf {
    let mut v: serde_json::Value = serde_json::from_str(&std::fs::read_to_string("testdata/acme-previous.json").unwrap()).unwrap();
    let nodes = v["nodes"].as_array_mut().unwrap();
    for i in 0..extra {
        let mut n = nodes[0].clone();
        n["id"] = format!("bt:infra-terraform/Extra_{i}").into();
        n["label"] = format!("Extra {i}").into();
        nodes.push(n);
    }
    let dir = std::env::temp_dir().join(format!("pgv-{tag}-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let p = dir.join("graph.json");
    std::fs::write(&p, serde_json::to_string(&v).unwrap()).unwrap();
    p
}

/// A comparison is a list indexed by the nodes of the graph it was made against. A reload
/// swaps the graph, and used to keep the old list: wrong marks, and a crash when the new
/// graph is bigger.
#[test]
fn reloading_a_bigger_graph_with_a_comparison_open_does_not_keep_stale_marks() {
    let path = grown_copy("reload", 0);
    let mut r = Rig::new(Model::load(&path).expect("fixture"));
    r.ws.start_compare(&r.ctx.clone(), std::path::PathBuf::from("testdata/acme-previous.json"));
    let t = std::time::Instant::now();
    while r.ws.compare.diff.is_none() && t.elapsed().as_secs() < 10 {
        std::thread::sleep(std::time::Duration::from_millis(5));
        r.frame();
    }
    assert!(r.ws.compare.diff.is_some(), "the comparison must finish first");
    let before = r.ws.m.g.len();

    let bigger = grown_copy("reload-big", 30);
    std::fs::copy(&bigger, &path).unwrap();
    r.ws.start_reload(&r.ctx.clone());
    let t = std::time::Instant::now();
    while r.ws.m.g.len() == before && t.elapsed().as_secs() < 10 {
        std::thread::sleep(std::time::Duration::from_millis(5));
        r.frame();
    }
    assert_eq!(r.ws.m.g.len(), before + 30, "the reload must have landed");

    // whatever the table and the map draw now must match the new graph
    for s in [Screen::Table, Screen::Map, Screen::Compare] {
        r.ws.go(s);
        r.settle();
    }
    let t = std::time::Instant::now();
    while r.ws.compare.diff.is_none() && t.elapsed().as_secs() < 10 {
        std::thread::sleep(std::time::Duration::from_millis(5));
        r.frame();
    }
    let d = r.ws.compare.diff.as_ref().expect("the comparison is redone against the new graph");
    assert_eq!(d.state.len(), r.ws.m.g.len(), "marks must be per node of the graph on screen");
    assert_eq!(d.added, 30, "the 30 new jobs are what changed");
}
