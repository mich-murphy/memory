//! Literal-outcome tests for the pure verification classifier (DESIGN.md 7.4 step 5, A6).
use memory_core::domain::{CommitOutcome, ETag, Sha256, ValidationError};
use memory_core::verify::{Verdict, classify};
use std::cell::Cell;
use std::collections::HashSet;

type R = Result<(), ValidationError>;

fn h(n: u8) -> Sha256 {
    Sha256::from_bytes([n; 32])
}
fn tag() -> Result<ETag, ValidationError> {
    ETag::try_from("\"e\"".to_owned())
}
fn run(
    current: u8,
    previous: Option<u8>,
    known: &[u8],
    newer: bool,
) -> Result<(Verdict, usize), ValidationError> {
    let known: HashSet<Sha256> = known.iter().map(|n| h(*n)).collect();
    let probes = Cell::new(0_usize);
    let verdict = classify(
        h(current),
        tag()?,
        h(1),
        previous.map(h),
        |x| known.contains(x),
        || {
            probes.set(probes.get() + 1);
            newer
        },
    );
    Ok((verdict, probes.get()))
}

#[test]
fn own_manifest_is_already_ours() -> R {
    let (verdict, probes) = run(1, Some(2), &[], true)?;
    assert_eq!(verdict, Verdict::Ours { etag: tag()? });
    assert_eq!(
        verdict.outcome(),
        CommitOutcome::AlreadyOurs { etag: tag()? }
    );
    assert_eq!(probes, 0);
    Ok(())
}

#[test]
fn ours_wins_over_known_and_base() -> R {
    let (verdict, _) = run(1, Some(1), &[1], false)?;
    assert_eq!(verdict, Verdict::Ours { etag: tag()? });
    Ok(())
}

#[test]
fn a6_known_ours_late_write_is_adopted_as_already_ours() -> R {
    let (verdict, probes) = run(7, Some(2), &[7], true)?;
    assert_eq!(
        verdict,
        Verdict::Adopt {
            etag: tag()?,
            manifest: h(7)
        }
    );
    assert_eq!(
        verdict.outcome(),
        CommitOutcome::AlreadyOurs { etag: tag()? }
    );
    assert_eq!(probes, 0);
    Ok(())
}

#[test]
fn known_ours_wins_over_base() -> R {
    let (verdict, _) = run(2, Some(2), &[2], false)?;
    assert_eq!(
        verdict,
        Verdict::Adopt {
            etag: tag()?,
            manifest: h(2)
        }
    );
    Ok(())
}

#[test]
fn previous_is_base_unchanged() -> R {
    let (verdict, probes) = run(2, Some(2), &[7], true)?;
    assert_eq!(
        verdict,
        Verdict::Base {
            etag: tag()?,
            manifest: h(2)
        }
    );
    assert_eq!(verdict.outcome(), CommitOutcome::BaseUnchanged);
    assert_eq!(probes, 0);
    Ok(())
}

#[test]
fn unknown_hash_is_foreign_after_one_probe() -> R {
    let (verdict, probes) = run(9, Some(2), &[7], false)?;
    assert_eq!(verdict, Verdict::Foreign { theirs: h(9) });
    assert_eq!(verdict.outcome(), CommitOutcome::Foreign { theirs: h(9) });
    assert_eq!(probes, 1);
    Ok(())
}

#[test]
fn first_write_has_no_base_so_previous_hash_is_foreign() -> R {
    let (verdict, _) = run(2, None, &[], false)?;
    assert_eq!(verdict, Verdict::Foreign { theirs: h(2) });
    Ok(())
}

#[test]
fn newer_schema_manifest_parks_instead_of_foreign() -> R {
    let (verdict, probes) = run(9, Some(2), &[], true)?;
    assert_eq!(verdict, Verdict::NewerSchema);
    assert_eq!(verdict.outcome(), CommitOutcome::NewerSchema);
    assert_eq!(probes, 1);
    Ok(())
}
