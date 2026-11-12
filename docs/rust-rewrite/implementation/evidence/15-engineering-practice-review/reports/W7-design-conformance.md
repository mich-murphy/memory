# W7: design conformance of `rust/` against DESIGN.md + amendments (A0..A13)

Scope read: DESIGN.md and the amendments in full. Rust: targeted `rg` and reads (privacy, domain, planner, coalescer, disposition, state, daemon, commit, main, Cargo files, scripts). Not read line by line: locator.rs, document.rs, schema.rs bodies, tests/. Marked [inferred] where I relied on grep absence rather than a read.
Code today: ~11.9k lines in src (including `artifacts/scaffold-baseline`, which is a frozen copy). Real adapters are partial: `realfs`, `resolve`, `watch`, `hook`, `state`, `config` exist. There is no real store, no zstd, no tracing, no reader, no SQLite. `main.rs:6-18` wires only `hook` and `daemon`; `daemon` opens state and exits 78 (`daemon.rs:349-358`). Every other subcommand exits 78.

## 1. Conformance table

| Section | Status | Code evidence | Amendment | Note |
|---|---|---|---|---|
| R1 14 kinds, withheld counted | partial | `domain.rs:353` SourceKind (19 variants), `locator.rs:313,863`, `WithholdReason` `domain.rs:607` | A4 | Locator and withhold enum exist. `memory status` surfacing is not started (slice 2). Snapshot kinds are slice 3. |
| R2 fail-closed privacy | partial | `privacy/mod.rs:166,172` RedactedBytes / AuthorizedBody, `admit` :264, `authorize` :340, `UnknownReason` :77-83 | none | Capability types are opaque. Whether `Store::put_*` (`environment.rs:180,195`) takes only AuthorizedBody was not read [inferred yes]. Enforced against fakes only. |
| R3 integrity over timeliness; oldest-age metric | partial | no `oldest` or status field found (rg) | none | Age metric is not started (slice 2). |
| R4 idempotent, convergent | partial | `commit.rs` (verify, rebase), `verify.rs:61` | A6 | Properties exist against fakes. Not proven on R2 (acceptance not run). |
| R5 light footprint | not started | none | A10.4 | Footprint measurement is slice 2/3. |
| R6 bytes proportional | implemented (shape) | `manifest/build.rs:52-104` delta/checkpoint, `planner.rs` Append | A10.1 | Table 3.1 is 30 s data. |
| R7 strict schemas | implemented | 64 `deny_unknown_fields` hits, `rust/schemas/*.json`, xtask | none | Gate 07 checks drift. |
| R8 / R9 | implemented | no encryption code | none | Plaintext as designed. |
| 5 domain model | implemented, renamed | `domain.rs` (all types listed), `limits.rs` | none | Names differ: `Redacted<T>`/`Authorized<T>` became `RedactedBytes`/`AuthorizedBody`. `UnknownPolicy` became `UnknownReason`. `Planning` wraps `Plan` (`planner.rs:126`). Cosmetic, unrecorded. |
| 6.2 keys | partial | `limits.rs:25-56,88,120`; heartbeat absent | A10.2 | Key builders good. `object_key` doc (`limits.rs:52`) still says "raw piece bytes", contradicting A10.2. Heartbeat is not started (slice 2/3). |
| 6.3 manifest | implemented | `manifest/schema.rs`, `build.rs`, `state.rs` fold, `CHECKPOINT_INTERVAL` | A3, A10.3 | `Encoding::Zstd3` label (`schema.rs:343`) is recorded as open (A10.3) and made a prerequisite (A13.2). |
| 6.4 epochs | implemented | `planner.rs:155-216`, ReplayReason | A1 | |
| 6.5 schema policy | implemented | `manifest/recovery.rs` projection structs, NewerRemoteSchema `commit.rs:114,227` | none | Recovery reads only positions. Good. |
| 7.1 coalescer | implemented (core), partial (wiring) | `coalescer.rs:88-217`, `watch.rs:9,119` | none | quiet/cap/flush/wake_action are pure. Real scheduler not exercised (A10.5). |
| 7.2 reconcile | partial | `planner.rs:13,168,208,242`, `daemon.rs:887` | A1, A12.2, A4 | Planner complete (Deleted, Adopt, Racy). Audit not implemented: `capture.rs:327` `AuditEvidence::NotDue`. |
| 7.3 capture | partial | `capture.rs:457-600` | A10.2, A10.3, A10.4 | No zstd, no SQLite, no concurrency limits. Order is transform, authorize, digest (A10.2). |
| 7.4 commit protocol | implemented (fakes) | `commit.rs:107-431`, `verify.rs`, `disposition.rs` | A6, A13.5 | Foreign, NewerSchema, AlreadyOurs, `known_ours`. No real client to disable retries on. |
| 7.5 shared stores | partial | `Owner::Shared` `domain.rs:326`, `current_key` | none | Type and key exist. Capturing them is slice 3. |
| 7.6 launch evidence | partial | `daemon.rs:756-830` attach_pending, `hook.rs` | A3, A11.1 | Attach exists. Retention is missing (A11.2). |
| 8.1 pipeline | implemented | `privacy/mod.rs` | none | |
| 8.2 admission | implemented | `privacy/names.rs`, `resolve.rs` | A4, A11.5 | |
| 8.3 transform | implemented | `privacy/document.rs`, `text.rs` | none | Rules digest `domain.rs:273`. |
| 8.4 launch allowlist | diverged (recorded) | `privacy/launch.rs` | A3, A12.1, A13.3 | Amended three times. DESIGN 8.4 is wrong in five places. |
| 8.5 unknown policy | partial | `privacy/mod.rs:266-278`, `daemon.rs:50` EXIT_CONFIG | none | |
| 9.1 files | partial | `state.rs:127,167,524,713`, `daemon.rs:319` vet_hints | A5, A11.2 | Retention sweep missing. `state/tmp` not seen [inferred]. |
| 9.2 durable writes | implemented | `state.rs:421-570` | A7, A11.4 | Checksummed header, fsync, rename, dir sync. |
| 9.3 index rebuild | partial | `recovery.rs`, `capture.rs:344` | none | Adopt path exists. End-to-end rebuild against R2 not shown. |
| 10.1 hook | implemented | `hook.rs:195-254`, `main.rs:8-14` | A3, A11.1 | No tokio or clap. Hand argv. |
| 10.2 filesystem | partial | `watch.rs`, notify 8.2.0 pinned | A13.6, A10.5 | |
| 10.3 safety reconcile | partial | `daemon.rs:48` (60 s index tick); 15 min timer not found [inferred] | A10.5 | |
| 11.2 error classes | partial | `domain.rs:697`, `disposition.rs:7-120` | A6 | `Transient` added. Startup jitter missing (A13.4). |
| 11.3 status | partial | `daemon.rs:113-214`, `state.rs:205` | A11.3 | Counters yes. oldest age, loss-window counter, `unpark`, `--verify` not found. |
| 11.4 audit, read-back | not started | `capture.rs:327` | A12.2 | Slice 2 (audit), slice 2b (read-back). Hard prerequisite of first write. |
| 11.5 logs | not started | no `tracing` dep (Cargo.toml) | A11.3, A13.2 | Owner: slice 2 daemon wiring. |
| 12 single instance | partial | `state.rs:170` try_lock, `:33` LockHeld, `daemon.rs:317` | none | Lock first: yes. 2-job / 8-upload limits absent; daemon is synchronous. |
| 13 crates | partial / diverged | `Cargo.toml`, `main.rs`, `memory-reader/lib.rs` | A12.4 | Reader is an empty stub (slice 2b). Subcommands `status`, `reconcile`, `restore`, `export`, `unpark` all exit 78. zstd, tracing, clap, rusqlite and the S3 client are absent (P2 undecided). |
| 14 lint table | implemented | `Cargo.toml` lints, 12 `#[expect]` | none | `missing_docs` is warn; gates run `-Dwarnings` (`gates.sh:22`), so it is effectively deny. |
| 14 slice helper | diverged, unrecorded | `slice.rs:1-11` | none | See D1. |
| 14 lint-check crate | partial | `scripts/check-lints.sh` runs `check-lints.py`; `lint-check` excluded in `Cargo.toml` | none | Mechanism is a Python script. Crate location not verified. |
| 14 error enums | implemented | `thiserror` throughout | none | |

Counts (rows above, merging R8/R9): implemented 17, partial 18, diverged 3 (8.4 recorded, slice helper unrecorded; domain-naming counted under implemented), not started 3 (R5, 11.4, 11.5). 41 rows in all; the slice-helper "diverged" row is the one unrecorded gap.

## 2. Undocumented divergences

D1. Slice helper (DESIGN 14: "Byte-range access goes through one `slice` helper module that returns `Result`"). `memory-core/src/slice.rs` is 11 lines and holds only `RangeError`. Its only user is `hint.rs:559`. Elsewhere code slices inline with `.get(a..b)`: `capture.rs:242` (`bytes.get(start..end).map(manifest_hash)`), `:288`, `:300`. The lint is satisfied, but the stated single-module rule is not followed. Low severity; either implement or amend.

D2. Stale object-key doc. `limits.rs:52` says "`objects/<sha256 of raw piece bytes>.zst`". A10.2 says the key hashes transformed bytes, and code does that (`Piece::from_authorized`). The doc on the one place that spells keys misleads a maintainer.

D3. Filesystem seam bypassed. DESIGN 12: "The clock, the filesystem and the store sit behind traits." `daemon.rs:587,598` call `std::fs::read_dir` and `File::open` directly for hints, although the daemon is generic over `FileSystem`. Hint scanning is therefore outside the simulator's control. [Read the lines; whether a second trait covers hints elsewhere is not checked.]

D4. Dependencies outside the DESIGN 13 table. `memory-core` depends on `regex` (`privacy/names.rs:1`) and `url` (`document.rs:508`); neither is in section 13 and no amendment covers them (A12.4 covers only `rustix`). Not harmful (no I/O), but DESIGN 13 says "no custom ... " and lists the dependency set.

D5. Type names diverge from section 5 (`Redacted<T>`, `Authorized<T>`, `PolicyDecision`, `UnknownPolicy` versus `RedactedBytes`, `AuthorizedBody`, `Admission`/`Authorization`, `UnknownReason`). DESIGN 5 is the document a reader searches; neither it nor an amendment maps the names.

D6. Lint-check mechanism: DESIGN 14 says "a crate of deliberate violations, one per lint, run by `scripts/check-lints.sh`". The script delegates to `check-lints.py` (`check-lints.sh:4`). The workspace excludes `lint-check` (`Cargo.toml`). Unrecorded, minor; I did not read the Python.

Checked and found conforming where I expected trouble: lock before anything (`daemon.rs:317` before `state` at :318); NewerSchema parks the owner; Hook has no async runtime.

## 3. Contract goals

| Goal | Upheld today by | Only intended |
|---|---|---|
| Session-local publication | `Owner` enum and `current_key` (`limits.rs:25`); one commit pointer per owner; `OwnerRt` per owner `daemon.rs:362` | Shared-store capture (slice 3); behaviour on the real bucket |
| Immutable evidence | `If-None-Match` writes via `Store::put_if_absent` (`environment.rs:180`); content keys; no delete path in code (rg finds none) | Verified against real R2 only in A13.4 probes, not through the Rust client; unreferenced old objects stay (A10.2) |
| Fail-closed privacy | Opaque `AuthorizedBody` (`privacy/mod.rs:172`); `UnknownReason` -> withheld; credential denylist `names.rs`; symlink skip (A4) | Store-side signature check [inferred]; canary property runs on fakes; no real transcript path exercised |
| Idempotent at-least-once writes | `verify.rs:61` classify + `known_ours` (A6); `commit.rs` rebase 4x and verify GET 4x (A13.5) | Crash windows on the real store; the audit is missing, so a mid-file rewrite is undetected (A12.2) |
| One authoritative reader | `manifest/build.rs:134` fold and `recovery.rs` are in core and reusable | `memory-reader` is an empty stub (`lib.rs:1-3`); writer-reader-importer chain is not started (slice 2b) |

## 4. Design-document health

Sections that are wrong or incomplete unless the amendments are read: header and section 0 (A0 says Go writers are stopped; section 0.1 says "keep running until cutover"), 0.5 and 6.1 "fresh bucket" and "acceptance bucket" (A2), 1 and 19 (A2), 2 R6 and 3.1 (A10.1, wrong fact), 4/6.2/7.3 object key (A10.2, wrong fact), 6.3 `launch.argv` (A3), 7.2 symlinks (A4) and audit (A1, A12.2), 7.3 piece cap and 80 MiB (A10.4), 7.4 step 5 and 11.2 (A6, A13.5), 8.4 (A3, A12.1, A13.3: five claims), 9 state root (A5), 9.1 retention (A11.2), 9.2 logging (A7, A11.3), 10.2 (A13.6), 10.5 constants (A10.5), 13 (A12.4, A13.2), 15 gate 8 and "monthly mutants" (A9, A10.8), 16 P-table needs and P5 (A1, A2), 17 slice 2b/3 shadow lengths (A13.1), 18 (A2, A13.1). That is about 27 section-level items; roughly half of the 21-section document. Appendix D18/D21 rows also inherit these.

Misleading places for a fresh reader: (a) section 0.1 versus A0; (b) section 6.1 and 18 "fresh bucket", "create production bucket" versus A2 (one existing bucket, old archive already deleted); (c) 6.2/7.3 key hashes "raw" bytes (A10.2) and `limits.rs:52`; (d) "final" in the status line when 13 amendments and two open items (A10.3 closed by A13.2) exist; (e) 18 has no exit criteria (A13.1 adds them); (f) AGENTS.md still says "frozen, running until cutover" and "fresh shared bucket".

Recommendation: fold. The amendments file itself says it cannot edit DESIGN.md only because `verify-evidence.py` hash-pins it; that is a tooling constraint, not a design one. Concretely: (1) produce a consolidated `DESIGN.md` revision (or `DESIGN-current.md`) that applies A0-A13 inline, tagging each changed paragraph `[amended A<n>]`, with the amendments file kept as the dated change log; (2) update the verify-evidence pin as a recorded decision; (3) fix AGENTS.md lines; (4) fix `limits.rs:52`. Until then, put a "read amendments alongside" banner at the top of DESIGN.md; the fastest option, and the one hash-pin cost.

## 5. Engineering judgement

- Boundaries are sound: pure core (no I/O deps, enforced by `memory-core/deny.toml` bans), I/O shell behind `Clock`/`FileSystem`/`Store`, and the reader reusing core `fold`. Evidence of discipline: every state is an enum and `Plan` is total.
- YAGNI candidates: `Sha256State` resumable hash is documented as cost-only, yet carries serialization and version-mismatch handling; heartbeat object and `known_ours` are specified before any measured failure; 8-upload/2-job limits are specified for a synchronous daemon. The `Counting<T>` wrapper (`environment.rs:268,291`) serves only footprint counters.
- Extensibility: new agents mean new `SourceKind` variants plus locator cases (closed enums, compiler-guided). Linux: `Birth::Unavailable` and `cfg(target_os)` hooks exist (one test), but the FS adapter has no Linux evidence (P10). Real store: the trait is narrow (`put_if_absent`, `put_if_match`, get), so adding it is cheap. Async vs sync is the only structural choice left (P2).
- Three riskiest unproven assumptions:
  1. Sources are append-only apart from head/tail windows; the audit that backstops this does not exist (A12.2), and rewrite frequency is deliberately unmeasured (A1).
  2. The R2 client contract: conditional PUT, disabled retries on `current.json`, the 412-after-5xx path. Only probes (A13.4) and fakes exist; no Rust client has run. Also whether a synchronous core fits the client chosen in P2.
  3. Footprint and notification reliability: the 128 MB peak with 8 concurrent 8 MiB PUTs is unmeasured (A10.4), and FSEvents loss under 10k-file bursts (A10.5) relies on rescan flags through a scheduler that has never run end to end.
