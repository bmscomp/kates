//! What a CRD change takes from resources a cluster already stores: the changes that must move
//! `crd-version`.

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use serde_json::Value;

use crate::marker;

const FLOORS: [&str; 4] = ["minimum", "minLength", "minItems", "minProperties"];
const CEILINGS: [&str; 4] = ["maximum", "maxLength", "maxItems", "maxProperties"];
// Compared whole: telling a wider pattern or union from a narrower one is not worth the code.
const OPAQUE: [&str; 6] = ["pattern", "format", "anyOf", "oneOf", "allOf", "not"];

/// Each field `new` removes or tightens against `old`, or nothing once `crd-version` has moved up.
/// A marker that went down is reported too: the upgrade hook would never apply that CRD.
#[must_use]
pub fn breaking(old: &CustomResourceDefinition, new: &CustomResourceDefinition) -> Vec<String> {
    let mut found = Vec::new();
    if marker(new) > marker(old) {
        return found;
    }
    if marker(new) < marker(old) {
        found.push("crd-version went down".to_owned());
    }
    // shortcut: the first version only, extend when a kind serves a second one
    compare(&schema(old), &schema(new), "", &mut found);
    found
}

fn schema(crd: &CustomResourceDefinition) -> Value {
    let crd = serde_json::to_value(crd).unwrap_or_default();
    crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"].clone()
}

fn compare(old: &Value, new: &Value, path: &str, found: &mut Vec<String>) {
    let mut tell = |what: String| found.push(format!("{path}: {what}"));
    if old["type"] != new["type"] {
        tell("type changed".to_owned());
        return;
    }
    if old["nullable"] == true && new["nullable"] != true {
        tell("no longer nullable".to_owned());
    }
    for name in strings(&new["required"]) {
        if !strings(&old["required"]).contains(&name) {
            tell(format!("{name} is now required"));
        }
    }
    if let Some(allowed) = new["enum"].as_array() {
        match old["enum"].as_array() {
            None => tell("enum added".to_owned()),
            Some(was) => was
                .iter()
                .filter(|value| !allowed.contains(value))
                .for_each(|value| tell(format!("enum value {value} removed"))),
        }
    }
    for key in FLOORS {
        if let Some(now) = new[key].as_f64()
            && old[key].as_f64().is_none_or(|was| now > was)
        {
            tell(format!("{key} raised"));
        }
    }
    for key in CEILINGS {
        if let Some(now) = new[key].as_f64()
            && old[key].as_f64().is_none_or(|was| now < was)
        {
            tell(format!("{key} lowered"));
        }
    }
    for key in OPAQUE {
        if !new[key].is_null() && new[key] != old[key] {
            tell(format!("{key} changed"));
        }
    }
    let rules = |schema: &Value| -> Vec<Value> {
        let rules = schema["x-kubernetes-validations"].as_array();
        rules
            .into_iter()
            .flatten()
            .map(|rule| rule["rule"].clone())
            .collect()
    };
    for rule in rules(new) {
        if !rules(old).contains(&rule) {
            tell(format!("new validation rule {rule}"));
        }
    }

    for (name, was) in old["properties"].as_object().into_iter().flatten() {
        let at = format!("{path}.{name}");
        match new["properties"].get(name) {
            Some(now) => compare(was, now, &at, found),
            None => found.push(format!("{at}: removed")),
        }
    }
    for key in ["items", "additionalProperties"] {
        let at = format!("{path}.{key}");
        match (&old[key], &new[key]) {
            (was @ Value::Object(_), now @ Value::Object(_)) => compare(was, now, &at, found),
            (Value::Null | Value::Bool(false), _) => {}
            (_, Value::Null | Value::Bool(false)) => found.push(format!("{at}: removed")),
            _ => {}
        }
    }
}

fn strings(value: &Value) -> Vec<&str> {
    let items = value.as_array().into_iter().flatten();
    items.filter_map(Value::as_str).collect()
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;

    const NONE: [&str; 0] = [];
    type Change = fn(&mut Value);

    fn crd(description: &str, spec: &Value) -> CustomResourceDefinition {
        serde_json::from_value(json!({
            "metadata": {"name": "things.kates.io"},
            "spec": {
                "group": "kates.io",
                "names": {"kind": "Thing", "plural": "things"},
                "scope": "Namespaced",
                "versions": [{"name": "v1alpha1", "served": true, "storage": true, "schema": {
                    "openAPIV3Schema": {"type": "object", "description": description,
                        "properties": {"spec": spec}},
                }}],
            },
        }))
        .unwrap()
    }

    fn base() -> Value {
        json!({"type": "object", "required": ["type"], "properties": {
            "type": {"type": "string", "enum": ["LOAD", "SPIKE"]},
            "records": {"type": "integer", "maximum": 100},
            "tags": {"type": "array", "items": {"type": "object", "properties": {
                "key": {"type": "string"}}}},
        }})
    }

    fn found(change: impl FnOnce(&mut Value)) -> Vec<String> {
        let mut spec = base();
        change(&mut spec);
        breaking(
            &crd("A thing. crd-version: 1", &base()),
            &crd("A thing. crd-version: 1", &spec),
        )
    }

    #[test]
    fn an_added_optional_field_is_not_breaking() {
        let none = found(|spec| spec["properties"]["note"] = json!({"type": "string"}));
        assert_eq!(none, NONE);
        let wider = found(|spec| spec["properties"]["records"]["maximum"] = json!(500));
        assert_eq!(wider, NONE);
    }

    #[test]
    fn a_removed_field_is_breaking_at_any_depth() {
        let top = found(|spec| {
            spec["properties"]
                .as_object_mut()
                .unwrap()
                .remove("records");
        });
        assert_eq!(top, [".spec.records: removed"]);
        let nested = found(|spec| spec["properties"]["tags"]["items"]["properties"] = json!({}));
        assert_eq!(nested, [".spec.tags.items.key: removed"]);
    }

    #[test]
    fn a_tightened_field_is_breaking() {
        let cases: [(Change, &str); 6] = [
            (
                |spec| spec["required"] = json!(["type", "records"]),
                "records is now required",
            ),
            (
                |spec| spec["properties"]["type"]["enum"] = json!(["LOAD"]),
                "\"SPIKE\" removed",
            ),
            (
                |spec| spec["properties"]["records"]["maximum"] = json!(10),
                "maximum lowered",
            ),
            (
                |spec| spec["properties"]["records"]["minimum"] = json!(1),
                "minimum raised",
            ),
            (
                |spec| spec["properties"]["records"]["type"] = json!("string"),
                "type changed",
            ),
            (
                |spec| spec["x-kubernetes-validations"] = json!([{"rule": "self.records > 0"}]),
                "new validation rule",
            ),
        ];
        for (change, expected) in cases {
            let found = found(change);
            assert_eq!(found.len(), 1, "{found:?}");
            assert!(found[0].contains(expected), "{found:?}");
        }
    }

    #[test]
    fn a_moved_marker_allows_the_change_and_a_lowered_one_never_passes() {
        let (one, two) = ("A thing. crd-version: 1", "A thing. crd-version: 2");
        let emptied = json!({"type": "object"});
        assert_eq!(breaking(&crd(one, &base()), &crd(two, &emptied)), NONE);
        let lowered = breaking(&crd(two, &base()), &crd(one, &base()));
        assert_eq!(lowered, ["crd-version went down"]);
    }
}
