# Explicit candidate backend construction

**2026-10-24 — implemented/reviewed opt-in integration; fresh offline gates pass.** This is not
production R2 acceptance, installed-runtime activation, archive replacement or deletion
authorization. Follow the [capture contract](session-capture-contract.md) and
[handoff ledger](session-capture-handoff.md) before rollout. Current wire/reader/report
contracts are v2; the `capture-v1` command prefix/namespace is unchanged. Check
**622.40s**, test **460.36s**, nine unchanged **30s** fuzz targets (**285.31s** total),
and analytics **73 tests / 23.744s**, including **11 actual-process journeys**, pass.
See [final offline evidence](session-capture-offline-evidence.md) for exact scope.

## Selection and credentials

Existing private `capture-runtime/v1` configurations with `store` remain Local.
Alternatively, declare an exclusive `backend`; supplying both is rejected. For example,
this **synthetic, nonoperational** fragment illustrates named credential references:

```json
{
  "backend": {
    "kind": "r2",
    "endpoint": "https://00000000000000000000000000000000.r2.cloudflarestorage.com",
    "bucket": "synthetic-bucket",
    "access_key_env": "CAPTURE_R2_ACCESS_KEY_ID",
    "secret_key_env": "CAPTURE_R2_SECRET_ACCESS_KEY"
  }
}
```

This fragment is not a complete runtime configuration and must not be used as a
production command. It contains no credentials. The factory accepts a constrained
HTTPS R2 account hostname without userinfo, port, path, query or fragment. It has no
insecure-endpoint production override, default AWS credential chain, shared AWS file,
instance-metadata discovery, legacy collector config lookup or implicit activation.
Only the two explicitly named environment references are resolved. No configuration
inspection, hint, hook, status, replay, save or service plan needs those values.

The SDK uses explicit static credentials, region `auto`, path-style addressing, a
bounded HTTP transport, no redirects and no SDK retries. The durable coordinator owns
retry/backoff and destination pause. Tests inject a nonnetwork HTTP client beneath the
actual SDK rather than connecting to the synthetic hostname.

For an explicit Local backend the specification is
`{"kind":"local","local_dir":"/private/isolated/store"}`. Actual local store,
spool, policy and loaded configuration paths remain protected regardless of which
configuration form selected the backend.

## Capture independence and binding

Remote runtime startup does not require available credentials or a reachable archive.
Bounded delivery workers lazily construct the client, verify the configured archive
binding before namespace access/upload, and record destination pause on unavailable
credentials/authentication. Local source capture continues. The Local runtime retains
its existing archive-mismatch-before-queue startup check.

The CLI wrapper preserves destination authentication sentinels on Open, Put, current
read and CAS, and forwards durability fencing. A destination failure must not become an
optional-artifact failure or consume an individual session's poison budget. Genuine
local-store/durability causes must remain visible, including joined cancellation;
the fatal-cause repair and independent re-review are recorded in the handoff.

`capture-v1 init --config PRIVATE_CONFIG` explicitly initializes the selected archive;
`run` never initializes/resets it implicitly. `capture-v1 export --config PRIVATE_CONFIG`
uses the same authoritative reader as `--store`. Optional explicit archive/machine
flags must match the loaded configuration. Export validates its session identity and
remote key limit before resolving credentials. Configuration-backed work commands are
not silently reinterpreted: use the runtime's named work/inspection/replay routes.

## Logical keys and identities

R2's 1,024-byte logical-key limit is checked against the longest generated manifest
key, including UTF-8 identity hex expansion. Unsupported identities are rejected, not
truncated, renamed or assigned another root namespace. A previously accepted spool is
not deleted to resolve a delivery restriction.

Local's disjoint hashed physical paths for long logical keys solve filesystem component
limits only. They do not compact remote keys or establish that every maximal Local
identity fits R2. Publication/reader contracts remain v2; this factory does not provide
retired-schema compatibility or reset old bindings.

## Evidence and remaining gates

- Factory/config focused race/shuffle, coverage, lint and scoped CRAP passed.
- Actual SDK serialization/conditional requests, opaque ETags, lost ACK and exact
  writer → reader bytes are tested with injected nonnetwork peers.
- A mandatory actual-process SDK fixture deletes only its synthetic original sources
  and spool before authoritative export, then the real Python importer/event index /
  report consumes the result.
- Missing-credential runtime tests verify local capture with a destination pause.

Exact latest commands/results and review scope belong in the handoff, not a claim
that this document proves them. The latest independent spot-check review reports no
new confirmed code/wiring blocker; that is not exhaustive correctness. Actual conditional R2 semantics, installed native
agents, two-machine inventories/policies, measured operating resources, controlled
cutover and any reviewed retired-key deletion remain separate gates. No production
credential lookup or R2 request was performed to implement or document this factory.
