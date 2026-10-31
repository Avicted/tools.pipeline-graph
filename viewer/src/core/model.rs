//! A loaded graph with everything derived from it.

use super::derive::Derived;
use super::graph::Graph;
use super::search::SearchIndex;
use std::path::{Path, PathBuf};
use std::time::Instant;

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
