#!/usr/bin/env python3
"""Verify pinned source input bytes before Docker build dependency installation."""
import hashlib
import json
from pathlib import Path, PurePosixPath

root = Path(__file__).resolve().parent
record = json.loads((root / 'source-build.json').read_text())
for name, expected in record['files'].items():
    logical = PurePosixPath(name)
    if logical.is_absolute() or '..' in logical.parts or '\\' in name or ':' in name:
        raise SystemExit('invalid source manifest path')
    path = root / name
    if path.is_symlink() or not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
        raise SystemExit('source verification failed: ' + name)
expected_names = set(record['files']) | {'Dockerfile', '.dockerignore', 'verify-source.py', 'source-build.json'}
observed_names = set()
for path in root.rglob('*'):
    if path.is_symlink():
        raise SystemExit('source tree contains a symbolic link')
    if path.is_file():
        observed_names.add(path.relative_to(root).as_posix())
if observed_names != expected_names:
    raise SystemExit('source tree contains unexpected or missing files')
print('Verified %d pinned source files.' % len(record['files']))
