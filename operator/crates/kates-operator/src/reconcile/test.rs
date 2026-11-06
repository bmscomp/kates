//! `KatesTest`: one run. Section 4.2 of plans/operator.md, step by step. The decisions are in
//! [`Pass`], over the status alone, so each is testable without a cluster.

use std::sync::Arc;
use std::time::Duration;

use jiff::{SignedDuration, Timestamp};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::Time;
use kates_client::{Error as ApiError, Report, Run, RunStatus};
use kates_crds::{KatesTest, KatesTestStatus, Phase, RunSummary};
use kube::api::{DeleteParams, Patch, PatchParams};
use kube::runtime::controller::Action;
use kube::runtime::finalizer::{self, Event as Finalizer};
use kube::{Api, Resource as _, ResourceExt as _};
use serde_json::{Value, json};

use super::{Error, RECONCILE_AT, paused};
use crate::connection::{Connection, Failure};
use crate::context::{Context, Note};
use crate::status;
use crate::telemetry::current_trace_id;

pub const FINALIZER: &str = "kates.io/finalizer";
const KIND: &str = "KatesTest";
const CLIENT_KEY: &str = "clientKey";
const MINIMUM_VERSION: &str = "1.38.0";
const POLL: Duration = Duration::from_secs(10);
const NO_CONNECTION_RETRY: Duration = Duration::from_mins(1);
const API_TOO_OLD_RETRY: Duration = Duration::from_mins(5);
const ENGINE_FULL_RETRY: Duration = Duration::from_secs(30);
const WRITE_RETRY: Duration = Duration::from_secs(30);

/// # Errors
/// A failed Kubernetes write, or a cancel that must hold the finalizer.
#[tracing::instrument(skip_all, fields(name = %test.name_any(), namespace = ?test.namespace()))]
pub async fn reconcile(test: Arc<KatesTest>, ctx: Arc<Context>) -> Result<Action, Error> {
    let _timer = ctx.metrics.reconcile_started(KIND, current_trace_id());
    let namespace = test.namespace().ok_or(Error::NoNamespace)?;
    let api: Api<KatesTest> = Api::namespaced(ctx.client.clone(), &namespace);

    // A settled resource never enters finalizer(), which re-adds its entry on every pass.
    if settled(&test) {
        return finish_settled(&test, &api, &ctx).await;
    }
    finalizer::finalizer(&api, FINALIZER, test, |event| async {
        match event {
            Finalizer::Apply(test) => apply(&test, &api, &ctx).await,
            Finalizer::Cleanup(test) => cleanup(&test, &namespace, &api, &ctx).await,
        }
    })
    .await
    .map_err(|error| Error::Finalizer(Box::new(error)))
}

#[must_use]
#[allow(clippy::needless_pass_by_value)] // Controller::run's signature
pub fn error_policy(test: Arc<KatesTest>, error: &Error, ctx: Arc<Context>) -> Action {
    ctx.metrics.reconcile_failed(KIND, error.name());
    tracing::warn!(%error, name = %test.name_any(), "reconcile failed");
    if error.is_cancel() {
        let conditions = test.status.as_ref().map_or(&[][..], |s| &s.conditions);
        return Action::requeue(status::backoff(conditions, Timestamp::now()));
    }
    Action::requeue(WRITE_RETRY)
}

fn settled(test: &KatesTest) -> bool {
    test.status.as_ref().is_some_and(|status| {
        status.finished_at.is_some() && status.observed_generation == test.metadata.generation
    })
}

async fn finish_settled(
    test: &KatesTest,
    api: &Api<KatesTest>,
    ctx: &Context,
) -> Result<Action, Error> {
    let name = test.name_any();
    let deleting = test.meta().deletion_timestamp.is_some();
    let mut pass = Pass::new(test, Timestamp::now());
    if paused(test.annotations()) && !deleting {
        pass.paused();
        write(api, &name, &pass.status, ctx).await?;
        return Ok(Action::await_change());
    }
    pass.handle_reconcile_at(test);
    pass.condition("Reconciling", "False", "Finished", "");
    // Status first, then the strip, so a crash between the two heals on the next pass.
    write(api, &name, &pass.status, ctx).await?;
    strip_finalizer(test, api).await?;
    if deleting {
        return Ok(Action::await_change());
    }
    match ttl_left(test, pass.now) {
        None => Ok(Action::await_change()),
        Some(left) if left.is_zero() => {
            api.delete(&name, &DeleteParams::default()).await?;
            Ok(Action::await_change())
        }
        Some(left) => Ok(Action::requeue(left)),
    }
}

// The remaining time, never the whole TTL, or every restart would reset the clock.
fn ttl_left(test: &KatesTest, now: Timestamp) -> Option<Duration> {
    let ttl = i64::from(test.spec.ttl_seconds_after_finished?);
    let finished = test.status.as_ref()?.finished_at.as_ref()?.0;
    let left = finished
        .saturating_add(SignedDuration::from_secs(ttl))
        .expect("a TTL fits a timestamp")
        .duration_since(now);
    Some(Duration::try_from(left).unwrap_or(Duration::ZERO))
}

// A test-guarded patch, as kube's own helper: a merge patch of the list could drop another
// controller's entry.
async fn strip_finalizer(test: &KatesTest, api: &Api<KatesTest>) -> Result<(), Error> {
    let finalizers = test.finalizers();
    let Some(index) = finalizers.iter().position(|f| f == FINALIZER) else {
        return Ok(());
    };
    let entry = format!("/metadata/finalizers/{index}");
    let patch: json_patch::Patch = serde_json::from_value(json!([
        {"op": "test", "path": entry, "value": FINALIZER},
        {"op": "remove", "path": entry},
    ]))
    .expect("a JSON patch");
    api.patch(
        &test.name_any(),
        &PatchParams::default(),
        &Patch::Json::<()>(patch),
    )
    .await?;
    Ok(())
}

async fn apply(test: &KatesTest, api: &Api<KatesTest>, ctx: &Context) -> Result<Action, Error> {
    let name = test.name_any();
    let now = Timestamp::now();
    let mut pass = Pass::new(test, now);
    if paused(test.annotations()) {
        pass.paused();
        write(api, &name, &pass.status, ctx).await?;
        return Ok(Action::await_change());
    }
    pass.handle_reconcile_at(test);
    pass.reopen_if_edited();

    let namespace = test.namespace().ok_or(Error::NoNamespace)?;
    let action = match ctx.connections.resolve(&ctx.client, &namespace).await {
        Ok(connection) => drive(test, &mut pass, &connection, ctx).await,
        Err(Failure::Missing) => pass.no_connection("NoConnection", &Failure::Missing.to_string()),
        Err(Failure::Invalid(message)) => pass.no_connection("InvalidConnection", &message),
        Err(Failure::Kube(error)) => return Err(error.into()),
    };
    write(api, &name, &pass.status, ctx).await?;
    ctx.publish(test, pass.notes).await;
    Ok(action)
}

async fn drive(
    test: &KatesTest,
    pass: &mut Pass,
    connection: &Connection,
    ctx: &Context,
) -> Action {
    let uid = test.uid().unwrap_or_default();
    let (api, key) = (&connection.api, &connection.key);
    let Some(id) = pass.status.run_id.clone() else {
        return match api.capabilities(key).await {
            Ok(capabilities) if capabilities.iter().any(|c| c == CLIENT_KEY) => {
                let answer = api.create_test(key, &uid, &request(test)).await;
                pass.created(answer, ctx)
            }
            Ok(_) => pass.api_too_old(),
            Err(error) => pass.failed(&error, ctx),
        };
    };
    match api.get_test(key, &id).await {
        Err(ApiError::NotFound) => pass.stall(
            "RunGone",
            "the Kates run no longer exists: it was deleted, or the database was replaced",
        ),
        Err(error) => pass.failed(&error, ctx),
        Ok(run) if run.client_key.as_deref() != Some(&uid) => pass.stall(
            "ForeignRun",
            "this run was not started by this resource, so it is neither followed nor cancelled",
        ),
        Ok(run) if !run.status.is_terminal() => pass.live(&run),
        Ok(run) => match api.report(key, &id).await {
            Ok(report) => pass.ended(&run, &report),
            // Never SpecRejected: a missing run answers 400 here, and the next pass finds RunGone.
            Err(error) => pass.report_failed(&error, ctx),
        },
    }
}

/// The body of `POST /api/tests`.
#[must_use]
pub fn request(test: &KatesTest) -> Value {
    let spec = &test.spec;
    let mut body = json!({"type": spec.test_type});
    for (key, value) in [
        ("spec", spec.spec.as_ref()),
        ("sla", spec.sla.as_ref()),
        ("scenario", spec.scenario.as_ref()),
    ] {
        if let Some(value) = value {
            body[key] = value.clone();
        }
    }
    if let Some(backend) = &spec.backend {
        body["backend"] = backend.as_str().into();
    }
    body
}

async fn cleanup(
    test: &KatesTest,
    namespace: &str,
    api: &Api<KatesTest>,
    ctx: &Context,
) -> Result<Action, Error> {
    let run_id = test.status.as_ref().and_then(|s| s.run_id.clone());
    let connection = match ctx.connections.resolve(&ctx.client, namespace).await {
        Ok(connection) => connection,
        // No key could ever cancel, and holding on would leave the namespace Terminating.
        // The API ends the run itself through its timeout reaper.
        Err(Failure::Missing | Failure::Invalid(_)) => {
            let message = format!(
                "no connection to cancel run {}: Kates ends it at its own timeout",
                run_id.as_deref().unwrap_or("(none)")
            );
            ctx.publish(test, vec![warning("CancelSkipped", message)])
                .await;
            return Ok(Action::await_change());
        }
        Err(Failure::Kube(error)) => return Err(error.into()),
    };
    match cancel(test, run_id.as_deref(), &connection).await {
        Ok(Some(id)) => {
            let note = Note {
                warning: false,
                reason: "Cancelled",
                message: format!("run {id} cancelled"),
            };
            ctx.publish(test, vec![note]).await;
            Ok(Action::await_change())
        }
        Ok(None) => Ok(Action::await_change()),
        Err(error) => {
            let mut pass = Pass::new(test, Timestamp::now());
            let hatch = format!(
                "{error}. The finalizer holds until the API answers; to give up on the run: \
                 kubectl patch katestest {} --type=merge -p '{{\"metadata\":{{\"finalizers\":null}}}}'",
                test.name_any()
            );
            pass.unsynced(api_reason(&error), &hatch);
            write(api, &test.name_any(), &pass.status, ctx).await?;
            ctx.publish(test, pass.notes).await;
            Err(Error::Cancel(error))
        }
    }
}

// The run this resource started, cancelled when live; its id when it was.
async fn cancel(
    test: &KatesTest,
    run_id: Option<&str>,
    connection: &Connection,
) -> Result<Option<String>, ApiError> {
    let uid = test.uid().unwrap_or_default();
    let (api, key) = (&connection.api, &connection.key);
    let run = match run_id {
        Some(id) => match api.get_test(key, id).await {
            Ok(run) => Some(run),
            Err(ApiError::NotFound) => None,
            Err(error) => return Err(error),
        },
        None => api.find_by_client_key(key, &uid).await?,
    };
    let Some(run) = run else { return Ok(None) };
    if run.client_key.as_deref() != Some(&uid) || run.status.is_terminal() {
        return Ok(None);
    }
    match api.cancel_test(key, &run.id).await {
        Ok(()) => Ok(Some(run.id)),
        // "Test is not running": it finished in between.
        Err(ApiError::Conflict { .. } | ApiError::NotFound) => Ok(None),
        Err(error) => Err(error),
    }
}

async fn write(
    api: &Api<KatesTest>,
    name: &str,
    status: &KatesTestStatus,
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

fn api_reason(error: &ApiError) -> &'static str {
    match error {
        ApiError::InvalidKey => "InvalidKey",
        ApiError::MissingScope => "MissingScope",
        _ => "ApiUnavailable",
    }
}

fn warning(reason: &'static str, message: String) -> Note {
    Note {
        warning: true,
        reason,
        message,
    }
}

/// One pass's status: what the run says, turned into conditions.
struct Pass {
    status: KatesTestStatus,
    generation: Option<i64>,
    now: Timestamp,
    notes: Vec<Note>,
}

impl Pass {
    fn new(test: &KatesTest, now: Timestamp) -> Self {
        let mut status = test.status.clone().unwrap_or_default();
        status.observed_generation = test.metadata.generation;
        Self {
            status,
            generation: test.metadata.generation,
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

    fn paused(&mut self) {
        self.condition(
            "Reconciling",
            "False",
            "Paused",
            "kates.io/pause-reconciliation is set",
        );
    }

    fn handle_reconcile_at(&mut self, test: &KatesTest) {
        if let Some(value) = test.annotations().get(RECONCILE_AT) {
            self.status.last_handled_reconcile_at = Some(value.clone());
        }
    }

    // A rejected resource whose spec was edited: the same key is safe, a 400 stored no run.
    fn reopen_if_edited(&mut self) {
        if self.status.finished_at.take().is_some() {
            self.status.phase = None;
            status::remove(&mut self.status.conditions, "Stalled");
        }
    }

    fn synced(&mut self, reason: &str) {
        self.condition("Synced", "True", reason, "");
    }

    /// `Synced=False`, with one Event per transition, never per retry.
    fn unsynced(&mut self, reason: &'static str, message: &str) {
        if self.reason("Synced") != Some(reason) {
            self.notes.push(warning(reason, message.to_owned()));
        }
        self.condition("Synced", "False", reason, message);
        self.condition("Ready", "False", reason, message);
        self.condition("Reconciling", "True", reason, message);
    }

    fn backoff(&self) -> Action {
        Action::requeue(status::backoff(&self.status.conditions, self.now))
    }

    fn no_connection(&mut self, reason: &'static str, message: &str) -> Action {
        self.unsynced(reason, message);
        Action::requeue(NO_CONNECTION_RETRY)
    }

    fn api_too_old(&mut self) -> Action {
        self.status.phase = Some(Phase::Pending);
        let message = format!(
            "this Kates API cannot honour an idempotency key, so no run is started: \
             it needs Kates {MINIMUM_VERSION} or newer"
        );
        self.unsynced("ApiTooOld", &message);
        Action::requeue(API_TOO_OLD_RETRY)
    }

    fn failed(&mut self, error: &ApiError, ctx: &Context) -> Action {
        ctx.metrics.reconcile_failed(KIND, error.name());
        self.unsynced(api_reason(error), &error.to_string());
        self.backoff()
    }

    fn report_failed(&mut self, error: &ApiError, ctx: &Context) -> Action {
        ctx.metrics.reconcile_failed(KIND, error.name());
        let reason = api_reason(error);
        self.unsynced(
            reason,
            &format!("the run ended but its report is not readable: {error}"),
        );
        self.backoff()
    }

    fn created(&mut self, answer: Result<Run, ApiError>, ctx: &Context) -> Action {
        match answer {
            Ok(run) => {
                self.notes.push(Note {
                    warning: false,
                    reason: "RunCreated",
                    message: format!("run {}", run.id),
                });
                self.status.run_id = Some(run.id.clone());
                self.status.started_at = Some(Time(self.now));
                self.synced("RunCreated");
                self.live(&run)
            }
            Err(ApiError::Rejected { message, .. }) => self.stall("SpecRejected", &message),
            Err(ApiError::RateLimited {
                retry_after,
                message,
            }) => {
                self.status.phase = Some(Phase::Pending);
                self.unsynced("EngineFull", &message);
                Action::requeue(retry_after.unwrap_or(ENGINE_FULL_RETRY))
            }
            Err(error) => self.failed(&error, ctx),
        }
    }

    fn live(&mut self, run: &Run) -> Action {
        self.status.phase = Some(if run.status == RunStatus::Pending {
            Phase::Pending
        } else {
            Phase::Running
        });
        if self.reason("Synced") != Some("RunCreated") {
            self.synced("Following");
        }
        self.condition("Ready", "False", "Running", "");
        self.condition("Reconciling", "True", "Running", "");
        self.condition("SlaPassed", "Unknown", "Running", "");
        Action::requeue(POLL)
    }

    /// A terminal run, whose report answered: every field in one patch.
    fn ended(&mut self, run: &Run, report: &Report) -> Action {
        self.status.summary = serde_json::from_value::<RunSummary>(report.summary.clone()).ok();
        let verdict = report.overall_sla_verdict.as_ref();
        let passed = verdict.is_none_or(|v| v.passed);
        let first = verdict
            .and_then(|v| v.violations.first())
            .map(ToString::to_string);
        self.synced("Finished");
        self.condition(
            "SlaPassed",
            if passed { "True" } else { "False" },
            if passed { "SlaPassed" } else { "SlaFailed" },
            first.as_deref().unwrap_or_default(),
        );
        if run.status == RunStatus::Failed {
            return self.stall("RunFailed", first.as_deref().unwrap_or("the run failed"));
        }
        if !passed {
            return self.stall("SlaFailed", first.as_deref().unwrap_or("the SLA failed"));
        }
        self.status.phase = Some(Phase::Succeeded);
        self.status.finished_at = Some(Time(self.now));
        self.condition("Ready", "True", "RunCompleted", "");
        self.condition("Reconciling", "False", "Finished", "");
        self.notes.push(Note {
            warning: false,
            reason: "RunCompleted",
            message: format!("run {} met its gates", run.id),
        });
        Action::requeue(Duration::ZERO)
    }

    /// Nothing here will progress without a new resource: `Failed`, once.
    fn stall(&mut self, reason: &'static str, message: &str) -> Action {
        self.status.phase = Some(Phase::Failed);
        self.status.finished_at = Some(Time(self.now));
        if reason != "RunFailed" && reason != "SlaFailed" {
            self.condition("Synced", "False", reason, message);
        }
        self.condition("Ready", "False", reason, message);
        self.condition("Stalled", "True", reason, message);
        self.condition("Reconciling", "False", reason, message);
        self.notes.push(warning(reason, message.to_owned()));
        Action::requeue(Duration::ZERO)
    }
}

#[cfg(test)]
mod tests {
    use kates_client::{SlaVerdict, Violation};
    use kates_crds::KatesTestSpec;

    use super::*;

    fn resource(body: Value) -> KatesTest {
        let spec: KatesTestSpec = serde_json::from_value(body).unwrap();
        let mut test = KatesTest::new("t", spec);
        test.metadata.generation = Some(1);
        test
    }

    fn flat() -> KatesTest {
        resource(json!({"type": "LOAD", "spec": {"numRecords": 1}}))
    }

    fn at(seconds: i64) -> Timestamp {
        Timestamp::from_second(seconds).unwrap()
    }

    fn pass() -> Pass {
        Pass::new(&flat(), at(100))
    }

    fn run(status: RunStatus) -> Run {
        Run {
            id: "r1".to_owned(),
            status,
            client_key: Some("uid".to_owned()),
        }
    }

    fn report(passed: bool, violation: Option<Violation>) -> Report {
        Report {
            summary: json!({"totalRecords": 5, "p99LatencyMs": 2.5, "ignored": 1}),
            overall_sla_verdict: Some(SlaVerdict {
                passed,
                violations: violation.into_iter().collect(),
            }),
        }
    }

    fn condition<'a>(pass: &'a Pass, type_: &str) -> (&'a str, &'a str) {
        let c = status::find(&pass.status.conditions, type_).expect(type_);
        (c.status.as_str(), c.reason.as_str())
    }

    fn reasons(pass: &Pass) -> Vec<&'static str> {
        pass.notes.iter().map(|note| note.reason).collect()
    }

    #[test]
    fn the_request_carries_the_wire_names() {
        let test = resource(json!({"type": "ROUND_TRIP", "backend": "native",
            "spec": {"numRecords": 1}, "sla": {"maxP99LatencyMs": 9.0}}));
        assert_eq!(
            request(&test),
            json!({"type": "ROUND_TRIP", "backend": "native", "spec": {"numRecords": 1},
                "sla": {"maxP99LatencyMs": 9.0}})
        );
        let scenario = resource(json!({"type": "LOAD", "scenario": {"name": "s"}}));
        assert_eq!(
            request(&scenario),
            json!({"type": "LOAD", "scenario": {"name": "s"}})
        );
    }

    #[test]
    fn a_created_run_records_its_id_and_is_followed() {
        let mut pass = pass();
        let action = pass.live(&run(RunStatus::Running));
        assert_eq!(action, Action::requeue(POLL));
        assert_eq!(pass.status.phase, Some(Phase::Running));
        assert_eq!(condition(&pass, "Ready"), ("False", "Running"));
        assert_eq!(condition(&pass, "SlaPassed"), ("Unknown", "Running"));
        assert!(pass.status.finished_at.is_none());
    }

    #[test]
    fn done_and_passed_is_ready() {
        let mut pass = pass();
        pass.ended(&run(RunStatus::Done), &report(true, None));
        assert_eq!(pass.status.phase, Some(Phase::Succeeded));
        assert_eq!(condition(&pass, "Ready"), ("True", "RunCompleted"));
        assert_eq!(condition(&pass, "SlaPassed"), ("True", "SlaPassed"));
        assert!(status::find(&pass.status.conditions, "Stalled").is_none());
        assert!(pass.status.finished_at.is_some());
        let summary = pass.status.summary.as_ref().unwrap();
        assert_eq!(summary.total_records, Some(5));
        assert_eq!(summary.p99_latency_ms, Some(2.5));
    }

    #[test]
    fn a_failed_sla_stalls_with_the_first_violation() {
        let violation = Violation {
            metric: "p99".to_owned(),
            threshold: 1.0,
            actual: 2.0,
            reason: None,
        };
        let mut pass = pass();
        pass.ended(&run(RunStatus::Done), &report(false, Some(violation)));
        assert_eq!(pass.status.phase, Some(Phase::Failed));
        assert_eq!(condition(&pass, "Ready"), ("False", "SlaFailed"));
        assert_eq!(condition(&pass, "Stalled"), ("True", "SlaFailed"));
        assert_eq!(condition(&pass, "SlaPassed"), ("False", "SlaFailed"));
        assert_eq!(condition(&pass, "Synced"), ("True", "Finished"));
        let ready = status::find(&pass.status.conditions, "Ready").unwrap();
        assert_eq!(ready.message, "p99: 2 against 1");
        assert_eq!(reasons(&pass), ["SlaFailed"]);
    }

    #[test]
    fn a_failed_run_fails_with_no_verdict_too() {
        let mut pass = pass();
        let no_verdict = Report {
            summary: Value::Null,
            overall_sla_verdict: None,
        };
        pass.ended(&run(RunStatus::Failed), &no_verdict);
        assert_eq!(condition(&pass, "Stalled"), ("True", "RunFailed"));
        assert_eq!(pass.status.phase, Some(Phase::Failed));
    }

    #[test]
    fn a_retried_failure_tells_once() {
        let mut pass = pass();
        pass.unsynced("ApiUnavailable", "down");
        pass.unsynced("ApiUnavailable", "down");
        assert_eq!(reasons(&pass), ["ApiUnavailable"]);
        assert_eq!(condition(&pass, "Synced"), ("False", "ApiUnavailable"));
        assert!(pass.status.phase.is_none());
        assert!(pass.status.finished_at.is_none());
    }

    #[test]
    fn missing_connection_and_old_api_are_retried_not_stalled() {
        let mut first = pass();
        assert_eq!(
            first.no_connection("NoConnection", "m"),
            Action::requeue(NO_CONNECTION_RETRY)
        );
        let mut second = pass();
        assert_eq!(second.api_too_old(), Action::requeue(API_TOO_OLD_RETRY));
        assert_eq!(second.status.phase, Some(Phase::Pending));
        for pass in [&first, &second] {
            assert!(status::find(&pass.status.conditions, "Stalled").is_none());
            assert!(pass.status.finished_at.is_none());
        }
    }

    #[test]
    fn a_stall_writes_failed_and_finished_in_one_go() {
        let mut pass = pass();
        pass.stall("RunGone", "gone");
        assert_eq!(pass.status.phase, Some(Phase::Failed));
        assert_eq!(pass.status.finished_at.as_ref().unwrap().0, at(100));
        assert_eq!(condition(&pass, "Synced"), ("False", "RunGone"));
        assert_eq!(condition(&pass, "Stalled"), ("True", "RunGone"));
    }

    #[test]
    fn an_edited_rejection_is_open_again() {
        let mut pass = pass();
        pass.stall("SpecRejected", "bad");
        pass.reopen_if_edited();
        assert!(pass.status.finished_at.is_none());
        assert!(pass.status.phase.is_none());
        assert!(status::find(&pass.status.conditions, "Stalled").is_none());
    }

    #[test]
    fn settled_needs_the_current_generation() {
        let mut test = flat();
        assert!(!settled(&test));
        test.status = Some(KatesTestStatus {
            finished_at: Some(Time(at(1))),
            observed_generation: Some(1),
            ..KatesTestStatus::default()
        });
        assert!(settled(&test));
        test.metadata.generation = Some(2);
        assert!(!settled(&test));
    }

    #[test]
    fn the_ttl_left_counts_from_the_end_of_the_run() {
        let mut test = resource(json!({"type": "LOAD", "spec": {"numRecords": 1},
            "ttlSecondsAfterFinished": 60}));
        test.status = Some(KatesTestStatus {
            finished_at: Some(Time(at(100))),
            ..KatesTestStatus::default()
        });
        assert_eq!(ttl_left(&test, at(130)), Some(Duration::from_secs(30)));
        assert_eq!(ttl_left(&test, at(500)), Some(Duration::ZERO));
        assert_eq!(ttl_left(&flat(), at(130)), None);
    }

    #[test]
    fn a_key_never_reaches_a_message() {
        let text = ApiError::InvalidKey.to_string();
        assert!(!text.contains("Bearer"));
        assert_eq!(api_reason(&ApiError::InvalidKey), "InvalidKey");
        assert_eq!(api_reason(&ApiError::MissingScope), "MissingScope");
    }
}
