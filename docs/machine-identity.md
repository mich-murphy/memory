# Machine identity: explicit creation and recovery

The machine ID selects this collector's remote archive namespace. Hostnames are
only descriptive: they can collide or change, and are never used to guess a lost
identity. The local SQLite upload cache does not establish which machine you are.
Losing both SQLite and the identity must not silently select a new, apparently
empty namespace and hide earlier history.

## A deliberately new machine

After installing the binary, explicitly choose a new machine identity **once**:

```sh
agent-sessions machine-id --new
agent-sessions install
$EDITOR ~/.config/agent-sessions/config.env
agent-sessions doctor
agent-sessions backfill
```

`machine-id --new` works without R2 settings or network access. It creates a random
v4 UUID in `machine-id` beside the selected config file (normally
`~/.config/agent-sessions/config.env`; `AGENT_SESSIONS_CONFIG` selects another
path). New directories are private (0700) and the identity file is private (0600).
Creation publishes a complete file with an exclusive link; concurrent explicit
requests share the winning ID, never overwrite it, and do not leave a half-written
identity.

With an existing identity, `--new` simply prints that identity and does not rotate
it. This includes an explicit `AGENT_SESSIONS_MACHINE_ID` environment/config-file
override; no identity file is created for an override. An existing empty or
whitespace-only file is refused, not replaced, even with `--new`. Restore the
original ID, or deliberately remove that empty file before choosing a new
identity. Do not remove a valid identity to repair an upload-cache problem.

To print the effective identity without creating anything:

```sh
agent-sessions machine-id
```

Keep a backup of the ID. It is not an R2 credential, but it is essential to locate
this machine's history. A newly created identity means a deliberately separate
archive namespace; it does not recover or delete an old namespace. Do not copy an
identity onto another concurrently active collector: one writer per machine ID is
the normal contract.

## Lost identity or local cache

Normal config loading, sync, backfill, doctor, hook uploads, install and reinstall
**never generate an identity**. If the identity file is missing or empty and no
explicit override exists, config loading fails with an actionable
`MissingMachineIDError`. Hooks still return success without printing or failing
the agent; uploads cannot proceed until an operator supplies an identity.

If only SQLite was lost, keep the existing machine identity; do not run `--new`
as a cache-recovery step. Remote-history recovery uses the same machine ID and
destination. This document addresses identity selection, not reconstruction of
source bytes that were never uploaded.

If the identity was also lost, stop the collector's uploads and choose explicitly:

1. **Resume the old machine:** restore the original `machine-id` file beside the
   selected config file, with mode 0600; or set the known previous ID in that
   config file:

   ```dotenv
   AGENT_SESSIONS_MACHINE_ID=your-known-previous-uuid
   ```

   An environment override also works:

   ```sh
   export AGENT_SESSIONS_MACHINE_ID=your-known-previous-uuid
   agent-sessions machine-id
   ```

   Environment values take precedence over config-file values. For hooks, use the
   config file or original identity file: an export in your interactive shell may
   not reach the agent. These overrides retain the existing object-key
   sanitisation for compatibility; use the exact original UUID or effective ID.
   Confirm the printed ID before resuming sync to the original destination.

2. **Start a deliberately separate machine:** only if that is your intended
   choice, run `agent-sessions machine-id --new` with no override and no existing
   identity file. With an empty existing file, deliberately remove it first.
   Earlier remote data remains in its old namespace and is not selected by this
   new ID.

There is **no automatic recovery of an unknown machine ID** and no automatic
choice of an old remote host or machine. If you do not know the original ID,
inspect the bucket's namespace keys using the R2 dashboard or your usual bucket
listing tools (for example an authenticated AWS CLI recursive listing, pointed
at your R2 endpoint). Locate `machine=<ID>` namespaces and verify their provenance
against your own records before supplying a known ID. Do not choose solely by a
matching hostname. The identity command does not contact R2 or discover remote
machines.

## Upgrade impact

Existing nonempty identities and explicit environment/config-file overrides keep
their previous behaviour; no namespace keys or UUID format change here. Existing
installs with a lost/empty identity now fail closed instead of silently minting a
new UUID. Fresh installs require the explicit new-machine step above. Neither
`agent-sessions install` nor the binary download/update script performs that step
for you, so ordinary reinstall/update cannot silently change identity.
