# 46. Slice 3, part 2: the `Ignored` verdict (2026-11-21)

Coordinator brief. The parent session owns this file and acceptance. It follows D54 and D55
([evidence 38](evidence/38-snapshot-scope/)), brief 45 and handoff items 20 and 21. Evidence goes to
`evidence/40-ignored-verdict/`. Text that states a plan stays as written; later changes are marked.

Production daemons run on this Mac (launchd) and on dev-01 (systemd), both on the `d5f6bab` build,
while this brief is worked on. Nothing here touches `~/.local/state/memory*`, `~/.codex`,
`~/.claude`, `~/.pi`, `~/Library`, the installed binaries, agent settings, `.env` or R2.

## Why

D54: known non-session files get an `Ignored` verdict, counted apart from `UnknownKind` and never
captured. Today `unknown_kind` mixes vendor code, caches and backups with files nobody has
classified, so the number says nothing: 19,192 files (1.01 GB) on this Mac and 48,198 files
(2.99 GB) on dev-01. D54 and D55 also drop Claude `skills/`, Claude `paste-cache/`, Pi
`pi-terminology/`, Codex `generated_images/` and Codex `attachments/`, and the review found that
the Codex `plugins/**` rule classes 297 MB of binaries as a config asset.

## Facts checked on `95f9511`

| Fact | Where |
|---|---|
| `Located` has three variants: `Source`, `UnknownKind`, `UnknownOwner`; `locate` maps `Miss::Kind` and `Miss::Owner` to the last two | `memory-core/src/locator.rs:318-346, 480-485, 941-970` |
| `withhold_reason()` maps the two unknown verdicts to `WithholdReason::UnknownKind`/`UnknownOwner`; `WithholdReason` has ten variants and is not in `rust/schemas/` | `locator.rs:338-346`, `memory-core/src/domain.rs:610-634` |
| The walk counts `UnknownKind`, `UnknownOwner` and non-store snapshots (`deferred_snapshot`) and yields only the rest | `memory/src/resolve.rs:483-515`, `Counts` at `resolve.rs:310-335` |
| Hints and directory reads use `locate` and keep only `Located::Source` | `resolve.rs:577-590, 655-690` |
| `memory status` and `memory check` print `unknown_kind=` and `unknown_owner=` from the walk counts | `memory/src/status.rs:58-198`, `memory/src/check.rs:106-170, 638` |
| Claude: `skills/**` is `ClaudeConfig` (`CLAUDE_DIRS`), `paste-cache/**` is `ClaudePaste`, `plugins/installed_plugins.json` is the only plugin asset | `locator.rs:526-533, 565-592` |
| Codex: `plugins/<sub>/**` with `sub != "cache"` is `CodexConfig` `Plugin`; `generated_images/` and `attachments/` match nothing; `logs_*.sqlite` is `UnknownKind` (brief 45); `*.sqlite-wal`/`-shm` match nothing | `locator.rs:692-735` |
| Pi agent dir: a multi-part path whose first part is not `sessions/` and not a config dir is `UnknownKind` (`PiMode::Strict`); a custom Pi root keeps every non-transcript file as sessionless `PiAux` | `locator.rs:765-905` |
| Every snapshot kind other than `CodexStore` is still deferred, never captured; so none of the paths this brief turns into `Ignored` is in any index or manifest | `resolve.rs:502-509` |

## Census (parent, 2026-11-21, names, counts and sizes only, no content read)

`unknown_kind` by root and first component, the largest rows:

| Path | This Mac files / MB | dev-01 files / MB |
|---|---|---|
| Pi `install/` | - | 30,640 / 228 |
| Pi `git/` | 9,677 / 145 | 9,816 / 146 |
| Codex `.tmp/` | 6,240 / 165 | 5,548 / 80 |
| Codex `plugins/` (cache and dot dirs) | 1,557 / 66 | 622 / 34 |
| Claude `plugins/` | 922 / 21 | 1,001 / 23 |
| Pi `pi-terminology/` | 307 / 42 | - |
| Codex `computer-use/` | 172 / 73 | - |
| Pi `npm/` | 121 / 1.4 | 121 / 1.4 |
| Codex `packages/` | 2 / 307 | 223 / 2,238 |
| Codex `cache/` | 13 / 97 | 6 / 101 |
| Codex top level (`logs_2.sqlite`, sidecars, locks, backups, caches) | 31 / 75 | 27 / 82 |
| Claude `telemetry/`, `backups/`, `cache/`, `shell-snapshots/`, `moshi-plugin/` | 63 / 8.6 | 22 / 3.8 |
| Codex `tui-thread-reference-capabilities/`, `app-server-*`, `*-locks/`, `tmp/`, `shell_snapshots/` | 38 / 0.3 | 39 / 0.1 |
| Codex `attachments/`, `generated_images/` | 11 / 8.6 | 1 / 0.0 |

Located snapshot kinds this brief changes: Claude `skills/` 900 files / 16.3 MB here and 458 /
8.4 MB on dev-01 (`ClaudeConfig`); Claude `paste-cache/` 59 and 26 (`ClaudePaste`); Codex
`plugins/.plugin-appserver/` 2 files / 297 MB here (`CodexConfig` `Plugin`).

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Verdict | `Located::Ignored`, a fourth unit variant, from a new `Miss::Ignored`. `withhold_reason()` returns `Some(WithholdReason::Ignored)` (new variant), so every caller that treats "not a source" as withheld keeps doing so. Ignored files are never yielded, never become hints, never reach admission |
| Counting | `Counts.ignored` in the walk; `ignored=<n>` printed by `memory status` and `memory check` right after `unknown_kind=`. `unknown_kind` keeps its meaning: files no rule knows |
| Scope of the rules | Agent-dir layouts only (`RootLayout::AgentDir`). A custom Pi root is a user-chosen session folder and keeps its current rules. The rules are checked only where today's result is `Miss::Kind` or one of the source kinds named below, so no path that is a session-owned source today can become `Ignored` |
| Claude (`~/.claude`) | Directories `skills/` (D54), `paste-cache/` (D55), `plugins/` except the top-level `installed_plugins.json` and `known_marketplaces.json`, `telemetry/`, `backups/`, `cache/`, `shell-snapshots/`, `moshi-plugin/`, `jobs/`, `state/`, `daemon/`, `downloads/`. Top-level files: `stats-cache.json`, `mcp-needs-auth-cache.json`, `.last-cleanup`, `.last-update-result.json`, and any `<known config file>.<suffix>` with a non-empty suffix (backups such as `settings.json.memory-backup-…`, `remote-settings.json.synced-at`). `known_marketplaces.json` becomes a `Plugin` config asset like `installed_plugins.json` |
| Codex (`~/.codex`) | Directories `.tmp/`, `tmp/`, `cache/`, `packages/`, `computer-use/`, `generated_images/` and `attachments/` (D55), `shell_snapshots/`, `tui-thread-reference-capabilities/`, any directory whose name starts with `app-server-` or ends with `-locks`. `plugins/`: `cache/**` and any dot directory (`plugins/.x/**`) are ignored; another file under `plugins/` stays a `Plugin` asset only if its extension is `json`, `toml` or `md`, otherwise ignored. Top-level files: `logs_*.sqlite` and the `-wal`, `-shm` and `-journal` sidecars of any `*.sqlite`, `*.sqlite3` or `*.db`; any `*.lock`; `models_cache.json`, `version.json`, `installation_id`, `chrome-native-hosts*.json`; any `<known config file>.<suffix>` backup (`hooks.json.memory-backup-…`) |
| Pi (`~/.pi/agent`) | Directories `git/`, `npm/`, `install/`, `pi-terminology/` (D54), `web-search-cache/`, any directory whose name starts with `pi-extensions-pre-` or `extensions-update-backup-`. Top-level files: any `*-debug.jsonl`, and any `<known config file>.<suffix>` backup (`settings.json.bak-…`, `settings.json.before-…`) |
| Left as `UnknownKind` | Everything else, on purpose, so the residual count stays a list to review: for example Codex `outputs/`, `tools/`, `sqlite/`, `browser/`, `automations/`, `.codex-global-state.json`; Pi `missions/`, `pi-peers/`, `machines-backups/`; Claude `sessions/`. Credential files keep their current verdict (the credential denylist covers them at admission) |
| Walk cost | The walk still descends into ignored directories and counts files. Pruning them is a later change with its own measurement |
| Size caps, content scan | Not here. Config snapshot capture (size cap per file) and file-history behind the content scan (D54) are later briefs |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | IGNORED | `rust/ignored-verdict` | worker (Sonnet, medium) |

Owns `memory-core/src/locator.rs`, `memory-core/src/domain.rs` (only `WithholdReason`),
`memory/src/{resolve,status,check}.rs`, `memory-core/tests/**`, `memory/tests/**`,
`rust/schemas/**` (only if the schema gate requires it; report it), `docs/install.md` (only if it
shows a status or check line). Read-only everywhere else.

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. `Located::Ignored`, `Miss::Ignored`, `WithholdReason::Ignored`, `withhold_reason()`; every
   `match` on `Located` handled. No rule yet. Tests: the new reason; existing tests unchanged.
2. The rules of the table above, as named constant tables plus small predicates in
   `locator.rs`, each with a doc comment saying why. Table tests: one row per rule (a path that is
   ignored), its near misses (a sibling name that is not), and for every agent the session sources
   that must not change (Claude transcripts, subagents, tool results, `file-history`,
   `projects/**/memory`; Codex rollouts plain and `.zst`, `history.jsonl`, `session_index.jsonl`,
   every non-`logs_` store; Pi sessions, tasks, forks, runs, config assets). A custom Pi root with
   the same paths is not ignored. The Codex `plugins/` narrowing with `json`/`toml`/`md` kept and a
   binary, a `.js` file and a dot-dir file ignored.
3. A property test (proptest, `PROPERTY_CASES`-driven like the others): for generated paths, a
   path that the rules ignore is never a session-owned `Source` under the rules of `95f9511`
   (keep a frozen copy of the old decision in the test only if no simpler oracle exists; say which
   you used), and `withhold_reason()` of `Ignored` is `Some(Ignored)`. Existing locator properties
   keep passing unchanged.
4. Walk, status and check: `Counts.ignored`, the walk never yields an ignored file, the hint and
   directory-read paths drop it; `ignored=` in the status and check lines. Tests on a temporary
   tree through `RealFs` (count, not yielded, a hint to an ignored path changes nothing) and the
   real-binary status and check lines.
5. Module docs.

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression.
- No `unsafe`, no new crate.
- Never touch `~/.local/state/memory*`, `~/.codex`, `~/.claude`, `~/.pi`, `~/Library`, the
  installed binary, `.env` or R2; do not list or read real agent directories. Use temporary
  directories. A production daemon runs on this Mac: do not inspect or signal it. No `git stash`.
  Never commit `.proptest-regressions`.
- No gate is loosened: no timeout raised, no test deleted or weakened, no coverage floor lowered.
  An existing test whose expectation changes because of a decision above is updated with a comment
  naming D54, D55 or this brief; list each one in the report.
- Report: commits, deviations with reasons, exact commands with test counts, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: an ignore rule that shadows a Claude transcript, a Codex rollout or a
Pi session; `Ignored` counted as `unknown_kind`; an ignored file yielded by the walk; a hint to an
ignored path captured; `logs_*` sidecars or a non-`logs_` store sidecar captured as a store; a
`plugins/` binary kept as an asset; the rules applied to a custom Pi root; `ignored=` missing from
`status`. Then the full gates once, the Linux container run of the locator and resolve tests, the
hosted gates, evidence 40, DESIGN (5, 8, D57), the handoff; merge; install on this Mac and dev-01;
rerun the census on both hosts and record `unknown_kind`, `ignored` and `deferred_snapshot` before
and after, and that the start round lands with `deleted=0`.

## Ledger

- 2026-11-21 03:00Z: brief written on `95f9511`.
