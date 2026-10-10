"""Redaction for harness snapshot files (settings, MCP configs, extensions).

Transcripts are uploaded unchanged to the private bucket and redacted later, when
they are normalized. Snapshot files are redacted before upload because settings
files routinely carry API keys in `env` blocks and MCP server definitions.
"""

from __future__ import annotations

import json
import re

REDACTED = "[REDACTED]"

SENSITIVE_KEY = re.compile(r"(token|secret|passw(or)?d|api[_-]?key|access[_-]?key|private[_-]?key|credential|^auth(orization)?$|cookie)", re.I)

# Well-known credential shapes, scrubbed from any text.
TOKEN_PATTERNS = [
    re.compile(r"sk-ant-[A-Za-z0-9_-]{20,}"),
    re.compile(r"sk-(?:proj-)?[A-Za-z0-9_-]{20,}"),
    re.compile(r"gh[pousr]_[A-Za-z0-9]{30,}"),
    re.compile(r"github_pat_[A-Za-z0-9_]{30,}"),
    re.compile(r"AKIA[0-9A-Z]{16}"),
    re.compile(r"xox[abprs]-[A-Za-z0-9-]{10,}"),
    re.compile(r"AIza[0-9A-Za-z_-]{35}"),
    re.compile(r"eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}"),  # JWT
    re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----"),
    re.compile(r"(?<=[Bb]earer )[A-Za-z0-9._~+/=-]{16,}"),
    re.compile(r"(?<=://)[^/\s:@]+:[^/\s@]+(?=@)"),  # user:password@ in URLs
]

# Keys whose values are all secrets-in-waiting (MCP server env and HTTP headers).
ENV_KEYS = {"env", "environment", "headers", "http_headers", "env_http_headers"}
TOML_TABLE = re.compile(r"^\s*\[\[?\s*(?P<name>[^\]]+?)\s*\]\]?\s*(?:#.*)?$")
TOML_KV = re.compile(r"^(?P<lhs>\s*(?P<key>[\w\"'.-]+)\s*=\s*).+$")

ASSIGNMENT = re.compile(
    r"^(?P<lhs>\s*(?:export\s+)?[\w.-]*(?:token|secret|passw(?:or)?d|api[_-]?key|access[_-]?key|credential)[\w.-]*\s*[=:]\s*).+$",
    re.I | re.M,
)


def _redact_json(obj, under_env: bool = False):
    if isinstance(obj, dict):
        out = {}
        for k, v in obj.items():
            key_is_env = k.lower() in ENV_KEYS
            if isinstance(v, str) and (under_env or SENSITIVE_KEY.search(k)):
                out[k] = REDACTED
            else:
                out[k] = _redact_json(v, under_env or key_is_env)
        return out
    if isinstance(obj, list):
        return [_redact_json(v, under_env) for v in obj]
    if isinstance(obj, str):
        return scrub_text(obj)
    return obj


def scrub_text(text: str) -> str:
    for pat in TOKEN_PATTERNS:
        text = pat.sub(REDACTED, text)
    return text


def redact_json_bytes(obj) -> bytes:
    return (json.dumps(_redact_json(obj), indent=2, sort_keys=True) + "\n").encode()


def redact_file(name: str, data: bytes) -> bytes:
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return b""  # snapshots are text configs; drop binaries
    if name.endswith(".json"):
        try:
            return redact_json_bytes(json.loads(text))
        except ValueError:
            pass
    if name.endswith(".toml"):
        text = _redact_toml(text)
    if name.endswith((".toml", ".env", ".ini", ".cfg", ".yaml", ".yml")) or name.startswith(".env"):
        text = ASSIGNMENT.sub(lambda m: m.group("lhs") + REDACTED, text)
    return scrub_text(text).encode()


def _redact_toml(text: str) -> str:
    """Blank every value inside [*.env] / [*.headers] tables and inline `env = {...}` values."""
    out = []
    in_env_table = False
    for line in text.splitlines(keepends=True):
        if m := TOML_TABLE.match(line):
            in_env_table = m["name"].split(".")[-1].strip("\"' ").lower() in ENV_KEYS
            out.append(line)
            continue
        m = TOML_KV.match(line)
        if m and (in_env_table or m["key"].split(".")[-1].strip("\"'").lower() in ENV_KEYS):
            out.append(m["lhs"] + f'"{REDACTED}"' + ("\n" if line.endswith("\n") else ""))
        else:
            out.append(line)
    return "".join(out)
