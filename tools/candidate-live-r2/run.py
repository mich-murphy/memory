#!/usr/bin/env python3
"""Deliberate live-only launcher. Parse private config as DATA, never source a shell."""
import argparse
import os
from pathlib import Path
import re
import stat
import subprocess
import sys


KEYS = {'R2_ENDPOINT', 'R2_BUCKET', 'R2_ACCESS_KEY_ID', 'R2_SECRET_ACCESS_KEY'}


def private_values(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise ValueError('private_config_permissions')
        raw = stream.read(65537)
    if len(raw) > 65536:
        raise ValueError('private_config_size')
    values = {}
    for line in raw.decode('utf-8').splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        match = re.fullmatch(r'(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)', line)
        if not match or match[1] not in KEYS:
            continue
        key, value = match.groups()
        if key in values:
            raise ValueError('duplicate_private_setting')
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        # No substitution, expansion, inline comments, or shell execution.
        # Reject ambiguous shell syntax rather than guessing credential contents.
        if not value or any(c.isspace() or c in '$`\\\"\'\x00' for c in value):
            raise ValueError('unsupported_private_setting_syntax')
        values[key] = value
    if set(values) != KEYS:
        raise ValueError('missing_private_setting')
    return values


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config-env', required=True, type=Path)
    parser.add_argument('--analytics-python', required=True, type=Path)
    parser.add_argument('--live-new-synthetic-namespace-only', action='store_true', required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    env = os.environ.copy()
    # Do not activate any legacy live tests, even when inherited from the caller.
    env.pop('AGENT_SESSIONS_TEST_R2', None)
    env.pop('AGENT_SESSIONS_LIVE', None)
    env.update(private_values(args.config_env))
    env['CANDIDATE_R2_LIVE_ACCEPTANCE'] = 'new-synthetic-namespace-only'
    # Preserve the invoked venv path: resolving its executable symlink selects
    # the base interpreter and silently loses the installed analytics packages.
    interpreter = args.analytics_python.absolute()
    if not interpreter.is_file() or not os.access(interpreter, os.X_OK):
        raise ValueError('analytics_interpreter_unavailable')
    env['CANDIDATE_R2_ANALYTICS_PYTHON'] = str(interpreter)
    env['PYTHONDONTWRITEBYTECODE'] = '1'
    return subprocess.run(['go', 'test', './internal/publicationr2',
                           '-run', '^TestCandidateR2LiveAcceptance$', '-count=1',
                           '-timeout=4m', '-v'], cwd=root, env=env, check=False).returncode


if __name__ == '__main__':
    try:
        status = main()
    except (OSError, ValueError, UnicodeError):
        # Never stringify parser/file/subprocess errors with private values.
        print('candidate live launcher failed (private details withheld)', file=sys.stderr)
        status = 1
    sys.exit(status)
