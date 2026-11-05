use std::sync::{Arc, RwLock};
use std::time::{Instant, SystemTime};

use kates_crds::{KafkaCompliance, KatesTest, Phase};
use kube::ResourceExt as _;
use kube::runtime::reflector::Store;

use opentelemetry::trace::TraceId;
use prometheus_client::collector::Collector;
use prometheus_client::encoding::{DescriptorEncoder, EncodeLabelSet, EncodeMetric as _};
use prometheus_client::metrics::counter::Counter;
use prometheus_client::metrics::exemplar::HistogramWithExemplars;
use prometheus_client::metrics::family::Family;
use prometheus_client::metrics::gauge::{ConstGauge, Gauge};
use prometheus_client::registry::{Registry, Unit};

const BUCKETS: [f64; 9] = [0.01, 0.05, 0.1, 0.5, 1.0, 5.0, 15.0, 30.0, 60.0];

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct KindLabel {
    kind: &'static str,
}

// error is a variant or reason name, never Debug or Display text: the label stays bounded.
#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct ErrorLabels {
    kind: &'static str,
    error: &'static str,
}

// route is the client's route template, never the URL.
#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct ApiRequestLabels {
    method: &'static str,
    route: &'static str,
    status: u16,
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct TraceLabel {
    trace_id: String,
}

type Duration = HistogramWithExemplars<TraceLabel>;

fn duration_histogram() -> Duration {
    Duration::new(BUCKETS.into_iter())
}

#[derive(Clone)]
pub struct Metrics {
    reconciliations: Family<KindLabel, Counter>,
    errors: Family<ErrorLabels, Counter>,
    duration: Family<KindLabel, Duration, fn() -> Duration>,
    api_requests: Family<ApiRequestLabels, Counter>,
    leader: Gauge,
}

impl Metrics {
    /// Registers every metric on `registry` under the `kates_operator` prefix.
    pub fn register(registry: &mut Registry) -> Self {
        let metrics = Self {
            reconciliations: Family::default(),
            errors: Family::default(),
            duration: Family::new_with_constructor(duration_histogram),
            api_requests: Family::default(),
            leader: Gauge::default(),
        };
        let registry = registry.sub_registry_with_prefix("kates_operator");
        registry.register(
            "reconciliations",
            "Reconcile passes",
            metrics.reconciliations.clone(),
        );
        registry.register(
            "reconciliation_errors",
            "Reconcile passes that failed",
            metrics.errors.clone(),
        );
        registry.register_with_unit(
            "reconcile_duration",
            "Time a reconcile pass took",
            Unit::Seconds,
            metrics.duration.clone(),
        );
        registry.register(
            "api_requests",
            "Calls to the Kates API",
            metrics.api_requests.clone(),
        );
        registry.register(
            "leader",
            "1 while this pod holds the Lease",
            metrics.leader.clone(),
        );
        metrics
    }

    pub fn api_request(&self, method: &'static str, route: &'static str, status: u16) {
        self.api_requests
            .get_or_create(&ApiRequestLabels {
                method,
                route,
                status,
            })
            .inc();
    }

    pub fn set_leader(&self, leader: bool) {
        self.leader.set(i64::from(leader));
    }

    /// Counts a pass; the returned guard records its duration when dropped.
    #[must_use]
    pub fn reconcile_started(&self, kind: &'static str, trace_id: TraceId) -> ReconcileTimer {
        self.reconciliations
            .get_or_create(&KindLabel { kind })
            .inc();
        ReconcileTimer {
            start: Instant::now(),
            kind,
            trace_id,
            duration: self.duration.clone(),
        }
    }

    pub fn reconcile_failed(&self, kind: &'static str, error: &'static str) {
        self.errors
            .get_or_create(&ErrorLabels { kind, error })
            .inc();
    }
}

pub struct ReconcileTimer {
    start: Instant,
    kind: &'static str,
    trace_id: TraceId,
    duration: Family<KindLabel, Duration, fn() -> Duration>,
}

impl Drop for ReconcileTimer {
    fn drop(&mut self) {
        let exemplar = (self.trace_id != TraceId::INVALID).then(|| TraceLabel {
            trace_id: self.trace_id.to_string(),
        });
        self.duration
            .get_or_create(&KindLabel { kind: self.kind })
            .observe(
                self.start.elapsed().as_secs_f64(),
                exemplar,
                Some(SystemTime::now()),
            );
    }
}

/// The stores behind the gauges, filled once the controllers exist.
#[derive(Clone, Debug, Default)]
pub struct Stores {
    pub tests: Arc<RwLock<Vec<Store<KatesTest>>>>,
    pub checks: Arc<RwLock<Vec<Store<KafkaCompliance>>>>,
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct TestLabels {
    namespace: String,
    phase: &'static str,
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct RuleLabels {
    namespace: String,
    name: String,
    verdict: &'static str,
}

/// Reads the stores at scrape time: a deleted resource's series goes at the next scrape, and a
/// restart cannot drift the counts.
#[derive(Debug)]
pub struct StoreCollector(pub Stores);

impl Collector for StoreCollector {
    fn encode(&self, mut encoder: DescriptorEncoder) -> Result<(), std::fmt::Error> {
        let mut tests = std::collections::BTreeMap::new();
        for store in self.0.tests.read().expect("the stores").iter() {
            for test in store.state() {
                let phase = match test.status.as_ref().and_then(|s| s.phase) {
                    Some(Phase::Running) => "Running",
                    Some(Phase::Succeeded) => "Succeeded",
                    Some(Phase::Failed) => "Failed",
                    Some(Phase::Pending) | None => "Pending",
                };
                *tests
                    .entry((test.namespace().unwrap_or_default(), phase))
                    .or_insert(0i64) += 1;
            }
        }
        let gauge = ConstGauge::new(0i64);
        let mut family = encoder.encode_descriptor(
            "kates_operator_tests",
            "KatesTests by phase",
            None,
            gauge.metric_type(),
        )?;
        for ((namespace, phase), count) in tests {
            let labels = TestLabels { namespace, phase };
            ConstGauge::new(count).encode(family.encode_family(&labels)?)?;
        }

        let mut family = encoder.encode_descriptor(
            "kates_operator_compliance_rules",
            "Rules of the last compliance check, by verdict",
            None,
            gauge.metric_type(),
        )?;
        for store in self.0.checks.read().expect("the stores").iter() {
            for check in store.state() {
                let Some(counts) = check.status.as_ref().and_then(|s| s.counts.as_ref()) else {
                    continue;
                };
                for (verdict, count) in [
                    ("pass", counts.pass),
                    ("fail", counts.fail),
                    ("unknown", counts.unknown),
                ] {
                    let labels = RuleLabels {
                        namespace: check.namespace().unwrap_or_default(),
                        name: check.name_any(),
                        verdict,
                    };
                    ConstGauge::new(count).encode(family.encode_family(&labels)?)?;
                }
            }
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use opentelemetry::trace::TraceId;
    use prometheus_client::encoding::text::encode;
    use prometheus_client::registry::Registry;

    use super::Metrics;

    fn scrape(registry: &Registry) -> String {
        let mut out = String::new();
        encode(&mut out, registry).unwrap();
        out
    }

    #[test]
    fn names_and_labels_are_the_ones_dashboards_read() {
        let mut registry = Registry::default();
        let metrics = Metrics::register(&mut registry);

        drop(metrics.reconcile_started("KatesTest", TraceId::INVALID));
        metrics.reconcile_failed("KatesTest", "Rejected");

        let out = scrape(&registry);
        assert!(out.contains(r#"kates_operator_reconciliations_total{kind="KatesTest"} 1"#));
        assert!(out.contains(
            r#"kates_operator_reconciliation_errors_total{kind="KatesTest",error="Rejected"} 1"#
        ));
        assert!(
            out.contains(r#"kates_operator_reconcile_duration_seconds_count{kind="KatesTest"} 1"#)
        );
        assert!(out.contains(r#"le="60.0""#));
        assert!(!out.contains("trace_id"));
    }

    #[test]
    fn a_traced_pass_leaves_an_exemplar() {
        let mut registry = Registry::default();
        let metrics = Metrics::register(&mut registry);

        drop(metrics.reconcile_started("KatesTest", TraceId::from(7)));

        assert!(scrape(&registry).contains(r#"trace_id="00000000000000000000000000000007""#));
    }
}
