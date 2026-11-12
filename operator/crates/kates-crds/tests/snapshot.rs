//! The committed `chart/crds` are the snapshot: what the last change shipped.

use std::fs;
use std::path::Path;

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;

#[test]
fn a_removed_or_tightened_field_moves_crd_version() {
    let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../chart/crds");
    for crd in kates_crds::crds() {
        let name = crd.metadata.name.as_deref().unwrap();
        // A kind not generated yet has no snapshot to break.
        let Ok(yaml) = fs::read_to_string(dir.join(format!("{name}.yaml"))) else {
            continue;
        };
        let committed: CustomResourceDefinition = serde_saphyr::from_str(&yaml).unwrap();
        let found = kates_crds::breaking(&committed, &crd);
        assert!(
            found.is_empty(),
            "{name} breaks stored resources, so raise its crd-version: {found:#?}"
        );
    }
}
