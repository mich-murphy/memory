from __future__ import annotations

import io
import json
import os
import subprocess
import sys
import time

import pytest

from agent_sessions import hook, install
from agent_sessions.cli import main
from conftest import PI_SID, bucket_keys


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
    monkeypatch.setenv("AGENT_SESSIONS_DISABLE", "1")
    assert main(["hook", "pi", "--path", "/x/s.jsonl"]) == 0
    assert spawned == []


def test_hook_end_to_end_detaches_and_syncs(env, capfd):
    """Run the real hook in a subprocess the way Codex does: wait for it, then check the
    detached sync finished on its own after the hook exited."""
    session = env["pi_dir"] / f"2026-10-10T10-00-00-000Z_{PI_SID}.jsonl"
    started = time.monotonic()
    r = subprocess.run([sys.executable, "-m", "agent_sessions", "hook", "pi", "--path", str(session)],
                       capture_output=True, timeout=10, env=os.environ.copy())
    assert r.returncode == 0 and r.stdout == b"" and r.stderr == b""
    assert time.monotonic() - started < 2.0
    key = f"raw/agent=pi/machine=testbox/sessions/--home-code-proj--/{session.name}.zst"
    deadline = time.monotonic() + 20
    while key not in bucket_keys(env["bucket"]) and time.monotonic() < deadline:
        time.sleep(0.2)
    assert key in bucket_keys(env["bucket"])
    assert not any((env["state"] / "pending").iterdir())


# Installers -------------------------------------------------------------------
CMD = ["/opt/agent-sessions/bin/agent-sessions"]


def test_session_start_records_launch_context(env, monkeypatch, capfd):
    spawned = []
    monkeypatch.setattr(hook, "_spawn", lambda args: spawned.append(args))
    monkeypatch.setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
    monkeypatch.setenv("CLAUDE_CODE_OAUTH_TOKEN", "secret")
    monkeypatch.setenv("ANTHROPIC_MODEL", "opus")
    payload = {"hook_event_name": "SessionStart", "source": "startup", "cwd": "/w", "transcript_path": "/x/s.jsonl",
               "permission_mode": "default"}
    assert run_hook(monkeypatch, "claude", json.dumps(payload).encode(), capfd) == 0
    assert len(spawned) == 1
    args = spawned[0]
    assert args[args.index("--path") + 1] == "/x/s.jsonl" and "--pending-marker" not in args
    launch = json.loads(open(args[args.index("--launch") + 1]).read())
    assert launch["source"] == "startup" and launch["cwd"] == "/w" and launch["permission_mode"] == "default"
    assert launch["env"]["CLAUDE_CODE_ENTRYPOINT"] == "cli" and launch["env"]["ANTHROPIC_MODEL"] == "opus"
    assert "CLAUDE_CODE_OAUTH_TOKEN" not in launch["env"]
    assert launch["process"]["ancestry"]  # pytest's own process tree; no claude in it
    # A session start never takes the debounce marker that turn hooks use.
    assert not list((env["state"] / "pending").glob("[0-9a-f]*"))


def test_pi_session_start_passes_reason_and_cwd(env, monkeypatch):
    spawned = []
    monkeypatch.setattr(hook, "_spawn", lambda args: spawned.append(args))
    argv = json.dumps(["/usr/bin/node", "/usr/lib/pi/dist/cli.js", "--model", "x"])
    assert main(["hook", "pi", "--path", "/p/s.jsonl", "--event", "session_start", "--source", "resume", "--cwd", "/w",
                 "--argv", argv]) == 0
    launch = json.loads(open(spawned[0][spawned[0].index("--launch") + 1]).read())
    assert launch["source"] == "resume" and launch["cwd"] == "/w" and launch["event"] == "session_start"
    assert launch["process"]["argv"][-2:] == ["--model", "x"] and launch["process"]["via"] == "extension"


def test_agent_process_is_found_among_ancestors(monkeypatch):
    tree = {10: (["/bin/sh", "-c", "/opt/agent-sessions hook claude"], 9), 9: (["claude", "--model", "opus"], 1)}
    monkeypatch.setattr(hook.os, "getppid", lambda: 10)
    monkeypatch.setattr(hook, "_proc", lambda pid: tree.get(pid))
    assert hook.launch_process("claude") == {"argv": ["claude", "--model", "opus"], "pid": 9, "ancestry": ["sh", "claude"]}
    tree = {10: (["/usr/bin/node", "/usr/lib/node_modules/@earendil-works/pi-coding-agent/dist/cli.js", "-c"], 1)}
    assert hook.launch_process("pi")["pid"] == 10


def test_claude_install_merges_idempotently_and_uninstalls(env):
    settings_path = env["home"] / ".claude" / "settings.json"
    settings = json.loads(settings_path.read_text())
    settings["hooks"] = {"Stop": [{"hooks": [{"type": "command", "command": "notify-send done"}]},
                                  {"hooks": [{"type": "command", "command": "/old/bin/memsync hook claude"}]}],
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
    assert stop[1]["hooks"][0] == {"type": "command", "command": "/opt/agent-sessions/bin/agent-sessions hook claude", "async": True, "timeout": 30}
    assert set(first["hooks"]) == {"SessionStart", "Stop", "StopFailure", "SubagentStop", "PreCompact", "TaskCompleted",
                                   "TeammateIdle", "SessionEnd", "PreToolUse"}
    assert "async" not in first["hooks"]["SessionEnd"][0]["hooks"][0]
    assert list(settings_path.parent.glob("settings.json.agent-sessions-backup-*"))

    install.install_claude(CMD, None, False, log, remove=True)
    after = json.loads(settings_path.read_text())
    assert after["hooks"] == {k: v[:1] for k, v in settings["hooks"].items()}  # the old memsync entry is gone too


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
    install.install_claude(["/Users/me/My Tools/agent-sessions"], None, False, [])
    cmd = json.loads((env["home"] / ".claude" / "settings.json").read_text())["hooks"]["Stop"][0]["hooks"][0]["command"]
    assert cmd == "'/Users/me/My Tools/agent-sessions' hook claude"


def test_codex_install_writes_hooks_json_and_reports_trust(env):
    root = env["home"] / ".codex"
    log: list[str] = []
    install.install_codex(CMD, False, log)
    config = json.loads((root / "hooks.json").read_text())
    assert set(config["hooks"]) == {"SessionStart", "Stop", "SubagentStop", "PreCompact", "Interrupt", "SessionEnd"}
    assert config["hooks"]["SessionEnd"][0]["hooks"][0] == {"type": "command", "command": "/opt/agent-sessions/bin/agent-sessions hook codex", "timeout": 3}
    assert any("/hooks" in line for line in log)
    assert install.codex_trust_state(root) == (6, 0)

    # Simulate the user trusting two of them in /hooks.
    hooks_json = root / "hooks.json"
    with (root / "config.toml").open("a") as fh:
        fh.write(f'\n[hooks.state."{hooks_json}:stop:0:0"]\ntrusted_hash = "sha256:x"\n')
        fh.write(f'\n[hooks.state."{hooks_json}:session_end:0:0"]\ntrusted_hash = "sha256:y"\n')
    assert install.codex_trust_state(root) == (6, 2)

    install.install_codex(CMD, False, [], remove=True)
    assert not hooks_json.exists()


def test_pi_install_writes_extension_and_respects_foreign_file(env):
    log: list[str] = []
    install.install_pi(CMD, False, log)
    ext = env["home"] / ".pi" / "agent" / "extensions" / "agent-sessions.ts"
    text = ext.read_text()
    assert install.PI_MARKER in text and '["/opt/agent-sessions/bin/agent-sessions"]' in text
    assert 'pi.on("agent_settled"' in text and 'pi.on("session_shutdown"' in text and 'pi.on("session_start"' in text
    ext.write_text("// someone else's extension\n")
    install.install_pi(CMD, False, log)
    assert ext.read_text() == "// someone else's extension\n"


def test_install_command_dry_run_touches_nothing(env, monkeypatch, capsys):
    monkeypatch.setattr(install, "install_timer", lambda *a, **k: None)
    before = {p: p.read_bytes() for p in env["home"].rglob("*") if p.is_file()}
    assert main(["install", "--dry-run"]) == 0
    assert {p: p.read_bytes() for p in env["home"].rglob("*") if p.is_file()} == before
    assert "would write" in capsys.readouterr().out


def test_install_skips_agents_not_on_this_machine(env, monkeypatch, capsys):
    import shutil

    monkeypatch.setattr(install, "install_timer", lambda *a, **k: None)
    monkeypatch.setattr(shutil, "which", lambda name: None)
    shutil.rmtree(env["home"] / ".pi")
    assert main(["install"]) == 0
    assert "pi: not used on this machine" in capsys.readouterr().out
    assert not (env["home"] / ".pi").exists()
    assert (env["home"] / ".codex" / "hooks.json").exists()
