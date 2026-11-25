# How `memory` hooks into each agent

`memory install` writes the hook entries below, each running `<path>/memory hook <agent>`.
A hook only appends one hint line to the day file in the state directory's hints directory;
the daemon reads the hints and captures. The hook exits 0 on every path, prints nothing,
never retries and never waits for the daemon. It stops reading stdin at the end of the JSON
payload and gives up after 2 s if none arrives. Hints are discovery hints, not capture:
bytes count as captured only once the daemon has committed them (see the
[capture contract](session-capture-contract.md) and [DESIGN.md](../DESIGN.md)).

Why the hook is silent and always exits 0:

- Claude Code treats `Stop` exit code 2 as "keep working".
- Codex fails a synchronous Stop hook whose stdout is not JSON, so nothing is printed.
- `SessionEnd` gets about 1.5 s (Claude Code) or 1 s by default and at most 3 s (Codex), so
  the hook does no work beyond the append.

On `SessionStart` the hook also records the agent's launch argv and an allowlisted
environment, with secret-looking values dropped (see [security](security.md)).

## Claude Code

Entries in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`), all running
`memory hook claude`:

| Event | Async | Timeout |
|---|---|---|
| `SessionStart`, `Stop`, `StopFailure`, `SubagentStop`, `PreCompact`, `TaskCompleted`, `TeammateIdle` | yes | 30 s |
| `SessionEnd` | no | 5 s |

- `Stop` does not fire when you interrupt Claude; the next turn's `Stop` or `SessionEnd`,
  or the daemon's safety reconcile, covers it.
- `StopFailure` covers turns that end on an API error.
- `SubagentStop`, `PreCompact`, `TaskCompleted` and `TeammateIdle` name sources that Claude
  Code rewrites or deletes (subagent files, the pre-compaction transcript, team task lists).
- `SessionEnd` fires on `/clear`, `/resume`, exit and logout.
- Claude Code deletes transcripts and related files after `cleanupPeriodDays` (30 by default);
  raising it reduces the risk of losing uncaptured content.

## Codex

`~/.codex/hooks.json` (or `$CODEX_HOME/hooks.json`), running `memory hook codex`:

| Event | Async | Timeout |
|---|---|---|
| `SessionStart`, `Stop`, `SubagentStop`, `PreCompact` | yes | 30 s |
| `Interrupt` | yes | 3 s |
| `SessionEnd` | no | 3 s |

- **Trust.** Codex skips a user hook until it has been reviewed in `/hooks`. Its trust record
  is a hash of the command string, so a changed command needs approving again. The hook
  command names the stable binary path, so an upgrade does not. `memory install` leaves the
  review to you and prints it as a `next:` line.
- Hooks run through `$SHELL -lc`, so your login profile runs first; that is another reason
  the turn hooks are async.
- The hook's `transcript_path` is null for sessions Codex does not persist; there is nothing
  to capture for those.
- Rollouts untouched for 7 days are recompressed to `.jsonl.zst`, and resuming one
  decompresses it again. `memory` keys a rollout without the date folder or `.zst`, decodes a
  `.zst` at capture, prefers a plain file over its `.zst` and verifies a file with a new
  inode against the stored digest instead of uploading it again (DESIGN D48).
- State databases (`state_<n>.sqlite` and similar) hold thread metadata that exists nowhere
  else; the rollout is the agent's persisted record, not proof that every transient event was
  observed.

## Pi

`memory install` writes the extension `~/.pi/agent/extensions/memory.ts` (or under
`$PI_CODING_AGENT_DIR`). Pi loads user extensions without a build step. The extension pipes
`{event, session_id, session_file, cwd}` to `memory hook pi` from a detached process and
registers three handlers, and nothing else at load:

- `session_start`: startup, `/new`, `/resume`, `/fork` and `/reload`.
- `agent_settled`: a prompt has finished, with no retry, compaction or follow-up left.
- `session_shutdown`: quit and the same transitions; SIGTERM and SIGHUP also emit it.

Pi awaits these handlers, so the extension only spawns the detached hook and returns.

- Pi creates the session file only once the session has a message, so an early event may
  name a file that does not exist yet; the next event or the reconcile picks it up.
- `--no-session` writes nothing and the extension does nothing.
- Sessions can live elsewhere (`--session-dir`, `PI_CODING_AGENT_SESSION_DIR`, the
  `sessionDir` setting). Configure that directory as a root in `config.toml`.
- `/tree` branches stay in one file; `/fork` and `/clone` write new files that name their
  `parentSession`.

## Edge cases

- **Config dirs set only in your shell.** `CLAUDE_CONFIG_DIR`, `CODEX_HOME` and Pi's session
  directory can differ between shells. The daemon watches only the roots in `config.toml`.
- **Half-written lines.** A transcript that ends mid-line is captured up to its last complete
  line and read again on the next round.
- **Settings files.** The installer never rewrites a settings file it cannot parse. It backs
  up every file before changing it, keeps other hooks in place and replaces only its own
  entries, each where it is, so a reinstall moves no hook (Codex keys each trust record by
  the hook's position).
- **No hook, no problem.** The daemon runs a full reconcile at start and every 15 minutes, so
  a crashed session or a missed hook is captured without another agent event, provided the
  source bytes still exist.
