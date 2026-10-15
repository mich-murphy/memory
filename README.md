# memory

AI memory pipeline research and optimisation.

The first piece is **agent-sessions**, a small collector that copies everything Claude Code, Codex and Pi keep on each of your machines into one Cloudflare R2 bucket: every session, how it was launched, and a snapshot of the instructions, skills, settings and scripts it ran with. Later stages (normalising to Parquet, then mining the sessions for CLAUDE.md, docs, prompt, skill and harness changes) read from that bucket.

## Set up a machine

You need an R2 API token scoped to the bucket with Object Read & Write. Use one token per machine so any machine can be revoked on its own. Nothing else: agent-sessions is a single binary for Linux and macOS (13 or later) on amd64 or arm64.

Install the binary pinned to a release tag, either with Nix (built from source; see [docs/install.md](docs/install.md#nix-built-from-source)) or from the release itself. The release route needs the GitHub CLI, logged in with `gh auth login`:

```bash
gh release download v0.3.0 -R mich-murphy/memory -p install.sh
gh release verify-asset v0.3.0 install.sh -R mich-murphy/memory   # GitHub's attestation of the published file
less install.sh                                                   # read it before running it
VERSION=v0.3.0 sh install.sh                                      # verifies the binary the same way, into ~/.local/bin
```

Then set the machine up:

```bash
agent-sessions machine-id --new   # deliberate NEW machine only; for a lost ID, restore it instead (see below)
agent-sessions install      # hooks and the Pi extension
$EDITOR ~/.config/agent-sessions/config.env   # R2 endpoint, bucket and keys (written for you, mode 600)
agent-sessions doctor       # checks the bucket and every hook
agent-sessions backfill     # uploads every session already on this machine
```

If you use Codex, open it once and approve the agent-sessions hooks in `/hooks`. Codex doesn't run a new hook until you've reviewed it, and `agent-sessions doctor` tells you whether that's done.

That's all. `agent-sessions install` only touches agents it finds on the machine, so rerun it after installing another agent. It's safe to rerun at any time: it replaces its own entries and leaves everything else in your settings alone, backing up any file before changing it.

### Keep the machine identity

`agent-sessions machine-id` prints the effective ID. Keep a backup of it: ordinary sync,
backfill, doctor, install and upgrades never generate an identity. `machine-id --new` is an
explicit new-machine choice, not a recovery command; an existing identity is printed, never
rotated. If `machine-id` is lost or empty, restore the original file or set the known previous
`AGENT_SESSIONS_MACHINE_ID` in `config.env` (or the agent's environment) before syncing.
The collector cannot recover an unknown ID or select an old namespace by hostname; inspect
remote namespaces with your bucket tools. See [docs/machine-identity.md](docs/machine-identity.md)
for fresh-install, recovery and upgrade details.

## How it captures sessions

Each agent tells agent-sessions when a session has new content. Nothing runs on a schedule: when any session starts or ends, a sweep of every agent's files catches whatever the hooks missed.

| Agent | Trigger | Installed into |
|---|---|---|
| Claude Code | `SessionStart`, `Stop`, `StopFailure`, `SubagentStop`, `PreCompact`, `TaskCompleted`, `TeammateIdle`, `SessionEnd` hooks | `~/.claude/settings.json` |
| Codex | `SessionStart`, `Stop`, `SubagentStop`, `PreCompact`, `Interrupt`, `SessionEnd` hooks | `~/.codex/hooks.json` |
| Pi | an extension handling `session_start`, `agent_settled` and `session_shutdown` | `~/.pi/agent/extensions/agent-sessions.ts` |

The hook command returns within milliseconds, prints nothing and always exits 0. It hands the upload to a detached process that waits a few seconds for the transcript to flush, so a slow network never slows an agent down and capture never interrupts one. At session start it also records the agent's command line and settings environment. The detached sync snapshots the harness and git state when it actually runs; the session-start event alone does not prove that capture preceded the first prompt. The sweep covers crashes, killed terminals, user interrupts, failed uploads and anything from before agent-sessions was installed, at the next session start or end on that machine.

[docs/hooks.md](docs/hooks.md) has the details for each agent, with the docs and source lines they were checked against, and the edge cases handled.

## What gets captured

- **Everything under each agent's directory, byte for byte.** Transcripts (main, subagent, spilled tool output, set-aside), memory, plans, prompt history, file-history backups, todos, tasks, agent-team mailboxes, debug logs, settings, skills and extensions. Codex rollouts are read through their `.zst` recompression, so they keep one key, and Codex's SQLite databases are uploaded as consistent backup copies.
- **Only what changed is uploaded.** While a transcript grows, each sync uploads just the bytes appended since the last one, and once the session goes quiet they are folded back into one object. Raw pieces are zstd-compressed; folds add a consolidated representation without removing earlier pieces.
- **Earlier versions.** Every acknowledged capture, including append intermediates, rewrites, empty files and database generations, remains restorable from immutable v3 records and content-addressed blobs. A file deleted locally gets a new deletion record; its captured bytes remain archived.
- **How each session was launched.** The agent's command line (`--model`, `--permission-mode`, `-p`, `--profile`...), its `CLAUDE_CODE_*`/`CODEX_*`/`PI_*` environment, the session-start hook's payload, and the repo's root, remote, branch, HEAD and dirty flag.
- **The harness each session ran with.** Every CLAUDE.md and AGENTS.md in scope, `@imports`, whole skill directories, subagents, commands, rules, settings, installed plugins, the hook and status-line scripts settings point at, MCP servers and managed settings, plus a separate set for the memory agents write for themselves. Files are stored once by content hash.
- **Secrets.** Credential files are left out (logins, API keys, OAuth tokens), and known secret values in config files, such as `primaryApiKey` and MCP server env and headers, are replaced with `[redacted]`; see [docs/storage-layout.md](docs/storage-layout.md). Everything else can contain anything a tool printed or a settings file holds, so keep the bucket private.

Claude Code deletes transcripts after 30 days by default. `agent-sessions install` raises `cleanupPeriodDays` to 3650 (never lowers it) so nothing is lost while a machine is offline.

Capture can be partial: unreadable sources, failed SQLite exports, malformed config and traversal limits are reported rather than treated as missing files. Partial discovery must not imply local deletion. Snapshot sets expose `complete` and list skipped entries; harness and memory bodies share a 64 MiB budget, with 5 MiB per-file and 3000-files-per-scope limits. FIFOs and devices are not read.

[docs/storage-layout.md](docs/storage-layout.md) describes the bucket layout, the manifest format and exactly what is skipped.

## Archive durability and recovery

The v3 archive is append-only: a typed plan precedes new dependencies and an immutable commit follows their durability. Unique parent chains select current content; conflicting writers fail closed rather than choosing by timestamp. The collector performs no remote deletion or garbage collection, so retained history grows without a fixed retention cap. Keep the bucket private and do not configure lifecycle expiry for archived objects.

SQLite is a disposable cache, bound to the destination, store identity and explicit machine ID. With the same machine ID, losing the local cache triggers paginated remote-only recovery before any new capture, launch or deletion. Legacy remote data is left untouched; the first v3 binding replaces only legacy local upload/session projections. There is no automatic remote migration. One writer per machine ID is required; the local machine/destination lock is independent of the selected cache directory. See [docs/archive-design.md](docs/archive-design.md) for failure boundaries and limits.

## Commands

| Command | What it does |
|---|---|
| `agent-sessions machine-id` | Prints identity. `--new` explicitly creates one for a new machine, never replacing an existing identity. |
| `agent-sessions install` | Hooks, Pi extension, config file. `--dry-run` shows what would change. |
| `agent-sessions doctor` | Checks settings, bucket access, each hook and the binary it runs, Codex hook trust, the last sweep and recent errors. |
| `agent-sessions backfill` | Uploads everything already on the machine. |
| `agent-sessions sync` | Uploads what changed. Hooks run this. |
| `agent-sessions uninstall` | Removes the hooks and the extension. Uploaded data stays. |

Set `AGENT_SESSIONS_DISABLE=1` in an agent's environment to persist a session opt-out. The hook still obtains the session path and writes a private exclusion record, but starts no capture or sweep. Later enabled hooks and sweeps do not re-enable that session. If the agent supplies no session path, the collector cannot guarantee future exclusion; recording failures are best-effort logged and never fail an agent.

The conservative policy also withholds agent-wide files in that source root (shared history, logs, databases, memory and settings), plus its user harness and generated memory snapshots, even for an allowed sibling session. It is not universal DLP for content copied elsewhere. Existing remote bytes are never removed. Opt-outs have no automatic expiry: deleting only a local record cannot clear the archive's monotonic exclusion union. A deliberate archive-identity reset or a future explicit revocation mechanism is required to re-enable capture; changing state directory alone is not a reset, and recovered exclusions must be restored before capture.

Logs are in `~/.local/state/agent-sessions/agent-sessions.log`.

To upgrade, change the tag in your flake input and rebuild, or download and verify the new release's `install.sh` and run it with the new `VERSION`. Releases are immutable, and the script refuses one that GitHub hasn't attested.

To remove the binary too, run `sh install.sh uninstall` from a local copy of the script. It removes the managed hooks and extension first, then the binary, without using the network. Config, credentials, machine identity, local state and all remote data are retained. See [docs/install.md](docs/install.md) for installation, removal and hook-trust verification details.

## Development

Use the locked Nix development shell on Linux (amd64/arm64) or macOS (arm64):

```bash
nix develop
# Or, with direnv and nix-direnv configured:
direnv allow
```

The shell supplies Go 1.27, [golangci-lint](https://golangci-lint.run) at the version in `.golangci-lint-version`, a C compiler for race tests, Make, Git, jq for the Claude hooks, Python 3.14 for verification, ShellCheck/zizmor for workflow checks, and GoReleaser/Syft for releases. Other Go tools stay pinned in `tools/go.mod` and `tools/gopls/go.mod`; Go downloads them on first use. Agent CLIs and live-test credentials are not provisioned.

Commit `flake.lock` when updating dependencies with `nix flake update`; keep Go aligned with `go.mod` and golangci-lint with `.golangci-lint-version`. Without Nix, install those versions and a C compiler manually. `nix fmt flake.nix` formats the flake.

```bash
make hooks   # git hooks: format and lint on commit, tests on push
make check   # every gate CI runs
make gopls   # the pinned gopls, for the Claude Code gopls-lsp plugin
```

For Claude Code, the repo's settings turn on the `gopls-lsp` plugin and the gopls MCP server in `.mcp.json`. Install the plugin once with `claude plugin install gopls-lsp@claude-plugins-official --scope project` after `make gopls`, and approve the MCP server when asked.

[docs/quality-gates.md](docs/quality-gates.md) explains each gate and its settings, and [CLAUDE.md](CLAUDE.md) has the rules for coding agents. Linux and macOS only.

For executable user-path verification, use the [agent-sessions verification skill](.agents/skills/verify-agent-sessions/SKILL.md). It builds the real CLI, drives isolated local journeys and optional sandboxed real-agent hooks, and preserves evidence after cleanup. Its feature map names the remaining gaps; local-store success is not live R2 acceptance. Separately authorised [live R2 acceptance](docs/r2-acceptance.md) exercises the actual backend and archive engine inside a disposable key namespace, with verified cleanup.
