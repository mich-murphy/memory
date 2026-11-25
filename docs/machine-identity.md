# Machine identity

The `machine_id` in `config.toml` selects this host's part of the archive: owner keys are
`sessions/<archive>/<machine>/<agent>/<session>/` and `shared/<archive>/<machine>/<agent>/`.
Hostnames are only descriptive: they can collide or change and are never used to guess an
identity. The id is chosen by the operator once and never generated implicitly.

## Choosing an id

- Set `machine_id` in `~/.local/state/memory/config.toml` (see [install](install.md)). Ids are
  `[A-Za-z0-9._:-]`, at most 1,024 bytes. An invalid id fails the config load
  (`config_machine_id`, exit 2).
- Use an id no other host uses. Hosts that share an `archive_id` land in one archive; two
  writers with one machine id would collide, so never copy an id onto a second active host.
- Keep a record of the id. It is not a credential, but it is the key to this host's history.

## Lost identity or state

Losing local state does not lose the identity: it lives in `config.toml`. Restore the original
`config.toml` (or write the known previous id) rather than choosing a new one, and do not
rotate an id to work around a recovery problem. A new id is a deliberately separate namespace:
earlier data stays under the old id and is not selected by the new one.

If the id is unknown, there is no automatic recovery and no choice by matching hostname. List
the bucket's `sessions/<archive>/` and `shared/<archive>/` prefixes with your usual bucket
tooling, find the `<machine>` components and verify their provenance against your own records
before writing one into `config.toml`. `memory` does not discover remote machines.
