//! Manifest and current-pointer acceptance: golden wire shape, negative fixtures, schema
//! dispatch, recovery projection and model-based properties (DESIGN.md section 15, property 6).
use hegel::{TestCase, generators as gs};
use memory_core::domain::{
    Agent, ArchiveId, CommitOutcome, EpochPosition, MachineId, Offset, Owner, OwnerParkReason,
    RulesDigest, SessionId, SessionKey, Sha256, SourceClass, SourceId, SourceKind,
};
use memory_core::manifest::*;
use memory_core::privacy::sanitize_launch;
use proptest::prelude::*;
use serde_json::{Value, json};
use sha2::{Digest, Sha256 as Hasher};
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::sync::atomic::{AtomicU64, Ordering};

/// Independent counters of generated history shapes (coverage guard, DESIGN.md section 15).
struct Shapes {
    multi_epoch: AtomicU64,
    snapshot_replacement: AtomicU64,
    delete: AtomicU64,
    fold_across_checkpoint: AtomicU64,
}
impl Shapes {
    const fn new() -> Self {
        Self {
            multi_epoch: AtomicU64::new(0),
            snapshot_replacement: AtomicU64::new(0),
            delete: AtomicU64::new(0),
            fold_across_checkpoint: AtomicU64::new(0),
        }
    }
    fn counts(&self, with_fold: bool) -> Vec<(&'static str, u64)> {
        let load = |c: &AtomicU64| c.load(Ordering::Relaxed);
        let mut v = vec![
            ("multi-epoch", load(&self.multi_epoch)),
            ("snapshot-replacement", load(&self.snapshot_replacement)),
            ("delete", load(&self.delete)),
        ];
        if with_fold {
            v.push(("fold-across-checkpoint", load(&self.fold_across_checkpoint)));
        }
        v
    }
}
fn bump(c: &AtomicU64) {
    c.fetch_add(1, Ordering::Relaxed);
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
static HEGEL_ROUNDTRIP: Shapes = Shapes::new();
static PROPTEST_ROUNDTRIP: Shapes = Shapes::new();
static HEGEL_FOLD: Shapes = Shapes::new();
static PROPTEST_FOLD: Shapes = Shapes::new();

type R = Result<(), Box<dyn std::error::Error>>;

// ------------------------------------------------------------------ helpers

fn h(n: u64) -> Sha256 {
    Sha256::from_bytes(Hasher::digest(n.to_le_bytes()).into())
}
fn hex(n: u64) -> String {
    String::from(h(n))
}
fn session_owner() -> Result<Owner, Box<dyn std::error::Error>> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned())?,
        machine: MachineId::try_from("m".to_owned())?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned())?,
    }))
}
fn shared_owner() -> Result<Owner, Box<dyn std::error::Error>> {
    Ok(Owner::Shared {
        archive: ArchiveId::try_from("a".to_owned())?,
        machine: MachineId::try_from("m2".to_owned())?,
        agent: Agent::Codex,
    })
}
fn ts() -> Result<Timestamp, ManifestError> {
    Timestamp::try_from("2026-11-07T10:30:00Z".to_owned())
}
fn writer() -> Writer {
    Writer {
        name: "memory".to_owned(),
        version: "0.1.0".to_owned(),
        commit: "abc123".to_owned(),
    }
}
fn launch_record() -> Result<LaunchRecord, ManifestError> {
    let argv = ["claude", "--model", "x"].map(str::to_owned);
    LaunchRecord::from_sanitized(&sanitize_launch(&argv, &[]))
}
fn header_for(owner: Owner, with_launch: bool) -> Result<Header, Box<dyn std::error::Error>> {
    Ok(Header {
        owner,
        captured_at: ts()?,
        writer: writer(),
        launch: if with_launch {
            vec![launch_record()?]
        } else {
            vec![]
        },
    })
}
fn stamp(n: u32, rules: Option<u64>) -> EpochStamp {
    EpochStamp {
        n,
        writer: EpochWriter {
            version: "0.1.0".to_owned(),
            commit: "abc123".to_owned(),
        },
        rules: rules.map(|r| RulesDigest::new(h(r))),
    }
}
fn piece(c: u64, off: u64, len: u64) -> Piece {
    Piece::new(Offset::new(off), len, h(c), 1 + c % 50, h(c + 1_000_000))
}
fn kind_of(append: bool) -> SourceKind {
    if append {
        SourceKind::ClaudeTranscript
    } else {
        SourceKind::ClaudeTodos
    }
}
fn swrite(
    name: &str,
    append: bool,
    epoch: EpochStamp,
    size: u64,
    whole: Sha256,
    pieces: Vec<Piece>,
) -> Result<SourceWrite, Box<dyn std::error::Error>> {
    Ok(SourceWrite {
        id: SourceId::try_from(name.to_owned())?,
        kind: kind_of(append),
        path: format!("p/{name}"),
        class: if append {
            SourceClass::Append
        } else {
            SourceClass::Snapshot
        },
        epoch,
        size,
        sha256: whole,
        pieces,
    })
}

/// Checkpoint seq 0 with an append source "t" and a snapshot source "c", then a delta that
/// extends "t" with two pieces and carries one launch record.
fn fixture() -> Result<(Built, Built), Box<dyn std::error::Error>> {
    let w0 = swrite("t", true, stamp(1, None), 10, h(1), vec![piece(1, 0, 10)])?;
    let w2 = swrite("c", false, stamp(1, Some(9)), 4, h(2), vec![piece(2, 0, 4)])?;
    let b0 = build(
        None,
        header_for(session_owner()?, false)?,
        vec![Change::Epoch(w0), Change::Replace(w2)],
    )?;
    let w1 = swrite(
        "t",
        true,
        stamp(1, None),
        30,
        h(3),
        vec![piece(4, 10, 5), piece(5, 15, 15)],
    )?;
    let b1 = build(
        Some((&b0.position, &b0.state)),
        header_for(session_owner()?, true)?,
        vec![Change::Extend(w1)],
    )?;
    Ok((b0, b1))
}

fn mutate(bytes: &[u8], f: impl FnOnce(&mut Value)) -> Result<Vec<u8>, serde_json::Error> {
    let mut v: Value = serde_json::from_slice(bytes)?;
    f(&mut v);
    serde_json::to_vec(&v)
}
fn put(v: &mut Value, parent: &str, key: &str, to: Value) {
    if let Some(Value::Object(m)) = v.pointer_mut(parent) {
        m.insert(key.to_owned(), to);
    }
}
fn del(v: &mut Value, parent: &str, key: &str) {
    if let Some(Value::Object(m)) = v.pointer_mut(parent) {
        m.remove(key);
    }
}
fn rejects(base: &[u8], f: impl FnOnce(&mut Value), want: ManifestError) -> R {
    let m = mutate(base, f)?;
    assert_eq!(parse_manifest(&m).err(), Some(want));
    Ok(())
}

// ------------------------------------------------------------------ golden

#[test]
fn golden_checkpoint_delta_and_current() -> R {
    let (b0, b1) = fixture()?;
    let rec = launch_record()?;
    let wr = json!({"name": "memory", "version": "0.1.0", "commit": "abc123"});
    let ew = json!({"version": "0.1.0", "commit": "abc123"});
    let owner =
        json!({"session": {"archive": "a", "machine": "m", "agent": "claude", "session_id": "s1"}});
    let pc = |c: u64, off: u64, len: u64| {
        json!({"offset": off, "len": len, "sha256": hex(c), "object": format!("objects/{}.zst", hex(c)),
               "encoding": "zstd-3", "encoded_len": 1 + c % 50, "encoded_sha256": hex(c + 1_000_000)})
    };
    let want0 = json!({
        "schema": "memory.manifest/1", "kind": "checkpoint", "owner": owner, "seq": 0,
        "previous": null, "captured_at": "2026-11-07T10:30:00Z", "writer": wr, "launch": [],
        "sources": [
            {"id": "c", "kind": "claude.todos", "path": "p/c", "class": "snapshot",
             "epochs": [{"epoch": {"n": 1, "writer": ew, "rules": hex(9)}, "size": 4, "sha256": hex(2), "pieces": [pc(2, 0, 4)]}]},
            {"id": "t", "kind": "claude.transcript", "path": "p/t", "class": "append",
             "epochs": [{"epoch": {"n": 1, "writer": ew}, "size": 10, "sha256": hex(1), "pieces": [pc(1, 0, 10)]}]}
        ]
    });
    assert_eq!(serde_json::from_slice::<Value>(&b0.bytes)?, want0);
    let want1 = json!({
        "schema": "memory.manifest/1", "kind": "delta", "owner": owner, "seq": 1,
        "previous": String::from(b0.hash), "checkpoint": String::from(b0.hash),
        "captured_at": "2026-11-07T10:30:00Z", "writer": wr,
        "launch": [{"digest": String::from(rec.digest()), "argv": rec.argv(), "env": rec.env()}],
        "changes": [{"op": "extend", "id": "t", "kind": "claude.transcript", "path": "p/t",
            "class": "append", "epoch": {"n": 1, "writer": ew}, "size": 30, "sha256": hex(3),
            "pieces": [pc(4, 10, 5), pc(5, 15, 15)]}]
    });
    assert_eq!(serde_json::from_slice::<Value>(&b1.bytes)?, want1);
    for built in [&b0, &b1] {
        let parsed = parse_manifest(&built.bytes)?;
        assert_eq!(parsed, built.manifest);
        assert_eq!(parsed.to_bytes()?, built.bytes);
        assert_eq!(manifest_hash(&built.bytes), built.hash);
    }
    assert_eq!(b1.position.seq, 1);
    assert_eq!(b1.position.checkpoint, b0.hash);
    let current = Current::new(b1.hash, 1, ts()?);
    let want = json!({"schema": "memory.current/1", "manifest": String::from(b1.hash), "seq": 1,
                      "updated_at": "2026-11-07T10:30:00Z"});
    let bytes = current.to_bytes()?;
    assert_eq!(serde_json::from_slice::<Value>(&bytes)?, want);
    assert_eq!(parse_current(&bytes)?, current);
    Ok(())
}

#[test]
fn schemas_are_derivable_and_object_key_is_canonical() -> R {
    let digest = Sha256::from_bytes([0xab; 32]);
    assert_eq!(
        object_key(&digest),
        format!("objects/{}.zst", "ab".repeat(32))
    );
    Ok(())
}

// ------------------------------------------------------------------ negatives

#[test]
fn delta_negative_fixtures() -> R {
    let (_, b1) = fixture()?;
    let d = b1.bytes.as_slice();
    let c0 = "/changes/0";
    let p0 = "/changes/0/pieces/0";
    let p1 = "/changes/0/pieces/1";
    rejects(d, |v| put(v, p1, "offset", json!(16)), ManifestError::Gap)?;
    rejects(
        d,
        |v| put(v, p1, "offset", json!(14)),
        ManifestError::Overlap,
    )?;
    rejects(
        d,
        |v| put(v, p0, "offset", json!(u64::MAX)),
        ManifestError::Overflow,
    )?;
    rejects(
        d,
        |v| put(v, c0, "op", json!("replace")),
        ManifestError::OpClassMismatch,
    )?;
    rejects(d, |v| put(v, c0, "op", json!("epoch")), ManifestError::Gap)?;
    rejects(
        d,
        |v| put(v, c0, "sha256", json!("abc")),
        ManifestError::Malformed,
    )?;
    rejects(
        d,
        |v| put(v, c0, "sha256", json!(hex(1).to_uppercase())),
        ManifestError::Malformed,
    )?;
    rejects(
        d,
        |v| put(v, c0, "sha256", json!(format!("{}0", hex(1)))),
        ManifestError::Malformed,
    )?;
    rejects(d, |v| put(v, "", "seq", json!(64)), ManifestError::BadSeq)?;
    rejects(d, |v| put(v, "", "seq", json!(0)), ManifestError::BadSeq)?;
    rejects(
        d,
        |v| put(v, p0, "object", json!("objects/x.zst")),
        ManifestError::BadObjectKey,
    )?;
    rejects(
        d,
        |v| put(v, p0, "object", json!(format!("objects/{}.zst", hex(77)))),
        ManifestError::BadObjectKey,
    )?;
    rejects(
        d,
        |v| put(v, c0, "size", json!(31)),
        ManifestError::SizeMismatch,
    )?;
    rejects(d, |v| put(v, p0, "len", json!(0)), ManifestError::BadPiece)?;
    rejects(
        d,
        |v| put(v, p0, "encoded_len", json!(0)),
        ManifestError::BadPiece,
    )?;
    rejects(
        d,
        |v| put(v, c0, "pieces", json!([])),
        ManifestError::BadPiece,
    )?;
    rejects(
        d,
        |v| put(v, "", "extra", json!(1)),
        ManifestError::Malformed,
    )?;
    rejects(
        d,
        |v| put(v, c0, "extra", json!(1)),
        ManifestError::Malformed,
    )?;
    rejects(
        d,
        |v| put(v, p0, "extra", json!(1)),
        ManifestError::Malformed,
    )?;
    rejects(
        d,
        |v| put(v, "", "kind", json!("bogus")),
        ManifestError::Malformed,
    )?;
    rejects(
        d,
        |v| put(v, "", "kind", json!("checkpoint")),
        ManifestError::Malformed,
    )?;
    rejects(
        d,
        |v| put(v, "", "owner", json!({"nobody": {}})),
        ManifestError::Malformed,
    )?;
    rejects(
        d,
        |v| put(v, "", "captured_at", json!("yesterday")),
        ManifestError::Malformed,
    )?;
    rejects(d, |v| del(v, "", "previous"), ManifestError::Malformed)?;
    rejects(d, |v| del(v, "", "checkpoint"), ManifestError::Malformed)?;
    rejects(
        d,
        |v| put(v, c0, "path", json!("../x")),
        ManifestError::BadPath,
    )?;
    rejects(
        d,
        |v| put(v, c0, "path", json!("/abs")),
        ManifestError::BadPath,
    )?;
    rejects(
        d,
        |v| put(v, c0, "path", json!("a//b")),
        ManifestError::BadPath,
    )?;
    rejects(d, |v| put(v, c0, "path", json!("")), ManifestError::BadPath)?;
    rejects(
        d,
        |v| put(v, c0, "path", json!("a\\b")),
        ManifestError::BadPath,
    )?;
    rejects(
        d,
        |v| put(v, c0, "path", json!("a".repeat(1025))),
        ManifestError::BadPath,
    )?;
    rejects(
        d,
        |v| put(v, "/writer", "name", json!("")),
        ManifestError::BadText,
    )?;
    rejects(
        d,
        |v| put(v, "/writer", "commit", json!("has space")),
        ManifestError::BadText,
    )?;
    rejects(
        d,
        |v| put(v, "/writer", "version", json!("v".repeat(65))),
        ManifestError::BadText,
    )?;
    rejects(
        d,
        |v| put(v, "/changes/0/epoch/writer", "version", json!("")),
        ManifestError::BadText,
    )?;
    rejects(
        d,
        |v| {
            if let Some(Value::Array(a)) = v.pointer_mut("/changes")
                && let Some(first) = a.first().cloned()
            {
                a.push(first);
            }
        },
        ManifestError::DuplicateSource,
    )?;
    rejects(
        d,
        |v| put(v, "/launch/0", "digest", json!(hex(99))),
        ManifestError::BadLaunch,
    )?;
    rejects(
        d,
        |v| put(v, "/launch/0", "argv", json!(vec!["claude"; 33])),
        ManifestError::BadLaunch,
    )?;
    rejects(
        d,
        |v| {
            put(
                v,
                "/launch/0",
                "env",
                json!({"A":"1","B":"1","C":"1","D":"1","E":"1","F":"1","G":"1","H":"1","I":"1"}),
            )
        },
        ManifestError::BadLaunch,
    )?;
    rejects(
        d,
        |v| {
            if let Some(Value::Array(a)) = v.pointer_mut("/launch")
                && let Some(first) = a.first().cloned()
            {
                a.push(first);
            }
        },
        ManifestError::BadLaunch,
    )?;
    Ok(())
}

#[test]
fn checkpoint_negative_fixtures() -> R {
    let (b0, _) = fixture()?;
    let c = b0.bytes.as_slice();
    rejects(c, |v| put(v, "", "seq", json!(1)), ManifestError::BadSeq)?;
    rejects(
        c,
        |v| put(v, "", "previous", json!(hex(1))),
        ManifestError::BadLink,
    )?;
    rejects(
        c,
        |v| {
            put(v, "", "seq", json!(64));
            put(v, "", "previous", Value::Null);
        },
        ManifestError::BadLink,
    )?;
    rejects(
        c,
        |v| {
            if let Some(Value::Array(a)) = v.pointer_mut("/sources") {
                a.swap(0, 1);
            }
        },
        ManifestError::DuplicateSource,
    )?;
    rejects(
        c,
        |v| {
            if let Some(Value::Array(a)) = v.pointer_mut("/sources")
                && let Some(first) = a.first().cloned()
            {
                a.insert(1, first);
            }
        },
        ManifestError::DuplicateSource,
    )?;
    rejects(
        c,
        |v| {
            if let Some(Value::Array(a)) = v.pointer_mut("/sources/0/epochs")
                && let Some(first) = a.first().cloned()
            {
                a.push(first);
            }
        },
        ManifestError::BadEpoch,
    )?;
    rejects(
        c,
        |v| {
            if let Some(Value::Array(a)) = v.pointer_mut("/sources/1/epochs")
                && let Some(mut first) = a.first().cloned()
            {
                put(&mut first, "/epoch", "n", json!(3));
                a.push(first);
            }
        },
        ManifestError::BadEpoch,
    )?;
    rejects(
        c,
        |v| put(v, "/sources/1", "epochs", json!([])),
        ManifestError::BadEpoch,
    )?;
    rejects(
        c,
        |v| put(v, "/sources/1/epochs/0", "size", json!(11)),
        ManifestError::SizeMismatch,
    )?;
    rejects(
        c,
        |v| put(v, "/sources/1/epochs/0/pieces/0", "offset", json!(1)),
        ManifestError::Gap,
    )?;
    rejects(
        c,
        |v| put(v, "", "changes", json!([])),
        ManifestError::Malformed,
    )?;
    rejects(
        c,
        |v| put(v, "/sources/1", "path", json!("..")),
        ManifestError::BadPath,
    )?;
    rejects(
        c,
        |v| put(v, "/sources/1", "deleted_at", json!("never")),
        ManifestError::Malformed,
    )?;
    Ok(())
}

#[test]
fn raw_document_rejections() -> R {
    let (b0, b1) = fixture()?;
    assert_eq!(parse_manifest(b"").err(), Some(ManifestError::Malformed));
    assert_eq!(parse_manifest(b"[]").err(), Some(ManifestError::Malformed));
    assert_eq!(
        parse_manifest(b"{\"schema\":5}").err(),
        Some(ManifestError::Malformed)
    );
    assert_eq!(parse_manifest(b"{}").err(), Some(ManifestError::Malformed));
    let mut trailing = b0.bytes.clone();
    trailing.extend_from_slice(b" 1");
    assert_eq!(
        parse_manifest(&trailing).err(),
        Some(ManifestError::Malformed)
    );
    let text = String::from_utf8(b1.bytes.clone())?;
    let dup = text.replacen("\"seq\":1", "\"seq\":1,\"seq\":1", 1);
    assert_ne!(dup, text);
    assert_eq!(
        parse_manifest(dup.as_bytes()).err(),
        Some(ManifestError::DuplicateKey)
    );
    let nested = text.replacen(
        "\"op\":\"extend\"",
        "\"op\":\"extend\",\"op\":\"extend\"",
        1,
    );
    assert_ne!(nested, text);
    assert_eq!(
        parse_manifest(nested.as_bytes()).err(),
        Some(ManifestError::DuplicateKey)
    );
    Ok(())
}

#[test]
fn duplicate_key_scanner_covers_all_value_kinds() {
    assert_eq!(
        check_no_duplicate_keys(br#"{"a":[1,-3,2.5,null,true,"s",{"b":1}]}"#),
        Ok(())
    );
    assert_eq!(
        check_no_duplicate_keys(br#"{"a":{"b":1,"b":2}}"#),
        Err(ManifestError::DuplicateKey)
    );
    assert_eq!(
        check_no_duplicate_keys(br#"[{"a":1,"a":1}]"#),
        Err(ManifestError::DuplicateKey)
    );
    assert_eq!(
        check_no_duplicate_keys(b"{} x"),
        Err(ManifestError::Malformed)
    );
    assert_eq!(check_no_duplicate_keys(b"{"), Err(ManifestError::Malformed));
}

#[test]
fn timestamp_validation() {
    for good in ["2026-11-07T10:30:00Z", "1999-01-31T00:00:59Z"] {
        assert!(Timestamp::try_from(good.to_owned()).is_ok(), "{good}");
    }
    for bad in [
        "",
        "2026-11-07T10:30:00",
        "2026-13-07T10:30:00Z",
        "2026-00-07T10:30:00Z",
        "2026-11-32T10:30:00Z",
        "2026-11-00T10:30:00Z",
        "2026-11-07T24:30:00Z",
        "2026-11-07T10:60:00Z",
        "2026-11-07T10:30:60Z",
        "2026-11-07 10:30:00Z",
        "20x6-11-07T10:30:00Z",
        "2026-11-07T10:30:00z",
        "2026-11-07T10:30:00+00:00",
    ] {
        assert_eq!(
            Timestamp::try_from(bad.to_owned()),
            Err(ManifestError::BadTimestamp),
            "{bad}"
        );
    }
}

// ------------------------------------------------------------------ schema dispatch

#[test]
fn schema_dispatch_is_typed() -> R {
    for (schema, want) in [
        ("memory.manifest/1", SchemaStatus::Supported),
        ("memory.manifest/2", SchemaStatus::Newer),
        ("memory.manifest/10", SchemaStatus::Newer),
        ("memory.manifest/0", SchemaStatus::Unknown),
        ("memory.manifest/01", SchemaStatus::Unknown),
        ("memory.manifest/", SchemaStatus::Unknown),
        ("memory.manifest/1x", SchemaStatus::Unknown),
        ("memory.manifest/-1", SchemaStatus::Unknown),
        ("memory.manifest", SchemaStatus::Unknown),
        ("memory.other/1", SchemaStatus::Unknown),
    ] {
        assert_eq!(
            classify_schema("memory.manifest/", schema),
            want,
            "{schema}"
        );
    }
    let (_, b1) = fixture()?;
    for (schema, want) in [
        ("memory.manifest/2", ManifestError::NewerSchema),
        ("memory.manifest/99", ManifestError::NewerSchema),
        ("memory.manifest/0", ManifestError::UnknownSchema),
        ("memory.current/1", ManifestError::UnknownSchema),
    ] {
        // Any body, even one that is not a manifest at all, yields the typed outcome first.
        let body = format!(r#"{{"schema":"{schema}","future":[1,2,{{"x":null}}]}}"#);
        assert_eq!(parse_manifest(body.as_bytes()).err(), Some(want));
        assert_eq!(recover_projection(body.as_bytes()).err(), Some(want));
        let real = mutate(&b1.bytes, |v| put(v, "", "schema", json!(schema)))?;
        assert_eq!(parse_manifest(&real).err(), Some(want));
        assert_eq!(recover_projection(&real).err(), Some(want));
    }
    assert_eq!(
        ManifestError::NewerSchema.park_reason(),
        Some(OwnerParkReason::NewerRemoteSchema)
    );
    assert_eq!(
        ManifestError::NewerSchema.commit_outcome(),
        Some(CommitOutcome::NewerSchema)
    );
    for other in [
        ManifestError::UnknownSchema,
        ManifestError::Malformed,
        ManifestError::DuplicateKey,
    ] {
        assert_eq!(other.park_reason(), None);
        assert_eq!(other.commit_outcome(), None);
    }
    // Current uses the same dispatch.
    let cur = Current::new(h(1), 3, ts()?).to_bytes()?;
    assert_eq!(
        parse_current(&mutate(&cur, |v| put(
            v,
            "",
            "schema",
            json!("memory.current/2")
        ))?)
        .err(),
        Some(ManifestError::NewerSchema)
    );
    assert_eq!(
        parse_current(&mutate(&cur, |v| put(
            v,
            "",
            "schema",
            json!("memory.manifest/1")
        ))?)
        .err(),
        Some(ManifestError::UnknownSchema)
    );
    assert_eq!(
        parse_current(&mutate(&cur, |v| put(v, "", "extra", json!(1)))?).err(),
        Some(ManifestError::Malformed)
    );
    assert_eq!(
        parse_current(&mutate(&cur, |v| put(v, "", "manifest", json!("zz")))?).err(),
        Some(ManifestError::Malformed)
    );
    let text = String::from_utf8(cur)?;
    let dup = text.replacen("\"seq\":3", "\"seq\":3,\"seq\":3", 1);
    assert_eq!(
        parse_current(dup.as_bytes()).err(),
        Some(ManifestError::DuplicateKey)
    );
    Ok(())
}

// ------------------------------------------------------------------ recovery projection

fn pos(n: u32, size: u64, whole: Sha256, rules: Option<Sha256>) -> EpochPosition {
    EpochPosition {
        epoch: n,
        captured_to: Offset::new(size),
        whole,
        rules: rules.map(RulesDigest::new),
    }
}

#[test]
fn projection_reads_positions_and_tolerates_other_fields() -> R {
    let (b0, b1) = fixture()?;
    let sid = |s: &str| SourceId::try_from(s.to_owned());
    let want1 = Projection {
        kind: ProjectedKind::Delta,
        seq: 1,
        previous: Some(b0.hash),
        checkpoint: Some(b0.hash),
        sources: vec![ProjectedSource {
            id: sid("t")?,
            latest: Some(pos(1, 30, h(3), None)),
        }],
    };
    assert_eq!(recover_projection(&b1.bytes)?, want1);
    let want0 = Projection {
        kind: ProjectedKind::Checkpoint,
        seq: 0,
        previous: None,
        checkpoint: None,
        sources: vec![
            ProjectedSource {
                id: sid("c")?,
                latest: Some(pos(1, 4, h(2), Some(h(9)))),
            },
            ProjectedSource {
                id: sid("t")?,
                latest: Some(pos(1, 10, h(1), None)),
            },
        ],
    };
    assert_eq!(recover_projection(&b0.bytes)?, want0);
    // Fields this build does not know are ignored by the projection (and only there).
    // Allowed envelope fields that the projection does not read may hold anything.
    let other = mutate(&b1.bytes, |v| {
        put(v, "", "writer", json!(1));
        put(v, "", "launch", json!({"x": [1]}));
        put(v, "/changes/0", "pieces", json!("garbage"));
        put(v, "/changes/0/epoch", "writer", json!(null));
    })?;
    assert_eq!(recover_projection(&other)?, want1);
    assert_eq!(parse_manifest(&other).err(), Some(ManifestError::Malformed));
    // Fields outside the per-kind allowed envelope are rejected, at any depth.
    for f in [
        (|v: &mut Value| put(v, "", "future", json!(1))) as fn(&mut Value),
        |v| put(v, "/changes/0", "future", json!(true)),
        |v| put(v, "/changes/0/epoch", "future", json!("y")),
    ] {
        let m = mutate(&b1.bytes, f)?;
        assert_eq!(recover_projection(&m).err(), Some(ManifestError::Malformed));
    }
    // The projection still enforces the envelope and duplicate keys.
    let text = String::from_utf8(b1.bytes.clone())?;
    let dup = text.replacen("\"seq\":1", "\"seq\":1,\"seq\":1", 1);
    assert_eq!(
        recover_projection(dup.as_bytes()).err(),
        Some(ManifestError::DuplicateKey)
    );
    Ok(())
}

#[test]
fn projection_rejects_inconsistent_envelopes() -> R {
    let (b0, b1) = fixture()?;
    let bad = |base: &[u8], f: fn(&mut Value), want: ManifestError| -> R {
        let m = mutate(base, f)?;
        assert_eq!(recover_projection(&m).err(), Some(want));
        Ok(())
    };
    bad(
        &b1.bytes,
        |v| put(v, "", "seq", json!(64)),
        ManifestError::BadSeq,
    )?;
    bad(
        &b0.bytes,
        |v| put(v, "", "seq", json!(1)),
        ManifestError::BadSeq,
    )?;
    bad(
        &b0.bytes,
        |v| put(v, "", "previous", json!(hex(1))),
        ManifestError::BadLink,
    )?;
    bad(
        &b0.bytes,
        |v| put(v, "", "changes", json!([{"id":"t","op":"deleted"}])),
        ManifestError::Malformed,
    )?;
    bad(
        &b0.bytes,
        |v| put(v, "/sources/0", "epochs", json!([])),
        ManifestError::Malformed,
    )?;
    bad(
        &b1.bytes,
        |v| del(v, "", "previous"),
        ManifestError::BadLink,
    )?;
    bad(
        &b1.bytes,
        |v| del(v, "", "checkpoint"),
        ManifestError::BadLink,
    )?;
    bad(
        &b1.bytes,
        |v| {
            put(v, "", "sources", json!([{"id": "t", "epochs": []}]));
        },
        ManifestError::Malformed,
    )?;
    bad(
        &b1.bytes,
        |v| del(v, "/changes/0", "size"),
        ManifestError::Malformed,
    )?;
    bad(
        &b1.bytes,
        |v| del(v, "/changes/0", "epoch"),
        ManifestError::Malformed,
    )?;
    bad(
        &b1.bytes,
        |v| del(v, "/changes/0", "sha256"),
        ManifestError::Malformed,
    )?;
    bad(
        &b1.bytes,
        |v| put(v, "", "sources", json!([])),
        ManifestError::Malformed,
    )?;
    bad(
        &b0.bytes,
        |v| put(v, "", "changes", json!([])),
        ManifestError::Malformed,
    )?;
    // Deleted change and deleted checkpoint source project to `latest: None`.
    let deleted = mutate(&b1.bytes, |v| {
        put(
            v,
            "",
            "changes",
            json!([{"op": "deleted", "id": "t", "kind": "claude.transcript", "deleted_at": "2026-11-07T10:30:00Z"}]),
        );
    })?;
    let p = recover_projection(&deleted)?;
    assert_eq!(
        p.sources
            .iter()
            .map(|s| s.latest.is_none())
            .collect::<Vec<_>>(),
        vec![true]
    );
    let cp = mutate(&b0.bytes, |v| {
        put(v, "/sources/0", "deleted_at", json!("2026-11-07T10:30:00Z"))
    })?;
    let p = recover_projection(&cp)?;
    assert_eq!(
        p.sources
            .iter()
            .map(|s| s.latest.is_none())
            .collect::<Vec<_>>(),
        vec![true, false]
    );
    Ok(())
}

// ------------------------------------------------------------------ apply / build / fold

fn base_state() -> Result<OwnerState, Box<dyn std::error::Error>> {
    Ok(fixture()?.0.state)
}

#[test]
fn apply_transition_rules() -> R {
    let ext = |name: &str,
               epoch: EpochStamp,
               off: u64|
     -> Result<SourceWrite, Box<dyn std::error::Error>> {
        swrite(name, true, epoch, off + 5, h(50), vec![piece(50, off, 5)])
    };
    let state = base_state()?;
    let check = |change: Change, want: ManifestError| -> R {
        let mut s = state.clone();
        assert_eq!(s.apply_changes(&[change]).err(), Some(want));
        assert_eq!(s, state, "state unchanged on error");
        Ok(())
    };
    check(
        Change::Extend(ext("zz", stamp(1, None), 0)?),
        ManifestError::MissingSource,
    )?;
    check(
        Change::Extend(ext("t", stamp(2, None), 10)?),
        ManifestError::BadEpoch,
    )?;
    check(
        Change::Extend(ext("t", stamp(1, Some(3)), 10)?),
        ManifestError::BadEpoch,
    )?;
    check(
        Change::Extend(ext("t", stamp(1, None), 11)?),
        ManifestError::Gap,
    )?;
    check(
        Change::Extend(ext("t", stamp(1, None), 9)?),
        ManifestError::Overlap,
    )?;
    let mut wrong_kind = ext("t", stamp(1, None), 10)?;
    wrong_kind.kind = SourceKind::ClaudeSubagent;
    check(Change::Extend(wrong_kind), ManifestError::SourceChanged)?;
    let mut wrong_path = ext("t", stamp(1, None), 10)?;
    wrong_path.path = "other".to_owned();
    check(Change::Extend(wrong_path), ManifestError::SourceChanged)?;
    check(
        Change::Epoch(swrite(
            "t",
            true,
            stamp(3, None),
            5,
            h(1),
            vec![piece(1, 0, 5)],
        )?),
        ManifestError::BadEpoch,
    )?;
    check(
        Change::Replace(swrite(
            "c",
            false,
            stamp(1, None),
            5,
            h(1),
            vec![piece(1, 0, 5)],
        )?),
        ManifestError::BadEpoch,
    )?;
    let id = |s: &str| SourceId::try_from(s.to_owned());
    check(
        Change::Deleted {
            id: id("zz")?,
            kind: SourceKind::ClaudeTranscript,
            deleted_at: ts()?,
        },
        ManifestError::MissingSource,
    )?;
    check(
        Change::Deleted {
            id: id("t")?,
            kind: SourceKind::ClaudeTodos,
            deleted_at: ts()?,
        },
        ManifestError::SourceChanged,
    )?;
    // Duplicate ids in one change set.
    let mut s = state.clone();
    let e = Change::Extend(ext("t", stamp(1, None), 10)?);
    assert_eq!(
        s.apply_changes(&[e.clone(), e]).err(),
        Some(ManifestError::DuplicateSource)
    );
    // Atomic: a good change followed by a bad one leaves nothing applied.
    let good = Change::Extend(ext("t", stamp(1, None), 10)?);
    let bad = Change::Deleted {
        id: id("zz")?,
        kind: SourceKind::ClaudeTodos,
        deleted_at: ts()?,
    };
    assert!(s.apply_changes(&[good, bad]).is_err());
    assert_eq!(s, state);
    // Delete, then extend/delete fail, an epoch revives.
    let del_t = Change::Deleted {
        id: id("t")?,
        kind: SourceKind::ClaudeTranscript,
        deleted_at: ts()?,
    };
    s.apply_changes(std::slice::from_ref(&del_t))?;
    assert_eq!(
        s.apply_changes(&[del_t]).err(),
        Some(ManifestError::SourceDeleted)
    );
    assert_eq!(
        s.apply_changes(&[Change::Extend(ext("t", stamp(1, None), 10)?)])
            .err(),
        Some(ManifestError::SourceDeleted)
    );
    s.apply_changes(&[Change::Epoch(swrite(
        "t",
        true,
        stamp(2, None),
        5,
        h(1),
        vec![piece(1, 0, 5)],
    )?)])?;
    let t = s.sources.get(&id("t")?).ok_or("t")?;
    assert!(t.deleted_at.is_none());
    assert_eq!(t.epochs.len(), 2, "append retains every epoch");
    // Snapshot replacement keeps only the latest epoch.
    s.apply_changes(&[Change::Replace(swrite(
        "c",
        false,
        stamp(2, None),
        5,
        h(1),
        vec![piece(1, 0, 5)],
    )?)])?;
    assert_eq!(s.sources.get(&id("c")?).ok_or("c")?.epochs.len(), 1);
    // Epoch counter overflow.
    let mut s = OwnerState::default();
    s.apply_changes(&[Change::Epoch(swrite(
        "m",
        true,
        stamp(u32::MAX, None),
        5,
        h(1),
        vec![piece(1, 0, 5)],
    )?)])?;
    assert_eq!(
        s.apply_changes(&[Change::Epoch(swrite(
            "m",
            true,
            stamp(0, None),
            5,
            h(1),
            vec![piece(1, 0, 5)]
        )?)])
        .err(),
        Some(ManifestError::BadEpoch)
    );
    // Structural validation applies inside apply too.
    assert_eq!(
        s.apply_changes(&[Change::Extend(swrite(
            "m",
            true,
            stamp(u32::MAX, None),
            0,
            h(1),
            vec![]
        )?)])
        .err(),
        Some(ManifestError::BadPiece)
    );
    Ok(())
}

#[test]
fn build_errors() -> R {
    let (b0, _) = fixture()?;
    let end = Position {
        seq: u64::MAX,
        manifest: h(1),
        checkpoint: h(2),
    };
    assert_eq!(
        build(
            Some((&end, &b0.state)),
            header_for(session_owner()?, false)?,
            vec![]
        )
        .err(),
        Some(ManifestError::Overflow)
    );
    let mut bad = header_for(session_owner()?, false)?;
    bad.writer.name = String::new();
    assert_eq!(build(None, bad, vec![]).err(), Some(ManifestError::BadText));
    let empty_extend = swrite("t", true, stamp(1, None), 0, h(1), vec![])?;
    assert_eq!(
        build(
            Some((&b0.position, &b0.state)),
            header_for(session_owner()?, false)?,
            vec![Change::Extend(empty_extend)]
        )
        .err(),
        Some(ManifestError::BadPiece)
    );
    // Seq 63 -> 64 is a checkpoint that links to 63 and starts a new checkpoint chain.
    let at63 = Position {
        seq: 63,
        manifest: h(7),
        checkpoint: h(8),
    };
    let cp = build(
        Some((&at63, &b0.state)),
        header_for(session_owner()?, false)?,
        vec![],
    )?;
    assert!(matches!(cp.manifest, Manifest::Checkpoint(_)));
    assert_eq!(cp.position.checkpoint, cp.hash);
    let next = build(
        Some((&cp.position, &cp.state)),
        header_for(session_owner()?, false)?,
        vec![],
    )?;
    assert_eq!(next.position.checkpoint, cp.hash);
    assert_eq!(next.position.seq, 65);
    Ok(())
}

#[test]
fn fold_rejects_broken_chains() -> R {
    let (b0, b1) = fixture()?;
    let (cp, delta) = (b0.bytes.as_slice(), b1.bytes.as_slice());
    assert_eq!(fold(&[]).err(), Some(ManifestError::BrokenChain));
    assert_eq!(fold(&[delta]).err(), Some(ManifestError::BrokenChain));
    assert_eq!(fold(&[cp, cp]).err(), Some(ManifestError::BrokenChain));
    assert_eq!(
        fold(&[b"{}".as_slice()]).err(),
        Some(ManifestError::Malformed)
    );
    assert_eq!(
        fold(&[cp, b"nope".as_slice()]).err(),
        Some(ManifestError::Malformed)
    );
    assert_eq!(fold(&vec![cp; 65]).err(), Some(ManifestError::TooManySteps));
    let skip = mutate(delta, |v| put(v, "", "seq", json!(2)))?;
    assert_eq!(fold(&[cp, &skip]).err(), Some(ManifestError::BadSeq));
    let prev = mutate(delta, |v| put(v, "", "previous", json!(hex(1))))?;
    assert_eq!(fold(&[cp, &prev]).err(), Some(ManifestError::BadLink));
    let link = mutate(delta, |v| put(v, "", "checkpoint", json!(hex(1))))?;
    assert_eq!(fold(&[cp, &link]).err(), Some(ManifestError::BadLink));
    let other = build(
        Some((&b0.position, &b0.state)),
        header_for(shared_owner()?, false)?,
        vec![],
    )?;
    assert_eq!(
        fold(&[cp, &other.bytes]).err(),
        Some(ManifestError::OwnerMismatch)
    );
    // A delta that extends a source its checkpoint never listed.
    let empty = build(None, header_for(session_owner()?, false)?, vec![])?;
    let rebased = mutate(delta, |v| {
        put(v, "", "previous", json!(String::from(empty.hash)));
        put(v, "", "checkpoint", json!(String::from(empty.hash)));
    })?;
    assert_eq!(
        fold(&[&empty.bytes, &rebased]).err(),
        Some(ManifestError::MissingSource)
    );
    // Happy path.
    let folded = fold(&[cp, delta])?;
    assert_eq!(folded.state, b1.state);
    assert_eq!(folded.position, b1.position);
    assert_eq!(folded.owner, session_owner()?);
    Ok(())
}

// ------------------------------------------------------------------ R2 dedup

#[test]
fn verify_encodings_reports_missing_and_mismatched() -> R {
    let (_, b1) = fixture()?;
    assert_eq!(
        b1.manifest.verify_encodings(|_| None).err(),
        Some(ManifestError::MissingObject)
    );
    let wrong = StoredEncoding {
        encoded_len: 999,
        encoded_sha256: h(5),
    };
    assert_eq!(
        b1.manifest.verify_encodings(|_| Some(wrong)).err(),
        Some(ManifestError::EncodingMismatch)
    );
    Ok(())
}

/// Two writers compress the same raw bytes differently; `If-None-Match` lets one win.
fn r2_race(seed: u64) -> R {
    let raw = h(seed);
    let a = StoredEncoding {
        encoded_len: 10 + seed % 90,
        encoded_sha256: h(seed ^ 0xA),
    };
    let b = StoredEncoding {
        encoded_len: 100 + seed % 90,
        encoded_sha256: h(seed ^ 0xB),
    };
    let (first, second) = if seed & 1 == 0 { (a, b) } else { (b, a) };
    let mut store: HashMap<String, StoredEncoding> = HashMap::new();
    for enc in [first, second] {
        store.entry(object_key(&raw)).or_insert(enc);
    }
    let winner = store.get(&object_key(&raw)).copied().ok_or("stored")?;
    let len = 1 + seed % 1000;
    let mut manifests = Vec::new();
    for (owner, enc) in [(session_owner()?, a), (shared_owner()?, b)] {
        let own = Piece::new(
            Offset::new(0),
            len,
            raw,
            enc.encoded_len,
            enc.encoded_sha256,
        );
        let adopted = own.clone().adopt_stored(&winner);
        assert_eq!(
            (adopted.offset, adopted.len, adopted.sha256, &adopted.object),
            (own.offset, own.len, own.sha256, &own.object)
        );
        assert_eq!(
            (adopted.encoded_len, adopted.encoded_sha256),
            (winner.encoded_len, winner.encoded_sha256)
        );
        let write = swrite("t", true, stamp(1, None), len, raw, vec![adopted])?;
        let built = build(
            None,
            header_for(owner.clone(), false)?,
            vec![Change::Epoch(write)],
        )?;
        built.manifest.verify_encodings(|k| store.get(k).copied())?;
        manifests.push(built);
        // Without adoption the loser's descriptor is rejected; the winner's needs no adoption.
        let write = swrite("t", true, stamp(1, None), len, raw, vec![own])?;
        let raw_built = build(None, header_for(owner, false)?, vec![Change::Epoch(write)])?;
        let verdict = raw_built
            .manifest
            .verify_encodings(|k| store.get(k).copied());
        if enc == winner {
            assert_eq!(verdict, Ok(()));
        } else {
            assert_eq!(verdict.err(), Some(ManifestError::EncodingMismatch));
        }
    }
    for built in &manifests {
        assert_eq!(parse_manifest(&built.bytes)?, built.manifest);
    }
    Ok(())
}

#[test]
fn r2_both_orders() -> R {
    r2_race(2)?;
    r2_race(3)
}

#[hegel::test]
fn hegel_r2_dedup_winner_descriptor(tc: TestCase) {
    let r = r2_race(tc.draw(gs::integers::<u64>()));
    assert!(r.is_ok(), "{r:?}");
}
proptest! {
    #[test]
    fn proptest_r2_dedup_winner_descriptor(seed in any::<u64>()) {
        let r = r2_race(seed);
        prop_assert!(r.is_ok(), "{r:?}");
    }
}

// ------------------------------------------------------------------ model-based history

struct Rng(u64);
impl Rng {
    fn below(&mut self, n: u64) -> u64 {
        self.0 ^= self.0 << 13;
        self.0 ^= self.0 >> 7;
        self.0 ^= self.0 << 17;
        (self.0.wrapping_mul(0x2545_F491_4F6C_DD1D) >> 33) % n.max(1)
    }
}

/// Independent model: plain vectors, no use of the builder's state types.
struct MEpoch {
    stamp: EpochStamp,
    size: u64,
    whole: Sha256,
    pieces: Vec<(u64, u64, Sha256)>,
}
struct MSrc {
    append: bool,
    epochs: Vec<MEpoch>,
    deleted: bool,
}
type EpochView = (EpochStamp, u64, Sha256, Vec<(u64, u64, Sha256)>);
type View = BTreeMap<String, (bool, Vec<EpochView>)>;

fn model_view(model: &BTreeMap<String, MSrc>) -> View {
    model
        .iter()
        .map(|(k, s)| {
            let epochs = s
                .epochs
                .iter()
                .map(|e| (e.stamp.clone(), e.size, e.whole, e.pieces.clone()))
                .collect();
            (k.clone(), (s.deleted, epochs))
        })
        .collect()
}
fn state_view(state: &OwnerState) -> View {
    state
        .sources
        .iter()
        .map(|(id, e)| {
            let epochs = e
                .epochs
                .iter()
                .map(|r| {
                    let pieces = r
                        .pieces
                        .iter()
                        .map(|p| (p.offset.get(), p.len, p.sha256))
                        .collect();
                    (r.epoch.clone(), r.size, r.sha256, pieces)
                })
                .collect();
            (id.as_str().to_owned(), (e.deleted_at.is_some(), epochs))
        })
        .collect()
}
fn latest(src: &MSrc) -> Option<EpochPosition> {
    if src.deleted {
        return None;
    }
    src.epochs.last().map(|e| EpochPosition {
        epoch: e.stamp.n,
        captured_to: Offset::new(e.size),
        whole: e.whole,
        rules: e.stamp.rules,
    })
}

fn fresh(rng: &mut Rng, ctr: &mut u64, from: u64) -> (Vec<Piece>, u64) {
    let count = 1 + rng.below(3);
    let mut off = from;
    let mut out = Vec::new();
    for _ in 0..count {
        *ctr += 1;
        let len = 1 + rng.below(1000);
        out.push(piece(*ctr, off, len));
        off += len;
    }
    (out, off)
}

fn gen_change(
    rng: &mut Rng,
    ctr: &mut u64,
    model: &mut BTreeMap<String, MSrc>,
    idx: u64,
    shapes: &Shapes,
) -> Result<Change, Box<dyn std::error::Error>> {
    let name = format!("s{idx}");
    let append = idx.is_multiple_of(2);
    let r = rng.below(10);
    let (exists, live, last_n) = model.get(&name).map_or((false, false, 0), |s| {
        (true, !s.deleted, s.epochs.last().map_or(0, |e| e.stamp.n))
    });
    *ctr += 1;
    let whole = h(*ctr + 2_000_000);
    if exists && live && r >= 8 {
        let src = model.get_mut(&name).ok_or("model")?;
        src.deleted = true;
        bump(&shapes.delete);
        return Ok(Change::Deleted {
            id: SourceId::try_from(name)?,
            kind: kind_of(append),
            deleted_at: ts()?,
        });
    }
    if exists && live && append && r < 6 {
        let src = model.get_mut(&name).ok_or("model")?;
        let last = src.epochs.last_mut().ok_or("model")?;
        let (pieces, end) = fresh(rng, ctr, last.size);
        last.size = end;
        last.whole = whole;
        last.pieces
            .extend(pieces.iter().map(|p| (p.offset.get(), p.len, p.sha256)));
        let epoch = last.stamp.clone();
        return Ok(Change::Extend(swrite(
            &name, true, epoch, end, whole, pieces,
        )?));
    }
    let rules = if append || rng.below(2) == 0 {
        None
    } else {
        Some(rng.below(1 << 20))
    };
    let epoch = stamp(last_n + 1, rules);
    let (pieces, end) = fresh(rng, ctr, 0);
    let record = MEpoch {
        stamp: epoch.clone(),
        size: end,
        whole,
        pieces: pieces
            .iter()
            .map(|p| (p.offset.get(), p.len, p.sha256))
            .collect(),
    };
    match model.get_mut(&name) {
        None => {
            model.insert(
                name.clone(),
                MSrc {
                    append,
                    epochs: vec![record],
                    deleted: false,
                },
            );
        }
        Some(src) => {
            src.deleted = false;
            if src.append {
                src.epochs.push(record);
            } else {
                src.epochs = vec![record];
            }
        }
    }
    if exists {
        bump(if append {
            &shapes.multi_epoch
        } else {
            &shapes.snapshot_replacement
        });
    }
    let write = swrite(&name, append, epoch, end, whole, pieces)?;
    Ok(if append {
        Change::Epoch(write)
    } else {
        Change::Replace(write)
    })
}

struct Stat {
    pieces: usize,
}

fn history(seed: u64, len: u64, nsrc: u64) -> Result<Vec<Stat>, Box<dyn std::error::Error>> {
    history_shapes(seed, len, nsrc, &Shapes::new())
}
fn history_shapes(
    seed: u64,
    len: u64,
    nsrc: u64,
    shapes: &Shapes,
) -> Result<Vec<Stat>, Box<dyn std::error::Error>> {
    let mut rng = Rng(seed | 1);
    let mut ctr = 0_u64;
    let owner = if seed & 2 == 0 {
        session_owner()?
    } else {
        shared_owner()?
    };
    let mut model: BTreeMap<String, MSrc> = BTreeMap::new();
    let mut prev: Option<(Position, OwnerState)> = None;
    let mut chain: Vec<Vec<u8>> = Vec::new();
    let mut hashes: Vec<Sha256> = Vec::new();
    let mut stats = Vec::new();
    for seq in 0..len {
        let s = usize::try_from(seq)?;
        let count = rng.below(nsrc.min(3) + 1);
        let mut picked = BTreeSet::new();
        let mut changes = Vec::new();
        for _ in 0..count {
            let idx = rng.below(nsrc);
            if picked.insert(idx) {
                changes.push(gen_change(&mut rng, &mut ctr, &mut model, idx, shapes)?);
            }
        }
        let header = header_for(owner.clone(), seq % 7 == 0)?;
        let built = build(
            prev.as_ref().map(|(p, st)| (p, st)),
            header,
            changes.clone(),
        )?;
        // Builder state equals the independent model.
        assert_eq!(state_view(&built.state), model_view(&model), "seq {seq}");
        // Round trip, canonical bytes, identity.
        let parsed = parse_manifest(&built.bytes)?;
        assert_eq!(parsed, built.manifest);
        assert_eq!(parsed.to_bytes()?, built.bytes);
        assert_eq!(manifest_hash(&built.bytes), built.hash);
        assert_eq!(built.position.seq, seq);
        let prev_hash = s.checked_sub(1).and_then(|i| hashes.get(i)).copied();
        let cp_seq = s - s % 64;
        // Expected projection from the model alone.
        let want_sources: Vec<ProjectedSource> = match &built.manifest {
            Manifest::Delta(_) => changes
                .iter()
                .map(|c| {
                    let latest = model.get(c.id().as_str()).and_then(latest);
                    ProjectedSource {
                        id: c.id().clone(),
                        latest,
                    }
                })
                .collect(),
            Manifest::Checkpoint(_) => model
                .iter()
                .map(|(k, v)| {
                    Ok(ProjectedSource {
                        id: SourceId::try_from(k.clone())?,
                        latest: latest(v),
                    })
                })
                .collect::<Result<_, Box<dyn std::error::Error>>>()?,
        };
        let projection = recover_projection(&built.bytes)?;
        assert_eq!(projection.seq, seq);
        assert_eq!(projection.sources, want_sources);
        match &built.manifest {
            Manifest::Delta(d) => {
                assert_ne!(seq % 64, 0);
                assert_eq!(Some(d.previous), prev_hash);
                assert_eq!(hashes.get(cp_seq).copied(), Some(d.checkpoint));
                assert_eq!(projection.kind, ProjectedKind::Delta);
                assert_eq!(
                    (projection.previous, projection.checkpoint),
                    (prev_hash, Some(d.checkpoint))
                );
            }
            Manifest::Checkpoint(c) => {
                assert_eq!(seq % 64, 0);
                assert_eq!(c.previous, prev_hash);
                assert_eq!(projection.kind, ProjectedKind::Checkpoint);
                assert_eq!(
                    (projection.previous, projection.checkpoint),
                    (prev_hash, None)
                );
                stats.push(Stat {
                    pieces: c
                        .sources
                        .iter()
                        .flat_map(|x| &x.epochs)
                        .map(|e| e.pieces.len())
                        .sum(),
                });
                // Snapshot-class sources keep only the latest epoch in checkpoints.
                for src in &c.sources {
                    if src.class == SourceClass::Snapshot {
                        assert_eq!(src.epochs.len(), 1);
                    }
                }
                assert_eq!(fold(&[&built.bytes])?.state, built.state);
                if seq > 0 {
                    // Checkpoint k plus deltas up to k+63, then this manifest's changes,
                    // equals checkpoint k+64.
                    let slice = chain.get(s - 64..s).ok_or("range")?;
                    let refs: Vec<&[u8]> = slice.iter().map(Vec::as_slice).collect();
                    let mut folded = fold(&refs)?;
                    bump(&shapes.fold_across_checkpoint);
                    assert_eq!(folded.position.seq, seq - 1);
                    assert_eq!(Some(folded.position.manifest), prev_hash);
                    folded.state.apply_changes(&changes)?;
                    assert_eq!(folded.state, OwnerState::from_checkpoint(c));
                    assert_eq!(folded.state, built.state);
                    assert_eq!(state_view(&folded.state), model_view(&model));
                    let mut longer = refs;
                    longer.push(&built.bytes);
                    assert_eq!(fold(&longer).err(), Some(ManifestError::TooManySteps));
                }
            }
        }
        chain.push(built.bytes.clone());
        hashes.push(built.hash);
        prev = Some((built.position, built.state));
    }
    // Reader rule at the end: latest checkpoint plus deltas equals the model.
    let last = usize::try_from(len - 1)?;
    let start = last - last % 64;
    let slice = chain.get(start..=last).ok_or("range")?;
    let refs: Vec<&[u8]> = slice.iter().map(Vec::as_slice).collect();
    let folded = fold(&refs)?;
    assert_eq!(state_view(&folded.state), model_view(&model));
    assert_eq!(folded.position.seq, len - 1);
    Ok(stats)
}

#[test]
fn history_smoke_and_r3_checkpoint_sizes() -> R {
    for (seed, len, nsrc) in [(7_u64, 321_u64, 4_u64), (8, 321, 40), (11, 193, 12)] {
        let stats = history(seed, len, nsrc)?;
        assert!(stats.len() >= 3);
    }
    // The "64 pieces per checkpoint" bound is false: a checkpoint lists every piece.
    let stats = history(8, 321, 40)?;
    assert!(
        stats.iter().any(|s| s.pieces > 64),
        "pieces bound is not a property"
    );
    Ok(())
}

fn guarded_hegel(shapes: &'static Shapes, fold: bool, f: fn(&TestCase) -> R) {
    hegel::Hegel::new(move |tc: TestCase| {
        let r = f(&tc);
        assert!(r.is_ok(), "{r:?}");
    })
    .run();
    let g = require_all_shapes(&shapes.counts(fold));
    assert!(g.is_ok(), "{g:?}");
}
fn roundtrip_hegel(tc: &TestCase) -> R {
    let seed = tc.draw(gs::integers::<u64>());
    let len = 1 + u64::from(tc.draw(gs::integers::<u8>())) % 6;
    let nsrc = 1 + u64::from(tc.draw(gs::integers::<u8>())) % 8;
    history_shapes(seed, len, nsrc, &HEGEL_ROUNDTRIP).map(|_| ())
}
fn fold_hegel(tc: &TestCase) -> R {
    let seed = tc.draw(gs::integers::<u64>());
    let len = 65 + u64::from(tc.draw(gs::integers::<u8>())) % 70;
    history_shapes(seed, len, 4, &HEGEL_FOLD).map(|_| ())
}
#[test]
fn hegel_roundtrip_of_arbitrary_valid_manifests() {
    guarded_hegel(&HEGEL_ROUNDTRIP, false, roundtrip_hegel);
}
#[test]
fn hegel_deltas_from_checkpoint_equal_next_checkpoint() {
    guarded_hegel(&HEGEL_FOLD, true, fold_hegel);
}
#[test]
fn proptest_roundtrip_of_arbitrary_valid_manifests() {
    let mut runner = proptest::test_runner::TestRunner::default();
    let r = runner.run(&(any::<u64>(), 1_u64..7, 1_u64..9), |(seed, len, nsrc)| {
        history_shapes(seed, len, nsrc, &PROPTEST_ROUNDTRIP)
            .map(|_| ())
            .map_err(|e| TestCaseError::fail(format!("{e:?}")))
    });
    assert!(r.is_ok(), "{r:?}");
    let g = require_all_shapes(&PROPTEST_ROUNDTRIP.counts(false));
    assert!(g.is_ok(), "{g:?}");
}
#[test]
fn proptest_deltas_from_checkpoint_equal_next_checkpoint() {
    let mut runner = proptest::test_runner::TestRunner::default();
    let r = runner.run(&(any::<u64>(), 0_u64..70), |(seed, extra)| {
        history_shapes(seed, 65 + extra, 4, &PROPTEST_FOLD)
            .map(|_| ())
            .map_err(|e| TestCaseError::fail(format!("{e:?}")))
    });
    assert!(r.is_ok(), "{r:?}");
    let g = require_all_shapes(&PROPTEST_FOLD.counts(true));
    assert!(g.is_ok(), "{g:?}");
}
#[test]
fn property6_coverage_guard_negative_controls_bite() {
    let zero = Shapes::new();
    let err = require_all_shapes(&zero.counts(true));
    assert!(err.is_err_and(|e| e.contains("multi-epoch") && e.contains("fold-across-checkpoint")));
    assert!(require_all_shapes(&[]).is_err());
    for missing in 0..4 {
        let sh = Shapes::new();
        for (i, c) in [
            &sh.multi_epoch,
            &sh.snapshot_replacement,
            &sh.delete,
            &sh.fold_across_checkpoint,
        ]
        .into_iter()
        .enumerate()
        {
            if i != missing {
                bump(c);
            }
        }
        let counts = sh.counts(true);
        let name = counts.get(missing).map(|c| c.0);
        let err = require_all_shapes(&counts);
        assert!(
            err.is_err_and(|e| name.is_some_and(|n| e.contains(n))),
            "{missing}"
        );
    }
    let full = Shapes::new();
    for c in [
        &full.multi_epoch,
        &full.snapshot_replacement,
        &full.delete,
        &full.fold_across_checkpoint,
    ] {
        bump(c);
    }
    assert!(require_all_shapes(&full.counts(true)).is_ok());
    // A tiny real run (3 histories of 2 manifests) cannot reach a checkpoint fold: guard fails.
    let small = Shapes::new();
    assert!(history_shapes(1, 2, 1, &small).is_ok());
    assert!(require_all_shapes(&small.counts(true)).is_err());
}
