# Independent production-adaptation review

## FINAL stable-handoff review

**Disposition: no reproduced blocking P1/P2 correctness issue remains within the documented trusted-user, POSIX, stable-worktree model.** The earlier source no-follow and instruction-backup findings remain fixed. Both P2s from the historical adaptation review below are now resolved. Approve a limited **lexical-default** rollout with explicit rebuild and normal-search fallback; this is not an adversarial-filesystem certification or evidence that graph won.

Reviewed the stable final source without editing it. **37/37 tests PASS (6.088 s)** against the real production module using isolated `XDG_CACHE_HOME=/tmp/navigation-final-review.XXXXXX/cache` (mktemp-generated directory) (actual disposable path recorded in `/tmp/navigation-final-review-last`), including the previously failing noncanonical `/tmp` parent spelling. No production index was rebuilt, and no frozen prototype/data/results or source files were changed.

### Final integrity/boundary verification

- All four frozen-source and all five runtime artifact hashes in `SOURCE_HASHES.json` match independently recomputed bytes.
- Final `nav.py` SHA-256: **`ec5bef8e9529fc2b2e8fa9c02234fda29a4769bdf5a275740a1dab3d076f8581`**.
- Final `test_runtime.py` SHA-256: `0f756c79c7e1d293259f78602557608fec90c35c04c00e70ec09c184c0705f5c`.
- Ranking region remains byte-identical to frozen, SHA-256 `c6a39ebf27a8b861fc5f97a56d70a467b84443d11bc0499dab2768d6d0d25ae0`; all 16 declared unchanged helpers and all 16 original complete test methods match. Full `frozen-nav.diff` exactly matches an independently generated diff.
- Extraction is **not byte-identical**, now accurately disclosed. Its runtime hash is `852bf38b516fa25a574342782302b7e66411143399b546516ed9b98b44c39f33`. Independently removing only the documented object/name/dependency/reread guards restores the exact frozen region and its `40aa05ca…` hash. This is a legitimate defensive behavior change, not a false preservation claim.

### Optimized signatures and targeted safety checks

`nav.py:173–223` memoizes each ancestor chain **per call**, never between queries. It preserves root-relative ancestor/leaf chain shape, directory inode/mode semantics, blocked missing/symlink handling, and leaf metadata including skipped eligible inputs. Chains are extended rather than mutated in place. The passing tests compare directly against the previous algorithm, count one lstat per unique ancestor/eligible leaf, and guard against descending through a known ancestor symlink. Observation timing during concurrent mutation changes, but an atomic snapshot was never promised. The full algorithm hash changes, so an explicit rebuild is required even though signature serialization is compatible.

Additional final-source reproductions, beyond the 37 tests:

- Leaf and ancestor replacement just before actual `os.open`: **rejected**; replacement after directory descriptor acquisition: returned original **`SAFE`**, not external secret.
- Dependency-manifest reread forced unreadable on its fourth read: **controlled `NavError('inputs changed during build; retry')`**, prior DB bytes unchanged, temporary DB removed.
- New untracked sibling: fingerprint unchanged; replacement of a tracked ancestor with an external symlink: fingerprint changed.
- Symlinked tool cache directory: rejected by private-directory validation.

### Earlier P2 dispositions and retained limitations

- **Malformed manifest shapes — resolved:** `nav.py:403–413,489–501` validates JSON object roots, nonempty string names and object dependency fields. The expanded suite passes arrays/null/scalars/bad names/dependency fields. Valid manifests retain original extraction semantics; malformed relations are skipped while text stays indexed.
- **Cache alias/no-open test gap — resolved:** `nav.py:20` canonicalizes only the XDG/home parent before appending the tool directory, fixing `/tmp` aliases without masking a symlinked cache directory. New tests spy on actual `os.open` across inventory/build/query for denied instructions, backups and agent descendants; the obsolete inherited guard is no longer the security evidence.
- External per-worktree caches, private permissions, stat-vs-strict distinction, bounded related traversal and safe text presentation remain as reviewed below and covered by the passing final suite. Trusted root/cache ancestry, same-user cache trust, metadata-integrity limitations and post-check concurrent edits remain explicit assumptions, not newly closed guarantees. No optimized real-corpus latency was independently measured; ancestor deduplication removes repeated stats but does not make freshness constant-time.

**Experiment separation:** coordinator reports the completed frozen run has **400 rows, zero errors**, with `first-run-validation.json` passing frozen hashes, stable ranks and byte caps, and the pre-integration corpus content fingerprint matching afterward. These are coordinator-supplied validation results, not newly re-audited here. Subsequent coordinator edits adding README/repository-guide links are integration changes, not evidence of historical benchmark drift. The previously identified scoring-failure denominator risk therefore is not an observed error incident in this run; first-run artifacts remain immutable. Full-run retrieval results still do not establish agent success or graph superiority.

---

## Historical pre-optimization review (preserved; dispositions superseded above)

**Scope:** `/Users/mm/businesscraft/businesscraft/tools/navigation/`, reviewed independently against frozen prototype source. No production code, frozen candidates, benchmark data/results or first review were modified. Tests and extra fixtures used an isolated external cache under `/private/tmp/navigation-prod-review.0hlh9R`. No session history or raw prompts were accessed.

**Historical snapshot status:** the following review and its passing hash/test checks describe the adaptation **before the coordinator-requested `signatures()` optimization**. At that time revalidation was pending; the FINAL section above now records completed verification of the stable optimized revision. The historical hash checks below do not describe the final revision. Coordinator reports the real production corpus fingerprint matches frozen inputs, and pre-optimization fast freshness/retrieval measured approximately 2.55 s / 0.05 s; these are coordinator measurements, not independently rerun here.

**Disposition:** the prior **P1 source-read symlink race is fixed** for path substitutions beneath a trusted worktree root; the prior **P2 instruction-backup examples are excluded before opening**. No new P1 deployment blocker was reproduced for the documented trusted-user, POSIX, non-concurrently-edited worktree model. Recommend a limited lexical-default rollout, with the P2 robustness/test fixes below directed to the coordinator. This is not an approval for adversarial filesystem integrity or evidence of graph superiority.

All line references below are to production `tools/navigation/` unless otherwise stated.

## Validation results

- **32/32 tests PASS**, 5.049 seconds, against the actual production module, using an absolute, canonical external `XDG_CACHE_HOME` and `PYTHONDONTWRITEBYTECODE=1`.
- An initial run with the noncanonical `/tmp/...` spelling produced **30/32 passes**, explained below; this is not silently omitted.
- Every `SOURCE_HASHES.json` frozen-input hash and runtime-artifact hash **matches** the inspected files (four frozen inputs, five runtime artifacts).
- All **16 declared unchanged helper functions** are exact source matches.
- Build extraction and query ranking regions are **byte-identical**, with their declared hashes respectively `40aa05ca4ff1232800434fb9826d3a5fff0fa32dc57287be0446223e3f6c5a9b` and `c6a39ebf27a8b861fc5f97a56d70a467b84443d11bc0499dab2768d6d0d25ae0`.
- All **16 original complete test methods** are identical. `frozen-nav.diff` exactly matches a freshly generated complete unified diff.

This substantiates the ranking/extraction preservation claims, **conditional on the same stable eligible corpus**. Broader exclusions, stable-open behavior, runtime defaults, freshness and presentation do change deployment behavior, as ADAPTATION already discloses. It is not unconditional benchmark equivalence.

## Prior findings: reproduced against production functions

### P1 no-follow race: resolved within the stated trust boundary

`nav.py:75–117` opens directories relative to held descriptors with `O_DIRECTORY|O_NOFOLLOW`, opens the leaf with `O_NOFOLLOW|O_NONBLOCK`, requires a regular file, reads at most MAX_FILE+1, and compares before/after descriptor metadata.

Additional deterministic races patched `os.open` immediately before the real production syscall:

| Substitution | Production `safe_file` result |
|---|---|
| Replace checked leaf with external symlink immediately before leaf open | `None` |
| Replace `src` with external symlink immediately before directory open | `None` |
| Replace `src` after its descriptor was acquired, immediately before leaf open | `b'SAFE'` from the pinned original directory, not external secret |

The suite also passes unstable-read rejection. The old `Path.read_bytes` check/open vulnerability is no longer present in source ingestion. Directory descriptors pin objects rather than freezing the checkout: a renamed directory can still supply its original bytes. This is acceptable under the documented no-concurrent-edit/snapshot limitations, not a promise that returned text always corresponds to the latest pathname.

The resolved root and its ancestor hierarchy, routes/runtime source, and current-user cache remain trusted inputs. No-follow opens beneath root do not sandbox a hostile replacement of ancestors *above* the opened root, bind mounts, hard links, or same-user cache tampering. Do not broaden the guarantee to those cases.

### P2 backup instruction ingestion: resolved for the reported cases

`nav.py:58–72` now rejects `AGENTS.backup-2026.md`, `CLAUDE.old.md`, and `SKILL.original.md`. I committed these synthetic files and guarded actual **`os.open`** calls to fail if any were opened; `inventory` completed and returned only `src/ok.md`. This tests exclusion-before-open with the new I/O implementation, not merely an eligibility predicate.

The policy remains a named-convention exclusion, not universal prompt-injection prevention. Other instruction conventions are not automatically denied; treat all repository snippets/provenance as data, never instructions.

## Remaining actionable P2 findings (coordinator fixes; no self-edits)

### P2 — Valid JSON with invalid package shape crashes build outside the error contract

**Location:** `nav.py:376–384,475–482`, CLI exception handling near the end of `main`.

A disposable tracked `package.json` containing `[]` caused the real `build` function to raise **`AttributeError: 'list' object has no attribute 'get'`** at line 381. `null` has the same shape problem. Invalid dependency-field types can also fail later. These errors are not caught by the CLI's handled exception list, so a temporarily malformed manifest can abort the whole navigation build with a traceback rather than a bounded JSON error/diagnostic. This is inherited from the byte-identical extractor, not a false hash claim or new ranking regression.

**Requested fix:** validate manifest object/name/dependency-field shapes before storing/using them; produce a controlled diagnostic or `NavError` and preserve the previous published index. Add malformed-shape regressions and update adaptation/hash evidence. This is an operational robustness issue, not demonstrated unauthorized file access; it need not block a constrained pilot with valid manifests.

### P2 — Test harness depends on canonical cache spelling; an old no-open assertion is now ineffective

**Location:** `test_nav.py:15–22,117–137`; `nav.py:20,50–55,146–148`.

With `XDG_CACHE_HOME=/tmp/navigation-prod-review.0hlh9R/cache`, tests build metadata using the `/tmp` root spelling, while CLI root discovery resolves it to `/private/tmp`. The two CLI-success tests fail with `STALE_INDEX: root`. Rerunning the unchanged 32 tests using canonical `/private/tmp/.../cache` passes. The production CLI already resolves roots before both build/query, so this is a reproducible test/API-precondition portability problem, not evidence that normal CLI builds cannot be queried.

Also, the inherited forbidden-content test patches `Path.read_bytes`, but production ingestion now uses `os.open/os.read`; its “not opened” assertion no longer observes source ingestion. The independent guard reproduction above covers the reported backups, but the checked-in suite should do so too.

**Requested fix:** canonicalize synthetic roots/cache bases in test setup (and document or normalize direct-API root requirements); move no-open guards to the actual syscall boundary, including original and backup instruction names. Do not count a green predicate-only test as equivalent no-read coverage.

## Runtime behavior and limits checked

- **Fast vs strict freshness:** `nav.py:173–230` fast mode enumerates tracked eligible paths and hashes stat signatures, including skipped binary/oversized files and ancestor identities; it does not run full content inventory. Tests verify same-size/same-mtime edits via ctime, rename/add/delete, HEAD/worktree/schema failures, build mutation, and strict content mismatch under a mocked signature collision. `--strict` adds full eligible-content hashing; neither mode is an atomic snapshot. Stat mode remains O(paths × path depth) metadata work, not constant-time; no real-repository speed claim was measured here.
- **External/private cache:** `nav.py:154–170,203–210,343–350` supports deployment inside the indexed checkout by storing DBs outside it. Default keys include resolved worktree root; root/HEAD metadata reject cross-worktree reuse. Builds use umask 077, private owned directories, 0600 DB and atomic replacement; queries reject nonregular/nonprivate/wrong-owner DBs. Existing nonprivate directories fail rather than being silently chmodded. A trusted XDG/home ancestor hierarchy is assumed; validation is not descriptor-pinned race protection for a hostile writable cache parent. Private permissions do not make same-user cached source text non-sensitive.
- **Related graph:** `nav.py:625–672` admits only explicit source references, Markdown links, imports and dependencies; excludes mentions, ownership and membership hubs. Both directions/provenance and declared-vs-unknown ownership are retained. CLI depth ≤2, fanout ≤24, results ≤50 and whole-response byte caps are enforced; per-edge dedup and bounded frontiers keep expansion bounded. Bounds are CLI preconditions, not independently enforced on every direct Python call. No new graph ranking is introduced by this separate follow-up command.
- **Text snippets:** `nav.py:699–740` preserve query result order/scores, use safe bounded file reads, cap excerpts at 24 words/240 characters, escape path controls, remove nonprintable snippet controls, and account for final-newline bytes. An extra ESC/BEL fixture produced no control characters in the excerpt (23 words/117 characters; full text response 188 bytes). Suite presentation/cap/nonmutation tests pass. These are heuristic source lines, not compiler spans or guaranteed semantic matches; post-freshness edits can change displayed snippets and strict mode does not prevent that. Excerpts may reveal sensitive tracked source; they are not redacted or instruction-trusted.

## Recommendation and experiment separation

Proceed only as **lexical-default, opt-in local navigation**, with ordinary search fallback and explicit unsupported-platform/concurrent-edit limitations. Ask the coordinator to fix the P2 items above and add descriptor-level no-read tests. Broader hostile-filesystem deployment would require a separate root/cache trust and race-hardening design.

The frozen benchmark scoring P1s remain a separate experiment-quality concern. Per coordinator context, current full-run errors are zero and qrels were manually validated; that does not imply an observed scoring-failure incident, and no first artifacts should be rewritten. I did not inspect private history or wait for another benchmark. Runtime lexical defaults and optional `related` are sensible product choices, **not evidence that graph won**. Measure discovery effort, useful evidence reached, end-to-end stat/strict latency and output cost before extending graph adoption claims.
