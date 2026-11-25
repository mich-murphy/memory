//! BRIEF57-SHIM: load an `index.json` written before brief 57 without a rebuild.
//!
//! A record may still hold the dead key `snapshot_at_ns` (ignored) and a `Sqlite` snapshot
//! fingerprint (loaded as no snapshot, together with its `snapshot_pieces`). The record is
//! written back without either. Every other unknown key is still refused. Delete this module,
//! the `mod` line in `lib.rs` and the `from` attribute on `SourceIndex` once both hosts have
//! run a release that rewrote their index.
use memory_core::domain::SourceState;
use memory_core::domain::{Fingerprint, RulesDigest, SnapshotFingerprint, WithholdReason};
use memory_core::manifest::Piece;
use serde::Deserialize;
use serde::de::IgnoredAny;

/// BRIEF57-SHIM: the pre-brief-57 snapshot fingerprint.
#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
enum RawFingerprint {
    Plain(Fingerprint),
    Sqlite {
        #[expect(dead_code)]
        db: IgnoredAny,
        #[expect(dead_code)]
        wal: IgnoredAny,
    },
}

/// BRIEF57-SHIM: the pre-brief-57 shape of one `index.json` record.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RawSourceIndex {
    index: Option<SourceState>,
    missing: u32,
    snapshot: Option<(RawFingerprint, Option<RulesDigest>)>,
    #[serde(default)]
    snapshot_pieces: Vec<Piece>,
    #[serde(default)]
    #[expect(dead_code)]
    snapshot_at_ns: Option<IgnoredAny>,
    deletion_recorded: bool,
    #[serde(default)]
    withheld: Option<(Fingerprint, Option<RulesDigest>, WithholdReason)>,
}

impl From<RawSourceIndex> for crate::daemon::SourceIndex {
    fn from(raw: RawSourceIndex) -> Self {
        let (snapshot, snapshot_pieces) = match raw.snapshot {
            Some((RawFingerprint::Plain(fp), rules)) => (
                Some((SnapshotFingerprint::Plain(fp), rules)),
                raw.snapshot_pieces,
            ),
            Some((RawFingerprint::Sqlite { .. }, _)) | None => (None, Vec::new()),
        };
        Self {
            index: raw.index,
            missing: raw.missing,
            snapshot,
            snapshot_pieces,
            deletion_recorded: raw.deletion_recorded,
            withheld: raw.withheld,
        }
    }
}
