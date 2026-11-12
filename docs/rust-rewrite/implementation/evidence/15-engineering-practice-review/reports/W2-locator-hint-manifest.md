# W2 review: locator, hint, manifest, schema export (C1-C7)

Read-only; no builds. Counts via rg over `rust/crates/*/src`. Line numbers read from the files at HEAD 41a07ff.

## 1. Verdict
| Crit | Verdict | Why |
|---|---|---|
| C1 boundaries | mostly sound | Pure modules, private Piece fields, strict/tolerant hint split (hint.rs:1-9). Launch evidence is defined twice across hint and manifest. |
| C2 extensibility | weak (locator), mostly sound (manifest/hint) | New agent = ~8 edit sites in code branching, not a table (see F2). |
| C3 maintainability | mostly sound | Small functions overall; `pi_session` (68 lines, 3 modes) and `apply_one` are the dense spots. |
| C4 names | mostly sound | Terse locator names (`Res`, `Hit`, `esc`, `ident`); finding-id labels (R1..R5) and "Go ..." history in comments. |
| C5 data structures | mostly sound | Manifest enum + Option-free Delta/Checkpoint, PathHint/LaunchHint sum types are good. Locator is branch-heavy where a rule table would do. |
| C6 DRY | weak in 2 places | Launch record + digest duplicated with DIFFERENT algorithms; wire-mirror structs in recovery.rs; field copying SourceWrite/SourceEntry/CheckpointSource. |
| C7 YAGNI | mostly sound | `Role`/`ConfigAsset` payloads have no non-test reader; `Encoding::Zstd3` + encoded_* fields are placeholders. |

## 2. What is good
- Hint wire shape has ONE definition: `HintRecord` (hint.rs:~330) is serialised by `encode` (hint.rs:~380) and parsed by `classify` (hint.rs:495) via serde, `deny_unknown_fields`. Hand-rolled code is only the newline/marker resync `scan_line` (517), which serde cannot do. Justified.
- States are types: `PathHint::{Absent,Present}`, `LaunchHint::{None,DigestOnly,Full}`, `Reduction`, `HintSchema::V1` (hint.rs:~106-340) instead of Options and booleans.
- Manifest delta/checkpoint is an enum `Manifest { Delta(Delta), Checkpoint(Checkpoint) }` (schema.rs:~870) with distinct structs, so `previous`/`checkpoint` Option-ness is encoded per kind (Delta.previous is non-optional, schema.rs:~680). `build` (build.rs:57) and `fold` (build.rs:139) read linearly.
- Wire types ARE the domain types: Serde derives on `Manifest`, `Change`, `SourceWrite`, `EpochRecord`; no separate DTO layer in manifest proper (only recovery.rs mirrors, see F4).
- Schema export is generated: xtask/src/main.rs:16-36 uses `schemars::schema_for!` on the real types and `--check` fails on drift (main.rs:42-44). Single source of truth for shape.
- `locate` is total and returns `Located::{Source,UnknownKind,UnknownOwner}` (locator.rs:~318-342) with `withhold_reason()`: the withhold is a value, not an error path. `OwnerState::apply_changes` (state.rs:~75) is atomic by clone-and-swap, easy to reason about.
- `ManifestError` is `Copy`, closed, never carries input text (error.rs:3).

## 3. Findings
**F1 (high, C6/C1) Launch evidence defined twice, with different digests.**
`hint::LaunchRecord` (hint.rs:~262-320) and `manifest::LaunchRecord` (schema.rs:~432-475) have identical fields (digest, argv, env), identical accessors and a `launch_digest` each: hint.rs:275 hashes `"memory.launch/1\0argv"` + length-prefixed parts; schema.rs:472 hashes `serde_json::to_vec(&(argv, env))`. Same input, two different digests. hint.rs:~311 documents the hint digest as "used to deduplicate launch records on the owner's manifest" [inferred: if the daemon dedups hint digest against manifest digest, they never match; I did not read the daemon]. Remedy: one `LaunchEvidence` type in `domain` or `privacy` (next to `SanitizedLaunch`), one digest function, both wire formats embed it. About -60 lines, 2 h, plus a schema regen (schemas/memory.hint.1.json and manifest both contain it).

**F2 (medium, C2/C5) Locator is per-agent branching code, not a rule table.**
Mapping lives in functions `claude` (locator.rs:561), `claude_top` (591), `claude_external` (612), `claude_project` (624), `claude_transcript` (659), `codex` (687), `codex_top` (706), `codex_rollout` (732), `pi` (755), `pi_asset` (765), `pi_session` (795) with only the file/dir -> `ConfigAsset` lookups as tables (512-559). Repeated pattern: "if dir in table -> asset; match parts slice -> shared(kind, class, role, parts)". 11 of the 12 `shared(...)`/`asset(...)` arms differ only in (prefix, kind, class). Edit sites for a 4th agent: `Agent` enum + `Agent` match in `locate` (864-889, the `(RootLayout, Agent)` arms; the wildcard `(RootLayout::ClaudePrimaryHome, _)` hides non-exhaustiveness), new `<agent>()` fn(s), two new tables, new `SourceKind` variants (domain, outside scope), new `ConfigAsset` variants (233-290), `RootLayout` if a special root, `event_kind` in hint.rs:179, `coordinates.rs` (2 hits), `memory/src/hook.rs` (2 hits). A new kind for an existing agent: one new arm in that agent fn + `SourceKind` variant (+ `ConfigAsset` if asset). Cheap for the second case, ~8 sites for the first. Remedy: a `&[Rule { prefix: &[&str], kind, class, role }]` table per agent for the ~20 simple "dir/prefix -> kind" arms (claude 561-590, codex 687-705, claude_top, codex_top), keep functions only for the genuinely structural cases (Claude project/session layout, Codex rollout, Pi session). Est. -120 lines, 4-6 h; do it with the next agent, not before. 924 lines (incl. ~300 lines docs/tests-free) are mostly rules encoded, so size is justified; the shape, not the size, is the issue. Duplication between the three agents is incidental, not knowledge duplication, except `*_FILES/*_DIRS` overlaps (`rules`, `skills`, `keybindings.json`, `AGENTS.md` repeated across tables 512-559).

**F3 (medium, C7) `Role` and `ConfigAsset` have no non-test reader.**
`Located::Source.role` / `Role::Asset(ConfigAsset)` (locator.rs:233-310, 318-330) are constructed everywhere in locator.rs but `rg '\.role|role,'` in `*/src` outside locator.rs finds nothing; the only readers are `memory-core/tests/core_locator.rs` and `memory/tests/realfs.rs:259`. 26 `ConfigAsset` variants + 7 `Role` variants (`Superseded` constructed once, `SessionIndex` once) are carried to answer a question nobody asks; the `SourceKind` already says `ClaudeConfig` etc. Test pins behaviour of a Go-parity label. Remedy: if parity with Go roles is wanted, say so in the module doc and keep; otherwise drop `Role`/`ConfigAsset` from `Located` (-90 lines, simplifies `asset()`, `shared()`, `Hit`) or fold into `SourceKind`. 2 h. Needs operator decision [inferred: may be wanted for a later reader/analytics slice].

**F4 (medium, C6/C5) recovery.rs mirrors the wire schema in 6 hand-written structs.**
recovery.rs:10-95 (`RawEpoch`, `RawChange`, `RawRecord`, `RawSource`, `RawDelta`, `RawCheckpoint`; 5 `#[expect(dead_code)]`) restate `Delta`/`Checkpoint`/`Change` field lists with `IgnoredAny` to be tolerant of everything but position. Also repeats rules from schema.rs: checkpoint seq multiple of 64 and `seq==0 <=> previous none` (recovery.rs:148-153 vs schema.rs:~810-816), delta seq not multiple (recovery.rs:~178 vs schema.rs:~703). When a manifest field is added, three places change (wire struct, Raw struct, state copy). The A12.3 cap itself is clear: one constant `CHECKPOINT_INTERVAL` (mod.rs:17), one check at build.rs:140 (`chain.len() as u64 > CHECKPOINT_INTERVAL`, unchecked cast, fine on 64-bit). Remedy: expose `Checkpoint::check_seq_and_link()` / `Delta::check_seq_and_link()` and call from both; keep Raw structs only if the tolerance vs strict-schema distinction (R5) is still required, else deserialise the real types with a lenient flag. ~-40 lines, 2-3 h.

**F5 (medium, C6) Field-by-field copies between three shapes of "a source".**
`CheckpointSource` (schema.rs:~620), `SourceEntry` (state.rs:9-18), `SourceWrite` (schema.rs:~485) share kind/path/class/epochs. Copies: state.rs:44-56 (`from_checkpoint`), 61-72 (`to_checkpoint_sources`), schema.rs `SourceWrite::entry`/`record` (~510-525). Remedy: `SourceEntry` = `CheckpointSource` minus `id` (or make `OwnerState.sources: BTreeMap<SourceId, SourceBody>` and let `CheckpointSource { id, #[serde(flatten)] body }`). Removes ~30 lines and one class of "forgot the new field" bug. 1-2 h. Related: `Delta::new` takes 8 args under `expect(too_many_arguments)` (schema.rs:~700); `Header` already exists, so `Delta::new(header, seq, previous, checkpoint, changes)` is 5.

**F6 (medium, C7) Placeholder wire fields.**
`Piece` carries `encoding`, `encoded_len`, `encoded_sha256` (schema.rs:~300-310) and `from_authorized` fills them equal to raw "until zstd lands" while hardcoding `Encoding::Zstd3` and key `objects/<sha>.zst` (schema.rs:~340-345). The manifest therefore declares zstd for bytes that are not zstd today. `adopt_stored`, `StoredEncoding`, `verify_encodings` (schema.rs:~918, only test callers: 2 hits, test + doc) exist for the dedup-race rule R2. Defensible if zstd is the next slice; otherwise a wire lie. Remedy: label the interim state in the schema docs or implement zstd before the first real bucket write (no-migration policy, DESIGN 6.5, means these wire values become permanent). Also `Encoding` has one variant.

**F7 (low, C5/C3) Hint builder and event table.**
`build_line_with_limit` (hint.rs:401-470, 70 lines) repeats `if paths {x.clone()} else {Absent}` three times (hint.rs:~420-434) and builds attempts through two near-identical `match` arms. A `fn without_paths(&self)` on `HookInput` and a `[Reduction; 3]` iteration would give ~35 lines. `event_kind` (179-212) lists "sessionstart/stop/..." per agent; as `const` tables per agent it would shrink and the "add an agent" step becomes data. Same agent-branching theme as F2. 1 h.

**F8 (low, C4) Process-history in names/comments.**
Manifest docs use finding labels `R1`, `R2`, `R3`, `R5` (8 occurrences in manifest/*.rs, e.g. mod.rs:3-9, recovery.rs:~143 "Recovery projection (R5)"); locator.rs has 8 "Go ..." references (`Go classifyLayout`, `Go RoleUnknown`, `Go agentDir=false`, locator.rs:~783, 810-815, 874) that describe the Go code being retired, not the job. Replace by the rule itself in one sentence; delete Go references once cutover is done. 0.5 h.

**F9 (low, C5) Hand-written JSON Schema impls duplicate validation.**
`Timestamp` (schema.rs:~190), `HintPath` (hint.rs:~93-101: `"maxLength": 1024` literal vs `MAX_PATH_BYTES`) hand-copy constants/patterns. Shape-only schemas then drift silently: schema.rs `check_path`, `token`, `argv.len()>32`, `env.len()>8` never appear in `schemas/*.json`. The `--check` guard cannot catch this. Remedy: use `MAX_PATH_BYTES` in the macro (`json_schema!` accepts expressions via format) and add a sentence to the schema doc that JSON schemas describe shape only. 0.5 h.

**F10 (low, C7/C4) `range-error.json` is unexplained.**
schemas/range-error.json is `{"title":"RangeError", ..., "type":"null"}` (6 lines): a unit struct exported as a "wire schema" (xtask/main.rs:20-23). Nothing in xtask, schemas dir or the type says who consumes it or why it is a committed contract. RangeError is hint.rs `scan`'s error (hint.rs:~565), an in-process value, not persisted [inferred; I saw no serialisation use]. Remedy: remove from `exported()` or add a one-line comment naming the consumer. 10 min.

## 4. Longest functions / nesting (in scope)
| fn | path:line | lines | note |
|---|---|---|---|
| `build_line_with_limit` | hint.rs:401 | ~70 | F7 |
| `OwnerState::apply_one` | state.rs:93 | ~72 | 3 arms, depth 4 (match, match, match cmp) ; Extend arm could be `SourceEntry::extend(&w)`, Epoch/Replace `SourceEntry::start_epoch` |
| `pi_session` | locator.rs:795 | ~68 | 3 modes x 2 shapes; depth 5 at the `[name]` arm. A `PiMode` table of (allow_nonstem, sessionless_nonjsonl) booleans would drop 2 guards |
| `recover_projection` | recovery.rs:145 | ~65 | two near-identical arms (F4) |
| `build` | manifest/build.rs:57 | ~55 | linear, fine |
| `scan_line` | hint.rs:517 | ~38 | necessary |
| `locate` | locator.rs:863 | ~30 | 9-arm `(layout, agent)` match; custom-root branch inlines Go logic |
All others < 35 lines. Nothing deeper than 5.

## 5. YAGNI counts (src only)
- `Role::Superseded`: 1 src hit (constructed), 0 readers. `Role` read outside locator.rs: 0 in src (F3).
- `ClaudeSessionIndex`, `RootLayout::{PiCustomRoot,ClaudePrimaryHome}`: used by `memory` daemon (5/10/3 hits) fine.
- `build_line_with_limit` is `pub` with 3 hits: def, `build_line`, test; limit parameter only varies in tests. Low.
- `SchemaStatus`/`classify_schema`: pub re-exports (mod.rs:26-28), used only in `preflight`; could be `pub(super)`. Low.
- `HintScan.rejected_version`: 3 hits (def, set, test) [no daemon reader found]. Low.
- `HintEvent::{TaskCompleted,TeammateIdle}`: 2 hits each (event table + enum), no consumer logic distinguishes them except `flushes`. Fine as evidence, low.
- `EpochWriter`, `StoredEncoding`: used (capture.rs:651; 7 hits).
- Schema fields written never read: Delta/Checkpoint `captured_at`, `writer`, `launch` read by builder/tests only [inferred]; unavoidable for evidence.

## 6. Names
Least clear -> better:
1. `Res` / `Hit` / `Miss` (locator.rs:~446-452) -> `Match` / `Matched` / `Unmapped::{Kind,Owner}`.
2. `esc` (locator.rs:~416) -> `escape_id_part`.
3. `ident` (locator.rs:~427) -> `qualified_id`.
4. `shape` (locator.rs:~348) -> `matches_template`.
5. `dir_asset` (used on `*_FILES` tables, locator.rs:597, 713) -> `asset_for_name`.
6. `put` (hint.rs:270) -> `hash_len_prefixed`.
7. `token` (schema.rs:~196) -> `check_printable_ascii`.
8. `Op` (schema.rs:~565) -> `WriteOp`; duplicates the `Change` variants 1:1 (see F5 theme).
9. `Fact::{Unique,Ambiguous}` (locator.rs:~144) -> `SlugClaim` / `OwnerClaim`.
10. `Reject` (hint.rs:~490) -> `LineRejection::{OtherVersion,Malformed}`.
Exemplary: `Located::withhold_reason` (locator.rs:~334); `OwnerState::apply_changes` (state.rs:~75); `CHECKPOINT_INTERVAL` (mod.rs:17) and `Reduction::{LaunchDropped,PathsDropped}` (hint.rs:~325) say the job directly.
Process-history names: R1-R5 labels and "Go" references (F8).

## 7. Checked and fine
- No duplicate-key / schema-dispatch duplication: `preflight` (schema.rs:~125) shared by manifest, current and recovery.
- `parse_manifest` always validates after decode (schema.rs:~935).
- `state.rs` folding: from_checkpoint + apply_changes is easy to follow; validations dedup (DuplicateSource) done once per change set.
- Hint parse path uses `serde_json::Value` + typed output, key-targeted, ignores unknown keys, size cap 1 MiB; no clap. Justified for the hook path (a streaming `#[derive(Deserialize)]` with `IgnoredAny` would avoid the Value tree if latency matters [inferred, not measured]).
- The 3 JSON schemas other than range-error are produced by the 3 exported types; manifest.1.json (919 lines) is generated, not maintained.

Not read: domain.rs, privacy.rs, limits.rs, slice.rs beyond `RangeError`, the daemon callers of manifest/hint, tests, the design docs and amendments (relied on code comments for rule intent; A3/A8/A12.3 not re-verified against text), the full generated JSON schemas.
