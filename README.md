# memory

AI memory pipeline research and optimisation.

The first piece is **agent-sessions**, a small collector that copies everything Claude Code, Codex and Pi keep on each of your machines into one Cloudflare R2 bucket: every session, how it was launched, and a snapshot of the instructions, skills, settings and scripts it ran with. Later stages (normalising to Parquet, then mining the sessions for CLAUDE.md, docs, prompt, skill and harness changes) read from that bucket.

## Set up a machine

You need an R2 API token scoped to the bucket with Object Read & Write. Use one token per machine so any machine can be revoked on its own. Nothing else: agent-sessions is a single binary for Linux and macOS (13 or later) on amd64 or arm64.

```bash
curl -fsSL https://raw.githubusercontent.com/mich-murphy/memory/main/install.sh | sh   # into ~/.local/bin
agent-sessions install      # hooks and the Pi extension
$EDITOR ~/.config/agent-sessions/config.env   # R2 endpoint, bucket and keys (written for you, mode 600)
agent-sessions doctor       # checks the bucket and every hook
agent-sessions backfill     # uploads every session already on this machine
```

While the repo is private, `raw.githubusercontent.com` returns 404, so fetch the script through `gh` instead, logged in with `gh auth login`:

```bash
gh api -H 'Accept: application/vnd.github.raw' repos/mich-murphy/memory/contents/install.sh | sh
```

If you use Codex, open it once and approve the agent-sessions hooks in `/hooks`. Codex doesn't run a new hook until you've reviewed it, and `agent-sessions doctor` tells you whether that's done.

That's all. `agent-sessions install` only touches agents it finds on the machine, so rerun it after installing another agent. It's safe to rerun at any time: it replaces its own entries and leaves everything else in your settings alone, backing up any file before changing it.

## How it captures sessions

Each agent tells agent-sessions when a session has new content. Nothing runs on a schedule: when any session starts or ends, a sweep of every agent's files catches whatever the hooks missed.

| Agent | Trigger | Installed into |
|---|---|---|
| Claude Code | `SessionStart`, `Stop`, `StopFailure`, `SubagentStop`, `PreCompact`, `TaskCompleted`, `TeammateIdle`, `SessionEnd` hooks | `~/.claude/settings.json` |
| Codex | `SessionStart`, `Stop`, `SubagentStop`, `PreCompact`, `Interrupt`, `SessionEnd` hooks | `~/.codex/hooks.json` |
| Pi | an extension handling `session_start`, `agent_settled` and `session_shutdown` | `~/.pi/agent/extensions/agent-sessions.ts` |

The hook command returns within milliseconds, prints nothing and always exits 0. It hands the upload to a detached process that waits a few seconds for the transcript to flush, so a slow network never slows an agent down and capture never interrupts one. At session start it also records the agent's command line and settings environment, and the sync snapshots the harness and git state before the first prompt. The sweep covers crashes, killed terminals, user interrupts, failed uploads and anything from before agent-sessions was installed, at the next session start or end on that machine.

[docs/hooks.md](docs/hooks.md) has the details for each agent, with the docs and source lines they were checked against, and the edge cases handled.

## What gets captured

- **Everything under each agent's directory, byte for byte.** Transcripts (main, subagent, spilled tool output, set-aside), memory, plans, prompt history, file-history backups, todos, tasks, agent-team mailboxes, debug logs, settings, skills and extensions. Codex rollouts are read through their `.zst` recompression, so they keep one key, and Codex's SQLite databases are uploaded as consistent backup copies.
- **Only what changed is uploaded.** While a transcript grows, each sync uploads just the bytes appended since the last one, and once the session goes quiet they are folded back into one object. Objects are zstd-compressed.
- **Earlier versions.** A file that is rewritten rather than appended to keeps its old bytes under `blobs/`, stored once by content hash however many files or machines had them. Database copies keep at most one earlier version a day. A file deleted locally is marked in its session's manifest.
- **How each session was launched.** The agent's command line (`--model`, `--permission-mode`, `-p`, `--profile`...), its `CLAUDE_CODE_*`/`CODEX_*`/`PI_*` environment, the session-start hook's payload, and the repo's root, remote, branch, HEAD and dirty flag.
- **The harness each session ran with.** Every CLAUDE.md and AGENTS.md in scope, `@imports`, whole skill directories, subagents, commands, rules, settings, installed plugins, the hook and status-line scripts settings point at, MCP servers and managed settings, plus a separate set for the memory agents write for themselves. Files are stored once by content hash.
- **Secrets.** Credential files are left out (logins, API keys, OAuth tokens), and known secret values in config files, such as `primaryApiKey` and MCP server env and headers, are replaced with `[redacted]`; see [docs/storage-layout.md](docs/storage-layout.md). Everything else can contain anything a tool printed or a settings file holds, so keep the bucket private.

Claude Code deletes transcripts after 30 days by default. `agent-sessions install` raises `cleanupPeriodDays` to 3650 (never lowers it) so nothing is lost while a machine is offline.

[docs/storage-layout.md](docs/storage-layout.md) describes the bucket layout, the manifest format and exactly what is skipped.

## Commands

| Command | What it does |
|---|---|
| `agent-sessions install` | Hooks, Pi extension, config file. `--dry-run` shows what would change. |
| `agent-sessions doctor` | Checks settings, bucket access, each hook and the binary it runs, Codex hook trust, the last sweep and recent errors. |
| `agent-sessions backfill` | Uploads everything already on the machine. |
| `agent-sessions sync` | Uploads what changed. Hooks run this. |
| `agent-sessions uninstall` | Removes the hooks and the extension. Uploaded data stays. |

Set `AGENT_SESSIONS_DISABLE=1` in an agent's environment to skip capture for that run.

Logs are in `~/.local/state/agent-sessions/agent-sessions.log`.

Rerun the install script to upgrade; it verifies each download against the release's `checksums.txt`. `VERSION=v0.1.0` picks a release. While the repo is private, the script downloads through `gh`, so log in with `gh auth login` first.

To remove the binary too, run `sh install.sh uninstall` from a local copy of the script. It removes the managed hooks and extension first, then the binary, without using the network. Config, credentials, machine identity, local state and all remote data are retained. See [docs/install.md](docs/install.md) for installation, removal and hook-trust verification details.

## Development

Go 1.27 and [golangci-lint](https://golangci-lint.run) at the version in `.golangci-lint-version`.

```bash
make hooks   # git hooks: format and lint on commit, tests on push
make check   # every gate CI runs
make gopls   # the pinned gopls, for the Claude Code gopls-lsp plugin
```

For Claude Code, the repo's settings turn on the `gopls-lsp` plugin and the gopls MCP server in `.mcp.json`. Install the plugin once with `claude plugin install gopls-lsp@claude-plugins-official --scope project` after `make gopls`, and approve the MCP server when asked.

[docs/quality-gates.md](docs/quality-gates.md) explains each gate and its settings, and [CLAUDE.md](CLAUDE.md) has the rules for coding agents. Linux and macOS only.
