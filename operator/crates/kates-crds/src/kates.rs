use k8s_openapi::api::core::v1::{EnvVar, ResourceRequirements};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::Condition;
use kube::{CustomResource, KubeSchema};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

/// One Kates backend, installed and kept running by the operator.
#[derive(CustomResource, KubeSchema, Serialize, Deserialize, Clone, Debug)]
#[kube(
    group = "kates.io",
    version = "v1alpha1",
    kind = "Kates",
    plural = "kates",
    singular = "kates",
    namespaced,
    status = "KatesStatus",
    doc = "One Kates backend. crd-version: 2",
    cel,
    // The name becomes a Service's, and the pods' with a suffix.
    validation = Rule::new("self.metadata.name.matches('^[a-z]([-a-z0-9]{0,50}[a-z0-9])?$')")
        .message("name must start with a letter and hold at most 52 lower-case letters, digits and -: it names the Service and the pods"),
    // A quorum's size and volumes are set once: changing them is scaling, which O5 adds.
    validation = Rule::new("self.spec.replicas == oldSelf.spec.replicas")
        .message("spec.replicas cannot change: delete and recreate the resource"),
    validation = Rule::new("has(self.spec.quorum) == has(oldSelf.spec.quorum) && (!has(self.spec.quorum) || self.spec.quorum == oldSelf.spec.quorum)")
        .message("spec.quorum cannot change once set: its volumes are made with the pods"),
    validation = Rule::new("!has(self.spec.quorum) || self.spec.replicas > 1")
        .message("spec.quorum goes with replicas 3 or 5"),
    printcolumn = r#"{"name":"Version","type":"string","jsonPath":".status.version"}"#,
    printcolumn = r#"{"name":"Replicas","type":"integer","jsonPath":".spec.replicas"}"#,
    printcolumn = r#"{"name":"Leader","type":"string","jsonPath":".status.leader"}"#,
    printcolumn = r#"{"name":"Ready","type":"string","jsonPath":".status.conditions[?(@.type=='Ready')].status"}"#,
    printcolumn = r#"{"name":"Mode","type":"string","jsonPath":".spec.mode"}"#,
    printcolumn = r#"{"name":"Drained","type":"string","jsonPath":".status.conditions[?(@.type=='Drained')].status"}"#,
    printcolumn = r#"{"name":"Age","type":"date","jsonPath":".metadata.creationTimestamp"}"#
)]
#[serde(rename_all = "camelCase")]
pub struct KatesSpec {
    /// The image tag to run, such as 1.39.1 or 1.39.1-native.
    #[schemars(length(min = 1, max = 128))]
    pub version: String,
    /// Drain: the backend starts no new run or fault and lets the live ones finish, and a pod
    /// whose version changed is replaced only once nothing is live. The operator never sets it
    /// back to Active.
    #[serde(default)]
    pub mode: Mode,
    /// How many members: 1, or 3 or 5 for a quorum that keeps working with one or two of them
    /// gone. Fixed once the resource exists.
    #[serde(default = "one")]
    #[x_kube(validation = Rule::new("self in [1, 3, 5]").message("replicas must be 1, 3 or 5"))]
    pub replicas: i32,
    /// The members' quorum, with replicas 3 or 5. Fixed once set.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub quorum: Option<Quorum>,
    /// The image repository. Empty: ghcr.io/bmscomp/kates.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub image: Option<String>,
    pub database: Database,
    pub kafka: Kafka,
    /// The Secret holding the API key. Without it the API asks for no key. The operator reads it
    /// to drain the backend, and its chart lets it read only a Secret named kates-api-key.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub api_key_secret_ref: Option<SecretKey>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub resources: Option<ResourceRequirements>,
    /// Further settings of the Kates container. The names the operator sets from the fields
    /// above are refused.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    #[x_kube(
        validation = Rule::new("self.all(e, !e.name.startsWith('QUARKUS_DATASOURCE_') && !e.name.startsWith('KATES_QUORUM_') && !(e.name in ['KATES_KAFKA_BOOTSTRAP_SERVERS', 'KATES_KAFKA_SECURITY_PROTOCOL', 'KATES_KAFKA_SASL_MECHANISM', 'KATES_KAFKA_SASL_USERNAME', 'KATES_KAFKA_SASL_PASSWORD', 'KATES_API_KEY', 'KATES_API_SECURITY_ENABLED', 'POD_NAME']))")
            .message("env must not name what database, kafka, apiKeySecretRef and the quorum set")
    )]
    #[x_kube(merge_strategy = ListMerge::Map(vec!["name".into()]))]
    #[schemars(length(max = 256))]
    pub env: Vec<EnvVar>,
}

fn one() -> i32 {
    1
}

/// Each member keeps its Raft term and vote on a volume of its own.
#[derive(Serialize, Deserialize, JsonSchema, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Quorum {
    /// The size of each member's volume, such as 1Gi. Empty: 1Gi.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schemars(regex(pattern = r"^[0-9]+(Mi|Gi)$"))]
    pub storage: Option<String>,
    /// The `StorageClass` of each member's volume. Empty: the cluster's default.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub storage_class_name: Option<String>,
}

#[derive(Serialize, Deserialize, JsonSchema, Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Mode {
    #[default]
    Active,
    Drain,
}

/// The `PostgreSQL` database Kates stores its results in. The operator does not create it.
#[derive(Serialize, Deserialize, JsonSchema, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Database {
    /// A Secret with the keys url (a jdbc:postgresql:// URL), username and password.
    pub secret_ref: SecretName,
}

#[derive(Serialize, Deserialize, JsonSchema, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Kafka {
    #[schemars(length(min = 1))]
    pub bootstrap_servers: String,
    /// PLAINTEXT, SSL, `SASL_PLAINTEXT` or `SASL_SSL`. Empty: the image's default.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub security_protocol: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub sasl: Option<Sasl>,
}

#[derive(Serialize, Deserialize, JsonSchema, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Sasl {
    /// Such as SCRAM-SHA-512.
    pub mechanism: String,
    pub username: String,
    pub password_secret_ref: SecretKey,
}

#[derive(Serialize, Deserialize, JsonSchema, Clone, Debug)]
pub struct SecretName {
    #[schemars(length(min = 1))]
    pub name: String,
}

#[derive(Serialize, Deserialize, JsonSchema, Clone, Debug)]
pub struct SecretKey {
    #[schemars(length(min = 1))]
    pub name: String,
    #[schemars(length(min = 1))]
    pub key: String,
}

#[derive(Serialize, Deserialize, KubeSchema, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct KatesStatus {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub observed_generation: Option<i64>,
    /// The version every pod runs, once the last change has rolled out.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub version: Option<String>,
    #[serde(default)]
    pub ready_replicas: i32,
    /// The member that leads the quorum, which the Service sends traffic to.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub leader: Option<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    #[x_kube(merge_strategy = ListMerge::Map(vec!["type".into()]))]
    pub conditions: Vec<Condition>,
}
