# BusinessCraft navigation: evidence-led decision

**Date:** 2026-10-20. **Decision:** keep the existing human repository/source maps, add a disposable local lexical index, and expose the typed graph as an explicit follow-up tool. **Do not automatically expand a graph into every answer or load a large map into every agent prompt.** No Neo4j service, embeddings, LLM extraction, or Microsoft GraphRAG pipeline is required by the evidence collected here.

This directory is the canonical proof bundle in `mich-murphy/memory`. Deployable files, navigation links, and a byte-identical proof mirror under `tools/navigation/benchmarks/context-graphs/` were added to the separate local `bsncraft/businesscraft` checkout. The existing `benchmarks/` exclusion keeps that evidence/tasks out of the navigation index. Raw sessions/cache databases/the uncompressed 123 MB query file are not mirrored. Nothing was committed, pushed, or deployed remotely.

## What Uber establishes—and what it does not

[Uber's Databook (2020)](https://www.uber.com/us/en/blog/metadata-insights-databook/) explicitly connects teams, datasets, dashboards, metrics and pipelines. Its graph representation coexists with MySQL persistence and event-derived Elasticsearch indices. This supports **typed relationships + lexical discovery + maintained provenance**, not a claim that LLM-generated GraphRAG improves coding-agent accuracy. The specific uSearch identification was not verified. See [source-backed research](research.md) for primary sources, GraphRAG evaluation scope and limitations.

For BusinessCraft, ownership comes only from `.github/CODEOWNERS`; most Web/Agent/docs ownership remains unknown. Git blame, directory names, and a plausible model response are not accountable ownership.

## The structure selected

```text
BusinessCraft/
├── README.md                         # discovery link, not a giant injected map
├── docs/repository/README.md          # existing human area map + lookup workflow
├── docs/knowledge/...                 # existing authored business/source maps
└── tools/navigation/
    ├── nav.py                        # bounded locate / query / related
    ├── routing.json                  # 12 broad routes, no task-answer catalogue
    ├── map.md                        # points to the existing human authority
    ├── README.md                     # operations, fallback and freshness contract
    ├── test_nav.py, test_runtime.py   # 37 tests
    └── ADAPTATION.md, SOURCE_HASHES.json, frozen-nav.diff

~/.cache/businesscraft-navigation/<worktree-id>/index.sqlite3
    # disposable, private, branch/worktree-aware source/edge/FTS cache; not in Git
```

The measured corpus contained **30,788 eligible files**, approximately **174,865 typed edges**, and a roughly **417 MiB** index. Final build took **24.06 seconds** on this Mac; generated navigation files remain untracked until reviewed/staged, so they were not ingestion inputs. The deployed corpus has two additional tracked guide edits compared with the frozen experiment. New tracked files/HEAD changes require a rebuild.

```text
locate information
  already know the file?       → read it directly
  unsure of the subsystem?    → existing human map
  have distinctive terms?     → bounded lexical locate
  need a cross-file connection?→ explicit related(path)
  missing a required facet?   → scoped rg / source inspection
  delegating or compacting?   → pass paths, findings and remaining questions
```

Example verified graph traversal: the Purchase Orders source map links to `BsnPoGetVenEmail.dbl` and `po_calc_line_cost.dbl`; `related` returns those source citations, incoming documentation links, source-line provenance and declared Classic owners in **1,877 bytes**. [Actual output](benchmark/related-source-map-smoke.json). A citation proves a recorded relationship, not runtime behavior or feature parity.

## Experiment design and provenance

- [Protocol](experiment-protocol.md) recorded before candidate/dataset completion.
- **20 sanitized real archive-derived tasks**, fixed **8 development / 12 test**, 98 judgments; queries/families split before implementation outcomes. [Dataset and caveats](benchmark/dataset.md).
- Historical tasks came from the **R2-imported Oct 16 snapshot**, not the machine's historical Pi logs. Live read-only R2 GETs verified all 20 task references against **17 immutable archive descriptors**. [R2 proof](benchmark/r2-provenance.json).
- **400 deterministic retrieval measurements**: five methods × 20 tasks × first process query plus three repetitions; rotated order, shared allowed corpus, top-10/9,600-byte ceiling. [Scoring](benchmark/SCORING.md).
- **Six fresh medium-effort same-model agents** (`openai/gpt-6.1-sol`, recorded source model string) initially tried baseline/map/hybrid navigation, two bundles of six questions per condition, 18 tool invocations per bundle. Two additional fresh agents tested the adapted runtime/workflow on the **same** tasks. Those follow-ups are exploratory, not another heldout sample. [Exact prompts](benchmark/pilot-prompts.json), [follow-up prompts](benchmark/pilot-followup-prompts.json).
- New experimental agent logs were used only for measurement; they were not the source of historical tasks. Raw transcripts stay private. Checked-in records retain sanitized answers, aggregate bytes/usage, source hashes and reproducible prompts.

## Results: graph expansion did not earn the default

Frozen automatic retrieval on the **11 answerable held-out tasks**:

| Method | Exact-judged hit@10 | MRR | Mean returned bytes¹ | Warm wall p50 / p95² |
|---|---:|---:|---:|---:|
| Scoped filename/rg | 2/11 | 0.182 | 1,547 | 1.151 / 3.524 s |
| Router only | 1/11 | 0.091 | 926 | 3.168 / 3.425 s |
| Lexical index | 2/11 | 0.114 | 2,059 | 3.257 / 3.567 s |
| Router + lexical | 2/11 | 0.114 | 2,227 | 3.448 / 3.811 s |
| Router + lexical + graph | 2/11 | 0.114 | 4,051 | 3.400 / 3.666 s |

¹ Returned-byte mean includes the external-evidence question; it has no positive repository qrels and is excluded from quality denominators. ² Timing pools development/test repetitions and includes interpreter/freshness overhead, not just ranking. Router-only unnecessarily shares the prototype's index freshness cost; this is **not** a measure of reading Markdown. Native envelopes differ in verbosity despite the common cap. These measurements do not establish a universal graph ranking result.

All methods struggled with full natural-language questions; **query formulation and scoped source inspection remain essential**. Graph/map additions offered no observed judged retrieval uplift and cost more output. The lexical/graph ranking deliberately preserves the top lexical seeds, limiting the possible graph improvement. One positive development judgment was outside the allowed generated/oversize corpus; it was disclosed, not silently removed. Judgments are incomplete: unjudged files are unknown, not proven irrelevant.

### Real-agent context measurements

Each row aggregates two six-question runs. File-location hit is not complete task correctness.

| Agent workflow | Tool-result text | Reported input / cache-read tokens³ | Tool calls | Wall sum | Frozen judged hit@5 |
|---|---:|---:|---:|---:|---:|
| Existing scoped search | 631,225 B | 198,127 / 1,355,136 | 36 | 290.3 s | 11/11 |
| Compact map + search | 488,745 B | 136,598 / 1,044,096 | 35 | 304.3 s | 10/11 |
| Initial hybrid index | 282,303 B | 86,158 / 693,760 | 36 | 319.2 s | 10/11 |
| Adapted lexical + scoped fallback⁴ | 395,545 B | 110,146 / 737,792 | 35 | 294.9 s | 10/11 |

³ Provider-reported fields, retained separately. They include repeated request prefixes, are not unique-context counts, and do not establish dollar savings. Output-token counts are also retained in JSON. ⁴ Same questions, fresh agents, compact snippets/fast freshness; post-experiment exploratory follow-up.

Frozen graded facet coverage@5 was **0.559 baseline**, **0.476 map**, **0.374 initial hybrid**, and **0.453 adapted workflow**. Lower context can omit useful facets; a broad entrypoint hit does not establish equal evidence completeness. Do not make index-first lookup mandatory for already-known paths or accept its first result as sufficient.

The initial index reduced returned tool text **55.3%**, but incomplete frozen judgments initially showed a lower hit. A condition-blinded review of **all 85 unique returned query/path pairs**, not just misses, found useful current entrypoints for **11/11 baseline and hybrid** and **10/11 map** tasks. This broader post-outcome adjudication does **not** replace the stricter frozen metrics or establish full facet coverage/answer correctness. [Blind review](benchmark/blind-path-review.json), [verified join](benchmark/blind-pilot-summary.json).

The adapted workflow reduced tool-result text **37.3%**, the reported input-token field **44.4%**, and cache-read tokens **45.6%** versus baseline. **Agent wall time and call count did not materially improve.** Do not claim that the agent is now globally faster or that graph traversal caused these savings. Budgeted outputs, query formulation and narrower source reads are the practical wins. A second blinded reviewer graded all six new follow-up query/path pairs. The [combined supplemental join](benchmark/blind-pilot-followup-summary.json) found useful entrypoints on 11/11 adapted-workflow tasks, without changing the original 10/11 frozen score or original qrels. This remains post-outcome review, not heldout confirmation.

### Runtime bottleneck removed without relevance tuning

The prototype cryptographically rehashed every input on every query: freshness dominated its ~3.26 s lexical median. The adaptation fingerprints ordinary POSIX metadata, deduplicates ancestor stats, retains strict hashing as an option, and keeps ranking unchanged.

[Final post-adaptation measurements](benchmark/production-results.json), 60 queries per mode:

| Final runtime | p50 / p95 wall | p50 freshness / ranking | Mean JSON bytes |
|---|---:|---:|---:|
| Lexical | **0.515 / 0.539 s** | 0.352 / 0.089 s | 2,647 |
| Hybrid graph | 0.657 / 0.812 s | 0.350 / 0.215 s | 4,484 |

Compact lexical text averaged **1,409 bytes** across 20 full questions. The same exact-judged raw-question hit@10 remained 2/11: this is a runtime/output optimization, **not an accuracy fix**. Generic strict-hash smoke queries were ~3.7 s. The runtime corpus differs by two tracked guide edits, so timings are not a perfectly isolated A/B. Concurrent agents/OS caches also limit inference.

## Validation, maintenance and rollout boundary

- **37 runtime/security tests passed**, independently rerun; source/algorithm boundary hashes checked. [Final independent review](production-review.md), [test log](benchmark/production-tests.log).
- Reproduced prototype symlink-open race and instruction-backup ingestion were fixed in deployment. Manifest shape guards and controlled retry preserve the old DB on failed builds.
- All 400 first-run queries succeeded, rankings were stable across repetitions, both byte caps passed, frozen hashes matched, and the post-run corpus fingerprint matched its build **before integration edits**. [Consistency proof](benchmark/first-run-validation.json). Original harness review weaknesses remain visible in [review.md](review.md).
- Fast stat freshness is **not adversarial integrity** or an atomic repository snapshot. Strict mode is stronger but still requires a stable worktree. New tracked files, edits, HEAD/tool/route changes fail closed until rebuild. Untracked files are intentionally absent. No incremental updater is claimed: rebuild cost remains ~24 s.
- Cache is private source-bearing data, external to the checkout and separate per canonical worktree. No retrieval/indexing model calls or remote service dependencies.
- `bc-01` initially resolved/accessed as dev-01, but later portability validation failed because SSH-agent signing was unavailable. [Failed attempt retained](benchmark/bc-01-validation.txt). Do **not** claim Linux or Windows validation; local POSIX tests ran on macOS.
- Root AGENTS/skills access was disabled. Those files were not read through alternate paths or modified. README/repository-map links were installed; an always-loaded two-line instruction pointer requires a separate authorized edit once enabled. No hidden hook/extension was installed.

**Suggested future instruction pointer (not installed):**

```text
For unfamiliar file locations, use tools/navigation/README.md and the existing repository map.
Prefer bounded locate; use related for explicit cross-file links, scoped rg for missing facets, and pass paths to subagents.
```

Maintain authored facts in existing source maps and CODEOWNERS. Regenerate caches, not a giant committed graph. Add semantic retrieval or language-aware SCIP/DBL symbol adapters only against new independently judged tasks where the present method demonstrably fails. A future meaningful trial should use 30–50 maintainer-reviewed tasks, multiple random model repeats, time-to-sufficient-evidence and actual coding outcomes—not just path hits.

## Credential-handling incident

An early broad configuration-discovery command exposed R2/search credentials in private tool output. The proof bundle does not contain those literal values: an [exact-match scan](benchmark/proof-secret-scan.json) checked the five known values across the evidence/tool files and found no matches. This is not universal DLP. Because raw sessions may be archived without transcript redaction, rotate the affected R2/search tokens. No credentials were rotated or archive objects deleted by this work.

## Proof inventory and reproduction

[Final validation](benchmark/final-validation.json) passed source/runtime hashes, all captured first-run hashes, the complete compressed-query hash/400-row count, JSON/Python syntax and both repository whitespace checks. `EVIDENCE_MANIFEST.json` snapshots the proof/runtime/docs hashes; the BusinessCraft mirror has an additional per-file `MIRROR_MANIFEST.json`.

- Research / design: [research.md](research.md), [repo-assessment.md](repo-assessment.md), [protocol](experiment-protocol.md).
- Archive: [scope/corrected call counts](archive-scope.json), [tasks](benchmark/tasks.json), [R2 descriptor verification](benchmark/r2-provenance.json).
- Frozen algorithms/tests: [prototype](prototype/README.md), [FREEZE.json](prototype/FREEZE.json).
- Automatic runs: [first manifest](benchmark/first_run/manifest.json), [summary](benchmark/first_run/summary.json), complete `queries.jsonl.gz` with [byte counts/SHA-256](benchmark/first_run/ARTIFACTS.json). The original 123 MB JSONL stays local/ignored; gzip is lossless.
- Agent trials: [initial results](benchmark/pilot-results.json), [follow-up results](benchmark/pilot-followup-results-v2.json), prompts/registries and collector script. The earlier follow-up artifact had a static six-agent label; [erratum](benchmark/pilot-followup-label-erratum.json) confirms numerical results/answers are identical.
- Adaptation: [initial slow timings](benchmark/production-initial-timings-v1.json), [build](benchmark/production-build-v2.json), [final query measurements](benchmark/production-results.json), [37-test log](benchmark/production-tests.log), independent reviews.

```sh
# Optional archive provenance replay; requires trusted R2 read credentials + analytics deps.
.analytics-venv/bin/python docs/research/agent-navigation-context-graphs/benchmark/verify_r2_provenance.py --output /tmp/new-r2-proof.json

# Dataset validates the recorded source-file hashes; first-run consistency additionally
# requires the historical worktree snapshot, not a subsequently edited/staged tree.
python3 docs/research/agent-navigation-context-graphs/benchmark/validate_dataset.py --repo /path/to/businesscraft
python3 docs/research/agent-navigation-context-graphs/benchmark/run.py --self-test

# New experiment; refuses to overwrite first evidence. Runtime DB must be in prototype/.cache.
python3 docs/research/agent-navigation-context-graphs/benchmark/run.py \
  --repo /path/to/businesscraft \
  --index docs/research/agent-navigation-context-graphs/prototype/.cache/new-run.sqlite3 \
  --output /tmp/new-private-navigation-run

# Runtime measurement, preserving first results.
python3 docs/research/agent-navigation-context-graphs/benchmark/measure_production.py \
  --repo /path/to/businesscraft --output /tmp/new-private-runtime-measurements.json
```

This is a limited evidence-led rollout, not proof of a global optimum. The supported choice is **human maps + bounded lexical discovery + optional explicit graph traversal + scoped fallback**, with context savings demonstrated in small pilots and no demonstrated default graph-ranking advantage.
