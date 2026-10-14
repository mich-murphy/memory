# Diagnostics and discoverability

A user checks which collector identity/build/destination is in use and sees actionable hook-trust/installation problems rather than mistaking a local stand-in for a working production bucket.

## Sub-features

- `owned-doctor` separate read-only verifier confirms exactly the owned instance/build/config/binding.
- `product-doctor` real diagnostic command reports local backend and unapproved Codex trust honestly.
- `version` real compiled binary reports unique build version.
- `help-gaps` discoverability aliases/error entry points are mapped but not all automated.

## How to get to it (user POV)

- `agent-sessions doctor` (not read-only: R2 probe plus state open).
- `agent-sessions --version`, `-version`, `version`.
- `agent-sessions help`, `-h`, `--help`, per-command `-h`; no command/unknown command returns usage error.
- Skill-only `verify.py doctor RUN` for safe owned-instance selection before driving.

## Driving it with verify.py

Preconditions:

- Launched run ready; helper doctor passes. For product diagnostics use setup's generated installed scratch settings, not real config.

- **Identity/build.** Run `"$H" doctor "$RUN"`. It reads token/config/bucket/SQLite, invokes only `--version` and `machine-id`, and compares runtime inventories before/after. Exit 0 JSON must name owned scratch, exact hash/identity and local stand-in.
- **Product diagnostic.** Run `"$H" drive "$RUN" setup` (or `all`). Real `doctor` expects exit 1 because scratch Codex hooks have not been reviewed; output must warn local directory not R2 and show zero/six trusted hooks. Bucket before/after hashes must match. Installed snapshot preserves the cause, not just output.
- **Proof.** Keep helper JSON if manually capturing stdout; setup action records real diagnostic argv/exit/stdout/stderr and resulting state. Cleanup evidence remains even when the expected product diagnostic exits 1.
- **Unverified discoverability.** Aliases other than `--version`, command help, and usage/unknown-command entry points are explicitly unverified in the automated skill. Do not claim them passed via helper help.

## Gotchas

- Production doctor can initialize/migrate SQLite and performs Put/Get/Delete against R2; never call it read-only or point it at production in this skill.
- Unapproved Codex failure is expected fixture evidence, not proof of real trust. The live harness uses independent pinned scratch trust fixtures; real user approval UI remains unverified.
- `AGENT_SESSIONS_STATE_DIR` is read from environment, not config.env. Config/path/store/machine mismatch must fail selection, not be repaired by doctor.
- Source drift means the build is stale. Launch again; never bypass doctor checks or silently rebuild inside a drive.
