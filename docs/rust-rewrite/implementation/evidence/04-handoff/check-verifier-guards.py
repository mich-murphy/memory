#!/usr/bin/env python3
"""Synthetic scratch checks for the explicit handoff documentation revision ledger."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

base = Path(__file__).resolve().parents[2]
root = base.parents[2]
baseline = json.loads((base / "baseline-identities.json").read_text())
prototypes = base.parent / "prototypes"
results = json.loads((prototypes / "results.json").read_text())
files = [root / name for name in baseline["sha256"]]
files += [base / name for name in (
    "baseline-identities.json", "handoff-documentation-identities.json", "verify-evidence.py",
)]
files += [prototypes / name for name in (
    "results.json", "independent-rerun.json", *results["source_sha256"],
)]

with tempfile.TemporaryDirectory(prefix="memory-doc-verifier-") as directory:
    scratch = Path(directory)
    for source in files:
        target = scratch / source.relative_to(root)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
    script = scratch / base.relative_to(root) / "verify-evidence.py"
    ledger = script.with_name("handoff-documentation-identities.json")

    def check(label, expected_status, diagnostic):
        result = subprocess.run([sys.executable, str(script)], capture_output=True, text=True)
        assert result.returncode == expected_status, (label, result.returncode, result.stderr)
        assert diagnostic in result.stdout + result.stderr, (label, result.stdout, result.stderr)
        print(f"PASS: {label}; verifier exit {result.returncode}")

    check("recorded current docs and frozen evidence", 0, "29 unchanged baseline identities")
    for name in ("DESIGN.md", "README.md"):
        path = scratch / name
        original = path.read_bytes()
        path.write_bytes(original + b"\nUNRECORDED SCRATCH CHANGE\n")
        check(f"reject unrecorded change to {name}", 1, "Changed source outside recorded evidence/revision")
        path.write_bytes(original)
    original = ledger.read_bytes()
    data = json.loads(original)
    data["sha256"]["DESIGN.md"] = {
        "before": baseline["sha256"]["DESIGN.md"],
        "after": hashlib.sha256((scratch / "DESIGN.md").read_bytes()).hexdigest(),
    }
    ledger.write_text(json.dumps(data))
    check("reject design exception in documentation ledger", 1, "Unexpected handoff documentation revision scope")
    data = json.loads(original)
    data["sha256"]["README.md"]["before"] = "0" * 64
    ledger.write_text(json.dumps(data))
    check("reject unrelated original-document baseline", 1, "Documentation revision does not match original baseline")
    ledger.write_bytes(original)
    check("restored scratch fixture", 0, "29 unchanged baseline identities")
print("All six checks passed; repository inputs were never modified.")
