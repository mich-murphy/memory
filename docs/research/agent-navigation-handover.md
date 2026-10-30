# Handover: BusinessCraft agent navigation and context efficiency

**Updated:** 20 October 2026. **Status:** research and a local implementation are complete; a newly identified evaluation gap—existing AGENTS.md guidance—needs auditing before adoption claims or further tuning.

## Start here

The user's latest question was: **“Would any of the agents.md in the repo have helped? I usually block this with pi-skill-toggle.”**

Answer so far: probably some could have helped, but their contents were not inspected because access is disabled. The experiment did **not** compare enabled existing instructions against the new navigation tool. Do not equate “guidance blocked” with “guidance absent.” The next useful work is that audit, not another graph implementation.

Ask the user to temporarily unblock the selected instruction files if still disabled. A direct `read` of the root `AGENTS.md` returned `AGENTS.md is disabled in /skill-toggle, so it cannot be read.` Do not bypass through Git blobs, shell reads, another agent, or an alternate copy. No instruction files were changed.

## User goal and working preferences

Original goal: analyse repeated file discovery in **R2-collected** BusinessCraft session history, research context/knowledge graphs (including Uber ownership/organisation mapping), compare them with Markdown/instruction maps, run experiments, and implement an efficient accurate navigation structure with proof in the repository.

- Use **medium-effort subagents** as useful; main assistant coordinates and validates.
- Prefer concise, visual explanations: small flow diagrams, shallow file trees or diffs.
- Historical task mining must use **R2-derived history**, not local Pi history.
- New experimental session logs were used only to measure the experiments themselves.
- Prompt caching in `pi-claude-sdk` was already addressed before this work. Large resumed sessions/compactions remain a separate context-cost concern.
- `dev-01` is accessed as **`ssh bc-01`** from this machine.

## Checkouts and authoritative files

| Purpose | Path |
|---|---|
| Current working repository | `/Users/mm/dev/memory` (`mich-murphy/memory`) |
| BusinessCraft implementation | `/Users/mm/businesscraft/businesscraft` (`bsncraft/businesscraft`) |
| Canonical research/proof directory | `memory/docs/research/agent-navigation-context-graphs/` |
| Main decision report | `memory/docs/research/agent-navigation-context-graphs/README.md` |
| BusinessCraft evaluation summary | `businesscraft/docs/repository/navigation-evaluation.md` |
| Deployed tool and usage | `businesscraft/tools/navigation/nav.py`, `tools/navigation/README.md` |
| Complete proof mirror in BusinessCraft | `businesscraft/tools/navigation/benchmarks/context-graphs/` |
| Mirror explanation/hash list | `businesscraft/tools/navigation/benchmarks/README.md`, `MIRROR_MANIFEST.json` |

Read the main decision report before exploring individual artifacts. This handover lives **outside** the frozen proof directory so adding it does not invalidate the proof snapshot or mirror.

## What was learned and implemented

### Research conclusion

The verified Uber precedent is **[Databook](https://www.uber.com/us/en/blog/metadata-insights-databook/)**: explicit ownership/lineage relationships combined with separate persistence and search infrastructure. The specific uSearch identification was not verified. This is not evidence that LLM-extracted GraphRAG improves coding-agent file navigation.

BusinessCraft already has authored repository/domain/source maps and skills. Existing human maps were retained, rather than replaced with another exhaustive authored map. Ownership is drawn only from `.github/CODEOWNERS`; unknown stays unknown.

### Selected, limited workflow

```text
known path                 → read directly
unfamiliar area            → existing repository/source map
distinctive terms          → bounded lexical locate
known path, missing link   → related: explicit citations/imports
missing facet / weak match → scoped rg and source inspection
delegation / continuation  → pass paths and findings forward
```

`tools/navigation/` implements Python-stdlib SQLite FTS5 plus typed explicit edges. **Lexical default; graph opt-in.** No graph service, embedding pipeline or LLM extraction dependency.

- Tracked eligible files only; generated/dependency/fixture/instruction/evaluation material excluded.
- `locate` returns bounded `path:line` snippets; `query` returns JSON; `related` returns bounded explicit links with direction/provenance and declared/unknown ownership.
- Private external per-canonical-worktree cache under XDG/`~/.cache/businesscraft-navigation`; no cache DB committed.
- Normal freshness uses stat signatures, HEAD and tool/routes fingerprints. `--strict` hashes content. No silent stale reuse/auto-build.
- Tracked changes require rebuilding. **No incremental updater:** full rebuild ~24 s. Measure this amortized cost during actual editing; do not claim all coding workflows are faster.
- Root-relative no-follow regular-file reads, bounded/stable reads, private-cache checks, instruction-backup exclusion and defensive package-manifest guards.
- Python 3.10+, Git, SQLite FTS5 and POSIX no-follow APIs required. macOS validated; Linux/Windows not validated.

```sh
cd /Users/mm/businesscraft/businesscraft
python3 tools/navigation/nav.py build
python3 tools/navigation/nav.py locate 'contract finance' --scope web/packages/contracts
python3 tools/navigation/nav.py related web/agent/README.md
python3 tools/navigation/nav.py query 'terms' --mode hybrid --strict
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tools/navigation -v
```

### Changes in BusinessCraft

- Modified `README.md` and `docs/repository/README.md` with discovery links/workflow.
- Added `docs/repository/navigation-evaluation.md`.
- Added `tools/navigation/`: runtime, routing metadata, minimal map pointer, tests, usage/adaptation docs, source hashes/diff and evidence summary.
- Added the full proof mirror under `tools/navigation/benchmarks/`; all mirrored paths were verified excluded from the index by the unchanged corpus policy.
- `benchmarks/conftest.py` prevents pytest from collecting the frozen experimental source as a second runtime test suite.
- No hooks or automatic instruction injection installed. The root AGENTS/skills files were disabled and left untouched. README links alone do not guarantee agents discover/use the new tool.

**Nothing was staged, committed, pushed, or deployed remotely.**

## Evidence and limits

### Dataset/provenance

- 20 sanitized archive-derived tasks: **8 development / 12 test**; 98 qrels.
- R2-derived **Oct 16 snapshot**, not a current full bucket import.
- Live read-only GETs verified all 20 task references against **17 immutable R2 descriptors**; proof at `benchmark/r2-provenance.json`.
- One test question requires current first-party Azure evidence and repository-only abstention; quality averages therefore use **11 answerable test tasks**.
- One positive development qrel points to generated oversized SQL outside the allowed corpus; disclosed in `benchmark/corpus-coverage.json`.
- Root paths/source hashes/anchors/family isolation validated. All these tasks have now been exposed; they are **not a fresh heldout pool for future tuning**.

### Automatic retrieval

Five methods × 20 questions × four repetitions = **400 queries**, zero errors, repeat-stable rankings, common top-10/9,600-byte ceilings.

| Frozen method | Test hit@10 | Test MRR | Warm wall p50 |
|---|---:|---:|---:|
| Scoped filename/rg | 2/11 | .182 | 1.151 s |
| Router only | 1/11 | .091 | 3.168 s |
| Lexical | 2/11 | .114 | 3.257 s |
| Router + lexical | 2/11 | .114 | 3.448 s |
| Hybrid graph | 2/11 | .114 | 3.400 s |

Full natural-language question retrieval was poor across methods. Reformulation/source inspection are necessary. Graph expansion gave **no measured uplift** and roughly doubled lexical output. These findings do not rule out every possible graph algorithm or semantic retrieval approach. The frozen graph deliberately preserves lexical seeds, limiting potential reranking improvements. Router-only also inherited unnecessary index freshness overhead; its timing is not a measure of reading Markdown.

### Fresh-agent pilots

Same recorded model string `openai/gpt-6.1-sol`, medium effort, two six-question bundles per condition, 18-call budget per bundle. Six initial agents; two later fresh agents tested the adapted workflow on the **same** questions.

| Condition | Tool-result bytes | Reported input tokens | Calls | Wall sum | Frozen hit@5 |
|---|---:|---:|---:|---:|---:|
| Baseline scoped search | 631,225 | 198,127 | 36 | 290.3 s | 11/11 |
| Compact map | 488,745 | 136,598 | 35 | 304.3 s | 10/11 |
| Initial hybrid | 282,303 | 86,158 | 36 | 319.2 s | 10/11 |
| Adapted lexical/fallback | 395,545 | 110,146 | 35 | 294.9 s | 10/11 |

Adapted workflow: **37.3% fewer returned bytes, 44.4% fewer reported input tokens**, but no material overall wall-time/call-count improvement. Frozen facet coverage@5 fell from **.559 baseline to .453 adapted** (.374 initial hybrid). Do not trade away completeness silently.

Condition-blinded supplemental review covered all 85 original unique query/path pairs, then all six new follow-up pairs. It found useful entrypoints on 11/11 baseline/hybrid/adapted tasks, 10/11 map. This demonstrates incomplete original judgments; it is **post-outcome adjudication, not independent heldout confirmation or full coding/answer correctness**. Frozen scores remain unchanged.

Input/cache-read/output usage fields are retained separately and include repeated request prefixes. **No dollar-savings claim.** Context savings cannot be attributed solely to graph/index structure: reformulation, bounded outputs and narrow reading also changed. A matched compact-native-search condition remains worth testing.

### Final runtime

- 30,788 files / 174,866 edges; **24.06 s** build; ~417 MiB cache.
- Lexical: **.5153 s p50 / .5386 s p95** across 60 queries.
- Hybrid: .6568 / .8121 s; no observed judged retrieval gain.
- Compact lexical text: mean **1,409 bytes** across 20 full questions.
- Strict generic smoke lookup: ~3.7 s.
- Runtime optimization reduced freshness overhead without relevance tuning; source/hash/diff proof records ranking equivalence to frozen candidate.
- Final runtime SHA-256: `ec5bef8e9529fc2b2e8fa9c02234fda29a4769bdf5a275740a1dab3d076f8581`.
- Two tracked guide edits distinguish the measured deployed corpus from the frozen trial. New tool/proof files were untracked; staging changes the corpus and requires a rebuild.

### Validation/proof locations

Relative to canonical research directory:

- `research.md`, `repo-assessment.md`, `experiment-protocol.md`.
- `prototype/FREEZE.json`, `prototype/VALIDATION.md`.
- `benchmark/tasks.json`, `dataset.md`, `r2-provenance.json`.
- `benchmark/first_run/summary.json`, `manifest.json`, `queries.jsonl.gz`, `ARTIFACTS.json`.
- `benchmark/first-run-validation.json` records corpus consistency **before integration edits**.
- `benchmark/pilot-results.json`, exact `pilot-prompts.json`.
- `benchmark/pilot-followup-results-v2.json`, `pilot-followup-prompts.json`.
- `benchmark/blind-pilot-summary.json`, `blind-pilot-followup-summary.json` and adjudications.
- `benchmark/production-results.json`, `production-build-v2.json`.
- `production-review.md`, `benchmark/production-tests.log`, `benchmark/final-validation.json`.
- `EVIDENCE_MANIFEST.json`; corresponding BusinessCraft `benchmarks/MIRROR_MANIFEST.json`.

**37 tests passed**, independently reviewed and rerun after mirroring. All captured first-run source hashes, deployed runtime hashes, compressed complete rankings and mirror hashes matched. Mirror: **60 files / 8,144,166 bytes**. Raw 123 MB query JSONL remains local/ignored; lossless gzip is included as an uncommitted proof addition. No raw session transcripts/cache DBs mirrored. No full BusinessCraft application/CI run performed.

Early symlink-race and instruction-backup ingestion findings were fixed. No blocking P1/P2 reproduced within the documented trusted-user POSIX stable-worktree model. Fast freshness is not adversarial integrity or an atomic snapshot.

## Immediate gap: existing AGENTS.md audit

Only filename/size metadata was inspected. There are **13 exact `AGENTS.md` files**, not 14. `docs/knowledge/customer-guides/set-up-contract-sales-agents.md` is a business document, not an instruction file. There are also 53 tracked `SKILL.md` files; their contents were not audited here.

| Instruction file | Bytes |
|---|---:|
| `AGENTS.md` | 6,612 |
| `classic/automation/AGENTS.md` | 3,202 |
| `classic/src/AGENTS.md` | 2,848 |
| `classic/src/runtimes/dbl-rpc/AGENTS.md` | 2,011 |
| `classic/src/runtimes/dbl-sql/AGENTS.md` | 3,405 |
| `docs/knowledge/AGENTS.md` | 1,923 |
| `docs/knowledge/model/AGENTS.md` | 941 |
| `tools/abc/AGENTS.md` | 2,920 |
| `web/agent/AGENTS.md` | 1,608 |
| `web/apps/web/AGENTS.md` | 1,445 |
| `web/packages/AGENTS.md` | 197 |
| `web/packages/bc-grid/AGENTS.md` | 1,153 |
| `web/packages/bc-grid/docs/AGENTS.md` | 699 |

Do not infer usefulness from size/path alone. Good candidates for the observed tasks are root, Classic source/SQL runtime, Web Agent, Web app/packages and grid guidance.

### Recommended next steps

1. **Unblock with the user**, then directly read the selected existing AGENTS files. No bypass. Read actual applicable project instructions before further implementation changes.
2. Build a compact audit table: scope, unique routing facts, safety/invariant guidance, build/test commands, duplicate prose, approximate token cost, and observed tasks it would have helped. Use medium-effort read-only subagents for independent scoped audits if helpful.
3. Compare existing AGENTS pointers with authored source maps, skills and the new tool. Identify duplication or unnecessary machinery before adding anything.
4. Recommend a selective loading policy only after inspecting contents: small root routing/invariants available; relevant scoped instructions on demand; large reference skills/docs only when needed. Preserve essential safety/build constraints even if they do not improve search.
5. If testing further, compare **existing enabled instructions**, minimal routing, **matched bounded native search**, and index-assisted workflow with equivalent reading/output budgets. Separate benefits of instructions, query reformulation, snippets and graph links.
6. Use new independently judged tasks; freeze before tuning. Measure sufficient-evidence/facet correctness, actual coding outcomes, model usage, tool bytes/calls, and wall time including cold builds and rebuilds during edits. Do not present reuse of the original 20 tasks as new heldout confirmation.
7. Preserve original evidence. New results belong in a new run directory; do not overwrite frozen proof/mirrors or silently update their manifests. If the AGENTS audit changes the conclusion, document that explicitly.

## Repository hygiene and environment

Pre-existing unrelated changes must stay untouched:

- Memory: modified `.gitignore`, deleted `CLAUDE.md`, untracked `tools/analytics/`.
- BusinessCraft: modified `infra/web/stamp/envs/staging.tfvars`, untracked `.pi/` and `docs/devops/audits/`.

Our work: canonical `docs/research/agent-navigation-context-graphs/`, this handover, two BusinessCraft README edits, new evaluation doc and `tools/navigation/`. Nothing committed/staged.

`bc-01` initially worked and identified itself as dev-01; a later validation attempt failed due to SSH-agent signing/authentication. Failure retained in `benchmark/bc-01-validation.txt`. Check authentication before assuming remote access or claiming Linux tests.

Analytics tools are in `memory/tools/analytics/`. Read their README before using them. Use `.analytics-venv/bin/python` for DuckDB; system Python lacks it, and some timestamp fetches lacked pytz. Existing snapshot DBs/caches are local/private, not proof artifacts. Collector `sync`/`backfill` upload data; **do not use them for a read-only archive analysis**. For current R2 coverage, use documented LIST/GET importers rather than mining local history.

## Security incident requiring follow-up

An early broad configuration-discovery command printed R2/search credentials into private tool output. Those literals were not copied into proof: `benchmark/proof-secret-scan.json` checked five known values and found no matches. This is an exact-match check, not universal DLP. Because raw session logs may be archived, **rotate affected R2/search tokens**. No rotation or archive deletion was performed. Never repeat values or print credential files while investigating this.

## Suggested opening prompt for the next session

> Continue from `docs/research/agent-navigation-handover.md`. First audit whether BusinessCraft's existing AGENTS.md guidance would have prevented the navigation problems. It was disabled during the earlier experiment, so that comparison is missing. Ask me to unblock the relevant files if needed; do not bypass pi-skill-toggle. Use medium-effort read-only subagents as useful, verify their conclusions, preserve the frozen evidence and unrelated changes, and recommend a selective instruction-loading policy before doing more implementation.
