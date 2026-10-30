# Independent review: frozen navigation prototype and experiment

**Recommendation:** retain as an exploratory, frozen retrieval experiment; do not deploy unchanged or claim graph/agent-efficiency success. Prefer the least complex lexical + small authored map solution unless hybrid shows a useful *increment over map_lexical*, at acceptable end-to-end cost. No benchmark completion was awaited and no aggregate winner is asserted here.

Reviewed `prototype/nav.py`, `test_nav.py`, README, FREEZE, and `benchmark/run.py`, SCORING, dataset and all tasks/qrels. Paths below are relative to this research directory. Frozen candidates, qrels/results and target checkout were not changed. Reproductions used copies and disposable Git fixtures in `/tmp/nav-review.hphF7X` (macOS resolves this under `/private/tmp`). Only this review was written here.

## Actionable findings

### P1 — Production file-read boundary is check-then-open, not symlink-safe

**Location:** `prototype/nav.py:74–85` (`safe_file`), used by inventory/build/query.

The ancestor symlink, containment and size checks precede a separate `Path.read_bytes()`. Replacing the checked file or an ancestor between these operations allows reading outside the checkout; the size bound can likewise be bypassed. Reproduced deterministically by intercepting `Path.read_bytes` to replace the previously ordinary `ok.md` with a symlink to an external fixture: `safe_file(root, 'ok.md')` returned `b'DENIED_EXTERNAL_CONTENT'`. This demonstrates the race, not an observed attack on the target.

**Fix before production with concurrent/untrusted writers:** open relative to a trusted directory descriptor with no-follow protection on every component; verify regular-file identity and read at most MAX_FILE+1 through the opened descriptor. Alternatively require an immutable, trusted snapshot and explicitly narrow the safety guarantee. The README's acknowledged post-fingerprint edit window (`README.md:79–82`) does not make the stronger “never read symlinks” statement true during races. Static symlinks are correctly rejected by existing tests. This does not establish contamination of a quiescent benchmark.

### P2 — Disabled/backup instruction exclusion is narrower than advertised

**Location:** `prototype/nav.py:63–71`; `prototype/README.md:64–68`; regression coverage `prototype/test_nav.py:109–137,180–184`.

`AGENTS.backup-2026.md`, `CLAUDE.old.md`, and `SKILL.original.md` are eligible. In a disposable committed repository, building with empty routes and querying a unique token in `AGENTS.backup-2026.md` returned that file as a lexical hit. Contents are therefore ingested, not merely exposed as paths. The tests cover only selected exact suffix forms. Other instruction conventions, such as `copilot-instructions.md`, are also eligible; whether to deny them needs an explicit policy, not inference from this prototype's three names.

**Fix:** define a reviewed instruction/config exclusion policy including actual repository backup conventions; test no-open behavior for each. If the intended contract is only enumerated names, narrow the README instead. Production agents must still treat indexed repository text and path/provenance strings as untrusted data, never executable instructions. Exclusion alone is not prompt-injection protection.

### P1 — Runner can silently evaluate stale qrels and a moving corpus

**Location:** `benchmark/run.py:114–130,139–147`; dataset requirements `benchmark/dataset.md:35,45,88`.

The runner hashes dataset files and records HEAD/dirty status, but never invokes equivalent qrel hash/anchor/revision validation. Candidate freshness compares against its newly built index, **not** the qrels' verified content. A later checkout can build and score successfully against stale judgments. During a run, candidates reject changed inputs, while `scoped_rg` rereads current content using the original path manifest without a freshness check. The latter can see changed files or follow replacement symlinks through explicit rg paths. This is asymmetric snapshot handling, not evidence it happened in first_run.

**Fix for the next experiment version:** preflight the frozen validator and capture a shared content manifest; use an immutable snapshot for all variants or reject the entire run on corpus drift. Verify freeze hashes programmatically too, rather than merely recording current artifacts. Keep existing results intact and mark any violations rather than repairing the frozen candidate mid-run.

### P1 — Retrieval failures disappear from quality averages

**Location:** `benchmark/run.py:147,155–159`.

Exceptions produce error-only rows; split quality averages select only rows containing metrics. Thus a method failing the hardest first-pass tasks can have inflated MRR/hit/coverage. `errors` is reported, but this does not restore a common task denominator; `splits.tasks` also counts only successful calls. All-failure methods yield null metrics rather than comparable operational failure rates.

**Fix:** report intended/success/error counts per split and class, plus unconditional operational success@k over the fixed eligible task set (retrieval errors count as failures). Preserve conditional relevance metrics separately if useful. Keep external-only task handling separate; do not manufacture positive ranking relevance for it.

### P2 — Shared numeric byte cap is not a shared effective retrieval budget

**Location:** `benchmark/run.py:55–63,94–99,141–146`; `prototype/nav.py:525–544`; `benchmark/SCORING.md:3`.

Candidates are capped first with their native metadata envelope and rich reasons, then normalized/capped again. Baseline gets only the second cap and much smaller per-hit objects. The saved candidate `ranking` is already native-truncated, contrary to interpreting it as a full pre-cap diagnostic. Reproduction: identical ten paths under the runner's 9,600-byte cap retained ten compact baseline objects but only nine rich graph-reason objects; candidate envelope overhead can remove more. This may be a legitimate *product response-cost* tradeoff, but does not isolate ranking quality.

**Fix/report:** distinguish native deployable-response quality/cost from ranking-only quality using one common result schema and one cap applied once. Record pre-cap rankings only through a separately designed diagnostic interface. Do not silently modify the frozen adapter or compare normalized bytes as if they were the full native response cost.

### P2 — Evaluation leaves a required negative control unmeasured and contains unreachable evidence

**Location:** `benchmark/run.py:37–53,154–160`; `benchmark/dataset.md:37–39`; `benchmark/tasks.json:1035–1047,1626–1691`; `prototype/nav.py:21–28,80`.

`bcnav-18` correctly has null ranking metrics (no positives), but the runner does not measure abstention/action correctness at all. Its output can return local paths without registering a failure of the requested external-evidence boundary. This is a retrieval-only limitation, not proof that an agent would make an unsafe answer. Dataset-requested nDCG is also absent from the scoring contract/implementation; disclose that deviation rather than implying all requested metrics were delivered.

The grade-3 `bcnav-11` bootstrap DDL, `classic/src/runtimes/dbl-sql/generated/tables.sql`, is impossible for **every** method: `generated` is excluded and the current file is 2,532,752 bytes, above 512 KiB. Static inspection of all positive qrels found this one unreachable positive. Shared exclusion is fair across methods, but coverage cannot reach 100% for that task and the excluded file is important to its bootstrap facet.

**Fix:** report corpus-reachable and full-task coverage separately without changing frozen qrels; disclose the omitted evidence. Add an independent answerability/abstention evaluation in a new protocol. Do not equate high path relevance with complete security/business-rule evidence.

## Production constraints and adoption tradeoffs (not hidden prototype defects)

- **Freshness cost is real:** `nav.py:91–102,117–122,438` reads/hashes every eligible file on *every query*, even map-only. Build reads repeatedly and inventories again before publication. SQLite retrieval speed alone therefore overstates the efficiency benefit. Measure subprocess wall time, freshness time, build/storage cost, and actual follow-up file reads. A production incremental cache needs reliable invalidation plus periodic full verification; size/mtime alone weakens current guarantees.
- **Cannot directly deploy this in its own indexed repo:** `nav.py:125–132` requires the DB inside prototype `.cache` AND outside the indexed worktree. Calling `db_path` for `/Users/mm/dev/memory` reproduced `database must not be inside indexed worktree`; supplying an external DB is also forbidden. Intentional experiment isolation, but production must support an external per-worktree cache and lifecycle/concurrency policy.
- **Ownership is metadata, not authority:** only `.github/CODEOWNERS` is used (`nav.py:283,304–313`; README:139–143). Last-match and empty-owner overrides are tested and owner hubs are not traversed. Root/`docs/CODEOWNERS` fallback, unsupported pattern diagnostics and validation of actual team membership are absent by design. Preserve unknown vs explicitly empty owners; never infer business/data authority or access permission from these labels.
- **Graph scope of evidence:** top five lexical hits are guaranteed to outrank graph additions (`nav.py:458–461`, README:104–109). Therefore hybrid improvements do not establish graph superiority at top five; hybrid vs lexical also conflates authored routing. Primary graph increment is hybrid vs map_lexical, especially lower-rank source/test/facet coverage. Missing graph-only runner ablation limits attribution further. Regex links/imports and mentions are navigation hints, not behavioral proof.
- Small purposive 20-task sample, repeated/source-clustered assignments, open-world incomplete judgments, and no fresh agent trials cannot establish general task success, real token savings or optimal structure. No concrete qrel-to-retriever leakage was found in the reviewed code: candidates exclude benchmark/task paths, baseline inherits indexed file paths, and only query text enters retrieval. Frozen hashes and provenance are useful safeguards, not proof of causal independence. Version blinded adjudication of unjudged alternatives across methods if needed.

## Checks completed

- Copied prototype unittest suite: **16/16 PASS** (2.001 s); no tests run in the target's cache.
- Copied benchmark `--self-test`: **PASS**.
- All five `FREEZE.json` artifact hashes: **MATCH**.
- Offline dataset validator on the existing BusinessCraft checkout: **PASS**, 20 tasks, 8 dev / 12 test, 98 qrels; hashes/anchors/family isolation/instruction exclusions/provenance cross-links valid. No network or target writes.
- Reproduced symlink race read, instruction-backup ingestion/retrieval, budget representation disparity, unreachable positive, and in-repo cache rejection as described above.

**Decision:** continue the frozen run only as descriptive navigation evidence. Before production, fix file-open safety and instruction policy, externalize caching, and validate realistic end-to-end freshness cost. Before drawing adoption conclusions, account for errors/snapshot validity, budget representation and answerability. Adopt compact map + lexical navigation if it reduces actual discovery effort; add graph machinery only for demonstrated incremental evidence reach, not to make an arbitrary graph experiment “succeed.”
