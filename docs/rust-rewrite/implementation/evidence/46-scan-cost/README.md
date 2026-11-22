# Evidence 46: the cost of the gitleaks scan (brief 51)

All dates are UTC, 2026-11-22. Status: increment SCANCOST accepted locally at `1b1d5e8`, which is 3
worker commits over the brief `41bb532` on `main` `6510ff7`. During the work nothing was installed
and nothing was sent to R2. The worker read no production state root and no real agent data. Both
production daemons kept running on `6510ff7`.

[Brief 51](../../51-scan-cost.md) holds the finding from evidence 45 and the scratch measurements
behind the decisions.

## Commits

| Commit | By | Change |
|---|---|---|
| `220f570` | worker | The keyword prefilter uses `memchr::memmem::find`, and an empty keyword never matches. `memchr` 2 is now a direct dependency of `memory-core` through `[workspace.dependencies]`. `Cargo.lock` changes by one line, the `memchr` edge of `memory-core`, with no new package and no version change |
| `fabb848` | worker | Each rule pattern is compiled once at load to validate it (a failure is still a table failure), and the compiled regex is then dropped. The rule's regex is compiled again at the first text whose keywords match and kept in a `OnceLock`. `Table::detect` returns `Detect::{Found, Clean, Failed}`, and the scan maps `Failed` to `ScrubFailed`. `dfa_size_limit` is 256 KiB. Two test-only hooks (`#[cfg(test)]`) were added: `Table::compiled()` and `failing_table()` |
| `1b1d5e8` | worker | Module docs |

Deviation, accepted: `compile` sets the 256 KiB DFA cap, so the allowlist regexes get it too, not
only the rule regexes. The cap is not semantic and makes memory slightly smaller. No existing test
expectation changed. `TABLE_VERSION` stays `scan/2` and the `rules_digest` golden is unchanged, so
nothing is re-judged after install.

## Parent acceptance

- Whole diff read.
- Teeth, 10 of 10 caught (`scancost_teeth.py`, the same `memory-core` filter as brief 50,
  baseline 50 tests):
  - prefilter inverted
  - prefilter dropped
  - `memmem` result inverted
  - an empty keyword matching
  - eager compile (regex held after load)
  - load-time validation skipped
  - a lazy failure returning `Clean`
  - `Failed` mapped to publish
  - `detect` ignoring `Failed`
  - the unescape pass dropped

  The DFA cap is measured, not tested.
- Full gates on `1b1d5e8`: 14 of 14. Seed 3088399108, nextest 911 passed, Hegel 33, proptest 36,
  consumer 3, line coverage 95.64 %.
- Linux container (Ubuntu 24.04): `memory-core` lib 22, `privacy_scan` 13, `privacy_port` 16, and
  `memory` `s3_store` 134 (1 ignored), all passed.
- `cargo deny` passes (worker run).

## Measurement (scratch crate, real `admit` and `transform`, one thread, counts only)

Every `Profile::Scanned` file on each host was judged by builds of the two trees. The crate prints
the resident set size before and after, the CPU time, the withhold count, and a hash over the sorted
(path, verdict) list. The paths stay in memory and only the hash is printed. Two runs each; the
figures below are the range.

| | This Mac, `6510ff7` | This Mac, `1b1d5e8` | dev-01, `6510ff7` | dev-01, `1b1d5e8` |
|---|---|---|---|---|
| Files judged | 2,108 | 2,108 | 1,639 | 1,639 |
| Withheld | 3 | 3 | 26 | 26 |
| Verdict hash | `8276e8b56d25d57b` | same | `d0f3b4783135d478` | same |
| Resident added after the walk | +21.9 to 22.1 MB | +12.6 MB (3.6 MB table, 9.0 MB caches) | +29.6 to 29.7 MB | +19.6 to 20.1 MB (2.7 MB table) |
| CPU | 11.6 to 12.0 s | 1.0 s | 43.5 s | 5.9 to 6.0 s |

The brief's target (at most +13 MB and 1.5 s, the same withholds) is met on this Mac, where it was
set. On dev-01 the verdicts are equal file for file and the CPU falls by a factor of 7. The added
memory falls by 10 MB but is above 13 MB, because dev-01's scanned files are larger Pi run logs. The
acceptance criterion is the daemon's peak below 128 MB on both hosts (install below).

## Merge, install on both hosts

- Stack #250 (PRs #247 to #249) merged as `ecb1d74`. Rust gates passed on all three PRs; CI failed
  only in the two accepted `govulncheck` steps.
- This Mac: installed in place at 06:11:50Z (sha256 prefix `abb1688a53a58c37`, `runs = 15`), with
  the `6510ff7` binary kept as `backup/memory-6510ff7`. Start round at 06:16:10Z: `landed=2
  failed=0 local=0 deleted=0 withheld=0` (the digest did not move, so nothing was judged again),
  `tmp/` empty, no WARN or ERROR line. `memory check` 06:26Z to 06:51Z: exit 0, 1,170 owners, 8,180
  sources, 8,177 match, 0 mismatch, 0 uncaptured, `remote_only=0`, `store_error=0`; withheld
  file_history 2, claude.memory 1 (`secret_in_text`). Two timer rounds ran during the check. Peak
  footprint over the start round, both timer rounds and the check: 82 MB (232 samples; it was
  130 MB on `6510ff7` in the same situation).
- dev-01: `ecb1d74` built on the host (sha256 prefix `f00659da2d3510da`); both Nix store
  dependencies are the pinned GC roots. Installed at 06:19:11Z with `backup/memory-6510ff7`. Start
  round: `landed=0 withheld=0`, `NRestarts=0`, `tmp/` empty, no WARN or ERROR line. `memory check`
  06:21Z to 06:26Z: exit 0, 454 owners, 5,971 sources, 5,945 match, all other counts 0; withheld
  file_history 4, pi.aux 12 (`secret_in_text`), pi.aux 10 (`source_limit`). `VmHWM` after the
  check: 109,016 kB.

Both peaks are below the 128 MB backlog target of DESIGN section 15. Exit criteria met.

