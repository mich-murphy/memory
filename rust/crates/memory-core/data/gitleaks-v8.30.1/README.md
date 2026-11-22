# Vendored gitleaks rules

- Source: https://github.com/gitleaks/gitleaks, tag `v8.30.1`, files `config/gitleaks.toml` and `LICENSE` (MIT).
- Both files are unchanged upstream copies.
- sha256 `gitleaks.toml`: `e163e53b9e7e8a8511e77271e2b323ed057759542a6d988258afe3a1fa329caf`
  (pinned by the test `privacy::gitleaks::tests::vendored_file_is_pinned`).
- sha256 `LICENSE`: `e3884b252b3bfc045e55be43a34d1e80da070bc6f804ac95bf4660e97d62ebc6`.
- Used by `src/privacy/gitleaks.rs` (embedded with `include_str!`) and covered by `rules_digest`.

## Updating

1. Copy the new tag's two files into a new `gitleaks-<tag>/` directory and remove the old one.
2. Point `RULES_TOML` in `gitleaks.rs` at it; update the sha256 pin and the rule counts in the tests.
3. Bump `TABLE_VERSION` in `scan.rs`. Run the `memory-core` tests (`gitleaks::`, `privacy_scan`).
4. A pattern that no longer compiles is a table failure (every scanned file is withheld), so the
   load test must stay green.
