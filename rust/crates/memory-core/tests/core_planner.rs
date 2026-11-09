//! Exhaustive decision-table boundary tests with independently supplied evidence.
use memory_core::{domain::*, planner::*};
fn hash(n: u8) -> Sha256 {
    Sha256::from_bytes([n; 32])
}
fn obs() -> Observed {
    Observed {
        id: FileId {
            dev: 1,
            ino: 2,
            birth: Birth::Unavailable,
        },
        size: 100,
        mtime_ns: 1,
        observed_at_ns: 9_000_000_000,
    }
}
fn position() -> EpochPosition {
    EpochPosition {
        epoch: 4,
        captured_to: Offset::new(100),
        whole: hash(3),
        rules: None,
    }
}
fn request() -> PlanInput {
    PlanInput {
        withheld: None,
        observation: Observation::Present(obs()),
        rules: None,
        audit: AuditEvidence::NotDue,
        strategy: Strategy::Append {
            index: Some(SourceState {
                observed: obs(),
                trust: StatTrust::Settled,
                position: position(),
                head: hash(1),
                tail: hash(2),
                audit: AuditState {
                    last_checked_at_ns: None,
                    consecutive_failures: 0,
                },
            }),
            remote: None,
            adopt_digest: None,
            windows: Some(WindowEvidence {
                head: hash(1),
                tail: hash(2),
            }),
        },
    }
}
fn planned(request: &PlanInput) -> Option<Plan> {
    match plan(request) {
        Planning::Ready { plan, .. } => Some(plan),
        Planning::NeedWindows | Planning::NeedAuditRecheck => None,
    }
}
fn replay(reason: ReplayReason) -> Option<Plan> {
    Some(Plan::Replay { reason })
}
#[test]
fn every_append_table_branch_and_independent_evidence_request() {
    let mut r = request();
    assert_eq!(planned(&r), Some(Plan::Unchanged));
    if let Strategy::Append { index, windows, .. } = &mut r.strategy {
        if let Some(state) = index {
            state.trust = StatTrust::Racy;
        }
        *windows = None;
    }
    assert_eq!(plan(&r), Planning::NeedWindows);
    if let Strategy::Append { windows, .. } = &mut r.strategy {
        *windows = Some(WindowEvidence {
            head: hash(1),
            tail: hash(2),
        });
    }
    assert_eq!(
        plan(&r),
        Planning::Ready {
            plan: Plan::Unchanged,
            refresh_stat: true,
            adopted: None
        }
    );
    if let Observation::Present(o) = &mut r.observation {
        o.mtime_ns += 1;
        o.size = 101;
    }
    assert_eq!(
        planned(&r),
        Some(Plan::Append {
            from: Offset::new(100),
            to: Offset::new(101)
        })
    );
    if let Strategy::Append {
        windows: Some(w), ..
    } = &mut r.strategy
    {
        w.tail = hash(9);
    }
    assert_eq!(planned(&r), replay(ReplayReason::WindowMismatch));
    if let Observation::Present(o) = &mut r.observation {
        o.size = 99;
    }
    assert_eq!(planned(&r), replay(ReplayReason::Truncated));
    if let Observation::Present(o) = &mut r.observation {
        o.id.birth = Birth::Known(1);
    }
    assert_eq!(planned(&r), replay(ReplayReason::IdentityChanged));
    r.observation = Observation::Missing { consecutive: 1 };
    assert_eq!(planned(&r), Some(Plan::Unchanged));
    r.observation = Observation::Missing { consecutive: 2 };
    assert_eq!(planned(&r), Some(Plan::Deleted));
    r.withheld = Some(WithholdReason::Credential);
    assert_eq!(
        planned(&r),
        Some(Plan::Withheld {
            reason: WithholdReason::Credential
        })
    );
}
#[test]
fn adoption_requires_full_digest_then_can_extend_without_new_epoch() {
    let mut r = request();
    r.strategy = Strategy::Append {
        index: None,
        remote: None,
        adopt_digest: None,
        windows: None,
    };
    assert_eq!(planned(&r), replay(ReplayReason::FirstCapture));
    if let Strategy::Append { remote, .. } = &mut r.strategy {
        *remote = Some(position());
    }
    assert_eq!(planned(&r), Some(Plan::Adopt { remote: position() }));
    if let Strategy::Append { adopt_digest, .. } = &mut r.strategy {
        *adopt_digest = Some(hash(9));
    }
    assert_eq!(planned(&r), replay(ReplayReason::AdoptMismatch));
    if let Strategy::Append { adopt_digest, .. } = &mut r.strategy {
        *adopt_digest = Some(hash(3));
    }
    assert_eq!(
        plan(&r),
        Planning::Ready {
            plan: Plan::Unchanged,
            refresh_stat: true,
            adopted: Some(position())
        }
    );
    if let Observation::Present(o) = &mut r.observation {
        o.size = 120;
    }
    assert_eq!(
        plan(&r),
        Planning::Ready {
            plan: Plan::Append {
                from: Offset::new(100),
                to: Offset::new(120)
            },
            refresh_stat: true,
            adopted: Some(position())
        }
    );
    if let Observation::Present(o) = &mut r.observation {
        o.size = 20;
    }
    assert_eq!(planned(&r), replay(ReplayReason::Truncated));
}
#[test]
fn full_audit_is_explicit_and_pinned_and_rules_are_not_writer_version() {
    let mut r = request();
    r.audit = AuditEvidence::Ineligible;
    assert_eq!(planned(&r), Some(Plan::Unchanged));
    r.audit = AuditEvidence::RecheckRequired;
    assert_eq!(plan(&r), Planning::NeedAuditRecheck);
    r.audit = AuditEvidence::StableMismatch;
    assert_eq!(planned(&r), replay(ReplayReason::AuditMismatch));
    r.rules = Some(RulesDigest(hash(8)));
    assert_eq!(planned(&r), replay(ReplayReason::RulesChanged));
    let captured = obs();
    assert!(audit_eligible(&captured, &captured, &captured));
    let mut changed = captured.clone();
    changed.size += 1;
    assert!(!audit_eligible(&captured, &changed, &changed));
    changed = captured.clone();
    changed.mtime_ns += 1;
    assert!(!audit_eligible(&captured, &captured, &changed));
    changed = captured.clone();
    changed.id.ino += 1;
    assert!(!audit_eligible(&captured, &changed, &changed));
    assert_eq!(captured.trust(), StatTrust::Settled);
    changed.mtime_ns = changed.observed_at_ns;
    assert_eq!(changed.trust(), StatTrust::Racy);
    changed.mtime_ns = i64::MAX;
    changed.observed_at_ns = i64::MIN;
    assert_eq!(changed.trust(), StatTrust::Racy);
}
fn fingerprint() -> Fingerprint {
    let o = obs();
    Fingerprint {
        id: o.id,
        size: o.size,
        mtime_ns: o.mtime_ns,
    }
}
#[test]
fn sqlite_db_and_wal_and_absence_are_each_part_of_snapshot_fingerprint() {
    let base = SnapshotFingerprint::Sqlite {
        db: fingerprint(),
        wal: WalFingerprint::Absent,
    };
    let mut r = request();
    r.strategy = Strategy::Snapshot {
        previous: Some(base.clone()),
        current: base.clone(),
        previous_rules: None,
    };
    assert_eq!(planned(&r), Some(Plan::Unchanged));
    if let Strategy::Snapshot { current, .. } = &mut r.strategy {
        *current = SnapshotFingerprint::Sqlite {
            db: fingerprint(),
            wal: WalFingerprint::Present(fingerprint()),
        };
    }
    assert_eq!(planned(&r), Some(Plan::Snapshot));
    r.rules = Some(RulesDigest(hash(6)));
    assert_eq!(planned(&r), replay(ReplayReason::RulesChanged));
    r.rules = None;
    if let Strategy::Snapshot { previous, .. } = &mut r.strategy {
        *previous = None;
    }
    assert_eq!(planned(&r), Some(Plan::Snapshot));
    let plain = SnapshotFingerprint::Plain(fingerprint());
    r.strategy = Strategy::Snapshot {
        previous: Some(plain.clone()),
        current: plain,
        previous_rules: None,
    };
    assert_eq!(planned(&r), Some(Plan::Unchanged));
}
#[test]
fn snapshot_audit_compares_transformed_bytes_even_when_fingerprint_is_stable() {
    let plain = SnapshotFingerprint::Plain(fingerprint());
    let mut r = request();
    r.strategy = Strategy::Snapshot {
        previous: Some(plain.clone()),
        current: plain,
        previous_rules: None,
    };
    r.audit = AuditEvidence::Ineligible;
    assert_eq!(planned(&r), Some(Plan::Unchanged));
    r.audit = AuditEvidence::RecheckRequired;
    assert_eq!(plan(&r), Planning::NeedAuditRecheck);
    r.audit = AuditEvidence::StableMismatch;
    assert_eq!(planned(&r), replay(ReplayReason::AuditMismatch));
}
#[test]
fn committed_deletion_repeat_has_no_capture_work() {
    let mut r = request();
    r.observation = Observation::Missing { consecutive: 2 };
    assert_eq!(planned(&r), Some(Plan::Deleted));
    // Shell updates its cache only after the deletion manifest commits.
    r.observation = Observation::DeletionRecorded;
    assert_eq!(planned(&r), Some(Plan::Unchanged));
}
#[test]
fn settled_same_size_changed_mtime_requires_windows_and_refreshes_stat() {
    let mut r = request();
    if let Observation::Present(o) = &mut r.observation {
        o.mtime_ns += 1;
    }
    if let Strategy::Append { windows, .. } = &mut r.strategy {
        *windows = None;
    }
    assert_eq!(plan(&r), Planning::NeedWindows);
    if let Strategy::Append { windows, .. } = &mut r.strategy {
        *windows = Some(WindowEvidence {
            head: hash(1),
            tail: hash(2),
        });
    }
    assert_eq!(
        plan(&r),
        Planning::Ready {
            plan: Plan::Unchanged,
            refresh_stat: true,
            adopted: None
        }
    );
    if let Strategy::Append {
        windows: Some(w), ..
    } = &mut r.strategy
    {
        w.head = hash(8);
    }
    assert_eq!(planned(&r), replay(ReplayReason::WindowMismatch));
}
#[test]
fn window_ranges_clamp_at_zero_and_old_committed_endpoint_without_overflow() {
    for at in [0_u64, 1, 4095, 4096, 65535, 65536, 100000, u64::MAX] {
        let (head, tail) = window_ranges(Offset::new(at));
        assert_eq!(head.start, Offset::new(0));
        assert_eq!(head.end.get(), if at < 4096 { at } else { 4096 });
        assert_eq!(tail.start.get(), at.saturating_sub(65536));
        assert_eq!(tail.end, Offset::new(at));
        assert!(head.end.get() + (tail.end.get() - tail.start.get()) <= 69632);
    }
}
