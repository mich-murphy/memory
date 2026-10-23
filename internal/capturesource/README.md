# Bounded source discovery (offline candidate)

`Discover(ctx, Options)` is read-only: it produces `sessioncapture.Request` hints,
content-free issue codes with private local path coordinates, and independent root
coverage. It does not own bytes, publish, parse transcript JSON, open legacy state,
read credentials, or use R2. Home and learned roots must be explicit. Canonical
`.claude`, `.codex`, and `.pi/agent` defaults do not consult environment overrides or
read settings. Learned roots must be supplied by a trusted policy/root recovery layer.

## Runtime integration

1. Supply the effective exclusion-policy callback and collector config/state paths.
   A missing callback fails closed before traversal. Callback scope includes every
   identity from `capturepolicy.Resolve`; built-in credential/noise/config/state
   exclusions apply independently. The callback must itself be bounded.
2. Call with a fresh cursor for each sweep, then retain `Result.Cursor` for subsequent
   bounded calls until all coverage states stop being `bounded`. Resume with unchanged
   archive/machine/home/root inputs. Do not use a cursor concurrently. Close it on
   completion, replacement, cancellation, or shutdown. Restart after errors when ready
   to re-observe; completed cursors deliberately do not repeatedly emit the same work.
3. Independently feed returned requests to `CaptureIfChanged` even when other roots
   have issues. Preserve coverage separately; a queue success must not clear discovery
   or enrichment gaps. Recheck policy in the queue at capture and delivery.
4. Each root requests its own entry budget (default 256, maximum 4096). The
   effective per-root slice is `min(requested, max(1, floor(4096/rootCount)))`,
   applied before any root runs. Therefore at most **4096 directory entries total**
   are examined per call, with independent fair slices and resumable bounded root
   states; an early root cannot consume a later root's allowance. Traversal keeps
   directory readers across calls, so a large directory does not require repeated
   unbounded rescans. Default depth is 32, maximum 64; at most 128 distinct roots.
   A cursor retains at most 256 directory descriptors globally: each root receives
   `min(MaxDepth, floor(256/rootCount))` handles. Skipped deeper subtrees report
   `fd_budget` (or `depth_budget`), never complete coverage. Absolute source paths
   are also capped at 64 components for bounded one-path metadata admission.
   Root input order is stable and each root gets a slice on every call. Within a root,
   native directory enumeration order is retained, not a globally sorted inventory.
   Directory changes during traversal require a later fresh sweep, not an absence
   inference. This is an in-process cursor, not a durable checkpoint.

`Classify(ctx, Options, agent, absolutePath)` performs bounded single-path hook
admission without enumerating directories or parsing bodies. It uses the same policy,
classification, ownership and emission helper as periodic discovery. The longest
configured matching root owns overlapping namespaces in both paths; outer-root walks
skip independently inventoried nested roots. Unsupported/denied hints return an
exported `*ClassificationError` whose `Issue` retains root/path/code coordinates for
durable coverage. Diagnostics contain no source contents or raw OS errors.

Session IDs have disjoint `session/` and `resource/` namespaces, both qualified by
**full SHA256(clean absolute root)** before the adapter session/resource key. Legacy
40-bit adapter custom-root tags alone are not collision-resistant identity. Source IDs
use adapter keys, preserving known Codex archive/recompression identity. No
content-based identity merging or unknown rename inference is performed.
Main/subagent/tool-result roles are first-class; every other admitted adapter role is
an artifact with `adapter-role:` association. Session-owned children retain the same
session. Each agent-wide source has its own explicitly marked resource session, so an
unrelated resource failure need not become a session publication dependency.

## Implemented integration and explicit boundaries

- `Options.Derived` defaults to false: SQLite, compressed transcripts, and protected
  config retain explicit unsupported gaps. With opt-in, discovery selects transforms
  only; the public queue owns bounded preparation, validation and original provenance:
  - SQLite suffixes `.db`, `.sqlite`, `.sqlite3`: `sqlite-backup`, artifact role,
    adapter-derived `db/<relative>` source keys and `adapter-role:db`. Known directory
    session ownership is retained without parsing or enumerating sibling sources.
  - Recognized main/subagent `.jsonl.zst`: `zstd`. Other compression types/roles remain
    unsupported. Codex retains its explicit adapter source-key mapping across
    recompression; Claude/Pi retain separate physical `.zst` source keys while sharing
    session ownership. Discovery never infers an append/epoch relationship.
  - `scrub.Recognizes` JSON/TOML: `scrubbed-config`, artifact role and
    `adapter-role:config`. Compressed config needs a separate decode+scrub pipeline
    and remains an explicit gap. Unrecognized adapter config is never raw fallback.
  Requests include `transform:<kind>` associations, original physical paths and full
  root-qualified session IDs. Discovery reads no bodies and prepares no bytes.
  With `Derived && !OnlyRoots`, the canonical `~/.claude.json` is additionally
  checked as exactly one standalone file per cursor sweep, never a Home traversal.
  Hook and periodic requests share `config/claude.json`, the canonical Claude
  root-qualified resource identity, original path, and `scrubbed-config` transform.
  Explicit configured roots that already own this path take precedence and avoid
  duplicate standalone observation. Missing primary files produce `observed_absence`;
  policy, unreadable, alias and nonregular cases retain exact-file issue coordinates.
  The generic protected-export gap is replaced by this actual bounded check only
  with derived default inventory enabled. Otherwise the outside-inventory boundary
  and protected-export gap remain. Credential backups, active collector config/state
  and aliases remain excluded; no raw config permission or broad Home authority is
  introduced. Queue preparation remains the only source-body read/scrub boundary.
- Claude external UUID-addressed artifacts use `Options.LookupOwner` and the bounded,
  ambiguity-aware durable ownership index wired by the runtime. Without that callback
  they remain `ownership_integration_required`; unknown/ambiguous ownership is partial,
  never permission to scan all projects or infer ownership globally.
- Declared root routes/exact leaf aliases are supported through `Options.Coordinates`.
  Undeclared links remain withheld; credential identities remain denied. Logical and
  observed coordinates and restrictive identities survive into Queue policy checks.
  No automatic symlink traversal, target-path deduplication or inode merging is granted.
- Discovery itself emits `launch_snapshot_integration_required` independently of raw
  traversal: it does not collect launch/snapshot bodies or claim complete enrichment.
  The separate implemented native-evidence/enrichment lane owns that work and reports
  its own basis/completeness. A discovery marker is not proof that the lane is absent.
- Adapter-filtered files, nonregular files, executable files, and depth limits are
  explicit gaps. Unknown raw artifacts are preserved when admitted by the adapter and
  policy; transcript bodies and incomplete/malformed JSONL are never semantic gates.
- Missing roots produce `observed_absence`, distinct from empty roots and policy
  unavailability. None of these states requests deletion of retained bytes.

Metadata operations are entry/depth bounded, not hard wall-clock deadlines: local OS
filesystem calls can block and directory trees can mutate. Cancellation is checked
between entries and roots. Adversarial concurrent symlink replacement is not prevented
by discovery; queue identity-aware opening/rechecks remain mandatory. Issues and
requests per call are bounded by 4096 examined entries plus fixed per-root coordinates
(at most 128 roots). This entry/output bound is independent of the 256-directory-FD cap.
Actual native/eligible-inventory coverage still requires separate acceptance. The
integrated offline candidate and full gates are now implemented/passing; see the
[current handoff](../../docs/session-capture-handoff.md) and
[final evidence](../../docs/session-capture-offline-evidence.md).

## Historical focused audit checks (2026-10-23)

These checkpoints predate final integration. Their compile blockers and pending
alias/launch statements describe that date's scope, not the current implementation.

Pinned Nix: `CGO_ENABLED=1 go test -race -shuffle=on
-coverprofile=/tmp/capturesource.cover ./internal/capturesource` passed (85.6% statement
coverage); `golangci-lint run ./internal/capturesource/...` passed with zero issues.
Tests include full-root identity qualification, default/custom hook/periodic equality,
children/results, stable Codex rename, overlapping roots, denied ancestors, unsupported
sources, global FD budgets and explicit caller cursor close after cancellation.
Unreadable-root isolation is exercised when the process cannot bypass permissions.
No full/native/live/end-to-end gates, installs or private host inventory were run.

### Derived selection follow-up checks (2026-10-23)

The same pinned Nix focused race/shuffle/coverage command passed at **87.2%** statement
coverage after adding opt-in derived hint selection. Focused lint passed before the
last test-only idiom cleanup; final test/lint reruns were blocked by a concurrent
`sessioncapture/runtime_discovery.go` compile error (`RuntimeOptions.DiscoverBatch`
undefined), outside this package ownership. New tests assert default/custom hook-periodic equality, main/subagent ownership,
DB/config roles and provenance, physical versus explicit Codex recompression mapping,
unsupported config/compression, outside-inventory primary Claude config, aliases,
missing policy and active collector config denial. These are metadata-selection tests,
not an independent validation of queue transform implementation or complete artifact
acceptance. No full/native/live gates or private-host inventory were run.

### Global entry budget follow-up checks (2026-10-23)

Implemented the global 4096-entry call bound with fair per-root slices. Unit tests
check every supported inventory size through 128 roots against requested budgets,
including the maximum inventory/budget case (128 × 32 entries). An 18-file fixture
checks six roots receive independent slices, retain bounded states, and resume without
duplicates. Existing fixture assertions and configured upper limits remain unchanged.

After the concurrent runtime API compile issue was resolved, pinned Nix
`CGO_ENABLED=1 go test -race -shuffle=on -coverprofile=/tmp/capturesource.cover
./internal/capturesource` passed with **87.3%** statement coverage;
`golangci-lint run ./internal/capturesource/...` passed with **zero issues**. These
checks include the derived-selection regressions above. Full/native/live gates were
not run; no private-host inventory, installs or outside-package edits were performed.

### Standalone primary config follow-up checks (2026-10-23)

Implemented exact canonical primary config selection under `Derived && !OnlyRoots`.
Tests cover hook/periodic request equality, exact clean source key and stable full-root
resource ID, no Home enumeration, single observation per cursor, learned-root precedence,
disabled-mode boundaries, absent/nonregular/alias sources, missing/denied policy and
active collector config/state exclusions. Policy is checked on the original coordinate
before source metadata resolution, then again with known resolved identities. The
trusted Home directory and exact leaf are lstat-checked; no transcript/config body is
read by discovery. Launch/snapshot and general alias integration were incomplete at
this historical checkpoint; subsequent declared-coordinate and independent enrichment
integration is described above and in the current handoff.

Pinned Nix focused CGO race/shuffle tests passed with **87.3%** statement coverage;
focused package lint passed with **zero issues**. No full/native/live gates or
outside-package edits were performed.

## Declared coordinates (implemented offline candidate)

`Options.Coordinates *capturecoordinates.Inventory` opts into shared explicit root
routes and exact leaf aliases. Nil retains the legacy inventory/classifier behavior.
Adapters see logical paths/roots (via `adapters.ClassifyLogical`); requests retain the
observed byte pathname. A missing hint can select an existing declared equivalent,
without enumerating its parent. Policy receives logical, observed, and all resolved
identities before admission. Every encountered resolution spelling must map to the
same declared coordinate. Descendant directory links are not recursively discovered.

The in-process cursor binds the complete inventory fingerprint, inventories nested
logical roots after projection, and independently budgets route walks and final exact
leaf checks. Unavailable alternate routes and unfinished leaf slices remain partial
or bounded; a healthy route does not erase those gaps. Extreme inherited-route
expansion can exhaust the directory-FD allocation and reports `fd_budget`, rather
than opening descriptors beyond the global bound.

Discovery alone is not Queue ownership or an atomic-read guarantee. Queue integration
now derives authoritative coordinates from the same compiled inventory, verifies
`Request.LogicalPath`, and retains restrictive `Request.Identities` across pathname
substitution. Logical main-path ownership prevents physical aliases creating a false
conflict. Actual reads bind the pre-policy-callback identity to descriptor-relative
no-follow traversal. Associations and caller-supplied identities cannot create read
authority; they are provenance/restrictions only.
