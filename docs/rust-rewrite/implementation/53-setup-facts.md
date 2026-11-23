# 53. Analytics, part 1: the agent setup of each session, from its transcript (2026-11-23)

Coordinator brief. The parent session owns this file and acceptance. Evidence goes to
`evidence/48-setup-facts/`. Text that states a plan stays as written; later changes are marked.

Production daemons run on this Mac (launchd) and on dev-01 (systemd), both on `94833f6`. This
brief changes only the analytics importer (`tools/analytics/`) and its tests; the capture binary
is unchanged and nothing is installed.

## Why

Operator, 2026-11-22: "restoring an agents setup doesn't count for a use, but we should have
details of the agent model, harness, extensions, skills, tool calls etc. for analytical purposes."
Operator, 2026-11-23: agreed the plan (capture reduction, setup facts, used facts, usage columns,
Codex `thread_history`, a report section) and the instruction-file rule below.

Research (parent scratch, report revision 2, prototype of 309 lines, 3 tests passing):

- The importer keeps tool calls, a per-file model and the message text. Control records (Claude
  `attachment` and `permission-mode`, Codex `session_meta` and `turn_context`, Pi `session`,
  `model_change` and `thinking_level_change`) become `system` events with text only; their
  structured fields are dropped.
- Those records carry the setup at the time the session ran. Config files cannot: a snapshot
  keeps only its newest version (DESIGN 6.4), so they cannot describe a past session.
- DDIA ch. 3 (stars and snowflakes): events are the fact table, the setup is a dimension; a
  dimension is derived data and is rebuilt, not patched. Schema-on-read for the transcripts
  (tolerant extraction), schema-on-write for the projection. DDIA ch. 5: additive change; newer
  readers re-derive from the immutable bodies.
- Counts on this Mac (records): Claude `skill_listing` 1,114, `mcp_instructions_delta` 792,
  `instructions` 673, `permission-mode` 5,750; Codex `turn_context` 104 of 104 with `model`;
  Pi `model_change` 5,130, `thinking_level_change` 4,711. Pi's `session.version` is the session
  format version (3), not the Pi release.
- Claude attachment shapes are not documented in the fetched docs; they are observed only and
  can change between versions, so extraction must be tolerant and report what it skipped.

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Table | `session_setup`, long format: the coordinate columns of `events` plus `trace_id`, `line`, `timestamp`, `fact`, `name`, `value`. One row per first sighting and per change of `(fact, name, value)` within a source; a repeat is skipped |
| Closed facts | `harness`, `harness_version`, `session_format`, `entrypoint`, `originator`, `model`, `provider`, `reasoning_effort`, `permission_mode`, `approval_policy`, `sandbox_policy`, `collaboration_mode`, `skill_available`, `skill_invoked`, `subagent_type_available`, `tool_available`, `tool_allowed`, `mcp_server_available`, `mcp_server_failed`, `plugin_disabled`, `instruction_file`. `harness` is the agent (`claude`, `codex`, `pi`). Pi's `session.version` is `session_format`, never `harness_version`. A fact outside this list is a test failure |
| Sources per agent | As the prototype: Claude `version`, `entrypoint`, assistant `message.model` and `effort`, `permission-mode`, attachments `model`, `skill_listing`, `agent_listing_delta`, `deferred_tools_delta` (incl. `failedMcpServers`), `mcp_instructions_delta`, `instructions`, `invoked_skills`, `command_permissions`; Codex `session_meta` (`cli_version`, `model_provider`, `originator`, `source`) and `turn_context` (`model`, `effort`, `approval_policy`, `sandbox_policy.type`, `collaboration_mode.mode`, `disabled_plugin_ids`, `agents_md`); Pi `session`, `model_change`, `thinking_level_change`, assistant `message.model`, `provider`, `thinkingLevel` |
| Instruction files (operator rule) | Only a file name and a scope, never a path, directory or text. `name` is the base name when it is one of `CLAUDE.md`, `CLAUDE.local.md`, `AGENTS.md`, `AGENTS.override.md`, `GEMINI.md`, else `other` (a rule or nested file name is user-chosen and can name a project). `value` is a closed scope: Claude's `type` mapped to `project`, `user`, `local`, `managed`, else `other`; Codex `agents_md` gives name `AGENTS.md` and scope `project` when its directory equals the session's `session_meta.cwd`, else `other` |
| Bounds | `name` and `value` are strings of at most 200 characters after the rule above; longer, non-string or non-encodable values are skipped and counted. Never a prompt, instruction text, tool input or output, path or cwd. A malformed line is skipped (events already issues it) |
| Re-derivation | The extractor has a version (`SETUP_VERSION = 1`). A table `session_setup_version(session_key, version)` records what derived each session's rows. An import of an unchanged publication whose recorded version differs re-derives that session's setup rows from the staged bodies inside the same transaction, so an existing database fills without `--reset`; an import of a new publication always derives them. An older database gains both tables on its first writable open; read-only opens (dashboard, `report`) never create tables and tolerate their absence |
| View | `session_dim`: one row per session (`session_key`, `agent`, `harness_version`, `session_format`, `entrypoint`, `models` and `providers` as sorted lists, first and last `reasoning_effort`, `permission_mode`, `approval_policy`, `sandbox_policy`, counts of skills, MCP servers, tools and instruction files available, the list of instruction-file names). Created with `CREATE OR REPLACE VIEW` on writable open, so its shape evolves without migration |
| Out of scope | "Used" facts (Pi SKILL.md reads, extensions, Codex plugin and MCP use: brief 54), usage and cost columns, Codex stores, shared-owner export, report sections, capture changes |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | SETUP | `analytics/setup-facts` | worker (Sonnet, medium) |

Ownership: `tools/analytics/setup_facts.py` (new), `tools/analytics/test_setup_facts.py` (new),
`tools/analytics/publications.py`, `tools/analytics/README.md`, and
`rust/crates/memory/tests/s3_store/consumer_gate.rs` (fixture records gain setup fields and the
Python check asserts `session_setup` rows end to end). Read-only everywhere else; if another file
must change, stop and report.

Steps (one commit each, the focused tests green before each):

1. (a) The extractor, the two tables, re-derivation and the view.
2. (b) Tests with synthetic fixtures only:
   - per agent, every fact in the "Sources" row, through `import_export`, read back by SQL;
   - change-only: a repeated model is one row, a model change is two;
   - the privacy rule: a canary string in an instruction file's path, directory, text and in a
     non-standard file name never appears in any column of `session_setup` or `session_dim`;
     `CLAUDE.md` with type `Project` gives (`CLAUDE.md`, `project`); a rule file gives `other`;
   - Pi `session.version` is `session_format`, never `harness_version`;
   - bounds: a 201-character name, a non-string, a lone surrogate and a malformed line are
     skipped and the import still succeeds;
   - re-derivation: a database built without the table (simulate the old schema) gains rows on
     the next import of the same unchanged publication; a second import changes nothing;
   - read-only: `report()` and the dashboard's validation work on an old database without the
     tables and create nothing;
   - every emitted `fact` is in the closed list;
   - the consumer gate (real writer → `memory export` → importer) asserts at least one
     `model`, `harness_version` and `instruction_file` row for Claude, `model` and
     `approval_policy` for Codex, `model` for Pi.
3. (c) README section and module docs.

## Rules for the worker

- Python: the pinned venv `/Users/mm/dev/memory/.analytics-venv/bin/python`, run with `-I` only for
  scripts outside the repo. Tests: `cd tools/analytics && <venv python> -m unittest discover -s .
  -v` (all must pass, not only the new ones). No new Python dependency.
- Cargo only for the consumer gate, through the shared lock (path in the prompt):
  `MEMORY_REQUIRE_CONSUMER=1 MEMORY_ANALYTICS_PYTHON=<venv python> cargo nextest run -p memory
  --test s3_store --locked -E 'test(/consumer_gate_/)'`, plus `cargo fmt --check` and
  `cargo clippy --workspace --all-targets --locked -- -Dwarnings` for the Rust file.
- The research prototype is a behaviour reference only (path in the prompt).
- Never read real agent directories, `~/.local/state/memory*`, `.env`, R2 or the analytics
  databases under `.analytics/`. Synthetic fixtures in temporary directories only.
- No gate is loosened; a changed existing expectation gets a comment naming this brief and is
  listed. If a decision looks wrong or cannot be implemented cleanly, stop and report. Do not edit
  the brief, DESIGN.md or the handoff. No `git stash`, no push, no `scripts/gates.sh`.
- Report: commits, deviations, changed expectations, exact commands with test counts,
  `git status --short`.

## Acceptance (parent)

Read the diff. Teeth: a fact dropped per agent, the instruction-file allowlist bypassed (full
path or base name stored), the scope not closed, `session_format` reported as
`harness_version`, change-only dropped, re-derivation skipped on an unchanged publication, the
version not recorded, a read-only open creating a table, the length bound removed. Then the
Python suite, the consumer gate, the full Rust gates, hosted CI, and a real-data run: every
export on this Mac imported into a scratch database (counts only, no names printed), with the
fact counts per agent and the number of sessions with a model, compared with the research counts.
Evidence 48, DESIGN (the analytics projection section and D65), the handoff.

## Ledger

- 2026-11-23: brief written on `0da72df`.
