//! Export of one session owner's folded state as a `session-reader/v2` directory
//! (`metadata.json` plus `bodies/NNNN.bin`), the envelope the Python importer reads.
//!
//! [`build_envelope`] (and [`build_shared_envelope`], the `shared-reader/v1` form for a shared
//! owner's run records, brief 56) is pure: it checks every limit and renders the whole
//! `metadata.json` before any file exists. [`write_export`] then streams each epoch body piece
//! by piece (fetch, [`decode_piece`], write, incremental SHA-256); at most one stored and one
//! decoded piece are alive at a time, and it never calls `restore_epoch`.
//!
//! The `object` descriptor of an epoch is shape-checked by the importer and never fetched. One
//! piece: that piece's key, `encoded_sha256` and `encoded_len`. Several pieces: the first
//! piece's key, `encoded_size` the checked sum of the encoded lengths, `encoded_sha256` the
//! SHA-256 of the pieces' `encoded_sha256` hex strings concatenated in order (a commitment, not a
//! fetchable object). No pieces: key `objects/none`, size 0, SHA-256 of the empty string.
//!
//! Directory contract: the destination must not exist; it is created 0700 with `bodies/` 0700
//! and files 0600 (`create_new`), every file fsynced; `.metadata.tmp` is fsynced and renamed to
//! `metadata.json` only after the bodies are synced, then the directory and its parent are
//! fsynced. A failure removes what this run created, so an absent `metadata.json` never has a
//! directory left behind.
use crate::{OwnerHead, decode_piece};
use memory_core::domain::{Owner, Sha256 as Digest};
use memory_core::locator::{export_role, shared_analytics};
use memory_core::manifest::{EpochRecord, Piece, manifest_hash};
use serde_json::{Value, json};
use sha2::{Digest as _, Sha256};
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::Write;
use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt};
use std::path::Path;

/// The importer's source bound.
pub const MAX_SOURCES: usize = 1024;
/// The shared importer's source bound (brief 56).
pub const MAX_SHARED_SOURCES: usize = 16_384;
/// The importer's `metadata.json` bound.
pub const MAX_METADATA: usize = 8 << 20;

/// Why an export was refused or failed. Closed; carries no input text.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum ExportError<E> {
    /// The owner is not a session owner (`build_envelope` was given a shared owner).
    #[error("owner_shared")]
    Shared,
    /// The owner is not a shared owner (`build_shared_envelope` was given a session owner).
    #[error("owner_session")]
    Session,
    /// The sequence or a size does not fit the envelope.
    #[error("export_overflow")]
    Overflow,
    /// A stored time is not representable as nanoseconds.
    #[error("export_bad_time")]
    BadTime,
    /// The exported bytes exceed the budget.
    #[error("export_too_large")]
    TooLarge,
    /// More than [`MAX_SOURCES`] epochs ([`MAX_SHARED_SOURCES`] for a shared owner).
    #[error("export_too_many_sources")]
    TooManySources,
    /// `metadata.json` would exceed [`MAX_METADATA`].
    #[error("export_metadata_too_large")]
    MetadataTooLarge,
    /// The destination already exists.
    #[error("output_exists")]
    DestinationExists,
    /// A filesystem operation failed.
    #[error("output_io")]
    Io,
    /// Fetching an object failed.
    #[error("store")]
    Fetch(E),
    /// A piece object is absent.
    #[error("object_missing")]
    ObjectMissing,
    /// An epoch did not decode to the bytes the manifest promises.
    #[error("source_corrupt")]
    Corrupt,
}

/// A rendered envelope and the epochs whose bodies it names, in body order.
#[derive(Debug)]
pub struct Envelope<'a> {
    /// The bytes of `metadata.json`.
    pub metadata: Vec<u8>,
    /// Epoch of each `bodies/NNNN.bin`, by index.
    pub epochs: Vec<&'a EpochRecord>,
    /// Live sources exported (epochs).
    pub sources: usize,
    /// Deleted sources exported (every epoch `sealed`).
    pub deleted: usize,
    /// Sum of the epoch sizes.
    pub bytes: u64,
    /// Stored entries left out because they are not analytics sources (shared owners only;
    /// always 0 for a session owner).
    pub skipped: usize,
}

fn hex(d: Digest) -> String {
    String::from(d)
}

fn object(record: &EpochRecord) -> Result<Value, ()> {
    let (key, encoded_size, encoded_sha) = match record.pieces.as_slice() {
        [] => ("objects/none".to_owned(), 0, hex(manifest_hash(b""))),
        [one] => (
            one.object().to_owned(),
            one.encoded_len(),
            hex(one.encoded_sha256()),
        ),
        [first, ..] => {
            let mut size: u64 = 0;
            let mut h = Sha256::new();
            for p in &record.pieces {
                size = size.checked_add(p.encoded_len()).ok_or(())?;
                h.update(hex(p.encoded_sha256()).as_bytes());
            }
            (
                first.object().to_owned(),
                size,
                hex(Digest::from_bytes(h.finalize().into())),
            )
        }
    };
    Ok(json!({
        "key": key,
        "encoding": "zstd",
        "sha256": hex(record.sha256),
        "encoded_sha256": encoded_sha,
        "size": record.size,
        "encoded_size": encoded_size,
    }))
}

/// Render the envelope for `head`, checking every limit. Touches no filesystem.
///
/// # Errors
/// A closed [`ExportError`] (never a `Fetch`).
pub fn build_envelope<E>(head: &OwnerHead, max_bytes: u64) -> Result<Envelope<'_>, ExportError<E>> {
    let Owner::Session(key) = &head.folded.owner else {
        return Err(ExportError::Shared);
    };
    let sequence = head
        .folded
        .position
        .seq
        .checked_add(1)
        .ok_or(ExportError::Overflow)?;
    let captured = head.captured_at.unix_ns().ok_or(ExportError::BadTime)?;
    let published = head.updated_at.unix_ns().ok_or(ExportError::BadTime)?;
    let manifest_hex = hex(head.folded.position.manifest);

    let mut epochs: Vec<&EpochRecord> = Vec::new();
    let mut sources: Vec<Value> = Vec::new();
    let mut bodies: Vec<Value> = Vec::new();
    let mut bytes: u64 = 0;
    let mut deleted = 0;
    for (id, entry) in &head.folded.state.sources {
        // Sessions are never deleted from the store: a deleted source's epochs are still
        // exported, none of them `active`.
        let gone = entry.deleted_at.is_some();
        deleted += usize::from(gone);
        let role = export_role(&entry.kind, &entry.path).as_str();
        let count = entry.epochs.len();
        for (index, record) in entry.epochs.iter().enumerate() {
            bytes = bytes
                .checked_add(record.size)
                .ok_or(ExportError::TooLarge)?;
            let n = record.epoch.n;
            let revision = u64::from(n).checked_add(1).ok_or(ExportError::Overflow)?;
            let last = index.checked_add(1) == Some(count);
            let position = sources.len();
            sources.push(json!({
                "source_id": id.as_str(),
                "epoch_id": n.to_string(),
                "role": role,
                "path": entry.path,
                "state": if last && !gone { "active" } else { "sealed" },
                "revision": revision,
                "observed_at_ns": captured,
                "captured_at_ns": captured,
                "object": object(record).map_err(|()| ExportError::Overflow)?,
                "optional": false,
                "associations": [],
            }));
            bodies.push(json!({
                "source_id": id.as_str(),
                "epoch_id": n.to_string(),
                "path": format!("bodies/{position:04}.bin"),
                "sha256": hex(record.sha256),
                "size": record.size,
            }));
            epochs.push(record);
        }
    }
    if bytes > max_bytes {
        return Err(ExportError::TooLarge);
    }
    if sources.len() > MAX_SOURCES {
        return Err(ExportError::TooManySources);
    }
    let envelope = json!({
        "contract": "session-reader/v2",
        "manifest_sha256": manifest_hex,
        "manifest": {
            "contract": "session-publication/v2",
            "identity": {
                "archive_id": key.archive.as_str(),
                "machine_id": key.machine.as_str(),
                "agent": key.agent.as_str(),
                "session_id": key.session.as_str(),
            },
            "publication_id": manifest_hex,
            "sequence": sequence,
            "observed_at_ns": captured,
            "captured_at_ns": captured,
            "published_at_ns": published,
            "coverage": {
                "discovery": "complete",
                "enrichment": "partial",
                "missing": ["snapshot-kinds-deferred"],
            },
            "sources": sources,
        },
        "bodies": bodies,
    });
    let metadata = serde_json::to_vec(&envelope).map_err(|_| ExportError::Overflow)?;
    if metadata.len() > MAX_METADATA {
        return Err(ExportError::MetadataTooLarge);
    }
    Ok(Envelope {
        metadata,
        sources: epochs.len(),
        epochs,
        deleted,
        bytes,
        skipped: 0,
    })
}

/// Render the `shared-reader/v1` envelope for a shared owner's `head`, checking every limit.
/// Touches no filesystem.
///
/// Only analytics sources are exported ([`shared_analytics`]); every other stored entry is left
/// out and counted in `skipped`. A source without epochs is left out (not counted). A deleted
/// source is exported with every epoch `sealed`. Bounds: [`MAX_SHARED_SOURCES`] sources,
/// [`MAX_METADATA`] bytes of metadata and `max_bytes` of bodies.
///
/// # Errors
/// A closed [`ExportError`] (never a `Fetch`).
pub fn build_shared_envelope<E>(
    head: &OwnerHead,
    max_bytes: u64,
) -> Result<Envelope<'_>, ExportError<E>> {
    let Owner::Shared {
        archive,
        machine,
        agent,
    } = &head.folded.owner
    else {
        return Err(ExportError::Session);
    };
    let sequence = head
        .folded
        .position
        .seq
        .checked_add(1)
        .ok_or(ExportError::Overflow)?;
    let captured = head.captured_at.unix_ns().ok_or(ExportError::BadTime)?;
    let published = head.updated_at.unix_ns().ok_or(ExportError::BadTime)?;
    let manifest_hex = hex(head.folded.position.manifest);

    let mut epochs: Vec<&EpochRecord> = Vec::new();
    let mut sources: Vec<Value> = Vec::new();
    let mut bodies: Vec<Value> = Vec::new();
    let mut bytes: u64 = 0;
    let mut deleted = 0;
    let mut skipped = 0;
    for (id, entry) in &head.folded.state.sources {
        if !shared_analytics(&entry.kind, &entry.path) {
            skipped += 1;
            continue;
        }
        if entry.epochs.is_empty() {
            continue;
        }
        let gone = entry.deleted_at.is_some();
        deleted += usize::from(gone);
        let count = entry.epochs.len();
        for (index, record) in entry.epochs.iter().enumerate() {
            bytes = bytes
                .checked_add(record.size)
                .ok_or(ExportError::TooLarge)?;
            let n = record.epoch.n;
            let revision = u64::from(n).checked_add(1).ok_or(ExportError::Overflow)?;
            let last = index.checked_add(1) == Some(count);
            let position = sources.len();
            sources.push(json!({
                "source_id": id.as_str(),
                "epoch_id": n.to_string(),
                "kind": "pi_run_record",
                "path": entry.path,
                "state": if last && !gone { "active" } else { "sealed" },
                "revision": revision,
                "captured_at_ns": captured,
                "object": object(record).map_err(|()| ExportError::Overflow)?,
            }));
            bodies.push(json!({
                "source_id": id.as_str(),
                "epoch_id": n.to_string(),
                "path": format!("bodies/{position:04}.bin"),
                "sha256": hex(record.sha256),
                "size": record.size,
            }));
            epochs.push(record);
        }
    }
    if bytes > max_bytes {
        return Err(ExportError::TooLarge);
    }
    if sources.len() > MAX_SHARED_SOURCES {
        return Err(ExportError::TooManySources);
    }
    let envelope = json!({
        "contract": "shared-reader/v1",
        "manifest_sha256": manifest_hex,
        "manifest": {
            "contract": "shared-publication/v1",
            "identity": {
                "archive_id": archive.as_str(),
                "machine_id": machine.as_str(),
                "agent": agent.as_str(),
            },
            "publication_id": manifest_hex,
            "sequence": sequence,
            "captured_at_ns": captured,
            "published_at_ns": published,
            "sources": sources,
        },
        "bodies": bodies,
    });
    let metadata = serde_json::to_vec(&envelope).map_err(|_| ExportError::Overflow)?;
    if metadata.len() > MAX_METADATA {
        return Err(ExportError::MetadataTooLarge);
    }
    Ok(Envelope {
        metadata,
        sources: epochs.len(),
        epochs,
        deleted,
        bytes,
        skipped,
    })
}

fn io<T>(r: std::io::Result<T>) -> Result<T, ExportError<()>> {
    r.map_err(|_| ExportError::Io)
}

fn new_file(path: &Path) -> std::io::Result<File> {
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)
}

/// Stream one epoch into `file`, verifying contiguity, size and digest.
fn write_body<E, B: AsRef<[u8]>>(
    record: &EpochRecord,
    file: &mut File,
    fetch: &mut impl FnMut(&str) -> Result<Option<B>, E>,
) -> Result<(), ExportError<E>> {
    let mut hasher = Sha256::new();
    let mut total: u64 = 0;
    for piece in &record.pieces {
        step(piece, total, file, &mut hasher, fetch)?;
        total = total.checked_add(piece.len()).ok_or(ExportError::Corrupt)?;
    }
    if total != record.size {
        return Err(ExportError::Corrupt);
    }
    if Digest::from_bytes(hasher.finalize().into()) != record.sha256 {
        return Err(ExportError::Corrupt);
    }
    file.sync_all().map_err(|_| ExportError::Io)
}

fn step<E, B: AsRef<[u8]>>(
    piece: &Piece,
    total: u64,
    file: &mut File,
    hasher: &mut Sha256,
    fetch: &mut impl FnMut(&str) -> Result<Option<B>, E>,
) -> Result<(), ExportError<E>> {
    if piece.offset().get() != total {
        return Err(ExportError::Corrupt);
    }
    let stored = fetch(piece.object())
        .map_err(ExportError::Fetch)?
        .ok_or(ExportError::ObjectMissing)?;
    let decoded = decode_piece(piece, stored.as_ref()).map_err(|_| ExportError::Corrupt);
    drop(stored);
    let decoded = decoded?;
    file.write_all(&decoded).map_err(|_| ExportError::Io)?;
    hasher.update(&decoded);
    Ok(())
}

fn publish(envelope: &Envelope<'_>, out: &Path) -> Result<(), ExportError<()>> {
    let tmp = out.join(".metadata.tmp");
    let mut file = io(new_file(&tmp))?;
    io(file.write_all(&envelope.metadata))?;
    io(file.sync_all())?;
    io(File::open(out.join("bodies")).and_then(|d| d.sync_all()))?;
    io(fs::rename(&tmp, out.join("metadata.json")))?;
    io(File::open(out).and_then(|d| d.sync_all()))?;
    if let Some(parent) = out.parent() {
        let parent = if parent.as_os_str().is_empty() {
            Path::new(".")
        } else {
            parent
        };
        io(File::open(parent).and_then(|d| d.sync_all()))?;
    }
    Ok(())
}

/// Write `envelope` and its bodies to `out`, which must not exist. `fetch` returns `Ok(None)` for
/// an absent key; its buffer type is anything viewable as bytes.
///
/// # Errors
/// A closed [`ExportError`]; after any failure past the destination check, what this run created
/// is removed.
pub fn write_export<E, B: AsRef<[u8]>>(
    envelope: &Envelope<'_>,
    mut fetch: impl FnMut(&str) -> Result<Option<B>, E>,
    out: &Path,
) -> Result<(), ExportError<E>> {
    match fs::symlink_metadata(out) {
        Ok(_) => return Err(ExportError::DestinationExists),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
        Err(_) => return Err(ExportError::Io),
    }
    DirBuilder::new()
        .mode(0o700)
        .create(out)
        .map_err(|_| ExportError::Io)?;
    let result = (|| {
        DirBuilder::new()
            .mode(0o700)
            .create(out.join("bodies"))
            .map_err(|_| ExportError::Io)?;
        for (index, record) in envelope.epochs.iter().enumerate() {
            let path = out.join("bodies").join(format!("{index:04}.bin"));
            let mut file = new_file(&path).map_err(|_| ExportError::Io)?;
            write_body(record, &mut file, &mut fetch)?;
        }
        publish(envelope, out).map_err(|_| ExportError::Io)
    })();
    if result.is_err() {
        let _ = fs::remove_dir_all(out);
    }
    result
}
