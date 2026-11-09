use super::schema::{decode, preflight};
use super::{CHECKPOINT_INTERVAL, ManifestError};
use crate::domain::{EpochPosition, Offset, RulesDigest, Sha256, SourceId};
use serde::Deserialize;

// ---------------------------------------------------------------- recovery projection

type Ignored = serde::de::IgnoredAny;
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
enum RawOp {
    Extend,
    Epoch,
    Replace,
    Deleted,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RawEpoch {
    n: u32,
    #[serde(default)]
    rules: Option<RulesDigest>,
    #[expect(dead_code, reason = "allowed envelope field, not projected")]
    writer: Ignored,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope fields, not projected")]
struct RawChange {
    id: SourceId,
    op: RawOp,
    kind: Option<Ignored>,
    path: Option<Ignored>,
    class: Option<Ignored>,
    pieces: Option<Ignored>,
    deleted_at: Option<Ignored>,
    epoch: Option<RawEpoch>,
    size: Option<u64>,
    sha256: Option<Sha256>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope field, not projected")]
struct RawRecord {
    epoch: RawEpoch,
    size: u64,
    sha256: Sha256,
    pieces: Ignored,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope fields, not projected")]
struct RawSource {
    id: SourceId,
    kind: Ignored,
    path: Ignored,
    class: Ignored,
    epochs: Vec<RawRecord>,
    deleted_at: Option<String>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope fields, not projected")]
struct RawDelta {
    schema: Ignored,
    owner: Ignored,
    seq: u64,
    previous: Option<Sha256>,
    checkpoint: Option<Sha256>,
    captured_at: Ignored,
    writer: Ignored,
    launch: Ignored,
    changes: Vec<RawChange>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope fields, not projected")]
struct RawCheckpoint {
    schema: Ignored,
    owner: Ignored,
    seq: u64,
    previous: Option<Sha256>,
    captured_at: Ignored,
    writer: Ignored,
    launch: Ignored,
    sources: Vec<RawSource>,
}
/// Strict per-kind envelope: a delta may carry `changes` and never `sources`; a checkpoint
/// the reverse. Presence of the wrong field is an error whatever its length.
#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
enum RawProjection {
    Delta(RawDelta),
    Checkpoint(RawCheckpoint),
}

/// Kind of a projected manifest.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ProjectedKind {
    /// Delta.
    Delta,
    /// Checkpoint.
    Checkpoint,
}
/// Latest epoch position of one source, or `None` if the source was deleted.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProjectedSource {
    /// Source id.
    pub id: SourceId,
    /// Latest epoch position.
    pub latest: Option<EpochPosition>,
}
/// Position fields of a remote manifest, as the writer's recovery path may read them.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Projection {
    /// Delta or checkpoint.
    pub kind: ProjectedKind,
    /// Sequence number.
    pub seq: u64,
    /// Previous manifest hash.
    pub previous: Option<Sha256>,
    /// Latest checkpoint hash (deltas only).
    pub checkpoint: Option<Sha256>,
    /// Changed sources (delta) or all sources (checkpoint).
    pub sources: Vec<ProjectedSource>,
}

fn position(epoch: &RawEpoch, size: u64, whole: Sha256) -> EpochPosition {
    EpochPosition {
        epoch: epoch.n,
        captured_to: Offset::new(size),
        whole,
        rules: epoch.rules,
    }
}

/// Recovery projection (R5): strict envelope (duplicate keys, schema dispatch, kind/seq
/// rule) and only the position fields; every other field, at any depth, is ignored. Strict
/// validation of everything else belongs to [`parse_manifest`].
///
/// # Errors
/// [`ManifestError::NewerSchema`] / [`ManifestError::UnknownSchema`] before the body is
/// read; otherwise [`ManifestError::Malformed`], [`ManifestError::BadSeq`] or
/// [`ManifestError::BadLink`].
pub fn recover_projection(bytes: &[u8]) -> Result<Projection, ManifestError> {
    preflight(bytes, "memory.manifest/")?;
    match decode::<RawProjection>(bytes)? {
        RawProjection::Checkpoint(c) => {
            if !c.seq.is_multiple_of(CHECKPOINT_INTERVAL) {
                return Err(ManifestError::BadSeq);
            }
            if (c.seq == 0) != c.previous.is_none() {
                return Err(ManifestError::BadLink);
            }
            let sources = c
                .sources
                .iter()
                .map(|s| {
                    let latest = match (&s.deleted_at, s.epochs.last()) {
                        (None, Some(r)) => Some(position(&r.epoch, r.size, r.sha256)),
                        (Some(_), Some(_)) => None,
                        (_, None) => return Err(ManifestError::Malformed),
                    };
                    Ok(ProjectedSource {
                        id: s.id.clone(),
                        latest,
                    })
                })
                .collect::<Result<Vec<_>, _>>()?;
            Ok(Projection {
                kind: ProjectedKind::Checkpoint,
                seq: c.seq,
                previous: c.previous,
                checkpoint: None,
                sources,
            })
        }
        RawProjection::Delta(d) => {
            if d.seq.is_multiple_of(CHECKPOINT_INTERVAL) {
                return Err(ManifestError::BadSeq);
            }
            if d.previous.is_none() || d.checkpoint.is_none() {
                return Err(ManifestError::BadLink);
            }
            let sources = d
                .changes
                .iter()
                .map(|c| {
                    let latest = match (c.op, &c.epoch, c.size, c.sha256) {
                        (RawOp::Deleted, _, _, _) => None,
                        (_, Some(e), Some(size), Some(whole)) => Some(position(e, size, whole)),
                        _ => return Err(ManifestError::Malformed),
                    };
                    Ok(ProjectedSource {
                        id: c.id.clone(),
                        latest,
                    })
                })
                .collect::<Result<Vec<_>, _>>()?;
            Ok(Projection {
                kind: ProjectedKind::Delta,
                seq: d.seq,
                previous: d.previous,
                checkpoint: d.checkpoint,
                sources,
            })
        }
    }
}
