# 56. Analytics, part 3: Pi subagent run records (2026-11-24)

Coordinator brief. The parent session owns this file and acceptance. Evidence goes to
`evidence/51-pi-run-records/`. Text that states a plan stays as written; later changes are marked.

Production daemons run on this Mac and on dev-01, both on `df23437`. This brief changes
`memory export`, `memory owners` and the reader library (capture is unchanged), and the analytics
importer. The binary is installed on both hosts after merge.

## Why

Brief 54 (D66) keeps capturing Pi subagent run records
(`sessions/<cwd>/subagent-artifacts/<name>.json`) because they are the only record of a run's
resolved extensions and, for most runs, its requested model, exit code and acceptance. They are
stored under the shared owner `shared/<archive>/<machine>/pi`, and `memory export` refuses shared
owners (`owner_shared`), so analytics cannot read them yet.

Measured on dev-01 (counts, key paths and shapes only, parent, 2026-11-24): 59 run records (this
Mac has none). Every record has `runId` (str, contained in the file name), `agent` (str: 6
distinct agent-type names), `exitCode` (int, 0 or 1), `task` (free text), `timestamp` (int, ms).
57 have `model` and `requestedModel` (shape `provider/model[:level]`), `usage` (`input`, `output`,
`cacheRead`, `cacheWrite`, `turns` int; `cost` float or int), `launchResolvedExtensions`
(`version` 1, `source`, `configured`/`effective`/`runtime`/`required` lists of `sha256:<hex>`
digests, `omitted.{configured,effective,required,runtime}` int, `disableAmbientExtensions` bool),
`skills` (empty lists), `transcriptPath` (a path), and `acceptance` (`status` and
`evidenceStatus` from a closed set, `explicit` bool, `criteria[]`, `effectiveAcceptance.level`,
`reviewResult.status` in 14, `runtimeChecks[]` and a `childReport` with commands, changed files,
diff summary and notes in 39). 9 have `error` (free text). 2 have `runner` (`type`
`external-cli`, `command`, `args`) and `externalProcess` (`durationMs`, `exitCode`, pid, paths).

Links: all 59 runs are named by structured fields in captured Pi sessions: toolResult
`message.details.runId` (23), `message.details.mission.workflowChildren[].runId` (55),
`message.details.workflowChildren.children[].runId` (9), `message.details.pending[].runId` (6),
`subagent_supervisor_request` `details.runId` (16), `subagent_supervisor_reply` `data.runId` (16),
`subagent_control_notice` `details.event.runId` (14). The run's own child session is captured with
its parent session's owner (brief 54), so a run's usage is already in `session_usage`.

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| What a shared export holds | Only the analytics sources of the shared owner: a pure `memory_core::locator::shared_analytics(kind, path)`, true for `PiAux` whose path is the run-record shape (the same rule as `pi_run_record`). Every other stored entry (retired kinds) is left out and counted as `skipped`. Deleted sources are exported `sealed`, as for sessions; a source without epochs is left out |
| Contract | `metadata.json` `{"contract": "shared-reader/v1", "manifest_sha256", "manifest": {"contract": "shared-publication/v1", "identity": {"archive_id", "machine_id", "agent"}, "publication_id", "sequence", "captured_at_ns", "published_at_ns", "sources": [...]}, "bodies": [...]}`; a source is `{"source_id", "epoch_id", "kind": "pi_run_record", "path", "state", "revision", "captured_at_ns", "object"}` (`object` as in `session-reader/v2`), a body `{"source_id", "epoch_id", "path": "bodies/NNNN.bin", "sha256", "size"}`. Same directory contract as session exports (0700, `create_new`, fsync, `metadata.json` last, cleanup on failure). Bound: at most 16,384 sources (run records accumulate, about 60 a week here) and 8 MiB of metadata |
| `memory export` | Accepts a shared owner text and writes the shared contract; a session owner is unchanged. Success line `memory: exported sources=<n> deleted=<n> bytes=<n> skipped=<n>` for shared owners (session line unchanged). `ExportError::Shared` and `Failure::Shared` are removed or kept unused only if a test still needs them (report which) |
| `memory owners` | `memory owners --shared` lists `<prefix>shared/` (keys `shared/<a>/<m>/<agent>/current.json`, any other key `listing_unexpected_key`); without the flag unchanged |
| Importer | `publications.import_shared(export_dir, db_path, *, reset=False)` validates the shared contract as strictly as `_load` (closed keys, bounds, digests, body sha256 and size), binds the archive like `import_export`, and replaces one shared owner's rows in one transaction; `unchanged` when the owner's `publication_id` and `RUNS_VERSION` are unchanged. A session export given to it, or a shared export given to `import_export`, fails `unsupported_contract` |
| Table `pi_run` | One row per run record: `owner_key`, `archive_id`, `machine_id`, `source_id`, `run_id`, `timestamp_ms`, `agent_type`, `model`, `requested_model`, `exit_code`, `has_error` (BOOLEAN: `error` present and non-empty; the text is never stored), `acceptance_status`, `evidence_status`, `acceptance_level`, `acceptance_explicit`, `review_status`, `criteria` (count), `runtime_checks` (count), `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `cost_usd`, `turns`, `extensions_configured`, `extensions_effective`, `extensions_runtime`, `extensions_required` (list lengths), `extensions_omitted` (sum of `omitted`), `ambient_extensions_disabled`, `runner_type`, `external_duration_ms`, `external_exit_code`. Status-like strings must match `[a-z][a-z0-9_-]{0,39}`; `run_id` and `agent_type` the used-name rule of brief 55; `model` and `requested_model` `[A-Za-z0-9._:@/-]{1,100}` not starting with `/`, `.` or `~` and without `//`. Numbers as brief 55 (non-negative integers below 2^53, cost finite non-negative; exit codes signed). An invalid value is NULL and counted (`runs_skipped`); a record that is not a JSON object or has no valid `run_id` is not a row and is counted. Never stored: `task`, `error` text, `transcriptPath`, any `childReport` content, criteria text or evidence, commands, args, paths, pid, the file name |
| Table `pi_run_extension` | `owner_key`, `run_id`, `list` (closed: `configured`, `effective`, `runtime`, `required`), `digest` (must match `sha256:[0-9a-f]{64}`, else skipped and counted). Digests identify an extension build, not a name |
| Table `pi_run_ref` | Derived from Pi session bodies by the brief 55 mechanism (`usage_facts` or a new module, one version row; `SETUP_VERSION = 3`): session coordinates, `trace_id`, `line`, `timestamp`, `run_id`, `via` (closed: `tool_result`, `workflow_child`, `pending`, `supervisor_request`, `supervisor_reply`, `control_notice`) from exactly the structured fields listed under Links; never from free text. One row per (source, run_id, via) first sighting |
| View `pi_run_dim` | `pi_run` LEFT JOIN its referencing sessions on `archive_id`, `machine_id`, `run_id`: `session_key` of the earliest reference (NULL when none), `referencing_sessions` (count). A run's tokens are already in `session_usage` through its child session: `pi_run` usage is per-run detail and is never added to session totals |
| Report | `report(summary=True)` gains `runs`: totals (runs, linked to a session, with an error, exit code not 0), by `agent_type` (runs, non-zero exits, each acceptance status), by `model` and by `requested_model` (runs, top 20), requested ≠ resolved model (count), extensions effective per run (min, median, max), distinct effective digests. Empty when the tables are absent; read-only opens create nothing |
| Out of scope | Capture changes, other shared kinds, Claude or Codex shared owners (their shared export is valid and empty), price tables |

## Increments

| # | Increment | Branch | Base | Owner |
|---|---|---|---|---|
| 1 | SHARED: the shared export and `owners --shared` | `rust/shared-export` | brief | worker (Sonnet, medium) |
| 2 | RUNS: `import_shared`, the run tables, `pi_run_ref`, view, report | `analytics/pi-runs` | brief | worker (Sonnet, medium), in parallel with 1 |
| 3 | GATE: the consumer gate exports a shared Pi owner with the built binary and imports it | `analytics/pi-runs-gate` | 2 rebased on 1 | worker (Sonnet, medium) |

Ownership. 1: `rust/crates/memory-core/src/locator.rs` (the predicate only),
`rust/crates/memory-reader/src/export.rs`, `rust/crates/memory/src/{export,owners}.rs`, their
tests (`memory-core/tests/core_locator.rs`, `memory-reader/tests/export.rs`,
`memory/tests/s3_store/{export,owners}.rs`), `docs/hooks.md` if it documents the commands.
2: `tools/analytics/` (a new `pi_runs.py` and `test_pi_runs.py`, `publications.py`,
`setup_facts.py` for the version, `usage_facts.py` or a new module for `pi_run_ref`,
`README.md`). 3: `rust/crates/memory/tests/s3_store/consumer_gate.rs`,
`tools/analytics/test_rust_export_journey.py`. Read-only everywhere else.

Steps, one commit each, the increment's suite green before each:

1. SHARED: (a) predicate and tests; (b) `build_shared_envelope` and tests (limits, deleted,
   skipped, no epochs, digests, byte-exact metadata, a retired kind never exported); (c)
   `memory export` for shared owners, success line, tests through the S3 rig (a shared Pi owner
   with a run record and a retired Pi config entry: one source exported, `skipped=1`); (d)
   `owners --shared` and tests; (e) module docs.
2. RUNS: (a) `import_shared` with validation and tests (each closed-key and bound refusal, a
   session export refused, re-import `unchanged`, `--reset`); (b) `pi_run`, `pi_run_extension`
   extraction and tests (every column; every never-stored field as a canary that never appears in
   any table, view or report; invalid values NULL and counted); (c) `pi_run_ref`, version 3,
   re-derivation of a version-2 database, tests per `via`; (d) view and report section, tests
   including a read-only open of an older database; (e) README and module docs.
3. GATE: the gate's Pi fixture gains a run record and a parent reference; the binary exports the
   shared owner, `import_shared` imports it, `pi_run_dim` links it to the session, and the canary
   fields are absent.

## Rules for the worker

As brief 55 (pinned venv, synthetic fixtures, cargo only through the lock, fmt and both clippy
passes, Rust lines at most 100 columns, no real agent data or state, no stash or push, stop and
report on a wrong decision). Report: commits, deviations, changed expectations, exact commands
with test counts, `git status --short`.

## Acceptance (parent)

Diff read; teeth (the predicate widened to a retired kind, a retired entry exported, `skipped`
not counted, the shared bound or digest check removed, `owners --shared` accepting a session key,
a never-stored field leaking, an invalid status stored, a digest unchecked, a `via` path dropped,
the version not bumped, the view adding run usage to sessions, a read-only open creating a table);
Python suite; full Rust gates; Linux container; hosted CI. Merge, install on both hosts, then the
real-data run: `memory owners --shared`, export dev-01's shared Pi owner, import it with the
session exports into a scratch database (counts, a leak check, the link rate), compared with the
counts above. Evidence 51, DESIGN (D68), the handoff.

## Ledger

- 2026-11-24: brief written on `2ff1640`.
- 2026-11-24: workers done (SHARED 5 commits, RUNS 5, GATE 1); deviations accepted (evidence
  51). Teeth RUNS 26 of 26 and SHARED 11 of 13 (2 equivalent) after two parent tests; Python 213
  OK, gates 14/14, Linux passed. Accepted locally at `2bf71d9`. Install and the real-data run
  follow merge.
