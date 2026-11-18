//! Export of one session owner's folded state as a `session-reader/v2` directory
//! (`metadata.json` plus `bodies/NNNN.bin`), the envelope the Python importer reads.
//!
//! [`build_envelope`] is pure: it checks every limit it can before any file exists and returns a
//! plan. [`write_export`] then streams each epoch body piece by piece (fetch, [`decode_piece`],
//! write, incremental SHA-256), renders `metadata.json` from the size and digest of what was
//! written, checks its size, and publishes it last. At most one stored piece, one decoded piece
//! and (for compressed epochs) the decoder window are alive at a time; it never calls
//! `restore_epoch`.
//!
//! A `CodexCompressed` epoch is verified as stored (contiguity, epoch size and SHA-256 over the
//! stored stream), and each decoded piece is fed to a streaming zstd decoder (window at most
//! 2^27 bytes) whose output is the body. The stream must decode completely: whole frames only,
//! no trailing bytes. The exported bytes count against the budget. Its `object.sha256` and
//! `object.size` and its body entry describe the exported (decompressed) body.
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
use memory_core::domain::{Owner, Sha256 as Digest, SourceKind};
use memory_core::locator::export_role;
use memory_core::manifest::{EpochRecord, manifest_hash};
use serde_json::{Value, json};
use sha2::{Digest as _, Sha256};
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::Write;
use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt};
use std::path::Path;
use zstd::stream::raw::{DParameter, Decoder, InBuffer, Operation, OutBuffer};

/// The importer's source bound.
pub const MAX_SOURCES: usize = 1024;
/// The importer's `metadata.json` bound.
pub const MAX_METADATA: usize = 8 << 20;

/// Why an export was refused or failed. Closed; carries no input text.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum ExportError<E> {
    /// The owner is not a session owner.
    #[error("owner_shared")]
    Shared,
    /// The sequence or a size does not fit the envelope.
    #[error("export_overflow")]
    Overflow,
    /// A stored time is not representable as nanoseconds.
    #[error("export_bad_time")]
    BadTime,
    /// The exported bytes exceed the budget.
    #[error("export_too_large")]
    TooLarge,
    /// More than [`MAX_SOURCES`] epochs.
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
    /// A compressed epoch is not one or more whole, valid zstd frames within the window bound.
    #[error("export_decompress")]
    Decompress,
}

/// The export plan: the envelope as the manifest describes it and the epochs whose bodies it
/// names, in body order. `metadata.json` is rendered by [`write_export`] after the bodies.
#[derive(Debug)]
pub struct Envelope<'a> {
    document: Value,
    max_bytes: u64,
    /// Epoch of each `bodies/NNNN.bin`, by index.
    pub epochs: Vec<&'a EpochRecord>,
    /// Whether each epoch is a compressed Codex rollout exported decompressed, by index.
    pub compressed: Vec<bool>,
    /// Live sources exported (epochs).
    pub sources: usize,
    /// Deleted sources skipped.
    pub skipped_deleted: usize,
    /// Sum of the stored epoch sizes (the up-front budget check; the exported total is what
    /// [`write_export`] returns).
    pub bytes: u64,
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

/// Plan the export for `head`, checking every limit that needs no body. Touches no filesystem.
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
    let mut compressed: Vec<bool> = Vec::new();
    let mut sources: Vec<Value> = Vec::new();
    let mut bodies: Vec<Value> = Vec::new();
    let mut bytes: u64 = 0;
    let mut skipped_deleted = 0;
    for (id, entry) in &head.folded.state.sources {
        if entry.deleted_at.is_some() {
            skipped_deleted += 1;
            continue;
        }
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
                "state": if last { "active" } else { "sealed" },
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
            compressed.push(entry.kind == SourceKind::CodexCompressed);
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
        document: envelope,
        max_bytes,
        sources: epochs.len(),
        epochs,
        compressed,
        skipped_deleted,
        bytes,
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

/// Largest decoder window accepted (Go's 128 MiB decoder memory).
const WINDOW_LOG_MAX: u32 = 27;
/// Decoded output is produced in chunks of this size.
const OUT_CHUNK: usize = 128 << 10;

/// What was written for one body.
#[derive(Debug, Clone, Copy)]
struct Written {
    size: u64,
    sha256: Digest,
}

/// The body file, its digest and the shared export budget.
struct Sink<'a> {
    file: &'a mut File,
    hasher: Sha256,
    size: u64,
    spent: &'a mut u64,
    max: u64,
}

impl Sink<'_> {
    fn put<E>(&mut self, bytes: &[u8]) -> Result<(), ExportError<E>> {
        let len = u64::try_from(bytes.len()).map_err(|_| ExportError::Overflow)?;
        *self.spent = self.spent.checked_add(len).ok_or(ExportError::TooLarge)?;
        if *self.spent > self.max {
            return Err(ExportError::TooLarge);
        }
        self.size = self.size.checked_add(len).ok_or(ExportError::TooLarge)?;
        self.file.write_all(bytes).map_err(|_| ExportError::Io)?;
        self.hasher.update(bytes);
        Ok(())
    }
}

/// Incremental zstd decoding of a stream of whole frames.
struct Inflater {
    decoder: Decoder<'static>,
    out: Vec<u8>,
    /// A frame has started and not ended.
    in_frame: bool,
    /// The last frame ended; the next input starts a new one.
    finished: bool,
}

impl Inflater {
    fn new() -> Option<Self> {
        let mut decoder = Decoder::new().ok()?;
        decoder
            .set_parameter(DParameter::WindowLogMax(WINDOW_LOG_MAX))
            .ok()?;
        Some(Self {
            decoder,
            out: Vec::with_capacity(OUT_CHUNK),
            in_frame: false,
            finished: false,
        })
    }

    fn feed<E>(&mut self, data: &[u8], sink: &mut Sink<'_>) -> Result<(), ExportError<E>> {
        let mut input = InBuffer::around(data);
        loop {
            if self.finished {
                if input.pos() >= data.len() {
                    return Ok(());
                }
                self.decoder.reinit().map_err(|_| ExportError::Decompress)?;
                self.finished = false;
            }
            self.out.clear();
            let before = input.pos();
            let (hint, full) = {
                let mut output = OutBuffer::around(&mut self.out);
                let hint = self
                    .decoder
                    .run(&mut input, &mut output)
                    .map_err(|_| ExportError::Decompress)?;
                (hint, output.pos() == output.capacity())
            };
            self.in_frame = hint != 0;
            self.finished = hint == 0;
            sink.put(&self.out)?;
            if input.pos() >= data.len() && !full {
                return Ok(());
            }
            if input.pos() == before && self.out.is_empty() {
                return Err(ExportError::Decompress);
            }
        }
    }

    fn finish<E>(&self) -> Result<(), ExportError<E>> {
        if self.in_frame {
            Err(ExportError::Decompress)
        } else {
            Ok(())
        }
    }
}

/// Stream one epoch into `file`, verifying contiguity, size and digest of the stored stream;
/// a compressed epoch is also decompressed, and the body is what the decoder produced.
fn write_body<E, B: AsRef<[u8]>>(
    record: &EpochRecord,
    compressed: bool,
    file: &mut File,
    spent: &mut u64,
    max: u64,
    fetch: &mut impl FnMut(&str) -> Result<Option<B>, E>,
) -> Result<Written, ExportError<E>> {
    let mut sink = Sink {
        file,
        hasher: Sha256::new(),
        size: 0,
        spent,
        max,
    };
    let mut inflater = if compressed {
        Some(Inflater::new().ok_or(ExportError::Decompress)?)
    } else {
        None
    };
    let mut stored_hasher = Sha256::new();
    let mut total: u64 = 0;
    for piece in &record.pieces {
        if piece.offset().get() != total {
            return Err(ExportError::Corrupt);
        }
        let stored = fetch(piece.object())
            .map_err(ExportError::Fetch)?
            .ok_or(ExportError::ObjectMissing)?;
        let decoded = decode_piece(piece, stored.as_ref()).map_err(|_| ExportError::Corrupt);
        drop(stored);
        let decoded = decoded?;
        stored_hasher.update(&decoded);
        match inflater.as_mut() {
            Some(inflater) => inflater.feed(&decoded, &mut sink)?,
            None => sink.put(&decoded)?,
        }
        total = total.checked_add(piece.len()).ok_or(ExportError::Corrupt)?;
    }
    if total != record.size {
        return Err(ExportError::Corrupt);
    }
    if Digest::from_bytes(stored_hasher.finalize().into()) != record.sha256 {
        return Err(ExportError::Corrupt);
    }
    if let Some(inflater) = &inflater {
        inflater.finish()?;
    }
    sink.file.sync_all().map_err(|_| ExportError::Io)?;
    Ok(Written {
        size: sink.size,
        sha256: Digest::from_bytes(sink.hasher.finalize().into()),
    })
}

/// Render `metadata.json` from the plan and what was written, checking its size.
fn render<E>(envelope: &Envelope<'_>, written: &[Written]) -> Result<Vec<u8>, ExportError<E>> {
    let mut document = envelope.document.clone();
    for (index, (w, compressed)) in written.iter().zip(&envelope.compressed).enumerate() {
        if !compressed {
            continue;
        }
        let digest = Value::String(hex(w.sha256));
        let size = Value::from(w.size);
        for (path, value) in [
            (format!("/manifest/sources/{index}/object/sha256"), &digest),
            (format!("/manifest/sources/{index}/object/size"), &size),
            (format!("/bodies/{index}/sha256"), &digest),
            (format!("/bodies/{index}/size"), &size),
        ] {
            *document.pointer_mut(&path).ok_or(ExportError::Overflow)? = value.clone();
        }
    }
    let metadata = serde_json::to_vec(&document).map_err(|_| ExportError::Overflow)?;
    if metadata.len() > MAX_METADATA {
        return Err(ExportError::MetadataTooLarge);
    }
    Ok(metadata)
}

fn publish(metadata: &[u8], out: &Path) -> Result<(), ExportError<()>> {
    let tmp = out.join(".metadata.tmp");
    let mut file = io(new_file(&tmp))?;
    io(file.write_all(metadata))?;
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

/// Write the bodies of `envelope`, then its `metadata.json`, to `out`, which must not exist.
/// `fetch` returns `Ok(None)` for an absent key; its buffer type is anything viewable as bytes.
/// Returns the exported (decompressed where applicable) byte total.
///
/// # Errors
/// A closed [`ExportError`]; after any failure past the destination check, what this run created
/// is removed.
pub fn write_export<E, B: AsRef<[u8]>>(
    envelope: &Envelope<'_>,
    mut fetch: impl FnMut(&str) -> Result<Option<B>, E>,
    out: &Path,
) -> Result<u64, ExportError<E>> {
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
        let mut spent: u64 = 0;
        let mut written = Vec::with_capacity(envelope.epochs.len());
        for (index, (record, compressed)) in
            envelope.epochs.iter().zip(&envelope.compressed).enumerate()
        {
            let path = out.join("bodies").join(format!("{index:04}.bin"));
            let mut file = new_file(&path).map_err(|_| ExportError::Io)?;
            written.push(write_body(
                record,
                *compressed,
                &mut file,
                &mut spent,
                envelope.max_bytes,
                &mut fetch,
            )?);
        }
        let metadata = render(envelope, &written)?;
        publish(&metadata, out).map_err(|_| ExportError::Io)?;
        Ok(spent)
    })();
    if result.is_err() {
        let _ = fs::remove_dir_all(out);
    }
    result
}
