//! Per-owner monotonic batching. Caller supplies time; this module never reads a clock.
use crate::domain::Owner;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use std::{collections::HashMap, time::Duration};

/// Invalid window or backwards monotonic clock, without input echo.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum CoalescerError {
    /// Windows must be nonzero and quiet must not exceed cap.
    #[error("invalid capture window")]
    Window,
    /// Caller violated the per-owner monotonic time contract.
    #[error("monotonic time moved backwards")]
    Backwards,
    /// Completion has no matching in-flight capture.
    #[error("no capture in flight")]
    NotInFlight,
}
/// Validated window configuration, expressed in seconds on the wire.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(try_from = "WindowSeconds", into = "WindowSeconds")]
pub struct Window {
    quiet: Duration,
    cap: Duration,
}
/// Strict configuration representation; conversion validates semantic bounds.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct WindowSeconds {
    /// Quiet interval in seconds, greater than zero.
    pub quiet: u64,
    /// Maximum pending age in seconds, at least quiet.
    pub cap: u64,
}
impl TryFrom<WindowSeconds> for Window {
    type Error = CoalescerError;
    fn try_from(value: WindowSeconds) -> Result<Self, Self::Error> {
        if value.quiet == 0 || value.cap < value.quiet {
            return Err(CoalescerError::Window);
        }
        Ok(Self {
            quiet: Duration::from_secs(value.quiet),
            cap: Duration::from_secs(value.cap),
        })
    }
}
impl From<Window> for WindowSeconds {
    fn from(value: Window) -> Self {
        Self {
            quiet: value.quiet.as_secs(),
            cap: value.cap.as_secs(),
        }
    }
}
impl Default for Window {
    fn default() -> Self {
        Self {
            quiet: Duration::from_secs(60),
            cap: Duration::from_secs(300),
        }
    }
}
/// Signal semantics are independent of agent spellings in hook input.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Signal {
    /// Ordinary filesystem/hook edge.
    Changed,
    /// Turn completion, still debounced.
    Stop,
    /// True session termination, immediately eligible.
    SessionEnd,
}
#[derive(Debug, Clone)]
struct Pending {
    first: Duration,
    last: Duration,
    flush: bool,
}
#[derive(Debug, Clone)]
struct Slot {
    last_time: Duration,
    pending: Option<Pending>,
    in_flight: bool,
}
/// A caller-owned scheduler, one in-flight capture per owner with later edges retained.
#[derive(Debug)]
pub struct Coalescer {
    window: Window,
    slots: HashMap<Owner, Slot>,
}
impl Coalescer {
    /// Create an empty scheduler.
    pub fn new(window: Window) -> Self {
        Self {
            window,
            slots: HashMap::new(),
        }
    }
    /// Record an edge. Call `poll` at the same time for immediate SessionEnd emission.
    ///
    /// # Errors
    /// Returns `Backwards` without changing state if this owner's clock regresses.
    pub fn signal(
        &mut self,
        owner: Owner,
        now: Duration,
        signal: Signal,
    ) -> Result<(), CoalescerError> {
        let slot = self.slots.entry(owner).or_insert(Slot {
            last_time: now,
            pending: None,
            in_flight: false,
        });
        advance(slot, now)?;
        match &mut slot.pending {
            Some(pending) => {
                pending.last = now;
                pending.flush |= signal == Signal::SessionEnd;
            }
            None => {
                slot.pending = Some(Pending {
                    first: now,
                    last: now,
                    flush: signal == Signal::SessionEnd,
                })
            }
        }
        Ok(())
    }
    /// Atomically take one eligible capture. No pending edge is consumed while in flight.
    ///
    /// # Errors
    /// Returns `Backwards` when this owner's clock regresses.
    pub fn poll(&mut self, owner: &Owner, now: Duration) -> Result<bool, CoalescerError> {
        let Some(slot) = self.slots.get_mut(owner) else {
            return Ok(false);
        };
        advance(slot, now)?;
        if slot.in_flight {
            return Ok(false);
        }
        let due = slot.pending.as_ref().is_some_and(|p| {
            p.flush
                || now.saturating_sub(p.last) >= self.window.quiet
                || now.saturating_sub(p.first) >= self.window.cap
        });
        if due {
            slot.pending = None;
            slot.in_flight = true;
        }
        Ok(due)
    }
    /// Finish only the in-flight capture, preserving signals received after its emission.
    ///
    /// # Errors
    /// Returns `Backwards` for clock regression, `NotInFlight` for an unmatched completion.
    pub fn complete(&mut self, owner: &Owner, now: Duration) -> Result<(), CoalescerError> {
        let slot = self
            .slots
            .get_mut(owner)
            .ok_or(CoalescerError::NotInFlight)?;
        if !slot.in_flight {
            return Err(CoalescerError::NotInFlight);
        }
        advance(slot, now)?;
        slot.in_flight = false;
        // An idle slot holds nothing; dropping it bounds memory by live owners.
        if slot.pending.is_none() {
            self.slots.remove(owner);
        }
        Ok(())
    }
    /// Earliest monotonic deadline at which any idle owner's pending edge becomes due,
    /// or `None` when nothing is pending. Owners with a capture in flight are excluded:
    /// they cannot be taken until `complete`. Pure; a run loop can sleep until then.
    pub fn next_due(&self) -> Option<Duration> {
        self.slots
            .values()
            .filter(|slot| !slot.in_flight)
            .filter_map(|slot| slot.pending.as_ref())
            .map(|p| {
                if p.flush {
                    p.first
                } else {
                    (p.last + self.window.quiet).min(p.first + self.window.cap)
                }
            })
            .min()
    }
}
fn advance(slot: &mut Slot, now: Duration) -> Result<(), CoalescerError> {
    if now < slot.last_time {
        return Err(CoalescerError::Backwards);
    }
    slot.last_time = now;
    Ok(())
}
/// Pure wake detector output; shell owns the 60-second tick and RNG.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WakeAction {
    /// No sleep-sized wall/monotonic gap.
    None,
    /// Set safety reconcile and stagger queued captures over this interval.
    ReconcileAndStagger {
        /// Maximum caller-selected delay.
        jitter_max: Duration,
    },
}
/// Wall minus monotonic gap that counts as a sleep/wake (DESIGN 10.3 safety reconcile).
pub const WAKE_GAP: Duration = Duration::from_secs(120);
/// Spread of queued captures after a wake (DESIGN 10.3 wake gap).
pub const WAKE_STAGGER: Duration = Duration::from_secs(30);

/// Detect wall elapsed minus monotonic elapsed strictly greater than [`WAKE_GAP`].
/// Wall jumps may conservatively trigger a reconcile; wall time never orders captures.
pub fn wake_action(wall_elapsed: Duration, monotonic_elapsed: Duration) -> WakeAction {
    if wall_elapsed.saturating_sub(monotonic_elapsed) > WAKE_GAP {
        WakeAction::ReconcileAndStagger {
            jitter_max: WAKE_STAGGER,
        }
    } else {
        WakeAction::None
    }
}
