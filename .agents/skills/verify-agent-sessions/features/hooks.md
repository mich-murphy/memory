# Hooks and real-agent lifecycle

Installed hooks must never fail an agent, capture new transcript bytes through detached sync, and preserve launch metadata when real isolated agents execute their lifecycle.

## Sub-features

- `hook-silent` real manual supplied-argv start entry returns 0 and prints nothing.
- `hook-detached` committed source arrives after hook exit without explicit sync.
- `hook-invalid` malformed agent argument still succeeds silently.
- `hook-pi-e2e` existing Pi targeted hook and shutdown sweep fixtures.
- `live-three` optional real Claude/Codex/Pi orderly start/turn/end and committed exact final source/launch.

## How to get to it (user POV)

- Installed Claude hooks: SessionStart, Stop, StopFailure, SubagentStop, PreCompact, TaskCompleted, TeammateIdle, SessionEnd, JSON on stdin to `agent-sessions hook claude`.
- Installed Codex hooks: SessionStart, Stop, SubagentStop, PreCompact, Interrupt, SessionEnd, JSON on stdin to `agent-sessions hook codex`; approve actual hooks through Codex `/hooks`.
- Pi installed extension: session_start, agent_settled, session_shutdown; `agent-sessions hook pi --path FILE --event EVENT`, optional `--source`, `--cwd`, `--argv` supplied by the extension.
- Opening/finishing/exiting a real agent session drives start/turn/end; other events need their specific user preconditions (compaction, tools/subagents/teams/interrupt).

## Driving it with verify.py

Preconditions:

- Fresh launched run/helper doctor; no hooks attempt. For live: exact installed paths, macOS sandbox-exec/script, compatible agent versions and C compiler; read docs/live-acceptance.md. No fallback or real provider credentials.

- **Synthetic supplied-argv start.** Run `"$H" drive "$RUN" hooks`. Native-shaped Claude fixture with owned cwd is fed through real hook SessionStart JSON stdin with explicit `--argv '["claude","synthetic-verification"]'` and owned `--cwd`; assert silence/exit 0, wait for owned detached collector, reconstruct committed bytes exactly. No explicit sync is used for this capture.
- **Synthetic provenance boundary.** Explicit nonempty argv JSON invokes the extension-supplied process path, bypassing ancestor scanning. Require exact `process.argv`, `via: extension`, owned cwd and source root in the committed launch. This is synthetic extension-supplied provenance, NOT native ancestor proof. Never omit or corrupt that argv fixture; only the live sandbox may test native ancestry.
- **Pi existing E2E.** Run `"$H" drive "$RUN" regression` (or `all`). Existing TestScripts verifies Pi detached targeted capture and shutdown sweep including another crashed session.
- **Real lifecycle.** Run `"$H" live "$RUN"` before cleanup. Existing TestLiveAgents uses exact `/opt/homebrew/bin/{claude,codex,pi}`, sandbox with exact loopback port, synthetic models and real interactive/print CLI. Require all three subtest PASS lines, no skips; import regular evidence plus literal symlink metadata (never follow/copy links), preserve originals. Require complete import manifests and hash inventories; a partial import fails the live feature.
- **Proof.** Inspect synthetic actions separately from live observer argv/payload, real agent output, native transcripts, committed reconstructed source hashes and actual launch/capture times.

## Gotchas

- Manual hook payloads are not real-agent lifecycle proof. Real-agent tests cannot be substituted with explicit sync/backfill after the agent.
- Live synthetic one-turn acceptance excludes real-provider quality/auth/TLS, tool turns, interruption, compaction, subagents, teams, resume, arbitrary secret argv redaction and empty argv words.
- All specialized registered Claude/Codex events and Pi `/new`/`fork`/`reload` variants remain unverified in this skill. Installation text proves registration only.
- Live missing agent/sandbox/toolchain is blocked, never skip-as-pass. Harness scratch trust/onboarding and controlled shutdown timing are stand-in setup not performed on a user's real HOME.
