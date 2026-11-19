# Evidence 29: Codex rollout identity across archive, recompression and resume (brief 38)

Dates are UTC; the work ran on 2026-11-18 from 22:20Z to 23:36Z. Status: increment IDENTITY
accepted locally as `213e67b` (5 commits over `afabc2f`), with one deviation from the brief
(deletions, below). Nothing was installed, sent to R2 or run against the acceptance run's state
root. The acceptance run `20261117-a28bfa4` kept running on its own release binary throughout.

[Brief 38](../../38-rollout-identity.md) sits on `afabc2f` (on top of `main` `d123b3e`). The
probes that found the problem are in the brief.

## Commits

`git log --reverse afabc2f..213e67b`. "Worker" is a delegated Sonnet session (medium).

| Commit | By | Change |
|---|---|---|
| `12517f0` | worker | `SourceEntry::same_identity` lets an append source change path on `Extend` and `Epoch`; the fold stores the new path. Kind and class changes stay `SourceChanged`; `Replace` keeps its path check; `Deleted` unchanged |
| `93f2a1b` | worker | `codex_rollout`: a `.jsonl.zst` spelling has id `rollouts:<name>.jsonl.zst`, the plain spellings keep `rollouts:<name>.jsonl`; one owner for all four spellings |
| `89229df` | worker | walk: per (root, owner, id) only the lexicographically first root-relative path is yielded, others counted in `Counts::duplicate`; same rule in `of`; `memory check` prints `duplicate=N` after `unknown_owner=N` |
| `bf2f9fb` | worker | daemon scenarios in `s3_store/rollout_identity.rs` (three marked ignored, see deviation) |
| `213e67b` | worker | the three scenarios assert the landed state instead; no ignored test remains |

The path update happens on the cloned state inside `apply_changes`, so a refused change set
leaves the state untouched. A duplicate is skipped before it enters `known`, so `enumerate`, `of`
and `note` agree.

## Deviation accepted: deletions are not detected (existing gap, DESIGN 17 slice 3)

The brief expected the vanished file of a recompression or resume to be recorded `Deleted` after
two missing reconciles. The worker found, and the parent confirmed in the code, that this never
happens in the daemon: `capture_owner` and `reconcile_once` capture only the sources the walk
yields (`WalkSources::of` and `enumerate`, both requiring a plain file on disk), so
`absence_plan` and `Change::Deleted` are reached only in the in-memory capture rig
(`removed_source_is_deleted_only_after_two_observations`). This is the existing gap recorded in
briefs 30 and 31 ("deleted files are never recorded as deleted", `remote_only` informational);
DESIGN 17 places deletions in slice 3. Brief 38 does not add it.

Consequence, recorded in the tests: after a recompression, the owner's export holds the plain
source and the `.zst` source as two live `main` sources with the same decompressed content; after
a resume, the old `.zst` source stays live beside the plain one; after resume-following-
recompression, the plain source's earlier epoch is exported `sealed` beside the new `active` one.
The importer counts both; no capture fails. Deletions (slice 3) remove the stale source.

## Tests

- `core_manifest::apply_transition_rules`: `Extend` and `Epoch` with a new path apply and fold
  the new path; `Replace` with a new path and `Epoch` with a new kind are `SourceChanged`.
- `core_locator` Codex row and `core_f2_gaps::row7c_codex_plain_spellings_share_one_id_zst_spellings_another_all_one_owner`.
- `resolve::two_files_with_one_id_yield_the_first_path_and_count_a_duplicate` (enumerate, note,
  of), `resolve::plain_and_zst_are_two_sources_and_no_duplicate`.
- `s3_store::rollout_identity`, each asserting `failed=0 parked=0` in every round, then the real
  `memory check` (exit 0, `mismatch=0 uncaptured=0`) and the real `memory export`:
  `archived_then_appended_lands_under_the_archived_path`,
  `recompressed_rollout_lands_the_zst_beside_the_plain_source`,
  `resumed_rollout_lands_the_plain_source_beside_the_zst`,
  `resumed_after_recompression_gives_the_plain_source_a_new_epoch`,
  `plain_and_zst_at_once_both_land`, `sessions_and_archived_copies_at_once_land_once`
  (`duplicate=1`).

## Teeth (parent)

Script in the parent's scratchpad (`identity_teeth.py`); each tooth replaced exact anchors, ran
the core manifest, locator and F2 tests, `resolve`, and the `rollout_identity` and `check`
modules of `s3_store`, and restored the files.

| Tooth | Caught by |
|---|---|
| path check restored in `same_identity` | `apply_transition_rules`, `archived_then_appended_…` |
| fold keeps the old path | `apply_transition_rules`, `archived_then_appended_…` |
| `.zst` id equal to the plain id | `matrix_rows_match_independent_expectations`, `row7c_…`, `hegel_property7_…`, `plain_and_zst_are_two_sources_…`, four `rollout_identity` scenarios |
| dedupe removed in `enumerate` | `two_files_with_one_id_…`, `sessions_and_archived_copies_at_once_land_once` |
| dedupe removed in `of` | `two_files_with_one_id_…` |
| kept duplicate chosen by reverse path order | `two_files_with_one_id_…`, `sessions_and_archived_copies_at_once_land_once` |

6 of 6 caught.

## Checks on `213e67b`

`nix develop .#rust --command sh -c 'cd rust && MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python
PROPERTY_CASES=500 bash scripts/gates.sh'`: all 14 sub-gates 0, seed 1620338970; 04-nextest 772
passed, 05-hegel 32, 06-proptest 34, 10-consumer 2; coverage 96.55 % (9,881 of 10,234 lines).
The worker ran `RUSTDOCFLAGS=-Dwarnings cargo doc --workspace --no-deps` (clean) and
`cargo xtask schemas --check` (no drift).

## Limits

- Deletions are not detected (above); stale rollout spellings stay live until slice 3.
- The check line gained `duplicate=N`. The acceptance run's `a28bfa4` binary still prints the old
  line; its `checks.log` is read by eye, not parsed.
- Not run: 2,000 property cases, `cargo mutants` (operator decision), a run on real data with a
  recompressed rollout (this Mac had none on 2026-11-18).
