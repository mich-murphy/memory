"""A shadow categoriser backed by Jev (TypeSafe AI's System One classifier).

Run by the collector as its categorise command:

  AGENT_SESSIONS_CATEGORISE_CMD=agent-sessions jev
  TYPESAFE_API_KEY=...

Reads records on stdin and writes labels on stdout in the format categorise.py describes. The
labels only describe a record; the collector uploads every byte regardless.

This follows how the AI That Works memory pipeline used Jev: one cheap classification per
message, seen with the message before it (a reply only reads as a correction next to what the
agent just did), long text cut in the middle rather than at the end, and every probability kept
so a threshold can be tuned later against a golden run's recall. The default questions are a
starting draft: put your own in AGENT_SESSIONS_JEV_QUESTIONS (a JSON file) and iterate on them by
reading the labels, as the video did.

Needs the TypeSafe SDK: `uv tool install 'agent-sessions[jev] @ git+https://github.com/mich-murphy/memory'`.
"""

from __future__ import annotations

import json
import os
import sys
from concurrent.futures import ThreadPoolExecutor

# {name: {"instructions": str, "options": [str, ...]}}. "category" and "useful" are read back
# into the label's category and waste fields; any other question is kept under "answers".
DEFAULT_QUESTIONS = {
    "category": {
        "instructions": "This is one record from a coding agent's session log (Claude Code, Codex or Pi), shown "
                        "after the record before it. What kind of record is it?",
        "options": ["human_message", "assistant_message", "reasoning", "tool_call", "tool_output",
                    "harness_injection", "compaction_summary", "bookkeeping", "other"],
    },
    "useful": {
        "instructions": "We mine these logs for three things: what the agent found out about the codebase, where "
                        "the human corrected the agent, and what the agent kept having to research. Could this "
                        "record help with any of the three?",
        "options": ["yes", "no"],
    },
}
# Jev reads up to about 32K tokens. Records longer than this keep their start and end.
MAX_CHARS = 48_000
CONTEXT_CHARS = 4_000
WORKERS = 8


def middle_truncate(text: str, limit: int) -> tuple[str, bool]:
    if len(text) <= limit:
        return text, False
    half = limit // 2
    return f"{text[:half]}\n[... {len(text) - 2 * half} characters cut ...]\n{text[-half:]}", True


def as_text(record) -> str:
    return record if isinstance(record, str) else json.dumps(record, ensure_ascii=False)


def load_questions() -> dict:
    path = os.environ.get("AGENT_SESSIONS_JEV_QUESTIONS")
    if not path:
        return DEFAULT_QUESTIONS
    with open(os.path.expanduser(path)) as fh:
        return json.load(fh)


def _dump(choice) -> dict:
    for attr in ("model_dump", "dict"):
        if hasattr(choice, attr):
            try:
                return getattr(choice, attr)()
            except Exception:
                pass
    return {"choice": getattr(choice, "choice", None)}


def main() -> int:
    try:
        from typesafe_sdk import Choice, TypeSafeClient
    except ImportError:
        print("agent-sessions jev needs the TypeSafe SDK: install agent-sessions with the [jev] extra", file=sys.stderr)
        return 2
    spec = load_questions()
    questions = {name: Choice(instructions=q["instructions"], criteria={o: None for o in q["options"]})
                 for name, q in spec.items()}
    records = [json.loads(line) for line in sys.stdin if line.strip()]

    with TypeSafeClient() as client:
        def one(rec: dict) -> dict:
            text, cut = middle_truncate(as_text(rec["record"]), MAX_CHARS)
            state = {"agent": rec["agent"], "role": rec["role"], "record": text}
            if rec.get("previous") is not None:
                state["previous_record"] = middle_truncate(as_text(rec["previous"]), CONTEXT_CHARS)[0]
            out: dict = {"line": rec["line"], "categoriser": "jev"}
            if cut:
                out["cut_for_categoriser"] = True
            try:
                r = client.system_one(state=state, questions=questions)
            except Exception as e:  # this record stays unlabelled; the rest still get labels
                out["error"] = f"{type(e).__name__}: {e}"[:500]
                return out
            answers = {name: _dump(r.choices[name]) for name in questions}
            if "category" in answers:
                out["category"] = answers["category"].get("choice")
            if "useful" in answers:
                out["waste"] = answers["useful"].get("choice") == "no"
            out["answers"] = answers
            return out

        with ThreadPoolExecutor(max_workers=WORKERS) as pool:
            for out in pool.map(one, records):
                sys.stdout.write(json.dumps(out, default=str) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
