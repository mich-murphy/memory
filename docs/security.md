# Capture security boundaries

The collector applies a known-file policy, not arbitrary transcript DLP. An unrecognised filename
remains byte-for-byte unchanged, even if its contents look like a supported config or contain a
literal credential. Copies renamed to unrelated filenames with no recognised path identity are
outside this policy.

Before reading, capture paths use `capturepolicy.Resolve` and check **every** logical, intermediate
symlink and final identity against `capturepolicy.Policy`. Known credential files, collector config
(including backups) and collector state are excluded. The primary `.claude.json` is permitted only
by the Claude adapter's explicit `ExcludesClaudeConfig` export/extraction path; that exception does
not permit its backups or any other excluded identity, and still requires scrubbing.

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
