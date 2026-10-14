# Opt-in live-agent acceptance (loopback, local store only)

The regular tests and `make check` **do not require or launch live agents**.
`cmd/agent-sessions/live_test.go` adds an explicitly enabled acceptance test for
installed Claude Code, Codex and Pi. It builds the collector through the existing
end-to-end `TestMain`; no new executable or module is needed.

```sh
AGENT_SESSIONS_LIVE=1 go test -race ./cmd/agent-sessions \
  -run '^TestLiveAgents$' -count=1 -v
```

Select one agent using `-run '^TestLiveAgents/codex$'`. Set
`AGENT_SESSIONS_LIVE_KEEP=1` to retain successful scratch directories. Failures
always retain their evidence. The test logs each directory, executable version,
model request routes, observed lifecycle events, source size/SHA-256, recorded
argv and launch/capture timestamps. Evidence includes agent output, installed
configuration, original transcripts, observer argv/payload files, collector
state/logs and the complete local bucket. These files are private (0600/0700).
Remove retained directories yourself once review is complete.

## Isolation is a precondition, not a best-effort setting

This test currently requires **macOS and `/usr/bin/sandbox-exec`**. It fails if
that boundary is unavailable: there is no unsandboxed fallback. Agent paths are
explicitly `/opt/homebrew/bin/{claude,codex,pi}`; the tested versions are Claude
Code 2.1.296, Codex 0.162.1 and Pi 1.1.0. Different versions may need protocol or
CLI flag updates and should not silently be treated as equivalent.

Each agent receives a fresh `/tmp/memory-live-*` scratch tree with independent
HOME, XDG dirs, CLAUDE_CONFIG_DIR, CODEX_HOME, PI_CODING_AGENT_DIR, workdir,
TMPDIR, collector config/state, coverage output and local bucket. Environment
variables are constructed from an explicit whitelist, **not** `os.Environ()`.
API keys are synthetic and R2 variables are absent. Telemetry, error reporting,
startup model downloads, update checks and MCP discovery are disabled where the
CLI supports them. All model traffic goes to a Go `httptest` server bound to
loopback, implementing minimal Anthropic Messages SSE and OpenAI Responses SSE.
It returns text only, never tool calls, and never proxies anything.

The inherited sandbox forbids all networking except outbound connections to
`localhost:<that server's exact port>`. It denies reads of real user homes,
managed Claude/Codex settings, system keychains and keychain service lookup. It
forbids all writes except scratch, `/dev/null` and PTY devices required by Codex's
interactive CLI. Before installing or running agents, the test proves loopback
is accessible and external networking, writes outside scratch and `/Users`
reads are denied. The external probe targets documentation address `192.0.2.1`
and is rejected inside the sandbox; there is no external model or R2 request.
Detached collector hook processes inherit the same restrictions.

Only the generated collector extension is loaded by Pi. The harness defines no
new extension behavior. Installation affects scratch roots exclusively. Codex
trust entries are written **only** into scratch `config.toml`; the test uses
independently specified canonical identities pinned to
`openai/codex@806d9732c974bc8a51b8317c1bd8985544fe627c`, not the collector's own trust
implementation. Fixed identity fixtures were checked with the independent Rust
schema/normalization program during development. That program is not a runtime
dependency, and no real user trust is changed.

## What constitutes a pass

- A real installed agent produces `ACCEPTANCE_OK` through the loopback model.
- The real agent executes session-start, turn-complete and session-end hooks:
  Claude/Codex `SessionStart`, `Stop`, `SessionEnd`; Pi `session_start`,
  `agent_settled`, `session_shutdown`. The observer preserves argv and the hook's
  JSON payload, decoding one object without waiting for EOF (Codex can leave
  stdin open), then `exec`s the built collector. Pi still uses the installer's
  generated extension.
- **No explicit sync or backfill is invoked after the agent**. Only installed
  hooks may populate the local bucket. The verifier waits for their detached
  work, reads the final source after agent exit, reconstructs its stored pieces,
  and compares every byte, size and SHA-256.
- Recorded launch argv has the **exact ordered suffix** actually launched (Pi's
  node/script prefix differs); recorded time is within real process execution.
  This run deliberately contains no secrets or empty argv words. It does not
  establish arbitrary argv redaction or empty-word preservation.
- The v2 interpreter verifies the legacy `pre_session` label and that the
  snapshot timestamp does not predate launch. That legacy label alone **does
  not prove** pre-prompt capture. The v3 interpreter requires `captured` and
  the actual capture timestamp at/after the recorded launch.

The archive verifier is an isolated wire-format seam. V2 reconstructs manifest
pieces. V3 reads immutable commits under
`v3/machines/<id>/records/<kind>/<stream>/{plans,commits}/`, checks content-hashed
record names, rejects forks/orphan parents/noncontiguous chains, and selects the
unique committed tip. It reconstructs file pieces from encoded-hash blobs and
checks their encoded/decoded hashes and offsets, then associates the separately
published launch envelope by session key. Plans alone never constitute a pass.
This harness is not a replacement for the archive's hostile-store/recovery tests.

## Keeping async hooks alive

A single immediate print/exec exit can kill queued asynchronous turn hooks before
they execute. Claude print **stream-json input** EOF is also nondeterministic for
`SessionEnd` in 2.1.296: repeated acceptance reproduced missing end hooks even
when EOF was withheld until both actual `Stop` and a successful final `result`.
Native debug logs showed cancelled async Stop hooks and no SessionEnd in failed
runs; exact v3 transcript and launch publication still succeeded. Waiting only
for a model result therefore does not establish orderly lifecycle shutdown.

Claude and Codex now run their real interactive CLIs through `/usr/bin/script`,
with a fixed 40×120 PTY, synthetic terminal-query responses and delayed Enter
(avoids paste detection). The harness keeps stdin open, observes actual `Stop`,
sends Claude `/exit` or Codex `/quit`, and **waits for process exit before closing
stdin**. It neither invokes hooks itself nor retries until a lifecycle test
passes. Claude's onboarding, scratch-project trust and synthetic API-key approval
fixture affect only its private config directory. Claude's native debug log is
retained with the evidence. Pi's print mode emits and awaits shutdown normally.
The fake model pauses one second before responding so asynchronous session-start
capture can run while its real agent ancestor is alive. This is controlled
scheduling, not a claim that every immediate teardown is safe.

A reply assertion parses the native JSONL record and requires an **assistant**
message containing exactly `ACCEPTANCE_OK`. A prompt echo cannot satisfy it (the
prompt deliberately contains that same string).

## Offline regression checks and limits

Normal `go test` exercises both SSE formats, rejects unexpected/missing-prompt
requests, checks Codex identities against independent fixtures, proves the
observer does not wait for pipe EOF, and tests the v3 committed-chain selection
against forks, missing parents, corrupt records and uncommitted plans, reconstructs
a source/launch fixture and rejects stale generations, and distinguishes actual
assistant replies from prompt echoes. Live
execution remains separately opt-in.

This validates one no-tool turn per agent on one machine against deterministic
synthetic model replies and a local store. It does **not** claim live R2
acceptance, real model quality, OAuth/keychain authentication, interrupted turns,
compaction, subagents, tools, resumption, model-server TLS, or other installed
agent versions. This native-agent harness intentionally does not exercise R2.
Separately authorised, namespace-isolated live R2 backend and archive-engine validation
is documented in [r2-acceptance.md](r2-acceptance.md).

## Repeated v3 validation

After switching to orderly interactive Claude exit, one fixed-count run of
`-count=3` passed all nine agent sessions under `-race` against the integrated v3
worktree. Every session proved the complete lifecycle, committed byte-exact final
source, ordered launch argv and actual capture timestamp. No lifecycle assertion
was dropped, no explicit sync/backfill was added, and isolation was unchanged.
Representative third repetition:

| Agent | Exact source bytes | Recorded launch | Actual capture |
|---|---:|---:|---:|
| Claude Code 2.1.296 | 234,854 | 1791963143.406818 | 1791963144.851029 |
| Codex 0.162.1 | 40,397 | 1791963149.608167 | 1791963150.369030 |
| Pi 1.1.0 | 3,362 | 1791963152.899059 | 1791963153.868039 |

All reported times are Unix seconds, not a claim of pre-prompt capture. Full
development evidence: `/tmp/memory-v3-live-interactive-three.log`; the invocation
can be reproduced with the command above and `-count=3` instead of `-count=1`.
