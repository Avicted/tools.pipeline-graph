//! Drives the interface without a window: egui runs and tessellates frames on
//! the CPU. Used by the frame benchmark and the smoke tests.

use super::app::{Model, Workspace};
use super::theme::{self, Mode};
use std::time::{Duration, Instant};

pub struct Rig {
    pub ctx: egui::Context,
    pub ws: Workspace,
    t: f64,
    /// Mesh primitives of the last frame, a rough sign that something was drawn.
    pub prims: usize,
}

impl Rig {
    pub fn new(m: Model) -> Rig {
        let ctx = egui::Context::default();
        theme::install_fonts(&ctx);
        theme::apply(&ctx, Mode::Light);
        let ws = Workspace::new(&ctx, m);
        Rig { ctx, ws, t: 0.0, prims: 0 }
    }

    pub fn frame(&mut self) -> Duration {
        let pal = theme::palette(Mode::Light);
        let mut mode = Mode::Light;
        let mut open = None;
        let raw = egui::RawInput {
            screen_rect: Some(egui::Rect::from_min_size(egui::Pos2::ZERO, egui::vec2(1600.0, 1000.0))),
            time: Some(self.t),
            ..Default::default()
        };
        self.t += 1.0 / 60.0;
        let t0 = Instant::now();
        let out = self.ctx.run(raw, |ctx| self.ws.frame(ctx, &pal, &mut mode, &mut open));
        self.prims = self.ctx.tessellate(out.shapes, out.pixels_per_point).len();
        t0.elapsed()
    }

    /// Waits for background layouts, then renders a few frames to settle.
    pub fn settle(&mut self) {
        self.frame(); // asks for the layout the screen needs
        self.frame();
        let t = Instant::now();
        while !self.ws.layouts_idle() && t.elapsed() < Duration::from_secs(30) {
            std::thread::sleep(Duration::from_millis(2));
            self.frame();
        }
        for _ in 0..6 {
            self.frame();
        }
    }
}
