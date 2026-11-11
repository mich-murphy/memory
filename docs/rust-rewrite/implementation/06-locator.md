# Slice 1 locator report

Status: implemented, focused checks pass. Requirements in "Proposals" are NOT accepted until the parent accepts them.

Owned paths: `rust/crates/memory-core/src/locator.rs`, `rust/crates/memory-core/tests/core_locator.rs`, this file. Nothing else was edited. No host agent directories or transcripts were read; Go code and tests were read for layouts only.

## API (all in `locator.rs`; no `domain.rs` change needed)

- `RootRelativePath::parse(&str)`: rejects empty, leading `/`, empty component, `.`/`..`, NUL, component >255 bytes, path >4096 bytes. Errors (`InputError`) never echo input.
- `RootNamespace::new`: 1-64 bytes of `[A-Za-z0-9.-]`. It qualifies every id, so two roots can reuse names.
- `RootLayout`: `AgentDir` or `ClaudePrimaryHome` (only the exact `.claude.json` maps).
- `ClaudeSessionIndex::record(uuid, slug)`: shell-supplied UUID to project-slug facts (R4). A UUID seen under two slugs becomes absorbing `Ambiguous`, so the result is order-independent and yields `UnknownOwner`.
- `Roots::new(archive, machine, agent, namespace, layout).with_claude_sessions(index)`.
- `locate(&RootRelativePath, &Roots) -> Located` is pure and total. `Located` is `Source{owner, kind, class, id, role}`, `UnknownKind` or `UnknownOwner`. `withhold_reason()` maps the last two to `WithholdReason`.
- `Role`: `Main`, `Superseded`, `Child`, `History`, `SessionIndex`, `Artifact`, `Asset(ConfigAsset)`. `ConfigAsset` is a closed 28-variant enum.
- Ids: `<ns>:<escaped parts joined by ':'>`. Escaping is byte-wise and reversible: `[A-Za-z0-9.-]` stay, every other byte becomes `_xx`. Above 1024 bytes the id becomes `<ns>:h:<sha256 of NUL-delimited parts>`. Ids depend only on path and namespace, never on siblings.
- Differences from DESIGN's illustrative `Located::Source`: it carries two extra fields, `class` and `role`.

## Matrix (path under an `AgentDir` root, per agent)

Class: A = append, S = snapshot. Owner: Sh = Shared, Se = Session. `*` = proposal needing acceptance.

### Claude

| Pattern | Kind | Class | Owner |
|---|---|---|---|
| `projects/<slug>/<stem>.jsonl` (stem has no `.`) | ClaudeTranscript, Main | A | Se `<slug>:<stem>` |
| `projects/<slug>/<stem>.orphaned-*.jsonl`, `<stem>.jsonl.superseded-*` | ClaudeTranscript, Superseded* | A | Se |
| `projects/<slug>/<sid>/subagents/**/*.jsonl` | ClaudeSubagent, Child | A | Se |
| `projects/<slug>/<sid>/tool-results/**` | ClaudeToolResults* | A* | Se |
| other `projects/<slug>/<sid>/**` | ClaudeAux* | S* | Se |
| `projects/<slug>/memory/**` | ClaudeMemory* | S* | Sh |
| `file-history/<uuid…>/**` | ClaudeFileHistory | S* | Se via index |
| `session-env/<uuid…>/**` | ClaudeSessionEnv* | S* | Se via index |
| `todos/<uuid…>`, `tasks/<uuid…>` | ClaudeTodos, ClaudeTasks | S* | Se via index |
| `debug/<uuid…>` | ClaudeDebug | A* | Se via index |
| `teams/**` | ClaudeTeams | S* | Sh |
| `plans/**`, `paste-cache/**`, `usage-data/**`, `agent-memory/**` | ClaudePlan, ClaudePaste, ClaudeUsage, ClaudeMemory* | S* | Sh |
| `history.jsonl` | ClaudeHistory | A | Sh |
| top-level settings, local settings, `CLAUDE.md`, keybindings, launcher, remote, policy-limits, `*.sh/.py/.js/.ts`, `plugins/installed_plugins.json`, and `rules/ skills/ agents/ commands/ output-styles/ hooks/` | ClaudeConfig + asset subtype | S | Sh |
| `.claude.json` in a `ClaudePrimaryHome` root only | ClaudeConfig, PrimaryConfig | S | Sh |

An external-artifact name is `<uuid>` followed by end, `.`, `-` or `_`. A name without a UUID, a UUID absent from the index, or an ambiguous UUID gives `UnknownOwner`. `plugins/cache/**`, `backups/**`, `sessions/**`, a primary `.claude.json` inside an agent dir, and anything else give `UnknownKind`.

### Codex

| Pattern | Kind | Class | Owner |
|---|---|---|---|
| `sessions/**/rollout-<ts>-<uuid>[_<uuid>].jsonl`, `archived_sessions/**` | CodexRollout | A | Se `<stem>` |
| same name with `.zst` | CodexCompressed | A | same Se and same id as uncompressed |
| `history.jsonl` | CodexHistory, History | A | Sh |
| `session_index.jsonl` | CodexHistory, SessionIndex* | A | Sh |
| `config.toml`, `hooks.json`, `AGENTS[.override].md`, `requirements.toml`, `managed_config.toml`, `rules/ skills/ prompts/`, `plugins/<non-cache>/**` | CodexConfig + subtype | S | Sh |
| top-level `*.sqlite`, `*.sqlite3`, `*.db` (case-insensitive) | CodexStore | S | Sh |
| `memories/**` | CodexMemory* | S* | Sh |
| `log/**` | CodexLog* | A* | Sh |

The rollout id drops date folders, archive location and `.zst`, so moving or recompressing a rollout keeps one logical id. WAL and shm files give `UnknownKind`.

### Pi (authoritative; includes corrections 1 and 2)

| Pattern | Kind | Class | Owner |
|---|---|---|---|
| `sessions/[--cwd--/]<name>.jsonl` | PiSession, Main | A | Se `[cwd:]<stem>` |
| `sessions/[--cwd--/]<ts_uuid>/tasks/*.jsonl`, `forks/*.jsonl`, `<uuid>/run-N/session.jsonl` | PiSession, Child | A | Se of the parent |
| `sessions/[--cwd--/]<ts_uuid>/<anything else>` | PiAux*, Artifact | S* | Se of the parent, path-qualified id |
| `sessions/<multi-part path whose first part is not a session dir and not a cwd dir>` (incl. `sessions/--cwd--/<non-session>/..`, and bare `sessions/.jsonl`, or `sessions` itself) | PiAux*, Artifact | S* | Sh, path-qualified id |
| `sessions/<single non-.jsonl file>`, `sessions/--cwd--` | none | n/a | UnknownKind (Go RoleUnknown) |
| flat layouts outside `sessions/` (`<ts_uuid>.jsonl`, `--cwd--/<ts_uuid>.jsonl`, child layouts, `<ts_uuid>/x`) | PiSession / PiAux | A / S* | Se, stem must be `<ts>_<uuid>` |
| other multi-part or non-session files outside `sessions/` | none | n/a | UnknownKind |
| `settings.json`, `models.json` | PiSettings, PiModels | S | Sh |
| `models-store.json`, `mcp.json`, `keybindings.json`, instruction files, `extensions/ prompts/ skills/ themes/ tools/ experimental/` | PiConfig* + subtype | S* | Sh |
| top-level `*.log` | PiLog* | A* | Sh |
| `PiCustomRoot` (not promoted): flat `*.jsonl`, `--cwd--/*.jsonl`, child layouts | PiSession | A | Se |
| `PiCustomRoot`: `<ts_uuid>/<other>` | PiAux* | S* | Se |
| `PiCustomRoot`, promoted (correction 3): path under `sessions/` (`sessions/<x>/..`), a top-level piRoles name (settings, models, models-store, mcp, keybindings, AGENTS[.override].md, CLAUDE.md, SYSTEM.md, APPEND_SYSTEM.md), a piRoles dir (`extensions/ prompts/ skills/ themes/ tools/ experimental/` with something below), or a single-part `*.log` | same as the agent-dir rows above (PiSettings, PiModels, PiConfig*, PiLog*, PiSession/PiAux under `sessions/`) | as above | as above |
| `PiCustomRoot`, not promoted: everything else (`x.txt`, `foo/x.jsonl`, `--cwd--`, `--cwd--/x.txt`, `.jsonl`, `skills`, `sessions`, `settings.json/x`, `unknown/x.log`) | PiAux*, Artifact | S* | Sh |
| other agent with `PiCustomRoot` | none | n/a | UnknownKind |

`PiSnapshot` is a generated harness snapshot, not a path kind, so `locate` never returns it (asserted by a test). An arbitrary nested `.jsonl` in a flat root gives `UnknownKind`. Neither `migration-backups` nor `bin` maps.

## Proposals the parent must accept

1. Classes marked `*`. Rationale: write-once or rewritten-in-place artifacts are snapshots, while logs and tool results are append.
2. `ClaudeAux`, `ClaudeMemory`, `CodexMemory`, `CodexLog`, `PiConfig` and `PiLog` kinds, as listed in acceptance-map section 2.
3. `Located::Source` carries `class` and `role` in addition to `owner/kind/id`.
4. Claude external artifacts take their owner only from the shell UUID→slug index. Conflicting facts mean `UnknownOwner`, with no first-sibling heuristic.
5. `projects/<slug>/memory/**` is Shared, not session-owned.
6. Superseded transcripts use `ClaudeTranscript` with `Role::Superseded` and a distinct SourceId (the physical filename).
7. Over-long ids are hashed (`<ns>:h:<sha256>`). This keeps ids valid and total, and a long session owner is hashed the same way.
8. Root namespace is a shell-supplied opaque string, for example a root hash. The ids it yields are not wire-format-final.
9. The no-session Claude project `memory` directory and Pi flat roots only follow Go's layout evidence. Config files are mapped by name, and the privacy module remains the authority for credential denial and transform selection. `locate` does not deny credentials.

## Tests (`tests/core_locator.rs`)

- Table of about 190 rows, with expectations written independently as literal pre-escaped strings. They cover every family above, bold gaps included, plus negatives for each pattern.
- Reachability test: every located kind (except `pi.snapshot`), every `ConfigAsset` and every `Role` appears.
- Namespace separation, oversized-id hashing against an independently computed SHA-256, path/namespace/index validation, ambiguity order-independence, and foreign-agent and foreign-layout rejection.
- Property 7, on both Hegel and proptest (3 properties each):
  1. Totality over arbitrary component sequences that include `..`, empty, NUL and non-ASCII fragments. The parse oracle is independent, and results are stable under unrelated facts and countable.
  2. Table rows are unchanged when unrelated UUID facts are added in either order. A conflicting fact for the row's own UUID can only withhold.
  3. Unknown first components always give `UnknownKind`, and Claude externals with an unindexed UUID always give `UnknownOwner`.

## Commands and exit statuses

All commands ran inside `nix develop`, with `CARGO_TARGET_DIR=target/w-locator`.

- `cargo fmt --check -p memory-core`: exit 0.
- `cargo clippy -p memory-core --lib --test core_locator --locked -- -Dwarnings`: no diagnostics. The full `--all-targets` command fails for reasons outside my paths: `manual_is_multiple_of` in `manifest.rs` and a missing `agent_transcript` field in `tests/core_hint.rs`. No diagnostic points at my files.
- `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory-core --test core_locator -- --test-threads=2`: exit 0, 15 passed, 0 failed.

SHA-256 values are in the final message, because this file cannot hash itself.

## Limitations

- Coverage was not measured. The parent's coverage gate must confirm the 98% floor. Every branch is reached by the table or the properties, but that was not checked by tooling.
- The matrix follows Go layouts. The Go skip lists (noise names, lock suffixes, installer backups) are privacy/policy concerns and are not duplicated here, so a path in a noise class may locate as a source and must still pass `admit`.
- Non-ASCII names are accepted and escaped into ids. A `SourceId` that fails validation after escaping returns `UnknownKind` (unreachable today, since the escaped alphabet is a subset of the id alphabet).
- Project and managed config paths need admitted roots from the shell, as in R4. Only the primary home layout exists in this API.

Owned paths released.

## Parent correction 1

Changes: `PiAux` mapping, `RootLayout::PiCustomRoot`, new table rows, generators extended (`Home::PiCustom` is drawn by the property-7 generators, and the session-directory and `tasks`/`forks` fragments reach `PiAux`).

Updated matrix rows (Pi):

| Pattern | Kind | Class | Owner |
|---|---|---|---|
| `sessions/[--cwd--/]<ts_uuid>/<anything other than tasks/forks/run-N child layouts>` | PiAux* | S* | Se of the parent, cwd-qualified as the main transcript; SourceId is path-qualified |
| same under flat layout (`<ts_uuid>/x`, `--cwd--/<ts_uuid>/x`) | PiAux* | S* | Se |
| `PiCustomRoot` layout: flat `*.jsonl`, `--cwd--/*.jsonl`, `<ts_uuid>/tasks|forks/*.jsonl`, `<ts_uuid>/<uuid>/run-N/session.jsonl` | PiSession | A | Se, namespace-qualified |
| `PiCustomRoot`: `<ts_uuid>/<other>` | PiAux* | S* | Se |
| `PiCustomRoot`: settings, models, mcp, `*.log`, instruction files, asset dirs, `sessions/…` | none | n/a | UnknownKind |
| Any other agent paired with `PiCustomRoot` | none | n/a | UnknownKind |

Decisions:
- A single unrecognised top-level file in a Pi agent dir stays UnknownKind (Go's `RoleUnknown`).
- Inside `sessions/` without a cwd dir, a multi-part path whose first part is not a session dir (Go: sessionless Aux) is **UnknownKind**. No session owner is derivable from the path and a missing owner fact cannot fix that, so UnknownOwner would suggest a retry that cannot succeed. It is still counted. Proposal: parent to accept.
- `PiAux` class Snapshot is a proposal (`*`).

Commands (inside `nix develop`, target dir `target/w-locator`):
- `rustfmt --edition 2024` on my two Rust files: exit 0.
- `cargo clippy -p memory-core --lib --test core_locator --locked -- -Dwarnings`: no diagnostics.
- `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory-core --test core_locator -- --test-threads=2`: 15 passed, 0 failed. (The `echo $?` after a piped `tail` was used for the status, so the pass/fail line is the authoritative result.)

SHA-256 values are in the final message. Paths released.

## Parent correction 2

Exact rule implemented (`PiMode` in `locator.rs`, modelled on `pi.go:186-205`):

1. Agent dir, path under `sessions/` (`Sessions` mode): strip optional `--cwd--/` (only if something follows). Then: one `.jsonl` file is Main; `<ts_uuid>/...` follows the child/aux rules; a single non-`.jsonl` part is UnknownKind (Go: sessionless Aux demoted to RoleUnknown); any other shape (multi-part with a first part that is not a session dir, bare `.jsonl`, or empty remainder) is `PiAux`, Snapshot, `Owner::Shared`, `Role::Artifact`, id = namespace plus escaped full path.
2. Agent dir, outside `sessions/` (`Strict`): unchanged. A single unrecognised top-level file stays UnknownKind.
3. `PiCustomRoot` (Go `agentDir=false`, no demotion to RoleUnknown): everything that is not a transcript or a session-dir child/aux becomes the same Shared PiAux, including single non-`.jsonl` files.

Changed rows:
- Pi agent dir: `sessions/foo/tasks/t.jsonl`, `sessions/foo/x`, `sessions/--c--/foo/x.txt`, `sessions/--c--/foo/tasks/t.jsonl`, `sessions/.jsonl`, `sessions/<ts-without-uuid>/x.txt`, `sessions/not_a_uuid/x`, `sessions/a/b/c/d.jsonl`, `sessions` moved from UnknownKind to Shared PiAux (`sessions` alone was an old negative; Go keeps it as Aux with an empty session, length 0, so it is not demoted).
- Still UnknownKind: `sessions/--c--`, `sessions/x.txt`, `sessions/--c--/x.txt`, `sessions/settings.json`, `foo.jsonl`, `nested/x.jsonl`, and the other old negatives.
- `PiCustomRoot`: `settings.json`, `models.json`, `mcp.json`, `pi.log`, `AGENTS.md`, `skills/a/b.md`, `x.txt`, `foo/x.jsonl`, `sessions/foo.jsonl`, `--c--`, `.jsonl` moved from UnknownKind to Shared PiAux, and `--c--/x.txt`, `--c--/foo/x.jsonl` were added. This follows Go exactly, so it changes correction 1's custom-root decision. Parent should confirm.
- Reachability test now requires the Shared PiAux arm. Property-7 `prop_unmapped` expects Shared PiAux for `PiCustom` homes, and also generates `sessions/[--c--/]zzN/<frag>` paths that must be Shared PiAux with an id starting `r1:sessions:`. Both Hegel and proptest use these functions.

Commands (inside `nix develop`, target `target/w-locator`): `cargo clippy -p memory-core --lib --test core_locator --locked -- -Dwarnings`: exit 0. `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory-core --test core_locator -- --test-threads=2`: exit 0, 15 passed. `rustfmt --edition 2024` on both Rust files.

Limitations: no coverage run; custom-root behavior above is a disagreement with correction 1 resolved toward Go. Paths released.

## Parent correction 3

Retracted premise: correction 1's "config/log/settings rows must NOT match under a custom root" was wrong. Go `Pi.Classify` (pi.go:158-168) uses agent-dir rules whenever (a) rel starts with `sessions/`, (b) `topRole(piRoles, rel)` is known (top component; dirs match with a trailing `/`, so the dir needs something below it), or (c) a single-part `*.log`. Otherwise agentDir=false (Shared PiAux demotion, correction 2).

Implemented in `locate` for `PiCustomRoot`: `promoted = ["sessions", _, ..] || pi_asset(parts).is_some()`, then `pi(parts)` (agent-dir rules, reusing `pi_asset`, no duplicated tables), else `pi_session(.., PiMode::Custom, ..)`. Note: a bare `sessions` and `sessions/<x>` single part follow the agent-dir rules only when `sessions/` has a component below it; bare `sessions` stays aux.

Rows: promotion rows for all 10 piRoles files, the 6 dirs, `pi.log`, `x.log`, `sessions/foo.jsonl`, `sessions/<ts_uuid>.jsonl`, `sessions/--c--/<ts_uuid>/tasks/t.jsonl`, `sessions/foo/x` (Shared PiAux), `sessions/x.txt` and `sessions/--c--` (UnknownKind). Non-promoted Shared PiAux rows: `x.txt`, `foo/x.jsonl`, `--c--`, `--c--/x.txt`, `--c--/foo/x.jsonl`, `.jsonl`, `.log`, `settings.json/x`, `models.json.bak`, `skills`, `sessions`, `unknown/x.log`. Property 7 (`prop_unmapped`, Hegel and proptest) asserts custom-root promoted paths equal the agent-dir result and have the expected kind.

Commands (inside `nix develop`, target `target/w-locator`): clippy exit 0; `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test ... core_locator`: exit 0, 15 passed. Paths released.
