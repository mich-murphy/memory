# Evidence 40: the `Ignored` verdict (brief 46)

All dates are UTC, 2026-11-21. Status: increment IGNORED accepted locally at `3b55337` (5 worker
commits over `b4cfe9f`). During the work nothing was installed, nothing was sent to R2, and nothing
read a production state root or real agent data. Both production daemons kept running.

[Brief 46](../../46-ignored-verdict.md) sits on `b4cfe9f`, on top of `main` `95f9511`. Reason:
D54 asks for an `Ignored` verdict that is counted apart from `UnknownKind`. D54 and D55 also drop
Claude `skills/` and `paste-cache/`, Pi `pi-terminology/`, and Codex `generated_images/` and
`attachments/`.

## Census before the change (parent, names, counts and sizes only)

The census tool walks with the production locator and prints only verdict, root, first
component, extension, file count and bytes.

| Host | `unknown_kind` | `deferred_snapshot` (located snapshot, not stores) |
|---|---|---|
| this Mac (`95f9511` locator) | 19,192 files / 1,014 MB | 3,121 files / 1,014 MB, including 5 stores |
| dev-01 (`d5f6bab` locator) | 48,198 files / 2,990 MB | 2,176 files / 293 MB, including 5 stores |

The brief lists the largest rows: Pi `install/` (30,640 files on dev-01), Pi `git/`, Codex `.tmp/`,
`plugins/` and `packages/` (2.24 GB on dev-01).

## Commits

| Commit | By | Change |
|---|---|---|
| `d2797e7` | worker | `Located::Ignored`, `Miss::Ignored`, `WithholdReason::Ignored`; `withhold_reason()` returns `Some(Ignored)` |
| `880a268` | worker | The ignore rules for agent-dir layouts as constant tables and predicates (`CLAUDE_IGNORED_*`, `CODEX_IGNORED_*`, `PI_IGNORED_*`, `backup_of`, `codex_plugin_ignored`). `apply_ignored` changes only a `Miss::Kind` or a `ClaudeConfig`/`ClaudePaste`/`CodexConfig` hit. Claude `known_marketplaces.json` becomes a `Plugin` asset |
| `186e1aa` | worker | Property in proptest and Hegel. An `Ignored` path never starts with a session root of its agent. A non-`logs_` `.sqlite`/`.sqlite3`/`.db` is never `Ignored`. A custom Pi root and the Claude primary home never give `Ignored` |
| `aa1f9ab` | worker | `Counts.ignored`. The walk counts and never yields an ignored file. `ignored=` follows `unknown_kind=` in the `memory status` and `memory check` lines |
| `3b55337` | worker | Module docs |

The oracle in step 3 is "never under a session root". It is simpler than a frozen copy of the old
locator. The guard in `apply_ignored` does the real protection, and the teeth below test it.

### Existing test expectations changed (each with a comment naming D54, D55 or brief 46)

- `resolve.rs` `snapshot_files_are_deferred_not_yielded_and_not_unknown`: `logs_2.sqlite` moves
  from `unknown_kind` 1 to 0.
- `core_locator.rs` matrix:
  - Claude `paste-cache/x` moves from `ClaudePaste` to `Ignored`, and the `skills` row leaves the
    `ClaudeConfig` rows.
  - Claude `plugins/cache/x`, `plugins/installed_plugins.json.bak` and `backups/.claude.json` move
    from `UnknownKind` to `Ignored`.
  - Codex `logs_2.sqlite`, `LOGS_1.SQLITE`, `state.sqlite-wal` and `plugins/cache/x` move from
    `UnknownKind` to `Ignored`.
  - Pi agent-dir `models.json.bak` moves from `UnknownKind` to `Ignored`.
- `every_kind_but_generated_snapshot_and_every_asset_is_reachable`: `claude.paste` is no longer
  reachable (D55), so the minimum kind count goes from 29 to 28.

`WithholdReason` is not in `rust/schemas/`. Nothing in the daemon, the state files or the reader
stores it (grep), so the new variant changes no persisted format.

## Parent acceptance

- Whole diff read.
- Two of the brief's teeth could not be built as written: "an ignored file yielded by the walk"
  and "a hint to an ignored path captured". `to_source` and `note()` keep only `Located::Source`,
  so either mutant changes nothing. Instead, three teeth remove the eligibility guard in
  `apply_ignored` and, in the same mutant, add a session root to the ignore list.
- Teeth, 15 of 15 caught (`ignored_teeth.py`, focused filter with a 96-test baseline):

  | Tooth | Result |
  |---|---|
  | Guard removed and Claude `projects` ignored | caught |
  | Guard removed and Codex `sessions`/`archived_sessions` ignored | caught |
  | Guard removed and Pi `sessions` ignored | caught |
  | Ignored counted as `unknown_kind` | caught (4 tests) |
  | `withhold_reason()` of `Ignored` is `UnknownKind` | caught (5) |
  | SQLite sidecars not ignored | caught (3) |
  | `logs_*.sqlite` not ignored | caught (4) |
  | A `plugins/` binary kept as an asset | caught (1) |
  | Rules applied to a custom Pi root | caught (4) |
  | `ignored=` missing from the status line | caught (1) |
  | `check` tally not filled | caught (1) |
  | `skills/` not ignored | caught (2) |
  | `paste-cache/` not ignored | caught (3) |
  | `known_marketplaces.json` ignored | caught (1) |
  | A backup with an empty suffix | caught (1) |

- Full gates on `3b55337`: 14 of 14, seed 270570902, nextest 851 passed, consumer 3, line
  coverage 95.47 %.
- Linux container (Ubuntu 24.04): `core_locator` 21, `resolve` 23, `realfs` 36, all passed.

## Limits

- The walk still descends into ignored directories. dev-01 enumerates about 30,000 Pi `install/`
  files each full round. Pruning needs its own measurement.
- Codex `outputs/` and `tools/`, Pi `missions/`, `pi-peers/` and `machines-backups/`, and Claude
  `sessions/` stay `UnknownKind` on purpose, as a list still to review.
- Snapshot capture of config assets, Claude aux and memory, and file-history behind the content
  scan are later briefs. Those kinds stay `deferred_snapshot`.
