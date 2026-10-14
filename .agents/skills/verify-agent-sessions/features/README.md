# agent-sessions verification map

This is the maintained source for user-visible verification. Read each matching recipe; the helper's `all` executes every **automated local** journey, not every supported production entry point. See SKILL.md for exact launch, doctor, evidence and cleanup commands.

## Baseline preconditions

- Launch a fresh empty private run using executable `verify.py`; save its printed canonical evidence directory as `RUN` and require exit 0.
- Python 3.14 stdlib zstd/SQLite, compatible installed Go, populated module cache. Network downloads disabled; missing prerequisites fail explicitly.
- Helper doctor passes: exact owned build, config, machine ID, Local bucket and SQLite binding. Never drive a real HOME/R2/config/credential.
- Synthetic transcript fixtures and seed config are created by the journey, not reused from earlier conversations. Host managed/ancestor settings must be absent for local capture.
- Each feature argument is one-shot. Use fresh launch for retry/individual journeys; `all` orders shared preconditions correctly.

## Driving conventions

`H=.agents/skills/verify-agent-sessions/verify.py` (from repo root). Use `"$H" drive "$RUN" all` for setup, capture, privacy, hooks and regression. Diagnostic assertions are part of setup. All real CLI actions and their exact resolved paths are saved in `run.json`; collector cwd is scratch work, Go cwd is checkout. Doctor is read-only and not the production diagnostic command. Evidence includes action/argv/input/exit/stdout/stderr and resulting private trees/hashes. Cleanup removes scratch, never proof.

## Proof and gap reporting

Report the feature ID **and entry point**: passed, failed, blocked, or unverified. Never turn a missing precondition/skip into pass via another path. All-local does not establish R2, real providers, release download, or another OS runtime. Manual hooks use synthetic payloads; only opt-in existing live harness proves real-agent lifecycle and launch metadata. There is no user restore CLI; internal Restore tests are labeled supplemental. Unlisted exotic flags/errors and every snapshot/export source are not exhaustively tested by this seed map; project gates remain separate.

## Features and exhaustive entry-point inventory

- [Identity and setup](setup.md): `machine-id [--new]`, config/environment known-ID selection, `install`/`uninstall` all-present and repeated `--agent`, `--dry-run`; installer script install/upgrade/removal entries and explicit gaps.
- [Capture and archive recovery](capture.md): `backfill [--agent NAME ...]`, `sync` all agents and `--agent NAME --path PATH`; immutable changed/unchanged/history/empty/deletion and cache/source-loss recovery. Internal restore supplemental, not a CLI.
- [Privacy and partial observation](privacy.md): `AGENT_SESSIONS_DISABLE=1`, hook with session path, known config/credential policy, partial discovery, monotonic exclusion recovery.
- [Hooks and real lifecycle](hooks.md): `hook claude|codex` JSON stdin, `hook pi --path` extension, every installed event and real sandbox harness. Synthetic supplied-argv SessionStart plus existing Pi e2e are local; real lifecycles require optional live.
- [Diagnostics and command discoverability](diagnostics.md): `doctor`, helper doctor, `--version`/`-version`/`version`, `help`/`-h`/`--help`, per-command `-h` and unknown/no-command handling; tested versus unverified aliases distinguished.

Public synchronization flags: repeatable `--agent`, repeatable sync `--path`, `--delay`, `--quiet`. Internal hook-generated sync flags are `--pending-marker`, `--launch`, `--trigger`; existing E2E exercises detached delay/marker/trigger through hooks, not a claim that every spelling/combination was driven manually. `backfill --agent`, multiple repeated paths/agents, manually selected delay/quiet/lock contention, and help/version aliases other than `--version` are **unverified** in this helper. Valid invocation syntax is obtained with real command help, never an invented restore command.

Installed Claude entry events: SessionStart, Stop, StopFailure, SubagentStop, PreCompact, TaskCompleted, TeammateIdle, SessionEnd. Codex: SessionStart, Stop, SubagentStop, PreCompact, Interrupt, SessionEnd. Pi: session_start, agent_settled, session_shutdown. Installation registers all; live only executes start/turn/end; specialized events remain unverified here. Agent trust UI, optional dirs/config layouts and nonstandard sessions retain their documented prerequisites, not synthetic happy claims.
