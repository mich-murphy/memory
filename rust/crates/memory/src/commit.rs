//! Commit protocol driver (DESIGN.md 7.3 step 6 and 7.4 steps 1-6).
//!
//! Synchronous and I/O-free beyond the [`Store`] trait: the clock, filesystem and store are
//! behind simulator-controlled seams (DESIGN.md 12). The driver PUTs pieces (a 412 adopts
//! the encoding that actually won, R2), PUTs the manifest if absent, performs the guarded
//! `current.json` PUT and classifies the step 5 verification GET.
use crate::environment::{Published, Store, StoreError};
use memory_core::disposition::{Outcome, Pacing, Streak, transition_paced};
use memory_core::domain::{
    CommitOutcome, Disposition, ETag, ErrorClass, Fault, Owner, OwnerParkReason, Sha256,
};
use memory_core::limits::current_key;
use memory_core::manifest::{
    Built, CHECKPOINT_INTERVAL, Change, Current, Header, Manifest, ManifestError, OwnerState,
    Position, ProjectedKind, StoredEncoding, build, fold, manifest_hash, parse_current,
    recover_projection,
};
use memory_core::verify::{Verdict, classify};
use std::collections::BTreeMap;
use std::time::Duration;

/// Manifests this daemon built for one owner since its last landed commit, by hash (amendment
/// A6). In memory only; [`commit_known`] clears it when a commit lands.
pub type KnownOurs = std::collections::HashMap<Sha256, Built>;

/// Retries of step 3 after `BaseUnchanged` (DESIGN.md 7.4 step 5: up to 4).
pub const REBASE_RETRIES: u32 = 4;
/// Attempts of the verification GET while it errors or answers 404.
pub const GET_ATTEMPTS: u32 = 4;

pub use memory_core::limits::manifest_key;

/// The committed position we fence on: what the index (or a recovery) says `current.json`
/// names, with the ETag it was read under.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Base {
    /// Position of the manifest named by `etag`.
    pub position: Position,
    /// Owner state after that manifest.
    pub state: OwnerState,
    /// Fence for the next `current.json` write.
    pub etag: ETag,
}

/// One piece object to PUT. The key is `objects/<sha256 of the transformed piece bytes>.zst`;
/// the body is those bytes zstd-compressed.
#[derive(Debug)]
pub struct Upload {
    /// Object key.
    pub key: String,
    /// Upload body; only authorized pieces or writer metadata can build one.
    pub body: Published,
}

/// Everything one commit needs. `changes` describe pieces with the encoding the caller
/// produced; the driver rewrites a descriptor to the stored encoding when a PUT is a 412.
#[derive(Debug)]
pub struct Commit {
    /// Manifest header: owner, time, writer, pending launch records.
    pub header: Header,
    /// Changes of the new manifest.
    pub changes: Vec<Change>,
    /// Piece objects to store first.
    pub uploads: Vec<Upload>,
}

/// How a landed commit became durable.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LandedHow {
    /// Our `current.json` PUT replied.
    Committed,
    /// Verification by GET proved our write had landed.
    AlreadyOurs,
}

/// This capture's own manifest is durable. Only `commit_known` builds one (fields are private
/// and there is no constructor), so code that applies local updates can demand it as proof.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Landed {
    how: LandedHow,
    etag: ETag,
    built: Built,
}

impl Landed {
    /// How the commit became durable.
    pub const fn how(&self) -> LandedHow {
        self.how
    }
    /// ETag of `current.json` after the commit.
    pub const fn etag(&self) -> &ETag {
        &self.etag
    }
    /// The committed manifest.
    pub const fn built(&self) -> &Built {
        &self.built
    }
}

/// `current.json` named an earlier attempt's manifest (`known_ours`): this commit's own
/// manifest did not land, so the caller must capture again and apply no local update.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Adopted {
    etag: ETag,
    built: Built,
}

impl Adopted {
    /// ETag of `current.json` after the adoption.
    pub const fn etag(&self) -> &ETag {
        &self.etag
    }
    /// The earlier attempt's manifest.
    pub const fn built(&self) -> &Built {
        &self.built
    }
}

/// A commit that ended with a durable `current.json`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CommitEnd {
    /// This capture's bytes landed.
    Landed(Landed),
    /// An earlier attempt of ours landed instead.
    Adopted(Adopted),
}

impl CommitEnd {
    /// The base for the next commit.
    pub fn base(&self) -> Base {
        let (built, etag) = match self {
            Self::Landed(l) => (&l.built, &l.etag),
            Self::Adopted(a) => (&a.built, &a.etag),
        };
        Base {
            position: built.position,
            state: built.state.clone(),
            etag: etag.clone(),
        }
    }
}

/// Why a commit did not land, or could not be confirmed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Failure {
    /// Nothing is referenced by `current.json`; retrying the capture is safe.
    NotLanded(ErrorClass),
    /// The `current.json` write could not be verified; it may have landed. The caller
    /// must refresh its base from the store before the next commit.
    LandingUnknown(ErrorClass),
    /// Nothing landed because the store throttled the request.
    Throttled {
        /// `Retry-After` of the reply, when present.
        retry_after: Option<Duration>,
    },
    /// `BaseUnchanged` persisted through every retry; the capture ends `Transient`.
    BaseUnchanged,
    /// Another writer advanced the owner: park, never merge.
    Foreign {
        /// Manifest `current.json` names.
        theirs: Sha256,
        /// Our manifest.
        ours: Sha256,
    },
    /// The remote document has a newer schema: park.
    NewerSchema,
    /// Our own manifest or invariant 6 was invalid (a bug, deterministic).
    Invalid(ManifestError),
    /// A remote document could not be read and is not a newer schema.
    Corrupt,
}

impl Failure {
    /// The memory-core commit outcome for the branches it names.
    pub fn commit_outcome(&self) -> Option<CommitOutcome> {
        match self {
            Self::BaseUnchanged => Some(CommitOutcome::BaseUnchanged),
            Self::Foreign { theirs, .. } => Some(CommitOutcome::Foreign { theirs: *theirs }),
            Self::NewerSchema => Some(CommitOutcome::NewerSchema),
            _ => None,
        }
    }
    /// Attempt outcome for [`transition`] (DESIGN.md 11.2, 11.4).
    pub fn outcome(&self) -> Outcome {
        match self {
            Self::NotLanded(class) | Self::LandingUnknown(class) => Outcome::Failed(class.clone()),
            Self::Throttled { .. } => Outcome::Failed(ErrorClass::Throttled),
            Self::BaseUnchanged => Outcome::Failed(ErrorClass::Transient),
            Self::Foreign { theirs, ours } => Outcome::OwnerFault(OwnerParkReason::ForeignWriter {
                theirs: *theirs,
                ours: *ours,
            }),
            Self::NewerSchema => Outcome::OwnerFault(OwnerParkReason::NewerRemoteSchema),
            Self::Invalid(_) | Self::Corrupt => {
                Outcome::Failed(ErrorClass::Deterministic(Fault::Schema))
            }
        }
    }
    /// `Retry-After` carried by a throttling reply.
    pub const fn retry_after(&self) -> Option<Duration> {
        match self {
            Self::Throttled { retry_after } => *retry_after,
            _ => None,
        }
    }
    /// True when the caller's base may no longer match `current.json`.
    pub const fn base_may_be_stale(&self) -> bool {
        matches!(self, Self::LandingUnknown(_))
    }
}

/// Map a commit result onto a [`Disposition`] with [`transition`].
///
/// Jitter draw 0: every jittered wait is its minimum. See [`disposition_paced`].
pub fn disposition(
    result: &Result<CommitEnd, Failure>,
    cancel_requested: bool,
    at: Duration,
    prior: &Streak,
) -> (Disposition, Streak) {
    disposition_paced(result, cancel_requested, at, prior, 0)
}

/// As [`disposition`], with the shell's uniform random `draw` for the full-jitter backoff.
/// A `Retry-After` on the failure is passed through.
pub fn disposition_paced(
    result: &Result<CommitEnd, Failure>,
    cancel_requested: bool,
    at: Duration,
    prior: &Streak,
    draw: u64,
) -> (Disposition, Streak) {
    let (outcome, retry_after) = match result {
        Ok(_) => (Outcome::Succeeded, None),
        Err(failure) => (failure.outcome(), failure.retry_after()),
    };
    transition_paced(
        &outcome,
        cancel_requested,
        at,
        prior,
        Pacing { draw, retry_after },
    )
}

/// Invariant 6 (DESIGN.md 7.4): the `previous` of the manifest being committed equals the
/// manifest `current.json` named under the ETag we fence on (`None` on the first write).
///
/// # Errors
/// [`Failure::Invalid`] with [`ManifestError::BadLink`] on a mismatch.
pub fn check_fence(manifest: &Manifest, named: Option<Sha256>) -> Result<(), Failure> {
    let previous = match manifest {
        Manifest::Delta(d) => Some(d.previous()),
        Manifest::Checkpoint(c) => c.previous(),
    };
    if previous == named {
        Ok(())
    } else {
        Err(Failure::Invalid(ManifestError::BadLink))
    }
}

fn not_landed(error: StoreError) -> Failure {
    match error {
        StoreError::Throttled(_) => Failure::Throttled {
            retry_after: error.retry_after(),
        },
        other => Failure::NotLanded(other.class()),
    }
}

/// SHA-256 of stored (encoded) object bytes; not a manifest identity.
fn sha256_of(bytes: &[u8]) -> Sha256 {
    manifest_hash(bytes)
}

/// A remote document that did not parse: a newer schema parks, anything else is corrupt.
fn newer_or_corrupt(e: &ManifestError) -> Failure {
    if e.commit_outcome() == Some(CommitOutcome::NewerSchema) {
        Failure::NewerSchema
    } else {
        Failure::Corrupt
    }
}

fn length(bytes: &[u8]) -> u64 {
    u64::try_from(bytes.len()).unwrap_or(u64::MAX)
}

fn adopt_winner(changes: &mut [Change], winners: &BTreeMap<String, StoredEncoding>) {
    for change in changes {
        if let Change::Extend(w) | Change::Epoch(w) | Change::Replace(w) = change {
            for piece in &mut w.pieces {
                if let Some(winner) = winners.get(piece.object()) {
                    *piece = piece.clone().adopt_stored(winner);
                }
            }
        }
    }
}

/// Step 1: PUT every piece. A 412 means the object is stored; read it back and describe
/// the encoding that actually won (R2).
fn upload_pieces<S: Store>(
    store: &mut S,
    uploads: Vec<Upload>,
    changes: &mut [Change],
) -> Result<(), Failure> {
    let mut winners = BTreeMap::new();
    for upload in uploads {
        match store.put_if_absent(&upload.key, upload.body) {
            Ok(_) => {}
            Err(StoreError::PreconditionFailed) => {
                let stored = store.get(&upload.key).map_err(not_landed)?;
                let bytes = stored.body.as_ref();
                winners.insert(
                    upload.key,
                    StoredEncoding {
                        encoded_len: length(bytes),
                        encoded_sha256: sha256_of(bytes),
                    },
                );
            }
            Err(error) => return Err(not_landed(error)),
        }
    }
    adopt_winner(changes, &winners);
    Ok(())
}

/// Step 5: GET `current.json` and classify it with the pure [`classify`]. `Ok(None)` is 404
/// through every attempt. The manifest hash is the request id.
fn verify<S: Store>(
    store: &mut S,
    key: &str,
    ours: Sha256,
    previous: Option<Sha256>,
    known: &KnownOurs,
    pause: &mut dyn FnMut(u32),
) -> Result<Option<Verdict>, Failure> {
    let mut last = StoreError::NotFound;
    for attempt in 0..GET_ATTEMPTS {
        match store.get(key) {
            Ok(stored) => {
                let current = parse_current(&stored.body).map_err(|e| newer_or_corrupt(&e))?;
                let verdict = classify(
                    current.manifest,
                    stored.etag,
                    ours,
                    previous,
                    |h| known.contains_key(h),
                    || {
                        store.get(&manifest_key(&current.manifest)).is_ok_and(|m| {
                            recover_projection(&m.body)
                                .err()
                                .and_then(|e| e.commit_outcome())
                                == Some(CommitOutcome::NewerSchema)
                        })
                    },
                );
                return Ok(Some(verdict));
            }
            Err(StoreError::Auth) => {
                return Err(Failure::LandingUnknown(ErrorClass::DestinationAuth));
            }
            Err(error) => last = error,
        }
        if attempt + 1 < GET_ATTEMPTS {
            pause(attempt);
        }
    }
    if last == StoreError::NotFound {
        Ok(None)
    } else {
        Err(Failure::LandingUnknown(last.class()))
    }
}

fn put_document<S: Store>(store: &mut S, key: &str, bytes: Vec<u8>) -> Result<(), Failure> {
    match store.put_if_absent(key, Published::metadata(bytes)) {
        Ok(_) | Err(StoreError::PreconditionFailed) => Ok(()),
        Err(error) => Err(not_landed(error)),
    }
}

/// Run one commit: DESIGN.md 7.4 steps 1-6.
///
/// `base` is `None` for the owner's first write. `pause` is called with the retry number
/// before each backoff; the shell supplies the jittered sleep.
///
/// # Errors
/// A [`Failure`] naming whether anything landed, and the disposition class.
pub fn commit<S: Store>(
    store: &mut S,
    base: Option<&Base>,
    request: Commit,
    pause: &mut dyn FnMut(u32),
) -> Result<CommitEnd, Failure> {
    commit_known(store, base, request, &mut KnownOurs::new(), pause)
}

/// As [`commit`], with the owner's `known_ours` (amendment A6): every manifest built since the
/// last landed commit. The new manifest is added before any request; the set is cleared when
/// a commit lands. A verification that finds `current.json` naming a known manifest adopts it
/// ([`CommitEnd::Adopted`]) instead of reporting a foreign writer.
///
/// # Errors
/// As [`commit`].
pub fn commit_known<S: Store>(
    store: &mut S,
    base: Option<&Base>,
    request: Commit,
    known: &mut KnownOurs,
    pause: &mut dyn FnMut(u32),
) -> Result<CommitEnd, Failure> {
    let Commit {
        header,
        mut changes,
        uploads,
    } = request;
    let key = current_key(&header.owner);
    let updated_at = header.captured_at.clone();
    upload_pieces(store, uploads, &mut changes)?;
    let built =
        build(base.map(|b| (&b.position, &b.state)), header, changes).map_err(Failure::Invalid)?;
    let ours = built.hash;
    known.insert(ours, built.clone());
    put_document(store, &manifest_key(&ours), built.bytes.clone())?;
    let pointer = Current::new(ours, built.position.seq, updated_at)
        .to_bytes()
        .map_err(Failure::Invalid)?;
    let previous = base.map(|b| b.position.manifest);
    let mut fence = base.map(|b| b.etag.clone());
    let mut named = previous;
    let mut rebases = 0_u32;
    loop {
        check_fence(&built.manifest, named)?;
        let attempt = match &fence {
            Some(etag) => store.put_if_match(&key, etag, Published::metadata(pointer.clone())),
            None => store.put_if_absent(&key, Published::metadata(pointer.clone())),
        };
        match attempt {
            Ok(etag) => {
                known.clear();
                return Ok(CommitEnd::Landed(Landed {
                    how: LandedHow::Committed,
                    etag,
                    built,
                }));
            }
            Err(StoreError::Auth) => {
                return Err(Failure::NotLanded(ErrorClass::DestinationAuth));
            }
            Err(_) => {}
        }
        let mut absent = false;
        match verify(store, &key, ours, previous, known, pause)? {
            Some(Verdict::Ours { etag }) => {
                known.clear();
                return Ok(CommitEnd::Landed(Landed {
                    how: LandedHow::AlreadyOurs,
                    etag,
                    built,
                }));
            }
            Some(Verdict::Adopt { etag, manifest }) => {
                let earlier = known.get(&manifest).cloned().ok_or(Failure::Corrupt)?;
                known.clear();
                return Ok(CommitEnd::Adopted(Adopted {
                    etag,
                    built: earlier,
                }));
            }
            Some(Verdict::Base { etag, manifest }) => {
                fence = Some(etag);
                named = Some(manifest);
            }
            Some(Verdict::Foreign { theirs }) => return Err(Failure::Foreign { theirs, ours }),
            Some(Verdict::NewerSchema) => return Err(Failure::NewerSchema),
            None => {
                // 404 is not final. Only a first write may retry step 3 (If-None-Match: *).
                if fence.is_some() {
                    return Err(Failure::LandingUnknown(ErrorClass::Offline));
                }
                absent = true;
            }
        }
        rebases += 1;
        if rebases > REBASE_RETRIES {
            return Err(if absent {
                Failure::LandingUnknown(ErrorClass::Offline)
            } else {
                Failure::BaseUnchanged
            });
        }
        pause(rebases);
    }
}

/// Rebuild the base from the store (DESIGN.md 9.3 step 2): GET `current.json`, walk
/// `previous` back to the checkpoint and fold the chain. `Ok(None)` is a first write.
///
/// # Errors
/// [`Failure::NotLanded`] for transport or auth errors, [`Failure::NewerSchema`] for a
/// newer remote schema and [`Failure::Corrupt`] for a chain that does not fold.
pub fn recover_base<S: Store>(store: &mut S, owner: &Owner) -> Result<Option<Base>, Failure> {
    let pointer = match store.get(&current_key(owner)) {
        Ok(stored) => stored,
        Err(StoreError::NotFound) => return Ok(None),
        Err(error) => return Err(not_landed(error)),
    };
    let current = parse_current(&pointer.body).map_err(|e| newer_or_corrupt(&e))?;
    let mut hash = current.manifest;
    let mut chain: Vec<Vec<u8>> = Vec::new();
    loop {
        // A valid chain is a checkpoint (seq % 64 == 0) plus at most 63 deltas: 64 manifests.
        // Holding 64 without having reached a checkpoint means a 65th GET cannot be valid.
        if chain.len() as u64 >= CHECKPOINT_INTERVAL {
            return Err(Failure::Corrupt);
        }
        let stored = store.get(&manifest_key(&hash)).map_err(|e| match e {
            StoreError::NotFound | StoreError::PreconditionFailed => Failure::Corrupt,
            other => not_landed(other),
        })?;
        let bytes: Vec<u8> = stored.body;
        if manifest_hash(&bytes) != hash {
            return Err(Failure::Corrupt);
        }
        let projection = recover_projection(&bytes).map_err(|e| newer_or_corrupt(&e))?;
        chain.push(bytes);
        match projection.kind {
            ProjectedKind::Checkpoint => break,
            ProjectedKind::Delta => hash = projection.previous.ok_or(Failure::Corrupt)?,
        }
    }
    chain.reverse();
    let refs: Vec<&[u8]> = chain.iter().map(Vec::as_slice).collect();
    let folded = fold(&refs).map_err(|e| newer_or_corrupt(&e))?;
    if folded.owner != *owner || folded.position.manifest != current.manifest {
        return Err(Failure::Corrupt);
    }
    Ok(Some(Base {
        position: folded.position,
        state: folded.state,
        etag: pointer.etag,
    }))
}
