//! pgview: a native, snappy viewer for pipeline-graph's graph.json.

use pgview::ui::app::{App, Cli};
use std::path::PathBuf;

const USAGE: &str =
    "pgview [graph.json] [--compare older/graph.json] [--goto pgview://table?q=repo:orders] [--shot DIR] [--theme light|dark]

With no file, pgview opens ./out/graph.json (what `pipeline-graph` writes) or asks for one.";

fn main() -> eframe::Result {
    let mut cli = Cli { path: None, compare: None, goto: None, shot: None, theme: None };
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        match a.as_str() {
            "-h" | "--help" => {
                println!("{USAGE}");
                return Ok(());
            }
            "--compare" => cli.compare = args.next().map(PathBuf::from),
            "--goto" => cli.goto = args.next(),
            "--shot" => cli.shot = args.next().map(PathBuf::from),
            "--theme" => cli.theme = args.next(),
            s if s.starts_with('-') => {
                eprintln!("pgview: unknown option {s}\n{USAGE}");
                std::process::exit(2);
            }
            s => cli.path = Some(PathBuf::from(s)),
        }
    }
    let size = if cli.shot.is_some() { [1600.0, 1000.0] } else { [1480.0, 900.0] };
    let opts = eframe::NativeOptions {
        viewport: egui::ViewportBuilder::default()
            .with_title(pgview::ui::app::APP_NAME)
            .with_inner_size(size)
            .with_min_inner_size([900.0, 560.0])
            .with_app_id("pgview"),
        ..Default::default()
    };
    eframe::run_native("pgview", opts, Box::new(move |cc| Ok(Box::new(App::new(cc, cli)))))
}
