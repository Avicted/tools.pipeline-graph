//! Facts computed once after load: job classes, transitive reach, entry points,
//! chain depth and per-repository statistics. Everything here is descriptive.

use super::graph::{Ek, Graph, Kind, Net, Scratch};
use super::intern::Sym;

#[derive(Clone, Copy, PartialEq, Eq, Debug, Hash)]
pub enum Class {
    None,
    Build,
    Test,
    Lint,
    Scan,
    Package,
    Publish,
    Deploy,
    Other,
}

impl Class {
    /// The classes a job can have, in the order they are listed in the UI.
    pub const STAGES: [Class; 8] =
        [Class::Build, Class::Lint, Class::Test, Class::Scan, Class::Package, Class::Publish, Class::Deploy, Class::Other];

    pub fn name(self) -> &'static str {
        match self {
            Class::None => "",
            Class::Build => "build",
            Class::Test => "test",
            Class::Lint => "lint",
            Class::Scan => "scan",
            Class::Package => "package",
            Class::Publish => "publish",
            Class::Deploy => "deploy",
            Class::Other => "other",
        }
    }

    pub fn parse(s: &str) -> Option<Class> {
        Class::STAGES.iter().copied().find(|c| c.name() == s)
    }

    pub fn idx(self) -> usize {
        Class::STAGES.iter().position(|&c| c == self).unwrap_or(7)
    }
}

// A trailing '=' means the whole word, otherwise the word may continue ("test" also matches "tests").
const KEYWORDS: [(Class, &[&str]); 7] = [
    (Class::Deploy, &["deploy", "rollout", "promote", "helm=", "kubectl=", "apply=", "plan=", "terraform="]),
    (Class::Publish, &["publish", "push=", "upload", "releas", "artifactory=", "changelog="]),
    (Class::Scan, &["scan", "cve=", "trivy=", "grype=", "snyk=", "sast=", "dast=", "vulnerab", "sonar"]),
    (Class::Test, &["test", "e2e=", "integration", "pact=", "verify=", "smoke", "pytest=", "jest=", "junit=", "playwright=", "coverage="]),
    (Class::Lint, &["lint", "fmt=", "format", "validate=", "vet=", "shellcheck=", "hadolint=", "eslint=", "check="]),
    (Class::Package, &["image", "docker=", "package", "bundle", "container", "assemble", "archive"]),
    (Class::Build, &["build", "compile", "gradle=", "maven=", "mvn=", "cargo="]),
];

fn keyword(text: &str) -> Option<(Class, String)> {
    let lower = text.to_lowercase();
    let toks: Vec<&str> = lower.split(|c: char| !c.is_alphanumeric()).filter(|t| !t.is_empty()).collect();
    for (class, kws) in KEYWORDS {
        for kw in kws {
            let (stem, exact) = match kw.strip_suffix('=') {
                Some(s) => (s, true),
                None => (*kw, false),
            };
            if let Some(t) = toks.iter().find(|t| if exact { **t == stem } else { t.starts_with(stem) }) {
                return Some((class, t.to_string()));
            }
        }
    }
    None
}

/// The class of a build and why: its name first, then what its steps run.
pub fn classify(g: &Graph, v: u32) -> (Class, String) {
    if g.kind[v as usize] != Kind::Build {
        return (Class::None, String::new());
    }
    if let Some((c, kw)) = keyword(g.node_label(v)) {
        return (c, format!("the name has “{kw}”"));
    }
    for st in g.steps(v) {
        let text = format!("{} {}", g.s(st.name), g.s(st.summary));
        if let Some((c, kw)) = keyword(&text) {
            let what = if st.summary == Sym(0) { kw } else { g.s(st.summary).to_string() };
            return (c, format!("a step runs “{what}”"));
        }
    }
    for &e in g.full.out.es(v) {
        if g.full.kind[e as usize] == Ek::Runs {
            if let Some((c, kw)) = keyword(g.node_label(g.full.to[e as usize])) {
                return (c, format!("it runs make target “{kw}”"));
            }
        }
    }
    for st in g.steps(v) {
        let script: String = g.s(st.script).chars().take(600).collect();
        if let Some((c, kw)) = keyword(&script) {
            return (c, format!("a script uses “{kw}”"));
        }
    }
    (Class::Other, "nothing in its name or steps matches a known stage".to_string())
}

pub const ENTRY_VCS: u8 = 1;
pub const ENTRY_SCHEDULE: u8 = 2;
pub const ENTRY_CHAINED: u8 = 4; // finish-build trigger
pub const ENTRY_MANUAL: u8 = 8;
pub const ENTRY_DEP: u8 = 16; // no trigger of its own, but upstream jobs
pub const ENTRY_NONE: u8 = 32; // no trigger and nothing upstream

#[derive(Clone, Debug, Default)]
pub struct RepoStats {
    pub name: Sym,
    pub builds: u32,
    pub makes: u32,
    pub images: u32,
    pub externals: u32,
    pub vcs: u32,
    pub scheduled: u32,
    pub chained: u32,
    pub manual: u32,
    pub dep_only: u32,
    pub classes: [u32; 8],
    pub isolated: u32,
    pub undefined_make: u32,
    pub warnings: u32,
    pub deploys: u32,
    pub deploys_no_test: u32,
    pub longest: u32,
    pub longest_path: Vec<u32>,
    pub containers: u32, // images some job runs inside
    pub cyclic: u32,     // jobs that wait on each other, directly or through others
    pub unused: u32,     // images nothing runs in, derives from or consumes
}

pub struct Derived {
    pub class: Vec<Class>,
    pub up: Vec<u32>,
    pub down: Vec<u32>,
    pub entry: Vec<u8>,
    pub isolated: Vec<bool>,
    pub no_test_up: Vec<bool>,
    /// Images some job or target runs inside.
    pub container: Vec<bool>,
    /// On a dependency cycle: waits (transitively) on itself, so it has no start order.
    pub cyclic: Vec<bool>,
    /// Images with nothing downstream: no job runs in them and no image derives from them.
    pub unused: Vec<bool>,
    /// The load-bearing jobs: the builds that the most others transitively depend on, most first.
    pub hubs: Vec<u32>,
    pub depth: Vec<u32>, // longest downstream chain starting here, in jobs
    pub next: Vec<u32>,  // the next job on that chain
    pub repo_of: Vec<u32>,
    pub repos: Vec<RepoStats>,
    pub totals: RepoStats,
}

pub const NONE: u32 = u32::MAX;

/// Topological order of the flow graph (Kahn); nodes on cycles are left out.
fn topo(net: &Net, n: usize) -> Vec<u32> {
    let mut indeg: Vec<u32> = (0..n as u32).map(|v| net.inn.nb(v).len() as u32).collect();
    let mut order: Vec<u32> = (0..n as u32).filter(|&v| indeg[v as usize] == 0).collect();
    let mut i = 0;
    while i < order.len() {
        let v = order[i];
        i += 1;
        for &w in net.out.nb(v) {
            indeg[w as usize] -= 1;
            if indeg[w as usize] == 0 {
                order.push(w);
            }
        }
    }
    order
}

/// How many load-bearing jobs are listed, and the fewest jobs that must hang off one to count.
pub const HUBS: usize = 10;
pub const HUB_MIN: u32 = 3;

/// Nodes on a cycle of the flow graph: members of a strongly connected component of
/// two or more nodes, or with an edge to themselves (Tarjan, iterative).
fn cycles(net: &Net, n: usize) -> Vec<bool> {
    const UNSEEN: u32 = u32::MAX;
    let mut index = vec![UNSEEN; n];
    let mut low = vec![0u32; n];
    let mut on = vec![false; n];
    let mut stack: Vec<u32> = Vec::new();
    let mut out = vec![false; n];
    let mut counter = 0u32;
    for root in 0..n as u32 {
        if index[root as usize] != UNSEEN {
            continue;
        }
        let mut work: Vec<(u32, usize)> = vec![(root, 0)];
        index[root as usize] = counter;
        low[root as usize] = counter;
        counter += 1;
        stack.push(root);
        on[root as usize] = true;
        while let Some(&mut (v, ref mut i)) = work.last_mut() {
            let nb = net.out.nb(v);
            if *i < nb.len() {
                let w = nb[*i];
                *i += 1;
                if w == v {
                    out[v as usize] = true;
                }
                if index[w as usize] == UNSEEN {
                    index[w as usize] = counter;
                    low[w as usize] = counter;
                    counter += 1;
                    stack.push(w);
                    on[w as usize] = true;
                    work.push((w, 0));
                } else if on[w as usize] {
                    low[v as usize] = low[v as usize].min(index[w as usize]);
                }
            } else {
                work.pop();
                if let Some(&(parent, _)) = work.last() {
                    low[parent as usize] = low[parent as usize].min(low[v as usize]);
                }
                if low[v as usize] == index[v as usize] {
                    let top = stack.iter().rposition(|&x| x == v).unwrap_or(0);
                    let members = stack.split_off(top);
                    for &m in &members {
                        on[m as usize] = false;
                        if members.len() > 1 {
                            out[m as usize] = true;
                        }
                    }
                }
            }
        }
    }
    out
}

impl Derived {
    pub fn compute(g: &Graph) -> Derived {
        let n = g.len();
        let class: Vec<Class> = (0..n as u32).map(|v| classify(g, v).0).collect();
        let repo_of: Vec<u32> = (0..n).map(|v| g.repos.iter().position(|r| r.name == g.repo[v]).map_or(NONE, |i| i as u32)).collect();

        // transitive reach, in parallel: every node has its own query
        let mut up = vec![0u32; n];
        let mut down = vec![0u32; n];
        let threads = std::thread::available_parallelism().map_or(4, |x| x.get()).min(16);
        let chunk = n.div_ceil(threads).max(1);
        std::thread::scope(|s| {
            for ((ups, downs), base) in up.chunks_mut(chunk).zip(down.chunks_mut(chunk)).zip((0..).step_by(chunk)) {
                s.spawn(move || {
                    let mut sc = Scratch::new(n);
                    for (k, (u, d)) in ups.iter_mut().zip(downs.iter_mut()).enumerate() {
                        let v = (base + k) as u32;
                        let net = g.net_for(v);
                        *u = sc.count(net, v, false);
                        *d = sc.count(net, v, true);
                    }
                });
            }
        });

        let mut entry = vec![0u8; n];
        let mut isolated = vec![false; n];
        for v in 0..n as u32 {
            if g.kind[v as usize] != Kind::Build {
                continue;
            }
            let mut e = 0;
            for t in g.triggers(v) {
                e |= match g.s(t.ty) {
                    "vcs" => ENTRY_VCS,
                    "schedule" => ENTRY_SCHEDULE,
                    "finishBuild" => ENTRY_CHAINED,
                    "manual" => ENTRY_MANUAL,
                    _ => 0,
                };
            }
            let has_up = !g.flow.inn.nb(v).is_empty();
            if e & (ENTRY_VCS | ENTRY_SCHEDULE | ENTRY_MANUAL) == 0 && e & ENTRY_CHAINED == 0 {
                e |= if has_up { ENTRY_DEP } else { ENTRY_NONE };
            }
            entry[v as usize] = e;
            isolated[v as usize] = !has_up && g.flow.out.nb(v).is_empty();
        }

        // chain depth and "is there a test before this deploy", in topological order
        let order = topo(&g.flow, n);
        let mut depth = vec![1u32; n];
        let mut next = vec![NONE; n];
        for &v in order.iter().rev() {
            for &w in g.flow.out.nb(v) {
                if depth[w as usize] + 1 > depth[v as usize] {
                    depth[v as usize] = depth[w as usize] + 1;
                    next[v as usize] = w;
                }
            }
        }
        let mut test_up = vec![false; n];
        for &v in &order {
            if class[v as usize] == Class::Test || test_up[v as usize] {
                for &w in g.flow.out.nb(v) {
                    test_up[w as usize] = true;
                }
            }
        }
        let no_test_up: Vec<bool> = (0..n).map(|v| class[v] == Class::Deploy && !test_up[v]).collect();
        let cyclic = cycles(&g.flow, n);
        let unused: Vec<bool> = (0..n as u32).map(|v| g.kind[v as usize] == Kind::Image && g.full.out.nb(v).is_empty()).collect();
        let mut hubs: Vec<u32> = (0..n as u32).filter(|&v| g.kind[v as usize] == Kind::Build && down[v as usize] >= HUB_MIN).collect();
        hubs.sort_by_key(|&v| (std::cmp::Reverse(down[v as usize]), v));
        hubs.truncate(HUBS);

        // statistics
        let mut containers = vec![false; n];
        for e in 0..g.full.len() {
            if g.full.kind[e] == Ek::RunsIn {
                containers[g.full.from[e] as usize] = true;
            }
        }
        let mut repos: Vec<RepoStats> = g.repos.iter().map(|r| RepoStats { name: r.name, ..Default::default() }).collect();
        let mut totals = RepoStats::default();
        let warn_repo: Vec<Option<usize>> = g
            .warnings
            .iter()
            .map(|&w| {
                let w = g.s(w);
                g.repos.iter().position(|r| w.starts_with(&format!("{}: ", g.s(r.name))))
            })
            .collect();
        for v in 0..n {
            let ri = repo_of[v];
            let add = |r: &mut RepoStats| match g.kind[v] {
                Kind::Build => {
                    r.builds += 1;
                    r.classes[class[v].idx()] += 1;
                    let e = entry[v];
                    r.vcs += (e & ENTRY_VCS != 0) as u32;
                    r.scheduled += (e & ENTRY_SCHEDULE != 0) as u32;
                    r.chained += (e & ENTRY_CHAINED != 0) as u32;
                    r.manual += (e & ENTRY_MANUAL != 0) as u32;
                    r.dep_only += (e & (ENTRY_DEP | ENTRY_NONE) != 0) as u32;
                    r.isolated += isolated[v] as u32;
                    r.deploys += (class[v] == Class::Deploy) as u32;
                    r.deploys_no_test += no_test_up[v] as u32;
                    r.cyclic += cyclic[v] as u32;
                }
                Kind::Make => {
                    r.makes += 1;
                    r.undefined_make += g.s(g.sub[v]).ends_with("(no rule)") as u32;
                }
                Kind::Image => {
                    r.images += 1;
                    r.containers += containers[v] as u32;
                    r.unused += unused[v] as u32;
                }
                Kind::External => r.externals += 1,
            };
            add(&mut totals);
            if ri != NONE {
                add(&mut repos[ri as usize]);
            }
        }
        for r in &warn_repo {
            totals.warnings += 1;
            if let Some(ri) = *r {
                repos[ri].warnings += 1;
            }
        }
        for v in 0..n as u32 {
            if g.kind[v as usize] != Kind::Build || entry[v as usize] & ENTRY_VCS == 0 {
                continue;
            }
            let ri = repo_of[v as usize];
            if ri == NONE {
                continue;
            }
            let r = &mut repos[ri as usize];
            if depth[v as usize] > r.longest || r.longest_path.is_empty() {
                r.longest = depth[v as usize];
                let mut path = vec![v];
                let mut c = v;
                while next[c as usize] != NONE && path.len() < 10_000 {
                    c = next[c as usize];
                    path.push(c);
                }
                r.longest_path = path;
            }
        }
        totals.longest = repos.iter().map(|r| r.longest).max().unwrap_or(0);
        Derived {
            class,
            up,
            down,
            entry,
            isolated,
            no_test_up,
            container: containers,
            cyclic,
            unused,
            hubs,
            depth,
            next,
            repo_of,
            repos,
            totals,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::core::graph::tests::sample;

    #[test]
    fn classifies_by_name_then_steps() {
        let g = sample();
        let c = |id: &str| classify(&g, g.node_by_id(id).unwrap());
        assert_eq!(c("bt:r1/Lint").0, Class::Lint);
        assert_eq!(c("bt:r1/Base").0, Class::Package);
        assert_eq!(c("bt:r2/App").0, Class::Test, "test wins over build in a name like “build & test”");
        assert_eq!(c("bt:r2/Deploy").0, Class::Deploy);
        assert_eq!(c("bt:r2/Lonely").0, Class::Other);
        assert_eq!(c("img:reg/base").0, Class::None);
        assert!(c("bt:r2/Deploy").1.contains("deploy"));
    }

    #[test]
    fn keyword_boundaries() {
        assert_eq!(keyword("git checkout main").map(|k| k.0), None, "check must be a whole word");
        assert_eq!(keyword("Run unit tests").map(|k| k.0), Some(Class::Test));
        assert_eq!(keyword("terraform apply").map(|k| k.0), Some(Class::Deploy));
    }

    #[test]
    fn entry_kinds_and_isolation() {
        let g = sample();
        let d = Derived::compute(&g);
        let id = |s: &str| g.node_by_id(s).unwrap() as usize;
        assert_eq!(d.entry[id("bt:r1/Lint")], ENTRY_VCS);
        assert_eq!(d.entry[id("bt:r2/Deploy")], ENTRY_CHAINED);
        assert_eq!(d.entry[id("bt:r2/App")], ENTRY_DEP);
        assert_eq!(d.entry[id("bt:r2/Lonely")], ENTRY_NONE);
        assert!(d.isolated[id("bt:r2/Lonely")]);
        assert!(!d.isolated[id("bt:r2/App")]);
    }

    #[test]
    fn counts_depth_and_tests_before_deploy() {
        let g = sample();
        let d = Derived::compute(&g);
        let id = |s: &str| g.node_by_id(s).unwrap() as usize;
        assert_eq!(d.up[id("bt:r2/Deploy")], 4, "App, img base, Base, Lint");
        assert_eq!(d.down[id("bt:r1/Lint")], 5);
        assert!(!d.no_test_up[id("bt:r2/Deploy")], "App is a test-class job upstream of Deploy");
        let r1 = &d.repos[0];
        assert_eq!(r1.builds, 2);
        assert_eq!(r1.warnings, 1);
        assert!(r1.longest >= 5, "Lint -> Base -> img -> App -> Deploy");
        assert_eq!(g.node_label(*r1.longest_path.last().unwrap()), "Deploy prod");
    }

    #[test]
    fn finds_cycles_hubs_and_unused() {
        let g = sample();
        let d = Derived::compute(&g);
        assert!(d.cyclic.iter().all(|&c| !c), "the sample graph is acyclic");
        let id = |s: &str| g.node_by_id(s).unwrap();
        assert_eq!(d.hubs.first(), Some(&id("bt:r1/Lint")), "Lint has the most jobs downstream");
        assert!(!d.hubs.contains(&id("bt:r2/Deploy")), "a leaf has nothing depending on it");
    }

    #[test]
    fn tarjan_marks_only_cycle_members() {
        // 0 -> 1 -> 2 -> 1, 2 -> 3, 4 -> 4
        let edges = [(0u32, 1u32), (1, 2), (2, 1), (2, 3), (4, 4)];
        let from: Vec<u32> = edges.iter().map(|e| e.0).collect();
        let to: Vec<u32> = edges.iter().map(|e| e.1).collect();
        let net = Net::build(5, from, to, vec![Ek::Snapshot; 5], vec![Sym(0); 5]);
        assert_eq!(cycles(&net, 5), [false, true, true, false, true]);
    }
}
