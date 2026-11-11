# P2 / P2b: S3 client choice and R2 checksum enforcement

Status: research, 2026-11-09. Only this file and `p2-s3-client/` were written. `rust/` is untouched.
Raw data: `p2-s3-client/results/` (`fake-*.jsonl`, `r2-*.jsonl`, `rss-*.jsonl`/`.time`, `p2b.jsonl`, `deny-*.txt`, `cleanup.json`).
Code: `p2-s3-client/{os-probe,rs-probe,common,cleanup,baseline}` (standalone cargo projects; `common/` holds the trait copy, a fake S3 server, a CONNECT fault proxy, and the scenarios).
Labels: [measured] on this Mac 2026-11-09, [vendor] from source or docs.

## Recommendation: `rusty-s3` 0.11.0 + `ureq` 3.5.1 (blocking, small thread pool)

Deciding reasons:
1. Neither crate has any retry logic, so "retries disabled on `current.json`" is the default and cannot be re-enabled by accident. [vendor, measured] `object_store` can only disable retries per client (`RetryConfig.max_retries = 0`), not per call, and `PutMode::Update` forces `retry_on_conflict(true)`. Using it safely needs two clients, one with retries off for `put_if_match`.
2. [measured] With default retries `object_store` resends a landed `current.json` write after a 5xx or dropped connection, and the caller sees `PreconditionFailed` instead of `Ambiguous`. This is the exact failure DESIGN 3.5 describes. It was reproduced on the fake server: `[200, 412]` server-side.
3. It is synchronous, so no tokio and no runtime in the shell: [measured] 1.46 MB vs 2.56 MB binary delta, 99 vs 181 crates.
4. [measured] `Retry-After` is readable (429 with `Retry-After: 7` -> `Some(7)`). `object_store` surfaces only status text.
5. [measured] Content-MD5 is one header line. `object_store` 0.14.2 has no Content-MD5 attribute [vendor, DESIGN 3.5].
6. Costs: we own signing glue, error mapping and a 64 MiB read cap (about 100 lines, see sketch). Neither client had a correctness failure on R2.

## Results

| # | Question | `object_store` 0.14.2 + tokio 1.48 | `rusty-s3` 0.11.0 + `ureq` 3.5.1 |
|---|---|---|---|
| 1 | Real R2 conditionals [measured] | create Ok; repeat 412; GET ETag == PUT ETag; If-Match fresh Ok; stale 412; If-Match on missing key 412; GET missing 404 | identical |
| 2a | Committed-then-5xx, retries OFF (fake) [measured] | 1 server PUT; 503 -> Throttled, 500 -> Server (reply is a real 5xx, the write landed) | 1 server PUT; same mapping |
| 2b | Committed-then-5xx / drop, retries ON (fake) [measured] | resent: server saw `[200, 412]`; caller got `PreconditionFailed` (false "stale fence") for 503, 500, drop and for `put_if_absent` | no retries exist; always 1 PUT |
| 2c | Lost reply on real R2 via CONNECT proxy dropping the reply [measured] | `Ambiguous`, write landed, verify GET = `AlreadyOurs`, 1 tunnel connection (retries off and on) | same |
| 2d | Hang after commit, client timeout [measured] | `Ambiguous` | `Ambiguous` (`Timeout(Global)`) |
| 2e | Other mappings [measured] | 401/403 Auth, 404, 412, connection refused Offline, 429/503 Throttled, 500 Server | same; plus `Retry-After` |
| 3 | DST fit | Adapter is a plain `impl Store`; `block_on` inside, tokio only in the shell crate. Trait-level DST unchanged. | Same; no runtime at all |
| 4a | Release binary size, `opt-level=s`, LTO, strip, panic=abort [measured] | 2,841,424 B (baseline 285,632; delta 2.56 MB) | 1,744,288 B (delta 1.46 MB) |
| 4b | Unique crates, `cargo tree -e normal` [measured] | 181 | 99 |
| 4c | Peak RSS, 8 concurrent 8 MiB PUTs x 3 rounds, whole process [measured] | 84.5 MB | 89.2 MB (md5 on) |
| 4d | `cargo deny check licenses bans sources`, `rust/deny.toml` [measured] | licenses FAIL, bans ok, sources ok | licenses FAIL, bans ok, sources ok |
| 5 | Content-MD5 | not settable per put; only `Checksum::SHA256`/CRC64NVME config [vendor] | `content-md5` header, signed; R2 accepted it (`md5_correct` 200) |
| 7 | Maintenance [measured crates.io] | 37 versions; 0.14.2 2026-09-15, 0.14.1 07-15, 0.14.0 06-22; open issues #840 (customizable retry policy), #885 (Azure lost-reply retry), #396 | rusty-s3 23 versions, 0.11.0 2026-10-27; no open retry/conditional issues. ureq 3.5.1 2026-11-02, 3.5.0 10-21 |

Notes:
- RSS includes the probe's own 8 x 8 MiB bodies (64 MiB), so the client overhead is about 20-25 MB for either and both are inside DESIGN 7.3's 80 MiB bound plus process base. Not isolated further. The difference between the two is within noise for 3 rounds.
- `cargo deny` failure, both alternatives: the only rejected license is `CDLA-Permissive-2.0` on `webpki-roots` (ureq's rustls) and `webpki-root-certs` (object_store). Fix: add it to `allow` in `rust/deny.toml` (parent decision). After that, bans pass; 6 (rs) and 7 (os) duplicate-version warnings (e.g. sha2/md-5/digest 0.10 vs the workspace's sha2 0.11) are warn-only. The probe crates needed `license = "MIT"` to pass the unlicensed check.
- Item 2c is not a discriminator: on real R2 with a dropped reply `object_store` did not resend either (1 connection). The resend only reproduced on the fake server (drop and 5xx). I did not find why the proxy case differs (the proxy swallows the reply and closes after 1.5 s, which may be classified non-retryable). Treat 2b as the proof of the hazard.
- `object_store` request-level retries stay on for GET/other paths unless the client is built with 0; a two-client adapter would be needed.

## P2b: does R2 enforce checksums? (information only) [measured, real R2]

| Case | Reply | Stored? |
|---|---|---|
| no checksum (control) | 200 | yes |
| correct Content-MD5 | 200 | yes |
| wrong Content-MD5 | 400 `BadDigest` | no |
| malformed Content-MD5 | 400 `InvalidDigest` | no |
| correct `x-amz-checksum-sha256` | 200 | yes |
| wrong `x-amz-checksum-sha256` | 400 `BadDigest` | no |
| wrong MD5 + right SHA-256 together | 400 `InvalidRequest` ("only specify one non-default checksum") | no |

So R2 enforces both, and rejects them together. Send one: Content-MD5 (works with `rusty-s3`) or SHA-256, not both. A 400 `BadDigest` should map to a non-retryable or `Local`-class error, not `Server`; the sketch maps it to `Server` today, which the parent should refine.

## Proposed `Store` adapter (not applied; `rust/` is read-only for me)

Module `rust/crates/memory/src/s3.rs`, deps `rusty-s3 =0.11.0` (feature `rustcrypto`), `ureq =3.5.1` (feature `rustls`), `md-5`, `base64`. See `p2-s3-client/rs-probe/src/adapter.rs` for a working version:
- `ureq` agent: `http_status_as_error(false)`, `max_redirects(0)`, global timeout from DESIGN 7.3 (30 s + size at 1 MiB/s, floor 60 s).
- `put_if_absent`: sign `PutObject` with header `if-none-match: *`; `put_if_match`: `if-match: <etag>`; both add `content-md5` when enabled. `get`: signed `GetObject`, body limited.
- Mapping: 412 PreconditionFailed, 404 NotFound, 401/403 Auth, 429/503 Throttled (keep `Retry-After` for the shell), other status Server, connect/DNS/TLS/refused Offline, any other transport error on a write Ambiguous (on a read Offline), a 200 without ETag Ambiguous.
- 8 uploads: shell holds a pool of 8 worker threads, each with its own clone of the agent (ureq agents share a connection pool). `Store::Body` is `AsRef<[u8]>`, so no copy is needed (the probe's `Vec<u8>` is a simplification).
- Needs a Rust test in the workspace that drives `AlreadyOurs` after an `Ambiguous` reply, and per A6 the `known_ours` path.

## Cleanup and request counts

Prefix `rust-acceptance/p2-20261109T033619Z/` (the only prefix used). Cleanup tool (`cleanup/`, refuses any prefix not starting `rust-acceptance/p2-`) listed 55 objects, deleted 55, relisted: 0 remaining [measured, `results/cleanup.json`: 2 list + 55 delete requests]. Approximate other requests: conformance + lost-reply about 25 per client, P2b 14, RSS 48 PUTs. Not counted precisely by the probes. No other key was addressed.

## Commands (all exit 0 unless noted)

- `nix develop --max-jobs 2 --cores 2 --command cargo generate-lockfile` / `cargo fetch` in each probe.
- `CARGO_TARGET_DIR=<scratchpad>/p2-target CARGO_BUILD_JOBS=2 nix develop ... cargo build --release` (os-probe, rs-probe, baseline, cleanup). First rs build failed (no crypto backend: needs feature `rustcrypto`); first os build failed on an unstable `str::as_str` (fixed).
- `p2-{os,rs}-probe fake` (fault suite, no network); `p2-{os,rs}-probe r2 <prefix>/<x>`; `p2-rs-probe p2b <prefix>/p2b`; `p2-{os,rs}-probe rss <prefix>/rss-<x>` under `/usr/bin/time -l`; credentials loaded via `sh -c 'set -a; . ./.env; set +a; exec ...'`.
- `cargo deny --manifest-path Cargo.toml --config rust/deny.toml check licenses bans sources`: exit 4 for both (license `CDLA-Permissive-2.0`, see above).

## Not measured

- BC-01 and Linux; real R2 429/503 (none seen, `Retry-After` verified only on the fake server).
- Real-R2 resend by `object_store` after a lost reply (did not reproduce, see note).
- Idle connection reuse after server close (stale pooled connection write). `ureq` 3 should reconnect before sending, but a write to a half-closed pooled connection was not tested; if it fails it surfaces as `Ambiguous`, which is safe.
- MadSim itself; only trait-level fit by construction.
- Exact request totals.
