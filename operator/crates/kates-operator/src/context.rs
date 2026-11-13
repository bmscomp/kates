use std::collections::BTreeSet;

use kube::Client;
use kube::core::ApiResource;
use kube::runtime::events::{Event, EventType, Recorder};

use crate::connection::Connections;
use crate::metrics::Metrics;

/// What a reconciler is handed.
pub struct Context {
    pub client: Client,
    pub recorder: Recorder,
    pub metrics: Metrics,
    pub connections: Connections,
    pub field_manager: String,
    /// `None` without the Strimzi CRD: compliance then runs on its interval only.
    pub kafka: Option<KafkaWatch>,
    /// The `ClusterRole` each `Kates` pod is bound to. `None` in a namespace-scoped install,
    /// which creates nothing cluster-scoped.
    pub backend_role: Option<String>,
    /// The namespaces whose `Kates` pods get that role, named by whoever installed the operator:
    /// any pod in such a namespace can run as that account.
    pub backend_namespaces: BTreeSet<String>,
}

/// The role a `Kates` in `namespace` is bound to, if the installer allowed that namespace.
#[must_use]
pub fn role_in<'a>(
    role: Option<&'a str>,
    namespaces: &BTreeSet<String>,
    namespace: &str,
) -> Option<&'a str> {
    role.filter(|_| namespaces.contains(namespace))
}

/// The namespaces whose `Kafka`s trigger a check, fixed at start.
pub struct KafkaWatch {
    pub resource: ApiResource,
    pub any_namespace: bool,
    pub namespaces: BTreeSet<String>,
    pub refused: BTreeSet<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Watched {
    Yes,
    NotConfigured,
    Forbidden,
}

impl KafkaWatch {
    #[must_use]
    pub fn watched(&self, namespace: &str) -> Watched {
        if self.refused.contains(namespace) {
            Watched::Forbidden
        } else if self.any_namespace || self.namespaces.contains(namespace) {
            Watched::Yes
        } else {
            Watched::NotConfigured
        }
    }
}

/// One transition worth telling `kubectl describe`.
pub struct Note {
    pub warning: bool,
    pub reason: &'static str,
    pub message: String,
}

impl Context {
    #[must_use]
    pub fn backend_role_in(&self, namespace: &str) -> Option<&str> {
        role_in(
            self.backend_role.as_deref(),
            &self.backend_namespaces,
            namespace,
        )
    }

    /// Never fails a pass: a terminating namespace refuses new objects, Events included.
    pub async fn publish<K>(&self, object: &K, notes: Vec<Note>)
    where
        K: kube::Resource<DynamicType = ()>,
    {
        let reference = object.object_ref(&());
        for note in notes {
            let event = Event {
                type_: if note.warning {
                    EventType::Warning
                } else {
                    EventType::Normal
                },
                reason: note.reason.to_owned(),
                note: Some(note.message),
                action: "Reconcile".to_owned(),
                secondary: None,
            };
            if let Err(error) = self.recorder.publish(&event, &reference).await {
                tracing::warn!(%error, name = ?reference.name, "event refused");
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_role_is_given_only_in_a_namespace_the_installer_named() {
        let named = BTreeSet::from(["payments".to_owned()]);
        assert_eq!(role_in(Some("r"), &named, "payments"), Some("r"));
        assert_eq!(role_in(Some("r"), &named, "tenant"), None);
        assert_eq!(role_in(Some("r"), &BTreeSet::new(), "payments"), None);
        assert_eq!(role_in(None, &named, "payments"), None);
    }
}
