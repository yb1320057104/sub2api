#!/usr/bin/env python3
"""Build a shareable toolkit from explicit paths; never archive site state."""
import hashlib
import json
from pathlib import Path
import re
import sys
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TOP = {'README.md', 'VALIDATION.md', 'LICENSE', 'requirements.txt', '.gitignore', '.gitattributes', 'cluster.py', 'deploy.sh'}
DIRECTORIES = {'ansible', 'build', 'patches', 'tools'}
SUFFIXES = {'.py', '.yml', '.yaml', '.j2', '.cfg', '.json', '.md', '.txt', '.patch', '.sh'}


def files():
    result = []
    for path in sorted(ROOT.rglob('*')):
        relative = path.relative_to(ROOT)
        if not path.is_file() or path.is_symlink() or any(p in ('__pycache__', '.venv', 'sites', 'dist', '.runtime') for p in relative.parts):
            continue
        if path.name.startswith('test_'):
            continue
        if (len(relative.parts) == 1 and path.name in TOP) or (relative.parts[0] in DIRECTORIES and (path.suffix in SUFFIXES or path.name in ('.dockerignore', 'Dockerfile', 'LICENSE') or path.name.startswith('Dockerfile.'))):
            result.append(path)
    required = TOP - {'.gitignore'}
    if not required.issubset({p.name for p in result if p.parent == ROOT}):
        raise RuntimeError('Required release documents missing')
    if not any(p.suffix == '.patch' for p in result):
        raise RuntimeError('Source patch is missing')
    for path in result:
        raw = path.read_bytes()
        # A key header, common actual token formats, or private data fixture must
        # never make it into a deployment toolkit. Generated placeholders are OK.
        if re.search(rb'-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----\r?\n[A-Za-z0-9+/=]{16,}|gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,}', raw):
            raise RuntimeError('Credential-shaped value found in ' + str(path.relative_to(ROOT)))
    return result


def main():
    selected = files()
    output = ROOT / 'dist'
    output.mkdir(exist_ok=True)
    archive = output / 'sub2api-cluster-kit-0.1.0.zip'
    manifest = {str(p.relative_to(ROOT)).replace('\\', '/'): hashlib.sha256(p.read_bytes()).hexdigest() for p in selected}
    with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as bundle:
        for path in selected:
            name = 'sub2api-cluster-kit/' + path.relative_to(ROOT).as_posix()
            info = zipfile.ZipInfo(name, date_time=(2026, 11, 8, 0, 0, 0))
            info.external_attr = (0o100755 if path.suffix == '.sh' else 0o100644) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            bundle.writestr(info, path.read_bytes())
        bundle.writestr('sub2api-cluster-kit/manifest.sha256.json', json.dumps(manifest, indent=2, sort_keys=True) + '\n')
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    (output / 'SHA256SUMS').write_text(digest + '  ' + archive.name + '\n', encoding='ascii')
    print(json.dumps({'archive': str(archive), 'files': len(selected), 'bytes': archive.stat().st_size, 'sha256': digest}))


if __name__ == '__main__':
    try:
        main()
    except RuntimeError as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)
