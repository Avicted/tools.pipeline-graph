//! The Go analyzer and this viewer meet at graph.json. These tests read the
//! analyzer's own golden output, so a change on either side that breaks the
//! contract fails here.

use pipeline_graph_viewer::core::graph::{Ek, Graph, SCHEMA};

const GOLDEN: &str = "../test/integration/testdata/graph.json.golden";

#[test]
fn reads_what_the_analyzer_writes() {
    let text = std::fs::read_to_string(GOLDEN).expect("golden file");
    let raw: serde_json::Value = serde_json::from_str(&text).unwrap();
    let g = Graph::from_slice(text.as_bytes()).unwrap();

    assert_eq!(raw["schemaVersion"], SCHEMA, "the viewer and the analyzer disagree on the schema version");
    assert_eq!(g.len(), raw["nodes"].as_array().unwrap().len());
    assert_eq!(g.full.len(), raw["edges"].as_array().unwrap().len(), "no edge may be dropped");
    assert_eq!(g.warnings.len(), raw["warnings"].as_array().map_or(0, Vec::len));
    assert_eq!(g.repos.len(), raw["repos"].as_array().unwrap().len());
}

#[test]
fn knows_every_edge_kind_the_analyzer_emits() {
    let g = Graph::from_slice(std::fs::read(GOLDEN).unwrap().as_slice()).unwrap();
    assert!(g.full.kind.iter().all(|k| *k != Ek::Other), "an edge kind in graph.json is unknown to the viewer: add it to core::graph::Ek");
}

#[test]
fn a_snapshot_compared_with_itself_has_no_difference() {
    let load = || Graph::from_slice(std::fs::read(GOLDEN).unwrap().as_slice()).unwrap();
    assert!(pipeline_graph_viewer::core::diff::compare(&load(), &load()).is_empty());
}

// ---- the contract is read field by field, not only counted -------------------------------
//
// Every Raw* struct in core::graph is `#[serde(default)]`, so a field that Go renames reads as
// empty and every count above still matches. These tests compare the loaded graph with the raw
// JSON value by value, so a rename, or a field the viewer stopped reading, fails here.

use pipeline_graph_viewer::core::graph::Kind;
use serde_json::Value;
use std::collections::BTreeSet;

fn golden() -> (Value, Graph) {
    let text = std::fs::read_to_string(GOLDEN).expect("golden file");
    (serde_json::from_str(&text).unwrap(), Graph::from_slice(text.as_bytes()).unwrap())
}

fn str_of<'a>(v: &'a Value, k: &str) -> &'a str {
    v[k].as_str().unwrap_or("")
}

fn keys(v: &Value) -> BTreeSet<&str> {
    v.as_object().expect("an object").keys().map(String::as_str).collect()
}

/// The keys the viewer reads, per kind of object. Keep in step with the Raw* structs in
/// core::graph; a key the golden file has and this lacks means Go added or renamed a field.
const TOP: &[&str] = &["schemaVersion", "generated", "repos", "systems", "nodes", "edges", "warnings"];
const NODE: &[&str] =
    &["id", "kind", "repo", "label", "sub", "project", "composite", "steps", "triggers", "params", "artifacts", "source", "system"];
const STEP: &[&str] = &["name", "type", "summary", "script", "workDir"];
const TRIGGER: &[&str] = &["type", "detail"];
const EDGE: &[&str] = &["from", "to", "kind", "label"];
const REPO: &[&str] = &["name", "url", "commit"];
const SYSTEM: &[&str] = &["name", "title", "job", "id"];

fn only(what: &str, got: BTreeSet<&str>, known: &[&str]) {
    let extra: Vec<_> = got.iter().filter(|k| !known.contains(k)).collect();
    assert!(extra.is_empty(), "{what} has keys the viewer does not read: {extra:?} (renamed or new on the Go side?)");
}

#[test]
fn the_golden_file_has_no_key_the_viewer_ignores() {
    let (raw, _) = golden();
    only("graph.json", keys(&raw), TOP);
    for r in raw["repos"].as_array().unwrap() {
        only("a repo", keys(r), REPO);
    }
    for s in raw["systems"].as_array().unwrap() {
        only("a system", keys(s), SYSTEM);
    }
    for e in raw["edges"].as_array().unwrap() {
        only("an edge", keys(e), EDGE);
    }
    for n in raw["nodes"].as_array().unwrap() {
        only("a node", keys(n), NODE);
        for s in n["steps"].as_array().into_iter().flatten() {
            only("a step", keys(s), STEP);
        }
        for t in n["triggers"].as_array().into_iter().flatten() {
            only("a trigger", keys(t), TRIGGER);
        }
    }
}

#[test]
fn every_value_in_the_golden_file_reaches_the_viewer() {
    let (raw, g) = golden();
    let (mut steps, mut triggers, mut params, mut arts, mut sources, mut systems, mut work_dirs) = (0, 0, 0, 0, 0, 0, 0);
    for n in raw["nodes"].as_array().unwrap() {
        let v = g.node_by_id(str_of(n, "id")).expect("every node loads");
        let i = v as usize;
        assert_eq!(g.s(g.label[i]), str_of(n, "label"));
        assert_eq!(g.s(g.sub[i]), str_of(n, "sub"));
        assert_eq!(g.s(g.project[i]), str_of(n, "project"));
        assert_eq!(g.s(g.repo[i]), str_of(n, "repo"));
        assert_eq!(g.s(g.source[i]), str_of(n, "source"));
        assert_eq!(g.s(g.system[i]), str_of(n, "system"));
        assert_eq!(g.composite[i], n["composite"].as_bool().unwrap_or(false));
        let want = match str_of(n, "kind") {
            "build" => Kind::Build,
            "make" => Kind::Make,
            "image" => Kind::Image,
            "external" => Kind::External,
            other => panic!("the golden file has a node kind this test does not know: {other}"),
        };
        assert_eq!(g.kind[i], want, "kind of {}", str_of(n, "id"));

        let raw_steps = n["steps"].as_array().cloned().unwrap_or_default();
        assert_eq!(g.steps(v).len(), raw_steps.len(), "steps of {}", str_of(n, "id"));
        for (s, r) in g.steps(v).iter().zip(&raw_steps) {
            assert_eq!(g.s(s.name), str_of(r, "name"));
            assert_eq!(g.s(s.ty), str_of(r, "type"));
            assert_eq!(g.s(s.summary), str_of(r, "summary"));
            assert_eq!(g.s(s.script), str_of(r, "script"));
            assert_eq!(g.s(s.workdir), str_of(r, "workDir"));
        }
        let raw_trigs = n["triggers"].as_array().cloned().unwrap_or_default();
        assert_eq!(g.triggers(v).len(), raw_trigs.len());
        for (t, r) in g.triggers(v).iter().zip(&raw_trigs) {
            assert_eq!(g.s(t.ty), str_of(r, "type"));
            assert_eq!(g.s(t.detail), str_of(r, "detail"));
        }
        let mut raw_params: Vec<(String, String)> =
            n["params"].as_object().into_iter().flatten().map(|(k, v)| (k.clone(), v.as_str().unwrap_or("").to_string())).collect();
        raw_params.sort();
        let got: Vec<(String, String)> = g.params(v).iter().map(|(k, val)| (g.s(*k).to_string(), g.s(*val).to_string())).collect();
        assert_eq!(got, raw_params, "params of {}", str_of(n, "id"));
        let raw_arts: Vec<&str> = n["artifacts"].as_array().into_iter().flatten().map(|a| a.as_str().unwrap_or("")).collect();
        let got_arts: Vec<&str> = g.artifacts(v).iter().map(|a| g.s(*a)).collect();
        assert_eq!(got_arts, raw_arts);

        work_dirs += raw_steps.iter().filter(|r| !str_of(r, "workDir").is_empty()).count();
        steps += raw_steps.len();
        triggers += raw_trigs.len();
        params += raw_params.len();
        arts += raw_arts.len();
        sources += usize::from(!str_of(n, "source").is_empty());
        systems += usize::from(!str_of(n, "system").is_empty());
    }
    // a comparison that never meets a value proves nothing: the fixtures must exercise each field
    for (what, n) in [
        ("steps", steps),
        ("triggers", triggers),
        ("params", params),
        ("artifacts", arts),
        ("sources", sources),
        ("systems", systems),
        ("working directories", work_dirs),
    ] {
        assert!(n > 0, "the golden file has no {what}, so this test cannot see them: extend test/fixtures");
    }

    for (r, raw) in g.repos.iter().zip(raw["repos"].as_array().unwrap()) {
        assert_eq!((g.s(r.name), g.s(r.url), g.s(r.commit)), (str_of(raw, "name"), str_of(raw, "url"), str_of(raw, "commit")));
    }
    assert_eq!(g.systems.len(), raw["systems"].as_array().unwrap().len());
    for (s, raw) in g.systems.iter().zip(raw["systems"].as_array().unwrap()) {
        assert_eq!(
            (g.s(s.name), g.s(s.title), g.s(s.job), g.s(s.id)),
            (str_of(raw, "name"), str_of(raw, "title"), str_of(raw, "job"), str_of(raw, "id"))
        );
    }
    assert_eq!(g.s(g.generated), str_of(&raw, "generated"));
}

/// graph.json's edge kind strings, and the viewer's reading of each. A kind the fixtures stop
/// producing, or one Go adds, changes this set and fails the test.
const EDGE_KINDS: [(&str, Ek); 10] = [
    ("snapshot", Ek::Snapshot),
    ("artifact", Ek::Artifact),
    ("trigger", Ek::Trigger),
    ("runs", Ek::Runs),
    ("depends", Ek::Depends),
    ("calls", Ek::Calls),
    ("produces", Ek::Produces),
    ("consumes", Ek::Consumes),
    ("derives", Ek::Derives),
    ("runsIn", Ek::RunsIn),
];

#[test]
fn every_edge_kind_is_in_the_golden_file_and_read_correctly() {
    let (raw, g) = golden();
    let present: BTreeSet<&str> = raw["edges"].as_array().unwrap().iter().map(|e| str_of(e, "kind")).collect();
    let expected: BTreeSet<&str> = EDGE_KINDS.iter().map(|(k, _)| *k).collect();
    assert_eq!(present, expected, "the fixtures must produce every edge kind, and nothing else may appear unannounced");
    for (i, e) in raw["edges"].as_array().unwrap().iter().enumerate() {
        let want = EDGE_KINDS.iter().find(|(k, _)| *k == str_of(e, "kind")).unwrap().1;
        assert_eq!(g.full.kind[i], want, "edge {i}");
        assert_eq!(g.node_id(g.full.from[i]), str_of(e, "from"));
        assert_eq!(g.node_id(g.full.to[i]), str_of(e, "to"));
        assert_eq!(g.s(g.full.label[i]), str_of(e, "label"));
    }
}

#[test]
fn the_analyzers_own_output_needs_no_repair() {
    let (_, g) = golden();
    let repairs: Vec<&str> = g.warnings.iter().map(|w| g.s(*w)).filter(|w| w.starts_with("graph.json:")).collect();
    assert!(repairs.is_empty(), "the loader had to repair what Go wrote: {repairs:?}");
}
