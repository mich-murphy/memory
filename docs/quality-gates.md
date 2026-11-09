# Quality gates

> **Scope (2026-10-23):** The development gates below remain applicable, but passing them is not end-to-end publication acceptance. The agreed [session-capture contract](session-capture-contract.md) also requires writer-generated fixtures through one authoritative reader shared by restore and analytics, exact source/epoch/body coverage, idempotent import and report freshness. Unsupported formats must fail explicitly rather than produce a successful-empty refresh. Existing all-generation recovery fixtures describe v3, not a legacy-compatibility requirement for the replacement.

The CI workflow configures development gates on each push and pull request; local `make` targets are listed below. Go mutation campaigns and repeated benchmarks are opt-in diagnostics, not CI gates; the Rust actual-window-mutation check below is mandatory. Workflow configuration is not evidence that hosted CI ran successfully. The rule behind the settings: **a check is a hard gate only if fixing what it flags always makes the code better.** Bug, safety and error-handling checks pass that test. Size and style metrics don't, because the cheapest way to satisfy them is often an arbitrary split or rename, so they are either off or set high enough to act as backstops.

The mandatory Go opt-in v2 writer → shared reader → importer → event index → report
gate is part of Go development checks, not native/R2/two-machine or Rust acceptance. See
[dated offline evidence](session-capture-offline-evidence.md) for historical results
and the [current handoff](session-capture-handoff.md#current-session-checkpoint) for
checkout qualification, deployment and next action. Defaults remain legacy; do not
weaken release gates.

The research behind these choices (tool versions, the configs of 13 well-regarded Go projects, and each tool's docs) is in the project's `research/collector-language/go-tooling.md`.

## Rust rewrite gates (implementation in progress)

The Rust workspace is under `rust/`; [DESIGN.md](../DESIGN.md) sections 14–17
specify its additional gates. These do not replace any Go or release acceptance
checks below. Current evidence and ownership are in the
[active handoff](session-capture-handoff.md#rust-implementation-delivery--active-coordinator-brief)
and [`rust-rewrite/implementation/`](rust-rewrite/implementation/).

Run the Rust gate wrapper in the pinned Nix shell, with bounded native parallelism:

```sh
nix develop --max-jobs 2 --cores 2 --command sh -c \
  'cd rust && PROPERTY_CASES=2000 bash scripts/gates.sh'
```

The wrapper runs format, strict clippy, dependency/license/security and pure-core
dependency bans, nextest, compile-fail doctests, Hegel, proptest, the actual production
window-comparison mutation, schema drift, coverage and the deliberate-lint checker. It records seeds and each command's exact status under `rust/artifacts/`;
a failed or unavailable gate makes the aggregate fail. CI uses at least 500 cases,
local slice acceptance requires 2,000. A scaffold/property pass is not proof of
the daemon, real R2 semantics, privacy at the actual store sink, restore/import,
footprint, or the two seven-day native shadows. The measured coverage floor is
**98%**, not to be lowered for added code. The latest source-bound integrated run
(gate 8, 2026-11-09) passed at **98.3173%** (5,200/5,289 lines), 369/369 nextest, 2,000
cases, seed 730210; see [its evidence](rust-rewrite/implementation/evidence/12-slice2-round10/).
At 2,000 cases the two daemon state-machine properties took about 6 minutes because every
simulated round did real `F_FULLFSYNC` writes. Amendment A7 is implemented on
`rust/sync-policy`: only those two properties select `SyncPolicy::None` and now take about
14–20 s; every other test and the binary keep `Full`. Hosted CI (`.github/workflows/rust.yml`,
500 cases, random seed) runs `scripts/gates.sh` on every branch of stack #56. Ad-hoc coverage runs at low case counts fail the property coverage guards by
design; use 2,000 cases or `--ignore-run-fail` and say so.
The [final-toolchain BC probe](rust-rewrite/implementation/evidence/03-bc-recheck/README.md)
also passed, on native x86_64. Neither result proves hosted CI or full release
acceptance; changed source requires relevant revalidation.

Focused examples (also run inside the Nix shell):

```sh
cd rust
cargo test --locked -p memory-core --test parent_privacy_review -- --test-threads=2
cargo test --locked -p memory-core --doc
cargo xtask schemas --check
```

The official Rust toolchain is separately pinned through `rust-overlay`; the
existing nixpkgs/Go dependency pin is preserved. This avoids the independently
reproduced profiling-runtime failure of the original locked nixpkgs Rust build;
it is not permission to skip coverage or use an unpinned host compiler.

## Commands

| Command | What it runs |
|---|---|
| `make check` | Format check, lint, fix/tidy checks, deadcode, vulnerability scan, coverage/CRAP, benchmark smoke, actionlint and `capture-acceptance`; no fuzzing, mutation campaign, repeated benchmarks or full Python discovery |
| `make fmt` | gofumpt and goimports, through `golangci-lint fmt` |
| `make lint` | golangci-lint with `.golangci.yml` |
| `make test` | Actual writer/reader/analytics compatibility gate, then `go test -race -shuffle=on ./...` |
| `make capture-acceptance` | Build the actual candidate; run writer/reader/import/event index/report and refresh process journeys, plus synthetic candidate-R2 and delivery-launcher tests (offline by default; pinned analytics environment required). The live launcher is separately opt-in, not acceptance from an offline pass. |
| `make cover` | Tests with the race detector, plus end-to-end runs of the built binary, merged into one profile and checked against `.testcoverage.yml`; `internal/syncer` runs in its own `go test` invocation so the two heaviest race-instrumented packages don't contend for cores, with the flags and the merged profile unchanged |
| `make crap` | `make cover`, then the CRAP gate on that profile |
| `make fuzz` | Each explicitly listed `FUZZ_TARGETS` entry for `FUZZTIME` (default 30s) |
| `make mutation` | Mutation testing with gremlins; `MUTATE_DIFF=origin/main` mutates only the lines changed since that ref. Not part of `make check` |
| `make bench` | Every benchmark `BENCHCOUNT` times (default 10) into `bench.txt`, for benchstat ([benchmarks.md](benchmarks.md)) |
| `make bench-smoke` | Every benchmark once, as CI does, so a broken one fails |
| `make hooks` | Installs the git hooks in `lefthook.yml` |
| `make gopls` | Installs the pinned gopls, for the Claude Code `gopls-lsp` plugin |

Tools other than golangci-lint are pinned in their own module, `tools/go.mod`, and run with `go tool -modfile=tools/go.mod <name>`, so they never touch the collector's `go.mod` and Dependabot updates them (it only reads files named `go.mod`). gopls has a module of its own, `tools/gopls/go.mod`, because it needs a different `golang.org/x/tools` from deadcode's. golangci-lint is installed as a binary, as its maintainers recommend, at the version in `.golangci-lint-version`: CI installs that version, and `make lint` fails when the local one differs.

## Toolchain

The pinned Nix shell supplies Go 1.27 and sets `GOTOOLCHAIN=local`; CI explicitly installs `go1.27.3` from the `go.mod` toolchain line. `go.mod` says `go 1.27.0`: the code uses the `uuid` package and other APIs new in 1.27. Binaries built with Go 1.27 run on macOS 13 or later.

Generated Pi producer-handler tests also require Node's built-in TypeScript stripping. The locked Nix shell supplies Node24.21.0, and the shared CI setup action explicitly selects that version rather than inheriting a host Node. These synthetic tests mock `spawn` and erase type-only imports: they neither import the Pi SDK at runtime nor execute a collector or model. Missing Node/type-stripping support fails the test; it is not skipped. This development-tool dependency does not change installed producer dispatch or install SDK packages.

## Formatting

gofumpt (stricter gofmt) and goimports, with this module's imports grouped last. There is no line-length limit: Go's style guides say to break lines by meaning, not length.

## Lint (`.golangci.yml`)

**Hard gates, at full strength:**

- **staticcheck**, every check except six naming and doc-comment style checks.
- **go vet**, every analyzer except `fieldalignment` (reorders struct fields for bytes) and `shadow` (noisy around `err`).
- **errcheck**, including type assertions. Ignoring an error is allowed only visibly, as `_ = f.Close()` with a comment.
- **errorlint**, **bodyclose**, **noctx**, **contextcheck**, **nilerr**, **nilnesserr**, **sqlclosecheck**, **rowserrcheck**, **copyloopvar**, **exhaustive**, **unparam**, **unconvert**, **reassign**, **gocritic** (diagnostic and performance tags), a curated **revive** rule set, **modernize**, **intrange**, **usestdlibvars**, **usetesting**, **perfsprint**.
- **gosec**, minus rules that fire on this tool's whole job: opening the paths it walks (G304, and its taint twin G703), running `git` with fixed arguments (G204, G702), and integer size conversions (G115). File-permission rules stay on: state and temp files are 0600 and directories 0700, because they hold transcripts.
- **depguard**: denies `io/ioutil`, `github.com/pkg/errors`, `math/rand` (v1) and testify.
- **Nothing hidden:** golangci-lint's default caps on repeated issues are off.
- **No blanket excuse for unchecked Close errors:** the `std-error-handling` preset stays off, because an unchecked `Close` or `Flush` on the write path loses data.

**Backstops, not targets:**

- **Cognitive complexity (gocognit) at 20.** Every `if err != nil` adds a point, so lower limits force straight-line code to be split. Restructure when it reads better; never split a function just to get under the limit.
- **Duplication (dupl) at 150 tokens**, outside tests.
- **FIXME comments fail the build**; TODOs are allowed.

**Off, because satisfying them makes the design worse here:** funlen, gocyclo, cyclop, nestif, maintidx (length and duplicate complexity metrics); lll, golines (line length); wrapcheck, err113 (wrapping or naming every error); varnamelen; nonamedreturns (named results are how a deferred `Close` error is returned); exhaustruct, ireturn, gochecknoglobals, mnd, goconst; wsl, nlreturn, gci, tagliatelle; paralleltest, testpackage (tests set HOME and environment variables, which parallel tests can't); forcetypeassert (duplicates errcheck).

**Test code.** Tests run every linter above, with three exceptions: dupl, gocognit and gosec are off in `_test.go` files and in `internal/testenv` (fixtures with fake tokens and world-readable fake homes), and errcheck's *type assertion* check is off in `_test.go` files. A wrong shape in a test panics and fails that test with a stack trace, which tells you as much as an `ok` check would. Unchecked errors from calls are still reported in tests.

**Suppressions:** every `//nolint` must name the linter and give a reason, and an unused one fails the build.

## Other static checks

- `go fix -diff ./...` fails when code isn't in current Go idiom.
- `go mod tidy -diff` fails on an untidy `go.mod`, for the collector and both tool modules. CI also runs `go mod verify`.
- `deadcode -test ./...` fails on functions nothing reaches from `main` or a test, and on code that doesn't load. `-test` stays because `internal/testenv` is only used by tests.
- `govulncheck ./...` fails on a known vulnerability in code the collector calls. It only sees code built for the OS it runs on, so CI runs it on Linux and on macOS.
- `actionlint` and zizmor check the GitHub Actions workflows. Every action is pinned to a commit SHA, credentials aren't persisted after checkout, and permissions are read-only unless a job needs more.

## Tests and coverage (`.testcoverage.yml`)

- Tests use the standard `testing` package and go-cmp; no assertion library. The command line is tested end to end with testscript (`cmd/agent-sessions/testdata/script`) against fake Claude Code, Codex and Pi homes. The S3 store is tested against gofakes3 in-process, and against real R2 when `AGENT_SESSIONS_TEST_R2=1` and R2 credentials are set.
- `make test` and `make cover` run the Go suite with cgo, `-race` and `-shuffle=on`; coverage uses atomic instrumentation and actual subprocess coverage. This is not universal across commands: `capture-acceptance` builds a `CGO_ENABLED=0` binary for Python process journeys; `make fuzz`, benchmark targets and mutation testing do not add race/shuffle flags. Fuzz seeds also run in the normal race-enabled suite. Release builds use `CGO_ENABLED=0`; libraries are pure Go.
- **Coverage gate:** 75% total, 60% per package, 85% for the packages that decide what gets uploaded (`internal/raw`, `internal/state`, `internal/syncer`). The end-to-end runs count, so `main` and wiring are tested rather than excluded. `internal/testenv` (test fixtures) is excluded. A coverage-ignore comment must give a reason. Coverage only finds untested code; it doesn't prove the tests are good, so raise it as the code settles rather than chasing it with tests that assert nothing.
- **CRAP gate (`make crap`, go-crap):** for each function, `CRAP = complexity² × (1 − coverage)³ + complexity`, from gocyclo's cyclomatic complexity and the coverage `make cover` measured. Any function over 30 fails, the threshold Savoia and Evans set and go-crap and fallow use. A fully tested function scores its complexity, so up to 30 branches pass when tested, while an untested one fails from 6 branches up. This catches what the per-package coverage gate can't: complex code with no tests inside a package that is well covered overall, such as the timer installers before this gate. Functions without coverage data count as untested. go-crap's baseline mode isn't used: it fails on any rise in any function's score, even from 2 to 3, and ignores new functions. A lower threshold would fail fully tested functions over that complexity, which is what gocognit's backstop is for. The fix for a failure is tests for the branches; simplify only where that also reads better.
- **Mutation testing (`make mutation`, gremlins):** changes each covered line in small ways (a flipped comparison, a negated condition, a dropped `break`) and runs the package's tests; a mutant the tests don't catch is a line no test checks the behaviour of. It runs every mutator, excluding `internal/testenv`. gremlins can't pass `-race` or `-shuffle`, counts a mutant that doesn't compile as caught and doesn't mutate error checks. A full run takes about 20 minutes on 4 cores, so it isn't a gate. It measures coverage per package, so a line only another package's tests reach counts as not covered. A surviving mutant needs a test of the behaviour it changes, or is equivalent (it can't change behaviour), not a test written to kill it.
- **Mutation timeout coefficient:** gremlins derives each mutant's timeout from a coverage pass, which is about 0.65 s for `internal/sessioncapture` although the package takes tens of seconds, so the former fixed `--timeout-coefficient=3` timed out nearly every mutant there. `make mutation` now uses `MUTATE_TIMEOUT_COEFFICIENT` (default 100, which gave valid kill/survive results). Package-wide runs of `internal/sessioncapture` are slow because every mutant reruns the whole package suite; restrict with `--exclude-files='(^|/)name\.go$'` (the path-prefixed forms do not match).
- **Property, crash and privacy tests (`internal/sessioncapture`):** `model_history_test.go` plays seeded, workload-shaped histories against a contract-derived model (`-histories=N`, `-model-seed=S`; replay a failure with `-model-seed=<seed> -histories=1`, failures print a shrunk history). `crash_process_test.go` kills a child process with `os.Exit` at every fault stage and checks reopen, exact bytes, sequence monotonicity and that no writer temporaries remain. `privacy_canary_test.go` scans every backend write, raw and zstd-decoded, for a canary in several encodings.
- **Simulated sessions (`internal/syncer/simulate_test.go`):** seeded histories of what agents and machines do to session files (appends, half-written lines, rewrites, truncation, deletion, recompression, odd bytes, secrets) between sweeps and hook syncs, some of which fail, lose the bucket's answer or are killed part way. After each sync that completes, every file must rebuild from the bucket both through its manifest and without one, earlier bytes a change replaced must be kept as a version, and no secret may reach the bucket. `go test` plays 8 histories; `-simulations=200` plays more.
- **Fuzzing:** `make fuzz` and CI campaign the fourteen explicit `FUZZ_TARGETS` in `Makefile`: the nine legacy targets, `internal/scrub:FuzzEquivalentSecretCopies`, and candidate-path targets for publication current parsing (`FuzzParseCurrent`), manifest decode/validate (`FuzzDecodeValidateManifest`), the spool `quotaWriter` (`FuzzQuotaWriter`) and capture-hook JSON editing (`FuzzEditJSON`). Each campaign defaults to 30 seconds (`FUZZTIME`) with `-fuzzminimizetime=$(FUZZMINIMIZETIME)` (default 5s). Go's default 60 s minimization freezes all executions for a single worker while one new input is minimized; a measured run fell from thousands of execs/s to 0 for the rest of the run, while the bounded setting sustained its rate. CI keeps failing input as a build artifact. All declared fuzz seeds also run in ordinary tests.

## CI and releases

- `.github/workflows/ci.yml`: static checks on Linux; `make crap` (tests, coverage and CRAP) on Linux amd64, Linux arm64 and macOS arm64, plus govulncheck on macOS; a separate pinned-Python consumer job runs `make capture-acceptance` and complete analytics discovery; fuzzing; zizmor with its pedantic persona. Go jobs install the version on `go.mod`'s toolchain line through `.github/actions/setup-go` (setup-go's own `go-version-file` reads only the `go` line); the zizmor job does not need Go. Every job has a timeout.
- `.github/workflows/release.yml`: on a `v*` tag, the whole of CI runs first; only then does GoReleaser (`.goreleaser.yaml`) build linux and darwin binaries for amd64 and arm64 with reproducible flags, plus `checksums.txt`, SBOMs and `install.sh`, into a draft release.
- Release immutability is enabled on the repo: publishing a release locks its tag and assets and GitHub signs a release attestation, which `install.sh` verifies. So GoReleaser only ever uploads to a draft; publish it with `gh release edit <tag> --draft=false` after checking its assets.
- While hosted CI is unavailable, releases are cut locally instead: run `make check` and `make fuzz` on the tagged commit, push the tag, then from a clean checkout of it in `nix develop` run `GORELEASER_CURRENT_TAG=<tag> GITHUB_TOKEN=$(gh auth token) goreleaser release --clean`, check the draft and publish it. Name the tag explicitly: when a release candidate and its release share a commit, GoReleaser otherwise picks the candidate's tag and tries to upload to that published (and so locked) release. A local build proves the assets are what was published, not that they were built from the tag; the Nix package builds from source.
- Dependabot updates Go modules and Actions weekly, grouped, after a 7-day cooldown.
- Required checks on `main` need branch protection, which GitHub offers free on public repos.

## Git hooks (`lefthook.yml`)

Pre-commit formats staged Go files, runs `make lint` and `make tidy-check`, and lints changed workflows; pre-push runs the tests. The installed hooks call lefthook through `go tool -modfile=tools/go.mod`, and fail rather than let a commit through when they can't find it.

## For agents

Follow the applicable repository/agent instructions. The existing Claude tooling in `.claude/settings.json`:

- formats and lints each Go file Claude Code edits (`.claude/hooks/go-check.sh`), waiting its turn when edits run in parallel, and tells you when golangci-lint or jq is missing instead of skipping silently;
- stops it editing the gate configs;
- turns on the `gopls-lsp` plugin (diagnostics after every edit, and navigation) and the gopls MCP server in `.mcp.json` (search, references, a package's API, safe renames and diagnostics). Claude Code doesn't start plugin language servers in cloud sessions, but it does start `.mcp.json` servers, so cloud sessions get gopls through MCP. Both run the version pinned in `tools/gopls/go.mod`. On a fresh clone the MCP server first downloads and builds gopls, which took longer than Claude Code's default 30-second MCP startup limit in a cloud session, so `env` raises `MCP_TIMEOUT` to 3 minutes. Claude Code doesn't restart a local MCP server that failed to start; `/mcp` retries it.
