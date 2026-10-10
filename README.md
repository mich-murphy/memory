# memory

AI memory pipeline research and optimisation.

The first piece is **memsync**, a small collector that copies every Claude Code, Codex and Pi session from each of your machines into one Cloudflare R2 bucket, together with a snapshot of the instructions, skills and settings each session ran with. Later stages (normalising to Parquet, then mining the sessions for CLAUDE.md, docs, prompt, skill and harness changes) read from that bucket.

## Set up a machine

You need [uv](https://docs.astral.sh/uv/) and an R2 API token scoped to the bucket with Object Read & Write. Use one token per machine so any machine can be revoked on its own.

```bash
uv tool install git+https://github.com/mich-murphy/memory
memsync install      # hooks, the Pi extension, a sweep every 10 minutes
$EDITOR ~/.config/memsync/config.env   # R2 endpoint, bucket and keys (written for you, mode 600)
memsync doctor       # checks the bucket, every hook and the timer
memsync backfill     # uploads every session already on this machine
```

If you use Codex, open it once and approve the memsync hooks in `/hooks`. Codex doesn't run a new hook until you've reviewed it, and `memsync doctor` tells you whether that's done.

That's all. `memsync install` only touches agents it finds on the machine, so rerun it after installing another agent. It's safe to rerun at any time: it replaces its own entries and leaves everything else in your settings alone, backing up any file before changing it.

On a Linux VM where you leave agents running after logging out (in tmux, say), also run `sudo loginctl enable-linger $USER` so the sweep keeps running. `memsync doctor` warns when this is needed.

## How it captures sessions

Each agent tells memsync when a session has new content, and a sweep catches whatever the hooks miss.

| Agent | Trigger | Installed into |
|---|---|---|
| Claude Code | `Stop`, `StopFailure`, `SubagentStop`, `SessionEnd` hooks | `~/.claude/settings.json` |
| Codex | `Stop`, `SubagentStop`, `Interrupt`, `SessionEnd` hooks | `~/.codex/hooks.json` |
| Pi | an extension handling `agent_settled` and `session_shutdown` | `~/.pi/agent/extensions/memsync.ts` |
| All | `memsync sync` every 10 minutes | systemd user timer, launchd or cron |

The hook command returns within milliseconds, prints nothing and always exits 0. It hands the upload to a detached process that waits a few seconds for the transcript to flush, so a slow network never slows an agent down and capture never interrupts one. The sweep covers crashes, killed terminals, user interrupts, `claude -p` runs, and anything from before memsync was installed.

[docs/hooks.md](docs/hooks.md) has the details for each agent, with the docs and source lines they were checked against, and the edge cases handled.

## What gets captured

- **Transcripts, byte for byte.** Main sessions, subagent transcripts, spilled tool output, and transcripts that Claude Code set aside on resume. Codex rollouts are read through their `.zst` recompression, so they keep one key.
- **Agent-side context.** Claude Code's auto memory, plan files and prompt history; Codex's prompt history and thread metadata (titles, token totals, sandbox and approval mode).
- **The harness each session ran with.** Every CLAUDE.md and AGENTS.md in scope, rules, skills, subagents, commands, settings, hooks and MCP servers, plus the repo's remote, branch and commit. Files are stored once by content hash. Secrets in these files are redacted before upload.
- **Raw transcripts are not redacted.** They can contain anything a tool printed, so keep the bucket private. Redaction happens at the normalisation step, before anything reaches an LLM.

Claude Code deletes transcripts after 30 days by default. `memsync install` raises `cleanupPeriodDays` to 3650 (never lowers it) so nothing is lost while a machine is offline.

[docs/storage-layout.md](docs/storage-layout.md) describes the bucket layout and the manifest format.

## Commands

| Command | What it does |
|---|---|
| `memsync install` | Hooks, Pi extension, sweep timer, config file. `--dry-run` shows what would change. |
| `memsync doctor` | Checks settings, bucket access, each hook, Codex hook trust, the timer and recent errors. |
| `memsync backfill` | Uploads everything already on the machine. |
| `memsync sync` | Uploads what changed. The timer runs this. |
| `memsync status` | Counts by agent, recent runs and errors. |
| `memsync uninstall` | Removes the hooks, the extension and the timer. Uploaded data stays. |

Set `MEMSYNC_DISABLE=1` in an agent's environment to skip capture for that run. Logs are in `~/.local/state/memsync/memsync.log`.

## Development

```bash
uv venv && uv pip install -e '.[dev]'
.venv/bin/pytest
```

Linux and macOS only.
