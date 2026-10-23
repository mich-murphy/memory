# Current-state and direction alignment audit

**2026-10-24 (AEDT).** This is a documentation/public-interface consistency audit of
the dirty offline candidate, not deployment or release acceptance. Requirements remain
[the capture contract](session-capture-contract.md) and
[the enrichment contract](session-enrichment-contract.md); current continuation is
[the handoff](session-capture-handoff.md).

## Result and scope

Three medium-effort implementation reviewers used disjoint ownership, followed by an
independent medium-effort read-only review. **No outstanding confirmed contradiction
remains from that review.** This is bounded static consistency plus focused execution,
not a formal proof of every runtime path or a claim that excluded material was audited.

Reviewed current root/agent guidance, session contracts/implementation notes, legacy /
operational documentation, internal package READMEs, analytics README, public Go help /
comments, and analytics docstrings/diagnostics against selected implementations,
constants, Makefile/CI wiring and actual caller defaults. Historical results were not
rewritten into new results. Frozen research, private analytics datasets and source data
were not modified. The operator-blocked verification skill was not read or bypassed;
its inventory remains an explicit audit gap in
[the verification index](verification/README.md).

## Corrections

- Replaced the cumulative handoff's stale opening/current-state tables and old restart
  prompts with current implementation, evidence, boundaries and next actions. Preserved
  the entire former text under the explicitly archived
  [historical ledger](session-capture-progress-history.md).
- Distinguished implemented **publication/reader/report v2** from valid `capture-v1`
  command/prefix, runtime/config v1 and enrichment v1 names. Removed stale proposed-v2,
  local-only factory, unimplemented coordinator/enrichment/aliases and pending-full-gate
  claims from current guidance.
- Aligned explicit Local/R2 backend construction and lazy runtime binding, native SDK /
  transient-hook evidence, independent enrichment and optional repair, descriptor-bound
  source/WAL privacy, and opt-in dashboard/query descriptions with current code.
- Kept legacy defaults and valid legacy v3 safety tests explicit; they are neither the
  replacement roadmap nor proof of its consumer contract. Kept actual native/R2 /
  representative-resource/two-machine/installed-path acceptance and cutover outstanding.
- Corrected internal source/hook README claims about ownership, alias provenance and
  Pi session-start metadata versus path hints. Discovery's enrichment coverage marker
  is not a claim that the independent enrichment lane is absent.
- Corrected the quality-gate claim that every invocation is race/shuffle: full Go test /
  coverage gates use those flags, while fuzzing, benchmarks, mutation and the
  CGO-disabled process-journey build have documented distinct roles.
- Independent review caught a remaining top-level **“local store only”** help claim.
  Corrected it to **“experimental opt-in durable capture/read (Local or configured R2)”**
  and added `TestCaptureBackendRootUsage` covering actual help/missing/unknown command
  output and exit codes. No backend selection, API, schema or runtime algorithm changed.

## Fresh audit validation

Pinned Nix, both live opt-ins unset; log
`/tmp/memory-current-state-audit/validation.log`:

- Full `make fmt-check` and `make lint`: **PASS, zero lint issues**.
- Whole `internal/cli`, CGO/race/shuffle, `-count=1`: **PASS, 33.364s**, including the
  new help regression.
- Fresh CGO-disabled candidate → complete analytics discovery: **73 tests / 22.839s,
  PASS**, no skips/warnings, including all eleven actual-process consumer journeys.
- `node --check tools/analytics/site/app.js`: **PASS**.
- Repository `git diff --check` and Markdown local-link/anchor/whitespace validation:
  **PASS — 38 project Markdown files, 209 local targets and four current-doc anchors**.
  Two blocked-skill links are deliberately not accessed; external URLs and frozen
  research content are not validated by this checker.
- Original `cover.out` remains SHA256
  `70386d5160553c62f5b9cef9c38af0f7273336aebd491000ab9259fd94f117b9`.

Independent reviewer `906ff5a4-b66a-4d1` verified closure of its remaining help finding,
current implementation-document corrections and the saved CLI/analytics results.

The [earlier full offline evidence](session-capture-offline-evidence.md) remains dated
engine-candidate evidence: check **622.40s**, test **460.36s**, coverage **86.8%**,
max CRAP **29.40**, nine × 30s fuzz targets. This wording/help/regression pass did **not**
rerun full `make check`, full-repository `make test` or fuzz. No live/native/host /
installation/inventory/production R2/cutover/deletion action occurred.
