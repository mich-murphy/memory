# Evidence 34: `memory status`, `memory unpark`, `memory reconcile` (brief 42)

All dates are UTC, 2026-11-20, from 06:14Z to about 07:10Z. Status: increment OPS accepted locally
at `4d5e1d6` (5 worker commits and 1 parent commit over `7c69dbb`). Nothing was installed, sent to
R2 or run against the production state root; the production daemon (`a47eb19` build) kept running
throughout.

[Brief 42](../../42-operations-commands.md) sits on `7c69dbb` (on top of `main` `c986828`).
Operator request, 2026-11-20: build the day-to-day commands. Before this brief `main.rs` exited 78
for `status`, `unpark` and `reconcile`.

## Commits

| Commit | By | Change |
|---|---|---|
| `3f0025f` | worker | `status.rs`: read-only, offline local view. No lock, nothing written, the store is never built (the token is not read). Reads `config.toml`, `status.json`, `index.json` (`check::read_state`), walks the roots, stats each source: `current behind changed unindexed`, `withheld` (by `admit`, no read), `oldest_behind_s`, walk counts; failure and audit counts; one `parked` line per entry (`check::parked_line`, shared). Exit 0, 1 (parked, host paused or `status.json` not loaded), 2, 64; `--verify` is 64 |
| `d6b0f46` | worker | `unpark.rs`: `<owner-prefix>` or `--all`; takes the daemon lock (75 beside a daemon); `not_parked` 1, `status_damaged` / `status_newer_schema` 2 with the file untouched; every other field kept; output `unparked owner=<prefix> entries=<n>` |
| `ea56823` | worker | `reconcile.rs`: signals, `start_from_config` (the lock), the log, one `reconcile_once`, `round` with the new `Cause::Manual` (`manual`), shutdown; stdout `reconcile landed= failed= parked= skipped= deferred=`; exit 0, 1, 2, 75, 130 |
| `869ccf5` | worker | `docs/install.md` "Operations": the three commands and the launchd stop, unpark, start sequence |
| `ce8b561` | worker | `tests/harness.rs`: the "unknown word exits 78" test used `status`; it now uses `frobnicate` (assertions unchanged) |
| `4d5e1d6` | parent | Two tests the worker left out: `status_runs_beside_a_held_lock`; `reconcile_stopped_by_sigterm_during_a_held_put_exits_130` (SIGTERM while a piece PUT is held: exit 130, empty stdout, `current.json` never written) |

### Worker deviations accepted

- `read_status` and `code_of` were made `pub` in `check.rs` (with a generic `read_state<T>` and a
  shared `parked_line`) instead of moving to a new module; nothing is copied.
- `tests/log.rs::round_names_every_cause` gained `Cause::Manual` (outside the ownership list; the
  only test that enumerates causes).
- `reconcile` takes its stop flag from a private `watch::inbox(1)`, the daemon's mechanism. It exits
  130 whenever a stop was requested, also if the round had just ended; a stop during a round leaves
  it unfinished, so the two coincide in practice.
- Snapshot sources that are `changed` are left out of `oldest_behind_s`: the index keeps no
  observation time for them.
- `as_of=none` when `status.json` is not loaded.

The walk only stats files and resolves paths (`resolve.rs`, `realfs.rs`); the only byte read is
`FileSystem::read`, which `status` never calls, so "no source byte is read" holds.

## Teeth (parent)

Script `ops_teeth.py` in the parent's scratchpad; each tooth replaced exact anchors, ran
`test(/ops::/) | test(/round_names_every_cause/)` and restored.

| Tooth | Caught by |
|---|---|
| `status` takes the daemon lock | `status_runs_beside_a_held_lock` |
| `status` writes a file under the state root | every `status_*` test (listing unchanged) |
| `status` builds the store and sends a GET | every `status_*` test (zero requests) |
| `status` prints source ids | every `status_*` test (leak check) |
| `behind` and `current` swapped | four `status_*` tests |
| `unpark` without the lock | `unpark_beside_a_held_lock_refuses_with_75` |
| `unpark` writes on no match | `unpark_without_a_match_exits_1_and_keeps_the_bytes` |
| `unpark` treats a damaged file as empty | `unpark_refuses_a_damaged_status_and_keeps_the_bytes` |
| `unpark` drops the counters | `unpark_one_owner_keeps_the_other_and_the_counters`, `unpark_all_empties_the_list` |
| `reconcile` ignores the lock refusal | `reconcile_beside_a_held_lock_refuses_with_75_and_no_request` |
| `reconcile` exits 0 on a failure | `reconcile_with_an_unreachable_store_fails_with_exit_1` |
| `reconcile` exits 0 when stopped | `reconcile_stopped_by_sigterm_during_a_held_put_exits_130` |
| `manual` rendered as `timer` | `round_names_every_cause`, `reconcile_captures_a_new_file_then_lands_nothing_with_zero_puts` |

13 of 13 caught. The first and the twelfth were caught only by the two parent tests.

## Checks

- Worker: fmt, clippy `-Dwarnings`, `cargo doc`, `cargo deny check` ok; workspace nextest with the
  consumer gate: 875 passed, 5 skipped.
- Parent, `4d5e1d6`: `ops::` 18 of 18; then `nix develop .#rust --command sh -c 'cd rust &&
  MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python PROPERTY_CASES=500 bash
  scripts/gates.sh'`: all 14 sub-gates 0, seed 4231285103; 04-nextest 809 passed, 05-hegel 32,
  06-proptest 34, 10-consumer 2; coverage 96.00 % (10,518 of 10,956 lines).

## Limits

- `status --verify` is not built; `memory check` is the verified view (DESIGN 11.3, D51).
- `unpark` clears by owner only: source ids are never printed, so they are not an argument.
- `index.json` is written at most every 60 s, so `status` may show a source captured in the last
  minute as `behind`.
- Not run: 2,000 property cases, `cargo mutants` (operator decision).
