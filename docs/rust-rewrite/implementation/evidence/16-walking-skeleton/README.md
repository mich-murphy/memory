# Evidence 16: walking skeleton (2026-11-12)

Raw material behind [report 26](../../26-walking-skeleton.md). Copied from the session scratch
directories. Paths inside the logs that start with `/Users/mm/dev/memory-wt/` name the local
worktrees the commands ran in. All file content written during these runs was synthetic. No
agent transcript was read. The R2 credentials were sourced from the ignored `.env` into a
subshell's environment and never read, printed or written by the session; every file under
`r2/` was searched for the secret, the key id, the endpoint, its host and the bucket name
before it was displayed or copied (0 matches).
Trailing whitespace and terminal colour codes were stripped from three logs; nothing else was
edited.

| Path | Content |
|---|---|
| `gates/status-8f10261.tsv`, `seeds-8f10261.txt`, `summary-8f10261.txt` | `rust/scripts/gates.sh` on `rust/skeleton-e2e` (`8f10261`): 13 sub-gates 0, 500 cases, seed 335062796, 471 tests, 12 doctests, coverage 6140/6290 lines |
| `r2/pre-list.jsonl` | Listing of `rust-acceptance/skeleton-20261112-8f10261/` before the run: 0 keys |
| `r2/run.log` | `skeleton_on_r2` (the ignored test in `rust/crates/memory/tests/s3_store/skeleton.rs`): passed in 5.1 s; 16 GET, 7 `If-None-Match` PUT, 2 `If-Match` PUT through the `Store` seam; 7 keys written |
| `r2/post-list.jsonl` | Listing after the run: the 7 keys (3 pieces, 3 manifests, one `current.json`) |
| `r2/cleanup.jsonl`, `final-list.jsonl` | Deletion of the prefix (7 listed, 7 deleted, 0 remaining) and an independent listing afterwards (0 keys) |
| `r2/started.txt`, `finished.txt` | UTC times of the run and of the final listing |
| `mutants/M1-identity-encoding.log` | `zstd::bulk::compress` replaced by the raw bytes: the skeleton test fails |
| `mutants/M2-reader-skips-decode.log` | `decode_piece` returns the stored bytes: the skeleton test fails |
| `mutants/M3-adapter-ignores-prefix.log` | The adapter drops its key prefix: the skeleton test fails |
| `mutants/M4-reader-skips-manifest-hash.log` | `read_owner` skips the manifest hash check: `a_manifest_that_does_not_hash_to_its_key_is_corrupt` fails (worker run, on `0192dc8`) |
| `mutants/M0-identity-encoding-on-8355cec.log` | The identity-encoding mutant on the zstd branch alone, before the skeleton existed: 20 of 75 focused tests fail |
| `cleanup-tool/` | The tool that listed and deleted the prefix. It accepts only `rust-acceptance/skeleton-<run id>/`, refuses before reading any variable, and prints keys and counts only |

M1 to M3 were run by the parent on the working tree that became `8f10261` (the test file was
not changed afterwards). Each mutant was applied by exact string replacement and reverted with
`git checkout`.

Requests against R2 in total: 25 by the test (above) and 12 by the cleanup tool (5 LIST,
7 DELETE). Nothing outside the run prefix was listed, read or deleted.

To repeat the R2 run, from the stack worktree with the `R2_*` variables in the environment:

```sh
SKELETON_RUN_ID=<id> nix develop /Users/mm/dev/memory#rust --command sh -c \
  'cd rust && cargo nextest run -p memory --locked --test s3_store --run-ignored only --no-capture skeleton_on_r2'
```

Then build `cleanup-tool` (`cargo build --release --locked`) and run
`skeleton-cleanup delete rust-acceptance/skeleton-<id>/` followed by `skeleton-cleanup list` on
the same prefix.
