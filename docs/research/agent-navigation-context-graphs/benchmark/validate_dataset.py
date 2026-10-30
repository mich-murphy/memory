#!/usr/bin/env python3
"""Offline, read-only validation of the frozen dataset and current target files."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--repo", type=Path, required=True)
args = parser.parse_args()
base = Path(__file__).resolve().parent
root = args.repo.resolve()
data = json.loads((base / "tasks.json").read_text())
tasks = data["tasks"]
assert data["status"] == "frozen-v1"
assert len(tasks) == 20
assert Counter(t["split"] for t in tasks) == {"dev": 8, "test": 12}
assert len({t["id"] for t in tasks}) == 20
intent = [(t["id"], t["query"], t["split"], t["source_family"]) for t in tasks]
assert hashlib.sha256(json.dumps(intent, separators=(",", ":")).encode()).hexdigest() == data["intent_split_sha256"]
for family in {t["source_family"] for t in tasks}:
    assert len({t["split"] for t in tasks if t["source_family"] == family}) == 1
for split in ("dev", "test"):
    assert sorted(t["id"] for t in tasks if t["split"] == split) == sorted(data["split_policy"][split + "_ids"])
for task in tasks:
    source = task["source"]
    assert source["trace_id"] == task["source_family"]
    assert source["line"] >= 1
    for field in ("component_text_sha256", "source_file_sha256"):
        assert re.fullmatch(r"[0-9a-f]{64}", source[field])
    qrels = task["qrels"]
    assert len({q["path"] for q in qrels}) == len(qrels)
    if task["answerability"] == "external_evidence_required":
        assert task["expected_action"] == "abstain_and_request_external_evidence"
        assert not any(q["grade"] > 0 for q in qrels)
    else:
        assert sum(q["grade"] >= 2 for q in qrels) >= 3
        assert any(q["grade"] >= 2 and q["kind"] == "source" for q in qrels)
    for qrel in qrels:
        rel = Path(qrel["path"])
        assert not rel.is_absolute() and ".." not in rel.parts
        path = root / rel
        assert path.is_file(), path
        assert qrel["grade"] in (0, 1, 2, 3)
        assert qrel["kind"] in ("source", "test", "entrypoint_doc")
        assert rel.name not in ("AGENTS.md", "CLAUDE.md", "SKILL.md"), path
        assert not any(part in (".agents", ".claude", ".pi", ".worktrees") for part in rel.parts), path
        body = path.read_bytes()
        verification = qrel["verification"]
        assert hashlib.sha256(body).hexdigest() == verification["sha256"], path
        lines = body.decode().splitlines()
        lo, hi = verification["line_start"], verification["line_end"]
        assert 1 <= lo <= hi <= len(lines), path
        assert any(re.search(verification["anchor_regex"], line, re.I) for line in lines[lo - 1:hi]), path
provenance = json.loads((base / "r2-provenance.json").read_text())
records = {record["task_id"]: record for record in provenance["tasks"]}
assert set(records) == {task["id"] for task in tasks}
for task in tasks:
    record = records[task["id"]]
    assert record["verified"]
    assert record["trace_id"] == task["source"]["trace_id"]
    assert record["source_line"] == task["source"]["line"]
    assert record["transcript_sha256"] == task["source"]["source_file_sha256"]
print(f"PASS: 20 tasks, 8 dev / 12 test, {sum(len(t['qrels']) for t in tasks)} qrels; paths, hashes, anchors, family isolation, instruction-file exclusions and R2 provenance cross-links valid.")
print("No network access or writes. R2 verification is inherited from r2-provenance.json, not repeated here.")
