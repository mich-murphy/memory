# 58. Remove the remaining Go code and references; settle the watcher test (2026-11-25)

Coordinator brief. The parent session owns this file and acceptance. Evidence goes to
`evidence/53-go-references/`.

## Why

Operator, 2026-11-25, on brief 57: "ensure all go code and any references to it are cleaned up, we
should no longer need them"; on the failing watcher test: "evaluate whether the flaky test is
tautological or provides value. if it is valuable then fix it, otherwise remove".

State on `ac918a5` (counts): Go code left is one prototype probe
(`docs/rust-rewrite/prototypes/p11-commit-retry/probe/{go.mod,main.go}`). Go references: 47 docs
outside `docs/rust-rewrite` (31 carry a historical banner), `DESIGN.md` (Go source paths in 8.2 to
8.4), `AGENTS.md`, `README.md`, and in `rust/` the `.agent-sessions-backup-` suffix in the
credential denylist (with three test examples) and a foreign-hook fixture named `agent-sessions`.
Outside the repo: GitHub releases v0.1.0 to v0.5.0 (Go binaries) and their tags, Dependabot PR
#266 (Go modules), four unmerged Go-era branches, Go installer backups beside the agent configs
(1+1 here, 4+4 on dev-01), Go-era files in `~/.local/state/memory-cutover/backup` on both hosts,
and Go caches on dev-01 (`~/go`, `~/.cache/go-build`; no `go` on its path).

## Decisions

| Topic | Decision |
|---|---|
| Go code | None left: the P11 probe is deleted (its record names the commit that held it) |
| Go-only docs | Deleted, not bannered: they describe only deleted code and stay in git history |
| Live docs | No Go reference in instructions, guides, contracts or design. Contracts the importer still consumes (`session-publication-v1.md`, `session-analytics-v1.md`) are rewritten to name `memory export` as the reader |
| Dated records | Handoff ledger items, `docs/rust-rewrite/**` briefs, evidence and reports, and `docs/research/**` keep their historical mentions: rewriting dated evidence would falsify it |
| `agent-sessions` | The R2 bucket keeps its name (production data, not Go code) |
| Denylist suffix | `.agent-sessions-backup-<n>` leaves the backup-suffix pattern once no such file exists on either host; tests use other suffixes |
| GitHub | Releases and tags v0.1.0 to v0.5.0 deleted (tag commits recorded in evidence); #266 closed; Go-era branches and merged branches deleted |
| Watcher test | Diagnose first. Kept and fixed if it is the only test of real backend behaviour; removed if tautological. No timeout is raised to make it pass |

## Increments

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | DOCS: delete Go-only docs, rewrite live docs | `docs/remove-go` | worker (Sonnet, medium) |
| 2 | RUST + DESIGN + probe + watcher test | `rust/go-refs` | parent |

DOCS owns `docs/` except `docs/rust-rewrite/**` and `docs/session-capture-handoff.md`, plus
`README.md`. The parent owns everything else.

## Ledger

- 2026-11-25: brief written on `ac918a5`.
- 2026-11-25: increments accepted at `216c3d2`: full gates 14/14 (seed 1136253774, 95.58 %),
  4 of 4 teeth caught; evidence 53. Host and GitHub cleanup done (evidence 53).
- 2026-11-25: merged as `befdd0a` (stack #293) and installed on both hosts; `memory check`
  exit 0 on both, all sources match (evidence 53, Install).
