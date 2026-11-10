#!/usr/bin/env python3
"""Prepare a reviewed HA adaptation against an immutable candidate commit.

Historical r4 inputs remain unchanged. The profile pins every candidate input
and reviewed result; changed candidate bytes require another explicit review.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tempfile

from builder import BASE_COMMIT, overlay_manifest


def require(value, message):
    if not value:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def path_name(name):
    # Profile inputs are files, never links or generated build output.
    require(isinstance(name, str), 'invalid adaptation path')
    p = PurePosixPath(name)
    require(isinstance(name, str) and not p.is_absolute() and '..' not in p.parts
            and '\\' not in name and ':' not in name
            and (name.startswith('backend/') or name == 'deploy/.env.example')
            and not name.startswith(('backend/migrations/', 'backend/internal/web/dist/')),
            'invalid adaptation path')
    return name


def candidate_file(repo, commit, name):
    result = subprocess.run(['git', '-C', str(repo), 'show', commit + ':' + name],
                            capture_output=True, timeout=60)
    return result.stdout if result.returncode == 0 else None


def prepare(repo, commit, previous, profile, output):
    require(re.fullmatch(r'[a-f0-9]{40}', commit), 'candidate must be a full commit')
    repo, previous, profile, output = map(Path, (repo, previous, profile, output))
    resolved = subprocess.check_output(['git', '-C', str(repo), 'rev-parse', commit + '^{commit}'], text=True).strip()
    require(resolved == commit, 'candidate commit unavailable')
    old_root, old = overlay_manifest({'overlay_dir': str(previous)})
    review_bytes = (profile / 'manifest.json').read_bytes()
    review = json.loads(review_bytes)
    require(review.get('schema') == 1 and review.get('baseline_overlay_sha256') == sha((old_root / 'manifest.json').read_bytes()),
            'adaptation baseline changed')
    patch = profile / review.get('patch', '')
    require(patch.is_file() and not patch.is_symlink() and patch.resolve().parent == profile.resolve()
            and sha(patch.read_bytes()) == review.get('patch_sha256'), 'adaptation patch changed')
    rows = review.get('files')
    require(isinstance(rows, list) and rows, 'adaptation file list missing')
    names = [path_name(row['path']) for row in rows]
    require(len(names) == len(set(names)) and set(names) == {row['path'] for row in old['files']},
            'adaptation must review every original overlay path')
    require(not output.exists() and not output.is_symlink(), 'output must be new')
    output.parent.mkdir(parents=True, exist_ok=True)
    temp = Path(tempfile.mkdtemp(prefix='.candidate-overlay-', dir=output.parent))
    try:
        working = temp / 'patched'
        working.mkdir()
        for row in rows:
            name = row['path']
            raw = candidate_file(repo, commit, name)
            require((sha(raw) if raw is not None else None) == row['before_sha256'],
                    'candidate file changed: ' + name)
            if raw is not None:
                for kind in ('base', 'patched'):
                    p = temp / kind / name
                    p.parent.mkdir(parents=True, exist_ok=True)
                    p.write_bytes(raw)
        for command in (['init', '--quiet'], ['-c', 'core.autocrlf=false', 'apply', '--check', str(patch.resolve())],
                        ['-c', 'core.autocrlf=false', 'apply', str(patch.resolve())]):
            result = subprocess.run(['git', '-C', str(working)] + command, capture_output=True, timeout=60)
            require(result.returncode == 0, 'adaptation patch failed')
        require(not any(p.is_symlink() for p in working.rglob('*')), 'adaptation introduced a symlink')
        actual = {p.relative_to(working).as_posix() for p in working.rglob('*')
                  if p.is_file() and '.git' not in p.relative_to(working).parts}
        require(actual == {row['path'] for row in rows if row['after_sha256'] is not None},
                'adaptation changed unlisted files')
        for row in rows:
            p = working / row['path']
            raw = p.read_bytes() if p.is_file() else None
            require((sha(raw) if raw is not None else None) == row['after_sha256'],
                    'adaptation result changed: ' + row['path'])
            if raw is not None:
                dest = temp / 'ours' / row['path']
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_bytes(raw)
        shutil.rmtree(working)
        manifest = dict(old)
        manifest.update(candidate_base_commit=commit,
                        adaptation_profile_sha256=sha(review_bytes),
                        adaptation_patch_sha256=review['patch_sha256'],
                        capabilities=sorted(set(old.get('capabilities', []) + ['pk61-background-lifecycle'])),
                        files=[{'path': row['path'], 'base_sha256': row['before_sha256'], 'ours_sha256': row['after_sha256']} for row in rows])
        payload = (json.dumps(manifest, sort_keys=True, indent=2) + '\n').encode()
        (temp / 'manifest.json').write_bytes(payload)
        for p in temp.rglob('*'):
            p.chmod(0o700 if p.is_dir() else 0o600)
        os.rename(temp, output)
        return {'output': str(output), 'source_commit': commit, 'files': len(rows), 'overlay_sha256': sha(payload)}
    finally:
        if temp.exists():
            require(temp.resolve().parent == output.parent.resolve() and temp.name.startswith('.candidate-overlay-')
                    and not temp.is_symlink(), 'unsafe temporary cleanup')
            shutil.rmtree(temp)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('repo', 'commit', 'previous', 'profile', 'output'):
        parser.add_argument('--' + name, required=True)
    args = parser.parse_args()
    print(json.dumps(prepare(args.repo, args.commit, args.previous, args.profile, args.output)))
