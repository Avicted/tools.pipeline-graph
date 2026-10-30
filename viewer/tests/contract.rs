//! The Go analyzer and this viewer meet at graph.json. These tests read the
//! analyzer's own golden output, so a change on either side that breaks the
//! contract fails here.

use pgview::core::graph::{Ek, Graph, SCHEMA};

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
    assert!(pgview::core::diff::compare(&load(), &load()).is_empty());
}
