# 54. Capture only the live session record (2026-11-23)

Coordinator brief. The parent session owns this file and acceptance. Evidence goes to
`evidence/49-capture-reduction/`. Text that states a plan stays as written; later changes are
marked.

Production daemons run on this Mac (launchd) and on dev-01 (systemd), both on `94833f6`. This
brief changes the capture binary; the parent installs it on both hosts after acceptance.

## Why

Operator, 2026-11-22: "restoring an agents setup doesn't count for a use"; capture only what
analytics uses. Operator, 2026-11-23: once `memory` is installed it captures every session live,
so the historical and auxiliary files are not needed, "unless they provide information that is
not captured live and is useful for analysis purposes"; the proposals to drop were agreed, and the
open Pi files were to be confirmed by measurement. "You now know my reasoning for any future
questions."

Measurements (counts only, parent, 2026-11-23; evidence 48 and 49):

- Every agent still writes a transcript for new sessions. Codex: all 8 threads started in the last
  30 days and all 7 with items in that time have a rollout; the 826 threads older than 90 days
  exist only in Codex's databases (history, not live).
- Claude and Codex `history.jsonl`: each prompt is also in its session's transcript. Claude memory
  notes and plans are written with the normal file tools, so they appear in transcripts. Claude
  file-history holds the whole file before an edit; transcripts hold the edit (and in 364 of 737
  edits the whole original). Config files can only describe the newest setup; brief 53 reads the
  setup from transcripts.
- Claude subagent `*.meta.json` (940 here, 755 on dev-01): `agentType` and `description` equal
  the parent's Agent call in 772 of 772 found; `model` in 453 of 461; `effort` in 50 of 50 (also
  in the subagent transcript). Claude session-folder `*.txt` (6 here): every tool line is in the
  session's transcripts.
- Pi `subagent-artifacts` on dev-01: the 59 `.jsonl` run logs are copies (all 2,553 tool calls
  are in captured child sessions; 56 of 59 runs located as `<session>/<uuid>/<run>/session.jsonl`
  with model and usage; the other 3 are 2 empty failed runs and one log with 1,112 of 1,114 lines
  in sessions). The short `.md` files are subagent input templates (66 files, 9 distinct texts);
  the other `.md` outputs appear line for line in sessions. The 59 `.json` run records are written
  live and are the only record of the resolved extensions (configured, effective, omitted), and
  of the requested model, exit code and acceptance result for most runs (sessions carry them for
  13, 2 and 28 of 59). 0.24 MB in all.
- Codex `session_index.jsonl` holds only thread titles (67 here, 42 on dev-01); analytics does
  not use titles.

Walk census of located sources (files, MB):

| Kind | This Mac | dev-01 | Decision |
|---|---|---|---|
| `ClaudeTranscript` | 255, 592 | 131, 513 | keep |
| `ClaudeSubagent` | 837, 1,577 | 748, 965 | keep |
| `ClaudeToolResults` | 827, 77 | 1,152, 74 | keep (session content stored beside the transcript) |
| `CodexRollout` (incl. `.zst`) | 29, 18 | 138, 440 | keep |
| `PiSession` (main, tasks, forks) | 4,123, 2,206 | 2,225, 1,200 | keep |
| `PiAux` run records `sessions/<cwd>/subagent-artifacts/<name>.json` | 0 | 59, 0.24 | keep |
| `PiAux` other (outputs, run logs, inputs, markers) | 72, 0.6 | 244, 39 | drop |
| `ClaudeAux` (subagent meta, session `.txt`) | 946, 1.2 | 755, 0.2 | drop |
| `ClaudeFileHistory` | 946, 10.4 | 514, 9.1 | drop |
| `ClaudeMemory`, `ClaudePlan`, `ClaudeHistory`, `ClaudeConfig`, `ClaudeDebug` | 136, 1.8 | 59, 0.5 | drop |
| `CodexStore` | 5, 655 | 5, 222 | drop |
| `CodexHistory`, `CodexConfig`, `CodexLog` | 6, 1.0 | 7, 0.1 | drop |
| `PiConfig`, `PiSettings`, `PiModels` | 7, 0.1 | 6, 0.1 | drop |

Kinds with no file on either host today (`ClaudeTodos`, `ClaudeTasks`, `ClaudeTeams`,
`ClaudeSessionEnv`, `ClaudeUsage`, `ClaudePaste`, `CodexMemory`, `CodexCompressed` apart from
`.zst` rollouts, `PiLog`, `PiSnapshot`) follow the same rule: dropped unless listed as kept.

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Captured set | Exactly: `ClaudeTranscript`, `ClaudeSubagent`, `ClaudeToolResults`, `CodexRollout`, `CodexCompressed`, `PiSession`, and `PiAux` only when the root-relative path is `sessions/<cwd>/subagent-artifacts/<name>.json` (`<cwd>` passes `pi_cwd`, `<name>` a non-empty stem, exactly that depth). One allowlist constant in `locator.rs`; every other located kind becomes `Located::Ignored` in every layout (agent dir, Claude primary home, Pi custom root). D57's "only a miss or a config/paste snapshot can become `Ignored`" is replaced by this allowlist |
| Kinds stay | `SourceKind` keeps every variant: stored manifests name them and the reader parses them with `deny_unknown_fields`; restore and export of stored entries keep working |
| Already stored entries ("retired") | A stored live entry whose path now locates to `Ignored` is retired: the daemon never stat-probes it, never commits `Deleted` for it, and does not count it as `absence_skipped`; the manifest entry stays as it is (the archive never deletes). An owner whose walk yields nothing and whose live entries are all retired leaves the owner set as if `all_gone`. Implemented once, as a pure helper (id namespace selects the root, the stored path is located) shared by the daemon's absence probe (`Sources::locate_absent` gains `Probe::Retired`) and `memory check` |
| `memory check` | Stored live entries of a fetched owner that are retired are counted `retired=` instead of `remote_only=`; `retired` never changes the exit code. `remote_only` keeps its meaning and its brief 52 confirmation |
| `deferred_snapshot` | Its three kinds are now `Ignored`, so the counter, the special case in `WalkSources` and the field in the `memory status` and `memory check` lines are removed |
| Machinery | No capture code is deleted in this brief (Codex SQLite backup, the 6 h store interval, file-history scan, config scrubbing, credential denylist). Its tests keep running: a test that reached a retired kind through the locator is rewritten to build the `Source` below the locator (or to use a Pi run record where a snapshot of any kind will do). Report every function that has no production caller left; removal is a later decision |
| Local state | `index.json` records of retired sources are left as they are (they hold no path); the parent measures their count and size |
| Out of scope | Analytics of the run records (shared-owner export, brief 55), hook changes, R2 cleanup, the Go tree |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | RETIRE | `rust/capture-reduction` | worker (Sonnet, medium) |

Ownership: `rust/crates/memory-core/src/locator.rs`, `rust/crates/memory/src/{resolve.rs,
daemon.rs,check.rs,status.rs}`, and the tests under `rust/crates/memory-core/tests/` and
`rust/crates/memory/tests/` that change with them. Read-only everywhere else; if another file
must change, stop and report.

Steps (one commit each, the focused tests green before each):

1. (a) Locator: the allowlist and the run-record shape; `deferred_snapshot` removed from the walk
   and from `status`/`check`. Tests: every kept kind still located as before; every dropped kind
   in the census table (one path each, all three layouts where the layout can produce it) is
   `Ignored`; the run-record shape is kept and its near misses are `Ignored` (a `.json` one level
   deeper, a `.md` or `.jsonl` beside it, a marker file, a `.json` directly under `sessions/<cwd>/`);
   a credential-named file in a kept kind's place still withholds `credential`. Changed existing
   expectations carry a comment naming this brief.
2. (b) Retired entries: `Probe::Retired`, the owner-set rule, `check`'s `retired=`. Tests through
   the existing rigs: a stored file-history snapshot and a stored `history.jsonl` entry whose files
   still exist are never probed and never get `Deleted` over several full rounds and then a file
   removal; an owner holding only retired entries leaves the owner set; a stored kept source that
   vanishes is still deleted after two misses (D52 unchanged); `memory check` reports a retired
   entry as `retired=1`, `remote_only=0`, exit 0.
3. (c) Module docs (`locator.rs` ignore section, `resolve.rs`, `daemon.rs`, `check.rs` headers).

## Rules for the worker

- Cargo only through the shared lock (path in the prompt), one job at a time, one nextest `-E`
  expression; the nextest filter `not test(/hegel_/) and not test(/proptest_/) and not
  test(/consumer_gate_/)` for the broad run. Before reporting: `cargo fmt --check`, `cargo clippy
  --workspace --all-targets --locked -- -Dwarnings`, `cargo clippy --workspace --lib --bins
  --locked -- -Dwarnings`, the whole nextest run with that filter. Lines at most 100 columns; a
  `//!` comment stays at the top of its module.
- Never read real agent directories, `~/.local/state/memory*`, `.env`, R2 or the installed binary.
  Synthetic fixtures in temporary directories only.
- No gate is loosened, the coverage floor is not lowered, and no test is deleted to pass. If a
  decision looks wrong or cannot be implemented cleanly, stop and report. Do not edit the brief,
  DESIGN.md or the handoff. No `git stash`, no push, no `scripts/gates.sh`.
- Report: commits, deviations, changed expectations (file and test), functions left without a
  production caller, exact commands with test counts, `git status --short`.

## Acceptance (parent)

Read the diff. Teeth: a dropped kind left captured (per agent), the run-record shape widened (one
level deeper, any extension) or dropped, a kept kind ignored, `Probe::Retired` treated as `Skip`
or as a handle, the owner-set rule removed, `retired` counted as `remote_only`, `retired` changing
the exit code. Then full gates, the Linux container, hosted CI, install on both hosts, and
measurement: `memory status` (sources, `ignored`), the start round (no `Deleted` for a retired
entry, no WARN/ERROR), peak memory, `memory check` (exit 0, `retired=` count, `remote_only`
unchanged at 0) and the `index.json` records of retired sources. Evidence 49, DESIGN (R1, section
8, D66), the handoff.

## Ledger

- 2026-11-23: brief written on `a6a7755`.
- 2026-11-23: first worker stopped twice at its turn limit (locator change and most locator
  tests uncommitted); the parent accepted that diff and decided the rig tests move to kept kinds
  (the rig runs a real walk; no test-only production path) and the five Codex store rig tests
  become one "ignored, nothing lands" test. Second worker finished in 3 commits; deviation
  accepted: `claude_external` ignores a dropped kind before the owner lookup. Teeth 18 of 19
  (1 equivalent; parent test `9566497`), property tests 69 of 69, gates 14/14, Linux passed.
  Accepted locally at `9566497`. Details in evidence 49.
