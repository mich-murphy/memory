//! Independent byte-model safety prerequisites, not store/manifest acceptance.
use hegel::{TestCase, generators as gs};
use memory_core::planner::Strategy;
use memory_core::{coalescer::*, domain::*, planner::*};
use proptest::prelude::*;
use sha2::{Digest, Sha256 as Hasher};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

/// Independent counters of generated case shapes (coverage guard, DESIGN.md section 15).
struct Shapes {
    multi_epoch: AtomicU64,
    window_rewrite: AtomicU64,
    interior_audit: AtomicU64,
    truncate_regrow: AtomicU64,
    identity_replacement: AtomicU64,
}
impl Shapes {
    const fn new() -> Self {
        Self {
            multi_epoch: AtomicU64::new(0),
            window_rewrite: AtomicU64::new(0),
            interior_audit: AtomicU64::new(0),
            truncate_regrow: AtomicU64::new(0),
            identity_replacement: AtomicU64::new(0),
        }
    }
    fn counts(&self) -> [(&'static str, u64); 5] {
        [
            ("multi-epoch", self.multi_epoch.load(Ordering::Relaxed)),
            (
                "rewrite-detected-at-window",
                self.window_rewrite.load(Ordering::Relaxed),
            ),
            (
                "interior-rewrite-with-audit",
                self.interior_audit.load(Ordering::Relaxed),
            ),
            (
                "truncate-regrow",
                self.truncate_regrow.load(Ordering::Relaxed),
            ),
            (
                "identity-replacement",
                self.identity_replacement.load(Ordering::Relaxed),
            ),
        ]
    }
}
fn bump(counter: &AtomicU64) {
    counter.fetch_add(1, Ordering::Relaxed);
}
/// Fails when any required shape was never generated over the run.
fn require_all_shapes(counts: &[(&'static str, u64)]) -> Result<(), String> {
    let missing: Vec<_> = counts
        .iter()
        .filter(|(_, n)| *n == 0)
        .map(|(k, _)| *k)
        .collect();
    if missing.is_empty() && !counts.is_empty() {
        Ok(())
    } else {
        Err(format!(
            "coverage guard: generator never produced {missing:?}"
        ))
    }
}
static HEGEL_SHAPES: Shapes = Shapes::new();
static PROPTEST_SHAPES: Shapes = Shapes::new();

fn digest(bytes: &[u8]) -> Sha256 {
    Sha256::from_bytes(Hasher::digest(bytes).into())
}
fn observed(size: usize, ino: u64, mtime_ns: i64) -> Observed {
    Observed {
        id: FileId {
            dev: 1,
            ino,
            birth: Birth::Known(7),
        },
        size: size as u64,
        mtime_ns,
        observed_at_ns: 10_000_000_000,
    }
}
fn windows(bytes: &[u8], at: usize) -> WindowEvidence {
    let head: Vec<_> = bytes.iter().copied().take(at.min(4096)).collect();
    let tail: Vec<_> = bytes
        .iter()
        .copied()
        .skip(at.saturating_sub(65536))
        .take(at.min(65536))
        .collect();
    WindowEvidence {
        head: digest(&head),
        tail: digest(&tail),
    }
}
fn state(bytes: &[u8], obs: Observed) -> SourceState {
    let window = windows(bytes, bytes.len());
    SourceState {
        observed: obs,
        position: EpochPosition {
            epoch: 0,
            captured_to: Offset::new(bytes.len() as u64),
            whole: digest(bytes),
            rules: None,
        },
        head: window.head,
        tail: window.tail,
        audit: AuditState {
            last_checked_at_ns: None,
            consecutive_failures: 0,
        },
        whole_state: None,
    }
}
fn input(bytes: &[u8], obs: Observed, old: SourceState) -> PlanInput {
    let at = old.position.captured_to.get() as usize;
    PlanInput {
        withheld: None,
        observation: Observation::Present(obs),
        strategy: Strategy::Append(AppendBasis::Indexed {
            state: Box::new(old),
            windows: Some(windows(bytes, at)),
        }),
        rules: None,
        audit: AuditEvidence::NotDue,
    }
}
fn decision(input: &PlanInput) -> Plan {
    match plan(input) {
        Planning::Ready { plan, .. } => plan,
        other => {
            assert!(
                matches!(other, Planning::Ready { .. }),
                "missing test evidence: {other:?}"
            );
            Plan::Unchanged
        }
    }
}
fn reassemble(old: &[u8], current: &[u8], plan: Plan) -> Vec<u8> {
    match plan {
        Plan::Replay { .. } | Plan::Snapshot => current.to_vec(),
        Plan::Append { from, to } => old
            .iter()
            .copied()
            .chain(
                current
                    .iter()
                    .copied()
                    .skip(from.get() as usize)
                    .take((to.get() - from.get()) as usize),
            )
            .collect(),
        Plan::Unchanged => old.to_vec(),
        Plan::Adopt { .. } | Plan::Deleted | Plan::Withheld { .. } => Vec::new(),
    }
}
/// Generator owns complete bytes, edits and capture schedule, independent of planner.
fn capture_oracle(seed: u16, shapes: &Shapes) {
    let n = 73_728 + usize::from(seed % 1024);
    let initial: Vec<_> = (0..n).map(|i| (i as u8).wrapping_add(seed as u8)).collect();
    let mut seen = [false; 10];
    let mut replayed = false;
    for operation in 0..10 {
        let mut current = initial.clone();
        let mut ino = 1;
        let mut mtime = 1;
        let mut old = state(&initial, observed(n, 1, 1));
        let edit = match operation {
            0 => None,            // append
            1 => Some(0),         // head start
            2 => Some(4095),      // head inclusive endpoint
            3 => Some(n - 65536), // tail inclusive start
            4 => Some(n - 1),     // tail final byte
            5 => {
                ino = 2;
                Some(n / 2)
            } // rename-replace
            6 => {
                // truncate then regrow; same inode, changed prefix
                current.truncate(20);
                current.resize(n, 77);
                Some(0)
            }
            7 => {
                old.observed.observed_at_ns = old.observed.mtime_ns;
                mtime = 1;
                Some(9)
            } // equal size + mtime racy
            8 => Some(5000), // strict interior: requires audit repair
            _ => {
                current.truncate(20);
                None
            } // pure truncation
        };
        if let Some(index) = edit
            && let Some(byte) = current.get_mut(index)
        {
            *byte ^= 0xff;
        }
        if operation != 7 && operation != 9 {
            current.extend([0xff, 0x80, b'\n', seed as u8]);
        }
        if operation != 7 {
            mtime += 1;
        }
        let obs = observed(current.len(), ino, mtime);
        let mut request = input(&current, obs.clone(), old.clone());
        let capture_plan = decision(&request);
        let is_replay = matches!(capture_plan, Plan::Replay { .. });
        replayed |= is_replay;
        if is_replay {
            bump(&shapes.multi_epoch);
            match operation {
                1..=4 => bump(&shapes.window_rewrite),
                6 => bump(&shapes.truncate_regrow),
                _ => {}
            }
        }
        if operation == 5
            && matches!(
                capture_plan,
                Plan::Replay {
                    reason: ReplayReason::IdentityChanged
                }
            )
        {
            bump(&shapes.identity_replacement);
        }
        let assembled = reassemble(&initial, &current, capture_plan);
        if operation != 8 {
            assert!(
                current.starts_with(&assembled),
                "operation={operation} seed={seed}"
            );
        } else {
            assert_ne!(
                assembled, current,
                "interior limitation must remain visible"
            );
        }
        // Model committed assembled epoch, then independently eligible audit of stable current input.
        let committed = state(&assembled, obs.clone());
        assert!(audit_eligible(&obs, &obs, &obs));
        request = input(&current, obs, committed.clone());
        request.audit = if digest(&current) == committed.position.whole {
            AuditEvidence::Matched
        } else {
            AuditEvidence::StableMismatch
        };
        assert_eq!(
            reassemble(&assembled, &current, decision(&request)),
            current
        );
        if operation == 8 {
            bump(&shapes.interior_audit);
        }
        if let Some(flag) = seen.get_mut(operation) {
            *flag = true;
        }
    }
    assert!(replayed, "applicable multi-epoch generator coverage guard");
    assert!(
        seen.into_iter().all(|covered| covered),
        "applicable edit generator coverage guard"
    );
}
#[test]
fn hegel_property1_independent_capture_and_eligible_audit() {
    hegel::Hegel::new(|tc: TestCase| capture_oracle(tc.draw(gs::integers::<u16>()), &HEGEL_SHAPES))
        .run();
    let guard = require_all_shapes(&HEGEL_SHAPES.counts());
    assert!(guard.is_ok(), "{guard:?}");
}
#[test]
fn proptest_property1_independent_capture_and_eligible_audit() {
    let mut runner = proptest::test_runner::TestRunner::default();
    let result = runner.run(&any::<u16>(), |seed| {
        capture_oracle(seed, &PROPTEST_SHAPES);
        Ok(())
    });
    assert!(result.is_ok(), "{result:?}");
    let guard = require_all_shapes(&PROPTEST_SHAPES.counts());
    assert!(guard.is_ok(), "{guard:?}");
}
#[test]
fn property1_coverage_guard_negative_controls_bite() {
    let zero = Shapes::new();
    let err = require_all_shapes(&zero.counts());
    assert!(err.is_err_and(|e| e.contains("multi-epoch") && e.contains("identity-replacement")));
    assert!(require_all_shapes(&[]).is_err());
    for missing in 0..5 {
        let shapes = Shapes::new();
        for (i, c) in [
            &shapes.multi_epoch,
            &shapes.window_rewrite,
            &shapes.interior_audit,
            &shapes.truncate_regrow,
            &shapes.identity_replacement,
        ]
        .into_iter()
        .enumerate()
        {
            if i != missing {
                bump(c);
            }
        }
        let name = shapes.counts().get(missing).map(|c| c.0);
        let err = require_all_shapes(&shapes.counts());
        assert!(
            err.is_err_and(|e| name.is_some_and(|n| e.contains(n))),
            "{missing}"
        );
    }
    let full = Shapes::new();
    for c in [
        &full.multi_epoch,
        &full.window_rewrite,
        &full.interior_audit,
        &full.truncate_regrow,
        &full.identity_replacement,
    ] {
        bump(c);
    }
    assert!(require_all_shapes(&full.counts()).is_ok());
}
fn owner() -> Result<Owner, ValidationError> {
    Ok(Owner::Shared {
        archive: ArchiveId::try_from("archive".to_owned())?,
        machine: MachineId::try_from("machine".to_owned())?,
        agent: Agent::Pi,
    })
}
fn coalescer_oracle(interval: u8) -> Result<(), Box<dyn std::error::Error>> {
    let owner = owner()?;
    let mut c = Coalescer::new(Window::default());
    let interval = u64::from(interval % 59 + 1);
    let mut first = 0;
    let mut last = 0;
    let mut emitted = false;
    c.signal(owner.clone(), Duration::ZERO, Signal::Stop)?;
    for second in 0..=300 {
        if second > 0 && second % interval == 0 && !emitted {
            c.signal(owner.clone(), Duration::from_secs(second), Signal::Changed)?;
            last = second;
        }
        let expected = !emitted && (second - last >= 60 || second - first >= 300);
        assert_eq!(c.poll(&owner, Duration::from_secs(second))?, expected);
        if expected {
            emitted = true;
            first = second;
        }
    }
    assert!(emitted, "cap liveness");
    c.signal(owner.clone(), Duration::from_secs(301), Signal::SessionEnd)?;
    assert!(!c.poll(&owner, Duration::from_secs(301))?, "one in flight");
    c.complete(&owner, Duration::from_secs(302))?;
    assert!(
        c.poll(&owner, Duration::from_secs(302))?,
        "later end signal preserved"
    );
    c.complete(&owner, Duration::from_secs(303))?;
    c.signal(owner.clone(), Duration::from_secs(304), Signal::Stop)?;
    assert!(!c.poll(&owner, Duration::from_secs(363))?);
    assert!(c.poll(&owner, Duration::from_secs(364))?);
    Ok(())
}
#[hegel::test]
fn hegel_property2_monotonic_quiet_cap_end(tc: TestCase) {
    assert!(coalescer_oracle(tc.draw(gs::integers::<u8>())).is_ok());
}
proptest! {
    #[test]
    fn proptest_property2_monotonic_quiet_cap_end(interval in any::<u8>()) { prop_assert!(coalescer_oracle(interval).is_ok()); }
}
#[test]
fn boundaries_strict_deserialization_and_dotted_wire_tags() -> Result<(), Box<dyn std::error::Error>>
{
    for bad in ["", ".", "..", "a/b", "a\nb", "é"] {
        assert!(serde_json::from_value::<SourceId>(serde_json::json!(bad)).is_err());
    }
    let tag = "\"opaque-store-token+/==\"";
    let fence: ETag = serde_json::from_value(serde_json::json!(tag))?;
    assert_eq!(fence.as_str(), tag);
    for bad in ["\"x\"\r", "x\ny", ""] {
        assert!(ETag::try_from(bad.to_owned()).is_err());
    }
    let lower = "ab".repeat(32);
    let hash: Sha256 = serde_json::from_value(serde_json::json!(lower))?;
    assert_eq!(String::from(hash), lower);
    assert!(serde_json::from_value::<Sha256>(serde_json::json!("AB".repeat(32))).is_err());
    assert!(serde_json::from_value::<Sha256>(serde_json::json!("ab")).is_err());
    assert_eq!(
        serde_json::to_value(SourceKind::ClaudeTranscript)?,
        serde_json::json!("claude.transcript")
    );
    assert!(
        serde_json::from_str::<FileId>(r#"{"dev":1,"ino":2,"birth":"unavailable","extra":0}"#)
            .is_err()
    );
    assert_eq!(
        Offset::new(u64::MAX).checked_add(1),
        Err(ValidationError::Overflow)
    );
    let schema = serde_json::to_string(&schemars::schema_for!(SourceId))?;
    assert!(schema.contains("pattern") && schema.contains("maxLength"));
    Ok(())
}
#[test]
fn coalescer_config_wake_and_backwards_are_explicit() -> Result<(), Box<dyn std::error::Error>> {
    assert!(Window::try_from(WindowSeconds { quiet: 0, cap: 300 }).is_err());
    assert!(
        Window::try_from(WindowSeconds {
            quiet: 301,
            cap: 300
        })
        .is_err()
    );
    assert!(serde_json::from_str::<Window>(r#"{"quiet":60,"cap":300,"extra":1}"#).is_err());
    assert!(serde_json::from_str::<Window>(r#"{"quiet":0,"cap":300}"#).is_err());
    assert_eq!(
        wake_action(Duration::from_secs(180), Duration::from_secs(60)),
        WakeAction::None
    );
    assert_eq!(
        wake_action(Duration::from_secs(181), Duration::from_secs(60)),
        WakeAction::ReconcileAndStagger {
            jitter_max: Duration::from_secs(30)
        }
    );
    let owner = owner()?;
    let mut c = Coalescer::new(Window::default());
    assert!(!c.poll(&owner, Duration::ZERO)?);
    assert_eq!(
        c.complete(&owner, Duration::ZERO),
        Err(CoalescerError::NotInFlight)
    );
    c.signal(owner.clone(), Duration::from_secs(2), Signal::Changed)?;
    assert_eq!(
        c.signal(owner.clone(), Duration::from_secs(1), Signal::Stop),
        Err(CoalescerError::Backwards)
    );
    assert_eq!(
        c.poll(&owner, Duration::from_secs(1)),
        Err(CoalescerError::Backwards)
    );
    assert!(c.poll(&owner, Duration::from_secs(62))?);
    assert_eq!(
        c.complete(&owner, Duration::from_secs(61)),
        Err(CoalescerError::Backwards)
    );
    c.complete(&owner, Duration::from_secs(62))?;
    Ok(())
}
#[test]
fn immediate_session_end_and_independent_owner_monotonic_clocks()
-> Result<(), Box<dyn std::error::Error>> {
    let first = owner()?;
    let second = Owner::Shared {
        archive: ArchiveId::try_from("other".to_owned())?,
        machine: MachineId::try_from("machine".to_owned())?,
        agent: Agent::Claude,
    };
    let mut c = Coalescer::new(Window::default());
    c.signal(first.clone(), Duration::from_secs(100), Signal::Stop)?;
    c.signal(second.clone(), Duration::ZERO, Signal::SessionEnd)?;
    assert!(c.poll(&second, Duration::ZERO)?);
    assert!(!c.poll(&first, Duration::from_secs(100))?);
    c.signal(first.clone(), Duration::from_secs(101), Signal::SessionEnd)?;
    assert!(c.poll(&first, Duration::from_secs(101))?);
    Ok(())
}
#[test]
fn owner_wire_goldens_and_schema_use_session_id_and_distinct_shared_coordinates()
-> Result<(), Box<dyn std::error::Error>> {
    let session = Owner::Session(SessionKey {
        archive: ArchiveId::try_from("archive".to_owned())?,
        machine: MachineId::try_from("machine".to_owned())?,
        agent: Agent::Claude,
        session: SessionId::try_from("session-1".to_owned())?,
    });
    let session_golden = serde_json::json!({"session": {
        "archive": "archive", "machine": "machine", "agent": "claude", "session_id": "session-1"
    }});
    assert_eq!(serde_json::to_value(&session)?, session_golden);
    assert_eq!(serde_json::from_value::<Owner>(session_golden)?, session);
    let shared = owner()?;
    let shared_golden = serde_json::json!({"shared": {
        "archive": "archive", "machine": "machine", "agent": "pi"
    }});
    assert_eq!(serde_json::to_value(&shared)?, shared_golden);
    assert_eq!(serde_json::from_value::<Owner>(shared_golden)?, shared);
    assert!(
        serde_json::from_value::<Owner>(serde_json::json!({"session": {
            "archive": "archive", "machine": "machine", "agent": "claude", "session": "session-1"
        }}))
        .is_err()
    );
    assert!(
        serde_json::from_value::<Owner>(serde_json::json!({"shared": {
            "archive": "archive", "machine": "machine", "agent": "pi", "session_id": "invented"
        }}))
        .is_err()
    );
    let schema = serde_json::to_value(schemars::schema_for!(Owner))?;
    let key = schema
        .pointer("/$defs/SessionKey")
        .ok_or("missing SessionKey schema")?;
    assert_eq!(
        key.pointer("/properties/session_id/$ref"),
        Some(&serde_json::json!("#/$defs/SessionId"))
    );
    assert!(key.pointer("/properties/session").is_none());
    assert_eq!(
        key.get("required"),
        Some(&serde_json::json!([
            "archive",
            "machine",
            "agent",
            "session_id"
        ]))
    );
    assert_eq!(
        key.get("additionalProperties"),
        Some(&serde_json::json!(false))
    );
    let shared_schema = schema
        .pointer("/oneOf/1/properties/shared")
        .ok_or("missing shared schema")?;
    assert_eq!(
        shared_schema.get("required"),
        Some(&serde_json::json!(["archive", "machine", "agent"]))
    );
    assert!(shared_schema.pointer("/properties/session_id").is_none());
    assert_eq!(
        shared_schema.get("additionalProperties"),
        Some(&serde_json::json!(false))
    );
    Ok(())
}
