# 18 Daemon skeleton (slice 2, partial)

## Done
- `daemon.rs`: synchronous `Daemon::start` and `reconcile_once`, generic over `FileSystem`,
  `Store<Body = Published>`, `Clock` and a `Sources` closure.
- Startup order (DESIGN 12, 9.1-9.3): `DaemonLock::acquire`, `StateDir::prepare`,
  `vet_hints_dir`, `Config::load`, read `index.json` (missing or damaged means rebuild),
  read `status.json`.
- Round (10.3, 11.2-11.3): group sources by owner; per owner `recover_base` once after
  start, then `capture`, `attach_launches`, `commit`. `Update`s apply only after the commit
  landed. After a lost reply or an unconfirmed landing the base is dropped and recovered from
  the store. `commit::disposition` supplies the streak. Parked owners go to `status.json`
  and are skipped in later rounds. Index is written at most every 60 s when dirty and at
  `shutdown`.
- Hint scan: `hint::scan` from the stored offset; launches are rebuilt via
  `sanitize_launch`; offset advances only to the last record whose owner's commit landed.
- Schemas: `IndexBody { sources, hints }`, `StatusBody { updated_at, rounds, host_paused,
  parked }`, both JSON with `deny_unknown_fields`.
- `memory daemon`: runs startup; a held lock exits 75, a state or config refusal exits 1 or
  2, success of startup exits 78 (no adapters). No stdout, never captures.
- Tests (`tests/daemon.rs`): lock-first with `tmp/` survival, hints symlink refusal, invalid
  config refusal, first round plus rebuild without re-upload, binary lock-held exit 75.

## Remaining
- Tests for lost reply convergence, foreign-writer parking, hint launch attach, and the
  Hegel and proptest properties.
- No notify, no real store, no coalescer timing. Coverage not measured.

## Parent review and correction 1 (2026-11-09): test leaked into host state

The parent reran `cargo test -p memory` at 2,000 cases. `tests/harness.rs`
`scaffold_binary_refuses_capture_instead_of_success` spawned `memory daemon` with the
inherited environment. Now that startup is real, it resolved the host's
`~/.local/state/memory`, created an empty `daemon.lock` and empty `hints/` and `tmp/` there,
then exited 2 (no config) instead of 78. The parent confirmed the directory held only those
three empty entries, all created by this test run, and removed exactly them. Nothing else on
the host was touched. The test now uses an isolated temp state root with a valid synthetic
config and `env_clear()` (passing only `LLVM_PROFILE_FILE`). It still asserts exit 78 and
empty stdout and stderr, and no assertion was relaxed. Rule for all later tests: never spawn
`memory` without an explicit `MEMORY_STATE_DIR` inside a temp dir. Recheck: fmt 0, workspace
clippy 0, `cargo test -p memory` at 2,000 cases exit 0; `~/.local/state/memory` is absent.

Items still missing from the worker's delivery: lost-reply convergence, foreign-writer
parking in status.json, hint launch attachment with the offset advanced only after landing,
the Hegel and proptest properties, PauseHost, clock-error and damaged index/status tests,
and coverage.

## Round 9 (daemon tests worker)

Status: PARTIAL. Items 1-5 are written and pass at default and 200 cases. Item 6 (coverage) was not measured. The 2,000-case runtime is not met.

Done (tests/daemon.rs):
- Item 1: `lost_reply_converges_without_duplicate_epoch`.
- Item 2: `foreign_writer_parks_owner_only`.
- Item 3: `hint_launch_attached_once_and_offset_advances_after_commit`.
- Item 4: `auth_failure_pauses_host_and_recovers`, `unrenderable_wall_clock_is_a_round_error`, `damaged_index_and_status_are_treated_as_missing`.
- Item 5: `hegel_daemon_state_machine`, `proptest_daemon_state_machine`, `guard_has_negative_controls`.

Defects found in daemon.rs: none. daemon.rs is unchanged.

Timing: one case costs about 0.1 s, because every round does a durable (fsync) status write plus a temp dir and restarts. Measured after shortening to 8 steps and 4 settle rounds: 200 cases take about 21 s per property. That extrapolates to about 210 s per property at 2,000 cases, against the 60 s target. A first run with 25 steps and 6 settle rounds was 20 minutes or more at 2,000 cases and was killed by the parent.

Remaining:
- Reach 60 s at 2,000 cases. The cost is the durability path in state.rs, which I don't own. Requested change: a durability seam, for example a `StateDir` option or `write_durable` flag that skips F_FULLFSYNC for tests. With it, the properties would also not need a fresh temp root per case, and the status write per round would be cheap.
- Item 6: coverage (`cargo llvm-cov`) was not run, so the uncovered lines in daemon.rs are not reported.
- The final clippy and full 2,000-case run were not repeated after shortening the sequences. Clippy passed (exit 0) before that edit.
- Hint-launch canary: the canary is checked only in stored objects. The test does not exercise a non-allowlisted env var beyond one canary.

## Parent review and correction 2 (2026-11-09): status write frequency, property runtime

Profiling the 200-case run (`sample`) showed about 75% of worker samples under
`state::write_durable`, then `fcntl(F_FULLFSYNC)`. `reconcile_once` rewrote `status.json` on every
round. DESIGN 11.3 makes the parked list authoritative but sets no write frequency. The parent
changed `daemon.rs` so status is written at once when `host_paused` or the parked list changes
(and on the first round), otherwise at most every `INDEX_INTERVAL` (60 s, as 9.2 sets for the
index), and always at `shutdown` once a round has run. Durability is unchanged and no seam skips
fsync. Recheck: clippy 0; `tests/daemon` 14/14 and `tests/harness` 3/3 at 2,000 cases, exit 0.

Open: the two daemon properties still take about 6 minutes together at 2,000 cases (357 s
wall), because their rounds advance the clock 61 s, so each round does real F_FULLFSYNC writes
plus per-case temp-dir/lock/prepare. Case counts were not lowered and the tests are not ignored.
Options for a later round: shorter clock advances with the index checked after shutdown, or
a durability seam in `state.rs` used only by simulation tests (state.rs's own tests keep the
real path). Coverage of `daemon.rs` was not measured by the worker; see gate 7.

## Coverage round (2026-11-09, COVERAGE-B worker)

Test-only; nothing under `src/` was edited. All new tests are in `rust/crates/memory/tests/daemon.rs`.

Uncovered before (integrated gate): daemon.rs 62, 191, 218, 383, 386, 396, 402, 404, 413, 416, 446, 447, 463-466, 533, 542, 555, 590, 607, 639, 645, 646, 657, 670, 671, 676-678; config.rs 104-106; state.rs 125, 126, 172, 173, 420, 443, 526-551, 607, 637, 638.
Uncovered after (segments with count 0, 50-case llvm-cov): daemon.rs 218, 386, 404, 542; config.rs none; state.rs 126, 172, 173, 420, 527-551 (only `.map_err(io)?` error arms: 527, 528, 530, 532, 535, 536, 542, 546-548, 550, 551), 607, 638.

New tests and literal expectations:
- start_error_code_table: bad config -> ("config_parse", 2); `tmp` is a file -> ("state_not_directory", 1).
- window_defaults_fill_the_missing_half: quiet=10 -> cap 300 s; cap=120 -> quiet 60 s.
- state_api_failure_table: unwritable parent, unreadable tmp/, unwritable root -> Io(PermissionDenied); body > MAX_BODY_BYTES -> TooLarge.
- unwritable_state_dir_is_a_round_error_on_first_status_write / unwritable_index_is_a_round_error_when_status_is_not_due: RoundError::State(Io(PermissionDenied)).
- status_inside_interval_is_deferred_then_flushed_at_shutdown: status.json bytes unchanged at 30 s; rounds on disk 3 at 60 s, still 3 at 61 s (memory 4); 4 after shutdown.
- authoritative_park_is_written_inside_the_interval: NewerRemoteSchema park at 10 s is on disk with rounds 2; at 20 s skipped=1, disk rounds stays 2 (memory 3).
- two_parked_owners_are_listed_sorted: (parked 2, landed 0), owners [s1, s2].
- corrupt_remote_parks_the_source_after_three_hours: (failed,parked) = (1,0),(1,0),(0,1) at 0, 1 h, 3 h; reason Source(RepeatedSchemaError); then skipped=1, zero PUTs.
- two_sources_of_one_owner_commit_once: landed 1.
- unlanded_first_write_resets_the_base_and_converges: (0,1) then (1,0).
- hint_partial_progress_commits_prefix_and_torn_tail_waits: offset = junk+first record while offline; then junk+both records, torn tail never consumed.
- replaced_hint_file_restarts_and_unreadable_entries_are_skipped: offset restarts to the shorter file length; directory named like a day file gives offset 0.
- missing_hints_dir_is_an_empty_scan: landed 1.
- shutdown_table: no round -> no status.json; unwritable root after a round -> Err(Io(PermissionDenied)).
- prepare_sweeps_tmp_entries_and_stale_root_temp_files: tmp/ emptied, `.index.json.tmp-1-1` removed, `keep.json` and `.hidden` kept.

Unreachable / blocked:
- daemon.rs 386 (non-UTF-8 hint file name): APFS rejects such names (EILSEQ); a test was tried and removed. Reachable on Linux only. No src change requested.
- daemon.rs 218 (`default_root()` is None): needs HOME and MEMORY_STATE_DIR both unset in the binary; not spawned per the rules. Request: none (or an injectable root).
- daemon.rs 404 (both `scan` calls Err): `scan(&bytes, 0)` never fails, so dead code. Request: replace with `unwrap_or_else`/infallible form.
- daemon.rs 542: `attached` is false only if `LaunchRecord::from_sanitized` fails; not producible from sanitized input. Likely dead.
- state.rs 172-173 (flock other error), 126, 420, 607, 638 and the `io` arms in prepare: need injected OS faults; not reachable without a filesystem seam.

Defects: none in src. Note: at 50 cases `hegel_daemon_state_machine` (existing coverage guard) and `commit_protocol::hegel_property4` ("lost-reply" never produced) fail; both pass at 2,000. I did not touch them.

Commands: clippy `-Dwarnings` exit 0; `cargo llvm-cov` at 50 cases exit 101 (the two guards above; report generated with `--no-report` then `cargo llvm-cov report`, exit 0); final `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory --test daemon --test harness -- --test-threads=2` exit 0 (run after all tests, including the prepare-sweep test, were added). `ls -d ~/.local/state/memory`: absent.
Paths released: tests/daemon.rs and this doc section.

## Direction 2026-11-09

The property runtime is addressed by [amendment A7](../design-amendments-20261109.md) (test-only `SyncPolicy::None` for the simulation properties, production unchanged, plus a crash-point property), based on [P12](../prototypes/P12-durable-state-writes.md). Not yet implemented.
