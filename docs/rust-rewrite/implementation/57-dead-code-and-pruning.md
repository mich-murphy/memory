# 57. Delete what does not run in production; prune stale index records (2026-11-25)

Coordinator brief. The parent session owns this file and acceptance. Evidence goes to
`evidence/52-dead-code/`. Text that states a plan stays as written; later changes are marked.

Production daemons run on this Mac (launchd) and dev-01 (systemd), both on `07bfc7d`. This brief
changes the capture binary; the parent installs it on both hosts after acceptance.

## Why

Operator, 2026-11-24: "cleanup dev-01 index record if not used", "prune retired records", "delete
anything that doesn't run in production, no dead code should be left behind". This overrides the
AGENTS.md wording that preserves config scrubbing; the credential denylist, the content scan
(gitleaks rules) and the argv allowlist still run in production and stay.

Read-only audits (parent, 2026-11-24, on `81e1f78`):

- Snapshot capture, `Profile::Scanned`, the content scan and the withheld judgement are live:
  Pi run records (`PiAux`, `sessions/<cwd>/subagent-artifacts/<name>.json`) are captured as
  snapshots and scanned whole.
- Dead in production: the Codex SQLite store backup (`rusqlite`, `sqlite_backup`,
  `Encoding::Sqlite`, the tmp-dir plumbing, `begin_snapshot`/`end_snapshot`, `Backup`,
  `SourceOutcome::Deferred`/`BackupFailed`, `LocalError::BackupFailed`), the 6 h store interval
  (`snapshot_at_ns`, `MEMORY_SIM_SNAPSHOT_INTERVAL`), `SnapshotFingerprint::Sqlite`,
  `WalFingerprint`, `check`'s `judge_store` and `CodexStore` branch, `Profile::Config`,
  `Profile::TextAsset`, `privacy/text.rs` and the config scrubbing in `privacy/document.rs` (no
  kept kind is a recognized config document).
- The whole Go tree and its tooling, `ci.yml`, `release.yml`, goreleaser, the Go-binary Python
  tools and the legacy analytics (events DB, R2 metadata, refresh, observatory, dashboard) do not
  run in production. Production analytics is `memory owners`/`export` then `publications.py
  import|import-shared|report` (docs/install.md).
- Stale `index.json` records: 8,195 records on this Mac, 6,172 on dev-01 (6.5 MB), against about
  6,080 and 4,531 checked sources. dev-01 keeps one record with `withheld` and `snapshot` set for a
  retired Pi run log (`subagent-artifacts/<…>.jsonl`); the brief 48 rule keeps any record with a
  snapshot, and its owner may have left the owner set (brief 54), so nothing ever drops it.
- `resolve::retired` relies on the locator answering `Ignored`; a kind-based test does not.

## Decisions

| Topic | Decision |
|---|---|
| Retired test | One pure function on a stored entry: retired unless its kind is in the captured set (`CAPTURED_KINDS`, plus `PiAux` only for the run-record shape of its stored path). Used by `Probe::Retired`, `all_gone`, `check`'s `retired=` and the prune. No root lookup. The worker reports every stored path shape of a kept kind that today's locator maps to `Ignored` (where the two tests would disagree); on both hosts `check`'s `retired=` must stay 1,791 and 1,492 |
| Prune rule | After a round whose walk was complete, and only when every owner in the owner set has a `Known` or `Absent` base: drop each local record whose id was not walked and is not a live (no `deleted_at`), non-retired entry of any owner's base. Replaces the brief 48 two-walk rule. Kept sources that vanished keep their record (D52's two misses unchanged); a round with any `Unknown` base prunes nothing. `dirty` is set so `index.json` is rewritten |
| Dead Rust code | Deleted with its tests: everything in the "dead in production" bullet above. Tests that used a dead path to reach live behaviour are rewritten through a kept kind (Pi run record for snapshots/scan, transcripts for append). No test of live behaviour is deleted |
| Locator ignore tables | Kept: they run on every walk and decide the `ignored=` / `unknown_kind=` counters |
| `index.json` compatibility | No forced rebuild. This release loads the current file: a record with the dead `snapshot_at_ns` key or a `Sqlite` fingerprint loads (the key ignored, the fingerprint as no snapshot) through one shim marked `BRIEF57-SHIM`, and is written back without them. The shim is deleted in a follow-up commit once both hosts have run this release (their files then hold neither) |
| `rules_digest` | Changes with the scrubbing removal; stored Pi run records replay once (59 small files on dev-01). Accepted |
| `check` reads | Unchanged apart from deleting `judge_store`; held for discussion with the operator |
| Go and legacy | Deleted: `cmd/`, `internal/`, `go.mod`/`go.sum`, `tools/go.*`, `tools/gopls/`, `Makefile`, `install.sh`, `lefthook.yml`, `.testcoverage.yml`, `.golangci*`, `.goreleaser.yaml`, `.github/actions/setup-go/`, `ci.yml`, `release.yml`, `.claude/`, `.mcp.json`, `.agents/skills/verify-agent-sessions/`, `tools/capture-delivery/`, `tools/candidate-*`, `tools/performance/`, and in `tools/analytics` every module and test outside the production workflow (including `serve.py`, `query.py`, `publication_dashboard.py`, `site/`, `projection_access.py`, `publication_refresh.py`, `bodies.py`, `r2_metadata.py`, `analyze.py`, `classify.mjs`, `report.sql`, `views.sql`, the legacy half of `events.py`, `export_session`, `WriterBacked`); `boto3` leaves `requirements.txt` |
| CI | `rust.yml` gains the analytics unit suite and workflow linting (zizmor, actionlint) that only `ci.yml` ran; `dependabot.yml` loses the Go ecosystems |
| Flake | Go outputs and tools removed; `default` becomes the Rust shell (plus what `rust/README.md` needs); `coverage` points at it |
| Docs | Go-only docs get a one-line historical banner, not deletion; dated evidence is untouched |

## Increments

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | RUST: retired test, prune, dead Rust code, shim | `rust/dead-code` | worker (Sonnet, medium) |
| 2 | LEGACY: Go tree, legacy tools, CI, flake, docs banners | `legacy/remove-go` | worker (Sonnet, medium) |
| 3 | Shim removal after both hosts ran increment 1 | `rust/drop-shim` | parent |

Ownership: RUST owns `rust/` (crates, `Cargo.*`, `deny.toml`, scripts). LEGACY owns everything
outside `rust/` except `AGENTS.md`, `DESIGN.md`, this brief, evidence and the handoff (parent).
LEGACY keeps `tools/analytics/test_rust_export_journey.py` and everything the consumer gate
reaches working, and may run the consumer gate (read-only use of `rust/`). RUST rewords `rust/`
comments that cite Go paths deleted by LEGACY.

Parent, local only (no commit): remove merged worktrees, rebuildable scratch data, old binary
backups (keep the previous release), old dev-01 build directories, the acceptance state
directories and build targets, and dangling container images. Kept: logs and READMEs that
evidence cites, pre-cutover config backups, and `~/.local/share/agent-sessions` on both hosts
(Go-era archive backups that may hold old session bytes no local source has; operator decision).

## Acceptance (parent)

Read every diff. Teeth: retired test widened or narrowed (a kept kind retired, a non-run-record
`PiAux` kept), prune without the complete-walk gate, prune with an `Unknown` base, a vanished kept
source's record pruned before its deletion, shim removed (old index fails to load). Full gates,
Linux container, hosted CI, the consumer gate, an old-format `index.json` loading in a test. Then
install on both hosts and measure: start round (no `Deleted`, no WARN/ERROR), `index.json` record
counts before and after, the dev-01 withheld record gone, `memory check` exit 0 with `retired=`
unchanged, peak memory. Then increment 3 and its install.

## Ledger

- 2026-11-25: brief written on `81e1f78`.
- 2026-11-25: LEGACY finished in 5 commits; RUST in 6, then the parent inlined `judge_source`
  (`1b1726c`). A read-only sweep found unused `pub` items; a sweep worker deleted them (5 commits).
  The parent found `document.rs` 40 % covered with `args_scrub` its only caller, showed with a
  20,000-case differential test that the scrub never changes `sanitize_launch` output, and
  deleted it with `MalformedProtected` (`ca1070e`). LEGACY rebased onto RUST. Teeth 11 of 12 (1
  equivalent), gates 14/14, Linux passed. Accepted locally at `4bc0a86`. Details in evidence 52.
