# Opt-in capture-v1 hook candidate

This package is independent of legacy install and is **not production cutover**.
Planning/dry run do not install hooks. Explicit Apply can write only planned native
settings/extensions and private backups; it does not invoke native agents, write
trust caches, open source DBs, copy transcripts or switch the legacy installer/defaults.

API: `Build(Options{Home, Roots, Executable, ConfigPath, Remove}) (Plan, error)`;
`Apply(ctx, plan, false)` always returns without filesystem calls. Only explicit
`true` mutates. Empty Roots selects canonical `.claude`, `.codex`, `.pi/agent`
under Home; nonempty Roots selects exactly those explicit roots. A caller deriving
roots from runtime configuration must reject an empty configured inventory before
calling Build, rather than unintentionally enabling defaults. Limits: 128 supplied
roots, 4 MiB per settings file, 16 MiB aggregate retained before/after bytes.
Backpressure diagnostics contain no settings bodies.

Plan JSON contains paths, hashes, change facts, argv and outstanding acceptance,
never configuration bodies. Apply uses private internal snapshots, not mutable
exported change facts. JSON-decoded plans cannot apply. Malformed/duplicate-key
JSON and symlink components fail closed. ConfigPath must be an existing 0600
regular file; the executable must be regular, executable, not group/world writable,
and unsymlinked. Paths use capturedaemon.SafePath (including newline rejection).
Existing 0644 native settings may be read; generated files and exclusive random
backups are 0600. Writes, backups, removals and parent directories are fsynced.

Only the exact shell prefix `: agent-sessions-capture-v1; exec ` owns JSON
handlers. Commands invoke `[exe, capture-v1, hook, --config, config, --agent, agent]`
with stdin inherited and shellwords escaping. No legacy regexp ownership is used.
Unknown JSON fields and unrelated/legacy handlers survive. Codex config.toml and
trust entries are untouched. Pi owns only the distinct `agent-sessions-capture-v1.ts`
filename **and** exact first-line owned prefix. Pi `session_start` passes bounded SDK
context through stdin JSON to the self-command; `agent_settled` / `session_shutdown`
pass `--path` hints. Child output is ignored. Native facts are sanitized/allowlisted
by the CLI and again at durable adoption; there is no transcript read or queue/DB
processing in the extension itself. Missing context remains explicit.

Apply preflights all targets with existence, SHA256 and exact-byte checks and
rechecks immediately before mutation. This is not a hostile-filesystem atomic CAS:
trusted ancestors must remain stable, and an external writer racing the final
check/rename can still conflict. Multi-file apply is independently durable, not
transactional; failure can leave a partial install and private backups. Replan
from actual state after failure. Uninstall removes only owned candidate hooks;
settings files, foreign entries, sources, runtime config, spool and policy remain.

## Evidence / remaining acceptance

At the historical 2026-10-24 checkpoint, the integrated offline candidate passed full
unchanged development gates and eleven actual-process consumer journeys. Later failures
and unqualified private fixes remain open; see the handoff's
[current scorecard](../../docs/session-capture-handoff.md#current-session-checkpoint). Synthetic installed native commands exercise
CLI hint/wake/context admission, including pathless durable discovery wake and indexed
artifacts. See [current evidence](../../docs/session-capture-offline-evidence.md).
These are not actual Claude/Codex/Pi, trust/reload or supervisor acceptance.

Focused pinned-Nix CGO race/shuffle coverage tests and scoped lint were run against
synthetic fake homes only. Tests cover dry run, idempotent install/uninstall,
unknown/legacy settings preservation, private backups, malformed input, symlinks,
concurrent edits, plan JSON secrecy, explicit roots, budgets and shell escaping
(apostrophes, spaces, percent, dollar, newline word roundtrips and UTF-8). Pi SDK
handlers are statically asserted; no native Pi process was installed/launched.
SDK and extensions documentation and relevant configuration/settings/examples
were read locally before generating the extension.

Candidate CLI stdin -> durable hint/native-evidence integration and pathless Codex
discovery wakes are implemented and exercised synthetically. Actual native Codex
`/hooks` hash-trust acceptance is explicitly outstanding, not simulated or claimed.
Historical native/R2 checks and experimental hook installations occurred; they do not
establish complete current-source native, restart, both-machine operating acceptance
or final cutover. Current health is unknown. The full offline gates above remain dated
evidence, not current qualification.

### Historical focused checks (2026-10-23)

All commands ran in the pinned Nix shell. Latest checks after budget and protected
input regressions:

```sh
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  CGO_ENABLED=1 go test -race -shuffle=on \
  -coverprofile=/tmp/capturehooks.cover ./internal/capturehooks -count=1
# PASS: 2.907s, 90.3% statements

nix develop --command golangci-lint run ./internal/capturehooks/...
# PASS: 0 issues
nix develop --command golangci-lint fmt --diff internal/capturehooks
# PASS: no diff

git diff --check -- internal/capturehooks
# PASS
```

Scoped CRAP uses the original module-qualified temporary coverage profile. This
version of go-crap's module discovery includes unrelated packages even when given
a package pattern, so explicitly exclude only nonowned directories:

```sh
EXCLUDE="(cmd/|tools/|internal/($(find internal -mindepth 1 -maxdepth 1 \
  -type d ! -name capturehooks -exec basename {} \; | paste -sd '|' -))/)"
nix develop --command go tool -modfile=tools/go.mod go-crap scan \
  ./internal/capturehooks --coverage-profile=/tmp/capturehooks.cover \
  --exclude="$EXCLUDE" --missing=pessimistic --threshold=30 --fail-above \
  --top=10 --no-progress
# PASS: no function above 30; highest 23.82 (applyEntry).
```

Earlier CRAP attempts without those scope exclusions, or from the package cwd,
failed due to unrelated/missing coverage/path mapping. They were not passing gates.
The successful scoped command preserves pessimistic missing coverage and threshold
30 for every owned production function. No repository coverage artifact was changed.

The retained-plan budget counts before and after byte slices together and rejects
before generating a new body if its input already exceeds remaining capacity.
Each input/generated body is also capped at 4 MiB. This is a retained-byte budget,
not a claim about total Go heap usage: JSON parsing/encoding has transient codec
allocations. Generated JSON is compact to avoid deep-indentation amplification.
The suite also executes candidate Claude/Codex shell commands against a synthetic
executable to verify exact argv and inherited synthetic stdin; this is not actual
native-agent or actual capture CLI acceptance.

### Independent security review (2026-10-23)

Fixed a producer/category mismatch: Apply previously named backups
`settings.json.agent-sessions-capture-v1-backup-<digits>`, outside the existing
capturepolicy backup suffix category. Private mode alone did not stop discovery
from admitting the preserved original settings as a raw artifact. The producer now
uses `<filename>.agent-sessions-backup-<CreateTemp digits>`, the already recognized
category; no policy regex, scrubber, source, queue, CLI or legacy security changed.
Exclusive random creation, 0600 permissions, original bytes, fsync/close error
handling and parent-directory syncing are unchanged. Existing unrecognized backups
are not renamed or deleted by this repair; inventory/remediation of any already
created copies remains an explicit operator task before capture of those roots.

`TestNativeBackupNeverPublishesRawSettings` uses only a synthetic fake home and
actual Apply backup output. Actual Discover must derive `scrubbed-config` and
artifact role (non-derived classification must withhold, never raw). The request
is passed unchanged to Queue.Capture, Deliver (including publication.Publish),
and the authoritative ExportSession reader; restored bytes must contain the
redacted TOKEN value, not its synthetic secret, while preserving unrelated settings
and foreign hooks. Discovery and queue callbacks apply identity-aware actual
capturepolicy checks. Producer-created primary `.claude.json`, `.credentials.json`
and `auth.json` backups are withheld. The original backup remains exact. This
journey uses the concurrently repaired Local backend long-key mapping and does
not shorten/replace the discovered identity or manually force its transform.

Audit findings beyond that fix:

- Plan JSON exposes facts/paths/hashes/argv, not retained settings or extension
  bodies. Private entries are authoritative; exported Changes cannot authorize
  mutations, and JSON-decoded plans cannot apply. Existing secrecy, protected-input
  and mutation regressions pass.
- All-target preflight and immediate per-target rechecks are present, including
  another check after backup creation and before rename/removal. Backup and final
  writes check write, Sync and Close; directory sync errors propagate. Multi-file
  apply is not transactional and the final check/rename is not a hostile-writer
  CAS. Trusted stable ancestors remain required, as documented above.
- At this historical checkpoint the generated Pi spawn passed only the session
  pathname to the self-command, with no shell/transcript read/captured output/network
  work. The current extension additionally sends bounded SDK metadata at session start,
  as described above; that metadata is not a transcript.
  Each child has a 2500ms SIGKILL timeout, ignored stdio, an error listener, and
  unref; missing session files are ignored and periodic discovery repairs failed
  hints. This is a per-child lifetime bound, not a global spawn-concurrency bound.
  Native Pi event/reload/shutdown runtime acceptance remains unproven. No native
  OS service or agent was invoked for this audit.

Exact fresh checks (pinned Nix; synthetic fixtures only):

```sh
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  CGO_ENABLED=1 go test -race -shuffle=on \
  -coverprofile=/tmp/capturehooks-security.cover ./internal/capturehooks -count=1
# PASS: 2.912s, 90.3% statements
nix develop --command golangci-lint run ./internal/capturehooks/...
# PASS: 0 issues
nix develop --command golangci-lint fmt --diff internal/capturehooks
# PASS: no diff
EXCLUDE="(cmd/|tools/|internal/($(find internal -mindepth 1 -maxdepth 1 \
  -type d ! -name capturehooks -exec basename {} \; | paste -sd '|' -))/)"
nix develop --command go tool -modfile=tools/go.mod go-crap scan \
  ./internal/capturehooks --coverage-profile=/tmp/capturehooks-security.cover \
  --exclude="$EXCLUDE" --missing=pessimistic --threshold=30 --fail-above \
  --top=10 --no-progress
# PASS: highest 23.82 (applyEntry), no owned function above 30
git diff --check -- internal/capturehooks
# PASS
```

The first regression attempt also rejected the retained `TOKEN` key itself and
failed; the corrected assertion matches existing scrub semantics (retain key,
require `[redacted]`, reject original literal). No security behavior was relaxed.
This historical package review did not run full gates or external acceptance. Full
candidate development gates later passed, as linked above. Actual native-agent/OS
lifecycle, live R2, installation, rollout and external source inventory/remediation
remain outside the offline evidence; use the current handoff for continuation.
