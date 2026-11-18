# 33. `memory owners`: list the captured session owners (2026-11-18)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 32](32-export-and-consumer-gate.md). Evidence goes to `evidence/24-owners/`. Text that
states a plan stays as written; later changes are marked.

The acceptance run `20261117-a28bfa4` keeps running while this brief is worked on. Nothing here
is installed, sent to R2 or run against the run's state root.

## Goal

The step after the acceptance run is "a short run with the streamed binary on real data that
measures the peak and exports and imports every captured owner" (brief 32, "After this brief").
`memory export` takes one owner, and nothing in Rust can say which owners exist in the store.
`memory check` and the consumer gate derive owners from the local walk, which cannot see an
owner whose local files are gone. Cutover (DESIGN 18 step 5, "restore, and analytics") needs the
same answer from the store itself.

**OWNERS**: `memory owners` lists every session owner that has a `current.json` under the
configured prefix, by ListObjectsV2 over `<prefix>sessions/`, one owner text per line on stdout,
sorted, in exactly the form `memory export` and `memory restore` take.

Not in this brief: shared owners (slice 3; `export` refuses them); `export --all` (a loop over
this output is enough for the run); `heads` pages or the Go refresher's CLI; decompressing
recompressed Codex rollouts (open decision; this Mac had 0 `rollout-*.jsonl.zst` and 28
`rollout-*.jsonl` on 2026-11-18 03:05Z, so the post-run step does not depend on it); lifting the
empty-prefix refusal.

## Checkout

`main` is `714635b`. This brief is on `docs/brief-33`. The worker gets `../memory-wt/b33-owners`
on `rust/owners` from `docs/brief-33`.

## Facts checked on `714635b`

| Fact | Where |
|---|---|
| DESIGN names rusty-s3 0.11.0 + ureq for "R2 PUT, GET, LIST, conditionals" | `DESIGN.md` section 13 table |
| The workspace pins `rusty-s3 =0.11.0`, `default-features = false`, `features = ["rustcrypto"]`; ListObjectsV2 (`Bucket::list_objects_v2`, `ListObjectsV2::parse_response`) needs feature `xml` | `rust/Cargo.toml:33`; rusty-s3 `Cargo.toml` `[features]`, `src/actions/mod.rs` |
| Parent probe: with `xml` the normal tree of `memory` gains exactly `instant-xml 0.7.7`, `instant-xml-macros 0.7.2`, `xmlparser 0.13.6`, `heck 0.5.0`; `cargo deny check` is clean (advisories, bans, licenses, sources) | probe worktree, removed |
| `ListObjectsV2::new` always sends `list-type=2` and `encoding-type=url`; `parse_response` URL-decodes keys and fails when `IsTruncated` is true without a token | rusty-s3 `src/actions/list_objects_v2.rs:111-121`, `:229-295` |
| The acceptance tool lists the run prefix with this same crate against R2 and works (it pages by continuation token) | `~/.local/state/memory-acceptance-run/acceptance-list/src/main.rs:24-50` (read only) |
| `S3Store`: "Every `Store` call is exactly one HTTP exchange ... this module has no loop"; key = `prefix + key`; GET body cap `MAX_GET_BODY` 64 MiB; error replies drained up to 64 KiB | `memory/src/s3.rs:1-23`, `:136-143`, `:267-300` |
| `map_reply` treats a 200 without an ETag as `Ambiguous`, so a list reply cannot go through it unchanged | `memory/src/s3.rs:100-121` |
| Session owners live only at `sessions/<archive>/<machine>/<agent>/<session>/current.json`; manifests and objects have their own families; `owner_prefix` / `parse_owner_prefix` are the one spelling of owner text | `DESIGN.md` 6.2; `memory-core/src/limits.rs:20-123` |
| `build_store` refuses an empty prefix (`config_prefix_required`, exit 2) | `memory/src/assemble.rs:40-60`, `:95-107` |
| `export` and `restore` entries: owner text, config from the state root, one stderr line, exits 0, 1, 2, 64, 66 | `memory/src/export.rs:180-219`, `memory/src/restore.rs:309-346` |
| The loopback fake drops the query string (`split('?')`), answers only GET and PUT on an object path | `memory/tests/common/s3_fake.rs:205-320` |
| `memory check` already prints owner text on stdout (`parked owner=...`, `mismatch owner=...`) | `memory/src/check.rs:303-330`, `:417-437` |

## Decisions the parent made where the design is silent

Each is reversible before cutover. A worker that finds one wrong reports it and does not decide.

| Topic | Decision | Reason |
|---|---|---|
| Dependency | Enable rusty-s3 feature `xml` in the workspace pin (no version change). The four crates above are the only additions; record them in the report and evidence | DESIGN 13 already chose this crate for LIST; a hand-written XML parser is the alternative R8 rejects |
| Seam | A new inherent method on `S3Store`, `list_page(&mut self, under: &str, token: Option<&str>) -> Result<Page, StoreError>`: one ListObjectsV2 exchange for `prefix + under`, returning keys with `prefix` stripped and the next token. Not on the `Store` trait (no other implementor needs it). The paging loop lives in the new `memory/src/owners.rs`, so `s3.rs` keeps "one call, one exchange, no loop" | Keeps the module invariant and leaves the ten `Store` impls untouched |
| Reply mapping | 200 -> parse (body read with a cap `MAX_LIST_BODY` = 4 MiB; over the cap or unparsable is `StoreError::Server`); any other status -> the existing `map_reply(Operation::Get, ...)` error mapping (a new `Operation::List` only if needed for clarity). A key not starting with `prefix + under` is refused (`listing_outside_prefix`) | Fail closed; a page of 1,000 keys is about 0.2 MB |
| What is listed | `<prefix>sessions/` only, no delimiter. Every key must be `<owner text>/current.json` with `parse_owner_prefix` accepting the owner text. Any other key under `sessions/` makes the command fail (`listing_unexpected_key`, exit 1) with the count, never the key | Only `current.json` lives under `sessions/`; anything else is a bug or a foreign writer |
| Paging bound | Follow continuation tokens; a repeated token or more than 10,000 pages fails (`listing_loop`, exit 1) | A server bug must not spin forever |
| Output | stdout: owner texts sorted and deduplicated, one per line. stderr on success: `memory: owners=N`; on failure one line `memory: <code>`. No endpoint, key, token or prefix in any line | Same discipline as `export` |
| Exit codes | 0 listed (also when N = 0), 1 failed (store, outside prefix, unexpected key, loop), 2 config or token (including `config_prefix_required`), 64 usage (any argument) | As `export` and `check` |
| Fake | `s3_fake.rs` answers `GET /<bucket>?list-type=2...` (or `/<bucket>/?...`): `prefix`, `continuation-token`, `max-keys` (default 1,000, as S3), `encoding-type=url` (keys percent-encoded in the reply, `EncodingType` echoed), sorted keys, `IsTruncated` and `NextContinuationToken`. Faults apply to list requests by the same `(method, suffix)` queue | Session ids contain `:`; pagination must be exercised |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | OWNERS | `rust/owners` | worker (Sonnet) |

Owns: `rust/Cargo.toml` (the one feature word), `rust/Cargo.lock`, `memory/src/s3.rs`
(`list_page`, its constants and the `Page` type only), new `memory/src/owners.rs`,
`memory/src/lib.rs` and `memory/src/main.rs` (one line each, and the module doc comment of
`main.rs`), `memory/tests/common/s3_fake.rs` (the list handler), new
`memory/tests/s3_store/owners.rs` plus its `mod` line in `memory/tests/s3_store.rs`.

- Proof (each a named test): the real binary against the fake after the real daemon captured
  owners lists exactly the owners the local walk finds (the consumer gate's `owners()` is the
  oracle); 2,500 seeded `current.json` keys across 3 pages list completely, sorted, with a
  session id containing `:` and a non-ASCII segment round-tripping; every listed owner exports
  with `memory export` (exit 0); an empty prefix family gives exit 0 and no stdout; a key outside
  the prefix, a manifest key under `sessions/`, an owner text `parse_owner_prefix` refuses, a
  repeated continuation token, a 500, a 403 and an unparsable body each give their exit code and
  code and no stdout; an argument gives 64; an empty prefix in the config gives 2
  `config_prefix_required`; no line of stdout or stderr contains the endpoint, the bucket, the
  prefix or a token. Unit tests of `list_page` reply mapping, including a body over the cap.
- Teeth (the parent applies each to the worker's code; each must fail a named test): only the
  first page read; a key outside the prefix accepted; an unexpected key skipped instead of
  failing; the repeated-token guard removed; exit 0 after a store error; output unsorted or with
  duplicates; `shared/` listed too.

## Rules for the worker

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply unchanged, with
those of briefs 29 to 32, except that enabling the rusty-s3 `xml` feature is allowed (and must be
reported). No change to what `status.json`, `index.json`, a manifest or an object stores. Never
inspect, signal or read the state of the running acceptance daemon or anything under
`~/.local/state/memory-acceptance*`; never read `.env`; never contact R2. Cargo only through
`nix develop /Users/mm/dev/memory#rust --command sh -c 'cd /Users/mm/dev/memory-wt/b33-owners/rust && ...'`.
Commit after each working step. Stress each new real-binary test 30 times alone and report
failures.

## Acceptance (parent)

Whole diff read; fmt, clippy, rustdoc, nextest, doctests, schemas, `cargo deny`, core bans; the
four mutation predicates still occur once; the teeth above; `scripts/gates.sh` once on the tip
with `MEMORY_ANALYTICS_PYTHON` set; then push (secret check of the range), hosted gates, merge.

## Ledger

- 2026-11-18 03:25Z: brief written on `714635b`; the dependency probe run by the parent.
- 2026-11-18 03:48Z: OWNERS accepted locally at `5d5366f` (4 worker commits, 1 parent commit over
  `1c9ff5d`). Three facts above were wrong and are corrected in [evidence 24](evidence/24-owners/):
  the empty-prefix refusal is in `assemble::build`, not `build_store` (`owners` checks it itself);
  a non-ASCII owner cannot exist (`SessionId` admits `[A-Za-z0-9._:-]`), so that proof was dropped;
  crate paths are under `rust/crates/`. Parent teeth: first pass 6 of 7 (unsorted output survived);
  the parent added `owners_sorts_by_owner_text_and_drops_a_repeated_key`, then 7 of 7. Full gates
  on `5d5366f`: 14 sub-gates 0, seed 1849428079, coverage 96.66 %. Next: push, hosted gates, merge.
