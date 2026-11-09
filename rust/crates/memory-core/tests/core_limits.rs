//! Boundary and property tests for limits and disposition.
use hegel::{TestCase, generators as gs};
use memory_core::disposition::*;
use memory_core::domain::*;
use memory_core::limits::*;
use proptest::prelude::*;
use std::time::Duration;

type R = Result<(), Box<dyn std::error::Error>>;
const S: fn(u64) -> Duration = Duration::from_secs;

fn id(n: usize) -> String {
    "a".repeat(n)
}
fn owner_session(
    archive: &str,
    machine: &str,
    session: &str,
    agent: Agent,
) -> Result<Owner, ValidationError> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from(archive.to_string())?,
        machine: MachineId::try_from(machine.to_string())?,
        agent,
        session: SessionId::try_from(session.to_string())?,
    }))
}
fn owner_shared(archive: &str, machine: &str, agent: Agent) -> Result<Owner, ValidationError> {
    Ok(Owner::Shared {
        archive: ArchiveId::try_from(archive.to_string())?,
        machine: MachineId::try_from(machine.to_string())?,
        agent,
    })
}
fn sid(n: usize) -> Result<SourceId, ValidationError> {
    SourceId::try_from(format!("s{n}"))
}
// Fixed overhead for session owner with agent "claude":
// "sessions/" 9 + "/"x3 + "claude" 6 + "/current.json" 13 = 31 (+ archive+machine+session)
const SESSION_FIXED_CLAUDE: usize = 9 + 3 + 6 + 13;
// shared with "pi": "shared/" 7 + "/"x2 + "pi" 2 + "/current.json" 13 = 24
const SHARED_FIXED_PI: usize = 7 + 2 + 2 + 13;

#[test]
fn key_exact_spelling() -> R {
    let o = owner_session("a", "m", "s", Agent::Claude)?;
    assert_eq!(current_key(&o), "sessions/a/m/claude/s/current.json");
    let o = owner_session("a", "m", "s", Agent::Codex)?;
    assert_eq!(current_key(&o), "sessions/a/m/codex/s/current.json");
    let o = owner_shared("a", "m", Agent::Pi)?;
    assert_eq!(current_key(&o), "shared/a/m/pi/current.json");
    Ok(())
}

#[test]
fn key_boundary_session_1024_ok_1025_denied() -> R {
    let rest = 1024 - SESSION_FIXED_CLAUDE;
    let o = owner_session("a", "m", &id(rest - 2), Agent::Claude)?;
    assert_eq!(current_key(&o).len(), 1024);
    assert_eq!(check_owner_key(&o), Ok(()));
    let o = owner_session("a", "m", &id(rest - 1), Agent::Claude)?;
    assert_eq!(current_key(&o).len(), 1025);
    assert_eq!(check_owner_key(&o), Err(WithholdReason::KeyTooLong));
    Ok(())
}

#[test]
fn key_boundary_shared() -> R {
    let rest = 1024 - SHARED_FIXED_PI;
    let o = owner_shared("a", &id(rest - 1), Agent::Pi)?;
    assert_eq!(current_key(&o).len(), 1024);
    assert_eq!(check_owner_key(&o), Ok(()));
    let o = owner_shared("aa", &id(rest - 1), Agent::Pi)?;
    assert_eq!(current_key(&o).len(), 1025);
    assert_eq!(check_owner_key(&o), Err(WithholdReason::KeyTooLong));
    Ok(())
}

#[test]
fn key_len_measures_utf8_bytes_not_chars() {
    // 2-byte, 3-byte and 4-byte characters: 1 char each but more bytes.
    assert_eq!(
        current_key_len("é", "m", "pi", None),
        SHARED_FIXED_PI + 2 + 1
    );
    assert_eq!(
        current_key_len("€", "m", "pi", None),
        SHARED_FIXED_PI + 3 + 1
    );
    assert_eq!(
        current_key_len("😀", "m", "pi", None),
        SHARED_FIXED_PI + 4 + 1
    );
    // 400 snowmen = 1200 bytes but only 400 chars: denied.
    let wide = "€".repeat(400);
    let len = current_key_len("a", &wide, "pi", None);
    assert_eq!(len, SHARED_FIXED_PI + 1 + 1200);
    assert_eq!(check_key_len(len), Err(WithholdReason::KeyTooLong));
    // 2-byte chars exactly hitting the boundary.
    let rest = 1024 - SHARED_FIXED_PI - 1;
    let m = "é".repeat(rest / 2) + &"a".repeat(rest % 2);
    assert_eq!(check_key_len(current_key_len("a", &m, "pi", None)), Ok(()));
    let m = m + "a";
    assert_eq!(
        check_key_len(current_key_len("a", &m, "pi", None)),
        Err(WithholdReason::KeyTooLong)
    );
    assert_eq!(
        current_key_len("a", "m", "claude", Some("s")),
        "sessions/a/m/claude/s/current.json".len()
    );
    assert_eq!(
        current_key_len(&id(10), "", "", None),
        usize::from(7_u8) + 2 + 13 + 10
    );
}

#[test]
fn key_len_saturates() {
    assert_eq!(check_key_len(usize::MAX), Err(WithholdReason::KeyTooLong));
    assert_eq!(check_key_len(0), Ok(()));
    assert_eq!(check_key_len(1024), Ok(()));
    assert_eq!(check_key_len(1025), Err(WithholdReason::KeyTooLong));
}

#[test]
fn agent_wire_matches_serde() -> R {
    for a in [Agent::Claude, Agent::Codex, Agent::Pi] {
        let json = serde_json::to_string(&a)?;
        assert_eq!(json, format!("\"{}\"", agent_wire(&a)));
    }
    Ok(())
}

#[test]
fn sources_cap_and_never_evict() -> R {
    let mut set = SourceSet::new();
    assert!(set.is_empty());
    for n in 0..1024 {
        assert_eq!(set.admit(&sid(n)?), Ok(()));
    }
    assert_eq!(set.len(), 1024);
    assert!(!set.is_empty());
    // Known source stays admitted at the cap, repeatedly (rewrites/epochs).
    for _ in 0..5 {
        assert_eq!(set.admit(&sid(0)?), Ok(()));
        assert_eq!(set.admit(&sid(1023)?), Ok(()));
    }
    assert_eq!(set.len(), 1024);
    // 1,025th distinct source is limited, repeatedly, and does not enter.
    for _ in 0..3 {
        assert_eq!(set.admit(&sid(1024)?), Err(WithholdReason::SourceLimit));
    }
    assert!(!set.contains(&sid(1024)?));
    assert_eq!(set.len(), 1024);
    for n in 0..1024 {
        assert!(set.contains(&sid(n)?));
    }
    Ok(())
}

#[test]
fn rewrites_of_one_source_use_one_slot() -> R {
    let mut set = SourceSet::new();
    for _ in 0..5000 {
        assert_eq!(set.admit(&sid(7)?), Ok(()));
    }
    assert_eq!(set.len(), 1);
    Ok(())
}

#[test]
fn budget_boundaries() {
    assert_eq!(request_budget(0), S(60));
    assert_eq!(request_budget(1), S(60));
    let mib = 1024 * 1024;
    assert_eq!(request_budget(4 * mib), S(60)); // median-ish piece
    assert_eq!(request_budget(8 * mib), S(60)); // DESIGN: 8 MiB cap is 60 s
    assert_eq!(request_budget(30 * mib), S(60));
    assert_eq!(request_budget(31 * mib), S(61));
    assert_eq!(request_budget(103 * mib), S(133));
    // fractional: 30 s + 30.5 MiB = 60.5 s
    assert_eq!(
        request_budget(30 * mib + mib / 2),
        Duration::from_millis(60_500)
    );
    assert_eq!(
        request_budget(30 * mib + 1),
        S(60) + Duration::from_nanos(953)
    );
    // u64::MAX does not panic and is huge but finite.
    let big = request_budget(u64::MAX);
    assert_eq!(
        big,
        S(30 + u64::MAX / mib) + Duration::from_nanos((u64::MAX % mib) * 1_000_000_000 / mib)
    );
    assert!(big > S(1 << 43));
}

fn det(f: Fault) -> Outcome {
    Outcome::Failed(ErrorClass::Deterministic(f))
}
const H: u64 = 3600;

#[test]
fn cancelled_keeps_streak_and_never_parks() {
    let prior = Streak {
        deterministic: 2,
        fault: Some(Fault::AuditMismatch),
        first_at: Some(S(0)),
        retries: 4,
    };
    let (d, next) = transition(&Outcome::Aborted, true, S(10 * H), &prior);
    assert_eq!(d, Disposition::Cancelled);
    assert_eq!(next, prior);
    // Restart: eligible again, fresh default state still retries.
    let (d, _) = transition(
        &Outcome::Failed(ErrorClass::Offline),
        false,
        S(0),
        &Streak::default(),
    );
    assert!(matches!(d, Disposition::Retry { .. }));
    // Even without the flag, an aborted attempt is Cancelled.
    assert_eq!(
        transition(&Outcome::Aborted, false, S(0), &prior).0,
        Disposition::Cancelled
    );
}

#[test]
fn cancel_swallows_only_retryable() {
    let prior = Streak::default();
    for o in [
        Outcome::TimedOut,
        Outcome::Failed(ErrorClass::Offline),
        Outcome::Failed(ErrorClass::Throttled),
        Outcome::Failed(ErrorClass::Local(LocalError::Io)),
    ] {
        let (d, n) = transition(&o, true, S(1), &prior);
        assert_eq!(d, Disposition::Cancelled);
        assert_eq!(n, prior);
    }
    // Fatal/integrity failures survive a simultaneous cancel.
    let (d, _) = transition(
        &Outcome::OwnerFault(OwnerParkReason::NewerRemoteSchema),
        true,
        S(1),
        &prior,
    );
    assert_eq!(
        d,
        Disposition::ParkOwner {
            reason: OwnerParkReason::NewerRemoteSchema
        }
    );
    let (d, n) = transition(&det(Fault::ReadbackMismatch), true, S(1), &prior);
    assert!(matches!(d, Disposition::Retry { .. }));
    assert_eq!(n.deterministic, 1);
    let (d, _) = transition(
        &Outcome::Failed(ErrorClass::DestinationAuth),
        true,
        S(1),
        &prior,
    );
    assert_eq!(
        d,
        Disposition::PauseHost {
            class: ErrorClass::DestinationAuth
        }
    );
    assert_eq!(
        transition(&Outcome::Succeeded, true, S(1), &prior).0,
        Disposition::Done
    );
}

#[test]
fn timeout_and_offline_retry_not_cancelled() {
    let (d, n) = transition(&Outcome::TimedOut, false, S(1), &Streak::default());
    assert_eq!(
        d,
        Disposition::Retry {
            after: S(1),
            class: ErrorClass::Offline
        }
    );
    assert_eq!(n.retries, 1);
    assert_eq!(n.deterministic, 0);
    let (d, _) = transition(&Outcome::Failed(ErrorClass::Throttled), false, S(1), &n);
    assert_eq!(
        d,
        Disposition::Retry {
            after: S(2),
            class: ErrorClass::Throttled
        }
    );
    let (d, _) = transition(
        &Outcome::Failed(ErrorClass::Local(LocalError::DiskFull)),
        false,
        S(1),
        &Streak::default(),
    );
    assert_eq!(
        d,
        Disposition::Retry {
            after: S(1),
            class: ErrorClass::Local(LocalError::DiskFull)
        }
    );
}

#[test]
fn backoff_bounds() {
    assert_eq!(backoff(0), S(1));
    assert_eq!(backoff(1), S(2));
    assert_eq!(backoff(9), S(512));
    assert_eq!(backoff(10), S(900));
    assert_eq!(backoff(u32::MAX), S(900));
}

#[test]
fn auth_pauses_host_without_touching_streak() {
    let prior = Streak {
        deterministic: 1,
        fault: Some(Fault::Schema),
        first_at: Some(S(5)),
        retries: 2,
    };
    let (d, n) = transition(
        &Outcome::Failed(ErrorClass::DestinationAuth),
        false,
        S(9),
        &prior,
    );
    assert_eq!(
        d,
        Disposition::PauseHost {
            class: ErrorClass::DestinationAuth
        }
    );
    assert_eq!(n, prior);
}

#[test]
fn owner_fault_is_distinct_from_source_park() {
    let r = OwnerParkReason::ForeignWriter {
        theirs: Sha256::from_bytes([1; 32]),
        ours: Sha256::from_bytes([2; 32]),
    };
    let (d, _) = transition(
        &Outcome::OwnerFault(r.clone()),
        false,
        S(0),
        &Streak::default(),
    );
    assert_eq!(d, Disposition::ParkOwner { reason: r });
}

#[test]
fn parks_after_three_failures_spanning_three_hours() {
    let mut st = Streak::default();
    let (d, s1) = transition(&det(Fault::AuditMismatch), false, S(0), &st);
    assert!(matches!(d, Disposition::Retry { .. }));
    st = s1.clone();
    let (d, s2) = transition(&det(Fault::AuditMismatch), false, S(H), &st);
    assert!(matches!(d, Disposition::Retry { .. }));
    // Third failure one second short of 3 h: still retry.
    let (d, s3) = transition(&det(Fault::AuditMismatch), false, S(3 * H - 1), &s2);
    assert!(matches!(d, Disposition::Retry { .. }));
    assert_eq!(s3.deterministic, 3);
    // Exactly 3 h: park.
    let (d, _) = transition(&det(Fault::AuditMismatch), false, S(3 * H), &s2);
    assert_eq!(
        d,
        Disposition::Park {
            reason: ParkReason::RepeatedAuditMismatch
        }
    );
    // Two failures spanning long enough are not enough.
    let (d, _) = transition(&det(Fault::AuditMismatch), false, S(50 * H), &s1);
    assert!(matches!(d, Disposition::Retry { .. }));
}

#[test]
fn every_deterministic_fault_parks_with_its_own_reason() {
    // DESIGN 11.2: non-JSON 200 and schema errors are deterministic and park too.
    for (f, reason) in [
        (Fault::AuditMismatch, ParkReason::RepeatedAuditMismatch),
        (
            Fault::ReadbackMismatch,
            ParkReason::RepeatedReadbackMismatch,
        ),
        (Fault::SourceUnstable, ParkReason::SourceUnstable),
        (Fault::NonJsonResponse, ParkReason::RepeatedNonJsonResponse),
        (Fault::Schema, ParkReason::RepeatedSchemaError),
    ] {
        assert_eq!(park_reason(&f), reason);
        let prior = Streak {
            deterministic: 5,
            fault: Some(f.clone()),
            first_at: Some(S(0)),
            retries: 0,
        };
        let (d, _) = transition(&det(f.clone()), false, S(10 * H), &prior);
        assert_eq!(d, Disposition::Park { reason });
        // Below threshold the same fault still retries.
        let young = Streak {
            deterministic: 1,
            ..prior
        };
        let (d, _) = transition(&det(f), false, S(10 * H), &young);
        assert!(matches!(d, Disposition::Retry { .. }));
    }
}

#[test]
fn different_fault_or_success_resets_streak() {
    let prior = Streak {
        deterministic: 2,
        fault: Some(Fault::AuditMismatch),
        first_at: Some(S(0)),
        retries: 2,
    };
    let (d, n) = transition(&det(Fault::ReadbackMismatch), false, S(10 * H), &prior);
    assert!(matches!(d, Disposition::Retry { .. }));
    assert_eq!((n.deterministic, n.first_at), (1, Some(S(10 * H))));
    // Streak with a fault but missing first_at restarts.
    let odd = Streak {
        deterministic: 2,
        fault: Some(Fault::Schema),
        first_at: None,
        retries: 0,
    };
    assert_eq!(
        transition(&det(Fault::Schema), false, S(4), &odd)
            .1
            .deterministic,
        1
    );
    let (d, n) = transition(&Outcome::Succeeded, false, S(1), &prior);
    assert_eq!(d, Disposition::Done);
    assert_eq!(n, Streak::default());
}

fn key_len_model(a: usize, m: usize, s: usize) -> usize {
    SESSION_FIXED_CLAUDE + a + m + s
}

fn check_key_monotonic(a: usize, m: usize, s: usize, extra: usize) -> Result<(), String> {
    let (ar, mr, sr) = (id(a), id(m), id(s));
    let l1 = current_key_len(&ar, &mr, "claude", Some(&sr));
    let l2 = current_key_len(&ar, &mr, "claude", Some(&id(s + extra)));
    if l1 != key_len_model(a, m, s) || l2 < l1 {
        return Err("length model or monotonicity violated".into());
    }
    let ok = check_key_len(l1).is_ok();
    if ok != (key_len_model(a, m, s) <= 1024) {
        return Err("boundary not exact".into());
    }
    if !ok && check_key_len(l2).is_ok() {
        return Err("longer key admitted after shorter denied".into());
    }
    Ok(())
}

fn check_admission(ids: &[u16], extra: &[u16]) -> Result<(), String> {
    let mut set = SourceSet::new();
    let mut admitted: std::collections::HashSet<SourceId> = std::collections::HashSet::new();
    for n in ids.iter().chain(extra) {
        let s =
            SourceId::try_from(format!("x{}", u32::from(*n) % 1500)).map_err(|e| e.to_string())?;
        let known = admitted.contains(&s);
        let r = set.admit(&s);
        if known && r.is_err() {
            return Err("known source rejected".into());
        }
        match r {
            Ok(()) => {
                admitted.insert(s.clone());
            }
            Err(WithholdReason::SourceLimit) if admitted.len() == 1024 => {}
            Err(_) => return Err("unexpected rejection".into()),
        }
        if set.len() != admitted.len() || set.contains(&s) != admitted.contains(&s) {
            return Err("eviction or miscount".into());
        }
    }
    if admitted.iter().any(|a| !set.contains(a)) {
        return Err("eviction".into());
    }
    Ok(())
}

fn check_budget(a: u64, b: u64) -> Result<(), String> {
    let (lo, hi) = (a.min(b), a.max(b));
    if request_budget(lo) > request_budget(hi) || request_budget(a) < S(60) {
        return Err("budget not monotonic or below floor".into());
    }
    Ok(())
}

fn fault_of(n: u8) -> Fault {
    match n % 5 {
        0 => Fault::NonJsonResponse,
        1 => Fault::Schema,
        2 => Fault::AuditMismatch,
        3 => Fault::ReadbackMismatch,
        _ => Fault::SourceUnstable,
    }
}

fn check_cancel(
    c: u32,
    n: u8,
    first: u32,
    retries: u32,
    at: u32,
    flag: bool,
) -> Result<(), String> {
    let prior = Streak {
        deterministic: c,
        fault: Some(fault_of(n)),
        first_at: Some(S(u64::from(first))),
        retries,
    };
    let (d, next) = transition(&Outcome::Aborted, flag, S(u64::from(at)), &prior);
    if d != Disposition::Cancelled || next != prior {
        return Err("cancel changed streak or disposition".into());
    }
    Ok(())
}

#[hegel::test]
fn hegel_key_monotonic(tc: TestCase) {
    let a = usize::from(tc.draw(gs::integers::<u16>()) % 700) + 1;
    let m = usize::from(tc.draw(gs::integers::<u16>()) % 700) + 1;
    let s = usize::from(tc.draw(gs::integers::<u16>()) % 700) + 1;
    let e = usize::from(tc.draw(gs::integers::<u8>()));
    assert_eq!(check_key_monotonic(a, m, s, e), Ok(()));
}
#[hegel::test]
fn hegel_admission(tc: TestCase) {
    let ids: Vec<u16> = tc.draw(gs::vecs(gs::integers::<u16>()));
    let extra: Vec<u16> = (0..1100_u16).collect();
    let rewrites: Vec<u16> = tc.draw(gs::vecs(gs::integers::<u16>()));
    assert_eq!(check_admission(&ids, &extra), Ok(()));
    assert_eq!(check_admission(&extra, &rewrites), Ok(()));
}
#[hegel::test]
fn hegel_budget(tc: TestCase) {
    assert_eq!(
        check_budget(
            tc.draw(gs::integers::<u64>()),
            tc.draw(gs::integers::<u64>())
        ),
        Ok(())
    );
}
#[hegel::test]
fn hegel_cancel(tc: TestCase) {
    let r = check_cancel(
        tc.draw(gs::integers::<u32>()),
        tc.draw(gs::integers::<u8>()),
        tc.draw(gs::integers::<u32>()),
        tc.draw(gs::integers::<u32>()),
        tc.draw(gs::integers::<u32>()),
        tc.draw(gs::booleans()),
    );
    assert_eq!(r, Ok(()));
}

proptest! {
    #[test]
    fn proptest_key_monotonic(a in 1_usize..700, m in 1_usize..700, s in 1_usize..700, e in any::<u8>()) {
        prop_assert_eq!(check_key_monotonic(a, m, s, usize::from(e)), Ok(()));
    }
    #[test]
    fn proptest_admission(ids in proptest::collection::vec(any::<u16>(), 0..64), rewrites in proptest::collection::vec(any::<u16>(), 0..64)) {
        let fill: Vec<u16> = (0..1100_u16).collect();
        prop_assert_eq!(check_admission(&ids, &fill), Ok(()));
        prop_assert_eq!(check_admission(&fill, &rewrites), Ok(()));
    }
    #[test]
    fn proptest_budget(a in any::<u64>(), b in any::<u64>()) {
        prop_assert_eq!(check_budget(a, b), Ok(()));
    }
    #[test]
    fn proptest_cancel(c in any::<u32>(), n in any::<u8>(), f in any::<u32>(), r in any::<u32>(), at in any::<u32>(), flag in any::<bool>()) {
        prop_assert_eq!(check_cancel(c, n, f, r, at, flag), Ok(()));
    }
}
