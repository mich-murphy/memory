# Opt-in live R2 acceptance

`internal/store/TestR2LiveAcceptance` verifies the actual R2 S3 protocol and the normal
v3 `syncer.New` engine. It is separate from the older `TestR2Live`: that test exercises
legacy store operations and uses inline cleanup, not the catalog/recovery acceptance
or failure-safe cleanup below. Do not use the older test as final v3 evidence.

## Authorisation and commands

First review the new test, namespace adapter and test-only multipart accessor. The
operator must explicitly authorise live writes and deletes. Credentials are supplied
**only through the child process environment**, with these four nonempty variables:

- `R2_ENDPOINT`
- `R2_BUCKET`
- `R2_ACCESS_KEY_ID`
- `R2_SECRET_ACCESS_KEY`

If an operator keeps them in a private `0600`, git-ignored `.env`, their trusted launcher
must inject them safely. The test does **not** open, source, print or parse `.env`.
Do not paste values into commands, test output, reports or shell history; do not dump
config structs or environment variables. Validate the endpoint as an operator before
running: the live test uses the configured `store.NewR2`, not a fake server.

After review/authorisation, run exactly this command in the safely injected environment:

```sh
AGENT_SESSIONS_TEST_R2=1 go test -race -count=1 -v -timeout=20m \
  ./internal/store -run '^TestR2LiveAcceptance$'
```

Do **not** run `./...`, `-run TestR2Live`, or a full gate with the opt-in flag set: those
also select the legacy live test. `-count=1` prevents cached acceptance evidence. The
20-minute process timeout exceeds the test's 12-minute work deadline and its two
independent 2-minute cleanup deadlines. Do not interrupt/kill a running acceptance
unless necessary: Go cannot run cleanup after SIGKILL or a process-level timeout.

Normal offline checks (explicitly disable inherited opt-in):

```sh
AGENT_SESSIONS_TEST_R2=0 go test -race -count=1 -timeout=3m \
  ./internal/store \
  -run '^(TestNamespace.*|TestR2AcceptanceFake|TestR2TestMultipart.*|TestR2LiveAcceptance)$'
```

The live test skips without the exact flag `AGENT_SESSIONS_TEST_R2=1`. No production
CLI option, archive format, bucket-global identity or existing archive is changed.

## Executed live result

With the operator-provided credentials, the exact race-enabled acceptance command
passed in **85.33 seconds**, then passed again on the final test-only layout in
**81.24 seconds**. The complete offline `make check` also passed (**88.4% coverage**),
with live opt-in explicitly disabled. An earlier read-only random-prefix LIST also authenticated
successfully without writing or deleting anything. The live run confirmed:

- Conditional-create preservation, exact metadata and streaming reads, and real pagination.
- **11,534,336 bytes** uploaded through three multipart parts and downloaded byte-for-byte.
- Four acknowledged content generations plus the retained deletion generation,
  restored after source loss and **three cold recoveries**.
- Excluded data withheld before capture while an allowed session was newly captured
  in both control passes; both allowed generations and prior private history restored.
- Cleanup deleted **46 owned test objects**, then verified **zero remaining objects
  and zero multipart uploads**. No existing archive namespace was accessed.

Sanitised private development evidence: `/tmp/memory-r2-live-acceptance.log` and
`/tmp/memory-r2-final-live.log`; final full gate: `/tmp/memory-r2-final-check.log`.
Credentials were injected only into the test process, never printed or copied into
proof artifacts. The operator's `.env` is mode `0600` and Git-ignored. This result
establishes the bounded backend-plus-engine scenarios below, **not native CLI/hook
execution directly to R2**, full-bucket scale, production lifecycle policy or every
network failure mode.

## Safety boundary

The unexported `namespace` helper in `internal/store/namespace_test.go`
(`package store_test`) generates a new cryptographically random nonce and confines
**every** Store method (`List`, `Head`, `OpenReader`, `Create`, `Put`, `PutFile`, `Get`,
`Copy` on both sides, and `Delete`) to:

```
_agent-sessions/test/<nonce>/
```

This is explicitly a **test-boundary namespace adapter**, not a production prefix
configuration or a second implementation of the archive protocol. The engine uses
normal logical v3 keys/references, and the adapter prepends the private prefix on
transport and validates returned physical keys before stripping it. Thus even its
logical `v3/store.json` and content-addressed shared blobs are private copies under
the nonce, never the bucket-global objects. The disposable SQLite binding still
uses the configured destination; it is not evidence for a production prefixed store.

Invalid logical keys, absolute paths, parent escapes, duplicate path separators,
foreign test prefixes and invalid UTF-8 are rejected before calling the underlying
store. HEAD must return exactly the requested physical key; all LIST keys must be
valid and within the exact requested prefix. Continuation tokens remain opaque and
are accepted only after being issued for the same logical prefix. The adapter caps
LIST requests at two objects to force real R2 pagination, including engine recovery.
There is no bucket-wide object listing.

Cleanup is registered before preflight. Both the exact object prefix and multipart
prefix must be empty before the test considers them owned or permits mutation. A
collision or failed preflight causes failure **without deletion of unowned data**.
On any ordinary test failure after ownership, cleanup runs with fresh contexts that
ignore cancellation of the work context:

1. `store.R2TestMultipart` (a `_test.go` export, not a production API) lists multipart
   uploads with the **exact Prefix**, validates every key/ID before aborting any,
   aborts only observed owned IDs, then re-lists to verify no owned uploads remain.
2. `namespace.clean` enumerates only the owned object prefix, validates all returned
   keys before any delete, deletes those exact wrapped keys, then re-lists to verify
   the prefix is empty. A multipart failure does not suppress object cleanup; each
   has its own bounded context.

No cleanup can reach global `v3/store.json`, another machine/archive, legacy keys or
shared production blobs. Cleanup failures fail the test, not silently pass. Record
the generated prefix from the proof log if interrupted cleanup needs operator review;
never broaden cleanup to `_agent-sessions/test/` or the whole bucket.

## Assertions and proof

The live run asserts, without relaxing behaviour for R2 differences:

- Conditional `Create` of existing different bytes returns `ErrExists` and preserves
  the original; `Head` returns exact size/metadata and logical key; `Get` and
  `OpenReader` return exact bytes.
- Real paginated LIST returns the exact expected keys, plus cross-prefix/escape
  rejection and an actual guarded `Copy` round trip.
- An 11 MiB cryptorandom synthetic source uploads and downloads exactly. The existing
  `store.SetMultipart` test-only setter forces a 1 MiB threshold and 5 MiB parts,
  so this is **three multipart parts**, not the production 64 MiB single-PUT path.
  Both body SHA-256 and HEAD metadata/size must match, and no multipart uploads remain.
- The normal v3 engine captures `A -> AB -> C -> empty`. An independent existing
  `testenv.Audit` checks physical canonical descriptors, matching plan/commit bytes,
  unique parent chains and encoded/decoded dependency checksums after every capture.
- Complete source and entire SQLite-directory loss recovers the empty current and
  restores every acknowledged generation by its immutable descriptor ID. Complete
  synthetic discovery commits a delete generation (retaining empty content), and
  two further whole-cache losses/recoveries retain the tombstone current and restore
  all five generations. No weaker manifest-only reconstruction oracle is used.
- A synthetic private session has an acknowledged pre-opt-out generation. Its opt-out
  is durably published, then all SQLite/local exclusion state is lost. Cold recovery
  suppresses the excluded source before discovery/capture, retains/restores the old
  acknowledged data, and a physical archive scan confirms the synthetic withheld
  sentinel never reached any object, including decompressed pieces. A distinct,
  unexcluded synthetic session in the same root must be discovered, captured and
  restored with exact bytes in both passes. Its bytes change after SQLite/local
  exclusion loss, so the second pass must acknowledge a new generation and restore
  both control generations; blanket empty discovery cannot satisfy the oracle.

All sources, roots, HOME, state and config paths are private synthetic fixtures. No
launch or cwd is provided, so no snapshots, managed settings or ancestor instruction
provenance is collected. Tests never use the operator's agent sources or config.

Logs contain only generated prefix, counts, booleans, byte lengths, SHA-256 identities
and action summaries. SDK errors are withheld at the adapter and assertion boundaries;
no endpoint, bucket, credential values, raw bodies or opaque tokens are printed. Keep
an operator-side sanitising log wrapper as defence in depth. Proof includes explicit
`remaining_uploads=0` and `remaining_objects=0`; a PASS without successful cleanup is
not possible under ordinary test execution.

`TestR2AcceptanceFake` runs the same archive and store byte/checksum assertions on
in-process gofakes3. Its empty multipart preflight is omitted because gofakes3 1.2.0
returns `NoSuchUpload` until its first multipart initiation. The **live** test does
not accept that error. Separate ordinary `TestR2TestMultipart` synthetic HTTP tests
exercise scoped pagination, foreign/parent-escape response rejection before abort,
abort failure, empty verification, invalid input and error-text withholding.
`TestNamespace*` tests cover confinement of every method, malicious HEAD/LIST replies,
opaque token scope, and preservation of a pre-existing unprefixed archive on cleanup.

This is an acceptance probe, not throughput benchmarking, distributed-writer testing,
lifecycle-policy inspection or proof against operator deletion. See
[storage-layout.md](storage-layout.md), [archive-design.md](archive-design.md) and
[quality-gates.md](quality-gates.md) for those contracts and the full normal gates.
