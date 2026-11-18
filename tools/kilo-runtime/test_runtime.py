"""Offline lifecycle/API tests; no dependency contacts Kilo or OpenAI."""
from __future__ import annotations

from datetime import datetime, timedelta, timezone
import http.client
import json
import sys
import threading
import time
import unittest
import uuid
from pathlib import Path
from unittest.mock import patch

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from protocol import ProtocolError, timestamp, parse_time
from runtime_service import Runtime, Server, APIError, validate_request, CREDENTIAL_EVIDENCE_FIELDS, OBSERVATION_FIELDS, IDENTITY_ASSOCIATION_FIELDS


class FakeProtocol:
    def __init__(self):
        self.started = threading.Event()
        self.release = threading.Event()
        self.block = False
        self.calls = []
        self.identities = []
    def health(self):
        return {"ready": True}
    def execute(self, request, state, cancel, register):
        self.calls.append(request["phase"])
        self.identities.append(dict(request["expected_identity"]))
        self.started.set()
        if self.block:
            self.release.wait(2)
        if cancel.is_set():
            raise ProtocolError("CANCELLED")
        identity = {"kilo_user_id": request.get("kilo_identity", {}).get("user_id", "kilo-owned-1"),
                    "openai_email": "" if request["phase"] == "resume_identity" else request["expected_identity"]["source_email"]}
        material = json.dumps({"version": 1, "kilo_user_id": identity["kilo_user_id"],
                               "expires_at": timestamp(time.time() + 86400), "cookies": []})
        return {"verified_identity": identity, "private_state": {"cookies": [], "verified_identity": identity},
                "identity_session_material": material,
                "expires_at": timestamp(time.time() + 86400), "api_key": "must-not-escape",
                **({"byok_enabled": True, "connection_email": identity["openai_email"],
                    "connection_workspace_id": request["expected_identity"].get("source_workspace_id", "")}
                   if request["phase"] == "connect_subscription" else {})}


def task(phase="login", **overrides):
    value = {"job_id": 42, "revision": 1, "operation_id": str(uuid.uuid4()), "phase": phase,
             "deadline_at": timestamp(time.time() + 60), "expected_identity": {"source_email": "owner@example.test"},
             "login_proxy": {"id": 7, "url": "socks5h://u:password@127.0.0.1:1080"},
             "credential": {"email": "owner@example.test", "password": "private-password", "totp_secret": "JBSWY3DPEHPK3PXP"}}
    value.update(overrides)
    return value


def personal_identity():
    return {"source_email": "owner@example.test", "source_issuer": "https://auth.openai.com",
            "source_subject": "auth0|original-source", "source_workspace_id": "personal-space", "source_plan_type": "plus"}


def reconcile_task(**overrides):
    return task("reconcile_subscription", **{
        "expected_identity": personal_identity(), "source_id_token": "synthetic.source.ticket",
        "kilo_identity": {"user_id": "kilo-owned-1", "email": "owner@example.test", "session_material": "synthetic-kilo-session"},
        **overrides,
    })


def reauthorize_task(**overrides):
    request = reconcile_task()
    request.update(phase="reauthorize_subscription")
    request.update(overrides)
    return request


def snapshot_response(request, response):
    expected = request["expected_identity"]
    deadline = parse_time(request["deadline_at"])
    observed = timestamp(deadline - 30)
    connected = timestamp(deadline - (86400 if request["phase"] == "reconcile_subscription" else 45))
    provider_created = timestamp(deadline - 2 * 86400)
    evidence = {
        "version": 1, "method": "credential_snapshot_password_totp", "kilo_user_id": response["verified_identity"]["kilo_user_id"],
        "kilo_client_id": "app_synthetic_kilo_client", "source_client_id": "app_EMoamEEZ73f0CkXaXp7hrann",
        "source_issuer": expected["source_issuer"], "source_subject": expected["source_subject"],
        "source_workspace_id": expected["source_workspace_id"], "source_plan_type": expected["source_plan_type"],
        "connection_subject": "auth0|different-kilo-client-subject", "provider_created_at": provider_created,
        "provider_fingerprint": "a" * 64, "observed_at": observed, "kilo_password_totp_verified": True,
        "source_password_totp_verified": True, "source_snapshot_signature_verified": True,
        "source_signature_verified": True, "nonce_verified": True, "provider_unchanged": True,
    }
    response.update(connection_identity_evidence="credential_snapshot", credential_identity_evidence=evidence,
                    identity_association={
                        "version": 1, "method": "credential_snapshot", "source_issuer": expected["source_issuer"],
                        "source_subject": expected["source_subject"], "source_workspace_id": expected["source_workspace_id"],
                        "source_plan_type": expected["source_plan_type"], "kilo_user_id": evidence["kilo_user_id"],
                        "kilo_client_id": evidence["kilo_client_id"], "kilo_subject": evidence["connection_subject"],
                        "verified_at": timestamp(time.time()),
                    },
                    connection_email=expected["source_email"], connection_subject=evidence["connection_subject"],
                    connection_workspace_id="", connection_at=connected, organization_id="")
    if request["phase"] == "reconcile_subscription":
        response.update(byok_enabled=False, byok_enable_evidence="", connection_observation={
            "version": 1, "method": "existing_delegated_connection", "kilo_user_id": evidence["kilo_user_id"],
            "organization_id": "", "openai_email": expected["source_email"], "openai_subject": evidence["connection_subject"],
            "connected_at": connected, "observed_at": observed, "provider_created_at": provider_created,
            "provider_fingerprint": evidence["provider_fingerprint"], "connection_unchanged": True,
            "provider_unchanged": True, "enabled_evidence": "unverified",
        })
    return response


class RequestValidationTests(unittest.TestCase):
    def test_expected_identity_preserves_bounded_identifiers(self):
        for fields in ({"source_workspace_id": "26784415-3acf-4a79-b6e0-964cd85a77e4", "source_plan_type": "chatgpt_business"},
                       {"source_workspace_id": "tenant_123-Alpha", "source_plan_type": "chatgpt_team"},
                       {"source_workspace_id": "w" * 128, "source_plan_type": "p" * 64},
                       {"source_workspace_id": "w"}, {"source_plan_type": "p"}):
            with self.subTest(fields=fields):
                identity = {"source_email": "Owner@EXAMPLE.test", **fields}
                request = validate_request(task(expected_identity=identity), time.time())
                self.assertEqual({"source_email": "owner@example.test", **fields}, request["expected_identity"])
                self.assertEqual("Owner@EXAMPLE.test", identity["source_email"])

    def test_expected_identity_normalizes_absent_and_empty_optional_fields(self):
        for fields in ({}, {"source_workspace_id": ""}, {"source_plan_type": ""},
                       {"source_workspace_id": "", "source_plan_type": ""}):
            with self.subTest(fields=fields):
                request = validate_request(task(expected_identity={"source_email": "owner@example.test", **fields}), time.time())
                self.assertEqual({"source_email": "owner@example.test"}, request["expected_identity"])

    def test_expected_identity_rejects_invalid_identifiers(self):
        for field, limit in (("source_workspace_id", 128), ("source_plan_type", 64)):
            for value in (None, True, 123, [], {}, "x" * (limit + 1), "workspace space", "workspace\n", "\tworkspace",
                          "workspace/path", "workspace\\path", "workspace.example", "工作区", "workspace\x00"):
                with self.subTest(field=field, value=value):
                    with self.assertRaises(APIError) as caught:
                        validate_request(task(expected_identity={"source_email": "owner@example.test", field: value}), time.time())
                    self.assertEqual(400, caught.exception.status)
                    self.assertEqual("IDENTITY_INVALID", caught.exception.code)


    def test_resume_accepts_independent_kilo_login_snapshot(self):
        identity = {"api_key": "kilo-private-key", "user_id": "kilo-owned-1", "email": "original@example.test",
                    "credential": {"email": "original@example.test", "password": "original-private", "totp_secret": "seed"}}
        request = validate_request(task("resume_identity", kilo_identity=identity), time.time())
        self.assertEqual("owner@example.test", request["credential"]["email"])
        self.assertEqual("original@example.test", request["kilo_identity"]["credential"]["email"])
        for changed in ({**identity, "session_material": "x" * (65536 + 1)},
                        {**identity, "credential": {"email": "owner@example.test", "password": "source-private"}},
                        {**identity, "api_key": "private\nkey"}):
            with self.subTest(fields=sorted(changed)):
                with self.assertRaises(APIError):
                    validate_request(task("resume_identity", kilo_identity=changed), time.time())


    def test_source_id_token_is_private_bounded_and_phase_limited(self):
        for phase in ("connect_subscription", "reconcile_subscription"):
            request = task(phase, session_handle="s" * 32) if phase == "connect_subscription" else reconcile_task()
            for token in ("synthetic.source.ticket", "x" * 65532 + ".b.c"):
                with self.subTest(phase=phase, size=len(token)):
                    self.assertEqual(token, validate_request({**request, "source_id_token": token}, time.time())["source_id_token"])
            for token in (None, False, 1, {}, [], " \t", "x" * 65537, "not-a-jwt", "a..c", ".b.c", "a.b.", "a.b.c.d", "a.b=.c"):
                with self.subTest(phase=phase, token_type=type(token).__name__):
                    with self.assertRaises(APIError) as caught:
                        validate_request({**request, "source_id_token": token}, time.time())
                    self.assertEqual("CREDENTIAL_INVALID", caught.exception.code)
        for phase in ("login", "resume_identity", "approve_device"):
            with self.subTest(phase=phase), self.assertRaises(APIError) as caught:
                validate_request(task(phase, source_id_token="synthetic.source.ticket"), time.time())
            self.assertEqual("REQUEST_FIELD_UNSUPPORTED", caught.exception.code)
        validate_request(task("connect_subscription", session_handle="s" * 32), time.time())
        self.assertEqual("", validate_request(task("connect_subscription", session_handle="s" * 32, source_id_token=""), time.time())["source_id_token"])

    def test_reconcile_requires_personal_source_and_complete_snapshot_without_key(self):
        request = reconcile_task()
        for fields in ({}, {"api_key": ""}, {"api_key": "synthetic-kilo-key"}):
            with self.subTest(key_fields=fields):
                accepted = validate_request({**request, "kilo_identity": {**request["kilo_identity"], **fields}}, time.time())
                self.assertNotIn("session_handle", accepted)
        for key in (None, ""):
            identity = {**request["kilo_identity"]}
            if key is not None: identity["api_key"] = key
            with self.subTest(resume_key=key), self.assertRaises(APIError):
                validate_request(task("resume_identity", kilo_identity=identity), time.time())
        invalid = [
            {"source_id_token": None}, {"source_id_token": ""}, {"kilo_identity": None},
            {"kilo_identity": {**request["kilo_identity"], "session_material": ""}},
            {"kilo_identity": {**request["kilo_identity"], "user_id": ""}},
            {"kilo_identity": {**request["kilo_identity"], "email": ""}},
            {"kilo_identity": {**request["kilo_identity"], "api_key": "bad\nkey"}},
            {"credential": {}}, {"credential": {**request["credential"], "email": "other@example.test"}},
            {"credential": {**request["credential"], "password": ""}},
            {"credential": {**request["credential"], "totp_secret": ""}},
            {"credential": {**request["credential"], "totp_secret": "x" * 257}},
            {"expected_identity": {**personal_identity(), "source_plan_type": "business"}},
            {"expected_identity": {**personal_identity(), "source_subject": "", "source_issuer": ""}},
            {"expected_identity": {**personal_identity(), "source_workspace_id": ""}},
            {"organization_id": str(uuid.uuid4())}, {"session_handle": "s" * 32}, {"device": {}},
        ]
        for fields in invalid:
            with self.subTest(fields=sorted(fields)), self.assertRaises(APIError):
                validate_request({**request, **fields}, time.time())
        for name in ("source_id_token", "kilo_identity"):
            missing = {key: value for key, value in request.items() if key != name}
            with self.subTest(missing=name), self.assertRaises(APIError):
                validate_request(missing, time.time())


    def test_reauthorization_requires_original_identity_and_source(self):
        original = reauthorize_task()
        self.assertEqual("reauthorize_subscription", validate_request(original, time.time())["phase"])
        for fields in ({"kilo_identity": None}, {"source_id_token": ""}, {"credential": {}},
                       {"session_handle": "s" * 32}, {"organization_id": str(uuid.uuid4())},
                       {"expected_identity": {**personal_identity(), "source_plan_type": "team"}},
                       {"kilo_identity": {**original["kilo_identity"], "session_material": ""}},
                       {"kilo_identity": {**original["kilo_identity"], "email": "other@example.test"}}):
            with self.subTest(fields=sorted(fields)), self.assertRaises(APIError):
                validate_request({**original, **fields}, time.time())


class RuntimeTests(unittest.TestCase):
    def setUp(self):
        self.protocol = FakeProtocol()
        self.runtime = Runtime(self.protocol)
        self.runtime.start()
    def tearDown(self):
        self.protocol.release.set()
        self.runtime.stop()
    def finished(self, task_id):
        until = time.monotonic() + 3
        while time.monotonic() < until:
            result = self.runtime.result(task_id)
            if result["status"] not in {"queued", "running"}:
                return result
            time.sleep(.005)
        self.fail("offline task did not finish")
    def login(self):
        submitted = self.runtime.submit(task())
        return self.finished(submitted["task_id"])
    def test_health_advertises_workspace_identity(self):
        for capability in ("workspace_identity", "reconcile_subscription", "reauthorize_subscription", "credential_snapshot", "existing_subscription_observation"):
            self.assertIn(capability, self.runtime.health()["capabilities"])

    def test_resume_material_survives_handle_close_and_new_source(self):
        login = self.login()
        material = login["identity_session_material"]
        self.assertGreater(parse_time(json.loads(material)["expires_at"]), parse_time(login["expires_at"]) + 3600)
        self.runtime.close_session(login["session_handle"])
        identity = {"api_key": "kilo-private-key", "user_id": "kilo-owned-1", "email": "owner@example.test",
                    "session_material": material}
        request = task("resume_identity", job_id=43, expected_identity={"source_email": "new@example.test", "source_workspace_id": "new-workspace"},
                       kilo_identity=identity)
        resumed = self.finished(self.runtime.submit(request)["task_id"])
        self.assertEqual("succeeded", resumed["status"])
        self.assertEqual("", resumed["verified_identity"]["openai_email"])
        connected = self.finished(self.runtime.submit(task("connect_subscription", job_id=43,
            expected_identity=request["expected_identity"], session_handle=resumed["session_handle"],
            credential={"email": "new@example.test", "password": "new-private"}))["task_id"])
        self.assertEqual("succeeded", connected["status"])
        self.assertEqual("new@example.test", connected["connection_email"])
        self.assertIn("identity_session_material", connected)
        self.assertNotIn("api_key", connected)
        self.assertNotIn("private_state", connected)
        self.assertEqual({}, self.runtime.tasks[resumed["task_id"]].request)
        self.runtime.acknowledge(resumed["task_id"])
        self.assertNotIn(resumed["task_id"], self.runtime.tasks)

    def test_resume_rejects_wrong_kilo_identity_and_missing_material_result(self):
        identity = {"api_key": "kilo-private-key", "user_id": "kilo-owned-1", "email": "owner@example.test"}
        execute = self.protocol.execute
        for failure in ("identity", "material"):
            def invalid_response(request, state, cancel, register):
                response = execute(request, state, cancel, register)
                if failure == "identity":
                    response["verified_identity"]["kilo_user_id"] = "other-kilo"
                else:
                    response.pop("identity_session_material")
                return response
            with patch.object(self.protocol, "execute", side_effect=invalid_response):
                result = self.finished(self.runtime.submit(task("resume_identity", kilo_identity=identity))["task_id"])
            self.assertEqual("failed", result["status"])
            self.assertEqual("IDENTITY_MISMATCH" if failure == "identity" else "REMOTE_RESPONSE_INVALID", result["error_code"])
            self.assertNotIn("identity_session_material", result)
        self.assertFalse(self.runtime.sessions)

    def test_provider_refusals_are_stable_needs_action_without_retry(self):
        for code in ("BYOK_AUTHORIZATION_REJECTED",):
            with self.subTest(code=code), patch.object(self.protocol, "execute", side_effect=ProtocolError(code, True, "Kilo官方拒绝本次GPT订阅授权，原身份保持")):
                result = self.finished(self.runtime.submit(task())["task_id"])
            self.assertEqual("needs_action", result["status"])
            self.assertEqual(code, result["error_code"])
            self.assertFalse(result["retryable"])
            self.assertNotIn("identity_session_material", result)
            self.assertFalse(self.runtime.sessions)

    def test_provider_facts_projection_contains_no_raw_provider_data(self):
        execute = self.protocol.execute
        def with_private_providers(request, state, cancel, register):
            response = execute(request, state, cancel, register)
            response["identity_rebind_capable"] = True
            response["identity_provider_id"] = "email"
            response["identity_provider_count"] = 2
            response["providers"] = [{"provider": "email", "email": "private@example.test"}]
            return response
        with patch.object(self.protocol, "execute", side_effect=with_private_providers):
            result = self.login()
        self.assertNotIn("identity_rebind_capable", result)
        self.assertNotIn("identity_provider_count", result)
        self.assertNotIn("identity_provider_id", result)
        self.assertNotIn("providers", result)
        self.assertNotIn("private@example.test", json.dumps(result))

    def test_dedupe_identity_session_and_ack(self):
        request = task()
        created = self.runtime.submit(request)
        duplicate = self.runtime.submit(request)
        self.assertEqual(created["task_id"], duplicate["task_id"])
        result = self.finished(created["task_id"])
        self.assertEqual("succeeded", result["status"])
        self.assertNotIn("api_key", result)
        self.assertNotIn("private_state", result)
        self.assertEqual({}, self.runtime.tasks[created["task_id"]].request)
        conflict = dict(request, credential={**request["credential"], "password": "changed"})
        with self.assertRaises(APIError) as caught:
            self.runtime.submit(conflict)
        self.assertEqual("OPERATION_ID_CONFLICT", caught.exception.code)
        connect = task("connect_subscription", session_handle=result["session_handle"])
        done = self.finished(self.runtime.submit(connect)["task_id"])
        self.assertTrue(done["byok_enabled"])
        self.assertEqual(result["session_handle"], done["session_handle"])
        self.runtime.acknowledge(created["task_id"])
        with self.assertRaises(APIError) as caught:
            self.runtime.submit(request)
        self.assertEqual(410, caught.exception.status)
    def test_fixed_proxy_and_owner(self):
        login = self.login()
        connect = task("connect_subscription", session_handle=login["session_handle"], login_proxy={"id": 8, "url": "http://127.0.0.1:9000"})
        with self.assertRaises(APIError) as caught:
            self.runtime.submit(connect)
        self.assertEqual("LOGIN_PROXY_CONFLICT", caught.exception.code)
        connect["login_proxy"] = task()["login_proxy"]
        connect["job_id"] = 43
        with self.assertRaises(APIError) as caught:
            self.runtime.submit(connect)
        self.assertEqual("SESSION_OWNERSHIP_MISMATCH", caught.exception.code)
    def test_workspace_identity_is_forwarded_across_all_phases(self):
        identity = {"source_email": "owner@example.test", "source_workspace_id": "26784415-3acf-4a79-b6e0-964cd85a77e4",
                    "source_plan_type": "chatgpt_business"}
        login = self.finished(self.runtime.submit(task(expected_identity=identity))["task_id"])
        self.assertEqual("succeeded", login["status"])
        connect = task("connect_subscription", expected_identity=identity, session_handle=login["session_handle"])
        connected = self.finished(self.runtime.submit(connect)["task_id"])
        self.assertEqual("succeeded", connected["status"])
        self.assertEqual(identity["source_workspace_id"], connected["connection_workspace_id"])
        approve = task("approve_device", expected_identity=identity, session_handle=connected["session_handle"],
                       device={"user_code": "ABCD-EFGH", "verification_url": "https://app.kilo.ai/device-auth"})
        approve.pop("credential")
        approved = self.finished(self.runtime.submit(approve)["task_id"])
        self.assertEqual("succeeded", approved["status"])
        self.assertEqual(login["session_handle"], approved["session_handle"])
        self.assertEqual(["login", "connect_subscription", "approve_device"], self.protocol.calls)
        self.assertEqual([identity] * 3, self.protocol.identities)

    def test_connect_rejects_missing_or_mismatched_result_workspace(self):
        identity = {"source_email": "owner@example.test", "source_workspace_id": "workspace-original", "source_plan_type": "chatgpt_business"}
        login = self.finished(self.runtime.submit(task(expected_identity=identity))["task_id"])
        self.assertEqual("succeeded", login["status"])
        session = self.runtime.sessions[login["session_handle"]]
        state, expires = session.state, session.expires
        execute = self.protocol.execute
        for workspace in (None, "", "workspace-other", 123):
            with self.subTest(workspace=workspace):
                def response_with_workspace(request, current_state, cancel, register):
                    response = execute(request, current_state, cancel, register)
                    if workspace is None:
                        response.pop("connection_workspace_id")
                    else:
                        response["connection_workspace_id"] = workspace
                    return response
                request = task("connect_subscription", expected_identity=identity, session_handle=login["session_handle"])
                with patch.object(self.protocol, "execute", side_effect=response_with_workspace):
                    result = self.finished(self.runtime.submit(request)["task_id"])
                self.assertEqual("failed", result["status"])
                self.assertEqual("IDENTITY_MISMATCH", result["error_code"])
                self.assertNotIn("connection_workspace_id", result)
                self.assertNotIn("byok_enabled", result)
                self.assertIs(state, session.state)
                self.assertEqual(expires, session.expires)

    def test_personal_subject_receipt_does_not_claim_a_workspace(self):
        expected = {"source_email": "owner@example.test", "source_workspace_id": "personal-space",
                    "source_plan_type": "plus", "source_subject": "auth0|original",
                    "source_issuer": "https://auth.openai.com"}
        original = self.protocol.execute
        for mode in ("valid", "other-subject", "invented-workspace", "business", "missing-subject"):
            with self.subTest(mode=mode):
                identity = dict(expected)
                if mode == "business": identity["source_plan_type"] = "business"
                if mode == "missing-subject":
                    identity.pop("source_subject")
                    identity.pop("source_issuer")
                login = self.finished(self.runtime.submit(task(expected_identity=identity))["task_id"])
                def personal_response(request, state, cancel, register):
                    value = original(request, state, cancel, register)
                    value.update(connection_identity_evidence="personal_subject", connection_workspace_id="",
                                 connection_subject="auth0|other" if mode == "other-subject" else "auth0|original")
                    if mode == "invented-workspace": value["connection_workspace_id"] = "personal-space"
                    return value
                with patch.object(self.protocol, "execute", side_effect=personal_response):
                    result = self.finished(self.runtime.submit(task("connect_subscription", expected_identity=identity,
                        session_handle=login["session_handle"]))["task_id"])
                self.assertEqual("succeeded" if mode == "valid" else "failed", result["status"])
                if mode == "valid":
                    self.assertEqual("", result["connection_workspace_id"])
                    self.assertEqual("personal_subject", result["connection_identity_evidence"])
                else:
                    self.assertEqual("IDENTITY_MISMATCH", result["error_code"])

    def test_credential_snapshot_connect_projects_receipt_and_keeps_ticket_out_of_session(self):
        expected = personal_identity()
        login = self.finished(self.runtime.submit(task(expected_identity=expected))["task_id"])
        original = self.protocol.execute
        forwarded = []
        def response_with_receipt(request, state, cancel, register):
            forwarded.append(request.get("source_id_token"))
            response = snapshot_response(request, original(request, state, cancel, register))
            response["credential_identity_evidence"]["raw_ticket"] = request["source_id_token"]
            response["identity_association"]["raw_ticket"] = request["source_id_token"]
            response["source_id_token"] = request["source_id_token"]
            response["private_state"].update(source_id_token=request["source_id_token"],
                                             identity_association=response["identity_association"],
                                             credential_identity_evidence=response["credential_identity_evidence"])
            return response
        fingerprint = self.runtime.sessions[login["session_handle"]].expected_identity_fingerprint
        with patch.object(self.protocol, "execute", side_effect=response_with_receipt):
            for token in ("synthetic-first.signed.ticket", "synthetic-refreshed.signed.ticket"):
                request = task("connect_subscription", expected_identity=expected, session_handle=login["session_handle"], source_id_token=token)
                result = self.finished(self.runtime.submit(request)["task_id"])
                self.assertEqual("succeeded", result["status"])
                self.assertTrue(result["byok_enabled"])
                self.assertNotEqual(expected["source_subject"], result["connection_subject"])
                self.assertEqual("", result["connection_workspace_id"])
                self.assertEqual(CREDENTIAL_EVIDENCE_FIELDS, set(result["credential_identity_evidence"]))
                self.assertEqual(IDENTITY_ASSOCIATION_FIELDS, set(result["identity_association"]))
                self.assertEqual(expected["source_subject"], result["identity_association"]["source_subject"])
                self.assertEqual(result["connection_subject"], result["identity_association"]["kilo_subject"])
                self.assertNotIn(token, json.dumps(result))
                self.assertNotIn(token, json.dumps(self.runtime.health()))
                session = self.runtime.sessions[result["session_handle"]]
                self.assertEqual(fingerprint, session.expected_identity_fingerprint)
                self.assertFalse({"source_id_token", "identity_association", "credential_identity_evidence", "connection_observation"} & session.state.keys())
                self.assertEqual({}, self.runtime.tasks[result["task_id"]].request)
        self.assertEqual(["synthetic-first.signed.ticket", "synthetic-refreshed.signed.ticket"], forwarded)

    def test_reauthorization_dedupes_and_requires_fresh_enablement(self):
        original = self.protocol.execute
        for mode in ("fresh", "unknown_enablement", "wrong_owner"):
            request = reauthorize_task()
            def recovered_response(payload, state, cancel, register):
                value = snapshot_response(payload, original(payload, state, cancel, register))
                value.update(byok_enabled=mode != "unknown_enablement",
                             byok_enable_evidence="fresh_delegated_authorization" if mode != "unknown_enablement" else "existing_connection_enablement_unknown")
                if mode == "wrong_owner": value["verified_identity"]["kilo_user_id"] = "other-owner"
                return value
            with self.subTest(mode=mode), patch.object(self.protocol, "execute", side_effect=recovered_response):
                submitted = self.runtime.submit(request)
                result = self.finished(submitted["task_id"])
                self.assertEqual(submitted["task_id"], self.runtime.submit(request)["task_id"])
            self.assertEqual("succeeded" if mode == "fresh" else "failed", result["status"])
            if mode == "fresh":
                self.assertNotIn(request["source_id_token"], json.dumps(result))
                self.assertFalse({"source_id_token", "identity_association", "credential_identity_evidence"} &
                                 self.runtime.sessions[result["session_handle"]].state.keys())
            else:
                self.assertNotIn("session_handle", result)
                self.assertIn(result["error_code"], {"IDENTITY_MISMATCH", "BYOK_ENABLE_UNVERIFIED"})

    def test_existing_connection_association_succeeds_without_claiming_enabled(self):
        expected = personal_identity()
        login = self.finished(self.runtime.submit(task(expected_identity=expected))["task_id"])
        original = self.protocol.execute
        def observed_response(request, state, cancel, register):
            response = snapshot_response(request, original(request, state, cancel, register))
            response.pop("credential_identity_evidence")
            response.update(byok_enabled=False, byok_enable_evidence="existing_connection_enablement_unknown")
            response["identity_association"]["raw_token"] = request["source_id_token"]
            response["private_state"]["identity_association"] = response["identity_association"]
            return response
        request = task("connect_subscription", expected_identity=expected, session_handle=login["session_handle"],
                       source_id_token="synthetic.source.ticket")
        with patch.object(self.protocol, "execute", side_effect=observed_response):
            result = self.finished(self.runtime.submit(request)["task_id"])
        self.assertEqual("succeeded", result["status"])
        self.assertIs(False, result["byok_enabled"])
        self.assertEqual("existing_connection_enablement_unknown", result["byok_enable_evidence"])
        self.assertEqual(IDENTITY_ASSOCIATION_FIELDS, set(result["identity_association"]))
        self.assertNotEqual(result["identity_association"]["source_subject"], result["identity_association"]["kilo_subject"])
        self.assertNotIn("credential_identity_evidence", result)
        self.assertNotIn(request["source_id_token"], json.dumps(result))
        self.assertNotIn("identity_association", self.runtime.sessions[result["session_handle"]].state)
        self.assertEqual(["login", "connect_subscription"], self.protocol.calls)

    def test_identity_association_rejects_invalid_or_misplaced_fields(self):
        expected = personal_identity()
        login = self.finished(self.runtime.submit(task(expected_identity=expected))["task_id"])
        session = self.runtime.sessions[login["session_handle"]]
        state, expires = session.state, session.expires
        original = self.protocol.execute
        invalid = {"version": True, "method": "email_match", "source_issuer": "https://other.example.test",
                   "source_subject": "other-source", "source_workspace_id": "other-space", "source_plan_type": "business",
                   "kilo_user_id": "other-kilo", "kilo_client_id": "bad\nclient", "kilo_subject": expected["source_subject"],
                   "verified_at": timestamp(time.time() - 3600)}
        for mode in [*invalid, "missing-association", "missing-field", "not-dict", "future-time", "empty-client",
                     "long-client", "non-ascii-client", "organization", "workspace", "legacy-smuggling", "enabled-unknown"]:
            with self.subTest(mode=mode):
                def invalid_response(request, current_state, cancel, register):
                    response = snapshot_response(request, original(request, current_state, cancel, register))
                    response.pop("credential_identity_evidence")
                    association = response["identity_association"]
                    if mode in invalid: association[mode] = invalid[mode]
                    elif mode == "missing-association": response.pop("identity_association")
                    elif mode == "missing-field": association.pop("source_subject")
                    elif mode == "not-dict": response["identity_association"] = []
                    elif mode == "future-time": association["verified_at"] = timestamp(time.time() + 30)
                    elif mode == "empty-client": association["kilo_client_id"] = ""
                    elif mode == "long-client": association["kilo_client_id"] = "a" * 257
                    elif mode == "non-ascii-client": association["kilo_client_id"] = "客户端"
                    elif mode == "organization": response["organization_id"] = str(uuid.uuid4())
                    elif mode == "workspace": response["connection_workspace_id"] = expected["source_workspace_id"]
                    elif mode == "legacy-smuggling":
                        response.update(connection_identity_evidence="personal_subject", connection_subject=expected["source_subject"])
                    elif mode == "enabled-unknown":
                        response.update(byok_enabled=True, byok_enable_evidence="existing_connection_enablement_unknown")
                    return response
                request = task("connect_subscription", expected_identity=expected, session_handle=login["session_handle"],
                               source_id_token="synthetic.source.ticket")
                with patch.object(self.protocol, "execute", side_effect=invalid_response):
                    result = self.finished(self.runtime.submit(request)["task_id"])
                self.assertEqual("failed", result["status"])
                self.assertEqual("BYOK_ENABLE_UNVERIFIED" if mode == "enabled-unknown" else "IDENTITY_MISMATCH", result["error_code"])
                self.assertNotIn("identity_association", result)
                self.assertNotIn("byok_enabled", result)
                self.assertIs(state, session.state)
                self.assertEqual(expires, session.expires)

    def test_credential_snapshot_connect_rejects_incomplete_mismatched_or_stale_receipts(self):
        expected = personal_identity()
        login = self.finished(self.runtime.submit(task(expected_identity=expected))["task_id"])
        session = self.runtime.sessions[login["session_handle"]]
        state, expires = session.state, session.expires
        original = self.protocol.execute
        invalid = {
            "version": True, "method": "email_match", "kilo_user_id": "other-kilo", "kilo_client_id": "",
            "source_client_id": "other-client", "source_issuer": "https://other.example.test",
            "source_subject": "original-source", "source_workspace_id": "other-space", "source_plan_type": "business",
            "connection_subject": "other-connection", "provider_created_at": "invalid", "provider_fingerprint": "a" * 63,
            "observed_at": timestamp(time.time() - 3600), "kilo_password_totp_verified": False,
            "source_password_totp_verified": False, "source_snapshot_signature_verified": False,
            "source_signature_verified": False, "nonce_verified": 1, "provider_unchanged": False,
        }
        for mode in [*("receipt:" + name for name in invalid), "missing-field", "not-dict", "same-client",
                     "future-observation", "future-provider", "old-connect", "workspace", "missing-token", "missing-totp", "owner"]:
            with self.subTest(mode=mode):
                request = task("connect_subscription", expected_identity=expected, session_handle=login["session_handle"],
                               source_id_token="synthetic.source.ticket")
                if mode == "missing-token": request.pop("source_id_token")
                if mode == "missing-totp": request["credential"].pop("totp_secret")
                def invalid_response(request, current_state, cancel, register):
                    response = snapshot_response(request, original(request, current_state, cancel, register))
                    evidence = response["credential_identity_evidence"]
                    if mode.startswith("receipt:"):
                        name = mode.split(":", 1)[1]
                        evidence[name] = invalid[name]
                    elif mode == "missing-field": evidence.pop("source_snapshot_signature_verified")
                    elif mode == "not-dict": response["credential_identity_evidence"] = []
                    elif mode == "same-client": evidence["kilo_client_id"] = evidence["source_client_id"]
                    elif mode == "future-observation": evidence["observed_at"] = timestamp(parse_time(request["deadline_at"]) + 61)
                    elif mode == "future-provider": evidence["provider_created_at"] = evidence["observed_at"]
                    elif mode == "old-connect": response["connection_at"] = timestamp(time.time() - 3600)
                    elif mode == "workspace": response["connection_workspace_id"] = expected["source_workspace_id"]
                    elif mode == "owner": response["verified_identity"]["kilo_user_id"] = "other-kilo"
                    return response
                with patch.object(self.protocol, "execute", side_effect=invalid_response):
                    result = self.finished(self.runtime.submit(request)["task_id"])
                self.assertEqual("failed", result["status"])
                self.assertEqual("IDENTITY_MISMATCH", result["error_code"])
                self.assertNotIn("credential_identity_evidence", result)
                self.assertNotIn("byok_enabled", result)
                self.assertIs(state, session.state)
                self.assertEqual(expires, session.expires)

    def test_reconcile_observes_existing_connection_without_key_or_enable_claim(self):
        original = self.protocol.execute
        def observed_response(request, state, cancel, register):
            self.assertIsNone(state)
            self.assertNotIn("api_key", request["kilo_identity"])
            response = snapshot_response(request, original(request, state, cancel, register))
            response["credential_identity_evidence"]["raw_token"] = "must-not-escape"
            response["connection_observation"]["callback"] = "must-not-escape"
            response["private_state"]["connection_observation"] = response["connection_observation"]
            return response
        request = reconcile_task()
        with patch.object(self.protocol, "execute", side_effect=observed_response):
            result = self.finished(self.runtime.submit(request)["task_id"])
        self.assertEqual("succeeded", result["status"])
        self.assertIs(False, result["byok_enabled"])
        self.assertEqual("", result["byok_enable_evidence"])
        self.assertEqual(CREDENTIAL_EVIDENCE_FIELDS, set(result["credential_identity_evidence"]))
        self.assertEqual(OBSERVATION_FIELDS, set(result["connection_observation"]))
        self.assertEqual("unverified", result["connection_observation"]["enabled_evidence"])
        self.assertLess(parse_time(result["connection_at"]), time.time() - 3600)
        self.assertNotIn("byok_evidence", result)
        self.assertNotIn("must-not-escape", json.dumps(result))
        self.assertEqual(["reconcile_subscription"], self.protocol.calls)
        self.assertFalse({"source_id_token", "credential_identity_evidence", "connection_observation"}
                         & self.runtime.sessions[result["session_handle"]].state.keys())

    def test_reconcile_rejects_enable_claims_and_mismatched_observations_without_saving_session(self):
        original = self.protocol.execute
        invalid = {
            "version": True, "method": "fresh_callback", "kilo_user_id": "other-kilo", "organization_id": "other-org",
            "openai_email": "other@example.test", "openai_subject": "original-source", "connected_at": timestamp(time.time() - 5),
            "observed_at": timestamp(time.time() - 5), "provider_created_at": timestamp(time.time() - 5),
            "provider_fingerprint": "b" * 64, "connection_unchanged": False, "provider_unchanged": 1,
            "enabled_evidence": "connected",
        }
        for mode in [*("observation:" + name for name in invalid), "missing-observation", "missing-receipt", "missing-field",
                     "personal-subject", "future-connection", "enabled", "missing-enabled", "enable-evidence", "missing-enable-evidence",
                     "callback-evidence", "device-approved", "organization", "source-owner"]:
            with self.subTest(mode=mode):
                def invalid_response(request, state, cancel, register):
                    response = snapshot_response(request, original(request, state, cancel, register))
                    if mode.startswith("observation:"):
                        name = mode.split(":", 1)[1]
                        response["connection_observation"][name] = invalid[name]
                    elif mode == "missing-observation": response.pop("connection_observation")
                    elif mode == "missing-receipt": response.pop("credential_identity_evidence")
                    elif mode == "missing-field": response["connection_observation"].pop("provider_fingerprint")
                    elif mode == "personal-subject": response["connection_identity_evidence"] = "personal_subject"
                    elif mode == "future-connection":
                        connected = timestamp(parse_time(response["connection_observation"]["observed_at"]) + 1)
                        response["connection_at"] = response["connection_observation"]["connected_at"] = connected
                    elif mode == "enabled": response["byok_enabled"] = True
                    elif mode == "missing-enabled": response.pop("byok_enabled")
                    elif mode == "enable-evidence": response["byok_enable_evidence"] = "callback_allowance_and_fresh_connected"
                    elif mode == "missing-enable-evidence": response.pop("byok_enable_evidence")
                    elif mode == "callback-evidence": response["byok_evidence"] = "consent_callback"
                    elif mode == "device-approved": response["device_approved"] = True
                    elif mode == "organization": response["organization_id"] = "other-org"
                    elif mode == "source-owner": response["verified_identity"]["kilo_user_id"] = "other-kilo"
                    return response
                with patch.object(self.protocol, "execute", side_effect=invalid_response):
                    result = self.finished(self.runtime.submit(reconcile_task())["task_id"])
                self.assertEqual("failed", result["status"])
                enable_claim = mode in {"enabled", "missing-enabled", "enable-evidence", "missing-enable-evidence", "callback-evidence", "device-approved"}
                self.assertEqual("BYOK_ENABLE_UNVERIFIED" if enable_claim else "IDENTITY_MISMATCH", result["error_code"])
                self.assertNotIn("connection_observation", result)
                self.assertNotIn("session_handle", result)
                self.assertFalse(self.runtime.sessions)

    def test_workspace_or_plan_change_conflicts_with_operation_id(self):
        identity = {"source_email": "owner@example.test", "source_workspace_id": "workspace-original", "source_plan_type": "chatgpt_team"}
        request = task(expected_identity=identity)
        submitted = self.runtime.submit(request)
        for field, replacement in (("source_workspace_id", "workspace-other"), ("source_plan_type", "chatgpt_business")):
            with self.subTest(field=field):
                changed = dict(request, expected_identity={**identity, field: replacement})
                with self.assertRaises(APIError) as caught:
                    self.runtime.submit(changed)
                self.assertEqual("OPERATION_ID_CONFLICT", caught.exception.code)
        self.assertEqual("succeeded", self.finished(submitted["task_id"])["status"])

    def test_session_rejects_changed_or_removed_workspace_and_plan(self):
        identity = {"source_email": "owner@example.test", "source_workspace_id": "workspace-original", "source_plan_type": "chatgpt_business"}
        login = self.finished(self.runtime.submit(task(expected_identity=identity))["task_id"])
        self.assertEqual("succeeded", login["status"])
        changes = [{**identity, "source_workspace_id": "workspace-other"}, {**identity, "source_plan_type": "chatgpt_plus"},
                   {**identity, "source_workspace_id": ""}, {**identity, "source_plan_type": ""},
                   {key: value for key, value in identity.items() if key != "source_workspace_id"},
                   {key: value for key, value in identity.items() if key != "source_plan_type"},
                   {"source_email": identity["source_email"]}]
        for phase in ("connect_subscription", "approve_device"):
            for changed in changes:
                with self.subTest(phase=phase, identity=changed):
                    request = task(phase, expected_identity=changed, session_handle=login["session_handle"])
                    if phase == "approve_device":
                        request.pop("credential")
                        request["device"] = {"user_code": "ABCD-EFGH", "verification_url": "https://app.kilo.ai/device-auth"}
                    with self.assertRaises(APIError) as caught:
                        self.runtime.submit(request)
                    self.assertEqual(409, caught.exception.status)
                    self.assertEqual("SESSION_OWNERSHIP_MISMATCH", caught.exception.code)
        self.assertEqual(["login"], self.protocol.calls)
        connected = self.finished(self.runtime.submit(task("connect_subscription", expected_identity=identity,
                                                        session_handle=login["session_handle"]))["task_id"])
        self.assertEqual("succeeded", connected["status"])

    def test_legacy_session_normalizes_empty_fields_and_rejects_added_identity(self):
        login = self.login()
        for fields in ({"source_workspace_id": "workspace-added"}, {"source_plan_type": "chatgpt_business"},
                       {"source_workspace_id": "workspace-added", "source_plan_type": "chatgpt_business"}):
            with self.subTest(fields=fields):
                request = task("connect_subscription", expected_identity={"source_email": "owner@example.test", **fields},
                               session_handle=login["session_handle"])
                with self.assertRaises(APIError) as caught:
                    self.runtime.submit(request)
                self.assertEqual("SESSION_OWNERSHIP_MISMATCH", caught.exception.code)
        self.assertEqual(["login"], self.protocol.calls)
        request = task("connect_subscription", expected_identity={"source_email": "owner@example.test", "source_workspace_id": "", "source_plan_type": ""},
                       session_handle=login["session_handle"])
        self.assertEqual("succeeded", self.finished(self.runtime.submit(request)["task_id"])["status"])
        self.assertEqual([{"source_email": "owner@example.test"}] * 2, self.protocol.identities)

    def test_cancel_and_new_revision_reject_late_result(self):
        self.protocol.block = True
        submitted = self.runtime.submit(task())
        self.assertTrue(self.protocol.started.wait(1))
        self.runtime.cancel(submitted["task_id"])
        newer = self.runtime.submit(task(revision=2))
        self.protocol.release.set()
        self.assertEqual("cancelled", self.finished(submitted["task_id"])["status"])
        done = self.finished(newer["task_id"])
        self.assertEqual(2, done["revision"])
        self.assertTrue(all(s.revision == 2 for s in self.runtime.sessions.values()))
        with self.assertRaises(APIError) as caught:
            self.runtime.submit(task())
        self.assertEqual("STALE_EXECUTION", caught.exception.code)
    def test_deadline_cancels_running_and_session_ttl(self):
        self.protocol.block = True
        submitted = self.runtime.submit(task(deadline_at=timestamp(time.time() + .05)))
        self.assertTrue(self.protocol.started.wait(1))
        time.sleep(.06)
        result = self.runtime.result(submitted["task_id"])
        self.assertEqual("cancelled", result["status"])
        self.assertEqual("DEADLINE_EXCEEDED", result["error_code"])
        self.protocol.release.set()
        self.assertEqual({}, self.runtime.tasks[submitted["task_id"]].request)
        self.assertFalse(self.runtime.sessions)
    def test_result_and_session_ttl_are_bounded(self):
        login = self.login()
        with self.runtime.lock:
            self.runtime.sessions[login["session_handle"]].expires = self.runtime.clock() - 1
            self.runtime.tasks[login["task_id"]].finished = self.runtime.clock() - self.runtime.result_ttl - 1
        with self.assertRaises(APIError) as caught:
            self.runtime.result(login["task_id"])
        self.assertEqual(410, caught.exception.status)
        with self.assertRaises(APIError) as caught:
            self.runtime.submit(task("connect_subscription", session_handle=login["session_handle"]))
        self.assertEqual("SESSION_LOST", caught.exception.code)
        self.assertFalse(self.runtime.sessions)

    def test_all_http_endpoints_require_bearer_and_unknown_is_404(self):
        server = Server(("127.0.0.1", 0), self.runtime, "a" * 32)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            for method, path in [("GET", "/v1/health"), ("POST", "/v1/tasks"), ("GET", "/v1/tasks/" + "x" * 24), ("POST", "/v1/tasks/" + "x" * 24 + "/ack"), ("DELETE", "/v1/sessions/" + "x" * 24)]:
                conn = http.client.HTTPConnection(*server.server_address, timeout=2)
                conn.request(method, path)
                self.assertEqual(401, conn.getresponse().status)
                conn.close()
            conn = http.client.HTTPConnection(*server.server_address, timeout=2)
            conn.request("GET", "/v1/tasks/" + "x" * 24, headers={"Authorization": "Bearer " + "a" * 32})
            self.assertEqual(404, conn.getresponse().status)
            conn.close()
        finally:
            server.shutdown(); server.server_close(); thread.join(1)


if __name__ == "__main__":
    unittest.main()
