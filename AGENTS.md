# Working on memory

## Start with the agreed direction

Read [docs/session-capture-contract.md](docs/session-capture-contract.md) before capture,
recovery, storage, reader, analytics, or rollout work. It is the authoritative replacement
direction, not implementation or release evidence by itself. [docs/README.md](docs/README.md)
separates current implementation references from historical evidence. The opt-in offline
candidate and passing development gates are recorded in the current handoff and
[docs/session-capture-offline-evidence.md](docs/session-capture-offline-evidence.md);
legacy defaults remain until controlled acceptance/cutover.
For continuation, read [docs/session-capture-handoff.md](docs/session-capture-handoff.md)
and update its progress ledger with implemented work, exact checks, and the next action.

- Build independent session capture with a durable spool/work queue, per-source progress,
  autonomous bounded retries, and explicit quarantine/coverage/freshness.
- Start with complete source-epoch snapshots and complete manifests. Chunking is an
  optional measured optimization, not a requirement to replay collector history.
- Use one authoritative storage reader for restore and analytics. Release acceptance
  includes actual writer output -> reader -> importer -> event index -> report.
- Do not resume the old concurrent-cold-recovery/pruned-piece patch stack or add retired
  schema compatibility as prerequisites. Legacy code/tests describe existing default
  behavior; preserve their working safety coverage until replaced and tested, not as
  future design. Candidate code/tests implement the independent path separately.
- The operator permits replacing R2 from sources on this Mac and BC-01. Follow the
  controlled inventory/validation/cutover/deletion sequence; permission is not evidence
  that local sources contain every old byte, or that any reset has happened.
- Preserve identities, effective privacy exclusions, learned custom roots, original
  sources, and local research evidence. Never print credentials or private transcripts.

## Changes and validation

Keep implemented behavior, target requirements, and dated evidence visibly distinct.
Do not delete working security code, assertions, or source data to make the new design
appear complete. Do not relax timeouts, race checks, coverage, or other quality gates.

Use the pinned Nix development environment and the commands in
[docs/quality-gates.md](docs/quality-gates.md). Run focused tests for changed behavior;
state which full/native/live gates were not run. A legacy suite passing does not prove
the replacement's consumer contract. Do not run live ingestion, remote cleanup, installs,
or host changes as an incidental documentation/test action.

Respect preexisting worktree changes and frozen research evidence. Do not restore the
intentionally deleted CLAUDE.md, remove unrelated files, or edit other worktrees to make
the checkout look clean. Use medium-effort subagents for broad audits when requested.
