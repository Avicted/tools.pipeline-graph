//! A loaded graph with everything derived from it.

use super::derive::Derived;
use super::graph::{Graph, Kind};
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

impl Model {
    /// The build with the most jobs upstream and downstream of it: the lineage that is the
    /// hardest to draw, and the one worth measuring.
    pub fn busiest_build(&self) -> Option<u32> {
        (0..self.g.len() as u32)
            .filter(|&v| self.g.kind[v as usize] == Kind::Build)
            .max_by_key(|&v| self.d.down[v as usize] + self.d.up[v as usize])
    }

    /// The repository with the longest commit → prod chain: the most useful one to open first.
    pub fn best_prod_repo(&self) -> usize {
        let d = &self.d;
        (0..d.repos.len()).max_by_key(|&i| d.repos[i].longest).unwrap_or(0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::core::derive::Derived;
    use crate::core::graph::tests::sample;
    use crate::core::search::SearchIndex;

    fn model(g: Graph) -> Model {
        let d = Derived::compute(&g);
        let ix = SearchIndex::build(&g);
        Model { g, d, ix, path: PathBuf::new(), load_ms: 0 }
    }

    #[test]
    fn the_busiest_build_has_the_longest_lineage() {
        let m = model(sample());
        let b = m.busiest_build().expect("the sample has builds");
        let weight = |v: u32| m.d.down[v as usize] + m.d.up[v as usize];
        assert_eq!(m.g.kind[b as usize], Kind::Build);
        for v in (0..m.g.len() as u32).filter(|&v| m.g.kind[v as usize] == Kind::Build) {
            assert!(weight(v) <= weight(b), "{} is busier than {}", m.g.node_id(v), m.g.node_id(b));
        }
    }

    #[test]
    fn the_best_prod_repo_has_the_longest_chain() {
        let m = model(sample());
        let best = m.best_prod_repo();
        assert!(m.d.repos.iter().all(|r| r.longest <= m.d.repos[best].longest));
    }

    #[test]
    fn an_empty_graph_has_no_busiest_build_and_opens_the_first_repo() {
        let m = model(Graph::from_slice(br#"{"schemaVersion":1}"#).unwrap());
        assert_eq!(m.busiest_build(), None);
        assert_eq!(m.best_prod_repo(), 0);
    }
}
