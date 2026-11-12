#!/usr/bin/env python3
"""Verify that the prototype results are bound to their source hashes and agree.

Checks docs/rust-rewrite/prototypes/results.json against the prototype sources,
and against independent-rerun.json. No private I/O.
"""
import hashlib
import json
from pathlib import Path

BASE = Path(__file__).resolve().parent
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
print("PASS: prototype results match their source hashes and the independent rerun")
print("Not a Rust implementation, native footprint, R2 or release acceptance gate")
