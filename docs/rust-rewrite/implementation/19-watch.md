# 19 Watcher (slice 2, partial)

## Done
- `rust/crates/memory/src/watch.rs`
  - `Scheduler<F: FnMut(&Path) -> Option<Owner>>`: pure, drives memory-core `Coalescer` with
    caller-supplied monotonic time (DESIGN 7.1). Inputs (`Input`): PathChanged, DirCreated,
    Rescan, WatcherError, Hint{owner, signal}, Tick{wall, mono}. Actions (`Action`):
    Capture{owner}, ReadDir{path}, ReconcileAll.
  - Unmapped paths are ignored and counted (`unmapped()`); DirCreated -> ReadDir (10.2);
    Rescan/WatcherError -> ReconcileAll; Tick wall-minus-mono gap > 120 s (`wake_action`)
    -> ReconcileAll; one capture in flight per owner (Coalescer), a follow-up is emitted by
    `complete()`; SessionEnd flushes immediately; Stop stays debounced.
    Clock regressions are counted in `clock_errors()`, never fatal.
  - `translate(notify::Result<Event>) -> Vec<Input>`: Err -> WatcherError, `need_rescan()` ->
    Rescan, Create(Folder) -> DirCreated, Any/Create/Modify/Remove -> PathChanged, Access/Other dropped.
  - `FsWatcher`: `notify::recommended_watcher`, each root `RecursiveMode::Recursive`, events
    forwarded over `std::sync::mpsc`; `recv_timeout`. No unsafe.
- `rust/crates/memory/tests/watch.rs`: 16 tests: scheduler tables (quiet, cap, SessionEnd,
  Stop, in-flight follow-up, unmapped, DirCreated, Rescan/error, wake gap 120 vs 121,
  clock regression), translate shapes, Hegel + proptest model properties, 3 FsWatcher
  temp-dir tests (10 s bound, "at least" semantics).

## Evidence
- clippy `-Dwarnings` exit 0; `cargo test -p memory --test watch` at 2,000 cases exit 0, 16 passed.
- `cargo llvm-cov` (with --ignore-run-fail; run exit 0, 1260 s): watch.rs lines 107/108 (99.07%),
  regions 160/161. One uncovered region at watch.rs line 209 col 11: the `?` after `recommended_watcher(...)`, i.e. watcher creation failing (inotify/FSEvents resource exhaustion), which a test cannot trigger without changing system limits. The missing-root `watch()` error is covered.
- sha256 src/watch.rs e433a1841f5184945029f425457a54ad144994a1ab5ddfd764fd2cd46ba60277;
  tests/watch.rs 878e84000165c12f794ba62e036b34fd8b5b84a2be17b5e162fe7cceb5bd3975.

## Remaining
- Not wired into the daemon loop (production path mapper will use `locate`; the 60 s tick,
  ReadDir and ReconcileAll execution belong to the daemon).
- Line 209 (watcher creation failure) stays uncovered; file coverage is 99.07%, above the 98% floor.
- FSEvents latency is host dependent; no replay across restarts (10.2).
