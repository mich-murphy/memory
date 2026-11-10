//! Watcher tests: scheduler tables, properties against an independent model, translation
//! of notify events, and real FsWatcher runs on temp dirs this file creates.
use hegel::{TestCase, generators as gs};
use memory::watch::{Action, FsWatcher, Input, Scheduler, inbox, translate, translate_with};
use memory_core::coalescer::{CoalescerError, Signal, Window, WindowSeconds};
use memory_core::domain::{Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey};
use notify::event::{AccessKind, CreateKind, Flag, ModifyKind, RemoveKind, RenameMode};
use notify::{Event, EventKind};
use proptest::prelude::*;
use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, Instant};

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}
fn s(n: u64) -> Duration {
    Duration::from_secs(n)
}
fn window() -> R<Window> {
    Window::try_from(WindowSeconds {
        quiet: 60,
        cap: 300,
    })
    .map_err(e)
}
fn owner(i: usize) -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from(format!("s{i}")).map_err(e)?,
    }))
}
/// `/r/oN/...` maps to owner N; everything else is unmapped.
fn map_path(path: &Path) -> Option<Owner> {
    let text = path.to_str()?;
    let rest = text.strip_prefix("/r/o")?;
    let digit = rest.chars().next()?.to_digit(10)?;
    owner(usize::try_from(digit).ok()?).ok()
}
type Sched = Scheduler<fn(&Path) -> Option<Owner>>;
fn sched() -> R<Sched> {
    Ok(Scheduler::new(
        window()?,
        map_path as fn(&Path) -> Option<Owner>,
    ))
}
fn changed(i: usize) -> Input {
    Input::PathChanged {
        path: PathBuf::from(format!("/r/o{i}/f.jsonl")),
    }
}
fn tick(n: u64) -> Input {
    Input::Tick {
        wall: s(n),
        mono: s(n),
    }
}
fn cap(i: usize) -> R<Action> {
    Ok(Action::Capture { owner: owner(i)? })
}

// ------------------------------------------------------------------ scheduler tables

#[test]
fn cap_expiry_under_continuous_signals() -> R {
    let mut sc = sched()?;
    for t in (0..300).step_by(30) {
        assert_eq!(sc.handle(changed(1), s(t)), vec![], "t={t}");
    }
    assert_eq!(sc.handle(changed(1), s(300)), vec![cap(1)?]);
    Ok(())
}

#[test]
fn in_flight_owner_queues_exactly_one_follow_up() -> R {
    let mut sc = sched()?;
    sc.handle(changed(1), s(0));
    assert_eq!(sc.handle(tick(60), s(0)), vec![cap(1)?]);
    assert_eq!(sc.handle(changed(1), s(61)), vec![]);
    assert_eq!(sc.handle(changed(1), s(62)), vec![]);
    assert_eq!(sc.handle(tick(200), s(0)), vec![]);
    assert_eq!(sc.complete(&owner(1)?, s(201)).map_err(e)?, vec![cap(1)?]);
    assert_eq!(sc.handle(tick(400), s(0)), vec![]);
    assert_eq!(sc.complete(&owner(1)?, s(401)).map_err(e)?, vec![]);
    assert_eq!(
        sc.complete(&owner(1)?, s(402)),
        Err(CoalescerError::NotInFlight)
    );
    Ok(())
}

#[test]
fn unmapped_path_is_counted_and_ignored() -> R {
    let mut sc = sched()?;
    let input = Input::PathChanged {
        path: PathBuf::from("/x/f"),
    };
    assert_eq!(sc.handle(input, s(0)), vec![]);
    assert_eq!(sc.unmapped(), 1);
    assert_eq!(sc.handle(tick(500), s(0)), vec![]);
    Ok(())
}

#[test]
fn dir_created_reads_the_directory() -> R {
    let mut sc = sched()?;
    let path = PathBuf::from("/r/o1/new");
    assert_eq!(
        sc.handle(Input::DirCreated { path: path.clone() }, s(0)),
        vec![Action::ReadDir { path }]
    );
    Ok(())
}

#[test]
fn rescan_and_error_reconcile_all() -> R {
    let mut sc = sched()?;
    assert_eq!(sc.handle(Input::Rescan, s(0)), vec![Action::ReconcileAll]);
    assert_eq!(
        sc.handle(Input::WatcherError, s(1)),
        vec![Action::ReconcileAll]
    );
    Ok(())
}

#[test]
fn wake_gap_over_120_seconds_reconciles() -> R {
    let mut sc = sched()?;
    let t = |wall, mono| Input::Tick {
        wall: s(wall),
        mono: s(mono),
    };
    assert_eq!(sc.handle(t(0, 0), s(0)), vec![]);
    assert_eq!(sc.handle(t(60, 60), s(0)), vec![]);
    // gap exactly 120 s: no action
    assert_eq!(sc.handle(t(240, 120), s(0)), vec![]);
    // gap 121 s: reconcile
    assert_eq!(sc.handle(t(421, 180), s(0)), vec![Action::ReconcileAll]);
    Ok(())
}

#[test]
fn clock_regressions_are_counted_not_fatal() -> R {
    let mut sc = sched()?;
    sc.handle(changed(1), s(100));
    assert_eq!(sc.clock_errors(), 0);
    sc.handle(changed(1), s(50));
    assert_eq!(sc.clock_errors(), 2); // signal rejected, then poll rejected
    assert_eq!(sc.handle(Input::Rescan, s(40)), vec![Action::ReconcileAll]);
    assert_eq!(sc.clock_errors(), 3);
    Ok(())
}

// ------------------------------------------------------------------ translate

#[test]
fn translate_covers_every_event_shape() -> R {
    let p = PathBuf::from("/r/o1/a");
    let ev = |k| Event::new(k).add_path(p.clone());
    assert_eq!(
        translate(Err(notify::Error::generic("x"))),
        vec![Input::WatcherError]
    );
    assert_eq!(
        translate(Ok(
            ev(EventKind::Modify(ModifyKind::Any)).set_flag(Flag::Rescan)
        )),
        vec![Input::Rescan]
    );
    assert_eq!(
        translate(Ok(ev(EventKind::Create(CreateKind::Folder)))),
        vec![Input::DirCreated { path: p.clone() }]
    );
    let changed = vec![Input::PathChanged { path: p.clone() }];
    for kind in [
        EventKind::Any,
        EventKind::Create(CreateKind::File),
        EventKind::Modify(ModifyKind::Any),
        EventKind::Remove(RemoveKind::File),
    ] {
        assert_eq!(translate(Ok(ev(kind))), changed, "{kind:?}");
    }
    assert_eq!(
        translate(Ok(ev(EventKind::Access(AccessKind::Any)))),
        vec![]
    );
    assert_eq!(translate(Ok(ev(EventKind::Other))), vec![]);
    Ok(())
}

#[test]
fn directory_moved_into_a_root_reads_its_children() {
    // FSEvents reports a moved-in directory as one rename event (W4 Q3), with no create flag.
    let dir = PathBuf::from("/r/o1/moved");
    let ev = |k| Event::new(k).add_path(dir.clone());
    let rename = EventKind::Modify(ModifyKind::Name(RenameMode::Any));
    let is_dir = |p: &Path| p == Path::new("/r/o1/moved");
    assert_eq!(
        translate_with(Ok(ev(rename)), is_dir),
        vec![Input::DirCreated { path: dir.clone() }]
    );
    assert_eq!(
        translate_with(Ok(ev(EventKind::Create(CreateKind::Any))), is_dir),
        vec![Input::DirCreated { path: dir.clone() }]
    );
    // A renamed file, or a renamed-away path that no longer exists, is a plain change.
    let changed = vec![Input::PathChanged { path: dir.clone() }];
    assert_eq!(translate_with(Ok(ev(rename)), |_| false), changed);
    // Content modifications of a directory path are not directory appearances.
    assert_eq!(
        translate_with(Ok(ev(EventKind::Modify(ModifyKind::Any))), is_dir),
        changed
    );
    assert_eq!(
        translate_with(Ok(ev(EventKind::Remove(RemoveKind::Folder))), is_dir),
        changed
    );
}

#[test]
fn full_inbox_drops_inputs_and_reports_one_rescan() {
    let (tx, rx) = inbox(2);
    let at = |n: u32| Input::PathChanged {
        path: PathBuf::from(format!("/r/o1/{n}")),
    };
    for n in 0..5 {
        tx.push(at(n));
    }
    // The overflow is reported first, once; then the queued inputs, then silence.
    let none = Duration::ZERO;
    assert_eq!(rx.recv_timeout(none), Some(Input::Rescan));
    assert_eq!(rx.recv_timeout(none), Some(at(0)));
    assert_eq!(rx.recv_timeout(none), Some(at(1)));
    assert_eq!(rx.recv_timeout(none), None);
    // After draining, the queue accepts inputs again without a spurious rescan.
    tx.push(at(9));
    assert_eq!(rx.recv_timeout(none), Some(at(9)));
}

// ------------------------------------------------------------------ properties

/// Independent model: per-owner pending window and in-flight flag, no shared code.
#[derive(Default, Clone)]
struct Model {
    pending: Option<(u64, u64, bool)>,
    in_flight: bool,
}

/// Kind (9 shapes), owner index, and a gap that is below the quiet window three times in
/// four, so runs of signals reach the 300 s cap without ever being quiet for 60 s.
fn decode(n: u32) -> (u32, usize, u64) {
    let wide = (n / 24).is_multiple_of(4);
    let gap = if wide { (n / 96) % 201 } else { (n / 96) % 50 };
    (
        n % 9,
        usize::try_from((n / 9) % 4).unwrap_or(0),
        u64::from(gap),
    )
}

fn property(raw: &[u32]) -> R {
    let mut sc = sched()?;
    let mut model: BTreeMap<usize, Model> = BTreeMap::new();
    let mut now = 0u64;
    for &n in raw {
        let (kind, who, dt) = decode(n);
        now += dt;
        let mut actions = Vec::new();
        let mut reads = Vec::new();
        let mut completed = false;
        let signalled = match kind {
            0 => {
                actions.extend(sc.handle(changed(who), s(now)));
                Some((who, false))
            }
            1..=3 => {
                let who = who % 3;
                let signal = match kind {
                    1 => Signal::Changed,
                    2 => Signal::Stop,
                    _ => Signal::SessionEnd,
                };
                let hint = Input::Hint {
                    owner: owner(who)?,
                    signal,
                };
                actions.extend(sc.handle(hint, s(now)));
                Some((who, kind == 3))
            }
            4 => {
                let who = who % 3;
                completed = model.get(&who).is_some_and(|m| m.in_flight);
                if completed {
                    actions.extend(sc.complete(&owner(who)?, s(now)).map_err(e)?);
                    if let Some(m) = model.get_mut(&who) {
                        m.in_flight = false;
                    }
                } else {
                    actions.extend(sc.handle(Input::Rescan, s(now)));
                }
                None
            }
            5 => {
                actions.extend(sc.handle(Input::Rescan, s(now)));
                None
            }
            6 => {
                actions.extend(sc.handle(tick(now), s(0)));
                None
            }
            7 => {
                let path = PathBuf::from(format!("/r/o{who}/d"));
                actions.extend(sc.handle(Input::DirCreated { path: path.clone() }, s(now)));
                reads.push(Action::ReadDir { path });
                None
            }
            _ => {
                actions.extend(sc.handle(Input::WatcherError, s(now)));
                None
            }
        };
        // owner 3 via PathChanged maps to /r/o3 which is mapped too: all 0..4 are owners.
        if let Some((who, flush)) = signalled {
            let m = model.entry(who).or_default();
            m.pending = Some(match m.pending {
                Some((first, _, f)) => (first, now, f || flush),
                None => (now, now, flush),
            });
        }
        // Non-capture actions: a directory is read, loss and errors reconcile everything.
        let mut other_want = reads;
        if matches!(kind, 5 | 8) || (kind == 4 && !completed) {
            other_want.push(Action::ReconcileAll);
        }
        let other_got: Vec<Action> = actions
            .iter()
            .filter(|a| !matches!(a, Action::Capture { .. }))
            .cloned()
            .collect();
        if other_got != other_want {
            return Err(format!("t={now} actions {other_got:?} want {other_want:?}"));
        }
        let got: Vec<Owner> = actions
            .into_iter()
            .filter_map(|a| match a {
                Action::Capture { owner } => Some(owner),
                _ => None,
            })
            .collect();
        let mut want = Vec::new();
        for (&who, m) in &mut model {
            if m.in_flight {
                continue;
            }
            if let Some((first, last, flush)) = m.pending
                && (flush || now - last >= 60 || now - first >= 300)
            {
                m.pending = None;
                m.in_flight = true;
                want.push(owner(who)?);
            }
        }
        let key = |o: &Owner| format!("{o:?}");
        let mut got_sorted = got.clone();
        got_sorted.sort_by_key(key);
        want.sort_by_key(key);
        if got_sorted != want {
            return Err(format!("t={now} got {got:?} want {want:?}"));
        }
    }
    // Settle: complete everything in flight, then wait out the quiet window.
    let flying: Vec<usize> = model
        .iter()
        .filter(|(_, m)| m.in_flight)
        .map(|(&k, _)| k)
        .collect();
    for who in flying {
        for action in sc.complete(&owner(who)?, s(now)).map_err(e)? {
            if let Action::Capture { owner: o } = action {
                if o != owner(who)? {
                    return Err("follow-up for another owner".into());
                }
                if let Some(m) = model.get_mut(&who) {
                    if m.pending.take().is_none() {
                        return Err("capture without a signal".into());
                    }
                    m.in_flight = true;
                }
            }
        }
    }
    now += 60;
    for action in sc.handle(Input::Rescan, s(now)) {
        if let Action::Capture { owner: o } = action {
            let hit = model
                .iter_mut()
                .find(|&(&k, _)| owner(k).as_ref() == Ok(&o));
            match hit {
                Some((_, m)) if m.pending.is_some() => {
                    m.pending = None;
                    m.in_flight = true;
                }
                _ => return Err("capture without a signal".into()),
            }
        }
    }
    for (who, m) in &model {
        if m.pending.is_some() && !m.in_flight {
            return Err(format!("owner {who} signalled but never emitted"));
        }
    }
    Ok(())
}

#[hegel::test]
fn hegel_scheduler_matches_model(tc: TestCase) {
    let raw: Vec<u32> = tc.draw(gs::vecs(gs::integers::<u32>()));
    let r = property(&raw);
    assert!(r.is_ok(), "{r:?}");
}

proptest! {
    #[test]
    fn proptest_scheduler_matches_model(raw in proptest::collection::vec(any::<u32>(), 0..80)) {
        let r = property(&raw);
        prop_assert!(r.is_ok(), "{r:?}");
    }
}

// ------------------------------------------------------------------ FsWatcher on temp dirs

struct Tmp(PathBuf);
impl Tmp {
    fn new() -> R<Self> {
        static N: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "memory-watch-{}-{}",
            std::process::id(),
            N.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&path).map_err(e)?;
        Ok(Self(path.canonicalize().map_err(e)?))
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

/// Poll until `want` accepts an input or 10 s pass (FSEvents latency is host dependent).
fn wait_for(w: &FsWatcher, mut want: impl FnMut(&Input) -> bool) -> bool {
    let deadline = Instant::now() + Duration::from_secs(10);
    while Instant::now() < deadline {
        if let Some(input) = w.recv_timeout(Duration::from_millis(200))
            && want(&input)
        {
            return true;
        }
    }
    false
}

#[test]
fn fs_watcher_reports_file_write() -> R {
    let tmp = Tmp::new()?;
    let w = FsWatcher::new(std::slice::from_ref(&tmp.0)).map_err(e)?;
    let file = tmp.0.join("a.jsonl");
    fs::write(&file, "x").map_err(e)?;
    let seen = wait_for(
        &w,
        |i| matches!(i, Input::PathChanged { path } if *path == file),
    );
    assert!(seen, "no PathChanged for {}", file.display());
    Ok(())
}

#[test]
fn fs_watcher_reports_new_directory() -> R {
    let tmp = Tmp::new()?;
    let w = FsWatcher::new(std::slice::from_ref(&tmp.0)).map_err(e)?;
    let dir = tmp.0.join("sub");
    fs::create_dir(&dir).map_err(e)?;
    fs::write(dir.join("b.jsonl"), "x").map_err(e)?;
    let seen = wait_for(
        &w,
        |i| matches!(i, Input::DirCreated { path } if *path == dir),
    );
    assert!(seen, "no DirCreated for {}", dir.display());
    Ok(())
}

#[test]
fn fs_watcher_reports_directory_moved_in_with_children() -> R {
    let tmp = Tmp::new()?;
    let root = tmp.0.join("root");
    let stage = tmp.0.join("stage");
    fs::create_dir_all(&root).map_err(e)?;
    fs::create_dir_all(stage.join("p")).map_err(e)?;
    fs::write(stage.join("p/k.jsonl"), "x").map_err(e)?;
    let w = FsWatcher::new(std::slice::from_ref(&root)).map_err(e)?;
    let dir = root.join("p");
    fs::rename(stage.join("p"), &dir).map_err(e)?;
    // The children produce no events (W4 Q3); the directory itself must be reported so
    // the scheduler reads it.
    let seen = wait_for(
        &w,
        |i| matches!(i, Input::DirCreated { path } if *path == dir),
    );
    assert!(seen, "no DirCreated for moved-in {}", dir.display());
    Ok(())
}

#[test]
fn fs_watcher_rejects_a_missing_root() -> R {
    // Documents the current behaviour: one absent root fails the whole watcher (report 02).
    // A quiet-period check is deliberately absent: FSEvents startup noise has no bound.
    let tmp = Tmp::new()?;
    assert!(FsWatcher::new(&[tmp.0.join("missing")]).is_err());
    assert!(FsWatcher::new(&[tmp.0.clone(), tmp.0.join("missing")]).is_err());
    Ok(())
}
