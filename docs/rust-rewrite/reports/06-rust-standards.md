# Rust standards and ecosystem review

Reviewer role: Rust standards and ecosystem. Read-only. Date of lookups: 2026-11-06.

Labels used throughout. **[read]** means I read the file in this session. **[looked up]** means I fetched it from crates.io, GitHub or vendor docs on 2026-11-06. **[memory]** means unverified recall. **[inferred]** and **[guess]** as stated.

## Findings first

1. BeautifulRust is not a lint framework and not a hook system. It is an agent-skills plugin (three Markdown skills) plus one drop-in `[lints]` table and a `clippy.toml`. It ships no `rustfmt.toml`, no `deny.toml`, no CI for the user's code, no pre-commit hooks, no edit-time controls, and its MCP server is built but "not deployed yet". [read: README.md, docs/mcp-server-plan.md:3, .github/workflows/*.yml, .claude-plugin/plugin.json]
2. The only hard mechanism worth taking is the lint table, and about half of it should be adapted or dropped. Two of its ideas are strong and match the Go gate philosophy: every suppression needs a reason and goes stale loudly (`#[expect]` plus `allow_attributes_without_reason`), and the lint table is proven by a crate of deliberate violations (`scripts/check-lints.sh`). The Go repo has the equivalent of the first (`nolintlint`, `.golangci.yml`) but not the second.
3. The "gate" content for cargo-deny, audit, udeps, Miri, fuzz, semver-checks, hack is only prose recommendations in the digests ("Audit with `cargo tree --duplicates`, `cargo deny`, `cargo udeps`, and `cargo audit`", skills/rust-best-practices/SKILL.md:422). No configuration is provided. I wrote the configuration below from the tools' own templates and memory.
4. Proposed Rust gate set (section 3) is stronger than the Go set in three places that matter for a durable daemon: `unsafe_code = "forbid"` in all crates, a core crate that cannot depend on I/O crates (cargo-deny `bans` plus workspace layout), and exhaustive-match and `non_exhaustive` policy on wire enums. It is weaker or equal elsewhere until the crates exist (mutation testing and CRAP-style gates need work).
5. Crate picks (section 4). SQLite: `rusqlite` with `bundled`, sync, one connection per owner. Storage client: `aws-sdk-s3` is the lowest-risk match to the Go code because the Go code already depends on `IfNoneMatch: "*"`, `IfMatch: <etag>` and `CopySourceIfMatch` (internal/publicationr2/backend.go:256,379-381; internal/store/r2.go:215). `object_store` is a credible alternative but hides CopySourceIfMatch and exposes only `PutMode::Create/Update`. Runtime: std threads plus blocking I/O is defensible for the daemon, but `aws-sdk-s3` forces tokio inside the storage adapter. That is the real decision (section 4.3).
6. The Go project builds with `CGO_ENABLED=0` and `modernc.org/sqlite` (go.mod:22, flake.nix `env.CGO_ENABLED = 0`). `rusqlite` bundled needs a C compiler for every target. That is the single largest cross-compilation cost of the Rust rewrite. [read; inferred for Rust cost]

## 1. What BeautifulRust is, exactly

### 1.1 Structure [read]

- `skills/rust-best-practices/SKILL.md` (511 lines) plus `references/` digests of Google Rust Style Guide, Rust API Guidelines, Effective Rust, Rust Design Patterns, Microsoft Pragmatic Rust Guidelines, and `bevy.md`.
- `skills/rust-review/SKILL.md` (181 lines) and `skills/rust-port/SKILL.md` (144 lines).
- `lints/lints.toml`, `lints/clippy.toml`, `lints/check/` (crate with one deliberate violation per lint), `scripts/check-lints.sh`.
- `scripts/check-skill.py`, `check-examples.py` (compiles every Rust block in docs with `cargo check`), `package-openai-plugin.py`, `build-mcp-data.py`.
- `.claude-plugin/plugin.json` (name `beautiful-rust`, version 1.5.0, MIT), `marketplace.json`, root `plugin.json` for Codex/ChatGPT.
- `mcp/` Cloudflare Worker in TypeScript (`search_guidance`, `explain_lint`, crate lookups). Built and tested locally, not deployed (docs/mcp-server-plan.md:3).
- `.github/workflows/`: `check` job (python skill checker), `lints` job (`dtolnay/rust-toolchain@stable` plus clippy, runs `scripts/check-lints.sh`), `examples` job, `mcp` job, plus a "notify website" workflow. No `cargo fmt`, `cargo deny` or `cargo test` for any user code, because there is no user code.
- Evidence of effect is thin: the README reports 29/30 vs 24/30 checks and a cross-agent benchmark of one run per configuration graded by a Claude model, and says so itself (README "Across agents"). Treat as marketing-grade, not evidence. [read]
- No hooks anywhere. grep for `hooks` in `.claude-plugin/` and `plugin.json` returned nothing. [measured]

### 1.2 The lint table, verbatim (lints/lints.toml) [read]

```toml
[lints.rust]
unsafe_op_in_unsafe_fn = "warn"          # each unsafe op needs its own unsafe block
missing_debug_implementations = "warn"   # every public type implements Debug
missing_docs = "warn"                    # [lib] document every public item
unreachable_pub = "warn"                 # pub items that aren't actually exported
rust_2018_idioms = { level = "warn", priority = -1 } # 2018-edition idioms, e.g. Formatter<'_>

[lints.clippy]
# Groups first, at lower priority so the individual settings below win.
all = { level = "warn", priority = -1 }
pedantic = { level = "warn", priority = -1 }

# Panics: return errors, and give every intentional panic a reason.
unwrap_used = "warn"
expect_used = "allow"                    # expect with a reason is the sanctioned form
panic_in_result_fn = "warn"
missing_panics_doc = "warn"              # [lib]
missing_errors_doc = "warn"              # [lib]
missing_assert_message = "warn"

# Suppressions must explain themselves and expire when no longer needed.
allow_attributes = "warn"                # prefer #[expect] (needs rust-version 1.81+)
allow_attributes_without_reason = "warn"

# Unsafe must be justified.
undocumented_unsafe_blocks = "warn"
multiple_unsafe_ops_per_block = "warn"

# API shape.
fn_params_excessive_bools = "warn"
struct_excessive_bools = "warn"
clone_on_ref_ptr = "warn"
redundant_clone = "warn"
rc_buffer = "warn"

# Evolution.
exhaustive_enums = "warn"                # [lib] exported enums are #[non_exhaustive]

# Idioms.
string_add = "warn"
wildcard_imports = "warn"

# Output and debugging leftovers.
dbg_macro = "warn"
todo = "warn"
print_stdout = "warn"                    # [lib]
print_stderr = "warn"                    # [lib]

# Casts that silently lose information.
as_conversions = "warn"
cast_possible_truncation = "warn"
cast_sign_loss = "warn"

# Pedantic lints that are mostly noise in practice, or that the skill disagrees with.
module_name_repetitions = "allow"
must_use_candidate = "allow"
module_inception = "allow"
enum_glob_use = "allow"
```

`lints/clippy.toml` [read]:

```toml
max-fn-params-bools = 0
allow-unwrap-in-tests = true
allow-expect-in-tests = true
allow-print-in-tests = true
```

CI strictness recommended by the README: `cargo clippy --all-targets --all-features -- -D warnings`. Everything is `warn`, so strictness lives only in the command line.

### 1.3 Other settings the repo states [read]

| Topic | What BeautifulRust says | Where |
|---|---|---|
| rustfmt | No `rustfmt.toml`. The Google digest recommends `max_width = 100`, `hex_literal_case = Lower`, `format_macro_matchers = true`, and `imports_granularity = Module`. | references/google-rust-style-guide.md:29,45. **[memory]** I believe `hex_literal_case`, `format_macro_matchers`, `imports_granularity` are nightly-only options. Verify before adopting. |
| MSRV | "set an MSRV deliberately"; `rust-version = "1.81"` needed so `allow_attributes*` lints are active (clippy disables them where `#[expect]` is unavailable). | SKILL.md:430, README lints section |
| unsafe | Three allowed reasons only. `// SAFETY:` on every block, `# Safety` docs on `unsafe fn`, Miri over unsafe, no `transmute`, no `unsafe impl Send` to silence the compiler. Does not recommend `forbid(unsafe_code)`. | SKILL.md:8 section |
| Warnings | "Never put `#![deny(warnings)]` in source". Enforce with `-D warnings` in CI. | SKILL.md:44 area |
| Workspace | "Share versions and lints through `[workspace.dependencies]` and `[workspace.lints]`"; flat `crates/` layout. | SKILL.md section 9 |
| Docs | `///` on every public item, `# Errors`, `# Panics`, `# Safety`, no `unwrap()` in doc examples; `cargo doc --no-deps` in the handback checklist. | SKILL.md section 10 |
| Dependencies | `cargo tree --duplicates`, `cargo deny`, `cargo udeps`, `cargo audit`. No config. | SKILL.md:422 |
| Tests | Fuzz parsers with `cargo fuzz`; Miri for unsafe; mockable I/O and time via `new_mocked() -> (Self, MockCtrl)` behind `test-util`. | SKILL.md section 11 |
| Features | Additive, bare-named (`std`, `serde`). | SKILL.md section 9 |

## 2. Judgement per item

The Go repo's rule: "a check is a hard gate only if fixing what it flags always makes the code better" (docs/quality-gates.md, line 3 of the Scope paragraph). [read]

Verdict key: **adopt** as is, **adapt** change the setting, **skip**.

| Item | Verdict | Reason and test result |
|---|---|---|
| Skills (writing, review, port) | **skip as dependency, adapt ideas** | The repo already has AGENTS.md and a contract. Skills add 800 lines of generic advice to agent context. The porting skill's "port the tests first, write PORTING.md of deliberate differences" matches the operator's need only partly: the operator rewrites for a contract, not for behaviour parity (AGENTS.md says legacy is not future design). Do not install. Consider a 40-line project-specific Rust section in AGENTS.md instead. |
| `rustc` lints `unsafe_op_in_unsafe_fn` | **adopt** | Always better. Moot if `forbid(unsafe_code)`. |
| `missing_debug_implementations` | **adapt** | Passes the test for secret-free types. Fails for types holding credentials: derived Debug leaks. Microsoft digest itself says hand-write Debug for secrets (references/microsoft-rust-guidelines.md M-PUBLIC-DEBUG). Keep the lint, require hand-written redacting Debug for credential types, and add a test that Debug output never contains the secret. Matches the repo rule "never print credentials". |
| `missing_docs` | **adapt** | Only for crates meant to be read (core, wire types). Fails the test for internal shell crates: the cheapest fix is a filler doc comment. Use `#![deny(missing_docs)]` in the core and wire crates only. |
| `unreachable_pub` | **skip** | Noisy in multi-crate workspaces where `pub` is the cross-crate surface. Cheapest fix is `pub(crate)` churn. Fails the test. |
| `rust_2018_idioms` | **skip** | Edition 2024 already covers it. |
| `clippy::all` | **adopt** at `deny` | Correctness, suspicious, style, complexity, perf. Always-better class. |
| `clippy::pedantic` (whole group) | **adapt** | Group has lints that fail the test. Take the group at `warn` (`deny` in CI) and allow the noisy ones explicitly. See list below. This mirrors Go's "staticcheck all minus six style checks". |
| `unwrap_used` | **adopt** at `deny` outside tests | Always better in a daemon: a panic kills the loop. |
| `expect_used = "allow"` | **adapt to `deny`** | `expect` panics just like `unwrap`; a message does not make a daemon survive. Allow `expect` only in `main` wiring and tests via `clippy.toml` (`allow-expect-in-tests`). For invariants that truly cannot fail use `#[expect(clippy::expect_used, reason = "...")]`. |
| `panic_in_result_fn` | **adopt** | Always better. |
| `missing_errors_doc`, `missing_panics_doc` | **adapt** | Core and wire crates only. Same reason as `missing_docs`. |
| `missing_assert_message` | **skip** | Fix is a boilerplate string. Fails the test. |
| `allow_attributes`, `allow_attributes_without_reason` | **adopt** | Matches Go `nolintlint` with `require-explanation` and `allow-unused: false`. `#[expect]` fails when stale, the exact analogue. Needs `rust-version >= 1.81`. |
| `undocumented_unsafe_blocks`, `multiple_unsafe_ops_per_block` | **adopt only if unsafe exists** | With `forbid(unsafe_code)` they never fire. Keep them on in any crate that opts out of forbid. |
| `fn_params_excessive_bools` with `max-fn-params-bools = 0` | **skip the 0, adapt to default (3)** | A single `bool` parameter is often clearer than a two-variant enum. Forcing zero produces churn. Fails the test. Prefer the type-system principle instead: brand flags that cross wire or policy boundaries. |
| `struct_excessive_bools` | **adopt** at default | Matches "make illegal states unrepresentable". |
| `clone_on_ref_ptr` | **adopt** if `Arc` is used | Cheap. Marginal. |
| `redundant_clone` | **verify group** | **[memory]** I believe this moved to `nursery`. Check with `cargo clippy` on the pinned toolchain. It is known for false positives, so keep nursery-level warn, not deny. |
| `rc_buffer` | **skip** | Matters little for a daemon; use `Arc<str>` when measured. |
| `exhaustive_enums` | **adapt** | Right for wire enums (they should be `#[non_exhaustive]`). Wrong for internal state enums: a `non_exhaustive` enum forces a wildcard arm in downstream crates and defeats exhaustive match (principle: exhaustive matching is the compiler's job). Apply it per-type by hand, not as a lint. Fails the test as a global lint in a multi-crate workspace. |
| `string_add` | **adopt** | Harmless. |
| `wildcard_imports` | **adopt** | Matches Go `dot-imports`. |
| `dbg_macro`, `todo` | **adopt** at `deny` | Matches Go `godox FIXME`. Add `unimplemented` and `unreachable`? **skip** unreachable: legitimate for proven invariants. |
| `print_stdout`, `print_stderr` | **adopt in all crates except the CLI crate** | In a daemon, stdout belongs to the CLI surface only. Enforces tracing usage. |
| `as_conversions` | **skip as blanket** | Fails the test. It flags `u8 as usize` that has no `From`, pushing `usize::try_from(x).expect()` which is worse. Adopt `cast_possible_truncation`, `cast_sign_loss`, `cast_possible_wrap`, `cast_lossless` instead (they flag real loss). Even those need local `#[expect]` with a reason for size math, as Go excluded `G115` for the same reason (.golangci.yml gosec excludes). |
| `module_name_repetitions`, `must_use_candidate`, `module_inception`, `enum_glob_use` allowed | **adopt the allows** | Plus allow `missing_errors_doc`/`missing_panics_doc` outside core, `too_many_lines`, `similar_names`, `items_after_statements`, `struct_field_names`, `doc_markdown`. **[memory]** names of pedantic lints; confirm each exists on the pinned toolchain. |
| `scripts/check-lints.sh` (lints proven to fire) | **adopt the idea** | Strong. A check crate with `// expect: lint` markers prevents silent lint-table rot. Reimplement in Rust or keep the Python script (it is plain JSON parsing of `--message-format=json`). I read it. It runs only clippy on a fixed crate, no secrets. Needs adapting to the final table. |
| Everything is `warn` and CI adds `-D warnings` | **adapt** | Put `deny` in the table for the always-better lints so a local `cargo clippy` already fails. Keep `-D warnings` in CI as backstop. Never `#![deny(warnings)]` in source (correct advice: new toolchains add warnings). Pin the toolchain so this is stable anyway. |
| MCP server, marketplace, ChatGPT packaging, benchmarks, website notify | **skip** | Irrelevant. MCP server is a docs search tool, not a control. |

Lints in the table that fail the "always better" test, summarised: `unreachable_pub`, `missing_assert_message`, `fn_params_excessive_bools` at 0, blanket `as_conversions`, blanket `exhaustive_enums`, `rc_buffer`, `missing_docs` on internal crates, and the whole `pedantic` group unless the noisy members are allowed. `expect_used = "allow"` is a gap in the other direction.

### Proposed workspace lint table (adapt of BeautifulRust plus the extras)

Mark: [memory] for lint names not read in BeautifulRust. Verify each against the pinned clippy with the check-crate technique.

```toml
[workspace.lints.rust]
unsafe_code = "forbid"
missing_debug_implementations = "warn"
unused_must_use = "deny"
unused_results = "allow"        # noisy; do not enable
non_ascii_idents = "deny"
trivial_casts = "warn"
unused_lifetimes = "warn"
unused_qualifications = "warn"
let_underscore_drop = "warn"

[workspace.lints.clippy]
all      = { level = "deny", priority = -1 }
pedantic = { level = "warn", priority = -1 }
# panics and partial functions
unwrap_used = "deny"
expect_used = "deny"
panic = "deny"
unimplemented = "deny"
todo = "deny"
dbg_macro = "deny"
indexing_slicing = "deny"        # see section 5.3
arithmetic_side_effects = "warn" # core crate only; measure noise first
panic_in_result_fn = "deny"
# suppressions
allow_attributes = "deny"
allow_attributes_without_reason = "deny"
# output
print_stdout = "deny"
print_stderr = "deny"
# casts
cast_possible_truncation = "deny"
cast_possible_wrap = "deny"
cast_sign_loss = "deny"
# errors swallowed
let_underscore_must_use = "deny"
map_err_ignore = "warn"
# api
wildcard_imports = "deny"
string_add = "deny"
# noise allowed
module_name_repetitions = "allow"
must_use_candidate = "allow"
missing_errors_doc = "allow"
missing_panics_doc = "allow"
too_many_lines = "allow"
similar_names = "allow"
```

Clippy config (`clippy.toml`):

```toml
allow-unwrap-in-tests = true
allow-expect-in-tests = true
allow-panic-in-tests = true
allow-indexing-slicing-in-tests = true
allow-print-in-tests = true
msrv = "<same as rust-version>"
disallowed-methods = [
  { path = "std::time::SystemTime::now", reason = "inject a Clock" },
  { path = "std::time::Instant::now", reason = "inject a Clock" },
]
```

`disallowed-methods` and `disallowed-types` are real clippy config keys **[memory]**. They are how Go's `depguard`-style rules (deny `math/rand`, testify, `.golangci.yml` depguard) translate: ban `SystemTime::now` in the core crate so time is injected. Use a per-crate `clippy.toml` (the core crate gets the ban, the shell crate's clock adapter does not).

## 3. Proposed Rust gate set

Versions are latest stable on crates.io as of 2026-11-06 **[looked up via crates.io API]** unless stated. Maintenance column uses the crate's last-published date from the same lookup. "Maintained" is my judgement from that date only. I did not read issue trackers.

| Gate | Tool and version | Invocation | Notes |
|---|---|---|---|
| Toolchain | Rust 1.99.1 stable (released 2026-10-16) **[looked up: GitHub releases API]**. Pin in `rust-toolchain.toml` and in the flake. | `channel = "1.99.1"`, `components = ["clippy","rustfmt","llvm-tools-preview"]` | The Go flow pins Go 1.27.3 and `GOTOOLCHAIN=local`. Same here. A newer stable may exist; I saw only the latest GitHub release object. |
| Format | rustfmt (ships with toolchain) | `cargo fmt --all --check` | Stable-only options. Minimal `rustfmt.toml`: `edition = "2024"`. Do not use nightly-only options (breaks pinned-stable). **[memory]** that `imports_granularity` is nightly. |
| Lint | clippy (toolchain) | `cargo clippy --workspace --all-targets --all-features --locked -- -D warnings` | Table from section 2. |
| Lint-table proof | script like `scripts/check-lints.sh` | `bash scripts/check-lints.sh` | Adapt BeautifulRust's. **[read]** |
| Tests | cargo-nextest 0.9.151 (2026-11-03) | `cargo nextest run --workspace --locked` plus `cargo test --doc` | nextest does not run doctests. Per-test process isolation helps the Go-style crash tests. Active. |
| Doc build | rustdoc | `RUSTDOCFLAGS="-D warnings" cargo doc --workspace --no-deps --document-private-items` | Catches broken intra-doc links. |
| Deny | cargo-deny 0.20.2 (2026-07-09) | `cargo deny --locked check` | Config below. Active. |
| Audit | cargo-audit 0.22.2 (2026-06-05) | `cargo audit --deny warnings` | Mostly redundant with `cargo deny check advisories` (both read the RustSec DB). **[inferred]** Keep deny only, run it on a schedule too (advisories appear without a commit). Skip audit as a separate gate unless you want `cargo audit bin` on release binaries via cargo-auditable. |
| Unused deps | cargo-machete 0.9.2 (2026-04-15) or cargo-shear 1.14.0 (2026-09-22); cargo-udeps 0.1.61 | `cargo machete` or `cargo shear` | udeps is accurate but needs nightly **[memory]**, which breaks a stable pin. Prefer shear or machete (fast, heuristic, stable). Pick one. Active. |
| Coverage | cargo-llvm-cov 0.9.1 (2026-09-06) | `cargo llvm-cov nextest --workspace --fail-under-lines 75 --codecov --output-path lcov` | Go gate is 75 total, 60 per package, 85 for upload-deciding packages (.testcoverage.yml). **cargo-llvm-cov has no per-package threshold [memory]**. Run once per crate with `-p <crate> --fail-under-lines N`, or post-process the JSON with a small script. |
| Mutation | cargo-mutants 27.1.0 (2026-06-02) | `cargo mutants --in-diff origin.diff -j2 --timeout-multiplier 3` and full run on `-p capture-core` nightly | Go treats mutation as opt-in, not CI. Same here. Rust has a real advantage: cargo-mutants is stable and understands `--in-diff`. Active. |
| Property tests | proptest 1.12.0 (2026-10-10); proptest-state-machine 0.9.0 | in-code | Replaces the Go seeded model histories (`model_history_test.go`, doc says). Use `proptest-state-machine` for the contract model. |
| Fuzz | cargo-fuzz 0.13.2 (2026-06-09), needs nightly **[memory]**; or bolero 0.13.7 (2026-10-03) | `cargo fuzz run <target> -- -max_total_time=30` | Go runs 14 fuzz targets 30 s each in CI. bolero runs the same harness as a property test on stable and as libFuzzer/AFL/Kani engines. **[memory]**: pick bolero if the pinned toolchain must stay stable; cargo-fuzz otherwise, run in a separate nightly job. |
| Miri | `cargo miri test` (rustup component, nightly) | only if any crate opts out of `forbid(unsafe_code)` | With `forbid`, skip. |
| Feature matrix | cargo-hack 0.6.46 (2026-11-01) | `cargo hack check --workspace --feature-powerset --depth 2` | Only meaningful if the crates have features. Keep features to zero, or one `test-util`. Then skip. |
| Semver | cargo-semver-checks 0.51.0 (2026-10-03) | `cargo semver-checks --package <wire-crate>` | Only for a published library crate. This product is a binary plus a wire contract. Use it on the wire crate only if it becomes a published crate for the Python consumer (it won't; Python reads JSON Schema). **skip** by default. |
| Supply chain beyond deny | cargo-vet 0.10.3 (2026-10-22), cargo-crev 0.27.1 | `cargo vet` | **skip** cargo-crev (about 1.1k recent downloads, **[inferred]** effectively dormant as an ecosystem). cargo-vet is the credible choice, but it needs audits or imports from Mozilla, Google, Bytecode Alliance feeds. For a two-machine personal tool the cost is high and the control is weak. Use cargo-deny plus `cargo-auditable` (0.7.7) plus locked builds. Revisit only if dependency count exceeds about 250 (aws-sdk pulls many). |
| Reproducible builds | nix + crane, `--locked`, `-C strip`, `--remap-path-prefix` | `nix build .#agent-sessions` | Go uses GoReleaser reproducible flags plus a Nix derivation (flake.nix). crane (pushed 2026-11-04 **[looked up]**) builds dependencies as a cached layer. |
| Cross-compile | targets aarch64-apple-darwin, x86_64-unknown-linux-gnu, aarch64-unknown-linux-gnu | Native builds in CI matrix like Go's tests matrix (linux amd64, linux arm64, macOS arm64, .github/workflows/ci.yml). Linux musl static optional. | With bundled SQLite and zstd (C code), cross-building needs a C toolchain. Prefer native CI runners per target over cross. `cargo-zigbuild` is an option **[memory]**, not verified. |
| Nix toolchain | oxalica/rust-overlay (pushed 2026-11-06 **[looked up]**) or nix-community/fenix (2026-11-05 **[looked up]**) | `rust-bin.fromRustupToolchainFile ./rust-toolchain.toml` | Both active. rust-overlay reads `rust-toolchain.toml` directly, so one source of truth. Prefer rust-overlay plus crane. **[judgement]** |
| Workflows | actionlint (pushed 2026-07-16 **[looked up]**), zizmor (in nixpkgs, already used: flake.nix lists `pkgs.zizmor`) | `actionlint`; `zizmor --pedantic .github/workflows` | Already in Go CI (`zizmorcore/zizmor-action`, ci.yml). Keep unchanged. |
| Dead code | rustc `dead_code` plus `unreachable_pub` off | `cargo clippy` | Go needs `deadcode` because `unused` misses exported functions. Rust's `dead_code` catches unused `pub(crate)` and private items by default. In a library-shaped workspace, `pub` items are not flagged. Keep crates `pub(crate)` by default. |
| Complexity (CRAP) | no mature Rust equivalent **[guess]** | skip | `clippy::cognitive_complexity` is in nursery and the Go repo itself treats gocognit as a backstop at 20. Use `cognitive_complexity` with `cognitive-complexity-threshold = 25` in `clippy.toml` as backstop **[memory]**. |
| Pre-commit | lefthook (same as Go) | `lefthook.yml` jobs: `cargo fmt`, `cargo clippy`, actionlint | Keep the existing hook tool. |
| Edit-time hook | same pattern as `.claude/hooks/go-check.sh` | per-edit `rustfmt --check` on the file and `cargo clippy -p <crate> --message-format=short` | Go repo has this (docs/quality-gates.md "For agents"). BeautifulRust has nothing equivalent. rust-analyzer LSP plugin for Claude Code is available **[memory]**. |
| Panic-on-abort | `panic = "abort"`? | **skip** | Daemon should unwind so `catch_unwind` at task boundary can quarantine one session. Decide in design. |

### cargo-deny configuration (adapt; defaults read from the template [looked up])

Template defaults are lax (`multiple-versions = "warn"`, `wildcards = "allow"`, `unknown-registry = "warn"`, `allow = []`). Tighten to:

```toml
[graph]
targets = ["aarch64-apple-darwin", "x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu"]
all-features = true

[advisories]
ignore = []                     # every ignore needs id and reason in this table form
yanked = "deny"

[licenses]
allow = ["MIT", "Apache-2.0", "Apache-2.0 WITH LLVM-exception", "BSD-3-Clause", "ISC", "Unicode-3.0", "Zlib", "CC0-1.0"]
confidence-threshold = 0.93
# no exceptions until a dependency forces one, each with a reason

[bans]
multiple-versions = "deny"      # start at "warn", then deny with explicit skip entries
wildcards = "deny"
highlight = "all"
deny = [
  { name = "openssl",     reason = "use rustls" },
  { name = "openssl-sys", reason = "use rustls" },
  { name = "native-tls",  reason = "use rustls" },
  { name = "chrono",      reason = "use jiff", use-instead = "jiff" },
  { name = "reqwest",     reason = "only the storage adapter may use it", wrappers = ["<storage-adapter-crate>"] },
]
# The layering bans (section 5.2) use `wrappers` to confine tokio, rusqlite, aws-* to shell crates.

[sources]
unknown-registry = "deny"
unknown-git = "deny"
allow-git = []
```

Notes. `[licenses] allow` is an allowlist and `confidence-threshold` and `wrappers` and `use-instead` keys exist in current cargo-deny **[memory]**. Verify with `cargo deny check` on the first run. `multiple-versions = "deny"` will fail early because the AWS SDK and tokio stack pull duplicate `hashbrown`, `syn`, `windows-*`. Start with `warn`, burn down with `skip` entries that give reasons. The Go repo's analogue is `go mod tidy -diff` plus `depguard` (.golangci.yml).

### Gate commands as a Makefile or justfile target (shape, not a patch)

```
check:  fmt-check clippy lint-proof nextest doc deny shear llvm-cov workflows capture-acceptance
```

Keep `capture-acceptance` (writer to reader to importer to event index to report, Makefile line 67 [read]) as a separate gate unchanged. It is language-independent and the contract says release acceptance is that path.

## 4. Crate recommendations

All versions **[looked up: crates.io API, 2026-11-06]**. "Last pub" is the crates.io `updated_at` date.

### 4.1 Core data

| Need | Pick | Version, last pub | Alternatives and judgement |
|---|---|---|---|
| SQLite | **rusqlite** `features = ["bundled"]` | 0.41.1, 2026-10-28; about 56M recent downloads | `sqlx` 0.9.0 (2026-05-21) is async with compile-time query checks that need a live DB or offline metadata. For one process, WAL, short transactions, a sync crate is simpler and has no executor coupling. The Go code runs `PRAGMA journal_mode=WAL` and `synchronous=FULL` (internal/sessioncapture/queue.go:458 [read]). rusqlite exposes these directly. Cost: needs a C compiler per target (modernc was pure Go). Mitigation: bundled builds with the `cc` crate are routine on macOS and Linux. Use one writer connection per owner and open read-only connections for views, matching the Go `runtime_view.go` split. |
| S3 client | **aws-sdk-s3** | 1.157.0, 2026-10-30; aws-config 1.13.0 | The Go code already uses `IfNoneMatch: "*"`, `IfMatch: etag`, `CopySourceIfMatch` (publicationr2/backend.go:256,379-381; store/r2.go:215). The Rust SDK has those same input fields **[memory]**. R2 docs say conditional PutObject headers are evaluated atomically against committed state, with 412 on failure **[looked up: developers.cloudflare.com S3 compatibility, via search snippet]**. The same page's detail on which headers apply to which operations I did not fully read. Cost: heavy compile, tokio required. Per-body timeouts are set via `TimeoutConfig` (operation, attempt, read) **[memory]**. |
| | `object_store` | 0.14.2, 2026-09-15 | Simpler API. S3 conditional put is off by default for non-AWS endpoints and must be enabled explicitly (`S3ConditionalPut::ETagMatch`) **[looked up: source snippets, docs.rs]**. `PutMode::Create` maps to `If-None-Match: *` and `Update` needs an ETag. No `CopySourceIfMatch`. Fine if the contract only needs create-if-absent and CAS-by-ETag. Adds an abstraction that hides status codes the Go code inspects. |
| | `rusty-s3` | 0.11.0, 2026-10-27; 1.1M downloads | Sans-I/O presigner. You bring HTTP (ureq 3.5.1 is sync, 82M downloads). Gives full control over headers and per-body timeouts and avoids tokio. More code to own. A strong fit for the "no tokio" option and for a fake S3 in tests. **[judgement]** Candidate if tokio is rejected. Verify multipart and ETag header handling on R2 before choosing. |
| Test S3 | `httpmock` 0.9.0 or a tiny in-process axum fake; Go used gofakes3 | | No equivalent of gofakes3 exists that I verified. **[unverified]** Plan a hand-written fake that implements PUT/GET/HEAD/copy with ETag semantics plus fault injection. Also keep the opt-in real-R2 test (`AGENT_SESSIONS_TEST_R2=1`, quality-gates.md). |
| zstd | **zstd** | 0.14.3, 2026-11-01; 115M | Binds libzstd (C, via zstd-safe). Pure-Rust `ruzstd` exists **[memory]**, decode-focused and slower. Keep the C binding. The Python reader uses `compression.zstd` (flake.nix comment), so format stays standard. |
| Hash (non-crypto) | **xxhash-rust** 0.8.19 (2026-09-28) or **twox-hash** 2.1.5 (2026-10-04) | | Go uses `cespare/xxhash/v2` (XXH64). Both implement XXH64 **[memory]**. xxhash-rust is `no_std` friendly and has `xxh64` feature. Pin one and test cross-language vectors against Go output. |
| Hash (crypto) | `sha2` 0.11.1 (2026-10-12) or `blake3` 1.8.7 | | Use whatever the contract names. Not verified here. |
| JSON | **serde** 1.0.229, **serde_json** 1.0.154 (2026-10-11) | | Strict policy below. |
| JSON Schema export | **schemars** 1.2.3 (2026-10-12) | | schemars 1.x emits JSON Schema 2020-12 **[memory]**. Python validates with `jsonschema`. Export from a `cargo run -p wire --bin schema` step and diff against a committed file in CI (generated-code drift check, as Effective Rust item 32 suggests). |
| JSON validation in Rust tests | `jsonschema` 0.60.2 | | Validate Rust output against the exported schema in tests to prove schema and serde agree. |
| Path-aware errors | `serde_path_to_error` 0.1.20 | | Good diagnostics at boundaries. Last pub 2025-09, stable. |
| CLI | **clap** 4.6.7 (2026-09-14) | | derive API. Standard. |
| Time | **jiff** 0.2.39 (2026-11-02) | | Active, by BurntSushi, strong tz and parsing. `chrono` 0.4.45 and `time` 0.3.55 also active. Pick jiff for new code. Wire timestamps stay as explicit UTC integers or RFC 3339 strings per the contract. Ban chrono via cargo-deny as above. |
| Errors | **thiserror** 2.0.21 in all library crates; **anyhow** 1.0.104 only in the `main` crate | | See 5.5. `eyre` 0.6.14 offers nothing here. |
| Newtypes | hand-written newtypes plus `serde(try_from = "String")`; optionally **nutype** 0.8.0 (2026-09-20) | | nutype is a proc macro with validation and sanitization. 1.7M downloads, young. A hand-written 15-line newtype is auditable and has no proc-macro dependency. Use nutype only if you will have more than about 15 validated types. **[judgement]** |
| Logging | **tracing** 0.1.44 (2025-12-18), **tracing-subscriber** 0.3.23 with `json` feature | | tracing is stable and has been at 0.1.x for years. Last publish 2025-12 is slow but normal. Use JSON layer to a file or stderr. |
| Locks | **fs4** 1.1.0 (2026-04-28) or **fd-lock** 4.0.4 (2025-03-10) or `std::fs::File::lock` | | std has had `File::lock`, `try_lock`, `unlock` stable since 1.89 **[memory]**. With Rust 1.99 you can drop both crates. Verify on the pinned toolchain. Behavior on macOS is `flock(2)`, same as gofrs/flock (runtime_inbox.go:16 [read]). |
| Atomic writes | **tempfile** 3.27.0 (`NamedTempFile::persist`) plus explicit `sync_all` on the file and on the parent directory | | There is a Go `internal/safefile` package; read it before porting and port its tests first. macOS `fsync` does not flush the drive cache. `fcntl(F_FULLFSYNC)` does **[memory]**. Decide durability level per the contract. `rustix` 1.1.6 or `nix` 0.31.3 give `fcntl` safe wrappers so `forbid(unsafe_code)` survives. |
| File watching | **notify** 8.2.1 (2026-10-27) vs polling | | The contract's autonomous bounded retries and freshness model are poll-and-reconcile shaped. notify uses FSEvents on macOS and inotify on Linux. Both drop events under load and on overflow. Keep a periodic full scan as the source of truth and use notify only as a wake-up hint, as the Go "hint inbox" does (runtime_inbox.go). **[inferred]** |
| Disk free | **sysinfo** 0.40.1 | | Heavy for one number. `rustix::fs::statvfs` gives it with no extra crate. Prefer rustix. |
| Syscalls | **rustix** 1.1.6 over **nix** 0.31.3 or libc | | rustix has safe wrappers and no libc dependency on Linux. Preserves `forbid(unsafe_code)`. For Mac `st_dev`, `st_mtime` etc. (capturesource/fingerprint_darwin.go uses raw `syscall.Stat_t`) use `std::os::unix::fs::MetadataExt`. |
| Signals | `signal-hook` 0.4.6 or tokio signal | | Needed for clean shutdown under launchd/systemd. |
| systemd notify | `sd-notify` 0.5.0 | | Optional. |
| launchd | no crate needed | | The Go repo writes plists (internal/launch, internal/install). `launchd` crate 0.3.0 last pub 2023-08 with about 466 downloads: skip. Generate the plist with a template and test it with `plutil -lint` on macOS CI. |
| macOS codesign | none in Rust | `codesign --force --sign - --options runtime <bin>` in the release step **[memory]** | Apple Silicon requires at least an ad-hoc signature to run **[memory]**, and Rust's linker ad-hoc signs by default on arm64 **[memory]**. Verify with `codesign -dv`. Go 1.27 binaries run on macOS 13+ (quality-gates.md). |
| BC-01 | Linux? | | The brief calls it a second machine; I did not verify its OS. |

### 4.2 Testing crates

| Need | Pick | Version | Judgement |
|---|---|---|---|
| Property tests | proptest 1.12.0 | 2026-10-10 | Standard. Shrinking works. Alternatives: quickcheck 1.1.0 (weaker shrinking), bolero 0.13.7 (unifies fuzz and property). |
| Model-based | proptest-state-machine 0.9.0 | 2026-10-10 | Maps to Go `model_history_test.go` (doc claim, not read by me). |
| Snapshots | insta 1.49.0 | 2026-10-03 | For golden wire fixtures. Review with `cargo insta review`. Beware: snapshots can hide privacy leaks. Keep fixtures synthetic. |
| CLI end to end | `assert_cmd` 2.2.2, `trycmd` 1.2.1, `snapbox` 1.2.2 | | The Go repo uses testscript (`rogpeppe/go-internal`, go.mod:18). The closest is `trycmd` (`.toml` or `.md` scripts) or the newer `snapbox`. Neither supports testscript's txtar archives with file setup as directly **[memory]**. Plan: `assert_cmd` for logic plus `trycmd` for readable scenarios, and keep Python process journeys in `capture-acceptance`. |
| Temp dirs | tempfile 3.27.0 | | |
| Deterministic time | a `Clock` trait injected into the shell, with a `ManualClock` for tests | | Ban `SystemTime::now` in core via `disallowed-methods`. Do not use `mock_instant` style global patching. |
| Deterministic simulation | **turmoil** 0.7.2 (2026-04-24), **madsim** 0.2.34 (2025-10-11) | | Both simulate network and tokio. The capture daemon talks to one S3 endpoint and a local filesystem. Neither simulates the filesystem or SQLite crash points. Not applicable. The Go `crash_process_test.go` approach (kill a child at every fault stage) is the right model. Port that with `assert_cmd` or a fail-point env var. `fail` 0.5.1 last pub 2022: dormant, do not use. Hand-rolled fault enum in the shell crate instead. **[judgement]** |
| Concurrency model checking | loom 0.7.2 (2024-04), shuttle 0.9.10 (2026-11-05) | | Only if the daemon has custom lock-free or multi-threaded state. With channels and one owner per resource, skip. |
| Benchmarks | criterion 0.8.2, divan 0.1.21 | | Go keeps `bench-smoke` (run each once). `cargo bench -- --test` style smoke exists for criterion **[memory]**. |
| Mock HTTP | httpmock 0.9.0 | | |

### 4.3 Runtime choice: tokio vs threads

Facts from the Go code **[read]**: one S3 endpoint, SQLite in WAL, flock-based ownership, a queue with attempts and next-retry (queue.go:458), per-file hash and compress. Concurrent operations are few.

- Sync threads plus blocking I/O fits the shape. Fewer colored functions, no `Send + 'static` pressure, easier `unwrap` banning, simple cancellation via a stop flag, simpler SQLite (blocking anyway).
- `aws-sdk-s3` is async-only and needs tokio. Then tokio lives in one storage adapter crate with a private runtime (`Runtime::new` and `block_on`), exposing a blocking trait to the rest. This is a standard bridge. Cost: a tokio dependency tree in the binary.
- With `rusty-s3` plus `ureq` 3.5.1 you can have a fully sync binary and a smaller supply chain. You own timeout and retry logic, which the contract wants anyway ("autonomous bounded retries").
- **Recommendation [judgement]**: sync core and shell; storage adapter in its own crate. Start with `aws-sdk-s3` behind a blocking trait, because it matches the Go behavior on R2 and is lowest risk. Keep the trait narrow (`put_if_absent`, `put_if_match`, `get`, `head`, `copy_if_match`, `list`) so a `rusty-s3` plus `ureq` implementation can replace it if compile time or supply chain hurts. Do not let tokio types appear outside that crate (cargo-deny `wrappers`, section 5.2).
- Measure before deciding. I did not build either.

### 4.4 Strict JSON policy

Mechanism **[memory of serde behavior, standard]**:

- Every wire struct: `#[derive(Serialize, Deserialize, JsonSchema)] #[serde(deny_unknown_fields)]`.
- Reading future versions: do not use `deny_unknown_fields` on an envelope that must tolerate additive fields. Read a small version header first, then dispatch to the exact version type. The contract's direction is explicit failure on unsupported formats (quality-gates.md Scope: "Unsupported formats must fail explicitly rather than produce a successful-empty refresh") so deny unknown fields and reject unknown versions.
- Two-step parse: `struct Header { schema: SchemaVersion }` then `match header.schema { V2 => serde_json::from_slice::<ManifestV2>(..) }`.
- Known `serde` gotcha: `deny_unknown_fields` does not work together with `#[serde(flatten)]`, and is unreliable with internally tagged enums **[memory]**. Avoid `flatten` and internal tagging on wire types. Use adjacently tagged or externally tagged enums, or separate structs per version.
- Duplicate keys: serde_json accepts duplicate keys silently (last wins) **[memory]**. If the contract requires rejecting them, parse once into a custom visitor or validate with a streaming check. Add a fuzz target.
- Canonical bytes: if manifests are hashed, define canonical serialization (field order, no floats, integer widths) and test round trip `bytes -> value -> bytes` equals input. `serde_json` preserves struct field order in `Serialize`, which is deterministic for derives **[memory]**.
- Integers: use `u64` for counters. Avoid `f64` on wire. JSON numbers above 2^53 break the Python and JavaScript sides **[standard]**. Serialize large ids as strings.
- Export schemas with schemars, commit them, and fail CI on drift. Python tests validate with `jsonschema`.

## 5. Rust design guidance

### 5.1 Workspace layout [judgement, motivated by the Go layout]

The Go `internal/` has 30 packages. Several mix policy with I/O, e.g. `sessioncapture` with SQLite, flock and filesystem in one package (queue.go, runtime_inbox.go, runtime_store.go). I did not audit the mix in depth, just saw SQLite and flock imports in that package.

```
crates/
  wire/        types, serde, schemars, versioned parse. No I/O. #![deny(missing_docs)]
  core/        pure state machines: epoch snapshots, manifests, coverage/freshness, retry policy,
               queue transitions. Input events and Clock values in, effects (data) out. No I/O.
  store-api/   trait definitions (BlobStore, Spool, Clock, Fs) used by core and shell
  shell-fs/    atomic writes, locks, fingerprints, rustix
  shell-sqlite/ rusqlite adapter implementing the spool/queue traits
  shell-s3/    aws-sdk-s3 (and tokio) adapter implementing BlobStore
  reader/      the one authoritative reader (restore and analytics share it)
  daemon/      wiring, scheduler, tracing setup
  cli/         clap binary (the only crate that prints and uses anyhow)
  testkit/     fakes: FakeS3, ManualClock, fault injection. dev-dependency only
```

The contract demands one reader for restore and analytics (AGENTS.md). Putting it in its own crate with no shell dependency lets analytics tests and restore tests link the same code. Keep `reader` depending on `wire` plus `store-api`, not `shell-*`.

### 5.2 Enforcing layering

1. Cargo itself: `core` lists only `wire`, `store-api`, `thiserror`, `serde` in `[dependencies]`. A forbidden crate cannot be named without a Cargo.toml edit, which a reviewer sees.
2. cargo-deny `bans` with `wrappers` blocks a crate from appearing in the graph unless a named crate depends on it. Example **[syntax from memory, verify]**:

```toml
deny = [
  { name = "tokio",   wrappers = ["shell-s3", "daemon"] },
  { name = "rusqlite", wrappers = ["shell-sqlite"] },
  { name = "aws-sdk-s3", wrappers = ["shell-s3"] },
]
```

   `wrappers` means "denied unless a direct dependent is in this list". That is the Rust analogue of Go `depguard`. It checks the whole graph, so it catches a transitive pull of tokio into `core` as well.
3. clippy `disallowed-methods` and `disallowed-types` in the core crate's `clippy.toml`: `std::fs::*`, `std::time::SystemTime::now`, `std::time::Instant::now`, `std::env::var`, `std::thread::sleep`, `std::net::*`. This gives the "pure core" check at the call level. `std::fs` is in std so Cargo cannot block it. **[memory]** that `disallowed-methods` accepts function paths and a `reason`.
4. `cargo tree -p core --edges normal` in CI, asserting no `libc`, `rustix`, `tokio` via a one-line grep. Cheap second opinion.
5. Do not use features to hide I/O in core.

### 5.3 Panic and indexing policy

- `unwrap_used`, `expect_used`, `panic`, `todo`, `unimplemented`, `indexing_slicing` at `deny` in non-test code. Tests allowed through clippy.toml keys above.
- `indexing_slicing` is the one that most often fails the "always better" test: `.get(i).ok_or(Error::...)` is better in parsers of untrusted data and worse in a loop with a proven bound. Apply `deny` in `wire`, `core`, `reader`; `warn` elsewhere; document `#[expect(..., reason = "len checked at line N")]`. Measure noise first. **[judgement]**
- Task boundary: wrap each unit of work so that a panic in one session's processing is caught, logged and the session is quarantined, while the daemon survives. This needs `panic = "unwind"` (default). `catch_unwind` needs `UnwindSafe` bounds, so keep per-task state owned. This mirrors the contract's quarantine requirement (AGENTS.md "explicit quarantine/coverage/freshness").
- Never `.unwrap()` on a lock: use `parking_lot` 0.13.0 (no poisoning) or handle `PoisonError` explicitly. With single-owner state and channels, there are few locks.

### 5.4 Unsafe, docs, must_use, non_exhaustive

- `[workspace.lints.rust] unsafe_code = "forbid"`. Note `forbid` cannot be locally overridden with `#[expect]` or `#[allow]`. If a crate truly needs unsafe (e.g. FFI), exclude only that crate from the workspace lint table and add Miri plus `undocumented_unsafe_blocks`. Prefer rustix and std (`MetadataExt`) so no crate needs it. Go code uses raw `syscall.Stat_t` only to read mtime/ctime/dev (fingerprint_*.go [read]), which `MetadataExt` provides safely.
- `#![deny(missing_docs)]` on `wire`, `core`, `store-api`, `reader` only. Docs there are the contract in code.
- `#[must_use]` on `Result`-like effect types and on every "decision" struct that `core` returns (a dropped effect list is a silent bug). `clippy::must_use_candidate` stays allowed (noise) but `unused_must_use = "deny"` and `let_underscore_must_use = "deny"` are on.
- `#[non_exhaustive]` on wire enums that may gain variants, since a consumer must handle "unknown version" explicitly. Do not put it on `core`'s internal state enums: the compiler must flag every match when a state is added (type-system discipline principle: exhaustive matching is the compiler's job). Inside the defining crate `non_exhaustive` has no effect, so this only bites cross-crate. Hence the layout puts wire enums in `wire` and matches over them in `core`: use an explicit `Unknown` handling arm that returns `Err(Unsupported)` rather than `_ => {}`.
- Ban silent wildcard arms on internal enums with `clippy::wildcard_enum_match_arm = "warn"` and `match_wildcard_for_single_variants` **[memory of names]**. Go `exhaustive` linter with `default-signifies-exhaustive: true` (.golangci.yml) is weaker than this. Rust can be stricter at no cost.

### 5.5 Errors

- Library crates: `thiserror` enums, one per crate. Each error type carries a **disposition** (retryable, quarantine, fatal, caller-bug) as a method `fn disposition(&self) -> Disposition` with an exhaustive match, so adding a variant forces a decision. The contract asks for "autonomous bounded retries" and "explicit quarantine", which are dispositions, not strings. Go code encodes this in `sessioncapture` error strings and `last_error TEXT` (queue.go:458 schema) **[read]**; making it a type is the main win.
- `anyhow` only in `cli` and `daemon::main` for top-level context. Never convert a classified error to `anyhow` before the disposition decision.
- Ban `Box<dyn Error>` returns in library crates via review, not lint **[no clippy lint I can name]**.
- Redacted errors: error `Display` for credential-bearing operations must not embed request URLs with presigned query strings. Test that.

### 5.6 Typestate and newtypes

From the type-system principle (make illegal states unrepresentable; brand primitives; parse at boundaries): newtypes for `SessionId`, `SourceEpoch`, `Sequence`, `ManifestDigest`, `BucketKey`, `HostId`; `TryFrom<&str>` validating once; `serde(try_from = "String", into = "String")` so wire parse is the validation. Make `Manifest` constructible only through a builder that proves completeness (complete manifests are a contract requirement), or model `Snapshot<Complete>` vs `Snapshot<Partial>` only if a function truly accepts one and not the other. Don't build typestate for every state transition: the Go queue state is a text column (`state TEXT`, queue.go:458) and a Rust `enum JobState { Pending, Running{..}, Done, Quarantined{reason} }` with data in variants removes the "optional fields that contradict" shape without typestate.

### 5.7 Versioned wire types

- One module per version: `wire::v1`, `wire::v2`. Never mutate a published version's struct. A new field means a new version.
- A `SchemaVersion` enum (`#[non_exhaustive]`) parsed from a header, then `match` dispatch with no wildcard in `reader`.
- Conversion `v1 -> core model` lives in `reader`; `core` sees only its own model type, so adding v3 touches `wire` and `reader`, not `core`.
- Golden fixtures per version produced by the real writer (contract requirement: "actual writer output -> reader -> importer -> event index -> report") committed and replayed forever, as `insta` snapshots or raw files. The Go repo's quality-gates.md says old all-generation fixtures describe v3 and are not required for legacy compat, so keep only fixtures the contract keeps.
- Schema export plus Python `jsonschema` check in `capture-acceptance`.

## 6. Open questions and unverified items

### Open questions for the operator

1. Is the Rust workspace allowed to depend on tokio inside the storage adapter, or must the binary be fully synchronous? This decides `aws-sdk-s3` vs `rusty-s3` plus `ureq`.
2. Must release binaries be signable and reproducible bit-for-bit (the Go flow ships reproducible flags, SBOMs and attestations, quality-gates.md "CI and releases")? That decides whether cargo-auditable, crane and a Nix-only release are required.
3. Will the Rust wire crate ever be published or consumed by another Rust program? If not, skip cargo-semver-checks and `non_exhaustive` ceremony beyond the version enum.
4. Is a C compiler on every build host acceptable (bundled SQLite and zstd)? The Go build avoids it today.
5. Durability level on macOS: `fsync` vs `F_FULLFSYNC` for spool and SQLite (`PRAGMA fullfsync`). Cost is large. Only a product owner can price it.
6. How strict on supply chain: cargo-deny only, or also cargo-vet with imported audits?

### Things I could not verify

- Exact present state of each tool's docs. Version numbers and publish dates came from the crates.io API, which I trust. Statements about features of tools (`wrappers`, `--in-diff`, nightly requirement of cargo-fuzz and udeps, per-package llvm-cov thresholds, std `File::lock` stable since 1.89, `redundant_clone` group, `disallowed-methods` keys) are from memory and need a first-run check.
- Whether a Rust release after 1.99.1 exists. I saw GitHub's latest release object only (published 2026-10-16). A WebSearch for November 2026 releases returned only older announcements (1.96 in May 2026).
- R2 conditional PutObject: the official docs page confirms `If-None-Match: *` atomicity per search excerpt. I did not read the full page for `If-Match` on PutObject nor `CopySourceIfMatch` on CopyObject, which the Go code uses (store/r2.go:215). Verify on a scratch bucket before committing to a client crate. I did not touch R2.
- Whether `aws-sdk-s3` for Rust exposes `if_none_match`, `if_match`, `copy_source_if_match` on the pinned version: from memory only.
- That `redundant_clone`, `cognitive_complexity`, `wildcard_enum_match_arm` have the group and name I stated.
- BeautifulRust's `mcp/` code and eval cases: I skimmed the plan and structure, not every line. The skills' per-section text (about 60 lines of SKILL.md section 3 to 5 and the review and port skills beyond their headers) was read only in part. The benchmark directory and eval cases were not read. They are not mechanisms.
- I did not run any script from BeautifulRust, including `check-lints.sh`. I read it. It writes only to `mktemp` directories and runs clippy on its own crate **[read]**. I did not run clippy at all, so "every lint in the table fires" is the repo's claim, not mine.
- Go-side claims about package internals (sessioncapture mixing I/O and policy) are from grep hits on imports, not from a code audit.
- Nix: `crane` and `rust-overlay` are active per GitHub `pushed_at`. I did not test building either for aarch64-darwin.
- cargo-vet and crev judgements are from download counts and recall, not from using them.

Sources used for lookups: crates.io API (`https://crates.io/api/v1/crates/<name>`), GitHub API for `rust-lang/rust` releases and `pushed_at` of fenix, rust-overlay, crane, actionlint, cargo-deny, rustsec; cargo-deny `deny.template.toml` (EmbarkStudios/cargo-deny main); Cloudflare R2 S3 API compatibility page (developers.cloudflare.com/r2/platform/s3-compatibility/api/, via search snippet); object_store source on docs.rs and googlesource mirror (via search snippet).
