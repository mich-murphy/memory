# Evidence 25: streamed `memory check` (brief 34) and the 2,000-case property run

Dates are UTC; the work ran from 2026-11-18 06:04Z to 07:17Z. Status: increment CHECK accepted
locally as `1adecef` (7 commits over `2a4d017`). Nothing was installed, sent to R2 or run against
the acceptance run's state root. The acceptance run `20261117-a28bfa4` kept running on its own
release binary throughout.

[Brief 34](../../34-streamed-check.md) sits on `2a4d017` (on top of `main` `b3a08d2`).

## 2,000-case property run (operator request, 2026-11-18)

On `main` `b3a08d2`, seed 2145483499, `HEGEL_TEST_CASES=PROPTEST_CASES=2000`, the nextest
filters of gates 05 and 06:

| Part | Result | Time |
|---|---|---|
| Hegel (`test(/hegel_/)`) | 31 of 31 passed (6 slow) | 646 s |
| proptest (`test(/proptest_/)`) | 33 of 33 passed (5 slow) | 455 s |

The slowest test, `hegel_property4_capture_state_machine`, ran over 7 minutes, inside the
15-minute property timeout. `cargo mutants` was not run (operator decision: skip).

## Commits

`git log --reverse 2a4d017..1adecef`. "Worker" is a delegated Sonnet session (medium), "parent"
the coordinating session.

| Commit | By | Change |
|---|---|---|
| `064f225` | worker | `Verdict` public; `judge_whole` (the current path, generic over `FileSystem` and a fetch closure) as the oracle; `judge_with` dispatch |
| `94ed079` | worker | streamed path for prefix-stable sources: local chunks against decoded stored pieces, both sides drained before the verdict, epoch size and `sha256` checked incrementally |
| `4b3d4f1` | worker | `memory/tests/check_property.rs`: `hegel_streamed_check_verdict_equals_whole` and two named cases |
| `6d7595e` | worker | real-binary peak test; failing at 249 MB when the worker ran out of turns |
| `a2879a6` | parent | exact allocation in `decode_piece` (capped at 8 MiB) and `S3Store::get` (from `Content-Length`, capped at 64 MiB) |
| `b37267c` | parent | rustfmt |
| `1adecef` | parent | test: a GET body one byte under the 64 MiB limit is read, one over is `Server` |

The worker ran out of turns after `6d7595e`; the parent took over its paths.

## The 249 MB peak

With the streamed path in place, `memory check` over one captured 100 MB incompressible source
still peaked at 249 MB (`/usr/bin/time -l`). A probe confirmed the source took the streamed path.
Bisection by temporary edits (reverted): no reads 3 MB; local reads only 28 MB; stored fetches
only 249 MB. Preallocating only the decoded piece gave 152 MB, only the GET body 141 MB, both
43 MB. Cause: two buffers grown by doubling per 8 MiB piece (`ureq` `read_to_vec` in
`S3Store::get`, `read_to_end` into an empty `Vec` in `decode_piece`), whose freed blocks the
macOS allocator keeps, the effect `realfs.rs` already notes for file reads. Both functions are
shared with restore, export and the daemon's read-back, which now allocate exactly too.

`decode_piece` caps the reservation at 8 MiB because a manifest piece's `len` is only checked to
be non-zero; a larger piece still decodes, growing past the reservation. `get` reads through
ureq's limit reader as before; an over-limit body is still `BodyExceedsLimit` (recovered by
`ureq::Error::from` from the reader's `io::Error`) and maps to `Server`. ureq refuses a body of
exactly the limit too (it reads once more after the last byte); that was already so with
`read_to_vec`.

After the fix: 43.4 to 43.8 MB over 30 stress runs of the peak test, 0 failures.

## Teeth (parent, applied to `check.rs`)

Script in the parent's scratchpad (`check_teeth.py`, 300 Hegel cases); each tooth replaced exact
anchors, ran `check_property` and the `check::` tests of `s3_store`, and restored the file.

| Tooth | Caught by |
|---|---|
| production judges prefix-stable sources whole | `checking_one_100_mb_incompressible_source_stays_under_128_mb` |
| local file read whole on each chunk | same |
| every decoded piece kept alive | same |
| epoch `sha256` check dropped | `hegel_streamed_check_verdict_equals_whole` |
| stop at the first difference without draining | `a_difference_after_the_local_end_with_a_corrupt_later_piece_is_restore_not_bytes`, the property |
| stat after the read removed | `a_file_that_changes_between_the_stats_is_unsettled_when_streamed`, the property |
| `Behind` without the prefix comparison | `a_changed_prefix_of_a_longer_file_is_a_mismatch_not_behind`, the property |
| chunk compared one byte off at a boundary | the property, `a_file_that_changes_between_the_stats_is_unsettled_when_streamed` |

8 of 8 caught.

## Checks on `1adecef`

`nix develop .#rust --command sh -c 'cd rust && MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python
PROPERTY_CASES=500 bash scripts/gates.sh'`: all 14 sub-gates 0, seed 4165249849; 04-nextest 715
passed, 05-hegel 32, 06-proptest 33, 10-consumer 2; coverage 96.68 % (9,151 of 9,465 lines).
(The run on `a2879a6` had only `01-fmt` failing, on the parent's own edit; fixed in `b37267c`.)

## Limits and open findings

- Sources that are not prefix-stable (protected config, `Config`/`TextAsset` profiles) still take
  the whole path; protected ones are capped at 8 MiB.
- The peak test runs on macOS and Linux runners in hosted CI; locally only macOS was measured.
- Not run: `cargo mutants` (skipped by operator decision), anything against R2.
