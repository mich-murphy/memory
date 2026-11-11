# Review 03: DESIGN.md sections 13-20, amendments, and evidence integrity

Reviewer: Sonnet 5.5, read-only. Date 2026-11-10. Repo HEAD 6ea015e (branch rust/coverage-floor-80).
Labels: (V) verified fact, (I) inference, (Q) open question. Severity/confidence per finding.

## Summary verdict

The design process is unusually well documented and its measured numbers are mostly arithmetically
consistent and reproducible where I could rerun them (3 of 3 reruns reproduced). The weak points are
not fabricated data but (1) several stated mechanisms that tooling does not actually enforce or schedule,
(2) a few decisions whose cited evidence explicitly disclaims supporting them (D18/P4), (3) a fixed 80%
coverage floor that is not justified by any measurement and sits 17 points under the actual figure, and
(4) a cutover plan that, after amendments A0/A2, has no rollback path and no shadow comparison against the
old system. Section 3 raw outputs for the transcript-reading experiments are not preserved, and I could not
rerun them (brief forbids reading session data).

## Findings (most severe first)

### F1. Cutover has no rollback and no old-vs-new comparison after A0/A2 (high, high)
- (V) A0 (amendments:9-14): Go writers stopped 2026-11-09. A2 (amendments:28-37): `capture-v1/` and `v3/`
  (435,618 objects) deleted from R2 before any Rust acceptance. DESIGN 18 step 6 wanted inventory-then-delete
  AFTER acceptance; the amendment inverted the order.
- (V) DESIGN 17 slice 2b: shadow run "Sessions the Go candidate captured and Rust did not are counted as a
  coverage cross-check [review 06 F1, F2]". Go is stopped and its archive deleted, so that cross-check cannot
  be performed. The only remaining comparison is `transform(local)` vs restored, which shares the transform
  code with the writer (a common-mode failure the old archive would have exposed).
- (V) Hooks "remain installed and write only into the local Go spool" (A0): the spool is a nonempty rollback
  asset only for the period since 2026-11-09, and DESIGN 18 step 2 removes them.
- Missing against established migration practice (unverified, from memory: parallel-run/shadow-comparison
  and "expand/contract" patterns, e.g. Fowler "ParallelChange", Google SRE canarying chapter): (a) a
  quantitative pass/fail per shadow day (restore success 100%, parked sources 0, oldest-uncaptured p99,
  Replay rate); slice 2b exit is only "every captured session restores to its source bytes"; (b) explicit
  abort/rollback triggers and what rollback means once Go data is gone; (c) a comparison against an
  independent reference (the old archive) before deletion; (d) a per-host canary order with soak time
  (step 2 says "one host" with no duration); (e) data-loss accounting for sessions that agents prune locally
  between 2026-11-09 and Rust capture (DESIGN 18 step 4 accepts old-archive loss but not this window).
- Action: write rollback criteria into section 18; state that the 7-day shadow is the only pre-deletion
  evidence and give numeric exit thresholds; keep a local spool/rsync copy of sources until acceptance.

### F2. D18 / piece cap 8 MiB cites P4, and P4 explicitly says it does not justify it (high, high)
- (V) DESIGN.md:559 "Pieces are at most 8 MiB raw. [default, prototype P4]"; D18 evidence "7.3; 04 M5".
- (V) P4-synthetic-footprint.md:3-4: "This benchmark does not justify a change to the 8 MiB / 8 uploads defaults."
  Its own table: 8 MiB x 8 random buffers = 135.12 MB peak RSS (Python, includes interpreter), above the
  128 MB peak target in DESIGN 15 (:1146), while DESIGN 7.3 (:595-596) claims "at most 80 MiB".
- (I) The "80 MiB" bound counts payload only; P4 shows process peak is higher in the model. The Rust
  figure is unmeasured (target, "measured in slices 2 and 3"). D18 therefore rests on assertion.
- Action: relabel as [default, unmeasured]; measure RSS in slice 2 with the real client; decide cap from it.

### F3. Coverage floor 80% is a number without evidence; the stated rationale is for dropping a ratchet, not for 80 (medium-high, high)
- (V) Amendment A9 (amendments:123-137): floor fixed at 80 by operator decision; reasons given concern why
  98% was bad. "The Go gate is 75% total" is the only anchor. rust/coverage-floor.txt = 80.
- (V) Actual: 97.03% (5,595/5,766 lines), artifacts/coverage.json. Branch coverage is 0/0 (llvm-cov branch
  data not collected), region 96.4%. An 80% floor lets ~1,150 more lines go untested (about 20% of code)
  before the gate trips; it cannot catch a regression from 97 to 81.
- Literature (V): Inozemtseva & Holmes, ICSE 2014, abstract via https://www.cs.ubc.ca/award/2014/06/acm-sigsoft-distinguished-paper-award-icse :
  correlation between coverage and effectiveness is low to moderate once suite size is controlled, and coverage
  "should not be used as a quality target". Google Testing Blog "Code Coverage Best Practices" (2020),
  snippet via search: 60% acceptable, 75% commendable, 90% exemplary, "no ideal number", avoid top-down
  mandates. So a floor is defensible as a backstop to find under-tested code, but 80 is not derived from
  anything here; the document should say "arbitrary backstop". A9 is honest that it is a backstop, but
  DESIGN 15 gate 8 and quality-gates.md present it as a gate with no argument that it detects anything.
- Just et al. FSE 2014 (V, abstract via search): mutant detection correlates with real-fault detection
  independent of coverage; 73% of 357 real faults coupled to mutants, 17% not, 10% need new operators.
  Petrovic & Ivankovic (V, research.google abstract): Google mutates only diff lines that have statement
  coverage, ~30% of diffs, developer-facing. Both support mutation checking over a coverage number.
- Action: keep the floor but ratchet via a recorded decision at slice exits; track mutation score on
  invariant modules; enable llvm-cov branch coverage as informational.

### F4. "No I/O crates in memory-core, enforced by cargo-deny bans" is true (V) but narrow; monthly cargo-mutants is stated, not scheduled (medium, high)
- (V) crates/memory-core/deny.toml has a real deny list and gate 03-core-bans runs it with --exclude-dev.
  Root rust/deny.toml has NO deny list (only wildcards=deny, multiple-versions=warn), so memory-reader and
  xtask are unconstrained although DESIGN 13 describes memory-reader as library "restore and export".
- (V) `cargo mutants` appears in DESIGN 15 ("Monthly") and flake.nix provides the tool, but it is not in
  scripts/gates.sh, `.github/workflows/rust.yml`, or any scheduled workflow (grep). Challenge 06 F8 even
  recommended deleting the monthly cadence; DESIGN kept it. Mutation evidence is instead four hand-written
  mutants (window, if-match, advance-on-fail, privacy-deny) in check-*.py. These are targeted and killed
  (artifacts/status.tsv all 0) but cover 4 sites, not memory-core generally.
- Action: either schedule cargo-mutants (CI cron, diff-scoped per Petrovic & Ivankovic) or delete the sentence.

### F5. Dependencies: table in section 13 and the tree diverge; some choices unjustified in the doc (medium, high)
- (V) Cargo.lock (148 packages) contains none of tokio, zstd, tracing, tracing-subscriber, clap, rusqlite,
  object_store, rusty-s3, ureq. Section 13 lists them as the stack. So "dependency justified" can only be
  assessed for: serde 1, serde_json, schemars 1.2.3, thiserror 2.0.21, toml 1.2.1, sha2 =0.11.1, regex 1.13.1,
  url 2.5.8, notify =8.2.0, hegeltest =0.52.0, proptest 1.12.0, proptest-state-machine 0.8.0.
- (V) `cargo deny --locked check advisories` run against the local advisory DB (commit 2026-11-09): "advisories ok".
  Prior gate log (artifacts/03-deny.log): advisories/bans/licenses/sources ok. No yanked check beyond that;
  crates.io API: hegeltest 0.52.0, notify 8.2.0, sha2 0.11.1, proptest-state-machine 0.8.0 are not yanked.
  cargo-audit not installed; cargo-deny covers RustSec.
- (V) hegeltest: created 2026-03-16, 256 versions by 2026-11-10 (0.52.1 published 2026-11-09), pre-1.0,
  depends on hegeltest-c (native engine; "static-engine" feature builds C). MSRV 1.86. DESIGN acknowledges
  churn and pins exactly. (I) `forbid(unsafe_code)` covers only workspace code; hegel's native engine is a
  dev-only unsafe surface. The pin is reasonable; there is no exit plan beyond "proptest versions become the
  gate" (19.5) and the proptest cross-check exists, so risk is contained. 0.52.1 exists; the pin lags by one patch.
- (V) notify: 8.2.0 pinned; crates.io newest is 9.0.0-rc.8 (2026-11-09). Pinning the last stable is defensible;
  DESIGN records source read (Cargo.toml comment). (I) the known gaps (no FSEvents replay; new-dir files missed)
  are compensated by the 15 min reconcile, a design-level dependency on a heuristic.
- (V) proptest-state-machine 0.8.0 is pinned by caret; 0.9.0 exists. Low.
- (V) rust-version = "1.99" equals the current toolchain, so MSRV carries no information (hegeltest needs 1.86,
  sha2 1.88). Low.
- (V) P2 results (P2-s3-client.md): adopting `ureq` or `object_store` fails `cargo deny` licenses
  (CDLA-Permissive-2.0 on webpki-roots / webpki-root-certs) until deny.toml `allow` is extended; section 13
  does not mention it. Rusty-s3 0.11.0 (2026-10-27) and ureq 3.5.1 (2026-11-02) are both active; object_store
  0.14.2 (2026-09-15). The P2 recommendation (rusty-s3 + ureq) is DESIGN's "or" still open in 13.
  RSS comparison 84.5 vs 89.2 MB is 3 rounds, within noise (V, P2 notes admit it).
- Action: update section 13 to the chosen client and add CDLA-Permissive-2.0 to allow with a reason.

### F6. Coding standards are mostly enforced, one claim is not literally true (low-medium, high)
- (V) Enforced: workspace lint table in rust/Cargo.toml; `cargo clippy --workspace --all-targets -Dwarnings`
  (gates.sh:21); check-lints.sh proves 12 lints fire (artifacts/09-lints.log, each "compiler exit 101, matching
  diagnostic" plus a safe control); `grep` finds zero unwrap/expect in tests, one `#[expect(clippy::too_many_arguments)]`.
  This is stronger than most projects and addresses "stated only" concerns.
- (V) Not enforced: "`missing_docs`/`missing_errors_doc` warn, promoted to deny in CI for memory-core and
  memory-reader" (DESIGN 14): they are `warn` and CI's `-Dwarnings` promotes them for the whole workspace,
  so it is satisfied, but through a blanket flag rather than the stated per-crate rule. "Byte-range access goes
  through one slice helper" (14) is enforced indirectly by `indexing_slicing=deny` only.
- (V) Clippy groups: no `clippy::pedantic`/`nursery` and no `arithmetic_side_effects`, `as_conversions`,
  `cast_possible_truncation`, `let_underscore_must_use`, `wildcard_enum_match_arm`. For a byte-offset/
  checkpoint codebase, integer-cast and overflow lints are the ones that match the "newtype every offset" rule.
  (I) The Rust API Guidelines (naming, C-GOOD-ERR, C-STRUCT-PRIVATE) are not tooling-checked at all; "Naming
  follows the Rust API Guidelines" is stated only. Unverified from memory (Clippy lint-group docs, API
  Guidelines checklist); I did not fetch them.
- Action: add `arithmetic_side_effects`, `cast_possible_truncation`, `as_conversions` to the lint table plus
  check-lints violations, or state why not.

### F7. Testing strategy: strong structure, but per-gate claims overstate (medium, medium)
- (V) Independent oracles and generator coverage guards are real (P1 oracle never calls the window
  predicate; DESIGN 15 guard). These are the practices Just et al. and Google blog posts favour over raw coverage.
- (I) Property case counts "500 in CI, 2,000 locally" are conventions, not derived; HEGEL seeds are random per
  run (gates.sh:7), so CI is non-reproducible by design but the seed is recorded; good.
- (V) Gate 8 relies on property coverage-guard failing at low case counts ("Ad-hoc coverage runs at low case
  counts fail the property coverage guards by design", quality-gates.md): the coverage gate is coupled to a
  2,000-case run; CI uses 500 and passes the guard? Quality-gates records one 2,000-case run; the hosted CI
  result is explicitly not claimed ("Workflow configuration is not evidence that hosted CI ran successfully").
  (Q) Has hosted CI at 500 cases ever passed all 13 subgates on all 3 OSes? Experiment: `gh run list --branch
  rust/coverage-floor-80` then read the latest rust.yml run (I did not run network commands against GitHub).
- (V) Fault injection: state-machine property (4) with crash points and a deterministic fake store; P12 flags
  that no crash-point-between-stage-and-publish property existed before A7. Good, but filesystem fault
  injection is in-process simulation with `SyncPolicy::None`, so real fsync ordering is untested by the
  properties that matter most (A7 admits Linux/ext4 unmeasured, BC-01 SSH unavailable).

### F8. Decision log entries resting on assertion or on disclaimed evidence (medium, medium-high)
- D18 (see F2).
- D2 quiet 60 s / cap 300 s: evidence 3.2 shows the trade-off (lag mean 145 s, p99 322 s; half the requests of
  30 s) but nothing says 60/300 beats 45/300 or 90/450; it is a judgement on one user's corpus. Footprint
  target "oldest uncaptured age 10 min" and p99 lag 322 s from 3.2 are different measures. (I)
- D3 "safety reconcile every 15 min": no experiment; justified only by the 360 ms walk number. Not P-tested.
- D16 `flock` single instance: one observation (EWOULDBLOCK on a held lock) is fine for the primitive; the
  daemon-crash/stale-lock behaviour is design reasoning.
- Footprint table (DESIGN 15: 40 MB idle, 128 MB peak, 20 ms CPU per capture, 20 MB disk): labelled [target];
  no measurement. Acceptable only as labelled.
- D19/D20, D7: operator decisions (not evidence), explicitly labelled in section 19. Acceptable.
- A1: P5 waived. P5 and P8 are "blocked", so D1's central premise (prefix rewrites are rare enough to make
  Replay cheap) has no measured prevalence; mitigation is the window check plus audit, so correctness holds
  but cost of Replay is unquantified. (I)

### F9. Evidence integrity of section 3 (medium, medium)
Table of experiments (what, n, rerun script, raw output, conclusion match):

| Item | What / n | Script | Raw output preserved | Conclusion vs data |
|---|---|---|---|---|
| 3.1 policy_sim.py | upload bytes/PUTs per policy; 945 Claude + 3,650 Pi sessions, one Mac | yes (experiments/policy_sim.py) | NO, only table in DESIGN | Arithmetic ok (20.25/1.90=10.7; 320.97/2.07=155). Script default Q=30 s; header says "quiet 60 s unless stated... 30 s window" - ambiguous |
| 3.2 batch_sweep.py | 4,621 sessions, 599,697 records | yes | NO | PUTs = 3 x captures checks out in all rows. Single host, single user |
| 3.3 piece_compress.py | 4 hand-picked sessions (median, p90, p99, max) | yes | NO | n=4; script uses Q=30 s not 60 s. "penalty 1.01x" on two largest is fine, small-n |
| 3.4 walk.py | 24,127 files | yes | NO | Reproduced (below) |
| 3.4 spawn/true | 500 runs | script not in experiments/ | not seen | p99 of 500 |
| 3.4 O_APPEND, flock, rename | [review] reviewer runs | inside reviewer reports | partial | n/a |
| 3.7 manifest_cost.py | 4,650 sessions, 27,683 captures | yes | NO | Percentages check (457/4010=11.4%, 29/4010=0.7%); assumed 220 B/piece is an assumption, and corpus (4,650) differs from 3.2 (4,621) |
| hegel-probe | 2 tests | cargo project | log not preserved | trivial |
| P1 | 420 cases seed 1601, synthetic | run.py | results.json + independent-rerun.json | Reproduces. Interior (middle) rewrite detection with 4+64 is 36/60; same-size 31/60; 53 wrong reassemblies without audit. DESIGN states this as audited, correct |
| P3, P6, P7 | synthetic | run.py | results.json | reproduced (P3 exactly, P7 timing varies 49 vs 66 ms) |
| P2/P2b | real R2 probes | yes | yes (p2-s3-client/results) | admits 2c is not a discriminator |
| P11 | 889 real R2 requests, sim 100 owners x 12 seeds x 8 approaches | sim.py | yes | reproduces byte-for-byte |
| P12 | APFS micro, e2e | bench.py, e2e.sh | yes | Linux unmeasured |
| P4 | synthetic Python/C | in run.py | results.json | self-disclaimed; see F2 |
| P5, P8 | blocked | none | none | honestly labelled |

- (V) The transcript-derived experiments (3.1-3.3, 3.7) have scripts but no preserved raw output, and by the
  design's own safety rules cannot be rerun by this review. Reproducibility depends on one operator machine's
  live (growing) corpus, so numbers drift (24,127 -> 24,516 files; 4,621 vs 4,650 sessions). A rerun in a
  year would not match. Action: save the aggregates (no content) as JSON next to each script with the
  script hash, as run.py already does.
- (I) Stronger-than-data: DESIGN 3.3 "penalty 1.01x" and 3.2's "bytes are identical across windows" are
  fine; but section 3.4's single-line costs (e.g. 360 ms walk) are warm-cache numbers (rerun below) and the
  1 s reconcile target is stated without a cold-cache figure.

## Reruns (scratch under /private/tmp/claude-501/-Users-mm-dev-memory/83693829-eddb-4e4e-a499-380d68c821f5/scratchpad/review/rerun/)
1. prototypes/run.py (P1, P3, P4, P6, P7), copied to scratch, exit 0 in 16.7 s: P1 all counts (detected,
   unsafe_commits, scoped_unsafe_commits, by_kind, audit_failures) identical for every window setting; P3
   identical (per_day lost 0 of 100; rename+unlink lost 100 of 100); P7 timings differ (CRC32C 49 ms vs 66 ms
   p50), P6 identical. Reproduces; timing is noisy and "p99" in these files is the max of 15 samples.
2. P11 sim.py, 12 seeds, scratch cwd, 30 s: `sim-results.json` identical to the stored file (all 56 result
   keys). Fully deterministic. Reproduces.
3. experiments/walk.py (stat-only metadata walk of agent roots; no file content read): 24,516 files; passes
   1479 ms (cold), 414 ms, 433 ms vs DESIGN 360 ms on 24,127 files. Warm result reproduces within ~15-20%
   (more files); the cold first pass is 4x worse and is not in the doc. Python, not Rust; the 1 s target in
   section 15 should be checked cold in slice 2 (Q).
Not rerun: batch_sweep, policy_sim, coalesce_sim, piece_compress, manifest_cost (parse session contents,
forbidden by the brief).

## What is sound
- Arithmetic of section 3 tables is internally consistent (checked ratios, PUT = 3 x captures, percentages).
- Evidence provenance is hash-pinned (implementation/verify-evidence.py) and caveats are written next to results
  (P1, P4, P5/P8, P2 notes) rather than hidden.
- The lint table is proven by deliberate-violation crate (12 lints + control), which answers "stated vs
  enforced" for those lints. cargo-deny advisories pass against a 2026-11-09 advisory DB (V).
- Challenge-review mapping (section 20) cross-links findings to sections; amendments are dated and attributed
  to operator decisions rather than evidence, which is the honest labelling.

## Open questions and experiments
1. Q: Does the 8 MiB / 8 upload bound fit 128 MB in Rust? Run slice-2 daemon under `/usr/bin/time -l` with 8
   concurrent 8 MiB random PUTs against the fake store; pass if peak RSS < 128 MB.
2. Q: Cold-cache reconcile at 30k sources. `sudo purge` then run the Rust reconcile counter test; pass < 1 s.
3. Q: Does hosted CI pass at 500 cases on all 3 OSes? `gh run list --branch rust/coverage-floor-80`,
   `gh run watch <id> --interval 60 --exit-status` (single watcher per global instructions).
4. Q: What is the actual mutation score of memory-core? `cargo mutants -p memory-core --in-diff origin/main`
   (compile-heavy; run once with CARGO_BUILD_JOBS=2); ties F3/F4 to data.
5. Q: Is the rollback story after A2 acceptable to the operator? Needs a written decision, not an experiment.

## Not covered
- Sections 3.4 reviewer-run microbenchmarks (O_APPEND, flock, spawn) were not rerun.
- I did not read the challenge/ and reports/ files line by line, only the references used above.
- No GitHub CI status, no crates.io download/audit-history review beyond version/yank/date data, no
  cargo-audit (not installed), no review of rusqlite/zstd/tokio/tracing/clap since they are not yet in the tree.
- Software Engineering at Google testing chapters and Rust API Guidelines/clippy docs were not fetched;
  statements about them are unverified, from memory. Fetch of Just et al. PDF and the ACM page failed; the
  figures quoted come from search-result abstracts.
