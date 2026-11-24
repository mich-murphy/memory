# Evidence 50: usage, tool outcomes and the setup actually used (brief 55)

All dates are UTC. Status: increment USAGE accepted locally at `6b66e77` on 2026-11-24. That is 7
commits (6 by the worker, 1 by the parent) over the brief `a5bb169`, on `main` `0636e20`. Only the
analytics importer (`tools/analytics/`) and the consumer-gate test changed; the capture binary is
unchanged and nothing was installed. The worker read no real agent data and no production state.

[Brief 55](../../55-usage-and-used.md) records the measured field shapes behind the decisions.

## Commits

| Commit | By | Change |
|---|---|---|
| `f774810` | worker | Used facts `skill_used`, `plugin_used`, `mcp_server_used`, `extension_used` in `setup_facts.py`; `SETUP_VERSION = 2`; one version row re-derives every derived table |
| `d7811d1` | worker | `usage_facts.py`: `session_usage` and `tool_outcome`, their extraction per agent, `session_dim` columns and the `usage_by_model` view |
| `014dd51` | worker | `report(summary=True)` `setup` section |
| `627e02e` | worker | 12 tests with synthetic fixtures (`test_usage_facts.py`) and the used-fact test |
| `233f9c8` | worker | Consumer gate: fixtures gain usage and tool-result fields; `test_rust_export_journey.py` asserts rows end to end |
| `37b357f` | worker | README and module docs |
| `6b66e77` | parent | Used names may hold single internal spaces (real-data run, below) |

Deviations, accepted:

- The setup part of the old `session_dim` view is now `session_setup_dim`
  (`setup_facts.ensure`); `usage_facts.ensure` builds `session_dim` from it with the new columns.
- One counter, `usage_skipped`, covers usage and tool-outcome skips; `setup_skipped` is unchanged.
- A Pi `bashExecution` (a user `!` command) is a `tool_outcome` row with `tool_name='bash'`, its
  exit code and no call id.
- Claude duration uses `durationMs` when valid, else `durationSeconds` × 1000 rounded. A Claude
  record is an outcome row only when it holds exactly one `tool_result` block.
- A usage row with no valid value, or an outcome row with no outcome field, is not written.
- The report's `tool_results` group also carries `known` (rows with `is_error` set) and
  `error_share` (errors / known).
- `test_rust_export_journey.py` (outside the ownership list) gained the end-to-end assertions;
  the gate needs them.
- Changed existing expectations: `test_closed_fact_list` 21 → 25 facts; the consumer gate's Codex
  `events` 7 → 8 because the fixture gained a `token_count` record (both commented). Five fixture
  lines added or edited in `consumer_gate.rs` are raw JSON strings over 100 columns, as 16 were
  before; rustfmt cannot wrap them.

## Real data (this Mac, counts only)

The 1,647 exports from brief 53 (installed `94833f6`, same owners) were imported with the brief 55
importer into a fresh scratch database: 1,647 imported, no failure; a second import gave 1,647
`unchanged`; every session has version 2; no unknown fact. 26 min for the import.

The first run, on the worker tip `37b357f`, skipped 1,959 setup values. A counts-only census
showed all of them were Claude `mcp_server_used` names with a space: 5 claude.ai connector names
shaped `a.a a` and `a.a a a`, 1,819 of the 1,959 uses equal to a name brief 53 already stores as
`mcp_server_available`. The rule `[A-Za-z0-9._:@-]{1,100}` dropped real usage without protecting
anything, so `6b66e77` allows words of those characters joined by single spaces (at most 100
characters; leading, doubled or tab spaces still rejected). After it: `setup_skipped` 0,
`usage_skipped` 0, and Claude `mcp_server_used` rose from 136 rows in 64 sessions to 186 in 79.

Used facts (rows, sessions):

| | Claude | Codex | Pi |
|---|---|---|---|
| `skill_used` | 381, 153 | — | 2,302, 558 |
| `plugin_used` | 15, 6 | 4, 4 | — |
| `mcp_server_used` | 186, 79 | 86, 63 | — |
| `extension_used` | — | — | 2,117, 376 |

Usage (`session_usage`):

| | Claude | Codex | Pi |
|---|---|---|---|
| Rows | 118,978 | 11,779 | 242,130 |
| Sessions with usage | 371 of 620 | 159 of 167 | 858 of 860 |
| Uncached input tokens | 478,065 | 27,104,649 | 656,543,547 |
| Cache read | 21,425,804,552 | 1,496,618,112 | 22,971,623,275 |
| Cache write | 434,808,935 | 0 | 714,872,067 |
| Output | 53,658,539 | 4,366,087 | 231,293,554 |
| Reasoning | 17,402,386 | 1,927,158 | 43,893,883 |
| Cost (USD) | none recorded | none recorded | 7,083.61 |

No row lacks output tokens or a model. Claude sessions with usage equal the Claude sessions with a
model (brief 53); the others hold only artifacts. `usage_by_model` has 49 rows.

Tool outcomes (`tool_outcome`):

| | Claude | Codex | Pi |
|---|---|---|---|
| Rows | 106,964 | 14,166 | 302,934 |
| Sessions | 301 | 138 | 813 |
| With a call id | 106,964 | 6,083 (MCP) | 302,916 |
| `is_error` known, errors | 102,352, 3,926 | 0 (not recorded) | 302,916, 14,929 |
| Exit codes | 0 | 8,083 | 18 (`bash`) |
| Durations | 4,612 | 14,166 | 247,891 |

Codex items carry a status, not an error flag, so `is_error` stays NULL (brief: NULL, never
guessed). Claude records a duration only on some tool results.

The summary report's `setup` section returned every group (harness versions 20, models 20,
reasoning efforts 14, permissions 7, usage by model 20, skills 20, plugins 2, MCP servers 20,
extensions 20, tool results 3).

Leak check: no used name holds `/`, `\` or a leading `~`; the longest is 42 characters. No tool
name holds a slash or a space; the longest is 52. The home path appears in no stored setup name or
value, tool name, call id or model.

## Parent acceptance

- Whole diff read.
- Teeth (`b55_teeth.py`, `test_usage_facts` and `test_setup_facts`, baseline 29 tests): 28 of 28
  caught on the worker tip, and the used-name tooth again after `6b66e77`:
  - usage: Claude or Codex dedup removed, the Codex cached subtraction removed, NULL turned into
    0, Claude output or reasoning dropped, Codex reasoning dropped, Pi cost or compaction dropped,
    the integer bound removed, a skip not counted;
  - outcomes: Pi duration dropped, the Claude tool name or seconds fallback dropped, the Codex
    exit code or duration dropped, the Codex command stored as the tool name;
  - used facts: Claude skill or plugin dropped, Codex MCP server dropped, the Pi `SKILL.md` read
    dropped, the extension prefix kept whole, the name rule removed;
  - the version not bumped, re-derivation deleting only setup rows, usage not written, the
    old-database guard of the report removed, a report group missing.
- Python suite on `6b66e77`: 188 tests, OK (28 environment-dependent skips).
- Full Rust gates on `37b357f`: 14 of 14, seed 632191717, line coverage 95.21 %. `6b66e77`
  changes only Python; the consumer gate was rerun on it (3 passed).
