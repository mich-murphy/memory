//! The event-loop driver with a scripted input source, a recording worker and a fake clock:
//! no sleeping, no watcher, no filesystem. Times in logs are seconds since the driver started.
use memory::driver::{Cause, Inputs, Next, Route, Timings, Work, drive};
use memory::environment::Clock;
use memory::run::{Failed, Outcome};
use memory::watch::Input;
use memory_core::coalescer::{Signal, Window, WindowSeconds};
use memory_core::domain::{Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey};
use std::cell::{Cell, RefCell};
use std::collections::VecDeque;
use std::path::{Path, PathBuf};
use std::time::Duration;

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}
fn s(n: u64) -> Duration {
    Duration::from_secs(n)
}
const NS: u64 = 1_000_000_000;
/// The monotonic clock does not start at zero: deadlines must be absolute instants.
const BASE: u64 = 1_000;

fn owner(i: usize) -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from(format!("s{i}")).map_err(e)?,
    }))
}

#[derive(Debug, Clone, PartialEq, Eq)]
enum Ev {
    Capture(usize),
    Reconcile(Cause),
    Hints,
    Shutdown,
}
type Entry = (u64, Ev);
fn start(t: u64) -> Entry {
    (t, Ev::Reconcile(Cause::Start))
}
fn recon(t: u64, cause: Cause) -> Entry {
    (t, Ev::Reconcile(cause))
}
fn cap(t: u64, i: usize) -> Entry {
    (t, Ev::Capture(i))
}
fn hints(t: u64) -> Entry {
    (t, Ev::Hints)
}
fn shut(t: u64) -> Entry {
    (t, Ev::Shutdown)
}

/// Fake clock plus the log and the arrival queue shared by the script and the worker.
struct Fake {
    mono: Cell<u64>,
    skew: Cell<u64>,
    stop: Cell<bool>,
    log: RefCell<Vec<Entry>>,
}
impl Clock for Fake {
    fn monotonic_ns(&self) -> u64 {
        self.mono.get()
    }
    fn wall_ns(&self) -> i128 {
        i128::from(self.mono.get()) + i128::from((5_000 + self.skew.get()) * NS)
    }
}
impl Fake {
    fn new() -> Self {
        Self {
            mono: Cell::new(BASE * NS),
            skew: Cell::new(0),
            stop: Cell::new(false),
            log: RefCell::new(Vec::new()),
        }
    }
    fn rel(&self) -> u64 {
        (self.mono.get() / NS).saturating_sub(BASE)
    }
    fn push(&self, ev: Ev) {
        self.log.borrow_mut().push((self.rel(), ev));
    }
    fn advance_to(&self, ns: u64) {
        if ns > self.mono.get() {
            self.mono.set(ns);
        }
    }
}

enum Step {
    In(Input),
    /// Wall clock runs this many seconds ahead of the monotonic clock from now on.
    Skew(u64),
    /// A stop is requested, without being returned by the wait.
    StopFlag,
    /// A stop is requested and the wait returns it.
    Stop,
    /// The source dies.
    Fail,
}

struct Script<'a> {
    fake: &'a Fake,
    steps: VecDeque<(u64, Step)>,
    until: u64,
    calls: u32,
}
impl Inputs for Script<'_> {
    fn next(&mut self, up_to: Duration) -> Next {
        self.calls += 1;
        if self.calls > 10_000 {
            return Next::Failed; // a runaway loop must fail an assertion, not hang
        }
        let target = self.fake.mono.get() + u64::try_from(up_to.as_nanos()).unwrap_or(0);
        let until = (BASE + self.until) * NS;
        loop {
            let at = self.steps.front().map(|(at, _)| (BASE + at) * NS);
            match at {
                Some(at) if at <= target && at <= until => {
                    self.fake.advance_to(at);
                    let Some((_, step)) = self.steps.pop_front() else {
                        return Next::Failed;
                    };
                    match step {
                        Step::In(input) => return Next::Input(input),
                        Step::Skew(n) => self.fake.skew.set(n),
                        Step::StopFlag => {
                            self.fake.stop.set(true);
                            return Next::Timeout;
                        }
                        Step::Stop => {
                            self.fake.stop.set(true);
                            return Next::Stop;
                        }
                        Step::Fail => return Next::Failed,
                    }
                }
                _ if until <= target => {
                    self.fake.advance_to(until);
                    self.fake.stop.set(true);
                    return Next::Stop;
                }
                _ => {
                    self.fake.advance_to(target);
                    return Next::Timeout;
                }
            }
        }
    }
    fn stop_requested(&mut self) -> bool {
        self.fake.stop.get()
    }
}

struct Rec<'a> {
    fake: &'a Fake,
    owners: Vec<Owner>,
    fail_capture_at: Option<usize>,
    fail_reconcile_at: Option<usize>,
    fail_shutdown: bool,
    stop_after_captures: Option<usize>,
    stop_after_reconciles: Option<usize>,
    hint_batches: VecDeque<Vec<(Owner, Signal)>>,
    /// Retry deadlines: (seconds since start, owner index).
    retries: Vec<(u64, usize)>,
    /// Retries the work leaves due after offering them (a paused host does this).
    sticky_retries: Vec<(u64, usize)>,
    reconcile_cost: u64,
    captures: usize,
    reconciles: usize,
}
impl Rec<'_> {
    fn index(&self, owner: &Owner) -> usize {
        self.owners
            .iter()
            .position(|o| o == owner)
            .unwrap_or(usize::MAX)
    }
}
impl Work for Rec<'_> {
    fn classify(&mut self, path: &Path) -> Route {
        let text = path.to_str().unwrap_or("");
        if text.starts_with("/h/") {
            return Route::Hints;
        }
        let digit = text
            .strip_prefix("/r/o")
            .and_then(|rest| rest.chars().next())
            .and_then(|c| c.to_digit(10))
            .and_then(|d| usize::try_from(d).ok());
        match digit.and_then(|d| self.owners.get(d)) {
            Some(owner) => Route::Owner(owner.clone()),
            None => Route::Nothing,
        }
    }
    fn hints(&mut self) -> Vec<(Owner, Signal)> {
        self.fake.push(Ev::Hints);
        self.hint_batches.pop_front().unwrap_or_default()
    }
    fn capture(&mut self, owner: &Owner) -> Result<(), Failed> {
        self.captures += 1;
        self.fake.push(Ev::Capture(self.index(owner)));
        if self.stop_after_captures == Some(self.captures) {
            self.fake.stop.set(true);
        }
        if self.fail_capture_at == Some(self.captures) {
            return Err(Failed);
        }
        Ok(())
    }
    fn reconcile(&mut self, cause: Cause) -> Result<(), Failed> {
        self.reconciles += 1;
        self.fake.push(Ev::Reconcile(cause));
        self.fake
            .advance_to(self.fake.mono.get() + self.reconcile_cost * NS);
        if self.stop_after_reconciles == Some(self.reconciles) {
            self.fake.stop.set(true);
        }
        if self.fail_reconcile_at == Some(self.reconciles) {
            return Err(Failed);
        }
        Ok(())
    }
    fn next_retry(&self) -> Option<Duration> {
        self.retries
            .iter()
            .chain(&self.sticky_retries)
            .map(|(at, _)| s(BASE + at))
            .min()
    }
    fn retries_due(&mut self, now: Duration) -> Vec<Owner> {
        let (due, later): (Vec<(u64, usize)>, Vec<(u64, usize)>) = self
            .retries
            .iter()
            .copied()
            .partition(|(at, _)| s(BASE + at) <= now);
        let mut offered = due;
        offered.extend(
            self.sticky_retries
                .iter()
                .copied()
                .filter(|(at, _)| s(BASE + at) <= now),
        );
        let owners = offered
            .iter()
            .filter_map(|(_, i)| self.owners.get(*i).cloned())
            .collect();
        self.retries = later;
        owners
    }
    fn shutdown(&mut self) -> Result<(), Failed> {
        self.fake.push(Ev::Shutdown);
        if self.fail_shutdown {
            Err(Failed)
        } else {
            Ok(())
        }
    }
}

struct Cfg {
    startup: u64,
    batch: usize,
    until: u64,
    steps: Vec<(u64, Step)>,
}
fn cfg(until: u64, steps: Vec<(u64, Step)>) -> Cfg {
    Cfg {
        startup: 7,
        batch: 64,
        until,
        steps,
    }
}

fn go(cfg: Cfg, setup: impl FnOnce(&mut Rec<'_>) -> R) -> R<(Outcome, Vec<Entry>)> {
    let (outcome, log, _) = go_calls(cfg, setup)?;
    Ok((outcome, log))
}

/// [`go`] that also returns how many times the loop called `Inputs::next`.
fn go_calls(cfg: Cfg, setup: impl FnOnce(&mut Rec<'_>) -> R) -> R<(Outcome, Vec<Entry>, u32)> {
    let fake = Fake::new();
    let mut owners = Vec::new();
    for i in 0..6 {
        owners.push(owner(i)?);
    }
    let mut rec = Rec {
        fake: &fake,
        owners,
        fail_capture_at: None,
        fail_reconcile_at: None,
        fail_shutdown: false,
        stop_after_captures: None,
        stop_after_reconciles: None,
        hint_batches: VecDeque::new(),
        retries: Vec::new(),
        sticky_retries: Vec::new(),
        reconcile_cost: 0,
        captures: 0,
        reconciles: 0,
    };
    setup(&mut rec)?;
    let mut script = Script {
        fake: &fake,
        steps: cfg.steps.into(),
        until: cfg.until,
        calls: 0,
    };
    let timings = Timings {
        startup: s(cfg.startup),
        tick: s(60),
        interval: s(900),
        batch: cfg.batch,
    };
    let window = Window::try_from(WindowSeconds {
        quiet: 60,
        cap: 300,
    })
    .map_err(e)?;
    let outcome = drive(timings, window, &fake, &mut script, &mut rec);
    let calls = script.calls;
    drop(script);
    drop(rec);
    Ok((outcome, fake.log.into_inner(), calls))
}
fn go_plain(until: u64, steps: Vec<(u64, Step)>) -> R<(Outcome, Vec<Entry>)> {
    go(cfg(until, steps), |_| Ok(()))
}

fn changed(i: usize) -> Step {
    Step::In(Input::PathChanged {
        path: PathBuf::from(format!("/r/o{i}/f.jsonl")),
    })
}
fn unmapped() -> Step {
    Step::In(Input::PathChanged {
        path: PathBuf::from("/x/f"),
    })
}
fn hint_dir() -> Step {
    Step::In(Input::PathChanged {
        path: PathBuf::from("/h/2026-11-17.jsonl"),
    })
}

// ------------------------------------------------------------------ captures

#[test]
fn a_change_is_captured_once_at_quiet_after_the_last_change() -> R {
    let (outcome, log) = go_plain(500, vec![(100, changed(1)), (130, changed(1))])?;
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(log, vec![start(7), cap(190, 1), shut(500)]);
    Ok(())
}

#[test]
fn an_owner_is_captured_again_after_its_capture() -> R {
    let (_, log) = go_plain(500, vec![(100, changed(1)), (200, changed(1))])?;
    assert_eq!(log, vec![start(7), cap(160, 1), cap(260, 1), shut(500)]);
    Ok(())
}

#[test]
fn continuous_changes_are_captured_at_cap() -> R {
    let steps = (0..10).map(|k| (100 + 30 * k, changed(1))).collect();
    let (_, log) = go_plain(600, steps)?;
    // The last change is at 370: quiet would give 430, the cap gives 100 + 300.
    assert_eq!(log, vec![start(7), cap(400, 1), shut(600)]);
    Ok(())
}

fn hint(i: usize, signal: Signal) -> R<Step> {
    Ok(Step::In(Input::Hint {
        owner: owner(i)?,
        signal,
    }))
}
fn with_hints(batches: Vec<Vec<(usize, Signal)>>) -> impl FnOnce(&mut Rec<'_>) -> R {
    move |rec| {
        for batch in batches {
            let mut one = Vec::new();
            for (i, signal) in batch {
                one.push((owner(i)?, signal));
            }
            rec.hint_batches.push_back(one);
        }
        Ok(())
    }
}
fn rescan() -> Step {
    Step::In(Input::Rescan)
}
fn error() -> Step {
    Step::In(Input::WatcherError)
}
fn dir() -> Step {
    Step::In(Input::DirCreated {
        path: PathBuf::from("/r/o1/new"),
    })
}

#[test]
fn a_session_end_hint_is_captured_in_the_same_turn() -> R {
    let steps = vec![(100, hint_dir())];
    let (_, log) = go(
        cfg(300, steps),
        with_hints(vec![vec![(1, Signal::SessionEnd)]]),
    )?;
    assert_eq!(log, vec![start(7), hints(100), cap(100, 1), shut(300)]);
    Ok(())
}

#[test]
fn a_stop_hint_waits_for_quiet() -> R {
    let steps = vec![(100, hint_dir())];
    let (_, log) = go(cfg(300, steps), with_hints(vec![vec![(1, Signal::Stop)]]))?;
    assert_eq!(log, vec![start(7), hints(100), cap(160, 1), shut(300)]);
    Ok(())
}

#[test]
fn a_change_while_the_capture_waits_for_the_start_reconcile_gives_one_follow_up() -> R {
    // The session end takes the owner in flight at 2; the start reconcile is at 7, so the
    // two changes at 3 and 4 arrive during the in-flight window and make one follow-up.
    let steps = vec![
        (2, hint(1, Signal::SessionEnd)?),
        (3, changed(1)),
        (4, changed(1)),
    ];
    let (_, log) = go_plain(300, steps)?;
    assert_eq!(log, vec![start(7), cap(7, 1), cap(64, 1), shut(300)]);
    Ok(())
}

#[test]
fn a_flushed_follow_up_runs_in_the_same_turn_as_its_capture() -> R {
    let steps = vec![
        (2, hint(1, Signal::SessionEnd)?),
        (3, hint(1, Signal::SessionEnd)?),
    ];
    let (_, log) = go_plain(300, steps)?;
    assert_eq!(log, vec![start(7), cap(7, 1), cap(7, 1), shut(300)]);
    Ok(())
}

#[test]
fn owners_due_together_run_in_edge_order_and_an_unmapped_path_does_nothing() -> R {
    let steps = vec![
        (100, changed(2)),
        (101, changed(1)),
        (102, unmapped()),
        (500, rescan()),
    ];
    let (_, log) = go_plain(700, steps)?;
    assert_eq!(
        log,
        vec![
            start(7),
            cap(160, 2),
            cap(161, 1),
            recon(500, Cause::Rescan),
            shut(700)
        ]
    );
    Ok(())
}

// ------------------------------------------------------------------ the tick

#[test]
fn a_wake_gap_of_121_seconds_reconciles_once_with_cause_wake() -> R {
    // The skew changes between the ticks at 60 and 120; the gap is the change, not the level.
    let steps = vec![(30, Step::Skew(500)), (90, Step::Skew(621))];
    let (_, log) = go_plain(300, steps)?;
    assert_eq!(log, vec![start(7), recon(120, Cause::Wake), shut(300)]);
    Ok(())
}

#[test]
fn a_wake_gap_of_120_seconds_does_not_reconcile() -> R {
    let steps = vec![(90, Step::Skew(120))];
    let (_, log) = go_plain(300, steps)?;
    assert_eq!(log, vec![start(7), shut(300)]);
    Ok(())
}

#[test]
fn an_input_does_not_move_the_tick() -> R {
    let steps = vec![
        (59, unmapped()),
        (90, Step::Skew(121)),
        (110, unmapped()),
        (119, unmapped()),
    ];
    let (_, log) = go_plain(200, steps)?;
    assert_eq!(log, vec![start(7), recon(120, Cause::Wake), shut(200)]);
    Ok(())
}

// ------------------------------------------------------------------ reconcile causes

#[test]
fn rescan_error_and_new_directory_each_reconcile_once() -> R {
    for (step, cause) in [
        (rescan(), Cause::Rescan),
        (error(), Cause::Rescan),
        (dir(), Cause::Dir),
    ] {
        let (_, log) = go_plain(300, vec![(100, step)])?;
        assert_eq!(log, vec![start(7), recon(100, cause), shut(300)]);
    }
    Ok(())
}

#[test]
fn many_causes_in_one_batch_give_one_reconcile_with_the_first_cause() -> R {
    let steps = vec![
        (100, rescan()),
        (100, error()),
        (100, dir()),
        (100, rescan()),
    ];
    let (_, log) = go_plain(300, steps)?;
    assert_eq!(log, vec![start(7), recon(100, Cause::Rescan), shut(300)]);
    let steps = vec![(100, dir()), (100, rescan()), (100, error())];
    let (_, log) = go_plain(300, steps)?;
    assert_eq!(log, vec![start(7), recon(100, Cause::Dir), shut(300)]);
    // Separate batches are separate reconciles.
    let (_, log) = go_plain(300, vec![(100, rescan()), (200, rescan())])?;
    assert_eq!(
        log,
        vec![
            start(7),
            recon(100, Cause::Rescan),
            recon(200, Cause::Rescan),
            shut(300)
        ]
    );
    Ok(())
}

#[test]
fn causes_beyond_the_batch_bound_belong_to_the_next_batch() -> R {
    let mut c = cfg(300, vec![(100, rescan()), (100, rescan()), (100, rescan())]);
    c.batch = 2;
    let (_, log) = go(c, |_| Ok(()))?;
    // Two inputs fill the bound; the third is the next batch and its own reconcile.
    assert_eq!(
        log,
        vec![
            start(7),
            recon(100, Cause::Rescan),
            recon(100, Cause::Rescan),
            shut(300)
        ]
    );
    Ok(())
}

// ------------------------------------------------------------------ deadlines

#[test]
fn the_timer_reconcile_restarts_from_the_end_of_the_last_reconcile_and_inputs_do_not_move_it() -> R
{
    let steps = vec![(905, unmapped()), (911, unmapped()), (1800, unmapped())];
    let (_, log) = go(cfg(1900, steps), |rec| {
        rec.reconcile_cost = 5;
        Ok(())
    })?;
    // Start at 7 ends at 12, so the timer is at 912; that one ends at 917, so the next is 1817.
    assert_eq!(
        log,
        vec![
            start(7),
            recon(912, Cause::Timer),
            recon(1817, Cause::Timer),
            shut(1900)
        ]
    );
    Ok(())
}

#[test]
fn any_reconcile_restarts_the_timer() -> R {
    let (_, log) = go(cfg(1100, vec![(100, rescan())]), |rec| {
        rec.reconcile_cost = 5;
        Ok(())
    })?;
    assert_eq!(
        log,
        vec![
            start(7),
            recon(100, Cause::Rescan),
            recon(1005, Cause::Timer),
            shut(1100)
        ]
    );
    Ok(())
}

#[test]
fn a_retry_runs_at_its_deadline_and_not_before() -> R {
    let steps = vec![(150, unmapped()), (199, unmapped())];
    let (_, log) = go(cfg(300, steps), |rec| {
        rec.retries.push((200, 3));
        Ok(())
    })?;
    assert_eq!(log, vec![start(7), cap(200, 3), shut(300)]);
    Ok(())
}

#[test]
fn no_capture_runs_before_the_start_reconcile() -> R {
    let steps = vec![
        (1, changed(2)),
        (2, hint(1, Signal::SessionEnd)?),
        (3, unmapped()),
    ];
    let (_, log) = go(cfg(300, steps), |rec| {
        rec.retries.push((4, 3));
        Ok(())
    })?;
    assert_eq!(
        log,
        vec![start(7), cap(7, 1), cap(7, 3), cap(61, 2), shut(300)]
    );
    Ok(())
}

// ------------------------------------------------------------------ stop

#[test]
fn a_stop_during_the_startup_delay_does_no_work() -> R {
    let steps = vec![(1, changed(1)), (3, Step::Stop)];
    let (outcome, log) = go_plain(300, steps)?;
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(outcome.exit_code(), 0);
    assert_eq!(log, vec![shut(3)]);
    Ok(())
}

#[test]
fn a_stop_between_two_due_captures_skips_the_second() -> R {
    let steps = vec![
        (100, hint(1, Signal::SessionEnd)?),
        (100, hint(2, Signal::SessionEnd)?),
    ];
    let (outcome, log) = go(cfg(300, steps), |rec| {
        rec.stop_after_captures = Some(1);
        Ok(())
    })?;
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(log, vec![start(7), cap(100, 1), shut(100)]);
    Ok(())
}

#[test]
fn a_stop_pending_while_inputs_are_queued_runs_no_further_work() -> R {
    let steps = vec![
        (100, hint(1, Signal::SessionEnd)?),
        (100, Step::StopFlag),
        (100, rescan()),
    ];
    let (outcome, log) = go_plain(300, steps)?;
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(log, vec![start(7), shut(100)]);
    Ok(())
}

#[test]
fn a_stop_is_seen_between_the_start_reconcile_and_the_captures() -> R {
    let steps = vec![(2, hint(1, Signal::SessionEnd)?)];
    let (outcome, log) = go(cfg(300, steps), |rec| {
        rec.stop_after_reconciles = Some(1);
        Ok(())
    })?;
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(log, vec![start(7), shut(7)]);
    Ok(())
}

#[test]
fn a_stop_between_two_due_retries_skips_the_second() -> R {
    let (outcome, log) = go(cfg(300, vec![]), |rec| {
        rec.retries.push((100, 1));
        rec.retries.push((100, 2));
        rec.stop_after_captures = Some(1);
        Ok(())
    })?;
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(log, vec![start(7), cap(100, 1), shut(100)]);
    Ok(())
}

// ------------------------------------------------------------------ outcomes

#[test]
fn start_timer_and_stop_run_two_reconciles_then_shut_down_with_exit_zero() -> R {
    let (outcome, log) = go_plain(1000, vec![])?;
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(outcome.exit_code(), 0);
    assert_eq!(log, vec![start(7), recon(907, Cause::Timer), shut(1000)]);
    Ok(())
}

#[test]
fn a_failed_source_ends_with_wait_failed_after_shutdown() -> R {
    let (outcome, log) = go_plain(300, vec![(100, Step::Fail)])?;
    assert_eq!(outcome, Outcome::WaitFailed);
    assert_eq!(outcome.exit_code(), 1);
    assert_eq!(log, vec![start(7), shut(100)]);
    Ok(())
}

#[test]
fn a_failed_capture_ends_with_round_failed_after_shutdown() -> R {
    let steps = vec![
        (100, hint(1, Signal::SessionEnd)?),
        (100, hint(2, Signal::SessionEnd)?),
    ];
    let (outcome, log) = go(cfg(300, steps), |rec| {
        rec.fail_capture_at = Some(1);
        Ok(())
    })?;
    assert_eq!(outcome, Outcome::RoundFailed);
    assert_eq!(outcome.exit_code(), 1);
    assert_eq!(log, vec![start(7), cap(100, 1), shut(100)]);
    Ok(())
}

#[test]
fn a_failed_reconcile_ends_with_round_failed_after_shutdown() -> R {
    for (at, want) in [
        (1, vec![start(7), shut(7)]),
        (2, vec![start(7), recon(907, Cause::Timer), shut(907)]),
    ] {
        let (outcome, log) = go(cfg(2000, vec![]), |rec| {
            rec.fail_reconcile_at = Some(at);
            Ok(())
        })?;
        assert_eq!(outcome, Outcome::RoundFailed);
        assert_eq!(outcome.exit_code(), 1);
        assert_eq!(log, want);
    }
    Ok(())
}

#[test]
fn a_failed_shutdown_after_a_stop_is_shutdown_failed_and_after_a_failure_is_ignored() -> R {
    let (outcome, log) = go(cfg(300, vec![]), |rec| {
        rec.fail_shutdown = true;
        Ok(())
    })?;
    assert_eq!(outcome, Outcome::ShutdownFailed);
    assert_eq!(outcome.exit_code(), 1);
    assert_eq!(log, vec![start(7), shut(300)]);
    let (outcome, _) = go(cfg(300, vec![(100, Step::Fail)]), |rec| {
        rec.fail_shutdown = true;
        Ok(())
    })?;
    assert_eq!(outcome, Outcome::WaitFailed);
    Ok(())
}

#[test]
fn only_a_stop_gives_exit_zero() {
    for outcome in [
        Outcome::RoundFailed,
        Outcome::ShutdownFailed,
        Outcome::WaitFailed,
    ] {
        assert_eq!(outcome.exit_code(), 1);
    }
    assert_eq!(Outcome::Stopped.exit_code(), 0);
}

// ------------------------------------------------------------------ hints

#[test]
fn the_hint_query_is_made_once_per_batch_that_touched_the_hint_directory() -> R {
    let steps = vec![
        (50, changed(4)),
        (100, hint_dir()),
        (100, hint_dir()),
        (100, hint_dir()),
        (200, hint_dir()),
        (250, unmapped()),
    ];
    let (_, log) = go(
        cfg(300, steps),
        with_hints(vec![vec![], vec![(5, Signal::Stop)]]),
    )?;
    assert_eq!(
        log,
        vec![
            start(7),
            hints(100),
            cap(110, 4),
            hints(200),
            cap(260, 5),
            shut(300)
        ]
    );
    Ok(())
}

#[test]
fn a_retry_that_stays_due_is_offered_once_per_turn_and_does_not_spin() -> R {
    let (outcome, log, calls) = go_calls(cfg(300, vec![]), |rec| {
        rec.sticky_retries.push((100, 1));
        Ok(())
    })?;
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(
        log,
        vec![
            start(7),
            cap(100, 1),
            cap(120, 1),
            cap(180, 1),
            cap(240, 1),
            shut(300)
        ]
    );
    // One wait per wake (7, 100, 120, 180, 240, 300), none for the stale deadline.
    assert!(calls <= 8, "calls {calls}");
    Ok(())
}

#[test]
fn a_retry_that_becomes_due_during_a_reconcile_runs_in_the_next_turn() -> R {
    let (_, log) = go(cfg(300, vec![(100, rescan())]), |rec| {
        rec.reconcile_cost = 5;
        rec.retries.push((102, 3));
        Ok(())
    })?;
    assert_eq!(
        log,
        vec![start(7), recon(100, Cause::Rescan), cap(105, 3), shut(300)]
    );
    Ok(())
}
