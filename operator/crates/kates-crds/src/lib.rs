//! The custom resources of `kates.io/v1alpha1`.

mod katestest;
mod wire;

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use kube::CustomResourceExt;

pub use katestest::{KatesTest, KatesTestSpec, KatesTestStatus, Phase, RunSummary, TestType};

/// Every CRD this build carries, for `crdgen`.
#[must_use]
pub fn crds() -> Vec<CustomResourceDefinition> {
    vec![KatesTest::crd()]
}
