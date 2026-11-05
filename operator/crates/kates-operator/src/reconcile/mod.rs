//! The reconcilers, one per kind.

pub mod compliance;
pub mod test;

use std::collections::BTreeMap;

pub const PAUSE: &str = "kates.io/pause-reconciliation";
pub const RECONCILE_AT: &str = "kates.io/reconcile-at";

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
    #[error("the run could not be cancelled: {0}")]
    Cancel(#[source] kates_client::Error),
    #[error(transparent)]
    Finalizer(#[from] Box<kube::runtime::finalizer::Error<Error>>),
}

impl Error {
    /// A bounded metric label.
    #[must_use]
    pub fn name(&self) -> &'static str {
        match self {
            Self::NoNamespace => "NoNamespace",
            Self::Kube(_) => "Kube",
            Self::Cancel(error) => error.name(),
            Self::Finalizer(error) => match &**error {
                kube::runtime::finalizer::Error::ApplyFailed(inner)
                | kube::runtime::finalizer::Error::CleanupFailed(inner) => inner.name(),
                _ => "Finalizer",
            },
        }
    }

    /// A failed cancel takes the Kates API's backoff; anything else is a Kubernetes API write.
    #[must_use]
    pub fn is_cancel(&self) -> bool {
        match self {
            Self::Cancel(_) => true,
            Self::Finalizer(error) => matches!(
                &**error,
                kube::runtime::finalizer::Error::CleanupFailed(Self::Cancel(_))
            ),
            _ => false,
        }
    }
}
