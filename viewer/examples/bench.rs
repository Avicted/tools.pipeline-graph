//! Times the headless core on a graph.json and fails when a budget is missed.
//!
//!   cargo run --release --example bench -- graph.json [--check]

use pgview::core::{
    derive::Derived,
    graph::{Graph, Scratch},
    search::{Query, SearchIndex},
};
use std::time::Instant;

fn rss_mb() -> f64 {
    let s = std::fs::read_to_string("/proc/self/status").unwrap_or_default();
    s.lines().find(|l| l.starts_with("VmHWM")).and_then(|l| l.split_whitespace().nth(1)?.parse::<f64>().ok()).unwrap_or(0.0) / 1024.0
}

fn main() {
    let mut a = std::env::args().skip(1);
    let path = a.next().expect("usage: bench <graph.json> [--check]");
    let check = a.next().as_deref() == Some("--check");
    let mut failed = vec![];
    let mut budget = |name: &str, ms: f64, limit: f64| {
        println!("{name:<28}{ms:>9.2} ms   (budget {limit} ms)");
        if ms > limit {
            failed.push(name.to_string());
        }
    };

    let t = Instant::now();
    let g = Graph::load(std::path::Path::new(&path)).unwrap();
    budget("load + flow", t.elapsed().as_secs_f64() * 1e3, 1000.0);
    println!("{} nodes, {} edges, {} flow edges", g.len(), g.full.len(), g.flow.len());

    let t = Instant::now();
    let d = Derived::compute(&g);
    budget("derive (classes, reach)", t.elapsed().as_secs_f64() * 1e3, 3000.0);

    let t = Instant::now();
    let ix = SearchIndex::build(&g);
    budget("search index", t.elapsed().as_secs_f64() * 1e3, 1500.0);

    for q in ["build", "docker run", "repo:tools class:test", "zzzzzz", "dplprd"] {
        let query = Query::parse(q);
        let t = Instant::now();
        let hits = ix.search(&g, &d, &query);
        budget(&format!("search “{q}” ({} hits)", hits.len()), t.elapsed().as_secs_f64() * 1e3, 25.0);
    }

    let mut sc = Scratch::new(g.len());
    let t = Instant::now();
    let mut total = 0;
    for v in (0..g.len() as u32).step_by((g.len() / 200).max(1)) {
        total += sc.count(g.net_for(v), v, true) + sc.count(g.net_for(v), v, false);
    }
    budget("lineage (avg of 200)", t.elapsed().as_secs_f64() * 1e3 / 200.0, 2.0);
    println!("(checksum {total})");

    let rss = rss_mb();
    println!("peak RSS {rss:.0} MB");
    if check && rss > 400.0 {
        failed.push(format!("RSS {rss:.0} MB"));
    }
    if check && !failed.is_empty() {
        eprintln!("over budget: {}", failed.join(", "));
        std::process::exit(1);
    }
}
