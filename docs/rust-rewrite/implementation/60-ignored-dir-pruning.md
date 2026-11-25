# 60. The walk skips ignored directories; snapshot routing closed (2026-11-25)

Coordinator brief. The parent session owns this file and acceptance. Evidence goes to
`evidence/55-ignored-dirs/`.

## Why

Operator, 2026-11-25: "update 1, 2, 3, 4", where 4 was the two open findings: the walk descends
into ignored directories (evidence 40, Limits), and watcher events for snapshot sources are not
routed (evidence 41, Limits).

## Measurements (2026-11-25, counts only)

| Agent dir | This Mac: entries / under ignored top-level dirs | dev-01: entries / under ignored dirs |
|---|---|---|
| Claude | 16,443 / 2,605 | 6,776 / 2,372 |
| Codex | 11,438 / 9,127 | 9,594 / 8,327 |
| Pi | 15,747 / 10,867 | 47,413 / 44,171 |

A Python walk of each dir takes under 0.4 s; `memory status` peaks at 80 MiB here and 87 MiB on
dev-01. The walk stops at 500,000 entries (`WalkLimits::max_entries`), and a truncated walk is
incomplete, so absence (deletions, D52) is not observed in that round. Ignored directories are
vendor code and caches (Pi `npm/`, `install/`, `git/`; Codex `.tmp/`, `packages/`), which grow
with tools the operator installs, not with sessions.

Pi run records (`sessions/<cwd>/subagent-artifacts/<name>.json`, the only snapshot kind left):
0 on this Mac; 59 on dev-01, the newest 56 days old. No record was created in that time and Pi
does not delete them.

## Decisions

| Topic | Decision |
|---|---|
| Pruning | The walk does not enter a directory when every path under it is `Located::Ignored`. A pure locator predicate `dir_ignored(dir_parts, roots)` decides; it is true only where `locate` returns `Ignored` for every file below, whatever its name or depth |
| Scope of the predicate | `AgentDir` layouts only (where `apply_ignored` applies). Whole-directory rules only: Claude `CLAUDE_IGNORED_DIRS` at top level (not `plugins`, whose two top-level files are assets); Codex `CODEX_IGNORED_DIRS`, `app-server-*`, `*-locks`, `skills/.system`, `plugins/<x>/cache` and `plugins/<x>/.<dot>`; Pi `PI_IGNORED_DIRS` and the two prefixes. Anything less certain is walked |
| Proof | A property test: for generated directory parts with `dir_ignored` true and any generated tail, `locate` of the joined path is `Ignored`, per agent; and a table test naming each pruned rule. A session-owned or captured kind can never sit under a pruned directory |
| Counts | `ignored=` keeps counting files the walk located `Ignored`; a new `ignored_dirs=` counts directories not entered. `memory status` prints both (status line documented) |
| `DirCreated` | `read_dir` (`walk_under`) prunes the same way, and a directory event for a pruned directory or one below it reads nothing |
| Snapshot routing | No code. A Pi run record not routed by the watcher is captured by the next 15-minute reconcile; with no new record in 56 days and none deleted, nothing is lost. Recorded as closed |

## Increments

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | RUST: `dir_ignored`, pruning in `walk_dir`, counts, status line, tests | `rust/prune-ignored` | worker (Sonnet, medium) |
| 2 | Record (evidence 55, D72, handoff item 39, DESIGN 7.1/11.3 text), install on both hosts | `docs/b60-record` | parent |

## Ledger

- 2026-11-25: brief written on `9b97cb8`.
- 2026-11-25: accepted at `fac4846`: 5 of 5 teeth caught; gates 13 of 14 locally, the
  `fs_watcher_*` failure reproduced on `main` with `fseventsd` saturated (evidence 55).
