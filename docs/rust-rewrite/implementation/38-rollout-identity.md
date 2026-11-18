# 38. Codex rollout identity across archive, recompression and resume (2026-11-18)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 37](37-export-decompress.md). Evidence goes to `evidence/29-rollout-identity/`. Text that
states a plan stays as written; later changes are marked.

The acceptance run `20261117-a28bfa4` keeps running while this brief is worked on. Nothing here is
installed, sent to R2 or run against the run's state root.

## Goal

Codex moves, recompresses and decompresses rollouts as part of normal use (`docs/hooks.md`,
Codex): archiving renames `sessions/YYYY/MM/DD/rollout-*.jsonl` to `archived_sessions/`, a
rollout untouched for 7 days is recompressed to `.jsonl.zst`, and resuming one decompresses it
again. On `main` (`d123b3e`) each of these stops capture of that owner for good once capture has
to write. Every Codex session idle for a week would stop being captured, and a resumed session's
new turns would never land. This is a cutover prerequisite.

**IDENTITY**: each of these Codex actions is captured with no failure, and the owner's export
afterwards holds the session's current content once as a `main` source.

## Parent probes (2026-11-18 22:20Z to 22:43Z, throwaway worktree against the fake store, removed)

Each scenario ran `reconcile_once` repeatedly after the change. "Loops" means: round 1
`failed=1 deterministic=1`, every later round `deferred=1`, never parked, never landed.

| Scenario | Result | Cause |
|---|---|---|
| Plain rollout moved to `archived_sessions/` (same inode), unchanged | fine (nothing to write) | — |
| Same, then appended to | loops | `Change::Extend` with a new path; `same_identity` refuses (`SourceChanged`) |
| Plain rollout replaced by its `.jsonl.zst` | loops | new inode, `Change::Epoch` with kind `CodexCompressed`; `SourceChanged` |
| `.jsonl.zst` captured first, replaced by a longer plain file (resume) | loops | `Change::Epoch` with kind `CodexRollout`; `SourceChanged` |
| Plain and `.zst` present at once | loops, from the first capture | both map to one source id; `DuplicateSource` fails the whole owner |
| Same name under `sessions/` and `archived_sessions/` at once | loops, from the first capture | as above |

## Checkout

`main` is `d123b3e`. This brief is on `docs/brief-38`. The worker gets
`../memory-wt/b38-identity` on `rust/rollout-identity` from `docs/brief-38`. Crate paths are under
`rust/crates/`.

## Facts checked on `d123b3e`

| Fact | Where |
|---|---|
| `codex_rollout` gives `rollouts:<name without .zst>` to every spelling, kind `CodexRollout` or `CodexCompressed`, role `Main`, class `Append`, owner the stem | `memory-core/src/locator.rs:732-753` |
| Locator tests pin one id for every spelling: `row7c_codex_sessions_archive_and_zst_share_one_owner_session_and_id` and the Codex rows of `core_locator.rs` | `memory-core/tests/core_f2_gaps.rs:557-608`; `core_locator.rs:510-540` |
| `SourceEntry::same_identity` refuses a change of kind, path or class on `Extend`, `Epoch` and `Replace`; `Deleted` refuses a change of kind | `memory-core/src/manifest/state.rs:22-30`, `:94-160` |
| An `Epoch` on an existing entry clears `deleted_at` and requires `n = last + 1` | `state.rs:119-142` |
| `apply_changes` refuses two changes for one id (`DuplicateSource`); the walk passes every located file on | `state.rs:77-92`; `memory/src/resolve.rs:373-425`, `:502-533` |
| The reader folds with the same `OwnerState` code, so a fold rule is shared by writer, `check`, `restore` and `export` | `memory-reader` |
| A missing source is `Deleted` after two consecutive missing reconciles | `memory-core/src/planner.rs:59-72`; `memory/src/capture.rs:429-451` |
| `memory export` skips deleted sources and exports each live epoch; `CodexCompressed` is decompressed (brief 37, D46) | `memory-reader/src/export.rs` |
| The check line is one fixed format; walk counts are `memory::resolve::Counts` | `memory/src/check.rs:150-170`, `resolve.rs:251-271` |

## Decisions

Decided by the parent on 2026-11-18 (DESIGN D47); each is reversible before cutover and the
operator may override. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Plain rollout id | Unchanged: `rollouts:<name>.jsonl`, for every location (`sessions/…`, `archived_sessions/`) |
| Compressed rollout id | `rollouts:<name>.jsonl.zst`: its own source in the same owner. A source's kind never changes, so every epoch of an entry has one encoding and export decompresses exactly the `CodexCompressed` entries |
| Path | May change on `Extend` and `Epoch` of an append source; the fold stores the new path. Kind and class still may not change (`SourceChanged`). `Replace` (snapshot) unchanged |
| Recompression | Plain source missing twice is `Deleted`; the `.zst` source is captured fresh. Between the two, an export holds both (transient duplicate, accepted) |
| Resume | The `.zst` source is `Deleted` when missing twice; the plain source, if deleted, gets a new epoch (`deleted_at` cleared), exported as the active epoch beside the sealed old one, as for any rewrite |
| Two files, one id, at once | The walk keeps one per (owner, id): the lexicographically first root-relative path. Others are not yielded and are counted in a new `Counts::duplicate`; `memory check` prints `duplicate=N` after `unknown_owner=N`. A duplicate does not change the check's exit code. Same rule in `enumerate`, `of` and the hint path (`note`) |
| Not changed | Stored formats and schemas, the Claude and Pi locators, `check` verdicts, `restore`, `export`, log lines other than the check line |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | IDENTITY | `rust/rollout-identity` | worker (Sonnet, medium) |

Owns `memory-core/src/manifest/state.rs`, `memory-core/src/locator.rs` (`codex_rollout` only),
`memory/src/resolve.rs`, `memory/src/check.rs` (the count and the line), and tests in
`memory-core/tests/{core_manifest,core_locator,core_f2_gaps}.rs`, `memory/tests/` (a new
`s3_store/rollout_identity.rs` module) and whatever test pins the check line. Read-only elsewhere.

- Proof (each a named test):
  - Core manifest: `Extend` and `Epoch` with a new path apply and the folded entry has the new
    path; a new kind or class is still `SourceChanged`; `Replace` and `Deleted` unchanged.
  - Locator: plain spellings (`sessions/…`, `archived_sessions/`) share one id; `.zst` spellings
    share another (`rollouts:<name>.jsonl.zst`); all four have one owner; the existing
    sibling/namespace properties hold.
  - Walk: two files with one id under one owner yield one source (the first path) and
    `duplicate=1`; `of` and `note` agree with `enumerate`.
  - Daemon against the fake store (each asserts every round `failed=0 parked=0`, then a check
    with `mismatch=0 uncaptured=0`, and the export of the owner through the real reader):
    - archived then appended: lands; the entry's path is `archived_sessions/…`; restore equals
      the file.
    - recompressed: the `.zst` lands at once; the plain source is `Deleted` after two missing
      reconciles; the final export has exactly one live `main` source, decompressed, equal to the
      original JSONL.
    - resumed (`.zst` first, then a longer plain file): the plain source lands; the `.zst` is
      `Deleted` after two missing reconciles; the export's active plain body equals the file.
    - resumed after recompression (plain, then `.zst`, then a longer plain): the plain source gets
      a new epoch and lands.
    - plain and `.zst` at once: both land.
    - `sessions/` and `archived_sessions/` copies at once: lands, `duplicate=1`.
- Teeth (the parent applies each; each must fail a named test): path check restored in
  `same_identity`; fold keeps the old path; `.zst` id equal to the plain id; dedupe removed; the
  kept duplicate chosen by walk order instead of path order.

## Rules for the worker

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply, with those of
briefs 29 to 37. No new dependency and no feature change. Never inspect, signal or read the
state of the running acceptance daemon or anything under `~/.local/state/memory-acceptance*`;
never read `.env`; never contact R2; never use `git stash`. Cargo only through
`nix develop /Users/mm/dev/memory#rust --command sh -c 'cd /Users/mm/dev/memory-wt/b38-identity/rust && ...'`,
one cargo job at a time. Commit after each working step.

## Acceptance (parent)

Whole diff read; fmt, clippy, rustdoc, nextest, doctests, schemas, `cargo deny`, lints; the teeth
above; `scripts/gates.sh` once on the tip with `MEMORY_ANALYTICS_PYTHON` set; docs (DESIGN
section 5 and decision log, handoff); push (secret check of the range), hosted gates, merge.

## Ledger

- 2026-11-18 23:00Z: brief written on `d123b3e`.
- 2026-11-18 23:36Z: IDENTITY accepted locally at `213e67b` (worker 5 commits). Deviation
  accepted: the "Recompression" and "Resume" rows' deletion of the vanished spelling does not
  happen, because the daemon captures only files the walk yields and has never recorded a
  deletion (the existing slice 3 gap of briefs 30 and 31); the scenarios assert the landed state
  with both spellings live. Teeth 6 of 6. Full gates on `213e67b`: 14 sub-gates 0, seed
  1620338970, coverage 96.55 %. Record: [evidence 29](evidence/29-rollout-identity/). Next: push,
  hosted gates, merge.
