//! Background work: run a closure on a thread and poll for the result from the UI.

use std::sync::mpsc;
use std::time::Instant;

pub struct Job<T> {
    rx: mpsc::Receiver<T>,
    pub started: Instant,
}

impl<T: Send + 'static> Job<T> {
    pub fn spawn(ctx: &egui::Context, f: impl FnOnce() -> T + Send + 'static) -> Job<T> {
        let (tx, rx) = mpsc::channel();
        let ctx = ctx.clone();
        std::thread::spawn(move || {
            let v = f();
            let _ = tx.send(v);
            ctx.request_repaint();
        });
        Job { rx, started: Instant::now() }
    }

    pub fn poll(&self) -> Option<T> {
        self.rx.try_recv().ok()
    }
}
