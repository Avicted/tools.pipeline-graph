//! Snapshot comparison: what was added, removed or changed between two runs
//! of the analysis. Source line numbers are ignored; they drift with every edit.

use super::graph::{Ek, Graph, Kind};
use std::collections::{HashMap, HashSet};

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Change {
    Same,
    Added,
    Changed,
}

#[derive(Clone, Debug)]
pub struct Removed {
    pub id: String,
    pub label: String,
    pub repo: String,
    pub kind: Kind,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct EdgeRef {
    pub from: String,
    pub to: String,
    pub kind: Ek,
}

#[derive(Clone, Debug, Default)]
pub struct RepoDelta {
    pub repo: String,
    pub added: u32,
    pub removed: u32,
    pub changed: u32,
    pub old_commit: String,
    pub new_commit: String,
}

pub struct Diff {
    pub old_generated: String,
    pub new_generated: String,
    /// Per node of the new graph.
    pub state: Vec<Change>,
    pub notes: HashMap<u32, Vec<String>>,
    pub removed: Vec<Removed>,
    pub edges_added: Vec<EdgeRef>,
    pub edges_removed: Vec<EdgeRef>,
    pub repos: Vec<RepoDelta>,
    pub added: u32,
    pub changed: u32,
}

impl Diff {
    pub fn is_empty(&self) -> bool {
        self.added == 0 && self.changed == 0 && self.removed.is_empty() && self.edges_added.is_empty() && self.edges_removed.is_empty()
    }
}

#[derive(Default)]
struct Repos(HashMap<String, RepoDelta>);

impl Repos {
    fn of(&mut self, r: &str) -> &mut RepoDelta {
        self.0.entry(r.to_string()).or_insert_with(|| RepoDelta { repo: r.to_string(), ..Default::default() })
    }
}

const MAX_NOTES: usize = 12;

fn note(notes: &mut HashMap<u32, Vec<String>>, v: u32, s: String) {
    let n = notes.entry(v).or_default();
    if n.len() < MAX_NOTES {
        n.push(s);
    } else if n.len() == MAX_NOTES {
        n.push("…and more".to_string());
    }
}

fn first_line(s: &str) -> &str {
    s.lines().next().unwrap_or("")
}

fn compare_nodes(old: &Graph, o: u32, new: &Graph, n: u32) -> Vec<String> {
    let mut out = vec![];
    let (oi, ni) = (o as usize, n as usize);
    let mut field = |name: &str, a: &str, b: &str| {
        if a != b {
            out.push(format!("{name}: “{a}” → “{b}”"));
        }
    };
    field("label", old.node_label(o), new.node_label(n));
    field("repo", old.node_repo(o), new.node_repo(n));
    field("project", old.s(old.project[oi]), new.s(new.project[ni]));
    field("id in CI", old.s(old.sub[oi]), new.s(new.sub[ni]));
    if old.kind[oi] != new.kind[ni] || old.composite[oi] != new.composite[ni] {
        out.push(format!("kind: {} → {}", old.kind_name(o), new.kind_name(n)));
    }
    let (os, ns) = (old.steps(o), new.steps(n));
    for i in 0..os.len().max(ns.len()) {
        match (os.get(i), ns.get(i)) {
            (Some(a), Some(b)) => {
                if old.s(a.script) != new.s(b.script) || old.s(a.ty) != new.s(b.ty) || old.s(a.workdir) != new.s(b.workdir) {
                    out.push(format!("step {} changed: “{}” → “{}”", i + 1, old.s(a.summary), new.s(b.summary)));
                }
            }
            (None, Some(b)) => out.push(format!("step {} added: {}", i + 1, first_line(new.s(b.summary)))),
            (Some(a), None) => out.push(format!("step {} removed: {}", i + 1, first_line(old.s(a.summary)))),
            (None, None) => {}
        }
    }
    let trig = |g: &Graph, v: u32| -> Vec<String> {
        let mut t: Vec<String> = g.triggers(v).iter().map(|t| format!("{} {}", g.s(t.ty), g.s(t.detail)).trim().to_string()).collect();
        t.sort();
        t
    };
    let (ot, nt) = (trig(old, o), trig(new, n));
    for t in nt.iter().filter(|t| !ot.contains(t)) {
        out.push(format!("trigger added: {t}"));
    }
    for t in ot.iter().filter(|t| !nt.contains(t)) {
        out.push(format!("trigger removed: {t}"));
    }
    let params = |g: &Graph, v: u32| -> HashMap<String, String> {
        g.params(v).iter().map(|(k, val)| (g.s(*k).to_string(), g.s(*val).to_string())).collect()
    };
    let (op, np) = (params(old, o), params(new, n));
    let mut keys: Vec<&String> = op.keys().chain(np.keys()).collect::<HashSet<_>>().into_iter().collect();
    keys.sort();
    for k in keys {
        match (op.get(k), np.get(k)) {
            (Some(a), Some(b)) if a != b => out.push(format!("param {k}: “{a}” → “{b}”")),
            (None, Some(b)) => out.push(format!("param added: {k}={b}")),
            (Some(a), None) => out.push(format!("param removed: {k}={a}")),
            _ => {}
        }
    }
    let arts = |g: &Graph, v: u32| -> Vec<String> { g.artifacts(v).iter().map(|a| g.s(*a).to_string()).collect() };
    let (oa, na) = (arts(old, o), arts(new, n));
    for a in na.iter().filter(|a| !oa.contains(a)) {
        out.push(format!("artifact rule added: {a}"));
    }
    for a in oa.iter().filter(|a| !na.contains(a)) {
        out.push(format!("artifact rule removed: {a}"));
    }
    out
}

pub fn compare(old: &Graph, new: &Graph) -> Diff {
    let mut state = vec![Change::Same; new.len()];
    let mut notes: HashMap<u32, Vec<String>> = HashMap::new();
    let mut removed = vec![];
    let mut repos = Repos::default();
    let (mut added, mut changed) = (0, 0);

    for v in 0..new.len() as u32 {
        match old.node_by_id(new.node_id(v)) {
            None => {
                state[v as usize] = Change::Added;
                added += 1;
                repos.of(new.node_repo(v)).added += 1;
            }
            Some(o) => {
                let diffs = compare_nodes(old, o, new, v);
                if !diffs.is_empty() {
                    state[v as usize] = Change::Changed;
                    changed += 1;
                    repos.of(new.node_repo(v)).changed += 1;
                    for s in diffs {
                        note(&mut notes, v, s);
                    }
                }
            }
        }
    }
    for o in 0..old.len() as u32 {
        if new.node_by_id(old.node_id(o)).is_none() {
            repos.of(old.node_repo(o)).removed += 1;
            removed.push(Removed {
                id: old.node_id(o).to_string(),
                label: old.node_label(o).to_string(),
                repo: old.node_repo(o).to_string(),
                kind: old.kind[o as usize],
            });
        }
    }
    for r in &new.repos {
        let name = new.s(r.name);
        repos.of(name).new_commit = new.s(r.commit).to_string();
    }
    for r in &old.repos {
        repos.of(old.s(r.name)).old_commit = old.s(r.commit).to_string();
    }

    let edges = |g: &Graph| -> HashSet<EdgeRef> {
        (0..g.full.len())
            .map(|e| EdgeRef { from: g.node_id(g.full.from[e]).to_string(), to: g.node_id(g.full.to[e]).to_string(), kind: g.full.kind[e] })
            .collect()
    };
    let (oe, ne) = (edges(old), edges(new));
    let mut edges_added: Vec<EdgeRef> = ne.difference(&oe).cloned().collect();
    let mut edges_removed: Vec<EdgeRef> = oe.difference(&ne).cloned().collect();
    edges_added.sort_by(|a, b| (&a.from, &a.to, a.kind).cmp(&(&b.from, &b.to, b.kind)));
    edges_removed.sort_by(|a, b| (&a.from, &a.to, a.kind).cmp(&(&b.from, &b.to, b.kind)));
    for e in &edges_added {
        // both ends existed before: the link itself is the news
        if let (Some(f), Some(t)) = (new.node_by_id(&e.from), new.node_by_id(&e.to)) {
            if old.node_by_id(&e.from).is_some() && old.node_by_id(&e.to).is_some() {
                note(&mut notes, f, format!("new link: {} → {}", e.kind.name(), new.node_label(t)));
                note(&mut notes, t, format!("new link: {} ← {}", e.kind.name(), new.node_label(f)));
            }
        }
    }
    for e in &edges_removed {
        if let (Some(f), Some(t)) = (new.node_by_id(&e.from), new.node_by_id(&e.to)) {
            note(&mut notes, f, format!("link gone: {} → {}", e.kind.name(), new.node_label(t)));
            note(&mut notes, t, format!("link gone: {} ← {}", e.kind.name(), new.node_label(f)));
        }
    }
    for &v in notes.keys() {
        if state[v as usize] == Change::Same {
            state[v as usize] = Change::Changed;
            changed += 1;
            // an edge-only change counts towards the repo, too
            repos.of(new.node_repo(v)).changed += 1;
        }
    }
    let mut repos: Vec<RepoDelta> =
        repos.0.into_values().filter(|r| r.added + r.removed + r.changed > 0 || r.old_commit != r.new_commit).collect();
    repos.sort_by(|a, b| a.repo.cmp(&b.repo));
    removed.sort_by(|a, b| (&a.repo, &a.label).cmp(&(&b.repo, &b.label)));
    Diff {
        old_generated: old.s(old.generated).to_string(),
        new_generated: new.s(new.generated).to_string(),
        state,
        notes,
        removed,
        edges_added,
        edges_removed,
        repos,
        added,
        changed,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::core::graph::tests::sample;

    fn modified(f: impl Fn(String) -> String) -> Graph {
        // the sample's nodes, without edges, as JSON that `f` may edit
        let g = sample();
        let mut buf = String::from(
            "{\"schemaVersion\":1,\"generated\":\"later\",\"repos\":[{\"name\":\"r1\",\"url\":\"u\",\"commit\":\"ffff\"}],\"nodes\":[",
        );
        let nodes: Vec<String> = (0..g.len() as u32)
            .map(|v| {
                format!(
                    "{{\"id\":\"{}\",\"kind\":\"{}\",\"repo\":\"{}\",\"label\":\"{}\"}}",
                    g.node_id(v),
                    match g.kind[v as usize] {
                        Kind::Build => "build",
                        Kind::Make => "make",
                        Kind::Image => "image",
                        Kind::External => "external",
                    },
                    g.node_repo(v),
                    g.node_label(v)
                )
            })
            .collect();
        buf.push_str(&nodes.join(","));
        buf.push_str("],\"edges\":[]}");
        Graph::from_slice(f(buf).as_bytes()).unwrap()
    }

    #[test]
    fn identical_graphs_have_no_diff() {
        assert!(compare(&sample(), &sample()).is_empty());
    }

    #[test]
    fn reports_added_removed_changed_and_links() {
        let old = sample();
        let new = modified(|s| {
            s.replace("\"label\":\"Lint\"", "\"label\":\"Lint all\"").replace(
                "{\"id\":\"bt:r2/Lonely\",\"kind\":\"build\",\"repo\":\"r2\",\"label\":\"Lonely\"}",
                "{\"id\":\"bt:r2/New\",\"kind\":\"build\",\"repo\":\"r2\",\"label\":\"New\"}",
            )
        });
        let d = compare(&old, &new);
        assert_eq!(d.added, 1);
        assert_eq!(d.removed.len(), 1);
        assert_eq!(d.removed[0].label, "Lonely");
        let lint = new.node_by_id("bt:r1/Lint").unwrap();
        assert_eq!(d.state[lint as usize], Change::Changed);
        assert!(d.notes[&lint].iter().any(|n| n.contains("label")));
        assert!(!d.edges_removed.is_empty(), "the new file has no edges");
        let r1 = d.repos.iter().find(|r| r.repo == "r1").unwrap();
        assert_eq!(r1.new_commit, "ffff");
        assert!(r1.old_commit.starts_with("abcdef"));
    }
}
