"""Bind the separate credential runtime to the same reviewed PK release."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import re
import subprocess

REPOSITORY = "yb1320057104/sub2api"
DESCRIPTOR = "reauth-runtime.json"
EVIDENCE = "reauth-runtime-validation.json"
CHECKS = {"portable_alpine", "portable_ubuntu", "worker_self_check",
          "isolated_excel_authorization", "graceful_shutdown"}
REQUIRED_INPUTS = {
    "tools/openai_oauth_reauth_worker.py", "tools/openai_totp_rotation.py",
    "tools/openai_totp_journal.mjs", "tools/openai_codex_oauth_adapter.py",
    "tools/openai_codex_diagnostics.mjs", "tools/openai_codex_password_flow.mjs",
    "tools/test_openai_codex_oauth_adapter.py", "tools/test_openai_codex_diagnostics.mjs",
    "tools/test_openai_codex_password_flow.mjs", "tools/fixtures/codex-http-transport.mjs",
    "tools/openai_excel_oauth_adapter.py",
    "tools/openai_excel_2fa_login.py", "tools/openai_excel_password_flow.mjs",
    "tools/openai_siwc_oauth_adapter.py", "tools/openai_siwc_password_flow.mjs",
    "tools/reauth-runtime/Dockerfile", "tools/reauth-runtime/package.py",
    "tools/reauth-runtime/package.json", "tools/reauth-runtime/package-lock.json",
    "tools/reauth-runtime/requirements.txt", "tools/reauth-runtime/smoke.mjs",
}


def need(value, reason):
    if not value:
        raise RuntimeError("credential_runtime_" + reason)


def required(version):
    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)-pk\.(\d+)", str(version))
    return bool(match and tuple(map(int, match.groups())) >= (0, 2, 19, 66))


def sha(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def regular(root, name):
    need(isinstance(name, str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", name), "invalid_filename")
    path = root / name
    need(path.is_file() and not path.is_symlink() and path.resolve().parent == root.resolve(), "missing_file")
    return path


def binding(value, version):
    need(isinstance(value, dict), "binding_missing")
    need(value.get("version") == version and value.get("architecture") == "amd64", "version_or_architecture")
    need(value.get("archive") == "sub2api-reauth_" + version + "_linux_amd64.tar.gz", "archive_name")
    need(re.fullmatch(r"[a-f0-9]{64}", str(value.get("sha256", ""))), "digest_missing")
    return value


def linker_flags(value, version):
    value = binding(value, version)
    prefix = "github.com/Wei-Shaw/sub2api/internal/reauthruntime."
    return " ".join("-X " + prefix + key + "=" + val for key, val in (
        ("ReleaseVersion", version), ("ArchiveSHA256", value["sha256"]),
        ("ArchiveArchitecture", value["architecture"])))


def assets(root, manifest, checkout):
    value = manifest.get("reauth_runtime")
    if value is None and not required(manifest.get("version")):
        return []
    value = binding(value, manifest["version"])
    archive = regular(root, value["archive"])
    need(0 < archive.stat().st_size <= 256 * 1024 * 1024 and sha(archive) == value["sha256"], "archive_changed")
    descriptor = json.loads(regular(root, DESCRIPTOR).read_bytes())
    proof = json.loads(regular(root, EVIDENCE).read_bytes())
    for document in (descriptor, proof):
        need(document.get("schema") == 1 and document.get("repository") == REPOSITORY, "document_identity")
        need(document.get("source_commit") == manifest["commit"] and document.get("source_tree") == manifest["tree"], "source_mismatch")
        need(document.get("runtime") == value, "document_binding")
    inputs = descriptor.get("source_files")
    need(isinstance(inputs, dict) and REQUIRED_INPUTS <= inputs.keys(), "source_inventory")
    for name, expected in inputs.items():
        need(isinstance(name, str) and name.startswith("tools/") and ".." not in name.split("/")
             and "\\" not in name and re.fullmatch(r"[a-f0-9]{64}", str(expected)), "source_path")
        raw = subprocess.check_output(["git", "-C", str(checkout), "cat-file", "blob", manifest["commit"] + ":" + name])
        need(hashlib.sha256(raw).hexdigest() == expected, "source_changed")
    need(proof.get("binary_sha256") == manifest["binary_sha256"], "binary_mismatch")
    checks = proof.get("checks")
    need(isinstance(checks, dict) and CHECKS <= checks.keys() and all(checks[k] is True for k in CHECKS), "verification_incomplete")
    need(proof.get("isolated") is True and proof.get("production_credentials_used") is False, "verification_scope")
    return [value["archive"], DESCRIPTOR, EVIDENCE]
