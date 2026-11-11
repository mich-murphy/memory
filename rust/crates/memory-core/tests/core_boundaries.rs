//! External boundary semantics missing from the first integrated coverage run.
use memory_core::{coalescer::*, domain::*, planner::*};
use std::time::Duration;

/// One body for the four coordinate brands: spelling is preserved, the 1024/1025 byte
/// boundary and the invalid set are rejected both by `try_from` and on the wire.
macro_rules! brand_boundary_test {
    ($name:ident, $brand:ident) => {
        #[test]
        fn $name() -> Result<(), Box<dyn std::error::Error>> {
            let valid = "root-qualified.0123456789_ABC:def-ghi";
            let brand = $brand::try_from(valid.to_owned())?;
            assert_eq!(brand.as_str(), valid);
            assert_eq!(String::from(brand.clone()), valid);
            assert_eq!(serde_json::to_value(&brand)?, serde_json::json!(valid));
            assert_eq!(
                serde_json::from_value::<$brand>(serde_json::json!(valid))?,
                brand
            );
            for invalid in ["", ".", "..", "slash/name", "a\nb", "\u{e9}"] {
                assert_eq!(
                    $brand::try_from(invalid.to_owned()),
                    Err(ValidationError::Identifier),
                    "{invalid:?}"
                );
                assert!(serde_json::from_value::<$brand>(serde_json::json!(invalid)).is_err());
            }
            let maximum = "a".repeat(1024);
            assert_eq!($brand::try_from(maximum.clone())?.as_str(), maximum);
            assert_eq!(
                $brand::try_from("a".repeat(1025)),
                Err(ValidationError::Identifier)
            );
            Ok(())
        }
    };
}
brand_boundary_test!(archive_id_validates_its_own_boundary, ArchiveId);
brand_boundary_test!(machine_id_validates_its_own_boundary, MachineId);
brand_boundary_test!(session_id_validates_its_own_boundary, SessionId);
brand_boundary_test!(source_id_validates_its_own_boundary, SourceId);

#[test]
fn opaque_fence_and_canonical_digest_keep_their_distinct_domains()
-> Result<(), Box<dyn std::error::Error>> {
    // Schema shape is pinned by `cargo xtask schemas --check`; only validation is asserted.
    let spelling = "W/\"opaque+/= \u{3bb}\"";
    let fence = ETag::try_from(spelling.to_owned())?;
    assert_eq!(serde_json::to_value(&fence)?, serde_json::json!(spelling));
    assert_eq!(String::from(fence.clone()), spelling);
    assert_eq!(
        serde_json::from_value::<ETag>(serde_json::json!(spelling))?,
        fence
    );
    assert_eq!(
        ETag::try_from("bad\u{0085}header".to_owned()),
        Err(ValidationError::EntityTag)
    );
    let canonical = "0123456789abcdef".repeat(4);
    let digest: Sha256 = serde_json::from_value(serde_json::json!(canonical))?;
    assert_eq!(serde_json::to_value(digest)?, serde_json::json!(canonical));
    assert_eq!(String::from(digest), canonical);
    for invalid in [
        "g".repeat(64),
        "A".repeat(64),
        "0".repeat(63),
        "0".repeat(65),
    ] {
        assert_eq!(Sha256::try_from(invalid), Err(ValidationError::Hash));
    }
    Ok(())
}

#[test]
fn valid_nondefault_window_roundtrips_and_controls_both_deadlines()
-> Result<(), Box<dyn std::error::Error>> {
    let seconds = WindowSeconds { quiet: 2, cap: 5 };
    let window = Window::try_from(seconds)?;
    assert_eq!(WindowSeconds::from(window), seconds);
    let wire = serde_json::json!({"quiet": 2, "cap": 5});
    assert_eq!(serde_json::to_value(window)?, wire);
    assert_eq!(serde_json::from_value::<Window>(wire)?, window);
    assert_eq!(
        WindowSeconds::from(Window::default()),
        WindowSeconds {
            quiet: 60,
            cap: 300
        }
    );
    let equal: Window = serde_json::from_value(serde_json::json!({"quiet": 7, "cap": 7}))?;
    assert_eq!(
        WindowSeconds::from(equal),
        WindowSeconds { quiet: 7, cap: 7 }
    );
    let owner = Owner::Shared {
        archive: ArchiveId::try_from("archive".to_owned())?,
        machine: MachineId::try_from("machine".to_owned())?,
        agent: Agent::Pi,
    };
    let mut quiet = Coalescer::new(window);
    quiet.signal(owner.clone(), Duration::ZERO, Signal::Changed)?;
    assert!(!quiet.poll(&owner, Duration::from_secs(1))?);
    assert!(quiet.poll(&owner, Duration::from_secs(2))?);
    let mut capped = Coalescer::new(window);
    capped.signal(owner.clone(), Duration::ZERO, Signal::Changed)?;
    for second in 1..=4 {
        capped.signal(owner.clone(), Duration::from_secs(second), Signal::Stop)?;
        assert!(!capped.poll(&owner, Duration::from_secs(second))?);
    }
    // Only one second quiet, but five seconds from first unflushed edge.
    assert!(capped.poll(&owner, Duration::from_secs(5))?);
    Ok(())
}

#[test]
fn unmatched_completion_does_not_consume_pending_signal_or_advance_owner_clock()
-> Result<(), Box<dyn std::error::Error>> {
    let owner = Owner::Shared {
        archive: ArchiveId::try_from("archive".to_owned())?,
        machine: MachineId::try_from("machine".to_owned())?,
        agent: Agent::Claude,
    };
    let mut scheduler = Coalescer::new(Window::try_from(WindowSeconds { quiet: 2, cap: 5 })?);
    scheduler.signal(owner.clone(), Duration::ZERO, Signal::Changed)?;
    assert_eq!(
        scheduler.complete(&owner, Duration::from_secs(100)),
        Err(CoalescerError::NotInFlight)
    );
    assert!(scheduler.poll(&owner, Duration::from_secs(2))?);
    scheduler.complete(&owner, Duration::from_secs(2))?;
    assert_eq!(
        scheduler.complete(&owner, Duration::from_secs(100)),
        Err(CoalescerError::NotInFlight)
    );
    scheduler.signal(owner.clone(), Duration::from_secs(3), Signal::Changed)?;
    assert!(!scheduler.poll(&owner, Duration::from_secs(4))?);
    assert!(scheduler.poll(&owner, Duration::from_secs(5))?);
    Ok(())
}

#[test]
fn changed_protected_rules_replay_remote_epoch_before_adopt_digest_is_requested() {
    let previous_rules = RulesDigest::new(Sha256::from_bytes([1; 32]));
    let current_rules = RulesDigest::new(Sha256::from_bytes([2; 32]));
    let remote = EpochPosition {
        epoch: 7,
        captured_to: Offset::new(100),
        whole: Sha256::from_bytes([3; 32]),
        rules: Some(previous_rules),
    };
    let mut request = PlanInput {
        withheld: None,
        observation: Observation::Present(Observed {
            id: FileId {
                dev: 1,
                ino: 2,
                birth: Birth::Unavailable,
            },
            size: 100,
            mtime_ns: 1,
            observed_at_ns: 9_000_000_000,
        }),
        strategy: Strategy::Append(AppendBasis::Unindexed {
            remote: remote.clone(),
            adopt_digest: None,
        }),
        rules: Some(current_rules),
        audit: AuditEvidence::NotDue,
    };
    let changed = Planning::Ready {
        plan: Plan::Replay {
            reason: ReplayReason::RulesChanged,
        },
        refresh_stat: false,
        adopted: None,
    };
    assert_eq!(plan(&request), changed);
    request.rules = None;
    assert_eq!(
        plan(&request),
        changed,
        "removing protected rules is not adoption under old rules"
    );
    request.rules = Some(previous_rules);
    assert_eq!(
        plan(&request),
        Planning::Ready {
            plan: Plan::Adopt { remote },
            refresh_stat: false,
            adopted: None
        }
    );
}

#[test]
fn idle_slots_are_evicted_and_next_due_reports_earliest_deadline()
-> Result<(), Box<dyn std::error::Error>> {
    let mk = |name: &str| -> Result<Owner, ValidationError> {
        Ok(Owner::Shared {
            archive: ArchiveId::try_from("archive".to_owned())?,
            machine: MachineId::try_from(name.to_owned())?,
            agent: Agent::Pi,
        })
    };
    let (a, b) = (mk("a")?, mk("b")?);
    let mut c = Coalescer::new(Window::try_from(WindowSeconds { quiet: 2, cap: 5 })?);
    assert_eq!(c.next_due(), None);
    c.signal(a.clone(), Duration::from_secs(10), Signal::Changed)?;
    c.signal(b.clone(), Duration::from_secs(11), Signal::Changed)?;
    assert_eq!(c.next_due(), Some(Duration::from_secs(12)));
    // Further edges push the quiet deadline but never past the cap.
    c.signal(a.clone(), Duration::from_secs(13), Signal::Changed)?;
    assert_eq!(c.next_due(), Some(Duration::from_secs(13)));
    c.signal(b.clone(), Duration::from_secs(14), Signal::SessionEnd)?;
    assert_eq!(c.next_due(), Some(Duration::from_secs(11)));
    assert!(c.poll(&b, Duration::from_secs(14))?);
    // b is in flight: excluded; a is due at min(15, 15).
    assert_eq!(c.next_due(), Some(Duration::from_secs(15)));
    c.complete(&b, Duration::from_secs(15))?;
    // b had no pending edge: its slot is gone, so a completion is unmatched.
    assert_eq!(
        c.complete(&b, Duration::from_secs(16)),
        Err(CoalescerError::NotInFlight)
    );
    assert!(c.poll(&a, Duration::from_secs(15))?);
    c.signal(a.clone(), Duration::from_secs(16), Signal::Changed)?;
    c.complete(&a, Duration::from_secs(16))?;
    // a keeps its follow-up edge.
    assert_eq!(c.next_due(), Some(Duration::from_secs(18)));
    assert!(c.poll(&a, Duration::from_secs(18))?);
    c.complete(&a, Duration::from_secs(18))?;
    assert_eq!(c.next_due(), None);
    Ok(())
}
