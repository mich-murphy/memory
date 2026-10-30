# Research: context graphs for agent repository navigation

## Summary

Start BusinessCraft with a compact, linked Markdown navigation map plus lexical search; add a small, provenance-bearing typed graph only if evaluation reveals relationship-traversal failures. Uber’s **Databook** is verified primary evidence for team→asset ownership, lineage, controlled vocabulary, and hybrid graph/search infrastructure—not evidence that LLM-generated GraphRAG improves coding agents. The suggested **uSearch** identification could not be verified and must not be conflated with Databook.

Research covered three angles: Uber ownership/discovery architecture; distinctions between graph representations and retrieval methods; cost/quality trade-offs and a controlled repository experiment. The websearch tool was attempted with three focused queries, but every provider returned “credential incompatible.” Direct webfetch and DuckDuckGo HTML discovery recovered primary sources; subsequent search encountered bot challenges. Important claims below are based on fetched originals, not search summaries.

## Findings

1. **Claim: Uber documents precisely the team/asset mapping pattern, but the verified system is Databook.** Its November 10, 2020 article states: “A team can manage or own various data entities such as dataset, business metric, dashboard, pipelines, and more.” It describes pipeline→table→metric→dashboard relationships, ownership metadata, and questions routed to the appropriate team’s on-call. **Sources:** [Uber, Turning Metadata Into Insights with Databook](https://www.uber.com/us/en/blog/metadata-insights-databook/), sections “Importance of relationships between data entities” and “Manage.” **Support:** direct evidence. **Confidence:** high for this historical architecture; not evidence of current deployment or agent performance.

2. **Claim: The useful Uber lesson is hybrid storage and disciplined updates, not “put everything in a graph database.”** The same article specifies MySQL persistent storage, entity/relationship graph representation, a Kafka metadata change log, and Elasticsearch indices derived from that log. It says changes appear in search in near real time, but gives no measured freshness bound. Schema definitions are domain-contributed and centrally reviewed through Dragon; periodic crawlers and event-based sources coexist. **Source:** [Uber 2020](https://www.uber.com/us/en/blog/metadata-insights-databook/), “Data Model,” “Metadata Sources,” “Event Log,” and “Search and Discovery.” **Support:** direct evidence. **Confidence:** high. **Researcher inference:** a local repository can apply this separation with ordinary SQLite entity/edge tables plus FTS5; it does not need Uber’s infrastructure.

3. **Claim: Lightweight maps are a legitimate starting point, not a failed graph implementation.** Uber’s original catalog used manually maintained static HTML locations/owners before richer discovery needs emerged. Aider documents a concise repository map containing files and key symbol signatures, selecting relevant parts by graph ranking under a token budget; its documented default map budget is 1k tokens, with dynamic expansion. **Sources:** [Uber 2018 Databook](https://www.uber.com/us/en/blog/databook/); [Aider repository map](https://aider.chat/docs/repomap.html). **Support:** direct evidence for mechanisms; no controlled quality result. **Confidence:** high. **Researcher inference:** curated architecture/domain Markdown plus generated signatures can provide cheap orientation without serializing a whole graph into every prompt.

4. **Claim: SQLite FTS5 provides a concrete low-complexity retrieval baseline.** Official documentation specifies BM25 ranking, per-column weights, snippets, Boolean/phrase/prefix/NEAR queries, and trigram substring matching. Better FTS5 BM25 matches have **lower** numeric scores. External-content tables require explicit synchronization; triggers do not backfill pre-existing rows, and a rebuild may be required. **Source:** [SQLite FTS5](https://www.sqlite.org/fts5.html), §§3–5, especially [BM25](https://www.sqlite.org/fts5.html#the_bm25_function) and [external-content pitfalls](https://www.sqlite.org/fts5.html#external_content_table_pitfalls). **Support:** direct evidence. **Confidence:** high. **Researcher inference:** split camelCase/snake_case aliases deliberately, weight symbols/paths/headings separately, and retain grep for literal exact matches; BM25 alone does not resolve program symbols or synonyms.

5. **Claim: Code intelligence and GraphRAG address different evidence types.** Sourcegraph’s precise navigation requires language-specific SCIP indexes, uploaded per repository; search-based navigation is a fallback when precise indexes are unavailable. Microsoft GraphRAG instead extracts entities/relationships from text, detects communities, summarizes them, and embeds text. **Sources:** [Sourcegraph precise code navigation](https://sourcegraph.com/docs/code_navigation/explanations/precise_code_navigation); [GraphRAG indexing](https://microsoft.github.io/graphrag/index/overview/). **Support:** direct evidence. **Confidence:** high. **Interpretation:** compiler/indexer-backed definitions/references should be distinguished from LLM-inferred semantic relationships; neither automatically supplies business ownership, architectural intent, or runtime behavior.

6. **Claim: GraphRAG’s primary evaluation does not establish superior repository-navigation accuracy.** The paper evaluates global sensemaking on podcast/news corpora of roughly one million and 1.7 million tokens, using generated questions and LLM comparative judgments. It reports better comprehensiveness/diversity than a vector-RAG baseline, while vector RAG gives the most direct answers; its limitations explicitly call for broader-domain evaluation and fabrication-rate comparisons. Its reported >97% context reduction for root summaries is **against map-reduce source-text summarization**, not against BM25, compact maps, or ordinary top-k retrieval. **Source:** [From Local to Global, version 2](https://arxiv.org/html/2404.16130v2), §§4–6. **Support:** direct evidence. **Confidence:** high about experimental scope; low transfer confidence to BusinessCraft. Do not restate these results as coding accuracy or general token savings.

## Terminology: distinctions to preserve

These are operational definitions/interpretations for this experiment, not claims of a universal taxonomy.

| Term | Meaning here | What it does not imply |
|---|---|---|
| Knowledge graph | Explicit entities, identifiers, attributes, typed relations; e.g. team `owns` module, module `depends_on` module | A dedicated graph database, LLM extraction, or RAG |
| Context graph | A task-oriented graph linking code, docs, decisions, owners and evidence; optionally temporal decision/history links | A standardized architecture or an inherently more accurate knowledge graph; define the schema instead of relying on the label |
| Code intelligence | Language-aware definitions, references, types, imports and related navigation | Correct business intent/ownership or full runtime dependency coverage |
| Graph-enhanced retrieval | Lexical/semantic seed retrieval followed by bounded graph expansion or reranking | Microsoft’s particular GraphRAG pipeline |
| GraphRAG | Broadly graph-assisted RAG; **Microsoft GraphRAG specifically** adds text extraction, graph communities and community summaries with local/global query modes | That every ownership graph needs extraction or global summarization |
| Embeddings/vector RAG | Similarity search over embedded chunks/descriptions, then answer generation from retrieved evidence | Explicit dependency/ownership edges; embeddings and graphs can coexist |

Microsoft’s [overview](https://microsoft.github.io/graphrag/) documents local, global, DRIFT and basic-vector search; “GraphRAG” must therefore specify the implementation and query mode being compared.

## Comparison for BusinessCraft

**All relative expectations below are researcher hypotheses, not measured rankings.** Accuracy means finding sufficient correct evidence and completing the task, not producing a more expansive answer. Retrieval latency, total agent latency, context tokens, and indexing cost must be measured separately.

| Approach | Expected accuracy / failure mode | Context cost | Latency / indexing cost | Maintenance and freshness |
|---|---|---|---|---|
| Compact linked Markdown maps | Strong orientation and domain entry points; omitted or stale links conceal relevant code | Small fixed overview plus on-demand subtree pages; an oversized always-loaded map wastes tokens | Simple file reads; no model required for curated/generated structural maps | Lowest tooling burden, but prose ownership requires review; generate paths/signatures and check links in CI |
| SQLite FTS5 / BM25 | Strong known names, errors, terms and headings; lexical mismatch and ambiguous names remain | Bounded ranked snippets, then source reads | No retrieval-time LLM required; local query/index latency unmeasured | Incremental changed-file indexing; handle deletes, tokenization, branch/worktree identity and transactions |
| Typed graph + FTS hybrid | Potential benefit for multi-hop owner/dependency/doc→implementation→test questions; bad or missing edges mislead | Return only selected paths and evidence, with hop/fanout/token caps | Additional joins/traversals; no LLM needed for deterministic edges; parser/indexer setup adds cost | Schema and ID design; source provenance per edge; update affected edges on rename/delete; distinguish authoritative from inferred edges |
| Embeddings + lexical fusion | Potential benefit for paraphrased intent/domain-language queries; semantic neighbors need not be correct callers/owners | Same bounded snippet budget possible; overlap can waste context | Document embedding at index time and query embedding at retrieval time; local versus hosted implementation matters | Re-embed changed chunks; record model/version and chunk hashes; model changes may require reindexing |
| LLM-extracted GraphRAG + summaries | Plausible for cross-document architectural synthesis; extraction/entity-resolution/summary errors may compound | Summaries compress broad evidence but may omit exact symbols or contradictory details; global mode can require multiple LLM stages | Extraction and summary-generation overhead; global map-reduce adds generation calls; no universal latency number justified | Most derived artifacts to invalidate: entities, edges, communities and summaries; retain source links and test update/delete behavior |

The evidence supports the **mechanisms** in this table ([Aider](https://aider.chat/docs/repomap.html), [FTS5](https://www.sqlite.org/fts5.html), [Uber](https://www.uber.com/us/en/blog/metadata-insights-databook/), [GraphRAG](https://microsoft.github.io/graphrag/index/overview/)), not its projected relative outcomes. More context or more edges is not automatically better.

## Practical experiment hypotheses

BusinessCraft was not inspected for this web-research brief; examples below must be instantiated with real modules, documentation and owner records rather than assumed repository structure.

1. **H1—orientation:** a 1–2k-token root Markdown map with links to domain submaps lowers irrelevant reads and total input tokens versus existing grep/file browsing, without reducing task success. Include purpose, entry points, invariants, test commands, source links and generated symbol hints—not an exhaustive file tree.
2. **H2—lexical baseline:** source-aware FTS chunks (symbol/heading boundaries, path and identifier weighting) improve evidence recall and latency over directory browsing for known-term tasks. Compare against grep rather than replacing it; ablate identifier splitting and field weights.
3. **H3—relationship value:** adding a small set of `defines`, `imports`, `documents`, `tested_by`, `owned_by` and explicit `supersedes` edges improves multi-hop retrieval beyond map+FTS at the **same context-token budget**. Start with manifests, explicit links, parsers/indexers and maintained owner metadata. Do not equate a Git author with an accountable owner. Store evidence path/range, content hash or commit, extraction method and timestamp; label inferred edges separately.
4. **H4—semantic value:** lexical+embedding fusion improves paraphrased domain questions, but may not improve symbol lookup. Only test GraphRAG community summaries if broad synthesis remains an important unsolved task; compare against simpler per-domain summaries to isolate the graph’s contribution.
5. **H5—freshness:** rename/delete a module, change an API and transfer an explicit owner in a test branch. Measure stale hits/edges, invalid citations, propagation delay and incremental indexing effort. No system passes merely by retrieving a plausible old answer.

**Protocol:** freeze a repository commit and build 30–50 maintainer-checked tasks spanning symbol lookup, domain-language lookup, cross-layer tracing, tests/change impact, ownership/decision rationale, and architecture synthesis. Record minimal sufficient evidence and acceptable alternative files before running. Compare baseline, map, map+FTS, map+FTS+typed edges, and lexical+embeddings; test GraphRAG last. Use the same model, prompts, context caps and task execution tools; repeat runs and keep a held-out tuning split. Measure evidence recall@budget, irrelevant retrieved tokens, correct path/line citations, unsupported claims, test-backed change success, total input/output tokens, tool calls, p50/p95 retrieval and end-to-end latency, initial/incremental indexing time and human maintenance. Report task strata: averages can hide exact-lookup regressions behind synthesis gains. Prefer the simplest system on the quality/cost frontier.

## Contradictions

- **Uber history is not one immutable architecture:** 2018 describes moving from MySQL to Cassandra; 2020 describes MySQL persistent storage in the rebuilt Databook. Cite the version, not “Uber uses Cassandra/MySQL” without qualification. The sources also differ on the static-catalog start year (2015 versus 2016); that chronology is immaterial here and remains unresolved.
- GraphRAG’s broad improvement language should be read alongside its paper’s narrow sensemaking evaluation and unresolved fabrication/generalization questions. This is a scope limitation, not evidence that it fails on code.

## Missing evidence

- No fetched primary uSearch source, or verified identification of the specific Uber organisational/work-owner system the user remembers. Databook proves a related pattern, not that identification.
- No controlled, same-agent BusinessCraft comparison of these methods; no justified absolute token, latency or maintenance savings.
- Uber articles are historical (2018/2020), not confirmation of current architecture. The 2019 Uber knowledge-graph talk is also historical and not an agent benchmark.
- No inspected BusinessCraft schema, languages, ownership metadata or measured query distribution; recommendations are hypotheses pending local evaluation.

## Sources

**Kept (originals fetched):**
- [Uber: Turning Metadata Into Insights with Databook (2020)](https://www.uber.com/us/en/blog/metadata-insights-databook/) — strongest ownership, relationship, schema and incremental-search evidence.
- [Uber: Databook (2018)](https://www.uber.com/us/en/blog/databook/) — lightweight origins and crawling/event freshness trade-offs.
- [Joshua Shinavier: Building an Enterprise Knowledge Graph @Uber, May 8, 2019](https://www.slideshare.net/slideshow/building-an-enterprise-knowledge-graph-uber-lessons-from-reality/146096919) — primary speaker slides, third-party hosted; slides 12–17 emphasize shared vocabulary, fitting existing infrastructure/data, budgeting other work and collaboration. Not used for quantitative proof.
- [Aider: Repository map](https://aider.chat/docs/repomap.html) — actual compact graph-ranked context implementation, not a head-to-head benchmark.
- [SQLite: FTS5](https://www.sqlite.org/fts5.html) — authoritative API/ranking/update semantics.
- [Sourcegraph: Precise code navigation](https://sourcegraph.com/docs/code_navigation/explanations/precise_code_navigation) — SCIP/indexing mechanism; pricing and marketing statements not used.
- [Microsoft GraphRAG overview](https://microsoft.github.io/graphrag/) and [indexing](https://microsoft.github.io/graphrag/index/overview/) — implementation and query-mode definitions, not proof of general superiority.
- [Edge et al.: From Local to Global, v2](https://arxiv.org/html/2404.16130v2) — primary experimental design, comparators and limitations.

**Rejected/deprioritized:** Toolify’s Uber talk summary and an unrelated GitHub “Uber” search-agent proof of concept — not Uber primary evidence; Neo4j customer story/config-graph promotion — different use case and vendor claims, not performance proof; YouTube talk page — fetched but no usable transcript; guessed uSearch/engineering-ownership URLs — returned 404, not evidence that those systems do not exist.

## Next steps

Obtain the original Uber link/title from the user if exact identification matters. For the repository decision, first inspect BusinessCraft’s existing navigation/ownership material and build the small held-out task set; map+FTS versus bounded typed-edge expansion is the highest-value first comparison.
