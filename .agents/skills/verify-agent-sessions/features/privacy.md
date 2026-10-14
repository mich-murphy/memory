# Privacy and partial observation

A user can persist a session opt-out, withhold known credentials/config secrets, and distinguish incomplete discovery from genuine local deletion without losing previously archived data.

## Sub-features

- `known-policy` recognized config redacts literal secrets; credential backups are excluded.
- `not-dlp` unrecognized ordinary file remains byte-exact, even with a synthetic secret-like value.
- `malformed` protected parse failure reports errors and retains previous generation.
- `partial` unavailable discovered source reports error, not deletion.
- `optout` disabled hook persists exclusion without capture; allowed sibling remains capturable.
- `cold-exclusion` remote monotonic exclusion recovers before backfill; shared root history stays withheld.

## How to get to it (user POV)

- `AGENT_SESSIONS_DISABLE=1` in the agent environment; actual agent supplies transcript path to installed hook/extension.
- `agent-sessions hook pi --path FILE` is the synthetic manual entry used here; Claude/Codex provide JSON stdin.
- `agent-sessions sync --agent pi` to retry after malformed/unavailable input is repaired.
- `agent-sessions backfill` with same identity/destination after cache loss.

## Driving it with verify.py

Preconditions:

- Fresh launched run and helper doctor; no privacy attempt. Synthetic values only. `all` orders capture before privacy. No real credentials may be seeded.

- **Policy.** Run `"$H" drive "$RUN" privacy`. The journey seeds synthetic Pi models/auth/auth-backup/unknown content, invokes real selected sync, asserts `[redacted]`, missing credential generations and unchanged unknown bytes.
- **Malformed input.** Same journey corrupts models.json, expects sync exit 1/error, and asserts no new protected generation. Repairs it for subsequent steps; previous archive capture remains.
- **Partial observation.** Same journey captures a source, temporarily replaces it with a self-referential symlink loop (ELOOP rather than ordinary missing-target absence), expects error and no new deletion generation, and removes the symlink before evidence/cleanup.
- **Opt-out.** Same journey seeds denied/allowed sibling and previously uncaptured shared history. Disabled real Pi hook must exit 0 silently, leave bucket hashes unchanged and write private exclusion. Enabled sync captures sibling, not denied/shared history. A later enabled real hook to the denied path must stay silent; after quiescence, no new commits or pending launch/capture may remain.
- **Cold recovery.** Same journey deletes entire local state, invokes real backfill, and asserts denied source still exists locally but remains uncaptured; shared history still withheld. Absence of source cannot falsely satisfy this exclusion proof.
- **Proof.** Inspect action exits/output, observations and state/exclusion/bucket hashes. Known-policy failure is not silently treated as a file missing.

## Gotchas

- This policy is not arbitrary transcript DLP. Synthetic secret-like unknown content is intentionally captured to demonstrate that boundary.
- Existing remote bytes are never removed; opt-out applies to future capture and recovered union, not retroactive erasure. Do not seed shared history before this journey's exclusion and then expect archive deletion.
- This local journey opts out Pi only; cross-agent roots, missing hook session path, write failure, unreadable chmod sources and snapshot budgets have additional tests but are not blanket-verified here.
- Replacing a source with a self-referential symlink loop (ELOOP rather than ordinary missing-target absence) is verification scaffolding, not a new product control API. It is removed in a finally block; failures still retain CLI evidence.
