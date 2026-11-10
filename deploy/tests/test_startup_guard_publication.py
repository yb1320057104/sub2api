import importlib.util
import hashlib
import io
import json
import os
import subprocess
from pathlib import Path
import sys
import tempfile
import unittest
from contextlib import ExitStack, redirect_stderr, redirect_stdout
from unittest.mock import patch
from types import SimpleNamespace

SOURCE = Path(__file__).resolve().parents[1] / "jenkins/startup_guard_publication.py"
spec = importlib.util.spec_from_file_location("guard_publication", SOURCE)
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)

LOCAL_SOURCE = SOURCE.parents[1] / "local-release.py"
local_spec = importlib.util.spec_from_file_location("pk_local_release_integration", LOCAL_SOURCE)
local = importlib.util.module_from_spec(local_spec)
sys.modules[local_spec.name] = local  # dataclasses resolve postponed annotations here
local_spec.loader.exec_module(local)


class PublicationGateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.directory = Path(self.temp.name)
        self.data = {"repository": gate.REPOSITORY, "version": "0.2.19-pk.57",
                     "commit": "a" * 40, "tree": "b" * 40, "package_id": "123-" + "a" * 12,
                     "binary_sha256": "c" * 64}
        (self.directory / "manifest.json").write_text(json.dumps(self.data))
        self.addCleanup(self.temp.cleanup)
        self.env = patch.dict(os.environ, {"PK_STARTUP_GUARD_HOOK_ARGV": json.dumps([str(SOURCE.resolve())])})
        self.env.start()
        self.addCleanup(self.env.stop)

    def reply(self, **changes):
        result = {"schema_version": 1, "target": gate.TARGET, "operation": "prepare",
                  "release": gate.release_identity(self.directory, self.data),
                  "status": "authorized", "allowed": True, "healthy": True,
                  "epoch_unchanged": True, "receipt_binding_verified": True,
                  "restart_performed": False, "binary_replaced": False, "ha_authority_grant": False,
                  "host_guard_sha256": "d" * 64,
                  "container_guard_sha256": "d" * 64}
        result.update(changes)
        return SimpleNamespace(returncode=0, stdout=json.dumps(result).encode(), stderr=b"")

    def test_requires_operator_hook_before_new_publication(self):
        with patch.dict(os.environ, {"PK_STARTUP_GUARD_HOOK_ARGV": ""}), patch.object(gate.subprocess, "run") as run:
            with self.assertRaisesRegex(RuntimeError, "operator_hook_required"):
                gate.authorize(self.directory, self.data, "prepare")
            run.assert_not_called()

    def test_candidate_and_manifest_are_bound_to_operator_request(self):
        with patch.object(gate.subprocess, "run", return_value=self.reply()) as run:
            gate.authorize(self.directory, self.data, "prepare")
        request = json.loads(run.call_args.kwargs["input"])
        self.assertEqual(request["release"], gate.release_identity(self.directory, self.data))
        self.assertEqual(request["operation"], "prepare")
        self.assertNotIn("shell", run.call_args.kwargs)

    def test_plan_accepts_unapproved_candidate_without_claiming_authorization(self):
        with patch.object(gate.subprocess, "run", return_value=self.reply(operation="plan", status="ready", allowed=False)):
            self.assertFalse(gate.authorize(self.directory, self.data, "plan")["allowed"])
        with patch.object(gate.subprocess, "run", return_value=self.reply(operation="check", status="ready", allowed=False)):
            with self.assertRaisesRegex(RuntimeError, "not_authorized"):
                gate.authorize(self.directory, self.data, "check")

    def test_changed_identity_or_live_state_blocks_release(self):
        for mutation in ({"release": {}}, {"healthy": False}, {"epoch_unchanged": False},
                         {"receipt_binding_verified": False}, {"restart_performed": True},
                         {"container_guard_sha256": "e" * 64}, {"allowed": False}):
            with self.subTest(mutation=mutation), patch.object(gate.subprocess, "run", return_value=self.reply(operation="check", **mutation)):
                with self.assertRaises(RuntimeError):
                    gate.authorize(self.directory, self.data, "check")

    def test_hook_failure_never_exposes_private_stderr(self):
        with patch.object(gate.subprocess, "run", return_value=SimpleNamespace(returncode=1, stdout=b"", stderr=b"private-token")):
            with self.assertRaisesRegex(RuntimeError, "startup_guard_authorization_failed") as error:
                gate.authorize(self.directory, self.data, "prepare")
            self.assertNotIn("private-token", str(error.exception))

    def test_changed_local_manifest_blocks_hook(self):
        (self.directory / "manifest.json").write_text("{}")
        with patch.object(gate.subprocess, "run") as run:
            with self.assertRaisesRegex(RuntimeError, "manifest_changed"):
                gate.authorize(self.directory, self.data, "prepare")
            run.assert_not_called()

    def test_historical_sealed_release_keeps_original_contract(self):
        self.data["version"] = "0.2.19-pk.56"
        with patch.object(gate.subprocess, "run") as run:
            self.assertIsNone(gate.authorize(self.directory, self.data, "check"))
            run.assert_not_called()


class HAPublicationGateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.data = {"repository": gate.REPOSITORY, "version": "0.2.19-pk.61",
                     "commit": "a" * 40, "tree": "b" * 40, "package_id": "61-" + "a" * 12,
                     "binary_sha256": "c" * 64}
        (self.directory / "manifest.json").write_text(json.dumps(self.data))
        self.review = {"source_commit": self.data["commit"], "overlay_profile_sha256": "1" * 64,
                       "overlay_manifest_sha256": "2" * 64, "migration_receipt_sha256": "3" * 64,
                       "migration_evidence_sha256": {"fresh": "4" * 64, "upgrade": "5" * 64, "rollback": "6" * 64}}
        self.review_hash = hashlib.sha256(json.dumps(self.review, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        self.enterContext(patch.dict(os.environ, {
            "PK_STARTUP_GUARD_TARGET": gate.HA_TARGET,
            "PK_STARTUP_GUARD_HOOK_ARGV": json.dumps([str(SOURCE.resolve())]),
            "PK_HA_PUBLICATION_REVIEW_SHA256": self.review_hash,
        }))

    def reply(self, operation="check", **changes):
        result = {"schema_version": 2, "target": gate.HA_TARGET, "operation": operation,
                  "release": gate.release_identity(self.directory, self.data),
                  "purpose": "ha-source-release-admission", "source_admission_only": True,
                  "runtime_authorization_changed": False, "status": "admitted", "allowed": True,
                  "healthy": True, "epoch_unchanged": True, "receipt_binding_verified": True,
                  "restart_performed": False, "binary_replaced": False, "ha_authority_grant": False,
                  "review": self.review, "review_sha256": self.review_hash,
                  "epoch_sha256": "7" * 64, "epoch_before_sha256": "7" * 64, "epoch_after_sha256": "7" * 64,
                  "admission_record_sha256": "8" * 64,
                  "nodes": {
                      "primary": {"api_running": True, "api_ready": True, "gate_held": False,
                                  "worker_running": True, "postgres_role": "primary"},
                      "secondary": {"api_running": True, "api_ready": True, "gate_held": False,
                                    "worker_running": False, "postgres_role": "replica"}}}
        result.update(changes)
        return SimpleNamespace(returncode=0, stdout=json.dumps(result).encode(), stderr=b"")

    def test_explicit_ha_contract_binds_review_and_release_without_fake_guard(self):
        with patch.object(gate.subprocess, "run", return_value=self.reply()) as run:
            receipt = gate.authorize(self.directory, self.data, "check")
        request = json.loads(run.call_args.kwargs["input"])
        self.assertEqual((request["schema_version"], request["target"]), (2, gate.HA_TARGET))
        self.assertEqual(request["release"], receipt["release"])
        self.assertNotIn("host_guard_sha256", receipt)
        self.assertNotIn("container_guard_sha256", receipt)

    def test_package_cannot_select_ha_or_downgrade_failed_default_contract(self):
        self.data["target"] = gate.HA_TARGET
        self.data["schema_version"] = 2
        (self.directory / "manifest.json").write_text(json.dumps(self.data))
        with patch.dict(os.environ, {"PK_STARTUP_GUARD_TARGET": gate.TARGET}), patch.object(gate.subprocess, "run", return_value=self.reply()) as run:
            with self.assertRaisesRegex(RuntimeError, "receipt_identity_mismatch"):
                gate.authorize(self.directory, self.data, "check")
        self.assertEqual(json.loads(run.call_args.kwargs["input"])["schema_version"], 1)
        self.assertEqual(run.call_count, 1)

    def test_review_requires_trusted_environment_pin_and_exact_source(self):
        for changes in ({"review_sha256": "9" * 64}, {"review": {**self.review, "source_commit": "b" * 40}},
                        {"review": {**self.review, "migration_evidence_sha256": {"fresh": "4" * 64}}}):
            with self.subTest(changes=changes), patch.object(gate.subprocess, "run", return_value=self.reply(**changes)):
                with self.assertRaises(RuntimeError):
                    gate.authorize(self.directory, self.data, "check")
        with patch.dict(os.environ, {"PK_HA_PUBLICATION_REVIEW_SHA256": ""}), patch.object(gate.subprocess, "run", return_value=self.reply()):
            with self.assertRaisesRegex(RuntimeError, "review_not_pinned"):
                gate.authorize(self.directory, self.data, "check")

    def test_ha_never_accepts_runtime_changes_or_single_node_guard_claim(self):
        for changes in ({"host_guard_sha256": "d" * 64}, {"container_guard_sha256": "d" * 64},
                        {"source_admission_only": False}, {"runtime_authorization_changed": True},
                        {"ha_authority_grant": True}, {"binary_replaced": True}, {"restart_performed": True},
                        {"purpose": "runtime-deployment"}, {"epoch_after_sha256": "9" * 64},
                        {"admission_record_sha256": ""}, {"allowed": False}):
            with self.subTest(changes=changes), patch.object(gate.subprocess, "run", return_value=self.reply(**changes)):
                with self.assertRaises(RuntimeError):
                    gate.authorize(self.directory, self.data, "check")

    def test_two_ready_apis_and_single_worker_on_writer_are_required(self):
        baseline = json.loads(self.reply().stdout)["nodes"]
        for node, changes in (("primary", {"api_ready": False}), ("secondary", {"gate_held": True}),
                              ("primary", {"worker_running": False}), ("secondary", {"worker_running": True}),
                              ("secondary", {"postgres_role": "primary"}), ("primary", {"postgres_role": "replica"})):
            nodes = {key: dict(value) for key, value in baseline.items()}
            nodes[node].update(changes)
            with self.subTest(node=node, changes=changes), patch.object(gate.subprocess, "run", return_value=self.reply(nodes=nodes)):
                with self.assertRaises(RuntimeError):
                    gate.authorize(self.directory, self.data, "check")
        moved = {key: dict(value) for key, value in baseline.items()}
        moved["primary"].update(worker_running=False, postgres_role="replica")
        moved["secondary"].update(worker_running=True, postgres_role="primary")
        with patch.object(gate.subprocess, "run", return_value=self.reply(nodes=moved)):
            self.assertTrue(gate.authorize(self.directory, self.data, "check")["allowed"])

    def test_plan_is_not_source_admission_and_unknown_target_fails_closed(self):
        with patch.object(gate.subprocess, "run", return_value=self.reply(operation="plan", status="ready", allowed=False, admission_record_sha256=None)):
            self.assertFalse(gate.authorize(self.directory, self.data, "plan")["allowed"])
        with patch.dict(os.environ, {"PK_STARTUP_GUARD_TARGET": "none"}), patch.object(gate.subprocess, "run") as run:
            with self.assertRaisesRegex(RuntimeError, "invalid_target"):
                gate.authorize(self.directory, self.data, "check")
            run.assert_not_called()


class LocalPublicationEntryTests(unittest.TestCase):
    """Run the real CLI/publish/gate chain with all external I/O replaced.

    Artifact/provenance validation has its own suite. Here its accepted fixture
    enters the real staging and publication flow, so removing either gate call
    or swallowing its failure makes these tests fail.
    """

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="pk-local-publication-test-")
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name).resolve()
        self.data = {
            "repository": gate.REPOSITORY, "version": "0.2.19-pk.57",
            "commit": "a" * 40, "tree": "b" * 40, "package_id": "123-" + "a" * 12,
            "binary_sha256": "c" * 64, "image_id": "sha256:" + "e" * 64,
        }
        raw = json.dumps(self.data).encode()
        (self.directory / "manifest.json").write_bytes(raw)
        self.assets = [local.Asset("manifest.json", len(raw), hashlib.sha256(raw).hexdigest())]
        self.events = []
        self.remote_draft = None
        self.failed_operation = None
        self.revoke = False
        self.stdout, self.stderr = io.StringIO(), io.StringIO()

        stack = self.enterContext(ExitStack())
        stack.enter_context(patch.dict(os.environ, {
            "PK_GITHUB_TOKEN": "fixture-no-network-token",
            "PK_STARTUP_GUARD_HOOK_ARGV": json.dumps([str(SOURCE.resolve())]),
        }))
        stack.enter_context(patch.object(sys, "argv", [str(LOCAL_SOURCE), "publish", "--artifact-dir", str(self.directory)]))
        stack.enter_context(patch.object(local, "validate", side_effect=self.accept_validated_fixture))
        stack.enter_context(patch.object(local.control, "release_ready"))
        stack.enter_context(patch.object(local, "verify_source"))
        self.request = stack.enter_context(patch.object(local, "request", side_effect=self.fake_request))
        self.upload = stack.enter_context(patch.object(local, "upload", side_effect=lambda *_: self.events.append("upload")))
        self.verify_assets = stack.enter_context(patch.object(local, "verify_assets"))
        stack.enter_context(patch.object(local.startup_guard.subprocess, "run", side_effect=self.fake_hook))
        # Defense in depth: a regression must fail, never reach GitHub, Docker,
        # SSH, or another real external command during this simulated publish.
        stack.enter_context(patch.object(local.control, "command", side_effect=AssertionError("external command forbidden")))
        stack.enter_context(patch("socket.create_connection", side_effect=AssertionError("network forbidden")))
        stack.enter_context(patch.object(local.urllib.request, "build_opener", side_effect=AssertionError("network forbidden")))
        stack.enter_context(patch.object(local.http.client, "HTTPSConnection", side_effect=AssertionError("network forbidden")))
        stack.enter_context(redirect_stdout(self.stdout))
        stack.enter_context(redirect_stderr(self.stderr))

    def accept_validated_fixture(self, directory):
        self.assertEqual(json.loads((directory / "manifest.json").read_bytes()), self.data)
        return self.data, self.assets

    def fake_hook(self, argv, **kwargs):
        self.assertEqual(argv, [str(SOURCE.resolve())])
        request = json.loads(kwargs["input"])
        operation = request["operation"]
        self.events.append(operation)
        self.assertEqual(request["release"], gate.release_identity(self.directory, self.data))
        if operation == self.failed_operation and not self.revoke:
            return SimpleNamespace(returncode=1, stdout=b"", stderr=b"private-hook-token")
        allowed = not (operation == self.failed_operation and self.revoke)
        reply = {
            **request, "status": "authorized" if allowed else "ready", "allowed": allowed,
            "healthy": True, "epoch_unchanged": True, "receipt_binding_verified": True,
            "restart_performed": False, "binary_replaced": False, "ha_authority_grant": False,
            "host_guard_sha256": "d" * 64, "container_guard_sha256": "d" * 64,
        }
        return SimpleNamespace(returncode=0, stdout=json.dumps(reply).encode(), stderr=b"")

    def fake_request(self, path, credential, *, method="GET", data=None):
        self.assertEqual(credential, "fixture-no-network-token")
        self.events.append(method)
        if method == "POST":
            self.assertEqual(path, local.API_ROOT + "/releases")
            self.assertIsNone(self.remote_draft)
            self.assertIs(data["draft"], True)
            self.remote_draft = {**data, "id": 1234}
        elif method == "GET":
            self.assertEqual(path, local.API_ROOT + "/releases/1234")
            self.assertIsNotNone(self.remote_draft)
        elif method == "PATCH":
            self.assertEqual(path, local.API_ROOT + "/releases/1234")
            self.assertIsNotNone(self.remote_draft)
            self.remote_draft.update(data)
        else:
            self.fail("unexpected release mutation: " + method)
        return dict(self.remote_draft)

    def test_prepare_failure_blocks_draft_creation_and_upload(self):
        self.failed_operation = "prepare"
        with self.assertRaisesRegex(RuntimeError, "startup_guard_authorization_failed"):
            local.main()
        self.assertEqual(self.events, ["prepare"])
        self.request.assert_not_called()
        self.upload.assert_not_called()
        self.verify_assets.assert_not_called()
        self.assertIsNone(self.remote_draft)
        self.assertNotIn("private-hook-token", self.stdout.getvalue() + self.stderr.getvalue())

    def test_check_failure_preserves_draft_without_publication_patch(self):
        self.failed_operation = "check"
        with self.assertRaisesRegex(RuntimeError, "startup_guard_authorization_failed"):
            local.main()
        self.assertEqual(self.events, ["prepare", "POST", "upload", "GET", "check"])
        self.assertIs(self.remote_draft["draft"], True)
        self.upload.assert_called_once()
        self.verify_assets.assert_called_once()
        self.assertNotIn("private-hook-token", self.stdout.getvalue() + self.stderr.getvalue())

    def test_revoked_approval_preserves_draft_without_publication_patch(self):
        self.failed_operation, self.revoke = "check", True
        with self.assertRaisesRegex(RuntimeError, "startup_guard_candidate_not_authorized"):
            local.main()
        self.assertEqual(self.events, ["prepare", "POST", "upload", "GET", "check"])
        self.assertIs(self.remote_draft["draft"], True)

    def test_positive_control_requires_both_gates_before_publication(self):
        self.assertEqual(local.main(), 0)
        self.assertEqual(self.events, ["prepare", "POST", "upload", "GET", "check", "PATCH"])
        self.assertIs(self.remote_draft["draft"], False)
        self.assertEqual(self.remote_draft["make_latest"], "true")


class CorrespondingSourceTests(unittest.TestCase):
    def test_combined_release_requires_source_without_changing_old_contract(self):
        source = local.control.corresponding_source
        self.assertFalse(source.declaration({"version": "0.2.19-pk.60"}))
        for version in ("0.2.19-pk.61", "0.2.20-pk.1"):
            with self.subTest(version=version), self.assertRaisesRegex(RuntimeError, "requires complete"):
                source.declaration({"version": version})

    def test_source_roundtrip_and_foreign_tree_rejection(self):
        source = local.control.corresponding_source
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, output = root / "repo", root / "output"
            repo.mkdir()
            output.mkdir()
            def git(*args):
                return subprocess.check_output(["git", *args], cwd=repo, stderr=subprocess.DEVNULL).decode().strip()
            git("init")
            git("config", "core.autocrlf", "true")
            for name in ("THIRD_PARTY_NOTICES.md", "LICENSES/BEpusdt-GPL-3.0.txt", "README.md",
                         "backend/go.mod", "frontend/pnpm-lock.yaml", "backend/third_party/bepusdt/README.sub2api.md"):
                file = repo / name
                file.parent.mkdir(parents=True, exist_ok=True)
                file.write_text("offline source fixture\n")
            git("add", ".")
            git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "source fixture")
            data = {"version": "0.2.19-pk.61", "commit": git("rev-parse", "HEAD"), "tree": git("rev-parse", "HEAD^{tree}")}
            source.create(output, data, repo)
            source.verify(output, data, checkout=repo)
            with self.assertRaisesRegex(RuntimeError, "Never overwrite"):
                source.create(output, data, repo)
            wrong = dict(data, tree="f" * 40)
            with self.assertRaisesRegex(RuntimeError, "tree differs"):
                source.verify(output, wrong, checkout=repo)
            archive = output / data["source_archive"]
            archive.write_bytes(archive.read_bytes() + b"tampered")
            with self.assertRaisesRegex(RuntimeError, "checksum mismatch"):
                source.verify(output, data, checkout=repo)

    def test_git_archive_attributes_cannot_omit_or_rewrite_corresponding_source(self):
        source = local.control.corresponding_source
        for attribute, content in (("export-ignore", "required implementation\n"),
                                   ("export-subst", "$Format:%H$\n")):
            with self.subTest(attribute=attribute), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                repo, output = root / "repo", root / "output"
                repo.mkdir()
                output.mkdir()
                def git(*args):
                    return subprocess.check_output(["git", *args], cwd=repo, stderr=subprocess.DEVNULL).decode().strip()
                git("init")
                for name in ("THIRD_PARTY_NOTICES.md", "LICENSES/BEpusdt-GPL-3.0.txt", "README.md",
                             "backend/go.mod", "frontend/pnpm-lock.yaml", "backend/third_party/bepusdt/README.sub2api.md"):
                    file = repo / name
                    file.parent.mkdir(parents=True, exist_ok=True)
                    file.write_text("offline source fixture\n")
                (repo / "backend/implementation.go").write_text(content)
                (repo / ".gitattributes").write_text("backend/implementation.go " + attribute + "\n")
                git("add", ".")
                git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "source fixture")
                data = {"version": "0.2.19-pk.61", "commit": git("rev-parse", "HEAD"), "tree": git("rev-parse", "HEAD^{tree}")}
                with self.assertRaisesRegex(RuntimeError, "complete release Git tree"):
                    source.create(output, data, repo)


if __name__ == "__main__":
    unittest.main()
