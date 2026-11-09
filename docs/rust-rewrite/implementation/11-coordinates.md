# 11. Coordinates (slice 1, row 7d)

Status: implemented and tested synthetically; committed in d8fbc4f (`rust/workspace`, PR #55) and covered by later integrated gates (report 23). At the time of this report, coverage and gates.sh were not run.

## Semantics (`rust/crates/memory-core/src/coordinates.rs`)

The module is pure. The shell supplies declared roots, explicit alias pairs, directory-identity observations and the link chain as data.

- **Routes.** A route is `(agent, physical, root)`. Every declared root is a route to itself. An explicit `RootAlias` adds a route and its root must be declared (Go `Compile`, coordinates.go:65-99).
- **Proof by observation.** An observation of a path becomes a route only when its identity equals the identity observed at a declared root of the same agent. This is the `/var` ↔ `/private/var` and case-alias mechanism. Nothing is inferred from spelling, so case is never folded. Distinct identities stay distinct. An unproven spelling returns `Unowned`.
- **Consistency.** `validate_routes` mirrors Go `validRoutes` (coordinates.go:127-154). A route nested under another must project to the same root. Alias nesting deeper than 8 is `TooDeep`. Overlaps are rejected as `Ambiguous`, not resolved by longest prefix.
- **Resolution.** The path is projected through the first matching route. The owner is then the longest declared root containing the projection, so nested roots keep their own identity (Go `Coordinate`, test :56).
- **Namespace.** The namespace is `<agent>-<first 16 bytes of sha256(root) in hex>`. It is a valid `locator::RootNamespace`, and the same root under another agent gives a different namespace.
- **Identities.** `identities` is the union of the whole shell link chain (kept verbatim, even if invalid), the resolved path, the logical path and every equivalent spelling. No intermediate is dropped, so deny checks can run over all of them.
- **`legacy_roots`.** These are the physical and logical root spellings for legacy namespace policy checks only. They mirror Go `PolicyCoordinates` and are never used for classification.
- **`verified`.** It re-resolves the coordinate's agent and path. A request-supplied root, logical path or namespace is never trusted.

## Go fixtures ported

The rows are in `tests/core_coordinates.rs`:

- Default and custom roots with no filesystem: coordinates_test.go:29.
- Nested projection and order independence: :56.
- Parent alias owning a nested root: :104.
- Rejected declarations: the Go reject table.
- Bounds and depth: the Go bounds test.
- `/var` alias, case alias and distinct case-sensitive directories: location_test.go:12,70,96, expressed as shell observations.

## Differences from Go

1. Go `Compile` takes only declarations. Rust adds `Observation` proof, because the pure core cannot stat. Go checks identity with `os.SameFile`, in `capturepolicy.Excludes` and its tests.
2. Go leaf aliases (exact-file aliases) are not ported. Row 7d needs only root aliases. A leaf can be added later.
3. Go `Compile` rejects an alias whose root is not declared exactly, and Rust does too. Go also requires `owner(root) == root`. In Rust that is implied by the root being declared.
4. Go has no `MAX_LEAVES`. `MAX_ROUTES = 128` here counts roots, aliases and observations together, which is stricter than Go.
5. Go has no root-relative path cleaning. Rust `valid_path` rejects empty, `.` and `..` components. Go uses `filepath.Clean(p) == p`. Go does not check UTF-8 because Rust `&str` guarantees it.
6. Two observations of the same path with different identities, or two declared roots with the same identity, are `Ambiguous`.
7. Go has no namespace and no fingerprint. Rust has no cursor fingerprint. `Inventory` is `Eq`, and tests assert equality across input orders.
8. A `/` root is supported, which Go also allows.

## Requested changes to files I do not own

None.

## Tests

- 11 table tests, plus a guard test that fails if the generators never produce a shape.
- Hegel and proptest properties:
  - (a) Compile and resolve results are independent of the order of roots, aliases, observations and chain.
  - (b) A path resolves only if a declared root, an explicit alias, or an observation with proven identity justifies it, using an independent justification function. The generators must produce proof-only resolutions.
  - (c) Every chain spelling, the path and the logical path appear in `identities`, and `verified` holds.
- Both run 2000 cases.

## Commands (pinned Nix, `CARGO_TARGET_DIR=target/w-coord`)

- `cargo clippy -p memory-core --lib --test core_coordinates --locked -- -Dwarnings`: exit 0.
- `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory-core --test core_coordinates -- --test-threads=2`: exit 0 (14 passed).
- `rustfmt --edition 2024 --check` on both files: exit 0.
- The exit statuses were captured directly from cargo, not through a pipe.

## sha256

- coordinates.rs: `82fd81b4e01240c2e8e6c6183b257913cfb9fbdbecd649e5adb1189e3f8b3c79`
- core_coordinates.rs: `a19fe01aeaf0a28854de4d9534643c0dc35e883e3d38ea88e63b2673c95d9029`

## Limitations

- The coverage percentage was not measured. Every branch has a test row as far as I can see, but that is unverified. `CoordError::InvalidPath` for a bad alias root and the `project` empty-join branch are the least certain.
- The properties use a small path pool, so deep real-world nesting is covered only by the table rows.
- The shell is trusted to report identities honestly. The module cannot detect a lying observation.
- The module has no wire format. Nothing here is wired into `locator` or `privacy` yet. The shell must pass `namespace` to `RootNamespace::new` and `identities` to `ResolvedSourceFacts.identities`.

Paths released.
