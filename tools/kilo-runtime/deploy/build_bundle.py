"""Assemble a Linux Kilo bundle from portable or explicit system dependencies.

Portable mode copies the existing Python/Node runtime. System mode packages the
Kilo/toSub2 assets and absolute-path launchers; it requires those Python/Node
installations on the destination and does not claim host-independent portability.
No login, service change, dependency installation or download occurs here.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import shlex
import subprocess
import sys
import tarfile
import tempfile

SOURCE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SOURCE))
from protocol import ASSET_HASHES, TOSUB2_BLOB, profile_files


def check_hash(path: Path, digest: str):
    if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
        raise ValueError("asset checksum mismatch: " + path.name)


def system_launcher(binary: Path) -> str:
    if not binary.is_absolute() or not str(binary).startswith("/") or any(ch in str(binary) for ch in "\r\n"):
        raise ValueError("system runtime executable must be an absolute Linux path")
    return '#!/bin/sh\nset -eu\nexec ' + shlex.quote(str(binary)) + ' "$@"\n'


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--runtime", type=Path, help="portable mode: unpacked existing Sub2API reauth runtime")
    p.add_argument("--system-python", type=Path, help="system mode: destination's absolute Python executable")
    p.add_argument("--system-node", type=Path, help="system mode: destination's absolute Node executable")
    p.add_argument("--tosub2-root", type=Path, help="system mode: fixed toSub2 checkout including node_modules/jsdom")
    p.add_argument("--sentinel-assets", type=Path, required=True)
    p.add_argument("--profile", type=Path, default=SOURCE / "profiles/safari-26-ios.json")
    p.add_argument("--cfsolve-bin", type=Path, required=True, help="Linux kilo-cfsolve compiled by build_cfsolve.sh")
    p.add_argument("--output", type=Path, required=True)
    args = p.parse_args()
    system = bool(args.system_python or args.system_node or args.tosub2_root)
    if bool(args.runtime) == system or system and not all((args.system_python, args.system_node, args.tosub2_root)):
        p.error("choose --runtime OR all of --system-python/--system-node/--tosub2-root")
    tosub2 = args.tosub2_root if system else args.runtime / "tosub2"
    if system:
        for binary in (args.system_python, args.system_node):
            system_launcher(binary)
            if not binary.is_file() or not os.access(binary, os.X_OK):
                raise ValueError("system executable missing: " + str(binary))
        subprocess.run([str(args.system_python), "-B", "-c", "import requests,socks; from ssl import SSLContext"], check=True)
        subprocess.run([str(args.system_node), "--version"], check=True)
    else:
        for name in ("python", "node"):
            if not (args.runtime / name).is_file():
                raise ValueError("incomplete base runtime: " + name)
        packages = list((args.runtime / "python-root/lib").glob("python*/site-packages"))
        if not any((folder / "requests/__init__.py").is_file() and (folder / "socks.py").is_file() for folder in packages):
            raise ValueError("base runtime must include requests[socks] from kilo-runtime/requirements.txt")
    for name in ("src/protocol-login.mjs", "src/tls-transport.mjs", "node_modules/jsdom/lib/api.js", "LICENSE"):
        if not (tosub2 / name).is_file():
            raise ValueError("incomplete toSub2 assets: " + name)
    source = (tosub2 / "src/protocol-login.mjs").read_bytes()
    if hashlib.sha1(f"blob {len(source)}\0".encode() + source).hexdigest() != TOSUB2_BLOB:
        raise ValueError("unsupported toSub2 source")
    for name, digest in ASSET_HASHES.items():
        check_hash(args.sentinel_assets / name, digest)
    answer_files = profile_files(args.profile)
    binary = args.cfsolve_bin.read_bytes()
    if binary[:4] != b"\x7fELF":
        raise ValueError("cfsolve wrapper must be a Linux ELF")
    contract = json.loads((SOURCE / "asset-manifest.json").read_text(encoding="utf-8"))
    for name, digest in contract["vendored_tosub2_files"].items():
        check_hash(SOURCE / "vendor/tosub2" / name, digest)
    check_hash(SOURCE / "licenses/cfsolve.LICENSE", contract["cfsolve_license_sha256"])
    proof_path = Path(str(args.cfsolve_bin) + ".source-verification.json")
    proof = json.loads(proof_path.read_text(encoding="utf-8"))
    if (proof.get("cfsolve_commit") != contract["cfsolve_commit"] or proof.get("source_unchanged") is not True
            or proof.get("rust_toolchain") != contract["rust_toolchain"]
            or proof.get("wrapper_source_sha256") != hashlib.sha256((SOURCE / "cfsolve/bridge.rs").read_bytes()).hexdigest()
            or proof.get("wrapper_sha256") != hashlib.sha256(binary).hexdigest()):
        raise ValueError("cfsolve wrapper source proof mismatch")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="kilo-runtime-package-") as temporary:
        target = Path(temporary) / "package"
        if system:
            target.mkdir()
            for name, binary in (("python", args.system_python), ("node", args.system_node)):
                launcher = target / name
                launcher.write_text(system_launcher(binary), encoding="utf-8")
                launcher.chmod(0o700)
            shutil.copytree(tosub2 / "node_modules", target / "tosub2/node_modules", symlinks=False, ignore=shutil.ignore_patterns("__pycache__", "*.pyc", "*.pyo"))
            for name in ("package.json", "package-lock.json"):
                if (tosub2 / name).is_file():
                    shutil.copy2(tosub2 / name, target / "tosub2" / name)
        else:
            target.mkdir()
            # A base runtime is a dependency distribution, never an archive of
            # a service working directory. Do not carry old workers, manifests,
            # release descriptors, credentials or journals into the Kilo bundle.
            for name in ("python", "node", "node.bin", "ca-certificates.crt"):
                source = args.runtime / name
                if not source.is_file():
                    raise ValueError("incomplete portable runtime: " + name)
                shutil.copy2(source, target / name)
            for name in ("python-root", "lib", "licenses"):
                source = args.runtime / name
                if not source.is_dir():
                    raise ValueError("incomplete portable runtime: " + name)
                shutil.copytree(source, target / name, symlinks=False,
                                ignore=shutil.ignore_patterns("__pycache__", "*.pyc", "*.pyo"))
            shutil.copytree(tosub2 / "node_modules", target / "tosub2/node_modules", symlinks=False)
        # Kilo imports only these pinned helpers and provides its own transport.
        # Full toSub2 CLI state and its source tree are not runtime inputs here.
        shutil.copytree(SOURCE / "vendor/tosub2", target / "tosub2", dirs_exist_ok=True)
        kilo = target / "kilo-runtime"
        kilo.mkdir()
        for name in ("runtime_service.py", "protocol.py", "tosub2_adapter.mjs", "requests_fetch.py",
                     "sentinel_curl_fetch.py", "asset-manifest.json", "requirements.txt"):
            shutil.copy2(SOURCE / name, kilo / name)
        shutil.copytree(SOURCE / "licenses", kilo / "licenses")
        assets = kilo / "sentinel_assets"
        assets.mkdir(exist_ok=True)
        for name in ASSET_HASHES:
            shutil.copy2(args.sentinel_assets / name, assets / name)
        # Replace only the transport shim; it carries the same fixed proxy via stdin.
        shutil.copy2(SOURCE / "sentinel_curl_fetch.py", assets / "sentinel_curl_fetch.py")
        (kilo / "profiles").mkdir(exist_ok=True)
        shutil.copy2(args.profile, kilo / "profiles/safari-26-ios.json")
        for name, source in answer_files.items():
            (kilo / "profiles" / name).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, kilo / "profiles" / name)
        (kilo / "bin").mkdir(exist_ok=True)
        shutil.copy2(args.cfsolve_bin, kilo / "bin/kilo-cfsolve")
        shutil.copy2(proof_path, kilo / "bin/kilo-cfsolve.source-verification.json")
        (kilo / "bin/kilo-cfsolve").chmod(0o700)
        files = {}
        for path in sorted(target.rglob("*")):
            if path.is_file():
                files[path.relative_to(target).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
        (target / "kilo-runtime-manifest.json").write_text(json.dumps({"protocol_version": 1, "runtime_mode": "system" if system else "portable",
            "tosub2_commit": contract["tosub2_commit"], "cfsolve_commit": contract["cfsolve_commit"],
            "rust_toolchain": contract["rust_toolchain"], "sentinel_assets": contract["sentinel_assets"],
            "external_dependencies": {"python": str(args.system_python), "node": str(args.system_node), "python_modules": ["requests", "socks"]} if system else {},
            "files": files}, indent=2) + "\n", encoding="utf-8")
        with tarfile.open(args.output, "w:gz", dereference=True) as archive:
            archive.add(target, arcname=".")
    print(json.dumps({"archive": str(args.output), "sha256": hashlib.sha256(args.output.read_bytes()).hexdigest(), "contains_real_credentials": False}))


if __name__ == "__main__":
    main()
