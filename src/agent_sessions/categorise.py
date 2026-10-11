"""Shadow categorisation: label every record of a transcript before its bytes are uploaded.

Nothing is dropped. The labels are uploaded next to the raw bytes, so after a trial period
`agent-sessions labels` shows what a categoriser would have marked as waste and how many
bytes that is, before anyone decides to drop anything.

Opt-in: set AGENT_SESSIONS_CATEGORISE_CMD to a command (split like a shell line). It reads
one JSON object per record on stdin and writes one per labelled record on stdout:

  in:  {"line": 12, "bytes": 830, "agent": "claude", "role": "main", "key": "projects/…/<sid>.jsonl",
        "record": {...the parsed JSONL line, or the raw text if it isn't JSON...},
        "previous": {...the record before it, for context, or null at the top of the file...}}
  out: {"line": 12, "category": "harness_injection", "waste": false}

Extra output fields (confidence, reason, model...) are kept. A record the command doesn't
label is stored as category "unlabelled". If the command fails, the upload goes ahead
unlabelled and the same bytes are offered to it again on the next sync.
"""

from __future__ import annotations

import json
import os
import subprocess

TIMEOUT_SECONDS = 300
# Transcripts are what a categoriser can say something about; everything else is uploaded as is.
ROLES = {"main", "subagent"}
# Bigger deltas than this are uploaded without labels rather than held in memory twice.
MAX_BYTES = 64 * 1024 * 1024


def _parse(raw: bytes):
    try:
        return json.loads(raw)
    except ValueError:
        return raw.decode("utf-8", "replace")


def label(cmd: list[str], *, agent: str, role: str, key: str, data: bytes, first_line: int,
          before: bytes | None = None, env: dict[str, str] | None = None) -> list[dict]:
    """Label each line of `data` (line numbers from first_line). `before` is the line just before
    it, given to the categoriser as context for the first record."""
    lines = data.splitlines(keepends=True)
    records = []
    previous = _parse(before) if before else None
    for i, raw in enumerate(lines):
        record = _parse(raw)
        records.append(json.dumps({"line": first_line + i, "bytes": len(raw), "agent": agent, "role": role,
                                   "key": key, "record": record, "previous": previous}))
        previous = record
    proc = subprocess.run(cmd, input=("\n".join(records) + "\n").encode(), capture_output=True,
                          timeout=TIMEOUT_SECONDS, check=True, env={**(env or {}), **os.environ})
    got: dict[int, dict] = {}
    for out in proc.stdout.splitlines():
        if out.strip():
            d = json.loads(out)
            got[int(d["line"])] = d
    labels = []
    for i, raw in enumerate(lines):
        d = got.get(first_line + i, {})
        labels.append({**d, "line": first_line + i, "bytes": len(raw),
                       "category": str(d.get("category") or "unlabelled"), "waste": bool(d.get("waste", False))})
    return labels


def summarise(labels: list[dict]) -> dict:
    """{category: {"records", "bytes", "waste_bytes"}}"""
    out: dict[str, dict] = {}
    for x in labels:
        s = out.setdefault(x["category"], {"records": 0, "bytes": 0, "waste_bytes": 0})
        s["records"] += 1
        s["bytes"] += x["bytes"]
        s["waste_bytes"] += x["bytes"] if x["waste"] else 0
    return out


def merge(summaries) -> dict:
    out: dict[str, dict] = {}
    for summary in summaries:
        for cat, s in summary.items():
            t = out.setdefault(cat, {"records": 0, "bytes": 0, "waste_bytes": 0})
            for k in t:
                t[k] += s.get(k, 0)
    return out
