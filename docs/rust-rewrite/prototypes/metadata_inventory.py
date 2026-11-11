#!/usr/bin/env python3
"""List repository research artifact names, never execute historical collectors.

No transcript files are read. Only the names under the three explicit repository
research directories are examined. Reviewers must inspect source/docs separately
before deciding whether a named artifact is suitable metadata.
"""
from collections import Counter
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
roots = [ROOT / "docs/rust-rewrite", ROOT / "docs/research", ROOT / "research"]
output = {}
for root in roots:
    files = sorted(p for p in root.rglob("*") if p.is_file() and not p.is_symlink()
                   and "prototypes" not in p.parts and "implementation" not in p.parts)
    candidates = [str(p.relative_to(ROOT)) for p in files
                  if p.suffix.lower() in {".json", ".jsonl", ".csv", ".tsv", ".parquet", ".db", ".sqlite", ".sqlite3"}
                  or any(word in p.name.lower() for word in ["hash", "offset", "corpus", "timeline", "duplicate"])]
    output[str(root.relative_to(ROOT))] = {
        "exists": root.exists(), "file_count": len(files),
        "extensions": dict(sorted(Counter(p.suffix for p in files).items())),
        "candidate_names_only": candidates,
    }
output["script_sha256"] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
print(json.dumps(output, indent=2, sort_keys=True))
