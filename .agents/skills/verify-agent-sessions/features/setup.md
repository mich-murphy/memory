# Identity and setup

A user explicitly chooses a new machine identity, installs agent hooks without damaging other settings, and later removes only managed entries while preserving their archive identity and data.

## Sub-features

- `identity-new` deliberate new ID, print, and nonrotation on repeated `--new`.
- `identity-recovery` missing ID fails; known previous override prints without creation.
- `install-all` hooks/extension for all present scratch agents; repeatability and foreign settings preservation.
- `install-select` selected Pi installation/removal; existing E2E repeated Claude/Codex selection.
- `install-dry` install/uninstall dry-run leaves every scratch file hash unchanged.
- `uninstall-preserve` removes managed entries, preserves foreign hook/model and identity.

## How to get to it (user POV)

- `agent-sessions machine-id`, `agent-sessions machine-id --new`.
- Select known ID via `AGENT_SESSIONS_MACHINE_ID` environment/config or restore original private identity file; never use a new ID to recover history.
- `agent-sessions install`, `install --agent claude --agent codex`, `install --agent pi`, with optional `--dry-run`.
- `agent-sessions uninstall` and selected `--agent`/`--dry-run`.
- `VERSION=vX.Y.Z sh install.sh [install]`, `sh install.sh uninstall`, `BIN_DIR`: release script entry points, not automated here (`internal/install/shell_test.go` covers them with a fake `gh`).

## Driving it with verify.py

Preconditions:

- Fresh launched run and helper doctor pass; no prior setup attempt. Scratch config contains no credentials. Compatible tools/cache as in SKILL.md.

- **Identity.** Run `"$H" drive "$RUN" setup`. Journey invokes real `machine-id`, repeated `--new`, missing-config `machine-id` expecting exit 1 and zero creation, and known-ID environment override. Every successful output equals launch ID.
- **Install.** Same journey seeds private foreign settings and invokes dry-run, all-present install and reinstall. Assert eight Claude events, six Codex events, three Pi event strings, foreign hook/model and retention preserved. `states/*-installed` is a second view of the resulting settings.
- **Remove.** Same journey checks uninstall dry-run invariance, then real all-agent uninstall and selected Pi install/uninstall. Managed Codex/Pi files disappear; foreign Claude hook/model and original identity remain.
- **Selected Claude/Codex entries.** Run `"$H" drive "$RUN" regression` or full `all`; existing `TestScripts/install` invokes repeated `--agent claude --agent codex` and asserts preservation/removal.
- **Proof.** Inspect setup/regression actions, exit codes and installed/final state hash inventories. Do not equate installed event text with real-agent execution.

## Gotchas

- Synthetic config directories make agents “present”; installation does not require executing a real agent. Real Codex approval belongs to `/hooks` and is intentionally not forged locally.
- Environment overrides take precedence over file values. Only the environment override recovery entry is automated; direct restored identity-file and config override journeys remain unverified here.
- Real release download, attestation verification, upgrade and release-script uninstall are unverified here; never reach out to real release/config/credentials from this skill.
- Malformed existing settings, symlink installs and other variants are project regression coverage, not claims from this seed journey.
