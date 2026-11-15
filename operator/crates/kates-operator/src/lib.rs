//! The Kates operator: a `Kates` resource installs one Kates backend, or a quorum of them.

pub mod connection;
pub mod context;
pub mod crds;
pub mod metrics;
pub mod reconcile;
pub mod run;
pub mod status;
pub mod telemetry;
