# Evidence 49: capture only the live session record (brief 54)

All dates are UTC. Status: increment RETIRE accepted locally at `9566497` on 2026-11-23. That is
4 commits (3 by workers, 1 by the parent) over the brief `ba3e5bf`, on `main` `a6a7755`. Install
on both hosts follows the merge and is recorded below when done.

[Brief 54](../../54-capture-reduction.md) records the measurements behind the decisions.

## Commits

| Commit | By | Change |
|---|---|---|
| `f07acab` | worker | `CAPTURED_KINDS` allowlist and `pi_run_record` in `locator.rs`, a final filter in `locate` for every layout; `claude_external` returns `Ignored` for a dropped kind before the owner lookup; `deferred_snapshot` removed from the walk, `memory status` and `memory check`; locator, walk and rig tests moved to kept kinds |
| `c5d4f4b` | worker | `resolve::retired` (pure: the id's namespace selects the root, the stored path is located), `Probe::Retired` from `WalkSources::locate_absent`, `candidates` skips retired entries, `all_gone` treats them as not live, `check` counts them `retired=` after `remote_only=` |
| `093cdef` | worker | Module docs |
| `9566497` | parent | Test for the surviving tooth: the real `WalkSources::locate_absent` retires `history.jsonl` and a file-history path (present or removed) and still returns a handle for a kept transcript |

Two workers: the first (`a43b0de1`) stopped twice at its turn limit with the locator change and
most locator test updates uncommitted; the parent accepted that diff as the base and a second
worker (`80f7d246`) finished it.

Deviations, accepted:

- `claude_external` returns `Ignored` for a dropped kind before it looks up the owner. Every kind
  that reaches it (file-history, session-env, debug, todos, tasks) is dropped, so a stored path
  judged without the walk's Claude session index is retired rather than `UnknownOwner`. As a
  result no current layout produces `unknown_owner`; the verdict stays in the enum.
- The rig tests run a real daemon over a real walk, so they were moved to kept kinds rather than
  fed hand-built sources (parent decision): the seven `check` tests use a Claude tool-results file
  where they used a debug file; `file_history`, `snapshot_kinds` and `snapshot_deletions` use a Pi
  run record (the kept `Scanned` snapshot kind) and assert the dropped files are `ignored`.
- The five Codex store rig tests (captured/checked/restored, failed backup counted as local,
  reconcile local=1, resnapshot of changed pieces, sim interval override) are replaced by
  `codex_sqlite_stores_are_ignored_and_nothing_lands`: no PUT, nothing in `state/tmp`, `check`
  exit 0 with `ignored=6`. The capture-level tests in `tests/sqlite_store.rs` are unchanged.
- `a_deferred_snapshot_is_counted_apart` became `session_env_usage_and_paste_files_are_counted_ignored`.
- `memory export` refuses a shared owner, so the scanned-kind test exports the session owner and
  restores the shared one.

Capture code left without a production path (kept, as the brief says; removal is a later
decision): the Codex SQLite backup (`sqlite_backup`, `begin_snapshot`, the `CodexStore` branch in
`resolve.rs` and `check.rs`), the 6 h snapshot interval, and config scrubbing for config kinds.
The content scan still runs on Pi run records.

## Parent acceptance

- Whole diff read.
- Teeth (`b54_teeth.py`, focused filter over `core_locator`, `resolve`, `realfs`, `daemon` and the
  `check`, `file_history`, `snapshot_*` and `codex_store` rig tests; baseline 219 passed): 18 of
  19 caught after the parent test; 1 equivalent.
  - Caught: a dropped kind kept (Claude memory, Codex history, Codex store, Pi config, any
    `PiAux`), the run-record shape one level deeper, with any extension, or dropped, a kept kind
    ignored (tool results, Pi session), the owner lookup before the dropped-kind check,
    `Probe::Retired` counted as skipped or turned into a handle (caught by `9566497`), the
    owner-set rule removed, `retired` counted as `remote_only`, not counted, or failing the exit,
    the helper always false.
  - Equivalent: Claude file-history added to the final allowlist; `claude_external` returns
    `Ignored` first, so the filter never sees it.
- Property tests on the worker tip (the worker had not run them after its last locator change):
  69 of 69 at 500 cases.
- Full gates on `9566497`: 14 of 14, seed 3170722460, line coverage 95.21 % (95.68 % before; the
  Codex store rig paths are no longer exercised end to end; floor 80).
- Linux container: `core_locator` 23, `resolve` 38, `daemon` 107, `s3_store` 143 (1 ignored),
  `memory --lib` 10, all passed.

## Measurements behind the decisions (2026-11-23, counts only)

Walk census of located sources before the change (files, MB), this Mac / dev-01: kept kinds
`ClaudeTranscript` 255 / 131, `ClaudeSubagent` 837 / 748, `ClaudeToolResults` 827 / 1,152,
`CodexRollout` 29 / 138, `PiSession` 4,123 / 2,225, Pi run records 0 / 59 (0.24 MB). Dropped:
`PiAux` other 72 / 244 (39 MB on dev-01), `ClaudeAux` 946 / 755, `ClaudeFileHistory` 946 / 514,
`ClaudeMemory`, `ClaudePlan`, `ClaudeHistory`, `ClaudeConfig`, `ClaudeDebug` 136 / 59,
`CodexStore` 5 (655 MB) / 5 (222 MB), `CodexHistory`, `CodexConfig`, `CodexLog` 6 / 7, Pi config
kinds 7 / 6.

- New Codex threads still write rollouts: 8 of 8 started and 7 of 7 active in the last 30 days.
- Claude subagent meta files: `agentType` and `description` equal the parent's Agent call in 772
  of 772 located, `model` in 453 of 461 (the 64 not in the subagent transcript are aliases),
  `effort` in 50 of 50. The 6 Claude session-folder `.txt` files hold only tool calls already in
  the session's transcripts.
- dev-01 Pi `subagent-artifacts`: all 2,553 tool calls of the 59 run logs are in captured child
  sessions (56 located with model and usage); the 3 without tool calls are 2 empty failed runs and
  one with 1,112 of 1,114 lines in sessions. Short `.md` files are input templates (66 files, 9
  distinct texts). Run records: resolved extensions (configured, effective, omitted) appear only
  there; requested model, exit code and acceptance appear in sessions for 13, 2 and 28 of 59.
- Codex `session_index.jsonl` holds only thread titles (67 here, 42 on dev-01).
