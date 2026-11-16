//! The event loop as a pure driver (docs/rust-rewrite/implementation/29-event-loop.md, LOOP-2a).
//!
//! One thread, two traits: [`Inputs`] yields what happened (the real one will be the watcher
//! inbox) and [`Work`] does things (the real one will be the daemon). The driver never sleeps,
//! reads no clock but the one it is given, and touches no filesystem or network.
//!
//! Wake sources, all absolute monotonic instants, none moved by an input:
//!
//! | Source | Instant | Work when it passes |
//! |---|---|---|
//! | startup delay | `t0 + startup` | `reconcile(Start)`; no capture runs before it |
//! | scheduler | [`Scheduler::next_due`] | captures that are due |
//! | retry | [`Work::next_retry`] | [`Work::retries_due`] owners are captured |
//! | tick | `t0 + k * tick` | [`Input::Tick`] fed to the scheduler (`Wake` on a gap) |
//! | reconcile | end of the last reconcile + `interval` | `reconcile(Timer)` |
//!
//! One turn: stop; tick; captures; retries; the reconcile (flag or timer, one reconcile
//! however many causes, the first set is reported); then wait for the earliest wake and drain
//! up to a batch of inputs. A stop is looked at between every two units of work.
use crate::environment::Clock;
use crate::run::{Failed, Outcome};
use crate::watch::{Action, Input, Scheduler};
use memory_core::coalescer::{Signal, Window};
use memory_core::domain::Owner;
use std::collections::VecDeque;
use std::path::Path;
use std::time::Duration;

/// Why a full reconcile runs.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Cause {
    /// The first one, after the startup delay.
    Start,
    /// The reconcile interval passed.
    Timer,
    /// The platform lost events or the watcher failed.
    Rescan,
    /// A wake gap was detected.
    Wake,
    /// A directory appeared.
    Dir,
}

/// The durations of the loop.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Timings {
    /// Wait before the first reconcile; no capture runs earlier.
    pub startup: Duration,
    /// Period of the wake tick (60 s).
    pub tick: Duration,
    /// Pause between full reconciles (15 min).
    pub interval: Duration,
    /// Most inputs taken per turn.
    pub batch: usize,
}

/// What the input source returned.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Next {
    /// One input.
    Input(Input),
    /// `up_to` passed with nothing to report.
    Timeout,
    /// A stop was requested.
    Stop,
    /// The source died; no stop can be seen any more.
    Failed,
}

/// Where inputs come from.
pub trait Inputs {
    /// Wait at most `up_to` for the next input; `Duration::ZERO` polls.
    fn next(&mut self, up_to: Duration) -> Next;
    /// Without blocking: has a stop been requested?
    fn stop_requested(&mut self) -> bool;
}

/// What a changed path is.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Route {
    /// A source of this owner.
    Owner(Owner),
    /// Under the hint directory.
    Hints,
    /// Nothing the loop cares about.
    Nothing,
}

/// What the loop does.
pub trait Work {
    /// Classify a changed path.
    fn classify(&mut self, path: &Path) -> Route;
    /// New hint signals; asked once per batch in which the hint directory changed.
    fn hints(&mut self) -> Vec<(Owner, Signal)>;
    /// Capture one owner.
    ///
    /// # Errors
    /// [`Failed`] ends the loop.
    fn capture(&mut self, owner: &Owner) -> Result<(), Failed>;
    /// Run a full reconcile.
    ///
    /// # Errors
    /// [`Failed`] ends the loop.
    fn reconcile(&mut self, cause: Cause) -> Result<(), Failed>;
    /// Earliest retry instant (absolute monotonic), if any.
    fn next_retry(&self) -> Option<Duration>;
    /// Owners whose retry is due at `now`.
    fn retries_due(&mut self, now: Duration) -> Vec<Owner>;
    /// Flush and release; called once when the loop ends.
    ///
    /// # Errors
    /// [`Failed`] turns a stop into [`Outcome::ShutdownFailed`].
    fn shutdown(&mut self) -> Result<(), Failed>;
}

/// Run the loop until a stop or a failure; shutdown runs in every case.
pub fn drive<I: Inputs, W: Work>(
    timings: Timings,
    window: Window,
    clock: &impl Clock,
    inputs: &mut I,
    work: &mut W,
) -> Outcome {
    let t0 = Duration::from_nanos(clock.monotonic_ns());
    let mut state = Driver {
        clock,
        inputs,
        work,
        timings,
        sched: Scheduler::new(window),
        ready: VecDeque::new(),
        flag: None,
        hints_dirty: false,
        tick_at: t0 + timings.tick,
        reconcile_at: None,
        startup_at: t0 + timings.startup,
    };
    let (ended, outcome) = match state.run() {
        Ended::Stop => (true, Outcome::Stopped),
        Ended::Round => (false, Outcome::RoundFailed),
        Ended::Wait => (false, Outcome::WaitFailed),
    };
    match (state.work.shutdown(), ended) {
        (Err(Failed), true) => Outcome::ShutdownFailed,
        _ => outcome,
    }
}

enum Ended {
    Stop,
    Round,
    Wait,
}

struct Driver<'a, I, W, C> {
    clock: &'a C,
    inputs: &'a mut I,
    work: &'a mut W,
    timings: Timings,
    sched: Scheduler,
    /// Owners the scheduler has taken (in flight) and the loop has not captured yet.
    ready: VecDeque<Owner>,
    flag: Option<Cause>,
    hints_dirty: bool,
    tick_at: Duration,
    /// `None` until the start reconcile has completed.
    reconcile_at: Option<Duration>,
    startup_at: Duration,
}

impl<I: Inputs, W: Work, C: Clock> Driver<'_, I, W, C> {
    fn now(&self) -> Duration {
        Duration::from_nanos(self.clock.monotonic_ns())
    }

    fn check_stop(&mut self) -> Result<(), Ended> {
        if self.inputs.stop_requested() {
            Err(Ended::Stop)
        } else {
            Ok(())
        }
    }

    fn run(&mut self) -> Ended {
        match self.turns() {
            Err(ended) => ended,
            Ok(never) => match never {},
        }
    }

    fn turns(&mut self) -> Result<std::convert::Infallible, Ended> {
        loop {
            self.check_stop()?;
            let now = self.now();
            if now >= self.tick_at {
                self.tick(now);
            }
            let actions = self.sched.poll(now);
            self.apply(actions, Cause::Wake);
            if self.reconcile_at.is_none() && now >= self.startup_at {
                self.reconcile(Cause::Start)?;
            }
            if self.reconcile_at.is_some() {
                self.captures()?;
                self.retries()?;
                self.timed_reconcile()?;
            }
            self.wait()?;
        }
    }

    fn tick(&mut self, now: Duration) {
        let wall = Duration::from_nanos(u64::try_from(self.clock.wall_ns()).unwrap_or(0));
        self.feed(Input::Tick { wall, mono: now });
        self.tick_at += self.timings.tick;
        if self.tick_at <= now {
            self.tick_at = now + self.timings.tick;
        }
    }

    fn feed(&mut self, input: Input) {
        let now = self.now();
        let cause = if matches!(input, Input::Tick { .. }) {
            Cause::Wake
        } else {
            Cause::Rescan
        };
        let actions = match input {
            Input::PathChanged { path } => match self.work.classify(&path) {
                Route::Owner(owner) => {
                    let input = Input::PathChanged { path };
                    self.sched.handle(input, now, &mut |_| Some(owner.clone()))
                }
                Route::Hints => {
                    self.hints_dirty = true;
                    Vec::new()
                }
                Route::Nothing => {
                    let input = Input::PathChanged { path };
                    self.sched.handle(input, now, &mut |_| None)
                }
            },
            other => self.sched.handle(other, now, &mut |_| None),
        };
        self.apply(actions, cause);
    }

    fn apply(&mut self, actions: Vec<Action>, cause: Cause) {
        for action in actions {
            match action {
                Action::Capture { owner } => self.ready.push_back(owner),
                Action::ReadDir { .. } => {
                    self.flag.get_or_insert(Cause::Dir);
                }
                Action::ReconcileAll => {
                    self.flag.get_or_insert(cause);
                }
            }
        }
    }

    fn captures(&mut self) -> Result<(), Ended> {
        while let Some(owner) = self.ready.front().cloned() {
            self.check_stop()?;
            self.ready.pop_front();
            self.work.capture(&owner).map_err(|Failed| Ended::Round)?;
            let actions = self
                .sched
                .complete(&owner, self.now())
                .map_err(|_| Ended::Round)?;
            self.apply(actions, Cause::Wake);
        }
        Ok(())
    }

    fn retries(&mut self) -> Result<(), Ended> {
        self.check_stop()?;
        for owner in self.work.retries_due(self.now()) {
            self.check_stop()?;
            self.work.capture(&owner).map_err(|Failed| Ended::Round)?;
        }
        Ok(())
    }

    fn timed_reconcile(&mut self) -> Result<(), Ended> {
        self.check_stop()?;
        let timer = self.reconcile_at.is_some_and(|at| self.now() >= at);
        match self.flag.or(timer.then_some(Cause::Timer)) {
            Some(cause) => self.reconcile(cause),
            None => Ok(()),
        }
    }

    fn reconcile(&mut self, cause: Cause) -> Result<(), Ended> {
        self.check_stop()?;
        self.work.reconcile(cause).map_err(|Failed| Ended::Round)?;
        self.flag = None;
        self.reconcile_at = Some(self.now() + self.timings.interval);
        Ok(())
    }

    fn wait(&mut self) -> Result<(), Ended> {
        let now = self.now();
        let mut wake = self.tick_at;
        // Until the start reconcile has run no capture may, so only it and the tick are wakes.
        let others = match self.reconcile_at {
            None => [None, None, Some(self.startup_at)],
            Some(at) => [self.sched.next_due(), self.work.next_retry(), Some(at)],
        };
        for at in others.into_iter().flatten() {
            wake = wake.min(at);
        }
        let mut up_to = wake.saturating_sub(now);
        let mut taken = 0;
        while taken < self.timings.batch.max(1) {
            match self.inputs.next(up_to) {
                Next::Input(input) => self.feed(input),
                Next::Timeout => break,
                Next::Stop => return Err(Ended::Stop),
                Next::Failed => return Err(Ended::Wait),
            }
            up_to = Duration::ZERO;
            taken += 1;
        }
        if std::mem::take(&mut self.hints_dirty) {
            for (owner, signal) in self.work.hints() {
                self.feed(Input::Hint { owner, signal });
            }
        }
        Ok(())
    }
}
