//! External boundary semantics missing from the first integrated coverage run.
use memory_core::{coalescer::*, domain::*, planner::*};
use std::time::Duration;

#[test]
fn every_coordinate_brand_validates_its_own_boundary_and_preserves_valid_spelling()
-> Result<(), Box<dyn std::error::Error>> {
    let valid = "root-qualified.0123456789_ABC:def-ghi";
    let archive = ArchiveId::try_from(valid.to_owned())?;
    let machine = MachineId::try_from(valid.to_owned())?;
    let session = SessionId::try_from(valid.to_owned())?;
    let source = SourceId::try_from(valid.to_owned())?;
    assert_eq!(archive.as_str(), valid);
    assert_eq!(machine.as_str(), valid);
    assert_eq!(session.as_str(), valid);
    assert_eq!(source.as_str(), valid);
    assert_eq!(String::from(archive.clone()), valid);
    assert_eq!(String::from(machine.clone()), valid);
    assert_eq!(String::from(session.clone()), valid);
    assert_eq!(String::from(source.clone()), valid);
    assert_eq!(serde_json::to_value(&archive)?, serde_json::json!(valid));
    assert_eq!(serde_json::to_value(&machine)?, serde_json::json!(valid));
    assert_eq!(serde_json::to_value(&session)?, serde_json::json!(valid));
    assert_eq!(serde_json::to_value(&source)?, serde_json::json!(valid));
    assert_eq!(
        serde_json::from_value::<ArchiveId>(serde_json::json!(valid))?,
        archive
    );
    assert_eq!(
        serde_json::from_value::<MachineId>(serde_json::json!(valid))?,
        machine
    );
    assert_eq!(
        serde_json::from_value::<SessionId>(serde_json::json!(valid))?,
        session
    );
    assert_eq!(
        serde_json::from_value::<SourceId>(serde_json::json!(valid))?,
        source
    );
    for invalid in ["", ".", "..", "slash/name", "a\nb", "é"] {
        assert_eq!(
            ArchiveId::try_from(invalid.to_owned()),
            Err(ValidationError::Identifier)
        );
        assert_eq!(
            MachineId::try_from(invalid.to_owned()),
            Err(ValidationError::Identifier)
        );
        assert_eq!(
            SessionId::try_from(invalid.to_owned()),
            Err(ValidationError::Identifier)
        );
        assert_eq!(
            SourceId::try_from(invalid.to_owned()),
            Err(ValidationError::Identifier)
        );
        assert!(serde_json::from_value::<ArchiveId>(serde_json::json!(invalid)).is_err());
        assert!(serde_json::from_value::<MachineId>(serde_json::json!(invalid)).is_err());
        assert!(serde_json::from_value::<SessionId>(serde_json::json!(invalid)).is_err());
        assert!(serde_json::from_value::<SourceId>(serde_json::json!(invalid)).is_err());
    }
    let maximum = "a".repeat(1024);
    assert_eq!(ArchiveId::try_from(maximum.clone())?.as_str(), maximum);
    assert_eq!(MachineId::try_from(maximum.clone())?.as_str(), maximum);
    assert_eq!(SessionId::try_from(maximum.clone())?.as_str(), maximum);
    assert_eq!(SourceId::try_from(maximum.clone())?.as_str(), maximum);
    let too_long = "a".repeat(1025);
    assert_eq!(
        ArchiveId::try_from(too_long.clone()),
        Err(ValidationError::Identifier)
    );
    assert_eq!(
        MachineId::try_from(too_long.clone()),
        Err(ValidationError::Identifier)
    );
    assert_eq!(
        SessionId::try_from(too_long.clone()),
        Err(ValidationError::Identifier)
    );
    assert_eq!(
        SourceId::try_from(too_long),
        Err(ValidationError::Identifier)
    );
    Ok(())
}

#[test]
fn opaque_fence_and_canonical_digest_serde_schemas_describe_their_distinct_domains()
-> Result<(), Box<dyn std::error::Error>> {
    let spelling = "W/\"opaque+/= λ\"";
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
    let fence_schema = serde_json::to_value(schemars::schema_for!(ETag))?;
    assert_eq!(fence_schema.get("title"), Some(&serde_json::json!("ETag")));
    assert_eq!(fence_schema.get("type"), Some(&serde_json::json!("string")));
    assert_eq!(fence_schema.get("minLength"), Some(&serde_json::json!(1)));
    assert_eq!(
        fence_schema.get("pattern"),
        Some(&serde_json::json!(r"^[^\u0000-\u001f\u007f-\u009f]+$"))
    );
    assert!(
        fence_schema.get("maxLength").is_none(),
        "entity tags are not coordinate IDs"
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
    let hash_schema = serde_json::to_value(schemars::schema_for!(Sha256))?;
    assert_eq!(hash_schema.get("title"), Some(&serde_json::json!("Sha256")));
    assert_eq!(hash_schema.get("type"), Some(&serde_json::json!("string")));
    assert_eq!(
        hash_schema.get("pattern"),
        Some(&serde_json::json!("^[0-9a-f]{64}$"))
    );
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
