# Publication local storage

`Local` implements the same logical-key backend interface as remote publication;
its filenames are not the wire format and it does not expose a listing API.

Physical layout beneath the private configured root:

- Ordinary logical keys remain at `data/<logical-key>` (including logical keys
  beginning with `long-data/`). Existing short-key fixtures keep their layout.
- Keys longer than 768 bytes, or with any component longer than 240 bytes, use
  `long-data/<lowercase SHA-256 of the complete logical key>` instead. This bounds
  physical path length even for hex-encoded root-qualified session identities.
- `.cas.lock` remains outside both object trees. All immutable writes and CAS use
  the same cross-process lock regardless of physical mapping.

The two physical trees are disjoint: a short logical `long-data/<digest>` lives at
`data/long-data/<digest>`, never at the long-key destination. Hash mapping assumes
SHA-256 collision resistance, like the publication's content identities. No wire
identity, namespace, schema, or remote key-length limit is changed by this layout.

Every access validates the original logical key before deriving a physical name.
The derived name is checked component by component for symlinks under `os.Root`,
including the `data` or `long-data` directory itself. Directory creation and sync
fences operate on the actual physical parents. Reopening after a failed sync does
not turn visible bytes into durable acknowledgement. Existing trusted-root and
filesystem durability assumptions still apply; untrusted concurrent filesystem
mutation is not supported.

Focused regressions in `local_long_keys_test.go` cover writer/publish/read/export
with 256-byte UTF-8 identity components and synthetic root-qualified Codex sessions,
exact binary bodies, lost acknowledgements and stale publication tokens, immutable
collision checks, disjoint reserved-looking short keys, long-key cross-process CAS,
symlink/confinement rejection, and post-rename failed-sync retries after reopening.
These offline tests do not establish actual R2, native-hook, analytics, power-loss,
or production acceptance.
