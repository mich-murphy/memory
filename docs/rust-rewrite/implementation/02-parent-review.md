# Parent implementation review and acceptance evidence

Status: local foundation verified on the source snapshot below; **no slice or
release acceptance**. The active task/ownership brief is
`docs/session-capture-handoff.md`; this file contains parent review evidence.

## Source qualification

Initial HEAD: `57d8400f632ab47a81e34d060f86d3687123153e`; existing design and
research hashes are recorded in `baseline-identities.json`. Source changes must be
identified separately from HEAD while this checkout is dirty.

Parent read DESIGN.md in full, the retained contract goals, AGENTS.md, repository
quality gates, the challenge synthesis and DDIA cross-check. No numerical design
targets or operator decisions have been changed.

Existing main CI: https://github.com/mich-murphy/memory/actions/runs/41988501354
has six successful jobs and a failed Go fuzz job. `gh run view 41988501354
--log-failed` (exit 0) identifies `internal/capturehooks/FuzzEditJSON`:
`remove(edit(x)) != x` for an empty hook-event list. This is inherited Go behavior,
not caused by Rust. No Go fixes or gate exemptions have been made. Full repository
acceptance must not be described as green on that evidence.

## Reviewed constraints for affected implementation

1. **Protected bytes and digest order** (DESIGN 7.3 versus 8.3).
   Structural transforms can change length and bytes. A protected snapshot must
   finish transformation before deriving stored offsets, object raw hashes and
   whole-content digests; otherwise a restore of the scrubbed content cannot
   verify against hashes of the secret-bearing input. Original file fingerprints
   and transformed content positions are different facts. Privacy audit confirmed
   this interpretation; protected body implementation follows it. No runtime path yet.
2. **Immutable dedup and encoding metadata** (6.2, 7.3).
   Object keys use decoded hashes, but manifests also promise encoded hashes and
   lengths. A 412 establishes existence, not that an existing encoding equals the
   newly attempted encoding. Storage implementation must obtain/validate existing
   encoded metadata before publishing references if encodings differ. Pinning a
   compressor alone is not cross-version correctness evidence. Pending protocol
   fixture before slice 2; never weaken reader digest verification.
3. **Checkpoint growth** (3.7 versus 6.3).
   A checkpoint includes every retained append epoch and its pieces, not only the
   latest 64 pieces. A 64-capture interval bounds traversal depth, not checkpoint
   size. Existing cost measurements remain frozen; do not claim strict linear
   metadata cost or a 64-piece checkpoint size from the interval. Independent review
   confirmed the implications without changing the decided format.
4. **Actual native architecture**.
   Scaffold worker's SSH probe reported BC-01 `Linux x86_64`. A native build there
   will prove x86_64, not aarch64. Keep actual-host evidence distinct from any
   future CI cross-platform coverage.

## Acceptance matrix

| Requirement group | Current evidence | Acceptance |
|---|---|---|
| Slice 0 pinned workspace and nine gates | Isolated scaffold and corrected integrated local wrapper passed; final-toolchain native BC Hegel probe passed after SSH retry | Local gates/native probe pass; full slice acceptance pending |
| P1/P5 prerequisites for core | P1 synthetic outcomes independently verified; P5 lacks safe historical hash metadata | Pending |
| Core types, planner, coalescer | Implemented, reviewed, 2,000-case tests plus actual-source mutation pass | Pure prerequisite verified; not slice 1 |
| Locator, retained manifests/checkpoints | Not implemented | Pending |
| Privacy/launch/strict boundary fixtures | Implemented and independently reviewed; transformation v2; known-file fixtures, canaries and compile-fail tests pass | Pure boundary verified; no real PUT/hint sink proof |
| Fault-injected commit and restart convergence | Not implemented yet | Pending |
| Native hook/watch/footprint | Synthetic prototypes are not native daemon acceptance | Pending |
| Authoritative reader and unchanged consumer journey | Not implemented yet | Pending |
| R2 actual semantics | Separate acceptance bucket/token unavailable | External prerequisite |
| Mac and BC-01 seven-day shadows | Not started; elapsed time cannot be simulated as acceptance | Pending |
| Cutover and retired archive deletion | Not attempted | Not authorized as incidental implementation work |

## Intermediate implementation corrections (parent review)

These are historical findings against in-progress files. Corrections were observed
in source and verified by the final integrated run below; this is not full-system
requirements acceptance.

- `domain.rs`: ETag cannot use identifier syntax because real conditional tokens
  are opaque/quoted; SourceKind's dotted wire vocabulary must match the specified
  schema; canonical digest syntax and generated brand constraints need tests.
  Mechanics owner corrected all three with exact wire/schema tests.
- `planner.rs`: snapshot branch ignored audit evidence and could return Unchanged
  after a stable full-digest mismatch. Sent required snapshot repair/recheck tests.
- `privacy/mod.rs`: protected-size rejection originally followed an input clone;
  correction requested before allocation. Canonical capability binding and actual
  consumed rules digest requested, including cross-source substitution fixtures.
- `privacy/document.rs`: handwritten URL splitting accepted invalid-port URLs
  rejected by Go net/url and could preserve literal data that Go withholds.
  Required a well-known URL parser plus malformed port/escape/IPv6/control tests.
- `privacy/document.rs`: TOML→JSON→TOML conversion risks converting nonsecret
  datetime values to strings on any scrubbed document. Required typed TOML
  preservation and a mixed safe-datetime/secret fixture.

## Parent verification performed

`python3 docs/rust-rewrite/implementation/verify-evidence.py` — **exit 0**.
Reviewed actual prototype code and verified the two completed runs share script
hashes and deterministic P1/P3/P6/P7 outcomes; all 33 recorded pre-existing
research/design file identities remain unchanged. This does not establish actual
Rust audit, hook, footprint or daemon behavior. P4 uses C/Python analogues; P5/P8
have no safe real-corpus hash metadata; P6 natural sleep remains unobserved.

## Parent corrected integrated verification — 2026-11-07

All workers explicitly released their paths. Parent reviewed new external boundary
tests and the independently found scalar/object-leaf Args parity defect. The minimal
fix applies Go's existing argument scrub semantics: preserves ordinary positional
text, sanitizes secret flags and inline JSON, leaves array semantics unchanged.
Parent advanced `TRANSFORM_VERSION` to `memory.privacy/2` and reviewed/refreshed the
rules-digest golden. This is an explicit semantic change, not silent digest reuse.
The deliberately stale golden failed 101 before refresh (log retained); no deployed
Rust state or archive migration was involved.

First unchanged integrated wrapper: **exit 1**, all subgates green except coverage:
1,261/1,363 = **92.5165%**, versus measured scaffold floor **98%**. Parent delegated
meaningful missing-behavior tests, not exclusions or guard removal. Five external
mechanics tests and twelve independent privacy fixtures added; invariant-only
fallbacks remain intact. Their targeted checks passed, but parent did not accept
those as a substitute for the complete rerun.

Final exact command:

```sh
nix develop --max-jobs 2 --cores 2 --command sh -c \
  'cd rust && PROPERTY_CASES=2000 PROPERTY_SEED=730108 bash scripts/gates.sh'
```

**Wrapper exit 0**, every recorded subgate exit 0:

- Formatting; workspace all-target clippy with denied warnings.
- Dependency/security/license checks and pure-core production dependency bans.
- Nextest **66/66 passed, zero skipped**; compile-fail doctests **5/5 passed**.
- Hegel and proptest: **2,000 cases**, recorded seed **730108**.
- Actual window mutation: original source baseline two properties pass; disabled
  production comparison in scratch compiles, then both engines fail the independent
  runtime byte oracle (mutant cargo 101). No compile failure counted as proof.
- Schema drift/roundtrip and enforced-lint diagnostic checker.
- Full workspace line coverage **1,338/1,363 = 98.1658%**, unchanged **98%** floor.
  No exclusions were added and no test/security assertions removed.

Evidence: `evidence/02-corrected/` contains exact command output/statuses, seeds,
coverage JSON, all subgate logs, source archive and 45-file SHA-256 snapshot.
All 45 files remained unchanged during the run. Source qualification is initial
HEAD plus these dirty-file hashes, not HEAD alone. `evidence/01-integrated/` retains
the failed coverage run rather than overwriting it with green results.

Parent reran `verify-evidence.py` and `git diff --check`: both **exit 0**. Frozen
33-file pre-existing design/research identities remain unchanged. Existing workflow
checks (`make actionlint`, offline pedantic zizmor) passed; hosted Rust CI was not
run. Full Go/native/live acceptance gates were not rerun, and inherited Go fuzz
failure remains recorded above.

## Acceptance boundary and next prerequisites

This verifies the local scaffold and pure safety/mechanics prerequisites only.
Privacy remains the retained known-file policy, **not general-purpose DLP** (Go's
non-UTF8/binary and unrelated positional text boundaries are preserved). Published
body checks are not real PUT/hint sink tests. The planner's structural epoch guard
is not retained multi-epoch reconstruction, zero-PUT recovery or property 4.

No slice accepted: the final BC official-toolchain probe blocker was cleared by the
operator-requested SSH retry below. P5 lacks approved safe real-corpus offset/hash
metadata and remains a slice-1 prerequisite. P8 lacks duplicate-share metadata but
is informational, not a gate. P2 needs a separate acceptance bucket and scoped token
before slice 2; P2b uses that bucket for an informational checksum check, not a gate.
Never substitute existing archive credentials. Bucket
administration starts at https://dash.cloudflare.com/ ; no credentials belong in
this report or chat. Actual Rust hook/watch/RSS, natural sleep, reader/import/index/
report journey, host shadow periods and controlled cutover remain unproved.
Locator/manifests/runtime/store/reader remain unimplemented. Existing Go defaults
and services are unchanged; capture was not re-enabled. Resolve remaining
prerequisite blocks before claiming slice entry or expanding into live work.

## Operator-requested SSH retry — final BC probe verified

Authentication recovered. Initial default-shell probe returned Linux x86_64 and
Nix; command exit 127 was solely missing cargo outside Nix, not SSH failure.
A new medium-effort gpt-6.1-sol worker received bounded report/evidence ownership
because the released original worker's session retention had expired.

Fresh scratch `/tmp/memory-slice0-official-bc.RcafTr` used byte-exact current
flake/lock, unchanged lint-version input and frozen synthetic Hegel probe. Native
compiler: official Rust **1.99.0**, commit `b940084d7eb6a299eb4bfeb8e34901bc051e7ac4`,
`x86_64-unknown-linux-gnu`, LLVM **23.1.1** and matching bundled tools. Hegel
**0.52.0**, static engine (`hegeltest-c 0.50.0`), selected `pieces_concatenate`:
**exit 0**, 2,000 cases, seed **730052**, 1 passed, 0 failed/ignored; one deliberately
wrong research property filtered, matching the original native probe scope.
Compilation 19.53s, test 0.39s; Nix jobs/cores and Cargo jobs/test threads limited to 2.

Parent independently reviewed actual command/compiler/test logs and scripts,
verified every evidence checksum, matched remote-returned inputs with current
local source hashes, reran frozen-evidence verification (0) and confirmed all 45
local integrated-gate source hashes unchanged. Logs, exact statuses, scratch lock
and source copies: `evidence/03-bc-recheck/`. Worker explicitly released paths.

**Specific SSH/final-toolchain native Hegel blocker cleared.** This is not the
integrated workspace native suite, ARM coverage, daemon/privacy sink, live R2 or
release acceptance. No production source, service, hooks or capture changed; no
unchanged local full suite was rerun.

## Session-transfer documentation reconciliation

The final handoff update changes documentation and its evidence verifier only,
not production code or canonical gate configuration. A read-only gpt-6.1-sol medium
reviewer (`ec236af4-f61c-488`, completed, no reservations) checked navigation outside
the parent's edits. Parent corrected README/doc-map authority, the stale byte-slicing-
only Rust guide, actual mandatory gate descriptions, informational versus blocking
prototypes, old opt-out instructions and worker-report ownership/status navigation.
Dated failures and operating records remain history; DESIGN.md and frozen research
were not rewritten to match implementation progress.

The single active brief now records uncommitted checkout provenance, all released
owners, evidence/replay pointers, precise remaining slices and safe next actions.
No commits/pushes, full suites, hosted CI, installs, live host checks or capture were
performed as incidental documentation work. Reuse recorded green results only
while relevant source identities still match; the source archive supplements its
recorded base checkout rather than representing a standalone repository.

The original baseline verifier returned 1 on the authorized README update because
its initial 33-file snapshot included mutable project entry points as well as frozen
research. The original manifest was retained unchanged. A separate
`handoff-documentation-identities.json` records exact original/new hashes for only
four explicitly named navigation/security documents. The verifier checks those
exact revisions and all 29 other baseline identities; it does not ignore mismatches.
Six isolated scratch checks passed (two valid states, four expected rejections),
including rejection of a DESIGN exception, later doc drift and a false original hash.

`evidence/04-handoff/` records this limited verification. The full Nix shell
preparation exceeded the tool's 60-second cap during dependency restoration,
before the checks ran; that attempt is not a pass. The exact Python 3.14.8 component
from locked nixpkgs then ran the verifier and six guard checks successfully (exit 0).
`git diff --check` passed; all 103 files across prior evidence checksum manifests,
45 integrated source hashes and 109 local documentation link targets checked clean.
Frozen design/research and all runtime code remain unchanged. No full/native/live
suite is claimed from these documentation checks.
