# Analytics: the publication importer and report

Production workflow (see [install](../../docs/install.md)): `memory export` writes one
directory per owner, then `publications.py` imports it into a private DuckDB database and
reports on it. No R2, reader binary or network access is involved.

```sh
py=.analytics-venv/bin/python; db=.analytics/publications.duckdb
$py tools/analytics/publications.py import EXPORT --database $db         # session-reader/v2
$py tools/analytics/publications.py import-shared EXPORT --database $db  # shared-reader/v1
$py tools/analytics/publications.py report --database $db --summary
```

Setup: `uv venv --python python3 .analytics-venv`, then
`uv pip install --python .analytics-venv/bin/python -r tools/analytics/requirements.txt`
(pinned `duckdb`, installed with `--only-binary` in CI).

Imports validate the export strictly (closed keys, size caps, body sha256 and size), are
idempotent (`unchanged` on a repeat) and bind one archive per database. `--reset` clears it.
Modules: `publications.py` (import, report), `events.py` (record normalization and Codex
mirror pairing), `setup_facts.py`, `usage_facts.py` and `pi_runs.py` (derived tables). Wire
contracts: [publication](../../docs/session-publication-v1.md) and
[analytics](../../docs/session-analytics-v1.md) (the `memory export` reader).

Tests: `python -m unittest discover -s tools/analytics` (run in CI). The export journey,
`test_rust_export_journey.py`, runs through the Rust consumer gate (`RUST_EXPORT_JOURNEY`).
Fixtures are synthetic; never print transcript contents.

## Agent setup facts (brief 53)

Each import also derives `session_setup`, a long-format change log of the agent setup recorded in the
transcript at the time: `harness`, `harness_version`, `session_format` (Pi's `session.version`),
`entrypoint`, `originator`, `model`, `provider`, `reasoning_effort`, `permission_mode`,
`approval_policy`, `sandbox_policy`, `collaboration_mode`, available/invoked skills, subagent types,
tools, MCP servers, disabled plugins and `instruction_file`. The fact list is closed
(`setup_facts.FACTS`). One row is kept per first sighting of each `(fact, name, value)` within a
source.

Privacy: instruction files store only an allowlisted base name (`CLAUDE.md`, `CLAUDE.local.md`,
`AGENTS.md`, `AGENTS.override.md`, `GEMINI.md`, else `other`) and a closed scope (`project`, `user`,
`local`, `managed`, `other`). Never a path, directory, cwd or text. Names and values are strings of at
most 200 characters; anything longer, non-string or non-encodable is skipped and counted
(`setup_skipped` in the import result).

`session_setup_version(session_key, version)` records the extractor version (`SETUP_VERSION`). An import
of an unchanged publication whose recorded version differs re-derives that session's rows from the owned
bodies in one transaction, so an existing database fills without `--reset`. The view `session_dim` (one
row per session) is recreated on every writable open. Read-only opens (`report`)
create nothing and tolerate missing tables.

```sh
duckdb -readonly .analytics/publications.duckdb "SELECT agent, models, harness_version FROM session_dim"
```

## Usage, tool outcomes and the setup actually used (brief 55)

`SETUP_VERSION = 3` (brief 56 added `pi_run_ref`). The one `session_setup_version` row per session governs `session_setup`,
`session_usage` and `tool_outcome`: an unchanged publication recorded with an older version is
re-derived (all three tables) in one transaction, a new publication derives all three, and
`--reset` clears them.

- Used facts in `session_setup`: `skill_used` (Claude `attributionSkill`; Pi `read` of a
  `.../SKILL.md`, name = the parent directory's name, never the path), `plugin_used`,
  `mcp_server_used`, `extension_used` (Pi `customType` before the first `:`). A name outside
  `[A-Za-z0-9._:@-]{1,100}` is skipped and counted.
- `session_usage`: one row per billable unit (Claude first record per `message.id`; Codex one per
  change of `total_token_usage.total_tokens`, input = input - cached; Pi message and compaction
  usage, cost = `cost.total`). Missing is NULL, never 0; a value that is not a non-negative
  integer below 2^53 (cost: finite, non-negative) is NULL and counted.
- `tool_outcome`: `tool_call_id`, `tool_name`, `is_error`, `exit_code`, `duration_ms`. Never a
  command, arguments, output, cwd or a path.
- Views: `session_dim` gains token, cost, tool and used-fact columns; `usage_by_model` is new.
  Both are created on writable open only.
- Import result: `setup_skipped` (setup facts) and `usage_skipped` (usage and tool outcomes).
- `report(summary=True)` gains `setup` (top 20 each by sessions); `{}` on a database without the
  new tables. Read-only opens create nothing.

## Pi subagent run records (brief 56)

`publications.import_shared(EXPORT, DB, reset=False)` imports a `shared-reader/v1` /
`shared-publication/v1` export (`memory export` of a shared Pi owner), validated as strictly as a
session export (closed keys, at most 16,384 sources, 8 MiB of metadata, body sha256 and size
checked while reading, source `kind` = `pi_run_record`). It binds the archive like `import_export`
(`archive_mismatch`), replaces one owner's rows in one transaction, and returns `unchanged` when
the owner's `publication_id` and `pi_runs.RUNS_VERSION` are unchanged. A session export given to
it, or a shared export given to `import_export`, fails `unsupported_contract`. `--reset` clears
the run tables too. From the command line: `publications.py import-shared EXPORT --database DB`;
`publications.py report --database DB --summary` includes the `setup` and `runs` sections.

- `pi_run`: one row per run record (columns in brief 56). An invalid value is NULL and counted in
  `runs_skipped`; a record that is not a JSON object or lacks a valid `runId` is not a row and is
  counted. `pi_run_extension`: extension digests per list (`sha256:` and 16 to 64 lowercase hex
  characters only). `pi_run_owner`: one row per shared owner (publication, version).
- Never stored: task, error text, transcript path, child report content, criteria text or
  evidence, commands, arguments, paths, pid, the file name.
- `pi_run_ref` is derived from Pi session bodies under `SETUP_VERSION` (a version-2 database
  re-derives once), only from the structured fields: toolResult `details.runId` (`tool_result`),
  `details.mission.workflowChildren[]` and `details.workflowChildren.children[]`
  (`workflow_child`), `details.pending[]` (`pending`), and the custom messages
  `subagent_supervisor_request`, `subagent_supervisor_reply`, `subagent_control_notice`.
  One row per (source, run id, via) first sighting.
- View `pi_run_dim`: `pi_run` plus the earliest referencing session and `referencing_sessions`.
  Run usage is per-run detail and is never added to session totals (it is already in
  `session_usage` through the run's child session).
- `report(summary=True)` gains `runs`; `{}` when the tables are absent. Tables and views are
  created on writable opens only.
