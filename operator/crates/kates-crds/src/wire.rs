//! The D2 conversion: names and types from the shipped request schema, so a mistyped key is
//! refused by the CRD, while values stay the API's job.

use schemars::{Schema, SchemaGenerator};
use serde_json::{Map, Value};

const TEST_REQUEST: &str = include_str!("../../../../schemas/test-request.v1.schema.json");

/// Keys the schema accepts for `kates` files and the API never binds: left in, they would pass
/// the CRD and be dropped silently, which on `sla` means `Ready=True` on an empty SLA.
const FILE_ONLY: &[(&str, &[&str])] = &[
    (
        "slaDefinition",
        &[
            "p95LatencyMs",
            "p99LatencyMs",
            "avgLatencyMs",
            "maxErrorRatePercent",
            "maxRecoveryTimeMs",
            "maxThroughputDegradationPercent",
            "maxIsrRecoveryTimeMs",
            "maxConsumerLag",
        ],
    ),
    ("scenarioPhase", &["type", "specOverrides"]),
];

pub fn test_spec(_: &mut SchemaGenerator) -> Schema {
    definition("wireTestSpec")
}

pub fn scenario(_: &mut SchemaGenerator) -> Schema {
    definition("multiPhaseScenario")
}

pub fn sla(_: &mut SchemaGenerator) -> Schema {
    definition("slaDefinition")
}

fn definition(name: &str) -> Schema {
    let root: Value = serde_json::from_str(TEST_REQUEST).expect("the shipped schema is JSON");
    let converted = convert_definition(name, &root["$defs"]);
    Schema::try_from(converted).expect("a converted definition is an object")
}

fn convert_definition(name: &str, defs: &Value) -> Value {
    let file_only = FILE_ONLY
        .iter()
        .find_map(|(def, keys)| (*def == name).then_some(*keys))
        .unwrap_or_default();
    convert(&defs[name], defs, file_only)
}

fn convert(node: &Value, defs: &Value, file_only: &[&str]) -> Value {
    if let Some(reference) = node["$ref"].as_str() {
        let name = reference
            .strip_prefix("#/$defs/")
            .expect("only local refs in the schema");
        let mut resolved = convert_definition(name, defs);
        if !node["description"].is_null() {
            resolved["description"] = node["description"].clone();
        }
        return resolved;
    }

    let mut out = Map::new();
    if !node["description"].is_null() {
        out.insert("description".into(), node["description"].clone());
    }
    // A oneOf is acks, the schema's only property taking two types.
    if !node["oneOf"].is_null() {
        out.insert("x-kubernetes-int-or-string".into(), true.into());
        return out.into();
    }

    let kind = node["type"]
        .as_str()
        .unwrap_or_else(|| panic!("no type in {node}"));
    out.insert("type".into(), kind.into());
    match kind {
        "object" => {
            if let Some(properties) = node["properties"].as_object() {
                let kept = properties
                    .iter()
                    .filter(|(key, _)| !file_only.contains(&key.as_str()))
                    .map(|(key, value)| (key.clone(), convert(value, defs, &[])));
                out.insert("properties".into(), Value::Object(kept.collect()));
            }
            if node["additionalProperties"].is_object() {
                let values = convert(&node["additionalProperties"], defs, &[]);
                out.insert("additionalProperties".into(), values);
            }
        }
        "array" => {
            out.insert("items".into(), convert(&node["items"], defs, &[]));
        }
        _ => {}
    }
    out.into()
}
