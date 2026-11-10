"""Operator-only startup authorization gate for new releases of this repository.

The hook is supplied by the trusted publisher environment, never by a package.
It runs over the operator's existing SSH channel; applications get no credentials
or privilege to add executable hashes. No service restart is requested here.
"""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

REPOSITORY = "yb1320057104/sub2api"
TARGET = "production-primary"
HA_TARGET = "production-ha"
MAX_REPLY = 65536


def require(ok, code):
    if not ok:
        raise RuntimeError(code)


def required(data):
    require(data.get("repository") == REPOSITORY, "startup_guard_repository_mismatch")
    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)-pk\.(\d+)", data.get("version", ""))
    require(match is not None, "startup_guard_invalid_release_version")
    return tuple(map(int, match.groups())) >= (0, 2, 19, 57)


def release_identity(directory, data):
    fields = ("repository", "version", "commit", "tree", "package_id", "binary_sha256")
    release = {key: data[key] for key in fields}
    for key, length in (("commit", 40), ("tree", 40), ("binary_sha256", 64)):
        require(re.fullmatch(r"[0-9a-f]{%d}" % length, release[key]) is not None,
                "startup_guard_invalid_release_identity")
    path = Path(directory) / "manifest.json"
    require(path.is_file() and not path.is_symlink(), "startup_guard_manifest_required")
    raw = path.read_bytes()
    require(json.loads(raw) == data, "startup_guard_manifest_changed")
    release["manifest_sha256"] = hashlib.sha256(raw).hexdigest()
    return release


def ha_receipt(reply, operation, release):
    """Admit a reviewed source release without granting runtime HA authority."""
    require(reply.get("purpose") == "ha-source-release-admission"
            and reply.get("source_admission_only") is True
            and reply.get("runtime_authorization_changed") is False
            and "host_guard_sha256" not in reply and "container_guard_sha256" not in reply,
            "ha_publication_wrong_contract")
    review = reply.get("review")
    fields = {"source_commit", "overlay_profile_sha256", "overlay_manifest_sha256",
              "migration_receipt_sha256", "migration_evidence_sha256"}
    require(isinstance(review, dict) and set(review) == fields
            and review.get("source_commit") == release["commit"], "ha_publication_review_binding_mismatch")
    evidence = review.get("migration_evidence_sha256")
    require(isinstance(evidence, dict) and set(evidence) == {"fresh", "upgrade", "rollback"},
            "ha_publication_migration_evidence_missing")
    hashes = [review.get(key) for key in fields - {"source_commit", "migration_evidence_sha256"}]
    hashes.extend(evidence.values())
    require(all(isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value) for value in hashes),
            "ha_publication_invalid_evidence_hash")
    expected = os.environ.get("PK_HA_PUBLICATION_REVIEW_SHA256", "")
    actual = hashlib.sha256(json.dumps(review, sort_keys=True, separators=(",", ":")).encode("ascii")).hexdigest()
    require(re.fullmatch(r"[0-9a-f]{64}", expected) is not None
            and actual == expected == reply.get("review_sha256"), "ha_publication_review_not_pinned")
    nodes = reply.get("nodes")
    require(isinstance(nodes, dict) and set(nodes) == {"primary", "secondary"}, "ha_publication_nodes_missing")
    writers, workers = [], []
    for node, state in nodes.items():
        require(isinstance(state, dict) and state.get("api_running") is True
                and state.get("api_ready") is True and state.get("gate_held") is False
                and type(state.get("worker_running")) is bool
                and state.get("postgres_role") in ("primary", "replica"), "ha_publication_node_not_ready")
        if state["postgres_role"] == "primary":
            writers.append(node)
        if state["worker_running"]:
            workers.append(node)
    require(len(writers) == 1 and workers == writers, "ha_publication_worker_writer_mismatch")
    epoch = reply.get("epoch_sha256", "")
    require(isinstance(epoch, str) and re.fullmatch(r"[0-9a-f]{64}", epoch)
            and reply.get("epoch_before_sha256") == epoch == reply.get("epoch_after_sha256"),
            "ha_publication_epoch_changed")
    if operation == "plan":
        require(reply.get("status") in ("ready", "admitted"), "ha_publication_plan_failed")
    else:
        record = reply.get("admission_record_sha256", "")
        require(reply.get("status") == "admitted" and reply.get("allowed") is True
                and isinstance(record, str) and re.fullmatch(r"[0-9a-f]{64}", record),
                "ha_publication_source_not_admitted")


def authorize(directory, data, operation):
    require(operation in ("plan", "prepare", "check"), "startup_guard_invalid_operation")
    if not required(data):
        return None  # Sealed historical releases keep their original contract.
    release = release_identity(directory, data)
    # This selection comes only from the trusted publisher's environment. A
    # package cannot select a weaker target or fall back after a failed gate.
    target = os.environ.get("PK_STARTUP_GUARD_TARGET", TARGET)
    require(target in (TARGET, HA_TARGET), "startup_guard_invalid_target")
    schema_version = 2 if target == HA_TARGET else 1
    try:
        argv = json.loads(os.environ.get("PK_STARTUP_GUARD_HOOK_ARGV", ""))
    except (ValueError, TypeError):
        raise RuntimeError("startup_guard_operator_hook_required") from None
    require(isinstance(argv, list) and 1 <= len(argv) <= 16
            and all(isinstance(arg, str) and arg and not any(c in arg for c in "\x00\r\n") for arg in argv)
            and Path(argv[0]).is_absolute(), "startup_guard_operator_hook_invalid")
    request = {"schema_version": schema_version, "operation": operation, "target": target, "release": release}
    try:
        process = subprocess.run(argv, input=json.dumps(request).encode("utf-8"),
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                 timeout=180, check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise RuntimeError("startup_guard_operator_hook_unavailable") from None
    # Deliberately do not print hook stderr: SSH/auth diagnostics can be private.
    require(process.returncode == 0 and 0 < len(process.stdout) <= MAX_REPLY,
            "startup_guard_authorization_failed")
    try:
        reply = json.loads(process.stdout)
    except (ValueError, UnicodeError):
        raise RuntimeError("startup_guard_invalid_receipt") from None
    require(isinstance(reply, dict) and type(reply.get("schema_version")) is int
            and reply["schema_version"] == schema_version and reply.get("target") == target
            and reply.get("operation") == operation
            and reply.get("release") == release, "startup_guard_receipt_identity_mismatch")
    require(reply.get("healthy") is True and reply.get("epoch_unchanged") is True
            and reply.get("receipt_binding_verified") is True
            and reply.get("restart_performed") is False
            and reply.get("binary_replaced") is False
            and reply.get("ha_authority_grant") is False,
            "startup_guard_receipt_not_safe")
    if target == HA_TARGET:
        ha_receipt(reply, operation, release)
        return reply
    host_hash = reply.get("host_guard_sha256", "")
    require(re.fullmatch(r"[0-9a-f]{64}", host_hash) is not None
            and reply.get("container_guard_sha256") == host_hash,
            "startup_guard_installation_not_synchronized")
    if operation == "plan":
        require(reply.get("status") in ("ready", "authorized"), "startup_guard_plan_failed")
    else:
        require(reply.get("status") == "authorized" and reply.get("allowed") is True,
                "startup_guard_candidate_not_authorized")
    return reply
