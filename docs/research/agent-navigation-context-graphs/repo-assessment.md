# BusinessCraft agent navigation assessment

## Decision

Keep the existing human maps; add a **small, disposable SQLite typed-edge index + FTS5**, not another authored repository map, vector database, or universal call graph. Start with document citations, Markdown links, workspace metadata, App Router entry points, and test imports. These answer “where should I read/change/test?” with inspectable provenance. Benchmark FTS alone against FTS plus bounded edge expansion before adding symbol/call extraction.

All paths below are relative to `/Users/mm/businesscraft/businesscraft` unless explicitly stated. This is an assessment, not an implementation; no target-repository files were changed and no benchmark timings are claimed.

## Inspection boundary and evidence

- Enumerated with `git ls-files -z`, not recursive filesystem discovery: **32,518 tracked paths**; HEAD `e4fbe8f3768b3348c349667f37f500b478950fed`. One tracked path differed from HEAD during inspection. Counts describe the tracked working-tree snapshot, not a clean commit build.
- Major tracked roots: `classic/` 15,601; `web/` 12,953; `docs/` 2,182; `tools/` 1,018. Did not read dumps, node_modules, other worktrees, binaries, or proof payloads. Tracked status alone is not a safe text-ingestion policy.
- Attempt to read root `AGENTS.md` returned “disabled in /skill-toggle”. No alternate access, nested AGENTS, or skill contents were read. **Instruction/skill navigation and any additional restrictions there remain an explicit assessment gap.** A prototype must preserve this exclusion, not ingest those files through Git or an index.
- Inspected repository/knowledge indexes, representative source maps and their source references, metadata schema/parser, CODEOWNERS, workspace/package scripts/exports, concrete route/test source, existing DBL indexing and Agent search code. This is targeted deep inspection, not validation of every implementation or link.

## What already works, and what is missing

| Actual artifact | Finding / implication |
| --- | --- |
| `docs/repository/README.md` | Excellent low-token top-level map: Web screens, business packages, Agent, Classic DBL/Windows/runtimes, ABC, automation, model, infra. Explicitly separates retained `classic/src/web/` from new Web work. Preserve as human entry point; do not duplicate its area ownership roots. |
| `repo.yaml` | Machine-readable Classic/tool/product roots and generated output locations. Useful extraction seed, **not a people-ownership registry**. Windows output paths must not become local ingestion roots. |
| `docs/knowledge/README.md` | Separates customer guidance from internal source/proof; declares archive exclusion, source authority hierarchy, committed publication inputs and verification responsibilities. Developer navigation must remain separate from hosted customer corpus eligibility. |
| `docs/knowledge/legacy/README.md`, `legacy/source-navigation.md` | Rich DBL/DBX/DBC, records, module codes and code-generation orientation. The index is `publicationStatus: excluded` despite developer-navigation value: publication exclusion and developer discoverability are different dimensions. Broad path labels are not exact semantic bindings. |
| `docs/knowledge/modules/README.md`, `modules/purchase-orders/README.md` | Module hub links operator guidance, internal source maps, boundaries, data models and workflow maps. PO index explicitly says navigation-only. Follow links rather than ingesting hubs as duplicate authoritative behavior. |
| `docs/knowledge/internal/modules/purchase-orders/purchase-order-source-map.md` | Strong example of reusable evidence: metadata ID, aliases, Classic surface, module, authority/date, sourceRefs, detailed file table and explicit runtime-proof gaps. Only two frontmatter citations but many body paths: extracting only frontmatter would miss useful navigation. Body path mention is not automatically a verified relationship. |
| `docs/knowledge/_meta/document.schema.json`; `web/agent/packages/bc-agent-knowledge/src/knowledge-document.ts` | Existing v3 contract includes sourceRefs, modelRefs, automationRefs, authority and lastVerified. Reuse the parser; do not create a second metadata dialect. Keep schema validation diagnostics (including compatibility differences) visible rather than silently repairing records. |
| `web/agent/packages/bc-agent-knowledge/src/source-links.ts` and `tests/source-links.test.ts` | Already handles repo-relative/relative paths, GitHub and businesscraft URLs, and line fragments. Reuse normalization semantics, but retain original URL/revision: its GitHub parser drops the blob ref, which is insufficient provenance for revision-sensitive navigation. |
| `tools/abc/crates/bc-source-index/src/lib.rs` | Existing redb DBL analysis cache over `bc-language` symbols/references/project sources; parser version 4, path/mtime/length freshness checks. Optional later adapter, not a replacement target and not evidence of whole-repo document navigation. Avoid a parallel DBL parser. |
| `web/agent/apps/bc-agent-service/src/search-index.ts` | Existing publication-oriented search builder covers rules/articles/dictionaries; explicit bounds include 6,000-byte text, 160-line article windows, 12 results, access-scope filtering. This is **not** arbitrary worktree code search. Reuse budget/provenance ideas, not its publication partition as a local code corpus. |

### Observed scale and reference quality

Tracked `docs/knowledge/` has **1,319 Markdown files excluding AGENTS**, 1,291 with frontmatter. A lightweight frontmatter scan finds 429 `documentType: source-map` pages (not merely filenames ending in source-map). Existing authored semantics are substantial: replacing them with generated folder descriptions would lose value.

A diagnostic regex scan found 15,462 frontmatter `- path:` occurrences. Of 48 raw unresolved strings, normalization of repeated separators reduced apparent misses to 12; some remaining strings are YAML folded/wrapped scalars, not real missing paths. **These are extraction diagnostics, not a certified broken-reference count.** Examples requiring proper YAML parsing and tracked-path validation:

- `docs/knowledge/internal/data-access/channel-management.md`: repeated `/` in paths to `classic/src/dbl/Core/BsnChn/`.
- `docs/knowledge/modules/accounts-receivable/configure-interfaces.md`: citation to `web/apps/web/app/(app)/[company]/accounts-receivable/admin/settings/form.tsx` absent from the tracked manifest.
- `docs/knowledge/modules/estimating/sales-estimate-quote-report-audit.md`: `classic/src/Web/...` casing differs from active `classic/src/web/...`; exact-case diagnostics matter across platforms.

Simple local Markdown destination checks found all **44** links from `docs/repository/README.md`, **23** from source-navigation, **20** from the module index and **43** from the PO hub resolve to tracked files/directories. This checks paths, not heading anchors, external URLs, correctness, or freshness. Existing maps are not generally broken; the opportunity is reverse lookup, joining evidence, and bounded retrieval.

## Route, domain and test organization: index the actual structure

- Next App Router is `web/apps/web/app/`, **not** `src/routes/`: 496 tracked `page.tsx` and 284 `route.ts`. Strip route groups from URL patterns; retain dynamic/catch-all parameters. Route presence does not prove feature availability or permission.
- Concrete starting point: `web/apps/web/app/(app)/[company]/purchase-orders/setup/terms/[code]/page.tsx` delegates to `../../_shared/detail-page` with `kind="terms"`. An index must follow imports to shared implementation, not return ten near-identical route wrappers.
- `web/apps/web/app/(app)/[company]/purchase-orders/setup/_shared/mutations.test.ts` imports `./mutations` dynamically and mocks `@bsn/purchase-orders/server`; both static imports and literal dynamic imports matter. Label mocks separately from tested implementation.
- Business rules are distributed among `web/packages/<module>/`, not all in `web/packages/domain/`. The latter mostly contains auth/users/groups/companies/security/runtime-cache. `web/packages/purchase-orders/package.json` exports setup, orders, lines, server and settings subpaths; a directory-name heuristic would miss this resolution.
- `web/package.json` defines Bun 1.4.2 workspace globs including nested Agent/model and bc-grid packages. Parse actual package names, exports, dependency names and scripts. Nearest directory or a hardcoded `packages/*` glob is insufficient.
- Tests are mixed: colocated app `.test.ts(x)`, module `tests/`, Playwright under `web/apps/web/e2e/`, Rust tests, and Classic workflow evidence. PO package unit/integration scripts differ; integration is environment-gated. App unit execution uses `scripts/test-unit-shards.ts` and `scripts/run-one-unit-test.ts`, not a generic Vitest command. Return declared commands as suggestions; never automatically execute environment-dependent scripts.
- Classic lineage: `classic/src/dbl/projects.json`, Foundation definitions/skeletons, module DBX/DBL, and `classic/automation/coverage/matrix.json`. Generated `.generated/classic/*` output is not authored source. A coverage relation is not proof of parity: the repository map specifically requires `oracle_green` evidence.

## Ownership: sparse declared coverage, no inferred people

`.github/CODEOWNERS` has 15 rules, concentrated in Classic, CI and infra, plus UI. Explicit examples:

- `/classic/`: `@johncbc @jasonhbc @brentpbc`.
- `/.github/workflows/`, `/.github/actions/`, `/.github/rulesets/`: `@michaelmbc @isrxlbc`.
- `/infra/` and more-specific infra entries: `@isrxlbc`.
- `/web/packages/ui`: `@mitchg-bc`.

There is **no catch-all and no declared owner for most Web, Agent, docs, or tools paths**, including `web/packages/purchase-orders/`. Report `owner: unknown`, not the author of the nearest README, the most frequent committer, or an imagined team. Distinguish `belongs_to_package/module` from `owned_by`. Apply GitHub CODEOWNERS matching and last-match semantics; store rule text, file and line. Git history can optionally show attribution labeled as such, never ownership. CODEOWNERS itself is review-routing evidence, not proof of operational accountability.

## Minimal implementation contract (proposed, not present)

**Placement:** prototype outside the target repo at `/Users/mm/dev/memory/tools/repo-nav/`, with `cli.ts`, `schema.sql`, `extract.ts`, `query.ts`, `tests/`, and `bench/cases.jsonl`. If adopted upstream later, propose `tools/repo-nav/`; do not mix navigation with the hosted customer knowledge publication package. Runtime cache outside either checkout: `$XDG_CACHE_HOME/bc-repo-nav/<repo-id>/<worktree-id>/index.sqlite` (fallback `~/.cache/`). No new giant committed JSON inventory.

### SQLite tables

```text
snapshot(id, repo_id, worktree_id, head_oid, manifest_hash,
         overlay_hash, extractor_version, policy_hash, built_at)
file(id, path UNIQUE, git_blob_oid, content_hash, language, role,
     package_id NULL, read_status, byte_size)
node(id, kind, file_id NULL, locator, label, metadata_json)
edge(src, type, dst, evidence_file_id, start_line, end_line,
     extractor, confidence, target_revision NULL, resolution_status)
chunk(id, node_id, start_line, end_line, content_hash, text)
chunk_fts USING fts5(path, title, identifiers, body, content='...')
diagnostic(file_id, code, start_line, detail)
```

Use node kinds `file`, `section`, `package`, `route`, `module`, `model_ref`, `automation_ref`, `owner_rule`; add `symbol` only when a real resolver supplies it. Stable IDs derive from repo-relative path + kind + locator; keep snapshot identity separate. Edge types: `contains`, `links_to`, `cites_source`, `mentions_path`, `belongs_to_module`, `depends_on`, `exports`, `imports`, `route_entry`, `test_imports`, `mocks`, `owner_rule_applies`. Every edge has extraction evidence; `source-backed` is an author's claim retained as metadata, not an indexer's verification verdict. Unresolved targets remain explicit reference nodes/diagnostics, never silently dropped or fabricated.

### Extraction, in priority order

1. Enumerate tracked paths once using NUL-safe Git output. Exclude disabled instruction/skill paths before any content reads; no Git-object fallback for denied files. Also exclude `.agents/`, `.claude/skills/`, dependency/vendor/build/worktree/cache roots, dumps, binary artifacts, lockfile bodies and proof payloads by versioned policy. Allowlist text extensions, bound bytes per file, reject binary content, and reject symlinks escaping root. Report excluded/skipped/missing counts.
2. Parse knowledge frontmatter with the existing YAML/parser contract; Markdown headings, ordinary/reference-style links and code-span paths via a Markdown parser. Preserve source line ranges and citation vs link vs mention distinctions. Resolve against the tracked manifest (including virtual directory nodes), not `exists()` alone. Preserve external refs without crawling them. Index authored summaries and sections; do not generate summaries with an LLM.
3. Parse workspace/package JSON (name/exports/scripts/dependencies), `repo.yaml`, App Router filenames, and CODEOWNERS. Resolve workspace subpaths/TS aliases from actual config; unresolved imports get diagnostics. Use syntax parsing for TS static imports, exports, literal dynamic imports and mock.module calls; no module evaluation.
4. Derive test relevance from explicit imports; fallback same-stem/adjacent tests as low-confidence candidates, never a `tests_behavior` claim. Use a bounded package aggregate when precise links are absent. Add Classic project/automation/model reference adapters only after phase-one retrieval proves useful; reuse existing parsers/catalogues.

FTS indexes path segments, exact identifiers and section text separately; split snake/camel names while retaining originals (`PO`, `BsnPoGetVenEmail`, `POHEADA`). Chunk by heading, then line windows capped around 2 KB; no full-file blobs by default. Markdown hubs remain useful candidates but should not outrank precise implementation on every query. Preserve archive/draft/internal labels; exclude archives by default, opt in explicitly. Developer index may expose internal source to its authorized operator; it must never be wired into customer search without publication/access qualification.

### Freshness and worktrees

Use canonical Git common-dir identity for the repository and canonical worktree root for the worktree; branch names are not cache keys. Base manifest: tracked path + index blob/mode; overlay: hashes of changed tracked file bytes and deletions, including staged/unstaged differences. No untracked content by default. Read-only inspection must not refresh/mutate target Git state. Changed HEAD, policy, parser or content invalidates relevant rows; package exports/config/CODEOWNERS changes invalidate dependent resolution. Incremental updates atomically replace affected chunks/edges; readers see one complete snapshot. Recheck HEAD/overlay at publish time to detect races.

Keep extraction freshness separate from semantic freshness: `lastVerified` is authored, not evidence the cited source is unchanged. Show changed-since-index and citation-currentness warnings. A citation without an anchored historical blob cannot reliably establish changed-since-verification; mark unknown instead of inventing a baseline. Missing tracked files and sparse checkout omissions are distinct from broken citations. Rebuildable caches need no migration complexity beyond schema version reset.

## Query interface and context budget

Proposed commands (run from the external prototype; all target operations read-only):

```sh
bun tools/repo-nav/cli.ts build --repo /Users/mm/businesscraft/businesscraft
bun tools/repo-nav/cli.ts locate 'purchase order email fallback' --kind source,doc --budget-tokens 2400
bun tools/repo-nav/cli.ts related 'web/packages/purchase-orders/src/setup.ts' --edges imports,test_imports,cites_source --depth 1
bun tools/repo-nav/cli.ts explain 'classic/src/dbl/Operations/PurchaseOrders/Domain/BsnPoGetVenEmail.dbl'
bun tools/repo-nav/cli.ts status --json
```

`--repo` should be available on every command; examples after build assume an explicit selected repo/cache, never silent CWD guessing. Return JSON or compact text: snapshot, query/filter, ranked paths + line ranges, relation/evidence, owner rule or unknown, source dates/status, warnings, truncation and next-read suggestions. `explain` shows why a hit was selected. Path/module/package/surface/kind filters apply **before** ranking/expansion.

Initial retrieval: exact path/symbol match, then weighted BM25 (identifier/title/path > body); retrieve at most 30 candidates, diversify to 8 paths, expand one hop through allowed typed edges (maximum 24 neighbors). Depth two only explicitly, no whole reverse-import fanout. Default response cap **2,400 tokens**, max 8 results, <=120 snippet tokens per result, with metadata/edge budget reserved. Enforce using a tokenizer or conservative byte ceiling and indicate omissions; separate `read path:start-end` remains the detailed-code tool. Returned source text is untrusted data, never executable instructions.

## Benchmark-ready comparison

Freeze tracked manifest, HEAD, overlay and restriction policy; run all variants on identical allowed content. Baselines: **A** existing repository/module/source-navigation maps + tracked `git grep`; **B** same extraction and chunks with FTS only; **C** B + typed one-hop expansion. No embeddings initially. Also ablate citations vs imports to see which relation earns maintenance cost. Do not contaminate the index with evaluation questions/expected paths.

Seed cases for `bench/cases.jsonl` (expected sets are navigation candidates, not proof all behavior is implemented):

| Query | Exact relevant paths / discriminators |
| --- | --- |
| Where is PO recipient fallback defined? | `docs/knowledge/internal/modules/purchase-orders/purchase-order-source-map.md`; `classic/src/dbl/Operations/PurchaseOrders/Domain/BsnPoGetVenEmail.dbl`. Avoid treating Classic behavior as implemented Web functionality. |
| Where do I change terms setup detail? | `web/apps/web/app/(app)/[company]/purchase-orders/setup/terms/[code]/page.tsx`; `web/apps/web/app/(app)/[company]/purchase-orders/setup/_shared/detail-page.tsx`; `web/packages/purchase-orders/src/setup.ts`; follow declared server export where relevant. |
| Which tests cover PO setup mutation permission guards? | `web/apps/web/app/(app)/[company]/purchase-orders/setup/_shared/mutations.test.ts`; its `mutations.ts`; distinguish module mocks from actual storage tests. |
| Who owns the PO Web package? | `.github/CODEOWNERS`; expected **unknown**, versus explicit Classic ownership for a contrasting Classic file. |
| Where is generated Classic DBL edited? | `docs/knowledge/legacy/source-navigation.md`; `classic/src/dbl/projects.json`; `classic/src/dbl/Foundation/Templates/`; avoid suggesting `.generated/classic/dbc` as authored edit target. |
| Does an excluded Classic index imply retired Classic? | `docs/knowledge/legacy/README.md`; `docs/repository/README.md`; preserve active-product versus publication-status distinction. |

Expand to ~30 human-labeled cases across Web, Classic, Agent, infra, stale links, aliases, unknown owners, disabled files, and a second read-only worktree. Measure Recall@5/10, MRR, time/tool calls to first useful implementation **and test**, output tokens, wrong-surface/owner claims, unresolved refs, cold build time, warm p50/p95 latency, cache size and single-file incremental update cost. Include rename/delete/export-change/worktree-switch tests and deterministic budget truncation. Choose C only if it improves multi-hop location over B without worse wrong-surface rates or disproportionate latency/maintenance. No full repository reorganization is justified before that result.
