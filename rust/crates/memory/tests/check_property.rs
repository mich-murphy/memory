//! The streamed `memory check` judgement equals the whole-buffer one for every input, reason code
//! included. Both paths run over an in-memory file with injected faults and an in-memory store.
use hegel::{TestCase, generators as gs};
use memory::capture::Source;
use memory::check::{Verdict, judge_whole, judge_with};
use memory::environment::{FileSystem, Stat, StoreError};
use memory_core::domain::{Birth, FileId, Offset, SourceClass, SourceId, SourceKind};
use memory_core::manifest::{
    EpochRecord, EpochStamp, EpochWriter, OwnerState, Piece, SourceEntry, manifest_hash,
};
use memory_core::privacy::{PolicyContext, Profile, ResolvedSourceFacts, rules_digest};
use std::collections::{BTreeMap, HashMap};

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}

/// A one-file filesystem with injected faults. `extra` makes the stat size larger than the
/// bytes (a short read); a read overlapping `err_at` fails; `moves` changes the mtime after
/// the first stat.
#[derive(Clone)]
struct Fs {
    bytes: Vec<u8>,
    extra: u64,
    err_at: Option<u64>,
    moves: bool,
    stats: u32,
}

impl Fs {
    fn plain(bytes: &[u8]) -> Self {
        Self {
            bytes: bytes.to_vec(),
            extra: 0,
            err_at: None,
            moves: false,
            stats: 0,
        }
    }
}

impl FileSystem for Fs {
    type Error = ();
    fn stat(&mut self, _: u64) -> Result<Option<Stat>, ()> {
        self.stats += 1;
        let size = u64::try_from(self.bytes.len())
            .map_err(|_| ())?
            .saturating_add(self.extra);
        Ok(Some(Stat {
            id: FileId {
                dev: 1,
                ino: 1,
                birth: Birth::Unavailable,
            },
            size,
            mtime_ns: if self.moves && self.stats > 1 { 2 } else { 1 },
        }))
    }
    fn read(&mut self, _: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, ()> {
        if let Some(at) = self.err_at
            && range.start <= at
            && at < range.end
        {
            return Err(());
        }
        let start = usize::try_from(range.start).map_err(|_| ())?;
        let end = usize::try_from(range.end)
            .map_err(|_| ())?
            .min(self.bytes.len());
        Ok(self.bytes.get(start..end).unwrap_or_default().to_vec())
    }
}

struct World {
    record: EpochRecord,
    objects: HashMap<String, Vec<u8>>,
}

fn build(content: &[u8], lens: &[usize]) -> R<World> {
    let mut objects = HashMap::new();
    let mut pieces = Vec::new();
    let mut at = 0_usize;
    let mut i = 0_usize;
    while at < content.len() {
        let len = lens.get(i % lens.len().max(1)).copied().unwrap_or(1).max(1);
        let end = at.saturating_add(len).min(content.len());
        let raw = content.get(at..end).ok_or("range")?;
        let sha = manifest_hash(raw);
        let stored = zstd::encode_all(raw, 3).map_err(e)?;
        let object = format!("objects/{}.zst", String::from(sha));
        let piece: Piece = serde_json::from_value(serde_json::json!({
            "offset": Offset::new(u64::try_from(at).map_err(e)?).get(),
            "len": raw.len(), "sha256": String::from(sha),
            "object": object, "encoding": "zstd-3",
            "encoded_len": stored.len(),
            "encoded_sha256": String::from(manifest_hash(&stored)),
        }))
        .map_err(e)?;
        objects.insert(object, stored);
        pieces.push(piece);
        at = end;
        i += 1;
    }
    Ok(World {
        record: EpochRecord {
            epoch: EpochStamp {
                n: 0,
                writer: EpochWriter {
                    version: "0.1.0".to_owned(),
                    commit: "abc".to_owned(),
                },
                rules: None,
            },
            size: u64::try_from(content.len()).map_err(e)?,
            sha256: manifest_hash(content),
            pieces,
        },
        objects,
    })
}

fn source() -> R<Source> {
    let path = "projects/p/s.jsonl".to_owned();
    Ok(Source {
        handle: 1,
        id: SourceId::try_from("src1".to_owned()).map_err(e)?,
        kind: SourceKind::ClaudeTranscript,
        class: SourceClass::Append,
        path: path.clone(),
        facts: ResolvedSourceFacts {
            identities: vec![path],
            file_identity: Some(1),
            ancestor_identities: vec![],
            path_resolved: true,
            root_resolved: true,
            owner: Some("o".to_owned()),
            profile: Profile::Transcript,
        },
    })
}

fn context() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "r".to_owned(),
        rules: Some(rules_digest()),
        collector_paths: vec![],
        collector_identities: vec![],
    }
}

fn state_of(src: &Source, record: Option<EpochRecord>) -> OwnerState {
    let mut sources = BTreeMap::new();
    if let Some(record) = record {
        sources.insert(
            src.id.clone(),
            SourceEntry {
                kind: src.kind.clone(),
                path: src.path.clone(),
                class: src.class.clone(),
                epochs: vec![record],
                deleted_at: None,
            },
        );
    }
    OwnerState { sources }
}

/// Both verdicts for one input; the fetch faults are keyed by object.
fn both(
    fs: &Fs,
    world: &World,
    state: &OwnerState,
    missing: Option<&str>,
    server: Option<&str>,
    chunk: usize,
) -> R<(Verdict, Verdict)> {
    let src = source()?;
    let ctx = context();
    let fetch = || {
        |key: &str| {
            if Some(key) == missing {
                Err(StoreError::NotFound)
            } else if Some(key) == server {
                Err(StoreError::Server)
            } else {
                world.objects.get(key).cloned().ok_or(StoreError::NotFound)
            }
        }
    };
    let whole = judge_whole(&mut fs.clone(), fetch(), &ctx, Some(state), &src);
    let streamed = judge_with(&mut fs.clone(), fetch(), chunk, &ctx, Some(state), &src);
    Ok((whole, streamed))
}

fn draw_u8(tc: &TestCase, max: u8) -> usize {
    usize::from(tc.draw(gs::integers::<u8>().min_value(0).max_value(max)))
}

fn case(tc: &TestCase) -> R {
    let local: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()).max_size(120));
    let kind = draw_u8(tc, 5);
    let cut = draw_u8(tc, 255) % (local.len() + 1);
    let at = draw_u8(tc, 255) % local.len().max(1);
    let extra_bytes: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()).max_size(20));
    let stored: Vec<u8> = match kind {
        0 => local.clone(),
        1 => local.get(..cut).unwrap_or_default().to_vec(),
        2 => [local.clone(), extra_bytes].concat(),
        3 => {
            let mut flipped = local.clone();
            match flipped.get_mut(at) {
                Some(b) => *b ^= 0x55,
                None => flipped.push(1),
            }
            flipped
        }
        _ => Vec::new(),
    };
    let lens: Vec<usize> = (0..4).map(|_| 1 + draw_u8(tc, 19)).collect();
    let chunk = 1 + draw_u8(tc, 63);
    let rare = || draw_u8(tc, 7) == 0;
    let (f_missing, f_server, f_corrupt, f_sha, f_size, f_seam, f_short, f_move) = (
        rare(),
        rare(),
        rare(),
        rare(),
        rare(),
        rare(),
        rare(),
        rare(),
    );
    let which = draw_u8(tc, 255);
    let seam_at = draw_u8(tc, 255) % (local.len() + 1);
    let short_by = 1 + draw_u8(tc, 4);
    let corrupt_at = draw_u8(tc, 255);

    let mut world = build(&stored, &lens)?;
    let key_of = |w: &World| -> Option<String> {
        let n = w.record.pieces.len();
        (n > 0)
            .then(|| w.record.pieces.get(which % n))
            .flatten()
            .map(|p| p.object().to_owned())
    };
    let target = key_of(&world);
    if f_corrupt
        && let Some(key) = &target
        && let Some(bytes) = world.objects.get_mut(key)
    {
        let n = bytes.len();
        if let Some(b) = bytes.get_mut(corrupt_at % n.max(1)) {
            *b ^= 0x21;
        }
    }
    if f_sha {
        world.record.sha256 = manifest_hash(b"wrong");
    }
    if f_size {
        world.record.size += 1;
    }
    let mut fs = Fs::plain(&local);
    if f_seam {
        fs.err_at = Some(u64::try_from(seam_at).map_err(e)?);
    }
    if f_short {
        fs.extra = u64::try_from(short_by).map_err(e)?;
    }
    fs.moves = f_move;
    let src = source()?;
    let state = if kind == 5 {
        state_of(&src, None)
    } else {
        state_of(&src, Some(world.record.clone()))
    };
    let missing = f_missing.then(|| target.clone()).flatten();
    let server = f_server.then(|| target.clone()).flatten();
    let (whole, streamed) = both(
        &fs,
        &world,
        &state,
        missing.as_deref(),
        server.as_deref(),
        chunk,
    )?;
    if whole == streamed {
        Ok(())
    } else {
        Err(format!("whole {whole:?} != streamed {streamed:?}"))
    }
}

#[test]
fn hegel_streamed_check_verdict_equals_whole() {
    hegel::Hegel::new(|tc: TestCase| {
        let result = case(&tc);
        assert!(result.is_ok(), "{result:?}");
    })
    .run();
}

#[test]
fn a_file_that_changes_between_the_stats_is_unsettled_when_streamed() -> R {
    let local = b"abcdefgh".to_vec();
    let world = build(&local, &[3])?;
    let src = source()?;
    let state = state_of(&src, Some(world.record.clone()));
    let mut fs = Fs::plain(&local);
    fs.moves = true;
    let (whole, streamed) = both(&fs, &world, &state, None, None, 2)?;
    assert_eq!((whole, streamed), (Verdict::Unsettled, Verdict::Unsettled));
    fs.moves = false;
    let (_, streamed) = both(&fs, &world, &state, None, None, 2)?;
    assert_eq!(streamed, Verdict::Match);
    Ok(())
}

#[test]
fn a_difference_after_the_local_end_with_a_corrupt_later_piece_is_restore_not_bytes() -> R {
    // The stored stream is longer than the local file (bytes difference) and its last piece is
    // corrupt: the streamed path must still fetch and decode it, so the reason is `restore`.
    let local = b"abc".to_vec();
    let mut world = build(b"abcdefgh", &[3])?;
    let last = world
        .record
        .pieces
        .last()
        .map(|p| p.object().to_owned())
        .ok_or("piece")?;
    let stored = world.objects.get_mut(&last).ok_or("object")?;
    if let Some(b) = stored.first_mut() {
        *b ^= 0xff;
    }
    let src = source()?;
    let state = state_of(&src, Some(world.record.clone()));
    let fs = Fs::plain(&local);
    let (whole, streamed) = both(&fs, &world, &state, None, None, 2)?;
    assert_eq!(whole, Verdict::Mismatch("restore"));
    assert_eq!(streamed, whole);
    Ok(())
}
