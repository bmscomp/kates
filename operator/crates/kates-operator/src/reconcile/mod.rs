//! The reconcilers, one per kind.

pub mod kates;
pub mod quorum;

use std::collections::BTreeMap;

pub const PAUSE: &str = "kates.io/pause-reconciliation";

#[must_use]
pub fn paused(annotations: &BTreeMap<String, String>) -> bool {
    annotations.get(PAUSE).is_some_and(|value| value == "true")
}

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("a namespaced resource without a namespace")]
    NoNamespace,
    #[error(transparent)]
    Kube(#[from] kube::Error),
    #[error(transparent)]
    Finalizer(#[from] Box<kube::runtime::finalizer::Error<Error>>),
    #[error("a quorum certificate could not be made: {0}")]
    Certificate(#[from] rcgen::Error),
}

impl Error {
    /// A bounded metric label.
    #[must_use]
    pub fn name(&self) -> &'static str {
        match self {
            Self::NoNamespace => "NoNamespace",
            Self::Kube(_) => "Kube",
            Self::Certificate(_) => "Certificate",
            Self::Finalizer(error) => match &**error {
                kube::runtime::finalizer::Error::ApplyFailed(inner)
                | kube::runtime::finalizer::Error::CleanupFailed(inner) => inner.name(),
                _ => "Finalizer",
            },
        }
    }
}
