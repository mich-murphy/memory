# Privacy port — staged implementation handoff

> **Completed historical worker report; all paths released.** The clippy/coverage
> blockers below were subsequently resolved in the parent's integrated run. Later
> Args parity correction and transform version `memory.privacy/2` supersede this
> worker's source snapshot. See [parent review](02-parent-review.md) and the
> [active handoff](../../session-capture-handoff.md#current-session-checkpoint).
> Focused test history is preserved, not relabelled as current sink/release acceptance.

Status: **owned source stabilized and released; canonical Nix default-feature focused tests and privacy doctests verified, including 2,000-case Hegel canary subproperty. Clippy remains blocked by unrelated domain lint. Not slice-1 acceptance.** Parent alone accepts requirements/implementation.

## Scope and ownership ledger

First action was a scope acknowledgement in this file, followed by an intermediate correction/check ledger. This worker modified only:

- `rust/crates/memory-core/src/privacy/{mod,document,names,text,launch}.rs`
- `rust/crates/memory-core/tests/privacy_port.rs` and, on bounded follow-up assignment, `privacy_property.rs`
- this report.

Parent/scaffold owns lib.rs, Cargo manifests/lock, domain types and all other paths. Parent's `tests/parent_privacy_review.rs` was read and exercised, never edited. No runtime I/O, locator work, installs, live state, credentials, transcripts, existing reports/experiments or DESIGN edits. No aggregate suite, coverage, native/live gates or host/service changes. Model/effort: parent-observed `openai/gpt-6.1-sol`, medium.

## Implemented boundary and integration signatures

`privacy/mod.rs:103-134` defines independent observational `ResolvedSourceFacts` and `PolicyContext`; parent domain integration remains outstanding. Public flow:

```text
admit(&PolicyContext, ResolvedSourceFacts)
  -> Admission::Allow(AdmittedSource) | Deny(Reason) | Unknown(UnknownReason)
transform(&AdmittedSource, &[u8]) -> Result<RedactedBytes, Reason>
authorize(AdmittedSource, RedactedBytes, &PolicyContext)
  -> Authorization::Allow(AuthorizedBody) | Deny(Reason) | Unknown(UnknownReason)
sanitize_launch(&[String], &[String]) -> SanitizedLaunch
```

- Only admission constructs `AdmittedSource`, only transform constructs `RedactedBytes`, only authorize constructs `AuthorizedBody`. Fields private; no Deserialize, Default or arbitrary From constructor. Five external compile-fail doctests cover source/redacted/authorized construction and deserialization (`mod.rs:5-24`).
- Source/profile/owner, all identity spellings, file/ancestor identities and context are explicitly length-prefixed and hashed into capability binding (`mod.rs:197-247`), not Debug serialization. Cross-source/profile reuse and changed context deny (`tests/privacy_port.rs:361`). Authorization consumes exact immutable transformed bytes and computes SHA-256 over **published** bytes, not original secret-bearing bytes (`mod.rs:326-351`). No free-standing upload token exists.
- The shell must actually validate resolution (including all-chain/cycle/255-link checks), admitted roots, file/ancestor identities and ownership. Supplying booleans/opaque IDs is not proof of a filesystem observation. Paths and facts cannot authorize arbitrary metadata; future sink/manifest/hint builders must preserve capability provenance. No upload/compression/runtime integration is implemented here.
- Rules unavailable/mismatched withholds protected/config/text profiles; independently admitted safe transcripts remain available. Invalid context/root/path/owner/kind still fails closed. Recognized config identity always forces structural handling, even if caller labels it Transcript.

## Go parity and review-correction uptake

Reference vectors are indexed in accepted `01-core-acceptance-map.md`, particularly sections 1/3. This implementation ports the known-file boundary, not retired opt-out or general transcript DLP.

| Behavior / correction | Implementation and synthetic evidence |
|---|---|
| All-chain basename/backup credential policy; collector spellings and file/ancestor identity aliases | `names.rs:10-72`; `mod.rs:250-295`; synthetic repeated uppercase/dated/editor/installer/archive suffixes, credential dirs, ordinary-name negative controls and collector aliases at `privacy_port.rs:263`. Exact primary `.claude.json` requires structural transform; backups cannot use exception. |
| All 11 structural JSON/TOML filenames, per-identity schema union, independent of collecting agent | Typed executable Family/Mode/rule arrays `document.rs:12-132`; all11 fixture across three agent spellings `privacy_port.rs:52`; union-only-encountered-schema fixture `:188`; every Claude/Codex rule family `:245`. No separate handwritten rule spec/parser to drift; exhaustive typed modes cannot silently skip unknown syntax. |
| Duplicate decoded keys at every depth, invalid Unicode, trailing documents, nonobject configs | Standard serde_json parser plus recursive duplicate-rejecting map visitor and explicit string decode `document.rs:179-229`; errors become fixed `MalformedProtected`, never parser text. Negative fixtures `privacy_port.rs:154`; parent independent duplicate fixture unchanged. |
| Equivalent decoded secret copies globally, without rewriting object keys | `document.rs:387-443`; escaped Unicode/newline/array/nested-copy/key-preservation fixture `privacy_port.rs:124`. Supported reference positives and literal negatives `:188`. Generated copy oracle independently asserts redacted selected/copy leaves AND exact unrelated safe value, 2,000 cases seed 20261107. |
| JSON numeric retention (self-review correction) | Go `internal/jsonx/jsonx.go:15-24` uses json.Number. RawValue-backed validated number leaves preserve huge integers, precision and exponents; no f64 roundtrip or arbitrary-precision reserved-key impersonation (`document.rs:134-229`; `privacy_port.rs:560`). Requires **serde_json raw_value feature**, requested from Cargo owner; no weakening duplicate checks. |
| Native TOML safe-type retention (parent correction) | Do not roundtrip native TOML through JSON serialization. Only transformed string leaves are restored into original typed TOML tree (`document.rs:231-249,324-350`); datetime/time/date/integer/float/bool retain native kinds. Datetime + secret test `privacy_port.rs:519` and parent independent datetime test pass. |
| URL malformed-port/escape/control/IPv6/userinfo handling (parent correction) | Reviewed `url::Url` validates URL/authority/port/IPv6; Go-specific strict escape/control checks and reference query decoding, then spelling-preserving edits avoid WHATWG normalization of safe host/port/path/escape fields (`document.rs:466-566`). Invalid port, invalid escape, IPv6/userinfo/query refs tests `privacy_port.rs:519`; explicit safe-spelling/network-path test `:588`; parent invalid-port/escape tests pass. Custom split parser was replaced, not patched as the validator. |
| Argv/env allowlist and protected inline argv | `launch.rs:26-121`; config args/header/inline JSON scrub `document.rs:568-608`; prompt/secret flags/exact env names/bounds fixtures `privacy_port.rs:477`; parent independent launch test passes. SanitizedLaunch has private fields. |
| Small text assignments, shell/fish/quotes/escapes/separators and source-code negatives | `text.rs`; Go-compatible 1 MiB/UTF-8/no-NUL scope, all-identity source-extension exception, no transcript text scanning. Positive/negative fixture `privacy_port.rs:399`. This remains a bounded known-asset guard, not DLP. |
| Protected allocation ordering (parent correction) | Recognized-input bound checked **before clone** (`mod.rs:301-319`); checked conservative tree/escaping/path bound before output serialization and final serialized cap (`document.rs:279-321`). Oversize fixture `privacy_port.rs:465`. No raw fallback. |
| Semantic rule version/digest | `TRANSFORM_VERSION = memory.privacy/1`; digest walks the same typed rules/names consumed by transform (`mod.rs:190-195`; `document.rs:118-132`), golden fixture `privacy_port.rs:577`. Parser/reference/copy/text/URL logic changes require explicit semantic version bump. |

One own test initially failed for a **bad oracle**: `CANARY_API` matched Go's explicitly permitted Pi environment-variable-name syntax. Fixture changed to literal `canary-api`, while an explicit `OPENAI_API_KEY` retention negative control remains. No allowlist was relaxed to pass it.

## Bounds and remaining acceptance limits

- **8 MiB whole protected-document input/output limit is provisional**, fail-closed, not a measured R1/capacity/memory acceptance. Conservative expansion bound can withhold below that size. Parsing/tree/replacement working memory has not been measured; do not infer daemon peak RSS from the output bound.
- JSON depth is bounded at 128 in the recursive strict tree; library TOML parser and the transform tree also reject unsafe/unrepresentable inputs. Any withholding is visible to future caller, never raw fallback. Explicitly declared coverage for valid-but-withheld configurations remains future R1 work.
- Unchanged documents retain exact input bytes; rewritten JSON/TOML may canonicalize layout, but safe value content/types and object keys are retained. Selected literal/copy strings are intentionally transformed. Transcript bytes are unchanged, including invalid semantic Unicode/partial lines.
- No real resolved filesystem facts, live token exclusion, before-read instrumentation, compressed sink inspection, hint serialization, exhaustive source-kind integration, runtime mutation oracle or DESIGN property 5 is proven here. The generated copy test is a focused independent oracle, **not** whole-system P5. P5/coverage/P1/P5-prototype and canonical integrated gates still block slice acceptance.

## Exact validation ledger

Shared integration was changing during this work. No other owner's source was changed to obtain green.

### Earlier attempts, retained honestly

1. `nix develop --max-jobs 2 --cores 2 -c bash -c 'cd rust && CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2 cargo test -p memory-core --test privacy_port --locked -- --test-threads=2'` → **101**, unrelated domain schemars derives; routed to parent.
2. Same focused test after domain repair → **101**, unresolved url before dependency wiring.
3. Direct realized pinned official toolchain, focused privacy + parent review tests with `--locked` → **101**, parent-owned lock update required.
4. Feature-enabled tests → **101**, 11 own passed/one bad-canary-oracle failed; all 6 parent tests passed. Corrected oracle as described above.
5. Feature-enabled tests then passed; initial clippy → **101**, seven owned collapsible-if warnings plus domain chunks_exact warning. All seven owned warnings fixed without suppressions. Subsequent clippy emitted only domain warning.

### Frozen-source focused commands

Default Nix shell was rebuilding the coverage package; parent explicitly authorized the already-realized pinned official closure for focused checks. Environment provenance:

```sh
export PATH=/nix/store/0nmx02v6h7c792800l861dkj17df9ifm-rust-minimal-1.99.0/bin:$PATH
# rustc 1.99.0 (b940084d7 2026-09-28)
# cargo 1.99.0 (5f94df478 2026-08-27)
cd rust
export CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2
PROPTEST_CASES=2000 PROPTEST_RNG_SEED=20261107 cargo test -p memory-core --features serde_json/raw_value --test privacy_port --test parent_privacy_review --locked -- --test-threads=2
cargo clippy -p memory-core --features serde_json/raw_value --lib --test privacy_port --test parent_privacy_review --locked -- -Dwarnings
cargo test -p memory-core --features serde_json/raw_value --doc privacy --locked -- --test-threads=2
cargo fmt --check
```

| Frozen-source check | Exact exit / outcome |
|---|---|
| Focused tests | **0**, own 14/14 + unchanged parent 6/6; generated oracle 2,000 cases, seed 20261107 |
| Focused clippy, -Dwarnings | **101**, only unrelated `domain.rs:226` chunks_exact_to_as_chunks; no privacy warning |
| Privacy compile-fail doctests | **0**, 5/5 |
| Read-only workspace cargo fmt --check | **1**, unrelated `tests/core_mechanics.rs:72`; earlier invocation before that concurrent edit was 0 |
| Owned-files-only rustfmt --check --edition 2024 (five module files + privacy_port.rs) | **0** |
| Manual pure-boundary review + grep for filesystem/network/process/unsafe/runtime/printing | No matches in owned module; not a compiler-enforced std I/O ban claim |

**Historical integration blockers, superseded where noted below:** raw_value default wiring was pending during these override checks; domain clippy and mechanics formatting were independently owned. CLI feature-enabled results above were not normal-default or canonical-Nix passes. The bounded follow-up below now supplies actual default-feature canonical-Nix evidence; it does not retroactively relabel earlier failures. No full suites or live gates run.

## Frozen source identity (SHA-256, dirty checkout not a commit)

```text
c099183d46788fda20cb3fbd8e695e689486a95dc5214129cb0ce8a28b14e44f  src/privacy/document.rs
ad7635658cbb4125454250f09891590cba8b99256d05b7578b2eeb12a0aaa65a  src/privacy/launch.rs
1161fd48701a0eff8b5e6fa3173bdc906b9b7e8946b42c4b79770dcf8ee393cb  src/privacy/mod.rs
551f956a46382b8c17f36bfb82211a6f35d13d98d2c807f69c104b0bfa609b57  src/privacy/names.rs
a4a60871ca14308e5633d23f013a53fc8337deecaa23d957bd6b1658933635e0  src/privacy/text.rs
8da5abf2f00a72ce4720d60d3fd66b2993bded8e8681e882c08b5ba7300a6575  tests/privacy_port.rs
```

Paths above relative to `rust/crates/memory-core/`. Context snapshot (not worker-owned):

```text
30a0ad68de1c9cbbeb9e0808b6e397958546c17eadd18b285ead1a1b9b8455df  DESIGN.md
cf61174fe932bc846cb2c76442902f1f961ff215fafa4e2ff8ad9e8271f9e5bf  rust/Cargo.toml
335e489dca6ce321d4c423c46b0580765f60e4ed76bac5f6a2e07a79801fa24f  rust/Cargo.lock
bbe0533000e51ce4ce6f99c0f3d34abf0d6498d87a0dfa7140ab9f67a8fb0149  rust/crates/memory-core/Cargo.toml
11dac4fe29919db813d695c7b405d5617d586731cef29c07c144b6f4fa36d3e9  rust/crates/memory-core/src/lib.rs
593f77558ee195c5ab639c36237dea4c2fc6954f55fb2e3124728fc0aacc5430  rust/crates/memory-core/tests/parent_privacy_review.rs
```

## Bounded generated-property follow-up — current evidence

Parent resumed the same ownership specifically to add the missing generated privacy subproperty and rerun focused default checks after Cargo-owner wiring. No changes to the five stabilized implementation files or privacy_port.rs were needed; their source hashes above are unchanged. New `tests/privacy_property.rs` has an independently authored fixture table, not an oracle that consults production rules to select expected protected fields.

- Eleven protected basenames have explicit selected literal + equivalent copy + safe-marker templates (`privacy_property.rs:11-56`). Each generated example samples one of **16 cases** and generates two u64 values used in lowercase canary/safe values, owner identity and alias selection (`:95-228,230-236`).
- The five other cases exercise an intermediate credential alias (five normalized backup/directory variants), Unknown profile denial, excluded argv/env canaries with allowed model metadata, raw transcript canary preservation (including invalid UTF-8), and an admitted known-file safe/raw negative control. Explicit deny outcomes cannot become transformed outputs.
- `independent_inventory_exercises_every_family_and_credential_variant` (`:238`) runs all 16 cases across five salts, independent of Hegel sampling. This guarantees deterministic fixture coverage, **not** that every generated family was sampled on any arbitrary seed.
- Claims are limited to pure **AuthorizedBody bytes and SanitizedLaunch fields**. No actual PUT/hint sinks, resolver, arbitrary symlink graphs, runtime policy-change schedules or full source-kind inventory is generated. One representative protected field per basename is generated; deterministic Go-parity fixtures cover other rules/malformed/reference edges. This closes the requested generated pure-core prerequisite, not whole-system DESIGN property 5 or slice acceptance.

Canonical command actually run, no CLI feature override:

```sh
nix develop --max-jobs 2 --cores 2 --command sh -c '
  cd rust
  export CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2
  export HEGEL_TEST_CASES=2000 HEGEL_SEED=20261107 HEGEL_PRINT_BLOB=true
  export PROPTEST_CASES=2000 PROPTEST_RNG_SEED=20261107
  cargo test --locked -p memory-core --test privacy_port --test privacy_property --test parent_privacy_review -- --test-threads=2
  cargo test --locked -p memory-core --doc privacy -- --test-threads=2
  cargo clippy --locked -p memory-core --lib --test privacy_port --test privacy_property -- -Dwarnings
'
```

The actual wrapper saved each status before continuing (rather than masking an earlier failure with a later success):

| Current focused check | Exact exit / outcome |
|---|---|
| Default-feature focused tests | **0**, privacy_port 14/14, privacy_property 2/2, unchanged parent_privacy_review 6/6 |
| Generated runs | Hegel **2,000 cases**, seed **20261107**; existing proptest copy oracle **2,000 cases**, seed **20261107** |
| Default-feature privacy compile-fail doctests | **0**, 5/5 |
| Default-feature focused clippy -Dwarnings | **101**, only unrelated `domain.rs:226` chunks_exact_to_as_chunks; routed to parent, no owner-boundary edit or lint suppression |
| Owned-file rustfmt --check --edition 2024, all five module files + both privacy tests | **0** |
| New test diff whitespace review (`git diff --no-index --check /dev/null .../privacy_property.rs`) | **0** |

The Nix wrapper returned **1** because the preserved clippy status was nonzero. Parent owns final unchanged aggregate gate after owners stabilize. No whole-workspace modifying formatter was used; no repeat Nix rebuild/polling loop. Earlier direct-closure/feature-override evidence is superseded for test/doctest compilation by these canonical default results, not deleted. Earlier unrelated workspace-format failure remains historical, not a current successful fmt claim.

Current additional/context SHA-256:

```text
2d1b29882dcbf4090402f92fc8985fb381e2a9414299c996197f78e105d38edf  rust/crates/memory-core/tests/privacy_property.rs
50b9ce468035e12a2b977ebd4b4f949d9c63d69618c22dbb9acf82e7f6817b45  rust/Cargo.toml
335e489dca6ce321d4c423c46b0580765f60e4ed76bac5f6a2e07a79801fa24f  rust/Cargo.lock
bbe0533000e51ce4ce6f99c0f3d34abf0d6498d87a0dfa7140ab9f67a8fb0149  rust/crates/memory-core/Cargo.toml
```

## Ownership release

**All owned privacy module paths, both privacy test files and this report are explicitly released to parent now.** Implementation is staged for independent review/integration, not accepted or deployed. Parent can return corrections by assigning these paths again. Frozen module/fixture hashes are unchanged; the bounded added test's final hash is recorded above. Remaining domain lint, aggregate/coverage/P5 sink integration, R1 and provisional bound acceptance must not be reported green.
