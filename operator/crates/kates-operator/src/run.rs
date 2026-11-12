use std::collections::BTreeSet;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, RwLock};
use std::time::Duration;

use axum::Router;
use axum::extract::State;
use axum::http::{StatusCode, header};
use axum::response::IntoResponse;
use axum::routing::get;
use futures::channel::oneshot;
use futures::stream::{self, BoxStream, Stream, StreamExt as _};
use futures::{FutureExt as _, future};
use k8s_openapi::api::apps::v1::StatefulSet;
use k8s_openapi::api::authorization::v1::{
    ResourceAttributes, SelfSubjectAccessReview, SelfSubjectAccessReviewSpec,
};
use kates_crds::{KafkaCompliance, Kates, KatesTest};
use kube::api::{Api, DynamicObject, PostParams};
use kube::discovery;
use kube::runtime::Controller;
use kube::runtime::events::{Recorder, Reporter};
use kube::runtime::reflector::{ObjectRef, Store};
use kube::runtime::watcher;
use kube::{Client, ResourceExt as _};
use kube_lease_manager::{LeaseManager, LeaseManagerBuilder};
use prometheus_client::encoding::text::encode;
use prometheus_client::registry::Registry;
use tokio::net::TcpListener;
use tokio::signal::unix::{SignalKind, signal};
use tokio::sync::watch;

use crate::connection::{Connections, FALLBACK_DIR};
use crate::context::{Context, KafkaWatch, Watched};
use crate::crds;
use crate::metrics::{Metrics, StoreCollector, Stores};
use crate::reconcile::{compliance, kates, test};

const OPENMETRICS: &str = "application/openmetrics-text; version=1.0.0; charset=utf-8";
const LEASE: &str = "kates-operator";
// A dead holder is replaced about 15 s after its last renewal.
const LEASE_DURATION: u64 = 15;
const LEASE_GRACE: u64 = 5;
const CRD_RETRY: Duration = Duration::from_secs(5);

type Fallible<T = ()> = Result<T, Box<dyn std::error::Error>>;

/// `KATES_OPERATOR_NAMESPACES`: `*`, or a comma list.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Scope {
    Any,
    Listed(BTreeSet<String>),
}

impl Scope {
    #[must_use]
    pub fn parse(value: &str) -> Self {
        if value.trim() == "*" {
            return Self::Any;
        }
        Self::Listed(
            value
                .split(',')
                .map(str::trim)
                .filter(|namespace| !namespace.is_empty())
                .map(str::to_owned)
                .collect(),
        )
    }

    fn apis<K>(&self, client: &Client) -> Vec<Api<K>>
    where
        K: kube::Resource<Scope = kube::core::NamespaceResourceScope, DynamicType = ()>,
    {
        match self {
            Self::Any => vec![Api::all(client.clone())],
            Self::Listed(namespaces) => namespaces
                .iter()
                .map(|namespace| Api::namespaced(client.clone(), namespace))
                .collect(),
        }
    }
}

/// What `/readyz` reads, and reports in its body without failing on it where it says so.
#[derive(Default)]
struct Health {
    leader: AtomicBool,
    kafka_refused: RwLock<BTreeSet<String>>,
}

struct App {
    client: Client,
    registry: Registry,
    health: Arc<Health>,
}

/// Serves health and metrics, takes the Lease, and reconciles until SIGTERM or a lost Lease.
///
/// # Errors
/// When no Kubernetes client can be built, port 8080 cannot be bound or the Lease cannot be
/// managed.
pub async fn run() -> Fallible {
    let client = Client::try_default().await?;
    let namespace = env_or("POD_NAMESPACE", client.default_namespace());
    let pod = env_or("POD_NAME", "kates-operator");
    let scope = Scope::parse(&env_or("KATES_OPERATOR_NAMESPACES", &namespace));
    let kafka_scope = Scope::parse(&env_or("KATES_OPERATOR_KAFKA_NAMESPACES", ""));

    let mut registry = Registry::default();
    let metrics = Metrics::register(&mut registry);
    let stores = Stores::default();
    registry.register_collector(Box::new(StoreCollector(stores.clone())));
    let health = Arc::new(Health::default());

    let listener = TcpListener::bind(("0.0.0.0", 8080)).await?;
    tracing::info!(address = %listener.local_addr()?, "serving health and metrics");
    let router = router(client.clone(), registry, health.clone());
    let server = tokio::spawn(async move {
        axum::serve(listener, router)
            .with_graceful_shutdown(shutdown())
            .await
    });

    let lease = LeaseManagerBuilder::new(client.clone(), LEASE)
        .with_namespace(&namespace)
        .with_identity(&pod)
        .with_duration(LEASE_DURATION)
        .with_grace(LEASE_GRACE)
        .with_field_manager(format!("kates-operator-{namespace}"))
        .build()
        .await?;
    let (leader, renewal) = lease.watch().await;
    let result = lead(LeadArgs {
        client,
        namespace,
        pod,
        scope,
        kafka_scope,
        metrics: metrics.clone(),
        stores,
        health: health.clone(),
        leader: leader.clone(),
    })
    .await;

    // Dropping the receivers makes watch() release the Lease.
    metrics.set_leader(false);
    drop(leader);
    release(renewal).await;
    server.abort();
    result
}

async fn release(renewal: tokio::task::JoinHandle<kube_lease_manager::Result<LeaseManager>>) {
    match renewal.await {
        Ok(Ok(_)) => tracing::info!("lease released"),
        Ok(Err(error)) => tracing::warn!(%error, "lease not released"),
        Err(error) => tracing::warn!(%error, "lease task failed"),
    }
}

struct LeadArgs {
    client: Client,
    namespace: String,
    pod: String,
    scope: Scope,
    kafka_scope: Scope,
    metrics: Metrics,
    stores: Stores,
    health: Arc<Health>,
    leader: watch::Receiver<bool>,
}

// A fresh process is a standby until the Lease reads true; the controllers then run until it
// reads false, and the Deployment restarts the process as a standby.
async fn lead(args: LeadArgs) -> Fallible {
    let LeadArgs {
        client,
        namespace,
        pod,
        scope,
        kafka_scope,
        metrics,
        stores,
        health,
        mut leader,
    } = args;
    tokio::select! {
        taken = leader.wait_for(|held| *held) => { taken?; }
        () = shutdown() => return Ok(()),
    }
    health.leader.store(true, Ordering::Relaxed);
    metrics.set_leader(true);
    tracing::info!(%pod, "leading");

    tokio::select! {
        () = wait_for_crds(&client) => {}
        () = shutdown() => return Ok(()),
    }

    let kafka = kafka_watch(&client, &kafka_scope).await;
    if let Some(watch) = &kafka {
        health
            .kafka_refused
            .write()
            .expect("the health state")
            .clone_from(&watch.refused);
    }
    let requests = metrics.clone();
    let connections = Connections::new(
        PathBuf::from(FALLBACK_DIR),
        Arc::new(move |method, route, status| requests.api_request(method, route, status)),
    );
    let ctx = Arc::new(Context {
        recorder: Recorder::new(
            client.clone(),
            Reporter {
                controller: "kates-operator".to_owned(),
                instance: Some(pod),
            },
        ),
        client: client.clone(),
        metrics,
        connections,
        field_manager: format!("kates-operator-{namespace}"),
        kafka,
    });

    let mut streams: Vec<BoxStream<'static, ()>> = Vec::new();
    for api in scope.apis::<KatesTest>(&client) {
        let controller = Controller::new(api, watcher::Config::default())
            .graceful_shutdown_on(lease_lost(leader.clone()))
            .shutdown_on_signal();
        stores
            .tests
            .write()
            .expect("the stores")
            .push(controller.store());
        streams.push(quiet(controller.run(
            test::reconcile,
            test::error_policy,
            ctx.clone(),
        )));
    }
    for api in scope.apis::<KafkaCompliance>(&client) {
        let controller = Controller::new(api, watcher::Config::default());
        let store = controller.store();
        stores
            .checks
            .write()
            .expect("the stores")
            .push(store.clone());
        let controller = kafka_triggers(controller, &client, ctx.kafka.as_ref(), &store)
            .graceful_shutdown_on(lease_lost(leader.clone()))
            .shutdown_on_signal();
        streams.push(quiet(controller.run(
            compliance::reconcile,
            compliance::error_policy,
            ctx.clone(),
        )));
    }
    let sets = scope.apis::<StatefulSet>(&client);
    for (api, sets) in scope.apis::<Kates>(&client).into_iter().zip(sets) {
        let controller = Controller::new(api, watcher::Config::default())
            .owns(sets, watcher::Config::default())
            .graceful_shutdown_on(lease_lost(leader.clone()))
            .shutdown_on_signal();
        streams.push(quiet(controller.run(
            kates::reconcile,
            kates::error_policy,
            ctx.clone(),
        )));
    }
    stream::select_all(streams).collect::<()>().await;
    Ok(())
}

// A failed pass is already counted and logged by its error policy.
fn quiet<T, E: std::fmt::Display>(
    outcomes: impl Stream<Item = Result<T, E>> + Send + 'static,
) -> BoxStream<'static, ()> {
    outcomes
        .for_each(|outcome| {
            if let Err(error) = outcome {
                tracing::debug!(%error, "controller");
            }
            future::ready(())
        })
        .into_stream()
        .boxed()
}

// The controllers hold off until the served schema is at least this build's: a stale install
// reconciles nothing rather than running on a schema that prunes fields it needs.
async fn wait_for_crds(client: &Client) {
    loop {
        match crds::served(client)
            .await
            .map(|served| crds::stale(&served))
        {
            Ok(None) => return,
            Ok(Some(message)) => tracing::warn!(message, "holding off"),
            Err(error) => tracing::warn!(%error, "the served CRDs are not readable"),
        }
        tokio::time::sleep(CRD_RETRY).await;
    }
}

// kube requires this trigger to be Sync, which a watch receiver's future is not.
fn lease_lost(mut leader: watch::Receiver<bool>) -> impl Future<Output = ()> + Send + Sync {
    let (lost, trigger) = oneshot::channel::<()>();
    tokio::spawn(async move {
        let _ = leader.wait_for(|held| !*held).await;
        tracing::warn!("lease lost");
        let _ = lost.send(());
    });
    trigger.map(|_| ())
}

async fn kafka_watch(client: &Client, scope: &Scope) -> Option<KafkaWatch> {
    let group = discovery::group(client, "kafka.strimzi.io").await;
    let resource = group
        .ok()
        .and_then(|group| group.recommended_kind("Kafka"))
        .map(|(resource, _)| resource);
    let Some(resource) = resource else {
        tracing::info!("no Strimzi Kafka CRD: compliance runs on its interval only");
        return None;
    };
    let (any_namespace, namespaces) = match scope {
        Scope::Any => (true, BTreeSet::new()),
        Scope::Listed(namespaces) => (false, namespaces.clone()),
    };
    let mut refused = BTreeSet::new();
    for namespace in &namespaces {
        if !may_list_kafkas(client, namespace).await {
            tracing::warn!(
                namespace,
                "the operator may not list kafkas here: bind its Role"
            );
            refused.insert(namespace.clone());
        }
    }
    Some(KafkaWatch {
        resource,
        any_namespace,
        namespaces,
        refused,
    })
}

// Without this, kube-runtime would retry a 403 list forever, visible only in the log.
async fn may_list_kafkas(client: &Client, namespace: &str) -> bool {
    let review = SelfSubjectAccessReview {
        spec: SelfSubjectAccessReviewSpec {
            resource_attributes: Some(ResourceAttributes {
                group: Some("kafka.strimzi.io".to_owned()),
                resource: Some("kafkas".to_owned()),
                verb: Some("list".to_owned()),
                namespace: Some(namespace.to_owned()),
                ..ResourceAttributes::default()
            }),
            ..SelfSubjectAccessReviewSpec::default()
        },
        ..SelfSubjectAccessReview::default()
    };
    let api: Api<SelfSubjectAccessReview> = Api::all(client.clone());
    api.create(&PostParams::default(), &review)
        .await
        .ok()
        .and_then(|answer| answer.status)
        .is_some_and(|status| status.allowed)
}

fn kafka_triggers(
    mut controller: Controller<KafkaCompliance>,
    client: &Client,
    kafka: Option<&KafkaWatch>,
    store: &Store<KafkaCompliance>,
) -> Controller<KafkaCompliance> {
    let Some(kafka) = kafka else {
        return controller;
    };
    let mut apis: Vec<Api<DynamicObject>> = Vec::new();
    if kafka.any_namespace {
        apis.push(Api::all_with(client.clone(), &kafka.resource));
    }
    for namespace in &kafka.namespaces {
        if kafka.watched(namespace) == Watched::Yes {
            apis.push(Api::namespaced_with(
                client.clone(),
                namespace,
                &kafka.resource,
            ));
        }
    }
    for api in apis {
        let store = store.clone();
        controller = controller.watches_with(
            api,
            kafka.resource.clone(),
            watcher::Config::default(),
            move |object| checks_of(&store, &object),
        );
    }
    controller
}

fn checks_of(
    store: &Store<KafkaCompliance>,
    kafka: &DynamicObject,
) -> Vec<ObjectRef<KafkaCompliance>> {
    let target = (kafka.namespace().unwrap_or_default(), kafka.name_any());
    store
        .state()
        .iter()
        .filter(|check| compliance::target(check) == target)
        .map(|check| ObjectRef::from_obj(&**check))
        .collect()
}

fn env_or(name: &str, default: &str) -> String {
    std::env::var(name)
        .ok()
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| default.to_owned())
}

fn router(client: Client, registry: Registry, health: Arc<Health>) -> Router {
    Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/readyz", get(readyz))
        .route("/metrics", get(metrics))
        .with_state(Arc::new(App {
            client,
            registry,
            health,
        }))
}

// Fails on an unreachable API server or a stale served schema; the Lease and a Kafka refusal are
// reported in the body and fail nothing, so a standby and a chart without the Kafka Role go Ready.
async fn readyz(State(app): State<Arc<App>>) -> impl IntoResponse {
    if let Err(error) = app.client.apiserver_version().await {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            format!("the Kubernetes API does not answer: {error}"),
        );
    }
    // A failed read is not staleness.
    if let Ok(served) = crds::served(&app.client).await
        && let Some(message) = crds::stale(&served)
    {
        return (StatusCode::SERVICE_UNAVAILABLE, message);
    }
    let role = if app.health.leader.load(Ordering::Relaxed) {
        "leader"
    } else {
        "standby"
    };
    let refused = app.health.kafka_refused.read().expect("the health state");
    let body = if refused.is_empty() {
        format!("ok ({role})")
    } else {
        let namespaces = refused.iter().cloned().collect::<Vec<_>>().join(", ");
        format!("ok ({role}); kafkas not readable in: {namespaces}")
    };
    (StatusCode::OK, body)
}

async fn metrics(State(app): State<Arc<App>>) -> impl IntoResponse {
    let mut body = String::new();
    match encode(&mut body, &app.registry) {
        Ok(()) => ([(header::CONTENT_TYPE, OPENMETRICS)], body).into_response(),
        Err(_) => StatusCode::INTERNAL_SERVER_ERROR.into_response(),
    }
}

async fn shutdown() {
    let mut terminate = signal(SignalKind::terminate()).expect("a SIGTERM handler");
    tokio::select! {
        _ = terminate.recv() => {}
        _ = tokio::signal::ctrl_c() => {}
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use axum::body::to_bytes;
    use axum::extract::State;
    use axum::http::{StatusCode, header};
    use axum::response::IntoResponse;
    use http::{Request, Response};
    use kube::Client;
    use kube::client::Body;
    use prometheus_client::registry::Registry;

    use super::{App, OPENMETRICS, Scope, metrics, readyz};
    use crate::metrics::Metrics;

    const SERVED: &str = r#"{"components":{"schemas":{
        "io.kates.v1alpha1.KatesTest":{"description":"crd-version: 1"},
        "io.kates.v1alpha1.KafkaCompliance":{"description":"crd-version: 1"},
        "io.kates.v1alpha1.Kates":{"description":"crd-version: 1"}}}}"#;

    const VERSION: &str = r#"{"major":"1","minor":"34","gitVersion":"v1.34.11","gitCommit":"c",
        "gitTreeState":"clean","buildDate":"2026-01-01T00:00:00Z","goVersion":"go1.25",
        "compiler":"gc","platform":"linux/amd64"}"#;

    fn answering(status: u16, body: &'static str) -> Client {
        answering_with(status, body, SERVED)
    }

    fn answering_with(status: u16, body: &'static str, openapi: &'static str) -> Client {
        let (service, mut handle) = tower_test::mock::pair::<Request<Body>, Response<Body>>();
        tokio::spawn(async move {
            while let Some((request, respond)) = handle.next_request().await {
                let (code, text) = if request.uri().path() == "/version" {
                    (status, body)
                } else {
                    (200, openapi)
                };
                respond.send_response(
                    Response::builder()
                        .status(code)
                        .body(Body::from(text.as_bytes().to_vec()))
                        .unwrap(),
                );
            }
        });
        Client::new(service, "default")
    }

    fn app(client: Client) -> State<Arc<App>> {
        let mut registry = Registry::default();
        Metrics::register(&mut registry);
        State(Arc::new(App {
            client,
            registry,
            health: Arc::default(),
        }))
    }

    #[tokio::test]
    async fn ready_when_the_kubernetes_api_answers() {
        let response = readyz(app(answering(200, VERSION))).await.into_response();
        assert_eq!(response.status(), StatusCode::OK);
    }

    #[tokio::test]
    async fn not_ready_when_it_does_not() {
        let response = readyz(app(answering(503, "down"))).await.into_response();
        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    }

    #[tokio::test]
    async fn not_ready_on_a_stale_schema_and_it_says_what_to_run() {
        let client = answering_with(200, VERSION, r#"{"components":{"schemas":{}}}"#);
        let response = readyz(app(client)).await.into_response();
        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        let body = to_bytes(response.into_body(), usize::MAX).await.unwrap();
        assert!(String::from_utf8_lossy(&body).contains("crds apply"));
    }

    #[tokio::test]
    async fn a_standby_is_ready_and_says_so() {
        let response = readyz(app(answering(200, VERSION))).await.into_response();
        let body = to_bytes(response.into_body(), usize::MAX).await.unwrap();
        assert_eq!(&body[..], b"ok (standby)");
    }

    #[test]
    fn the_scope_is_every_namespace_or_a_list() {
        assert_eq!(Scope::parse("*"), Scope::Any);
        let Scope::Listed(namespaces) = Scope::parse("a, b,,") else {
            panic!("a list");
        };
        assert_eq!(namespaces.into_iter().collect::<Vec<_>>(), ["a", "b"]);
    }

    #[tokio::test]
    async fn metrics_are_openmetrics() {
        let response = metrics(app(answering(200, VERSION))).await.into_response();
        assert_eq!(response.headers()[header::CONTENT_TYPE], OPENMETRICS);
        let body = to_bytes(response.into_body(), usize::MAX).await.unwrap();
        assert!(String::from_utf8_lossy(&body).ends_with("# EOF\n"));
    }
}
