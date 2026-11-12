//! `Kates`: one backend, as a `StatefulSet` of one pod with its Services. Section 4 of
//! plans/operator-kates-kind.md. Every object is owned by the resource, so deleting it removes
//! them, and each is applied whole on every pass.

use std::sync::Arc;
use std::time::Duration;

use jiff::Timestamp;
use k8s_openapi::api::apps::v1::StatefulSet;
use k8s_openapi::api::core::v1::{Service, ServiceAccount};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::OwnerReference;
use kates_crds::{Kates, KatesSpec, KatesStatus, SecretKey};
use kube::api::{Api, Patch, PatchParams};
use kube::runtime::controller::Action;
use kube::{Resource as _, ResourceExt as _};
use serde_json::{Value, json};

use super::{Error, paused};
use crate::context::{Context, Note};
use crate::status;
use crate::telemetry::current_trace_id;

const KIND: &str = "Kates";
const WRITE_RETRY: Duration = Duration::from_secs(30);
const NAME_RETRY: Duration = Duration::from_mins(1);
const DEFAULT_IMAGE: &str = "ghcr.io/bmscomp/kates";

/// # Errors
/// A failed Kubernetes write.
#[tracing::instrument(skip_all, fields(name = %kates.name_any(), namespace = ?kates.namespace()))]
pub async fn reconcile(kates: Arc<Kates>, ctx: Arc<Context>) -> Result<Action, Error> {
    let _timer = ctx.metrics.reconcile_started(KIND, current_trace_id());
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
    let objects = objects(&kates);
    let peers = objects.peers["metadata"]["name"]
        .as_str()
        .unwrap_or_default();

    let before = kates.status.clone().unwrap_or_default();
    let mut after = before.clone();
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
        for service in [&objects.service, &objects.peers] {
            let name = service["metadata"]["name"].as_str().unwrap_or_default();
            services
                .patch(name, &params, &Patch::Apply(service))
                .await?;
        }
        let set = sets
            .patch(&name, &params, &Patch::Apply(&objects.set))
            .await?;
        observe(&kates, &set, &mut after, Timestamp::now());
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
    let reason = |status: &KatesStatus| {
        let ready = status::find(&status.conditions, "Ready");
        ready.map(|condition| condition.reason.clone())
    };
    if reason(&after) != reason(&before) {
        let note = match reason(&after).as_deref() {
            Some("Running") => Some((false, "Ready", format!("running {}", kates.spec.version))),
            Some("NameTaken") => taken.map(|what| (true, "NameTaken", format!("{what} exists"))),
            _ => None,
        };
        if let Some((warning, reason, message)) = note {
            let note = Note {
                warning,
                reason,
                message,
            };
            ctx.publish(&*kates, vec![note]).await;
        }
    }
    if reason(&after).as_deref() == Some("NameTaken") {
        // The other object is not watched, so its removal is only seen by looking again.
        return Ok(Action::requeue(NAME_RETRY));
    }
    // The StatefulSet is watched: its next change starts the next pass.
    Ok(Action::await_change())
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

/// What a `Kates` becomes, each as the manifest that is applied.
pub struct Objects {
    pub account: Value,
    pub service: Value,
    pub peers: Value,
    pub set: Value,
}

#[must_use]
pub fn objects(kates: &Kates) -> Objects {
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
                            "env": env(spec),
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
#[must_use]
pub fn env(spec: &KatesSpec) -> Vec<Value> {
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
        // shortcut: the pod's account is bound to no role yet, so faults are refused; drop this
        // default when the operator binds the chaos roles.
        plain("KATES_CHAOS_PROVIDER", "noop"),
    ];
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
    let rolled_out = observed.observed_generation == set.metadata.generation
        && observed.update_revision.is_some()
        && observed.update_revision == observed.current_revision;
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
    fn the_database_comes_from_the_secret_and_never_from_a_value() {
        let env = env(&kates(json!({})).spec);
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
        let env = env(&kates(json!({
            "apiKeySecretRef": {"name": "keys", "key": "api-key"},
            "kafka": {"bootstrapServers": "kafka:9093", "securityProtocol": "SASL_PLAINTEXT",
                "sasl": {"mechanism": "SCRAM-SHA-512", "username": "kates",
                    "passwordSecretRef": {"name": "kates-backend", "key": "password"}}},
        }))
        .spec);
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
        let env = env(&kates(json!({"env": [
            {"name": "KATES_CHAOS_PROVIDER", "value": "kubernetes"},
            {"name": "KATES_DEFAULTS_REPLICATION_FACTOR", "value": "1"},
        ]}))
        .spec);
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
        let objects = objects(&kates(json!({"image": "registry.local/kates"})));
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
