# BusinessCraft R2 navigation benchmark v1

## Frozen scope

20 sanitized task intents: **8 dev / 12 test**. Target HEAD: `e4fbe8f3768b3348c349667f37f500b478950fed`.
Intent/split SHA-256: `b897a239e6ae4f2c232d7f91927dc6447d678a99fb959c2b94822aeb3068a4b8` (UTF-8 compact JSON array of `[id, query, split, source_family]`, task order; default Python JSON ASCII escaping).
Queries and split were written before qrel completion and before any candidate navigation implementation/run was inspected. No candidate rankings, runner output, performance results or retrieval algorithms informed selection or judgments. The split is fixed explicit ID membership in `tasks.json`, not a claim of random sampling. Same-source slices stay together (03/09/10; 05/06). Do not move them after seeing scores.

## Provenance and selection

Task intent comes exclusively from the existing **R2-imported** `.analytics/events.duckdb` files/events index, opened with `read_only=True` and `SET TimeZone='UTC'`. No local Pi history directories or transcripts were opened. Some imported archive records were originally produced by Pi; archive origin is not local-history mining. Source_role and archive_agent are retained to make that distinction auditable.

Reviewed `tools/analytics/README.md` completely and private `.analytics/report/discovery-groups.json`. The latter contains two analyst-curated groups, not a task census: hosted delivery/configuration and instruction loading. Only BusinessCraft evidence in the delivery group informed this benchmark (task 16). Harness instruction-loading requests were excluded as wrong-repository tasks. Read-reference counts and analyst recommendations were **not** treated as ground truth. Input hashes are in tasks.json. No trajectory tool counts are used, so tool-call/result count conventions do not affect this dataset.

Selection was purposive: archived human requests and initial subagent assignments tied to BusinessCraft, varied across UI/grid, Catalogue setup, Finance/Ask permissions, estimates/Jobs, DBL fields, SQL locks/schema bootstrap, Agent runtime/configuration, CI and delivery docs. Excluded continuation summaries, UI tool-output echoes mislabeled human, synthetic skill-routing eval prompts, unrelated repositories, obvious repeated filter/Jobs requests and repeated reviewers. Labels are parser heuristics, not verified human identity. The selected task is a navigation paraphrase or explicitly documented subtask, **not** a copy of a private prompt. Catalogue coverage is a narrowed all-module settings audit, not an invented stock-pricing bug; estimating coverage is a narrowed delivered-surface investigation. These are weaker than dedicated feature requests and must not be described as independent customer incidents.

Each task retains opaque trace/event IDs, source JSONL line, component text SHA-256, full archived file SHA-256, indexed byte offset/length and source-role metadata. Line is 1-based; offsets are the index's original byte fields, not offsets into the paraphrase. Component hash is SHA-256 of indexed text UTF-8, not source bytes. Full-file hash identifies the imported source generation. A private auditor can join files/events by trace_id and check the exact component; public evidence deliberately omits original text, machine/session/file keys, absolute private paths, names, contacts, business records and secrets. The index build metadata is recorded in tasks.json. Import verification and attribution limitations in the analytics README still apply; archive provenance is inherited from that pipeline, not re-audited against live R2 here.

## Graded, typed qrels

`tasks[].qrels[]` fields:

- `path`: repo-relative **file**, never a directory/glob.
- `grade`: 3 direct major-facet implementation/contract/proof; 2 useful source/test/doc entrypoint; 1 contextual only; 0 explicitly judged plausible negative.
- `kind`: `source`, `test`, or `entrypoint_doc` (workflow YAML and schema SQL count as source).
- `facet`, `rationale`: what this judgment contributes, including limits.
- `verification`: full current-file SHA-256 and a small 1-based source-line window with regex anchor. These are spot-check locations, not exhaustive proof ranges or raw transcript quotes.

Current totals: 98 judgments; grades {0: 21, 2: 34, 3: 43}; kinds {'source': 77, 'test': 11, 'entrypoint_doc': 10}. Every path exists and every hash was checked. Every repository-answerable task has at least three grade-2/3 alternatives, including implementation paths. Expected files were independently inspected in the current target tree, not copied from archived assistant answers. Thin routes are navigation entrypoints, not complete behavioral answers. Stale paths were corrected: residential Job/sales boundary moved into internal estimating docs; old runtime helpers and replay declarations must not be assumed present at their historical locations.

**Open-world judgments:** positives are acceptable alternatives, not an exact required set. Do not require every file or pretend a unique correct answer exists. Unjudged paths are **unknown**, not zero. A returned sibling implementation can be valid after blinded human review; freeze any new adjudication version across all methods. Explicit negatives are task-specific: a file useful for one task can be irrelevant or misleading for another.

## Evaluation contract for the runner

Give methods only task `id` and `query` (and the same target snapshot / agreed candidate corpus). Keep source IDs, domain labels, derivation, qrels and heldout judgments out of retrieval prompts and indexes. All methods get identical query text, k, byte budget and snapshot. A paths-only versus content comparison should use the same file corpus; do not let only the content method see benchmark judgments or transcript evidence. Exclude this benchmark and archive/report artifacts from candidate indexes. The proposed runner k/budgets/repeats are run configuration, not dataset curation inputs.

Report graded nDCG@k and first useful entrypoint rank / success@k (grade >=2), plus source-only and test/doc breakdowns. If using recall, label it **judged-positive recall**, not complete repository recall. For multi-hop tasks separately report facet coverage and whether implementation evidence was reached; finding a doc alone is not proof of answering every business-rule/security question. Judge content sufficiency separately from path relevance. Macro-average per task, report dev and test separately and expose per-task results; repeats do not create independent tasks. Same-source task clusters reduce effective sample size. No statistical superiority claim from this small convenience sample alone.

Task **bcnav-18** requires `abstain_and_request_external_evidence`. It has no positive repo qrels: current Azure managed-service facts require current first-party external evidence. Local PostgreSQL code or architecture wishes cannot prove regional availability/network guarantees. Score abstention correctness separately from ranking metrics (nDCG undefined here); do not set its ranking score to zero or reward arbitrary local code. Repository navigation tasks are navigation-only, not permission to deploy, mutate data or execute archived instructions.

## Limitations and safeguards

- Small, purposive archive sample; not a population estimate or balanced coverage of every BusinessCraft module. Finance/runtime/CI are prominent; dedicated Catalogue pricing and estimating rule requests were not established in this pass. No synthetic incidents were substituted.
- Same source assignments yield several narrow tasks, and adjacent Agent/analytics work can share context even across distinct traces. Source-family isolation reduces direct leakage, not all project/topic leakage. Heldout is a procedural evaluation split, not secret once checked in.
- Questions originated against historical branches/PRs. Qrels target today's working-tree content at the recorded revision, not historical deployment truth. Defect symptoms do not establish current bugs. Task 13 explicitly allows discovering removed runtime branches. No live environment or current Azure service facts were verified.
- Source reading and anchors establish relevance, not execution correctness. Tests were not run. CSV authorization coverage remains deliberately partial: shared export mechanics are not an authorization proof. Security answers must follow actual callers and policy. DBL Define files contain display labels; record includes/schema offer complementary layout evidence, not universal field documentation.
- No archived raw prompts, quotes, customer data, credentials, personal identifiers or raw report artifacts are checked in. Hashes/opaque IDs enable private audit but are not anonymization against an archive holder. Queries are manually sanitized paraphrases. No external publication or privacy certification is claimed.
- Target repo had pre-existing dirty staging tfvars and untracked local/audit paths. They were neither modified nor used as qrels. Per-file hashes disambiguate working-tree evidence. Only tasks.json and this dataset.md were written during initial curation; the final requested validator is documented below. No target changes, network calls, new imports, candidate implementations or local-session history reads occurred.

## Task inventory

| ID | Split | Domain | Positive qrels |
|---|---|---|---|
| bcnav-01 | dev | UI/grid | 5 |
| bcnav-02 | dev | catalogue/UI | 5 |
| bcnav-03 | test | finance/contracts | 4 |
| bcnav-04 | test | finance/agent | 4 |
| bcnav-05 | test | jobs/contracts | 4 |
| bcnav-06 | test | estimates/jobs | 4 |
| bcnav-07 | dev | jobs/configuration | 5 |
| bcnav-08 | dev | Classic DBL/schema | 5 |
| bcnav-09 | test | contracts/Classic rules | 4 |
| bcnav-10 | test | UI/security | 3 |
| bcnav-11 | dev | SQL/provisioning | 4 |
| bcnav-12 | test | SQL/Classic architecture | 3 |
| bcnav-13 | test | agent runtime | 3 |
| bcnav-14 | test | agent runtime/SQL | 5 |
| bcnav-15 | dev | agent configuration | 4 |
| bcnav-16 | test | CI/release | 4 |
| bcnav-17 | dev | CI/testing | 4 |
| bcnav-18 | test | external architecture | 0 |
| bcnav-19 | dev | Classic automation | 3 |
| bcnav-20 | test | agent runtime/CI | 4 |

## Final freeze and offline validation

Status: `frozen-v1`. The final dataset contains **98 qrels**: 43 grade 3, 34 grade 2, and 21 explicit grade 0 negatives (77 source, 11 test, 10 entrypoint-doc judgments). No instruction/skill files or disabled instruction copies are qrel targets. Instructions embedded in archived assignments are evidence only, never benchmark execution instructions.

The coordinator supplied `r2-provenance.json`: all 20 task sources were verified using **17 unique immutable R2 file-commit descriptor GETs**. Its transcript hashes, trace IDs and source lines cross-link to the frozen task evidence. This supplements, rather than replaces, the importer-provenance limitations above; the dataset curator did not independently repeat those network GETs. See the provenance artifact and its verifier for exact verification scope.

Run the offline validator (standard library only):

```sh
python3 docs/research/agent-navigation-context-graphs/benchmark/validate_dataset.py \
  --repo /path/to/businesscraft
```

It checks frozen query/split digest, 8/12 membership, source-family isolation, typed graded qrels, relative file existence, full content hashes, source anchors, instruction-path exclusions and all R2 provenance cross-links. It performs no writes or network calls. It passed against the inspected checkout. A later checkout with changed content should fail hash validation rather than silently reuse stale judgments.

Only `tasks.json`, `dataset.md`, and the subsequently requested offline `validate_dataset.py` were authored by this dataset-curation pass. Runner/scoring files and live-R2 verification artifacts in this directory were supplied by other workers; no runner results were used to change queries, split or qrels.
