use kates_operator::{crds, run, telemetry};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // rustls panics at the first handshake unless the provider is named.
    rustls::crypto::aws_lc_rs::default_provider()
        .install_default()
        .expect("crypto provider");
    telemetry::init();
    let args: Vec<String> = std::env::args().skip(1).collect();
    match args.iter().map(String::as_str).collect::<Vec<_>>()[..] {
        [] | ["run"] => run::run().await,
        ["crds", "apply"] => crds::apply(kube::Client::try_default().await?).await,
        _ => Err("usage: kates-operator [run | crds apply]".into()),
    }
}
