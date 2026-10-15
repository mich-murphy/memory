# Isolated acceptance and adversarial review

This suite is **not an integrated acceptance pass**. It was prepared independently
against the pre-change base. No real sources/configuration, production hooks,
remote captures, R2 credentials or R2 mutations are used. All literals are fake.

## Repeatable CLI journeys

From this checkout, using the project's toolchain:

```sh
nix develop /Users/mm/dev/memory -c sh -c 'AGENT_SESSIONS_TEST_R2=0 go test -race -count=1 -timeout=5m ./cmd/agent-sessions -run "^TestAcceptance"'
python3 .agents/skills/verify-agent-sessions/verify_test.py
```

The new tests use the existing compiled CLI with an explicitly constructed safe
environment, private synthetic homes, and the production Local store. They refuse
host managed/ancestor instruction paths without reading them. No hooks are run.
They verify physical stream hashes, plan/commit equality and contiguous parent
chains; reconstruct every historical generation with encoded/decoded piece and
whole-file hashes; and distinguish immutable history from mutable checkpoints.
They do not invoke a fictitious restore CLI or substitute a SQLite cache oracle.

Coverage prepared:

- Base plus exactly one append tail; explicit append kind; idle source does not
  fold; rewrite/empty/deletion history after two complete cache losses.
- 65 incremental appends consolidate the bounded chain without replacing the
  first base; every acknowledged generation remains reconstructible after loss.
- Native-shaped Pi task child grouped into the parent manifest as a subagent;
  late model change beyond the head prefix survives append and cold recovery.
- Shell/fish literal assignments and a project-snapshot literal absent from ALL
  physical archive bytes, decompressing every zstd object; references and an
  explicit transcript non-DLP control remain exact. App/cache/UI/logs SQLite
  noise excluded; Pi git/npm source provenance retained.
- Regular symlink target captured exactly. Guard reclassification retains prior
  history without a false tombstone, including cold recovery; an allowed sibling
  proves the sweep did not simply skip all capture.
- Synthetic >8 MiB live WAL database exported consistently; fixed 1 MiB pieces;
  one row edit changes only header/data chunks, untouched pieces reuse objects,
  no unrelated new blobs; restored SQL integrity/count/value checked, then
  source/cache loss retains all generations including the deletion tip.

The skill's regression journey now requires these named CLI passes and rejects
skips. Python and native live wire observers accept omitted FILE dependencies,
but an explicit legacy list must still match every field in piece order.
The native observer also checks encoded lengths. Supplemental decoder tests run
offline, without launching actual agents. Metadata dependencies are not omitted.

Directory/dangling symlink skips fence deletion observations only at the skipped logical
path and its descendants; unrelated absent siblings can still receive tombstones, including
after cold recovery. Any fenced historical source alias retains the whole generation.
Actual discovery/read/export failures still make the root incomplete and block its deletions.
Snapshot traversal policy is unchanged.

## Must-test before release / gaps

1. **False deletion:** transition previously captured files into each noise or
   literal guard exclusion, and replace a previously captured subtree by a
   directory symlink. Exclusion/partial observation must not publish deletion.
   Exercise regular links, dangling links, ELOOP, directory links, changed targets,
   and links into credential/config/state/noise identities in raw AND snapshots.
   This suite covers regular links only; explicit directory-skip diagnostics and
   false-tombstone transitions still require integrated coverage.
2. **Metadata precedence:** missing main transcript must not let a child's
   session ID/cwd/model impersonate the parent. Main removed, child-only, multiple
   children, deleted-main recovery, fork/resume and rewritten tails need assertions
   for both values and provenance. A stale cached main must not hide loss. This
   suite checks late model and child membership, not all new field spellings.
3. **Dependency compatibility:** omitted FILE arrays derive ordered dependencies
   from pieces, including repeated objects; explicit empty/null/mismatched legacy
   arrays must fail. Test corrupt/missing shared blobs during cold recovery and
   publication failure. Keep metadata dependencies strict. Existing Go archive
   oracle updates are owned by archive worker, not replaced by weaker checks here.
4. **Record bounds:** removing duplicate FILE dependencies does not bound manifests
   that repeat pieces/versions and all historical snapshots. Test approaching the
   16 MiB descriptor bound with long paths, many chunks/files/versions, and ensure
   no acknowledged source becomes permanently uncapturable after crossing it.
5. **Chunking compatibility:** threshold edges (<8 MiB, exactly 8 MiB, >8 MiB),
   final short chunk, DB growth/shrink/reversion, migration from legacy whole-file
   pieces and WAL checkpoint/vacuum need exact history and dedup tests. Do not
   silently interpret new boundaries as old append pieces. Backup is not raw
   source-file equality while WAL is live: query restored consistency and values.
6. **Secret guard:** shell/fish quoting, export/set variants, references/default
   expansions, multiline/oversize/binary sources and parser failure boundaries
   must be tested in snapshots as well as raw discovery. Known-config parsing
   and malformed-config protection must remain strict. This is not transcript
   DLP or retroactive archive deletion. Snapshot guard returning no files must
   not be mistaken for successful complete capture without diagnostics.
7. **Append/fold:** exact threshold boundary, append after a fold, interruption
   during consolidated tail upload, mutation during compression, rewrite with
   common prefix, and cold append continuation require additional fault tests.
   Idle must not publish new data merely because age changed.
8. **Pi package provenance:** parent commit d7545fd owns Git HEAD/dirty/npm lock
   assertions. The new CLI test proves source bytes survive, not all package
   provenance fields. Native-agent, R2, full skill and normalizer acceptance remain
   parent-owned; normalizer is explicitly not implemented.

## Chunk experiment

Deferred the generic benchmark port to the parent rather than copying the private
experiment module or its results. No real DB, private path or results were added.
A safe follow-up should live in-module, call internal/raw.Compress (actual collector
settings), accept explicit user-supplied consistent DB pairs, emit only numeric
sizes/counts and opaque scenario indices, and never print paths/content/hashes.
Do not call estimated record bytes an actual descriptor-size measurement. Separate
CPU timing from a shared compression cache; deduplicate new chunks within a
generation, not just against the previous one. Fixed-size vs CDC comparison is
an experiment, not a production performance claim; require benchstat separately.

## Prepared-branch execution (not integrated acceptance)

- Python decoder contracts: 3 tests passed.
- Native wire decoder/reconstruction/chain contracts: passed, including race.
- Formatting and golangci-lint: passed (0 issues).
- `make check` was invoked with live R2 disabled. Formatting, lint, go fix,
  tidy, deadcode and vulnerability checks passed; the command exceeded the
  300-second harness deadline during race/coverage execution and was terminated.
  Coverage/CRAP/bench/actionlint completion is not claimed.
- Pre-implementation CLI baseline fails the new-design assertions as intended:
  append kind is capture; idle folds add a generation; append 64 replaces base;
  Pi child/late model missing; literal/noise bytes archived; regular links absent;
  >8 MiB SQLite is one whole-file piece. A mutable-checkpoint retention assertion
  was corrected in the test oracle, not in product code.

No production implementation was changed. Gopls MCP is not available in this
agent's tool set; compiler/race tests and lint were used for diagnostics.
Final integrated `make check`, all seven CLI journeys, native agents, full skill
and separately authorised R2 acceptance remain required and parent-owned.
