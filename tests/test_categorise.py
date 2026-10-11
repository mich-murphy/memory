from __future__ import annotations

import io
import json
import sys
import types

from agent_sessions import jev
from agent_sessions import raw as rawfmt
from agent_sessions.cli import main
from conftest import CLAUDE_SID, bucket_keys, jsonl
from test_sync import CLAUDE_KEY, SLUG, manifest, raw, read_object, settle

FAKE = '''
import json, sys
for line in sys.stdin:
    rec = json.loads(line)
    kind = rec["record"].get("type", "other") if isinstance(rec["record"], dict) else "text"
    print(json.dumps({"line": rec["line"], "category": kind, "waste": kind == "assistant",
                      "had_previous": rec["previous"] is not None}))
'''


def use_categoriser(env, monkeypatch, body: str = FAKE) -> None:
    script = env["tmp"] / "categorise.py"
    script.write_text(body)
    monkeypatch.setenv("AGENT_SESSIONS_CATEGORISE_CMD", f"{sys.executable} {script}")


def label_rows(env, key: str) -> list[dict]:
    keys = sorted(k for k in bucket_keys(env["bucket"]) if k.startswith(f"labels/agent=claude/machine=testbox/{key}/"))
    return [json.loads(x) for k in keys for x in rawfmt.decompress((env["bucket"] / k).read_bytes()).splitlines()]


def test_shadow_labels_every_record_and_drops_nothing(env, monkeypatch, capsys):
    use_categoriser(env, monkeypatch)
    key = f"projects/{SLUG}/{CLAUDE_SID}.jsonl"
    path = env["claude_slug"] / f"{CLAUDE_SID}.jsonl"
    main(["sync", "--quiet"])
    with path.open("a") as fh:
        fh.write(jsonl({"type": "user", "message": {"role": "user", "content": "no, use tabs"}}))
    settle(path)
    main(["sync", "--quiet"])

    assert read_object(env, raw("claude", key)) == path.read_bytes()  # every byte still uploaded
    rows = label_rows(env, key)
    assert [r["line"] for r in rows] == [0, 1, 2]
    assert [r["category"] for r in rows] == ["user", "assistant", "user"]
    assert [r["waste"] for r in rows] == [False, True, False]
    # The first record of the second delta still sees the record before it.
    assert [r["had_previous"] for r in rows] == [False, True, True]
    assert sum(r["bytes"] for r in rows) == path.stat().st_size

    main_file = next(f for f in manifest(env, "claude", CLAUDE_KEY)["transcripts"] if f["role"] == "main")
    assert [(ls["start"], ls["end"]) for ls in main_file["labels"]] == [(0, rows[0]["bytes"] + rows[1]["bytes"]),
                                                                        (rows[0]["bytes"] + rows[1]["bytes"],
                                                                         path.stat().st_size)]
    capsys.readouterr()
    assert main(["labels"]) == 0
    out = capsys.readouterr().out
    assert "assistant" in out and "nothing has been dropped" in out


def test_failing_categoriser_never_blocks_the_upload(env, monkeypatch):
    use_categoriser(env, monkeypatch, "import sys; sys.exit(3)\n")
    key = f"projects/{SLUG}/{CLAUDE_SID}.jsonl"
    path = env["claude_slug"] / f"{CLAUDE_SID}.jsonl"
    assert main(["sync", "--quiet"]) == 0
    assert read_object(env, raw("claude", key)) == path.read_bytes()
    assert label_rows(env, key) == []
    # Once it works, the bytes it missed are labelled.
    use_categoriser(env, monkeypatch)
    path.touch()
    main(["sync", "--quiet"])
    assert [r["line"] for r in label_rows(env, key)] == [0, 1]


def test_jev_categoriser_maps_answers_and_cuts_the_middle(monkeypatch, capsys):
    seen = []

    class Choice:
        def __init__(self, instructions, criteria):
            self.instructions, self.criteria = instructions, criteria

    class Answer:
        def __init__(self, choice):
            self.choice = choice

        def model_dump(self):
            return {"choice": self.choice, "probability": 0.9}

    class Client:
        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

        def system_one(self, state, questions):
            seen.append((state, questions))
            human = '"human"' in state["record"]
            return types.SimpleNamespace(choices={"category": Answer("human_message" if human else "tool_output"),
                                                  "useful": Answer("yes" if human else "no")})

    monkeypatch.setitem(sys.modules, "typesafe_sdk", types.SimpleNamespace(Choice=Choice, TypeSafeClient=Client))
    records = [{"line": 0, "bytes": 10, "agent": "claude", "role": "main", "record": {"who": "human"}, "previous": None},
               {"line": 1, "bytes": 10, "agent": "claude", "role": "main", "record": "x" * (jev.MAX_CHARS + 10),
                "previous": {"who": "human"}}]
    monkeypatch.setattr(sys, "stdin", io.StringIO("".join(json.dumps(r) + "\n" for r in records)))
    assert jev.main() == 0
    out = [json.loads(x) for x in capsys.readouterr().out.splitlines()]
    assert [(o["category"], o["waste"]) for o in out] == [("human_message", False), ("tool_output", True)]
    assert out[1]["cut_for_categoriser"] and out[1]["answers"]["useful"]["probability"] == 0.9
    long_state = next(s for s, _ in seen if "characters cut" in s["record"])
    assert long_state["record"].startswith("x") and long_state["record"].endswith("x")
    assert "previous_record" in long_state
    assert set(seen[0][1]["category"].criteria) == set(jev.DEFAULT_QUESTIONS["category"]["options"])
