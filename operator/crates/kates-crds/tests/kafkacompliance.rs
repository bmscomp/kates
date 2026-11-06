use kates_crds::{KafkaCompliance, KafkaComplianceSpec};
use kube::CustomResourceExt;
use serde_json::{Value, json};

fn spec() -> KafkaComplianceSpec {
    serde_json::from_value(json!({
        "reference": {"target": {"namespace": "kafka", "name": "krafter", "clusterId": "x"}},
        "interval": "1h",
    }))
    .unwrap()
}

fn schema() -> Value {
    let crd = serde_json::to_value(KafkaCompliance::crd()).unwrap();
    crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"].clone()
}

#[test]
fn a_dns_label_name_passes_and_others_are_refused() {
    let good = KafkaCompliance::new("krafter-rules", spec());
    assert!(good.validate_cel().is_ok());
    for name in ["a.b", "UPPER", &"x".repeat(64)] {
        let bad = KafkaCompliance::new(name, spec());
        assert!(bad.validate_cel().is_err(), "{name}");
    }
}

#[test]
fn unknown_keys_reach_the_strict_reader_at_both_levels() {
    let reference = &schema()["properties"]["spec"]["properties"]["reference"];
    assert_eq!(reference["x-kubernetes-preserve-unknown-fields"], true);
    assert_eq!(reference["required"], json!(["target"]));
    let target = &reference["properties"]["target"];
    assert_eq!(target["x-kubernetes-preserve-unknown-fields"], true);
    assert_eq!(target["required"], json!(["name", "namespace"]));
}

#[test]
fn the_interval_is_checked_by_the_schema() {
    let interval = &schema()["properties"]["spec"]["properties"]["interval"];
    assert!(interval["pattern"].as_str().unwrap().starts_with('^'));
}
