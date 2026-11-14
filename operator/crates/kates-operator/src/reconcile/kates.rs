//! `Kates`: one backend, as a `StatefulSet` of one pod with its Services. Section 4 of
//! plans/operator-kates-kind.md. Every object is owned by the resource, so deleting it removes
//! them, and each is applied whole on every pass. The one exception is the `ClusterRoleBinding`
//! that gives the pod its rights: a namespaced owner cannot hold a cluster-scoped object, so a
//! finalizer removes it.
//!
//! The pod is replaced by the operator, not by the `StatefulSet` (`OnDelete`): under `mode: Drain`
//! it asks the backend to start nothing new and waits for `/api/drain` to say nothing is live.

use std::collections::BTreeSet;
use std::sync::Arc;
use std::time::Duration;

use jiff::Timestamp;
use k8s_openapi::api::apps::v1::StatefulSet;
use k8s_openapi::api::core::v1::{Pod, Secret, Service, ServiceAccount};
use k8s_openapi::api::rbac::v1::ClusterRoleBinding;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::OwnerReference;
use kates_client::Drain;
use kates_crds::{Kates, KatesSpec, KatesStatus, Mode, SecretKey};
use kube::api::{Api, DeleteParams, ListParams, Patch, PatchParams, Preconditions};
use kube::runtime::controller::Action;
use kube::runtime::finalizer::{self, Event as Finalizer};
use kube::{Resource as _, ResourceExt as _};
use secrecy::SecretString;
use serde_json::{Value, json};

use super::{Error, paused};
use crate::context::{Context, Note};
use crate::status;
use crate::telemetry::current_trace_id;

const KIND: &str = "Kates";
pub const FINALIZER: &str = "kates.io/cluster-role-binding";
const WRITE_RETRY: Duration = Duration::from_secs(30);
const NAME_RETRY: Duration = Duration::from_mins(1);
const DEFAULT_IMAGE: &str = "ghcr.io/bmscomp/kates";
const DRAINED: &str = "Drained";
// The one Secret name the chart lets the operator read for the key: RBAC narrows only by name.
const KEY_SECRET: &str = "kates-api-key";
// /api/drain is not watched: while live work finishes, the operator looks again this often.
const DRAIN_POLL: Duration = Duration::from_secs(5);
const REVISION: &str = "controller-revision-hash";

/// # Errors
/// A failed Kubernetes write.
#[tracing::instrument(skip_all, fields(name = %kates.name_any(), namespace = ?kates.namespace()))]
pub async fn reconcile(kates: Arc<Kates>, ctx: Arc<Context>) -> Result<Action, Error> {
    let _timer = ctx.metrics.reconcile_started(KIND, current_trace_id());
    let namespace = kates.namespace().ok_or(Error::NoNamespace)?;
    // Without a role there is no binding, so no finalizer: one in a namespace-scoped install
    // would hold the namespace's deletion on an operator that is being deleted with it. One left
    // from when this namespace had a role still runs its cleanup.
    if ctx.backend_role_in(&namespace).is_none() && !bound_before(&kates) {
        return apply(&kates, &ctx).await;
    }
    let api = Api::<Kates>::namespaced(ctx.client.clone(), &namespace);
    finalizer::finalizer(&api, FINALIZER, kates, |event| async {
        match event {
            Finalizer::Apply(kates) => apply(&kates, &ctx).await,
            Finalizer::Cleanup(kates) => cleanup(&kates, &ctx).await,
        }
    })
    .await
    .map_err(|error| Error::Finalizer(Box::new(error)))
}

fn bound_before(kates: &Kates) -> bool {
    kates.finalizers().iter().any(|f| f == FINALIZER)
}

/// Removes the binding, the one object no owner reference removes.
async fn cleanup(kates: &Kates, ctx: &Context) -> Result<Action, Error> {
    let namespace = kates.namespace().ok_or(Error::NoNamespace)?;
    unbind(ctx, &binding_name(&namespace, &kates.name_any())).await?;
    Ok(Action::await_change())
}

async fn unbind(ctx: &Context, binding: &str) -> Result<(), kube::Error> {
    let bindings = Api::<ClusterRoleBinding>::all(ctx.client.clone());
    match bindings.delete(binding, &DeleteParams::default()).await {
        Ok(_) => Ok(()),
        Err(kube::Error::Api(response)) if response.code == 404 => Ok(()),
        Err(error) => Err(error),
    }
}

/// Removes, at start, the bindings to the backend's role in namespaces the installer does not
/// name. No pass can be trusted with it: a namespace taken off the list may no longer be watched,
/// and the finalizer that records a binding is its editors' to remove.
///
/// # Errors
/// A failed list or delete: the operator must not run while a revoked namespace keeps the role.
pub async fn sweep(ctx: &Context) -> Result<(), kube::Error> {
    let Some(role) = &ctx.backend_role else {
        return Ok(());
    };
    let ours = ListParams::default().labels("app.kubernetes.io/managed-by=kates-operator");
    let bindings = Api::<ClusterRoleBinding>::all(ctx.client.clone());
    for binding in bindings.list(&ours).await? {
        if revoked(&binding, role, &ctx.backend_namespaces) {
            tracing::warn!(binding = %binding.name_any(), "namespace not in the list: unbinding");
            unbind(ctx, &binding.name_any()).await?;
        }
    }
    Ok(())
}

/// A binding to `role` with a subject outside `namespaces`. Another release's role is not ours.
#[must_use]
pub fn revoked(binding: &ClusterRoleBinding, role: &str, namespaces: &BTreeSet<String>) -> bool {
    binding.role_ref.name == role
        && binding.subjects.iter().flatten().any(|subject| {
            subject
                .namespace
                .as_ref()
                .is_none_or(|namespace| !namespaces.contains(namespace))
        })
}

async fn apply(kates: &Kates, ctx: &Context) -> Result<Action, Error> {
    let namespace = kates.namespace().ok_or(Error::NoNamespace)?;
    let name = kates.name_any();
    if paused(kates.annotations()) {
        return Ok(Action::await_change());
    }

    let uid = kates.uid().unwrap_or_default();
    let client = &ctx.client;
    let accounts = Api::<ServiceAccount>::namespaced(client.clone(), &namespace);
    let services = Api::<Service>::namespaced(client.clone(), &namespace);
    let sets = Api::<StatefulSet>::namespaced(client.clone(), &namespace);
    let objects = objects(kates, ctx.backend_role_in(&namespace).is_some());
    let peers = objects.peers["metadata"]["name"]
        .as_str()
        .unwrap_or_default();

    let before = kates.status.clone().unwrap_or_default();
    let mut after = before.clone();
    let (mut drain, mut replaced) = (None, None);
    // Applying over an object of the same name would hand it to whoever may create a Kates:
    // an existing ServiceAccount to run their image as, or a Service to point elsewhere.
    let taken = [
        taken(&accounts, &name, &uid).await?,
        taken(&services, &name, &uid).await?,
        taken(&services, peers, &uid).await?,
        taken(&sets, &name, &uid).await?,
    ];
    let taken = taken.into_iter().flatten().next();
    if let Some(what) = &taken {
        let message = format!("{what} exists and is not this resource's: rename one of them");
        let generation = kates.metadata.generation;
        after.observed_generation = generation;
        let conditions = &mut after.conditions;
        let now = Timestamp::now();
        status::set(
            conditions,
            "Ready",
            "False",
            "NameTaken",
            &message,
            generation,
            now,
        );
    } else {
        let params = PatchParams::apply(&ctx.field_manager).force();
        accounts
            .patch(&name, &params, &Patch::Apply(&objects.account))
            .await?;
        // Before the StatefulSet, so the pod starts with its rights.
        if let Some(role) = ctx.backend_role_in(&namespace) {
            let binding = binding(kates, role);
            Api::<ClusterRoleBinding>::all(client.clone())
                .patch(
                    &binding_name(&namespace, &name),
                    &params,
                    &Patch::Apply(&binding),
                )
                .await?;
        }
        for service in [&objects.service, &objects.peers] {
            let name = service["metadata"]["name"].as_str().unwrap_or_default();
            services
                .patch(name, &params, &Patch::Apply(service))
                .await?;
        }
        let set = sets
            .patch(&name, &params, &Patch::Apply(&objects.set))
            .await?;
        (drain, replaced) = roll(kates, ctx, &set, &before, &mut after).await?;
    }

    if after != before {
        let patch = json!({
            "apiVersion": "kates.io/v1alpha1",
            "kind": KIND,
            "status": after,
        });
        let params = PatchParams::apply(&ctx.field_manager).force();
        Api::<Kates>::namespaced(client.clone(), &namespace)
            .patch_status(&name, &params, &Patch::Apply(patch))
            .await?;
    }
    announce(kates, ctx, &before, &after, taken, replaced).await;
    let reason = |status: &KatesStatus| {
        let ready = status::find(&status.conditions, "Ready");
        ready.map(|condition| condition.reason.clone())
    };
    if reason(&after).as_deref() == Some("NameTaken") {
        // The other object is not watched, so its removal is only seen by looking again.
        return Ok(Action::requeue(NAME_RETRY));
    }
    // The StatefulSet is watched: its next change starts the next pass. Neither /api/drain nor
    // the key's Secret is.
    Ok(match &drain {
        Some(Ok(state)) if kates.spec.mode == Mode::Drain && !state.drained => {
            Action::requeue(DRAIN_POLL)
        }
        Some(Err(unknown)) if unknown.retry => Action::requeue(WRITE_RETRY),
        Some(Err(_)) => Action::requeue(NAME_RETRY),
        _ => Action::await_change(),
    })
}

/// Tells `kubectl describe` of a change of Ready and of a pod replaced.
async fn announce(
    kates: &Kates,
    ctx: &Context,
    before: &KatesStatus,
    after: &KatesStatus,
    taken: Option<String>,
    replaced: Option<String>,
) {
    let reason = |status: &KatesStatus| {
        let ready = status::find(&status.conditions, "Ready");
        ready.map(|condition| condition.reason.clone())
    };
    let mut notes = Vec::new();
    if reason(after) != reason(before) {
        let note = match reason(after).as_deref() {
            Some("Running") => Some((false, "Ready", format!("running {}", kates.spec.version))),
            Some("NameTaken") => taken.map(|what| (true, "NameTaken", format!("{what} exists"))),
            _ => None,
        };
        if let Some((warning, reason, message)) = note {
            notes.push(Note {
                warning,
                reason,
                message,
            });
        }
    }
    if let Some(pod) = replaced {
        notes.push(Note {
            warning: false,
            reason: "Replacing",
            message: format!("replacing {pod} with {}", kates.spec.version),
        });
    }
    if !notes.is_empty() {
        ctx.publish(kates, notes).await;
    }
}

/// Reads the `StatefulSet` and the drain into `after`, and replaces a stale pod unless a drain
/// holds it. Answers the drain and the pod replaced.
async fn roll(
    kates: &Kates,
    ctx: &Context,
    set: &StatefulSet,
    before: &KatesStatus,
    after: &mut KatesStatus,
) -> Result<(Option<Result<Drain, Unknown>>, Option<String>), Error> {
    let namespace = kates.namespace().ok_or(Error::NoNamespace)?;
    let now = Timestamp::now();
    observe(kates, set, after, now);
    let drain = sync_drain(kates, ctx, &namespace, before, after.ready_replicas > 0).await;
    mark_drain(kates, drain.as_ref(), after, now);
    let Some(pod) = stale_pod(ctx, &namespace, set).await? else {
        return Ok((drain, None));
    };
    if held(kates.spec.mode, drain.as_ref()) {
        let message = "the pod is replaced once the drain ends";
        let generation = kates.metadata.generation;
        let conditions = &mut after.conditions;
        status::set(
            conditions,
            "Ready",
            "False",
            "RollingOut",
            message,
            generation,
            now,
        );
        return Ok((drain, None));
    }
    replace(ctx, &namespace, &pod).await?;
    Ok((drain, Some(pod.name_any())))
}

/// Why the backend's drain state is not known this pass.
#[derive(Debug)]
pub struct Unknown {
    reason: &'static str,
    message: String,
    /// The backend may answer on the next try; otherwise something only a person changes.
    retry: bool,
}

/// Sets the backend's switch to the mode and answers its state. `None`: Active and never
/// drained, so there is nothing to lift and no call.
async fn sync_drain(
    kates: &Kates,
    ctx: &Context,
    namespace: &str,
    before: &KatesStatus,
    ready: bool,
) -> Option<Result<Drain, Unknown>> {
    let want = kates.spec.mode == Mode::Drain;
    if !want && status::find(&before.conditions, DRAINED).is_none() {
        return None;
    }
    if !ready {
        return Some(Err(Unknown {
            reason: "NotReady",
            message: "the pod is not ready".to_owned(),
            retry: false,
        }));
    }
    Some(ask(kates, ctx, namespace, want).await)
}

async fn ask(kates: &Kates, ctx: &Context, namespace: &str, want: bool) -> Result<Drain, Unknown> {
    let key = key(kates, ctx, namespace).await?;
    let url = format!("http://{}.{namespace}.svc:8080", kates.name_any());
    let api = ctx.connections.client_for(&url).map_err(|error| Unknown {
        reason: "Unavailable",
        message: error.to_string(),
        retry: true,
    })?;
    let state = api.drain(&key).await.map_err(|error| refused(&error))?;
    if state.draining == want {
        return Ok(state);
    }
    api.set_drain(&key, want)
        .await
        .map_err(|error| refused(&error))
}

/// The key the pod runs with. Without `apiKeySecretRef` the API asks for none.
async fn key(kates: &Kates, ctx: &Context, namespace: &str) -> Result<SecretString, Unknown> {
    let Some(from) = &kates.spec.api_key_secret_ref else {
        return Ok(SecretString::from(String::new()));
    };
    let unreadable = |why: &str| Unknown {
        reason: "KeyUnreadable",
        message: format!(
            "key {} of Secret {}: {why}; the operator's chart lets it read only a Secret named {KEY_SECRET}",
            from.key, from.name
        ),
        retry: false,
    };
    let secrets = Api::<Secret>::namespaced(ctx.client.clone(), namespace);
    let secret = secrets
        .get_opt(&from.name)
        .await
        .map_err(|error| unreadable(&error.to_string()))?
        .ok_or_else(|| unreadable("not found"))?;
    let value = secret
        .data
        .as_ref()
        .and_then(|data| data.get(&from.key))
        .ok_or_else(|| unreadable("no such key"))?;
    let text = std::str::from_utf8(&value.0).map_err(|_| unreadable("not text"))?;
    Ok(SecretString::from(text.trim().to_owned()))
}

#[must_use]
pub fn refused(error: &kates_client::Error) -> Unknown {
    use kates_client::Error as E;
    match error {
        E::Rejected { status: 404, .. }
        | E::Unavailable {
            status: Some(404), ..
        } => Unknown {
            reason: "DrainUnsupported",
            message: "the backend has no /api/drain: its version predates the drain switch"
                .to_owned(),
            retry: false,
        },
        E::InvalidKey | E::MissingScope => Unknown {
            reason: "KeyRefused",
            message: format!("{error}: the drain needs the admin scope"),
            retry: false,
        },
        _ => Unknown {
            reason: "Unavailable",
            message: error.to_string(),
            retry: true,
        },
    }
}

/// The `Drained` condition, shown only while there is a drain to report or to lift.
pub fn mark_drain(
    kates: &Kates,
    drain: Option<&Result<Drain, Unknown>>,
    status: &mut KatesStatus,
    now: Timestamp,
) {
    let generation = kates.metadata.generation;
    let conditions = &mut status.conditions;
    let mut set = |value, reason, message: &str| {
        status::set(conditions, DRAINED, value, reason, message, generation, now);
    };
    match (kates.spec.mode, drain) {
        (Mode::Active, None | Some(Ok(_))) => status::remove(conditions, DRAINED),
        (Mode::Drain, Some(Ok(state))) if state.drained => {
            set("True", "Drained", "nothing is live");
        }
        (Mode::Drain, Some(Ok(state))) => set(
            "False",
            "Draining",
            &format!(
                "{} runs, {} faults and {} webhook deliveries live, or a fault still holds its lease",
                state.live_runs, state.faults_in_flight, state.webhooks_in_flight
            ),
        ),
        // Kept while Active too, so the next pass tries the lift again.
        (_, Some(Err(unknown))) => set("False", unknown.reason, &unknown.message),
        (Mode::Drain, None) => {}
    }
}

/// Whether a stale pod must wait: under Drain, until the backend says nothing is live.
#[must_use]
pub fn held(mode: Mode, drain: Option<&Result<Drain, Unknown>>) -> bool {
    mode == Mode::Drain && !matches!(drain, Some(Ok(state)) if state.drained)
}

/// The pod, when it runs a revision the `StatefulSet` no longer asks for. With `OnDelete` it
/// stays until the operator deletes it.
async fn stale_pod(
    ctx: &Context,
    namespace: &str,
    set: &StatefulSet,
) -> Result<Option<Pod>, kube::Error> {
    let observed = set.status.clone().unwrap_or_default();
    let Some(update) = observed
        .update_revision
        .filter(|_| observed.observed_generation == set.metadata.generation)
    else {
        return Ok(None);
    };
    let pods = Api::<Pod>::namespaced(ctx.client.clone(), namespace);
    let pod = pods.get_opt(&format!("{}-0", set.name_any())).await?;
    Ok(pod.filter(|pod| stale(pod, &update)))
}

#[must_use]
pub fn stale(pod: &Pod, update_revision: &str) -> bool {
    pod.metadata.deletion_timestamp.is_none()
        && pod
            .labels()
            .get(REVISION)
            .is_some_and(|revision| revision != update_revision)
}

/// Deletes that pod and no other: a pod recreated since the read keeps running.
async fn replace(ctx: &Context, namespace: &str, pod: &Pod) -> Result<(), kube::Error> {
    let pods = Api::<Pod>::namespaced(ctx.client.clone(), namespace);
    let params = DeleteParams {
        preconditions: Some(Preconditions {
            uid: pod.uid(),
            resource_version: None,
        }),
        ..DeleteParams::default()
    };
    match pods.delete(&pod.name_any(), &params).await {
        Ok(_) => Ok(()),
        Err(kube::Error::Api(response)) if matches!(response.code, 404 | 409) => Ok(()),
        Err(error) => Err(error),
    }
}

#[allow(clippy::needless_pass_by_value)]
pub fn error_policy(kates: Arc<Kates>, error: &Error, ctx: Arc<Context>) -> Action {
    ctx.metrics.reconcile_failed(KIND, error.name());
    tracing::warn!(%error, name = %kates.name_any(), "reconcile failed");
    Action::requeue(WRITE_RETRY)
}

/// The kind and name of an object that exists under `name` without being controlled by the
/// `Kates` with this uid, or `None` when the name is free or already this resource's.
async fn taken<K>(api: &Api<K>, name: &str, uid: &str) -> Result<Option<String>, kube::Error>
where
    K: kube::Resource<DynamicType = ()> + Clone + serde::de::DeserializeOwned + std::fmt::Debug,
{
    let found = api.get_opt(name).await?;
    Ok(found
        .filter(|object| !controlled_by(object.owner_references(), uid))
        .map(|_| format!("{} {name}", K::kind(&()))))
}

fn controlled_by(owners: &[OwnerReference], uid: &str) -> bool {
    let controls = |owner: &OwnerReference| owner.controller == Some(true) && owner.uid == uid;
    owners.iter().any(controls)
}

/// Dots cannot appear in a namespace or a `Kates` name, so no two resources share one.
#[must_use]
pub fn binding_name(namespace: &str, name: &str) -> String {
    format!("kates-backend.{namespace}.{name}")
}

/// Binds the pod's `ServiceAccount` to the `ClusterRole` the chart ships. The operator holds
/// `bind` on that role, never its rights.
#[must_use]
pub fn binding(kates: &Kates, role: &str) -> Value {
    let name = kates.name_any();
    let namespace = kates.namespace().unwrap_or_default();
    json!({
        "apiVersion": "rbac.authorization.k8s.io/v1",
        "kind": "ClusterRoleBinding",
        "metadata": {
            "name": binding_name(&namespace, &name),
            "labels": {
                "app.kubernetes.io/name": "kates",
                "app.kubernetes.io/instance": name,
                "app.kubernetes.io/managed-by": "kates-operator",
            },
        },
        "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": role},
        "subjects": [{"kind": "ServiceAccount", "name": name, "namespace": namespace}],
    })
}

/// What a `Kates` becomes, each as the manifest that is applied.
pub struct Objects {
    pub account: Value,
    pub service: Value,
    pub peers: Value,
    pub set: Value,
}

/// `bound`: the pod's account gets the backend's role, so faults are not turned off.
#[must_use]
pub fn objects(kates: &Kates, bound: bool) -> Objects {
    let name = kates.name_any();
    let peers = format!("{name}-peers");
    let selector = json!({
        "app.kubernetes.io/name": "kates",
        "app.kubernetes.io/instance": name,
    });
    let mut labels = selector.clone();
    labels["app.kubernetes.io/managed-by"] = json!("kates-operator");
    let meta = |name: &str| {
        json!({
            "name": name,
            "labels": labels,
            "ownerReferences": [kates.controller_owner_ref(&())],
        })
    };
    let ports = json!([
        {"name": "http", "port": 8080, "targetPort": "http", "protocol": "TCP"},
        {"name": "grpc", "port": 9000, "targetPort": "grpc", "protocol": "TCP"},
    ]);
    let spec = &kates.spec;
    let image = spec.image.as_deref().unwrap_or(DEFAULT_IMAGE);
    let probe = |path: &str| json!({"httpGet": {"path": path, "port": "http"}});
    let mut startup = probe("/q/health/started");
    startup["failureThreshold"] = json!(60);
    startup["periodSeconds"] = json!(2);
    startup["timeoutSeconds"] = json!(3);
    let mut readiness = probe("/q/health/ready");
    readiness["timeoutSeconds"] = json!(5);
    let mut liveness = probe("/q/health/live");
    liveness["periodSeconds"] = json!(30);
    liveness["timeoutSeconds"] = json!(5);
    // Kubernetes' default is 300 s: the time the API stays gone when its node dies.
    let gone = |key: &str| json!({"key": key, "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 30});

    Objects {
        account: json!({"apiVersion": "v1", "kind": "ServiceAccount", "metadata": meta(&name)}),
        service: json!({
            "apiVersion": "v1", "kind": "Service", "metadata": meta(&name),
            "spec": {"selector": selector, "ports": ports},
        }),
        // Names each pod for its peers: serviceName cannot change once the StatefulSet exists.
        peers: json!({
            "apiVersion": "v1", "kind": "Service", "metadata": meta(&peers),
            "spec": {"clusterIP": "None", "publishNotReadyAddresses": true,
                "selector": selector, "ports": ports},
        }),
        set: json!({
            "apiVersion": "apps/v1", "kind": "StatefulSet", "metadata": meta(&name),
            "spec": {
                "replicas": 1,
                "serviceName": peers,
                // The operator replaces the pod, once a drain allows it.
                "updateStrategy": {"type": "OnDelete"},
                "selector": {"matchLabels": selector},
                "template": {
                    "metadata": {"labels": labels},
                    "spec": {
                        "serviceAccountName": name,
                        "terminationGracePeriodSeconds": 60,
                        "securityContext": {"runAsNonRoot": true, "runAsUser": 100, "fsGroup": 100,
                            "seccompProfile": {"type": "RuntimeDefault"}},
                        "containers": [{
                            "name": "kates",
                            "image": format!("{image}:{}", spec.version),
                            "ports": [
                                {"name": "http", "containerPort": 8080, "protocol": "TCP"},
                                {"name": "grpc", "containerPort": 9000, "protocol": "TCP"},
                            ],
                            "env": env(spec, bound),
                            "resources": spec.resources,
                            "securityContext": {"allowPrivilegeEscalation": false,
                                "readOnlyRootFilesystem": true, "runAsNonRoot": true,
                                "capabilities": {"drop": ["ALL"]}},
                            "volumeMounts": [{"name": "tmp", "mountPath": "/tmp"}],
                            "startupProbe": startup,
                            "readinessProbe": readiness,
                            "livenessProbe": liveness,
                            // Lets the Service drop the pod before it stops answering.
                            "lifecycle": {"preStop": {"exec": {"command": ["sh", "-c", "sleep 5"]}}},
                        }],
                        "volumes": [{"name": "tmp", "emptyDir": {}}],
                        "tolerations": [
                            gone("node.kubernetes.io/not-ready"),
                            gone("node.kubernetes.io/unreachable"),
                        ],
                    },
                },
            },
        }),
    }
}

/// The container's environment: what the typed fields set, then `spec.env`, which replaces a
/// default of the same name. The CRD refuses the names of the typed fields in `spec.env`.
/// Unbound, as in the Kates chart's namespace scope, faults default to off: the backend could
/// read nothing they need.
#[must_use]
pub fn env(spec: &KatesSpec, bound: bool) -> Vec<Value> {
    let plain = |name: &str, value: &str| json!({"name": name, "value": value});
    let secret = |name: &str, from: &SecretKey| json!({"name": name, "valueFrom": {"secretKeyRef": {"name": from.name, "key": from.key}}});
    let database = |name: &str, key: &str| {
        let name = format!("QUARKUS_DATASOURCE_{name}");
        let from = SecretKey {
            name: spec.database.secret_ref.name.clone(),
            key: key.to_owned(),
        };
        secret(&name, &from)
    };
    let mut env = vec![
        plain("QUARKUS_DATASOURCE_DB_KIND", "postgresql"),
        database("JDBC_URL", "url"),
        database("USERNAME", "username"),
        database("PASSWORD", "password"),
        plain("QUARKUS_HIBERNATE_ORM_DATABASE_GENERATION", "none"),
        plain("QUARKUS_FLYWAY_MIGRATE_AT_START", "true"),
        plain("QUARKUS_FLYWAY_BASELINE_ON_MIGRATE", "true"),
        plain(
            "KATES_KAFKA_BOOTSTRAP_SERVERS",
            &spec.kafka.bootstrap_servers,
        ),
        // Off as in the Kates chart: without a collector every export is a warning.
        plain("QUARKUS_OTEL_ENABLED", "false"),
    ];
    if !bound {
        env.push(plain("KATES_CHAOS_PROVIDER", "noop"));
    }
    if let Some(protocol) = &spec.kafka.security_protocol {
        env.push(plain("KATES_KAFKA_SECURITY_PROTOCOL", protocol));
    }
    if let Some(sasl) = &spec.kafka.sasl {
        env.push(plain("KATES_KAFKA_SASL_MECHANISM", &sasl.mechanism));
        env.push(plain("KATES_KAFKA_SASL_USERNAME", &sasl.username));
        env.push(secret(
            "KATES_KAFKA_SASL_PASSWORD",
            &sasl.password_secret_ref,
        ));
    }
    let key = spec.api_key_secret_ref.as_ref();
    env.push(plain(
        "KATES_API_SECURITY_ENABLED",
        &key.is_some().to_string(),
    ));
    if let Some(key) = key {
        env.push(secret("KATES_API_KEY", key));
    }
    for extra in &spec.env {
        env.retain(|default| default["name"] != extra.name);
        env.push(json!(extra));
    }
    env
}

/// Reads the `StatefulSet` into `status`. Ready means the one pod is ready and runs what the
/// resource asks for now, so a roll in progress is not Ready.
pub fn observe(kates: &Kates, set: &StatefulSet, status: &mut KatesStatus, now: Timestamp) {
    let observed = set.status.clone().unwrap_or_default();
    // Not currentRevision: under OnDelete the StatefulSet never moves it.
    let rolled_out = observed.observed_generation == set.metadata.generation
        && observed.update_revision.is_some()
        && observed.updated_replicas.unwrap_or(0) >= 1;
    status.ready_replicas = observed.ready_replicas.unwrap_or(0);
    status.observed_generation = kates.metadata.generation;
    let (value, reason, message) = match (rolled_out, status.ready_replicas > 0) {
        (true, true) => ("True", "Running", ""),
        (true, false) => ("False", "NotReady", "the pod is not ready"),
        (false, _) => ("False", "RollingOut", "the pod is being replaced"),
    };
    if value == "True" {
        status.version = Some(kates.spec.version.clone());
    }
    status::set(
        &mut status.conditions,
        "Ready",
        value,
        reason,
        message,
        kates.metadata.generation,
        now,
    );
}

#[cfg(test)]
mod tests {
    use k8s_openapi::api::apps::v1::StatefulSetStatus;

    use super::*;

    fn kates(spec: Value) -> Kates {
        let mut spec = spec;
        spec["version"] = json!("1.39.1");
        spec["database"] = json!({"secretRef": {"name": "kates-db"}});
        if spec["kafka"].is_null() {
            spec["kafka"] = json!({"bootstrapServers": "kafka:9092"});
        }
        let mut kates = Kates::new("main", serde_json::from_value(spec).unwrap());
        kates.metadata.namespace = Some("kates".to_owned());
        kates.metadata.uid = Some("uid".to_owned());
        kates.metadata.generation = Some(3);
        kates
    }

    fn value<'a>(env: &'a [Value], name: &str) -> Option<&'a Value> {
        env.iter().find(|entry| entry["name"] == name)
    }

    #[test]
    fn faults_are_off_only_when_the_pod_has_no_role() {
        let spec = kates(json!({})).spec;
        let provider = |bound| value(&env(&spec, bound), "KATES_CHAOS_PROVIDER").cloned();
        assert_eq!(provider(false).unwrap()["value"], "noop");
        // Bound: the image's own default, as the Kates chart leaves it.
        assert!(provider(true).is_none());
    }

    #[test]
    fn only_this_roles_bindings_outside_the_list_are_revoked() {
        let listed = BTreeSet::from(["payments".to_owned()]);
        let bound = |namespace: &str, role: &str| -> ClusterRoleBinding {
            let mut kates = kates(json!({}));
            kates.metadata.namespace = Some(namespace.to_owned());
            serde_json::from_value(binding(&kates, role)).unwrap()
        };
        assert!(!revoked(&bound("payments", "r"), "r", &listed));
        assert!(revoked(&bound("tenant", "r"), "r", &listed));
        assert!(revoked(&bound("payments", "r"), "r", &BTreeSet::new()));
        assert!(!revoked(&bound("tenant", "other"), "r", &listed));
    }

    #[test]
    fn the_binding_names_the_pods_account_and_only_the_role_given() {
        let kates = kates(json!({}));
        let binding = binding(&kates, "kates-backend-kates-operator");
        assert_eq!(binding["metadata"]["name"], "kates-backend.kates.main");
        assert_eq!(binding["roleRef"]["name"], "kates-backend-kates-operator");
        assert_eq!(
            binding["subjects"],
            json!([{"kind": "ServiceAccount", "name": "main", "namespace": "kates"}])
        );
        let objects = objects(&kates, true);
        assert_eq!(
            objects.set["spec"]["template"]["spec"]["serviceAccountName"],
            objects.account["metadata"]["name"]
        );
        serde_json::from_value::<ClusterRoleBinding>(binding).unwrap();
        // a-b/c and a/b-c would collide on a dash.
        assert_ne!(binding_name("a-b", "c"), binding_name("a", "b-c"));
    }

    #[test]
    fn the_database_comes_from_the_secret_and_never_from_a_value() {
        let env = env(&kates(json!({})).spec, true);
        let password = value(&env, "QUARKUS_DATASOURCE_PASSWORD").unwrap();
        assert_eq!(
            password["valueFrom"]["secretKeyRef"],
            json!({"name": "kates-db", "key": "password"})
        );
        assert!(password.get("value").is_none());
        assert_eq!(
            value(&env, "KATES_API_SECURITY_ENABLED").unwrap()["value"],
            "false"
        );
        assert!(value(&env, "KATES_API_KEY").is_none());
        assert!(value(&env, "KATES_KAFKA_SASL_MECHANISM").is_none());
    }

    #[test]
    fn a_key_turns_security_on_and_sasl_sets_its_three_names() {
        let env = env(
            &kates(json!({
                "apiKeySecretRef": {"name": "keys", "key": "api-key"},
                "kafka": {"bootstrapServers": "kafka:9093", "securityProtocol": "SASL_PLAINTEXT",
                    "sasl": {"mechanism": "SCRAM-SHA-512", "username": "kates",
                        "passwordSecretRef": {"name": "kates-backend", "key": "password"}}},
            }))
            .spec,
            true,
        );
        assert_eq!(
            value(&env, "KATES_API_SECURITY_ENABLED").unwrap()["value"],
            "true"
        );
        assert_eq!(
            value(&env, "KATES_API_KEY").unwrap()["valueFrom"]["secretKeyRef"]["name"],
            "keys"
        );
        assert_eq!(
            value(&env, "KATES_KAFKA_SASL_USERNAME").unwrap()["value"],
            "kates"
        );
        assert_eq!(
            value(&env, "KATES_KAFKA_SASL_PASSWORD").unwrap()["valueFrom"]["secretKeyRef"]["key"],
            "password"
        );
    }

    #[test]
    fn spec_env_replaces_a_default_and_no_name_appears_twice() {
        let env = env(
            &kates(json!({"env": [
                {"name": "KATES_CHAOS_PROVIDER", "value": "kubernetes"},
                {"name": "KATES_DEFAULTS_REPLICATION_FACTOR", "value": "1"},
            ]}))
            .spec,
            false,
        );
        assert_eq!(
            value(&env, "KATES_CHAOS_PROVIDER").unwrap()["value"],
            "kubernetes"
        );
        let mut names: Vec<&str> = env.iter().filter_map(|e| e["name"].as_str()).collect();
        let count = names.len();
        names.sort_unstable();
        names.dedup();
        assert_eq!(names.len(), count);
    }

    #[test]
    fn every_object_is_owned_and_the_services_select_the_pod() {
        let objects = objects(&kates(json!({"image": "registry.local/kates"})), true);
        for object in [
            &objects.account,
            &objects.service,
            &objects.peers,
            &objects.set,
        ] {
            assert_eq!(object["metadata"]["ownerReferences"][0]["kind"], "Kates");
            assert_eq!(object["metadata"]["ownerReferences"][0]["controller"], true);
        }
        let pod = &objects.set["spec"]["template"];
        let selector = &objects.set["spec"]["selector"]["matchLabels"];
        assert_eq!(&objects.service["spec"]["selector"], selector);
        assert_eq!(&objects.peers["spec"]["selector"], selector);
        for (key, value) in selector.as_object().unwrap() {
            assert_eq!(&pod["metadata"]["labels"][key], value);
        }
        assert_eq!(
            objects.set["spec"]["serviceName"],
            objects.peers["metadata"]["name"]
        );
        assert_eq!(
            pod["spec"]["containers"][0]["image"],
            "registry.local/kates:1.39.1"
        );
        // The apiserver must take the manifest as a StatefulSet.
        serde_json::from_value::<StatefulSet>(objects.set).unwrap();
    }

    #[test]
    fn only_this_resource_as_controller_makes_an_object_ours() {
        let owner = |uid: &str, controller: Option<bool>| OwnerReference {
            uid: uid.to_owned(),
            controller,
            ..OwnerReference::default()
        };
        assert!(controlled_by(&[owner("uid", Some(true))], "uid"));
        assert!(!controlled_by(&[], "uid"));
        assert!(!controlled_by(&[owner("other", Some(true))], "uid"));
        // A plain owner reference is not control: anyone may add one.
        assert!(!controlled_by(&[owner("uid", None)], "uid"));
    }

    fn state(drained: bool) -> Drain {
        Drain {
            draining: true,
            drained,
            live_runs: u32::from(!drained),
            faults_in_flight: 0,
            webhooks_in_flight: 0,
        }
    }

    #[test]
    fn a_stale_pod_waits_for_the_drain_only_under_drain() {
        let busy = Some(Ok(state(false)));
        let done = Some(Ok(state(true)));
        let unknown = Some(Err(refused(&kates_client::Error::InvalidKey)));
        assert!(held(Mode::Drain, busy.as_ref()));
        assert!(held(Mode::Drain, unknown.as_ref()));
        assert!(held(Mode::Drain, None));
        assert!(!held(Mode::Drain, done.as_ref()));
        // Active replaces at once, as a RollingUpdate did.
        assert!(!held(Mode::Active, busy.as_ref()));
        assert!(!held(Mode::Active, None));
    }

    #[test]
    fn drained_reports_the_backend_and_goes_once_a_drain_is_lifted() {
        let mark = |mode: Mode, drain: Option<Result<Drain, Unknown>>, status: &mut KatesStatus| {
            let mut kates = kates(json!({}));
            kates.spec.mode = mode;
            mark_drain(&kates, drain.as_ref(), status, Timestamp::UNIX_EPOCH);
            status::find(&status.conditions, DRAINED).map(|c| (c.status.clone(), c.reason.clone()))
        };
        let mut status = KatesStatus::default();
        let busy = mark(Mode::Drain, Some(Ok(state(false))), &mut status);
        assert_eq!(busy, Some(("False".into(), "Draining".into())));
        let done = mark(Mode::Drain, Some(Ok(state(true))), &mut status);
        assert_eq!(done, Some(("True".into(), "Drained".into())));
        // A failed lift keeps the condition, so the next pass calls the backend again.
        let unreachable = kates_client::Error::Unavailable {
            status: None,
            api_message: None,
            detail: "refused".into(),
        };
        let failed = mark(Mode::Active, Some(Err(refused(&unreachable))), &mut status);
        assert_eq!(failed, Some(("False".into(), "Unavailable".into())));
        assert_eq!(
            mark(Mode::Active, Some(Ok(state(false))), &mut status),
            None
        );
    }

    #[test]
    fn an_old_backend_is_told_apart_from_a_refused_key_and_an_outage() {
        let rejected = kates_client::Error::Rejected {
            status: 404,
            message: String::new(),
            field_errors: std::collections::BTreeMap::new(),
        };
        let old = refused(&rejected);
        assert_eq!((old.reason, old.retry), ("DrainUnsupported", false));
        let key = refused(&kates_client::Error::MissingScope);
        assert_eq!((key.reason, key.retry), ("KeyRefused", false));
        let down = refused(&kates_client::Error::Unavailable {
            status: Some(503),
            api_message: None,
            detail: String::new(),
        });
        assert_eq!((down.reason, down.retry), ("Unavailable", true));
    }

    #[test]
    fn only_a_live_pod_on_another_revision_is_stale() {
        let pod = |revision: &str, deleting: bool| {
            let mut pod = Pod::default();
            pod.metadata.labels = Some([(REVISION.to_owned(), revision.to_owned())].into());
            if deleting {
                pod.metadata.deletion_timestamp = Some(
                    k8s_openapi::apimachinery::pkg::apis::meta::v1::Time(Timestamp::UNIX_EPOCH),
                );
            }
            pod
        };
        assert!(stale(&pod("old", false), "new"));
        assert!(!stale(&pod("new", false), "new"));
        assert!(!stale(&pod("old", true), "new"));
        assert!(!stale(&Pod::default(), "new"));
        let set = objects(&kates(json!({})), true).set;
        assert_eq!(set["spec"]["updateStrategy"]["type"], "OnDelete");
    }

    #[test]
    fn ready_needs_a_ready_pod_on_the_revision_asked_for() {
        let observe = |status: StatefulSetStatus| {
            let mut set = StatefulSet::default();
            set.metadata.generation = Some(2);
            set.status = Some(status);
            let mut out = KatesStatus::default();
            super::observe(&kates(json!({})), &set, &mut out, Timestamp::UNIX_EPOCH);
            let ready = status::find(&out.conditions, "Ready").unwrap();
            (ready.status.clone(), ready.reason.clone(), out.version)
        };
        let revision = |current: &str, update: &str, ready: i32| StatefulSetStatus {
            observed_generation: Some(2),
            current_revision: Some(current.to_owned()),
            update_revision: Some(update.to_owned()),
            ready_replicas: Some(ready),
            updated_replicas: Some(i32::from(current == update)),
            ..StatefulSetStatus::default()
        };
        let running = observe(revision("a", "a", 1));
        assert_eq!(
            running,
            ("True".into(), "Running".into(), Some("1.39.1".into()))
        );
        assert_eq!(observe(revision("a", "a", 0)).1, "NotReady");
        // The old pod is still ready while the new revision waits: not Ready, no version yet.
        let rolling = observe(revision("a", "b", 1));
        assert_eq!(rolling, ("False".into(), "RollingOut".into(), None));
        let stale = observe(StatefulSetStatus {
            observed_generation: Some(1),
            ..revision("a", "a", 1)
        });
        assert_eq!(stale.1, "RollingOut");
    }
}
