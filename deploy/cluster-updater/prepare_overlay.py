#!/usr/bin/env python3
"""Package pinned HA r4 and version-comparison inputs; no network or deployment."""
import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import os
import shutil
import subprocess
import tarfile
import tempfile
import time

BASE_COMMIT = 'fd80b8314fc2ddb9ec6470755abed0f741b27680'
PATCHES = Path(__file__).resolve().parent / 'patches'

def sha(data):
    return hashlib.sha256(data).hexdigest()

def require(value, message):
    if not value:
        raise ValueError(message)

def safe_path(value):
    require(isinstance(value, str) and value and '\\' not in value and ':' not in value, 'invalid source path')
    path = PurePosixPath(value)
    require(not path.is_absolute() and '..' not in path.parts and path.as_posix() == value, 'unsafe source path')
    return value

def supplemental_inputs(source, base, previous_paths, parent):
    raw_manifest = (PATCHES / 'manifest.json').read_bytes()
    manifest = json.loads(raw_manifest)
    require(manifest.get('schema') == 1 and manifest.get('base_commit') == BASE_COMMIT,
            'unexpected supplemental baseline')
    patch = PATCHES / safe_path(manifest['patch'])
    require(not patch.is_symlink() and PATCHES.resolve() in patch.resolve().parents
            and sha(patch.read_bytes()) == manifest['patch_sha256'], 'supplemental patch changed')
    pins = manifest.get('files')
    require(isinstance(pins, list) and bool(pins), 'supplemental file pins missing')
    names = [safe_path(row['path']) for row in pins]
    require(len(set(names)) == len(names) and not set(names).intersection(previous_paths),
            'supplemental path repeated')
    for row in pins:
        name = row['path']
        require(name.startswith('backend/') and not name.startswith(('backend/migrations/', 'backend/internal/web/dist/')),
                'unexpected supplemental path')
        require(name in base and sha(base[name]) == row['before_sha256']
                and sha((source / name).read_bytes()) == row['before_sha256'], 'supplemental base changed')
    tests = manifest.get('targeted_tests')
    require(isinstance(tests, list) and bool(tests), 'supplemental tests missing')
    temporary = Path(tempfile.mkdtemp(prefix='.version-apply-', dir=str(parent)))
    try:
        for name in names:
            p = temporary / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(base[name])
        commands = (['init', '--quiet'], ['-c', 'core.autocrlf=false', 'apply', '--check', str(patch)],
                    ['-c', 'core.autocrlf=false', 'apply', str(patch)])
        for command in commands:
            result = subprocess.run(['git', '-C', str(temporary)] + command,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
            require(result.returncode == 0, 'supplemental patch application failed')
        actual = {p.relative_to(temporary).as_posix() for p in temporary.rglob('*')
                  if p.is_file() and '.git' not in p.relative_to(temporary).parts}
        require(actual == set(names), 'supplemental patch modified unlisted files')
        result = {}
        for row in pins:
            p = temporary / row['path']
            require(p.is_file() and not p.is_symlink() and sha(p.read_bytes()) == row['after_sha256'],
                    'supplemental result changed')
            result[row['path']] = p.read_bytes()
        return result, tests, sha(raw_manifest), manifest['patch_sha256']
    finally:
        require(not temporary.is_symlink() and temporary.resolve().parent == parent.resolve()
                and temporary.name.startswith('.version-apply-'), 'unsafe supplemental cleanup')
        shutil.rmtree(str(temporary))

def prepare(repo, source, manifest_path, output):
    source, output = Path(source).resolve(), Path(output).absolute()
    manifest_path = Path(manifest_path).resolve()
    manifest_raw = manifest_path.read_bytes()
    release = json.loads(manifest_raw)
    require(release.get('schema') == 1 and release.get('base_commit') == BASE_COMMIT, 'unexpected release base')
    patch = manifest_path.parent / safe_path(release['patch'])
    require(sha(patch.read_bytes()) == release['patch_sha256'], 'release patch changed')
    require(source.is_dir() and not output.exists() and not output.is_symlink(), 'output exists or source missing')
    result = subprocess.run(['git', '-C', str(Path(repo).resolve()), 'archive', '--format=tar', BASE_COMMIT], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
    require(result.returncode == 0, 'fixed Git archive unavailable')
    expected = release['base_files']
    base = {}
    with tarfile.open(fileobj=io.BytesIO(result.stdout), mode='r:') as archive:
        for member in archive:
            if member.name not in expected:
                continue
            require(member.isfile() and member.name not in base, 'invalid baseline archive member')
            data = archive.extractfile(member).read()
            require(sha(data) == expected[member.name]['sha256'], 'base content mismatch')
            base[member.name] = data
    require(set(base) == set(expected), 'incomplete fixed baseline')
    final_pins = {name: entry['sha256'] for name, entry in expected.items()}
    final_pins.update({row['path']: row['after_sha256'] for row in release['patched_files']})
    for name, expected_sha in final_pins.items():
        safe_path(name)
        path = source / name
        if expected_sha is None:
            require(not path.exists() and not path.is_symlink(), 'deleted source present')
        else:
            require(path.is_file() and not path.is_symlink() and source in path.resolve().parents and sha(path.read_bytes()) == expected_sha, 'prepared source content mismatch: ' + name)
    migrations = {name: pin['sha256'] for name, pin in expected.items() if name.startswith('backend/migrations/')}
    require(bool(migrations), 'empty migration baseline')
    output.parent.mkdir(parents=True, exist_ok=True)
    extra, extra_tests, extra_manifest_sha, extra_patch_sha = supplemental_inputs(
        source, base, {row['path'] for row in release['patched_files']}, output.parent)
    temporary = Path(tempfile.mkdtemp(prefix='.overlay-prepare-', dir=str(output.parent)))
    temporary.chmod(0o700)
    rows = []
    try:
        for row in release['patched_files']:
            name = safe_path(row['path'])
            require((name.startswith('backend/') or name == 'deploy/.env.example') and not name.startswith('backend/migrations/'), 'unexpected HA overlay path')
            original = base.get(name)
            require((sha(original) if original is not None else None) == row['before_sha256'], 'patch base mismatch')
            ours = (source / name).read_bytes() if row['after_sha256'] is not None else None
            for kind, data in (('base', original), ('ours', ours)):
                if data is None:
                    continue
                destination = temporary / kind / name
                destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                destination.write_bytes(data)
                destination.chmod(0o600)
            rows.append({'path': name, 'base_sha256': row['before_sha256'], 'ours_sha256': row['after_sha256']})
        for name, ours in sorted(extra.items()):
            for kind, data in (('base', base[name]), ('ours', ours)):
                destination = temporary / kind / name
                destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                destination.write_bytes(data)
                destination.chmod(0o600)
            rows.append({'path': name, 'base_sha256': sha(base[name]), 'ours_sha256': sha(ours)})
        manifest = {'schema': 1, 'build_type': 'cluster-ha', 'capabilities': ['ha-r4', 'version-baseline'], 'base_commit': BASE_COMMIT, 'base_baseline': '0.2.19-pk.58', 'files': rows, 'base_migrations': migrations, 'targeted_tests': release['targeted_tests'] + extra_tests, 'source_manifest_sha256': sha(manifest_raw), 'source_patch_sha256': release['patch_sha256'], 'supplemental_manifest_sha256': extra_manifest_sha, 'supplemental_patch_sha256': extra_patch_sha}
        payload = (json.dumps(manifest, sort_keys=True, indent=2) + '\n').encode()
        (temporary / 'manifest.json').write_bytes(payload)
        (temporary / 'manifest.json').chmod(0o600)
        # Windows scanners may briefly retain a handle after checking new files.
        # Keep the atomic rename and never overwrite an existing output tree.
        for attempt in range(6):
            require(not output.exists() and not output.is_symlink(), 'output appeared during preparation')
            try:
                os.rename(str(temporary), str(output))
                break
            except PermissionError as error:
                if os.name != 'nt' or getattr(error, 'winerror', None) not in (5, 32) or attempt == 5:
                    raise
                time.sleep(0.2 * (attempt + 1))
        return {'output': str(output), 'files': len(rows), 'migration_files': len(migrations), 'sql_migrations': sum(name.endswith('.sql') for name in migrations), 'overlay_sha256': sha(payload), 'base_commit': BASE_COMMIT}
    finally:
        if temporary.exists():
            require(not temporary.is_symlink() and temporary.resolve().parent == output.parent.resolve() and temporary.name.startswith('.overlay-prepare-'), 'unsafe temporary cleanup')
            shutil.rmtree(str(temporary))

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', required=True, type=Path)
    parser.add_argument('--source', required=True, type=Path)
    parser.add_argument('--manifest', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    print(json.dumps(prepare(args.repo, args.source, args.manifest, args.output)))

if __name__ == '__main__':
    main()
