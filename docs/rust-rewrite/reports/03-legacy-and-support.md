# Report 03: legacy collector path and support packages

Reviewer scope: `internal/syncer`, `state`, `raw`, `snapshot`, `store`, `adapters`, `hook`, `install`, `launch`, `doctor`, `scrub`, `exclusion`, `safefile`, `fsutil`, `shellwords`, `jsonx`, `gitinfo`, `config`, `logging`, and the non-`capture-v1` commands in `internal/cli` and `cmd/agent-sessions`.
Repo state read: `main` at `57d8400`, working tree with three modified docs. Nothing was run. Every claim is labelled **measured** (I read the code at the cited line), **doc says**, **inferred**, or **guess**.

## 0. Findings first

1. **About 7,200 of the roughly 16,500 in-scope non-test LOC (about 45 percent) is a v3 archive mechanism the Rust rewrite should not port.** `syncer` (4,825 LOC non-test), `state` (1,384), `store` (743), and most of `raw` (256) implement plans, commits, parent chains, checkpoints, folds and a machine-wide recovery pass. The candidate path imports none of them. Measured with an import scan (`scratchpad/stat.sh`). Drop them whole.
2. **The domain knowledge worth keeping lives in about 6,900 LOC.** That is `adapters` (3,106), `scrub` (827), `snapshot` (1,615), `exclusion` (256), `launch` (152), the process and payload parts of `hook` (about 250), `safefile` (179), `install`'s Codex trust and settings merge (about 500), and small helpers. The candidate already imports `adapters`, `scrub`, `snapshot`, `exclusion`, `launch`, `safefile`, `jsonx`, `shellwords` and part of `hook`. So the rewrite is mostly a port of these, not new design.
3. **The adapters encode layout facts that exist nowhere else in writing.** Examples: Claude per-session files outside `projects/` are keyed by a slug lookup (`adapters/claude.go:144`); Codex forks replay parent `session_meta` headers that must be filtered by the filename UUID (`adapters/metadata.go:46`); Pi children live in four different shapes (`adapters/pi.go:141`). Section 2 lists all of it.
4. **The candidate installer does not raise Claude's `cleanupPeriodDays`.** The legacy installer sets it to 3650 because Claude otherwise deletes transcripts after 30 days (`install/install.go:306-324`, doc `hooks.md:80`). A grep for `cleanupPeriod` finds no other non-test site. So after cutover, source expiry silently returns. This is the sharpest regression risk I found. Label: measured for the grep, inferred for the consequence.
5. **The "over-broad Codex exclusion" is not Codex specific.** `Registry.Excludes` denies every session-less file in the same agent and root once any record exists (`exclusion/exclusion.go:211-217`). The test at `cli/capture_runtime_privacy_test.go:148-176` pins the same behaviour for Claude (`history.jsonl`, `settings.json`, `state.db` all denied after excluding session A). Codex only looks worse because its SQLite stores are the main shared artifact. Redesign this rule, do not port it.
6. **Legacy exclusions were mirrored to R2; the candidate only persists them locally.** Legacy publishes `v3/machines/<m>/exclusions/<sha>.json` and restores them on recovery (`syncer/recovery.go:142-183`). A grep finds no remote exclusion code in candidate packages. The contract says "preserve effective local/remote exclusions" (`session-capture-contract.md` section 6 step 2). This needs an operator decision (section 8).
7. **Legacy single lock plus mandatory machine-wide `Recover` is the structural cause of session-coupling.** Every sync, including a one-session hook sync, calls `Recover` before touching its session (`cli/commands.go:171`, `syncer/recovery.go:186`). A cold state means enumerating the whole remote history first. The contract's "session A must not require session B" is a direct reaction to this.

## 1. Package verdicts

LOC is non-test lines from `wc -l`. "Exported" is a regex count of exported funcs, methods, types, consts and vars, so treat it as an order of magnitude (measured with `stat.sh`). "Candidate uses" is from import scans of `internal/` excluding tests.

| Package | LOC | Exported | What it does | Candidate uses it | Verdict |
|---|---|---|---|---|---|
| `syncer` | 4,825 | 15 | Legacy v3 collector. Prepares captures, uploads pieces, writes plan then commit records, tombstones deletions, folds appends, recovers machine-wide (`syncer.go:1-5` says it is not the replacement). | No (only `cli`'s legacy commands) | **Drop** |
| `state` | 1,384 | 75 | SQLite projection of the v3 archive: binding, observations, archive_records, checkpoints (`state/state.go:33-50`), plus `sync.lock` flock (`state/lock.go`). | No | **Drop**. Keep the lock idiom (section 5). |
| `raw` | 256 | 14 | v3 `Generation`, `Piece`, `Source` wire types and key validation (`raw/record.go:20-60`). | Indirectly, through `snapshot` | **Drop** the wire types. Keep key and UTF-8 validation as ideas. |
| `store` | 743 | 18 | `Store` interface over R2 and a local dir, Put/Copy/Create semantics (`store/store.go:17-37`). | No (candidate uses `publicationr2`) | **Drop** |
| `snapshot` | 1,615 | 20 | Harness snapshot: instruction, skill, settings, plugin, script, import files per scope, with byte, file and walk budgets (`snapshot/snapshot.go:29-37`), plus a canonical TAR bundle (`snapshot/bundle.go`). | Yes, `cli/capture_runtime_enrichment.go:62,72` | **Carry forward** (scopes, budgets, `git ls-files` selection, bundle). Rebuild its content-addressed set format on the new wire. |
| `adapters` | 3,106 | 80 | Per-agent layout, classification, session identity, discovery, related files, exports, SQLite backup, session info. | Yes, heavily (`SessionFile`, `Adapter`, `All`, Role constants, `BackupSQLiteAuthorized`) | **Carry forward**. Section 2. |
| `hook` | 485 | 3 | Legacy hook entry (`Run`), debounce markers, detached sync spawn, process-tree argv reader, launch envelope writer. | Only `InspectNativeArgs` (`cli/capture_runtime_launch.go:85`, `hook/native.go`) | **Carry forward** the stdin read, process walk, `isAgent`. **Drop** markers, detached sync, sweep. |
| `install` | 860 | 21 | Merges hook entries into Claude `settings.json` and Codex `hooks.json`, writes the Pi extension, validates Codex hook schema, computes Codex trust hashes. | No. `capturehooks` is a separate re-implementation (`capturehooks/README.md`) | **Redesign**. Port the Codex hash and schema checks and the `cleanupPeriodDays` raise. |
| `launch` | 152 | 7 | Durable launch envelope, native argv and env allowlist sanitizers. | Yes (`SanitizeNativeArgs`, `SanitizeNativeEnvironment`) | **Carry forward** the sanitizers. **Drop** `Decode`'s flat legacy format (`launch/launch.go:51-75`). |
| `doctor` | 311 | 1 | Health checks for config, hooks, Codex trust, runs. | No | **Redesign** against the candidate's status surface. Keep the check list. |
| `scrub` | 827 | 12 | Structural redaction of known JSON and TOML configs, argv scrubber, shell assignment guard, rules digest. | Yes (`Recognizes`, `Identities`, `RulesDigest`) | **Carry forward**. Section 4. |
| `exclusion` | 256 | 8 | Persistent session opt-out records and registry. | Yes (`cli/capture_runtime_*`, `RecordFile`, `Load`, `Excludes`) | **Carry forward the record and fail-closed load. Redesign `Excludes` scope.** |
| `safefile` | 179 | 4 | `openat` chain with `O_NOFOLLOW` from `/`, pinned to an expected inode. Bounded directory walk. | Yes (`OpenResolved`) | **Carry forward**. It is the core TOCTOU defence. |
| `fsutil` | 103 | 7 | Atomic write, `SameLocation`, `Within`. | Yes (`capturepolicy`, others) | **Carry forward** (small). |
| `shellwords` | 90 | 2 | shlex-compatible quote and split. | Yes (`capturehooks`) | **Carry forward**. A crate exists; verify quoting parity. |
| `jsonx` | 242 | 16 | Lossless numbers (`json.Number`), insertion-ordered `Object`, sorted compact output. | Yes | **Carry forward as a requirement.** `serde_json` with `preserve_order` and `arbitrary_precision` covers most of it. Duplicate-key rejection is separate (section 4). |
| `gitinfo` | 108 | 3 | Repo root, remote (userinfo stripped), branch, head, dirty, and a `repo_id` hash (`gitinfo/gitinfo.go:77-108`). | No. `RepoID` appears nowhere else | **Drop unless repo identity is wanted** (section 8). |
| `config` | 216 | 10 | Env-file plus env overrides, `machine-id` file, explicit creation. | No (candidate uses `capturedaemon.Config`) | **Redesign**. Keep machine-id rules. |
| `logging` | 54 | 1 | 1 MB rotating slog file plus terminal warnings. | No | **Drop**. Use `tracing` with an appender. |
| `cli` (legacy part) | about 700 of 3,710 | n/a | `sync`, `backfill`, `machine-id`, `install`, `uninstall`, `doctor`, `hook` dispatch (`cli/cli.go:105-130`). | n/a | `sync`/`backfill`: **drop**. `machine-id`, `install`, `uninstall`, `doctor`, `hook`: **redesign** on the candidate. |
| `cmd/agent-sessions` | thin | n/a | `main` only (`main.go`). The rest is acceptance tests of the v3 CLI. | n/a | **Drop** the legacy acceptance tests. |

Note on weight. The first `wc` I ran counted tests and reported `adapters` at 5,890; the non-test count is 3,106. Tests are about twice the code, so the Go suite is a large behavioural spec the rewrite can mine.

## 2. The agent adapters (the domain knowledge)

### 2.1 Shared model

- **Adapter surface** (`adapters/base.go:97-120`). `Classify(path, root) -> SessionFile`, `Related`, `InferRoot`, `Exports`, `SessionInfo`, `MemoryDirs`, `Plugins`, `ExtraSnapshotEntries`, `Rules`, `SkipDirs`, `ManagedSettingsFiles`. **Measured.**
- **`SessionFile`** carries `Agent, Root, Path, SourcePath (for generated exports), Key, SessionKey, Role, Zstd` (`base.go:57-70`). `SessionKey == ""` means agent-wide shared file. That single empty string drives the exclusion over-breadth (section 4).
- **22 roles** (`base.go:27-50`): `main, subagent, tool_result, superseded, aux, file_history, session_env, debug, todo, task, team, plan, paste, usage, memory, history, liveness, experimental, config, log, db, unknown`.
- **Root namespaces.** The canonical root is fixed from `$HOME`, not the environment, so a hook (sees `CLAUDE_CONFIG_DIR`) and a sweep (does not) agree on keys (`base.go:~100`, `layout.CanonicalRoot` at `base.go:~230`). Other roots get key prefix `roots/<sha256(root)[:10]>/` (`base.go:205-215`). Pi session dirs outside the agent dir get `dirs/<tag>/`.
- **Walk policy** (`base.go:260-330`). Resolve the root symlink for traversal only, keep logical paths for keys. Skip dirs named `node_modules .git __pycache__ .venv venv cache .cache tmp .tmp temp shell-snapshots shell_snapshots` (`base.go:153`). Skip file suffixes `.lock .tmp .pid .sock .swp -wal -shm -journal` (`base.go:~160`) but keep dependency locks such as `flake.lock` (`base.go:177-183`). Skip ELF, Mach-O and PE binaries by magic when any exec bit is set (`base.go:190-205`). Credentials and "noise" come from `capturepolicy` (`.credentials.json auth.json oauth.json mcp-auth.json config.env *.key *.pem .env .env.* .claude.json*`, `policy.go:16-21`; noise includes `.app`, `migration-backups`, `.DS_Store`, `.codex-global-state.json`, `*_cache.json`, `logs_*.sqlite`, `policy.go:128-139`). All **measured**.
- **Symlinks.** Legacy follows regular-file symlinks and checks every intermediate identity against policy, skips directory and dangling links and records a `Skipped` entry that fences deletion (`base.go:458-485`). The candidate instead records a gap `alias_integration_required` for any symlink (`capturesource/discover.go:318-321`). **Measured.** The two behaviours differ. If users symlink skills or settings into the walked roots, the candidate captures less than legacy did. See section 8.
- **SQLite.** Any file ending `.sqlite .sqlite3 .db` is diverted to the database path, never raw-copied (`base.go:158,310`). Legacy used the backup API through a path (`adapters/sqlite.go:79-127`). The candidate uses a pinned DB and WAL pair read through a custom VFS with before/after identity checks (`adapters/sqlite_authorized.go:70-110`, doc `session-capture-phase3.md:170-180`).
- **Heads.** Bounded observations: first 20 lines or 1 MiB of head, last 256 complete lines of tail (doc `storage-layout.md:~245`). Timestamps normalise to UTC, numeric timestamps keep exact spelling (`metadata.go:~108-120`). `last_event_at` is an observation, never an end time.

### 2.2 Claude Code

**Roots.** `$CLAUDE_CONFIG_DIR` else `~/.claude` (`claude.go:50`, `layout.Dir`). Plus roots learned from hooks (`syncer/syncer.go:~119` `roots()` joins default, canonical and learned extras).

**What it discovers (`claude.go:90-141`).**

| Path under root | Role | SessionKey |
|---|---|---|
| `projects/<slug>/<sid>.jsonl` | main | `<slug>/<sid>` |
| `projects/<slug>/<sid>.orphaned-*.jsonl`, `<sid>.jsonl.superseded-*` | superseded | `<slug>/<sid>` |
| `projects/<slug>/<sid>/subagents/**` (needs at least 5 path parts) | subagent | `<slug>/<sid>` |
| `projects/<slug>/<sid>/tool-results/**` | tool_result | `<slug>/<sid>` |
| other `projects/<slug>/<sid>/**` | aux | `<slug>/<sid>` |
| `projects/<slug>/memory/**` | memory | none |
| `projects/<x>/<y>` with fewer than 3 parts, or other names | unknown | none |
| `file-history/<sid>/**`, `session-env/<sid>/**`, `debug/<sid>*`, `todos/<sid>*`, `tasks/<sid>*` (first path part must start with a UUID) | file_history, session_env, debug, todo, task | looked up by slug index, else none |
| `history.jsonl` | history | none |
| `settings.json`, `settings.local.json`, `CLAUDE.md`, `keybindings.json`, `launcher-settings.json`, `remote-settings.json`, `policy-limits.json`, `skills/ agents/ commands/ rules/ output-styles/ hooks/ plugins/` | config | none |
| `plans/`, `teams/`, `paste-cache/`, `usage-data/`, `agent-memory/`, `sessions/` | plan, team, paste, usage, memory, liveness | none |
| top-level `*.sh *.py *.js *.ts` | config | none |

Skip dirs: `plugins/cache plugins/marketplaces local downloads backups statsig ide telemetry` (`claude.go:57`). `backups/` holds copies of `~/.claude.json` with the API key (comment at `claude.go:54-56`). `sessions/*.key` are credentials (doc `hooks.md:81`).

**Identity.** Session key is `<slug>/<sessionId>`. The slug is the project directory name. File name stem is the session id (`claudeMainRE`, `claude.go:25`). Per-session directories outside `projects/` resolve their slug through `sessionKey()` (`claude.go:144`): one directory scan per root, memoised in `Claude.slugs`, first slug in name order wins when a sid exists in several projects, with a glob fallback for sessions that started after the index was read.
`SessionInfo` takes `sessionId, agentId, cwd, gitBranch, version, entrypoint` first non-empty from the head, sets lineage `subagent` with parent = `sessionId` when `agentId` is present (`claude.go:430-445`).

**Related files for a hooked session** (`claude.go:180-215`). `projects/<slug>/<sid>.jsonl`, the `<sid>/` directory, `file-history/<sid>`, `session-env/<sid>`, the **whole** `tasks/` and `teams/` directories, `debug/<sid>.txt`, glob `<sid>.*` in the project dir, glob `<sid>*` in `todos/`. Whole `tasks/` and `teams/` because Claude deletes team state when the team finishes (doc `hooks.md:79`).

**Exports.** `~/.claude.json` is **not** under `~/.claude`. For the canonical root it is `$HOME/.claude.json`, otherwise `<root>/.claude.json` (`claude.go:241-246`). It is read through the policy exception `ExcludesClaudeConfig`, scrubbed, written to the state dir as `claude.json`, refreshed at most hourly (`recopyAfter`, `syncer.go:~50`) because Claude rewrites it on every start (`claude.go:247-275`). In the candidate it is "one exact standalone observation" (`session-capture-phase3.md:183`).

**Harness snapshot rules** (`claude.go:60-72`). User scope `CLAUDE.md settings.json settings.local.json launcher-settings.json keybindings.json rules/** skills/** agents/** commands/** output-styles/** hooks/** plugins/installed_plugins.json`. Project scope nested `CLAUDE.md`, `CLAUDE.local.md`, `AGENTS.md`, `.claude/{settings*.json,rules,skills,agents,commands,output-styles,hooks}`, `.mcp.json`. Managed settings at `/etc/claude-code/*` and `/Library/Application Support/ClaudeCode/*` (`claude.go:58-65`). Plugins come from `plugins/installed_plugins.json` `installPath` (`claude.go:340-385`). MCP part of `.claude.json` only (`mcpServers`, plus per-project `enabledMcpjsonServers disabledMcpjsonServers allowedTools`) (`claude.go:278-327`). Memory dirs: `agent-memory`, `projects/<slug>/memory`, `<project>/.claude/agent-memory` (`claude.go:330-340`).

**Known hazards.**
- `~/.claude.json` churns on every start. Handoff says 206 epochs in 8 days (handoff "resume here", item 4). Treat it as a snapshot artifact with an interval, not an append stream.
- `Stop` does not fire on interrupt; `StopFailure` exists for API errors; `SubagentStop` carries `agent_transcript_path` (doc `hooks.md:73-77`).
- Transcript lags the in-memory conversation when a hook fires, which is why legacy waited 3 s (`hook.go:36 syncDelay`).
- Claude deletes transcripts, subagent files, tool results, plans and file history after 30 days unless `cleanupPeriodDays` is raised (doc `hooks.md:80`).
- Duplicate sid across slugs is resolved by name order, which is arbitrary but stable (`claude.go:162-176`). **Inferred** hazard. I found no test that pins a real duplicate.
- Subagent files need five path parts. A shorter path silently falls to `aux` (`claude.go:131-137`).

### 2.3 Codex

**Roots.** `$CODEX_HOME` else `~/.codex`. Skip dirs `plugins/cache bin packages` (`codex.go:41`). Managed config at `/etc/codex/{config,requirements,managed_config}.toml`.

**Discovery.**
- Rollouts. Regex `rollout-<YYYY-MM-DDTHH-MM-SS>-<36-char thread uuid>[_<36-char rollout uuid>].jsonl[.zst]` under `sessions/` or `archived_sessions/` (`codex.go:10-13`). Key is `rollouts/<name without .zst>` with date folders dropped. Session key is that name minus `.jsonl` (`codex.go:65-83`). Doc says Codex recompresses rollouts idle for 7 days to `.jsonl.zst` and decompresses on resume (`hooks.md:107`). Hashing the decompressed bytes is what keeps the key stable. `Zstd` records that the source was compressed.
- History: `history.jsonl`, `session_index.jsonl` both role `history`, session-less.
- Config: `config.toml hooks.json AGENTS.md AGENTS.override.md requirements.toml managed_config.toml rules/ skills/ prompts/ plugins/` role config. `memories/` memory, `log/` log (`codex.go:14-19`).
- **SQLite stores** under root, found by suffix. Doc lists `state_<n>.sqlite` (titles, token totals, sandbox and approval mode), `memories_*`, `goals_*`, `thread_history_*`, `queue_*` (`hooks.md:110`). `logs_*.sqlite` are excluded as noise (`policy.go:136`). "Paginated" history mode keeps some thread metadata only in SQLite (doc, `rollout/src/metadata.rs:315` of Codex). These are session-less, shared across all threads.

**Identity.** The thread UUID in the filename is the identity (`codexRolloutRE` group 1). Forked rollouts replay parent `session_meta` headers before or after their own. `ownCodexEvents` keeps only `session_meta` events whose `payload.id` equals the filename UUID (`metadata.go:46-55`), and `codexHead` deletes `started_at` if no own header was seen (`metadata.go:27-44`). Lineage: `forked_from_id` gives `fork`; `parent_thread_id` or `source.subagent.thread_spawn.parent_thread_id` gives `child_thread` (`codex.go:118-140`). Info fields: `id, session_id, cwd, cli_version, originator, forked_from_id, parent_thread_id, agent_role`, git `branch commit_hash repository_url`, `turn_context.model` (`codex.go:22-30`). Commit `9e32155` made it keep the first non-empty value; `b54b62c` filtered replayed headers from tail observations.

**Shared multi-session state.** `history.jsonl`, `session_index.jsonl`, and every Codex SQLite store hold rows for all threads. There is no per-session slice. Capturing them whole means one excluded session's rows ride inside every capture. That is why the exclusion rule is broad (section 4).

**Hazards.**
- `thread_history_1.sqlite` is rewritten in place. Handoff measures about 205 MiB of new epoch per Codex run, 6 epochs and 1,232 MiB on the Mac (handoff "resume here", item 4). A whole-file epoch model multiplies storage on every rewrite.
- Hook trust is a per-hook hash of the command string stored in `config.toml` under `[hooks.state."<hooks.json path>:<event>:<group>:<handler>"]` (doc `hooks.md:96-99`). Changing the command, including a new wrapper prefix, voids trust. Handoff records BC-01 Codex 0.163.0 trust stale (handoff status table). An operator must re-approve in `/hooks`. Never write `trusted_hash` from outside (handoff "working rules").
- Hooks run via `$SHELL -lc`, so the login profile runs first (doc `hooks.md:105`). `SessionEnd` is synchronous with a 3 s cap and its stdin may stay open (doc `hooks.md:28`).
- `transcript_path` is null for sessions Codex does not persist (doc `hooks.md:107`).
- Defining `[hooks]` in both `config.toml` and `hooks.json` makes Codex run both and warn (`install.go:360-363`).
- Two rollouts with the same logical key in `sessions/` and `archived_sessions/` collide on key. Legacy resolved this with alias groups (equal bytes share authority, proven prefix chooses longer, divergence is a reported conflict) (doc `storage-layout.md:~165`, `syncer/aliases.go`). Candidate has its own alias handling. **Inferred** that the rewrite must handle it.
- Codex truncates very large command output before writing it to the rollout in paginated mode (doc `hooks.md:110`). Nothing the collector can fix.

### 2.4 Pi

**Roots** (`pi.go:48-72`). `$PI_CODING_AGENT_DIR` else `~/.pi/agent`; plus `$PI_CODING_AGENT_SESSION_DIR`; plus an **absolute** `sessionDir` from `<agentdir>/settings.json`. Relative `sessionDir` resolves per project and is reported by hooks. Roots inside the agent dir are dropped to avoid double walk.

**Layouts** (`pi.go:130-215`). Agent-dir layout `sessions/--<cwd>--/<ts>_<uuid>.jsonl`. Timestamp shape is `piSessionDirRE` `^\d{4}-\d{2}-\d{2}T[0-9:.Z+-]+_<uuid>$`. A custom root can also hold flat `<ts>_<uuid>.jsonl` files. Children of a validated parent `<ts>_<uuid>/`:
- `tasks/*.jsonl` and `forks/*.jsonl` role subagent;
- `<uuid>/run-N/session.jsonl` role subagent, `N` includes zero (commit `1a1a0eb`);
- anything else under the parent dir is aux.
`subagent-artifacts/` is aux with no session link (doc `storage-layout.md:~262`).

**Identity.** Session key is `<prefix><cwd-dir>/<ts>_<uuid>` where prefix is `""` (canonical), `roots/<tag>/` or `dirs/<tag>/`. Info comes from the first `type: "session"` event: `id, cwd, version, parentSession`; lineage `fork` from `parentSession` via `piParentID` (`pi.go:225-250`, `metadata.go:75-94`). Child lineage relation `task|fork|run` comes from the path, parent id from the header when present (`piChildLineage`, `metadata.go:58-74`).

**Other discovery.** `settings.json models.json models-store.json mcp.json keybindings.json AGENTS.md AGENTS.override.md CLAUDE.md SYSTEM.md APPEND_SYSTEM.md extensions/ prompts/ skills/ themes/ tools/` config; `experimental/`; top-level `*.log` (e.g. `pi-debug.log`) log; skip `bin migration-backups` (`pi.go:15-20,41`). Harness snapshot adds `npm/package.json`, `npm/package-lock.json` and a synthetic package provenance record for `git/` checkouts, with commit and dirty state observed in a disposable config-free Git repo, no filters, no hooks, scrubbed `GIT_*` env (`adapters/packages.go:30-60,160-180`, `pi_snapshot_git.go:140-150`).

**Hazards.**
- An arbitrary nested `.jsonl` must not become a main session. `InferRoot` demands either a recognised marker or Pi's own timestamp/UUID filename (`pi.go:76-105`; commits `b0b4f9c`, `c8fdf7a`).
- Pi renames its process, hiding argv from the process table, so the extension passes `process.argv` (doc `hooks.md`, Pi section).
- The session file is created only after the first user or assistant message, so an early event can name a missing file. `--no-session` gives no file (doc).
- Pi awaits extension handlers, so they must return fast (doc `hooks.md`, Pi section). Legacy extension spawns detached (`install/setup.go:24-56`). Candidate extension spawns with a 2.5 s SIGKILL timeout (`capturehooks/pi.go:14-30`).
- Pi `models.json` carries provider `apiKey` and `headers` (scrub table, section 4).

### 2.5 Launch records and harness snapshots

- **Launch envelope** (`launch/launch.go:15-24`). `{schema 1, id, agent, path, recorded_at, payload}`. ID is `rand.Text()` assigned before persistence so retries keep identity (`hook.go:253`). Payload from a SessionStart hook is `event, source, cwd, permission_mode, hook_input (whole stdin JSON), process {argv scrubbed, pid, ancestry}, env` (`hook.go:239-250`).
- **Process walk.** Up to 10 ancestors; `isAgent` looks at the first 3 argv tokens for the binary or npm package marker (`claude`/`@anthropic-ai/claude-code`, `codex`/`@openai/codex`, `pi`/`pi-coding-agent`) and rejects `agent-sessions` (`hook.go:139-170`). Linux reads `/proc/<pid>/cmdline` and `/stat` (`proc_linux.go`), macOS uses `sysctl kern.procargs2` and `kern.proc.pid` (`proc_darwin.go`). Commit `9621004` replaced `ps` with sysctl.
- **Legacy env allowlist.** Prefixes `CLAUDE_CODE_ CODEX_ PI_ ANTHROPIC_DEFAULT_` plus a few names, minus names containing a secret word (`hook.go:44-47,195-205`). **Candidate allowlist is exact-name** and capped at 8 entries (`launch/native.go:~50`). Candidate argv keeps only a flag allowlist (`--resume --continue --no-session --model -m --provider --permission-mode --session-id --session --config --config-dir --cwd ...`), 32 args max, everything else `[redacted]` (`launch/native.go:15-47`).
- **Harness snapshot** (`snapshot/snapshot.go:29-37`). Limits: file 5 MiB, 3,000 files per scope, 5,000 dirs and 12,000 entries walk per scope, 3,000 skipped entries, 64 MiB total, import depth 5, bundle 128 MiB, bundle metadata 8 MiB. Scopes `user home project ancestor managed plugin script import`, plus `auto/user/project` for the memory set (doc `storage-layout.md` snapshot section). Time label is `captured`, not `pre_session` (doc, commit around `a1b63da`).

## 3. Hooks and install

### 3.1 What gets installed

**Claude Code** (legacy `install/install.go:32-41`, doc `hooks.md:55-66`). Eight events written into `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR`): `SessionStart Stop StopFailure SubagentStop PreCompact TaskCompleted TeammateIdle` as `{"type":"command","command":"<shell-quoted exe> hook claude","async":true,"timeout":30}` and `SessionEnd` synchronous with timeout 5. No matcher. `cleanupPeriodDays` raised to 3650 if lower (`install.go:306-324`).

**Codex** (`install.go:43-50`). Six events in `~/.codex/hooks.json`: `SessionStart Stop SubagentStop PreCompact` async 30 s, `Interrupt` async 3 s, `SessionEnd` sync 3 s. The installer never touches `config.toml` trust. It logs "open Codex and run /hooks once" (`install.go:358`).

**Pi** (`install/setup.go:22-56`). `~/.pi/agent/extensions/agent-sessions.ts` (under `$PI_CODING_AGENT_DIR`), marker first line `// agent-sessions: managed by ...`. Handlers on `session_start` (passes `--event --source --cwd --argv`), `agent_settled`, `session_shutdown`. If a file without the marker exists it is left alone (`setup.go:91-97`).

**Candidate hooks** (`capturehooks/json.go:19-23`, `capturehooks/README.md`). Same event lists. Handler shell prefix `: agent-sessions-capture-v1; exec <exe> capture-v1 hook --config <cfg> --agent <agent>` owns each JSON handler, so ownership is a literal prefix, not a regexp. Pi uses a distinct filename `agent-sessions-capture-v1.ts` and a first-line prefix. Executable must be a regular, non-symlink, non-group/world-writable file. Config must be a 0600 regular file. All writes are fsynced with private backups. **It does not set `cleanupPeriodDays` and writes no trust.**

### 3.2 Hook payloads

- Claude and Codex pass JSON on stdin. Fields consumed: `transcript_path`, `agent_transcript_path`, `hook_event_name`, `source`, `cwd`, `permission_mode` (`hook.go:84-93,239-250`). Read is capped at 1 MiB and 2 s, and stops at the end of the first JSON object because a parent may leave stdin open (`hook.go:63-92`; commit `6a1c82c`).
- Pi passes everything as flags (`--path --event --source --cwd --argv`). Candidate Pi `session_start` instead writes a JSON payload with `event, source, session_file, session_id, cwd, model, provider, argv (32 x 256), context_origin` to stdin, max 64 KiB (`capturehooks/pi.go:35-50`).
- Hook must exit 0 and print nothing. Claude treats `Stop` exit 2 as "keep working". Codex fails a synchronous Stop whose stdout is not JSON (doc table `hooks.md:19-27`).
- Legacy hook then spawned `agent-sessions sync ...` detached with `Setsid`, cwd `/`, never cancelled (`hook.go:280-293`). Debounce marker per transcript, stale after 120 s (`hook.go:33,102-130`). Launches written atomically to `pending/launch-<id>.json` so a later sync that gets the lock records them (`hook.go:253-260`, commit `d6e0778`).
- **Candidate hook** has a 2 s total context (`cli/capture_runtime_hook.go:25-27`), writes a durable hint and native evidence, and exits. A pathless payload writes a discovery wake (`:41-43`).

### 3.3 Trust, uninstall, doctor

- **Codex trust hash** (`install/trust.go:17-70`, doc `install.md` "Doctor and Codex trust"). SHA-256 prefixed `sha256:` over recursively key-sorted compact JSON of `{event_name, hooks:[{type, command, timeout, async, statusMessage?, additionalContextLimit?}], matcher?}`. Timeout defaults 600, minimum 1, `SessionEnd` and `Interrupt` default 1 and cap 3. `Stop` and `Interrupt` ignore matcher. U+2028 and U+2029 fail closed as "not verified". Doc cites `openai/codex@806d973`. **Doc says** it was checked against 0.162.1; the handoff reports a 0.163.0 trust stale, which was fixed by operator re-approval, not by recomputing (handoff "working rules"). Any algorithm drift is therefore an open risk.
- **Settings merge** (`install.go:201-275`). Replace our entries in place (matched by `Ours` regexp `(agent-sessions|agent_sessions).*\bhook (claude|codex|pi)\b`, `install.go:23`), keep foreign hooks, never rewrite a file that does not parse (`loadJSON`, `install.go:159`), back up first as `<path>.agent-sessions-backup-<YYYYMMDD-HHMMSS>` (`install.go:93-110`). "In place" matters because Codex keys trust by position, so moving a hook voids approval (doc `hooks.md:140`, commit `23a7be0`).
- **Codex schema gate** (`install/codexschema.go:18-150`). Whole-document admission: unknown top-level fields or malformed known fields can prevent every hook loading. The installer refuses to rewrite a document that fails (commit `eadbfef`).
- **Uninstall.** `Remove` mode deletes only matched entries, deletes `hooks.json` if empty (after a backup), removes the Pi extension only if it carries the marker. Missing files are left alone (commit `884f597`). `install.sh uninstall` runs the installed binary's `uninstall` first and only then removes the binary. It preserves config, credentials, machine-id, state, backups and all remote objects (doc `install.md`).
- **Doctor** (`doctor/doctor.go`). Fails when `AGENT_SESSIONS_DISABLE` is set (`:71`), when Claude `disableAllHooks` is set, `cleanupPeriodDays` low (`:146-155`), Codex `[features] hooks = false`, Codex trusted count below configured (`:177`), Pi extension missing or stale (`:202-206`), hook binary not runnable (`:253-260`), no recent sweep (`:287`).

### 3.4 Services, sandbox

- **Legacy had no scheduler.** Commit `ae88967` (2026-10-13) removed the systemd timer, launchd agent and cron line and replaced them with a sweep started by each session start and end. It also made install and uninstall remove the old timers and made doctor fail while one remained. Lesson in section 7.
- **Candidate service** (`capturedaemon/service.go:31-70`). macOS LaunchAgent `Library/LaunchAgents/<ServiceName>.plist` with `RunAtLoad`, `KeepAlive{SuccessfulExit=false}`, `ThrottleInterval 300`, `ProcessType Background`, `Umask 63`. Linux systemd user unit `Restart=on-failure`, `RestartSec=60`, `RestartSteps=4`, `RestartMaxDelaySec=900`, `StartLimitBurst=5` per hour, `UMask=0077`, `WantedBy=default.target`. Restart commands `launchctl kickstart -k` and `systemctl --user restart`. Handoff reports the Mac unit under `launchctl` and BC-01 under systemd user (handoff status table).
- **Sandbox constraints that mattered.**
  - Hooks run with the agent's environment, not the user's shell. A shell `export` may not reach them, which is why the legacy design read `config.env` (`config/config.go` header, doc `machine-identity.md`).
  - Hook timeouts are hard: Claude `SessionEnd` 1.5 s native, Codex 1 s default and 3 s cap (doc `hooks.md:24`).
  - Codex kills the hook's process group on timeout and waits for its stdout to close, which is why detached children use their own session and null stdio (doc `hooks.md:25-26`).
  - A hook inside Codex's `read-only` exec sandbox still wrote the inbox and queue (handoff probe, "Codex hook probes", 09:00 to 09:06Z). So the model's sandbox does not confine the hook. **Doc says**, from a probe. I did not re-run it.
  - macOS Background QoS changes coordinator CPU about 3x (handoff working rules).
  - Handoff notes `unshare -rn` blocked by AppArmor on BC-01 and BC-01 has no `sqlite3` CLI. Operational, not code.

## 4. Scrub and exclusion

### 4.1 Scrub

**Which files.** The single rule table `ruleTable` (`scrub/scrub.go:94-106`) lists 11 base names: `claude.json .claude.json settings.json settings.local.json managed-settings.json .mcp.json mcp.json managed-mcp.json models.json config.toml managed_config.toml`. Selection is by filename of **every** identity in a symlink chain, never by collecting agent (`Identities`, `scrub.go:156-170`). Names are case-folded and repeatedly stripped of known backup suffixes (`~`, `.~N~`, `.bak .backup .orig .old`, `-YYYYMMDD[-HHMMSS]`, `.before-<label>`, `.agent-sessions-backup-<time>`, archive extensions) by `capturepolicy.BaseName` (`policy.go:81-126`). So `settings.json.bak` is scrubbed like `settings.json`.

**Rules (patterns, `*` is one segment).**
- Claude JSON: `primaryApiKey` (nothing kept), `customApiKeyResponses.*` (nothing), and MCP rules at the top, under `projects.*.`, and under `project.` (the snapshot extract) (`scrub.go:58-63`).
- MCP rule set: `mcpServers.*.env.*` and `.headers.*` keep references, `.url` keeps URL without credentials, `.oauth.clientSecret` nothing, `.args` keeps argv with secret flags redacted (`scrub.go:42-48`).
- Settings: `env.*` keep references, `hooks.*.*.hooks.*.headers.*` keep references.
- Pi `models.json`: `providers.*.apiKey` keep references, `!command`, and env-var names; `providers.*.headers.*` keep references and `!command`.
- Codex TOML: `mcp_servers.*` env, http_headers, url, bearer_token, args; `model_providers.*` http_headers, experimental_bearer_token, base_url; `shell_environment_policy.set.*`; `otel.*.*.headers.*` (`scrub.go:69-80`).

**What survives as "not a secret"** (`holdsNoSecret`, `scrub.go:374-395`). Empty, `[redacted]`, `true false yes no on off`, numbers, and strings made only of `$VAR` or `${VAR}` references joined by the words `bearer basic token`. So `Bearer ${TOKEN}` is kept and `Bearer abc` is redacted.

**Secret-name test** (`IsSecretName`, `scrub.go:201-207`). Judge the last dotted segment, lowercase, split on `-` and `_`. Whole words `token key auth`. Substrings anywhere `secret passw passphrase credential cookie apikey authorization`. So `MAX_THINKING_TOKENS` is not secret (word is `tokens`), `--client-secret` is.

**Argv scrubber** (`Args`, `scrub.go:211-232`). Redacts the value after `=` or the next word for secret-named flags, redacts `-H`/`--header` values unless they are pure references, and scrubs inline JSON arguments with the settings and MCP rules. Unparseable inline JSON becomes `[redacted]`.

**URL** (`redactURL`, `scrub.go:~395-420`). Drops a userinfo password, replaces each query value that is not a reference with `[redacted]`. A URL that fails to parse or has no host becomes `[redacted]`.

**Algorithm.** Decode with duplicate-name rejection, walk with rules collecting a map `original -> replacement`, then try an in-place byte replacement of every JSON-quoted spelling of each original (plain and escaped), skipping occurrences that are object keys (`replaceNonKeys`, `scrub.go:~500-525`). If any original is not found in place, or a copy of a secret remains after replacement, **re-encode the whole document** with all equal decoded string values replaced (`replaceCopies`, `copies.go`). This is the equivalence class rule: every string value equal to a redacted secret is redacted everywhere, never keys. Re-encode loses comments and layout (comment at `scrub.go:187-188`).

**Fail closed.**
- Any protected file that does not parse is withheld, not copied raw (`File`, `scrub.go:128-139`). JSON must be an object, have no duplicate decoded names (including escaped), no invalid Unicode, no trailing document (`decode`, `scrub.go:245-265`, uses Go 1.27 `jsontext`). TOML errors return a fixed string because parser diagnostics can echo an unquoted credential (`scrub.go:249`).
- Streaming capture must check `Recognizes` for every resolved identity before choosing a raw byte path (doc `security.md`). A Claude skill symlinked to Pi `models.json` is still scrubbed.
- `RulesDigest` hashes the table, and capture binds it into transformed-source fingerprints so a rule change forces re-prepare (`scrub/digest.go:17-27`, commits `fe9aedf`, `1ac6223`, GAP-11). Logic outside the table (the secret-name test, `scrubDoc`) is not covered by the digest (comment at `digest.go:10-14`).

**Literal assignment guard** (`Text`, `scrub/text.go:44-61`). Withholds a file (not redacts) when small (at most 1 MiB), valid UTF-8, no NUL, and a command-position line assigns a literal to a secret-named variable. Recognises `NAME=value`, `export NAME=value`, and fish `set -gx|-xg|-g -x|--global --export NAME value`, with complete quoted spellings, after `;`, `|`, `&`, `and`, `or`. Pure references stay allowed. Skips files reached only through names with source extensions (`.py .js .ts .go .rs ...`, `text.go:25-32`) because keyword args like `key=lambda ...` look like shell assignments (commit `ffb6726`). Returns `ErrLiteralSecret` (`literal_secret_assignment`), a deliberate skip, not a retry. Never applied to main, subagent or superseded transcripts. It is explicitly not DLP (doc `security.md`, `storage-layout.md`).

**Gaps worth knowing.** Unrecognised filenames are byte-exact, even with credentials inside. Transcripts are never scrubbed. Oversize or binary text is not checked. Renamed copies with no recognised path identity are outside policy (doc `security.md`).

### 4.2 Exclusion and the opt-out record

- **Trigger.** `AGENT_SESSIONS_DISABLE` non-empty in the agent's environment when a hook fires. Legacy `hook.Run` records an exclusion for each `transcript_path` and `agent_transcript_path` and returns without capturing (`hook.go:308-311`). The candidate hook does the same through `runDisabledNativeCaptureHook`, which resolves metadata only, never opens the queue or reads source bytes, and fails if the policy dir is missing or unreadable (`cli/capture_runtime_disabled.go:19-72`, `capture_runtime_hook.go:28`). A hook with no path logs "future capture cannot be suppressed" (`hook.go ~340`).
- **Record** (`exclusion/exclusion.go:27-38`). `{schema 1, id, agent, root, resolved_root, session_key, paths[], recorded_at}`. `id = sha256(agent \0 resolved_root \0 sessionName(key))` where `sessionName` strips a `roots/<tag>/` or `dirs/<tag>/` prefix (`exclusion.go:104-107,58-67`). So the same session under different root spellings maps to one ID.
- **Persistence.** One file per record at `<stateDir>/exclusions/<id>.json`, compact JSON, 0600, written with `WriteFileAtomic` (`exclusion.go:170-180`). Candidate uses `PolicyDir` instead of the state dir (`capturedaemon/config.go:37,138`). Repeated observations merge `Paths`, keep the earlier identity, and never clear (`recordFile`, `exclusion.go:126-155`). There is no delete code path. Record paths include the original and the canonical (symlink-resolved, resolving existing ancestors) forms because early hooks name files not yet created (`canonical`, `exclusion.go:41-56`).
- **Load fails closed.** Any unreadable, undecodable or invalid record makes `Load` return an error (`exclusion.go:182-205`). The candidate treats that as "effective claimed-session exclusions unavailable" and denies capture; test `TestRuntimePrivacyFailClosedAndConfigurationIdentity` drops a `{` file into the policy dir and expects denial (`capture_runtime_privacy_test.go:~218-226`). The test also pins that setting `AGENT_SESSIONS_DISABLE` in the policy-evaluating process denies everything.
- **Remote.** Legacy mirrors records to `v3/machines/<m>/exclusions/<sha>.json` and restores the union before any capture (`syncer/recovery.go:142-183`). Candidate: no remote write found (measured by grep). The contract treats remote-only exclusions as something to preserve.
- **Match rule** (`Registry.Excludes`, `exclusion.go:211-232`). A file is excluded when some record has the same agent and the same root (exact string or `SameLocation` of resolved roots), and (the file's `SessionKey` is empty **or** equals the record's `sessionName`). Or when any record path aliases the file's source path. This is the over-broad rule. An empty key matches every record in that root.
- **Over-broad Codex behaviour, as the handoff describes it.** One Codex exclusion record on the Mac denies all Codex shared resources, about 7,900 active `policy_denied` issues, no Codex resource captured since 12:02:55Z, and the record must not be deleted because the shared SQLite stores contain the excluded session's rows (handoff status table, Privacy row). **Measured** that the code path is `sf.SessionKey == ""` above. It is intended, not a bug in the narrow sense, and pinned by `TestRuntimePrivacyScopesAggregatesAndUntrustedIdentity`.

## 5. Safe writing, locking, fencing, atomic idioms

| Idiom | Where | What problem it solved |
|---|---|---|
| Temp file in the same dir, `chmod`, `fsync` file, `rename`, `fsync` dir | `fsutil/fsutil.go:16-60`, copies at `capturedaemon/config.go:326-346`, `sessioncapture/runtime_inbox.go:70-76`, `capturehooks/apply.go:99-133` | Readers never see a partial file. Directory sync is best effort because some filesystems cannot (`fsutil.go:54-56`). |
| Create private file then `os.Link` into place, ignore `EEXIST`, read back the winner | `config/config.go:178-200` (machine id), `store/catalog.go:176-205` | Two concurrent explicit creations share one ID, and a crash cannot leave an empty identity file. Commit `ccc4aed` fixed "two runs starting together could each pick a different machine id, and an empty machine-id file gave a new id every run." |
| `O_CREATE|O_EXCL` marker with 120 s stale takeover | `hook/hook.go:102-130` | Debounce one queued sync per transcript, recover from a sync that died. Not a durable queue (comment at `hook.go:100-101`). |
| `flock` on a lock file | `state/lock.go:16-31` (250 ms retry, timeout 30 s hook, 60 s sweep), `sessioncapture/queue.go:187`, `runtime_inbox.go:119-152`, `cli/capture_runtime_policy_pauses.go:47` | One writer per machine and destination. Legacy lock dir is `~/.local/state/agent-sessions-locks/<streamID(destination, machine)>` and is deliberately **independent of the selectable state dir**, so two state dirs cannot run two writers (`syncer/recovery.go:474-481`). |
| `openat` chain from `/` with `O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK`, compare `os.SameFile` to an earlier `Lstat` | `safefile/open.go:41-86` | Close the gap between "authorize path" and "read bytes". `ErrReplaced` on mismatch. Directory FD budget `depth+1` in `walk.go`. |
| Read through a bounded `LimitReader` and reject non-regular files, open with `O_NONBLOCK` | `adapters/claude.go:400-430` (5 MiB) | A config swapped for a FIFO or grown between stat and read. |
| 0600 files and 0700 dirs, created 0600 before SQLite opens them | `state/state.go:151-158`, `adapters/sqlite.go:83-88` | Commit `ccc4aed`: "state.sqlite3 and database backup copies were created world-readable." |
| `Setsid` detached child, `cmd.Dir = "/"`, never cancelled | `hook/hook.go:280-293` | Outlive a hook that Claude or Codex tears down. |
| Stat-signature (size, mtime of db and `-wal`) plus hourly minimum to decide whether to re-export | `adapters/sqlite.go:42-70` | Avoid re-copying hot Codex DBs every sweep. |
| Backup via SQLite API into `.partial` then rename, 2 min context | `adapters/sqlite.go:79-127` | Rows in `-wal` included, no partial copy at the real name. |
| Stable-pair check: observe DB+WAL identities, pin FDs, backup, re-hash and re-observe, typed `ErrSQLiteSourceChanged` for any change | `adapters/sqlite_authorized.go:70-200` | A live writer. Commits `ce6ef2d`, `4ce3d33` (same-size same-mtime rewrite), `79eef7d` (WAL-mode header with no `-wal` sidecar made SQLite fail `SQLITE_CANTOPEN`, quarantining the source; fixed by opening immutable only when WAL absence is verified). Rollback-journal databases return `ErrSQLiteRollbackJournal` and are never raw-copied (`sqlite_authorized.go:34`). |
| Read-only SQLite URIs for diagnostics | handoff working rules | Do not disturb queue DBs. |
| Backup-then-write-in-place for user settings | `install/install.go:93-157` | Never lose a user's `settings.json`. Refuse to write a file that did not parse. |

**Not a fix, but worth knowing.** `fsutil.WriteAtomic` uses `Sync()`. On macOS Go's `File.Sync` uses `F_FULLFSYNC`, and Rust's `File::sync_all` does too (**inferred** from std behaviour, not read here). `config.go` has no fsync of the temp-then-link path beyond `WriteFileAtomic`.

## 6. What the legacy v3 archive got wrong (and the contract rejects)

**Concurrent cold recovery.** `Recover` is mandatory machine-wide before any capture (`syncer/recovery.go:186-226`) and runs under the single machine lock (`cli/commands.go:171`). In a cold or mismatched checkpoint state it enumerates every remote record, validates schemas, body hashes and dependency HEADs, then reconciles every stream (`recovery.go:205-226,484-500`). A hook sync for session A waits behind it, with a 30 s lock timeout (`cli/commands.go:69-78`). Attempts to speed this up by verifying records concurrently (`c33c644`) and to tolerate pruned fold pieces (`84a15c4`) live only on side branches `fix/cold-recovery-pruned-pieces` and `fix/log-failed-sync` (`git branch --contains`, measured), not on `main`. They patch a design whose recovery cost is proportional to all historical metadata (doc `archive-design.md`, "Limits"). Contract: session A must not need machine history.

**Pruned pieces.** On 2026-10-19 the operator hand-pruned 1,323 pieces (260 MB, four Pi transcripts) that folds had replaced (doc `storage-layout.md`, commit `2d43362`). The v3 promise says every committed generation stays reconstructible, and the doc says the claim that readers rebuild missing historical pieces from a later prefix "was not an established deployed recovery guarantee". So the archive promised "every intermediate version" and the operator then deleted objects those versions needed. The contract drops intermediate versions as a requirement (`contract.md` section 1: "not every collector observation or chunk layout").

**Chunk folding.** Appends chain up to 64 pieces, then `foldFrom` merges the last two and every earlier piece no bigger than twice what is merged so far, never the base (`syncer/prepare.go:624-660`, `maxSegments = 64` at `syncer.go:35`). A first version recompressed nearly all bytes at every 64th append (commit `ffb6726` message). The fold exists to bound a replay chain the design itself created. The contract replaces this with full compressed source-epoch objects and "chunking is an optional measured optimization" (`contract.md` section 4). Database snapshots also chunked at 1 MiB for 8 MiB and up (doc `storage-layout.md`). **Doc says**, not re-measured.

**Retired schema compatibility.** The reader accepts schema 3 manifests (copied generations, quadratic growth), 4 (inline launches and mtimes) and 5 (launch references, mtimes blob), and republishes a schema-3-only session under the new kind (`syncer/publication.go:29-37`, `validation.go:20,71,102`, `recovery.go:502-520`; doc `storage-layout.md` manifests section). The envelope stays schema 3 and the namespace `v3/` while payloads are schema 5 (`research/session-capture-contract-review.md:~219`). That muddle of version dimensions is itself the lesson. Also in the support packages: `launch.Decode` accepts a pre-envelope flat format and hashes its bytes for identity (`launch/launch.go:51-75`), and `state.Open` migrates a `disk_size` column and back-fills `observations` (`state/state.go:179-200`). All of this exists only to read older local or remote data. A rebuild from sources does not need it.

## 7. Lessons for the Rust rewrite

Each lesson names the Go evidence.

1. **Make the session key a typed value with an explicit `Shared` variant, and make exclusion scope a function of that type.** Go uses `SessionKey == ""` for both "agent-wide file" and "unknown owner" (`adapters/base.go:65`). `Excludes` then treats both as matching every record (`exclusion.go:215`). The Claude test and the Codex handoff row are the same bug. In Rust, `enum Owner { Session(Key), Shared(ResourceClass), Unknown }`, and let policy choose per class (deny, capture-with-row-redaction, or capture-and-warn). The foundational-thinking principle (data shape first) applies directly.
2. **Port adapters as pure data plus small pure functions, with the Go tests as the oracle.** `claudeRoles`, `codexRoles`, `piRoles`, the three regexes, and `piSession` are tables (`claude.go:30-49`, `codex.go:14-20`, `pi.go:14-20,141-163`). Commit `cb9f55f` ("Describe each agent's fixed layout as data") already did this in Go. The adapter tests are about 2x the code. Port them as table-driven fixtures first.
3. **Keep identity, root and namespace derivation in one place.** `keyPrefix`, `rootTag`, `sessionName` and `canonical` are implemented separately in `adapters` and `exclusion` and must agree (`base.go:203-218`, `exclusion.go:58-67`). Disagreement means an opt-out silently misses. One `Identity::derive(path, roots)` used by discovery, hook, exclusion and policy.
4. **Resolve the three discovery caches before writing any walker.** Claude's per-session directories need a sid-to-slug index (`claude.go:144-176`), Pi needs a validated-parent check (`pi.go:76-130`), Codex needs the filename UUID for header filtering (`metadata.go:46`). Candidate commits `9725302` and later `perf/discovery-*` branches show the cost of re-deriving these per file.
5. **Never reopen a path after authorizing it.** `safefile.OpenResolved` plus an expected `FileInfo` is the one pattern repeated in candidate source reads, snapshots, SQLite and enrichment (`safefile/open.go:25`, `adapters/sqlite_authorized.go:70`, `snapshot/options.go:120-137`). In Rust, use `rustix` or `nix` `openat2` or an `openat` chain with `O_NOFOLLOW`, and carry an `Authorized<File>` newtype so the type system forbids a raw `File::open` in capture code.
6. **Do not rely on a custom SQLite VFS hack.** `adapters/sqlite_authorized_vfs.go` patches a pure-Go driver's C ABI through `unsafe` (`:22-60`). It exists because the pure-Go driver gives no way to open a pinned descriptor. In Rust, `rusqlite` over a pinned fd (`sqlite3_open_v2` with a custom VFS, or the `immutable=1` and `file:/dev/fd/N` approach) needs a spike first. Keep the stable-pair proof (observe, backup, re-hash, re-observe) and the typed `SourceChanged` error. Label: guess for the Rust approach, measured for the Go constraint.
7. **Treat `~/.claude.json`, Codex SQLite stores and `history.jsonl` as periodic snapshots with a minimum interval and a retention rule, not as transcripts.** Legacy already refreshed them at most hourly (`syncer.go:~50 recopyAfter`, `adapters/sqlite.go:44-52`). Without a retention rule the handoff measured 206 epochs in 8 days for `.claude.json` and about 205 MiB per Codex run for one SQLite store. This is an operator decision (section 8) and the rewrite should make it a policy field, not a constant.
8. **Port `scrub` with a parser that rejects duplicate keys and preserves layout.** `serde_json` accepts duplicate keys silently (last wins), exactly the failure the Go comment calls out (`scrub.go:253-257`). Use a validating pre-pass or a streaming parser that errors on duplicates, and `toml_edit` to keep TOML layout instead of re-encoding (`scrub.go:187-188` accepts layout loss). Keep: filename-driven schema selection across all identities, the equivalence-class replacement, fixed-string parse errors, `RulesDigest`.
9. **Fold the `RulesDigest` idea into every transform.** A rule change must invalidate transformed fingerprints. Today the digest covers only the table and not the logic (`digest.go:10-14`). In Rust, version the whole scrub module and include that in the transform fingerprint.
10. **Own an autonomous scheduler from day one, and keep hooks as hints only.** Legacy had no timer (`ae88967`), so a crashed session waited for the next session to start. `hook.go:100-101` says markers are "not a durable capture queue". The candidate fixes this with a coordinator and a launchd or systemd unit (`capturedaemon/service.go:31-70`). Keep hook budgets: 2 s read, exit 0, print nothing.
11. **Port the installer's three safety rules, drop its regexp ownership.** Parse-or-refuse, backup-before-write, replace-in-place, and Codex whole-document schema admission (`install.go:93,159,201`, `codexschema.go:18`). Ownership by literal prefix (the candidate's `: agent-sessions-capture-v1; exec `) beats the legacy regexp `Ours` because it cannot match a foreign hook that happens to mention `hook claude`. Also **port `cleanupPeriodDays`** (finding 4) and decide whether the installer or `doctor` owns it.
12. **Keep Codex trust as a read-only verification.** The installer never writes `trusted_hash` (doc `hooks.md:96`, handoff rule). Doctor recomputes it from `hooks.json` using the algorithm at a pinned Codex commit (`trust.go:17-70`). The handoff shows the algorithm or the command string drifted at least once (0.163.0 stale trust). Rust should treat "hash unverifiable" as a first-class doctor status, never "trusted". Hash stability depends on the hook command string, so a wrapper prefix change forces re-approval on both machines.
13. **Refuse to separate the exclusion registry from the shared policy load.** Legacy and candidate both read `<dir>/exclusions/*.json` with fail-closed `Load` and monotonic `Save` (`exclusion.go:170-205`). Keep these three properties as the registry's contract: monotonic, validated on load, unavailable means deny. Also decide remote mirroring (section 8).
14. **Subtract first.** Do not port `syncer`, `state`, `store`, `raw` wire types, schema 3/4/5 readers, `launch.Decode` legacy, `state.addDiskSize`, `config.sanitize` back-compat for existing machine IDs (keep the explicit-creation rule), or the CLI `sync/backfill` commands. This removes about 7,200 of roughly 16,500 in-scope non-test LOC before any Rust is written. That is the subtract-before-you-add principle applied to the port itself.
15. **Reduce reader load in the policy path.** The candidate stacks `capturepolicy`, `exclusion`, `runtimeComposition.privacyOwner`, alias maps and claimed-session checks (`cli/capture_runtime_policy.go:61-160`, plus `capture_runtime_alias_policy.go`, `capture_runtime_claimed_policy.go`, `capture_runtime_launch_policy.go`). A new reader cannot answer "why was this path denied" in 30 seconds. In Rust, make `decide(request) -> Decision { Allow | Deny(Reason) }` a single pure function over an immutable `PolicySnapshot` loaded once, and log the `Reason`. That is the minimize-reader-load principle.
16. **Port tests that pin hazards, not coverage.** Commit `ccc4aed` found data loss with a seeded simulation of "what agents and machines do to session files between syncs." Keep that idea for the new writer: append, truncate, rename, same-size rewrite, lost acknowledgement, killed mid-run.
17. **Do not reproduce the file naming mistakes of the wire.** `v3/` namespace with schema 5 payloads (section 6) shows that one version number per layer is not enough. Version the namespace, envelope, and payload separately and write them down.

## 8. Open questions and unverified items

### Open questions for the operator

1. **Shared stores policy.** Should Codex SQLite stores and `history.jsonl` (and Claude `history.jsonl`, `.claude.json`) be captured whole, row-filtered per session, or not captured at all while any exclusion exists? Handoff item 4 already lists it. The answer decides whether `Excludes` can stay as is.
2. **Remote exclusions.** Legacy mirrors opt-outs to R2 and the candidate does not. The contract says to preserve remote-only exclusions. Should the rewrite write them to the new namespace, read the legacy ones once at cutover, or treat local state as the only source?
3. **`cleanupPeriodDays`.** Should the new installer raise it to 3650 on Claude, leave the user's value and have doctor warn, or neither? The legacy installer changed a user setting without asking (`install.go:314-324`).
4. **Symlinks.** Legacy followed regular-file symlinks; the candidate gaps them. Are there live symlinks under `~/.claude`, `~/.codex`, `~/.pi/agent` on either machine whose content must be captured?
5. **Repo identity.** `gitinfo.RepoID` (normalised remote or machine plus git dir, hashed to 16 hex) is used only by legacy. Do analytics consumers need it?
6. **Epoch retention** for in-place rewritten artifacts. Already in the handoff. It decides storage for Codex DBs and `.claude.json`.
7. **Machine ID policy.** Keep the explicit-creation rule and the `machine=<ID>` namespace, or rely on the candidate's separate archive ID binding (`capturedaemon.Config`)? Doc `machine-identity.md` says IDs must not rotate through the rebuild.

### Things I could not verify

- **Native agent behaviour.** All Claude, Codex and Pi hook semantics, timeouts, rollout recompression after 7 days, `session_meta` fork replay, and Pi layout claims come from `docs/hooks.md` and code comments. I did not run an agent or read their sources. Label: doc says.
- **Codex trust algorithm currency.** The hash follows `openai/codex@806d973`. I did not check whether 0.163.0 changed it. Handoff says trust was stale on BC-01, but also says "the hooks work" under bypass, which does not distinguish drift from a changed command string.
- **Exported-surface counts** are a regex and will miscount multi-line signatures and grouped declarations.
- **Candidate parity.** I checked that candidate packages import `adapters`, `scrub`, `exclusion`, `launch`, `safefile`, `snapshot` and `hook.InspectNativeArgs`. I did not audit whether every legacy behaviour listed in section 2 is reproduced in the candidate. Two I confirmed differ: symlink handling and `cleanupPeriodDays`. Remote exclusions I found absent by grep only.
- **Whether `Related()` and the hint-driven path in the candidate collect `todos/`, `tasks/`, `teams/`, `debug/`, `session-env/` for Claude.** Candidate tests reject those top-level paths before classification in one privacy test (`capture_runtime_privacy_test.go:~191-198`), which is about privacy ownership, not about whether the artifacts are captured. Needs a read of `capturesource/ownership.go`.
- **Concurrency behaviour** of the legacy sweep versus hook (lock timeouts 30 s and 60 s) is from `lockTimeout` and doc, not from a run.
- **Legacy gitinfo and snapshot git calls** use `git` subprocesses with 10 to 20 s timeouts. I did not check how the candidate bounds git. Label: unverified.
- **A stray placeholder file** was created at `/private/tmp/claude-501/-Users-mm/dev-memory/bcdf3267-4f81-4da3-9ef6-cea1f254476c/scratchpad/reports/03-legacy-and-support.md` through a mistyped path. A permission rule blocked removing it. It is a one-word file in the session scratchpad and is harmless.
