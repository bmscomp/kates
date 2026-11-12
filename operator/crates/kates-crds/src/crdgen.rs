use std::path::PathBuf;
use std::{env, fs};

fn main() {
    let mut args = env::args().skip(1);
    let (Some("--out"), Some(dir), None) = (args.next().as_deref(), args.next(), args.next())
    else {
        eprintln!("usage: crdgen --out <dir>");
        std::process::exit(2);
    };
    let dir = PathBuf::from(dir);

    fs::create_dir_all(&dir).expect("create the output directory");
    for crd in kates_crds::crds() {
        let name = crd.metadata.name.as_deref().expect("a CRD has a name");
        let path = dir.join(format!("{name}.yaml"));
        // Without this, regenerating would overwrite the snapshot the test compares against.
        if let Ok(committed) = fs::read_to_string(&path) {
            let committed = serde_saphyr::from_str(&committed).expect("the committed CRD parses");
            let found = kates_crds::breaking(&committed, &crd);
            if !found.is_empty() {
                eprintln!("{name} breaks stored resources, so raise its crd-version: {found:#?}");
                std::process::exit(1);
            }
        }
        let yaml = serde_saphyr::to_string(&crd).expect("a CRD serialises");
        fs::write(path, yaml).expect("write the CRD");
    }
}
