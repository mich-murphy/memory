# 36. `memory install` and `memory uninstall` on macOS (2026-11-18)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 35](35-prefix-guard.md). Evidence goes to `evidence/27-install/`. Text that states a plan
stays as written; later changes are marked.

The acceptance run `20261117-a28bfa4` keeps running while this brief is worked on. Nothing here is
installed or run against the real home directory, the run's state root or R2. Every test sets
`HOME` to a temporary directory with a cleared environment.

## Goal

Cutover step 2 (DESIGN 18) installs `memory` and its hooks. The acceptance run did that by hand:
a plist, eight Claude and six Codex handler entries and a Pi extension, each written outside the
repository. DESIGN 10.1 says hooks are installed per agent "as today" (`docs/hooks.md`), which the
Go installer does (`internal/install`). Nothing in Rust can do it, so the cutover on this Mac,
and later on BC-01, would repeat hand edits of the operator's agent settings.

**INSTALL**: `memory install` writes the launchd agent file, the Claude and Codex hook entries and
the Pi extension for the running binary; `memory uninstall` removes exactly those. Both manage only
their own entries, keep every other byte's meaning and key order, back a file up before changing
it, never rewrite a file they cannot parse, and are idempotent. Neither runs `launchctl`; each
prints the command to run.

Not in this brief: removing the Go hooks, the Go Pi extensions or the Go launchd agents (the Go
binary's own `agent-sessions uninstall` and the capture-v1 hook uninstall do that at cutover);
writing `config.toml` (operator-owned, DESIGN 9.1); raising Claude's `cleanupPeriodDays` (already
3650 on this Mac; Go set it); Linux and systemd (slice 3); Codex trust (a human approves the hooks
in Codex `/hooks`; the command prints that reminder).

## Checkout

`main` is `ae26e3e`. This brief is on `docs/brief-36`. The worker gets `../memory-wt/b36-install`
on `rust/install` from `docs/brief-36`. Crate paths are under `rust/crates/`.

## Facts checked on `ae26e3e`

| Fact | Where |
|---|---|
| Go hook events, async flags and timeouts: Claude `SessionStart`, `Stop`, `StopFailure`, `SubagentStop`, `PreCompact`, `TaskCompleted`, `TeammateIdle` async 30 s, `SessionEnd` sync 5 s; Codex `SessionStart`, `Stop`, `SubagentStop`, `PreCompact` async 30 s, `Interrupt` async 3 s, `SessionEnd` sync 3 s | `internal/install/install.go:31-49`; `docs/hooks.md` |
| The Rust hook accepts exactly those Claude and Codex events, and Pi `session_start`, `agent_settled`, `session_shutdown`; it reads `session_id`, `cwd`, `transcript_path` or Pi's `session_file` from stdin JSON | `memory-core/src/hint.rs:178-256` |
| Go `replaceOurs` puts ours in place of the first of our handlers and drops our others, so Codex trust keys (`<hooks.json>:<event>:<group>:<handler>`) of foreign handlers keep their positions; an empty group is dropped, an event with no group is deleted | `internal/install/install.go:199-271` |
| Go refuses a Codex `hooks.json` whose top level has a key other than `description` and `hooks`, or whose `hooks` is not an object | `internal/install/codexschema.go:14-50` |
| Agent directories: `CLAUDE_CONFIG_DIR` else `~/.claude`; `CODEX_HOME` else `~/.codex`; `PI_CODING_AGENT_DIR` else `~/.pi/agent` | `internal/adapters/{claude,codex,pi}.go` |
| The acceptance run's plist: `RunAtLoad`, `KeepAlive` `{SuccessfulExit false}`, `ThrottleInterval` 60, `ProcessType` `Background`, stdout `/dev/null`, stderr to a log file (DESIGN 11.5) | `~/Library/LaunchAgents/memory-acceptance.plist` (read only) |
| The acceptance Pi extension pipes `{event, session_id, session_file, cwd}` to `memory hook pi` on stdin, detached, errors swallowed | `~/.pi/agent/extensions/memory-acceptance.ts` (read only) |
| Workspace serde_json has `raw_value` and not `preserve_order`; turning `preserve_order` on reorders the three exported schemas and fails `schema_cli_check_dispatches_and_rejects_unknown_commands` (parent probe, worktree removed) | `rust/Cargo.toml:17` |
| This Mac's `settings.json` and `hooks.json` are byte-equal to a 2-space-indented rewrite of their parsed JSON with a trailing newline, non-ASCII unescaped (parent check, counts only) | — |
| State root: `MEMORY_STATE_DIR`, else `$HOME/.local/state/memory`; config at `<root>/config.toml` | `memory/src/state.rs:100-112` |

## Decisions

Each is reversible before cutover. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Commands | `memory install` and `memory uninstall`, each with optional `--dry-run` (no write, same report). Any other argument: `memory: usage`, exit 64 |
| Binary | `std::env::current_exe()`, canonicalized. Hook command: `<q> hook <agent>`, `<q>` the POSIX-quoted path (unquoted when it matches `^[A-Za-z0-9@%+=:,./_-]+$`, else single quotes with `'"'"'`) |
| State root | Install refuses when `MEMORY_STATE_DIR` is set (`install_state_dir_override`, exit 2): the installed hooks and agent run without it, so they must use the default root. It loads `<default root>/config.toml` and its token check and refuses on any config error with that error's code (exit 2), so hooks never point at a daemon that cannot start. Uninstall needs no config |
| Ours | A Claude or Codex handler is ours when its `command` string is exactly `<q> hook <agent>` where `<q>` unquotes to an absolute path whose last component is `memory`, and `<agent>` is the file's agent. Anything else, including the acceptance run's `: memory-acceptance; exec env ...` commands and the Go `agent-sessions ... hook` commands, is foreign and kept |
| Handler shape | `{"type": "command", "command": ..., "async": true, "timeout": N}` for async events, without `async` for the sync ones, keys in that order, as Go writes them |
| Merge | Go's `replaceOurs`/`mergeHooks` rules exactly (facts table). Uninstall is the same merge with no handler of ours |
| JSON | A small ordered JSON type in a new module `memory/src/install/json.rs` (or `install.rs` plus a submodule): object = `Vec<(String, Value)>` in file order, deserialized with a serde visitor from `serde_json` (no new crate, no feature change); numbers kept as `serde_json::Number`; duplicate keys refuse the file. Written 2-space indented with a trailing newline, non-ASCII unescaped. A file that is not a JSON object, or is unparsable, is refused (`install_unparsable`, the file untouched). A missing or blank file is an empty object |
| Codex document | The Go top-level check above; failing it refuses the file (`install_codex_schema`) |
| Pi | `<pi dir>/extensions/memory.ts`, first line `// memory: managed by \`memory install\`; edits are overwritten.`; body as the acceptance extension with the binary path as a JSON string constant and no `MEMORY_STATE_DIR`. An existing file without that first line is foreign: left alone and reported |
| launchd | `~/Library/LaunchAgents/memory.plist`, label `memory`, `ProgramArguments` `[<exe>, "daemon"]`, the acceptance plist's other keys, stderr `~/Library/Logs/memory/daemon.log` (directory created 0700). XML-escaped. Uninstall removes it only if its label is `memory` and its first argument names a `memory` binary. On a non-macOS target the plist step is skipped and reported (`launchd: skipped (not macOS)`); hooks and Pi still run |
| Writes | Unchanged content: no write, no backup. Otherwise: copy the old file to `<path>.memory-backup-<UTC YYYYMMDDTHHMMSSZ>` with its mode, write a temporary sibling, `sync_all`, rename; new files 0644 (plist, hooks, Pi) and the old mode kept for existing ones |
| Report | stdout, one line per file: `wrote <path> (backup <name>)`, `wrote <path>`, `unchanged <path>`, `removed <path>`, `kept <path> (not ours)`; then `next: launchctl bootstrap gui/$(id -u) <plist>` (install) or `next: launchctl bootout gui/$(id -u)/memory` (uninstall) and `next: approve the memory hooks in Codex /hooks` when `hooks.json` changed on install. Refusals: one stderr line `memory: <code>`; a refused file stops the run before any later file is written, and exit is 1 |
| Order | Plist, Claude, Codex, Pi |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | INSTALL | `rust/install` | worker (Sonnet, medium) |

Owns new `memory/src/install.rs` (and submodules), its line in `memory/src/lib.rs` and the two
words in `memory/src/main.rs` (and its module comment), and new tests in
`memory/tests/install.rs`. Read-only everywhere else.

- Proof (each a named test):
  - Ordered JSON: parse then write is byte-identical for fixtures with nested objects, arrays,
    empty containers, unicode, escapes, integers, negative and fractional numbers; duplicate keys
    and non-object documents are refused. A property (`proptest_`-prefixed, under gate 06):
    for generated ordered documents, write(parse(write(x))) == write(x) and keys keep order.
  - Merge: install into a document with foreign handlers before and after ours keeps every foreign
    handler at its index (group and handler) and every other key and its order; a second install
    is byte-identical; an older path of ours is replaced in place; uninstall then leaves exactly
    the document a fresh foreign-only document had (when nothing of ours was there before);
    acceptance-style and Go-style commands are never touched; an event left with no group is
    deleted; a Codex document with an unknown top-level key is refused untouched.
  - Real binary, `HOME` a temp dir, cleared environment, a valid config at the default root:
    install writes all four files and the report; a second install reports `unchanged` for each
    and creates no backup; changed files get exactly one backup with the old bytes and mode;
    `--dry-run` writes nothing; an unparsable `settings.json` exits 1 with
    `memory: install_unparsable`, leaves it byte-identical and writes no later file; a foreign
    `memory.ts` is kept; `MEMORY_STATE_DIR` set exits 2 `install_state_dir_override`; no config
    exits 2 with the config code; uninstall removes ours and the plist and keeps foreign entries;
    `memory install extra` exits 64.
  - The written Pi extension and plist match golden text with the path substituted; the plist
    parses with `plutil -lint` on macOS (test skipped elsewhere, name says so).
  - Hook round trip: the command string written for each agent, run through `sh -c` with a Claude,
    Codex and Pi payload on stdin (Pi through the extension's payload shape), appends one hint
    line to the temp state root.
- Teeth (the parent applies each; each must fail a named test): ours matched by substring
  (`memory hook`) instead of the exact rule; ours appended instead of replaced in place; foreign
  object keys sorted; backup skipped; write without the unchanged check; unparsable file treated
  as empty; Pi marker check removed; `MEMORY_STATE_DIR` check removed.

## Rules for the worker

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply, with those of
briefs 29 to 35. No new dependency and no feature change. No change to stored formats, state
files or log lines. Tests never touch the real `HOME`, `~/Library`, `~/.claude`, `~/.codex` or
`~/.pi`, and never run `launchctl`. Never inspect, signal or read the state of the running
acceptance daemon or anything under `~/.local/state/memory-acceptance*`; never read `.env`; never
contact R2; never use `git stash`. Cargo only through
`nix develop /Users/mm/dev/memory#rust --command sh -c 'cd /Users/mm/dev/memory-wt/b36-install/rust && ...'`,
one cargo job at a time. Commit after each working step.

## Acceptance (parent)

Whole diff read; fmt, clippy, rustdoc, nextest, doctests, schemas, `cargo deny`, lints; the teeth
above; a dry run of the built binary against copies of this Mac's `settings.json` and `hooks.json`
in a temp `HOME` (structure and counts only, no content printed); `scripts/gates.sh` once on the
tip; docs (`docs/hooks.md` note, DESIGN 18 step 2 pointer, handoff); push (secret check of the
range), hosted gates, merge.

## Ledger

- 2026-11-18 11:10Z: brief written on `ae26e3e`.
- 2026-11-18 11:43Z: INSTALL accepted locally at `f3b3deb` (worker 4 commits, parent 1). Changed
  by the parent: numbers keep their exact text (the worker's `serde_json::Number` could change a
  17-digit float); the binary path is used as invoked, not canonicalized, so an upgrade behind a
  stable `memory` link keeps Codex's trust hash, and a binary not named `memory` is refused
  (`install_binary_name`, exit 2). Teeth 10 of 10. Copies of this Mac's settings: foreign
  handlers kept in place, uninstall byte-identical. Full gates on `f3b3deb`: 14 sub-gates 0, seed
  4123598146, coverage 96.55 %. Record: [evidence 27](evidence/27-install/). Next: push, hosted
  gates, merge.
