//! Writes a synthetic graph.json for performance work: a make-heavy,
//! Yocto-and-layered-images shaped organisation.
//!
//!   cargo run --release --example gen -- out.json [builds=3000] [make=45000]

use serde_json::{json, Value};
use std::io::Write;

struct Rng(u64);
impl Rng {
    fn next(&mut self) -> u64 {
        self.0 = self.0.wrapping_mul(6364136223846793005).wrapping_add(1442695040888963407);
        self.0 >> 33
    }
    fn below(&mut self, n: usize) -> usize {
        (self.next() % n.max(1) as u64) as usize
    }
}

fn main() {
    let mut a = std::env::args().skip(1);
    let out = a.next().expect("usage: gen <out.json> [builds] [make]");
    let n_builds: usize = a.next().map_or(3000, |s| s.parse().unwrap());
    let n_make: usize = a.next().map_or(45000, |s| s.parse().unwrap());
    let mut r = Rng(42);
    let repos = ["yocto-layers", "firmware", "platform", "tools", "services", "ui", "release", "infra"];
    let stages = ["Build", "Unit tests", "Lint", "Scan", "Package image", "Publish", "Deploy staging", "Deploy prod", "Integration tests"];
    let mut nodes: Vec<Value> = vec![];
    let mut edges: Vec<Value> = vec![];

    // layered images: base -> sdk -> toolchain -> per-product
    let n_img = (n_builds / 6).max(8);
    let mut images: Vec<String> = vec![];
    for i in 0..n_img {
        let id = format!("img:registry.acme.io/{}/img{i}", repos[i % repos.len()]);
        nodes.push(json!({"id": id, "kind":"image", "repo": repos[i % repos.len()], "label": format!("img{i}"), "sub": id[4..]}));
        if i > 3 {
            let base = &images[r.below(i.min(40))];
            edges.push(json!({"from": base, "to": id, "kind":"derives"}));
        }
        images.push(id);
    }

    let mut builds: Vec<String> = vec![];
    let per_make = n_make / n_builds.max(1);
    let mut make_ids: Vec<String> = vec![];
    for b in 0..n_builds {
        let repo = repos[b % repos.len()];
        let stage = stages[r.below(stages.len())];
        let id = format!("bt:{repo}/Build{b}");
        let mut triggers = vec![];
        if b % 4 == 0 {
            triggers.push(json!({"type":"vcs","detail":"+:<default>"}));
        } else if b % 17 == 0 {
            triggers.push(json!({"type":"schedule","detail":"daily hour=3"}));
        }
        let script = format!("set -e\nmake -C components/c{b} {}\n./ci/run.sh --target t{b}\ndocker run --rm registry.acme.io/tools/sdk bitbake core-image-{b}\n", stage.to_lowercase().replace(' ', "-"));
        nodes.push(json!({
            "id": id, "kind":"build", "repo": repo, "label": format!("{stage} {b}"), "sub": format!("{repo}_Build{b}"),
            "project": format!("{repo} / group{}", b % 9), "triggers": triggers,
            "steps": [{"name": "run", "type":"script", "summary": format!("make {}", stage.to_lowercase()), "script": script}],
            "params": {"env.REGISTRY": "registry.acme.io", "IMAGE": format!("img{}", b % n_img)},
            "artifacts": [format!("out/{b}/**")],
            "source": ".teamcity/settings.kts:42"
        }));
        if b > 0 {
            for _ in 0..(1 + r.below(3)) {
                let win = b.min(300);
                let from = &builds[b - win + r.below(win)];
                edges.push(json!({"from": from, "to": id, "kind":"snapshot"}));
            }
        }
        edges.push(json!({"from": images[r.below(n_img)], "to": id, "kind":"runsIn"}));
        if r.below(5) == 0 {
            edges.push(json!({"from": id, "to": images[r.below(n_img)], "kind":"produces"}));
        }
        // a make tree under this build: a few entry targets, each with prerequisites
        let first = make_ids.len();
        for m in 0..per_make {
            let mid = format!("mk:{repo}/Makefile:b{b}_t{m}");
            nodes.push(json!({
                "id": mid, "kind":"make", "repo": repo, "label": format!("b{b}_t{m}"), "sub": format!("components/c{b}/Makefile"),
                "steps": [{"type":"recipe","summary": format!("gcc -O2 -c t{m}.c"), "script": format!("gcc -O2 -c t{m}.c -o t{m}.o\nar rcs lib{m}.a t{m}.o")}]
            }));
            if m < 3 {
                edges.push(json!({"from": id, "to": mid, "kind":"runs"}));
            } else {
                let parent = &make_ids[first + r.below(m)];
                edges.push(json!({"from": parent, "to": mid, "kind": if m % 5 == 0 { "calls" } else { "depends" }}));
            }
            make_ids.push(mid);
        }
        builds.push(id);
    }
    nodes.sort_by(|a, b| a["id"].as_str().cmp(&b["id"].as_str()));
    let repos_json: Vec<Value> =
        repos.iter().map(|r| json!({"name": r, "url": format!("git@git.acme.io:team/{r}.git"), "commit": "0123456789abcdef"})).collect();
    let g = json!({"schemaVersion":1,"generated":"2026-01-01 00:00 UTC","repos":repos_json,"nodes":nodes,"edges":edges,"warnings":["tools: Makefile:1: example warning"]});
    let mut f = std::io::BufWriter::new(std::fs::File::create(&out).unwrap());
    serde_json::to_writer(&mut f, &g).unwrap();
    f.flush().unwrap();
    eprintln!("{} nodes, {} edges -> {out}", g["nodes"].as_array().unwrap().len(), g["edges"].as_array().unwrap().len());
}
