//! Scenes: what the map shows, laid out. A scene is a selection of the graph (all of it, a
//! repository, one job's lineage, ...) with a position for every node and a route for every
//! edge. Building one needs no window, so it is benchmarked and tested headless like the rest
//! of `core`; the interface only draws the result.

use super::derive::{ENTRY_VCS, NONE};
use super::graph::{Ek, Kind, Net, Scratch};
use super::layout::{self, Opts};
use super::model::Model;
use super::spatial::{Grid, Rect};
use std::collections::HashMap;

/// What the canvas shows.
#[derive(Clone, PartialEq, Eq, Hash, Debug)]
pub enum Scene {
    /// One box per repository, with counted links between them.
    Repos,
    /// A repository's jobs and what touches them from outside.
    Repo(u32),
    /// Every job and image, in repository lanes.
    All,
    /// One node's upstream and downstream, laid out on its own.
    Focus(u32),
    /// Everything downstream of a repository's commit-triggered jobs.
    Flow(u32),
}

pub struct SNode {
    /// A graph node, or in `Scene::Repos` a repository index.
    pub gid: u32,
    pub rect: Rect,
}

pub struct SEdge {
    pub a: u32,
    pub b: u32,
    pub kind: Ek,
    pub pts: Vec<[f32; 2]>,
    pub bbox: Rect,
    pub weight: u32,
}

pub struct SceneLayout {
    pub nodes: Vec<SNode>,
    pub edges: Vec<SEdge>,
    pub lanes: Vec<(String, Rect)>,
    pub w: f32,
    pub h: f32,
    pub(crate) ngrid: Grid,
    pub(crate) egrid: Grid,
    pub index_of: HashMap<u32, u32>,
    pub aggregated: bool,
    /// How many nodes the scene has before it was cut down to the limit.
    pub total: usize,
    /// Title and sub line of aggregated nodes.
    pub agg_text: Vec<(String, String)>,
    pub kinds: Vec<Ek>,
}

fn card_size(m: &Model, v: u32) -> (f32, f32) {
    let g = &m.g;
    let title = g.node_label(v).chars().count() as f32;
    let sub = g.s(g.sub[v as usize]).chars().count() as f32;
    let w = (title * 7.2 + 32.0).max(sub * 5.6 + 28.0).clamp(150.0, 250.0);
    let h = match g.kind[v as usize] {
        Kind::Build | Kind::External => 58.0,
        _ => 46.0,
    };
    (w, h)
}

/// How many nodes a scene shows before it keeps only the nearest ones.
pub const DEFAULT_LIMIT: usize = 500;

/// Past this many nodes the first map is the repositories, not every job at once.
pub const BIG_GRAPH: usize = 1500;

impl Scene {
    /// The scene a graph of `nodes` nodes opens on.
    pub fn first_for(nodes: usize) -> Scene {
        if nodes > BIG_GRAPH {
            Scene::Repos
        } else {
            Scene::All
        }
    }
}

/// The `limit` nodes closest to `roots`, walking up and/or down the flow, nearest first.
fn nearest(net: &Net, roots: &[u32], up: bool, down: bool, limit: usize) -> Vec<u32> {
    let mut seen: std::collections::HashSet<u32> = roots.iter().copied().collect();
    let mut out: Vec<u32> = roots.to_vec();
    let mut qs: [std::collections::VecDeque<u32>; 2] = [roots.iter().copied().collect(), roots.iter().copied().collect()];
    let dirs = [(down, true), (up, false)];
    'outer: loop {
        let mut progressed = false;
        for (qi, &(on, downward)) in dirs.iter().enumerate() {
            if !on {
                continue;
            }
            if let Some(v) = qs[qi].pop_front() {
                progressed = true;
                for &w in net.adj(downward).nb(v) {
                    if seen.insert(w) {
                        out.push(w);
                        qs[qi].push_back(w);
                        if out.len() >= limit {
                            break 'outer;
                        }
                    }
                }
            }
        }
        if !progressed {
            break;
        }
    }
    out
}

pub fn build_scene(m: &Model, scene: &Scene, limit: usize) -> SceneLayout {
    let (g, d) = (&m.g, &m.d);
    let n_repo = g.repos.len() as u32;
    let mut members: Vec<u32> = vec![];
    let mut edges: Vec<(u32, u32, Ek, u32)> = vec![]; // (scene a, scene b, kind, weight)
    let mut sizes: Vec<(f32, f32)> = vec![];
    let mut lanes_of: Vec<u32> = vec![];
    let mut agg_text = vec![];
    let mut lane_names: Vec<String> = vec![];
    let mut with_lanes = true;
    let mut aggregated = false;
    let mut index_of: HashMap<u32, u32> = HashMap::new();

    let take = |members: &mut Vec<u32>, index_of: &mut HashMap<u32, u32>, v: u32| {
        if let std::collections::hash_map::Entry::Vacant(e) = index_of.entry(v) {
            e.insert(members.len() as u32);
            members.push(v);
        }
    };
    let mut sc = Scratch::new(g.len());
    match scene {
        Scene::Repos => {
            aggregated = true;
            with_lanes = false;
            let mut counts: HashMap<(u32, u32), u32> = HashMap::new();
            let ext = n_repo;
            let rep = |v: u32| if d.repo_of[v as usize] == NONE { ext } else { d.repo_of[v as usize] };
            for e in 0..g.flow.len() {
                let (a, b) = (rep(g.flow.from[e]), rep(g.flow.to[e]));
                if a != b {
                    *counts.entry((a, b)).or_default() += 1;
                }
            }
            let mut used = vec![false; n_repo as usize + 1];
            for &(a, b) in counts.keys() {
                used[a as usize] = true;
                used[b as usize] = true;
            }
            for (i, r) in d.repos.iter().enumerate() {
                if r.builds + r.images > 0 || used[i] {
                    index_of.insert(i as u32, members.len() as u32);
                    members.push(i as u32);
                    agg_text.push((g.s(r.name).to_string(), format!("{} jobs · {} images · {} make", r.builds, r.images, r.makes)));
                    sizes.push((250.0, 74.0));
                }
            }
            if used[ext as usize] || d.totals.externals > 0 {
                index_of.insert(ext, members.len() as u32);
                members.push(ext);
                agg_text.push(("External".into(), format!("{} builds outside the analysed repos", d.totals.externals)));
                sizes.push((250.0, 74.0));
            }
            let mut keys: Vec<_> = counts.into_iter().collect();
            keys.sort();
            for ((a, b), w) in keys {
                if let (Some(&sa), Some(&sb)) = (index_of.get(&a), index_of.get(&b)) {
                    edges.push((sa, sb, Ek::Other, w));
                }
            }
            lanes_of = vec![0; members.len()];
            lane_names = vec![String::new()];
        }
        Scene::All | Scene::Repo(_) => {
            if let Scene::Repo(r) = scene {
                for v in 0..g.len() as u32 {
                    if g.kind[v as usize] != Kind::Make && d.repo_of[v as usize] == *r {
                        take(&mut members, &mut index_of, v);
                    }
                }
                let own = members.clone();
                for v in own {
                    for &w in g.flow.out.nb(v).iter().chain(g.flow.inn.nb(v)) {
                        take(&mut members, &mut index_of, w);
                    }
                }
            } else {
                for v in 0..g.len() as u32 {
                    if g.kind[v as usize] != Kind::Make {
                        take(&mut members, &mut index_of, v);
                    }
                }
            }
        }
        Scene::Focus(v) => {
            with_lanes = false;
            let net = g.net_for(*v);
            let up = sc.reach(net, &[*v], false);
            let down = sc.reach(net, &[*v], true);
            for x in up.iter().chain(down.iter()) {
                take(&mut members, &mut index_of, x);
            }
            members.sort_unstable();
            index_of.clear();
            for (i, &x) in members.iter().enumerate() {
                index_of.insert(x, i as u32);
            }
        }
        Scene::Flow(r) => {
            with_lanes = false;
            let roots: Vec<u32> = (0..g.len() as u32)
                .filter(|&v| g.kind[v as usize] == Kind::Build && d.repo_of[v as usize] == *r && d.entry[v as usize] & ENTRY_VCS != 0)
                .collect();
            let down = sc.reach(&g.flow, &roots, true);
            for x in down.iter() {
                take(&mut members, &mut index_of, x);
            }
        }
    }

    let total = members.len();
    if !aggregated && total > limit {
        let importance = |v: &u32| std::cmp::Reverse(d.down[*v as usize] + d.up[*v as usize]);
        let mut keep: Vec<u32> = match scene {
            Scene::Focus(v) => nearest(g.net_for(*v), &[*v], true, true, limit),
            Scene::Flow(r) => {
                let roots: Vec<u32> = (0..g.len() as u32)
                    .filter(|&v| g.kind[v as usize] == Kind::Build && d.repo_of[v as usize] == *r && d.entry[v as usize] & ENTRY_VCS != 0)
                    .collect();
                nearest(&g.flow, &roots, false, true, limit)
            }
            Scene::Repo(r) => {
                let (mut own, mut other): (Vec<u32>, Vec<u32>) = members.iter().partition(|&&v| d.repo_of[v as usize] == *r);
                own.sort_by_key(importance);
                other.sort_by_key(importance);
                own.truncate(limit);
                other.truncate(limit.saturating_sub(own.len()).max(limit / 5));
                own.extend(other);
                own
            }
            _ => {
                let mut all = members.clone();
                all.sort_by_key(importance);
                all.truncate(limit);
                all
            }
        };
        keep.sort_unstable();
        keep.dedup();
        members = keep;
        index_of.clear();
        for (i, &x) in members.iter().enumerate() {
            index_of.insert(x, i as u32);
        }
    }
    if !aggregated {
        let net = match scene {
            Scene::Focus(v) => g.net_for(*v),
            _ => &g.flow,
        };
        for (i, &v) in members.iter().enumerate() {
            sizes.push(card_size(m, v));
            lanes_of.push(if with_lanes {
                let r = d.repo_of[v as usize];
                if r == NONE {
                    n_repo
                } else {
                    r
                }
            } else {
                0
            });
            for &e in net.out.es(v) {
                let w = net.to[e as usize];
                if let Some(&j) = index_of.get(&w) {
                    edges.push((i as u32, j, net.kind[e as usize], 1));
                }
            }
        }
        lane_names = if with_lanes {
            g.repos.iter().map(|r| g.s(r.name).to_string()).chain(std::iter::once("External".to_string())).collect()
        } else {
            vec![String::new()]
        };
    }

    let pairs: Vec<(u32, u32)> = edges.iter().map(|e| (e.0, e.1)).collect();
    let mut opts = Opts { layer_gap: 64.0, node_gap: 14.0, margin: 20.0, lane_gap: 12.0, ..Opts::default() };
    opts.top_offset = if with_lanes { 8.0 } else { 0.0 };
    if with_lanes {
        opts.lane_header = 8.0;
        opts.lane_pad = 16.0;
        opts.lane_top = 56.0;
    } else {
        opts.lane_header = 0.0;
        opts.lane_pad = 8.0;
    }
    let out = layout::compute(&layout::Input { size: &sizes, lane: &lanes_of, lane_names: &lane_names, edges: &pairs }, &opts);

    let mut ngrid = Grid::new(out.w, out.h);
    for (i, r) in out.boxes.iter().enumerate() {
        ngrid.insert(i as u32, r);
    }
    let mut egrid = Grid::new(out.w, out.h);
    let mut sedges = Vec::with_capacity(edges.len());
    let mut kinds: Vec<Ek> = vec![];
    for (i, e) in edges.iter().enumerate() {
        let pts = out.routes[i].clone();
        if pts.len() < 2 {
            continue;
        }
        let (mut x0, mut y0, mut x1, mut y1) = (f32::MAX, f32::MAX, f32::MIN, f32::MIN);
        for p in &pts {
            x0 = x0.min(p[0]);
            y0 = y0.min(p[1]);
            x1 = x1.max(p[0]);
            y1 = y1.max(p[1]);
        }
        let bbox = Rect { x0, y0, x1, y1 };
        egrid.insert(sedges.len() as u32, &bbox);
        if !kinds.contains(&e.2) {
            kinds.push(e.2);
        }
        sedges.push(SEdge { a: e.0, b: e.1, kind: e.2, pts, bbox, weight: e.3 });
    }
    let lanes = if with_lanes { out.lanes.iter().map(|(i, r)| (lane_names[*i as usize].clone(), *r)).collect() } else { vec![] };
    SceneLayout {
        nodes: members.iter().zip(&out.boxes).map(|(&gid, &rect)| SNode { gid, rect }).collect(),
        edges: sedges,
        lanes,
        w: out.w,
        h: out.h,
        ngrid,
        egrid,
        index_of,
        aggregated,
        total: total.max(members.len()),
        agg_text,
        kinds,
    }
}
