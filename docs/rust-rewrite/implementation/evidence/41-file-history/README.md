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
