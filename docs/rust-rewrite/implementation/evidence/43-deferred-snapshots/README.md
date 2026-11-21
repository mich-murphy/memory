# Evidence 43: the remaining snapshot kinds behind the fail-closed scan (brief 49)

All dates are UTC, 2026-11-21. Status: increment SNAPKINDS accepted locally at `b9ef539`. That is
5 worker commits and 1 parent commit over `aef3912`. During the work nothing was installed,
nothing was sent to R2, and the worker read no production state root or real agent data. Both
production daemons kept running.

[Brief 49](../../49-deferred-snapshots.md) sits on `aef3912`, on top of `main` `9884ac2`.

## Probe before the brief (counts only)

A scratch crate ran the real `admit` and `transform` in memory over every deferred snapshot file
on both hosts. It judged each file under its profile at the time and under `Profile::Scanned`, and
printed only (kind, extension, outcome) counts. No name, path or content was printed.

| Host | Deferred files judged | Withheld under the profile at the time | Withheld under `Scanned` |
|---|---|---|---|
| This Mac (`9884ac2`) | 1,211 | 0 | 16: 2 Claude aux `txt`, 3 Claude memory `md`, 3 Pi aux `md`, 1 Pi extension `ts` (all `SecretInText`); in Codex `skills/.system/` 3 `py` (`SecretInText`) and 4 `png` (`Binary`) |
| dev-01 (`87795f3`) | 1,174 | 0 | 53: 4 Claude memory `md`, 1 Pi aux `md`, 1 Pi extension `ts` (`SecretInText`); 40 Pi `subagent-artifacts/*.jsonl`, 30 `SecretInText` and 10 over 1 MiB (`SourceLimit`); Codex `skills/.system/` as on the Mac (7) |

The first draft of this table and of brief 49 summed the rows wrongly (1,270 and 19 here, 1,200
and 88 on dev-01; 71 of 1,072 `OtherKnown` files on dev-01). The totals above were recounted from
the probe output before the record was committed.

The 59 Pi `subagent-artifacts/*.jsonl` files on dev-01 (40 MB) are run logs: none equals a captured
transcript by SHA-256 (in-memory comparison), none starts with a `session` header line, all are
older than 7 days, the largest is 2.3 MB.

## Commits

| Commit | By | Change |
|---|---|---|
| `0e6e65e` | worker | `codex_ignored`: `skills/.system/**` is `Ignored` in a Codex agent dir; user skills and the near-misses `.systemx/` and a bare `.system` file stay `CodexConfig`; a custom root is unaffected |
| `f78b719` | worker | `profile_of`: every snapshot kind but `CodexStore` is `Scanned` (config assets, `ClaudeMemory`, `CodexMemory`, `ClaudePlan`, `ClaudeFileHistory`, todos, tasks, teams, `ClaudeAux`, `PiAux`, `PiSnapshot`); append kinds unchanged. The walk defers only `ClaudeSessionEnv`, `ClaudeUsage` and `ClaudePaste` |
| `e663498` | worker | Capture tests: memory note, session aux and `settings.json` land; a memory note with a secret is withheld `secret_in_text` and not read again; a `png` asset is withheld `binary`; a 2 MiB Pi aux file is withheld `source_limit` without a read; `settings.json` with a token field lands scrubbed (one piece holds the document, so the plaintext search is meaningful) |
| `eb7e005` | worker | Real binary against the fake S3 store (`tests/s3_store/snapshot_kinds.rs`): one reconcile lands the session and the shared Claude owner; `check` shows `uncaptured=0 mismatch=0 deferred_snapshot=0`; `restore` of both owners gives equal bytes; `export` exits 0 |
| `ebdc887` | worker | Module docs |
| `b9ef539` | parent finding | The tooth "Pi aux back to `OtherKnown`" survived: no test walked a Pi file. The profile test now walks a Pi session aux file (`Scanned`) and the Pi transcript beside it (`Transcript`) |

Changed existing test expectations, each with a comment naming brief 49:
`resolve.rs::shared_owner_and_profiles_are_literal` (plan and `settings.json` are `Scanned`),
`resolve.rs::snapshot_files_are_deferred_not_yielded_and_not_unknown` (a session-env file added;
settings, `config.toml` and Pi settings are yielded),
`resolve.rs::of_over_every_owner_equals_enumerate_on_a_mixed_tree` (6 walked, 1 deferred), and
`s3_store/check.rs::a_deferred_snapshot_is_counted_apart` (uses a session-env file).

`memory-core/src/privacy` is unchanged: `rules_digest` and `TRANSFORM_VERSION` are the same, so no
captured source is re-judged by a rules change. `Profile::Config` and `Profile::TextAsset` remain
in `memory-core` but no located kind maps to them any more.

## Parent acceptance

- Whole diff read.
- Teeth, 12 of 12 caught (`snapkinds_teeth.py`, focused filter with a 210-test baseline):

  | Tooth | Result |
  |---|---|
  | Claude memory left deferred | caught (2) |
  | Claude aux left deferred | caught (2) |
  | Claude config back to `Config` | caught (2) |
  | Claude memory back to `TextAsset` | caught (1) |
  | Claude aux back to `OtherKnown` | caught (1) |
  | Pi aux back to `OtherKnown` | survived at first; caught (1) after `b9ef539` |
  | Tool results (append) moved to `Scanned` | caught (1) |
  | Claude history (append) moved to `Scanned` | caught (1) |
  | Session-env yielded | caught (3) |
  | Codex `.system` skills captured | caught (1) |
  | A user Codex skill ignored | caught (2) |
  | Scrub skipped for a `Scanned` config document | caught (1) |

- Full gates on `b9ef539`: 14 of 14, seed 1051600503, nextest 887 passed, consumer 3, line
  coverage 95.58 %.
- Linux container (Ubuntu 24.04): `memory-core` `core_locator` 22, `memory` `capture` 40,
  `daemon` 95, `resolve` 37, all passed.

## Limits

- The scanner's code-shaped false positives (evidence 41) now also withhold some memory notes, aux
  files and config assets. The operator question stays open.
- Config documents that `transform` recognizes are scrubbed, then scanned whole. A config file
  whose scrubbed form still holds a secret-named literal is withheld, not published partly.
- Session-env, usage-data and paste-cache (custom roots) stay `deferred_snapshot`.
- Watcher events still route append sources only; these kinds are captured by full rounds and by
  owner captures that walk them.
