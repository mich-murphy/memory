# Offline SDK publication transport

`Backend` implements `internal/publication.Backend` using an explicitly injected
`*s3.Client`, bucket, and positive timeout. It does not discover configuration or
credentials. `internal/capturebackend` now constructs it for explicit opt-in
candidate configuration; `capture-v1 init/export --config` and runtime delivery are
wired to that factory. Remote runtime construction/binding is lazy so missing
credentials or network cannot prevent local raw capture. The legacy default store
transport remains unchanged. This is implemented wiring, **not deployment or actual
R2 endpoint acceptance**; see [backend configuration](../../docs/session-capture-backends.md).

## Implemented behavior

- Only canonical `capture-v1/archive.json` and the writer's hex-encoded
  archive/machine/agent/session namespaces are accepted. Session references are
  `current.json`; payloads are `manifests/<lowercase-sha256>.json` and
  `objects/<lowercase-sha256>.zst`. Keys over S3's 1024-byte limit, traversal,
  alternate prefixes, encoded separators, and noncanonical hex are rejected.
- `Open` streams a GET with a timeout that remains active until the caller closes
  the stream. Callers **must close** even after EOF or a read error. Declared
  lengths are checked; truncated/excess bodies fail rather than becoming a
  successful read. Object length is capped at **1 GiB**, metadata at
  `publication.MaxMetadata` (8 MiB). S3 GETs must provide a Content-Length.
- `PutFile` accepts only regular local payload files and uses a seekable bounded
  file section with one native create-only `PutObject` (`If-None-Match: *`) and
  explicit Content-Length. SDK retries are disabled for this write. Payloads are
  capped at **1 GiB**, manifests at 8 MiB. No multipart operation is used. FIFO
  opens are nonblocking so invalid file types cannot wait for a peer. File reads
  and SDK checksum/signing passes stream; the library does not allocate the file
  contents in memory. Before any network request, the file's streamed SHA-256
  must match its content-addressed key. A local mismatch returns `ErrConflict`.
  On conditional collision or ambiguous write failure, GET reconciliation streams
  the existing body and accepts only the exact expected length and SHA-256.
  Different/corrupt existing bytes return `ErrConflict`; nothing is overwritten
  or deleted. Every reconciliation response body is closed, including failures.
- `Current` accepts only reference keys, reads at most 8 MiB + one overflow byte,
  and returns the exact opaque ETag from that same GET. It does not infer an ETag
  from a body hash, parse multipart suffixes, or do a separate HEAD.
- `CompareAndSwap` accepts only reference keys and bounded metadata. It sends
  exactly one conditional `PutObject`: `If-None-Match: *` for an empty token, or
  `If-Match: <exact ETag>` for replacement. The archive descriptor is create-only.
  Automatic SDK retries are disabled for this operation, even if the injected
  client enables them. No HEAD-then-PUT, copy-source condition, or multipart
  completion is used to approximate CAS.
- HTTP 404 maps to `publication.ErrNotFound`; 412 and 409 map to
  `publication.ErrConflict`; 401/403 map to this package's
  `ErrDestinationPaused`. Context cancellation/deadlines remain identifiable.
  Other SDK/file errors are sanitized: no raw endpoint, key, path, response body,
  credential, or transcript is included in returned diagnostics.

The caller owns immutable spool files throughout `PutFile`, including verification
and SDK checksum/signing passes. A file section cannot read growth past its
captured size, and truncation causes an error. Same-size concurrent file mutation
is not a supported snapshot boundary. Local spool filesystem operations assume a
responsive trusted local filesystem; hashing checks context between bounded reads.
The single operation deadline covers verification, PUT, and any reconciliation GET,
including its full response stream; reconciliation does not renew the budget.

A successful CAS does **not** establish source ordering or an atomic multi-object
transaction. A failed/expired request can still have committed remotely. Immutable
PUT ambiguity is reconciled only by exact streamed read-back. CAS ambiguity remains
an error here: stable publication identity, guarded current read-back, stale-job
decisions, dependency verification, and local acknowledgement/reclamation belong to
the publication/queue layers. Missing read-back after a failed immutable PUT retains
the write error, not success or permission to overwrite.

## Offline evidence and remaining acceptance

The final integrated candidate passes full unchanged check/test/fuzz and eleven
actual-process consumer journeys, including the actual SDK factory against an injected
HTTPS peer. See [current evidence](../../docs/session-capture-offline-evidence.md).
The dated package checks below are preserved narrower checkpoints, not claims that
integration or full gates are still pending.

**Live R2 conditional-write semantics remain UNVERIFIED.** These tests exercise
actual AWS SDK HTTP requests against an in-process, minimal S3 protocol peer,
not R2 and not a claim that an emulator establishes R2 semantics. They use static
synthetic credentials and synthetic bytes only, with no production configuration.

The focused suite covers native conditional headers, stale tokens, concurrent
first creation and replacement, opaque ETags, archive binding, typed 404/409/412
and auth failures, safe diagnostics, namespace/method confinement, metadata/file
limits, source/response length mismatch, full-stream timeout/cancellation, close
cancellation, and lost acknowledgement without automatic CAS replay. The
publication-layer end-to-end reconciliation journey is owned by its separate
suite, not established here.

Checkpoint (2026-10-23):

```sh
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  CGO_ENABLED=1 sh -c 'gofmt -w internal/publicationr2/*.go && go test -race -shuffle=on ./internal/publicationr2 -count=1 -timeout=60s'
```

Passed (21 top-level tests; run 2.076s). Repeated race/shuffle validation also
passed (three runs, 3.756s):

```sh
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  CGO_ENABLED=1 go test -race -shuffle=on ./internal/publicationr2 -count=3 -timeout=60s
```

`git diff --check -- internal/publicationr2` also passed. An earlier run exposed a
test-peer shutdown hang when a simulated stalled PUT did not drain its request body; the
peer was corrected without relaxing the timeout and the suite passed. This is
not a full lint, repository, native, live, writer/reader/importer/report, or
release acceptance result. No live R2, deployment, installation, host changes,
source deletion, or remote deletion was performed.

Historical owner handoff: package integration and full lint/consumer gates were
subsequently completed in the opt-in candidate. Current continuation lives in
[the handoff](../../docs/session-capture-handoff.md). Isolated real R2/native /
two-machine acceptance remains outstanding; injected SDK tests do not authorize
production activation or establish real endpoint semantics.

## Immutable create correction (2026-10-23)

Replaced unconditional immutable payload PUTs with the verified native conditional
create and exact-byte reconciliation above. The actual SDK offline tests now cover
same-file duplicate success, different-file/same-key rejection before networking,
existing corruption (same length and different length) without overwrite/deletion,
and lost immutable PUT acknowledgement followed by exact or corrupt read-back.
The lost-ack case enables client retries and asserts exactly one conditional PUT
followed by one GET; no HEAD, overwrite, delete, multipart, or replay is allowed.
Existing upload/truncation/deadline fixtures derive keys from their actual payloads.
Native reference CAS behavior is unchanged.

Focused pinned checks passed:

```sh
nix develop --command golangci-lint fmt internal/publicationr2/*.go
nix develop --command golangci-lint run ./internal/publicationr2/...
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  CGO_ENABLED=1 go test -race -shuffle=on ./internal/publicationr2 -count=1 -timeout=60s
```

Lint: zero issues. Focused race/shuffle tests: passed (2.184s).
No full repository tests/check, native/live gates, or production R2 calls were run.
Parent owns the final integration gate and handoff ledger; live R2 remains unverified.
