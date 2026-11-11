# Capture security boundaries

> **Update (2026-11-07):** session opt-out is retired by operator decision (see README
> safety boundaries and contract section 6 step 2). The Go capture daemons were stopped on
> 2026-11-09; the installed Go hooks still honor the existing record until cutover. The credential, config-scrubbing and launch-argv
> boundaries below remain requirements and are ported by DESIGN.md section 8.

> **Historical Go scope (2026-10-23; opt-out instructions apply only until cutover):** These capture-policy and scrubbing boundaries remain safety requirements for the agreed [session-capture contract](session-capture-contract.md), including spool acceptance, repair and delivery. Unknown exclusion state must fail closed for the affected scope. The authorized rebuild/reset does not revoke opt-outs: preserve and reapply effective exclusions, including remote-only exclusions, and recheck policy before delivering queued work.

The implementation details below describe the existing Go paths and the protections
being ported. Rust currently implements pure policy/transform/launch boundaries, not
the real filesystem observer or upload sink; see the
[active handoff](session-capture-handoff.md#current-session-checkpoint) for evidence.
DESIGN.md section 8 governs the Rust pipeline.

The Go collector applies a known-file policy, not arbitrary transcript DLP. An unrecognised filename
remains byte-for-byte unchanged, even if its contents look like a supported config or contain a
literal credential. Copies renamed to unrelated filenames with no recognised path identity are
outside this policy.

Before reading, capture paths use `capturepolicy.Resolve` and check **every** logical, intermediate
symlink and final identity against `capturepolicy.Policy`. Known credential files, collector config
(including backups) and collector state are excluded. The primary `.claude.json` is permitted only
by the Claude adapter's explicit `ExcludesClaudeConfig` export/extraction path; that exception does
not permit its backups or any other excluded identity, and still requires scrubbing.
Configured config parents and state-directory ancestors are also compared by filesystem identity:
case aliases remain excluded without conflating distinct directories on case-sensitive volumes.
In the running Go path, persistent opt-out checks also compare existing root and recorded source-object identities.
These checks do not provide an atomic filesystem snapshot or defend against hostile concurrent
path replacement.

After reading, `scrub.Identities` applies the supported schema for **each** recognised filename in
those identities. Protection does not depend on which agent is collecting: a Claude skill symlink
to Pi's `models.json`, for example, must redact Pi provider credentials. Known backup suffixes are normalised centrally, as is filename case: editor copies (`~`,
numbered tilde backups, `.bak`, `.backup`, `.orig`, `.old`), eight-digit date suffixes, installer
backups, and common archive extensions (`gz`, `bz2`, `xz`, `zst`, `zstd`, `zip`, `7z`, `rar`,
`tar`, `tgz`). Credential exclusion does not depend on decoding a compressed backup. A compressed
protected config is withheld if its bytes cannot be parsed safely. Arbitrary suffixes such as
`auth.json.example` or `notes.key.txt` remain outside this known-filename policy. Shared names such as `settings.json` use their supported filename's rules,
not an agent-specific interpretation; distinct filenames in a symlink chain combine their rules.
This does not apply every supported schema to every JSON document.

Streaming capture must call `scrub.Recognizes(path)` for **every** resolved identity before choosing
a raw-byte streaming path. Any recognised identity requires the protected-config preparation path,
regardless of file size or collecting agent. `scrub.Applies(agent, name)` remains a compatibility
wrapper with the same agent-independent recognition. `File`, `Paths` and `Identities` retain their
agent arguments for compatibility, not schema selection.

Malformed protected JSON or TOML is withheld, not copied unchanged. JSON configs must be objects;
duplicate decoded object names, invalid Unicode and trailing documents are rejected. Selected
literal secrets and equivalent decoded string copies are redacted without rewriting object keys.
Supported environment references and Pi secret-command references remain references. Parser
errors must not echo credential values.

## Opt-in candidate source boundary

The default collector remains legacy; the implemented `capture-v1` candidate adds
source/component no-follow reads and pinned SQLite main/WAL provenance checks,
including sidecar/alias policy, plus immediate pre-publication policy rechecks.
Native-context and independent enrichment use bounded admitted evidence, not a
permission to read arbitrary Home/ancestor/managed paths. See the
[phase-3 boundary](session-capture-phase3.md) and
[independent enrichment contract](session-enrichment-contract.md).
These protections supplement—not replace—the exclusions and scrubbing above.
[Passing offline tests](session-capture-offline-evidence.md) do not prove hostile
filesystem snapshot atomicity, actual native/R2 behavior or installed acceptance.
No default activation or archive deletion follows from them.

## Previously archived data

These checks protect new captures. The existing v3 collector does not retroactively scrub or
delete remote objects: its append-only behavior retains already captured history, including
legacy namespaces. That describes v3 behavior, not a requirement to retain the old archive
forever. On 2026-11-09, by operator instruction, the Go writers were stopped, the bucket was
inventoried and the old capture prefixes were deleted (handoff brief; DESIGN amendment A2).
This page performs no deletion. New retention
policy follows DESIGN.md for Rust and the retained contract goals, not v3 collector-generation retention.

If a credential is known to have been archived, rotate or revoke it and treat remaining remote
objects and local evidence/backups as sensitive. Updating the collector, deleting the local
cache or changing a filename does not remove earlier remote bytes. Resetting the archive never
authorizes deleting local research/classification artifacts, original sources or secret-policy
state. While Go remains active, preserve and honor its effective opt-out records; do not remove
them incidentally. Rust session opt-out is retired: the existing record is removed only at the
controlled cutover step in [DESIGN.md section 18](../DESIGN.md#18-cutover), after Go is stopped.
Credential denial, configuration scrubbing and launch-argv protections are not opt-outs and
remain mandatory. No legacy opt-out migration is a Rust delivery prerequisite.
