# How memsync hooks into each agent

Checked on 2026-10-10 against:

- **Claude Code** v2.1.296, using the docs at https://code.claude.com/docs/en/hooks, `/claude-directory` and `/settings-reference`.
- **Codex** source at [openai/codex@806d973](https://github.com/openai/codex/tree/806d9732c974bc8a51b8317c1bd8985544fe627c), and the released CLI 0.162.1.
- **Pi** source and docs at [earendil-works/pi@42a3497](https://github.com/earendil-works/pi/tree/42a3497d03ad17e308a2299fa824727894f2c0ec) (formerly badlogic/pi-mono; `packages/coding-agent`), and the released `@earendil-works/pi-coding-agent` 1.1.0.

All three were also run for real (Claude Code 2.1.296, Codex 0.162.1, Pi 1.1.0) against a local fake model server, with memsync installed into a scratch home. Each one's hook fired and its session landed in the store byte for byte.

## One rule for every agent

The hook command is `memsync hook <agent>`. It reads the hook's JSON from stdin, or takes `--path` from the Pi extension. It starts `memsync sync --path <transcript> --delay 3` in a new session with stdin, stdout and stderr on `/dev/null`, then exits 0 without printing anything.

Why each part matters:

| Constraint | Source | What memsync does |
|---|---|---|
| Claude Code treats `Stop` exit code 2 as "keep working" | hooks docs, Stop decision control | Always exit 0 |
| Codex fails a *synchronous* Stop hook whose stdout isn't JSON | `codex-rs/hooks/src/events/stop.rs:624` | Print nothing. Stop hooks are also `async`, which ignores stdout, so a login shell that prints a banner can't break them |
| Claude Code `SessionEnd` gets 1.5 s; Codex `SessionEnd` gets 1 s by default, at most 3 s, and can't be async | hooks docs; `session_end.rs:20-23`, `discovery.rs:533` | The hook returns in about 0.1 s. Timeouts are set to 5 s (Claude Code) and 3 s (Codex) as headroom for a cold interpreter |
| `claude -p` kills async hooks still running at teardown, and the docs say to "start a fully detached process" | hooks docs, "Run hooks in the background" | The sync runs in its own session (`start_new_session`) |
| Codex kills the hook's process group on timeout, and waits for the hook's stdout to close | `command_runner.rs:339`, `:413`, wait_with_output | Own session, so the group kill misses it; stdio detached, so Codex isn't left waiting. Codex's own comment at `:281`: "Successfully completed hooks may intentionally leave detached helpers running" |
| The transcript can lag the in-memory conversation when a hook fires | hooks docs, `transcript_path` field | The sync waits 3 s before reading. The sweep catches anything later |
| Stop fires after every turn | all three | Debounce: a marker per transcript means only one sync is queued at a time. The queued one reads the file after its delay, so it includes the turns that arrived meanwhile |

The sweep (`memsync sync` every 10 minutes, plus `memsync backfill` once) is what makes capture complete. Hooks only make it prompt.

## Claude Code

**Installed:** four entries in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`):

```json
"Stop":         [{"hooks": [{"type": "command", "command": "/path/memsync hook claude", "async": true, "timeout": 30}]}],
"StopFailure":  [... same ...],
"SubagentStop": [... same ...],
"SessionEnd":   [{"hooks": [{"type": "command", "command": "/path/memsync hook claude", "timeout": 5}]}]
```

- **`Stop`** runs after each turn. It **does not fire when you interrupt Claude**, and the docs are explicit about that. `SessionEnd` and the sweep cover interrupted turns.
- **`StopFailure`** covers turns that end on an API error, which fire it instead of `Stop`.
- **`SubagentStop`** carries `agent_transcript_path`. memsync syncs the whole session around it: the main transcript, every subagent file and spilled tool output.
- **`SessionEnd`** fires on `/clear`, `/resume`, exit and logout.
- **No `matcher`.** Stop doesn't support one, and the others should fire for every value.
- **The command is in shell form, quoted with `shlex`**, so a path with spaces works on every Claude Code version.
- **`cleanupPeriodDays`** is raised to 3650 if lower. Claude Code otherwise deletes transcripts, subagent files, tool results, plans and file history after 30 days.

**Files captured** (`claude-directory` docs, "Application data"):

- `projects/<slug>/<session>.jsonl`, the transcript
- `<session>/subagents/*` and `<session>/tool-results/*`
- `<session>.orphaned-*.jsonl` and `<session>.jsonl.superseded-*`, earlier transcripts set aside on resume
- `projects/<slug>/memory/`, auto memory
- `plans/*.md` and `history.jsonl`

Not captured: `file-history/` (pre-edit file backups), `paste-cache/`, debug logs, and `sessions/`, which only holds liveness files for running sessions.

## Codex

**Installed:** `~/.codex/hooks.json` (or `$CODEX_HOME/hooks.json`). The schema is `codex-rs/config/src/hook_config.rs:36` (events) and `:163` (handlers): Claude-style `{matcher?, hooks: [{type: "command", command, timeout?, async?}]}`.

- **`Stop`** runs after every turn (async, 30 s).
- **`SubagentStop`** covers subagent threads. It carries `agent_transcript_path`; each subagent has its own rollout file.
- **`Interrupt`** runs when you cancel a turn (async, 3 s, which is the cap for this event).
- **`SessionEnd`** runs on exit (synchronous, 3 s).

**Trust.** Codex skips a user hook until it has been reviewed. Its trust record is a hash in `config.toml` under `[hooks.state."<hooks.json path>:<event>:<group>:<handler>"]`, written when you approve it in `/hooks` (`discovery.rs:715`, `:796`). memsync deliberately leaves that review to you. `memsync doctor` reads `config.toml` and reports how many memsync hooks are trusted.

The trust hash covers the command string, so a hook that changes needs approving again. The installer uses the `memsync` shim on your `PATH` (from `uv tool install`), whose path survives upgrades, so an upgrade doesn't need a new review.

**Other details:**

- **Hooks are on by default.** The feature flag `hooks` is stable and enabled (`codex-rs/features/src/lib.rs:1333`). `doctor` flags `[features] hooks = false`.
- **Hooks run through `$SHELL -lc`** (`command_runner.rs:399`), so your login profile runs first. That's another reason the turn hooks are async.
- **Transcripts.** The hook's `transcript_path` is null for sessions Codex doesn't persist, and there is nothing to capture for those.

**Files captured:**

- `sessions/YYYY/MM/DD/rollout-*.jsonl` and `archived_sessions/`.
  - **Recompression.** Rollouts untouched for 7 days are recompressed to `.jsonl.zst` (`rollout/src/compression.rs:364`), and resuming one decompresses it again (`:121`). memsync keys rollouts without the date folder or `.zst`, and hashes the decompressed bytes. So recompression, archiving and resuming never cause a re-upload.
- `history.jsonl` and `session_index.jsonl`.
- `state/threads.jsonl`, exported by each sweep from the `threads` table of `state_<n>.sqlite`. It holds titles, token totals, and sandbox and approval mode. Codex's current "paginated" history mode keeps some of this metadata only in SQLite (`rollout/src/metadata.rs:315`). The rollout is still the full record of the conversation. One limit: in paginated mode, Codex itself truncates very large command output before writing it to the rollout.

## Pi

**Installed:** `~/.pi/agent/extensions/memsync.ts` (or under `$PI_CODING_AGENT_DIR`). Pi auto-loads user extensions with `jiti`, so there's no build step, and user extensions don't need project trust (`docs/extensions.md`).

```ts
pi.on("agent_settled",    (_e, ctx) => kick(ctx.sessionManager.getSessionFile()));
pi.on("session_shutdown", (_e, ctx) => kick(ctx.sessionManager.getSessionFile()));
```

- **`agent_settled`** means a prompt has fully finished, with no retry, compaction or queued follow-up left to run (`types.ts:1005`).
- **`session_shutdown`** fires on quit, `/new`, `/resume`, `/fork` and `/reload` (`types.ts:810`).
  - Pi awaits these handlers (`runner.ts:258`), so `kick` only spawns a detached `memsync hook pi --path …` and returns.
  - SIGTERM and SIGHUP also emit `session_shutdown` before Pi exits, in both interactive mode (`interactive-mode.ts:4403`) and print mode (`print-mode.ts:59`).
- **Nothing in the factory.** The extension starts no processes or timers when it loads, as Pi's lifecycle rules ask.

**Edge cases:**

- **The session file appears late.** Pi creates it only once the session has a user or assistant message (`session-manager.ts:1176`), so an early event may name a file that doesn't exist yet. That's fine; the next event or sweep picks it up.
- **`--no-session` writes nothing.** `getSessionFile()` returns undefined, and the extension does nothing.
- **Sessions can live elsewhere.** `--session-dir`, `PI_CODING_AGENT_SESSION_DIR` and the `sessionDir` setting move them, and a relative `sessionDir` resolves per project. The extension reports the real path, and memsync remembers any new directory so the sweep covers it too. Absolute `sessionDir` values in `~/.pi/agent/settings.json` are swept from the start.
- **Branches stay in one file.** `/tree` branches stay in the same file, while `/fork` and `/clone` write new files that name their `parentSession`.
- **The prompt and tools are in the transcript.** Pi records the system prompt sections and tool declarations as system messages, so its sessions carry their own harness.

## Edge cases common to all three

- **Config dirs set only in your shell.** `CLAUDE_CONFIG_DIR`, `CODEX_HOME` and Pi's session dir settings are invisible to cron. Hooks run with the agent's environment, so the first hook from a non-default directory teaches memsync that root, and later sweeps include it.
- **Half-written lines.** A transcript written in the last minute that ends mid-line is uploaded up to its last complete line, and re-read on the next sync.
- **Hooks and the sweep at once.** One lock serialises syncs. A hook's sync gives up after 30 s if a long backfill holds the lock, so waiting syncs can't pile up; the next sweep covers it. A pending marker older than 2 minutes, left by a sync that died, is taken over.
- **Offline machines.** A failed upload isn't recorded as done, so the next sweep retries it. Nothing is lost while the transcript stays on disk, which is why `cleanupPeriodDays` is raised.
- **Settings files.** The installer never rewrites a settings file it can't parse. It backs up every file before changing it, keeps other hooks in place, and replaces only its own entries, which it recognises by the `memsync … hook <agent>` command.
- **Sleeping laptops.** The systemd timer is `Persistent=true`, and launchd runs a missed interval on wake.
- **Linux VMs after logout.** User timers stop when you log out unless lingering is enabled. `install` and `doctor` print the `loginctl enable-linger` command when it's off.
