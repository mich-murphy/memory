//! `read_owner` against an in-memory object map holding real manifests built by `memory-core`.
use memory_core::domain::{
    Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey, Sha256, SourceClass, SourceId,
    SourceKind,
};
use memory_core::limits::{current_key, manifest_key};
use memory_core::manifest::{
    Built, Change, Current, EpochStamp, EpochWriter, Folded, Header, SourceWrite, Timestamp,
    Writer, build, manifest_hash,
};
use memory_reader::{ChainFault, OwnerError, read_owner, restore_source};
use std::collections::HashMap;

mod common;
use common::{R, enc, hex, piece};

type Objects = HashMap<String, Vec<u8>>;
type Read = Result<Option<Folded>, OwnerError<String>>;

fn bad<T>(_: T) -> String {
    "invalid test value".to_owned()
}
fn owner(session: &str) -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(bad)?,
        machine: MachineId::try_from("m".to_owned()).map_err(bad)?,
        agent: Agent::Claude,
        session: SessionId::try_from(session.to_owned()).map_err(bad)?,
    }))
}
fn now() -> R<Timestamp> {
    Timestamp::try_from("2026-11-07T10:30:00Z".to_owned()).map_err(bad)
}
fn sha(bytes: &[u8]) -> R<Sha256> {
    Sha256::try_from(hex(bytes)).map_err(bad)
}
fn source_id() -> R<SourceId> {
    SourceId::try_from("t".to_owned()).map_err(bad)
}

/// A stored chain of `count` manifests (seq 0 is the checkpoint) for one append source.
struct Chain {
    objects: Objects,
    builts: Vec<Built>,
    whole: Vec<u8>,
    owner: Owner,
}

fn chain(count: usize) -> R<Chain> {
    let owner = owner("s1")?;
    let mut objects = Objects::new();
    let mut builts: Vec<Built> = Vec::new();
    let mut whole: Vec<u8> = Vec::new();
    for i in 0..count {
        let chunk = format!("line {i} of the transcript\n")
            .repeat(4)
            .into_bytes();
        let stored = enc(&chunk)?;
        let offset = u64::try_from(whole.len()).map_err(bad)?;
        let p = piece(&chunk, offset, &stored)?;
        objects.insert(p.object().to_owned(), stored);
        whole.extend(&chunk);
        let write = SourceWrite {
            id: source_id()?,
            kind: SourceKind::ClaudeTranscript,
            path: "p/t".to_owned(),
            class: SourceClass::Append,
            epoch: EpochStamp {
                n: 1,
                writer: EpochWriter {
                    version: "v".to_owned(),
                    commit: "c".to_owned(),
                },
                rules: None,
            },
            size: u64::try_from(whole.len()).map_err(bad)?,
            sha256: sha(&whole)?,
            pieces: vec![p],
        };
        let change = if i == 0 {
            Change::Epoch(write)
        } else {
            Change::Extend(write)
        };
        let header = Header {
            owner: owner.clone(),
            captured_at: now()?,
            writer: Writer {
                name: "memory".to_owned(),
                version: "v".to_owned(),
                commit: "c".to_owned(),
            },
            launch: vec![],
        };
        let built = build(
            builts.last().map(|b| (&b.position, &b.state)),
            header,
            vec![change],
        )
        .map_err(bad)?;
        objects.insert(manifest_key(&built.hash), built.bytes.clone());
        builts.push(built);
    }
    let head = builts.last().ok_or("empty chain")?;
    let pointer = Current::new(head.hash, head.position.seq, now()?);
    objects.insert(current_key(&owner), pointer.to_bytes().map_err(bad)?);
    Ok(Chain {
        objects,
        builts,
        whole,
        owner,
    })
}

/// The result and the number of fetches it took.
fn read(objects: &Objects, owner: &Owner) -> (Read, usize) {
    let mut fetches = 0;
    let got = read_owner(owner, |key| {
        fetches += 1;
        Ok(objects.get(key).cloned())
    });
    (got, fetches)
}
fn corrupt(fault: ChainFault) -> Read {
    Err(OwnerError::Corrupt(fault))
}
fn hash_of(c: &Chain, index: usize) -> R<Sha256> {
    Ok(c.builts.get(index).ok_or("no such manifest")?.hash)
}
fn restored(c: &Chain, folded: &Folded) -> R<Vec<u8>> {
    let entry = folded.state.sources.get(&source_id()?).ok_or("no source")?;
    restore_source(entry, |key| c.objects.get(key).cloned().ok_or("missing"))
        .map_err(|e| e.to_string())
}

#[test]
fn a_checkpoint_and_deltas_restore_the_expected_state() -> R {
    let c = chain(5)?;
    let (got, fetches) = read(&c.objects, &c.owner);
    let folded = got.map_err(|e| e.to_string())?.ok_or("absent")?;
    assert_eq!(folded.owner, c.owner);
    assert_eq!(folded.position.seq, 4);
    assert_eq!(folded.position.manifest, hash_of(&c, 4)?);
    assert_eq!(fetches, 1 + 5, "current.json plus five manifests");
    assert_eq!(restored(&c, &folded)?, c.whole);
    Ok(())
}

#[test]
fn the_walk_stops_at_the_nearest_checkpoint() -> R {
    let c = chain(66)?; // seq 64 is a checkpoint, the head is seq 65
    let (got, fetches) = read(&c.objects, &c.owner);
    let folded = got.map_err(|e| e.to_string())?.ok_or("absent")?;
    assert_eq!(fetches, 1 + 2);
    assert_eq!(restored(&c, &folded)?, c.whole);
    Ok(())
}

#[test]
fn an_absent_current_json_is_no_owner() -> R {
    let (got, fetches) = read(&Objects::new(), &owner("s1")?);
    assert_eq!(got, Ok(None));
    assert_eq!(fetches, 1);
    Ok(())
}

#[test]
fn a_fetch_error_passes_through() -> R {
    let got = read_owner(&owner("s1")?, |_| Err::<Option<Vec<u8>>, _>("boom"));
    assert_eq!(got, Err(OwnerError::Fetch("boom")));
    Ok(())
}

#[test]
fn a_manifest_that_does_not_hash_to_its_key_is_corrupt() -> R {
    let mut c = chain(3)?;
    let other = c.builts.first().ok_or("none")?.bytes.clone();
    c.objects.insert(manifest_key(&hash_of(&c, 1)?), other);
    assert_eq!(
        read(&c.objects, &c.owner).0,
        corrupt(ChainFault::HashMismatch)
    );
    Ok(())
}

#[test]
fn a_missing_manifest_is_corrupt_not_absent() -> R {
    for missing in [2, 1, 0] {
        let mut c = chain(3)?;
        c.objects.remove(&manifest_key(&hash_of(&c, missing)?));
        assert_eq!(
            read(&c.objects, &c.owner).0,
            corrupt(ChainFault::MissingManifest),
            "manifest {missing}"
        );
    }
    Ok(())
}

#[test]
fn sixty_five_deltas_without_a_checkpoint_stop_after_64_fetches() -> R {
    let owner = owner("s1")?;
    let mut objects = Objects::new();
    let mut previous = hex(b"never fetched");
    let mut head = None;
    // Sequence numbers skip 64, which would have to be a checkpoint.
    for seq in (1_u64..).filter(|s| s % 64 != 0).take(65) {
        let bytes = serde_json::to_vec(&serde_json::json!({
            "schema": "memory.manifest/1", "kind": "delta", "owner": owner, "seq": seq,
            "previous": previous, "checkpoint": hex(b"checkpoint"),
            "captured_at": "2026-11-07T10:30:00Z",
            "writer": {"name": "memory", "version": "v", "commit": "c"},
            "launch": [], "changes": [],
        }))
        .map_err(bad)?;
        let hash = manifest_hash(&bytes);
        objects.insert(manifest_key(&hash), bytes);
        previous = String::from(hash);
        head = Some((hash, seq));
    }
    let (hash, seq) = head.ok_or("no head")?;
    let pointer = Current::new(hash, seq, now()?);
    objects.insert(current_key(&owner), pointer.to_bytes().map_err(bad)?);
    let (got, fetches) = read(&objects, &owner);
    assert_eq!(got, corrupt(ChainFault::TooLong));
    assert_eq!(fetches, 1 + 64);
    Ok(())
}

#[test]
fn a_chain_of_another_owner_is_rejected() -> R {
    let mut c = chain(3)?;
    let other = owner("s2")?;
    let pointer = c.objects.get(&current_key(&c.owner)).cloned();
    c.objects
        .insert(current_key(&other), pointer.ok_or("no pointer")?);
    assert_eq!(read(&c.objects, &other).0, corrupt(ChainFault::WrongOwner));
    Ok(())
}

#[test]
fn a_newer_schema_is_not_corruption() -> R {
    let mut c = chain(3)?;
    let newer_current =
        serde_json::to_vec(&serde_json::json!({"schema": "memory.current/2"})).map_err(bad)?;
    let mut with_newer_current = c.objects.clone();
    with_newer_current.insert(current_key(&c.owner), newer_current);
    assert_eq!(
        read(&with_newer_current, &c.owner).0,
        Err(OwnerError::NewerSchema)
    );

    let newer_manifest =
        serde_json::to_vec(&serde_json::json!({"schema": "memory.manifest/2"})).map_err(bad)?;
    // Stored under its own hash so the hash check passes and the schema check is what fires.
    let hash = manifest_hash(&newer_manifest);
    c.objects.insert(manifest_key(&hash), newer_manifest);
    c.objects.insert(
        current_key(&c.owner),
        Current::new(hash, 1, now()?).to_bytes().map_err(bad)?,
    );
    assert_eq!(read(&c.objects, &c.owner).0, Err(OwnerError::NewerSchema));
    Ok(())
}
