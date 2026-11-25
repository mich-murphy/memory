//! Boundary and property tests for limits and disposition.
use hegel::{TestCase, generators as gs};
use memory_core::disposition::*;
use memory_core::domain::*;
use memory_core::limits::*;
use proptest::prelude::*;
use std::time::Duration;

fn transition(
    o: &Outcome,
    cancel: bool,
    at: Duration,
    prior: &Streak,
) -> (memory_core::domain::Disposition, Streak) {
    memory_core::disposition::transition_paced(
        o,
        cancel,
        at,
        prior,
        memory_core::disposition::Pacing::FLOOR,
    )
}

type R = Result<(), Box<dyn std::error::Error>>;
const S: fn(u64) -> Duration = Duration::from_secs;

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
fn manifest_and_object_keys_have_the_published_layout() -> R {
    // Mutant: any change to the `manifests/<hash>.json` or `objects/<hash>.zst` spelling,
    // the single definition both the core and the shell use.
    let hash = Sha256::try_from("0123456789abcdef".repeat(4))?;
    assert_eq!(
        manifest_key(&hash),
        "manifests/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.json"
    );
    assert_eq!(
        object_key(&hash),
        "objects/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.zst"
    );
    Ok(())
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
    assert_eq!(d, Disposition::PauseHost);
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
    // A6: full jitter; draw 0 is the window minimum (1 s) instead of the old fixed 2 s.
    let (d, _) = transition(&Outcome::Failed(ErrorClass::Throttled), false, S(1), &n);
    assert_eq!(
        d,
        Disposition::Retry {
            after: S(1),
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
    assert_eq!(d, Disposition::PauseHost);
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
    fn proptest_cancel(c in any::<u32>(), n in any::<u8>(), f in any::<u32>(), r in any::<u32>(), at in any::<u32>(), flag in any::<bool>()) {
        prop_assert_eq!(check_cancel(c, n, f, r, at, flag), Ok(()));
    }
}

#[test]
fn a6_jitter_windows_hold_for_every_draw() {
    use memory_core::disposition::{Pacing, jittered, transient_ceiling, transition_paced};
    let draws = [0_u64, 1, u64::MAX / 3, u64::MAX / 2, u64::MAX - 1, u64::MAX];
    for n in [0_u32, 1, 2, 5, 6, 7, 9, 10, 11, 20, 21, u32::MAX] {
        for draw in draws {
            let pacing = Pacing {
                draw,
                retry_after: None,
            };
            let prior = Streak {
                retries: n,
                ..Streak::default()
            };
            let wait = |class: ErrorClass| match transition_paced(
                &Outcome::Failed(class),
                false,
                S(0),
                &prior,
                pacing,
            )
            .0
            {
                Disposition::Retry { after, .. } => after,
                _ => Duration::ZERO,
            };
            let offline = wait(ErrorClass::Offline);
            assert!(
                offline >= S(1) && offline <= backoff(n).min(S(900)),
                "{n} {draw} {offline:?}"
            );
            let transient = wait(ErrorClass::Transient);
            assert!(
                transient >= S(1) && transient <= S(120),
                "{n} {draw} {transient:?}"
            );
            assert!(transient <= transient_ceiling(n));
            // Throttled without Retry-After uses the Offline schedule.
            assert_eq!(wait(ErrorClass::Throttled), offline);
        }
    }
    assert_eq!(transient_ceiling(0), S(2));
    assert_eq!(transient_ceiling(5), S(64));
    assert_eq!(transient_ceiling(6), S(120));
    assert_eq!(jittered(S(900), 0), S(1));
    assert_eq!(jittered(S(900), u64::MAX), S(900));
}

#[test]
fn a6_retry_after_is_honoured_and_transient_is_its_own_class() {
    use memory_core::disposition::{Pacing, transition_paced};
    let pacing = Pacing {
        draw: 12345,
        retry_after: Some(S(37)),
    };
    let (d, n) = transition_paced(
        &Outcome::Failed(ErrorClass::Throttled),
        false,
        S(0),
        &Streak::default(),
        pacing,
    );
    assert_eq!(
        d,
        Disposition::Retry {
            after: S(37),
            class: ErrorClass::Throttled
        }
    );
    assert_eq!(n.retries, 1);
    // Transient ignores any Retry-After.
    let (d, _) = transition_paced(
        &Outcome::Failed(ErrorClass::Transient),
        false,
        S(0),
        &Streak::default(),
        pacing,
    );
    assert!(
        matches!(d, Disposition::Retry { class: ErrorClass::Transient, after } if after >= S(1) && after <= S(2))
    );
    // Cancel swallows it like other retryable classes.
    let (d, _) = transition(
        &Outcome::Failed(ErrorClass::Transient),
        true,
        S(0),
        &Streak::default(),
    );
    assert_eq!(d, Disposition::Cancelled);
}

#[test]
fn a6_retry_after_is_clamped_to_one_second_and_fifteen_minutes() {
    use memory_core::disposition::{Pacing, transition_paced};
    for (given, want) in [(0, 1), (1, 1), (900, 900), (901, 900), (86_400, 900)] {
        let pacing = Pacing {
            draw: u64::MAX,
            retry_after: Some(S(given)),
        };
        let (d, _) = transition_paced(
            &Outcome::Failed(ErrorClass::Throttled),
            false,
            S(0),
            &Streak::default(),
            pacing,
        );
        assert_eq!(
            d,
            Disposition::Retry {
                after: S(want),
                class: ErrorClass::Throttled
            },
            "{given}"
        );
    }
}

// ---------- owner text form ----------

fn check_owner_text(owner: &Owner) -> Result<(), String> {
    let p = owner_prefix(owner);
    if parse_owner_prefix(&p).as_ref() != Ok(owner) {
        return Err("parse(prefix(owner)) != owner".into());
    }
    if current_key(owner) != format!("{p}/current.json") {
        return Err("current_key != prefix + suffix".into());
    }
    Ok(())
}

const ID_CHARS: &[u8] = b"abcXYZ019._:-";

fn id_from(bytes: &[u8]) -> String {
    let mut s: String = bytes
        .iter()
        .map(|b| {
            ID_CHARS
                .get(usize::from(*b) % ID_CHARS.len())
                .map_or('a', |c| char::from(*c))
        })
        .collect();
    if s.is_empty() || s == "." || s == ".." {
        s = "a".into();
    }
    s
}

fn owner_from(
    kind: bool,
    a: &[u8],
    m: &[u8],
    s: &[u8],
    agent: u8,
) -> Result<Owner, ValidationError> {
    let agent = match agent % 3 {
        0 => Agent::Claude,
        1 => Agent::Codex,
        _ => Agent::Pi,
    };
    if kind {
        owner_session(&id_from(a), &id_from(m), &id_from(s), agent)
    } else {
        owner_shared(&id_from(a), &id_from(m), agent)
    }
}

#[hegel::test]
fn hegel_owner_text_roundtrip(tc: TestCase) {
    let kind = tc.draw(gs::booleans());
    let a: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()).max_size(40));
    let m: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()).max_size(40));
    let s: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()).max_size(40));
    let agent = tc.draw(gs::integers::<u8>());
    let owner = owner_from(kind, &a, &m, &s, agent);
    assert!(owner.is_ok());
    if let Ok(o) = owner {
        assert_eq!(check_owner_text(&o), Ok(()));
    }
}

proptest! {
    #[test]
    fn proptest_owner_text_roundtrip(
        kind in any::<bool>(),
        a in prop::collection::vec(any::<u8>(), 0..40),
        m in prop::collection::vec(any::<u8>(), 0..40),
        s in prop::collection::vec(any::<u8>(), 0..40),
        agent in any::<u8>()
    ) {
        let owner = owner_from(kind, &a, &m, &s, agent);
        prop_assert!(owner.is_ok());
        if let Ok(o) = owner {
            prop_assert_eq!(check_owner_text(&o), Ok(()));
        }
    }
}

#[test]
fn owner_text_refusals() {
    let long = "a".repeat(1100);
    let table: [(&str, OwnerTextError); 14] = [
        ("session/a/m/claude/s", OwnerTextError::Prefix),
        ("", OwnerTextError::Prefix),
        ("sessions/a/m/claude", OwnerTextError::Segments),
        ("shared/a/m", OwnerTextError::Segments),
        ("sessions/a/m/claude/s/extra", OwnerTextError::Segments),
        ("shared/a/m/claude/extra", OwnerTextError::Segments),
        ("sessions/a/m/claude/s/", OwnerTextError::Segments),
        ("shared/a/m/claude/", OwnerTextError::Segments),
        ("shared/a/m/", OwnerTextError::EmptySegment),
        ("sessions/a//claude/s", OwnerTextError::EmptySegment),
        ("shared/a/m/claude/current.json", OwnerTextError::Segments),
        (
            "sessions/a/m/claude/s/current.json",
            OwnerTextError::Segments,
        ),
        ("shared/a/m/gemini", OwnerTextError::Agent),
        ("shared/a/../claude", OwnerTextError::Identifier),
    ];
    for (text, want) in table {
        assert_eq!(parse_owner_prefix(text), Err(want), "{text}");
    }
    assert_eq!(
        parse_owner_prefix(&format!("shared/{long}/m/claude")),
        Err(OwnerTextError::TooLong)
    );
    assert_eq!(
        parse_owner_prefix("shared/a b/m/claude"),
        Err(OwnerTextError::Identifier)
    );
}

#[test]
fn owner_text_length_bound_is_the_key_bound() -> R {
    // 1,024-byte key: prefix may be 1,024 - 13 bytes.
    let fixed = "shared///claude".len();
    let room = MAX_KEY_BYTES - 13 - fixed;
    let ok = format!("shared/{}/{}/claude", "a".repeat(room - 1), "b");
    assert_eq!(ok.len() + 13, MAX_KEY_BYTES);
    // ids are capped at 1,024 each, so this is the key bound, not the id bound
    let owner = parse_owner_prefix(&ok)?;
    assert_eq!(current_key(&owner).len(), MAX_KEY_BYTES);
    let over = format!("shared/{}/{}/claude", "a".repeat(room), "b");
    assert_eq!(parse_owner_prefix(&over), Err(OwnerTextError::TooLong));
    Ok(())
}
