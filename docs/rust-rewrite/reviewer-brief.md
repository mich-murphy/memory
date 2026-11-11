# Shared brief for reviewers (read fully before starting)

Repo: /Users/mm/dev/memory (Go 1.27, ~36k LOC non-test, ~62k LOC tests, 430 commits since 2026-10-10).
Product: "agent-sessions" captures Claude Code / Codex / Pi session evidence on two machines (Mac, BC-01),
publishes complete per-session publications to Cloudflare R2 (S3 API), and serves one authoritative reader
used by restore and by a Python analytics consumer (tools/analytics, DuckDB).

The operator intends a REWRITE IN RUST. Your job is NOT to fix anything. It is read-only review producing
a written report that a design author will use. Do not edit repository files. Do not run live ingestion,
installs, network or anything touching ~/.config, ~/.local, R2, BC-01. You may run `go test` only for a
single small package if it answers a question; prefer reading. Never print transcripts, identities or credentials.

Authoritative direction: docs/session-capture-contract.md (read it first, it is 210 lines).
Status: docs/session-capture-handoff.md "resume here" section (first ~140 lines). The candidate path
(`agent-sessions capture-v1`) is running live on both hosts, merged main = 57d8400, not yet accepted.
Legacy path (sync/backfill, internal/syncer etc.) still exists as defaults.
Map of docs: docs/README.md.

Report rules:
- Write your report to the path given in your task. Markdown. Lead with findings, then evidence.
- Every claim must cite file:line (or doc section / commit hash). Label each claim measured (you read/ran it),
  inferred, or guess. Do not pass through doc claims as your own measurement; say "doc says".
- Short declarative sentences. No em dashes. No colons as mid-sentence connectors. Sentence-case headings.
- Be candid and specific. "The abstraction X leaks because Y at file:line" beats generic advice.
- Where you recommend something for the Rust rewrite, state what in the Go code motivates it.
- Finish with "Open questions for the operator" (things only a product owner can decide) and
  "Things I could not verify".
