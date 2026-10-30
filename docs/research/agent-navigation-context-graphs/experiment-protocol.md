# Agent navigation experiment protocol

Recorded 2026-10-20 before candidate/dataset completion. Coordinator: current coding session; all delegated work requests medium reasoning effort.

## Question

For `bsncraft/businesscraft`, does an explicit relation graph improve finding relevant code/docs beyond scoped search and a small Markdown map, enough to justify its maintenance and runtime costs?

## Candidates

1. Existing scoped `git ls-files` / `rg` exploration (real-agent baseline).
2. Small human-readable area/router map plus scoped search.
3. Local lexical path/heading/content index, with the same bounded output interface as (4).
4. Lexical index plus explicit typed relations, bounded graph expansion and authored domain routes (hybrid). Record ablations so a lexical index improvement is not misattributed to a graph.

No graph database server, embeddings, remote LLM indexing, or inferred human ownership is assumed necessary. Research may identify a reason to add a candidate, but additions and their timing must be disclosed.

## Corpus and task isolation

- Target: tracked files in the local BusinessCraft checkout, current HEAD recorded with each run. Do not traverse dependency trees, dumps, other worktrees, credentials, disabled instruction/skill contents, or arbitrary symlink targets.
- Ground truth: sanitized paraphrases of actual tasks in the existing R2-derived analytics snapshot, independently selected before algorithms inspect judgments; validated current paths, with provenance trace IDs, source line and hashes. No raw transcripts enter the evidence repository.
- Development and test splits frozen in `benchmark/tasks.json`; candidate implementation worker is instructed not to read it. No candidate-specific tuning on held-out results. If fixes are made after test evaluation, preserve first results and label reruns as post-test iterations.
- Historical tasks/paths can be stale. Judgments are current-entrypoint judgments, not a claim to reproduce the old checkout or full task outcome.

## Retrieval measurements

For each task/candidate save returned paths, scores/reasons if available, raw output UTF-8 byte count, estimated context size, latency and ranking relevance judgments. Measure top-5/top-10 hit, reciprocal rank, and graded coverage where reasonable. Report docs vs source retrieval, not only a combined hit that can hide missing implementations.

Use equal result budgets when comparing index candidates. Output bytes are exact; any token proxy is explicitly an estimate, not provider billing. Separate cold build, freshness checking and warm query latency. Repeat timings; disclose filesystem/cache effects. Query execution order should be interleaved/rotated.

## Agent measurements

Where practical, fresh medium-effort subagents perform matched bounded file-location tasks under baseline/map/hybrid instructions. Same model, task wording, result limit and search-call budget. Capture actual tool calls/results and model usage from **new experimental runs**, not historical local Pi files. Agent performance is distinct from algorithmic retrieval. A small pilot is not a statistically powered demonstration of downstream coding improvements.

## Validation

Independent review of corpus safety, link/import extraction, ownership semantics, stale-index behavior, zero-result behavior, context cap, evaluation leakage and scoring. Run deterministic tests and exercise real source/document queries. Validate portability on bc-01 if practical using isolated temporary files without modifying its BusinessCraft checkout.

## Adoption rule

Choose the smallest maintainable method that meets accuracy requirements with low returned context and latency. Prefer Markdown alone if indexing adds no useful retrieval; prefer lexical+router if graph expansion adds no reliable uplift. Ship a graph layer only to the extent supported by measured results, exposing graph relations for targeted follow-up rather than silently flooding first results. No guaranteed percentage token savings or cost claims from call counts alone.

## Evidence and privacy

Research, scripts, dataset, first and later results, tests and conclusions are kept under this directory in the `memory` repository. Deployable artifacts and a compact usage guide may be added to BusinessCraft once validated. Derived local indices and private raw experiment transcripts remain outside tracked evidence. Preserve unrelated local changes; do not commit or deploy automatically.
