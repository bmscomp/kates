use k8s_openapi::apimachinery::pkg::apis::meta::v1::{Condition, Time};
use kube::{CustomResource, KubeSchema};
use schemars::{JsonSchema, Schema, SchemaGenerator, json_schema};
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// A standing compliance check of one Kafka.
#[derive(CustomResource, KubeSchema, Serialize, Deserialize, Clone, Debug)]
#[kube(
    group = "kates.io",
    version = "v1alpha1",
    kind = "KafkaCompliance",
    namespaced,
    status = "KafkaComplianceStatus",
    doc = "A standing compliance check of one Kafka. crd-version: 1",
    cel,
    validation = Rule::new("self.metadata.name.matches('^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$')")
        .message("name must be a DNS label (at most 63 lower-case letters, digits and -): it names the stored compliance check"),
    printcolumn = r#"{"name":"Verdict","type":"string","jsonPath":".status.verdict"}"#,
    printcolumn = r#"{"name":"Checked","type":"date","jsonPath":".status.checkedAt"}"#,
    printcolumn = r#"{"name":"Age","type":"date","jsonPath":".metadata.creationTimestamp"}"#
)]
#[serde(rename_all = "camelCase")]
pub struct KafkaComplianceSpec {
    /// A `KafkaReference` `.spec`, inline; its target names the Kafka.
    #[schemars(schema_with = "reference")]
    pub reference: Value,
    /// How often to check, such as 30m or 1h.
    #[schemars(regex(pattern = r"^([0-9]+(s|m|h))+$"))]
    pub interval: String,
    /// Stops new checks without touching what the last one found.
    #[serde(default)]
    pub suspend: bool,
}

// Pruning resumes at every declared property, so target keeps the flag too: without it the
// apiserver would drop clusterId, platform or a mistyped key before the strict reader sees them.
fn reference(_: &mut SchemaGenerator) -> Schema {
    json_schema!({
        "type": "object",
        "x-kubernetes-preserve-unknown-fields": true,
        "required": ["target"],
        "properties": {
            "target": {
                "type": "object",
                "x-kubernetes-preserve-unknown-fields": true,
                "required": ["namespace", "name"],
                "properties": {
                    "namespace": {"type": "string"},
                    "name": {"type": "string"},
                },
            },
        },
    })
}

#[derive(Serialize, Deserialize, JsonSchema, Clone, Debug, Default, PartialEq, Eq)]
pub struct ComplianceCounts {
    pub pass: i64,
    pub fail: i64,
    pub unknown: i64,
}

#[derive(Serialize, Deserialize, JsonSchema, Clone, Debug, PartialEq, Eq)]
pub struct FailedRule {
    pub rule: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub detail: Option<String>,
}

#[derive(KubeSchema, Serialize, Deserialize, Clone, Debug, Default)]
#[serde(rename_all = "camelCase")]
pub struct KafkaComplianceStatus {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub observed_generation: Option<i64>,
    /// PASS, FAIL or UNKNOWN.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub verdict: Option<String>,
    /// `/api/conformity/checks/{id}`; absent when the API kept no report.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub check_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub checked_at: Option<Time>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub next_check_at: Option<Time>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counts: Option<ComplianceCounts>,
    /// At most 20; the report has the rest.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub failed: Vec<FailedRule>,
    /// The Kafka's `status.observedGeneration` at the last answered check.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub observed_kafka_generation: Option<i64>,
    /// The `kates.io/reconcile-at` value last acted on.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_handled_reconcile_at: Option<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    #[x_kube(merge_strategy = ListMerge::Map(vec!["type".into()]))]
    pub conditions: Vec<Condition>,
}
