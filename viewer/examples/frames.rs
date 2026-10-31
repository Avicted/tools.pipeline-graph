//! Frame times of the real interface, measured headlessly: egui layout plus
//! tessellation for the screens that matter, on a big graph. No GPU involved,
//! so this is the CPU half of a frame; it fails when a budget is missed.
//!
//!   cargo run --release --example frames -- graph.json [--check]

use pipeline_graph_viewer::core::model::Model;
use pipeline_graph_viewer::core::scene::Scene;
use pipeline_graph_viewer::ui::app::{Screen, Workspace};
use pipeline_graph_viewer::ui::headless::Rig;

const BUDGET_P95_MS: f64 = 8.0;

fn measure(rig: &mut Rig, name: &str, frames: usize, mut each: impl FnMut(&mut Workspace, usize), failed: &mut Vec<String>) {
    rig.settle();
    let mut ms: Vec<f64> = (0..frames)
        .map(|i| {
            each(&mut rig.ws, i);
            rig.frame().as_secs_f64() * 1e3
        })
        .collect();
    ms.sort_by(|a, b| a.partial_cmp(b).unwrap());
    let mean = ms.iter().sum::<f64>() / ms.len() as f64;
    let p95 = ms[(ms.len() as f64 * 0.95) as usize - 1];
    println!("{name:<34}mean {mean:>6.2} ms   p95 {p95:>6.2} ms   max {:>6.2} ms   ({} primitives)", ms[ms.len() - 1], rig.prims);
    if p95 > BUDGET_P95_MS {
        failed.push(format!("{name} p95 {p95:.1} ms"));
    }
}

fn main() {
    let mut a = std::env::args().skip(1);
    let path = a.next().expect("usage: frames <graph.json> [--check]");
    let check = a.next().as_deref() == Some("--check");
    let m = Model::load(std::path::Path::new(&path)).expect("load");
    println!("{} nodes, p95 budget {BUDGET_P95_MS} ms per frame", m.g.len());
    let mut rig = Rig::new(m);
    let mut failed = vec![];

    let busiest = {
        let m = rig.ws.m.clone();
        (0..m.g.len() as u32)
            .filter(|&v| m.g.kind[v as usize] == pipeline_graph_viewer::core::graph::Kind::Build)
            .max_by_key(|&v| m.d.down[v as usize] + m.d.up[v as usize])
    };
    let repo = {
        let d = &rig.ws.m.d;
        (0..d.repos.len()).max_by_key(|&i| d.repos[i].longest).unwrap_or(0)
    };

    rig.ws.screen = Screen::Snapshot;
    measure(&mut rig, "snapshot", 60, |_, _| {}, &mut failed);

    rig.ws.screen = Screen::Table;
    measure(
        &mut rig,
        "table, selecting down the list",
        120,
        |ws, i| {
            if let Some(&v) = ws.table.rows.get(i * 7 % ws.table.rows.len().max(1)) {
                ws.select(Some(v));
                ws.table.reveal = Some(v);
            }
        },
        &mut failed,
    );

    rig.ws.select(busiest);
    measure(&mut rig, "table with the inspector open", 60, |_, _| {}, &mut failed);

    rig.ws.screen = Screen::Map;
    rig.ws.scene = Scene::Repos;
    measure(&mut rig, "map: repositories", 60, |_, _| {}, &mut failed);

    rig.ws.scene = Scene::Repo(repo as u32);
    rig.ws.canvas.fit_pending = true;
    measure(
        &mut rig,
        "map: one repository, panning",
        180,
        |ws, i| {
            ws.canvas.pan.x -= 6.0;
            ws.canvas.pan.y += (i as f32 * 0.2).sin() * 4.0;
        },
        &mut failed,
    );

    for (label, k) in [("zoomed out", 0.2f32), ("fitted", 0.5), ("zoomed in", 1.2)] {
        measure(&mut rig, &format!("map: one repository, {label}"), 60, |ws, _| ws.canvas.k = k, &mut failed);
    }

    if let Some(v) = busiest {
        rig.ws.open_focus(v);
        measure(&mut rig, "map: lineage of the busiest job", 60, |_, _| {}, &mut failed);
    }

    rig.ws.go(Screen::Prod);
    measure(&mut rig, "commit -> prod", 60, |_, _| {}, &mut failed);

    rig.ws.go(Screen::Images);
    measure(&mut rig, "images & layers", 60, |_, _| {}, &mut failed);

    rig.ws.go(Screen::Table);
    rig.ws.palette.toggle();
    rig.ws.palette.query = "deploy".into();
    measure(&mut rig, "command palette, search results", 60, |_, _| {}, &mut failed);

    if check && !failed.is_empty() {
        eprintln!("over budget: {}", failed.join(", "));
        std::process::exit(1);
    }
}
