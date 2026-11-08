#!/usr/bin/env bash
set -euo pipefail
umask 077
toolkit_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
command -v python3 >/dev/null || { echo '请先安装 Python 3.11+ 和 python3-venv。' >&2; exit 1; }
python3 -c 'import sys; assert sys.version_info >= (3,11), "Requires Python 3.11+"'
if [[ ! -x "$toolkit_dir/.venv/bin/python" ]]; then
  python3 -m venv "$toolkit_dir/.venv"
fi
requirements_digest="$(sha256sum "$toolkit_dir/requirements.txt" | cut -d' ' -f1)"
installed_digest="$(cat "$toolkit_dir/.venv/requirements.sha256" 2>/dev/null || true)"
if [[ "$requirements_digest" != "$installed_digest" ]]; then
  "$toolkit_dir/.venv/bin/python" -m pip install -r "$toolkit_dir/requirements.txt"
  printf '%s\n' "$requirements_digest" > "$toolkit_dir/.venv/requirements.sha256"
fi
export PATH="$toolkit_dir/.venv/bin:$PATH"
exec "$toolkit_dir/.venv/bin/python" "$toolkit_dir/cluster.py" "$@"
