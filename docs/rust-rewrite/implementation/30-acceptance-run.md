# 30. Commit pauses, the walk for slice 2, the acceptance check and the 2-day run (2026-11-17)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 29](29-event-loop.md) (the event loop, merged as `f15f651`) and the operator's answers of
2026-11-17 recorded in the handoff's "State on 2026-11-17" block (decisions 1 to 3 and answers 4
to 10). Evidence goes to `evidence/21-acceptance-run/`. Text that states a plan stays as written;
later changes are marked.

## Goal

Run the approved 2-day acceptance run (DESIGN 17 slice 2b exit criterion, A13.1, A14.2) on this
Mac's real session directories, writing only under `rust-acceptance/<run-id>/` in the existing
bucket, and decide from it whether streaming capture is needed before the first production
write. Before the run can start, four things must exist:

1. the commit pauses of decision 3 (D44);
2. a walk that captures what slice 2 says and no more: append-class sources, with snapshot and
   config kinds withheld and counted, and Claude per-session artifacts owned through the
   transcripts the walk sees;
3. a read-only `memory check` that measures the exit criterion: restore every captured source
   and compare it with `transform(local)`, count parked owners, count local session files with
   no captured owner;
4. the run's installation: a launchd user agent `memory-acceptance`, its own state root and
   config, a 0600 token file, the Rust hooks for Claude, Codex and Pi beside the Go hooks.

## Checkout

`main` is `90cdbdf`. This brief and the operator's answers are on `docs/brief-30`. Branches go on
top of it in the order of the increment table; the parent creates each worktree under
`../memory-wt/`.

## Facts checked on `90cdbdf` before writing

A read-only planner scoped the work; the parent rechecked each fact below. Paths are under
`rust/crates/`.

| Fact | Where |
|---|---|
| `Daemon::land` passes an empty pause, so every commit pause is zero | `memory/src/daemon.rs:876` |
| The verification GET pauses after attempts 0, 1, 2 (`GET_ATTEMPTS = 4`); the rebase loop pauses with 1 to 4 (`REBASE_RETRIES = 4`). Both call the same `FnMut(u32)`, so the two waits share the numbers 1 and 2 | `memory/src/commit.rs:27-29`, `:350-377`, `:564-572` |
| One test pins a pause value (`vec![1]`, a rebase), three pin counts | `memory/tests/commit_protocol.rs:1675-1682`, `:1824`, `:1858`, `:2031`, `:2066` |
| `disposition::jittered` has a 1 s floor, so with a 1 s ceiling it never jitters; it cannot express `uniform(0, ceiling)` | `memory-core/src/disposition.rs:88-95` |
| The daemon already has a jitter source (splitmix over a counter and the clocks) and a boxed-probe setter pattern | `memory/src/daemon.rs:656-664`, `:619-624` |
| Nothing withholds snapshot or config kinds. A located snapshot-class file is yielded by the walk, admitted with the production rules digest and captured by `snapshot()` (replace) | `memory/src/resolve.rs:345-384`, `memory/src/assemble.rs:130-136`, `memory/src/capture.rs:413-420`; DESIGN 17 slice 2 says "Snapshot and config kinds stay withheld until slice 3" |
| Every config asset is snapshot class | `memory-core/src/locator.rs:492-494` |
| Claude `file-history`, `session-env`, `debug`, `todos`, `tasks` need the `ClaudeSessionIndex` (uuid to project slug). Nothing in production fills it: `locator_roots` builds `Roots::new` without it, so all of them are `unknown_owner`. Of these only `debug` is append class | `memory-core/src/locator.rs:150-228`, `:561-575`, `:619-622`; `memory/src/roots.rs:79-84` |
| The walk counts `unknown_kind` and `unknown_owner` and never yields them | `memory/src/resolve.rs:249-268`, `:365-369` |
| The local index holds no owner and no path (keyed by `SourceId`); a checker cannot enumerate captured owners from it. The `Store` trait has no LIST | `memory/src/daemon.rs:104-125`; `memory/src/environment.rs:190-` |
| Durable state is written temp-then-rename and read without the lock, so a second process can read `status.json` while the daemon runs | `memory/src/state.rs:393`, `:548-599` |
| Capture produces piece bytes with `transform` then `authorize` in the private `Job::authorized_body`; the audit already does stat, read `[0, size)`, stat, transform | `memory/src/capture.rs:604-635` |
| `memory-reader` restores the latest epoch of a source and checks size and digest | `memory-reader/src/lib.rs:99-122`, `:124`, `:185` |
| The Rust hook reads the event, `session_id` and `transcript_path` or Pi's `session_file` from stdin JSON. It ignores argv flags; the Go Pi extension passes `--path`/`--event` flags and no stdin | `memory-core/src/hint.rs:217-256`; `memory/src/hook.rs:278-304`; `internal/install/setup.go:26-50` |
| The hook and the daemon find the state root through `MEMORY_STATE_DIR`, else `$HOME/.local/state/memory` | `memory/src/state.rs:107-113` |
| `rustix` 1.1.6 is built with `std`, `fs` only and has no `getrusage`; the crate forbids unsafe code | `rust/Cargo.toml:30`, `:49` |

Measured on this Mac on 2026-11-17 (file names and sizes only, no content read):

- Claude: 2,990 files under `projects/`, 2,245 MB, largest 35 MB, 3 over 32 MB. 253 transcript
  uuids, none under two slugs. `session-env` 2,745 entries, 238 with a transcript uuid;
  `file-history` 80, all with one; `debug` 2, one with one; `todos` and `tasks` absent.
- Codex: 27 files under `sessions/`, 17 MB, largest 3 MB.
- Pi: 4,145 files under `sessions/`, 2,183 MB, largest 98 MB, 3 over 64 MB.

A capture holds its whole range in memory, so the first reconcile reads a 98 MB file whole. The
peak-memory target is 128 MB (A10.4); the run measures it.

## Decisions the parent made where the design is silent

Each is reversible before the first production write. A worker that finds one wrong reports it
as a question and does not decide.

| Topic | Decision | Reason |
|---|---|---|
| Pause lengths | `uniform(0, ceiling)` per D44: verification pauses after attempts 0, 1, 2 have ceilings 1 s, 2 s, 4 s; rebase pauses 1, 2, 3, 4 have 1 s, 2 s, 4 s, 4 s. A pure `memory-core` function maps (wait, draw) to a duration: draw 0 is 0, draw `u64::MAX` is the ceiling, monotone in the draw | Operator decision 3 and answer 7 |
| Which wait | The commit callback takes a closed `Wait::{Verify(n), Rebase(n)}` instead of a bare `u32` | The two waits share numbers today |
| Sleep default | The daemon sleeps for real by default (`std::thread::sleep`); tests install a recorder through a setter. Production cannot forget to wire it | Fail-safe default; `set_stop` is the pattern |
| A stop during a pause | Not cut. A pause inside the publish part runs to its end | Operator answer 7; "never between the write and its verification" |
| Snapshot and config kinds | The walk yields only append-class sources until slice 3. A located snapshot-class file is counted as `deferred_snapshot` and yielded by neither `enumerate` nor `of` | DESIGN 17 slice 2 |
| Claude per-session artifacts | The walk of a Claude root first lists `projects/<slug>/<uuid>.jsonl` names and fills the `ClaudeSessionIndex`; then it locates. A uuid under two slugs stays `unknown_owner` (the index already does this) | Locator says the shell supplies these facts; default 3 of DESIGN 19 keeps `ClaudeDebug` |
| What `memory check` compares | For each yielded source: GET its owner, take the latest live epoch, restore it, and compare with `authorize(transform(local[0, size)))` read with stat, read, stat. Equal: `match`. Local longer and the source prefix-stable and the restored bytes a prefix: `behind`. Local held still across the read and differs otherwise: `mismatch`. Local moved during the read, or longer and not prefix-stable: `unsettled` | Restore is checked against what capture would publish, not a reimplementation |
| Uncaptured | A yielded source whose owner has no `current.json`, or whose owner has no live entry for it: `uncaptured`. `unknown_owner` and `unknown_kind` are reported apart, with `deferred_snapshot` | Exit criterion; a deferred kind is withheld by design, not missed |
| Remote only | A live remote entry with no local file is `remote_only`, informational (deletions are slice 3) | Known gap |
| Check output | One line on stdout, `key=value` counts in a closed vocabulary, then one line per `mismatch`, `uncaptured` or parked owner with the owner prefix and the reason. Never a path, a source id or content. Exit 0 only when `mismatch`, `uncaptured` and `parked` are 0; exit 1 when any is not; exit 2 for a config or token refusal; exit 3 when the store failed, so the result is unknown | The run reads it twice a day |
| Check and the store | GET only. No write to the store or the state root; no lock taken | Runs beside the daemon |
| Peak memory | Measured from outside: `/usr/bin/time -l` in front of the daemon in the plist (peak at exit), and a parent sampler of `footprint`/`ps` RSS every 10 s during the run | No safe in-process API; no new dependency for one measurement |
| Hooks | Claude `settings.json` and Codex `hooks.json` gain a command `env MEMORY_STATE_DIR=<root> <abs>/memory hook <agent>` beside the Go entries; Pi gets a second extension that writes a JSON payload (`event`, `session_id`, `session_file`, `cwd`) to the hook's stdin. Backups of each edited file are kept outside the repository and restored at the end | Answer 5; the Rust hook already reads `session_file` |

## Increments

| # | Increment | Branch | Depends on | Wave |
|---|---|---|---|---|
| 1 | PAUSE: commit pauses | `rust/commit-pauses` | none | 1 |
| 2 | WALK: slice-2 walk (snapshots deferred, Claude session index) | `rust/slice2-walk` | none | 1 |
| 3 | CHECK: `memory check` | `rust/acceptance-check` | 2 | 2 |
| 4 | RUN: templates, hook extension, the run | parent | 1 to 3 | 3 |

### PAUSE

Owns `memory-core/src/pause.rs` (new, one line in `lib.rs`), its tests in
`memory-core/tests/`, `memory/src/commit.rs`, `memory/src/daemon.rs` (the sleep seam, `land`,
`next_draw` only), `memory/tests/commit_protocol.rs`, `memory/tests/daemon.rs`, and any other
test file only to adapt a `pause` closure. Must not touch `resolve.rs`, `realfs.rs`, `capture.rs`,
`run.rs`, `wired.rs`.

- Proof: the pure function at draw 0, `u64::MAX` and a property over draws (never above the
  ceiling, monotone); the commit protocol records `Verify(0..=2)` for four failed GETs and
  `Rebase(1..=4)` for exhausted rebases; the daemon, after an ambiguous `current.json` PUT,
  sleeps once per pause with each duration within its ceiling and the durations differ across
  draws; a healthy commit sleeps never.
- Teeth: swap the two `Wait` variants; pass `rebases - 1`; a ceiling of 2 s for rebase 4;
  the daemon ignores the draw; the daemon's default sleep is a no-op.

### WALK

Owns `memory/src/realfs.rs` (walk), `memory/src/resolve.rs`, `memory/src/roots.rs`,
`memory/tests/realfs.rs`, `memory/tests/resolve.rs`, `memory/tests/roots.rs`. Must not touch
`daemon.rs`, `commit.rs`, `capture.rs`, `memory-core/src/locator.rs` (use its public API).

- Proof (synthetic trees): a Claude root with `projects/<slug>/<uuid>.jsonl` and
  `debug/<uuid>.txt` yields the debug file under that session owner; the same uuid under two
  slugs leaves it `unknown_owner`; `settings.json`, `file-history/...` and a Codex SQLite file
  are counted as `deferred_snapshot` and yielded by neither `enumerate` nor `of`; append-class
  files are yielded exactly as before (the existing `enumerate` and `of` equality tests still
  pass); counts are exact.
- Teeth: snapshot rows yielded; index built after the locate; `of` yields a snapshot; a
  `deferred_snapshot` row counted as `unknown_kind`.

### CHECK

Owns `memory/src/check.rs` (new), the `check` arm in `memory/src/main.rs`, one line in
`memory/src/lib.rs`, `memory/tests/check.rs` (new), and in `memory/src/capture.rs` only the
extraction of the transform-and-authorize step into one `pub fn` that `Job::authorized_body`
then calls. Must not change what capture stores.

- Proof against the local S3 fake with real files: a captured quiet file gives `match`, exit 0;
  a flipped stored byte gives `mismatch`, exit 1; an appended file gives `behind`; an owner never
  captured gives `uncaptured`; a parked entry in `status.json` gives `parked`, exit 1; a deleted
  local file gives `remote_only`, exit 0; a deferred snapshot is counted apart; a store failure
  gives exit 3; no output line contains a path, a source id or file bytes; no PUT is sent.
- Teeth: compare only the restored length; `unsettled` counted as `match`; `behind` without the
  prefix check; a path printed; a PUT sent.

### RUN (parent)

1. Templates outside the repository's tracked code: plist, `config.toml`, Pi extension, hook
   command lines. Parse the config with `Config::parse` through the binary's refusal path.
2. Build the release binary from the accepted tip; copy it to a fixed path outside the repo.
3. State root `~/.local/state/memory-acceptance` (not under a session root); config with roots
   `~/.claude`, `~/.codex`, `~/.pi/agent`, prefix `rust-acceptance/<run-id>/`, a fresh archive id
   and machine id; token file 0600 built from `.env` in a subshell, never printed.
4. Install the hooks with backups; load the agent; keep `caffeinate`; start the RSS sampler.
5. Run `memory check` after the first reconcile settles, then twice a day. Abort triggers
   (A13.1): any `mismatch`, any owner parked as `Foreign`. The export-canary trigger cannot fire
   (answer 9).
6. End: two consecutive days with `mismatch`, `uncaptured`, `parked` all 0. Unload the agent,
   restore the hook files, record evidence 21, delete the prefix and list it empty (A2).

## Rules for workers

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply unchanged, with
those of brief 29: an existing test is removed only when the report names the test that now
carries the same assertion; no new dependency; no change to what `status.json` or `index.json`
store.

## Acceptance (parent)

As in brief 29: whole diff read, the seven checks, the mutation predicates, teeth per guarantee
on the intended assertion, `scripts/gates.sh` on the combined tip.

## Progress ledger

- 2026-11-17: brief written on `90cdbdf` with the operator's answers 4 to 10.
- 2026-11-17: PAUSE accepted locally as `f0cb744`, WALK as `80e2943`, CHECK as `09e763d`
  (seven checks 0 on each combined tip; 704 passed on `09e763d`). Full gates on `09e763d`: all 13
  sub-gates 0, 500 cases, seed 3302469678, coverage 96.97 %. Parent mutants, survivors and their
  fixes: [evidence 21](evidence/21-acceptance-run/). Changes made at review: CHECK tests live in
  `tests/s3_store/check.rs` (a module of the `s3_store` binary, which already holds the S3 fake),
  not `tests/check.rs`; `memory check` also exits 64 on a usage error; the uncaptured reason code
  is `no_live_entry`; a missing `status.json` reports `status=missing` and does not fail the
  check (the run reads it only after the daemon has written one). Next: push the stack, then RUN.
- 2026-11-17: stack #132 (PRs #127 to #131) merged as `a28bfa4`; one ARM gates job timed out at
  30 minutes on a docs-only branch, cause unknown, passed on rerun (evidence 21, open finding).
- 2026-11-17 05:30Z: RUN started as `20261117-a28bfa4` (launchd agent, hooks, sampler, check
  loop; details and teardown in evidence 21). First findings: peak memory 431 MB against 128 MB;
  the first reconcile takes hours at this host's upload rate and logs nothing until it ends.
  Next: after the first `round` line, read `checks.log`; two consecutive clean days end the run;
  then teardown, prefix deletion verified empty, and decision 2 on streaming.
- 2026-11-17 09:50Z (day 0): first round ended 07:07:10Z (1,074 landed, 1 offline then landed);
  first `memory check` clean (6,026 of 6,026 sources match; mismatch, uncaptured, parked 0). The
  19,015 `unknown_kind` files hold no transcript (census). Peak memory 1,254 MB, during the audit
  round right after the start round. Operator: Codex hooks trusted; **decision 2: streaming
  capture is required before the first production write** (DESIGN D45). Open findings: bursts of
  `cause=dir` full reconciles (one every 3 s at 09:40Z); hint delivery unverified until a new
  session starts. Day 1 counts from the first clean check (07:46Z): the run can end at the
  earliest after the check due about 2026-11-19T07:46Z if every check until then is clean.
- 2026-11-17 12:01Z (day 0, continued): healthy (211 rounds, no failure, park or audit mismatch
  since the first round). Pi hint delivery verified on a new Pi session; Claude and Codex
  unverified (no new session of either). The `cause=dir` storms come from lock directories
  (`*.json.lock`) that Claude Code makes and removes under `~/.claude/plugins/store/` on each
  turn; the daemon runs a full reconcile for each, where DESIGN 10.2 reads only the new
  directory. Goes to brief 31 (evidence 21, "Day 0, continued").
- 2026-11-17 20:10Z (check 2): second `memory check` clean (6,033 of 6,033 sources match;
  mismatch, uncaptured, parked 0). Daemon unchanged since the start; `offline` failures only, each
  landed within two minutes. Hints: Pi only; no new Claude or Codex session. Brief 31 (streaming)
  merged as `2e5f193`; this run keeps its `a28bfa4` binary. Next checks about 2026-11-18T08:09Z and
  20:35Z, then about 2026-11-19T09:00Z, after which the run can end (evidence 21, "Check 2").
