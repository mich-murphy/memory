from __future__ import annotations

import gzip
import json
import os
import sqlite3
import subprocess
import time

import zstandard

from agent_sessions import sync
from agent_sessions.cli import main
from conftest import CLAUDE_SID, CODEX_THREAD, PI_SID, bucket_keys, jsonl

RAW = "raw/agent={agent}/machine=testbox/"
SLUG = "-home-code-proj"
CLAUDE_KEY = f"{SLUG}/{CLAUDE_SID}"
CODEX_KEY = f"rollout-2026-10-10T10-00-00-{CODEX_THREAD}"
PI_KEY = f"2026-10-10T10-00-00-000Z_{PI_SID}"


def raw(agent: str, key: str) -> str:
    return RAW.format(agent=agent) + key + ".gz"


def read_object(env, key: str) -> bytes:
    return gzip.decompress((env["bucket"] / key).read_bytes())


def manifest(env, agent: str, session_key: str) -> dict:
    return json.loads((env["bucket"] / f"manifests/agent={agent}/machine=testbox/{session_key}.json").read_text())


def settle(path, age: float = 120) -> None:
    old = time.time() - age
    os.utime(path, (old, old))


def test_sweep_captures_every_agent_dir_and_is_idempotent(env, capsys):
    assert main(["sync", "--quiet"]) == 0
    keys = bucket_keys(env["bucket"])
    p = f"projects/{SLUG}"
    expected = {
        raw("claude", f"{p}/{CLAUDE_SID}.jsonl"),
        raw("claude", f"{p}/{CLAUDE_SID}/subagents/agent-abc.jsonl"),
        raw("claude", f"{p}/{CLAUDE_SID}/subagents/agent-abc.meta.json"),
        raw("claude", f"{p}/{CLAUDE_SID}/tool-results/toolu_1.txt"),
        raw("claude", f"{p}/{CLAUDE_SID}.jsonl.superseded-20261001"),
        raw("claude", f"{p}/memory/MEMORY.md"),
        raw("claude", "plans/brave-plan.md"),
        raw("claude", "history.jsonl"),
        raw("claude", "settings.json"),
        raw("claude", "skills/deploy/scripts/go.sh"),
        raw("claude", f"file-history/{CLAUDE_SID}/abc@v1"),
        raw("claude", "teams/alpha/inboxes/lead.json"),
        raw("claude", "sessions/123.json"),
        raw("claude", "mystery.dat"),
        raw("claude", "claude.json"),
        raw("codex", f"rollouts/{CODEX_KEY}.jsonl"),
        raw("codex", "history.jsonl"),
        raw("codex", "config.toml"),
        raw("codex", "memories/notes.md"),
        raw("pi", f"sessions/--home-code-proj--/{PI_KEY}.jsonl"),
        raw("pi", "AGENTS.md"),
        f"manifests/agent=claude/machine=testbox/{CLAUDE_KEY}.json",
        f"manifests/agent=codex/machine=testbox/{CODEX_KEY}.json",
        f"manifests/agent=pi/machine=testbox/{PI_KEY}.json",
    }
    assert expected <= keys
    # Raw bytes are stored unmodified.
    original = (env["claude_slug"] / f"{CLAUDE_SID}.jsonl").read_bytes()
    assert read_object(env, raw("claude", f"{p}/{CLAUDE_SID}.jsonl")) == original

    m = manifest(env, "claude", CLAUDE_KEY)
    assert m["schema"] == 2 and m["machine_id"] == "testbox" and m["hostname"]
    assert m["info"]["session_id"] == CLAUDE_SID
    assert m["info"]["cwd"] == str(env["project"])
    roles = {f["role"] for f in m["transcripts"]}
    assert {"main", "subagent", "tool_result", "superseded", "file_history"} <= roles
    main_t = next(f for f in m["transcripts"] if f["role"] == "main")
    assert main_t["line_count"] == 2 and main_t["prefix_sha256"] and main_t["head"]["session_id"] == CLAUDE_SID
    assert main_t["versions"] == []
    snap = m["snapshots"][0]
    assert snap["timing"] == "post_hoc" and snap["git"]["branch"] == "main"
    assert m["git"]["head"] and m["git"]["remote"] == "https://github.com/me/proj.git"

    codex = manifest(env, "codex", CODEX_KEY)["info"]
    assert codex["session_id"] == CODEX_THREAD and codex["git_branch"] == "main" and codex["agent_version"] == "0.162.1"
    pi = manifest(env, "pi", PI_KEY)["info"]
    assert pi["session_id"] == PI_SID and pi["format_version"] == 3

    before = {k: (env["bucket"] / k).stat().st_mtime_ns for k in bucket_keys(env["bucket"])}
    assert main(["sync"]) == 0
    assert "uploaded 0 files" in capsys.readouterr().out
    assert {k: (env["bucket"] / k).stat().st_mtime_ns for k in bucket_keys(env["bucket"])} == before


def test_credentials_and_noise_are_never_copied(env):
    main(["sync", "--quiet"])
    keys = bucket_keys(env["bucket"])
    assert not any(k.endswith((".credentials.json.gz", "auth.json.gz", ".key.gz")) for k in keys)
    assert not any("shell-snapshots" in k for k in keys)
    everything = b"".join(gzip.decompress(p.read_bytes()) if p.suffix == ".gz" else p.read_bytes()
                          for p in env["bucket"].rglob("*") if p.is_file())
    assert b"secret-oauth" not in everything and b"secret-key" not in everything
    assert b"sk-ant-api-secret" not in everything and b"ghp_token" not in everything
    claude_json = json.loads(read_object(env, raw("claude", "claude.json")))
    assert "primaryApiKey" not in claude_json and claude_json["oauthAccount"]["emailAddress"] == "me@example.com"
    # Unknown files are kept and counted, not dropped.
    runs = sqlite3.connect(env["state"] / "state.sqlite3").execute("SELECT summary FROM runs").fetchall()
    assert json.loads(runs[-1][0])["unknown"] >= 1


def test_appended_session_is_reuploaded_without_a_version(env):
    main(["sync", "--quiet"])
    path = env["claude_slug"] / f"{CLAUDE_SID}.jsonl"
    with path.open("a") as fh:
        fh.write(jsonl({"type": "user", "message": {"role": "user", "content": "more"}}))
    settle(path)
    main(["sync", "--quiet"])
    assert read_object(env, raw("claude", f"projects/{SLUG}/{CLAUDE_SID}.jsonl")) == path.read_bytes()
    main_file = next(f for f in manifest(env, "claude", CLAUDE_KEY)["transcripts"] if f["role"] == "main")
    assert main_file["size"] == path.stat().st_size and main_file["line_count"] == 3 and main_file["versions"] == []
    assert not any(k.startswith("raw-versions/") for k in bucket_keys(env["bucket"]))


def test_rewritten_file_keeps_its_earlier_version(env):
    main(["sync", "--quiet"])
    path = env["claude_slug"] / f"{CLAUDE_SID}.jsonl"
    old = path.read_bytes()
    old_sha = __import__("hashlib").sha256(old).hexdigest()
    path.write_text(jsonl({"type": "user", "message": {"role": "user", "content": "rewritten"}}))
    settle(path)
    main(["sync", "--quiet"])
    key = f"projects/{SLUG}/{CLAUDE_SID}.jsonl"
    version = f"raw-versions/agent=claude/machine=testbox/{key}/{old_sha}.gz"
    assert read_object(env, version) == old
    assert read_object(env, raw("claude", key)) == path.read_bytes()
    main_file = next(f for f in manifest(env, "claude", CLAUDE_KEY)["transcripts"] if f["role"] == "main")
    assert [v["object"] for v in main_file["versions"]] == [version] and main_file["versions"][0]["size"] == len(old)


def test_large_files_stream_and_keep_versions(env, monkeypatch):
    monkeypatch.setattr(sync, "LARGE_FILE_BYTES", 64)
    monkeypatch.setattr(sync, "CHUNK", 16)
    path = env["pi_dir"] / f"{PI_KEY}.jsonl"
    key = raw("pi", f"sessions/--home-code-proj--/{path.name}")
    main(["sync", "--quiet"])
    assert read_object(env, key) == path.read_bytes()
    with path.open("a") as fh:
        fh.write(jsonl({"type": "message", "id": "x" * 50}))
    settle(path)
    main(["sync", "--quiet"])
    assert read_object(env, key) == path.read_bytes()
    assert not any(k.startswith("raw-versions/") for k in bucket_keys(env["bucket"]))
    before = path.read_bytes()
    path.write_bytes(b'{"type":"session","version":3,"id":"replaced"}\n' * 3)
    settle(path)
    main(["sync", "--quiet"])
    versions = [k for k in bucket_keys(env["bucket"]) if k.startswith("raw-versions/agent=pi/")]
    assert len(versions) == 1 and read_object(env, versions[0]) == before
    t = next(f for f in manifest(env, "pi", PI_KEY)["transcripts"] if f["role"] == "main")
    assert t["line_count"] == 3


def test_deleted_file_gets_a_tombstone(env):
    main(["sync", "--quiet"])
    sub = env["claude_slug"] / CLAUDE_SID / "subagents" / "agent-abc.jsonl"
    sub.unlink()
    main(["sync", "--quiet"])
    m = manifest(env, "claude", CLAUDE_KEY)
    gone = [d["key"] for d in m["deleted_locally"]]
    assert gone == [f"projects/{SLUG}/{CLAUDE_SID}/subagents/agent-abc.jsonl"]
    # The object itself stays in the bucket.
    assert raw("claude", gone[0]) in bucket_keys(env["bucket"])


def test_partial_last_line_is_held_back_while_being_written(env):
    main(["sync", "--quiet"])
    path = env["pi_dir"] / f"{PI_KEY}.jsonl"
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
    assert not any(k.startswith("raw-versions/") for k in bucket_keys(env["bucket"]))


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
    assert manifest(env, "codex", CODEX_KEY)["deleted_locally"] == []


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


def test_custom_claude_config_dir_gets_the_same_keys_from_hooks_and_sweeps(env, tmp_path, monkeypatch):
    other = tmp_path / "work-claude"
    slug = other / "projects" / "-w"
    slug.mkdir(parents=True)
    sid = "99999999-2222-3333-4444-555555555555"
    (slug / f"{sid}.jsonl").write_text(jsonl({"type": "user", "sessionId": sid}))
    (other / "settings.json").write_text("{}")
    monkeypatch.setenv("CLAUDE_CONFIG_DIR", str(other))
    main(["sync", "--quiet", "--agent", "claude", "--path", str(slug / f"{sid}.jsonl")])
    hook_keys = {k for k in bucket_keys(env["bucket"]) if "/roots/" in k}
    monkeypatch.delenv("CLAUDE_CONFIG_DIR")
    main(["sync", "--quiet", "--agent", "claude"])
    sweep_keys = {k for k in bucket_keys(env["bucket"]) if "/roots/" in k}
    assert hook_keys and hook_keys <= sweep_keys
    assert any(k.endswith("/settings.json.gz") for k in sweep_keys)
    # ~/.claude keeps unprefixed keys.
    assert raw("claude", "settings.json") in bucket_keys(env["bucket"])


def test_hook_sync_pulls_in_the_whole_claude_session_and_its_short_lived_files(env):
    sub = env["claude_slug"] / CLAUDE_SID / "subagents" / "agent-abc.jsonl"
    assert main(["sync", "--quiet", "--agent", "claude", "--path", str(sub), "--trigger", "hook:SubagentStop"]) == 0
    keys = bucket_keys(env["bucket"])
    assert raw("claude", f"projects/{SLUG}/{CLAUDE_SID}.jsonl") in keys
    assert raw("claude", f"projects/{SLUG}/{CLAUDE_SID}/tool-results/toolu_1.txt") in keys
    assert raw("claude", f"file-history/{CLAUDE_SID}/abc@v1") in keys
    assert raw("claude", "teams/alpha/inboxes/lead.json") in keys
    assert not any("agent=codex" in k for k in keys)
    assert manifest(env, "claude", CLAUDE_KEY)["snapshots"][0]["timing"] == "in_session"


def test_missing_or_foreign_hook_path_is_harmless(env, tmp_path):
    assert main(["sync", "--quiet", "--agent", "claude", "--path", str(tmp_path / "nope.txt")]) == 0
    not_yet = env["pi_dir"] / "2026-10-10T13-00-00-000Z_0199a1b2-1111-7bbb-8ccc-dddddddddddd.jsonl"
    assert main(["sync", "--quiet", "--agent", "pi", "--path", str(not_yet)]) == 0


def test_snapshot_records_whole_harness_unredacted(env):
    target = env["tmp"] / "dotfiles" / "review"
    target.mkdir(parents=True)
    (target / "SKILL.md").write_text("review skill\n")
    (env["claude"] / "skills" / "review").symlink_to(target)
    main(["sync", "--quiet", "--agent", "claude"])
    m = manifest(env, "claude", CLAUDE_KEY)
    snap = m["snapshots"][0]
    doc = json.loads((env["bucket"] / f"snapshots/sets/{snap['id']}.json").read_text())
    assert doc["kind"] == "harness"
    files = {(f["scope"], f["path"]): f for f in doc["files"]}
    home = env["home"]
    for k in [("user", "CLAUDE.md"), ("user", "settings.json"), ("user", "skills/deploy/SKILL.md"),
              ("user", "skills/deploy/scripts/go.sh"), ("user", "skills/review/SKILL.md"), ("user", "hooks/guard.sh"),
              ("project", "CLAUDE.md"), ("project", "src/CLAUDE.md"), ("project", ".claude/settings.json"),
              ("ancestor", str(home / "code" / "CLAUDE.md")), ("user", ".claude.json#mcp"),
              ("script", os.path.realpath(home / ".claude" / "hooks" / "guard.sh")),
              ("script", os.path.realpath(home / "bin" / "status.py")),
              ("import", os.path.realpath(env["project"] / "docs" / "conventions.md"))]:
        assert k in files, k
    assert files[("user", "skills/review/SKILL.md")]["target"] == os.path.realpath(target / "SKILL.md")
    assert "harness:user:CLAUDE.md" in snap["mtimes"]

    def blob(scope, path):
        return (env["bucket"] / f"snapshots/blobs/{files[(scope, path)]['sha256']}").read_text()

    assert "sk-ant-" in blob("user", "settings.json")
    assert "hunter2" in blob("project", ".claude/settings.json")
    assert "ghp_" in blob("user", ".claude.json#mcp")

    memory = json.loads((env["bucket"] / f"snapshots/sets/{snap['memory_id']}.json").read_text())
    assert memory["kind"] == "memory"
    assert [f["scope"] for f in memory["files"]] == ["auto"]

    # The repo's history and tree at the snapshotted HEAD.
    head = snap["git"]["head"]
    repo = snap["git"]["repo_id"]
    log = gzip.decompress((env["bucket"] / f"git/{repo}/log/{head}.txt.gz").read_bytes()).decode()
    assert head in log and "CLAUDE.md" in log and "first" in log
    tree = gzip.decompress((env["bucket"] / f"git/{repo}/tree/{head}.txt.gz").read_bytes()).decode()
    assert "docs/conventions.md" in tree


def test_snapshot_does_not_walk_home_outside_a_repo(env):
    from agent_sessions import snapshot
    from agent_sessions.adapters import make_adapters

    (env["home"] / "deep" / "x").mkdir(parents=True)
    (env["home"] / "deep" / "x" / "CLAUDE.md").write_text("should not be found")
    adapter = make_adapters(env["home"])["claude"]
    collected = snapshot.collect(adapter, env["home"] / ".claude", str(env["home"]))
    assert not any("deep" in e.path for e in collected.entries)


def test_launch_is_recorded_with_a_pre_session_snapshot(env):
    launch = env["tmp"] / "launch.json"
    launch.write_text(json.dumps({"event": "SessionStart", "source": "startup", "cwd": str(env["project"]),
                                  "permission_mode": "plan", "process": {"argv": ["claude", "--model", "opus"]},
                                  "env": {"CLAUDE_CODE_ENTRYPOINT": "cli"}}))
    path = env["claude_slug"] / f"{CLAUDE_SID}.jsonl"
    assert main(["sync", "--quiet", "--agent", "claude", "--path", str(path), "--launch", str(launch),
                 "--trigger", "hook:SessionStart"]) == 0
    assert not launch.exists()
    m = manifest(env, "claude", CLAUDE_KEY)
    assert m["launch"]["process"]["argv"] == ["claude", "--model", "opus"] and m["launch"]["permission_mode"] == "plan"
    assert m["launch"]["git"]["head"] == m["git"]["head"] and m["git"]["dirty"] is False
    assert [s["timing"] for s in m["snapshots"]] == ["pre_session"]
    assert m["launch"]["snapshot"] == m["snapshots"][0]["id"]


def test_status_and_backfill_commands(env, capsys):
    assert main(["backfill"]) == 0
    m = manifest(env, "pi", PI_KEY)
    assert m["snapshots"][0]["timing"] == "post_hoc"
    assert main(["status"]) == 0
    out = capsys.readouterr().out
    assert "claude/main" in out and "backfill" in out


def test_codex_databases_are_copied_with_the_backup_api(env):
    db_path = env["home"] / ".codex" / "state_5.sqlite"
    db = sqlite3.connect(db_path)
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("PRAGMA wal_autocheckpoint=0")
    db.execute("CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, tokens_used INTEGER)")
    db.execute("INSERT INTO threads VALUES (?, 'Fix the build', 1234)", (CODEX_THREAD,))
    db.commit()  # left open: the row is still only in the -wal file
    try:
        main(["sync", "--quiet"])
    finally:
        db.close()
    keys = bucket_keys(env["bucket"])
    assert not any(k.endswith(("-wal.gz", "-shm.gz")) for k in keys)
    out = env["tmp"] / "copy.sqlite"
    out.write_bytes(read_object(env, raw("codex", "db/state_5.sqlite")))
    rows = sqlite3.connect(out).execute("SELECT * FROM threads").fetchall()
    assert rows == [(CODEX_THREAD, "Fix the build", 1234)]


def test_machine_id_is_a_persistent_uuid(env, monkeypatch):
    import uuid

    from agent_sessions.config import Config

    monkeypatch.delenv("AGENT_SESSIONS_MACHINE_ID")
    first = Config.load()
    uuid.UUID(first.machine_id)
    assert Config.load().machine_id == first.machine_id
    assert (env["tmp"] / "machine-id").read_text().strip() == first.machine_id


def test_git_remote_normalisation():
    from agent_sessions.gitinfo import normalise_remote, strip_userinfo

    for url in ("https://github.com/Me/proj.git", "git@github.com:Me/proj.git", "ssh://git@github.com:22/Me/proj",
                "https://x:tok@GitHub.com/Me/proj/"):
        assert normalise_remote(url) == "github.com/Me/proj"
    assert strip_userinfo("https://x:tok@github.com/a/b") == "https://github.com/a/b"
    assert subprocess.run(["git", "--version"], capture_output=True).returncode == 0
