# 55. Analytics, part 2: usage, tool outcomes and the setup actually used (2026-11-24)

Coordinator brief. The parent session owns this file and acceptance. Evidence goes to
`evidence/50-usage-and-used/`. Text that states a plan stays as written; later changes are marked.

Production daemons run on this Mac and on dev-01, both on `df23437`. This brief changes only the
analytics importer (`tools/analytics/`) and its tests; the capture binary is unchanged and nothing
is installed.

## Why

Operator, 2026-11-22: analytics should have "details of the agent model, harness, extensions,
skills, tool calls etc."; 2026-11-24: "finish off the work". Brief 53 (D65) records the setup that
was available. This brief records what it cost and what was used, and adds a report section.
The Pi subagent run records (shared owner, needs an export change) are brief 56.

Measured on this Mac (counts only, parent, 2026-11-24, newest 300 files per agent):

- Usage. Claude `assistant.message.usage` (24,779 records) with `input_tokens`,
  `cache_read_input_tokens`, `cache_creation_input_tokens`, `output_tokens`,
  `output_tokens_details.thinking_tokens`; no cost. Records sharing a `message.id` repeat the same
  usage (18,589 repeats, all identical): one row per `message.id` per source. Codex
  `event_msg/token_count` `payload.info.{last,total}_token_usage` with `input_tokens`,
  `cached_input_tokens`, `cache_write_input_tokens`, `output_tokens`, `reasoning_output_tokens`,
  `total_tokens`; events repeat (28 repeats in 27 rollouts); summing `last_token_usage` once per
  change of `total_token_usage.total_tokens` equals the final total in 27 of 27 rollouts, while
  `token_usage_record.turn_token_usage` sums match in only 13 of 27 (not used). Pi
  `message.usage` on assistant messages (16,332) with `input`, `output`, `cacheRead`,
  `cacheWrite`, `reasoning`, `totalTokens` and `cost.{input,output,cacheRead,cacheWrite,total}`
  (int or float), plus `compaction.usage` (49).
- Tool outcomes. Pi `toolResult` `toolCallId`, `toolName`, `isError`, `durationMs` (16,517 with a
  duration); `bashExecution.exitCode`. Claude user records with `toolUseResult.durationMs` or
  `durationSeconds` and one `tool_result` block (`tool_use_id`, `is_error`). Codex
  `event_msg/item_completed` items: `CommandExecution` (`exit_code`, `duration{secs,nanos}`),
  `McpToolCall` (`server`, `tool`, `pluginId`, `duration`); `item.id` equals a `call_id` for MCP
  calls (33) but not for command executions (281).
- Used. Claude `assistant.attributionSkill` (1,484), `attributionPlugin` (64),
  `attributionMcpServer` (54). Pi `read` tool calls whose `path` ends in `SKILL.md` (53; also 2 in
  `bash` commands), `customType` (`subagents:record`, `subagent-notification`, ...). Codex
  `McpToolCall.server`, `pluginId`, `metadata.mcp_attribution.sources[].server_name`.

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Version | `SETUP_VERSION = 2`. `session_setup_version` now governs every derived side table of this brief; an unchanged publication with version 1 is re-derived (setup, usage, tool outcomes) in one transaction, as brief 53 |
| Used facts | Added to the closed list of `session_setup`: `skill_used` (Claude `attributionSkill`; Pi a `read` whose `path` ends in `/SKILL.md`, name = the parent directory's name; the existing `skill_invoked` stays as it is), `plugin_used` (Claude `attributionPlugin`; Codex `item.pluginId`), `mcp_server_used` (Claude `attributionMcpServer`; Codex `McpToolCall.server` and `mcp_attribution.sources[].server_name`), `extension_used` (Pi `customType`, the part before the first `:`). Same change-only rule, bounds and path guard as brief 53; a name that is not identifier-like (`[A-Za-z0-9._:@-]`, at most 100 characters) is skipped and counted |
| Table `session_usage` | One row per billable unit: coordinates, `trace_id`, `line`, `timestamp`, `model` (the forward-filled model of that source, as `events`), `input_tokens` (uncached input), `cache_read_tokens`, `cache_write_tokens`, `output_tokens`, `reasoning_tokens`, `cost_usd` (DOUBLE). Claude: first assistant record per `message.id` in a source; `input_tokens` = `input_tokens`, cache read/write from `cache_read_input_tokens`/`cache_creation_input_tokens`, reasoning from `output_tokens_details.thinking_tokens`, no cost. Codex: one row per `token_count` whose `total_token_usage.total_tokens` differs from the previous one, from `last_token_usage`; `input_tokens` = `input_tokens - cached_input_tokens` (negative: skipped and counted), cache read = `cached_input_tokens`, cache write = `cache_write_input_tokens`, reasoning = `reasoning_output_tokens`. Pi: assistant `message.usage` and `compaction.usage`; cost = `cost.total` (int or float). A missing field is NULL, never 0; a value that is not a non-negative integer below 2^53 (cost: finite, non-negative) makes that column NULL and counts a skip |
| Table `tool_outcome` | One row per tool result that carries an outcome: coordinates, `trace_id`, `line`, `timestamp`, `tool_call_id` (NULL when the native id is not a call id: Codex command executions), `tool_name` (Pi `toolName`; Claude the name of the matching `tool_use` in the same source, else NULL; Codex the item type `CommandExecution` or `McpToolCall`), `is_error` (BOOLEAN or NULL), `exit_code` (BIGINT or NULL), `duration_ms` (BIGINT or NULL; Claude `durationSeconds` × 1000 rounded; Codex secs × 1000 + nanos / 10^6). Never the command, arguments, output, cwd or a path |
| View | `session_dim` gains, by LEFT JOIN on session, `input_tokens`, `cache_read_tokens`, `cache_write_tokens`, `output_tokens`, `reasoning_tokens` (sums, NULL when no row has the column), `cost_usd` (sum, NULL when none), `tool_results`, `tool_errors`, `tool_duration_ms`, `skills_used`, `plugins_used`, `mcp_servers_used`, `extensions_used` (distinct counts). New view `usage_by_model` (agent, model, sessions, the token sums, cost) |
| Report | `report(summary=True)` gains `setup` (closed shapes, top 20 each by sessions): sessions by (agent, harness_version), by (agent, model), by (agent, reasoning_effort), by (agent, permission_mode or approval_policy), tokens and cost by (agent, model), top skills, plugins, MCP servers and extensions used, tool results and error share by agent. Names come only from the stored, already bounded facts. Read-only opens of a version-1 database without the new tables still work and report `setup` as empty |
| Out of scope | Pi subagent run records (brief 56), capture changes, the legacy `report.sql`/dashboard, cost estimation from price tables |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | USAGE | `analytics/usage-and-used` | worker (Sonnet, medium) |

Ownership: `tools/analytics/setup_facts.py`, a new `tools/analytics/usage_facts.py`, a new
`tools/analytics/test_usage_facts.py`, `tools/analytics/test_setup_facts.py`,
`tools/analytics/publications.py`, `tools/analytics/README.md`, and
`rust/crates/memory/tests/s3_store/consumer_gate.rs` (fixtures gain usage and tool-outcome fields;
the Python check asserts rows end to end). Read-only everywhere else.

Steps (one commit each, the whole Python suite green before each):

1. (a) Used facts in `setup_facts.py`, the version bump, re-derivation of all derived tables.
2. (b) `usage_facts.py`: the two tables, their extraction, the view changes and `usage_by_model`.
3. (c) `report(summary=True)` `setup` section.
4. (d) Tests (synthetic fixtures only): per agent every field above through `import_export`, read
   back by SQL; Claude repeated `message.id` counted once; Codex repeated `token_count` counted
   once and session total equals the final `total_token_usage`; absent fields NULL not 0; a
   negative, float, boolean or huge token value skipped and counted; a canary in tool arguments,
   output, command, cwd and a SKILL.md path never appears in `session_usage`, `tool_outcome`,
   `session_setup`, the views or the report except the skill's directory name; a version-1
   database re-derives on the next import of the same publication and a second import changes
   nothing; a read-only `report()` on a version-1 database creates nothing; the consumer gate
   asserts at least one `session_usage` row per agent and one `tool_outcome` row for Claude and Pi.
5. (e) README and module docs.

## Rules for the worker

As brief 53 (pinned venv, synthetic fixtures, cargo only through the lock for the consumer gate,
fmt and both clippy passes for the Rust file, no real agent data or state, no stash or push, stop
and report on a wrong decision). Report: commits, deviations, changed expectations, exact commands
with test counts, `git status --short`.

## Acceptance (parent)

Diff read; teeth (a dropped field per agent, dedup removed for Claude or Codex, NULL turned into 0,
the Codex cached subtraction removed, a canary leak, the version not bumped, re-derivation skipped,
a read-only open creating a table, the report section missing a group); Python suite; consumer
gate; full Rust gates; hosted CI; real-data run of every export on this Mac into a scratch
database (counts, sums and a leak check only), compared with the counts above. Evidence 50, DESIGN
(D67), the handoff.

## Ledger

- 2026-11-24: brief written on `0636e20`.
