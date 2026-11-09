# 13. Commit protocol (slice 2)

Status: implemented against a deterministic in-memory fake store only. Not real R2, not
prototype P2, not the daemon. Dated 2026-11-09.

## Trait design (`crates/memory/src/environment.rs`)
- `Store` is a typed object-store seam: `put_if_absent` (If-None-Match: *), `put_if_match`
  (ETag fence), `get` (body + ETag). `Body: AsRef<[u8]>` is the authorized-body capability;
  there is no public raw-bytes constructor. The only bytes the driver wraps are its own
  metadata documents, via `Store::document`. Piece bodies arrive as `Upload<S::Body>`.
- `StoreError` is closed: `PreconditionFailed` (412), `NotFound`, `Offline`, `Server` (5xx),
  `Auth` (401/403), `Ambiguous` (lost reply). `StoreError::class()` maps to `ErrorClass`.
- `Clock` and `FileSystem` are kept unchanged.

## Protocol branches (`crates/memory/src/commit.rs`, DESIGN 7.3-7.4)
- Step 1, pieces: 412 is success (6.2); the object is read back and the descriptor is
  rewritten to the stored encoding via `Piece::adopt_stored` (R2).
- Step 2: manifest `put_if_absent`; 412 is success.
- Step 3: guarded `current.json` PUT (`If-Match`, or `If-None-Match: *` on first write);
  no retry inside the call.
- Step 4: `Committed { etag }` on success.
- Step 5, on any error: GET `current.json`, up to `GET_ATTEMPTS`. Names ours: `AlreadyOurs`.
  Names our previous: `BaseUnchanged`, retry from step 3 with the fresh ETag up to
  `REBASE_RETRIES` = 4, then `Failure::BaseUnchanged`. Anything else: `Foreign` (park, both
  hashes), or `NewerSchema` if the named manifest or the pointer has a newer schema.
  404: not final. A first write retries with If-None-Match: *. Under a fence it is
  `LandingUnknown`. Auth is `LandingUnknown(DestinationAuth)`.
- Step 6: `check_fence` enforces invariant 6 before every step 3 attempt.
- `disposition()` maps results onto `disposition::transition`.
- `recover_base` rebuilds the base from the store (9.3) with `recover_projection` and `fold`.

## Oracle independence
The reference model (`Reference`) holds files, per-source history and expected committed
bytes. It is updated from the steps alone and never reads the driver, planner or store.
Safety is checked by `read_chain`, which reads store contents directly and folds them with
memory-core. It checks object existence, descriptor against stored encoding
(`verify_encodings`), chain consistency, monotonic head and 1a (committed bytes are a prefix
of bytes the source held). Liveness runs after `Settle`.
Capture is modelled by a stand-in planner (full-prefix hash, 4-byte pieces) feeding
`manifest::build`. This is a model, not the real planner or shell.

## Property 4
Written in Hegel (`hegel::state_machine`, 40 steps) and in proptest-state-machine
(1..40 transitions). Steps: append, rewrite, truncate, remove-source, reconcile,
crash-before-put, crash-after-piece, crash-after-manifest, committed-reply-lost,
committed-then-5xx, restart, delete-index, go-offline, go-online, revoke/restore-token,
foreign-writer, settle.
Coverage guard: the run fails unless it produced multi-epoch, restart, crash-after-manifest,
lost-reply (AlreadyOurs) and delete-index. Negative controls: `guard_has_negative_controls`
and `oracle_detects_tampered_store_contents`.

## Commands
`cargo clippy -p memory --all-targets --locked -- -Dwarnings`: exit 0.
`HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory --test
commit_protocol --test harness -- --test-threads=2`: exit 0, 46 + 3 tests, about 27 s wall.

## Limitations
Not real R2 semantics, not P2 client behaviour, not the daemon. No planner, shell or index
persistence. The stand-in encoding is not zstd. Full daemon property 4 remains later.

## Parent correction 1 (2026-11-09): 5xx classification

DESIGN 11.2 lists only 429 and 503 as `Throttled`. The worker mapped every
`StoreError::Server` to `Throttled`. The parent added `StoreError::Throttled` (429/503 →
`ErrorClass::Throttled`) and reclassified `StoreError::Server` (any other 5xx) as
`ErrorClass::Offline`, which uses the exponential backoff. Four test expectations that
followed a `Server` injection were deliberately changed from `Throttled` to `Offline`,
and a `Throttled` row was added to `store_errors_classify_and_display`. No assertion was
removed. `BaseUnchanged` exhaustion still maps to `Retry` with class `Throttled` (worker
choice; ErrorClass has no closer class) and the parent records this as an open item.
Focused checks (pinned Nix, `target/w-parent`): `cargo fmt --check` 0; workspace clippy
`-Dwarnings` clean; `cargo test -p memory` at 2,000 cases: commit_protocol 46/46,
harness 3/3, hook 26/26 (2 ignored measurement tests).
