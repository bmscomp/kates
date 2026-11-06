//! Conditions, shared by both kinds.

use std::time::Duration;

use jiff::{SignedDuration, Timestamp};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::{Condition, Time};

const BACKOFF_MIN: Duration = Duration::from_secs(5);
const BACKOFF_MAX: Duration = Duration::from_mins(5);

#[must_use]
pub fn find<'a>(conditions: &'a [Condition], type_: &str) -> Option<&'a Condition> {
    conditions.iter().find(|c| c.type_ == type_)
}

#[must_use]
pub fn is_true(conditions: &[Condition], type_: &str) -> bool {
    find(conditions, type_).is_some_and(|c| c.status == "True")
}

/// Sets a condition. `lastTransitionTime` moves only when `status` does, which is what the backoff
/// reads.
#[allow(clippy::too_many_arguments)]
pub fn set(
    conditions: &mut Vec<Condition>,
    type_: &str,
    status: &str,
    reason: &str,
    message: &str,
    generation: Option<i64>,
    now: Timestamp,
) {
    let existing = conditions.iter().position(|c| c.type_ == type_);
    let last_transition_time = existing
        .map(|i| &conditions[i])
        .filter(|c| c.status == status)
        .map_or(Time(now), |c| c.last_transition_time.clone());
    let condition = Condition {
        last_transition_time,
        message: message.to_owned(),
        observed_generation: generation,
        reason: reason.to_owned(),
        status: status.to_owned(),
        type_: type_.to_owned(),
    };
    match existing {
        Some(i) => conditions[i] = condition,
        None => conditions.push(condition),
    }
}

pub fn remove(conditions: &mut Vec<Condition>, type_: &str) {
    conditions.retain(|c| c.type_ != type_);
}

/// Derived, not stored: each wait lasts as long as the failure has, which doubles the gap with no
/// counter, survives a restart and writes no status per retry.
#[must_use]
pub fn backoff(conditions: &[Condition], now: Timestamp) -> Duration {
    let failing_for = find(conditions, "Synced")
        .filter(|c| c.status == "False")
        .map_or(SignedDuration::ZERO, |c| {
            now.duration_since(c.last_transition_time.0)
        });
    Duration::try_from(failing_for)
        .unwrap_or(BACKOFF_MIN)
        .clamp(BACKOFF_MIN, BACKOFF_MAX)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn at(seconds: i64) -> Timestamp {
        Timestamp::from_second(seconds).unwrap()
    }

    #[test]
    fn the_transition_time_moves_only_with_the_status() {
        let mut conditions = Vec::new();
        set(
            &mut conditions,
            "Synced",
            "False",
            "A",
            "m",
            Some(1),
            at(10),
        );
        set(
            &mut conditions,
            "Synced",
            "False",
            "B",
            "n",
            Some(2),
            at(20),
        );
        assert_eq!(conditions[0].last_transition_time.0, at(10));
        assert_eq!(conditions[0].reason, "B");
        set(&mut conditions, "Synced", "True", "C", "", Some(2), at(30));
        assert_eq!(conditions[0].last_transition_time.0, at(30));
        assert_eq!(conditions.len(), 1);
    }

    #[test]
    fn backoff_follows_the_failure_between_its_bounds() {
        let mut conditions = Vec::new();
        assert_eq!(backoff(&conditions, at(100)), BACKOFF_MIN);
        set(&mut conditions, "Synced", "False", "A", "m", None, at(100));
        assert_eq!(backoff(&conditions, at(101)), BACKOFF_MIN);
        assert_eq!(backoff(&conditions, at(160)), Duration::from_mins(1));
        assert_eq!(backoff(&conditions, at(9000)), BACKOFF_MAX);
    }
}
