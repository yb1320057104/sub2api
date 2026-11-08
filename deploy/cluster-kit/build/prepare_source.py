#!/usr/bin/env python3
"""Prepare verified HA application source from a fixed Git commit or source archive.

The input checkout is read-only. Its working tree and index are never used.
Only manifest-listed source files enter the new output directory.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess
import sys
import tarfile
import tempfile

HERE = Path(__file__).resolve().parent
PATCHES = HERE.parent / 'patches'
MAX_ARCHIVE_BYTES = 1024 ** 3
MAX_FILE_BYTES = 128 * 1024 ** 2


def sha(data):
    return hashlib.sha256(data).hexdigest()


def require(value, message):
    if not value:
        raise ValueError(message)


def safe_name(value):
    require(isinstance(value, str) and value and '\\' not in value and ':' not in value, 'invalid archive path')
    path = PurePosixPath(value)
    require(not path.is_absolute() and '..' not in path.parts and path.parts[0] not in ('.git', '.runtime', '.local'), 'unsafe archive path')
    return path.as_posix()


def git(args, cwd=None):
    completed = subprocess.run(['git'] + args, cwd=str(cwd) if cwd else None,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    require(completed.returncode == 0, 'git operation failed: ' + args[0])
    return completed.stdout


def extract_verified(archive_path, output, expected):
    count, total = 0, 0
    with tarfile.open(str(archive_path), 'r:*') as archive:
        members = archive.getmembers()
        require(len(members) < 50000, 'too many archive entries')
        files = {}
        for member in members:
            name = safe_name(member.name.rstrip('/'))
            require(member.isdir() or member.isfile(), 'archive links or special files are not accepted')
            if member.isdir():
                continue
            require(name not in files and 0 <= member.size <= MAX_FILE_BYTES, 'duplicate or oversized archive file')
            total += member.size
            require(total <= MAX_ARCHIVE_BYTES, 'source archive too large')
            files[name] = member
        prefixes = ['']
        roots = {name.split('/', 1)[0] for name in files}
        if len(roots) == 1:
            prefixes.append(next(iter(roots)) + '/')
        prefixes = [prefix for prefix in prefixes if all(prefix + name in files for name in expected)]
        require(len(prefixes) == 1, 'archive does not contain the fixed base source tree')
        prefix = prefixes[0]
        for name, pin in expected.items():
            require(safe_name(name) == name and pin['mode'] in ('100644', '100755'), 'invalid manifest file')
            data = archive.extractfile(files[prefix + name]).read()
            require(sha(data) == pin['sha256'], 'base content mismatch: ' + name)
            target = output / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
            os.chmod(str(target), 0o755 if pin['mode'] == '100755' else 0o644)
            count += 1
    return count


def remove_owned_tree(path, parent, prefix):
    require(not path.is_symlink(), 'temporary cleanup refuses symlink')
    path, parent = path.resolve(), parent.resolve()
    require(path.parent == parent and path.name.startswith(prefix), 'temporary cleanup escaped its directory')
    if path.exists():
        shutil.rmtree(str(path))


def verify_existing(output, manifest, raw_manifest):
    require(output.is_dir() and not output.is_symlink(), 'existing output is not a regular directory')
    record_path=output/'source-build.json'
    require(record_path.is_file() and not record_path.is_symlink(), 'existing output is not a prepared source tree')
    record=json.loads(record_path.read_text(encoding='utf-8'))
    expected={name: pin['sha256'] for name,pin in manifest['base_files'].items()}
    expected.update({entry['path']:entry['after_sha256'] for entry in manifest['patched_files']})
    require(record.get('manifest_sha256')==sha(raw_manifest) and record.get('files')==expected,
            'existing output belongs to another manifest')
    for key in ('schema', 'release_version', 'base_commit', 'source_date_epoch',
                'source_repository', 'license', 'patch_sha256', 'targeted_tests'):
        require(record.get(key)==manifest[key], 'existing output metadata differs: '+key)
    controls=('Dockerfile','.dockerignore','verify-source.py')
    observed=set()
    for path in output.rglob('*'):
        require(not path.is_symlink(), 'existing output contains symlink')
        if path.is_file():observed.add(path.relative_to(output).as_posix())
    require(observed==set(expected)|set(controls)|{'source-build.json'}, 'existing output contains unknown or missing files')
    for name,digest in expected.items():
        require(sha((output/name).read_bytes())==digest,'existing source content differs: '+name)
    for name in controls:
        require((output/name).read_bytes()==(HERE/name).read_bytes(),'existing build control differs: '+name)
    return {'output':str(output),'base_commit':manifest['base_commit'],'source_files':len(expected),
            'version':manifest['release_version'],'existing_output_verified':True,'input_worktree_modified':False}


def prepare(repo, archive, output, manifest_path=PATCHES / 'manifest.json'):
    raw_manifest = manifest_path.read_bytes()
    manifest = json.loads(raw_manifest)
    require(manifest['schema'] == 1 and len(manifest['base_commit']) == 40, 'unsupported manifest')
    patch = manifest_path.parent / safe_name(manifest['patch'])
    require(sha(patch.read_bytes()) == manifest['patch_sha256'], 'patch hash mismatch')
    output = output.absolute()
    if output.exists() or output.is_symlink():
        return verify_existing(output,manifest,raw_manifest)
    output.parent.mkdir(parents=True, exist_ok=True)
    output = output.parent.resolve() / output.name
    temporary = Path(tempfile.mkdtemp(prefix='.source-prepare-', dir=str(output.parent)))
    try:
        tree = temporary / 'source'
        tree.mkdir()
        if repo is not None:
            commit = git(['-C', str(repo.resolve()), 'rev-parse', manifest['base_commit'] + '^{commit}']).decode().strip()
            require(commit == manifest['base_commit'], 'Git base commit differs')
            archive_path = temporary / 'base.tar'
            git(['-C', str(repo.resolve()), 'archive', '--format=tar', '--output=' + str(archive_path), commit])
        else:
            archive_path = archive.resolve()
            require(archive_path.is_file(), 'archive missing')
        file_count = extract_verified(archive_path, tree, manifest['base_files'])
        # This temporary repository is created solely to apply the patch. Never
        # initialize, reset, checkout or write the caller's input repository.
        git(['init', '--quiet'], tree)
        git(['-c', 'core.autocrlf=false', 'apply', '--check', str(patch.resolve())], tree)
        git(['-c', 'core.autocrlf=false', 'apply', str(patch.resolve())], tree)
        changed = {entry['path']: entry for entry in manifest['patched_files']}
        expected = {name: pin['sha256'] for name, pin in manifest['base_files'].items()}
        for name, entry in changed.items():
            require(safe_name(name) == name, 'invalid patch path')
            require(entry['before_sha256'] == expected.get(name), 'patch base pin differs: ' + name)
            expected[name] = entry['after_sha256']
        actual_names = {path.relative_to(tree).as_posix() for path in tree.rglob('*')
                        if path.is_file() and '.git' not in path.relative_to(tree).parts}
        require(actual_names == set(expected), 'patch unexpectedly added or removed files')
        for name, digest in expected.items():
            require(sha((tree / name).read_bytes()) == digest, 'patched content mismatch: ' + name)
        remove_owned_tree(tree / '.git', tree, '.git')
        for name in ('Dockerfile', '.dockerignore', 'verify-source.py'):
            shutil.copyfile(str(HERE / name), str(tree / name))
        build_record = {'schema': 1, 'release_version': manifest['release_version'],
                        'base_commit': manifest['base_commit'], 'source_date_epoch': manifest['source_date_epoch'],
                        'source_repository': manifest['source_repository'], 'license': manifest['license'],
                        'patch_sha256': manifest['patch_sha256'], 'manifest_sha256': sha(raw_manifest),
                        'files': expected, 'targeted_tests': manifest['targeted_tests']}
        (tree / 'source-build.json').write_text(json.dumps(build_record, indent=2, sort_keys=True) + '\n', encoding='utf-8')
        os.rename(str(tree), str(output))
        return {'output': str(output), 'base_commit': manifest['base_commit'], 'base_files_verified': file_count,
                'patched_files_verified': len(changed), 'source_files': len(expected),
                'version': manifest['release_version'], 'input_worktree_modified': False}
    finally:
        remove_owned_tree(temporary, output.parent, '.source-prepare-')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--repo', type=Path, help='existing repository containing the fixed commit; dirty worktree is allowed')
    source.add_argument('--archive', type=Path, help='git archive or GitHub tar.gz of the fixed commit')
    parser.add_argument('--output', required=True, type=Path, help='new directory, or an unchanged prepared tree to verify; never overwritten')
    args = parser.parse_args()
    print(json.dumps(prepare(args.repo, args.archive, args.output), ensure_ascii=False))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, tarfile.TarError) as error:
        print(json.dumps({'ok': False, 'error': str(error)}), file=sys.stderr)
        sys.exit(1)
