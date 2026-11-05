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
