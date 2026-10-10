from __future__ import annotations

import io
import json
import os
import subprocess
import sys
import time
from pathlib import Path

import pytest

from memsync import hook, install
from memsync.cli import main
from memsync.redact import redact_file
from conftest import CLAUDE_SID, PI_SID, bucket_keys


class FakeStdin(io.BytesIO):
    def isatty(self):
        return False


@pytest.fixture
def spawned(monkeypatch):
    calls = []
    monkeypatch.setattr(hook, "spawn_sync", lambda agent, path, marker, event: calls.append((agent, path, event)))
    return calls


def run_hook(monkeypatch, agent: str, payload: bytes, capfd) -> int:
    r, w = os.pipe()
    os.write(w, payload)
    os.close(w)
    monkeypatch.setattr(sys, "stdin", os.fdopen(r, "r"))
    code = main(["hook", agent])
    out, err = capfd.readouterr()
    assert out == "" and err == ""  # hooks must print nothing
    return code


def test_hook_queues_one_sync_per_transcript_and_debounces(env, monkeypatch, spawned, capfd):
    payload = json.dumps({"hook_event_name": "SubagentStop", "transcript_path": "/x/main.jsonl",
                          "agent_transcript_path": "/x/main/subagents/agent-1.jsonl"}).encode()
    assert run_hook(monkeypatch, "claude", payload, capfd) == 0
    assert spawned == [("claude", "/x/main.jsonl", "SubagentStop"), ("claude", "/x/main/subagents/agent-1.jsonl", "SubagentStop")]
    # The next turn's Stop arrives while that sync is still queued: nothing new is spawned.
    assert run_hook(monkeypatch, "claude", json.dumps({"transcript_path": "/x/main.jsonl"}).encode(), capfd) == 0
    assert len(spawned) == 2


def test_stale_pending_marker_is_taken_over(env, monkeypatch, spawned, capfd):
    marker = hook.claim_pending("/x/a.jsonl")
    old = time.time() - hook.PENDING_STALE_SECONDS - 5
    os.utime(marker, (old, old))
    run_hook(monkeypatch, "codex", json.dumps({"transcript_path": "/x/a.jsonl"}).encode(), capfd)
    assert spawned == [("codex", "/x/a.jsonl", None)]


@pytest.mark.parametrize("payload", [b"", b"not json", b"[1,2]", json.dumps({"transcript_path": None}).encode()])
def test_hook_never_fails_or_prints(env, monkeypatch, spawned, capfd, payload):
    assert run_hook(monkeypatch, "codex", payload, capfd) == 0
    assert spawned == []


def test_hook_with_unknown_agent_or_disabled_is_a_noop(env, monkeypatch, spawned, capfd):
    assert main(["hook"]) == 0
    assert main(["hook", "cursor"]) == 0
    monkeypatch.setenv("MEMSYNC_DISABLE", "1")
    assert main(["hook", "pi", "--path", "/x/s.jsonl"]) == 0
    assert spawned == []


def test_hook_end_to_end_detaches_and_syncs(env, capfd):
    """Run the real hook in a subprocess the way Codex does: wait for it, then check the
    detached sync finished on its own after the hook exited."""
    session = env["pi_dir"] / f"2026-10-10T10-00-00-000Z_{PI_SID}.jsonl"
    started = time.monotonic()
    r = subprocess.run([sys.executable, "-m", "memsync", "hook", "pi", "--path", str(session)],
                       capture_output=True, timeout=10, env=os.environ.copy())
    assert r.returncode == 0 and r.stdout == b"" and r.stderr == b""
    assert time.monotonic() - started < 2.0
    key = f"raw/agent=pi/machine=testbox/sessions/--home-code-proj--/{session.name}.gz"
    deadline = time.monotonic() + 20
    while key not in bucket_keys(env["bucket"]) and time.monotonic() < deadline:
        time.sleep(0.2)
    assert key in bucket_keys(env["bucket"])
    assert not any((env["state"] / "pending").iterdir())


# Installers -------------------------------------------------------------------
CMD = ["/opt/memsync/bin/memsync"]


def test_claude_install_merges_idempotently_and_uninstalls(env):
    settings_path = env["home"] / ".claude" / "settings.json"
    settings = json.loads(settings_path.read_text())
    settings["hooks"] = {"Stop": [{"hooks": [{"type": "command", "command": "notify-send done"}]}],
                         "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh"}]}]}
    settings["cleanupPeriodDays"] = 60
    settings_path.write_text(json.dumps(settings))

    log: list[str] = []
    install.install_claude(CMD, 3650, False, log)
    first = json.loads(settings_path.read_text())
    install.install_claude(CMD, 3650, False, log)
    assert json.loads(settings_path.read_text()) == first

    assert first["cleanupPeriodDays"] == 3650
    assert first["env"] == settings["env"]
    stop = first["hooks"]["Stop"]
    assert stop[0]["hooks"][0]["command"] == "notify-send done"
    assert stop[1]["hooks"][0] == {"type": "command", "command": "/opt/memsync/bin/memsync hook claude", "async": True, "timeout": 30}
    assert set(first["hooks"]) == {"Stop", "StopFailure", "SubagentStop", "SessionEnd", "PreToolUse"}
    assert "async" not in first["hooks"]["SessionEnd"][0]["hooks"][0]
    assert list(settings_path.parent.glob("settings.json.memsync-backup-*"))

    install.install_claude(CMD, None, False, log, remove=True)
    after = json.loads(settings_path.read_text())
    assert after["hooks"] == settings["hooks"]


def test_claude_install_never_lowers_retention_and_refuses_broken_json(env):
    settings_path = env["home"] / ".claude" / "settings.json"
    settings_path.write_text(json.dumps({"cleanupPeriodDays": 9999}))
    install.install_claude(CMD, 3650, False, [])
    assert json.loads(settings_path.read_text())["cleanupPeriodDays"] == 9999
    settings_path.write_text("{ not json")
    with pytest.raises(ValueError):
        install.install_claude(CMD, 3650, False, [])
    assert settings_path.read_text() == "{ not json"


def test_claude_hook_command_quotes_paths_with_spaces(env):
    install.install_claude(["/Users/me/My Tools/memsync"], None, False, [])
    cmd = json.loads((env["home"] / ".claude" / "settings.json").read_text())["hooks"]["Stop"][0]["hooks"][0]["command"]
    assert cmd == "'/Users/me/My Tools/memsync' hook claude"


def test_codex_install_writes_hooks_json_and_reports_trust(env):
    root = env["home"] / ".codex"
    log: list[str] = []
    install.install_codex(CMD, False, log)
    config = json.loads((root / "hooks.json").read_text())
    assert set(config["hooks"]) == {"Stop", "SubagentStop", "Interrupt", "SessionEnd"}
    assert config["hooks"]["SessionEnd"][0]["hooks"][0] == {"type": "command", "command": "/opt/memsync/bin/memsync hook codex", "timeout": 3}
    assert any("/hooks" in line for line in log)
    assert install.codex_trust_state(root) == (4, 0)

    # Simulate the user trusting two of them in /hooks.
    hooks_json = root / "hooks.json"
    with (root / "config.toml").open("a") as fh:
        fh.write(f'\n[hooks.state."{hooks_json}:stop:0:0"]\ntrusted_hash = "sha256:x"\n')
        fh.write(f'\n[hooks.state."{hooks_json}:session_end:0:0"]\ntrusted_hash = "sha256:y"\n')
    assert install.codex_trust_state(root) == (4, 2)

    install.install_codex(CMD, False, [], remove=True)
    assert not hooks_json.exists()


def test_pi_install_writes_extension_and_respects_foreign_file(env):
    log: list[str] = []
    install.install_pi(CMD, False, log)
    ext = env["home"] / ".pi" / "agent" / "extensions" / "memsync.ts"
    text = ext.read_text()
    assert install.PI_MARKER in text and '["/opt/memsync/bin/memsync"]' in text
    assert 'pi.on("agent_settled"' in text and 'pi.on("session_shutdown"' in text
    ext.write_text("// someone else's extension\n")
    install.install_pi(CMD, False, log)
    assert ext.read_text() == "// someone else's extension\n"


def test_install_command_dry_run_touches_nothing(env, monkeypatch, capsys):
    monkeypatch.setattr(install, "install_timer", lambda *a, **k: None)
    before = {p: p.read_bytes() for p in env["home"].rglob("*") if p.is_file()}
    assert main(["install", "--dry-run"]) == 0
    assert {p: p.read_bytes() for p in env["home"].rglob("*") if p.is_file()} == before
    assert "would write" in capsys.readouterr().out


def test_redact_toml_env_tables_and_urls():
    text = b'model = "gpt-5"\n[mcp_servers.db]\nenv = { PGPASSWORD = "pw" }\nurl = "postgres://u:pw@h/db"\n[mcp_servers.db.env]\nAPI_BASE = "https://x"\n'
    out = redact_file("config.toml", text).decode()
    assert 'model = "gpt-5"' in out and "pw" not in out and "https://x" not in out


def test_install_skips_agents_not_on_this_machine(env, monkeypatch, capsys):
    import shutil

    monkeypatch.setattr(install, "install_timer", lambda *a, **k: None)
    monkeypatch.setattr(shutil, "which", lambda name: None)
    shutil.rmtree(env["home"] / ".pi")
    assert main(["install"]) == 0
    assert "pi: not used on this machine" in capsys.readouterr().out
    assert not (env["home"] / ".pi").exists()
    assert (env["home"] / ".codex" / "hooks.json").exists()
