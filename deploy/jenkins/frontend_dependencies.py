#!/usr/bin/env python3
"""Trusted, opt-in dependency re-resolution for one isolated Jenkins workspace.

The container receives only the disposable source and raw evidence directory.
Original lock backups, policy evaluation and sealed metadata stay outside it.
"""
from __future__ import annotations

import datetime as dt
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys

PROFILE = "pk-frontend-reinstall-v1"
FILES = {
    "source_lock_sha256": "frontend-source-lock.yaml",
    "effective_lock_sha256": "frontend-effective-lock.yaml",
    "package_json_sha256": "frontend-package.json",
    "audit_sha256": "frontend-audit.json",
    "audit_policy_sha256": "frontend-audit-policy.yml",
    "audit_checker_sha256": "frontend-audit-checker.py",
}
REPORT = "frontend-dependencies.json"
BINDINGS = ("frontend_dependencies_sha256", "frontend_effective_lock_sha256", "frontend_audit_sha256")


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def digest(path):
    require(path.is_file() and not path.is_symlink(), "Missing regular dependency file: " + path.name)
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def write_json(path, data):
    path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def valid_digest(value):
    return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value) is not None


def declaration(directory, data):
    """Permit old packages unchanged; never downgrade opt-in dependency evidence."""
    if data.get("schema_version") == 1:
        require(not any(key in data for key in ("frontend_dependencies", *BINDINGS))
                and not any((directory / name).exists() or (directory / name).is_symlink()
                            for name in (*FILES.values(), REPORT)),
                "Dependency reinstall evidence requires manifest schema 2")
        return False
    require(data.get("schema_version") == 2, "Unsupported dependency manifest schema")
    config = data.get("frontend_dependencies")
    require(isinstance(config, dict) and config.get("schema_version") == 1
            and config.get("mode") == "reinstall", "Missing explicit dependency reinstall declaration")
    require(data.get("validation_profile") in (None, "pk-artifact-only-v1"),
            "Dependency reinstall cannot rewrite legacy sealed validation profiles")
    for key in ("source_lock_sha256", "package_json_sha256"):
        require(valid_digest(config.get(key)), "Missing original dependency digest: " + key)
    return True


def verify(directory, data):
    if not declaration(directory, data):
        return
    for key in BINDINGS:
        require(valid_digest(data.get(key)), "Missing dependency evidence binding: " + key)
    require(digest(directory / REPORT) == data["frontend_dependencies_sha256"],
            "Dependency evidence report changed")
    proof = json.loads((directory / REPORT).read_bytes())
    require(isinstance(proof, dict) and proof.get("schema_version") == 1
            and proof.get("profile") == PROFILE and proof.get("mode") == "reinstall"
            and proof.get("status") == "passed", "Dependency audit did not pass")
    for key in ("repository", "commit", "tree", "package_id"):
        require(proof.get(key) == data.get(key) and bool(data.get(key)), "Dependency source binding mismatch: " + key)
    for key, filename in FILES.items():
        require(valid_digest(proof.get(key)) and digest(directory / filename) == proof[key],
                "Dependency evidence changed: " + filename)
    for key in ("source_lock_sha256", "package_json_sha256"):
        require(proof[key] == data["frontend_dependencies"][key], "Original dependency input changed: " + key)
    require(proof["effective_lock_sha256"] == data["frontend_effective_lock_sha256"]
            and proof["audit_sha256"] == data["frontend_audit_sha256"], "Effective dependency binding mismatch")
    require(proof.get("node_image") == "node:24-bookworm-slim"
            and re.fullmatch(r"v24\.[0-9]+\.[0-9]+", str(proof.get("node_version", ""))) is not None
            and proof.get("pnpm_version") == "9.15.9"
            and proof.get("install_flags") == ["--no-frozen-lockfile", "--prefer-offline=false", "--offline=false"]
            and proof.get("audit_scope") == "production"
            and type(proof.get("audit_exit_code")) is int and proof["audit_exit_code"] in (0, 1)
            and type(proof.get("policy_exit_code")) is int and proof["policy_exit_code"] == 0,
            "Unexpected dependency installation or audit provenance")
    validate_audit(json.loads((directory / FILES["audit_sha256"]).read_bytes()))


def validate_audit(audit):
    require(isinstance(audit, dict) and not audit.get("error"), "Dependency audit request failed")
    require(isinstance(audit.get("advisories"), dict) or isinstance(audit.get("vulnerabilities"), dict),
            "Dependency audit has no vulnerability results")
    for key in ("advisories", "vulnerabilities"):
        require(key not in audit or isinstance(audit[key], dict), "Malformed dependency vulnerability results")
    metadata = audit.get("metadata")
    require(isinstance(metadata, dict), "Dependency audit has no complete vulnerability summary")
    counts = metadata.get("vulnerabilities")
    require(isinstance(counts, dict) and all(type(counts.get(name)) is int and counts[name] >= 0
            for name in ("info", "low", "moderate", "high", "critical")),
            "Dependency audit has no complete vulnerability summary")
    entries = [*(audit.get("advisories") or {}).values(), *(audit.get("vulnerabilities") or {}).values()]
    require(all(isinstance(item, dict) for item in entries), "Malformed dependency advisories")
    for severity in ("high", "critical"):
        require(not counts[severity] or any(item.get("severity") == severity for item in entries),
                "Dependency audit summary has missing advisories")


def workspace():
    root = Path.cwd().resolve(strict=True)
    source, frontend = root / "source", root / "source/frontend"
    require(source.is_dir() and not source.is_symlink() and source.resolve() == source,
            "Source must be this build's regular checkout directory")
    require(frontend.is_dir() and not frontend.is_symlink() and frontend.resolve() == frontend,
            "Frontend must be inside this build's checkout")
    return root, frontend, root / ".frontend-dependency-state", root / ".frontend-dependency-raw"


def load_manifest(root):
    path = root / "output/manifest.json"
    digest(path)
    data = json.loads(path.read_bytes())
    require(declaration(path.parent, data), "Dependency reinstallation was not requested")
    return data


def start():
    root, frontend, state, raw = workspace()
    data = load_manifest(root)
    lock, package = frontend / "pnpm-lock.yaml", frontend / "package.json"
    for path, key in ((lock, "source_lock_sha256"), (package, "package_json_sha256")):
        require(digest(path) == data["frontend_dependencies"][key], "Original dependency input changed")
        original = subprocess.run(["git", "show", data["commit"] + ":frontend/" + path.name],
                                  cwd=root / "source", check=True, stdout=subprocess.PIPE).stdout
        require(hashlib.sha256(original).hexdigest() == digest(path), "Dependency input differs from source commit")
    state.mkdir(mode=0o700)
    raw.mkdir(mode=0o700)
    backup = state / "pnpm-lock.yaml"
    shutil.copyfile(lock, backup)
    backup.chmod(0o400)
    write_json(state / "state.json", {"source_lock_sha256": digest(backup),
                                      "mode": lock.stat().st_mode & 0o777, "restored": False})
    modules = frontend / "node_modules"
    if modules.is_symlink():
        modules.unlink()
    elif modules.exists():
        require(modules.is_dir(), "Unexpected node_modules file")
        shutil.rmtree(modules)
    lock.unlink()
    require(not modules.exists() and not modules.is_symlink() and not lock.exists(),
            "Previous dependency locks or modules remain")


def audit():
    root, frontend, state, raw = workspace()
    data = load_manifest(root)
    require(digest(frontend / "package.json") == data["frontend_dependencies"]["package_json_sha256"],
            "Dependency installation modified package.json")
    require(digest(frontend / "pnpm-lock.yaml") == digest(raw / "effective-lock.yaml")
            and (frontend / "pnpm-lock.yaml").stat().st_size > 0, "Installed dependency lock differs from audit input")
    digest(raw / "audit.json")
    validate_audit(json.loads((raw / "audit.json").read_bytes()))
    for filename in ("audit-exit-code.txt", "node-version.txt", "pnpm-version.txt"):
        digest(raw / filename)
    audit_exit = int((raw / "audit-exit-code.txt").read_text().strip())
    require(audit_exit in (0, 1), "Dependency audit command failed")
    trusted = Path(__file__).resolve().parents[2]
    checker, policy = trusted / "tools/check_pnpm_audit_exceptions.py", trusted / ".github/audit-exceptions.yml"
    digest(checker)
    digest(policy)
    subprocess.run([sys.executable, "-B", str(checker), "--audit", str(raw / "audit.json"),
                    "--exceptions", str(policy)], check=True)
    output = root / "output"
    sources = {"source_lock_sha256": state / "pnpm-lock.yaml",
               "effective_lock_sha256": raw / "effective-lock.yaml",
               "package_json_sha256": frontend / "package.json", "audit_sha256": raw / "audit.json",
               "audit_policy_sha256": policy, "audit_checker_sha256": checker}
    proof = {key: data[key] for key in ("repository", "commit", "tree", "package_id")}
    proof.update(schema_version=1, profile=PROFILE, mode="reinstall", status="passed",
                 node_image="node:24-bookworm-slim", node_version=(raw / "node-version.txt").read_text().strip(),
                 pnpm_version=(raw / "pnpm-version.txt").read_text().strip(),
                 install_flags=["--no-frozen-lockfile", "--prefer-offline=false", "--offline=false"], audit_scope="production",
                 audit_exit_code=audit_exit, policy_exit_code=0, audited_at=dt.datetime.now(dt.timezone.utc).isoformat())
    for key, path in sources.items():
        proof[key] = digest(path)
        destination = output / FILES[key]
        require(not destination.exists() and not destination.is_symlink(), "Dependency evidence already exists")
        shutil.copyfile(path, destination)
    write_json(output / REPORT, proof)
    data.update(frontend_dependencies_sha256=digest(output / REPORT),
                frontend_effective_lock_sha256=proof["effective_lock_sha256"], frontend_audit_sha256=proof["audit_sha256"])
    verify(output, data)
    write_json(output / "manifest.json", data)


def restore():
    root, frontend, state, _ = workspace()
    state_file = state / "state.json"
    if not state_file.exists():
        return
    require(not state.is_symlink() and state.resolve() == state, "Invalid dependency backup directory")
    digest(state_file)
    saved = json.loads(state_file.read_bytes())
    data = load_manifest(root)
    backup, lock = state / "pnpm-lock.yaml", frontend / "pnpm-lock.yaml"
    require(digest(backup) == saved["source_lock_sha256"] == data["frontend_dependencies"]["source_lock_sha256"],
            "Original lock backup changed; refusing restoration")
    if saved.get("restored"):
        require(digest(lock) == saved["source_lock_sha256"], "Restored original lock changed")
        return
    if lock.exists() or lock.is_symlink():
        require(not lock.is_dir(), "Cannot restore lock over a directory")
        lock.unlink()
    shutil.copyfile(backup, lock)
    lock.chmod(saved["mode"])
    require(digest(lock) == saved["source_lock_sha256"], "Original lock restoration failed")
    saved["restored"] = True
    write_json(state_file, saved)


def finish():
    root, frontend, _, _ = workspace()
    data = load_manifest(root)
    verify(root / "output", data)
    require(digest(frontend / "pnpm-lock.yaml") == data["frontend_effective_lock_sha256"],
            "Frontend build changed the audited effective lock")
    require(digest(frontend / "package.json") == data["frontend_dependencies"]["package_json_sha256"],
            "Frontend build changed package.json")
    restore()


if __name__ == "__main__":
    try:
        operations = {"start": start, "audit": audit, "finish": finish, "restore": restore}
        require(len(sys.argv) == 2 and sys.argv[1] in operations, "Unknown dependency operation")
        operations[sys.argv[1]]()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print("PK frontend dependencies: " + str(error), file=sys.stderr)
        sys.exit(1)
