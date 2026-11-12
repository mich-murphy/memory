//! Measurement only: per-commit cost of `commit_known` on the shared in-memory store for the
//! three success paths (plain commit, verified by GET, adopted earlier attempt).
use memory::capture::Published;
use memory::commit::{Base, Commit, CommitEnd, Failure, KnownOurs, commit_known};
use memory::environment::{Store, StoreError, Stored};
use memory_core::domain::{Agent, ArchiveId, ETag, MachineId, Owner, SessionId, SessionKey};
use memory_core::limits::current_key;
use memory_core::manifest::{Current, Header, Timestamp, Writer};
use std::hint::black_box;
use std::time::Instant;

#[path = "common/store.rs"]
mod mem_store;
use mem_store::MemStore;

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}

#[derive(Clone, Copy, PartialEq)]
enum Mode {
    Plain,
    LostReply,
    FailAll,
    Foreign,
}
struct Bench {
    mem: MemStore,
    mode: Mode,
    foreign: Option<(String, Vec<u8>, Vec<u8>)>,
}
impl Bench {
    fn put(
        &mut self,
        key: &str,
        body: Published,
        fence: Option<&ETag>,
    ) -> Result<ETag, StoreError> {
        let current = key.ends_with("current.json");
        if current && self.mode == Mode::FailAll {
            return Err(StoreError::Offline);
        }
        if current
            && self.mode == Mode::Foreign
            && let Some((mkey, manifest, pointer)) = self.foreign.take()
        {
            self.mem.write(&mkey, manifest);
            self.mem.write(key, pointer);
        }
        let tag = match fence {
            None => self.mem.put_if_absent(key, body),
            Some(etag) => self.mem.put_if_match(key, etag, body),
        }?;
        if current && self.mode == Mode::LostReply {
            return Err(StoreError::Ambiguous);
        }
        Ok(tag)
    }
}
impl Store for Bench {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        self.put(key, body, None)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        self.put(key, body, Some(etag))
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        if key.ends_with("current.json") && self.mode == Mode::FailAll {
            return Err(StoreError::Offline);
        }
        self.mem.get(key)
    }
}

fn owner() -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned()).map_err(e)?,
    }))
}
fn ts() -> R<Timestamp> {
    Timestamp::try_from("2026-11-07T10:30:00Z".to_owned()).map_err(e)
}
fn request(owner: &Owner, who: &str) -> R<Commit> {
    Ok(Commit {
        header: Header {
            owner: owner.clone(),
            captured_at: ts()?,
            writer: Writer {
                name: who.to_owned(),
                version: "0.1.0".to_owned(),
                commit: "abc".to_owned(),
            },
            launch: vec![],
        },
        changes: vec![],
        uploads: vec![],
    })
}

type Res = Result<CommitEnd, Failure>;
// The only lines a `Landed` type change forces: how a result yields the next base.
fn next_base(result: &Res) -> Option<Base> {
    result.as_ref().ok().map(CommitEnd::base)
}

fn run(mode: Mode, n: usize) -> R<u128> {
    let owner = owner()?;
    let mut store = Bench {
        mem: MemStore::default(),
        mode: Mode::Plain,
        foreign: None,
    };
    let mut known = KnownOurs::new();
    let first = commit_known(
        &mut store,
        None,
        request(&owner, "w0")?,
        &mut known,
        &mut |_| {},
    );
    let mut base = next_base(&first).ok_or("first commit")?;
    let mut total = 0_u128;
    for i in 0..n {
        let name = format!("w{i}");
        match mode {
            Mode::Foreign => {
                // Capture A fails without landing; its manifest stays in `known_ours`.
                store.mode = Mode::FailAll;
                let a = commit_known(
                    &mut store,
                    Some(&base),
                    request(&owner, &name)?,
                    &mut known,
                    &mut |_| {},
                );
                assert!(matches!(a, Err(Failure::LandingUnknown(_))));
                let m1 = *known.keys().next().ok_or("known")?;
                let seq = known.get(&m1).ok_or("built")?.position.seq;
                store.foreign = Some((
                    memory::commit::manifest_key(&m1),
                    store
                        .mem
                        .objects
                        .get(&memory::commit::manifest_key(&m1))
                        .map(|o| o.0.clone())
                        .ok_or("m1")?,
                    Current::new(m1, seq, ts()?).to_bytes().map_err(e)?,
                ));
                store.mode = Mode::Foreign;
            }
            _ => store.mode = mode,
        }
        let req = request(&owner, &format!("x{i}"))?;
        let start = Instant::now();
        let result = commit_known(&mut store, Some(&base), req, &mut known, &mut |_| {});
        let result = black_box(result);
        total += start.elapsed().as_nanos();
        base = next_base(&result).ok_or_else(|| format!("{i}: {:?}", result.err()))?;
        let _ = current_key(&owner);
    }
    Ok(total / n as u128)
}

#[test]
#[ignore = "measurement: run with --release -- --ignored --nocapture"]
fn landed_bench() -> R {
    let n = 10_000;
    let plain = run(Mode::Plain, n)?;
    let verified = run(Mode::LostReply, n)?;
    let adopted = run(Mode::Foreign, n)?;
    eprintln!("BENCH n={n} plain={plain}ns verified={verified}ns adopted={adopted}ns");
    eprintln!(
        "SIZE landed={} result={}",
        std::mem::size_of::<CommitEnd>(),
        std::mem::size_of::<Res>()
    );
    Ok(())
}
