//! The members' quorum of a `Kates` with replicas 3 or 5 (O4 of plans/operator-kates-kind.md).
//!
//! The operator keeps a CA for the quorum and a certificate for each member, so the members reach
//! each other over mutual TLS. Its chart lets it read and change only two Secrets, by name, so
//! there is one replicated `Kates` per namespace. It also finds the leader from each member's
//! public `/api/health`, labels its pod for the Service, and picks which member to replace next.

use std::collections::BTreeMap;

use jiff::{SignedDuration, Timestamp};
use k8s_openapi::ByteString;
use k8s_openapi::api::core::v1::{PersistentVolumeClaim, Pod, Secret};
use kates_client::QuorumView;
use kates_crds::Kates;
use kube::api::{Api, Patch, PatchParams};
use kube::{Resource as _, ResourceExt as _};
use rcgen::{
    BasicConstraints, CertificateParams, DnType, ExtendedKeyUsagePurpose, IsCa, Issuer, KeyPair,
    KeyUsagePurpose,
};
use serde_json::json;
use time::OffsetDateTime;

use super::Error;
use crate::context::Context;

/// The quorum's CA, key included. Never mounted into a pod.
pub const CA_SECRET: &str = "kates-quorum-ca";
/// Each member's certificate and key, and the CA certificate: mounted into every member.
pub const TLS_SECRET: &str = "kates-quorum-tls";
pub const PORT: i32 = 9300;
pub const MOUNT: &str = "/etc/kates/quorum";
pub const DIR: &str = "/var/lib/kates/quorum";
/// The Service sends traffic to the pod with this label.
pub const ROLE: &str = "kates.io/quorum-role";
pub const LEADER: &str = "leader";
const OWNER: &str = "kates.io/owner";
const ISSUED: &str = "kates.io/issued.";
const CA_LIFE: SignedDuration = SignedDuration::from_hours(24 * 3650);
const MEMBER_LIFE: SignedDuration = SignedDuration::from_hours(24 * 365);
/// Re-issued at two thirds of its life, so a failing renewal has months to be noticed.
const RENEW_AFTER: SignedDuration = SignedDuration::from_hours(24 * 243);
/// How long a member waits for its volume's node before it gets a new volume: past a node's
/// restart, so a member is moved only when its node is gone.
pub const LOST_AFTER: SignedDuration = SignedDuration::from_mins(2);
/// The claim the `StatefulSet` makes for a member's term and vote.
const CLAIM: &str = "quorum";

#[must_use]
pub fn members(name: &str, replicas: i32) -> Vec<String> {
    (0..replicas).map(|i| format!("{name}-{i}")).collect()
}

/// A member's name in DNS and in its certificate: its own Service, whose address outlives the
/// pod's. Through the headless Service, a peer that had looked up a pod went on dialling its old
/// address for up to 30 s after it restarted, the JVM's and gRPC's caches.
#[must_use]
pub fn host(namespace: &str, pod: &str) -> String {
    format!("{pod}.{namespace}.svc")
}

/// `kates.quorum.peers`: every member, its own included.
#[must_use]
pub fn peers(name: &str, namespace: &str, replicas: i32) -> String {
    members(name, replicas)
        .iter()
        .map(|pod| format!("{pod}={}:{PORT}", host(namespace, pod)))
        .collect::<Vec<_>>()
        .join(",")
}

/// The CA certificate and key, PEM.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Ca {
    pub cert: String,
    pub key: String,
}

// The same name every time: an issuer is rebuilt from it and the stored key, so a member's
// certificate names the CA that signed it without the CA certificate being parsed.
fn ca_params(owner: &str) -> Result<CertificateParams, rcgen::Error> {
    let mut params = CertificateParams::new(Vec::<String>::new())?;
    params
        .distinguished_name
        .push(DnType::CommonName, format!("kates quorum CA {owner}"));
    params.is_ca = IsCa::Ca(BasicConstraints::Constrained(0));
    params.key_usages = vec![KeyUsagePurpose::KeyCertSign, KeyUsagePurpose::CrlSign];
    Ok(params)
}

fn instant(at: Timestamp) -> OffsetDateTime {
    OffsetDateTime::from_unix_timestamp(at.as_second()).unwrap_or(OffsetDateTime::UNIX_EPOCH)
}

/// A new CA for the quorum of `owner` (namespace/name), valid ten years from `now`.
///
/// # Errors
/// A key or certificate rcgen could not make.
pub fn new_ca(owner: &str, now: Timestamp) -> Result<Ca, rcgen::Error> {
    let key = KeyPair::generate()?;
    let mut params = ca_params(owner)?;
    params.not_before = instant(now - SignedDuration::from_mins(5));
    params.not_after = instant(now + CA_LIFE);
    let cert = params.self_signed(&key)?;
    Ok(Ca {
        cert: cert.pem(),
        key: key.serialize_pem(),
    })
}

/// A member's certificate and key (PKCS#8, as the backend reads it), for `host`, valid a year.
///
/// # Errors
/// A CA key that does not parse, or a certificate rcgen could not make.
pub fn issue(
    ca: &Ca,
    owner: &str,
    pod: &str,
    host: &str,
    now: Timestamp,
) -> Result<(String, String), rcgen::Error> {
    let issuer = Issuer::new(ca_params(owner)?, KeyPair::from_pem(&ca.key)?);
    let key = KeyPair::generate()?;
    let mut params = CertificateParams::new(vec![host.to_owned()])?;
    params.distinguished_name.push(DnType::CommonName, pod);
    params.key_usages = vec![
        KeyUsagePurpose::DigitalSignature,
        KeyUsagePurpose::KeyEncipherment,
    ];
    params.extended_key_usages = vec![
        ExtendedKeyUsagePurpose::ServerAuth,
        ExtendedKeyUsagePurpose::ClientAuth,
    ];
    params.not_before = instant(now - SignedDuration::from_mins(5));
    params.not_after = instant(now + MEMBER_LIFE);
    let cert = params.signed_by(&key, &issuer)?;
    Ok((cert.pem(), key.serialize_pem()))
}

/// Whether a pod started before its current certificate was issued, so it still runs the old one.
/// Both in whole seconds: a pod made in the second its certificate was issued has it.
#[must_use]
pub fn renewed(issued: Option<Timestamp>, started: Option<Timestamp>) -> bool {
    issued
        .zip(started)
        .is_some_and(|(issued, started)| issued.as_second() > started.as_second())
}

/// Whether a member's certificate, issued at `issued`, is due again.
#[must_use]
pub fn due(issued: Option<Timestamp>, now: Timestamp) -> bool {
    issued.is_none_or(|at| now.duration_since(at) >= RENEW_AFTER)
}

/// When each member's certificate was issued, read from the Secret's annotations.
#[must_use]
pub fn issued_at(secret: Option<&Secret>) -> BTreeMap<String, Timestamp> {
    secret
        .map(kube::ResourceExt::annotations)
        .into_iter()
        .flatten()
        .filter_map(|(key, value)| {
            let pod = key.strip_prefix(ISSUED)?;
            Some((pod.to_owned(), value.parse().ok()?))
        })
        .collect()
}

fn text(secret: &Secret, key: &str) -> Option<String> {
    let bytes = secret.data.as_ref()?.get(key)?;
    String::from_utf8(bytes.0.clone()).ok()
}

/// Makes or renews the CA and every member's certificate. Answers when each was issued, or why
/// the Secrets are another resource's.
///
/// # Errors
/// A failed read or write, or a certificate that could not be made.
pub async fn certificates(
    kates: &Kates,
    ctx: &Context,
    namespace: &str,
    params: &PatchParams,
    now: Timestamp,
) -> Result<Result<BTreeMap<String, Timestamp>, String>, Error> {
    let name = kates.name_any();
    let owner = format!("{namespace}/{name}");
    // Whole seconds, as a pod's creationTimestamp: a pod made in the same second as its
    // certificate must not look older than it, and be replaced for it.
    let now = Timestamp::from_second(now.as_second()).unwrap_or(now);
    let secrets = Api::<Secret>::namespaced(ctx.client.clone(), namespace);
    let taken = |secret: &Secret| {
        let by = secret.annotations().get(OWNER).cloned().unwrap_or_default();
        (by != name).then(|| {
            format!(
                "Secret {} belongs to Kates {by:?}: a namespace holds one Kates with a quorum",
                secret.name_any()
            )
        })
    };
    let ca = match secrets.get_opt(CA_SECRET).await? {
        Some(secret) => {
            if let Some(message) = taken(&secret) {
                return Ok(Err(message));
            }
            let read = text(&secret, "ca.crt").zip(text(&secret, "ca.key"));
            read.map(|(cert, key)| Ca { cert, key })
        }
        None => None,
    };
    let ca = if let Some(ca) = ca {
        ca
    } else {
        let ca = new_ca(&owner, now)?;
        let data = [("ca.crt", &ca.cert), ("ca.key", &ca.key)];
        let applied = secret(kates, CA_SECRET, &data, &[]);
        secrets
            .patch(CA_SECRET, params, &Patch::Apply(applied))
            .await?;
        ca
    };

    let current = secrets.get_opt(TLS_SECRET).await?;
    if let Some(message) = current.as_ref().and_then(taken) {
        return Ok(Err(message));
    }
    let mut issued = issued_at(current.as_ref());
    let same_ca = current.as_ref().and_then(|s| text(s, "ca.crt")).as_ref() == Some(&ca.cert);
    let mut data: BTreeMap<String, String> = BTreeMap::new();
    data.insert("ca.crt".to_owned(), ca.cert.clone());
    let mut changed = !same_ca;
    for pod in members(&name, kates.spec.replicas) {
        let (crt, key) = (format!("{pod}.crt"), format!("{pod}.key"));
        let kept = current
            .as_ref()
            .and_then(|s| text(s, &crt).zip(text(s, &key)))
            .filter(|_| same_ca && !due(issued.get(&pod).copied(), now));
        let (pem, private) = if let Some(kept) = kept {
            kept
        } else {
            changed = true;
            issued.insert(pod.clone(), now);
            issue(&ca, &owner, &pod, &host(namespace, &pod), now)?
        };
        data.insert(crt, pem);
        data.insert(key, private);
    }
    if changed {
        let data: Vec<(&str, &String)> = data.iter().map(|(k, v)| (k.as_str(), v)).collect();
        let notes: Vec<(String, String)> = issued
            .iter()
            .map(|(pod, at)| (format!("{ISSUED}{pod}"), at.to_string()))
            .collect();
        secrets
            .patch(
                TLS_SECRET,
                params,
                &Patch::Apply(secret(kates, TLS_SECRET, &data, &notes)),
            )
            .await?;
    }
    Ok(Ok(issued))
}

fn secret(
    kates: &Kates,
    name: &str,
    data: &[(&str, &String)],
    notes: &[(String, String)],
) -> Secret {
    let mut annotations: BTreeMap<String, String> = notes.iter().cloned().collect();
    annotations.insert(OWNER.to_owned(), kates.name_any());
    let mut secret: Secret = serde_json::from_value(json!({
        "apiVersion": "v1",
        "kind": "Secret",
        "type": "Opaque",
        "metadata": {
            "name": name,
            "labels": {
                "app.kubernetes.io/name": "kates",
                "app.kubernetes.io/instance": kates.name_any(),
                "app.kubernetes.io/managed-by": "kates-operator",
            },
            "ownerReferences": [kates.controller_owner_ref(&())],
        },
    }))
    .unwrap_or_default();
    secret.metadata.annotations = Some(annotations);
    secret.data = Some(
        data.iter()
            .map(|(key, value)| ((*key).to_owned(), ByteString(value.as_bytes().to_vec())))
            .collect(),
    );
    secret
}

/// One member as the operator sees it this pass.
#[derive(Clone, Debug, PartialEq)]
pub struct Member {
    pub pod: String,
    pub ready: bool,
    /// It runs an old revision, or started before its current certificate was issued.
    pub stale: bool,
    pub view: Option<QuorumView>,
    /// Since when its pod waits for the one node its volume can be on, which is gone or refuses it.
    pub lost_since: Option<Timestamp>,
    /// Its pod waits, and its claim is gone or going: the pod is deleted so the `StatefulSet`
    /// makes both again.
    pub orphaned: bool,
}

/// The claim that holds a member's term and vote.
#[must_use]
pub fn claim(pod: &str) -> String {
    format!("{CLAIM}-{pod}")
}

/// The member that says it leads, in the highest term any member reports.
#[must_use]
pub fn leader(members: &[Member]) -> Option<&str> {
    members
        .iter()
        .filter(|member| member.view.as_ref().is_some_and(|view| view.leader))
        .max_by_key(|member| member.view.as_ref().map_or(0, |view| view.term))
        .map(|member| member.pod.as_str())
}

/// What the roll does next.
#[derive(Debug, PartialEq, Eq)]
pub enum Next {
    /// Nothing is stale.
    Done,
    /// Replacing anything now could cost the quorum its majority: wait, for this reason.
    Wait(String),
    Replace(String),
}

/// Followers first, one at a time, and the leader last. A member is replaced only while every
/// member is ready and knows the leader, and has applied an entry: one with an empty log is no
/// voter, so replacing another then could leave no majority.
#[must_use]
pub fn next(members: &[Member]) -> Next {
    if members.iter().all(|member| !member.stale) {
        return Next::Done;
    }
    let Some(leader) = leader(members) else {
        return Next::Wait("the quorum has no leader".to_owned());
    };
    let unsettled = members.iter().find(|member| {
        let caught_up = member.view.as_ref().is_some_and(|view| {
            view.leader_id.as_deref() == Some(leader) && view.applied_index >= 0
        });
        !member.ready || !caught_up
    });
    if let Some(member) = unsettled {
        return Next::Wait(format!("{} is not ready or has not caught up", member.pod));
    }
    let stale = |pod: &Member| pod.stale && pod.pod != leader;
    members
        .iter()
        .find(|member| stale(member))
        .or_else(|| members.iter().find(|member| member.stale))
        .map_or(Next::Done, |member| Next::Replace(member.pod.clone()))
}

/// A member whose volume's node has been gone for [`LOST_AFTER`] gets a new volume, one at a time
/// and only while every other member that is not lost too is ready and follows a leader. Its log
/// is then empty, so it is no voter until it catches up: the quorum keeps its majority only if
/// the others have one without it. With no leader, nothing is replaced.
#[must_use]
pub fn lost(members: &[Member], now: Timestamp) -> Next {
    let Some(member) = members.iter().find(|member| {
        member
            .lost_since
            .is_some_and(|since| now.duration_since(since) >= LOST_AFTER)
    }) else {
        return Next::Done;
    };
    let Some(leader) = leader(members) else {
        return Next::Wait(format!(
            "{}'s volume is on a node that is gone, and the quorum has no leader to replace it under",
            member.pod
        ));
    };
    let unsettled = members.iter().find(|other| {
        let caught_up = other.view.as_ref().is_some_and(|view| {
            view.leader_id.as_deref() == Some(leader) && view.applied_index >= 0
        });
        other.lost_since.is_none() && (!other.ready || !caught_up)
    });
    if let Some(other) = unsettled {
        return Next::Wait(format!(
            "{}'s volume is on a node that is gone; it is replaced once {} is ready and caught up",
            member.pod, other.pod
        ));
    }
    Next::Replace(member.pod.clone())
}

/// The scheduler found no node for the pod because its volume can only be on another.
fn volume_elsewhere(message: &str) -> bool {
    message.contains("didn't match PersistentVolume's node affinity")
        || message.contains("volume node affinity conflict")
}

/// When the scheduler last found no node for this pending pod because of its volume.
fn lost_since(pod: &Pod) -> Option<Timestamp> {
    let status = pod.status.as_ref()?;
    if status.phase.as_deref() != Some("Pending") {
        return None;
    }
    // shortcut: matched on the scheduler's VolumeBinding reason text, in its wording before and
    // since Kubernetes 1.26; read the volume's node instead if the operator ever reads nodes.
    status.conditions.iter().flatten().find_map(|c| {
        (c.type_ == "PodScheduled"
            && c.status == "False"
            && c.message.as_deref().is_some_and(volume_elsewhere))
        .then_some(c.last_transition_time.as_ref())
        .flatten()
        .and_then(|at| at.0.to_string().parse().ok())
    })
}

/// Reads each member: its pod and its public `/api/health`.
///
/// # Errors
/// A failed read of a pod.
pub async fn observe(
    kates: &Kates,
    ctx: &Context,
    namespace: &str,
    update_revision: Option<&str>,
    issued: &BTreeMap<String, Timestamp>,
) -> Result<(Vec<Member>, BTreeMap<String, Pod>), Error> {
    let name = kates.name_any();
    let pods = Api::<Pod>::namespaced(ctx.client.clone(), namespace);
    let claims = Api::<PersistentVolumeClaim>::namespaced(ctx.client.clone(), namespace);
    let mut members = Vec::new();
    let mut found = BTreeMap::new();
    for pod_name in self::members(&name, kates.spec.replicas) {
        let Some(pod) = pods.get_opt(&pod_name).await? else {
            members.push(Member {
                pod: pod_name,
                ready: false,
                stale: false,
                view: None,
                lost_since: None,
                orphaned: false,
            });
            continue;
        };
        // A pod being deleted still says Ready, and answers, until it stops: counted as ready,
        // the next pass would replace another member before this one came back.
        let ready = pod.metadata.deletion_timestamp.is_none()
            && pod.status.as_ref().is_some_and(|status| {
                status
                    .conditions
                    .iter()
                    .flatten()
                    .any(|c| c.type_ == "Ready" && c.status == "True")
            });
        let started: Option<Timestamp> = pod
            .metadata
            .creation_timestamp
            .as_ref()
            .and_then(|at| at.0.to_string().parse().ok());
        let renewed = renewed(issued.get(&pod_name).copied(), started);
        let stale = update_revision.is_some_and(|revision| super::kates::stale(&pod, revision))
            || (renewed && pod.metadata.deletion_timestamp.is_none());
        let url = format!("http://{}:8080", host(namespace, &pod_name));
        let view = match ctx.connections.client_for(&url) {
            Ok(client) => client.quorum().await.ok(),
            Err(_) => None,
        };
        let pending = pod
            .status
            .as_ref()
            .is_some_and(|status| status.phase.as_deref() == Some("Pending"));
        let orphaned = pending
            && pod.metadata.deletion_timestamp.is_none()
            && claims
                .get_opt(&claim(&pod_name))
                .await?
                .is_none_or(|claim| claim.metadata.deletion_timestamp.is_some());
        members.push(Member {
            pod: pod_name.clone(),
            ready,
            stale,
            view,
            lost_since: lost_since(&pod),
            orphaned,
        });
        found.insert(pod_name, pod);
    }
    Ok((members, found))
}

/// Puts the leader label on the leader's pod and takes it off every other.
///
/// # Errors
/// A failed patch.
pub async fn label(
    ctx: &Context,
    namespace: &str,
    pods: &BTreeMap<String, Pod>,
    leader: Option<&str>,
) -> Result<(), Error> {
    let api = Api::<Pod>::namespaced(ctx.client.clone(), namespace);
    for (name, pod) in pods {
        let has = pod.labels().get(ROLE).map(String::as_str) == Some(LEADER);
        let wants = Some(name.as_str()) == leader;
        if has != wants {
            let value = if wants { json!(LEADER) } else { json!(null) };
            let patch = json!({"metadata": {"labels": {ROLE: value}}});
            api.patch(name, &PatchParams::default(), &Patch::Merge(&patch))
                .await?;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn member(pod: &str, stale: bool, leader_id: &str, leads: bool) -> Member {
        Member {
            pod: pod.to_owned(),
            ready: true,
            stale,
            view: Some(QuorumView {
                enabled: true,
                member_id: Some(pod.to_owned()),
                leader: leads,
                leader_id: Some(leader_id.to_owned()),
                term: 2,
                applied_index: 4,
            }),
            lost_since: None,
            orphaned: false,
        }
    }

    fn gone(pod: &str, at: Timestamp) -> Member {
        Member {
            ready: false,
            view: None,
            lost_since: Some(at),
            ..member(pod, false, "", false)
        }
    }

    #[test]
    fn a_member_whose_node_is_gone_is_replaced_only_under_a_settled_leader() {
        let now: Timestamp = "2026-11-15T12:00:00Z".parse().unwrap();
        let long_ago = now - LOST_AFTER;
        let healthy = [
            member("main-0", false, "main-0", true),
            member("main-1", false, "main-0", false),
        ];
        let with = |lost: Vec<Member>| [healthy.to_vec(), lost].concat();

        assert_eq!(lost(&healthy, now), Next::Done);
        // Not yet: its node may only be restarting.
        assert_eq!(lost(&with(vec![gone("main-2", now)]), now), Next::Done);
        assert_eq!(
            lost(&with(vec![gone("main-2", long_ago)]), now),
            Next::Replace("main-2".to_owned())
        );
        // Two of five lost: one at a time, each other lost member left out of the check.
        let five = with(vec![
            member("main-2", false, "main-0", false),
            gone("main-3", long_ago),
            gone("main-4", long_ago),
        ]);
        assert_eq!(lost(&five, now), Next::Replace("main-3".to_owned()));
        // Another member is catching up: wait.
        let mut catching_up = with(vec![gone("main-2", long_ago)]);
        catching_up[1].view.as_mut().unwrap().applied_index = -1;
        assert!(matches!(lost(&catching_up, now), Next::Wait(why) if why.contains("main-1")));
        // Two of three lost: no leader, nothing done.
        let quorum_lost = [
            member("main-0", false, "", false),
            gone("main-1", long_ago),
            gone("main-2", long_ago),
        ];
        assert!(matches!(lost(&quorum_lost, now), Next::Wait(why) if why.contains("no leader")));
    }

    #[test]
    fn a_volume_on_another_node_is_read_from_the_schedulers_message_old_and_new() {
        assert!(volume_elsewhere(
            "0/3 nodes are available: 1 node(s) had untolerated taint(s), 2 node(s) didn't match PersistentVolume's node affinity."
        ));
        assert!(volume_elsewhere(
            "0/2 nodes are available: 2 node(s) had volume node affinity conflict."
        ));
        assert!(!volume_elsewhere(
            "0/3 nodes are available: 3 Insufficient cpu."
        ));
    }

    #[test]
    fn peers_name_every_member_by_its_dns_name() {
        assert_eq!(
            peers("main", "kates", 3),
            "main-0=main-0.kates.svc:9300,main-1=main-1.kates.svc:9300,main-2=main-2.kates.svc:9300"
        );
    }

    #[test]
    fn a_member_certificate_is_signed_by_the_stored_ca() {
        let now: Timestamp = "2026-11-15T00:00:00Z".parse().unwrap();
        let ca = new_ca("kates/main", now).unwrap();
        let (cert, key) = issue(&ca, "kates/main", "main-0", "main-0.kates.svc", now).unwrap();
        assert!(cert.starts_with("-----BEGIN CERTIFICATE-----"));
        assert!(
            key.starts_with("-----BEGIN PRIVATE KEY-----"),
            "PKCS#8, as the backend reads it"
        );
        // Rebuilt from the stored key alone, the issuer signs again: no CA certificate parsing.
        assert!(issue(&ca, "kates/main", "main-1", "main-1.kates.svc", now).is_ok());
    }

    #[test]
    fn a_certificate_is_renewed_at_two_thirds_of_its_year() {
        let now: Timestamp = "2026-11-15T00:00:00Z".parse().unwrap();
        assert!(due(None, now));
        assert!(!due(Some(now - SignedDuration::from_hours(24 * 200)), now));
        assert!(due(Some(now - SignedDuration::from_hours(24 * 243)), now));
    }

    #[test]
    fn the_roll_replaces_followers_first_and_the_leader_last() {
        let all = |stale: [bool; 3]| {
            vec![
                member("main-0", stale[0], "main-0", true),
                member("main-1", stale[1], "main-0", false),
                member("main-2", stale[2], "main-0", false),
            ]
        };
        assert_eq!(next(&all([false; 3])), Next::Done);
        assert_eq!(next(&all([true; 3])), Next::Replace("main-1".to_owned()));
        assert_eq!(
            next(&all([true, false, true])),
            Next::Replace("main-2".to_owned())
        );
        assert_eq!(
            next(&all([true, false, false])),
            Next::Replace("main-0".to_owned())
        );
    }

    #[test]
    fn the_roll_waits_for_every_member_to_be_ready_and_caught_up() {
        let mut members = vec![
            member("main-0", true, "main-0", true),
            member("main-1", false, "main-0", false),
            member("main-2", false, "main-0", false),
        ];
        members[2].ready = false;
        assert!(matches!(next(&members), Next::Wait(_)));
        members[2].ready = true;
        members[2].view.as_mut().unwrap().applied_index = -1;
        assert!(
            matches!(next(&members), Next::Wait(why) if why.contains("main-2")),
            "no voter yet"
        );
        members[2].view.as_mut().unwrap().applied_index = 0;
        assert_eq!(next(&members), Next::Replace("main-0".to_owned()));
        for member in &mut members {
            member.view.as_mut().unwrap().leader = false;
        }
        assert!(matches!(next(&members), Next::Wait(why) if why.contains("no leader")));
    }

    #[test]
    fn a_pod_made_in_the_second_of_its_certificate_has_it() {
        let issued: Timestamp = "2026-11-15T04:07:51.600Z".parse().unwrap();
        let started: Timestamp = "2026-11-15T04:07:51Z".parse().unwrap();
        assert!(
            !renewed(Some(issued), Some(started)),
            "creationTimestamp has whole seconds"
        );
        assert!(renewed(
            Some(issued + SignedDuration::from_secs(1)),
            Some(started)
        ));
        assert!(!renewed(None, Some(started)));
    }

    #[test]
    fn issued_times_are_read_from_annotations() {
        let mut secret = Secret::default();
        secret.metadata.annotations = Some(BTreeMap::from([
            (format!("{ISSUED}main-0"), "2026-11-15T00:00:00Z".to_owned()),
            (OWNER.to_owned(), "main".to_owned()),
        ]));
        let issued = issued_at(Some(&secret));
        assert_eq!(issued.len(), 1);
        assert!(issued.contains_key("main-0"));
    }
}
