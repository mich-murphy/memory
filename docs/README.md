# Documentation map

## Direction and goals

- [DESIGN.md](../DESIGN.md): the authoritative design of the Rust writer. Its section 0 says
  what to read next.
- [Session capture contract](session-capture-contract.md): the retained goals. DESIGN.md
  supersedes its mechanism sections.
- [Delivery handoff](session-capture-handoff.md#current-session-checkpoint): current status,
  ownership and next action.
- [AGENTS.md](../AGENTS.md): contribution guardrails and scope.

## Using and operating `memory`

- [Install](install.md): set up, upgrade, remove, operate, analytics workflow.
- [Hooks](hooks.md): what `memory install` writes for Claude Code, Codex and Pi.
- [Machine identity](machine-identity.md) and [security](security.md).
- [Quality gates](quality-gates.md) and the [Rust workspace guide](../rust/README.md).

## Analytics

- [Analytics tools](../tools/analytics/README.md): importer, report and derived tables.
- [Export contract](session-publication-v1.md): what `memory export` writes.
- [Analytics projection](session-analytics-v1.md): what the importer builds.

## Dated record

- [rust-rewrite/](rust-rewrite/): design amendments, challenge review, briefs, evidence and
  reports.
- [research/](research/): frozen research evidence and the
  [design review](research/session-capture-contract-review.md). Do not rewrite its findings.
