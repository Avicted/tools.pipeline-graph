//! The graph in memory: struct-of-arrays nodes, edge lists and CSR adjacency.
//! Loaded by streaming `graph.json`, so the peak is the graph, not the file twice.

use super::intern::{Interner, Sym};
use serde::de::{self, DeserializeSeed, IgnoredAny, MapAccess, SeqAccess, Visitor};
use serde::{Deserialize, Deserializer as _};
use std::borrow::Cow;
use std::collections::{HashMap, HashSet};
use std::fmt;
use std::io::Read;
use std::path::Path;

/// The newest `schemaVersion` this build understands.
pub const SCHEMA: u32 = 1;

#[derive(Clone, Copy, PartialEq, Eq, Debug, Hash)]
pub enum Kind {
    Build,
    External,
    Make,
    Image,
}

impl Kind {
    fn parse(s: &str) -> Kind {
        match s {
            "external" => Kind::External,
            "make" => Kind::Make,
            "image" => Kind::Image,
            _ => Kind::Build,
        }
    }
}

#[derive(Clone, Copy, PartialEq, Eq, Debug, Hash, PartialOrd, Ord)]
pub enum Ek {
    Snapshot,
    Artifact,
    Trigger,
    Runs,
    Depends,
    Calls,
    Produces,
    Consumes,
    Derives,
    RunsIn,
    Other,
}

impl Ek {
    pub const ALL: [Ek; 10] =
        [Ek::Snapshot, Ek::Artifact, Ek::Trigger, Ek::Produces, Ek::Consumes, Ek::Derives, Ek::RunsIn, Ek::Runs, Ek::Depends, Ek::Calls];

    fn parse(s: &str) -> Ek {
        match s {
            "snapshot" => Ek::Snapshot,
            "artifact" => Ek::Artifact,
            "trigger" => Ek::Trigger,
            "runs" => Ek::Runs,
            "depends" => Ek::Depends,
            "calls" => Ek::Calls,
            "produces" => Ek::Produces,
            "consumes" => Ek::Consumes,
            "derives" => Ek::Derives,
            "runsIn" => Ek::RunsIn,
            _ => Ek::Other,
        }
    }

    pub fn name(self) -> &'static str {
        match self {
            Ek::Snapshot => "snapshot dependency",
            Ek::Artifact => "artifact dependency",
            Ek::Trigger => "finish-build trigger",
            Ek::Runs => "step runs make target",
            Ek::Depends => "make prerequisite",
            Ek::Calls => "recursive make",
            Ek::Produces => "produces image",
            Ek::Consumes => "consumes image",
            Ek::Derives => "image built FROM image",
            Ek::RunsIn => "runs in container",
            Ek::Other => "other link",
        }
    }

    /// Edges that are part of the pipeline flow (everything but the make internals).
    pub fn is_flow(self) -> bool {
        !matches!(self, Ek::Runs | Ek::Depends | Ek::Calls)
    }

    /// Edges that go from a caller to the make target it invokes.
    pub fn is_make(self) -> bool {
        matches!(self, Ek::Runs | Ek::Depends | Ek::Calls)
    }
}

#[derive(Clone, Copy, Debug)]
pub struct Step {
    pub name: Sym,
    pub ty: Sym,
    pub summary: Sym,
    pub script: Sym,
    pub workdir: Sym,
}

#[derive(Clone, Copy, Debug)]
pub struct Trig {
    pub ty: Sym,
    pub detail: Sym,
}

#[derive(Clone, Copy, Debug)]
pub struct RepoInfo {
    pub name: Sym,
    pub url: Sym,
    pub commit: Sym,
}

/// A CI system the jobs come from, in its own words (`systems` in graph.json).
#[derive(Clone, Copy, Debug)]
pub struct SystemInfo {
    pub name: Sym,
    pub title: Sym,
    /// What one job is called: "build configuration", "workflow job".
    pub job: Sym,
    /// What a job's id is called: "TeamCity id", "job path".
    pub id: Sym,
}

/// Compressed sparse row adjacency: neighbours and edge ids of every node.
#[derive(Default)]
pub struct Adj {
    off: Vec<u32>,
    to: Vec<u32>,
    edge: Vec<u32>,
}

impl Adj {
    fn build(n: usize, key: &[u32], other: &[u32]) -> Adj {
        let mut off = vec![0u32; n + 1];
        for &k in key {
            off[k as usize + 1] += 1;
        }
        for i in 0..n {
            off[i + 1] += off[i];
        }
        let mut fill = off.clone();
        let mut to = vec![0u32; key.len()];
        let mut edge = vec![0u32; key.len()];
        for (e, &k) in key.iter().enumerate() {
            let p = fill[k as usize] as usize;
            fill[k as usize] += 1;
            to[p] = other[e];
            edge[p] = e as u32;
        }
        Adj { off, to, edge }
    }

    #[inline]
    pub fn nb(&self, v: u32) -> &[u32] {
        &self.to[self.off[v as usize] as usize..self.off[v as usize + 1] as usize]
    }

    #[inline]
    pub fn es(&self, v: u32) -> &[u32] {
        &self.edge[self.off[v as usize] as usize..self.off[v as usize + 1] as usize]
    }
}

/// A set of edges with adjacency in both directions. Edges point downstream
/// (the direction work flows), or from caller to callee for make.
#[derive(Default)]
pub struct Net {
    pub from: Vec<u32>,
    pub to: Vec<u32>,
    pub kind: Vec<Ek>,
    pub label: Vec<Sym>,
    pub out: Adj,
    pub inn: Adj,
}

impl Net {
    pub fn build(n: usize, from: Vec<u32>, to: Vec<u32>, kind: Vec<Ek>, label: Vec<Sym>) -> Net {
        let out = Adj::build(n, &from, &to);
        let inn = Adj::build(n, &to, &from);
        Net { from, to, kind, label, out, inn }
    }

    pub fn len(&self) -> usize {
        self.from.len()
    }

    pub fn is_empty(&self) -> bool {
        self.from.is_empty()
    }

    #[inline]
    pub fn adj(&self, down: bool) -> &Adj {
        if down {
            &self.out
        } else {
            &self.inn
        }
    }
}

/// A growable bit set over node indexes.
#[derive(Clone, Default)]
pub struct Bits(Vec<u64>);

impl Bits {
    pub fn new(n: usize) -> Bits {
        Bits(vec![0; n.div_ceil(64)])
    }
    #[inline]
    pub fn set(&mut self, i: u32) {
        self.0[(i >> 6) as usize] |= 1 << (i & 63);
    }
    #[inline]
    pub fn get(&self, i: u32) -> bool {
        self.0.get((i >> 6) as usize).is_some_and(|w| w & (1 << (i & 63)) != 0)
    }
    pub fn count(&self) -> usize {
        self.0.iter().map(|w| w.count_ones() as usize).sum()
    }
    pub fn intersect(&mut self, o: &Bits) {
        for (i, w) in self.0.iter_mut().enumerate() {
            *w &= o.0.get(i).copied().unwrap_or(0);
        }
    }
    pub fn iter(&self) -> impl Iterator<Item = u32> + '_ {
        self.0.iter().enumerate().flat_map(|(wi, &w)| {
            let mut w = w;
            std::iter::from_fn(move || {
                if w == 0 {
                    return None;
                }
                let b = w.trailing_zeros();
                w &= w - 1;
                Some(wi as u32 * 64 + b)
            })
        })
    }
}

/// Reusable scratch for graph walks, so repeated lineage queries do not allocate.
#[derive(Default)]
pub struct Scratch {
    stamp: Vec<u32>,
    cur: u32,
    stack: Vec<u32>,
}

impl Scratch {
    pub fn new(n: usize) -> Scratch {
        Scratch { stamp: vec![0; n], cur: 0, stack: Vec::new() }
    }

    /// Visits every node reachable from `roots` (roots included) once, downstream or upstream.
    pub fn walk(&mut self, net: &Net, roots: &[u32], down: bool, mut visit: impl FnMut(u32)) {
        self.cur = self.cur.wrapping_add(1);
        if self.cur == 0 {
            self.stamp.fill(0);
            self.cur = 1;
        }
        let adj = net.adj(down);
        self.stack.clear();
        for &r in roots {
            if self.stamp[r as usize] != self.cur {
                self.stamp[r as usize] = self.cur;
                self.stack.push(r);
            }
        }
        while let Some(v) = self.stack.pop() {
            visit(v);
            for &w in adj.nb(v) {
                if self.stamp[w as usize] != self.cur {
                    self.stamp[w as usize] = self.cur;
                    self.stack.push(w);
                }
            }
        }
    }

    pub fn reach(&mut self, net: &Net, roots: &[u32], down: bool) -> Bits {
        let mut b = Bits::new(self.stamp.len());
        self.walk(net, roots, down, |v| b.set(v));
        b
    }

    /// The nodes of `set` in the order a walk from `root` meets them (nearest first).
    pub fn order_from(&mut self, net: &Net, root: u32, down: bool, set: &Bits) -> Vec<u32> {
        let adj = net.adj(down);
        let mut dist = std::collections::HashMap::new();
        let mut q = std::collections::VecDeque::new();
        dist.insert(root, 0u32);
        q.push_back(root);
        while let Some(v) = q.pop_front() {
            let dv = dist[&v];
            for &w in adj.nb(v) {
                if set.get(w) && !dist.contains_key(&w) {
                    dist.insert(w, dv + 1);
                    q.push_back(w);
                }
            }
        }
        let mut out: Vec<u32> = dist.keys().copied().collect();
        out.sort_by_key(|v| (dist[v], *v));
        out
    }

    /// How many nodes are reachable from `root`, not counting it.
    pub fn count(&mut self, net: &Net, root: u32, down: bool) -> u32 {
        let mut n = 0u32;
        self.walk(net, &[root], down, |_| n += 1);
        n - 1
    }
}

pub struct Graph {
    pub strs: Interner,
    pub schema: u32,
    pub generated: Sym,
    pub repos: Vec<RepoInfo>,
    pub warnings: Vec<Sym>,
    /// Empty in files from before graph.json named its systems.
    pub systems: Vec<SystemInfo>,

    pub id: Vec<Sym>,
    pub kind: Vec<Kind>,
    pub repo: Vec<Sym>,
    pub label: Vec<Sym>,
    pub sub: Vec<Sym>,
    pub project: Vec<Sym>,
    pub source: Vec<Sym>,
    /// `SystemInfo::name` of the system each job comes from; empty for make targets and images.
    pub system: Vec<Sym>,
    pub composite: Vec<bool>,
    steps: Vec<Step>,
    step_off: Vec<u32>,
    trigs: Vec<Trig>,
    trig_off: Vec<u32>,
    params: Vec<(Sym, Sym)>,
    param_off: Vec<u32>,
    arts: Vec<Sym>,
    art_off: Vec<u32>,

    /// Every edge, including the make internals.
    pub full: Net,
    /// The pipeline flow: no make nodes; images produced or consumed inside make
    /// are re-attached to the builds that run those targets.
    pub flow: Net,
    node_of: Vec<u32>, // Sym -> node index (u32::MAX when the string is not a node id)
}

fn capitalise(s: &str) -> String {
    let mut c = s.chars();
    c.next().map(|f| f.to_uppercase().chain(c).collect()).unwrap_or_default()
}

#[derive(Debug)]
pub struct LoadError(pub String);

impl fmt::Display for LoadError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for LoadError {}

// ---- streaming JSON loader -------------------------------------------------

#[derive(Deserialize, Default)]
#[serde(default)]
struct RawStep {
    name: String,
    #[serde(rename = "type")]
    ty: String,
    summary: String,
    script: String,
    #[serde(rename = "workDir")]
    work_dir: String,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct RawTrig {
    #[serde(rename = "type")]
    ty: String,
    detail: String,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct RawNode {
    id: String,
    kind: String,
    repo: String,
    label: String,
    sub: String,
    project: String,
    composite: bool,
    steps: Vec<RawStep>,
    triggers: Vec<RawTrig>,
    params: HashMap<String, String>,
    artifacts: Vec<String>,
    source: String,
    system: String,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct RawSystem {
    name: String,
    title: String,
    job: String,
    id: String,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct RawEdge {
    from: String,
    to: String,
    kind: String,
    label: String,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct RawRepo {
    name: String,
    url: String,
    commit: String,
}

struct Builder {
    g: Graph,
    edges: Vec<(Sym, Sym, Ek, Sym)>,
}

struct NodesSeed<'a>(&'a mut Builder);
struct EdgesSeed<'a>(&'a mut Builder);

impl<'de> DeserializeSeed<'de> for NodesSeed<'_> {
    type Value = ();
    fn deserialize<D: de::Deserializer<'de>>(self, d: D) -> Result<(), D::Error> {
        struct V<'a>(&'a mut Builder);
        impl<'de> Visitor<'de> for V<'_> {
            type Value = ();
            fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
                f.write_str("a list of nodes")
            }
            fn visit_seq<A: SeqAccess<'de>>(self, mut seq: A) -> Result<(), A::Error> {
                while let Some(n) = seq.next_element::<RawNode>()? {
                    self.0.add_node(n);
                }
                Ok(())
            }
        }
        d.deserialize_seq(V(self.0))
    }
}

impl<'de> DeserializeSeed<'de> for EdgesSeed<'_> {
    type Value = ();
    fn deserialize<D: de::Deserializer<'de>>(self, d: D) -> Result<(), D::Error> {
        struct V<'a>(&'a mut Builder);
        impl<'de> Visitor<'de> for V<'_> {
            type Value = ();
            fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
                f.write_str("a list of edges")
            }
            fn visit_seq<A: SeqAccess<'de>>(self, mut seq: A) -> Result<(), A::Error> {
                while let Some(e) = seq.next_element::<RawEdge>()? {
                    let b = &mut *self.0;
                    let (f, t, l) = (b.g.strs.intern(&e.from), b.g.strs.intern(&e.to), b.g.strs.intern(&e.label));
                    b.edges.push((f, t, Ek::parse(&e.kind), l));
                }
                Ok(())
            }
        }
        d.deserialize_seq(V(self.0))
    }
}

impl<'de> Visitor<'de> for &mut Builder {
    type Value = ();
    fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str("a pipeline-graph graph.json object")
    }
    fn visit_map<A: MapAccess<'de>>(self, mut map: A) -> Result<(), A::Error> {
        while let Some(key) = map.next_key::<String>()? {
            match key.as_str() {
                "schemaVersion" => self.g.schema = map.next_value()?,
                "generated" => {
                    let s: String = map.next_value()?;
                    self.g.generated = self.g.strs.intern(&s);
                }
                "repos" => {
                    for r in map.next_value::<Vec<RawRepo>>()? {
                        let (name, url, commit) = (self.g.strs.intern(&r.name), self.g.strs.intern(&r.url), self.g.strs.intern(&r.commit));
                        self.g.repos.push(RepoInfo { name, url, commit });
                    }
                }
                "systems" => {
                    for x in map.next_value::<Vec<RawSystem>>()? {
                        let s = &mut self.g.strs;
                        let info =
                            SystemInfo { name: s.intern(&x.name), title: s.intern(&x.title), job: s.intern(&x.job), id: s.intern(&x.id) };
                        self.g.systems.push(info);
                    }
                }
                "warnings" => {
                    for w in map.next_value::<Vec<String>>()? {
                        let s = self.g.strs.intern(&w);
                        self.g.warnings.push(s);
                    }
                }
                "nodes" => map.next_value_seed(NodesSeed(&mut *self))?,
                "edges" => map.next_value_seed(EdgesSeed(&mut *self))?,
                _ => {
                    map.next_value::<IgnoredAny>()?;
                }
            }
        }
        Ok(())
    }
}

impl Builder {
    fn new() -> Builder {
        let mut strs = Interner::new();
        let generated = strs.intern("");
        let mut g = Graph {
            strs,
            schema: 0,
            generated,
            repos: vec![],
            warnings: vec![],
            systems: vec![],
            id: vec![],
            kind: vec![],
            repo: vec![],
            label: vec![],
            sub: vec![],
            project: vec![],
            source: vec![],
            system: vec![],
            composite: vec![],
            steps: vec![],
            step_off: vec![0],
            trigs: vec![],
            trig_off: vec![0],
            params: vec![],
            param_off: vec![0],
            arts: vec![],
            art_off: vec![0],
            full: Net::default(),
            flow: Net::default(),
            node_of: vec![],
        };
        g.generated = Sym(0);
        Builder { g, edges: vec![] }
    }

    fn add_node(&mut self, n: RawNode) {
        let g = &mut self.g;
        let s = &mut g.strs;
        g.id.push(s.intern(&n.id));
        g.kind.push(Kind::parse(&n.kind));
        g.repo.push(s.intern(&n.repo));
        g.label.push(s.intern(&n.label));
        g.sub.push(s.intern(&n.sub));
        g.project.push(s.intern(&n.project));
        g.source.push(s.intern(&n.source));
        g.system.push(s.intern(&n.system));
        g.composite.push(n.composite);
        for st in &n.steps {
            let step = Step {
                name: s.intern(&st.name),
                ty: s.intern(&st.ty),
                summary: s.intern(&st.summary),
                script: s.intern(&st.script),
                workdir: s.intern(&st.work_dir),
            };
            g.steps.push(step);
        }
        g.step_off.push(g.steps.len() as u32);
        for t in &n.triggers {
            let trig = Trig { ty: s.intern(&t.ty), detail: s.intern(&t.detail) };
            g.trigs.push(trig);
        }
        g.trig_off.push(g.trigs.len() as u32);
        let mut ps: Vec<_> = n.params.iter().collect();
        ps.sort();
        for (k, v) in ps {
            let kv = (s.intern(k), s.intern(v));
            g.params.push(kv);
        }
        g.param_off.push(g.params.len() as u32);
        for a in &n.artifacts {
            let a = s.intern(a);
            g.arts.push(a);
        }
        g.art_off.push(g.arts.len() as u32);
    }

    fn finish(mut self) -> Result<Graph, LoadError> {
        let mut g = self.g;
        if g.schema > SCHEMA {
            return Err(LoadError(format!(
                "graph.json has schemaVersion {}, this viewer understands up to {}: update pipeline-graph",
                g.schema, SCHEMA
            )));
        }
        let n = g.id.len();
        g.node_of = vec![u32::MAX; g.strs.len()];
        for (i, s) in g.id.iter().enumerate() {
            g.node_of[s.0 as usize] = i as u32;
        }
        let (mut from, mut to, mut kind, mut label) = (vec![], vec![], vec![], vec![]);
        for (f, t, k, l) in self.edges.drain(..) {
            let (a, b) = (g.node_of[f.0 as usize], g.node_of[t.0 as usize]);
            if a == u32::MAX || b == u32::MAX || a == b {
                continue;
            }
            from.push(a);
            to.push(b);
            kind.push(k);
            label.push(l);
        }
        g.flow = g.build_flow(n, &from, &to, &kind, &label);
        g.full = Net::build(n, from, to, kind, label);
        g.strs.shrink();
        Ok(g)
    }
}

impl Graph {
    pub fn from_reader(r: impl Read) -> Result<Graph, LoadError> {
        let mut b = Builder::new();
        let mut de = serde_json::Deserializer::from_reader(std::io::BufReader::with_capacity(1 << 20, r));
        de.deserialize_map(&mut b).map_err(|e| LoadError(format!("graph.json: {e}")))?;
        b.finish()
    }

    pub fn from_slice(b: &[u8]) -> Result<Graph, LoadError> {
        Graph::from_reader(b)
    }

    pub fn load(p: &Path) -> Result<Graph, LoadError> {
        let f = std::fs::File::open(p).map_err(|e| LoadError(format!("{}: {e}", p.display())))?;
        Graph::from_reader(f)
    }

    /// The pipeline flow without make nodes. Images produced or consumed in make
    /// are attached to every build that runs the target (the Go `WithoutMake`).
    fn build_flow(&self, n: usize, from: &[u32], to: &[u32], kind: &[Ek], label: &[Sym]) -> Net {
        let is_make = |v: u32| self.kind[v as usize] == Kind::Make;
        let mut seen: HashSet<(u32, u32, Ek)> = HashSet::new();
        let (mut ff, mut ft, mut fk, mut fl) = (vec![], vec![], vec![], vec![]);
        let mut push = |a: u32, b: u32, k: Ek, l: Sym, seen: &mut HashSet<(u32, u32, Ek)>| {
            if a != b && seen.insert((a, b, k)) {
                ff.push(a);
                ft.push(b);
                fk.push(k);
                fl.push(l);
            }
        };
        for e in 0..from.len() {
            if !is_make(from[e]) && !is_make(to[e]) {
                push(from[e], to[e], kind[e], label[e], &mut seen);
            }
        }
        // out/in adjacency over the make edges only
        let mut mk_from = vec![];
        let mut mk_to = vec![];
        let mut img_out: Vec<(u32, u32)> = vec![]; // make target -> image it produces
        let mut img_in: Vec<(u32, u32, Ek)> = vec![]; // image -> make target that consumes / runs in it
        for e in 0..from.len() {
            match kind[e] {
                k if k.is_make() => {
                    mk_from.push(from[e]);
                    mk_to.push(to[e]);
                }
                Ek::Produces if is_make(from[e]) => img_out.push((from[e], to[e])),
                Ek::Consumes | Ek::RunsIn if is_make(to[e]) => img_in.push((from[e], to[e], kind[e])),
                _ => {}
            }
        }
        let mk = Adj::build(n, &mk_from, &mk_to);
        let mut by_make_out: HashMap<u32, Vec<u32>> = HashMap::new();
        for (m, i) in img_out {
            by_make_out.entry(m).or_default().push(i);
        }
        let mut by_make_in: HashMap<u32, Vec<(u32, Ek)>> = HashMap::new();
        for (i, m, k) in img_in {
            by_make_in.entry(m).or_default().push((i, k));
        }
        let mut stamp = vec![0u32; n];
        let mut cur = 0u32;
        let mut stack: Vec<u32> = vec![];
        for b in 0..n as u32 {
            if self.kind[b as usize] != Kind::Build {
                continue;
            }
            cur += 1;
            stack.clear();
            for &t in mk.nb(b) {
                if stamp[t as usize] != cur {
                    stamp[t as usize] = cur;
                    stack.push(t);
                }
            }
            while let Some(m) = stack.pop() {
                for &i in by_make_out.get(&m).into_iter().flatten() {
                    push(b, i, Ek::Produces, Sym(0), &mut seen);
                }
                for &(i, k) in by_make_in.get(&m).into_iter().flatten() {
                    push(i, b, k, Sym(0), &mut seen);
                }
                for &t in mk.nb(m) {
                    if stamp[t as usize] != cur {
                        stamp[t as usize] = cur;
                        stack.push(t);
                    }
                }
            }
        }
        Net::build(n, ff, ft, fk, fl)
    }

    // ---- accessors ----

    pub fn len(&self) -> usize {
        self.id.len()
    }

    pub fn is_empty(&self) -> bool {
        self.id.is_empty()
    }

    #[inline]
    pub fn s(&self, s: Sym) -> &str {
        &self.strs[s]
    }

    pub fn node_by_id(&self, id: &str) -> Option<u32> {
        let sym = self.strs.lookup(id)?;
        match self.node_of.get(sym.0 as usize) {
            Some(&v) if v != u32::MAX => Some(v),
            _ => None,
        }
    }

    pub fn node_id(&self, v: u32) -> &str {
        self.s(self.id[v as usize])
    }
    pub fn node_label(&self, v: u32) -> &str {
        self.s(self.label[v as usize])
    }
    pub fn node_repo(&self, v: u32) -> &str {
        self.s(self.repo[v as usize])
    }

    pub fn steps(&self, v: u32) -> &[Step] {
        &self.steps[self.step_off[v as usize] as usize..self.step_off[v as usize + 1] as usize]
    }
    pub fn triggers(&self, v: u32) -> &[Trig] {
        &self.trigs[self.trig_off[v as usize] as usize..self.trig_off[v as usize + 1] as usize]
    }
    pub fn params(&self, v: u32) -> &[(Sym, Sym)] {
        &self.params[self.param_off[v as usize] as usize..self.param_off[v as usize + 1] as usize]
    }
    pub fn artifacts(&self, v: u32) -> &[Sym] {
        &self.arts[self.art_off[v as usize] as usize..self.art_off[v as usize + 1] as usize]
    }

    pub fn has_trigger(&self, v: u32, ty: &str) -> bool {
        self.triggers(v).iter().any(|t| self.s(t.ty) == ty)
    }

    pub fn repo_index(&self, name: &str) -> Option<usize> {
        self.repos.iter().position(|r| self.s(r.name) == name)
    }

    /// The net that answers lineage questions for `v`: make targets live only in the full graph.
    pub fn net_for(&self, v: u32) -> &Net {
        if self.kind[v as usize] == Kind::Make {
            &self.full
        } else {
            &self.flow
        }
    }

    /// The system a job comes from; none for make targets, images and files that do not name systems.
    pub fn system_of(&self, v: u32) -> Option<&SystemInfo> {
        let name = self.system[v as usize];
        self.systems.iter().find(|x| x.name == name && !self.s(name).is_empty())
    }

    /// What kind of node `v` is, in its CI system's words ("Build configuration",
    /// "Composite workflow job"); a file that names no systems gets the generic wording.
    pub fn kind_name(&self, v: u32) -> Cow<'static, str> {
        let job = self.system_of(v).map(|x| self.s(x.job)).filter(|j| !j.is_empty());
        match (self.kind[v as usize], job) {
            (Kind::Build, Some(j)) if self.composite[v as usize] => Cow::Owned(format!("Composite {j}")),
            (Kind::Build, Some(j)) => Cow::Owned(capitalise(j)),
            (Kind::External, Some(j)) => Cow::Owned(format!("External {j}")),
            (Kind::Build, None) if self.composite[v as usize] => Cow::Borrowed("Composite build"),
            (Kind::Build, None) => Cow::Borrowed("Build configuration"),
            (Kind::External, None) => Cow::Borrowed("External build"),
            (Kind::Make, _) => Cow::Borrowed("Make target"),
            (Kind::Image, _) => Cow::Borrowed("Docker image"),
        }
    }

    /// What a job's id is called in its system ("TeamCity id"); "CI id" when that is not known.
    pub fn id_name(&self, v: u32) -> String {
        match self.system_of(v).map(|x| self.s(x.id)) {
            Some(id) if !id.is_empty() => capitalise(id),
            _ => "CI id".to_string(),
        }
    }

    /// The jobs of this graph in the words of the systems they come from, plural:
    /// "build configurations and workflow jobs"; "pipeline jobs" when no system is named.
    pub fn jobs_name(&self) -> String {
        let mut terms: Vec<String> = vec![];
        for x in &self.systems {
            let j = self.s(x.job);
            if !j.is_empty() && !terms.iter().any(|t| t == j) {
                terms.push(j.to_string());
            }
        }
        let plural: Vec<String> = terms.iter().map(|t| format!("{t}s")).collect();
        match plural.as_slice() {
            [] => "pipeline jobs".to_string(),
            [one] => one.clone(),
            [init @ .., last] => format!("{} and {last}", init.join(", ")),
        }
    }
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;

    /// A small graph: lint -> base -> (img base) -> app build -> deploy, plus a make target run by `app`.
    pub fn sample() -> Graph {
        let json = r#"{
          "schemaVersion": 1, "generated": "2026-01-01 00:00 UTC",
          "repos": [{"name":"r1","url":"git@github.com:o/r1.git","commit":"abcdef0123456789"},{"name":"r2","url":"https://x/y"}],
          "nodes": [
            {"id":"bt:r1/Lint","kind":"build","repo":"r1","label":"Lint","sub":"R1_Lint","triggers":[{"type":"vcs"}],"steps":[{"type":"script","summary":"make lint","script":"make lint"}]},
            {"id":"bt:r1/Base","kind":"build","repo":"r1","label":"Base image","sub":"R1_Base","steps":[{"type":"script","summary":"docker build","script":"docker build -t reg/base ."}]},
            {"id":"img:reg/base","kind":"image","repo":"r1","label":"base","sub":"reg/base"},
            {"id":"bt:r2/App","kind":"build","repo":"r2","label":"App build & test","sub":"R2_App","params":{"b":"2","a":"1"},"artifacts":["dist/**"]},
            {"id":"bt:r2/Deploy","kind":"build","repo":"r2","label":"Deploy prod","sub":"R2_Deploy","triggers":[{"type":"finishBuild","detail":"App"}]},
            {"id":"mk:r2/Makefile:test","kind":"make","repo":"r2","label":"test","sub":"Makefile"},
            {"id":"mk:r2/Makefile:image","kind":"make","repo":"r2","label":"image","sub":"Makefile"},
            {"id":"img:reg/app","kind":"image","repo":"r2","label":"app","sub":"reg/app"},
            {"id":"bt:r2/Lonely","kind":"build","repo":"r2","label":"Lonely","sub":"R2_Lonely"}
          ],
          "edges": [
            {"from":"bt:r1/Lint","to":"bt:r1/Base","kind":"snapshot"},
            {"from":"bt:r1/Base","to":"img:reg/base","kind":"produces"},
            {"from":"img:reg/base","to":"bt:r2/App","kind":"consumes"},
            {"from":"bt:r2/App","to":"bt:r2/Deploy","kind":"snapshot"},
            {"from":"bt:r2/App","to":"mk:r2/Makefile:test","kind":"runs"},
            {"from":"bt:r2/App","to":"mk:r2/Makefile:image","kind":"runs"},
            {"from":"mk:r2/Makefile:image","to":"img:reg/app","kind":"produces"},
            {"from":"mk:r2/Makefile:image","to":"mk:r2/Makefile:test","kind":"depends"},
            {"from":"img:reg/base","to":"img:reg/app","kind":"derives"},
            {"from":"nope","to":"bt:r2/App","kind":"snapshot"}
          ],
          "warnings": ["r1: .teamcity/settings.kts:1: something odd"]
        }"#;
        Graph::from_slice(json.as_bytes()).unwrap()
    }

    #[test]
    fn names_follow_the_ci_system_of_each_job() {
        // `systems` comes after `nodes`, as pipeline-graph writes it
        let g = Graph::from_slice(
            br#"{"schemaVersion": 1,
              "nodes": [
                {"id":"bt:a/Build","kind":"build","repo":"a","label":"Build","sub":"A_Build","system":"teamcity"},
                {"id":"bt:a/All","kind":"build","repo":"a","label":"All","sub":"A_All","system":"teamcity","composite":true},
                {"id":"bt:b/ci/test","kind":"build","repo":"b","label":"test","sub":"b/ci.yml#test","system":"github"},
                {"id":"ext:X","kind":"external","label":"X","sub":"not in analysed repos","system":"github"},
                {"id":"mk:a/Makefile:t","kind":"make","repo":"a","label":"t","sub":"Makefile"}
              ],
              "systems": [
                {"name":"github","title":"GitHub Actions","job":"workflow job","id":"job path"},
                {"name":"teamcity","title":"TeamCity","job":"build configuration","id":"TeamCity id"}
              ]}"#,
        )
        .unwrap();
        let v = |id| g.node_by_id(id).unwrap();
        assert_eq!(g.kind_name(v("bt:a/Build")), "Build configuration");
        assert_eq!(g.kind_name(v("bt:a/All")), "Composite build configuration");
        assert_eq!(g.kind_name(v("bt:b/ci/test")), "Workflow job");
        assert_eq!(g.kind_name(v("ext:X")), "External workflow job");
        assert_eq!(g.kind_name(v("mk:a/Makefile:t")), "Make target");
        assert_eq!(g.id_name(v("bt:a/Build")), "TeamCity id");
        assert_eq!(g.id_name(v("bt:b/ci/test")), "Job path");
        assert_eq!(g.id_name(v("mk:a/Makefile:t")), "CI id", "no system, no term");
        assert_eq!(g.s(g.system_of(v("bt:b/ci/test")).unwrap().title), "GitHub Actions");
        assert!(g.system_of(v("mk:a/Makefile:t")).is_none());
        assert_eq!(g.jobs_name(), "workflow jobs and build configurations");
    }

    #[test]
    fn a_file_without_systems_keeps_the_generic_wording() {
        let g = sample();
        let v = |id| g.node_by_id(id).unwrap();
        assert!(g.systems.is_empty());
        assert_eq!(g.kind_name(v("bt:r2/App")), "Build configuration");
        assert_eq!(g.id_name(v("bt:r2/App")), "CI id");
        assert_eq!(g.jobs_name(), "pipeline jobs");
        assert!(g.system_of(v("bt:r2/App")).is_none());
    }

    #[test]
    fn loads_nodes_edges_and_drops_dangling() {
        let g = sample();
        assert_eq!(g.len(), 9);
        assert_eq!(g.full.len(), 9, "the edge from an unknown node is dropped");
        assert_eq!(g.warnings.len(), 1);
        let app = g.node_by_id("bt:r2/App").unwrap();
        assert_eq!(g.node_label(app), "App build & test");
        let keys: Vec<_> = g.params(app).iter().map(|(k, _)| g.s(*k)).collect();
        assert_eq!(keys, ["a", "b"], "params are sorted");
        assert_eq!(g.artifacts(app).len(), 1);
    }

    #[test]
    fn flow_reattaches_images_made_in_make() {
        let g = sample();
        let app = g.node_by_id("bt:r2/App").unwrap();
        let img = g.node_by_id("img:reg/app").unwrap();
        let out: Vec<_> = g.flow.out.nb(app).to_vec();
        assert!(out.contains(&img), "app produces its image through make");
        let mk = g.node_by_id("mk:r2/Makefile:image").unwrap();
        assert!(g.flow.out.nb(mk).is_empty() && g.flow.inn.nb(mk).is_empty(), "make nodes are not in the flow");
    }

    #[test]
    fn lineage_counts() {
        let g = sample();
        let mut sc = Scratch::new(g.len());
        let base = g.node_by_id("bt:r1/Base").unwrap();
        assert_eq!(sc.count(&g.flow, base, false), 1, "Lint");
        // Base -> img base -> App -> Deploy, and img base -> img app
        let down = sc.count(&g.flow, base, true);
        assert_eq!(down, 4, "img base, App, Deploy and img app");
    }

    #[test]
    fn route_order_follows_the_walk() {
        let g = sample();
        let mut sc = Scratch::new(g.len());
        let (lint, deploy) = (g.node_by_id("bt:r1/Lint").unwrap(), g.node_by_id("bt:r2/Deploy").unwrap());
        let mut set = sc.reach(&g.flow, &[lint], true);
        set.intersect(&sc.reach(&g.flow, &[deploy], false));
        let names: Vec<_> = sc.order_from(&g.flow, lint, true, &set).iter().map(|&v| g.node_label(v).to_string()).collect();
        assert_eq!(names, ["Lint", "Base image", "base", "App build & test", "Deploy prod"]);
    }

    #[test]
    fn rejects_a_newer_schema() {
        let e = Graph::from_slice(br#"{"schemaVersion": 99}"#).err().unwrap();
        assert!(e.0.contains("update pipeline-graph"), "{e}");
    }

    #[test]
    fn bits_iterate_in_order() {
        let mut b = Bits::new(200);
        for i in [3, 64, 150, 199] {
            b.set(i);
        }
        assert_eq!(b.iter().collect::<Vec<_>>(), vec![3, 64, 150, 199]);
        assert_eq!(b.count(), 4);
    }
}
