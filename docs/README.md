# Documentation map

## Authoritative direction

1. **[Session capture contract](session-capture-contract.md)** — agreed replacement
   architecture, failure semantics, writer/reader/analytics acceptance, and controlled
   rebuild of this Mac and BC-01. Start here for new implementation work.
2. **[Implementation handoff](session-capture-handoff.md)** — exact current state,
   phased implementation details, validation/cutover gates, and next-session commands.
3. [Design review and sources](research/session-capture-contract-review.md) — rationale,
   alternatives, industry precedents, and analysis of the legacy consumer mismatch.
4. [Repository guidance](../AGENTS.md) — contribution guardrails and scope.

The [offline replacement slice](session-capture-implementation.md),
[phase-3 candidate](session-capture-phase3.md), [wire/reader v2](session-publication-v1.md),
[analytics v2](session-analytics-v1.md), [enrichment refinement](session-enrichment-contract.md)
and [candidate backends](session-capture-backends.md) document implemented opt-in
interfaces. Their filenames and the `capture-v1` prefix do not imply v1 wire support.
Unselected collector and analytics defaults remain legacy. For current deployment,
qualification and next action, use the handoff's
[current-session checkpoint](session-capture-handoff.md#current-session-checkpoint).
The [2026-10-24 offline evidence](session-capture-offline-evidence.md) and
[current-state alignment audit](current-state-audit.md) are dated records, not current
operating acceptance or rollout instructions.

## References for the implementation that exists today

These remain useful for safe maintenance and tests, but their archive-specific
mechanisms do not override the replacement contract:

- [Install](install.md), [machine identity](machine-identity.md), [hooks](hooks.md).
- [Security and capture policy](security.md), [quality gates](quality-gates.md),
  [gate-runtime diagnosis](quality-gate-runtime.md), [naming](naming.md).
- [v3 archive design](archive-design.md), [v3 storage layout](storage-layout.md).
- [Verification](verification.md), [verification harness](verification/README.md),
  [verification skill](../.agents/skills/verify-agent-sessions/SKILL.md).
- [Analytics tools and caller modes](../tools/analytics/README.md) — preserved legacy
  inspection/defaults and explicit publication-backed import/query/dashboard; candidate
  consumers use the authoritative reader.

## Current development review evidence

- [Adversarial review](adversarial-review-20261029.md) — baseline findings and rationale.
- [Resolution evidence](adversarial-resolution-evidence-20261029.md) — per-item repairs,
  independent reviews and passing combined development gates. This is not installed,
  live-R2 or two-host operating acceptance; the handoff retains that boundary.

## Historical evidence, not rollout instructions

Keep dated results as evidence; do not apply old release/reset plans to the replacement.

- [Historical implementation ledger](session-capture-progress-history.md) — preserved
  intermediate plans, failures and repaired findings; not current continuation tasks.
- [Production-readiness record](production-readiness.md).
- [Acceptance review](acceptance-review.md), [live acceptance](live-acceptance.md),
  [R2 acceptance](r2-acceptance.md), [live chunks](live-chunks.md).
- [Benchmark methodology/results](benchmarks.md), [chunk experiment](chunk-experiment.md),
  [archive pieces implementation](archive-pieces-implementation.md).

`research/agent-navigation-context-graphs/` and related research handovers concern
separate experiments and contain frozen evidence. They are not the collector roadmap;
do not rewrite their findings or recorded hashes to align them with a new storage format.
