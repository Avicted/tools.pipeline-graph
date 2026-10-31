//! Background work: run a closure on a thread and poll for the result from the UI.

use std::sync::mpsc::{self, TryRecvError};

pub struct Job<T> {
    rx: mpsc::Receiver<T>,
}

/// Where a job stands.
#[derive(Debug, PartialEq)]
pub enum Poll<T> {
    Pending,
    Ready(T),
    /// The thread ended without a result: it panicked. (The release build aborts on a panic,
    /// so this is what a debug build or a test sees.)
    Lost,
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
        Job { rx }
    }

    pub fn poll(&self) -> Poll<T> {
        match self.rx.try_recv() {
            Ok(v) => Poll::Ready(v),
            Err(TryRecvError::Empty) => Poll::Pending,
            Err(TryRecvError::Disconnected) => Poll::Lost,
        }
    }
}

impl<T: Send + 'static> Job<Result<T, String>> {
    /// For a job that can fail on its own: a lost thread is one more failure, so the spinner
    /// ends with a message instead of running for ever.
    pub fn poll_result(&self) -> Option<Result<T, String>> {
        match self.poll() {
            Poll::Pending => None,
            Poll::Ready(r) => Some(r),
            Poll::Lost => Some(Err("the background task stopped unexpectedly".into())),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    fn wait<T: Send + 'static>(job: &Job<T>) -> Poll<T> {
        for _ in 0..500 {
            match job.poll() {
                Poll::Pending => std::thread::sleep(Duration::from_millis(10)),
                other => return other,
            }
        }
        Poll::Pending
    }

    #[test]
    fn a_finished_job_hands_over_its_result_once() {
        let job = Job::spawn(&egui::Context::default(), || 7);
        assert_eq!(wait(&job), Poll::Ready(7));
        assert_eq!(job.poll(), Poll::Lost, "the result is taken, nothing more will come");
    }

    #[test]
    fn a_panicking_job_is_lost_not_pending() {
        let job: Job<u32> = Job::spawn(&egui::Context::default(), || panic!("worker bug"));
        assert_eq!(wait(&job), Poll::Lost);
    }

    #[test]
    fn a_lost_fallible_job_becomes_an_error() {
        let job: Job<Result<u32, String>> = Job::spawn(&egui::Context::default(), || panic!("worker bug"));
        let r = loop {
            if let Some(r) = job.poll_result() {
                break r;
            }
            std::thread::sleep(Duration::from_millis(10));
        };
        assert!(r.is_err_and(|e| e.contains("stopped unexpectedly")));
    }
}
