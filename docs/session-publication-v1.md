# Session export contract (`session-reader/v2`)

`memory export <owner> --out DIR` writes one session owner as a directory that
`tools/analytics/publications.py import` reads. This page states that wire contract as the Rust
writer (`rust/crates/memory-reader/src/export.rs`) produces it and the importer validates it.
The file name is kept for existing links; the contract versions are `session-reader/v2`
(envelope) and `session-publication/v2` (manifest). The importer does not read storage: it
consumes only this directory. See the [analytics projection](session-analytics-v1.md) for what
the importer builds, and the [capture contract](session-capture-contract.md) for the goals.

## Command

```sh
memory owners                              # session owners, one per line
memory export "<owner>" --out DIR [--max-bytes N]
```

- `DIR` must not exist. It is created 0700 with `bodies/` 0700 and files 0600, every file
  fsynced. `metadata.json` is written last, through `.metadata.tmp` and a rename, after the
  bodies are synced; an absent `metadata.json` means an incomplete export. After any failure the
  run removes what it created.
- Bodies are streamed piece by piece (fetch, decode, write, incremental SHA-256); at most one
  stored and one decoded piece are held at a time. Sizes and digests are verified against the
  manifest.
- `--max-bytes` bounds the cumulative decoded size of all epochs of the owner. Default 1 GiB,
  accepted range 1 to 1 TiB. Exceeding it exits 4 (`export_too_large`) before any file exists.
- Exit codes: 0 exported, 1 failed, 2 config or token, 4 too large, 64 usage or owner text,
  66 owner absent, 75 transient store failure (retry), 77 the store refused the credential.
  One stderr line, nothing on stdout.
- Other closed failure codes: `owner_shared`, `export_overflow`, `export_bad_time`,
  `export_too_many_sources`, `export_metadata_too_large`, `output_exists`, `output_io`,
  `object_missing`, `source_corrupt`.

## Directory

```
DIR/metadata.json
DIR/bodies/0000.bin, 0001.bin, ...
```

Body files are numbered in `sources` order and hold the exact decoded source bytes of one
epoch. Body paths in `metadata.json` are relative (`bodies/0000.bin`); the importer rejects
absolute paths, `..`, backslashes and symlinks.

## `metadata.json`

Top level, closed keys: `contract` (`session-reader/v2`), `manifest_sha256`, `manifest`,
`bodies`. `manifest_sha256` is 64 lowercase hex characters; the exporter sets it to the stored
manifest's digest, and the importer stores it as the publication fingerprint. A file without it
fails `reader_digest_missing`.

Manifest (`session-publication/v2`), closed keys:

| Field | Value as exported |
|---|---|
| `contract` | `session-publication/v2` |
| `identity` | `archive_id`, `machine_id`, `agent`, `session_id`, all nonempty strings |
| `publication_id` | the manifest digest (hex) |
| `sequence` | stored sequence + 1; a positive integer at most 2^63 - 1 |
| `observed_at_ns`, `captured_at_ns` | capture time, Unix nanoseconds |
| `published_at_ns` | the owner's last update time, Unix nanoseconds |
| `coverage` | `discovery` `complete`, `enrichment` `partial`, `missing` `["snapshot-kinds-deferred"]` |
| `sources` | one entry per retained epoch, at most 1,024 |

Timestamps are non-negative integers (Unix nanoseconds, not floating-point seconds; zero is
unknown). Python consumers must keep integer precision.

Source, one per epoch of every source of the owner:

| Field | Value as exported |
|---|---|
| `source_id` | the source id |
| `epoch_id` | the epoch number as a decimal string |
| `role` | the export role of the source kind and path (for example `primary`, `subagent`, `artifact`) |
| `path` | the source path, provenance only, never a destination |
| `state` | `active` for the last epoch of a live source, else `sealed` |
| `revision` | epoch number + 1 |
| `observed_at_ns`, `captured_at_ns` | capture time |
| `object` | descriptor, below |
| `optional` | `false` |
| `associations` | `[]` |

A deleted source's epochs are still exported, none of them `active`. Effective
(source, epoch) pairs must be unique (`duplicate_source`).

Object descriptor, closed keys: `key`, `encoding` (only `zstd`), `sha256` (of the decoded
epoch), `encoded_sha256`, `size` (decoded bytes), `encoded_size`. The importer checks its shape
and never fetches it. One piece: that piece's key, encoded digest and length. Several pieces:
the first piece's key, `encoded_size` the sum of the encoded lengths and `encoded_sha256` the
SHA-256 of the pieces' hex digests concatenated in order (a commitment, not a fetchable
object). No pieces: key `objects/none`, size 0, the SHA-256 of the empty string.

Body record, closed keys: `source_id`, `epoch_id`, `path`, `sha256`, `size`. Records cover
exactly the selected (source, epoch) pairs, and `sha256` and `size` equal the object's.

## Importer bounds

`metadata.json` at most 8 MiB; at most 1,024 sources; unknown keys rejected at every level;
`null` accepted for the arrays `missing`, `sources`, `bodies` and `associations`; digests
lowercase hex SHA-256; `object` sizes below 2^63 - 1. The exporter applies the same bounds
before writing anything. JSON duplicate-key rejection is not implemented: the exporter emits
unique keys.

## Withheld optional artifacts (importer only)

The importer also accepts a source with `optional` true (role `artifact` only) and a
`withheld` object `{source_id_hash, snapshot_commitment, reason}` (hashes 64 lowercase hex;
reason `policy`, `integrity` or `unavailable`). Such a source has empty `source_id`, `path` and
`associations`, no `object` and no body record, and the manifest coverage must be enrichment
`partial` with `source-withheld` in `missing`. The Rust exporter does not emit withheld
sources today.

## Shared owners (`shared-reader/v1`)

`memory owners --shared` lists shared owners (Pi subagent run records). Their export uses the
same directory layout with these differences, all closed and all required:

- `contract` `shared-reader/v1`; manifest `contract` `shared-publication/v1`; manifest keys
  `contract`, `identity` (`archive_id`, `machine_id`, `agent`), `publication_id`, `sequence`,
  `captured_at_ns`, `published_at_ns`, `sources`.
- Each source has `source_id`, `epoch_id`, `kind` (`pi_run_record`), `path`, `state`,
  `revision`, `captured_at_ns` and `object`. Only analytics sources are exported; other stored
  entries are skipped and counted.
- At most 16,384 sources. The 8 MiB metadata and `--max-bytes` bounds apply.
- `publications.py import-shared` consumes it. A session export given to it, or the reverse,
  fails `unsupported_contract`.
