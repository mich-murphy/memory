# Evidence 24: `memory owners` (brief 33)

Dates are UTC; the work ran from 2026-11-18 03:05Z to 03:48Z. Status: increment OWNERS accepted
locally as `5d5366f` (5 commits over `1c9ff5d`). Nothing was installed, sent to R2 or run against
the acceptance run's state root. The acceptance run `20261117-a28bfa4` kept running on its own
release binary throughout.

[Brief 33](../../33-owner-listing.md) sits on `1c9ff5d` (on top of `main` `714635b`).

## What it does

`memory owners` lists every session owner with a `current.json` under `<prefix>sessions/` by
ListObjectsV2, one owner text per line on stdout, sorted and deduplicated, in the form `memory
export` and `memory restore` take. stderr is `memory: owners=N` or one closed `memory: <code>`
line. Exit 0 listed (also N = 0), 1 failed (`store`, `listing_outside_prefix`,
`listing_unexpected_key`, `listing_loop`), 2 config or token (`config_prefix_required` included),
64 usage. Nothing reaches stdout unless the whole listing succeeded.

## Commits

`git log --reverse 1c9ff5d..5d5366f`. "Worker" is a delegated Sonnet session (medium), "parent"
the coordinating session.

| Commit | By | Change |
|---|---|---|
| `106b8b9` | worker | rusty-s3 feature `xml`; `S3Store::list_page` (one exchange), `Page`, `MAX_LIST_BODY` 4 MiB, pure `map_list_reply` and 5 unit tests |
| `ab941ad` | worker | loopback fake: ListObjectsV2 handler (prefix, continuation token, `max-keys`, `encoding-type=url`) and `Fault::ListBody` |
| `2aefbf7` | worker | `memory/src/owners.rs` (paging loop, strict key check, exit codes); `main.rs`, `lib.rs` wiring |
| `984f708` | worker | 13 real-binary tests in `memory/tests/s3_store/owners.rs`; six consumer-gate helpers made `pub(super)` |
| `5d5366f` | parent | a test where key order is not owner-text order and a key repeats across pages; test name corrected |

The worker finished within its turns and reported its checks; the parent reran every check below.

## Dependency

Feature `xml` on the existing `rusty-s3 =0.11.0` pin adds `instant-xml 0.7.7`,
`instant-xml-macros 0.7.2`, `xmlparser 0.13.6` and `heck 0.5.0` to the lock file, the same four
the parent's probe found before the brief; `cargo deny check` is clean. DESIGN 13 already names
this crate for LIST.

## Corrections to the brief

- The brief said `build_store` refuses an empty prefix. It does not; the refusal is in
  `assemble::build` (`assemble.rs:133`), which `restore` and `export` do not call either.
  `owners` checks `config.prefix.is_empty()` itself before building the store (exit 2,
  `config_prefix_required`, no request sent; test `owners_empty_config_prefix_is_exit_2`).
- The brief asked for a non-ASCII segment to round-trip. `SessionId` admits only
  `[A-Za-z0-9._:-]`, so no such owner can be written, and `memory owners` fails closed on one
  (`listing_unexpected_key`). The `:` case is kept.
- The brief named crate paths as `memory/...`; they are under `rust/crates/`.

## Teeth (parent, applied to `owners.rs`)

Script in the parent's scratchpad (`owners_teeth.py`); each tooth replaced one exact anchor, ran
`cargo nextest run -p memory --test s3_store -E 'test(/owners/)'` and restored the file.

| Tooth | Caught by |
|---|---|
| only the first page read | `owners_2500_keys_across_three_pages_sorted_with_colon`, `owners_repeated_continuation_token_is_a_loop` |
| a key outside the prefix accepted | `owners_key_outside_the_prefix_fails` |
| an unexpected key skipped | `owners_refused_owner_text_is_unexpected`, `owners_shared_key_under_sessions_is_unexpected` |
| repeated-token guard removed | `owners_repeated_continuation_token_is_a_loop` |
| exit 0 after a store error | `owners_a_403_fails_as_store`, `owners_a_500_fails_as_store`, `owners_unparsable_body_fails_as_store` |
| unsorted output with duplicates | first pass: survived (every seeded key list was already in owner-text order); after `5d5366f`: `owners_sorts_by_owner_text_and_drops_a_repeated_key` |
| `shared/` listed too | `owners_empty_family_is_exit_0_without_stdout`, `owners_lists_exactly_the_local_walk_and_every_owner_exports` |

7 of 7 caught on `5d5366f`.

## Checks on `5d5366f`

`nix develop .#rust --command sh -c 'cd rust && MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python
PROPERTY_CASES=500 bash scripts/gates.sh'`: all 14 sub-gates 0, seed 1849428079, 500 cases;
04-nextest 711 passed, 05-hegel 31, 06-proptest 33, 10-consumer 2; coverage 96.66 % (8,991 of
9,302 lines). Worker stress: the owners tests 30 runs alone, 0 failures.

## Limits and open findings

- Session owners only; shared owners are slice 3 (`export` refuses them).
- Not run against R2. The acceptance tool lists the run prefix with the same crate and paging,
  so the protocol is exercised there, but `memory owners` itself first meets R2 in the short
  post-run run.
- Not run: 2,000 property cases, `cargo mutants`.
