use k8s_openapi::apimachinery::pkg::apis::meta::v1::{Condition, Time};
use kube::{CustomResource, KubeSchema};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::wire;

/// One Kates run, as `kubectl apply` takes it.
#[derive(CustomResource, KubeSchema, Serialize, Deserialize, Clone, Debug)]
#[kube(
    group = "kates.io",
    version = "v1alpha1",
    kind = "KatesTest",
    namespaced,
    status = "KatesTestStatus",
    doc = "One Kates run. crd-version: 1",
    cel,
    validation = Rule::new("has(self.spec.spec) != has(self.spec.scenario)")
        .message("exactly one of spec.spec and spec.scenario is required"),
    validation = Rule::new("!has(self.spec.sla) || has(self.spec.spec)")
        .message("spec.sla goes with spec.spec: a scenario carries its own sla"),
    validation = Rule::new("!has(oldSelf.status) || !has(oldSelf.status.runId) || oldSelf.spec == self.spec")
        .message("spec is immutable once the run has started: create a new KatesTest"),
    printcolumn = r#"{"name":"Phase","type":"string","jsonPath":".status.phase"}"#,
    printcolumn = r#"{"name":"Run","type":"string","jsonPath":".status.runId"}"#,
    printcolumn = r#"{"name":"Age","type":"date","jsonPath":".metadata.creationTimestamp"}"#
)]
#[serde(rename_all = "camelCase")]
pub struct KatesTestSpec {
    #[serde(rename = "type")]
    pub test_type: TestType,
    /// Execution backend: native (the API's default) or trogdor.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub backend: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schemars(schema_with = "wire::test_spec")]
    pub spec: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schemars(schema_with = "wire::scenario")]
    pub scenario: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schemars(schema_with = "wire::sla")]
    pub sla: Option<Value>,
    /// Seconds after the end of the run to delete this resource. Never set it on a resource Flux
    /// or Argo CD holds: the tool re-creates it, and the run repeats every TTL.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    // The API never sees this field, so the CRD is the only check on it.
    #[schemars(range(min = 0))]
    pub ttl_seconds_after_finished: Option<i32>,
}

/// `TestType`, as `POST /api/tests` takes it.
#[derive(Serialize, Deserialize, JsonSchema, Clone, Copy, Debug, PartialEq, Eq)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum TestType {
    Load,
    Stress,
    Spike,
    Endurance,
    Volume,
    Capacity,
    RoundTrip,
    Integrity,
    TuneReplication,
    TuneAcks,
    TuneBatching,
    TuneCompression,
    TunePartitions,
    IntegrationCdc,
}

#[derive(Serialize, Deserialize, JsonSchema, Clone, Copy, Debug, PartialEq, Eq)]
pub enum Phase {
    Pending,
    Running,
    Succeeded,
    Failed,
}

// Every field stays omitted until set: `kubectl wait`'s value-less jsonpath form matches any
// present value, null included, and the operator's server-side apply must not claim empty fields.
#[derive(KubeSchema, Serialize, Deserialize, Clone, Debug, Default)]
#[serde(rename_all = "camelCase")]
pub struct KatesTestStatus {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub observed_generation: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub run_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub phase: Option<Phase>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub started_at: Option<Time>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub finished_at: Option<Time>,
    /// The `kates.io/reconcile-at` value last acted on.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_handled_reconcile_at: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub summary: Option<RunSummary>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    #[x_kube(merge_strategy = ListMerge::Map(vec!["type".into()]))]
    pub conditions: Vec<Condition>,
}

/// `ReportSummary`'s fields verbatim, from `GET /api/tests/{id}/report`.
#[derive(Serialize, Deserialize, JsonSchema, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RunSummary {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub total_records: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub avg_throughput_rec_per_sec: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub peak_throughput_rec_per_sec: Option<f64>,
    #[serde(
        default,
        skip_serializing_if = "Option::is_none",
        rename = "avgThroughputMBPerSec"
    )]
    pub avg_throughput_mb_per_sec: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub avg_latency_ms: Option<f64>,
    #[serde(
        default,
        skip_serializing_if = "Option::is_none",
        rename = "p50LatencyMs"
    )]
    pub p50_latency_ms: Option<f64>,
    #[serde(
        default,
        skip_serializing_if = "Option::is_none",
        rename = "p95LatencyMs"
    )]
    pub p95_latency_ms: Option<f64>,
    #[serde(
        default,
        skip_serializing_if = "Option::is_none",
        rename = "p99LatencyMs"
    )]
    pub p99_latency_ms: Option<f64>,
    #[serde(
        default,
        skip_serializing_if = "Option::is_none",
        rename = "p999LatencyMs"
    )]
    pub p999_latency_ms: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_latency_ms: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub total_errors: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error_rate: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub duration_ms: Option<i64>,
}
