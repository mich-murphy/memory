# agent-sessions

A Go command-line collector that copies Claude Code, Codex and Pi session data into a Cloudflare R2 bucket. It runs from agent hooks on many machines, so it must be fast, never fail an agent, and never lose or corrupt data.

- Layout: `cmd/agent-sessions` (entry point and end-to-end tests), `internal/*` (one package per concern: `adapters` per agent, `syncer` decides and runs uploads, `state` is the local SQLite record, `raw` is hashing, pieces and zstd, `store` is R2 or a local dir).
- The bucket layout is a contract other stages read: `docs/storage-layout.md`. Don't change a key, field or format without updating that doc and saying so in the PR.
- Hook behaviour per agent, with sources: `docs/hooks.md`.
- Naming rules, with sources: `docs/naming.md`. Follow them in every change.

## Working on the code

The gopls MCP server (`.mcp.json`) sees the code as the type checker does. Prefer it to grep:

- `go_search` and `go_package_api` to find symbols and read a package's API; `go_file_context` after reading a file for the first time.
- `go_symbol_references` before changing any declaration, then update every reference.
- `go_rename_symbol` for renames.
- `go_diagnostics` on the files you changed after each edit, before running tests.

## Before you finish

Run `make check`. It runs formatting, lint, `go fix`, tidy, deadcode, govulncheck, the race-enabled tests, the coverage gate and the CRAP gate, as CI does. Explanations of every gate: `docs/quality-gates.md`.

## Rules

- Never edit `.golangci.yml` or `.testcoverage.yml` to get a change through. Fix the code, or ask.
- Never add `//nolint` or a coverage-ignore comment without naming the linter and giving a real reason.
- Never split a function just to get under the complexity limit. Restructure when it reads better; otherwise leave it and say why.
- When the CRAP gate fails, test the function's branches; simplify it only where that also reads better.
- Handle every error. Ignore one only visibly (`_ = f.Close()`) with a comment saying why it's safe, and never on the write path.
- Files and directories the collector creates hold transcripts: 0600 and 0700.
- Tests: standard `testing` and go-cmp, no assertion libraries. A new behaviour gets a test that would fail without it. Don't write tests that only raise coverage.
- R2 credentials come only from the environment or the config file. Never print, log or commit them.
