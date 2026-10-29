#!/usr/bin/env python3
"""Prepare only: never builds/runs Go or launches a container."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True, help='absolute frozen source checkout')
p.add_argument('--output', type=Path, required=True, help='absolute new private overlay directory')
p.add_argument('--worker-source', default='/src', help='absolute worker mount for sanitized source')
p.add_argument('--worker-overlay', default='/overlay', help='absolute worker mount for overlay directory')
a = p.parse_args()
if not a.source.is_absolute() or not a.output.is_absolute():
    p.error('absolute paths required')
source = a.source.resolve(strict=True)
head = subprocess.check_output(['git', '-C', str(source), 'rev-parse', 'HEAD'], text=True).strip()
allowed = [subprocess.check_output(['git', '-C', str(source), 'rev-parse', ref], text=True).strip()
           for ref in ('b8cbed2', 'c8fdf7a')]
if head not in allowed:
    p.error('source must be exactly b8cbed2 or c8fdf7a (use private frozen worker snapshots)')
if subprocess.check_output(['git', '-C', str(source), 'diff', '--name-only', 'HEAD'], text=True).strip():
    p.error('tracked source changes would confound matching')
if not Path(a.worker_source).is_absolute() or not Path(a.worker_overlay).is_absolute():
    p.error('absolute worker mappings required')
# Refuse untracked/ignored compilable Go files; do not attest only git diff.
tracked = set(subprocess.check_output(['git', '-C', str(source), 'ls-files'], text=True).splitlines())
for base in ('cmd', 'internal'):
    for f in (source / base).rglob('*.go'):
        if str(f.relative_to(source)) not in tracked:
            p.error('untracked Go input requires sanitized snapshot review')
digest = hashlib.sha256()
for name in sorted(tracked):
    if name.startswith(('cmd/', 'internal/')) or name in ('go.mod', 'go.sum', 'flake.nix', 'flake.lock'):
        f = source / name
        if f.is_file():
            digest.update(name.encode() + b'\0' + hashlib.sha256(f.read_bytes()).digest())
os.umask(0o077)
a.output.mkdir(mode=0o700, parents=False, exist_ok=False)
template = Path(__file__).resolve().with_name('fixture_test.go.txt').read_bytes()
target = a.output / 'performance_diagnostic_test.go'
target.write_bytes(template)
overlay = {'Replace': {str(Path(a.worker_source) / 'internal/cli/performance_diagnostic_test.go'): str(Path(a.worker_overlay) / target.name)}}
(a.output / 'overlay.json').write_text(json.dumps(overlay, indent=2) + '\n')
(a.output / 'identity.json').write_text(json.dumps({
    'source_input_sha256': digest.hexdigest(), 'source_commit': head, 'overlay_sha256': hashlib.sha256(template).hexdigest(),
    'seed': 'xorshift-local-fixed-v1', 'ordinary_mains': 96, 'artifacts': 96,
    'transform_sources': 9, 'enrichment': False,
    'poll_seconds': 1, 'capture_workers': 1, 'delivery_workers': 1,
}, indent=2) + '\n')
print(json.dumps({'source_input_sha256': digest.hexdigest(), 'source_commit': head, 'overlay_sha256': hashlib.sha256(template).hexdigest()}))
