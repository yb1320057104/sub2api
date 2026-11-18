#!/usr/bin/env bash
# Build in a NEW isolated directory. Never change /opt/kilo-login, the supplied
# portable runtime, running units, credentials or production configuration.
# Dependencies: existing portable reauth runtime, Rust1.98, verified cfsolve +
# Sentinel assets. Optional wheelhouse keeps pip fully offline on the build host.
set -euo pipefail
: "${KILO_BASE_RUNTIME:?set KILO_BASE_RUNTIME to the unpacked existing Python/Node/toSub2 runtime}"
: "${CFSOLVE_SOURCE:?set CFSOLVE_SOURCE to verified cfsolve source (19efa316)}"
: "${KILO_SENTINEL_SOURCE:?set KILO_SENTINEL_SOURCE to the verified Sentinel asset directory}"
: "${KILO_BUILD_DIR:?set KILO_BUILD_DIR to a NEW isolated build directory}"
script_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
case "$KILO_BUILD_DIR" in /opt/*|/etc/*|/usr/*|/var/lib/*) echo 'choose an isolated build directory outside live service roots' >&2; exit 2;; esac
if [ -e "$KILO_BUILD_DIR" ]; then echo 'KILO_BUILD_DIR must not exist' >&2; exit 2; fi
if [ ! -x "$KILO_BASE_RUNTIME/python" ] || [ ! -x "$KILO_BASE_RUNTIME/node" ]; then echo 'incomplete portable runtime' >&2; exit 2; fi
mkdir -m 0700 "$KILO_BUILD_DIR"
cp -aL "$KILO_BASE_RUNTIME" "$KILO_BUILD_DIR/runtime"
runtime="$KILO_BUILD_DIR/runtime"
export NODE_EXECUTABLE="$runtime/node" TOSUB2_PYTHON="$runtime/python" TOSUB2_ROOT="$runtime/tosub2"
# All changes below affect the copied package. Never use a host-global pip.
if [ -n "${KILO_WHEELHOUSE:-}" ]; then
  "$runtime/python" -m pip install --no-index --find-links "$KILO_WHEELHOUSE" --only-binary=:all: --no-cache-dir -r "$script_root/requirements.txt"
else
  "$runtime/python" -m pip install --only-binary=:all: --no-cache-dir -r "$script_root/requirements.txt"
fi
"$runtime/python" -c 'import requests,socks; print("package HTTP/SOCKS dependencies present")'
export CFSOLVE_OUTPUT="$KILO_BUILD_DIR/kilo-cfsolve"
bash "$script_root/deploy/build_cfsolve.sh"
"$runtime/python" -B "$script_root/test_runtime.py" -v
"$runtime/node" "$script_root/test_protocol.mjs"
output="${KILO_BUNDLE_OUTPUT:-$KILO_BUILD_DIR/kilo-runtime-linux-$(uname -m).tar.gz}"
"$runtime/python" -B "$script_root/deploy/build_bundle.py" --runtime "$runtime" \
  --sentinel-assets "$KILO_SENTINEL_SOURCE" \
  --profile "${KILO_PROFILE_SOURCE:-$script_root/profiles/safari-26-ios.json}" \
  --cfsolve-bin "$CFSOLVE_OUTPUT" --output "$output"
# Relocate once and exercise only offline checks. No login, task submission or
# service changes occur in this acceptance step.
mkdir -m 0700 "$KILO_BUILD_DIR/relocated"
tar -xzf "$output" -C "$KILO_BUILD_DIR/relocated"
portable="$KILO_BUILD_DIR/relocated"
TOSUB2_ROOT="$portable/tosub2" NODE_EXECUTABLE="$portable/node" TOSUB2_PYTHON="$portable/python" \
  "$portable/python" -B "$portable/kilo-runtime/runtime_service.py" --check
printf 'bundle=%s\n' "$output"
sha256sum "$output"
