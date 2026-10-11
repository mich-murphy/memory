# memory

AI memory pipeline research and optimisation.

The first piece is **agent-sessions**, a small collector that copies everything Claude Code, Codex and Pi keep on each of your machines into one Cloudflare R2 bucket: every session, how it was launched, and a snapshot of the instructions, skills, settings and scripts it ran with. Later stages (normalising to Parquet, then mining the sessions for CLAUDE.md, docs, prompt, skill and harness changes) read from that bucket.

## Set up a machine

You need [uv](https://docs.astral.sh/uv/) and an R2 API token scoped to the bucket with Object Read & Write. Use one token per machine so any machine can be revoked on its own.

```bash
uv tool install git+https://github.com/mich-murphy/memory
agent-sessions install      # hooks, the Pi extension, a sweep every 10 minutes
$EDITOR ~/.config/agent-sessions/config.env   # R2 endpoint, bucket and keys (written for you, mode 600)
agent-sessions doctor       # checks the bucket, every hook and the timer
agent-sessions backfill     # uploads every session already on this machine
```

If you use Codex, open it once and approve the agent-sessions hooks in `/hooks`. Codex doesn't run a new hook until you've reviewed it, and `agent-sessions doctor` tells you whether that's done.

That's all. `agent-sessions install` only touches agents it finds on the machine, so rerun it after installing another agent. It's safe to rerun at any time: it replaces its own entries and leaves everything else in your settings alone, backing up any file before changing it.

On a Linux VM where you leave agents running after logging out (in tmux, say), also run `sudo loginctl enable-linger $USER` so the sweep keeps running. `agent-sessions doctor` warns when this is needed.

## How it captures sessions

Each agent tells agent-sessions when a session has new content, and a sweep catches whatever the hooks miss.

| Agent | Trigger | Installed into |
|---|---|---|
| Claude Code | `SessionStart`, `Stop`, `StopFailure`, `SubagentStop`, `PreCompact`, `TaskCompleted`, `TeammateIdle`, `SessionEnd` hooks | `~/.claude/settings.json` |
| Codex | `SessionStart`, `Stop`, `SubagentStop`, `PreCompact`, `Interrupt`, `SessionEnd` hooks | `~/.codex/hooks.json` |
| Pi | an extension handling `session_start`, `agent_settled` and `session_shutdown` | `~/.pi/agent/extensions/agent-sessions.ts` |
| All | `agent-sessions sync` every 10 minutes | systemd user timer, launchd or cron |

The hook command returns within milliseconds, prints nothing and always exits 0. It hands the upload to a detached process that waits a few seconds for the transcript to flush, so a slow network never slows an agent down and capture never interrupts one. At session start it also records the agent's command line and settings environment, and the sync snapshots the harness and git state before the first prompt. The sweep covers crashes, killed terminals, user interrupts, `claude -p` runs, and anything from before agent-sessions was installed.

[docs/hooks.md](docs/hooks.md) has the details for each agent, with the docs and source lines they were checked against, and the edge cases handled.

## What gets captured

- **Everything under each agent's directory, byte for byte.** Transcripts (main, subagent, spilled tool output, set-aside), memory, plans, prompt history, file-history backups, todos, tasks, agent-team mailboxes, debug logs, settings, skills and extensions. Codex rollouts are read through their `.zst` recompression, so they keep one key, and Codex's SQLite databases are uploaded as consistent backup copies.
- **Only what changed is uploaded.** While a transcript grows, each sync uploads just the bytes appended since the last one, and once the session goes quiet they are folded back into one object. Objects are zstd-compressed.
- **Earlier versions.** A file that is rewritten rather than appended to keeps its old bytes under `blobs/`, stored once by content hash however many files or machines had them. Database copies keep at most one earlier version a day. A file deleted locally is marked in its session's manifest.
- **How each session was launched.** The agent's command line (`--model`, `--permission-mode`, `-p`, `--profile`...), its `CLAUDE_CODE_*`/`CODEX_*`/`PI_*` environment, the session-start hook's payload, and the repo's root, remote, branch, HEAD and dirty flag.
- **The harness each session ran with.** Every CLAUDE.md and AGENTS.md in scope, `@imports`, whole skill directories, subagents, commands, rules, settings, installed plugins, the hook and status-line scripts settings point at, MCP servers and managed settings, plus a separate set for the memory agents write for themselves. Files are stored once by content hash, and the repo's git history is exported alongside.
- **Optional shadow labels.** Set `AGENT_SESSIONS_CATEGORISE_CMD` (for example `agent-sessions jev`, which uses Jev) and every transcript record is labelled with a category and a waste flag before upload. Nothing is dropped; `agent-sessions labels` shows how much would have been.
- **No redaction.** Only credential files are left out (logins, API keys, OAuth tokens, `primaryApiKey`); see [docs/storage-layout.md](docs/storage-layout.md). Everything else can contain anything a tool printed or a settings file holds, so keep the bucket private.

Claude Code deletes transcripts after 30 days by default. `agent-sessions install` raises `cleanupPeriodDays` to 3650 (never lowers it) so nothing is lost while a machine is offline.

[docs/storage-layout.md](docs/storage-layout.md) describes the bucket layout, the manifest format and exactly what is skipped.

## Commands

| Command | What it does |
|---|---|
| `agent-sessions install` | Hooks, Pi extension, sweep timer, config file. `--dry-run` shows what would change. |
| `agent-sessions doctor` | Checks settings, bucket access, each hook, Codex hook trust, the timer and recent errors. |
| `agent-sessions backfill` | Uploads everything already on the machine. |
| `agent-sessions sync` | Uploads what changed. The timer runs this. |
| `agent-sessions status` | Counts by agent, recent runs and errors. |
| `agent-sessions uninstall` | Removes the hooks, the extension and the timer. Uploaded data stays. |

Set `AGENT_SESSIONS_DISABLE=1` in an agent's environment to skip capture for that run.

Logs are in `~/.local/state/agent-sessions/agent-sessions.log`.

If a machine has memsync (this tool's earlier name) installed, run `memsync uninstall` first. A reinstall also replaces any memsync hook entries it finds.

## Development

```bash
uv venv && uv pip install -e '.[dev]'
.venv/bin/pytest
```

Linux and macOS only.
