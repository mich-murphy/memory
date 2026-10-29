# Explicit candidate supervisor launcher

`launch.py` provides a deliberate credential handoff for a supervisor or SSH reader.
It is not a default credential chain, shell environment loader, service installer or
legacy archive migration. The caller supplies absolute paths to a trusted candidate
executable and an explicit private credential-file path:

```sh
/usr/bin/python3 /private/installed/launch.py \
  --binary /private/installed/agent-sessions-capture.bin \
  --credentials-env /private/config.env -- capture-v1 run --config /private/runtime.json
```

Only `capture-v1` commands are accepted. The executable must be an owned regular,
non-symlinked executable and not group/world writable. Trusted ancestors must remain
trusted; this is not a defense against a malicious owner concurrently replacing them.

Only `init`, `run`, `export` and `current-heads` receive destination credentials.
The fixed file keys `R2_ACCESS_KEY_ID` and `R2_SECRET_ACCESS_KEY` are parsed as
bounded data from an explicitly named owned/private regular no-follow file. When
an explicit runtime config selects other credential environment references, only
these successfully parsed values are mapped to those references; their ambient
values are removed first, including on credential-file failure.

The wrapper reads only the bounded credential-reference projection of a private
runtime JSON; the binary still validates the full configuration. This accepts one
`--config PATH`, `-config PATH`, `--config=PATH` or `-config=PATH`. Duplicate config
flags and duplicate JSON keys (including case aliases) are rejected rather than
choosing a winner. Invalid/unavailable config is a safe launcher failure, never an
ambient fallback. Other CLI options are left to the binary. No shell expansion, code evaluation, ambient AWS lookup,
credential arguments or value-bearing error diagnostic is used. Other commands,
including hooks and publication/status inspection, neither open that file nor inherit
those two ambient values. A sandboxed native producer therefore needs no credential
file mount merely to append lightweight evidence/hints. Development live-test opt-ins
are removed before execution.

`run` is the one destination-readiness exception: a missing, unreadable, unsafe,
malformed or incomplete credential file produces a fixed content-free warning and
executes the coordinator **without either credential value**. Local capture can then
accept durable bytes while delivery records a visible destination pause. No partial
parse or ambient credential fallback is used. Explicit `init`, `export` and
`current-heads` still reject credential-file failure. Repair the private file, stop
and restart the coordinator through this launcher, and use `capture-v1 resume
--config /private/runtime.json` while it is stopped to clear the destination pause.
A running process does not refresh credentials from the file. Inspect `work-status`
to distinguish captured local work from acknowledged publication.

An operator-installed private wrapper can bind these paths using shell-quoted static
arguments and forward `"$@"`; it must not embed secret values. Use that same explicit
wrapper for owned hooks, the service and remote reader operations. The credential file
remains outside source inventory. `capture-v1 service --action install --apply` starts
the service; do not mistake it for dormant unit generation.

## Trusted lifecycle boundaries

Local `work-status`, quarantine/enrichment/optional inspection, service stop/removal,
and hook removal use the same strict configuration parser and trusted paths as
activation, but do not require the effective policy directory to remain available.
Capture, replay/resume and activation still fail closed on policy loss. Hook removal
requires the declared old executable path to be safe, not the old file to exist.
Applied service activation requires an owned safe executable. Service changes reject
foreign installed unit contents before invoking a supervisor or deleting a file.
Installed service trust recognizes only the supported generated unit grammar, not
general plist/systemd configuration. For stop/removal and replacement install, old safe executable/config paths may
differ from the desired plan and need not still exist. Start/restart do not replace
the installed unit: its executable/config argv must match the currently qualified
desired coordinates, otherwise explicitly install the new plan first. Safe restart
tuning drift alone does not require reinstallation. Supported tuning is limited to
canonical positive decimal integers (at most six digits): launchd
`ThrottleInterval`, or systemd `StartLimitIntervalSec`, `StartLimitBurst`,
`RestartSec`, `RestartSteps`, and `RestartMaxDelaySec`. Unknown keys/directives,
execution overrides, duplicate/reset directives, and alternate whitespace/layout
are rejected before any supervisor action. This restriction deliberately does not
attempt to interpret arbitrary operator-modified unit files.

Native hook targets use the existing declared coordinate inventory: each root route
and every live resolution identity must map to the same logical root. A single safe
physical directory is used even if the logical root is absent or a declared symlink;
undeclared traversal and multiple distinct live target directories fail closed.
Managed settings/extension validation and race checks remain in force. Exact-file
source aliases confer no authority over sibling hook targets.

Supervisor files remain 0600. Their immediate parent may be owned and readable
(e.g. 0755), but may not be group/world writable. Existing directory modes are never
changed. This does not relax `WritePrivate`: secret/config parents must still be
private.

Offline launcher unit tests are part of `make capture-acceptance`. For the actual
production-binary missing/malformed credential journey, additionally supply
`CAPTURE_V1_BINARY=/absolute/built/agent-sessions` when running
`python -m unittest discover -s tools/capture-delivery -p 'test_*.py'`.
The journey uses only synthetic local inputs and pauses before any remote request.
An explicitly supplied `CAPTURE_V1_REPAIR_BINARY` may additionally exercise repaired
restart/resume against a **test-only** injected non-network SDK peer; it is not a
production transport option or actual R2 evidence. Installed supervision,
secret availability, restart and actual native acceptance remain separate operating
checks recorded in [the delivery ledger](../../docs/session-capture-delivery.md).
