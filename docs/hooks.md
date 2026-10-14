# How agent-sessions hooks into each agent

Checked on 2026-10-10 against:

- **Claude Code** v2.1.296, using the docs at https://code.claude.com/docs/en/hooks, `/claude-directory` and `/settings-reference`.
- **Codex** source at [openai/codex@806d973](https://github.com/openai/codex/tree/806d9732c974bc8a51b8317c1bd8985544fe627c), and the released CLI 0.162.1.
- **Pi** source and docs at [earendil-works/pi@42a3497](https://github.com/earendil-works/pi/tree/42a3497d03ad17e308a2299fa824727894f2c0ec) (formerly badlogic/pi-mono; `packages/coding-agent`), and the released `@earendil-works/pi-coding-agent` 1.1.0.

All three were also run for real (Claude Code 2.1.296, Codex 0.162.1, Pi 1.1.0) against a local fake model server, with agent-sessions installed into a scratch home, most recently on 2026-10-11. Each one's session-start hook recorded the agent's command line and a pre-session snapshot, and its turn hooks landed the session in the store byte for byte.

## One rule for every agent

The hook command is `agent-sessions hook <agent>`. It reads the hook's JSON from stdin, or takes `--path` from the Pi extension. It starts `agent-sessions sync --path <transcript> --delay 3` in a new session with stdin, stdout and stderr on `/dev/null`, then exits 0 without printing anything.

Why each part matters:

| Constraint | Source | What agent-sessions does |
|---|---|---|
| Claude Code treats `Stop` exit code 2 as "keep working" | hooks docs, Stop decision control | Always exit 0 |
| Codex fails a *synchronous* Stop hook whose stdout isn't JSON | `codex-rs/hooks/src/events/stop.rs:624` | Print nothing. Stop hooks are also `async`, which ignores stdout, so a login shell that prints a banner can't break them |
| Claude Code `SessionEnd` gets 1.5 s; Codex `SessionEnd` gets 1 s by default, at most 3 s, and can't be async | hooks docs; `session_end.rs:20-23`, `discovery.rs:533` | The hook returns in about 0.1 s. Timeouts are set to 5 s (Claude Code) and 3 s (Codex) as headroom for a cold interpreter |
| `claude -p` kills async hooks still running at teardown, and the docs say to "start a fully detached process" | hooks docs, "Run hooks in the background" | The sync runs in its own session (`start_new_session`) |
| Codex kills the hook's process group on timeout, and waits for the hook's stdout to close | `command_runner.rs:339`, `:413`, wait_with_output | Own session, so the group kill misses it; stdio detached, so Codex isn't left waiting. Codex's own comment at `:281`: "Successfully completed hooks may intentionally leave detached helpers running" |
| The transcript can lag the in-memory conversation when a hook fires | hooks docs, `transcript_path` field | The sync waits 3 s before reading. The next turn's hook or the next sweep catches anything later |
| Stop fires after every turn | all three | Debounce: a marker per transcript means only one sync is queued at a time. The queued one reads the file after its delay, so it includes the turns that arrived meanwhile |
| A parent can leave the hook's stdin open, and a read deadline can't be set on a blocking pipe | Codex `SessionEnd` is synchronous | Stop reading at the end of the JSON payload, and give up after 2 s if none arrives |

## The sweep at session start and end

Nothing runs on a schedule. When any agent's session starts (`SessionStart`, `session_start`) or ends (`SessionEnd`, `session_shutdown`), the hook also starts a sweep: `agent-sessions sync --trigger sweep:<event>` over every agent's directories, 3 s later so a session-start sync gets the lock first. It is what makes capture complete, together with `agent-sessions backfill` once at install. It picks up:

- the end of a session that crashed, was killed or was interrupted after its last hook;
- uploads and manifests that failed, for example while the machine was offline;
- files no hook names: databases (copied at most once an hour), `~/.claude.json`, settings and skills outside a session, history files;
- files deleted locally, which get `deleted_locally` in their manifest;
- roots learned from earlier hooks, and the default root of each agent even when the hook's environment points elsewhere.

One marker in the state directory's `pending/` means only one sweep is queued at a time; a session that starts or ends while one is queued doesn't add another. Hooks make capture prompt, catch short-lived files before the agent deletes them, and record what only exists at session start.

## Session start

`SessionStart` (Claude Code, Codex) and `session_start` (Pi) work differently from the turn hooks. While the agent is still the hook's ancestor, the hook:

- walks up the process tree to the agent's process and records its command line (`/proc/<pid>/cmdline` on Linux, the `kern.procargs2` sysctl on macOS). Pi renames its process, which hides its arguments, so the Pi extension passes `process.argv` itself. Secrets in it, such as the value of `--api-key` or the headers in an inline `--mcp-config`, are redacted as in MCP server `args` ([storage-layout.md](storage-layout.md#raw-files)).
- keeps allowlisted environment variables (`CLAUDE_CODE_*`, `CODEX_*`, `PI_*`, `ANTHROPIC_MODEL`, `MAX_THINKING_TOKENS`...), dropping any with a word in its name that marks a secret (`TOKEN`, `KEY`, `AUTH`, or `SECRET`, `PASSWORD`, `CREDENTIAL`, `COOKIE` anywhere). Whole words only, so limits such as `MAX_THINKING_TOKENS` are kept.
- saves the hook's whole payload (`source`, `cwd`, `permission_mode`, `model`...).

It writes that to a file and starts a detached sync with no delay and no debounce. The sync snapshots the harness and the repo's git state before the first prompt (`timing: pre_session`) and stores the launch in the session's manifest. A resumed session gets another launch entry.

## Claude Code

**Installed:** eight entries in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`):

```json
"SessionStart": [{"hooks": [{"type": "command", "command": "/path/agent-sessions hook claude", "async": true, "timeout": 30}]}],
"Stop":         [... same ...],
"StopFailure":  [... same ...],
"SubagentStop": [... same ...],
"PreCompact":   [... same ...],
"TaskCompleted":[... same ...],
"TeammateIdle": [... same ...],
"SessionEnd":   [{"hooks": [{"type": "command", "command": "/path/agent-sessions hook claude", "timeout": 5}]}]
```

- **`SessionStart`** fires on startup, resume, `/clear` and after compaction. It records the launch (above). It is async, and prints nothing, so it adds no context to the session.
- **`Stop`** runs after each turn. It **does not fire when you interrupt Claude**, and the docs are explicit about that. The next turn's `Stop`, `SessionEnd` and its sweep cover interrupted turns.
- **`StopFailure`** covers turns that end on an API error, which fire it instead of `Stop`.
- **`SubagentStop`** carries `agent_transcript_path`. agent-sessions syncs the whole session around it: the main transcript, every subagent file and spilled tool output.
- **`PreCompact`** syncs the transcript before compaction rewrites the context.
- **`TaskCompleted`** and **`TeammateIdle`** sync agent-team task lists and mailboxes (`tasks/`, `teams/`), which Claude Code deletes when a team is done.
- **`SessionEnd`** fires on `/clear`, `/resume`, exit and logout. It also starts a sweep.
- **Every Claude Code hook sync** also picks up the session's `file-history/`, `todos/`, `session-env/` and `debug/` files, and all of `tasks/` and `teams/`, since these are deleted with the session.
- **No `matcher`.** Stop doesn't support one, and the others should fire for every value.
- **The command is in shell form, quoted with `shlex`**, so a path with spaces works on every Claude Code version.
- **`cleanupPeriodDays`** is raised to 3650 if lower. Claude Code otherwise deletes transcripts, subagent files, tool results, plans and file history after 30 days.

**Files captured:** all of `~/.claude` except credentials (`.credentials.json`, `sessions/*.key`), Claude Code's own install, caches and plugin source checkouts. See [storage-layout.md](storage-layout.md). `~/.claude.json` is uploaded with its `primaryApiKey` redacted.

## Codex

**Installed:** `~/.codex/hooks.json` (or `$CODEX_HOME/hooks.json`). The schema is `codex-rs/config/src/hook_config.rs:36` (events) and `:163` (handlers): Claude-style `{matcher?, hooks: [{type: "command", command, timeout?, async?}]}`.

- **`SessionStart`** runs on startup and resume (async, 30 s). It records the launch (above); its payload includes `permission_mode`.
- **`Stop`** runs after every turn (async, 30 s).
- **`PreCompact`** runs before compaction (async, 30 s).
- **`SubagentStop`** covers subagent threads. It carries `agent_transcript_path`; each subagent has its own rollout file.
- **`Interrupt`** runs when you cancel a turn (async, 3 s, which is the cap for this event).
- **`SessionEnd`** runs on exit (synchronous, 3 s). It also starts a sweep.

**Trust.** Codex skips a user hook until it has been reviewed. Its trust record is a hash in `config.toml` under `[hooks.state."<hooks.json path>:<event>:<group>:<handler>"]`, written when you approve it in `/hooks` (`discovery.rs:715`, `:796`). agent-sessions deliberately leaves that review to you. `agent-sessions doctor` reads `config.toml` and reports how many agent-sessions hooks are trusted.

The trust hash covers the command string, so a hook that changes needs approving again. The installer uses the `agent-sessions` binary on your `PATH` (`~/.local/bin/agent-sessions` from `install.sh`), whose path survives upgrades, so an upgrade doesn't need a new review. Without one, it writes the path of the binary it was run as and warns, since hooks pointing at a download directory break when that is cleaned up. `agent-sessions doctor` fails any agent's hooks whose binary can't be run.

**Other details:**

- **Hooks are on by default.** The feature flag `hooks` is stable and enabled (`codex-rs/features/src/lib.rs:1333`). `doctor` flags `[features] hooks = false`.
- **Hooks run through `$SHELL -lc`** (`command_runner.rs:399`), so your login profile runs first. That's another reason the turn hooks are async.
- **Transcripts.** The hook's `transcript_path` is null for sessions Codex doesn't persist, and there is nothing to capture for those.

**Files captured:** all of `$CODEX_HOME` except `auth.json`, `.credentials.json` (MCP OAuth tokens when there's no keyring), `.env` (API keys Codex loads at start), locks, temp files, caches and shell snapshots.

- `sessions/YYYY/MM/DD/rollout-*.jsonl` and `archived_sessions/`.
  - **Recompression.** Rollouts untouched for 7 days are recompressed to `.jsonl.zst` (`rollout/src/compression.rs:364`), and resuming one decompresses it again (`:121`). agent-sessions keys rollouts without the date folder or `.zst`, and hashes the decompressed bytes. So recompression, archiving and resuming never cause a re-upload.
- `history.jsonl`, `session_index.jsonl`, `memories/`, `log/`, `config.toml`, `skills/`, `rules/`, `prompts/`.
- **Databases.** `state_<n>.sqlite` (titles, token totals, sandbox and approval mode), `logs_<n>.sqlite`, `memories_*`, `goals_*`, `thread_history_*` and `queue_*` are uploaded as SQLite backup-API copies under `db/`, so rows still in the `-wal` file are included. Codex's current "paginated" history mode keeps some thread metadata only in SQLite (`rollout/src/metadata.rs:315`). The rollout is still the full record of the conversation. One limit: in paginated mode, Codex itself truncates very large command output before writing it to the rollout.

## Pi

**Installed:** `~/.pi/agent/extensions/agent-sessions.ts` (or under `$PI_CODING_AGENT_DIR`). Pi auto-loads user extensions with `jiti`, so there's no build step, and user extensions don't need project trust (`docs/extensions.md`).

```ts
pi.on("session_start",    (e, ctx) => kick(ctx.sessionManager.getSessionFile(),
                                           ["--event", "session_start", "--source", e.reason, "--cwd", ctx.cwd,
                                            "--argv", JSON.stringify(process.argv)]));
pi.on("agent_settled",    (_e, ctx) => kick(ctx.sessionManager.getSessionFile(), ["--event", "agent_settled"]));
pi.on("session_shutdown", (_e, ctx) => kick(ctx.sessionManager.getSessionFile(), ["--event", "session_shutdown"]));
```

- **`session_start`** fires on startup, `/new`, `/resume`, `/fork` and `/reload` (`types.ts:741`), with the reason. It records the launch (above).
- **`agent_settled`** means a prompt has fully finished, with no retry, compaction or queued follow-up left to run (`types.ts:1005`).
- **`session_shutdown`** fires on quit, `/new`, `/resume`, `/fork` and `/reload` (`types.ts:810`). It also starts a sweep, unless the session has no file (`--no-session`, or no message yet).
  - Pi awaits these handlers (`runner.ts:258`), so `kick` only spawns a detached `agent-sessions hook pi --path …` and returns.
  - SIGTERM and SIGHUP also emit `session_shutdown` before Pi exits, in both interactive mode (`interactive-mode.ts:4403`) and print mode (`print-mode.ts:59`).
- **Nothing in the factory.** The extension starts no processes or timers when it loads, as Pi's lifecycle rules ask.
- **An extension from an earlier version** passes no `--event` on shutdown, so it starts no sweep. `agent-sessions doctor` warns when the extension is out of date, and `agent-sessions install` rewrites it.

**Edge cases:**

- **The session file appears late.** Pi creates it only once the session has a user or assistant message (`session-manager.ts:1176`), so an early event may name a file that doesn't exist yet. That's fine; the next event or sweep picks it up.
- **`--no-session` writes nothing.** `getSessionFile()` returns undefined, and the extension does nothing.
- **Sessions can live elsewhere.** `--session-dir`, `PI_CODING_AGENT_SESSION_DIR` and the `sessionDir` setting move them, and a relative `sessionDir` resolves per project. The extension reports the real path, and agent-sessions remembers any new directory so the sweep covers it too. Absolute `sessionDir` values in `~/.pi/agent/settings.json` are swept from the start.
- **Branches stay in one file.** `/tree` branches stay in the same file, while `/fork` and `/clone` write new files that name their `parentSession`.
- **The prompt and tools are in the transcript.** Pi records the system prompt sections and tool declarations as system messages, so its sessions carry their own harness.

## Edge cases common to all three

- **Config dirs set only in your shell.** `CLAUDE_CONFIG_DIR`, `CODEX_HOME` and Pi's session dir settings can differ between shells. Hooks run with the agent's environment, so the first hook from a non-default directory teaches agent-sessions that root, and later sweeps include it, whichever agent's hook starts them.
- **Half-written lines.** A transcript written in the last minute that ends mid-line is uploaded up to its last complete line, and re-read on the next sync.
- **Hooks and the sweep at once.** One lock serialises syncs. A hook's sync gives up after 30 s, and a sweep after 60 s, if a long backfill holds the lock, so waiting syncs can't pile up; the next hook or sweep covers it, and records any session-start launch the one that gave up left behind. A pending marker older than 2 minutes, left by a sync that died, is taken over.
- **Offline machines.** A failed upload isn't recorded as done, so the session's next hook or the next sweep retries it. Nothing is lost while the transcript stays on disk, which is why `cleanupPeriodDays` is raised.
- **Settings files.** The installer never rewrites a settings file it can't parse. It backs up every file before changing it, keeps other hooks in place, and replaces only its own entries, which it recognises by the `agent-sessions … hook <agent>` command. It replaces each one where it is, so a reinstall moves no hook: Codex keys each trust record by the hook's position, and would skip a moved hook, ours or yours, until it was approved again.
- **No session, no sweep.** Without a timer, what only a sweep picks up waits for the next session to start or end on that machine. A crashed session's last turns, a failed upload from another session, or a database that changed while a long session ran stay on disk until then; `agent-sessions sync` uploads them at any time.
