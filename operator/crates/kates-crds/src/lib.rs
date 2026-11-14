//! The custom resources of `kates.io/v1alpha1`.

mod compat;
mod kafkacompliance;
mod kates;
mod katestest;
mod wire;

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use kube::CustomResourceExt;

pub use compat::breaking;
pub use kafkacompliance::{
    ComplianceCounts, FailedRule, KafkaCompliance, KafkaComplianceSpec, KafkaComplianceStatus,
};
pub use kates::{
    Database, Kafka, Kates, KatesSpec, KatesStatus, Mode, Sasl, SecretKey, SecretName,
};
pub use katestest::{KatesTest, KatesTestSpec, KatesTestStatus, Phase, RunSummary, TestType};

/// Every CRD this build carries, for `crdgen` and `crds apply`.
#[must_use]
pub fn crds() -> Vec<CustomResourceDefinition> {
    vec![KatesTest::crd(), KafkaCompliance::crd(), Kates::crd()]
}

/// The `crd-version: <N>` a root schema's description ends in.
#[must_use]
pub fn crd_version(description: &str) -> Option<u32> {
    description
        .rsplit("crd-version: ")
        .next()?
        .trim()
        .parse()
        .ok()
}

/// A CRD's marker, from its first version's root schema.
#[must_use]
pub fn marker(crd: &CustomResourceDefinition) -> Option<u32> {
    let schema = crd.spec.versions.first()?.schema.as_ref()?;
    crd_version(schema.open_api_v3_schema.as_ref()?.description.as_deref()?)
}

#[cfg(test)]
mod tests {
    use super::{crd_version, crds, marker};

    #[test]
    fn the_marker_ends_the_description() {
        assert_eq!(crd_version("One Kates run. crd-version: 12"), Some(12));
        assert_eq!(crd_version("One Kates run."), None);
    }

    #[test]
    fn the_run_kinds_are_deprecated_and_kates_is_not() {
        for crd in crds() {
            let version = &crd.spec.versions[0];
            let deprecated = crd.spec.names.kind != "Kates";
            assert_eq!(
                version.deprecated.unwrap_or(false),
                deprecated,
                "{}",
                crd.spec.names.kind
            );
            assert_eq!(version.deprecation_warning.is_some(), deprecated);
        }
    }

    #[test]
    fn every_crd_carries_a_marker() {
        for crd in crds() {
            assert!(marker(&crd).is_some(), "{:?}", crd.metadata.name);
        }
    }
}
