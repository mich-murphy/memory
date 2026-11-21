# Evidence 41: Claude file-history behind a fail-closed content scan (brief 47)

All dates are UTC, 2026-11-21. Status: increment FILEHISTORY accepted locally at `68043e3`. That is
5 worker commits and 3 parent commits over `f9fc20c`. During the work nothing was installed,
nothing was sent to R2, and nothing read a production state root or real agent data. Both
production daemons kept running.

[Brief 47](../../47-file-history.md) sits on `f9fc20c`, on top of `main` `6f5139f`. Reason: D54
captures file-history only behind a scan that withholds the whole file on a secret, binary or
non-UTF-8 content, an oversize file or a scanner error. The names are hashes, so no path denylist
can see what a file holds.

## Commits

| Commit | By | Change |
|---|---|---|
| `a87d600` | worker | `Profile::Scanned` (never prefix-stable, own binding byte, needs the current rules digest), `privacy/scan.rs`, `Reason::Binary`, `WithholdReason::Binary`. The scanner table (`scan/1`), the 1 MiB cap and every pattern go into `rules_digest()` |
| `3fd3a26` | worker | The walk yields `ClaudeFileHistory` with profile `Scanned`; every other non-store snapshot kind stays `deferred_snapshot`. A file over the cap is withheld from its stat, before any read |
| `6c84d00` | worker | `SourceIndex.withheld` (`#[serde(default)]`: fingerprint, rules, reason). The same pair gives `Unchanged` without a read. A landed snapshot clears it. The round line gets `withheld=`, and `memory status` counts a recorded withhold as `withheld` |
| `fd79f9a` | worker | Real binary against the fake S3 store: clean, secret and binary file-history files, then reconcile, status, check, a second reconcile, restore and export. The output never holds a file name, the secret, a content byte or the temporary path |
| `8b5be01` | worker | Module docs |
| `1359290` | parent finding | `Update::Present` moves above the scanned early returns. Before, a cached withhold returned without resetting the absence count: a file missing once and then back with the same fingerprint kept `missing=1`, so the next single miss would have recorded a deletion. Test `a_cached_withhold_still_resets_the_absence_count` fails without the fix |
| `6e70050` | parent finding | The generic assignment search restarts at the start of the last value. `captures_iter` does not overlap, so in `args: --token=…` or `url=…?api_key=…` the outer name consumed the inner assignment and its secret name was never judged. Test `an_assignment_nested_in_a_value_is_withheld` fails without the fix |
| `68043e3` | parent finding | Test for the reused shell guard, the only rule that sees a value under 8 characters and fish's `set -gx NAME value`. Without it, dropping the guard survived every test |

### Scanner (as built)

Order, first hit wins:

1. Longer than 1 MiB: `source_limit`. The daemon decides this from the stat and never reads the
   file.
2. A NUL byte or not valid UTF-8: `binary`.
3. A fixed pattern: PEM, OpenSSH and PGP private-key headers; AWS key ids; GitHub tokens; Slack;
   Anthropic and OpenAI keys; Google API keys; Stripe live keys; JWTs; credentials in a URL. The
   reason is `secret_in_text`.
4. The shell-assignment guard of `text::check`, reused with no extension exemption:
   `secret_in_text`.
5. A generic assignment: a secret name (`names::is_secret_name`), `=` or `:`, then a literal value
   of 8 or more characters that is neither a reference nor a `<placeholder>`. The reason is
   `secret_in_text`.
6. A pattern that fails to compile, or a capture without its groups: `scrub_failed`.

A clean file is published byte for byte. Nothing is redacted.

### Existing test expectations changed (each with a comment naming D54 or brief 47)

- `privacy_port.rs` `rule_digest_golden_requires_explicit_semantic_review`: the golden digest
  changes, because the scanner table is now part of `rules_digest()`.
- `resolve.rs` `snapshot_files_are_deferred_not_yielded_and_not_unknown`: file-history is yielded
  (yielded 2, deferred 1).
- `log.rs` round-line test and `common/log_lines.rs`: the round line has 20 keys, ending with
  `withheld`.
- `s3_store/check.rs` `a_missing_root_deletes_nothing_on_the_real_binary`: the round line ends
  `absence_skipped=1 withheld=0`.

### Effect of the rules-digest change

Admission now requires the current digest for `Scanned`, as it already did for `Config` and
`TextAsset`. Before this change only `Config` and `TextAsset` sources recorded a rules digest. Those
kinds are still deferred and were never captured by the Rust writer, so no stored epoch is replayed
because of the new digest. File-history has no earlier epochs. A future change to the table
re-judges every scanned file, withheld ones included, at the next round.

## Parent acceptance

- Whole diff read. Three findings, fixed in `1359290`, `6e70050` and `68043e3` (above).
- Teeth, 23 of 23 caught (`scan_teeth.py`, focused filter with a 199-test baseline). The rule-table
  teeth change where the table is matched, not the table itself, so the golden digest test cannot
  catch them by accident:

  | Tooth | Result |
  |---|---|
  | Binary passes | caught (4 tests) |
  | Non-UTF-8 passes | caught (3) |
  | Cap off by one | caught (1) |
  | Oversize file read before the cap check | caught (1) |
  | PEM header missed | caught (2) |
  | AWS key id missed | caught (3) |
  | Generic assignment missed | caught (3) |
  | Shell guard dropped | survived at first; caught (1) after `68043e3` |
  | Nested assignment skipped | caught (1) |
  | A compile error passes | caught (5) |
  | `Scanned` prefix-stable | caught (3) |
  | Scanner table left out of the rules digest | caught (1) |
  | Scanner not applied | caught |
  | `binary` reason lost | caught (4) |
  | Withheld record ignored (rescanned each round) | caught (3) |
  | `Present` after the cached withhold | caught (1) |
  | `withheld=` not counted | caught (1) |
  | Record not saved to the index | caught (1) |
  | Record not loaded from the index | caught (1) |
  | The walk still defers file-history | caught (2) |
  | Another snapshot kind yielded | caught (2) |
  | `status` ignores the record | caught (1) |
  | A path in a `check` line | caught (1) |

- Full gates on `68043e3`: 14 of 14, seed 3887993034, nextest 869 passed, consumer 3, line coverage
  95.58 %.
- Linux container (Ubuntu 24.04): `memory-core` `privacy_scan` and `privacy_port`, and `memory`
  `capture`, `daemon` and `resolve`. Five binaries, 16 + 11 and 35 + 86 + 36 tests, all passed.

## Merge, install on both hosts, first round

- Stack #230 (PRs #227 to #229) merged as `36adb8c`. Rust gates passed on all three PRs. CI failed
  only in the two accepted `govulncheck` steps.
- This Mac: installed in place at 06:53:32Z (sha256 prefix `309ecb69070e1928`, `runs = 11`). The
  `89b155e` binary is kept as `backup/memory-89b155e`. Start round at 06:59:59Z: `landed=76
  failed=0 local=0 deleted=0 withheld=141`. `tmp/` was empty afterwards, there was no WARN or ERROR
  line, and the peak footprint was 81 MB.
- dev-01: `36adb8c` built on the host (sha256 prefix `c187441b018cfef7`). Both Nix store
  dependencies are the pinned GC roots. Installed in place at 07:03:37Z, with the `89b155e` binary
  kept as `backup/memory-89b155e`. Start round at 07:05:56Z: `landed=30 failed=0 deleted=0
  withheld=95`. `VmHWM` was 103 MB, `NRestarts=0`, and there was no WARN or ERROR line.
- File-history after the first round (from `memory status` and `index.json`, counts only):

  | Host | Files | Landed | Withheld (all `secret_in_text`) | Withheld size range | `deferred_snapshot` before / after |
  |---|---|---|---|---|---|
  | this Mac | 946 | 805 | 141 | 540 B to 152 KB | 2,156 / 1,210 |
  | dev-01 | 514 | 419 | 95 | 2.4 KB to 244 KB | 1,688 / 1,174 |

  The drop in `deferred_snapshot` equals the file-history count on each host. No file was
  withheld as `binary`, `source_limit` or `scrub_failed`.
- `memory check`, exit 0 on both hosts. This Mac (07:00Z to 07:23Z): 1,079 owners, 7,010
  sources, 6,869 `match`, 141 `withheld`, 0 mismatch, 0 uncaptured, 0 `store_error`. dev-01 (07:23Z
  to 07:28Z): 449 owners, 4,830 sources, 4,735 `match`, 95 `withheld`, 0 mismatch, 0 uncaptured.
  Every `withheld` line reads `kind=claude.file_history reason=secret_in_text`.
- Restore of three Mac sessions with 26, 19 and 13 file-history files into a private temporary
  directory: restore exit 0 each, the transcript restored each time, 57 of 58 files restored and
  equal to the local file by size and SHA-256. The one file not restored is a withheld one (matched
  through `index.json`). The restored copies were deleted.

## Finding: most withholds look like false positives

On this Mac, 141 of 141 withholds come from the generic assignment rule or the shell guard; none
matched a fixed secret pattern (an in-memory reimplementation of the table, counts only). A second
in-memory pass sorted each value that made the generic rule fire into heuristic shape classes
(no value printed):

| Host | Files where every hit is code-shaped (call, member access, env or vault lookup, identifier, path) | Files with another value shape | Shell guard only | Files with a high-entropy value |
|---|---|---|---|---|
| this Mac | 79 | 43 | 15 | 4 |
| dev-01 | 55 | 29 | 8 | 3 |

So more than half of the withheld pre-edit copies are probably source code such as
`apiKey: getKey()` or `token = process.env.X`, not secrets. The behaviour is as designed (the brief
accepts false positives, and the loss is a pre-edit copy that the transcript and git usually
hold). Narrowing the generic rule to literal-shaped values is a privacy-rule change and is left
for an operator decision; the classes above are a heuristic, not proof that a file holds no
secret.

## Limits

- The manifest does not record a withheld version; only the local index and `memory check` know
  of it. Recording it in R2 needs a schema change.
- Snapshot deletions are still not recorded (D52 probes append sources only). A file-history file
  that Claude's cleanup removes stays live in the manifest and shows as `remote_only` in `memory
  check`.
- Watcher events for file-history are not routed (`note` is append-only); the next reconcile picks
  them up, at most 15 minutes later.
- A pattern scanner has false negatives, for example a secret under a name that `is_secret_name`
  does not know and in no fixed shape. A false positive costs one missing pre-edit copy.
