# Evidence 44: the whole archive exports, imports and reports (2026-11-21)

All times are UTC. Operator requirement (2026-11-20, handoff item 14): analytics matter only as
tested workflows, and the saved data must be queryable. After briefs 45 to 49 added new kinds
(Codex stores, file-history, memory notes, aux files, config assets), this run checks the real
archive end to end on this Mac with the installed `30002d9` binary and `main` `7f7c9d1`. Nothing was
written to R2. Output went to a private scratch directory (mode 700) and was deleted afterwards
(19 GB). Only counts, exit codes and closed codes were printed.

## Method

1. `memory owners` (from `~`, production state root, read-only on R2): 1,618 owners, all
   `sessions/…`. Both hosts share one archive id, so this covers this Mac and dev-01.
2. A scratch script ran `memory export <owner> --out <dir>` for every owner, then
   `tools/analytics/publications.py` `import_export` of each export into one DuckDB, then a
   second import of every export. The script from evidence 31 was updated for the current export
   line (`deleted=` replaced `skipped_deleted=` in D52).
3. `publication_dashboard.PublicationDashboard(db).report()` and `sessions()` over that database.
4. Counts by `source_role` and by masked `original_path` shape (known directory names only).

## Results

| Step | Result |
|---|---|
| Export | 1,618 of 1,618 exit 0, no failure code; 13,299 sources, 0 deleted, 7,962,795,606 bytes; 2,133 s |
| Import | 1,618 `imported`, 0 failures; 13,299 sources, 1,770,753 events, 91,519 issues; 261 s |
| Reimport | 1,618 `unchanged`; 46 s |
| Database | 6 tables; `sessions` 1,618 (claude 594, codex 167, pi 857; normalization complete 1,016, partial 602); `sources` 13,299, one row per (session, source, epoch); `events` 1,770,753 |
| `report()` | totals: 1,618 sessions, 13,299 sources, 0 failures; `withheld` 0 in the projection; 3 agents, 3 source roles; `sessions()` answers |
| Source roles | subagent 7,027, artifact 4,888, main 1,384 |

New session-owned kinds reached the projection with counts equal to the captures on both hosts:

| Kind (masked `original_path` shape) | Sources | Captured (this Mac + dev-01) |
|---|---|---|
| Claude `file-history/<x>/*` (D58) | 1,224 | 805 + 419 |
| Claude `projects/<x>/subagents/*.json` aux (D60) | 1,694 | 940 + 754 |

Other shapes: Pi `sessions/<x>/tasks/*.jsonl` 5,234, Claude subagent transcripts 1,584, Pi main
transcripts 1,048, Claude tool results 1,962 (`txt`, images, `pdf`, `html`, `json`, `bin`), Claude
main transcripts 360, Codex rollouts 167, Pi forks 18, Claude session `txt` 4, Claude debug 1.

## Limits

- `memory owners` and `memory export` cover session owners only. Shared owners (Codex stores,
  Claude memory notes, plans, config assets) are restorable (evidence 39 and 43) but are not in the
  `session-reader/v2` projection, so they are not queryable through analytics.
- `original_path` holds the root-relative path; `path` is the export file name.
- Run once on this Mac; not rerun on dev-01, whose sessions are in the same archive and were
  exported here.
