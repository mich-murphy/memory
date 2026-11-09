# Documentation map

## Authoritative direction

0. **[Rust rewrite design](../DESIGN.md)** — approved and revised to final 2026-11-07
   after the [challenge review](rust-rewrite/challenge/00-synthesis.md). The design for the
   replacement writer, with its evidence in [rust-rewrite/](rust-rewrite/). Start here for
   rewrite work; its section 0 lists what not to take direction from. Read it with the
   [2026-11-09 amendments](rust-rewrite/design-amendments-20261109.md), which win where
   they conflict (bucket, P5, hook argv, symlinks, retry class, test sync policy).
1. **[Session capture contract](session-capture-contract.md)** — retained goals,
   writer/reader/analytics acceptance and running Go-path maintenance contract.
   For Rust, DESIGN.md supersedes its spool/work-queue/whole-object mechanisms.
2. **[Delivery handoff](session-capture-handoff.md#current-session-checkpoint)** — sole
   current status, ownership and next-action brief. Resume at its **Rust implementation
   delivery** section, not a dated Go checkpoint. Local Rust foundation gates and the
   final-toolchain BC-01 synthetic probe passed; the collector/reader and release
   acceptance are incomplete. Earlier Go host-state tables are historical observations,
   not a fresh health check or instructions to enable capture.
3. [Design review and sources](research/session-capture-contract-review.md) — rationale,
   alternatives, industry precedents, and analysis of the legacy consumer mismatch.
4. [Repository guidance](../AGENTS.md) — contribution guardrails and scope.

The [offline replacement slice](session-capture-implementation.md),
[phase-3 candidate](session-capture-phase3.md), [wire/reader v2](session-publication-v1.md),
[analytics v2](session-analytics-v1.md), [enrichment refinement](session-enrichment-contract.md)
and [candidate backends](session-capture-backends.md) document implemented opt-in
Go interfaces, not the Rust roadmap. Their filenames and the `capture-v1` prefix do
not imply v1 wire support.
Unselected collector and analytics defaults remain legacy. For current deployment,
qualification and next action, use the handoff's
[current-session checkpoint](session-capture-handoff.md#current-session-checkpoint).
The [source-bound qualification evidence](session-capture-offline-evidence.md) includes
October 30's actual canonical aggregate pass and successful PR/merged-main CI, followed
by the earlier `74c00f4` staged-prerequisite and October 24 records. It does not establish live
operating acceptance. The [current-state alignment audit](current-state-audit.md) is
also a dated record, not a current-checkout certification or rollout instruction.

## References for the implementation that exists today

These remain useful for safe maintenance and tests, but their archive-specific
mechanisms do not override DESIGN.md for Rust or the retained contract goals:

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

- [Rust workspace guide](../rust/README.md) — implemented scope and gate commands.
- [Rust parent review](rust-rewrite/implementation/02-parent-review.md) — source-bound
  results, corrections, acceptance boundary and remaining work. Reports 00–05 in
  that directory retain worker-specific evidence, not independent release approvals.
- [Latest integrated run, gate 8](rust-rewrite/implementation/evidence/12-slice2-round10/) —
  369/369 nextest, 2,000-case properties, seed 730210, 98.3173% coverage against the
  unchanged 98% floor; current for the uncommitted source as of 2026-11-09.
- [Corrected integrated run](rust-rewrite/implementation/evidence/02-corrected/) — slice-0
  foundation result, superseded for current source by gate 8:
  66 tests, 5 compile-fail doctests, 2,000-case properties, actual source mutation,
  98.1658% line coverage against the unchanged 98% floor, logs and source snapshot.
- [Final BC-01 probe](rust-rewrite/implementation/evidence/03-bc-recheck/README.md) —
  official pinned Rust/LLVM, Hegel static engine, native **x86_64**, 2,000 cases.
  SSH is no longer blocked; this is not the full integrated Linux/ARM/live suite.
- [Production gap register — November 2](production-gap-register-20261102.md) —
  offline experiments against `2deac62`: confirmed liveness/efficiency/cap gaps,
  verified-sound areas, proven weak oracles and ready-to-adopt tests. It is not
  operating acceptance and changes no code.
- [Architecture, test quality and readiness review](architecture-review-20261030.md) —
  current-source findings, focused repairs, mutation proofs and bounded mechanism
  measurements. Its evidence section distinguishes completed checks from pending work;
  it does not authorize installed activation or establish operating acceptance.
- [Adversarial review](adversarial-review-20261029.md) — baseline findings and rationale.
- [Resolution evidence](adversarial-resolution-evidence-20261029.md) — per-item repairs,
  independent reviews and passing combined development gates. This is not installed,
  live-R2 or two-host operating acceptance; the handoff retains that boundary.

## Historical evidence, not rollout instructions

Keep dated results as evidence; do not apply old release/reset plans to the replacement.

- [October 30 handoff archive](session-capture-handoff-archive-20261030.md) — verbatim
  pre-reconciliation ledger, including earlier experimental installs/containment and
  pre-merge tasks. Its “current” checkpoints and worker assignments are historical;
  use the sole current handoff above, not archived rollout commands.
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
