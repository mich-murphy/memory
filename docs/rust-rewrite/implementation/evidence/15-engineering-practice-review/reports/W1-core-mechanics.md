# W1 review: memory-core pure mechanics (C1 to C7)

Read-only. Read in full: lib.rs, domain.rs, planner.rs, coalescer.rs, coordinates.rs, limits.rs, disposition.rs, verify.rs, slice.rs. Counts via rg over `memory/src` (caller crate), `memory-core/src`, and test dirs. Not read: locator, hint, manifest, privacy, test bodies, DESIGN text (checked comments only).

## 1. Verdict
| C | Verdict | Why |
|---|---|---|
| C1 boundaries | mostly sound | Pure core vs shell is clean; `verify.rs`/`disposition.rs`/`planner.rs` are called exactly as designed. `coordinates.rs` and parts of `limits.rs` sit in core with no shell caller. `slice.rs` is a misplaced 11-line file. |
| C2 extensibility | mostly sound | New agent = 3 match arms + `agent_rank`; new SourceKind = 1 variant (plus locator). `SnapshotFingerprint::Sqlite` exists, no producer yet. |
| C3 reasoning | mostly sound | Planner and verify are very readable. `plan()` is 104 lines with the same audit match duplicated; `transition_paced` 75 lines. |
| C4 names | mostly sound | Good domain vocabulary; a few vague ones (list below). No finding-id names in scope files. |
| C5 data structures | mostly sound | `AppendBasis`, `Planning`, `Verdict` remove real branches. Some newtypes/enums are ceremony (below). |
| C6 DRY | mostly sound | Hex encode/decode written 3x, nibble parse 2x; `1024` as identifier length vs key length vs `MAX_ID`. |
| C7 YAGNI | weak-ish | ~7 pub items in core with no non-test caller (coordinates, SourceSet, request_budget, key checks, next_due, audit_eligible). |

## 2. What is good
- `planner.rs:77-95` `AppendBasis {Indexed, Unindexed, Fresh}`: only valid combos representable. Consumer `capture.rs:315/…` builds one of three; no Option-pair juggling.
- `planner.rs:126-147` `Planning::{NeedWindows, NeedAuditRecheck, Ready{..}}` makes "missing evidence" a typed request. `capture.rs:733-760` is a clean evidence loop matching on it (3 arms).
- `verify.rs:63-86` `classify`: ordered if-chain, lazy `names_newer_schema` closure, 86 lines whole file, one caller (`commit.rs:294`). Exemplary pure/shell split.
- `limits.rs:19-48` single owner of object-key spelling; shell goes through `current_key`/`manifest_key` (used at `commit.rs:369,376,459,473`).
- `coalescer.rs:47-66` `Window` validated via `try_from`; `complete` removes idle slots (`:177`) so state is bounded.

## 3. Findings (ranked)

**F1 (medium, C7) Core API with no non-test user.** rg in `memory/src` returns 0 for: `coordinates::*`/`Inventory`/`Coordinate` (313 lines, `coordinates.rs`), `SourceSet` (`limits.rs:84`), `request_budget` (`:128`), `check_owner_key`, `check_key_len`, `current_key_len` (`:56-80`), `Coalescer::next_due` (`coalescer.rs:187`), `audit_eligible` (`planner.rs:42`), `WalFingerprint` (domain.rs:518), `Observed::trust` is used only inside planner. Test-only: 1 test file each. Some are planned (DESIGN 6.2/7.3 admission, slice 1 coordinates for Go parity) but nothing wires them. Also `legacy_roots` (`coordinates.rs:98`) is "legacy namespace policy checks only" in a design that retires legacy. Remedy: either wire into the next vertical slice or mark `#[doc(hidden)]`/move behind a feature; at minimum record in the handoff which are pre-built. ~0 lines to delete now; 1h to list. Largest YAGNI exposure is `coordinates.rs` (313 lines, 28 `Coordinate` fields-worth of output, 3 of 6 fields consumed by nobody).

**F2 (medium, C6) Hex and nibble parsing duplicated.** `domain.rs:140-165` (Sha256 parse, own `nibble`), `:226-246` (Sha256State parse, different `nibble` via `to_digit` + explicit uppercase check), encode loops at `:167-175` and `:248-256`, a third at `coordinates.rs:296` (`format!("{b:02x}")`). Behaviors are the same rule ("lowercase hex"). Remedy: one `hex::encode`/`decode_lower` helper (~25 lines saved, 1h), or the `hex` crate.

**F3 (medium, C3) `plan()` is the longest function and repeats itself.** `planner.rs:155-258`, 104 lines, depth 4 (match > match arm > if > return). The `match input.audit {StableMismatch.., RecheckRequired.., NotDue|..}` block appears twice (`:196-201`, `:232-237`); the Snapshot arm and Indexed arm both check rules-changed first. Remedy: lift `audit_gate(input.audit) -> Option<Planning>` and one fn per `Strategy` arm (`plan_snapshot`, `plan_unindexed`, `plan_indexed`): ~15 lines saved, each fn under 40, 1h. Also `ready(plan, false, None)` with two bools/Options positional: `Planning::Ready{refresh_stat: bool, adopted: Option<..>}` encodes 4 combos, but only 3 occur (adopted Some implies refresh_stat true, `:222`). A `cache: CacheUpdate {None, RefreshStat, Adopt(EpochPosition)}` enum would make the impossible state unrepresentable and remove the positional `false, None` at ~8 call sites. 1h.

**F4 (low-medium, C5) `Plan` is a flat 7-variant enum that two layers treat differently.** Producer: `planner.rs:155-258`. Consumers in `capture.rs`: `:358` (only Deleted), `:433` (Snapshot|Replay, else Unchanged), `:742` (Adopt), `:791-806` (Append/Replay/Withheld, `_ => Unchanged`). No caller matches `Plan::Unchanged` explicitly (0 in memory/src; wildcards swallow it). So Unchanged and "Adopt" never reach the final mode switch, yet that switch must still carry `_` for them: the type does not narrow. `Adopt` and `Planning::NeedWindows` are the same concept (request for evidence) in two enums: `Adopt` is a `Plan` variant but is consumed as a request (`capture.rs:742`) and must not reach `:791`. Moving `Adopt` into `Planning` as `NeedAdoptDigest{remote}` would shrink `Plan` to 6 and drop one wildcard hazard. All other variants are produced and consumed (no dead variant). `Withheld` is only ever produced when `input.withheld` is Some; `capture.rs` passes `withheld: None` at the two sites I saw (`:318`, `:429`) [inferred for others], so Withheld arrives from a different path at `:805` only. 0.5h.

**F5 (low, C5) Ceremony types.**
- `RulesDigest(Sha256)` `domain.rs:273`: genuine brand against `Sha256` content digests; used in 8 memory/src lines and prevents swapping with `whole`/`head`. Keep. But `new`/`digest` are trivial.
- `ETag` (`:84`, 18 uses in memory/src) validates non-control: keeps.
- `Offset(u64)` (`:111`): 7 `Offset::new/checked_add` sites in memory/src; all arithmetic is done on `.get()` u64 (`planner.rs:40-41,262`). Prevents `size` vs `offset` mix-ups only in `EpochPosition.captured_to` and `Plan::Append`. Weak but cheap; `checked_add` has no non-test caller [inferred from 7-site count, not verified individually].
- `Birth::{Known, Unavailable}` (`:449`) vs `Option<i64>`: only 2 sites (`realfs.rs:97`); `Option` would remove an enum and 1 serde shape. Low.
- `StatTrust` (`:485`) bool-shaped enum, 1 consumer (`planner.rs:224`); `fn is_settled(&self) -> bool` suffices.
- `ValidationError` has 4 variants for 4 types; fine.
- `identifier!` macro (`:34-79`): sound DRY for 4 brands (ArchiveId/MachineId/SessionId/SourceId; 22 uses in memory/src for the first three, SourceId widely). Real mix-up prevention (they are all strings). Good.
- `Sha256State` (`:182-262`, 80 lines): used in 9 memory/src lines for resumable hashing. Real, but 100+ lines of hazmat serialization and 0 test files directly.
- `LocalError`/`Fault`/`ParkReason`/`OwnerParkReason`: each variant used (`disposition.rs`); `Fault→ParkReason` is a 1:1 map (`disposition.rs:73-80`) which could be a method on Fault; `ParkReason` could be derived `Repeated{fault}`. Low.
- `Agent::as_str` plus `agent_rank` (`coordinates.rs:84`) are two parallel Agent→primitive maps; `agent_rank` duplicates the variant order; use `Agent` `Ord` (derive) as map key. 5 lines.

**F6 (low, C3) Coalescer Slot state is a tri-state in disguise.** `coalescer.rs:76-80`: `pending: Option<Pending>`, `in_flight: bool`, plus `Pending.flush: bool`. Legal states: Idle / Pending / InFlight / InFlight+Pending. Written as 3 fields the invariant ("slot with neither is removed", `:177`) lives in a comment. `last_time` duplicates `Pending.last` only partly (monotonic guard). It is small and well tested; I rate it acceptable. Optional: `enum Phase {Idle, Pending(P), InFlight(Option<P>)}` would remove the `in_flight` check in `poll`, `next_due`, `complete` (3 sites). 1.5h, low value. `poll` returning `Result<bool>` named like a query but mutating: rename `take_due`.

**F7 (low, C1) `slice.rs` is a module for one error type.** `slice.rs:1-11`, sole user `hint.rs:11,559-561`. Move `RangeError` into `hint.rs` (or `domain.rs`), delete module and `pub mod slice` (`lib.rs:12`). Module name "slice" says nothing. 15 lines / 10 min.

**F8 (low, C6) Constant 1024 defined three ways.** `domain.rs:44,74` identifier max (literal twice plus schema), `limits.rs:8 MAX_KEY_BYTES` (object key), `locator.rs:54 MAX_ID` (out of scope), `manifest/schema.rs:208` path. Different rules, same number; identifier max could be a `const MAX_IDENTIFIER_BYTES` used by both `try_from` and the schema JSON (currently two literal copies in one macro that must agree). No duplication of limits between `memory` and core except `capture.rs:47 MAX_PIECE_BYTES` vs `privacy::MAX_PROTECTED_BYTES` (both 8 MiB, different things). Disposition/backoff constants appear only in `disposition.rs` (rg: not in memory/src). `HEAD/TAIL_WINDOW_BYTES` only in planner. Good.

**F9 (low, C4) Names.** See section 4 below.

**F10 (low, C3) `transition_paced`** `disposition.rs:120-195` is 75 lines, depth 4, 5 params (`outcome, cancel_requested, at, prior, pacing`). The Deterministic arm (`:167-190`) inlines streak arithmetic; extract `bump_streak(prior, fault, at)`. Name: "paced" is a leftover of a removed unpaced `transition` ("there is deliberately no jitterless entry point", `:117`): rename to `transition`. 20 min.

**F11 (low, C3) `Disposition::Retry{class}` clones `ErrorClass` and `Streak` clone on every outcome** (`disposition.rs:126-133`); `bumped` computed even for non-retry outcomes. Not a correctness issue.

## 4. Names
Least clear (better):
1. `Planning` / `Plan` (`planner.rs:126`, `domain.rs:631`): near-identical; `Planning` = `Decision {NeedWindows, NeedAuditRecheck, Ready}` -> `PlanStep`.
2. `Observation` (`planner.rs:54`) is about presence/absence -> `Presence`.
3. `Strategy` (`:97`) -> `SourceBasis` (it carries evidence, not a strategy).
4. `ready(plan, false, None)` (`:115`) -> `done`/`no_cache_update`.
5. `Pacing.draw` (`disposition.rs:30`) -> `jitter_draw`.
6. `transition_paced` -> `transition`.
7. `Coalescer::poll` returns bool -> `take_due`.
8. `Coordinate.identities` vs `equivalent_paths` vs `legacy_roots` (`coordinates.rs:94-98`) -> `deny_spellings`, `aliases`, `legacy_roots` (delete).
9. `Inventory::verified` (`:303`) -> `matches_resolution` (also `Inventory::locate` returns an unnamed `(String,String)`; struct it).
10. `Observed` vs `Fingerprint` (`domain.rs:471,506`): same fields minus `observed_at_ns`; `Stat` / `Fingerprint`.
Also `Streak.deterministic` -> `fault_count`; `Sha256State::of` -> `from_hasher`; `AuditState.consecutive_failures` ok.

Exemplary: `AppendBasis::Unindexed`/`Fresh` (states the evidence); `Verdict::{Ours, Adopt, Base, Foreign}` (`verify.rs`); `WithholdReason`/`ReplayReason` (closed, domain-named, `serde` snake_case). Also `window_ranges`, `extension`.
No process-history names (F-ids, round numbers) in scope files. References to "amendment A6" (`domain.rs:703`, `disposition.rs:15`, `verify.rs:1,18`) are spec citations, acceptable; the comment "Mirrors `internal/capturecoordinates`... Go `validRoutes`" (`coordinates.rs:9,203`) ties naming to Go history, which will rot after Go removal. Test file named `core_f2_gaps.rs` carries a finding id (outside scope, noted).
Doc-comment boilerplate: `domain.rs:354-440` 29 variants each "XFamily family; not arbitrary-path admission." and `ReplayReason`/`WithholdReason`/`LocalError`/`Fault` variant docs that restate the name ("DiskFull local failure"): ~80 lines of noise, and the replay-reason docs claim "requires byte-zero replay" for each. Replace with one enum-level doc.

## 5. Longest functions / nesting
| fn | path:line | lines | depth |
|---|---|---|---|
| `plan` | planner.rs:155 | 104 | 4 |
| `transition_paced` | disposition.rs:120 | 75 | 4 |
| `Inventory::resolve` | coordinates.rs:255 | 58 | 4 (for>for>if) |
| `Inventory::compile` | coordinates.rs:142 | 35 | 2 |
| `add_proven` | coordinates.rs:178 | 31 | 3 |
| `Sha256State::try_from` | domain.rs:226 | 24 | 2 |
`resolve` is O(routes x routes) with three nested route loops and a `BTreeSet<(u8,String,String)>`; routes are capped at 128 so cost is fine. A `Route {agent, from, to}` struct instead of tuples with `.0/.1/.2` (`:215-224`, `validate_routes`) would remove the index-based reading; a per-agent `BTreeMap<Agent, Vec<Route>>` would drop the `*k == rank` filter repeated 5 times (`:223,241,246,270,277`). ~15 lines saved. Only matters if coordinates is wired (F1).

## 6. Public API with no outside-module user
Non-test, non-core callers = 0: `coordinates::*` (all), `limits::{SourceSet, request_budget, check_key_len, check_owner_key, current_key_len, MAX_*, BUDGET_*}`, `coalescer::{Coalescer::next_due, WAKE_GAP, WAKE_STAGGER}` (used internally), `planner::audit_eligible`, `domain::WalFingerprint` (constructed only in tests; `SnapshotFingerprint::Sqlite` also not produced by memory/src, only `Plain` at capture.rs:394), `Offset::checked_add`, `RulesDigest::digest`. Used: `plan`, `Planning`, `PlanInput`, `Observation`, `AppendBasis`, `Strategy`, `WindowEvidence`, `window_ranges` (4), `transition_paced`, `Streak`, `Pacing`, `Outcome`, `classify`, `Verdict`, `current_key`, `manifest_key`, `Coalescer::{new,signal,poll,complete}`, `wake_action`. `lib.rs` exposes all modules `pub`; no re-exports or prelude, so every pub item is public API of the crate: narrowing to `pub(crate)` is only possible for core-internal ones.

## 7. Checked and fine
- No clock/I/O in scope files; no `unsafe`; `unwrap` absent in scope.
- Error types are non-echoing and closed.
- `Disposition` variants all produced by `transition_paced`; `CommitOutcome` variants all produced by `Verdict::outcome` and consumed in `commit.rs` (8 refs).
- `ErrorClass::Transient` produced and consumed (2+2).
- Planner variants: every `Plan` variant has a producer and a non-test consumer, except `Unchanged` which is consumed only via wildcard.
- Backoff/park constants defined once.
