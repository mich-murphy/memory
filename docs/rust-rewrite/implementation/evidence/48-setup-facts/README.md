# Evidence 48: the agent setup of each session, from its transcript (brief 53)

All dates are UTC. Status: increment SETUP accepted locally at `e4159a2` on 2026-11-23. That is 7
commits (3 by the worker, 4 by the parent) over the brief `cd516d6`, on `main` `0da72df`. Only the
analytics importer (`tools/analytics/`) and the consumer-gate test changed; the capture binary is
unchanged and nothing was installed. The worker read no real agent data and no production state.

[Brief 53](../../53-setup-facts.md) records the research behind the decisions.

## Commits

| Commit | By | Change |
|---|---|---|
| `86b747c` | worker | `setup_facts.py`: per-agent extractor, closed fact list (21 facts), bounds (strings of at most 200 characters, encodable), change-only rows per source, `harness` row per source. Tables `session_setup` and `session_setup_version`, view `session_dim`, all created on the writable open only. An unchanged publication whose recorded `SETUP_VERSION` differs is re-derived from the stored bodies in its own transaction. The import result gains `setup_skipped` |
| `94c44ca` | worker | 12 tests with synthetic fixtures; the consumer gate's fixtures gain setup fields and `test_rust_export_journey.py` asserts `session_setup` rows end to end |
| `16a5325` | worker | README section |
| `7e9609a` | parent | An absent Codex `approval_policy` records nothing (only a structured one is `granular`); brief 53 comments on the three changed consumer-gate event counts |
| `09939cc` | parent | Tests for the surviving tooth: a `cwd` and `gitBranch` canary in the Claude fixture, and an unknown fact skipped and counted |
| `832d85c` | parent | Fixes from the real-data run (below): Codex `agents_md` read from `world_state` records at `payload.state.agents_md`; a Claude skill name keeps only its last path segment; a permission pattern `Tool(argument)` keeps only the tool name; outside `model`, any name or value with `/`, `\` or a leading `~` is skipped and counted |
| `e4159a2` | parent | The unknown-fact test uses a value without a path character, so only the closed list can reject it |

Deviations, accepted:

- `harness` is emitted once per source on its first parsed line.
- Codex `source` as an object is stored as `entrypoint=subagent`; a non-string `approval_policy`
  as `granular` (both from the prototype).
- Changed existing expectations: `consumer_gate.rs` event counts for Claude `s1` 6 → 7 (phase 2:
  7 → 8) and Codex 6 → 7, because the fixtures gained one control record each (commented).

## Real data (this Mac, counts only)

`memory owners` listed 1,647 owners. The installed `94833f6` binary exported all 1,647 (exit 0,
42 min, GET only). The brief 53 importer imported all 1,647 into a scratch database, and a second
import gave 1,647 `unchanged`. `setup_skipped` 0, no unknown fact.

The first run found three defects, which `832d85c` fixed:

- 8,422 Claude `skill_available` rows carried one two-segment relative directory before the
  plugin skill name (`.x/y/plugin:skill`).
- 4 `tool_allowed` rows were permission patterns with home-relative paths (`Read(~/…)`).
- Codex gave no `instruction_file` row: real rollouts carry `agents_md` in `world_state` records
  at `payload.state.agents_md` (27 records in 29 rollouts), not where the worker looked.

After the fix (same exports, fresh database, 450,294 setup rows):

| | Claude | Codex | Pi |
|---|---|---|---|
| Sessions | 620 | 167 | 860 |
| With a model | 371 | 162 | 860 |
| With `harness_version` | 386 | 167 | 0 (Pi records only `session_format`) |
| With an instruction file | 218 | 156 | — |
| With skills listed | 371 | — | — |
| With MCP servers listed | 251 | — | — |

The 234 Claude sessions without setup rows hold only artifacts and have no events.

Instruction files (name, scope, rows): `AGENTS.md` project 1,251, `CLAUDE.md` user 694, `other`
other 660, `CLAUDE.md` project 272, `AGENTS.md` other 27, `other` user 4. Longest name 67
characters, longest value 18. No row holds a slash or backslash outside `model` and the tool
facts, no row holds the home path or the user name as a path segment or word part. (A plain
substring search for the two-letter user name matches 17,175 rows, all inside tool and skill
words such as "command".)

## Parent acceptance

- Whole diff read.
- Teeth (`b53_teeth.py`, `test_setup_facts`, baseline 16 tests): 27 of 27 caught after the
  parent tests:
  - a fact dropped per agent (Claude model, Claude instructions, Codex `turn_context` model, Codex
    `cli_version`, Pi `model_change`), Pi `session_format` stored as `harness_version`;
  - the instruction-file allowlist bypassed (base name, full path), the scope not closed or not
    lower-cased, the Codex scope ignoring the cwd, a text-only `agents_md` ignored, the
    `world_state` path wrong;
  - change-only dropped, the length bound removed, a non-string accepted, the fact list open
    (with a `gitBranch` leak), the `harness` row dropped, skipped values not counted;
  - re-derivation skipped, the version not recorded, a new import writing no setup rows or
    keeping the old ones, a read-only open creating tables;
  - the skill-name leaf dropped, the permission pattern kept, the path guard removed.

  At first `fact_list_open` survived (no fixture record had a `cwd`); `09939cc` and `e4159a2`
  close it.
- Python suite on `e4159a2`: 174 tests, OK (27 environment-dependent skips).
- Full Rust gates on `09939cc`: 14 of 14, seed 2247821627, nextest 933, Hegel 33, proptest 36,
  consumer 3, line coverage 95.68 %. The later commits change only Python; on `e4159a2` fmt, both
  clippy passes and the consumer gate (3 passed) were rerun.

## Capture-reduction measurements (for the next brief, 2026-11-23)

Counts-only probes (hash sets of normalized lines, run with `timeout`):

- This Mac: Pi `subagent-artifacts` holds 67 `.md` files and 5 `.last-cleanup` markers. All 67
  appear line for line in captured Pi sessions. The one Claude plan appears whole in a transcript.
- dev-01: `subagent-artifacts` holds 182 `.md`, 59 `.jsonl` run logs (40 MB), 59 `.json` run
  records and 3 markers. 116 `.md` appear line for line in captured sessions; 59 short ones
  (59 to 84 characters) appear nowhere, not even as substrings; 7 have no long line. 57 of 59 run
  logs have at least 90 % of their text lines in sessions, none as identical records. The run
  records carry model, requested model, resolved extensions and usage (setup facts); the parent
  sessions' `subagents:record` entries carry status, timing, turn budget and tool uses but not
  model or extensions.

So stopping `subagent-artifacts` capture outright would lose dev-01's run records and 59 short
outputs. The next brief decides this with the operator.
