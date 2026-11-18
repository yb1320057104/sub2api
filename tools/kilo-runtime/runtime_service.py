"""Versioned local Kilo executor. Go owns jobs; this process owns short-lived jars.

Every endpoint requires the service bearer, including health/result/ack/cancel.
The only durable configuration is a bearer file and dependency paths. Passwords,
TOTP seeds and proxy credentials live only in request/process memory. Kilo-only
session material leaves through authenticated task results for Go to encrypt.
"""
from __future__ import annotations

import argparse
from collections import OrderedDict
from dataclasses import dataclass, field
import hashlib
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import secrets
import signal
import threading
import time
from typing import Any
from urllib.parse import urlsplit
import uuid

from protocol import Protocol, ProtocolConfig, ProtocolError, parse_time, timestamp

PROTOCOL_VERSION = 1
VERSION = "1.1.0"
PHASES = {"login", "resume_identity", "connect_subscription", "reconcile_subscription", "reauthorize_subscription", "approve_device"}
INITIAL_PHASES = {"login", "resume_identity", "reconcile_subscription", "reauthorize_subscription"}
TERMINAL = {"succeeded", "failed", "needs_action", "cancelled"}
ACTION_ERRORS = {"ACTION_REQUIRED", "BYOK_AUTHORIZATION_REJECTED"}
MAX_IDENTITY_MATERIAL = 64 * 1024
MAX_SOURCE_ID_TOKEN = 65536
MAX_REQUEST = 192 * 1024
SOURCE_CLIENT_ID = "app_EMoamEEZ73f0CkXaXp7hrann"
SOURCE_ISSUER = "https://auth.openai.com"
CREDENTIAL_EVIDENCE_FIELDS = {
    "version", "method", "kilo_user_id", "kilo_client_id", "source_client_id", "source_issuer",
    "source_subject", "source_workspace_id", "source_plan_type", "connection_subject",
    "provider_created_at", "provider_fingerprint", "observed_at", "kilo_password_totp_verified",
    "source_password_totp_verified", "source_snapshot_signature_verified", "source_signature_verified",
    "nonce_verified", "provider_unchanged",
}
OBSERVATION_FIELDS = {
    "version", "method", "kilo_user_id", "organization_id", "openai_email", "openai_subject",
    "connected_at", "observed_at", "provider_created_at", "provider_fingerprint",
    "connection_unchanged", "provider_unchanged", "enabled_evidence",
}
IDENTITY_ASSOCIATION_FIELDS = {
    "version", "method", "source_issuer", "source_subject", "source_workspace_id", "source_plan_type",
    "kilo_user_id", "kilo_client_id", "kilo_subject", "verified_at",
}
HANDLE_PATTERN = re.compile(r"^[a-zA-Z0-9_-]{20,128}$")
KNOWN_ERRORS = {"CANCELLED", "DEADLINE_EXCEEDED", "EXECUTOR_UNAVAILABLE", "REMOTE_UNAVAILABLE", "REMOTE_RESPONSE_INVALID",
                "REMOTE_RESPONSE_TOO_LARGE", "REMOTE_REDIRECT_REJECTED", "REMOTE_REQUEST_INVALID", "REMOTE_HTTP_ERROR",
                "REMOTE_RATE_LIMITED", "REMOTE_ACCESS_DENIED", "LOGIN_PROXY_INVALID", "LOGIN_PROXY_CONFLICT", "TURNSTILE_UNAVAILABLE", "TURNSTILE_REJECTED",
                "SENTINEL_UNAVAILABLE", "AUTH_PROTOCOL_ERROR", "AUTHORIZATION_CONTRACT_INVALID", "CALLBACK_STATE_MISMATCH",
                "AUTHORIZATION_INCOMPLETE", "AUTHORIZATION_REJECTED", "AUTH_SESSION_EXPIRED", "IDENTITY_MISMATCH", "IDENTITY_UNVERIFIED",
                "ACTION_REQUIRED", "BYOK_AUTHORIZATION_REJECTED", "LOGIN_MATERIAL_MISSING", "LOGIN_REJECTED", "MFA_REJECTED", "MFA_VERIFY_TRANSIENT", "SESSION_LOST", "SESSION_BUSY",
                "BYOK_LINK_REJECTED", "BYOK_SCOPE_MISSING", "BYOK_NOT_CONNECTED", "BYOK_ENABLE_UNVERIFIED", "DEVICE_INPUT_INVALID",
                "DEVICE_APPROVAL_FAILED", "IDENTITY_SESSION_INVALID", "REMOTE_OUTCOME_UNKNOWN", "STALE_EXECUTION", "UNSUPPORTED_TOSUB2_LAYOUT", "PHASE_UNSUPPORTED", "EXECUTOR_FAILED"}


class APIError(Exception):
    def __init__(self, status: int, code: str, retry_after: int | None = None):
        self.status, self.code, self.retry_after = status, code, retry_after
        super().__init__(code)


@dataclass
class Session:
    handle: str
    job_id: int
    revision: int
    source_email: str
    expected_identity_fingerprint: str
    proxy_fingerprint: str
    state: dict
    expires: float
    lock: threading.Lock = field(default_factory=threading.Lock)


@dataclass
class Task:
    id: str
    key: tuple
    fingerprint: str
    request: dict
    created: float
    deadline: float
    result: dict
    cancel: threading.Event = field(default_factory=threading.Event)
    process: Any = None
    finished: float = 0


def _integer(value) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and 0 < value < 2 ** 63


def _email(value) -> str:
    if not isinstance(value, str) or not 3 <= len(value) <= 320 or "@" not in value or any(ord(c) <= 32 for c in value):
        raise APIError(400, "IDENTITY_INVALID")
    return value.strip().lower()


def _complete_source_credential(credential: dict, source: str) -> bool:
    try:
        return (_email(credential.get("email")) == source
                and isinstance(credential.get("password"), str) and 1 <= len(credential["password"]) <= 4096
                and isinstance(credential.get("totp_secret"), str) and bool(credential["totp_secret"].strip())
                and len(credential["totp_secret"]) <= 256)
    except APIError:
        return False


def _evidence_time(value) -> float:
    if not isinstance(value, str) or not value:
        raise ProtocolError("IDENTITY_MISMATCH")
    try:
        return parse_time(value)
    except (ValueError, TypeError, AttributeError, OverflowError):
        raise ProtocolError("IDENTITY_MISMATCH") from None


def _credential_identity_evidence(request: dict, response: dict, owner: str, deadline: float) -> dict:
    raw = response.get("credential_identity_evidence")
    if not isinstance(raw, dict) or not CREDENTIAL_EVIDENCE_FIELDS <= raw.keys():
        raise ProtocolError("IDENTITY_MISMATCH")
    evidence = {name: raw[name] for name in CREDENTIAL_EVIDENCE_FIELDS}
    expected = request["expected_identity"]
    subject = response.get("connection_subject")
    client = evidence["kilo_client_id"]
    if (expected.get("source_plan_type") not in {"plus", "pro"}
            or expected.get("source_issuer") != SOURCE_ISSUER
            or not expected.get("source_subject") or not expected.get("source_workspace_id")
            or not request.get("source_id_token")
            or not _complete_source_credential(request["credential"], expected["source_email"])
            or response.get("connection_workspace_id") != ""
            or not isinstance(subject, str) or not re.fullmatch(r"[\x21-\x7e]{1,256}", subject)
            or not isinstance(owner, str) or not 1 <= len(owner) <= 256
            or any(ord(c) <= 32 for c in owner)
            or not isinstance(client, str) or not re.fullmatch(r"[a-zA-Z0-9_-]{1,256}", client)
            or client == SOURCE_CLIENT_ID
            or type(evidence["version"]) is not int or evidence["version"] != 1):
        raise ProtocolError("IDENTITY_MISMATCH")
    required = {
        "method": "credential_snapshot_password_totp", "kilo_user_id": owner,
        "source_client_id": SOURCE_CLIENT_ID, "source_issuer": SOURCE_ISSUER,
        "source_subject": expected["source_subject"], "source_workspace_id": expected["source_workspace_id"],
        "source_plan_type": expected["source_plan_type"], "connection_subject": subject,
    }
    if any(evidence[name] != value for name, value in required.items()):
        raise ProtocolError("IDENTITY_MISMATCH")
    if any(evidence[name] is not True for name in (
            "kilo_password_totp_verified", "source_password_totp_verified", "source_snapshot_signature_verified",
            "source_signature_verified", "nonce_verified", "provider_unchanged")):
        raise ProtocolError("IDENTITY_MISMATCH")
    fingerprint = evidence["provider_fingerprint"]
    if not isinstance(fingerprint, str) or not re.fullmatch(r"[a-fA-F0-9]{64}", fingerprint):
        raise ProtocolError("IDENTITY_MISMATCH")
    try:
        if _email(response.get("connection_email")) != expected["source_email"]:
            raise ProtocolError("IDENTITY_MISMATCH")
    except APIError:
        raise ProtocolError("IDENTITY_MISMATCH") from None
    observed = _evidence_time(evidence["observed_at"])
    connected = _evidence_time(response.get("connection_at"))
    provider_created = _evidence_time(evidence["provider_created_at"])
    # A historical connection is valid only for observation, never as fresh authorization.
    if (not deadline - 15 * 60 - 60 <= observed <= deadline + 60
            or provider_created > connected or connected > observed + 60
            or (request["phase"] in {"connect_subscription", "reauthorize_subscription"} and not deadline - 15 * 60 - 60 <= connected <= deadline + 60)):
        raise ProtocolError("IDENTITY_MISMATCH")
    return evidence


def _identity_association(request: dict, response: dict, owner: str, created: float, deadline: float, now: float) -> dict:
    raw = response.get("identity_association")
    if not isinstance(raw, dict) or not IDENTITY_ASSOCIATION_FIELDS <= raw.keys():
        raise ProtocolError("IDENTITY_MISMATCH")
    association = {name: raw[name] for name in IDENTITY_ASSOCIATION_FIELDS}
    expected = request["expected_identity"]
    subject, client = response.get("connection_subject"), association["kilo_client_id"]
    if (type(association["version"]) is not int or association["version"] != 1
            or expected.get("source_issuer") != SOURCE_ISSUER
            or expected.get("source_plan_type") not in {"plus", "pro"}
            or not expected.get("source_subject") or not expected.get("source_workspace_id")
            or request["organization_id"] != "" or response.get("organization_id", "") != ""
            or response.get("connection_workspace_id") != ""
            or not isinstance(owner, str) or not 1 <= len(owner) <= 256
            or any(ord(c) <= 32 for c in owner)
            or not isinstance(subject, str) or not re.fullmatch(r"[\x21-\x7e]{1,256}", subject)
            or not isinstance(client, str) or not re.fullmatch(r"[\x21-\x7e]{1,256}", client)):
        raise ProtocolError("IDENTITY_MISMATCH")
    required = {
        "method": "credential_snapshot", "source_issuer": SOURCE_ISSUER,
        "source_subject": expected["source_subject"], "source_workspace_id": expected["source_workspace_id"],
        "source_plan_type": expected["source_plan_type"], "kilo_user_id": owner, "kilo_subject": subject,
    }
    if any(association[name] != value for name, value in required.items()):
        raise ProtocolError("IDENTITY_MISMATCH")
    verified = _evidence_time(association["verified_at"])
    # Association proves the source pair for this phase, not a historical workspace selection.
    if not created - 60 <= verified <= min(deadline, now):
        raise ProtocolError("IDENTITY_MISMATCH")
    return association


def _connection_observation(request: dict, response: dict, evidence: dict) -> dict:
    if (response.get("byok_enabled") is not False or response.get("byok_enable_evidence") != ""
            or response.get("byok_evidence", "") != "" or response.get("device_approved", False) is not False):
        raise ProtocolError("BYOK_ENABLE_UNVERIFIED")
    raw = response.get("connection_observation")
    if not isinstance(raw, dict) or not OBSERVATION_FIELDS <= raw.keys():
        raise ProtocolError("IDENTITY_MISMATCH")
    observation = {name: raw[name] for name in OBSERVATION_FIELDS}
    required = {
        "method": "existing_delegated_connection", "kilo_user_id": evidence["kilo_user_id"],
        "organization_id": "", "openai_email": request["expected_identity"]["source_email"],
        "openai_subject": evidence["connection_subject"], "connected_at": response.get("connection_at"),
        "observed_at": evidence["observed_at"], "provider_created_at": evidence["provider_created_at"],
        "provider_fingerprint": evidence["provider_fingerprint"], "enabled_evidence": "unverified",
    }
    if (type(observation["version"]) is not int or observation["version"] != 1
            or request["organization_id"] != "" or response.get("organization_id", "") != ""
            or any(observation[name] != value for name, value in required.items())
            or observation["connection_unchanged"] is not True or observation["provider_unchanged"] is not True
            or _evidence_time(observation["connected_at"]) > _evidence_time(observation["observed_at"])):
        raise ProtocolError("IDENTITY_MISMATCH")
    return observation


def validate_request(raw: dict, now: float) -> dict:
    if not isinstance(raw, dict):
        raise APIError(400, "REQUEST_INVALID")
    if raw.get("protocol_version", PROTOCOL_VERSION) != PROTOCOL_VERSION:
        raise APIError(409, "PROTOCOL_VERSION_UNSUPPORTED")
    value = dict(raw)
    if not _integer(value.get("job_id")) or not _integer(value.get("revision")):
        raise APIError(400, "EXECUTION_ID_INVALID")
    try:
        operation = str(uuid.UUID(value.get("operation_id", "")))
        deadline = parse_time(value["deadline_at"])
    except (ValueError, TypeError, KeyError, AttributeError):
        raise APIError(400, "REQUEST_INVALID") from None
    if not now < deadline <= now + 3600:
        raise APIError(400, "DEADLINE_INVALID")
    if value.get("phase") not in PHASES:
        raise APIError(400, "PHASE_UNSUPPORTED")
    if "source_id_token" in value:
        if value["phase"] not in {"connect_subscription", "reconcile_subscription", "reauthorize_subscription"}:
            raise APIError(400, "REQUEST_FIELD_UNSUPPORTED")
        token = value["source_id_token"]
        if (not isinstance(token, str) or len(token) > MAX_SOURCE_ID_TOKEN
                or (token and not re.fullmatch(r"[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+", token))):
            raise APIError(400, "CREDENTIAL_INVALID")
    if value["phase"] in {"reconcile_subscription", "reauthorize_subscription"} and ("session_handle" in value or "device" in value):
        raise APIError(400, "REQUEST_FIELD_UNSUPPORTED")
    identity = value.get("expected_identity")
    proxy = value.get("login_proxy")
    credential = value.get("credential", {})
    if not isinstance(identity, dict) or not isinstance(proxy, dict) or not _integer(proxy.get("id")):
        raise APIError(400, "REQUEST_INVALID")
    source = _email(identity.get("source_email"))
    expected_identity = {"source_email": source}
    for name, limit in (("source_workspace_id", 128), ("source_plan_type", 64)):
        identifier = identity.get(name, "")
        if not isinstance(identifier, str) or len(identifier) > limit or (identifier and not re.fullmatch(r"[a-zA-Z0-9_-]+", identifier)):
            raise APIError(400, "IDENTITY_INVALID")
        # Missing and empty optional fields mean unknown, never a workspace fallback.
        if identifier:
            expected_identity[name] = identifier
    subject, issuer = identity.get("source_subject", ""), identity.get("source_issuer", "")
    if not isinstance(subject, str) or len(subject) > 256 or (subject and not re.fullmatch(r"[\x21-\x7e]+", subject)):
        raise APIError(400, "IDENTITY_INVALID")
    if issuer not in ("", "https://auth.openai.com") or bool(subject) != bool(issuer):
        raise APIError(400, "IDENTITY_INVALID")
    if subject:
        expected_identity.update(source_subject=subject, source_issuer=issuer)
    try:
        route = urlsplit(proxy["url"])
        valid = route.scheme in {"http", "https", "socks5", "socks5h"} and route.hostname and route.port and not route.fragment and route.path in {"", "/"} and not route.query
    except (ValueError, TypeError, KeyError):
        valid = False
    if not valid:
        raise APIError(400, "LOGIN_PROXY_INVALID")
    if not isinstance(credential, dict):
        raise APIError(400, "CREDENTIAL_INVALID")
    kilo_identity = value.get("kilo_identity")
    if kilo_identity is not None:
        if (not isinstance(kilo_identity, dict) or set(kilo_identity) - {"api_key", "user_id", "email", "session_material", "credential"}
                or not isinstance(kilo_identity.get("user_id"), str) or not 1 <= len(kilo_identity["user_id"]) <= 256
                or any(ord(c) <= 32 for c in kilo_identity["user_id"])):
            raise APIError(400, "CREDENTIAL_INVALID")
        api_key = kilo_identity.get("api_key", "")
        if (not isinstance(api_key, str) or len(api_key) > 16384
                or (not api_key and value["phase"] not in {"reconcile_subscription", "reauthorize_subscription"})
                or any(ord(c) < 33 or ord(c) > 126 for c in api_key)):
            raise APIError(400, "CREDENTIAL_INVALID")
        kilo_identity = dict(kilo_identity)
        kilo_identity["email"] = _email(kilo_identity.get("email"))
        material = kilo_identity.get("session_material", "")
        if (not isinstance(material, str) or len(material.encode("utf-8")) > MAX_IDENTITY_MATERIAL
                or (value["phase"] in {"reconcile_subscription", "reauthorize_subscription"} and not material.strip())):
            raise APIError(400, "IDENTITY_SESSION_INVALID")
        login = kilo_identity.get("credential")
        if login is not None:
            if (not isinstance(login, dict) or set(login) - {"email", "password", "totp_secret"}
                    or _email(login.get("email")) != kilo_identity["email"]
                    or not isinstance(login.get("password"), str) or not 1 <= len(login["password"]) <= 4096
                    or not isinstance(login.get("totp_secret", ""), str) or len(login.get("totp_secret", "")) > 256):
                raise APIError(400, "CREDENTIAL_INVALID")
            kilo_identity["credential"] = dict(login)
        value["kilo_identity"] = kilo_identity
    if value["phase"] in {"resume_identity", "reconcile_subscription", "reauthorize_subscription"} and kilo_identity is None:
        raise APIError(400, "LOGIN_MATERIAL_MISSING")
    if value["phase"] in {"reconcile_subscription", "reauthorize_subscription"}:
        if (expected_identity.get("source_plan_type") not in {"plus", "pro"}
                or expected_identity.get("source_issuer") != SOURCE_ISSUER
                or not expected_identity.get("source_subject") or not expected_identity.get("source_workspace_id")):
            raise APIError(400, "IDENTITY_INVALID")
        if not value.get("source_id_token") or not _complete_source_credential(credential, source):
            raise APIError(400, "LOGIN_MATERIAL_MISSING")
        if value["phase"] == "reauthorize_subscription" and kilo_identity["email"] != source:
            raise APIError(400, "IDENTITY_INVALID")
    if value["phase"] == "login":
        if _email(credential.get("email")) != source or not isinstance(credential.get("password"), str) or not 1 <= len(credential["password"]) <= 4096:
            raise APIError(400, "LOGIN_MATERIAL_MISSING")
        seed = credential.get("totp_secret", "")
        if not isinstance(seed, str) or len(seed) > 256:
            raise APIError(400, "LOGIN_MATERIAL_INVALID")
    elif value["phase"] not in INITIAL_PHASES and (not isinstance(value.get("session_handle"), str) or not HANDLE_PATTERN.fullmatch(value["session_handle"])):
        raise APIError(400, "SESSION_HANDLE_INVALID")
    if value["phase"] == "connect_subscription":
        # OpenAI normally reuses its own session; credentials can be re-supplied
        # for a fresh official challenge but are never retained between phases.
        if credential and (_email(credential.get("email")) != source or not isinstance(credential.get("password"), str)):
            raise APIError(400, "CREDENTIAL_INVALID")
    organization = value.get("organization_id", "")
    if organization:
        try:
            organization = str(uuid.UUID(organization))
        except (ValueError, TypeError, AttributeError):
            raise APIError(400, "ORGANIZATION_INVALID") from None
    if value["phase"] in {"reconcile_subscription", "reauthorize_subscription"} and organization:
        raise APIError(400, "ORGANIZATION_INVALID")
    if value["phase"] == "approve_device":
        device = value.get("device")
        if not isinstance(device, dict) or not isinstance(device.get("user_code"), str) or not 1 <= len(device["user_code"]) <= 64:
            raise APIError(400, "DEVICE_INPUT_INVALID")
        target = urlsplit(str(device.get("verification_url", "")))
        if target.scheme != "https" or target.netloc != "app.kilo.ai" or target.path != "/device-auth" or target.fragment:
            raise APIError(400, "DEVICE_INPUT_INVALID")
        # The secret code is deliberately discarded: only Go consumes it.
        value["device"] = {"user_code": device["user_code"], "verification_url": device["verification_url"]}
    value.update({"protocol_version": PROTOCOL_VERSION, "operation_id": operation,
                  "expected_identity": expected_identity, "organization_id": organization or "",
                  "credential": dict(credential)})
    # Ignore no identity, credential or arbitrary endpoint fields accidentally.
    allowed = {"protocol_version", "job_id", "revision", "operation_id", "phase", "deadline_at", "expected_identity", "login_proxy", "credential", "kilo_identity", "session_handle", "organization_id", "device", "source_id_token"}
    if set(value) - allowed:
        raise APIError(400, "REQUEST_FIELD_UNSUPPORTED")
    return value


class Runtime:
    def __init__(self, protocol, *, capacity: int = 1, max_queue: int = 64,
                 session_ttl: int = 900, result_ttl: int = 600, max_sessions: int = 128,
                 max_tasks: int = 2048, clock=time.time):
        self.protocol, self.clock = protocol, clock
        self.instance_id = str(uuid.uuid4())
        self.capacity, self.max_queue = capacity, max_queue
        self.session_ttl, self.result_ttl = session_ttl, result_ttl
        self.max_sessions, self.max_tasks = max_sessions, max_tasks
        self.tasks: OrderedDict[str, Task] = OrderedDict()
        self.operations: dict[tuple, str] = {}
        self.phase_owners: dict[tuple, str] = {}
        self.revisions: dict[int, int] = {}
        self.sessions: dict[str, Session] = {}
        self.expired: OrderedDict[str, float] = OrderedDict()
        self.fingerprint_key = secrets.token_bytes(32)
        self.lock = threading.RLock()
        self.condition = threading.Condition(self.lock)
        self.stopped = threading.Event()
        self.threads: list[threading.Thread] = []

    def start(self):
        for index in range(self.capacity):
            worker = threading.Thread(target=self._loop, name=f"kilo-executor-{index}", daemon=True)
            worker.start()
            self.threads.append(worker)
        cleaner = threading.Thread(target=self._clean_loop, name="kilo-executor-clean", daemon=True)
        cleaner.start()
        self.threads.append(cleaner)

    def stop(self):
        self.stopped.set()
        with self.condition:
            for task in list(self.tasks.values()):
                if task.result["status"] not in TERMINAL:
                    self._cancel_locked(task, "CANCELLED")
            self.condition.notify_all()
        for thread in self.threads:
            thread.join(timeout=5)
        with self.lock:
            self.sessions.clear()

    def _fingerprint(self, value) -> str:
        raw = json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":")).encode()
        return hmac.new(self.fingerprint_key, raw, hashlib.sha256).hexdigest()

    def health(self):
        dependency = self.protocol.health()
        with self.lock:
            active = sum(t.result["status"] == "running" for t in self.tasks.values())
            queued = sum(t.result["status"] == "queued" for t in self.tasks.values())
            return {"ok": dependency.get("ready", False), "protocol_version": PROTOCOL_VERSION, "version": VERSION,
                    "instance_id": self.instance_id, "capabilities": sorted(PHASES | {"cancel", "ack", "fixed_proxy", "session_ttl", "workspace_identity", "identity_session", "identity_rebind_guard", "credential_snapshot", "existing_subscription_observation"}),
                    "max_concurrency": self.capacity, "running": active, "queue": queued,
                    "available_capacity": max(0, self.capacity - active), "available_queue": max(0, self.max_queue - queued),
                    "session_ttl_seconds": self.session_ttl, "result_ttl_seconds": self.result_ttl, "dependencies": dependency}

    def submit(self, raw: dict):
        now = self.clock()
        request = validate_request(raw, now)
        key = (request["job_id"], request["revision"], request["phase"], request["operation_id"])
        fingerprint = self._fingerprint(request)
        with self.condition:
            self._cleanup_locked(now)
            if key in self.operations:
                task = self.tasks.get(self.operations[key])
                if task is None:
                    raise APIError(410, "RESULT_EXPIRED")
                if task.fingerprint != fingerprint:
                    raise APIError(409, "OPERATION_ID_CONFLICT")
                return dict(task.result)
            job, revision, phase, _ = key
            current = self.revisions.get(job, 0)
            if revision < current:
                raise APIError(409, "STALE_EXECUTION")
            phase_key = (job, revision, phase)
            previous = self.tasks.get(self.phase_owners.get(phase_key, ""))
            if previous and previous.result["status"] not in TERMINAL:
                raise APIError(409, "PHASE_ALREADY_RUNNING")
            if len(self.tasks) >= self.max_tasks or sum(t.result["status"] == "queued" for t in self.tasks.values()) >= self.max_queue:
                raise APIError(429, "EXECUTOR_BUSY", 3)
            if not self.protocol.health().get("ready"):
                raise APIError(503, "EXECUTOR_UNAVAILABLE", 10)
            if revision > current:
                for task in list(self.tasks.values()):
                    if task.key[0] == job and task.key[1] < revision and task.result["status"] not in TERMINAL:
                        self._cancel_locked(task, "STALE_EXECUTION")
                for handle, session in list(self.sessions.items()):
                    if session.job_id == job and session.revision < revision:
                        self.sessions.pop(handle, None)
            self.revisions[job] = revision
            if phase not in INITIAL_PHASES:
                self._session_locked(request)
            elif len(self.sessions) >= self.max_sessions:
                raise APIError(429, "SESSION_CAPACITY", 10)
            task_id = secrets.token_urlsafe(24)
            result = {"task_id": task_id, "instance_id": self.instance_id, "job_id": job,
                      "revision": revision, "phase": phase, "status": "queued", "expires_at": timestamp(parse_time(request["deadline_at"]))}
            task = Task(task_id, key, fingerprint, request, now, parse_time(request["deadline_at"]), result)
            self.tasks[task_id] = task
            self.operations[key] = task_id
            self.phase_owners[phase_key] = task_id
            self.condition.notify()
            return dict(result)

    def _session_locked(self, request) -> Session:
        session = self.sessions.get(request.get("session_handle", ""))
        if session is None or session.expires <= self.clock():
            raise APIError(410, "SESSION_LOST")
        if ((session.job_id, session.revision, session.source_email) != (request["job_id"], request["revision"], request["expected_identity"]["source_email"])
                or session.expected_identity_fingerprint != self._fingerprint(request["expected_identity"])):
            raise APIError(409, "SESSION_OWNERSHIP_MISMATCH")
        if request.get("kilo_identity") and request["kilo_identity"]["user_id"] != session.state["verified_identity"]["kilo_user_id"]:
            raise APIError(409, "SESSION_OWNERSHIP_MISMATCH")
        if session.proxy_fingerprint != self._fingerprint(request["login_proxy"]):
            raise APIError(409, "LOGIN_PROXY_CONFLICT")
        return session

    def result(self, task_id):
        with self.lock:
            self._cleanup_locked(self.clock())
            task = self.tasks.get(task_id)
            if task is None:
                raise APIError(410 if task_id in self.expired else 404, "RESULT_EXPIRED" if task_id in self.expired else "TASK_NOT_FOUND")
            return dict(task.result)

    def cancel(self, task_id):
        with self.condition:
            task = self.tasks.get(task_id)
            if task is None:
                raise APIError(404, "TASK_NOT_FOUND")
            if task.result["status"] not in TERMINAL:
                self._cancel_locked(task, "CANCELLED")
            self.condition.notify_all()
            return dict(task.result)

    def acknowledge(self, task_id):
        with self.lock:
            task = self.tasks.get(task_id)
            if task is None:
                return {"acknowledged": True}
            if task.result["status"] not in TERMINAL:
                raise APIError(409, "TASK_NOT_FINISHED")
            self._remove_locked(task)
            return {"acknowledged": True}

    def close_session(self, handle):
        with self.condition:
            for task in list(self.tasks.values()):
                if task.request.get("session_handle") == handle and task.result["status"] not in TERMINAL:
                    self._cancel_locked(task, "CANCELLED")
            self.sessions.pop(handle, None)
            return {"closed": True}

    def _cancel_locked(self, task, code):
        task.cancel.set()
        Protocol.stop(task.process)
        handle = task.request.get("session_handle")
        if handle:
            self.sessions.pop(handle, None)
        task.result.update(status="cancelled", error_code=code, retryable=False, next_action="从后端最新执行代次恢复")
        task.request.clear()
        task.finished = self.clock()

    def _remove_locked(self, task):
        self.tasks.pop(task.id, None)
        self.expired[task.id] = self.clock() + self.result_ttl
        task.request.clear()
        task.result.clear()
        # Keep only bounded operation tombstones for the result TTL, so retrying
        # an acknowledged operation cannot silently submit the same side effect.
        while len(self.expired) > self.max_tasks:
            self.expired.popitem(last=False)

    def _cleanup_locked(self, now):
        for task in list(self.tasks.values()):
            if task.result["status"] not in TERMINAL and now >= task.deadline:
                self._cancel_locked(task, "DEADLINE_EXCEEDED")
            elif task.finished and now - task.finished >= self.result_ttl:
                self._remove_locked(task)
        for handle, session in list(self.sessions.items()):
            if session.expires <= now:
                self.sessions.pop(handle, None)
        for task_id, expires in list(self.expired.items()):
            if expires <= now:
                self.expired.pop(task_id, None)
        for key, task_id in list(self.operations.items()):
            if task_id not in self.tasks and task_id not in self.expired:
                self.operations.pop(key, None)
        for key, task_id in list(self.phase_owners.items()):
            if task_id not in self.tasks:
                self.phase_owners.pop(key, None)
        live_jobs = {t.key[0] for t in self.tasks.values()} | {s.job_id for s in self.sessions.values()}
        for job in list(self.revisions):
            if job not in live_jobs and not any(key[0] == job for key in self.operations):
                self.revisions.pop(job, None)

    def _clean_loop(self):
        while not self.stopped.wait(1):
            with self.lock:
                self._cleanup_locked(self.clock())

    def _loop(self):
        while not self.stopped.is_set():
            with self.condition:
                pending = next((task for task in self.tasks.values() if task.result["status"] == "queued"), None)
                if pending is None:
                    self.condition.wait(1)
                    continue
                pending.result["status"] = "running"
            self._execute(pending)

    def _execute(self, task):
        session = None
        session_acquired = False
        request = None
        try:
            with self.lock:
                if task.cancel.is_set() or task.result["status"] == "cancelled":
                    return
                request = dict(task.request)
                if request["phase"] not in INITIAL_PHASES:
                    session = self._session_locked(request)
                    if not session.lock.acquire(blocking=False):
                        raise ProtocolError("SESSION_BUSY", True)
                    session_acquired = True
            def register(child):
                with self.lock:
                    task.process = child
                    if child is not None and task.cancel.is_set():
                        Protocol.stop(child)
            response = self.protocol.execute(request, session.state if session else None, task.cancel, register)
            with self.lock:
                if task.cancel.is_set() or self.clock() >= task.deadline or self.revisions.get(task.key[0]) != task.key[1] or task.id not in self.tasks:
                    return
                identity = response.get("verified_identity", {})
                if not isinstance(identity, dict) or not identity.get("kilo_user_id"):
                    raise ProtocolError("IDENTITY_MISMATCH")
                if request["phase"] != "resume_identity" and _email(identity.get("openai_email")) != request["expected_identity"]["source_email"]:
                    raise ProtocolError("IDENTITY_MISMATCH")
                if request.get("kilo_identity") and identity["kilo_user_id"] != request["kilo_identity"]["user_id"]:
                    raise ProtocolError("IDENTITY_MISMATCH")
                material = response.get("identity_session_material")
                if not isinstance(material, str) or not material or len(material.encode("utf-8")) > MAX_IDENTITY_MATERIAL:
                    raise ProtocolError("REMOTE_RESPONSE_INVALID")
                if request["phase"] in {"connect_subscription", "reconcile_subscription", "reauthorize_subscription"}:
                    expected = request["expected_identity"]
                    evidence = response.get("connection_identity_evidence")
                    if request["phase"] == "reauthorize_subscription" and (response.get("byok_enabled") is not True or response.get("byok_enable_evidence") != "fresh_delegated_authorization"):
                        raise ProtocolError("BYOK_ENABLE_UNVERIFIED")
                    if evidence == "credential_snapshot":
                        response["identity_association"] = _identity_association(
                            request, response, identity["kilo_user_id"], task.created, task.deadline, self.clock())
                        if "credential_identity_evidence" in response or request["phase"] == "reconcile_subscription":
                            response["credential_identity_evidence"] = _credential_identity_evidence(request, response, identity["kilo_user_id"], task.deadline)
                    elif (request["phase"] == "reconcile_subscription"
                          or "credential_identity_evidence" in response or "identity_association" in response):
                        raise ProtocolError("IDENTITY_MISMATCH")
                    elif evidence == "personal_subject":
                        if (expected.get("source_plan_type") not in {"plus", "pro"}
                                or expected.get("source_issuer") != SOURCE_ISSUER
                                or not expected.get("source_subject")
                                or response.get("connection_subject") != expected["source_subject"]
                                or response.get("connection_workspace_id")):
                            raise ProtocolError("IDENTITY_MISMATCH")
                    elif expected.get("source_workspace_id") and response.get("connection_workspace_id") != expected["source_workspace_id"]:
                        raise ProtocolError("IDENTITY_MISMATCH")
                    if (response.get("byok_enable_evidence") == "existing_connection_enablement_unknown"
                            and (response.get("byok_enabled") is not False or response.get("byok_evidence", "") != ""
                                 or response.get("device_approved", False) is not False)):
                        raise ProtocolError("BYOK_ENABLE_UNVERIFIED")
                    if request["phase"] == "reconcile_subscription":
                        response["connection_observation"] = _connection_observation(request, response, response["credential_identity_evidence"])
                    elif "connection_observation" in response:
                        raise ProtocolError("IDENTITY_MISMATCH")
                else:
                    if "identity_association" in response:
                        raise ProtocolError("IDENTITY_MISMATCH")
                    response.pop("credential_identity_evidence", None)
                    response.pop("connection_observation", None)
                if session and self.sessions.get(session.handle) is not session:
                    raise ProtocolError("SESSION_LOST")
                if session and identity["kilo_user_id"] != session.state["verified_identity"]["kilo_user_id"]:
                    raise ProtocolError("IDENTITY_MISMATCH")
                state = response.pop("private_state", None)
                if not isinstance(state, dict) or not isinstance(state.get("cookies"), list):
                    raise ProtocolError("REMOTE_RESPONSE_INVALID")
                # Signed source tickets and operation receipts never become resumable Kilo state.
                state = {key: value for key, value in state.items() if key not in
                         {"source_id_token", "identity_association", "credential_identity_evidence", "connection_observation"}}
                expires = self.clock() + self.session_ttl
                try:
                    expires = min(expires, parse_time(response.get("expires_at", "")))
                except (ValueError, TypeError, AttributeError):
                    pass
                if expires <= self.clock():
                    raise ProtocolError("AUTH_SESSION_EXPIRED")
                if session:
                    session.state, session.expires = state, expires
                else:
                    handle = secrets.token_urlsafe(32)
                    session = Session(handle=handle, job_id=task.key[0], revision=task.key[1],
                                      source_email=request["expected_identity"]["source_email"],
                                      expected_identity_fingerprint=self._fingerprint(request["expected_identity"]),
                                      proxy_fingerprint=self._fingerprint(request["login_proxy"]), state=state, expires=expires)
                    self.sessions[handle] = session
                # Explicit projection prevents cookies/keys/raw server data from
                # escaping through a future protocol result field.
                allowed = {key: response[key] for key in {"verified_identity", "identity_session_material", "byok_enabled", "byok_enable_evidence", "byok_evidence", "connection_email", "connection_workspace_id", "connection_identity_evidence", "connection_subject", "connection_at", "organization_id", "device_approved", "identity_association", "credential_identity_evidence", "connection_observation"} if key in response}
                task.result.update(allowed, status="succeeded", session_handle=session.handle, expires_at=timestamp(expires), retryable=False)
                task.finished = self.clock()
        except (ProtocolError, APIError) as error:
            with self.lock:
                if not task.cancel.is_set() and task.id in self.tasks:
                    code = error.code if error.code in KNOWN_ERRORS else "AUTH_PROTOCOL_ERROR"
                    task.result.update(status="needs_action" if code in ACTION_ERRORS else "failed", error_code=code,
                                       retryable=code not in ACTION_ERRORS and bool(getattr(error, "retryable", False)), next_action=str(getattr(error, "next_action", ""))[:300])
                    task.finished = self.clock()
        except Exception:
            with self.lock:
                if not task.cancel.is_set() and task.id in self.tasks:
                    task.result.update(status="failed", error_code="AUTH_PROTOCOL_ERROR", retryable=False, next_action="检查执行器版本和依赖")
                    task.finished = self.clock()
        finally:
            with self.lock:
                task.request.clear()
                task.process = None
            if request is not None:
                request.clear()
            if session is not None and session_acquired:
                try:
                    session.lock.release()
                except RuntimeError:
                    pass


class Server(ThreadingHTTPServer):
    daemon_threads = True
    def __init__(self, address, runtime: Runtime, token: str):
        self.runtime, self.token = runtime, token
        super().__init__(address, Handler)


class Handler(BaseHTTPRequestHandler):
    server_version = "KiloRuntime/" + VERSION
    def log_message(self, *args):
        pass

    def _json(self, status, value, retry=None):
        body = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        if retry:
            self.send_header("Retry-After", str(retry))
        self.end_headers()
        self.wfile.write(body)

    def _dispatch(self):
        expected = "Bearer " + self.server.token
        if not hmac.compare_digest(self.headers.get("Authorization", ""), expected):
            self._json(401, {"error_code": "UNAUTHORIZED"})
            return
        if urlsplit(self.path).query or "%" in self.path:
            raise APIError(400, "PATH_INVALID")
        path = self.path.rstrip("/")
        runtime = self.server.runtime
        if self.command == "GET" and path in {"/v1/health", "/health"}:
            self._json(200, runtime.health())
            return
        if self.command == "POST" and path == "/v1/tasks":
            try:
                length = int(self.headers.get("Content-Length", "0"))
                if not 1 <= length <= MAX_REQUEST or self.headers.get("Transfer-Encoding"):
                    raise APIError(413, "REQUEST_TOO_LARGE")
                payload = json.loads(self.rfile.read(length))
            except (ValueError, UnicodeDecodeError):
                raise APIError(400, "REQUEST_INVALID") from None
            self._json(202, runtime.submit(payload))
            return
        parts = path.split("/")
        if len(parts) in (4, 5) and parts[1:3] == ["v1", "tasks"] and HANDLE_PATTERN.fullmatch(parts[3]):
            task_id = parts[3]
            if self.command == "GET" and len(parts) == 4:
                result = runtime.result(task_id)
                self._json(202 if result["status"] in {"queued", "running"} else 200, result)
                return
            if self.command == "POST" and len(parts) == 5 and parts[4] in {"ack", "cancel"}:
                self._json(200, runtime.acknowledge(task_id) if parts[4] == "ack" else runtime.cancel(task_id))
                return
        if self.command == "DELETE" and len(parts) == 4 and parts[1:3] == ["v1", "sessions"] and HANDLE_PATTERN.fullmatch(parts[3]):
            self._json(200, runtime.close_session(parts[3]))
            return
        if path == "/login" or path.startswith("/result/"):
            self._json(410, {"error_code": "LEGACY_API_REPLACED", "next_action": "改用/v1/tasks；旧结果不含BYOK或设备取钥能力"})
            return
        raise APIError(404, "NOT_FOUND")

    def _handle(self):
        self.connection.settimeout(10)
        self.close_connection = True
        try:
            self._dispatch()
        except APIError as error:
            self._json(error.status, {"error_code": error.code}, error.retry_after)
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            pass
        except Exception:
            self._json(500, {"error_code": "EXECUTOR_INTERNAL_ERROR"})

    do_GET = _handle
    do_POST = _handle
    do_DELETE = _handle


def load_token() -> str:
    path = os.getenv("KILO_SERVICE_TOKEN_FILE", "")
    token = Path(path).read_text(encoding="utf-8").strip() if path else os.getenv("KILO_SERVICE_TOKEN", "").strip()
    if len(token) < 32 or any(ord(c) < 33 or ord(c) > 126 for c in token):
        raise ValueError("KILO_SERVICE_TOKEN_FILE must contain at least 32 printable nonspace characters")
    return token


def main() -> int:
    parser = argparse.ArgumentParser(description="Kilo pure-HTTP phase executor")
    parser.add_argument("--check", action="store_true", help="check dependencies without login or network")
    args = parser.parse_args()
    protocol = Protocol(ProtocolConfig.from_env())
    if args.check:
        health = protocol.health()
        print(json.dumps(health))
        return 0 if health["ready"] else 2
    token = load_token()
    runtime = Runtime(protocol, capacity=max(1, min(4, int(os.getenv("KILO_CONCURRENCY", "1")))))
    server = Server((os.getenv("KILO_BIND", "127.0.0.1"), int(os.getenv("KILO_PORT", "8099"))), runtime, token)
    runtime.start()
    def stop(*_):
        threading.Thread(target=server.shutdown, daemon=True).start()
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        server.serve_forever(poll_interval=0.2)
    finally:
        server.server_close()
        runtime.stop()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
