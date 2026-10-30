# Frozen navigation experiment prototypes — v1

Dependency-light Python 3.9+ / SQLite FTS5. No service, pip dependency,
embeddings, remote calls or owner inference. `nav.py` checks FTS5 at startup;
local verification succeeded with Python 3.14 and SQLite 3.53.3. Uses only
stdlib and the `git` executable. All runtime writes stay in this directory's
ignored `.cache/`; nothing is written to BusinessCraft. This cache restriction
is intentional for the experiment, not a production deployment design.

## Ready-to-run interface

```bash
P=/Users/mm/dev/memory/docs/research/agent-navigation-context-graphs/prototype
R=/Users/mm/businesscraft/businesscraft
python3 "$P/nav.py" --root "$R" build
python3 "$P/nav.py" --root "$R" query 'business rules' --mode lexical --limit 10 --max-bytes 9600
python3 "$P/nav.py" --root "$R" query 'business rules' --mode map_lexical --limit 10 --max-bytes 9600
python3 "$P/nav.py" --root "$R" query 'business rules' --mode hybrid --limit 10 --max-bytes 9600
python3 "$P/nav.py" --root "$R" query 'business rules' --mode map_only --limit 10 --max-bytes 9600
# Extracted graph without authored map (additional ablation):
python3 "$P/nav.py" --root "$R" query 'business rules' --mode graph --depth 2 --limit 10 --max-bytes 9600
# Optional exact directory/file scope, not a glob:
python3 "$P/nav.py" --root "$R" query 'navigation' --mode lexical --scope web/apps/web --limit 10
(cd "$P" && python3 -m unittest -v test_nav.py)
```

`map.md` is the human-readable map for the agent pilot. `routing.json` is the
same compact authored routing candidate for the CLI. Its only architectural
basis is `docs/repository/README.md` and `repo.yaml`; no task/judgment data or
disabled instructions were consulted. CODEOWNERS is a separate extracted
metadata source, not a basis for the authored map.

Global `--root`, `--db`, `--routes` precede the subcommand. By default the DB is
`.cache/<sha256-of-absolute-worktree-root-prefix>.sqlite3`. Optional `--db` must
also be inside this prototype's `.cache/` and outside the indexed worktree.
Build atomically replaces the DB; do not run concurrent builds to the same DB.
The tool never modifies Git state. Rebuild after editing this script or routes.

## Candidate definitions / freeze

| Mode | What it returns |
| --- | --- |
| `lexical` | Shared path/heading/content FTS rank, optionally scoped |
| `map_lexical` (alias `map`) | Same lexical seeds + matching authored routes and scoped lexical searches |
| `map_only` (alias `map-only`) | Authored matching roots/start documents only, **no implementation search** |
| `graph` | Same lexical seeds + extracted adjacency, no authored routes |
| `hybrid` | Lexical + authored routing + extracted adjacency |

The independent runner supplies `scoped_rg`; this prototype does not relabel
FTS as ripgrep. Map-only is an intentionally different orientation baseline,
not a claim of equivalent implementation recall. Equal top-k/byte budgets
should be used across all indexed candidates. No full benchmark has been run
by this implementation worker. The coordinator-requested generic stopword and
source-reference corrections were made **before** freeze/evaluation. Hashes in
`FREEZE.json` identify the final candidate artifacts. No post-heldout tuning is
part of this version; production cache/freshness improvements belong separately.

## Shared corpus and freshness

- Start from `git ls-files -z`, not recursive filesystem discovery. Only tracked
  UTF-8 source/docs/config allowlisted extensions, at most 512 KiB/file. Includes
  Classic `.dbl`, `.dbx`, `.dbc` and `.def`. Binary/NUL-containing and oversized
  files are skipped. Submodules are not recursively traversed.
- Never read symlinks (including symlinked ancestors), paths outside the root,
  `AGENTS.md`/`CLAUDE.md`/`SKILL.md` instruction files (including disabled/backup
  variants), agent configuration trees, `tasks.json`, judgment
  files, benchmark/eval/dataset trees, dependencies, fixtures, dumps, generated
  build/artifact trees, lockfiles, minified bundles or common local caches.
  Exclusions are deliberately conservative and identical for all modes. This
  is a navigation corpus, not every byte of the checkout.
- Every query recomputes SHA-256 for eligible inputs, plus HEAD, resolved root,
  algorithm file and routing JSON. Dirty tracked changes, staged additions,
  deletions, changed HEAD, and another worktree's DB cause `STALE_INDEX` with
  exit status 2. A newly created **untracked** file is intentionally not an
  input; `git add` makes it visible and invalidates the DB. Changes to excluded
  files do not invalidate it. Build checks again before atomic publication.
  Lockfile exclusions use known names and `.lock`/`.lockb` suffixes, not a
  substring: legitimate `lock.rs`, `clock.ts` and `agents.ts` remain eligible.
- Fingerprinting re-reads the corpus on each query: correctness first, potentially
  seconds of latency. `freshness_seconds` and `retrieval_seconds` are reported
  separately; measure end-to-end subprocess latency as well. A concurrent edit
  after the final fingerprint cannot be prevented without checkout snapshots.
- FTS5 stores path, Markdown heading lines and full eligible content; weights
  are path 8, headings 4, body 1, Porter/unicode61. All lexical-using modes share
  exactly those fields and input files. No hidden semantic fields help graph
  seeds. Query terms are Unicode alphanumerics, deduplicated, generic stopwords
  removed, first 32 meaningful terms, combined with OR. No alias/config guessing.

## Frozen ranking and traversal policy

Lexical top 40 gets normalized score `1/(1 + .08*zero_based_rank)`.
Routes use literal term-set overlap/query-term-count, at most 3 matching routes;
ties use route ID. Matching roots/docs score `.48 + .10*overlap`. For each
matching route prefix, top 10 shared-FTS scoped hits score
`(.62 + .06*overlap)/(1 + .08*rank)`. Map-only stops at roots/docs.

Graph starts at the first 8 lexical seeds; hybrid also starts at matched route
roots/docs with score `.5`. Depth is 1 by default, configurable only 1 or 2.
Fanout is 12 by default, configurable 1–24. Both incoming and outgoing edges
are traversed. Explicit links precede mentions, then membership; deterministic
ties use edge kind/path/provenance/line/direction. Up to 16 strongest frontier
entries survive to the second hop. Ownership is never traversed.

Explicit edge scores are source score × `.65`, membership × `.36`, and mere
path mentions × `.30`. Results retain the **maximum**, not summed contributions
(no hub accumulation), at most three distinct reasons. Seeds are never demoted:
the top five lexical hits always outrank graph or authored additions. No source,
test, or docs boost is hard-coded; related docs/tests appear only through actual
edges or their lexical terms. Results sort score descending, then path.

## Extracted relations vs authored routing

- `markdown_link`: relative inline/reference Markdown links to exact corpus
  files/directories; fragments stripped. Remote/absolute/escaping targets are
  ignored; missing/excluded relative targets enter `issues`. Fenced examples
  are ignored. This is a conservative regex subset, not a Markdown parser.
- `source_reference`: exact existing root-relative `sourceRefs` in leading
  Markdown frontmatter. Supported forms: indented `- path: value`, quoted
  variants, or simple scalar list entries; `sourceRefs: []` is accepted.
  Complex/inline/folded/tagged/glob/escaping values receive diagnostics, never
  guessed YAML semantics. These are source-authored references, not independent
  evidence that the target proves a behaviour.
- `path_mention`: simple root-relative body literals matching exact indexed
  nodes, outside fences/frontmatter; max 256 distinct destinations/document
  including sourceRefs. Explicitly labelled `mention-not-proof`, weaker than
  links. No fuzzy basename resolution, arbitrary absolute paths or code globs.
- `ts_import`: statically written relative TS/JS imports/reexports/require,
  only when an exact target or extension/index resolution is unique. `.js`
  to unique `.ts`/`.tsx` is supported. No tsconfig aliases, dynamic expressions,
  nonliteral imports, or ambiguous candidates are guessed. Regex extraction
  may still see literals in comments; this is not a compiler/type checker.
- `ts_package_import`: unique exact local package name to its manifest;
  package subpaths are not guessed.
- `package_dependency`: dependencies/dev/peer/optional dependencies to unique
  tracked local package manifests. External packages are not graph nodes.
- `directory_member`, `domain_member`, `package_member`: immediate directory,
  top-level directory domain and nearest containing package manifest. These
  are structural navigation relations, **not inferred business ownership**.
- `owned_by`: explicit `.github/CODEOWNERS` only; last matching rule wins,
  including empty-owner overrides. Leading-slash/directory/segment globs are
  supported; unsupported negation, brackets and escaped spaces are ignored.
  Owners remain unknown (`null`) when unmatched; named humans/teams are never
  inferred. Matching provenance line is returned; owner hubs never expand.
- `authored_route`: separately marked `origin=authored`, from `routing.json`
  with original repository-map provenance. Extracted graph traversal selects
  only `origin=extracted`; routing is activated only by map/hybrid modes.

The DB has `nodes`, `lex`, `edges`, `owners`, `issues`, and fingerprint `meta`.
Build reports file/directory/edge counts, dead Markdown link count and all
extraction diagnostic count. Diagnostics remain in SQLite; no target file
bodies are returned by the CLI. Extraction is deliberately incomplete rather
than inventing links or silently resolving ambiguous imports.

## Compact output / errors

Query emits one JSON line with `status`, `mode`, root/HEAD/input/algorithm
fingerprints, timing, `results`, `truncated`, `output_bytes`, and
`estimated_tokens` (ceil bytes/4, **not provider usage**). Each result has `rank`,
`path`, `kind`, `line`, `score`, bounded `reasons`, and explicit owners or null.
Reasons include lexical rank/BM25 or edge type/from/direction/provenance/source
line/hop/origin. Result `line` is the first literal query-term line (or 1), not a
compiler span; edge provenance `line` belongs to the edge's source document.
Directory lines are null except authored entrypoints may carry line 1.

No file body/snippet dump. Default limit 10 (max 50), default total UTF-8 budget
12000 bytes (1024–65536 accepted), including JSON and trailing newline. The
lowest-ranked results are removed until the whole response fits; metadata-only
overflow yields `CONTEXT_LIMIT`. No-result responses are successful with
`results: []`. Query length is bounded to 1000 UTF-8 bytes. Invalid scope, stale
DB, unavailable FTS5 or another handled failure produces
`{"status":"error","error":"..."}` and exit 2. Parse stdout as JSON; inspect
exit status. An error is not a retrieval miss. Graph depth affects graph/hybrid
only. Absolute/parent-escaping scopes are rejected.

## Tests / limitations

Synthetic tests create isolated Git repositories/worktrees **inside `.cache/`**
and remove them afterwards. They cover FTS5/no-results, stale edits, staged new
files, deletions, HEAD/worktree changes, excluded/untracked inputs, no reads of
forbidden instruction files, symlink/path escape, dead links, ambiguous imports,
package edges, CODEOWNERS overrides/nontraversal, mode ablations, sourceRef
subset/unsupported values, meaningful token limits, scope and output budgets.
No real task list or relevance judgment is inspected. See `VALIDATION.md` for
local checks; the coordinator owns evaluation and adoption decisions.
