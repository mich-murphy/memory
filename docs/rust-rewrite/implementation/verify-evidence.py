#!/usr/bin/env python3
"""Verify frozen evidence and explicit handoff-doc revisions; no private I/O."""
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
BASE = Path(__file__).resolve().parent
baseline = json.loads((BASE / "baseline-identities.json").read_text())
# The operator requested current navigation/security documentation before handoff.
# Preserve the original baseline; permit only these exact, recorded revisions.
# Never admit DESIGN.md, AGENTS.md, the contract or frozen research as exceptions.
document_paths = {
    "README.md", "docs/README.md", "docs/security.md",
    "docs/production-gap-register-20261102.md",
}
updates = json.loads((BASE / "handoff-documentation-identities.json").read_text())["sha256"]
if set(updates) != document_paths:
    raise SystemExit("Unexpected handoff documentation revision scope")
for name, digest in baseline["sha256"].items():
    if name in updates:
        change = updates[name]
        if set(change) != {"before", "after"} or change["before"] != digest:
            raise SystemExit(f"Documentation revision does not match original baseline: {name}")
        digest = change["after"]
    if hashlib.sha256((ROOT / name).read_bytes()).hexdigest() != digest:
        raise SystemExit(f"Changed source outside recorded evidence/revision: {name}")

prototypes = BASE.parent / "prototypes"
a = json.loads((prototypes / "results.json").read_text())
b = json.loads((prototypes / "independent-rerun.json").read_text())
for name, digest in a["source_sha256"].items():
    if hashlib.sha256((prototypes / name).read_bytes()).hexdigest() != digest:
        raise SystemExit(f"Stale prototype source evidence: {name}")
if a["source_sha256"] != b["source_sha256"]:
    raise SystemExit("Prototype runs have different source identities")
for key in ("cases", "results"):
    if a["P1"][key] != b["P1"][key]:
        raise SystemExit(f"P1 deterministic outcome differs: {key}")
for key in ("P3", "P6"):
    if a[key] != b[key]:
        raise SystemExit(f"{key} deterministic outcome differs")
for name in a["P7"]["results"]:
    if not (a["P7"]["results"][name]["rejected"] ==
            b["P7"]["results"][name]["rejected"] == 8):
        raise SystemExit(f"P7 corruption detection differs: {name}")
print(f"PASS: {len(baseline['sha256']) - len(updates)} unchanged baseline identities, "
      f"{len(updates)} exact authorized documentation revisions, "
      "and source-bound deterministic prototype outcomes")
print("Not a Rust implementation, native footprint, R2 or release acceptance gate")
