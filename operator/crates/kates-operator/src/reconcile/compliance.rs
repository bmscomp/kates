//! `KafkaCompliance`: a standing check. Section 4.4 of plans/operator.md. A watch only enqueues and
//! a pass cannot see why it ran, so whether to post is decided from state, in [`due`].

use std::sync::Arc;
use std::time::Duration;

use jiff::{SignedDuration, Timestamp};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::Time;
use kates_client::{ComplianceAnswer, Error as ApiError};
use kates_crds::{ComplianceCounts, FailedRule, KafkaCompliance, KafkaComplianceStatus};
use kube::ResourceExt as _;
use kube::api::{Api, DynamicObject, Patch, PatchParams};
use kube::runtime::controller::Action;
use serde_json::{Value, json};

use super::{Error, RECONCILE_AT, paused};
use crate::connection::Failure;
use crate::context::{Context, Note, Watched};
use crate::status;
use crate::telemetry::current_trace_id;

const KIND: &str = "KafkaCompliance";
const NO_CONNECTION_RETRY: Duration = Duration::from_mins(1);
const WRITE_RETRY: Duration = Duration::from_secs(30);
const MIN_INTERVAL: Duration = Duration::from_mins(1);
const MAX_INTERVAL: Duration = Duration::from_hours(24 * 365);
const MAX_FAILED: usize = 20;

/// # Errors
/// A failed Kubernetes write.
#[tracing::instrument(skip_all, fields(name = %check.name_any(), namespace = ?check.namespace()))]
pub async fn reconcile(check: Arc<KafkaCompliance>, ctx: Arc<Context>) -> Result<Action, Error> {
    let _timer = ctx.metrics.reconcile_started(KIND, current_trace_id());
    let namespace = check.namespace().ok_or(Error::NoNamespace)?;
    let api: Api<KafkaCompliance> = Api::namespaced(ctx.client.clone(), &namespace);
    let name = check.name_any();
    let now = Timestamp::now();
    let mut pass = Pass::new(&check, now);

    if paused(check.annotations()) {
        pass.condition(
            "Reconciling",
            "False",
            "Paused",
            "kates.io/pause-reconciliation is set",
        );
        write(&api, &name, &pass.status, &ctx).await?;
        return Ok(Action::await_change());
    }
    if check.spec.suspend {
        pass.status.observed_generation = check.metadata.generation;
        pass.condition("Reconciling", "False", "Suspended", "spec.suspend is set");
        write(&api, &name, &pass.status, &ctx).await?;
        return Ok(Action::await_change());
    }

    let (target_namespace, target_name) = target(&check);
    let watched = pass.kafka_watched(&ctx, &target_namespace);
    let kafka = if watched == Some(Watched::Yes) {
        read_kafka(&ctx, &target_namespace, &target_name).await
    } else {
        None
    };
    if !due(&check, &pass.status, now, kafka) {
        let wait = wait_until(pass.status.next_check_at.as_ref(), now);
        write(&api, &name, &pass.status, &ctx).await?;
        ctx.publish(&*check, pass.notes).await;
        return Ok(wait);
    }

    let action = match ctx.connections.resolve(&ctx.client, &namespace).await {
        Ok(connection) => {
            let document = reference_document(&name, &check.spec.reference);
            let answer = connection
                .api
                .check_compliance(&connection.key, &document)
                .await;
            pass.answered(&check, answer, kafka, &ctx)
        }
        Err(Failure::Missing) => {
            pass.unsynced("NoConnection", &Failure::Missing.to_string());
            Action::requeue(NO_CONNECTION_RETRY)
        }
        Err(Failure::Invalid(message)) => {
            pass.unsynced("InvalidConnection", &message);
            Action::requeue(NO_CONNECTION_RETRY)
        }
        Err(Failure::Kube(error)) => return Err(error.into()),
    };
    write(&api, &name, &pass.status, &ctx).await?;
    ctx.publish(&*check, pass.notes).await;
    Ok(action)
}

#[must_use]
#[allow(clippy::needless_pass_by_value)] // Controller::run's signature
pub fn error_policy(check: Arc<KafkaCompliance>, error: &Error, ctx: Arc<Context>) -> Action {
    ctx.metrics.reconcile_failed(KIND, error.name());
    tracing::warn!(%error, name = %check.name_any(), "reconcile failed");
    Action::requeue(WRITE_RETRY)
}

/// The `Kafka` a check names; the CRD requires both.
#[must_use]
pub fn target(check: &KafkaCompliance) -> (String, String) {
    let field = |key: &str| {
        check.spec.reference["target"][key]
            .as_str()
            .unwrap_or_default()
            .to_owned()
    };
    (field("namespace"), field("name"))
}

// (generation, status.observedGeneration); a failed or 404 get is no trigger.
async fn read_kafka(ctx: &Context, namespace: &str, name: &str) -> Option<(i64, i64)> {
    let watch = ctx.kafka.as_ref()?;
    let api: Api<DynamicObject> =
        Api::namespaced_with(ctx.client.clone(), namespace, &watch.resource);
    let kafka = api.get(name).await.ok()?;
    Some((
        kafka.metadata.generation?,
        kafka.data["status"]["observedGeneration"].as_i64()?,
    ))
}

/// The document `POST /api/conformity/check` reads: the resource's name becomes the stored name.
#[must_use]
pub fn reference_document(name: &str, reference: &Value) -> Value {
    json!({
        "apiVersion": "conformity.kates.io/v1alpha1",
        "kind": "KafkaReference",
        "metadata": {"name": name},
        "spec": reference,
    })
}

/// Whether this pass posts. A `Kafka` still rolling (`observedGeneration` behind) is no trigger:
/// the report would grade a half-rolled cluster. The interval still fires, so a stuck Kafka keeps
/// being checked.
#[must_use]
fn due(
    check: &KafkaCompliance,
    status: &KafkaComplianceStatus,
    now: Timestamp,
    kafka: Option<(i64, i64)>,
) -> bool {
    let changed = check.metadata.generation != status.observed_generation;
    let forced = check
        .annotations()
        .get(RECONCILE_AT)
        .is_some_and(|value| status.last_handled_reconcile_at.as_ref() != Some(value));
    let elapsed = status.next_check_at.as_ref().is_some_and(|at| now >= at.0);
    let stalled = status::is_true(&status.conditions, "Stalled");
    let kafka_moved = !stalled
        && kafka.is_some_and(|(generation, observed)| {
            observed == generation && status.observed_kafka_generation != Some(observed)
        });
    changed || forced || elapsed || kafka_moved
}

fn wait_until(next: Option<&Time>, now: Timestamp) -> Action {
    next.map_or(Action::await_change(), |at| {
        let left = at.0.duration_since(now);
        Action::requeue(Duration::try_from(left).unwrap_or(Duration::ZERO))
    })
}

/// `1h`, `30m`, `1h30m`: the shape the CRD's pattern allows, floored so a `0s` cannot spin, and
/// capped because the pattern allows any number of digits and a timestamp does not.
#[must_use]
pub fn interval(text: &str) -> Duration {
    let mut seconds = 0u64;
    let mut number = 0u64;
    for c in text.chars() {
        if let Some(digit) = c.to_digit(10) {
            number = number.saturating_mul(10).saturating_add(u64::from(digit));
            continue;
        }
        let unit = match c {
            'h' => 3600,
            'm' => 60,
            _ => 1,
        };
        seconds = seconds.saturating_add(number.saturating_mul(unit));
        number = 0;
    }
    Duration::from_secs(seconds).clamp(MIN_INTERVAL, MAX_INTERVAL)
}

async fn write(
    api: &Api<KafkaCompliance>,
    name: &str,
    status: &KafkaComplianceStatus,
    ctx: &Context,
) -> Result<(), Error> {
    let patch = json!({
        "apiVersion": "kates.io/v1alpha1",
        "kind": KIND,
        "status": status,
    });
    let params = PatchParams::apply(&ctx.field_manager).force();
    api.patch_status(name, &params, &Patch::Apply(patch))
        .await?;
    Ok(())
}

struct Pass {
    status: KafkaComplianceStatus,
    generation: Option<i64>,
    now: Timestamp,
    notes: Vec<Note>,
}

impl Pass {
    fn new(check: &KafkaCompliance, now: Timestamp) -> Self {
        Self {
            status: check.status.clone().unwrap_or_default(),
            generation: check.metadata.generation,
            now,
            notes: Vec::new(),
        }
    }

    fn condition(&mut self, type_: &str, value: &str, reason: &str, message: &str) {
        let conditions = &mut self.status.conditions;
        status::set(
            conditions,
            type_,
            value,
            reason,
            message,
            self.generation,
            self.now,
        );
    }

    fn reason(&self, type_: &str) -> Option<&str> {
        status::find(&self.status.conditions, type_).map(|c| c.reason.as_str())
    }

    fn after(&self, wait: Duration) -> Time {
        let wait = SignedDuration::try_from(wait).expect("an interval fits");
        Time(self.now.saturating_add(wait).expect("an interval fits"))
    }

    fn kafka_watched(&mut self, ctx: &Context, namespace: &str) -> Option<Watched> {
        let Some(watch) = &ctx.kafka else {
            self.condition(
                "KafkaWatched",
                "False",
                "NoStrimzi",
                "no Strimzi CRD: interval only",
            );
            return None;
        };
        let state = watch.watched(namespace);
        let (value, reason, message) = match state {
            Watched::Yes => ("True", "Watching", String::new()),
            Watched::NotConfigured => (
                "False",
                "NamespaceNotConfigured",
                format!("add {namespace} to kafka.namespaces; until then, interval only"),
            ),
            Watched::Forbidden => (
                "False",
                "Forbidden",
                format!(
                    "the operator may not list kafkas in {namespace}: bind its kafka-reader Role"
                ),
            ),
        };
        if value == "False" && self.reason("KafkaWatched") != Some(reason) {
            self.notes.push(Note {
                warning: true,
                reason: "KafkaNotWatched",
                message: message.clone(),
            });
        }
        self.condition("KafkaWatched", value, reason, &message);
        Some(state)
    }

    fn unsynced(&mut self, reason: &'static str, message: &str) {
        if self.reason("Synced") != Some(reason) {
            self.notes.push(Note {
                warning: true,
                reason,
                message: message.to_owned(),
            });
        }
        self.condition("Synced", "False", reason, message);
    }

    fn record(&mut self, check: &KafkaCompliance, kafka: Option<(i64, i64)>) {
        self.status.observed_generation = check.metadata.generation;
        if let Some(value) = check.annotations().get(RECONCILE_AT) {
            self.status.last_handled_reconcile_at = Some(value.clone());
        }
        if let Some((_, observed)) = kafka {
            self.status.observed_kafka_generation = Some(observed);
        }
    }

    fn answered(
        &mut self,
        check: &KafkaCompliance,
        answer: Result<ComplianceAnswer, ApiError>,
        kafka: Option<(i64, i64)>,
        ctx: &Context,
    ) -> Action {
        let every = interval(&check.spec.interval);
        match answer {
            Ok(answer) => {
                self.record(check, kafka);
                self.verdict(answer, every, kafka)
            }
            // The reference is wrong as written: no POST until the spec or reconcile-at changes.
            Err(ApiError::Rejected { message, .. }) => {
                self.record(check, kafka);
                self.status.next_check_at = None;
                self.unsynced("SpecRejected", &message);
                self.condition("Ready", "False", "SpecRejected", &message);
                self.condition("Stalled", "True", "SpecRejected", &message);
                Action::await_change()
            }
            // A refused check waits for the interval, not the next pass.
            Err(
                ApiError::Conflict { message }
                | ApiError::Unavailable {
                    status: Some(503),
                    api_message: Some(message),
                    ..
                },
            ) => self.refused(check, kafka, every, &message),
            Err(error) => {
                ctx.metrics.reconcile_failed(KIND, error.name());
                let reason = match error {
                    ApiError::InvalidKey => "InvalidKey",
                    ApiError::MissingScope => "MissingScope",
                    _ => "ApiUnavailable",
                };
                self.unsynced(reason, &error.to_string());
                Action::requeue(status::backoff(&self.status.conditions, self.now))
            }
        }
    }

    fn refused(
        &mut self,
        check: &KafkaCompliance,
        kafka: Option<(i64, i64)>,
        every: Duration,
        message: &str,
    ) -> Action {
        self.record(check, kafka);
        self.status.next_check_at = Some(self.after(every));
        self.unsynced("CheckRefused", message);
        Action::requeue(every)
    }

    fn verdict(
        &mut self,
        answer: ComplianceAnswer,
        every: Duration,
        kafka: Option<(i64, i64)>,
    ) -> Action {
        let report = answer.report;
        let previous = self.status.verdict.replace(report.verdict.clone());
        let failed: Vec<FailedRule> = report
            .results
            .iter()
            .filter(|result| result.status == "FAIL")
            .take(MAX_FAILED)
            .map(|result| FailedRule {
                rule: result.rule.clone(),
                detail: result.detail.clone(),
            })
            .collect();
        let first = failed
            .first()
            .map_or(String::new(), |rule| match &rule.detail {
                Some(detail) => format!("{}: {detail}", rule.rule),
                None => rule.rule.clone(),
            });
        match (previous.as_deref(), report.verdict.as_str()) {
            (Some("FAIL"), "FAIL") => {}
            (_, "FAIL") => self.notes.push(Note {
                warning: true,
                reason: "ComplianceFailed",
                message: first.clone(),
            }),
            (Some("FAIL"), "PASS") => self.notes.push(Note {
                warning: false,
                reason: "ComplianceRestored",
                message: "every rule passes".to_owned(),
            }),
            _ => {}
        }
        self.status.counts = Some(ComplianceCounts {
            pass: report.summary.pass,
            fail: report.summary.fail,
            unknown: report.summary.unknown,
        });
        self.status.failed = failed;
        self.status.checked_at = Some(Time(self.now));
        self.status.check_id.clone_from(&answer.check_id);
        status::remove(&mut self.status.conditions, "Stalled");

        let lagging = kafka.is_some_and(|(generation, observed)| generation != observed);
        let message = if lagging {
            format!("Kafka not yet reconciled. {first}")
                .trim()
                .to_owned()
        } else {
            first
        };
        let compliant = match report.verdict.as_str() {
            "PASS" => "True",
            "FAIL" => "False",
            _ => "Unknown",
        };
        self.condition("Compliant", compliant, &report.verdict, &message);
        self.condition("Ready", "True", "Checked", "");
        self.condition("Reconciling", "False", "Checked", "");

        // The usual cause of a lost report is the Kates database, so it retries on the backoff.
        if answer.check_id.is_none() {
            self.unsynced(
                "ReportNotStored",
                "the API ran the check but kept no report",
            );
            let wait = status::backoff(&self.status.conditions, self.now);
            self.status.next_check_at = Some(self.after(wait));
            return Action::requeue(wait);
        }
        self.condition("Synced", "True", "Checked", "");
        self.status.next_check_at = Some(self.after(every));
        Action::requeue(every)
    }
}

#[cfg(test)]
mod tests {
    use kates_client::{ComplianceReport, ComplianceResult, ComplianceSummary};
    use kates_crds::KafkaComplianceSpec;

    use super::*;

    fn at(seconds: i64) -> Timestamp {
        Timestamp::from_second(seconds).unwrap()
    }

    fn check() -> KafkaCompliance {
        let spec: KafkaComplianceSpec = serde_json::from_value(json!({
            "reference": {"target": {"namespace": "kafka", "name": "krafter"}},
            "interval": "1h",
        }))
        .unwrap();
        let mut check = KafkaCompliance::new("rules", spec);
        check.metadata.generation = Some(1);
        check
    }

    fn checked() -> KafkaComplianceStatus {
        KafkaComplianceStatus {
            observed_generation: Some(1),
            next_check_at: Some(Time(at(1000))),
            observed_kafka_generation: Some(7),
            ..KafkaComplianceStatus::default()
        }
    }

    fn answer(verdict: &str, check_id: Option<&str>) -> ComplianceAnswer {
        ComplianceAnswer {
            report: ComplianceReport {
                verdict: verdict.to_owned(),
                summary: ComplianceSummary {
                    pass: 3,
                    fail: i64::from(verdict == "FAIL"),
                    ..ComplianceSummary::default()
                },
                results: vec![ComplianceResult {
                    rule: "kafka.replicas.min".to_owned(),
                    status: verdict.to_owned(),
                    detail: Some("2 < 3".to_owned()),
                }],
            },
            check_id: check_id.map(str::to_owned),
        }
    }

    fn pass() -> Pass {
        Pass::new(&check(), at(100))
    }

    fn reasons(pass: &Pass) -> Vec<&'static str> {
        pass.notes.iter().map(|note| note.reason).collect()
    }

    #[test]
    fn the_document_wraps_the_reference() {
        assert_eq!(
            reference_document("rules", &json!({"target": {"name": "k"}})),
            json!({"apiVersion": "conformity.kates.io/v1alpha1", "kind": "KafkaReference",
                "metadata": {"name": "rules"}, "spec": {"target": {"name": "k"}}})
        );
    }

    #[test]
    fn intervals_parse_and_are_floored() {
        assert_eq!(interval("1h"), Duration::from_hours(1));
        assert_eq!(interval("1h30m"), Duration::from_mins(90));
        assert_eq!(interval("90s"), Duration::from_secs(90));
        assert_eq!(interval("0s"), MIN_INTERVAL);
        assert_eq!(interval("99999999999999999999999h"), MAX_INTERVAL);
    }

    #[test]
    fn a_new_resource_a_new_generation_or_a_forced_pass_posts() {
        assert!(due(
            &check(),
            &KafkaComplianceStatus::default(),
            at(100),
            None
        ));

        let mut edited = check();
        edited.metadata.generation = Some(2);
        assert!(due(&edited, &checked(), at(100), None));

        let mut forced = check();
        forced
            .annotations_mut()
            .insert(RECONCILE_AT.to_owned(), "now".to_owned());
        assert!(due(&forced, &checked(), at(100), None));
        let handled = KafkaComplianceStatus {
            last_handled_reconcile_at: Some("now".to_owned()),
            ..checked()
        };
        assert!(!due(&forced, &handled, at(100), None));
    }

    #[test]
    fn nothing_new_waits_for_the_interval() {
        assert!(!due(&check(), &checked(), at(100), Some((7, 7))));
        assert!(due(&check(), &checked(), at(1000), None));
    }

    #[test]
    fn a_kafka_triggers_only_once_it_has_settled() {
        assert!(due(&check(), &checked(), at(100), Some((8, 8))));
        assert!(!due(&check(), &checked(), at(100), Some((8, 7))));
    }

    #[test]
    fn a_rejected_reference_stops_the_kafka_trigger() {
        let mut status = checked();
        status::set(
            &mut status.conditions,
            "Stalled",
            "True",
            "SpecRejected",
            "",
            None,
            at(1),
        );
        status.next_check_at = None;
        assert!(!due(&check(), &status, at(100), Some((8, 8))));
    }

    #[test]
    fn a_failure_tells_once_and_a_recovery_tells_once() {
        let mut pass = pass();
        pass.verdict(answer("FAIL", Some("c1")), MIN_INTERVAL, None);
        pass.verdict(answer("FAIL", Some("c2")), MIN_INTERVAL, None);
        assert_eq!(reasons(&pass), ["ComplianceFailed"]);
        pass.verdict(answer("PASS", Some("c3")), MIN_INTERVAL, None);
        assert_eq!(reasons(&pass), ["ComplianceFailed", "ComplianceRestored"]);
        assert_eq!(pass.reason("Compliant"), Some("PASS"));
        assert_eq!(pass.status.check_id.as_deref(), Some("c3"));
        assert_eq!(pass.status.failed, []);
    }

    #[test]
    fn the_verdict_fills_the_status() {
        let mut pass = pass();
        let action = pass.verdict(answer("FAIL", Some("c1")), Duration::from_hours(1), None);
        assert_eq!(action, Action::requeue(Duration::from_hours(1)));
        assert_eq!(pass.status.counts.as_ref().unwrap().fail, 1);
        assert_eq!(pass.status.failed[0].rule, "kafka.replicas.min");
        assert_eq!(pass.status.next_check_at.as_ref().unwrap().0, at(3700));
        let compliant = status::find(&pass.status.conditions, "Compliant").unwrap();
        assert_eq!(compliant.status, "False");
        assert_eq!(compliant.message, "kafka.replicas.min: 2 < 3");
    }

    #[test]
    fn a_lost_report_clears_the_old_id_and_retries_soon() {
        let mut pass = pass();
        pass.status.check_id = Some("old".to_owned());
        let action = pass.verdict(answer("PASS", None), Duration::from_hours(1), None);
        assert_eq!(pass.status.check_id, None);
        assert_eq!(pass.reason("Synced"), Some("ReportNotStored"));
        assert_eq!(reasons(&pass), ["ReportNotStored"]);
        assert_eq!(action, Action::requeue(Duration::from_secs(5)));
    }

    #[test]
    fn a_lagging_kafka_is_said_in_the_message() {
        let mut pass = pass();
        pass.verdict(answer("PASS", Some("c1")), MIN_INTERVAL, Some((8, 7)));
        let compliant = status::find(&pass.status.conditions, "Compliant").unwrap();
        assert_eq!(compliant.message, "Kafka not yet reconciled.");
    }

    #[test]
    fn the_target_comes_from_the_reference() {
        assert_eq!(target(&check()), ("kafka".to_owned(), "krafter".to_owned()));
    }
}
