# W4 review: daemon.rs, state.rs, watch.rs, resolve.rs, config.rs, lib.rs

Read in full: daemon.rs, state.rs (1-700 and 700-803), config.rs, watch.rs (1-200, 200-330), lib.rs, resolve.rs (1-215, 240-319), environment.rs (traits). Not read: capture.rs, commit.rs, realfs.rs, hook.rs bodies, test files (grepped for counts only), DESIGN.md and the amendments (I judged against the code's own doc comments; design conformance is [inferred]).
Function lengths are from line numbers I read, approximate (+-3).

## 1. Verdict

| Crit | Verdict | Why |
|---|---|---|
| C1 boundaries | mostly sound | No cycles, `state.rs` and `watch.rs` depend only on memory-core. `daemon.rs` holds 5 responsibilities and borrows date helpers from `hook.rs`. |
| C2 extensibility | mostly sound | `Store` shape fits a sync `ureq` adapter. Linux is nearly free (no `cfg(target_os)` anywhere). Missing: a per-owner capture entry for the scheduler, and a real `Clock`. |
| C3 maintainability | mostly sound | Small functions, `ControlFlow` use, closed errors. `OwnerRt` and the `Daemon` god struct carry invariants in comments. |
| C4 names | mostly sound | Domain names are good. Some process-history names and tags (`known_ours`, `W4 Q3`, `D20`, `slice 2`), plus `rt`, `at`, `got`, `draw`. |
| C5 data structures | mixed | Enums for outcomes are good (`Loaded`, `StateLoad`, `Disposition`). `OwnerRt` is a struct of Option and bool. `FailureCounts` is 6 hand-written fields. `SourceIndex` mirrors `Local`. |
| C6 DRY | mostly sound | One envelope codec, one `vet`. Duplicates: error code strings (twice per variant), index/status write throttle, `SourceIndex` <-> `Local` copy. |
| C7 YAGNI | mostly sound | `watch.rs` (330 lines) has no production caller yet. `create_machine_id` and `resolve_facts` have no non-test caller. `sim` surface is larger than minimal and is threaded through prod functions. |

## 2. What is good

- One durable-write pipeline: `encode`/`decode`/`stage_with`/`Staged::publish`/`publish_new` (state.rs:~290-560). Every file goes through it. Load outcome is an enum, `Loaded::Present | Missing(MissingReason)` with 10 reasons (state.rs:~245-270) and `decode` never panics.
- `vet` (state.rs:~108) is the single implementation of the directory rule, shared by hook and daemon, with a doc comment saying who checks ownership and why.
- `Disposition` handling in `settle` (daemon.rs:641-~700) is an exhaustive 6-arm `match`. Adding a disposition forces a compile error here.
- `Scheduler` (watch.rs:~60-150) is pure: time passed in, `Action`s out. `translate_with(result, is_dir)` injects the one impure test (watch.rs:190). Good seam, small.
- `ControlFlow<bool>` from `recover` (daemon.rs:707) keeps `run_owner` flat (about 22 lines, early returns).
- `DaemonLock::acquire` (state.rs:~135): `StateDir::prepare_inner` takes `&DaemonLock`, so "sweep tmp only while holding the lock" is a type fact, not a comment.
- `ConfigError`/`StateError` are `Copy`, carry no values, and have fixed `code()`s. Secret-safe by construction.

## 3. Findings (ranked)

### F1 (medium, C3/C5) `OwnerRt` is a struct of Options and a bool; phases live in comments. daemon.rs:362-383, 641-700
```
struct OwnerRt { base: Option<Base>, needs_recover: bool, streak, parked: Option<ParkedReason>, known_ours, not_before: Option<Duration> }
```
- `base` + `needs_recover` encode three states: unknown (None,true), known-empty (None,false; `recover_base` can return `Ok(None)`), known (Some,false). `(Some,true)` is unreachable but representable. `settle` has to set both fields together (lines ~654-657), `recover` sets both (~716-718).
- `parked` and `not_before` are exclusive: a parked owner never runs, so `not_before` is dead while parked. `due()` (~668-680) tests both in sequence.
- Remedy: `enum Base { Unknown, Absent, Known(Base) }` replaces the `Option`+`bool`; `enum Phase { Active { not_before: Option<Duration> }, Parked(ParkedReason) }`. Removes the `needs_recover` branch in `recover` and `settle`, and the `(Some, true)` state. About 20 lines changed, 1-2 h. Also stops `parked` being stored twice (see F5).

### F2 (medium, C1/C3) `daemon.rs` (987 lines, about 880 non-test) is five modules in one file; `Daemon` has 19 fields
Responsibilities found:
1. Startup: `open`, `Opened`, `load_state`, `count_load`, `StartError` (daemon.rs:~35-60, 285-350).
2. Persisted bodies and their write throttle: `IndexBody`, `SourceIndex`, `StatusBody`, `FailureCounts`, `EventCounts`, `StateLoad`, `write_index`, `write_status` (~65-250, 860-940).
3. Hint scanning: `PendingHint`, `ScannedFile`, `scan_hints` (~55 lines), `advance_hints`, `attach_pending` (~385-400, 586-640, 832-850).
4. Per-owner turn: `OwnerRt`, `due`, `recover`, `capture_owner`, `land`, `settle`, `run_owner` (362-830).
5. Round orchestration and status assembly: `reconcile_once` (887-~950).
Also small stray helpers: `stamp_of`, `header_of`, `next_draw` (splitmix64 RNG, ~475).
Proposed split (no behaviour change): `status.rs` (StatusBody, FailureCounts, EventCounts, ParkedEntry, StateLoad), `index.rs` (IndexBody, SourceIndex), `hints.rs` (scan + advance + pending), `owner.rs` (OwnerRt/Phase, `settle`, `due`), `startup.rs` (open, load_state, StartError). `daemon.rs` keeps `Daemon`, `Parts`, `reconcile_once`. About 1 day of pure moves; `daemon.rs` falls to about 350 lines.
Evidence of the pressure: `run_owner` and `land` each take self + 6 params (daemon.rs:806, 771); `reconcile_once` removes the `OwnerRt` from the map and reinserts it (~910-916) to avoid a split borrow. `settle` is already an associated fn without `self`, i.e. it wants to be a method on the owner type.

### F3 (medium, C6) Error codes written twice per variant. state.rs:29-80, config.rs:25-70
Every variant has `#[error("lock_held")]` and a `code()` arm `"lock_held"`: 10 + 11 = 21 duplicated strings; payload variants differ (`"lock_unavailable: {0}"` vs `"lock_unavailable"`), so Display and `code()` can drift silently. Remedy: keep `code()` as the source of truth and write `Display` as `write!(f, "{}", self.code())` plus an optional kind suffix, or a 10-line macro. Drop `thiserror` for these two. About 40 lines removed, 1 h.

### F4 (medium, C5/C6) `SourceIndex` mirrors `capture::Local` field for field, copied in both directions. daemon.rs:~72-85, 505-515, 826-838
Four fields (`index`, `missing`, `snapshot`, `deletion_recorded`) are copied in `start_inner` and again in `index_body`. Adding a `Local` field needs three edits and the compiler only catches one direction. Remedy: derive `Serialize, Deserialize, deny_unknown_fields` on `Local` (it is in capture.rs, W5's file, so coordinate) and make `IndexBody.sources: BTreeMap<SourceId, Local>`. About 30 lines removed. If the on-disk shape must be decoupled from the in-memory type on purpose, say so in a comment; I found none.

### F5 (medium, C3/C5) Parked state is held in two places and re-derived with a Debug-string sort. daemon.rs:~935-948
`OwnerRt.parked` is the live truth; `StatusBody.parked` is rebuilt every round from the owner map, sorted with `parked.sort_by_key(|p| format!("{:?}", p.owner))`, and cloned a second time into `before` to detect change (~893, ~950). Debug output is not a contract, and it allocates per owner per round. Also `groups` is built by linear `find` (O(n^2) in owners, ~895-901). Remedy: `Owner: Ord` in memory-core (needed for any `BTreeMap<Owner,_>` [inferred: Owner has no Ord, which is why Debug is used]); use `BTreeMap<Owner, OwnerState>` for both `groups` and `owners`; set a `parked_changed` flag where `settle` parks instead of diffing. About 25 lines, 1-2 h.

### F6 (low, C5/C6) `FailureCounts` is six hand-written counters with two six-line methods. daemon.rs:~168-205
`bump` (6 arms) and `add` (6 saturating adds) must grow together with `ErrorClass`. Remedy: `[u64; 6]` indexed by a `class.index()`, or `BTreeMap<&'static str,u64>` keyed by class code, with `add` as a zip. Serialized JSON shape would change (schema 1 `deny_unknown_fields`, `#[serde(default)]`, so adding fields is already tolerated). Low value; do it only with F2.

### F7 (low, C6) Index and status write throttle duplicated. daemon.rs:~860-875, ~925-940
Both do `last.is_none_or(|l| now.saturating_sub(l) >= INDEX_INTERVAL)`, then `serde_json::to_vec(..).map_err(|_| StateError::Io(InvalidData))`, then `state.write`, then record `last`. Remedy: `struct Throttled { last: Option<Duration> }` with `fn due(&self, now)`, and `StateDir::write_json<T: Serialize>(file, &T)`. A serialization failure disguised as `Io(InvalidData)` is also a mis-typed error (daemon.rs:~868). `INDEX_INTERVAL` is used for status too: rename `DERIVED_WRITE_INTERVAL`. About 20 lines.

### F8 (low, C6/C1) Load-and-parse is split across modules; generic helper exists but in the wrong place. daemon.rs:~290-305 vs state.rs:~780
`load_state<T: DeserializeOwned>` (daemon.rs) is the generic helper the question asks about, so there is no per-file duplication. But it lives in the daemon, collapses 10 `MissingReason`s to 4 `StateLoad`s, and treats JSON parse failure as `Damaged`. Natural home is `StateDir::load<T>() -> (Option<T>, StateLoad)` next to `read`, with `StateLoad` moved to state.rs. `MachineId` has its own bespoke read path (state.rs:~789-800), the only other loader. Small move, 30 min.

### F9 (low, C1) `daemon.rs` imports `is_day_name`, `utc_day` from `hook.rs` (daemon.rs:26)
The daemon depends on the hook's module for date formatting. `hook.rs` also depends on `state`. No cycle, but a shared `time`/`day` helper (about 30 lines move) belongs below both. `stamp_of` (daemon.rs:400) hand-renders ISO-8601 with `% 24` arithmetic: with `utc_day` it is the only calendar code in the crate [inferred: memory-core may have a better one].

### F10 (low, C2/C7) `watch.rs` is not reachable from the daemon; Scheduler's output has no consumer API. watch.rs, daemon.rs:887
`rg` finds `Scheduler`/`FsWatcher`/`inbox` only in `watch.rs` and `tests/watch.rs` (15 hits). The header says "Neither is wired into the daemon loop yet", so this is planned (slice 3), not dead. But the cost of wiring: `Action::Capture{owner}` needs a per-owner entry; `Daemon` only offers `reconcile_once()` (all owners) and a private `run_owner`. Plan a `Daemon::capture_owner_now(&Owner)`. Also `Scheduler.owners: Vec<Owner>` with `contains` per signal and a full `drain` over every owner after every input (watch.rs:~100-125), never pruned: O(owners) per event. Remedy: use the Coalescer's own map if it can list keys. 1 h.

### F11 (low, C7) Pub items with no non-test caller (`rg` over `rust/crates/*/src`)
- `StateDir::create_machine_id` (state.rs:789): 1 hit in src (the definition); tests only. `StateFile::MachineId` and `StateFile::Roots` (state.rs:202-230): `Roots` has no reader or writer in src. DESIGN 9 lists them, so this is planned [inferred].
- `resolve::resolve_facts` (resolve.rs:156): definition only; tests use it. `to_source` is the production route.
- `Daemon::rebuilt`, `index_load`, `status_load`, `fs_mut`, `store_mut`, `clock_mut`, `root` (daemon.rs:~455-480): src non-test callers 0 outside the definitions [inferred from 7 combined hits for the first three, the rest are test seams].
- `StateDir::sync_policy`, `tmp_dir`, `uid`, `with_sync`, `prepare_with`: 21 combined hits, test-dominated.
- Config fields `roots`, `window`, `bucket`, `token_path`: no reader in daemon.rs/main.rs/hook.rs (only `archive_id`, `machine_id` at daemon.rs:579-580). All map to planned slices (watch, S3 adapter), so only note them. `check_token` runs at load, so `token_path` has one reader.
- `write_durable`, `stage` free functions (state.rs:~540-560) duplicate `StateDir::write`/`stage` with `Full` hardcoded. I did not count callers [not checked].

### F12 (low, C7/C3) `sim` surface leaks into production function bodies. state.rs:425-530, 647-660; daemon.rs:275, 467
Counted `#[cfg(feature = "sim")]` sites in state.rs: 14, of which 8 sit inside production functions (`fsync_dir`, `sync_file`, `rename_file`, `link_file`, `write_synced`) plus enum variants `SyncPolicy::{None, FailFileSync}`, the `trace` module (5 steps), `prepare_with`, `with_sync`, `start_with`, `StartOptions`. Elsewhere: `Published::sim` (environment.rs:160). It is documented (A7), and the release binary is clean, but the production durable-write code is interleaved with test recorders. Remedy: one `trait Durability { sync_file, sync_dir, rename, link }` with `Real` and (cfg sim) `Traced`; production functions then contain no cfg. About 60 lines moved, 2-3 h. Medium-low value: only do it if more durability steps arrive. `StartOptions` is a one-field struct whose only purpose is carrying `sync`; fine.

### F13 (low, C4) Names
Ten least clear, with replacements:
1. `OwnerRt` (daemon.rs:362) -> `OwnerState` (and the local `rt` -> `owner_state`).
2. `known_ours` (daemon.rs:368, type `KnownOurs` in commit.rs) -> `manifests_since_landing` / `unlanded_manifests`. "ours" records the A6 finding history; the reader needs "manifests this process built that have not yet been confirmed landed".
3. `at` (daemon.rs:641-830) -> `now`.
4. `draw` -> `jitter_seed`; `next_draw` -> `next_jitter_seed`.
5. `got` (daemon.rs:~815) -> `captured`.
6. `stamp_of` -> `manifest_timestamp`.
7. `due` (daemon.rs:668) -> `turn_or_skip` / `gate`; it mutates the report, so `due` hides a side effect.
8. `plain` (state.rs:~270) -> `is_plain_name`; `vet` (state.rs:108) -> `vet_dir`-ish `check_state_dir`.
9. `Loaded`/`StateLoad`/`MissingReason`: three similar names for two levels of one concept. `Loaded` -> `Read` or `Envelope`; keep `StateLoad`.
10. `chain`/`pack`/`spelling` (resolve.rs:~34-60) -> `symlink_chain`, `pack_identity`, `path_text`. And `fs` as a param name shadows `std::fs` in `to_source` (resolve.rs:164).
Process history in names/comments: "slice 2" (daemon.rs:1, resolve.rs:1, watch.rs:1), "W4 Q3: 1000 of 1000 children unreported" (watch.rs:~186, a review-question id), "D20" (resolve.rs, WalkSources doc), "A6" (daemon.rs:368 comment), `EXIT_NO_ADAPTERS`. A reader without the review docs cannot resolve "W4 Q3" or "D20"; replace with the fact (the measurement, the rule) or a link to the doc.
Also `config::Window` and `memory_core::coalescer::Window` share a name; `watch.rs` imports the latter. `RawWindow` (seconds) vs `Window` (Durations) is fine but `Config.window` is a third.
Exemplary: `DaemonLock` (type says what it is, drop releases), `MissingReason` (variants like `ChecksumMismatch`, `ExtraBytes` say exactly why), `translate_with` (says it is `translate` with an injected seam), `SyncPolicy`.

### F14 (low, C3) Hint scanning bypasses the `FileSystem` seam. daemon.rs:586-640
`scan_hints` calls `std::fs::read_dir`/`File::open` directly (up to 64 MiB per file, `MAX_HINT_FILE`), re-reads and re-scans every day file on every round (~600-612), and nests closures/match to depth 5 in the `records` map (~618-635). Simulation tests therefore need a real temp dir for hints and the sim `FileSystem` cannot inject hint faults [inferred]. Remedy: move to `hints.rs` (F2) behind a small `HintSource` or through `FileSystem`; offset check already avoids reparse only if `scan` is incremental (it takes `start`, so only the read is repeated). Low priority.

## 4. Question-by-question answers

Q1 Dependency graph (`use crate::`): daemon -> capture, commit, config, environment, hook, state. capture -> commit, environment. commit -> environment. hook -> state. realfs -> environment. resolve -> capture, environment, realfs, daemon (impl `Sources`). state, watch, config -> memory-core/std only. No cycles. `resolve -> daemon` is a trait impl, a reasonable direction (the shell implements the daemon's trait). Odd edge: daemon -> hook (F9). `daemon` does not touch `state` internals beyond `StateDir::read/write/hints_dir`, `StateFile`, `Loaded`, `MissingReason`. `state` knows nothing of R2 or the store. Daemon ownership: five responsibilities, see F2.

Q2 Traits:
| trait | where | methods | prod impls | test impls |
|---|---|---|---|---|
| `Clock` | environment.rs:8 | 2 | 0 (only the `Counting` wrapper, environment.rs:291) | 1 (`TestClock`, tests/common/clock.rs) |
| `FileSystem` | environment.rs:30 | 3 (+assoc `Error`) | 2 (`RealFs`, `SharedFs`) + `Counting` | 2 (`MemFs`, `Spy`) |
| `Store` | environment.rs:174 | 3 | 0 | 6 (`FakeStore` x2, `Judged`, `MemStore`, `FStore`, `Ghost`) |
| `Sources` | daemon.rs:250 | 1 | 1 (`WalkSources`) + blanket for `FnMut` | closures |
`Scheduler` is a struct generic over a closure, not a trait; there is no watcher trait (single `FsWatcher`). Note `Clock` has zero production implementors and `main_entry` exits 78, so no production time source exists yet; trivial (`Instant` + `SystemTime`, about 15 lines) but real. `Store` shape: sync, `&mut self`, closed `StoreError` with a 412 vs 404 rule documented (environment.rs:~185), `ETag` is a memory-core type, bodies are `Published`. This is implementable by `rusty-s3` (request signing) + `ureq` (blocking) without changing callers. Caveat: `Published` constructors are `pub(crate)` (environment.rs:146,154), fine for an in-crate adapter. No `list`/`delete`: not needed by the commit path as read. Method with no production caller: I did not check `FileSystem::stat_len` (environment.rs:43) [not checked].

Q3 State representation: see F1, F5, F2. Largest interpretive ladders: `settle` (~50 lines, 6-arm match plus an if-let guard) and `reconcile_once` (~66 lines, linear but long). A `BTreeMap<Owner, OwnerState>` with a `Phase` enum would remove the `due` ladder, the `needs_recover` branches, and the parked rebuild/sort.

Q4 state.rs: files `roots.json`, `index.json`, `status.json`, `machine_id` (state.rs:200-232), schema `name/1` per file, envelope magic `memory-state/1`. Versioning: schema version in the header; newer schema is `NewerSchema`, a different envelope version is `UnknownVersion`. Durable write: stage (create_new 0600, write, sync) -> rename -> dir sync, `publish_new` via `link`. Load outcome is an enum (`Loaded`, `MissingReason`), then collapsed in daemon (F8). One generic loader exists (`load_state`), one generic writer path; write duplication is only the serde step (F7). `decode` is about 65 lines of early returns, linear and readable; a small `Header::parse` returning `Result<Header, MissingReason>` would shorten it by 15 lines [low value].

Q5 watch/resolve/config:
- `watch.rs`: three jobs in one file: pure `Scheduler`, `translate`, and bounded `inbox` + `FsWatcher`. Splittable but cohesive at 330 lines. Platform: only `notify::recommended_watcher`; on Linux that is inotify with no change to this file. The `translate_with` rename/dir heuristic is FSEvents-motivated but harmless on inotify (the `is_dir` probe still gives the right answer). No `cfg(target_os)` anywhere in my scope; but `std::os::unix` is used in state.rs, config.rs, resolve.rs (fine for Linux, would block Windows, not planned).
- `resolve.rs`: two jobs joined: privacy facts (`resolve`, `chain`, `pack`) and `WalkSources`/`SharedFs`/`Counts`. `resolve()` is about 55 lines with 5-level nesting (resolve.rs:102-150) and mutable accumulators (`path_resolved`, `file_identity`, `final_path`) reset at the end; a `try`-style inner function returning `Option<Resolved>` would remove the flag juggling. `profile_of` has a `_ => OtherKnown` catch-all: a new `SourceKind` silently becomes `OtherKnown`; for a privacy path prefer exhaustive arms (resolve.rs:~75-90). Medium-low.
- `config.rs`: single job, validated parse with closed errors. Knob use: see F11. `valid_bucket` re-implements bucket naming (config.rs:~125); fine.

Q6 Errors: `StateError` (10), `ConfigError` (11), `StartError` (wraps both), `RoundError` (State, Clock): one enum per module as designed; `watch` uses `notify::Error` and `CoalescerError`; `resolve` has none (flags). All variants I read are constructed in non-test code. Stringly: none in my scope; `StateError::Io(InvalidData)` for a serde failure is a mis-typed error (F7), and `ManifestError::BadTimestamp` reused for a clock out of range (daemon.rs:~401; mapped to `RoundError::Clock` so contained). Duplicate string tables: F3.

Q7 Longest functions: `reconcile_once` daemon.rs:887 (~66), `decode` state.rs:329 (~65), `settle` daemon.rs:641 (~50), `resolve` resolve.rs:102 (~55), `scan_hints` daemon.rs:586 (~55), `prepare_inner` state.rs:666 (~35), `FsWatcher::new` watch.rs:~285 (~35), `translate_with` watch.rs:190 (~35). >5 params (counting self): `run_owner` daemon.rs:806 (7), `land` daemon.rs:771 (7), `to_source` resolve.rs:164 (6), `settle` daemon.rs:641 (5), `recover` daemon.rs:707 (5). Deepest nesting: `resolve` (5), `scan_hints` closure (5). Names: F13.

Q8 YAGNI: F10, F11, F12. `sim` is documented and compiled out of release, but not minimal in shape (F12).

## 5. Checked and fine
- No cycles; `state.rs` has no crate dependencies.
- Lock-before-anything order is enforced by types (`prepare_inner(&DaemonLock, ..)`).
- Temp-file naming, sweep, `create_new`, 0600/0700 modes, uid probe reasoning (documented tradeoff, state.rs:~655).
- `Config::parse` ordering and closed errors; token is never read, only metadata.
- Overflow handling in `InboxTx::push` (flag + one `Rescan`) is small and correct as read.
- `FailureCounts`, `EventCounts` use `saturating_add` throughout.

## 6. Not read
capture.rs, commit.rs, realfs.rs, hook.rs, main.rs bodies; all tests (counted only); DESIGN.md and amendments (conformance statements are from doc comments, [inferred]); `FileSystem::stat_len` callers; callers of `write_durable`/`stage`.
