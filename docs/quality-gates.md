# Quality gates

Every gate here runs in CI on each push and pull request, and locally through `make`. The rule behind the settings: **a check is a hard gate only if fixing what it flags always makes the code better.** Bug, safety and error-handling checks pass that test. Size and style metrics don't, because the cheapest way to satisfy them is often an arbitrary split or rename, so they are either off or set high enough to act as backstops.

The research behind these choices (tool versions, the configs of 13 well-regarded Go projects, and each tool's docs) is in the project's `research/collector-language/go-tooling.md`.

## Commands

| Command | What it runs |
|---|---|
| `make check` | Everything below except zizmor (CI only) and fuzzing |
| `make fmt` | gofumpt and goimports, through `golangci-lint fmt` |
| `make lint` | golangci-lint with `.golangci.yml` |
| `make test` | `go test -race -shuffle=on ./...` |
| `make cover` | Tests with the race detector, plus end-to-end runs of the built binary, merged into one profile and checked against `.testcoverage.yml` |
| `make crap` | `make cover`, then the CRAP gate on that profile |
| `make fuzz` | Each fuzz target for `FUZZTIME` (default 30s) |
| `make hooks` | Installs the git hooks in `lefthook.yml` |
| `make gopls` | Installs the pinned gopls, for the Claude Code `gopls-lsp` plugin |

Tools other than golangci-lint are pinned in their own module, `tools/go.mod`, and run with `go tool -modfile=tools/go.mod <name>`, so they never touch the collector's `go.mod` and Dependabot updates them (it only reads files named `go.mod`). gopls has a module of its own, `tools/gopls/go.mod`, because it needs a different `golang.org/x/tools` from deadcode's. golangci-lint is installed as a binary, as its maintainers recommend, at the version in `.golangci-lint-version`: CI installs that version, and `make lint` fails when the local one differs.

## Toolchain

Go 1.27 builds and tests; `go.mod` says `go 1.26.0`, the oldest supported release and the minimum the SQLite driver needs. Binaries built with Go 1.27 run on macOS 13 or later.

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
- `deadcode -test ./...` fails on functions nothing reaches from `main` or a test, and on code that doesn't load. `-test` stays because `raw.Join`, the manifest reader, is how tests prove a manifest rebuilds its file.
- `govulncheck ./...` fails on a known vulnerability in code the collector calls. It only sees code built for the OS it runs on, so CI runs it on Linux and on macOS.
- `actionlint` and zizmor check the GitHub Actions workflows. Every action is pinned to a commit SHA, credentials aren't persisted after checkout, and permissions are read-only unless a job needs more.

## Tests and coverage (`.testcoverage.yml`)

- Tests use the standard `testing` package and go-cmp; no assertion library. The command line is tested end to end with testscript (`cmd/agent-sessions/testdata/script`) against fake Claude Code, Codex and Pi homes. The S3 store is tested against gofakes3 in-process, and against real R2 when `AGENT_SESSIONS_TEST_R2=1` and R2 credentials are set.
- Every test run uses the race detector (which needs cgo) and shuffled order. Release builds use `CGO_ENABLED=0`; every library is pure Go.
- **Coverage gate:** 75% total, 60% per package, 85% for the packages that decide what gets uploaded (`internal/raw`, `internal/state`, `internal/syncer`). The end-to-end runs count, so `main` and wiring are tested rather than excluded. `internal/testenv` (test fixtures) is excluded. A coverage-ignore comment must give a reason. Coverage only finds untested code; it doesn't prove the tests are good, so raise it as the code settles rather than chasing it with tests that assert nothing.
- **CRAP gate (`make crap`, go-crap):** for each function, `CRAP = complexity² × (1 − coverage)³ + complexity`, from gocyclo's cyclomatic complexity and the coverage `make cover` measured. Any function over 30 fails, the threshold Savoia and Evans set and go-crap and fallow use. A fully tested function scores its complexity, so up to 30 branches pass when tested, while an untested one fails from 6 branches up. This catches what the per-package coverage gate can't: complex code with no tests inside a package that is well covered overall, such as the timer installers before this gate. Functions without coverage data count as untested. go-crap's baseline mode isn't used: it fails on any rise in any function's score, even from 2 to 3, and ignores new functions. A lower threshold would fail fully tested functions over that complexity, which is what gocognit's backstop is for. The fix for a failure is tests for the branches; simplify only where that also reads better.
- **Fuzzing:** transcript heads (`internal/adapters`), hook input (`internal/hook`), JSON settings (`internal/jsonx`), zstd round trips (`internal/raw`), shell quoting (`internal/shellwords`) and credential stripping from git remotes (`internal/gitinfo`). Seeds run with every `go test`; CI fuzzes each for 30 seconds and keeps any failing input as a build artifact.

## CI and releases

- `.github/workflows/ci.yml`: static checks on Linux; `make crap` (tests, coverage and CRAP) on Linux amd64, Linux arm64 and macOS arm64, plus govulncheck on macOS; fuzzing; zizmor with its pedantic persona. Every job installs the Go on `go.mod`'s toolchain line through `.github/actions/setup-go` (setup-go's own `go-version-file` reads only the `go` line) and has a timeout.
- `.github/workflows/release.yml`: on a `v*` tag, the whole of CI runs first; only then does GoReleaser (`.goreleaser.yaml`) builds linux and darwin binaries for amd64 and arm64 with reproducible flags, plus `checksums.txt` and SBOMs.
- Dependabot updates Go modules and Actions weekly, grouped, after a 7-day cooldown.
- Required checks on `main` need branch protection, which GitHub offers free on public repos.

## Git hooks (`lefthook.yml`)

Pre-commit formats staged Go files, runs `make lint` and `make tidy-check`, and lints changed workflows; pre-push runs the tests. The installed hooks call lefthook through `go tool -modfile=tools/go.mod`, and fail rather than let a commit through when they can't find it.

## For agents

`CLAUDE.md` holds the rules for coding agents. `.claude/settings.json`:

- formats and lints each Go file Claude Code edits (`.claude/hooks/go-check.sh`), waiting its turn when edits run in parallel, and tells you when golangci-lint or jq is missing instead of skipping silently;
- stops it editing the gate configs;
- turns on the `gopls-lsp` plugin (diagnostics after every edit, and navigation) and the gopls MCP server in `.mcp.json` (search, references, a package's API, safe renames and diagnostics). Claude Code doesn't start plugin language servers in cloud sessions, but it does start `.mcp.json` servers, so cloud sessions get gopls through MCP. Both run the version pinned in `tools/gopls/go.mod`. On a fresh clone the MCP server first downloads and builds gopls, which took longer than Claude Code's default 30-second MCP startup limit in a cloud session, so `env` raises `MCP_TIMEOUT` to 3 minutes. Claude Code doesn't restart a local MCP server that failed to start; `/mcp` retries it.
