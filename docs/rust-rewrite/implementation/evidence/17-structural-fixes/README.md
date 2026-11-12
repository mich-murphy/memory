# Evidence 17: structural fixes (plan step 3 of report 25, first batch)

Date 2026-11-12. Branch `rust/structural-fixes`, tip `6fd536a`, on top of
`docs/walking-skeleton` (`27a071b`). Local only, nothing pushed. Over `27a071b` the branch
changes 22 files under `rust/`, +376/−366.

## Commits

| Commit | By | Change |
|---|---|---|
| `87fe671` | worker | `profile_of` names all 30 `SourceKind` variants; the wildcard arm is gone. The mapping is unchanged. |
| `88888e1` | worker | `FileSystem::stat_len` removed (no production caller), with `RealFsError::Absent`, which lost its only constructor. Every removed assertion had a `stat` assertion of the same fact in the same test. |
| `cf91439` | worker, README rewrapped by parent | `Counting` and its `Counters` moved from `environment.rs` into `tests/counters.rs`, their only user. `realfs::Counters` is untouched. |
| `d877196` | worker | One `LaunchRecord` and one launch digest (`memory-core/src/launch.rs`), re-exported from `hint` and `manifest`. The digest is the hint's (`memory.launch/1`, length-prefixed); `from_sanitized` cannot fail. The manifest JSON shape is unchanged; only a schema `description` string changed. |
| `baf8b1a` | parent | `Daemon::land` and `apply_landed` take `&Owner`, not `Option<&Owner>`: attaching launch records cannot fail any more, so the `None` case had no caller. `EventCounts.launch_attach_failed` is removed for the same reason (see the note on amendment A11.3). |
| `4eea7ab` | parent | Bug fix, see below. |
| `6fd536a` | parent | The generated-launch property (fixed shapes, Hegel, proptest) now also checks that sanitising a sanitised launch changes nothing. The daemon does that second pass before attaching a hint's record. |

## The bug fixed in `4eea7ab`

Amendment A13.3 removed the cap of 8 on allowlisted environment names; the sanitiser publishes
all nine. The manifest check still rejected a launch record with more than 8 names
(`schema.rs`, `env.len() > 8`; the same line is on `main` at `41a07ff`, `schema.rs:464`). A
launch that set all nine names passed sanitising and then failed `build` with `BadLaunch`,
which fails the owner's commit, not only the launch record. Shown before the fix in
`teeth.txt` (first block).

The two existing bound tests could not see this. They changed `argv` or `env` in a built
manifest without recomputing the digest, so they passed on the digest mismatch whatever the
bound was.

Now the manifest takes both bounds from the sanitiser (`LAUNCH_ARGV_MAX` = 32,
`LAUNCH_ENV_MAX` = the allowlist length). The bound tests build records whose digest matches,
computed by a second implementation in the test: 32 argv with 9 names parses, 33 argv is
rejected, 10 names is rejected. A new test sends a launch with all nine names through
`build` and `parse_manifest`.

## Checks

- `gates/`: full local gates on `6fd536a`, 500 cases, seed 3194196348. All 13 sub-gates 0;
  472 tests (414 nextest, 30 Hegel, 28 proptest), 12 doctests, coverage 6031/6170 = 97.75 %,
  the three mutants and the window mutant killed, schemas unchanged.
- Rustdoc with `-Dwarnings` is clean on `6fd536a` (run by hand; no gate runs it).
- `verify-evidence.py` passes.
- `teeth.txt`: each new or changed test fails when the code it guards is changed back.
  One worker run passes by design: with a single digest function, a digest that ignores the
  environment still agrees with itself, so that test cannot detect it. The hint canary and
  manifest tests cover the digest's content.
- Not run: 2,000 cases, hosted CI, `cargo mutants`.

## Left from plan step 3

`coordinates.rs` (wire or delete, decided with the run loop), the parked-owner sort, the
daemon's direct `std::fs` hint reads, `recover_base` delegating to `read_owner`, rustdoc in a
gate. `docs/rust-rewrite/implementation/07-manifest.md:29` still says "env ≤ 8"; it is a
dated report and is left as written.
