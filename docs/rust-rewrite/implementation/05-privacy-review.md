# Independent bounded privacy validation correction

> **Completed historical worker report; all paths released.** Progress/ownership
> statements below preserve the attempt ledger. The parent subsequently advanced
> the transform version to `memory.privacy/2`, refreshed the golden digest and passed
> the complete integrated gates. Current results and limits are in
> [parent review](02-parent-review.md) and the
> [active handoff](../../session-capture-handoff.md#current-session-checkpoint).

## Scope and live progress

Initially owned only new `rust/crates/memory-core/tests/privacy_coverage.rs` and this report. All five privacy implementation modules were read-only. Read AGENTS, DESIGN sections 0/8/15, prior worker's released `03-privacy-port.md`, current source/tests, integrated missing-lines evidence, relevant Go scrub/text/launch fixtures, and quality-gate instructions. No private files, runtime services, native/live/R2 operations, installs, aggregate coverage, threshold/exclusion edits, or workspace-wide modifying formatter.

Parent has now explicitly delegated **only `src/privacy/document.rs` additionally**, for a minimal scalar/object-leaf `Mode::Args` Go-parity correction. Other privacy source remains read-only. This report is progress evidence, not acceptance.

## Independent finding and correction in progress

Rust `redact` used `Mode::Args => false`, replacing every scalar args string (including object string leaves) wholesale. Go `internal/scrub/scrub.go` `redact`/`keepArgs` instead returns `Args([]string{s})[0]`: ordinary scalar strings remain, actual secret flags/inline JSON are sanitized. Existing array handling is separate and must remain unchanged.

Twelve adversarial fixture tests currently exist. First focused run: **101**, 11/12 passed; nested-array fixture had an incorrect oracle (Go preserves positional strings too), corrected by inspecting Go `redactAll`/`redactArgs`. Second focused run: **101**, 11/12 passed, now the real scalar secret-flag regression (`--token=fixture-scalar` should become `--token=[redacted]`, not wholesale `[redacted]`). Scoped clippy **0**, owned rustfmt check **0**. No production edit made before explicit delegation.

Fixtures cover unknown config/root/path/kind/owner, collector ancestor identity and descendant spelling, missing/mismatched rules, safe OtherKnown, unknown launch program/flags/missing values/exact-name duplicate env, Go fish nonassignments and incomplete values, invalid UTF-8 boundary, malformed URL forms and safe spelling/reference query preservation, selected nested strings vs nonsecret JSON primitives, odd args shapes/header references/inline JSON, deep JSON rejection, below-input-cap expansion rejection, and TOML arrays/native types.

## Commands so far

Pinned invocation prefix throughout:

```sh
nix develop --max-jobs 2 --cores 2 --command sh -c '
  cd rust
  export CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2
  # focused commands below; statuses saved independently
'
```

- `rustfmt --edition 2024 crates/memory-core/tests/privacy_coverage.rs` (only owned test modified).
- `cargo test --locked -p memory-core --test privacy_coverage -- --test-threads=2`: **101**, 11/12, real regression as above (second run).
- `cargo clippy --locked -p memory-core --lib --test privacy_coverage -- -Dwarnings`: **0**.
- `rustfmt --check --edition 2024 crates/memory-core/tests/privacy_coverage.rs`: **0**.
- Second Nix wrapper: **1**, preserving test=101/clippy=0/fmt=0.

## Coverage oracle limits and invariant guards

Parent supplied integrated baseline **1261/1363 = 92.5165%**, measured scaffold floor **98% immutable**. No coverage rerun here, so no measured line gain or percentage claim. The test table targets reachable missing policy/launch/text/URL/document behavior rather than manufacturing coverage.

Missing invariant-only defenses remain intact: binding's absent file identity/owner/Unknown profile are rejected before capability construction; authorize's final Deny/Unknown arms cannot be reached under identical bound context/facts; compiled constant regex failure arms cannot be forced through public input; document kind guard follows recognition, visitor `expecting` follows prevalidated syntax, TOML restore shape mismatches cannot arise from string-only replacement, and final serialized-size bound is dominated by conservative preflight bound. They should not be removed or exposed with test-only constructors. Deep TOML/parser limits need distinction from direct JSON-depth coverage; no assertion here claims a particular parser's internal rejection site.

## Evidence freshness

Prior `03-privacy-port.md` document.rs hash and source-tied tests/rules digest golden/integrated gate evidence will become **stale for corrected source**. Other source hashes remain unchanged unless parent changes them. Parent owns rerunning existing privacy property/parity/doctest and full canonical integrated gates, including coverage, canary/mutation/docs/lint. No old green is relabelled as proof for the new implementation.

## Ownership

Work in progress; no paths released yet. Final exact patch, source hashes, focused statuses and explicit release will be recorded below.

## Completed bounded correction and final focused evidence

Parent explicitly accepted the finding and delegated `document.rs`. Exact production patch (no other source changes by this worker):

```diff
-        Mode::Args => false,
+        // Go keepArgs applies the same flag/inline-document scrub to scalar
+        // leaves; positional strings are not a general-purpose DLP target.
+        Mode::Args => return args_scrub(&[value.to_owned()]).pop().unwrap_or_default(),
```

The one-element scrub preserves cardinality; no indexing, unsafe access, guard removal or broad rule rewrite. Array dispatch and treatment of non-string array members are unchanged. Regression now asserts ordinary scalar and object leaves retained, secret flag name retained with value redacted, inline JSON selected literals redacted, array positional strings/native primitives retained, header references retained and literal headers sanitized. These expectations follow Go's `keepArgs`/`Args`, not production rules queried by the oracle.

Final actual command:

```sh
nix develop --max-jobs 2 --cores 2 --command sh -c '
  cd rust
  export CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2
  rustfmt --edition 2024 crates/memory-core/tests/privacy_coverage.rs
  cargo test --locked -p memory-core --test privacy_coverage -- --test-threads=2
  test_status=$?
  cargo clippy --locked -p memory-core --lib --test privacy_coverage -- -Dwarnings
  clippy_status=$?
  rustfmt --check --edition 2024 crates/memory-core/src/privacy/document.rs crates/memory-core/tests/privacy_coverage.rs
  fmt_status=$?
  printf "test=%s clippy=%s fmt=%s\n" "$test_status" "$clippy_status" "$fmt_status"
  test "$test_status" -eq 0 && test "$clippy_status" -eq 0 && test "$fmt_status" -eq 0
'
```

Outcome: **wrapper 0; test=0 (12/12); clippy=0; fmt=0**. No whole-workspace formatter or aggregate test/coverage command. New-file `git diff --no-index --check /dev/null <file>` emitted no whitespace diagnostic (status 1 for differing files); not misreported as a clean tracked-diff status 0.

Baseline missing-line targets, by source inspection (NOT measured execution): document `156,181,227,280,343,413-416,423,457,494,510,520,524,542,580,603`; launch `37,40,58,90,93`; policy `252,289`; text `94,104-105,116,122,161`. These are roughly 30 candidate old-line locations, including helper/error boundaries; compiler line attribution and which parser rejects depth first can differ. No percentage improvement is claimed. Parent's unchanged canonical coverage oracle is authoritative. Invariant-only lines are intentionally left rather than chased.

Final dirty-checkout SHA-256:

```text
eaf5e098f9d341331d17337735b6f225cc5e4f0182f7079f171ba2d7b7a35a5e  rust/crates/memory-core/src/privacy/document.rs
5f45c21f0aa47be28fbdd97f4c04c8543f305ec4e6f29069483c2a8d14e99f8d  rust/crates/memory-core/tests/privacy_coverage.rs
```

Prior document hash was `c099183d46788fda20cb3fbd8e695e689486a95dc5214129cb0ce8a28b14e44f`. Other privacy source hashes matched prior worker's released report at inspection. Parent was notified that `TRANSFORM_VERSION` is in unassigned `mod.rs`; semantic-version and golden rules digest policy needs parent disposition. The rule table/digest is not changed by this minimal dispatch patch alone. Earlier tests remain historical observations, not corrected-source acceptance. Full existing privacy suite/properties/doctests and canonical integrated gates, schema/docs/mutation/canary/coverage, native/live/R2 and whole-system PUT/hint privacy were **not run by this worker**. Parent owns their rerun and acceptance; no deployment or release claim.

**Explicit ownership release:** `rust/crates/memory-core/src/privacy/document.rs`, `rust/crates/memory-core/tests/privacy_coverage.rs` and this report are now released to parent. No further edits by this worker without reassignment.
