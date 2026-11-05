use opentelemetry::trace::{TraceContextExt as _, TraceId, TracerProvider as _};
use opentelemetry_sdk::trace::{SdkTracer, SdkTracerProvider};
use tracing_opentelemetry::OpenTelemetrySpanExt as _;
use tracing_subscriber::layer::{Layer as _, SubscriberExt as _};
use tracing_subscriber::util::SubscriberInitExt as _;
use tracing_subscriber::{EnvFilter, Registry, fmt};

/// The trace id of the current span: invalid outside one, and a metric then has no exemplar.
#[must_use]
pub fn current_trace_id() -> TraceId {
    tracing::Span::current()
        .context()
        .span()
        .span_context()
        .trace_id()
}

/// Logs to stdout, JSON under `LOG_FORMAT=json`, at `RUST_LOG` (default `info`).
pub fn init() {
    let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info"));
    let logs = if std::env::var("LOG_FORMAT").is_ok_and(|format| format == "json") {
        fmt::layer().json().boxed()
    } else {
        fmt::layer().boxed()
    };
    Registry::default()
        .with(filter)
        .with(logs)
        .with(tracing_opentelemetry::layer().with_tracer(tracer()))
        .init();
}

// Spans get trace ids with export off too, so an exemplar needs no collector.
fn tracer() -> SdkTracer {
    let builder = SdkTracerProvider::builder();
    #[cfg(feature = "telemetry")]
    let builder = with_export(builder);
    let provider = builder.build();
    opentelemetry::global::set_tracer_provider(provider.clone());
    provider.tracer("kates-operator")
}

// The endpoint comes from OTEL_EXPORTER_OTLP_ENDPOINT; unset, there is no collector to send to.
#[cfg(feature = "telemetry")]
fn with_export(
    builder: opentelemetry_sdk::trace::TracerProviderBuilder,
) -> opentelemetry_sdk::trace::TracerProviderBuilder {
    use opentelemetry_otlp::SpanExporter;

    if std::env::var_os("OTEL_EXPORTER_OTLP_ENDPOINT").is_none() {
        return builder;
    }
    let exporter = SpanExporter::builder()
        .with_tonic()
        .build()
        .expect("an OTLP exporter");
    builder.with_batch_exporter(exporter)
}

#[cfg(test)]
mod tests {
    use opentelemetry::trace::TraceId;
    use tracing_subscriber::layer::SubscriberExt as _;
    use tracing_subscriber::{Registry, fmt};

    use super::{current_trace_id, tracer};

    #[test]
    fn a_span_has_a_trace_id_with_export_off() {
        let subscriber = Registry::default()
            .with(fmt::layer())
            .with(tracing_opentelemetry::layer().with_tracer(tracer()));

        tracing::subscriber::with_default(subscriber, || {
            assert_eq!(current_trace_id(), TraceId::INVALID);
            let span = tracing::info_span!("reconcile");
            let _entered = span.enter();
            assert_ne!(current_trace_id(), TraceId::INVALID);
        });
    }
}
