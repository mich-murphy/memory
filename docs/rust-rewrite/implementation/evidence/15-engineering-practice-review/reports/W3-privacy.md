# W3 privacy pipeline review (C1 to C7)

Scope: `rust/crates/memory-core/src/privacy/{mod,document,launch,names,text}.rs` (1497 lines), consumers `memory/src/capture.rs`, `hook.rs`, `resolve.rs`.
Read-only; no builds. Line numbers are from the files as read. Items marked [inferred] were not confirmed by reading code.

## 1. Verdict

| Crit | Verdict | Why |
|---|---|---|
| C1 boundaries | mostly sound | Typestate is real and sealed (`mod.rs:146-176`). `document.rs` mixes five concerns. Two independent "is this a config" classifiers (locator `profile_of` and basename `document::recognizes`). |
| C2 extensibility | mostly sound | New basename or env name = 1-2 edit sites. New config family = 5-6 sites. New `SourceKind` defaults to `OtherKnown` silently. |
| C3 maintainability | mostly sound | Invariants are in types at the outer layer. Inside, `url` (76 lines), `literal` (74), `words` (70) are long, early-return heavy; `document.rs` re-parses subtrees. |
| C4 names | mostly sound | Core types excellent. Helper names (`kind`, `copies`, `flag`, `matches`, `binding`) are vague. |
| C5 data structures | mostly sound | Sealed capability types remove branches at the call site. Bool/Option flags in `ResolvedSourceFacts` and `PolicyContext` re-introduce 5 branches in `admit`. Hand-written `binding()` is fragile. |
| C6 DRY | mostly sound | Policy lists are each defined once. Duplication is in small regexes, the `[redacted]` literal (16x), and the "needs rules" predicate (4x). |
| C7 YAGNI | mostly sound | Few dead items. Three `pub use` helpers have no user outside the privacy module. Some unreachable branches. |

## 2. What is good

- Sealed capabilities, enforced by `compile_fail` doctests: `RedactedBytes`, `AuthorizedBody`, `AdmittedSource` have private fields, no serde, no Default (`mod.rs:5-24`, `146-176`). The doctests also pin that these types cannot be deserialized.
- `UnknownReason` is a separate enum from `Reason` and `Admission` has a third arm `Unknown` (`mod.rs:75-82`, `136-143`). Unknown cannot be mistaken for allow. `capture.rs:339-343` maps it to `Blocked`, not `Withheld`.
- Typed rule table is also the digest input (`document.rs:7-8`, `digest_rules` at `121`). One source of truth, no drift between spec and executable rules.
- `launch.rs` `Slot` enum plus `safe(slot, value)` (`launch.rs:~65-160`) replaces what is likely a chain of per-flag regex branches with one lookup. Each variant is documented.
- Fail-closed on regex compile failure in the credential path: `is_credential` returns true if the backup-suffix regex is None (`names.rs:30-32`).
- `prefix_stable` (`mod.rs:155`) carries a precise contract in its doc comment rather than a bare bool.

## 3. Findings, ranked

### Q1 answer (explicit)
**YES, the "safe to upload" type is unforgeable outside the module.** Fields of `AuthorizedBody` (`mod.rs:172-176`), `RedactedBytes` (`166-169`), `AdmittedSource` (`146-149`) are private (not even `pub(crate)`). There is no `sim` feature or `cfg(sim)` in `memory-core` (rg on `Cargo.toml` and `src` found none; `sim` exists only in `memory/Cargo.toml:25` and concerns SyncPolicy). The only constructors are `admit` (`mod.rs:264`), `transform` (`315`), `authorize` (`340`). Consumers take `&AuthorizedBody`: `capture.rs:562` (`slice`), `environment.rs:147`, `manifest/schema.rs:322`. So "upload unscrubbed bytes" is unrepresentable at those call sites; this is type-enforced, not convention. Caveat: the shell supplies `ResolvedSourceFacts` with public fields (`mod.rs:~100-120`), so a wrong `profile` or empty `identities` is a shell trust issue, not a type hole (documented as "Observations, not authority"). No path found where bytes reach upload without the pipeline. No high-severity item.

### F1 (medium, C5/C3) `authorize` consumes and re-admits; callers clone facts to satisfy it
`mod.rs:340-363` takes `AdmittedSource` by value, then calls `admit(context, source.facts)` again and re-matches `Allow(admitted) => match admitted.facts.owner { None => Deny(UnknownOwner) }`. The `None` arm is unreachable because `admit` already denies empty/absent owner (`mod.rs:~304`). Because of the by-value signature `capture.rs:659-668` (`admitted_clone`) clones `ResolvedSourceFacts` and calls `admit` a third time per body (`step` at `capture.rs:339` is the first). Result: three admissions per authorized body, a dead branch, and a clone helper that exists only to feed the signature.
Remedy: `authorize(&AdmittedSource, RedactedBytes, &PolicyContext)`; compare binding, then re-run only the cheap checks (or just the binding check, since binding already covers context+facts). Delete `admitted_clone` and the unreachable arm. ~25 lines removed, 1-2 h.

### F2 (medium, C1/C3/C6) `document.rs` bundles five concerns; "is config" decided in two places
`document.rs` contains: (a) rule tables and digest (`40-135`), (b) strict JSON value parser (`137-230`), (c) TOML bridge (`231-255`, `324-350`), (d) walk/redact/copies tree passes (`352-445`), (e) URL scrub (`494-569`) and argv scrub (`571-610`). Also `args_scrub` calls `scrub` recursively for inline JSON args (`571-580`), so (e) depends on (b)-(d).
Separately, `resolve.rs:83-99` `profile_of(SourceKind)` decides Config/TextAsset, while `document::recognizes(basename)` decides structural scrub. `mod.rs` combines them with the pair `Profile::Config | Profile::TextAsset` checked at `mod.rs:155-161`, `mod.rs:~276` (admit), `mod.rs:~330` (transform), and `capture.rs:349`. Four copies of the same predicate.
Remedy: split into `rules.rs`, `value.rs`, `url.rs` (about 610 lines into 4 files of 100-200; mechanical, 1-2 h). Add `AdmittedSource::needs_rules(&self) -> bool` and use it in all four places (about 10 lines, 30 min).

### F3 (medium, C2) A new `SourceKind` falls into `OtherKnown` silently
`resolve.rs:97` `_ => Profile::OtherKnown`. If a new config-like kind is added and not listed, it is admitted without the text guard and without the rules-digest requirement unless its basename happens to be in `CONFIGS` (`document.rs:40-52`). The basename recognizer is a good backstop, but the default is permissive. Remedy: make the match exhaustive (list the remaining kinds explicitly) so adding a kind is a compile error. ~8 lines, 20 min.

### F4 (medium, C5) Evidence flags as bools/Options instead of a resolved/unresolved type
`ResolvedSourceFacts` carries `path_resolved: bool`, `root_resolved: bool`, `file_identity: Option<u128>`, `owner: Option<String>` plus `PolicyContext.valid: bool`, `rules: Option<[u8;32]>`. `admit` (`mod.rs:264-312`) then re-derives 5 Unknown/Deny branches from them, and `owner: Some("")` is a legal state handled by `is_none_or(String::is_empty)`. An enum `Resolution { Resolved{...}, Unresolved(UnknownReason) }` built by the shell would make the invalid states (resolved but no file identity) unrepresentable and drop about 3 branches. Cost: touches `resolve.rs` and `capture.rs` fixtures. 3-4 h; medium value, so optional.

### F5 (medium, C3/C5) `binding()` hand-serializes every field; new field silently unbound
`mod.rs:211-262`: 50 lines of manual `h.update` sequences for context and facts, including a `Profile -> u8` mapping that duplicates the enum (`mod.rs:253-259`). Adding a field to `ResolvedSourceFacts` or `PolicyContext` compiles without binding it. Remedy: destructure both structs exhaustively in `binding` (`let ResolvedSourceFacts{identities, ..} = facts` without `..`) so a new field is a compile error; give `Profile` a `#[repr(u8)]` or `fn code()` like `Mode::code` (`document.rs:29`). ~10 lines, 30 min.

### F6 (medium, C3) `parse` re-parses subtrees; quadratic-ish
`document.rs:179-230`: for each object/array it reads `Box<RawValue>`, then calls `parse(raw.get().as_bytes(), depth+1)` which again does `serde_json::from_slice::<Box<RawValue>>` on the subtree, then `from_str` again with a visitor. Each nesting level re-scans its whole subtree, so cost is O(depth x size), bounded by depth 128 and 8 MiB (`MAX_PROTECTED_BYTES`) but not small [inferred; no benchmark seen]. Hand-rolling versus serde_json is justified: serde_json `Value` silently keeps the last duplicate key and loses number lexemes; the code needs duplicate rejection and `RawValue` numbers (comment `document.rs:133-137`). But a single-pass `DeserializeSeed`/visitor over `serde_json::Deserializer::from_slice` (recursive `Value: Deserialize` impl with the same duplicate check and a depth counter) does the same in about 40 lines and parses once. Depth limit is explicit (128 at `document.rs:180` and `232`). Remedy: 1-2 h; keep the existing duplicate-key tests as the oracle.

### F7 (low, C6) Small duplicated knowledge
- Identifier regex `^[A-Za-z_][A-Za-z0-9_]*$` appears as inline literals twice in `text.rs` (`~115`, `~140`); flag-name regex `^-{0,2}[A-Za-z0-9][\w.-]*$` twice in `document.rs:~577,~588`; `SAFE_VALUE` body is repeated inside `MODEL_VALUE` (`launch.rs`). Name them as constants beside `NUMBER`/`REFERENCE_VAR` in `names.rs`.
- `"[redacted]"` literal appears 16 times across the module; one `const REDACTED` removes the chance of typos and makes `names::reference` (which also tests `"[redacted]"`) obviously coupled. 15 min.
- `names::matches` (`names.rs:99`) calls `Regex::new` on every invocation (12 call sites in the module incl. per-line in `text.rs`). Go `regexp.MustCompile` habit carried over; the file already has the `OnceLock` pattern at `names.rs:7-10`. Use `LazyLock<Regex>` per pattern; this also removes the "compile error means false" ambiguity (see Q4). 1 h.

### F8 (low, C7) Surface with no non-test user
`pub use names::{basename, is_credential, is_secret_name}` (`mod.rs:32`): rg over `memory/src`, `memory-core/src` (outside `privacy/`), `memory-reader`, `xtask` shows no callers. Used only inside privacy and by tests (`is_credential` 2 hits, `basename` 1 hit in `memory-core/tests`). `TRANSFORM_VERSION` (`mod.rs:37`) is only read by `rules_digest`. Make them `pub(crate)`/private; tests can go through `admit`. Note: my first count with `--glob '!privacy/*'` did not exclude the directory, so I confirmed absence by listing hits. 10 min.

### F9 (low, C4) Names (10 least clear, with better ones)
1. `mod.rs:211` `binding` -> `admission_fingerprint`
2. `document.rs:85` `kind` -> `family_of`
3. `document.rs:91` `recognizes` -> `is_protected_document`
4. `document.rs:426` `copies` -> `propagate_replacements`
5. `document.rs:599` `flag` -> `scrub_flag_value`
6. `document.rs:446` `redact` -> `redact_leaf`; `Mode::Nothing` (`document.rs:17`) -> `MustBeEmpty`
7. `names.rs:99` `matches` -> `matches_pattern`; `names.rs:103` `reference` -> `is_safe_reference`
8. `names.rs:7` `suffix` -> `backup_suffix_regex`
9. `text.rs:81` `literal` -> `assigns_literal_secret`; `text.rs:11` `words` -> `split_commands`
10. `launch.rs:150` `safe` -> `is_safe_value`; `launch.rs:139` `opaque` -> `looks_like_token`
Exemplary: `UnknownReason` (`mod.rs:75`), `AuthorizedBody` with doc "no serde, Default, arbitrary From or public fields" (`mod.rs:171`), `prefix_stable` (`mod.rs:155`), `Slot` variants (`launch.rs`).

### F10 (low, C5/C6) `Family` enum plus `rules()` match is ceremony
`document.rs:40-52` maps basenames to `Family`, then `rules(family)` (`94-119`) maps `Family` back to a const slice via a 5-arm match, allocating `Vec<(Vec<String>, Mode)>` on every call (also from `digest_rules` per name, and per scrub). A `CONFIGS: &[(&[&str], &[(&str, Mode)])]` plus one `Claude` special case (extra MCP prefixes) would drop the enum, the match and the allocations. Adding a family currently needs: enum variant, `CONFIGS` row, rule const, `rules()` arm (and `from_toml` special-case if TOML). ~20 lines saved, 1 h.

## 4. Q4: Wildcard match arms and fail-closed table

All `_ =>` arms in scope (rg `^\s+_ =>|^\s+_ if`): four.
| Site | Arm | Direction |
|---|---|---|
| `launch.rs:103` `flag_slot` | `_ => Slot::Plain` | Least specific shape (`SAFE_VALUE`), but reached only for flags already in `VALUE_FLAGS` and still passes `is_secret_name` and `secret_shaped`. Safe, but adding a value flag with its own shape requires remembering this match. |
| `launch.rs:112` `env_slot` | `_ => Slot::Model` | Gated by `ENV_ALLOWLIST`; Model shape is restrictive. Safe. |
| `text.rs:69` | `_ => value.push(c)` | Ordinary character in tokenizer. Not a policy decision. |
| `text.rs:108` | `_ => return false` in fish `set` option parsing | **Fail-open for the guard**: an unrecognized `set` option (e.g. `-u`) means "not a literal secret", so the line is not flagged. Heuristic text guard, documented as "not an evaluator"; flag as low. |
| `document.rs:228` | `_ => Ok(Value::Number(raw))` | Any other leading byte becomes a number lexeme; serde_json already validated syntax, so safe. A `Some(b'-' \| b'0'..=b'9')` arm plus `_ => Err` would be self-documenting. |

Other default-allow shapes worth knowing (all documented, none a bypass):
- `text::check` returns `Ok(())` for >1 MiB, NUL bytes, non-UTF-8, and files whose all identities have a code extension (`text.rs:156-180`). Intentional scope limits of the guard.
- `names::matches` returns false on regex compile error (`names.rs:99`): closed in `launch::safe` (value rejected), OPEN in `text.rs` identifier checks (name not recognized so not flagged). A `static_patterns_compile` test covers only some patterns (`names.rs:121-129`); a `LazyLock` (F7) removes the question.
- `Profile::Unknown` -> `Deny(UnknownKind)` (`mod.rs:~298`); unknown policy -> `Unknown(...)` -> `Blocked`. This is the explicit enum design the question asks for. `withhold_of` (`capture.rs:226-237`) is exhaustive without wildcard.
- `unwrap_or_default`/`is_none_or`/`.ok()` appear 15 times in the module; spot-checked ones in `sanitize_launch` (`launch.rs:~170-205`): defaults produce `[redacted]` or empty, i.e. closed.

## 5. Q2: lists defined once?
- Credential names/dirs: only `names.rs:36-62`. Protected basenames: `document.rs:40-52` (`CONFIGS`), once. Env allowlist `launch.rs ENV_ALLOWLIST`, argv allowlist `PROGRAMS/FLAGS/VALUE_FLAGS`, value shape `SAFE_VALUE`/`*_VALUE`, key prefixes `KEY_PREFIXES`: each once.
- `memory` crate does not re-derive any list; the only related logic there is `resolve.rs:83` `profile_of` (kind -> profile), see F2/F3.
- Tests: `memory-core/tests/privacy_port.rs:265-278` lists credential names again as a literal table. That is an independent oracle (fine); it would be a copy only if it imported the constant, which it does not.
- Partial overlap: secret-name concept lives in `is_secret_name` (`names.rs:82`) and key-shaped token concept in `KEY_PREFIXES` (`launch.rs`); they are different rules (names vs values), not duplication.

## 6. Q5: extension cost
| Change | Edit sites | Proportional? |
|---|---|---|
| New protected basename (credential) | `names.rs:36` list + test row | yes (1) |
| New config basename for existing family | `CONFIGS` row (`document.rs:40`); digest auto-updates; bump `TRANSFORM_VERSION` | yes (2) |
| New allowed env name | `ENV_ALLOWLIST` + `env_slot` arm (else defaults to Model) | ok (2); the default hides a forgotten shape |
| New value flag | `VALUE_FLAGS` + `flag_slot` | ok (2) |
| New agent config-scrub rule (new family) | `Family` variant, `CONFIGS` row, rule const, `rules()` arm, maybe `from_toml`, `resolve.rs profile_of` and `SourceKind` | heavy (5-6); F10 would cut to 2-3 |
| New source kind | `SourceKind` (locator) + `resolve.rs:83` `profile_of`; `_ =>` default hides omission | 2 sites but F3 |
| New Profile | enum, `binding` mapping (`mod.rs:253`), `admit`, `transform`, `prefix_stable` | heavy for a closed enum (5) |

## 7. Q6: port from Go, simplified vs carried over
Simplified: (1) Reasons are a closed `Reason` enum with `thiserror` display strings, content-free (`mod.rs:44-72`) rather than free strings; (2) rule table is typed and also the digest input (`document.rs:7`), removing the Go-era second textual spec; (3) `Admission`/`Authorization` three-way enums instead of `(ok, reason, err)` tuples.
Carried over: (1) per-call regex compile in `names::matches` (Go `MustCompile` at use); (2) `copies` global value-replacement pass (`document.rs:426`, "Go keepArgs" comments at `document.rs:~460`): scrubbing a leaf then propagating the replacement to equal strings elsewhere is a Go-era workaround for aliasing that a rule-based walk would not need if all aliases were covered by rules [inferred, not verified against Go]; (3) bool flags in facts and context (F4) mirror the Go struct; (4) `Mode` char codes (`document.rs:29-37`) exist only to feed the digest; a `#[repr(u8)]` discriminant does the same.

## 8. Q3: `document.rs` summary
610 lines. Not a plain "duplicate-key JSON scrubber": it is the config-family rule engine (JSON and TOML), a duplicate-rejecting parser, URL and argv scrubbers. Longest functions: `url` 76 lines (`494`, seven separate `"[redacted]"` early returns), `args_scrub` 28, `scrub` 40 (`256`), `walk` 35 (`352`). Recursion limits: explicit depth 128 in `parse` and `from_toml`, plus an `expansion_bound` (`297`) that checks scrubbed size against `MAX_PROTECTED_BYTES` with checked arithmetic. `walk`, `redact_all`, `copies`, `restore_toml_strings` are four hand-written traversals of the same `Value` shape; each is short and exhaustive (no wildcards), so they are readable though repetitive.
Other long functions in scope: `text::literal` 74 lines (`81`), `text::words` 70 (`11`), `launch::sanitize_launch` 50 (`163`). `words` and `literal` are state-machine style with nested loops and early returns; `literal` handles two shell dialects in one body (fish `set -gx` first half, POSIX `export` second); splitting by dialect would make each about 35 lines.

## 9. Checked and found fine
- Doctest coverage of non-constructibility (`mod.rs:5-24`).
- `withhold_of` exhaustive, no wildcard (`capture.rs:226`).
- Hook path: `sanitize_launch` is the only constructor of `SanitizedLaunch` (private fields, `launch.rs:5-8`); `hook.rs:242` consumes it; `manifest/schema.rs:464` revalidates (length, digest) on the manifest side.
- `transform` refuses oversize protected input instead of falling back to raw (`mod.rs:315-324`).
- Rules digest covers every basename and every rule (`document.rs:121-133`).

## 10. Not read
Tests under `memory-core/tests/privacy_*.rs` beyond grep hits; Go originals (`internal/capturepolicy`, `internal/scrub`, `internal/launch`) not opened, so Go-comparison points are from comments and structure only [inferred]; `DESIGN.md` section 8 and amendments A3/A4/A12.1/A13.3 not re-read in this pass; `resolve.rs` beyond lines 80-110; most of `capture.rs` and `hook.rs`; `environment.rs` and `manifest/schema.rs` only at the cited lines.
