# Working on memory

## Start with the agreed direction

For the Rust rewrite of session capture, read [DESIGN.md](DESIGN.md) first. It is the
authoritative design, approved 2026-11-07, and its section 0 says what to read next and
what not to take direction from. Its evidence is under
[docs/rust-rewrite/](docs/rust-rewrite/). Where the contract or the handoff describe a
mechanism that DESIGN.md replaces (spool, work queue, SQLite job tables, periodic walk as
driver, whole-object upload), DESIGN.md wins. The contract's goals
(session-local publication, immutable evidence, fail-closed privacy, idempotent
at-least-once writes, one authoritative reader) still hold.
[docs/session-capture-contract.md](docs/session-capture-contract.md) remains the
authoritative statement of goals. [docs/README.md](docs/README.md) separates current
references from historical evidence; [docs/install.md](docs/install.md) is how a host is set up.
For continuation, read [docs/session-capture-handoff.md](docs/session-capture-handoff.md)
and update its progress ledger with implemented work, exact checks, and the next action.

- Rust `memory` (DESIGN.md, final 2026-11-07, amendments A0 to A14 folded in 2026-11-12;
  in production on this Mac and dev-01):
  append pieces per session window, delta manifests with checkpoints, no spool or job
  tables, hooks and filesystem notifications with a slow safety reconcile, strict schemas,
  small classified local state (section 9), plaintext storage in the one existing
  `agent-sessions` bucket (acceptance writes only under `rust-acceptance/<run-id>/`), no
  migration of existing R2 data. Measured, not assumed; rerun the experiment scripts before
  changing a number.
- Use one authoritative storage reader for restore and analytics. Release acceptance
  includes actual writer output -> reader -> importer -> event index -> report.
- The old R2 archive (`capture-v1/` and `v3/`) was deleted on 2026-11-09 and the Rust writer
  rebuilds from sources on this Mac and dev-01. Follow DESIGN.md section 18; that the archive
  is gone is not evidence that local sources contain every old byte.
- Preserve identities, learned custom roots, original sources, local research evidence,
  and the secret protections that run in production (credential denylist, the gitleaks
  content scan, the launch argv allowlist).
  Session opt-out is retired (operator decision 2026-11-07, see README). Never print
  credentials or private transcripts.

## Delivery ownership

The operator expects autonomous coordination through delivery, not a sequence of
checkpoints handed back as next steps. A commit, audit report, failed gate or completed
subagent is a milestone, not a stopping condition. Diagnose failures, implement fixes,
independently review changed invariants, rerun unchanged gates and continue the agreed
acceptance/cutover sequence within the authorized scope. Do not ask the operator to
perform work that available tools can complete. Record actual external blockers with
attempted checks; never substitute a green claim or bypass privacy/access boundaries.
Do not add legacy migration/refresh compatibility as a delivery prerequisite. Measure
capacity/retention in the appropriate validated vertical slice, not speculatively.

## Changes and validation

Keep implemented behavior, target requirements, and dated evidence visibly distinct.
Do not delete working security code, assertions, or source data to make the new design
appear complete. Do not relax timeouts, race checks, coverage, or other quality gates.

Use the pinned Nix development environment and the commands in
[docs/quality-gates.md](docs/quality-gates.md). Run focused tests for changed behavior;
state which full/native/live gates were not run. Do not run live ingestion, remote cleanup,
installs, or host changes as an incidental documentation/test action.

Respect preexisting worktree changes and frozen research evidence. Do not restore the
intentionally deleted CLAUDE.md, remove unrelated files, or edit other worktrees to make
the checkout look clean. Use medium-effort subagents for broad audits when requested.
