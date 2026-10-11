from __future__ import annotations

import json
import os
import subprocess
import time
from pathlib import Path

import pytest

CLAUDE_SID = "11111111-2222-3333-4444-555555555555"
CODEX_THREAD = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
PI_SID = "0199a1b2-aaaa-7bbb-8ccc-dddddddddddd"


def jsonl(*objs: dict) -> str:
    return "".join(json.dumps(o) + "\n" for o in objs)


@pytest.fixture
def env(tmp_path, monkeypatch):
    """A fake home with Claude Code, Codex and Pi sessions for one git project."""
    home = tmp_path / "home"
    home.mkdir()
    for var in ("CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "AGENT_SESSIONS_DISABLE"):
        monkeypatch.delenv(var, raising=False)
    monkeypatch.setenv("HOME", str(home))
    monkeypatch.setenv("AGENT_SESSIONS_CONFIG", str(tmp_path / "config.env"))
    monkeypatch.setenv("AGENT_SESSIONS_STATE_DIR", str(tmp_path / "state"))
    monkeypatch.setenv("AGENT_SESSIONS_LOCAL_STORE", str(tmp_path / "bucket"))
    monkeypatch.setenv("AGENT_SESSIONS_MACHINE_ID", "testbox")

    project = home / "code" / "proj"
    (project / "src").mkdir(parents=True)
    subprocess.run(["git", "init", "-q", "-b", "main", str(project)], check=True)
    (project / "CLAUDE.md").write_text("# proj rules\n@docs/conventions.md\n")
    (project / "docs").mkdir()
    (project / "docs" / "conventions.md").write_text("use tabs\n")
    (project / "AGENTS.md").write_text("# agents rules\n")
    (project / "src" / "CLAUDE.md").write_text("nested\n")
    (project / ".claude").mkdir()
    (project / ".claude" / "settings.json").write_text(json.dumps({"env": {"DB_PASSWORD": "hunter2"}}))
    (home / "code" / "CLAUDE.md").write_text("ancestor rules\n")
    git = ["git", "-C", str(project), "-c", "user.name=t", "-c", "user.email=t@example.com"]
    subprocess.run([*git, "remote", "add", "origin", "https://user:ghp_token@github.com/me/proj.git"], check=True)
    subprocess.run([*git, "add", "-A"], check=True)
    subprocess.run([*git, "commit", "-q", "-m", "first"], check=True)

    # Claude Code
    claude = home / ".claude"
    slug = claude / "projects" / "-home-code-proj"
    (slug / CLAUDE_SID / "subagents").mkdir(parents=True)
    (slug / CLAUDE_SID / "tool-results").mkdir(parents=True)
    (slug / "memory").mkdir()
    (slug / f"{CLAUDE_SID}.jsonl").write_text(jsonl(
        {"type": "user", "sessionId": CLAUDE_SID, "cwd": str(project), "gitBranch": "main", "version": "2.1.296",
         "uuid": "u1", "parentUuid": None, "message": {"role": "user", "content": "hi"}},
        {"type": "assistant", "sessionId": CLAUDE_SID, "uuid": "a1", "parentUuid": "u1", "message": {"role": "assistant", "content": "hello"}},
    ))
    (slug / CLAUDE_SID / "subagents" / "agent-abc.jsonl").write_text(jsonl({"type": "user", "isSidechain": True}))
    (slug / CLAUDE_SID / "subagents" / "agent-abc.meta.json").write_text("{}")
    (slug / CLAUDE_SID / "tool-results" / "toolu_1.txt").write_text("big output\n")
    (slug / f"{CLAUDE_SID}.jsonl.superseded-20261001").write_text(jsonl({"type": "user"}))
    (slug / "memory" / "MEMORY.md").write_text("- remember this\n")
    (claude / "plans").mkdir()
    (claude / "plans" / "brave-plan.md").write_text("# plan\n")
    (claude / "history.jsonl").write_text(jsonl({"display": "hi", "project": str(project)}))
    (claude / "hooks").mkdir()
    (claude / "hooks" / "guard.sh").write_text("#!/bin/sh\nexit 0\n")
    (home / "bin").mkdir()
    (home / "bin" / "status.py").write_text("print('status')\n")
    (claude / "settings.json").write_text(json.dumps({
        "env": {"ANTHROPIC_API_KEY": "sk-ant-xxxxxxxxxxxxxxxxxxxxxxxx"}, "model": "opus",
        "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "~/.claude/hooks/guard.sh"}]}]},
        "statusLine": {"type": "command", "command": f"python3 {home}/bin/status.py"},
    }))
    (claude / "CLAUDE.md").write_text("user rules\n")
    skill = claude / "skills" / "deploy"
    (skill / "scripts").mkdir(parents=True)
    (skill / "SKILL.md").write_text("---\nname: deploy\n---\nRun scripts/go.sh\n")
    (skill / "scripts" / "go.sh").write_text("echo go\n")
    (claude / ".credentials.json").write_text('{"claudeAiOauth": {"accessToken": "secret-oauth"}}')
    (claude / "sessions").mkdir()
    (claude / "sessions" / "123.key").write_text("secret-key")
    (claude / "sessions" / "123.json").write_text('{"pid": 123}')
    (claude / "file-history" / CLAUDE_SID).mkdir(parents=True)
    (claude / "file-history" / CLAUDE_SID / "abc@v1").write_text("old contents\n")
    (claude / "teams" / "alpha" / "inboxes").mkdir(parents=True)
    (claude / "teams" / "alpha" / "inboxes" / "lead.json").write_text("[]")
    (claude / "shell-snapshots").mkdir()
    (claude / "shell-snapshots" / "snap.sh").write_text("export X=1")
    (claude / "mystery.dat").write_text("?")
    (home / ".claude.json").write_text(json.dumps({
        "primaryApiKey": "sk-ant-api-secret",
        "oauthAccount": {"emailAddress": "me@example.com"},
        "mcpServers": {"gh": {"command": "npx", "env": {"GITHUB_TOKEN": "ghp_" + "a" * 36}}},
        "projects": {str(project): {"mcpServers": {"local": {"url": "http://x", "headers": {"Authorization": "Bearer abc"}}}, "history": ["x"]}},
    }))

    # Codex
    codex = home / ".codex"
    day = codex / "sessions" / "2026" / "10" / "10"
    day.mkdir(parents=True)
    rollout = day / f"rollout-2026-10-10T10-00-00-{CODEX_THREAD}.jsonl"
    rollout.write_text(jsonl(
        {"timestamp": "2026-10-10T10:00:00Z", "type": "session_meta",
         "payload": {"id": CODEX_THREAD, "session_id": CODEX_THREAD, "cwd": str(project), "cli_version": "0.162.1",
                     "originator": "codex_cli_rs", "git": {"branch": "main", "commit_hash": "abc"}}},
        {"timestamp": "2026-10-10T10:00:01Z", "type": "event_msg", "payload": {"type": "user_message", "message": "hi"}},
    ))
    (codex / "history.jsonl").write_text(jsonl({"session_id": CODEX_THREAD, "text": "hi"}))
    (codex / "config.toml").write_text('model = "gpt-5"\n[mcp_servers.gh.env]\nGITHUB_TOKEN = "ghp_secret"\n')
    (codex / "AGENTS.md").write_text("codex user rules\n")
    (codex / "auth.json").write_text('{"OPENAI_API_KEY": "sk-secret"}')
    (codex / "memories").mkdir()
    (codex / "memories" / "notes.md").write_text("codex remembers\n")

    # Pi
    pi_sessions = home / ".pi" / "agent" / "sessions" / "--home-code-proj--"
    pi_sessions.mkdir(parents=True)
    (pi_sessions / f"2026-10-10T10-00-00-000Z_{PI_SID}.jsonl").write_text(jsonl(
        {"type": "session", "version": 3, "id": PI_SID, "timestamp": "2026-10-10T10:00:00.000Z", "cwd": str(project)},
        {"type": "message", "id": "a0b1c2d3", "parentId": None, "timestamp": "2026-10-10T10:00:01.000Z",
         "message": {"role": "user", "content": "hi", "timestamp": 1}},
    ))
    (home / ".pi" / "agent" / "AGENTS.md").write_text("pi user rules\n")
    (home / ".pi" / "agent" / "auth.json").write_text('{"anthropic": {"key": "sk-secret"}}')

    # Make everything look settled (older than the partial-line window).
    old = time.time() - 3600
    for p in home.rglob("*"):
        if p.is_file():
            os.utime(p, (old, old))

    return {"home": home, "project": project, "bucket": tmp_path / "bucket", "state": tmp_path / "state", "claude": claude,
            "claude_slug": slug, "codex_rollout": rollout, "pi_dir": pi_sessions, "tmp": tmp_path}


def bucket_keys(bucket: Path) -> set[str]:
    return {p.relative_to(bucket).as_posix() for p in bucket.rglob("*") if p.is_file()}
