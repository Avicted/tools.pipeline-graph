//! Walks the whole interface on a real graph, headlessly, to catch panics:
//! every screen, every scene, the palette, filters and a comparison.

use pgview::core::graph::Kind;
use pgview::ui::app::{LineageMode, Model, Screen};
use pgview::ui::canvas::Scene;
use pgview::ui::headless::Rig;
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
    assert!(link.starts_with("pgview://table?"), "{link}");
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
