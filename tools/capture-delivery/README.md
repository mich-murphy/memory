# Explicit candidate supervisor launcher

`launch.py` provides a deliberate credential handoff for a supervisor or SSH reader.
It is not a default credential chain, shell environment loader, service installer or
legacy archive migration. The caller supplies absolute paths to a trusted candidate
executable and an existing private credential file:

```sh
/usr/bin/python3 /private/installed/launch.py \
  --binary /private/installed/agent-sessions-capture.bin \
  --credentials-env /private/config.env -- capture-v1 run --config /private/runtime.json
```

Only `capture-v1` commands are accepted. The executable must be an owned regular,
non-symlinked executable and not group/world writable. Trusted ancestors must remain
trusted; this is not a defense against a malicious owner concurrently replacing them.

Only `init`, `run`, `export` and `current-heads` receive `R2_ACCESS_KEY_ID` and `R2_SECRET_ACCESS_KEY`.
Those two values are parsed as bounded data from an explicitly named owned/private
regular no-follow file. No shell expansion, code evaluation, ambient AWS lookup,
credential arguments or value-bearing error diagnostic is used. Other commands,
including hooks and publication/status inspection, neither open that file nor inherit
those two ambient values. A sandboxed native producer therefore needs no credential
file mount merely to append lightweight evidence/hints. Development live-test opt-ins
are removed before execution.

An operator-installed private wrapper can bind these paths using shell-quoted static
arguments and forward `"$@"`; it must not embed secret values. Use that same explicit
wrapper for owned hooks, the service and remote reader operations. The credential file
remains outside source inventory. `capture-v1 service --action install --apply` starts
the service; do not mistake it for dormant unit generation.

Offline launcher tests are part of `make capture-acceptance`. Installed supervision,
secret availability, restart and actual native acceptance remain separate operating
checks recorded in [the delivery ledger](../../docs/session-capture-delivery.md).
