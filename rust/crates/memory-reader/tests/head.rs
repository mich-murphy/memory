//! `read_owner_head` returns the head manifest's `captured_at` and `current.json`'s `updated_at`.
use memory_core::domain::{
    Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey, Sha256, SourceClass, SourceId,
    SourceKind,
};
use memory_core::limits::{current_key, manifest_key};
use memory_core::manifest::{
    Built, Change, Current, EpochStamp, EpochWriter, Header, SourceWrite, Timestamp, Writer, build,
};
use memory_reader::{read_owner, read_owner_head};
use std::collections::HashMap;

mod common;
use common::{R, enc, hex, piece};

fn bad<T: std::fmt::Debug>(e: T) -> String {
    format!("{e:?}")
}
fn ts(s: &str) -> R<Timestamp> {
    Timestamp::try_from(s.to_owned()).map_err(bad)
}

/// Objects of a chain of `count` manifests whose `captured_at` is 10:30 + i minutes and whose
/// `current.json` says 11:00.
fn chain(count: usize) -> R<(HashMap<String, Vec<u8>>, Owner)> {
    let owner = Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(bad)?,
        machine: MachineId::try_from("m".to_owned()).map_err(bad)?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned()).map_err(bad)?,
    });
    let mut objects = HashMap::new();
    let mut builts: Vec<Built> = Vec::new();
    let mut whole: Vec<u8> = Vec::new();
    for i in 0..count {
        let c = format!("line {i}\n").repeat(5).into_bytes();
        let stored = enc(&c)?;
        let p = piece(&c, whole.len() as u64, &stored)?;
        objects.insert(p.object().to_owned(), stored);
        whole.extend(&c);
        let write = SourceWrite {
            id: SourceId::try_from("t".to_owned()).map_err(bad)?,
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
            size: whole.len() as u64,
            sha256: Sha256::try_from(hex(&whole)).map_err(bad)?,
            pieces: vec![p],
        };
        let change = if i == 0 {
            Change::Epoch(write)
        } else {
            Change::Extend(write)
        };
        let header = Header {
            owner: owner.clone(),
            captured_at: ts(&format!("2026-11-07T10:{:02}:00Z", 30 + i))?,
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
    let h = builts.last().ok_or("empty")?;
    let pointer = Current::new(h.hash, h.position.seq, ts("2026-11-07T11:00:00Z")?);
    objects.insert(current_key(&owner), pointer.to_bytes().map_err(bad)?);
    Ok((objects, owner))
}

#[test]
fn the_head_carries_the_head_manifests_captured_at_and_currents_updated_at() -> R {
    for (count, captured) in [(1usize, 1_794_047_400u64), (3, 1_794_047_520)] {
        let (objects, owner) = chain(count)?;
        let head = read_owner_head(&owner, |k| Ok::<_, ()>(objects.get(k).cloned()))
            .map_err(bad)?
            .ok_or("absent")?;
        assert_eq!(
            head.captured_at.unix_ns(),
            Some(captured * 1_000_000_000),
            "{count}"
        );
        assert_eq!(head.updated_at.unix_ns(), Some(1_794_049_200_000_000_000));
        assert_eq!(head.folded.position.seq, (count - 1) as u64);
        let plain = read_owner(&owner, |k| Ok::<_, ()>(objects.get(k).cloned())).map_err(bad)?;
        assert_eq!(plain, Some(head.folded), "read_owner unchanged");
    }
    Ok(())
}

#[test]
fn an_absent_owner_has_no_head() -> R {
    let (_, owner) = chain(1)?;
    let got = read_owner_head(&owner, |_| Ok::<Option<Vec<u8>>, ()>(None)).map_err(bad)?;
    assert!(got.is_none());
    Ok(())
}
