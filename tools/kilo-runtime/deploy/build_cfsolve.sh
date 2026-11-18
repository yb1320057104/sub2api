#!/usr/bin/env bash
# Compile a no-artifact, stdin-only adapter against the already verified cfsolve
# library. Build in an isolated copy; never edit the supplied source checkout.
set -euo pipefail
: "${CFSOLVE_SOURCE:?set CFSOLVE_SOURCE to the verified cfsolve checkout}"
: "${CFSOLVE_OUTPUT:?set CFSOLVE_OUTPUT to the output Linux binary path}"
script_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if [ "$(git -c "safe.directory=$CFSOLVE_SOURCE" -C "$CFSOLVE_SOURCE" rev-parse HEAD)" != '19efa31656328528457376ae0895a61164b22da0' ]; then
  echo 'cfsolve source commit must match the verified 19efa316 build' >&2
  exit 2
fi
source_root=$(CDPATH= cd -- "$CFSOLVE_SOURCE" && pwd)
if [ -n "$(git -c "safe.directory=$source_root" -C "$source_root" status --porcelain --untracked-files=all)" ]; then
  echo 'cfsolve fixed checkout must be clean before building' >&2
  exit 2
fi
if [ "$(rustc +1.98.0 --version | cut -d ' ' -f 2)" != 1.98.0 ]; then
  echo 'cfsolve wrapper requires Rust 1.98.0' >&2
  exit 2
fi
# Cargo's own target lock serializes builds. The existing cache may gain the
# wrapper and dependency metadata; no checkout file is changed. Callers may set
# an isolated target instead when cache writes are undesirable.
target_dir=${CFSOLVE_TARGET_DIR:-$source_root/target}
mkdir -p "$target_dir"
target_dir=$(CDPATH= cd -- "$target_dir" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
python3 - "$source_root" "$work/source-before.json" <<'PY'
import hashlib,json,os,sys
from pathlib import Path
root=Path(sys.argv[1]);files={}
for folder,dirs,names in os.walk(root):
    dirs[:]=sorted(d for d in dirs if d not in {'target','.git','__pycache__'})
    for name in sorted(names):
        path=Path(folder)/name
        if path.is_file(): files[path.relative_to(root).as_posix()]=hashlib.sha256(path.read_bytes()).hexdigest()
Path(sys.argv[2]).write_text(json.dumps(files,sort_keys=True))
PY
# Do not copy the potentially multi-gigabyte target or git database into /tmp.
tar -C "$source_root" --exclude='./target' --exclude='./.git' -cf - . | tar -C "$work" -xf -
mkdir -p "$work/src/bin"
cp "$script_root/cfsolve/bridge.rs" "$work/src/bin/kilo-cfsolve.rs"
(cd "$work" && CARGO_TARGET_DIR="$target_dir" cargo +1.98.0 build --locked --release --bin kilo-cfsolve)
install -m 0700 "$target_dir/release/kilo-cfsolve" "$CFSOLVE_OUTPUT"
python3 - "$source_root" "$work/source-before.json" "$CFSOLVE_OUTPUT" "$target_dir" "$script_root/cfsolve/bridge.rs" <<'PY'
import hashlib,json,os,sys
from pathlib import Path
root=Path(sys.argv[1]);before=json.loads(Path(sys.argv[2]).read_text());after={}
for folder,dirs,names in os.walk(root):
    dirs[:]=sorted(d for d in dirs if d not in {'target','.git','__pycache__'})
    for name in sorted(names):
        path=Path(folder)/name
        if path.is_file(): after[path.relative_to(root).as_posix()]=hashlib.sha256(path.read_bytes()).hexdigest()
if before!=after: raise SystemExit('cfsolve source changed during isolated build')
binary=Path(sys.argv[3]);record={'cfsolve_commit':'19efa31656328528457376ae0895a61164b22da0','rust_toolchain':'1.98.0','wrapper_source_sha256':hashlib.sha256(Path(sys.argv[5]).read_bytes()).hexdigest(),'source_unchanged':True,'source_files':len(before),'source_tree_sha256':hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest(),'cargo_target_dir':sys.argv[4],'source_copy_excluded':['target','.git'],'wrapper_sha256':hashlib.sha256(binary.read_bytes()).hexdigest()}
Path(str(binary)+'.source-verification.json').write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps(record),flush=True)
PY
sha256sum "$CFSOLVE_OUTPUT"
