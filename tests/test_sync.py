from __future__ import annotations

import gzip
import json
import os
import time

import zstandard

from memsync.cli import main
from conftest import CLAUDE_SID, CODEX_THREAD, PI_SID, bucket_keys, jsonl

RAW = "raw/agent={agent}/machine=testbox/"


def raw(agent: str, key: str) -> str:
    return RAW.format(agent=agent) + key + ".gz"


def read_object(env, key: str) -> bytes:
    return gzip.decompress((env["bucket"] / key).read_bytes())


def manifest(env, agent: str, session_key: str) -> dict:
    return json.loads((env["bucket"] / f"manifests/agent={agent}/machine=testbox/{session_key}.json").read_text())


def test_sweep_uploads_every_agent_and_is_idempotent(env, capsys):
    assert main(["sync", "--quiet"]) == 0
    keys = bucket_keys(env["bucket"])
    slug = "projects/-home-code-proj"
    expected = {
        raw("claude", f"{slug}/{CLAUDE_SID}.jsonl"),
        raw("claude", f"{slug}/{CLAUDE_SID}/subagents/agent-abc.jsonl"),
        raw("claude", f"{slug}/{CLAUDE_SID}/subagents/agent-abc.meta.json"),
        raw("claude", f"{slug}/{CLAUDE_SID}/tool-results/toolu_1.txt"),
        raw("claude", f"{slug}/{CLAUDE_SID}.jsonl.superseded-20261001"),
        raw("claude", f"{slug}/memory/MEMORY.md"),
        raw("claude", "plans/brave-plan.md"),
        raw("claude", "history.jsonl"),
        raw("codex", f"rollouts/rollout-2026-10-10T10-00-00-{CODEX_THREAD}.jsonl"),
        raw("codex", "history.jsonl"),
        raw("pi", f"sessions/--home-code-proj--/2026-10-10T10-00-00-000Z_{PI_SID}.jsonl"),
        f"manifests/agent=claude/machine=testbox/{CLAUDE_SID}.json",
        f"manifests/agent=codex/machine=testbox/rollout-2026-10-10T10-00-00-{CODEX_THREAD}.json",
        f"manifests/agent=pi/machine=testbox/2026-10-10T10-00-00-000Z_{PI_SID}.json",
    }
    assert expected <= keys
    # Raw bytes are stored unmodified.
    original = (env["claude_slug"] / f"{CLAUDE_SID}.jsonl").read_bytes()
    assert read_object(env, raw("claude", f"{slug}/{CLAUDE_SID}.jsonl")) == original

    m = manifest(env, "claude", CLAUDE_SID)
    assert m["info"]["session_id"] == CLAUDE_SID
    assert m["info"]["cwd"] == str(env["project"])
    roles = {f["role"] for f in m["files"]}
    assert roles == {"main", "subagent", "tool_result", "superseded"}
    assert m["snapshots"] and "git" in m["snapshots"][0]

    codex = manifest(env, "codex", f"rollout-2026-10-10T10-00-00-{CODEX_THREAD}")["info"]
    assert codex["session_id"] == CODEX_THREAD and codex["git_branch"] == "main" and codex["agent_version"] == "0.162.1"
    pi = manifest(env, "pi", f"2026-10-10T10-00-00-000Z_{PI_SID}")["info"]
    assert pi["session_id"] == PI_SID and pi["format_version"] == 3

    before = {k: (env["bucket"] / k).stat().st_mtime_ns for k in keys}
    assert main(["sync"]) == 0
    assert "uploaded 0 files" in capsys.readouterr().out
    assert {k: (env["bucket"] / k).stat().st_mtime_ns for k in bucket_keys(env["bucket"])} == before


def test_appended_session_is_reuploaded_and_manifest_refreshed(env):
    main(["sync", "--quiet"])
    path = env["claude_slug"] / f"{CLAUDE_SID}.jsonl"
    with path.open("a") as fh:
        fh.write(jsonl({"type": "user", "message": {"role": "user", "content": "more"}}))
    old = time.time() - 120
    os.utime(path, (old, old))
    main(["sync", "--quiet"])
    assert read_object(env, raw("claude", f"projects/-home-code-proj/{CLAUDE_SID}.jsonl")) == path.read_bytes()
    main_file = [f for f in manifest(env, "claude", CLAUDE_SID)["files"] if f["role"] == "main"][0]
    assert main_file["size"] == path.stat().st_size


def test_partial_last_line_is_held_back_while_being_written(env):
    main(["sync", "--quiet"])
    path = env["pi_dir"] / f"2026-10-10T10-00-00-000Z_{PI_SID}.jsonl"
    complete = path.read_bytes()
    with path.open("a") as fh:
        fh.write('{"type":"message","id":"half')  # writer mid-line, mtime is now
    main(["sync", "--quiet"])
    key = raw("pi", f"sessions/--home-code-proj--/{path.name}")
    assert read_object(env, key) == complete
    with path.open("a") as fh:
        fh.write('"}\n')
    main(["sync", "--quiet"])
    assert read_object(env, key) == path.read_bytes()


def test_codex_recompression_keeps_key_and_skips_reupload(env):
    main(["sync", "--quiet"])
    rollout = env["codex_rollout"]
    key = raw("codex", f"rollouts/{rollout.name}")
    first_mtime = (env["bucket"] / key).stat().st_mtime_ns
    data = rollout.read_bytes()
    zst = rollout.with_name(rollout.name + ".zst")
    zst.write_bytes(zstandard.ZstdCompressor().compress(data))
    rollout.unlink()
    time.sleep(0.01)
    assert main(["sync", "--quiet"]) == 0
    assert (env["bucket"] / key).stat().st_mtime_ns == first_mtime
    assert read_object(env, key) == data


def test_hook_path_outside_default_roots_is_remembered_for_sweeps(env, tmp_path):
    custom = tmp_path / "pi-custom-sessions"
    custom.mkdir()
    sid = "0199a1b2-ffff-7bbb-8ccc-dddddddddddd"
    session = custom / f"2026-10-10T11-00-00-000Z_{sid}.jsonl"
    session.write_text(jsonl({"type": "session", "version": 3, "id": sid, "cwd": str(env["project"])}))
    assert main(["sync", "--quiet", "--agent", "pi", "--path", str(session)]) == 0
    keys = bucket_keys(env["bucket"])
    custom_keys = [k for k in keys if k.startswith("raw/agent=pi/machine=testbox/dirs/")]
    assert len(custom_keys) == 1 and custom_keys[0].endswith(session.name + ".gz")

    # A second session in that directory, never reported by a hook, is found by the sweep.
    sid2 = "0199a1b2-eeee-7bbb-8ccc-dddddddddddd"
    (custom / f"2026-10-10T12-00-00-000Z_{sid2}.jsonl").write_text(jsonl({"type": "session", "version": 3, "id": sid2}))
    main(["sync", "--quiet"])
    assert any(sid2 in k for k in bucket_keys(env["bucket"]))


def test_hook_sync_for_subagent_pulls_in_the_whole_claude_session(env):
    sub = env["claude_slug"] / CLAUDE_SID / "subagents" / "agent-abc.jsonl"
    assert main(["sync", "--quiet", "--agent", "claude", "--path", str(sub)]) == 0
    keys = bucket_keys(env["bucket"])
    assert raw("claude", f"projects/-home-code-proj/{CLAUDE_SID}.jsonl") in keys
    assert raw("claude", f"projects/-home-code-proj/{CLAUDE_SID}/tool-results/toolu_1.txt") in keys
    assert not any("agent=codex" in k for k in keys)


def test_missing_or_foreign_hook_path_is_harmless(env, tmp_path):
    assert main(["sync", "--quiet", "--agent", "claude", "--path", str(tmp_path / "nope.txt")]) == 0
    not_yet = env["pi_dir"] / "2026-10-10T13-00-00-000Z_0199a1b2-1111-7bbb-8ccc-dddddddddddd.jsonl"
    assert main(["sync", "--quiet", "--agent", "pi", "--path", str(not_yet)]) == 0


def test_snapshot_records_harness_and_redacts_secrets(env):
    main(["sync", "--quiet", "--agent", "claude"])
    m = manifest(env, "claude", CLAUDE_SID)
    set_id = m["snapshots"][0]["id"]
    doc = json.loads((env["bucket"] / f"snapshots/sets/{set_id}.json").read_text())
    files = {(f["scope"], f["path"]): f["sha256"] for f in doc["files"]}
    assert ("user", "CLAUDE.md") in files and ("user", "settings.json") in files
    assert ("project", "CLAUDE.md") in files and ("project", "src/CLAUDE.md") in files
    assert ("project", ".claude/settings.json") in files
    assert ("ancestor", str(env["home"] / "code" / "CLAUDE.md")) in files
    assert ("user", ".claude.json#mcp") in files

    def blob(scope, path):
        return (env["bucket"] / f"snapshots/blobs/{files[(scope, path)]}").read_text()

    assert "sk-ant-" not in blob("user", "settings.json") and '"model": "opus"' in blob("user", "settings.json")
    assert "hunter2" not in blob("project", ".claude/settings.json")
    mcp = blob("user", ".claude.json#mcp")
    assert "ghp_" not in mcp and "Bearer abc" not in mcp and "emailAddress" not in mcp and "local" in mcp


def test_snapshot_does_not_walk_home_outside_a_repo(env, monkeypatch):
    from memsync import snapshot
    from memsync.adapters import make_adapters

    (env["home"] / "deep" / "x").mkdir(parents=True)
    (env["home"] / "deep" / "x" / "CLAUDE.md").write_text("should not be found")
    adapter = make_adapters(env["home"])["claude"]
    entries = snapshot.collect(adapter, env["home"] / ".claude", str(env["home"]))
    assert not any("deep" in rel for _, rel, _ in entries)


def test_status_and_backfill_commands(env, capsys):
    assert main(["backfill"]) == 0
    m = manifest(env, "pi", f"2026-10-10T10-00-00-000Z_{PI_SID}")
    assert m["snapshots"][0]["during_backfill"] is True
    assert main(["status"]) == 0
    out = capsys.readouterr().out
    assert "claude/main" in out and "backfill" in out


def test_codex_thread_metadata_is_exported_by_sweeps(env):
    import sqlite3

    db = sqlite3.connect(env["home"] / ".codex" / "state_5.sqlite")
    db.execute("CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, tokens_used INTEGER)")
    db.execute("INSERT INTO threads VALUES (?, 'Fix the build', 1234)", (CODEX_THREAD,))
    db.commit()
    db.close()
    main(["sync", "--quiet"])
    key = "raw/agent=codex/machine=testbox/state/threads.jsonl.gz"
    rows = [json.loads(l) for l in read_object(env, key).splitlines()]
    assert rows == [{"id": CODEX_THREAD, "title": "Fix the build", "tokens_used": 1234}]
