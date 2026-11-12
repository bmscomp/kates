//! `kates-operator crds apply`, the Helm hook's writer, and the check that the served CRDs are at
//! least this build's.

use std::collections::BTreeMap;
use std::time::Duration;

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use kates_crds::{crd_version, crds, marker};
use kube::api::{Api, Patch, PatchParams};
use kube::runtime::wait::{await_condition, conditions};
use kube::{Client, Resource as _};
use serde_json::Value;

const FIELD_MANAGER: &str = "kates-operator-crd-upgrade";
const ESTABLISH_TIMEOUT: Duration = Duration::from_mins(1);
const OPENAPI: &str = "/openapi/v3/apis/kates.io/v1alpha1";

/// Whether to apply over a live CRD: never over a strictly newer one, so with several releases on
/// a cluster the newest wins. Equal applies, which gives the hook co-ownership of every field.
#[must_use]
pub fn should_apply(live: Option<u32>, own: Option<u32>) -> bool {
    match (live, own) {
        (Some(live), Some(own)) => live <= own,
        _ => true,
    }
}

/// Applies every CRD this build carries and waits for each to be Established.
///
/// # Errors
/// A refused apply, or a CRD not Established in time.
pub async fn apply(client: Client) -> Result<(), Box<dyn std::error::Error>> {
    let api: Api<CustomResourceDefinition> = Api::all(client);
    for crd in crds() {
        let name = crd.meta().name.clone().ok_or("a CRD without a name")?;
        let live = api.get_opt(&name).await?;
        if !should_apply(live.as_ref().and_then(marker), marker(&crd)) {
            println!("{name}: a newer version is installed, left alone");
            continue;
        }
        let params = PatchParams::apply(FIELD_MANAGER).force();
        api.patch(&name, &params, &Patch::Apply(&crd)).await?;
        let established = await_condition(api.clone(), &name, conditions::is_crd_established());
        tokio::time::timeout(ESTABLISH_TIMEOUT, established).await??;
        println!("{name}: applied");
    }
    Ok(())
}

/// `crd-version` of each kind the API server serves, from its `OpenAPI` document.
///
/// # Errors
/// A read that failed: the group not being served reads as an empty map.
pub async fn served(client: &Client) -> Result<BTreeMap<String, u32>, kube::Error> {
    let request = http::Request::get(OPENAPI)
        .body(Vec::new())
        .map_err(|error| {
            kube::Error::BuildRequest(kube::core::request::Error::BuildRequest(error))
        })?;
    match client.request_text(request).await {
        Ok(text) => Ok(parse_served(&text)),
        Err(kube::Error::Api(status)) if status.code == 404 => Ok(BTreeMap::new()),
        Err(error) => Err(error),
    }
}

fn parse_served(text: &str) -> BTreeMap<String, u32> {
    let document: Value = serde_json::from_str(text).unwrap_or_default();
    let schemas = document["components"]["schemas"].as_object();
    schemas
        .into_iter()
        .flatten()
        .filter_map(|(key, schema)| {
            let kind = key.strip_prefix("io.kates.v1alpha1.")?;
            let version = crd_version(schema["description"].as_str()?)?;
            Some((kind.to_owned(), version))
        })
        .collect()
}

/// What `/readyz` says when a served CRD is behind this build, or `None` when none is.
#[must_use]
pub fn stale(served: &BTreeMap<String, u32>) -> Option<String> {
    crds().iter().find_map(|crd| {
        let kind = &crd.spec.names.kind;
        let own = marker(crd)?;
        let behind = served.get(kind).is_none_or(|version| *version < own);
        behind.then(|| {
            format!("{kind} is not served at crd-version {own}: run kates-operator crds apply, or helm upgrade")
        })
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_newer_crd_is_never_downgraded() {
        assert!(should_apply(None, Some(2)));
        assert!(should_apply(Some(1), Some(2)));
        assert!(should_apply(Some(2), Some(2)));
        assert!(!should_apply(Some(3), Some(2)));
    }

    #[test]
    fn staleness_reads_the_served_markers() {
        let text = r#"{"components":{"schemas":{
            "io.kates.v1alpha1.KatesTest":{"description":"One Kates run. crd-version: 1"},
            "io.kates.v1alpha1.KafkaCompliance":{"description":"A check. crd-version: 1"},
            "io.kates.v1alpha1.Kates":{"description":"One Kates backend. crd-version: 1"},
            "io.k8s.other":{"description":"x"}}}}"#;
        let served = parse_served(text);
        assert_eq!(served.len(), 3);
        assert_eq!(stale(&served), None);
    }

    #[test]
    fn a_kind_not_served_is_stale_and_says_what_to_run() {
        let message = stale(&BTreeMap::new()).unwrap();
        assert!(message.contains("crds apply"));
    }
}
