use std::collections::BTreeSet;

use kates_crds::{KatesTest, KatesTestSpec, KatesTestStatus, Phase, TestType};
use kube::CustomResourceExt;
use serde_json::{Value, json};

const NONE: [&str; 0] = [];

fn spec(body: Value) -> KatesTestSpec {
    serde_json::from_value(body).expect("a KatesTest spec")
}

fn resource(body: Value) -> KatesTest {
    KatesTest::new("nightly-load", spec(body))
}

fn flat() -> Value {
    json!({"type": "LOAD", "spec": {"numRecords": 1}})
}

fn started(body: Value) -> KatesTest {
    let mut test = resource(body);
    test.status = Some(KatesTestStatus {
        run_id: Some("8f2a1c0e".into()),
        ..KatesTestStatus::default()
    });
    test
}

fn schema() -> Value {
    let crd = serde_json::to_value(KatesTest::crd()).unwrap();
    crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"].clone()
}

fn spec_schema() -> Value {
    schema()["properties"]["spec"].clone()
}

// The apiserver prunes these and kubectl's strict field validation refuses them.
fn unknown_fields(schema: &Value, value: &Value, path: &str, found: &mut Vec<String>) {
    match value {
        Value::Object(fields) => {
            for (key, child) in fields {
                let here = format!("{path}.{key}");
                let declared = schema["properties"]
                    .get(key)
                    .or_else(|| schema.get("additionalProperties"));
                match declared {
                    Some(declared) => unknown_fields(declared, child, &here, found),
                    None => found.push(here),
                }
            }
        }
        Value::Array(items) => {
            for (i, child) in items.iter().enumerate() {
                unknown_fields(&schema["items"], child, &format!("{path}[{i}]"), found);
            }
        }
        _ => {}
    }
}

fn refused(body: &Value) -> Vec<String> {
    let mut found = Vec::new();
    unknown_fields(&spec_schema(), body, "spec", &mut found);
    found
}

fn walk<'a>(node: &'a Value, seen: &mut Vec<&'a Value>) {
    seen.push(node);
    match node {
        Value::Object(fields) => fields.values().for_each(|child| walk(child, seen)),
        Value::Array(items) => items.iter().for_each(|child| walk(child, seen)),
        _ => {}
    }
}

fn property_names(node: &Value) -> BTreeSet<String> {
    node["properties"]
        .as_object()
        .expect("properties")
        .keys()
        .cloned()
        .collect()
}

fn scenario_with_sla() -> Value {
    json!({
        "type": "LOAD",
        "scenario": {"name": "ramp", "sla": {"maxP99LatencyMs": 10}},
    })
}

#[test]
fn exactly_one_of_spec_and_scenario() {
    assert!(resource(flat()).validate_cel().is_ok());
    assert!(resource(scenario_with_sla()).validate_cel().is_ok());

    let both = json!({"type": "LOAD", "spec": {}, "scenario": {"name": "ramp"}});
    assert!(resource(both).validate_cel().is_err());
    assert!(resource(json!({"type": "LOAD"})).validate_cel().is_err());
}

#[test]
fn sla_goes_with_spec_only() {
    let with_spec = json!({"type": "LOAD", "spec": {}, "sla": {"maxP99LatencyMs": 10}});
    assert!(resource(with_spec).validate_cel().is_ok());

    let beside_scenario = json!({
        "type": "LOAD",
        "scenario": {"name": "ramp"},
        "sla": {"maxP99LatencyMs": 10},
    });
    assert!(resource(beside_scenario).validate_cel().is_err());
}

#[test]
fn spec_is_frozen_once_a_run_is_recorded() {
    let old = started(flat());

    let same = started(flat());
    assert!(same.validate_cel_update(&old).is_ok());

    let records = started(json!({"type": "LOAD", "spec": {"numRecords": 2}}));
    assert!(records.validate_cel_update(&old).is_err());

    let sla =
        started(json!({"type": "LOAD", "spec": {"numRecords": 1}, "sla": {"maxErrorRate": 0.1}}));
    assert!(sla.validate_cel_update(&old).is_err());

    let ttl =
        started(json!({"type": "LOAD", "spec": {"numRecords": 1}, "ttlSecondsAfterFinished": 60}));
    assert!(ttl.validate_cel_update(&old).is_err());
}

#[test]
fn spec_can_change_before_a_run_is_recorded() {
    let no_status = resource(flat());
    let changed = resource(json!({"type": "LOAD", "spec": {"numRecords": 2}}));
    assert!(changed.validate_cel_update(&no_status).is_ok());

    let mut empty_status = resource(flat());
    empty_status.status = Some(KatesTestStatus::default());
    assert!(changed.validate_cel_update(&empty_status).is_ok());
}

#[test]
fn a_mistyped_test_spec_key_is_refused() {
    assert_eq!(
        refused(&json!({"spec": {"records": 1}})),
        ["spec.spec.records"]
    );
    assert_eq!(refused(&json!({"spec": {"numRecords": 1}})), NONE);
}

#[test]
fn every_scenario_spec_is_the_wire_test_spec() {
    let spec = spec_schema();
    let scenario = &spec["properties"]["scenario"]["properties"];
    let phase = &scenario["phases"]["items"]["properties"];
    let wire = &spec["properties"]["spec"]["properties"];
    assert_eq!(&scenario["baseSpec"]["properties"], wire);
    assert_eq!(&phase["spec"]["properties"], wire);
}

#[test]
fn no_property_carries_a_default() {
    let root = schema();
    let mut nodes = Vec::new();
    walk(&root, &mut nodes);
    for node in nodes {
        assert!(node.get("default").is_none(), "default survived: {node}");
    }
}

#[test]
fn the_wire_schema_keeps_names_and_types_only() {
    let spec = spec_schema();
    let mut nodes = Vec::new();
    for field in ["spec", "scenario", "sla"] {
        walk(&spec["properties"][field], &mut nodes);
    }
    for node in nodes {
        let Value::Object(fields) = node else {
            continue;
        };
        if fields.get("type").is_none_or(Value::is_object) {
            continue;
        }
        for key in [
            "minimum", "maximum", "pattern", "enum", "anyOf", "oneOf", "required",
        ] {
            assert!(!fields.contains_key(key), "{key} survived: {node}");
        }
        assert_ne!(fields.get("additionalProperties"), Some(&json!(false)));
        assert!(!fields.contains_key("x-kubernetes-preserve-unknown-fields"));
    }
}

#[test]
fn a_negative_ttl_is_refused() {
    let ttl = &spec_schema()["properties"]["ttlSecondsAfterFinished"];
    assert_eq!(ttl["minimum"], json!(0.0));
}

#[test]
fn acks_takes_an_integer_or_a_string() {
    let acks = &spec_schema()["properties"]["spec"]["properties"]["acks"];
    assert_eq!(acks["x-kubernetes-int-or-string"], json!(true));
    assert!(acks.get("type").is_none());
}

#[test]
fn sla_has_exactly_the_keys_the_api_binds() {
    let spec = spec_schema();
    let expected: BTreeSet<String> = [
        "maxP99LatencyMs",
        "maxP999LatencyMs",
        "maxAvgLatencyMs",
        "minThroughputRecPerSec",
        "maxErrorRate",
        "minRecordsProcessed",
        "maxDataLossPercent",
        "maxRtoMs",
        "maxRpoMs",
    ]
    .map(String::from)
    .into();
    assert_eq!(property_names(&spec["properties"]["sla"]), expected);
    assert_eq!(
        spec["properties"]["scenario"]["properties"]["sla"]["properties"],
        spec["properties"]["sla"]["properties"]
    );

    assert_eq!(
        refused(&json!({"sla": {"p99LatencyMs": 1}})),
        ["spec.sla.p99LatencyMs"]
    );
    assert_eq!(refused(&json!({"sla": {"maxP99LatencyMs": 1}})), NONE);
}

#[test]
fn phases_take_only_what_the_api_binds() {
    let phase = |fields: Value| json!({"scenario": {"phases": [fields]}});

    assert_eq!(
        refused(&phase(json!({"specOverrides": {}}))),
        ["spec.scenario.phases[0].specOverrides"]
    );
    assert_eq!(
        refused(&phase(json!({"type": "RAMP"}))),
        ["spec.scenario.phases[0].type"]
    );
    assert_eq!(refused(&phase(json!({"phaseType": "RAMP"}))), NONE);
}

#[test]
fn scenario_labels_stay_a_string_map() {
    let spec = spec_schema();
    let labels = &spec["properties"]["scenario"]["properties"]["labels"];
    assert_eq!(labels["additionalProperties"]["type"], json!("string"));
    assert_eq!(
        refused(&json!({"scenario": {"labels": {"team": "payments"}}})),
        NONE
    );
}

#[test]
fn test_type_is_the_wire_enum() {
    let wire: Value = serde_json::from_str(include_str!(
        "../../../../schemas/test-request.v1.schema.json"
    ))
    .unwrap();
    let crd = spec_schema()["properties"]["type"]["enum"].clone();
    assert_eq!(crd, wire["$defs"]["testType"]["enum"]);
    assert_eq!(
        serde_json::to_value(TestType::IntegrationCdc).unwrap(),
        json!("INTEGRATION_CDC")
    );
}

#[test]
fn schema_ends_in_the_crd_version_marker() {
    let description = schema()["description"].as_str().unwrap().to_owned();
    let marker = description.rsplit("crd-version: ").next().unwrap();
    assert!(
        marker.parse::<u32>().is_ok(),
        "no marker in {description:?}"
    );
}

#[test]
fn printer_columns_are_the_dashboard() {
    let crd = serde_json::to_value(KatesTest::crd()).unwrap();
    let columns = crd["spec"]["versions"][0]["additionalPrinterColumns"]
        .as_array()
        .unwrap();
    let paths: Vec<_> = columns
        .iter()
        .map(|c| c["jsonPath"].as_str().unwrap())
        .collect();
    assert_eq!(
        paths,
        [
            ".status.phase",
            ".status.runId",
            ".metadata.creationTimestamp"
        ]
    );
}

#[test]
fn an_unset_status_field_is_omitted_never_null() {
    assert_eq!(
        serde_json::to_value(KatesTestStatus::default()).unwrap(),
        json!({})
    );
}

#[test]
fn status_uses_report_summary_field_names() {
    let status: KatesTestStatus = serde_json::from_value(json!({
        "phase": "Succeeded",
        "summary": {"avgThroughputMBPerSec": 1.5, "p999LatencyMs": 9.0, "totalRecords": 10},
    }))
    .unwrap();
    assert_eq!(status.phase, Some(Phase::Succeeded));
    let summary = serde_json::to_value(&status).unwrap()["summary"].clone();
    assert_eq!(
        summary,
        json!({"avgThroughputMBPerSec": 1.5, "p999LatencyMs": 9.0, "totalRecords": 10})
    );
}

#[test]
fn conditions_merge_by_type_under_server_side_apply() {
    let conditions = &schema()["properties"]["status"]["properties"]["conditions"];
    assert_eq!(conditions["x-kubernetes-list-type"], json!("map"));
    assert_eq!(conditions["x-kubernetes-list-map-keys"], json!(["type"]));
}
