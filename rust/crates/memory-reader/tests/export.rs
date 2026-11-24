//! `build_envelope` and `write_export` against heads built in memory: one test per decision row,
//! the importer's key sets, streaming, corruption, limits and the destination contract.
use memory_core::domain::{
    Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey, Sha256, SourceClass, SourceId,
    SourceKind,
};
use memory_core::manifest::{
    EpochRecord, EpochStamp, EpochWriter, Folded, OwnerState, Piece, Position, SourceEntry,
    Timestamp,
};
use memory_reader::export::{ExportError, build_envelope, build_shared_envelope, write_export};
use memory_reader::{OwnerHead, restore_epoch};
use serde_json::Value;
use std::cell::Cell;
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::rc::Rc;

mod common;
use common::{R, enc, hex, piece, piece_json};

type Objects = HashMap<String, Vec<u8>>;

fn bad<T: std::fmt::Debug>(e: T) -> String {
    format!("{e:?}")
}
fn owner() -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("arch".to_owned()).map_err(bad)?,
        machine: MachineId::try_from("mach".to_owned()).map_err(bad)?,
        agent: Agent::Claude,
        session: SessionId::try_from("sess-1".to_owned()).map_err(bad)?,
    }))
}
fn ts(s: &str) -> R<Timestamp> {
    Timestamp::try_from(s.to_owned()).map_err(bad)
}
fn sha(b: &[u8]) -> R<Sha256> {
    Sha256::try_from(hex(b)).map_err(bad)
}
fn tmp(label: &str) -> R<PathBuf> {
    let dir = std::env::temp_dir().join(format!("b32-export-{}-{label}", std::process::id()));
    let _ = fs::remove_dir_all(&dir);
    fs::create_dir_all(&dir).map_err(bad)?;
    Ok(dir)
}
fn chunk(tag: usize, i: usize) -> Vec<u8> {
    format!("chunk {tag}-{i} ").repeat(30 + i).into_bytes()
}
fn record(n: u32, size: u64, sha256: Sha256, pieces: Vec<Piece>) -> EpochRecord {
    EpochRecord {
        epoch: EpochStamp {
            n,
            writer: EpochWriter {
                version: "v".to_owned(),
                commit: "c".to_owned(),
            },
            rules: None,
        },
        size,
        sha256,
        pieces,
    }
}
/// An epoch whose pieces are `chunks` in order; their stored objects go into `objects`.
fn epoch(n: u32, chunks: &[Vec<u8>], objects: &mut Objects) -> R<EpochRecord> {
    let mut whole: Vec<u8> = Vec::new();
    let mut pieces = Vec::new();
    for c in chunks {
        let stored = enc(c)?;
        let p = piece(c, whole.len() as u64, &stored)?;
        objects.insert(p.object().to_owned(), stored);
        whole.extend(c);
        pieces.push(p);
    }
    Ok(record(n, whole.len() as u64, sha(&whole)?, pieces))
}
fn entry(kind: SourceKind, path: &str, epochs: Vec<EpochRecord>, deleted: bool) -> R<SourceEntry> {
    Ok(SourceEntry {
        kind,
        path: path.to_owned(),
        class: SourceClass::Append,
        epochs,
        deleted_at: if deleted {
            Some(ts("2026-11-07T10:00:00Z")?)
        } else {
            None
        },
    })
}
fn head(entries: Vec<(&str, SourceEntry)>, seq: u64) -> R<OwnerHead> {
    let mut sources = BTreeMap::new();
    for (id, en) in entries {
        sources.insert(SourceId::try_from(id.to_owned()).map_err(bad)?, en);
    }
    Ok(OwnerHead {
        folded: Folded {
            owner: owner()?,
            state: OwnerState { sources },
            position: Position {
                seq,
                manifest: sha(b"manifest")?,
                checkpoint: sha(b"checkpoint")?,
            },
        },
        captured_at: ts("2026-11-07T10:30:00Z")?,
        updated_at: ts("2026-11-07T10:45:00Z")?,
    })
}
fn export(h: &OwnerHead, max: u64, objects: &Objects, out: &Path) -> Result<(), ExportError<()>> {
    let env = build_envelope::<()>(h, max)?;
    write_export(&env, |k: &str| Ok::<_, ()>(objects.get(k).cloned()), out)
}
fn meta(out: &Path) -> R<Value> {
    serde_json::from_slice(&fs::read(out.join("metadata.json")).map_err(bad)?).map_err(bad)
}
fn mode(p: &Path) -> R<u32> {
    Ok(fs::metadata(p).map_err(bad)?.permissions().mode() & 0o777)
}
fn names(dir: &Path) -> R<Vec<String>> {
    let mut v: Vec<String> = fs::read_dir(dir)
        .map_err(bad)?
        .map(|x| x.map(|x| x.file_name().to_string_lossy().into_owned()))
        .collect::<Result<_, _>>()
        .map_err(bad)?;
    v.sort();
    Ok(v)
}
fn s(v: &Value) -> R<&str> {
    v.as_str().ok_or_else(|| format!("not a string: {v}"))
}
fn keys(v: &Value) -> R<BTreeSet<&str>> {
    Ok(v.as_object()
        .ok_or("not an object")?
        .keys()
        .map(String::as_str)
        .collect())
}
fn set(items: &[&'static str]) -> BTreeSet<&'static str> {
    items.iter().copied().collect()
}

/// One live source's epochs: (n, chunks).
type Spec = Vec<(u32, Vec<Vec<u8>>)>;

struct Fx {
    head: OwnerHead,
    objects: Objects,
    /// Expected (source id, role, path, specs) of every exported source, in order.
    live: Vec<(&'static str, &'static str, &'static str, Spec)>,
}

fn fixture() -> R<Fx> {
    let mut objects = Objects::new();
    let specs: Vec<(
        &'static str,
        SourceKind,
        &'static str,
        &'static str,
        bool,
        Spec,
    )> = vec![
        (
            "a",
            SourceKind::ClaudeSubagent,
            "subagent",
            "p/s/subagents/agent-1.jsonl",
            false,
            vec![(0, vec![chunk(1, 0)])],
        ),
        (
            "b",
            SourceKind::ClaudeTranscript,
            "main",
            "p/sess-1.jsonl",
            false,
            vec![
                (0, vec![chunk(2, 0)]),
                (1, (0..3).map(|i| chunk(3, i)).collect()),
            ],
        ),
        (
            "c",
            SourceKind::ClaudeTranscript,
            "main",
            "p/gone.jsonl",
            true,
            vec![(0, vec![chunk(4, 0)])],
        ),
        (
            "d",
            SourceKind::CodexCompressed,
            "artifact",
            "sessions/rollout-x.jsonl.zst",
            false,
            vec![(0, vec![])],
        ),
        (
            "e",
            SourceKind::ClaudeTranscript,
            "transcript",
            "p/old.1.jsonl",
            false,
            vec![(0, vec![chunk(5, 0), chunk(5, 1)])],
        ),
    ];
    let mut entries = Vec::new();
    let mut live = Vec::new();
    for (id, kind, role, path, deleted, spec) in specs {
        let mut epochs = Vec::new();
        for (n, chunks) in &spec {
            epochs.push(epoch(*n, chunks, &mut objects)?);
        }
        entries.push((id, entry(kind, path, epochs, deleted)?));
        // A deleted source is exported too (sessions are never deleted), all `sealed`.
        live.push((id, role, path, spec));
    }
    Ok(Fx {
        head: head(entries, 4)?,
        objects,
        live,
    })
}

/// The exported sources flattened: (id, role, path, state, n, chunks).
type Flat = (
    &'static str,
    &'static str,
    &'static str,
    &'static str,
    u32,
    Vec<Vec<u8>>,
);
fn flat(fx: &Fx) -> Vec<Flat> {
    let mut v = Vec::new();
    for (id, role, path, spec) in &fx.live {
        for (i, (n, chunks)) in spec.iter().enumerate() {
            let state = if i + 1 == spec.len() && *id != "c" {
                "active"
            } else {
                "sealed"
            };
            v.push((*id, *role, *path, state, *n, chunks.clone()));
        }
    }
    v
}

fn exported() -> R<(Fx, PathBuf, PathBuf, Value)> {
    let fx = fixture()?;
    let base = tmp("fixture")?;
    let out = base.join("out");
    export(&fx.head, 1 << 30, &fx.objects, &out).map_err(bad)?;
    let m = meta(&out)?;
    Ok((fx, base, out, m))
}

#[test]
fn unit_identity_digest_sequence_times_and_coverage() -> R {
    let (fx, base, _out, m) = exported()?;
    assert_eq!(at(&m, "contract"), "session-reader/v2");
    let manifest_hex = String::from(fx.head.folded.position.manifest);
    assert_eq!(at(&m, "manifest_sha256"), manifest_hex);
    assert_eq!(at(&at(&m, "manifest"), "publication_id"), manifest_hex);
    assert_eq!(
        at(&at(&m, "manifest"), "contract"),
        "session-publication/v2"
    );
    assert_eq!(
        at(&at(&m, "manifest"), "sequence"),
        5,
        "seq 4 -> sequence 5"
    );
    // 2026-11-07T10:30:00Z and 10:45:00Z, independently computed.
    assert_eq!(
        at(&at(&m, "manifest"), "captured_at_ns"),
        1_794_047_400_000_000_000u64
    );
    assert_eq!(
        at(&at(&m, "manifest"), "observed_at_ns"),
        1_794_047_400_000_000_000u64
    );
    assert_eq!(
        at(&at(&m, "manifest"), "published_at_ns"),
        1_794_048_300_000_000_000u64
    );
    assert_eq!(
        at(&at(&m, "manifest"), "identity"),
        serde_json::json!({"archive_id":"arch","machine_id":"mach","agent":"claude","session_id":"sess-1"})
    );
    assert_eq!(
        at(&at(&m, "manifest"), "coverage"),
        serde_json::json!({"discovery":"complete","enrichment":"partial","missing":["snapshot-kinds-deferred"]})
    );
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn sources_one_per_epoch_ordered_deleted_sealed_with_role_state_revision() -> R {
    let (fx, base, _out, m) = exported()?;
    let want = flat(&fx);
    let got = m
        .pointer("/manifest/sources")
        .and_then(Value::as_array)
        .ok_or("sources")?;
    assert_eq!(got.len(), 6, "a/0, b/0, b/1, c/0, d/0, e/0");
    assert_eq!(got.len(), want.len());
    for (g, (id, role, path, state, n, _)) in got.iter().zip(&want) {
        assert_eq!(at(g, "source_id"), *id);
        assert_eq!(at(g, "path"), *path);
        assert_eq!(at(g, "role"), *role);
        assert_eq!(at(g, "state"), *state);
        assert_eq!(at(g, "epoch_id"), n.to_string());
        assert_eq!(at(g, "revision"), u64::from(*n) + 1);
        assert_eq!(at(g, "optional"), false);
        assert_eq!(at(g, "associations"), serde_json::json!([]));
        assert!(g.get("withheld").is_none());
        assert_eq!(at(g, "observed_at_ns"), 1_794_047_400_000_000_000u64);
        assert_eq!(at(g, "captured_at_ns"), 1_794_047_400_000_000_000u64);
    }
    let order: Vec<(&str, &str)> = want.iter().map(|w| (w.0, w.3)).collect();
    assert_eq!(
        order,
        [
            ("a", "active"),
            ("b", "sealed"),
            ("b", "active"),
            ("c", "sealed"),
            ("d", "active"),
            ("e", "active")
        ]
    );
    assert!(
        got.iter()
            .all(|g| at(g, "source_id") != "c" || at(g, "state") == "sealed"),
        "a deleted source is never active"
    );
    let env = build_envelope::<()>(&fx.head, 1 << 30).map_err(bad)?;
    assert_eq!((env.sources, env.deleted), (6, 1));
    let total: u64 = want
        .iter()
        .map(|w| w.5.iter().map(|c| c.len() as u64).sum::<u64>())
        .sum();
    assert_eq!(env.bytes, total);
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn object_descriptors_for_zero_one_and_several_pieces() -> R {
    let (fx, base, _out, m) = exported()?;
    let want = flat(&fx);
    let got = m
        .pointer("/manifest/sources")
        .and_then(Value::as_array)
        .ok_or("sources")?;
    for (g, (_, _, _, _, _, chunks)) in got.iter().zip(&want) {
        let o = &at(g, "object");
        let whole: Vec<u8> = chunks.concat();
        assert_eq!(at(o, "encoding"), "zstd");
        assert_eq!(at(o, "sha256"), hex(&whole));
        assert_eq!(at(o, "size"), whole.len() as u64);
        let stored: Vec<Vec<u8>> = chunks.iter().map(|c| enc(c)).collect::<R<_>>()?;
        match stored.as_slice() {
            [] => {
                assert_eq!(at(o, "key"), "objects/none");
                assert_eq!(at(o, "encoded_size"), 0);
                assert_eq!(at(o, "encoded_sha256"), hex(b""));
            }
            [one] => {
                assert_eq!(
                    at(o, "key"),
                    format!(
                        "objects/{}.zst",
                        hex(chunks.first().map_or(&[][..], Vec::as_slice))
                    )
                );
                assert_eq!(at(o, "encoded_size"), one.len() as u64);
                assert_eq!(at(o, "encoded_sha256"), hex(one));
            }
            many => {
                assert_eq!(
                    at(o, "key"),
                    format!(
                        "objects/{}.zst",
                        hex(chunks.first().map_or(&[][..], Vec::as_slice))
                    )
                );
                let size: u64 = many.iter().map(|x| x.len() as u64).sum();
                assert_eq!(at(o, "encoded_size"), size);
                // Commitment: sha256 of the pieces' encoded digests (hex text) in order.
                let concat: String = many.iter().map(|x| hex(x)).collect();
                assert_eq!(at(o, "encoded_sha256"), hex(concat.as_bytes()));
                let reversed: String = many.iter().rev().map(|x| hex(x)).collect();
                assert_ne!(hex(reversed.as_bytes()), hex(concat.as_bytes()));
            }
        }
    }
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn bodies_paths_digests_and_destination_contract() -> R {
    let (fx, base, out, m) = exported()?;
    let want = flat(&fx);
    let bodies = m
        .pointer("/bodies")
        .and_then(Value::as_array)
        .ok_or("bodies")?;
    assert_eq!(bodies.len(), want.len());
    for (i, (b, (id, _, _, _, n, chunks))) in bodies.iter().zip(&want).enumerate() {
        let whole: Vec<u8> = chunks.concat();
        assert_eq!(at(b, "source_id"), *id);
        assert_eq!(at(b, "epoch_id"), n.to_string());
        assert_eq!(at(b, "path"), format!("bodies/{i:04}.bin"));
        assert_eq!(at(b, "sha256"), hex(&whole));
        assert_eq!(at(b, "size"), whole.len() as u64);
        let file = out.join(s(&at(b, "path"))?);
        assert_eq!(fs::read(&file).map_err(bad)?, whole);
        assert_eq!(mode(&file)?, 0o600);
    }
    assert_eq!(mode(&out)?, 0o700);
    assert_eq!(mode(&out.join("bodies"))?, 0o700);
    assert_eq!(mode(&out.join("metadata.json"))?, 0o600);
    assert_eq!(names(&out)?, ["bodies", "metadata.json"]);
    assert_eq!(names(&out.join("bodies"))?.len(), want.len());
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn metadata_key_sets_are_exactly_the_importers() -> R {
    let (_fx, base, _out, m) = exported()?;
    assert_eq!(
        keys(&m)?,
        set(&["contract", "manifest_sha256", "manifest", "bodies"])
    );
    let man = &at(&m, "manifest");
    assert_eq!(
        keys(man)?,
        set(&[
            "contract",
            "identity",
            "publication_id",
            "sequence",
            "observed_at_ns",
            "captured_at_ns",
            "published_at_ns",
            "coverage",
            "sources"
        ])
    );
    assert_eq!(
        keys(&at(man, "identity"))?,
        set(&["archive_id", "machine_id", "agent", "session_id"])
    );
    assert_eq!(
        keys(&at(man, "coverage"))?,
        set(&["discovery", "enrichment", "missing"])
    );
    // _SOURCE minus withheld.
    let source_keys = set(&[
        "source_id",
        "epoch_id",
        "role",
        "path",
        "state",
        "revision",
        "observed_at_ns",
        "captured_at_ns",
        "object",
        "optional",
        "associations",
    ]);
    let object_keys = set(&[
        "key",
        "encoding",
        "sha256",
        "encoded_sha256",
        "size",
        "encoded_size",
    ]);
    let body_keys = set(&["source_id", "epoch_id", "path", "sha256", "size"]);
    for src in man
        .pointer("/sources")
        .and_then(Value::as_array)
        .ok_or("sources")?
    {
        assert_eq!(keys(src)?, source_keys);
        assert_eq!(keys(&at(src, "object"))?, object_keys);
    }
    for b in m
        .pointer("/bodies")
        .and_then(Value::as_array)
        .ok_or("bodies")?
    {
        assert_eq!(keys(b)?, body_keys);
    }
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn bodies_equal_restore_epoch_for_0_1_3_and_70_pieces() -> R {
    for count in [0usize, 1, 3, 70] {
        let mut objects = Objects::new();
        let chunks: Vec<Vec<u8>> = (0..count).map(|i| chunk(9, i)).collect();
        let rec = epoch(0, &chunks, &mut objects)?;
        let want = restore_epoch(&rec, |k| objects.get(k).cloned().ok_or(())).map_err(bad)?;
        let h = head(
            vec![(
                "a",
                entry(SourceKind::ClaudeTranscript, "p/x.jsonl", vec![rec], false)?,
            )],
            0,
        )?;
        let base = tmp(&format!("bodies-{count}"))?;
        let out = base.join("out");
        export(&h, 1 << 30, &objects, &out).map_err(bad)?;
        assert_eq!(
            fs::read(out.join("bodies/0000.bin")).map_err(bad)?,
            want,
            "{count} pieces"
        );
        assert_eq!(want, chunks.concat());
        let _ = fs::remove_dir_all(&base);
    }
    Ok(())
}

struct Tracked {
    data: Vec<u8>,
    live: Rc<Cell<usize>>,
}
impl AsRef<[u8]> for Tracked {
    fn as_ref(&self) -> &[u8] {
        &self.data
    }
}
impl Drop for Tracked {
    fn drop(&mut self) {
        self.live.set(self.live.get().saturating_sub(1));
    }
}

#[test]
fn a_70_piece_epoch_streams_with_at_most_one_stored_buffer_alive() -> R {
    let mut objects = Objects::new();
    let chunks: Vec<Vec<u8>> = (0..70).map(|i| chunk(7, i)).collect();
    let rec = epoch(0, &chunks, &mut objects)?;
    let h = head(
        vec![(
            "a",
            entry(SourceKind::ClaudeTranscript, "p/x.jsonl", vec![rec], false)?,
        )],
        0,
    )?;
    let base = tmp("stream")?;
    let out = base.join("out");
    let env = build_envelope::<()>(&h, 1 << 30).map_err(bad)?;
    let live = Rc::new(Cell::new(0usize));
    let (before_max, peak, calls) = (Cell::new(0usize), Cell::new(0usize), Cell::new(0usize));
    let fetch = |k: &str| {
        calls.set(calls.get() + 1);
        before_max.set(before_max.get().max(live.get()));
        let data = objects.get(k).cloned().ok_or(())?;
        live.set(live.get() + 1);
        peak.set(peak.get().max(live.get()));
        Ok::<_, ()>(Some(Tracked {
            data,
            live: Rc::clone(&live),
        }))
    };
    write_export(&env, fetch, &out).map_err(bad)?;
    assert_eq!(calls.get(), 70);
    assert_eq!(before_max.get(), 0, "a stored buffer was alive at a fetch");
    assert_eq!(peak.get(), 1);
    assert_eq!(live.get(), 0);
    assert_eq!(
        fs::read(out.join("bodies/0000.bin")).map_err(bad)?,
        chunks.concat()
    );
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

/// A good first source `a` and a bad second source `b` built by `mk`.
fn bad_case(
    label: &str,
    expect: ExportError<()>,
    mk: impl FnOnce(&[u8], &[u8], &mut Objects) -> R<EpochRecord>,
) -> R {
    let mut objects = Objects::new();
    let good = epoch(0, &[chunk(1, 0)], &mut objects)?;
    let raw = chunk(8, 0);
    let stored = enc(&raw)?;
    let bad_rec = mk(&raw, &stored, &mut objects)?;
    let h = head(
        vec![
            (
                "a",
                entry(SourceKind::ClaudeTranscript, "p/a.jsonl", vec![good], false)?,
            ),
            (
                "b",
                entry(
                    SourceKind::ClaudeTranscript,
                    "p/b.jsonl",
                    vec![bad_rec],
                    false,
                )?,
            ),
        ],
        0,
    )?;
    let base = tmp(&format!("bad-{label}"))?;
    let out = base.join("out");
    assert_eq!(export(&h, 1 << 30, &objects, &out), Err(expect), "{label}");
    assert!(!out.exists(), "{label} left a directory");
    assert_eq!(names(&base)?.len(), 0, "{label} left something beside out");
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

fn custom(raw: &[u8], stored: &[u8], f: impl FnOnce(&mut Value)) -> R<EpochRecord> {
    let mut j = piece_json(raw, 0, stored);
    f(&mut j);
    let p: Piece = serde_json::from_value(j).map_err(bad)?;
    Ok(record(0, raw.len() as u64, sha(raw)?, vec![p]))
}
fn put(v: &mut Value, k: &str, x: Value) {
    if let Some(o) = v.as_object_mut() {
        o.insert(k.to_owned(), x);
    }
}

#[test]
fn every_decode_corruption_a_missing_object_a_gap_and_bad_epoch_totals_leave_nothing() -> R {
    let c = ExportError::Corrupt;
    bad_case("stored-length", c.clone(), |raw, stored, o| {
        let r = custom(raw, stored, |_| {})?;
        let mut longer = stored.to_vec();
        longer.push(0);
        o.insert(
            r.pieces
                .first()
                .map(|p| p.object().to_owned())
                .unwrap_or_default(),
            longer,
        );
        Ok(r)
    })?;
    bad_case("stored-digest", c.clone(), |raw, stored, o| {
        let r = custom(raw, stored, |_| {})?;
        let mut flipped = stored.to_vec();
        if let Some(b) = flipped.first_mut() {
            *b ^= 1;
        }
        o.insert(
            r.pieces
                .first()
                .map(|p| p.object().to_owned())
                .unwrap_or_default(),
            flipped,
        );
        Ok(r)
    })?;
    bad_case("bad-zstd", c.clone(), |raw, _, o| {
        let garbage = b"definitely not a zstd frame".to_vec();
        let r = custom(raw, &garbage, |_| {})?;
        o.insert(
            r.pieces
                .first()
                .map(|p| p.object().to_owned())
                .unwrap_or_default(),
            garbage,
        );
        Ok(r)
    })?;
    let with_stored = |f: fn(&mut Value, usize)| {
        move |raw: &[u8], stored: &[u8], o: &mut Objects| -> R<EpochRecord> {
            let n = raw.len();
            let r = custom(raw, stored, |j| f(j, n))?;
            o.insert(
                r.pieces
                    .first()
                    .map(|p| p.object().to_owned())
                    .unwrap_or_default(),
                stored.to_vec(),
            );
            Ok(r)
        }
    };
    bad_case(
        "too-long",
        c.clone(),
        with_stored(|j, n| put(j, "len", (n - 1).into())),
    )?;
    bad_case(
        "raw-length",
        c.clone(),
        with_stored(|j, n| put(j, "len", (n + 1).into())),
    )?;
    bad_case(
        "raw-digest",
        c.clone(),
        with_stored(|j, _| put(j, "sha256", hex(b"other").into())),
    )?;
    bad_case(
        "gap",
        c.clone(),
        with_stored(|j, _| put(j, "offset", 3.into())),
    )?;
    bad_case("epoch-size", c.clone(), |raw, stored, o| {
        let mut r = custom(raw, stored, |_| {})?;
        o.insert(
            r.pieces
                .first()
                .map(|p| p.object().to_owned())
                .unwrap_or_default(),
            stored.to_vec(),
        );
        r.size += 1;
        Ok(r)
    })?;
    bad_case("epoch-digest", c, |raw, stored, o| {
        let mut r = custom(raw, stored, |_| {})?;
        o.insert(
            r.pieces
                .first()
                .map(|p| p.object().to_owned())
                .unwrap_or_default(),
            stored.to_vec(),
        );
        r.sha256 = sha(b"other")?;
        Ok(r)
    })?;
    bad_case("missing", ExportError::ObjectMissing, |raw, stored, _| {
        custom(raw, stored, |_| {})
    })?;
    Ok(())
}

#[test]
fn a_fetch_error_passes_through_and_leaves_nothing() -> R {
    let mut objects = Objects::new();
    let rec = epoch(0, &[chunk(1, 0)], &mut objects)?;
    let h = head(
        vec![(
            "a",
            entry(SourceKind::ClaudeTranscript, "p/a.jsonl", vec![rec], false)?,
        )],
        0,
    )?;
    let base = tmp("fetch-err")?;
    let out = base.join("out");
    let env = build_envelope::<&str>(&h, 1 << 30).map_err(bad)?;
    let r = write_export(&env, |_: &str| Err::<Option<Vec<u8>>, _>("boom"), &out);
    assert_eq!(r, Err(ExportError::Fetch("boom")));
    assert!(!out.exists());
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

/// The composition `export_owner` performs, counting fetches.
fn limit_case(h: &OwnerHead, max: u64, want: ExportError<()>, label: &str) -> R {
    let base = tmp(&format!("limit-{label}"))?;
    let out = base.join("out");
    let calls = Cell::new(0);
    let r = build_envelope::<()>(h, max).and_then(|env| {
        write_export(
            &env,
            |_: &str| {
                calls.set(calls.get() + 1);
                Ok::<Option<Vec<u8>>, ()>(None)
            },
            &out,
        )
    });
    assert_eq!(r, Err(want), "{label}");
    assert_eq!(calls.get(), 0, "{label}: fetch called");
    assert!(!out.exists(), "{label}: out exists");
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

fn empties(count: u32) -> R<Vec<EpochRecord>> {
    (0..count)
        .map(|n| Ok(record(n, 0, sha(b"")?, vec![])))
        .collect()
}

#[test]
fn limits_refuse_before_anything_exists() -> R {
    let k = SourceKind::ClaudeTranscript;
    let at = |n| entry(k.clone(), "p/x.jsonl", empties(n)?, false);
    limit_case(
        &head(vec![("a", at(1025)?)], 0)?,
        1 << 30,
        ExportError::TooManySources,
        "1025",
    )?;
    // 1024 is allowed.
    let base = tmp("1024")?;
    export(
        &head(vec![("a", at(1024)?)], 0)?,
        1 << 30,
        &Objects::new(),
        &base.join("out"),
    )
    .map_err(bad)?;
    let _ = fs::remove_dir_all(&base);

    let big = |size: u64| record(0, size, Sha256::from_bytes([0; 32]), vec![]);
    let h = head(
        vec![
            ("a", entry(k.clone(), "p/a", vec![big(u64::MAX)], false)?),
            ("b", entry(k.clone(), "p/b", vec![big(1)], false)?),
        ],
        0,
    )?;
    limit_case(&h, u64::MAX, ExportError::TooLarge, "overflow")?;
    let h = head(
        vec![("a", entry(k.clone(), "p/a", vec![big(1001)], false)?)],
        0,
    )?;
    limit_case(&h, 1000, ExportError::TooLarge, "over-budget")?;
    // Exactly the budget is allowed.
    let base = tmp("exact")?;
    export(&h, 1001, &Objects::new(), &base.join("out"))
        .map_err(|e| format!("{e:?}"))
        .err();
    let _ = fs::remove_dir_all(&base);

    let long = "x".repeat(16 << 10);
    let entries = (0..600)
        .map(|i| {
            Ok((
                format!("s{i:04}"),
                entry(k.clone(), &long, empties(1)?, false)?,
            ))
        })
        .collect::<R<Vec<_>>>()?;
    let refs = entries
        .iter()
        .map(|(id, en)| (id.as_str(), en.clone()))
        .collect();
    limit_case(
        &head(refs, 0)?,
        1 << 30,
        ExportError::MetadataTooLarge,
        "8mib",
    )?;
    Ok(())
}

#[test]
fn a_budget_equal_to_the_bytes_is_allowed() -> R {
    let mut objects = Objects::new();
    let rec = epoch(0, &[chunk(1, 0)], &mut objects)?;
    let size = rec.size;
    let h = head(
        vec![(
            "a",
            entry(SourceKind::ClaudeTranscript, "p/a.jsonl", vec![rec], false)?,
        )],
        0,
    )?;
    let base = tmp("exact-budget")?;
    export(&h, size, &objects, &base.join("out")).map_err(bad)?;
    assert_eq!(
        export(&h, size - 1, &objects, &base.join("out2")),
        Err(ExportError::TooLarge)
    );
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn an_existing_destination_is_refused_and_left_unchanged() -> R {
    let fx = fixture()?;
    let base = tmp("exists")?;
    let dir = base.join("dir");
    fs::create_dir_all(&dir).map_err(bad)?;
    fs::write(dir.join("keep"), b"x").map_err(bad)?;
    let file = base.join("file");
    fs::write(&file, b"y").map_err(bad)?;
    let link = base.join("link");
    std::os::unix::fs::symlink(&dir, &link).map_err(bad)?;
    let dangling = base.join("dangling");
    std::os::unix::fs::symlink(base.join("nowhere"), &dangling).map_err(bad)?;
    for p in [&dir, &file, &link, &dangling] {
        assert_eq!(
            export(&fx.head, 1 << 30, &fx.objects, p),
            Err(ExportError::DestinationExists),
            "{p:?}"
        );
    }
    assert_eq!(names(&dir)?, ["keep"]);
    assert_eq!(fs::read(&file).map_err(bad)?, b"y");
    assert!(!base.join("nowhere").exists());
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn a_missing_parent_is_an_io_error_and_creates_nothing() -> R {
    let fx = fixture()?;
    let base = tmp("noparent")?;
    let out = base.join("missing").join("out");
    assert_eq!(
        export(&fx.head, 1 << 30, &fx.objects, &out),
        Err(ExportError::Io)
    );
    assert!(!base.join("missing").exists());
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn shared_owner_heads_and_overflowing_sequence_are_refused() -> R {
    let mut h = head(vec![], 0)?;
    h.folded.owner = memory_core::limits::parse_owner_prefix("shared/a/m/claude").map_err(bad)?;
    assert_eq!(
        build_envelope::<()>(&h, 1).map(|_| ()),
        Err(ExportError::Shared)
    );
    let h = head(vec![], u64::MAX)?;
    assert_eq!(
        build_envelope::<()>(&h, 1).map(|_| ()),
        Err(ExportError::Overflow)
    );
    let mut h = head(vec![], 0)?;
    h.captured_at = ts("1969-12-31T23:59:59Z")?;
    assert_eq!(
        build_envelope::<()>(&h, 1).map(|_| ()),
        Err(ExportError::BadTime)
    );
    Ok(())
}

fn at(v: &Value, k: &str) -> Value {
    v.get(k).cloned().unwrap_or(Value::Null)
}

const RUN: &str = "sessions/--c--/subagent-artifacts/run1.json";

fn g<'a>(v: &'a Value, k: &str) -> R<&'a Value> {
    v.get(k).ok_or_else(|| format!("missing {k}"))
}

fn shared_head(entries: Vec<(&str, SourceEntry)>) -> R<OwnerHead> {
    let mut h = head(entries, 6)?;
    h.folded.owner = memory_core::limits::parse_owner_prefix("shared/arch/mach/pi").map_err(bad)?;
    Ok(h)
}
fn shared_export(h: &OwnerHead, max: u64, objects: &Objects, out: &Path) -> R<usize> {
    let env = build_shared_envelope::<()>(h, max).map_err(bad)?;
    write_export(&env, |k: &str| Ok::<_, ()>(objects.get(k).cloned()), out).map_err(bad)?;
    Ok(env.skipped)
}

/// Brief 56: byte-exact `shared-reader/v1` metadata for one empty run record.
#[test]
fn shared_metadata_is_byte_exact() -> R {
    let rec = record(0, 0, sha(b"")?, vec![]);
    let h = shared_head(vec![(
        "a",
        entry(SourceKind::PiAux, RUN, vec![rec], false)?,
    )])?;
    let env = build_shared_envelope::<()>(&h, 1).map_err(bad)?;
    let m = hex(b"manifest");
    let want = serde_json::json!({
        "contract": "shared-reader/v1",
        "manifest_sha256": m,
        "manifest": {
            "contract": "shared-publication/v1",
            "identity": {"archive_id": "arch", "machine_id": "mach", "agent": "pi"},
            "publication_id": m,
            "sequence": 7,
            "captured_at_ns": 1_794_047_400_000_000_000_u64,
            "published_at_ns": 1_794_048_300_000_000_000_u64,
            "sources": [{
                "source_id": "a",
                "epoch_id": "0",
                "kind": "pi_run_record",
                "path": RUN,
                "state": "active",
                "revision": 1,
                "captured_at_ns": 1_794_047_400_000_000_000_u64,
                "object": {
                    "key": "objects/none",
                    "encoding": "zstd",
                    "sha256": hex(b""),
                    "encoded_sha256": hex(b""),
                    "size": 0,
                    "encoded_size": 0,
                },
            }],
        },
        "bodies": [{
            "source_id": "a",
            "epoch_id": "0",
            "path": "bodies/0000.bin",
            "sha256": hex(b""),
            "size": 0,
        }],
    });
    assert_eq!(env.metadata, serde_json::to_vec(&want).map_err(bad)?);
    assert_eq!(
        (env.sources, env.deleted, env.skipped, env.bytes),
        (1, 0, 0, 0)
    );
    Ok(())
}

/// Brief 56: retired kinds and non-run-record `PiAux` paths are skipped and counted; deleted
/// sources are exported sealed; a source without epochs is left out (not counted).
#[test]
fn shared_export_keeps_run_records_only() -> R {
    let mut objects = Objects::new();
    let live = epoch(0, &[chunk(1, 0)], &mut objects)?;
    let gone0 = epoch(0, &[chunk(2, 0)], &mut objects)?;
    let gone1 = epoch(1, &[chunk(2, 1)], &mut objects)?;
    let retired = epoch(0, &[chunk(3, 0)], &mut objects)?;
    let h = shared_head(vec![
        ("a", entry(SourceKind::PiAux, RUN, vec![live], false)?),
        (
            "b",
            entry(
                SourceKind::PiAux,
                "sessions/--c--/subagent-artifacts/run2.json",
                vec![gone0, gone1],
                true,
            )?,
        ),
        (
            "c",
            entry(
                SourceKind::PiConfig,
                "settings.json",
                vec![retired.clone()],
                false,
            )?,
        ),
        (
            "d",
            entry(
                SourceKind::PiAux,
                "sessions/--c--/other/x.json",
                vec![retired],
                false,
            )?,
        ),
        ("e", entry(SourceKind::PiAux, RUN, vec![], false)?),
        (
            "f",
            entry(SourceKind::ClaudeTranscript, "p/x.jsonl", vec![], false)?,
        ),
    ])?;
    let base = tmp("shared-keep")?;
    let out = base.join("out");
    let skipped = shared_export(&h, 1 << 30, &objects, &out)?;
    assert_eq!(skipped, 3);
    let m = meta(&out)?;
    let srcs = g(g(&m, "manifest")?, "sources")?
        .as_array()
        .ok_or("sources")?;
    let got: Vec<(&str, &str, &str)> = srcs
        .iter()
        .map(|x| {
            Ok((
                s(g(x, "source_id")?)?,
                s(g(x, "epoch_id")?)?,
                s(g(x, "state")?)?,
            ))
        })
        .collect::<R<_>>()?;
    assert_eq!(
        got,
        [
            ("a", "0", "active"),
            ("b", "0", "sealed"),
            ("b", "1", "sealed")
        ]
    );
    for x in srcs {
        assert_eq!(
            keys(x)?,
            set(&[
                "source_id",
                "epoch_id",
                "kind",
                "path",
                "state",
                "revision",
                "captured_at_ns",
                "object",
            ])
        );
        assert_eq!(s(g(x, "kind")?)?, "pi_run_record");
    }
    assert_eq!(
        keys(&m)?,
        set(&["contract", "manifest_sha256", "manifest", "bodies"])
    );
    assert_eq!(
        keys(g(&m, "manifest")?)?,
        set(&[
            "contract",
            "identity",
            "publication_id",
            "sequence",
            "captured_at_ns",
            "published_at_ns",
            "sources",
        ])
    );
    assert_eq!(
        keys(g(g(&m, "manifest")?, "identity")?)?,
        set(&["archive_id", "machine_id", "agent"])
    );
    let b = g(&m, "bodies")?.as_array().ok_or("bodies")?;
    assert_eq!(b.len(), 3);
    for x in b {
        assert_eq!(
            keys(x)?,
            set(&["source_id", "epoch_id", "path", "sha256", "size"])
        );
    }
    assert_eq!(
        names(&out.join("bodies"))?,
        ["0000.bin", "0001.bin", "0002.bin"]
    );
    let env = build_shared_envelope::<()>(&h, 1 << 30).map_err(bad)?;
    assert_eq!((env.sources, env.deleted, env.skipped), (3, 1, 3));
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

/// Brief 56: a shared owner with only retired entries exports valid and empty.
#[test]
fn shared_export_of_only_retired_entries_is_empty() -> R {
    let h = shared_head(vec![(
        "c",
        entry(
            SourceKind::PiConfig,
            "settings.json",
            vec![record(0, 0, sha(b"")?, vec![])],
            false,
        )?,
    )])?;
    let env = build_shared_envelope::<()>(&h, 1).map_err(bad)?;
    assert_eq!((env.sources, env.skipped, env.bytes), (0, 1, 0));
    let v: Value = serde_json::from_slice(&env.metadata).map_err(bad)?;
    assert_eq!(*g(g(&v, "manifest")?, "sources")?, serde_json::json!([]));
    assert_eq!(*g(&v, "bodies")?, serde_json::json!([]));
    Ok(())
}

/// Brief 56: the shared bounds (16,384 sources, 8 MiB metadata, byte budget) and owner kinds.
#[test]
fn shared_limits_and_owner_kinds() -> R {
    let empties_of = |n| entry(SourceKind::PiAux, RUN, empties(n)?, false);
    // 16,384 sources pass the source bound; their metadata (about 600 bytes each) is over
    // 8 MiB, so the metadata bound is the one that refuses it (brief 56 note).
    let h = shared_head(vec![("a", empties_of(16_384)?)])?;
    assert_eq!(
        build_shared_envelope::<()>(&h, 1).map(|_| ()),
        Err(ExportError::MetadataTooLarge)
    );
    let h = shared_head(vec![("a", empties_of(10_000)?)])?;
    assert!(build_shared_envelope::<()>(&h, 1).is_ok());
    let h = shared_head(vec![("a", empties_of(16_385)?)])?;
    assert_eq!(
        build_shared_envelope::<()>(&h, 1).map(|_| ()),
        Err(ExportError::TooManySources)
    );
    let long = format!(
        "sessions/--c--/subagent-artifacts/{}.json",
        "x".repeat(4000)
    );
    let entries = (0..3000)
        .map(|i| {
            Ok((
                format!("s{i:04}"),
                entry(SourceKind::PiAux, &long, empties(1)?, false)?,
            ))
        })
        .collect::<R<Vec<_>>>()?;
    let refs = entries
        .iter()
        .map(|(id, en)| (id.as_str(), en.clone()))
        .collect();
    assert_eq!(
        build_shared_envelope::<()>(&shared_head(refs)?, 1).map(|_| ()),
        Err(ExportError::MetadataTooLarge)
    );
    let big = |size: u64| record(0, size, Sha256::from_bytes([0; 32]), vec![]);
    let h = shared_head(vec![(
        "a",
        entry(SourceKind::PiAux, RUN, vec![big(11)], false)?,
    )])?;
    assert_eq!(
        build_shared_envelope::<()>(&h, 10).map(|_| ()),
        Err(ExportError::TooLarge)
    );
    assert!(build_shared_envelope::<()>(&h, 11).is_ok());
    // A retired entry's size never counts against the budget.
    let h = shared_head(vec![(
        "a",
        entry(
            SourceKind::PiConfig,
            "settings.json",
            vec![big(u64::MAX)],
            false,
        )?,
    )])?;
    assert!(build_shared_envelope::<()>(&h, 1).is_ok());
    // Owner kinds are not interchangeable.
    let h = head(vec![], 0)?;
    assert_eq!(
        build_shared_envelope::<()>(&h, 1).map(|_| ()),
        Err(ExportError::Session)
    );
    let mut h = shared_head(vec![])?;
    h.folded.position.seq = u64::MAX;
    assert_eq!(
        build_shared_envelope::<()>(&h, 1).map(|_| ()),
        Err(ExportError::Overflow)
    );
    Ok(())
}

/// Brief 56 (parent, teeth): a deleted run record with no epochs is left out entirely; it is
/// neither exported nor counted as deleted or skipped.
#[test]
fn shared_deleted_source_without_epochs_is_left_out() -> R {
    let mut objects = Objects::new();
    let live = epoch(0, &[chunk(1, 0)], &mut objects)?;
    let h = shared_head(vec![
        ("a", entry(SourceKind::PiAux, RUN, vec![live], false)?),
        (
            "b",
            entry(
                SourceKind::PiAux,
                "sessions/--c--/subagent-artifacts/run2.json",
                vec![],
                true,
            )?,
        ),
    ])?;
    let env = build_shared_envelope::<()>(&h, 1 << 30).map_err(bad)?;
    assert_eq!((env.sources, env.deleted, env.skipped), (1, 0, 0));
    Ok(())
}
