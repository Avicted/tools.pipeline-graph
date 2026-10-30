//! pgview: a native viewer for the `graph.json` that pipeline-graph writes.
//!
//! `core` holds everything that is independent of the GUI (loading, indexes,
//! search, layout, diff) so it is unit-testable and benchmarkable headless;
//! `ui` holds the egui application.

pub mod core;
pub mod ui;
