//! `pgview --shot DIR`: drives the app through its screens and saves a PNG of
//! each, so the documentation pictures are generated, not hand-made.

use super::app::{Screen, Workspace};
use super::canvas::Scene;
use std::path::PathBuf;

pub struct ShotRun {
    pub dir: PathBuf,
    step: usize,
    wait: u32,
    asked: bool,
    name: String,
}

impl ShotRun {
    pub fn new(dir: PathBuf) -> ShotRun {
        ShotRun { dir, step: 0, wait: 0, asked: false, name: String::new() }
    }
}

const STEPS: &[&str] = &["snapshot", "table", "map", "focus", "prod", "images", "inspector", "compare", "palette"];

/// Puts the workspace into the state of step `i`; false when there is nothing for it.
fn arrange(ws: &mut Workspace, name: &str) -> bool {
    let m = ws.m.clone();
    let d = &m.d;
    let busiest = (0..m.g.len() as u32)
        .filter(|&v| m.g.kind[v as usize] == crate::core::graph::Kind::Build)
        .max_by_key(|&v| d.down[v as usize] + d.up[v as usize]);
    ws.palette.open = false;
    match name {
        "snapshot" => {
            ws.select(None);
            ws.go(Screen::Snapshot);
        }
        "table" => {
            ws.table.query.clear();
            ws.table.dirty = true;
            ws.select(busiest);
            ws.table.reveal = busiest;
            ws.screen = Screen::Table;
        }
        "map" => {
            ws.select(busiest);
            ws.scene = Scene::All;
            ws.screen = Screen::Map;
            ws.canvas.fit_pending = true;
        }
        "focus" => match busiest {
            Some(v) => ws.open_focus(v),
            None => return false,
        },
        "prod" => {
            ws.select(None);
            ws.go(Screen::Prod);
        }
        "images" => {
            ws.go(Screen::Images);
        }
        "inspector" => {
            ws.select(busiest);
            ws.screen = Screen::Table;
        }
        "compare" => {
            if ws.compare.diff.is_none() {
                return false;
            }
            ws.go(Screen::Compare);
        }
        "palette" => {
            ws.go(Screen::Table);
            ws.palette.toggle();
            ws.palette.query = "deploy".into();
        }
        _ => return false,
    }
    true
}

impl Workspace {
    /// Called once per frame; returns true while a run is in progress.
    pub fn drive_shots(&mut self, ctx: &egui::Context) -> bool {
        let Some(mut run) = self.shot.take() else { return false };
        // collect a finished screenshot
        let image = ctx.input(|i| {
            i.raw.events.iter().find_map(|e| match e {
                egui::Event::Screenshot { image, .. } => Some(image.clone()),
                _ => None,
            })
        });
        if let Some(img) = image {
            let (w, h) = (img.width() as u32, img.height() as u32);
            let rgba: Vec<u8> = img.pixels.iter().flat_map(|c| c.to_array()).collect();
            let path = run.dir.join(format!("{}.png", run.name));
            match image::save_buffer(&path, &rgba, w, h, image::ColorType::Rgba8) {
                Ok(()) => println!("{}", path.display()),
                Err(e) => eprintln!("pgview: {}: {e}", path.display()),
            }
            run.asked = false;
            run.step += 1;
            run.wait = 0;
        }
        if run.asked {
            self.shot = Some(run);
            ctx.request_repaint();
            return true;
        }
        if run.wait == 0 {
            // pick the next step that applies to this graph
            while run.step < STEPS.len() {
                let name = STEPS[run.step];
                if arrange(self, name) {
                    run.name = name.to_string();
                    break;
                }
                run.step += 1;
            }
            if run.step >= STEPS.len() {
                ctx.send_viewport_cmd(egui::ViewportCommand::Close);
                return false;
            }
        }
        run.wait += 1;
        let layouts_busy = !self.layouts_idle();
        if run.wait >= 14 && !layouts_busy {
            ctx.send_viewport_cmd(egui::ViewportCommand::Screenshot(egui::UserData::default()));
            run.asked = true;
        }
        self.shot = Some(run);
        ctx.request_repaint();
        true
    }
}
