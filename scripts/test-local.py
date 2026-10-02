#!/usr/bin/env python3
"""Run the former CI checks on the developer's Linux/WSL computer.

No application release is compiled here. Go may compile test binaries. Docker
creates one disposable seed-change fixture; it is never published.
"""

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile
import uuid


ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = "pengkang0322/sub2api"
SUITES = (
    "backend-unit", "backend-integration", "backend-race", "backend-lint",
    "frontend", "shell", "binance-proxy", "release-helpers", "security",
    "artifact-runtime",
)
RACE_PATTERN = (
    "Test(PKCyber|PKEarly|PKFirstResponse|PKUsage|PKSystemOne|PKCacheConversion|"
    "PKChannelMonitor|StreamEarlyHeartbeat|UpdateSettingsEarlyHeartbeat|"
    "ServerTiming|Inflight|EstimateInflight|ReserveInflight|WrapUsageRecord|"
    "SyncBalanceCacheAfterDeduction|RequestMaxOutputTokens|WebSearch_RejectsWhenInflight)"
)


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True).strip()


def sha256(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


class Runner:
    def __init__(self, output):
        self.output = output
        # Inherit tool locations, local Docker access, caches and download
        # settings only. Do not copy application configuration or live keys.
        # In particular PROMPT_AUDIT_TEST_POSTGRES_DSN enables untagged tests
        # that TRUNCATE the supplied database; PROMPT_AUDIT_TEST_REDIS_ADDR
        # and TEST_REDIS_URL also connect directly to externally supplied data
        # services. Those variables must never enter this standard profile.
        tool_environment = {
            "PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP",
            "LANG", "LANGUAGE", "TZ", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME",
            "XDG_CACHE_HOME", "XDG_DATA_HOME", "DOCKER_HOST", "DOCKER_CONTEXT",
            "DOCKER_CONFIG", "DOCKER_API_VERSION", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH",
            "GOPATH", "GOCACHE", "GOMODCACHE", "GOPROXY", "GOSUMDB", "GOPRIVATE",
            "GONOPROXY", "GONOSUMDB", "SSL_CERT_FILE", "SSL_CERT_DIR",
            "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
            "http_proxy", "https_proxy", "all_proxy", "no_proxy",
        }
        self.env = {key: value for key, value in os.environ.items()
                    if key in tool_environment or key.startswith("LC_")}
        # Ignore go env -w and ancestor go.work files; prevent inherited flags
        # from enabling e2e/live tags, selecting tests, or replacing the toolchain.
        self.env.update(CI="true", CGO_ENABLED="1", PYTHONDONTWRITEBYTECODE="1",
                        GOENV="off", GOWORK="off", GOFLAGS="", GOTOOLCHAIN="local",
                        GOOS="linux", GOARCH="amd64", GOAMD64="v1", CC="gcc", CXX="g++")
        self.results = []
        self.current = None
        self.log = None
        self.artifact = None

    def run(self, args, cwd=ROOT, *, env=None, stdin=None, allowed=(0,), go_json=False, required=()):
        command = shlex.join(str(value) for value in args)
        self.current["commands"].append(command)
        print("+ " + command, flush=True)
        self.log.write("+ " + command + "\n")
        process = subprocess.Popen(
            args, cwd=cwd, env={**self.env, **(env or {})},
            stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
        if stdin is not None:
            process.stdin.write(stdin)
            process.stdin.close()
        output = []
        passed = set()
        skipped = set()
        for line in process.stdout:
            output.append(line)
            self.log.write(line)
            self.log.flush()
            print(line, end="", flush=True)
            if go_json:
                try:
                    event = json.loads(line)
                except ValueError:
                    continue
                if event.get("Test"):
                    if event.get("Action") == "pass":
                        passed.add(event["Test"])
                    elif event.get("Action") == "skip":
                        skipped.add(event["Test"])
        code = process.wait()
        if code not in allowed:
            raise RuntimeError(f"Command exited {code}: {command}")
        if go_json:
            self.current.setdefault("skipped_tests", []).extend(sorted(skipped))
            if not passed:
                raise RuntimeError("Go command did not execute any passing tests")
            protected_skips = sorted(name for name in skipped if name.startswith("TestPK"))
            if protected_skips:
                raise RuntimeError("Protected tests were skipped: " + ", ".join(protected_skips))
            for prefix in required:
                if not any(name.startswith(prefix) for name in passed):
                    raise RuntimeError("Required test did not pass: " + prefix)
        return "".join(output)

    def suite(self, name, callback):
        self.current = {"name": name, "status": "failed", "exit_code": 1,
                        "commands": [], "started_at": now(), "log": name + ".log"}
        self.results.append(self.current)
        with (self.output / self.current["log"]).open("w", encoding="utf-8") as self.log:
            try:
                callback()
                self.current.update(status="passed", exit_code=0)
            except Exception as error:
                self.current["error"] = str(error)
                self.log.write("\nERROR: " + str(error) + "\n")
                print(f"{name}: FAILED: {error}", file=sys.stderr, flush=True)
            finally:
                self.current["finished_at"] = now()
        return self.current["status"] == "passed"

    def preflight(self):
        if sys.platform != "linux":
            raise RuntimeError("Use local Linux or WSL 2, not native Windows or a remote test server")
        if sys.version_info < (3, 12):
            raise RuntimeError("Python 3.12+ is required")
        for tool in ("git", "go", "node", "pnpm", "gcc", "docker", "make", "bash", "sh",
                     "golangci-lint", "govulncheck"):
            if not shutil.which(tool):
                raise RuntimeError(f"Missing local dependency: {tool}; see PK_MAINTENANCE.md")
        versions = (
            (["go", "version"], r"\bgo1\.27\.0\b", "Go 1.27.0"),
            (["node", "--version"], r"^v24\.", "Node.js 24"),
            (["pnpm", "--version"], r"^9\.", "pnpm 9"),
            (["golangci-lint", "version"], r"\b2\.13\.", "golangci-lint 2.13.x"),
        )
        for command, pattern, label in versions:
            if not re.search(pattern, self.run(command)):
                raise RuntimeError("Required tool version: " + label)
        self.run([sys.executable, "-c", "import yaml; assert yaml.__version__ == '6.0.3'"])
        self.run(["govulncheck", "-version"])
        docker_host = self.env.get("DOCKER_HOST", "")
        if docker_host and not docker_host.startswith("unix://"):
            raise RuntimeError("DOCKER_HOST must use a local Unix socket; remote Docker is not local validation")
        context = self.run(["docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}"]).strip()
        if not context.startswith("unix://"):
            raise RuntimeError("Docker context must use the local Linux/WSL Unix socket")
        info = json.loads(self.run(["docker", "info", "--format", "{{json .}}"]))
        if info.get("OSType") != "linux" or info.get("Architecture") not in ("x86_64", "amd64"):
            raise RuntimeError("Local Docker must run Linux amd64 containers")
        # Pin the verified local endpoint for Testcontainers too, so a user's
        # ~/.testcontainers.properties cannot select a different Docker host.
        self.env["DOCKER_HOST"] = context if self.env.get("DOCKER_CONTEXT") else (docker_host or context)
        self.run(["docker", "compose", "version"])
        # The repository harness supplies isolated PostgreSQL/Redis containers.
        # CI=true makes missing Docker fail rather than silently skip integration.
        if git("status", "--porcelain=v1", "--untracked-files=all"):
            raise RuntimeError("Commit source changes first; a clean checkout is required for SHA-bound evidence")

    def go(self, args, *, env=None, required=()):
        self.run(["go", "test", "-json", "-count=1", *args], ROOT / "backend",
                 env=env, go_json=True, required=required)

    def race(self):
        self.go(["-race", "-tags", "unit", "./internal/service", "./internal/handler",
                 "./internal/handler/dto", "./internal/handler/admin", "./internal/repository",
                 "./internal/server", "./internal/server/middleware", "-run", RACE_PATTERN],
                required=("TestPKEarly", "TestPKCacheConversion", "TestPKCyber"))
        self.go(["-race", "-tags", "unit", "./internal/service", "-run",
                 "Test(BackgroundStartGate|BatchImageCleanupLifecycle)"])
        self.go(["-race", "-tags", "unit", "./internal/config", "./internal/payment",
                 "./internal/service", "./internal/handler", "-run", "TestPKBinance"],
                required=("TestPKBinance",))
        # Preserve both the original harness default and the explicitly PG15
        # candidate check for Binance; automatic-disable coverage requires PG15.
        self.go(["-race", "-tags", "integration", "./internal/repository", "-run", "TestPKBinance"],
                required=("TestPKBinance",))
        self.go(["-race", "-tags", "integration", "./internal/repository", "-run", "TestPK(Binance|Cyber)"],
                env={"SUB2API_TEST_POSTGRES_IMAGE": "postgres:15-alpine"},
                required=("TestPKBinance", "TestPKCyber"))

    def frontend(self):
        self.run(["pnpm", "install", "--frozen-lockfile"], ROOT / "frontend")
        self.run(["make", "test-frontend"])

    def shell(self):
        self.run(["sh", "-n", "deploy/docker-entrypoint.sh"])
        self.run(["bash", "-n", "deploy/install.sh", "deploy/docker-deploy.sh", "deploy/apple-container.sh"])
        # The existing Apple fake-runtime checks use two macOS CLI interfaces.
        # Adapt only their real stat/JSON semantics; never stub a success result.
        # These temporary adapters are deleted even when an assertion fails.
        with tempfile.TemporaryDirectory(prefix="pk-apple-cli-") as directory:
            adapter = Path(directory)
            stat_code = '''import os, stat, sys
if len(sys.argv) != 4 or sys.argv[1] != "-f" or sys.argv[2] not in ("%u", "%Lp"):
    raise SystemExit("unsupported BSD stat invocation")
value = os.stat(sys.argv[3])
print(value.st_uid if sys.argv[2] == "%u" else format(stat.S_IMODE(value.st_mode), "o"))
'''
            plutil_code = '''import json, sys
if len(sys.argv) != 7 or sys.argv[1] != "-extract" or sys.argv[3] not in ("raw", "json") or sys.argv[4:] != ["-o", "-", "-"]:
    raise SystemExit("unsupported plutil invocation")
value = json.load(sys.stdin)
for key in sys.argv[2].split("."):
    value = value[int(key)] if isinstance(value, list) else value[key]
print(json.dumps(value, separators=(",", ":")) if sys.argv[3] == "json" else value)
'''
            for name, code in (("stat", stat_code), ("plutil", plutil_code)):
                path = adapter / name
                path.write_text("#!" + sys.executable + "\n" + code, encoding="utf-8")
                path.chmod(0o700)
            self.run(["bash", "deploy/tests/apple-container-test.sh"],
                     env={"PATH": str(adapter) + os.pathsep + self.env["PATH"]})
        for shell, script in (
            ("bash", "deploy/tests/install-github-token-test.sh"),
            ("sh", "deploy/tests/docker-compose-simple-mode-env-test.sh"),
            ("sh", "deploy/tests/docker-compose-security-test.sh"),
            ("sh", "deploy/tests/docker-compose-gateway-env-test.sh"),
            ("sh", "deploy/tests/docker-runtime-resources-test.sh"),
            ("sh", "deploy/test-caddyfile-cache.sh"),
        ):
            self.run([shell, script])

    def release_helpers(self):
        self.run([sys.executable, "-B", "-m", "unittest", "discover", "-s",
                  ".github/release-tools", "-p", "test_release_matrix.py"])
        self.run(["bash", "-n", ".github/release-tools/release-images.sh"])

    def security(self):
        self.run(["govulncheck", "./..."], ROOT / "backend")
        audit = self.run(["pnpm", "audit", "--prod", "--audit-level=high", "--json"],
                         ROOT / "frontend", allowed=(0, 1))
        # Only the machine-readable report is temporary; keep the original log.
        with tempfile.TemporaryDirectory(prefix="pk-audit-") as directory:
            path = Path(directory) / "audit.json"
            path.write_text(audit, encoding="utf-8")
            self.run([sys.executable, "tools/check_pnpm_audit_exceptions.py", "--audit", str(path),
                      "--exceptions", ".github/audit-exceptions.yml"])

    def runtime(self, directory, commit, tree):
        manifest = json.loads((directory / "manifest.json").read_text(encoding="utf-8"))
        for key, expected in (("schema_version", 1), ("repository", REPOSITORY),
                              ("commit", commit), ("tree", tree), ("platform", "linux/amd64")):
            if manifest.get(key) != expected:
                raise RuntimeError("Artifact manifest mismatch: " + key)
        for key in ("binary_archive", "image_archive"):
            name = manifest[key]
            if Path(name).name != name or name in ("", ".", ".."):
                raise RuntimeError("Invalid archive filename in manifest")
            if sha256(directory / name) != manifest[key + "_sha256"]:
                raise RuntimeError("Artifact hash mismatch: " + key)
        with tarfile.open(directory / manifest["binary_archive"], "r:gz") as archive:
            members = archive.getmembers()
            if len(members) != 1 or members[0].name not in ("sub2api", "./sub2api") or not members[0].isfile():
                raise RuntimeError("Unexpected binary archive layout")
            if hashlib.file_digest(archive.extractfile(members[0]), "sha256").hexdigest() != manifest["binary_sha256"]:
                raise RuntimeError("Binary hash does not match manifest")
        image = manifest["image_tag"]
        if image != "ghcr.io/" + REPOSITORY + ":" + manifest["version"]:
            raise RuntimeError("Artifact image tag does not match private repository/version")
        self.run(["docker", "load", "-i", str(directory / manifest["image_archive"])])
        image_id = self.run(["docker", "image", "inspect", "--format", "{{.Id}}", image]).strip()
        if image_id != manifest["image_id"]:
            raise RuntimeError("Loaded image ID does not match manifest")
        packaged_hash = self.run(["docker", "run", "--rm", "--entrypoint", "sha256sum", image, "/app/sub2api"]).split()[0]
        if packaged_hash != manifest["binary_sha256"]:
            raise RuntimeError("Image does not contain the exact archive binary")
        suffix = uuid.uuid4().hex
        volume = "pk-local-runtime-" + suffix
        fixture = "pk-local-seed:" + suffix
        self.run(["docker", "volume", "create", volume])
        def container(*args, tag=image):
            return self.run(["docker", "run", "--rm", "-v", volume + ":/app/data", tag, *args])
        def version(tag=image):
            if manifest["version"] not in container("-version", tag=tag):
                raise RuntimeError("Runtime version mismatch")
        try:
            version()
            version()
            container("sh", "-ec", 'test "$(id -u)" != 0; test -x /app/data/bin/sub2api; '
                      'test -w /app/data/bin; test ! -w /app/sub2api; '
                      'test -s /app/data/bin/.image-seed.sha256; cmp /app/sub2api /app/data/bin/sub2api')
            updated = container("sh", "-ec", 'printf "\\nPK_LOCAL_UPDATED\\n" >> /app/data/bin/sub2api; '
                                'sha256sum /app/data/bin/sub2api').strip()
            version()
            if container("sha256sum", "/app/data/bin/sub2api").strip() != updated:
                raise RuntimeError("Same seed overwrote a web update")
            container("mv", "/app/data/bin/sub2api", "/app/data/bin/sub2api.backup")
            version()
            if container("sha256sum", "/app/data/bin/sub2api").strip() != updated:
                raise RuntimeError("Interrupted replacement did not recover backup")
            # A disposable test fixture only: no frontend/server build occurs.
            dockerfile = (f"FROM {image}\nRUN chmod 0755 /app/sub2api && "
                          'printf "PK_LOCAL_IMAGE" >> /app/sub2api && chmod 0555 /app/sub2api\n')
            self.run(["docker", "build", "--network=none", "-t", fixture, "-"], stdin=dockerfile)
            version(fixture)
            container("cmp", "/app/sub2api", "/app/data/bin/sub2api", tag=fixture)
            version()
            container("cmp", "/app/sub2api", "/app/data/bin/sub2api")
            self.artifact = {key: manifest[key] for key in
                             ("package_id", "binary_sha256", "binary_archive_sha256",
                              "image_archive_sha256", "image_id", "version")}
        finally:
            # Names are random and belong only to this invocation.
            self.run(["docker", "image", "rm", "-f", fixture], allowed=(0, 1))
            self.run(["docker", "volume", "rm", volume])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifact-dir", type=Path, help="Downloaded Jenkins package containing manifest.json")
    parser.add_argument("--output-dir", type=Path, help="New output directory outside the source checkout")
    args = parser.parse_args()
    output = (args.output_dir or Path(tempfile.gettempdir()) / "sub2api-local-validation" /
              (dt.datetime.now().strftime("%Y%m%d-%H%M%S") + "-" + uuid.uuid4().hex[:8])).resolve()
    if output == ROOT or ROOT in output.parents:
        parser.error("Output must be outside the repository so it cannot change the tested tree")
    output.mkdir(parents=True, exist_ok=False)
    os.chdir(ROOT)
    commit, tree = git("rev-parse", "HEAD"), git("rev-parse", "HEAD^{tree}")
    report = {"schema_version": 1, "tests_profile": "pk-local-v1", "repository": REPOSITORY,
              "commit": commit, "tree": tree, "status": "failed", "started_at": now()}
    runner = Runner(output)
    try:
        ready = runner.suite("preflight", runner.preflight)
        if ready:
            runner.suite("backend-unit", lambda: runner.go(["-tags", "unit", "./..."]))
            runner.suite("backend-integration", lambda: runner.go(["-tags", "integration", "./..."],
                         required=("TestPKUsageFirstResponsePersistence",)))
            runner.suite("backend-race", runner.race)
            runner.suite("backend-lint", lambda: runner.run(["golangci-lint", "run", "--timeout=30m"], ROOT / "backend"))
            runner.suite("frontend", runner.frontend)
            runner.suite("shell", runner.shell)
            runner.suite("binance-proxy", lambda: runner.run([sys.executable, "-B", "-c", GATEWAY_CHECKS]))
            runner.suite("release-helpers", runner.release_helpers)
            runner.suite("security", runner.security)
            if args.artifact_dir:
                runner.suite("artifact-runtime", lambda: runner.runtime(args.artifact_dir.resolve(), commit, tree))
            else:
                runner.results.append({"name": "artifact-runtime", "status": "skipped", "exit_code": None,
                                       "commands": [], "reason": "No --artifact-dir; not eligible for publication"})
            if git("rev-parse", "HEAD") != commit or git("rev-parse", "HEAD^{tree}") != tree or git("status", "--porcelain=v1", "--untracked-files=all"):
                report["error"] = "Source changed during validation; evidence cannot be used"
            elif all(item["status"] == "passed" or
                     (item["name"] == "artifact-runtime" and item["status"] == "skipped") for item in runner.results):
                report["status"] = "passed"
    finally:
        report.update(finished_at=now(), suites=runner.results, artifact=runner.artifact)
        report["publish_eligible"] = report["status"] == "passed" and runner.artifact is not None
        (output / "local-validation.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
        print("Evidence: " + str(output / "local-validation.json"))
        print("Status: " + report["status"] + "; publish_eligible=" + str(report["publish_eligible"]))
    return 0 if report["status"] == "passed" else 1


# Existing gateway assertions are relocated verbatim from backend-ci.yml.
# Kept inline and executed with python -B -c: no generated test file survives.
GATEWAY_CHECKS = r"""
import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import socket
import ssl
import tempfile
import threading
import time
from types import SimpleNamespace
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("binance_gateway", "deploy/binance-proxy/gateway.py")
gateway = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gateway)
AUTH = "Basic " + base64.b64encode(b"ci-user:ci-password-with-32-characters!!").decode()
PRINCIPAL = hashlib.sha256(base64.b64decode(AUTH.split()[1])).digest()
NODES = (("hk-a", "127.0.0.1", 18001), ("hk-b", "127.0.0.1", 18002))


class MemorySocket:
    def __init__(self, data=b""):
        self.data = bytearray(data)
        self.sent = bytearray()
        self.closed = False
        self.timeouts = []

    def settimeout(self, value):
        self.timeouts.append(value)

    def recv(self, size):
        chunk = bytes(self.data[:size])
        del self.data[:size]
        return chunk

    def sendall(self, data):
        self.sent.extend(data)

    def close(self):
        self.closed = True

    def do_handshake(self):
        pass


def response(body, status=200):
    return ("HTTP/1.1 %d Result\r\nContent-Length: %d\r\n\r\n" % (status, len(body))).encode() + body


class GatewayChecks(unittest.TestCase):
    def setUp(self):
        self.state = gateway.GatewayState(PRINCIPAL, NODES)
        self.addCleanup(self.state.executor.shutdown, wait=True)
        self.addCleanup(self.state.close_all)
        # No test may resolve or open a real network connection.
        for name in ("create_connection", "socket", "getaddrinfo"):
            patcher = mock.patch.object(gateway.socket, name, side_effect=AssertionError("unexpected network"))
            patcher.start()
            self.addCleanup(patcher.stop)

    def request(self, method, path, headers=None, prefix=b""):
        values = {"Host": gateway.TARGET}
        values.update(headers or {})
        raw = (method + " " + path + " HTTP/1.1\r\n" + "".join(k + ": " + v + "\r\n" for k, v in values.items()) + "\r\n").encode() + prefix
        sock = MemorySocket(raw)
        gateway.GatewayHandler(sock, ("127.0.0.1", 1), SimpleNamespace(state=self.state))
        head, body = bytes(sock.sent).split(b"\r\n\r\n", 1)
        return int(head.split()[1]), json.loads(body) if body else None

    def test_authentication_and_no_unchecked_tunnel(self):
        with mock.patch.object(self.state, "upstream") as upstream, mock.patch.object(self.state, "preflight") as preflight:
            for auth in (None, "Basic !!!!", "Bearer invalid", "Basic " + base64.b64encode(b"wrong:password").decode()):
                headers = {} if auth is None else {"Authorization": auth, "Proxy-Authorization": auth}
                self.assertEqual(self.request("POST", "/_binance/preflight", headers)[0], 401)
                self.assertEqual(self.request("CONNECT", gateway.TARGET, headers)[0], 407)
            self.assertEqual(self.request("GET", "/health")[0], 401)
            self.assertEqual(self.request("GET", "/health", {"Authorization": AUTH})[0], 200)
            self.assertEqual(self.request("CONNECT", gateway.TARGET, {"Proxy-Authorization": AUTH})[0], 403)
            upstream.assert_not_called()
            preflight.assert_not_called()

    def test_only_exact_binance_connect_destination(self):
        token = self.state.issue(NODES[0])
        headers = {"Proxy-Authorization": AUTH, "X-Binance-Route-Token": token}
        with mock.patch.object(self.state, "upstream") as upstream:
            for target in ("example.com:443", "api.binance.com:80", "api.binance.com:443.evil", "api.binance.com.:443", "127.0.0.1:443", "www.cloudflare.com:443"):
                self.assertEqual(self.request("CONNECT", target, headers)[0], 403)
            self.assertEqual(self.request("CONNECT", gateway.TARGET, dict(headers, Host="evil.example:443"))[0], 403)
            self.assertEqual(self.request("GET", "https://api.binance.com/api/v3/time", {"Authorization": AUTH})[0], 404)
            upstream.assert_not_called()

    def test_rejects_duplicate_headers_and_request_bodies(self):
        with mock.patch.object(self.state, "preflight") as preflight:
            self.assertEqual(self.request("POST", "/_binance/preflight", {"Authorization": AUTH, "authorization": AUTH})[0], 400)
            for extra in ({"Content-Length": "1"}, {"Transfer-Encoding": "chunked"}, {"Expect": "100-continue"}):
                self.assertEqual(self.request("POST", "/_binance/preflight", dict(extra, Authorization=AUTH))[0], 400)
            self.assertEqual(self.request("POST", "/_binance/preflight", {"Authorization": AUTH}, b"body")[0], 400)
            preflight.assert_not_called()

    def test_preflight_failures_and_capacity_fail_closed(self):
        with mock.patch.object(self.state, "probe", return_value=None):
            self.assertEqual(self.request("POST", "/_binance/preflight", {"Authorization": AUTH}), (503, {"error": "no_available_node"}))
        self.assertEqual(len(self.state.tokens), 0)
        for _ in range(gateway.MAX_PREFLIGHTS):
            self.assertTrue(self.state.preflights.acquire(blocking=False))
        try:
            with mock.patch.object(self.state, "preflight") as preflight:
                self.assertEqual(self.request("POST", "/_binance/preflight", {"Authorization": AUTH})[0], 429)
                preflight.assert_not_called()
        finally:
            for _ in range(gateway.MAX_PREFLIGHTS):
                self.state.preflights.release()

    def test_fastest_route_is_fixed_for_repeated_connects(self):
        with mock.patch.object(self.state, "probe", side_effect=lambda node, deadline: (0.3 if node == NODES[0] else 0.1, node)):
            status, data = self.request("POST", "/_binance/preflight", {"Authorization": AUTH})
        self.assertEqual(status, 200)
        token = data["route_token"]
        self.assertEqual(self.state.lookup(token), NODES[1])
        with mock.patch.object(self.state, "probe", side_effect=lambda node, deadline: (0.1 if node == NODES[0] else 0.3, node)):
            later = self.state.preflight()
        self.assertEqual(self.state.lookup(later), NODES[0])
        headers = {"Proxy-Authorization": AUTH, "X-Binance-Route-Token": token}
        with mock.patch.object(self.state, "upstream", side_effect=lambda *args: MemorySocket()) as upstream, mock.patch.object(gateway, "relay") as relay:
            for _ in range(2):
                self.assertEqual(self.request("CONNECT", gateway.TARGET, headers, b"opaque-tls")[0], 200)
            self.assertEqual([call.args[0] for call in upstream.call_args_list], [NODES[1], NODES[1]])
            self.assertEqual(relay.call_args.args[2], b"opaque-tls")
        with mock.patch.object(self.state, "upstream", side_effect=OSError("selected node failed")) as upstream:
            self.assertEqual(self.request("CONNECT", gateway.TARGET, headers), (502, {"error": "selected_node_unavailable"}))
            upstream.assert_called_once()
            self.assertEqual(upstream.call_args.args[0], NODES[1])
        self.assertEqual(self.state.lookup(token), NODES[1])
        self.assertIsNone(self.state.lookup("unknown-token-" + "x" * 32))
        with mock.patch.object(gateway, "TOKEN_TTL", -1):
            expired = self.state.issue(NODES[0])
        self.assertIsNone(self.state.lookup(expired))

    def test_hong_kong_and_binance_must_pass_same_node_deadline(self):
        with mock.patch.object(self.state, "probe_response", side_effect=[b"ip=192.0.2.1\nloc=HK\n", b'{"serverTime":1234}']) as probe:
            self.assertEqual(self.state.probe(NODES[0], time.monotonic() + 10)[1], NODES[0])
        calls = probe.call_args_list
        self.assertEqual([call.args[:3] for call in calls], [(NODES[0], "www.cloudflare.com", "/cdn-cgi/trace"), (NODES[0], "api.binance.com", "/api/v3/time")])
        self.assertEqual(calls[0].args[3], calls[1].args[3])
        self.assertLessEqual(calls[0].args[3], time.monotonic() + gateway.NODE_TIMEOUT)
        for trace in (b"loc=US\n", b"loc=HK\nloc=HK\n", b"ip=192.0.2.1\n"):
            with mock.patch.object(self.state, "probe_response", return_value=trace) as probe:
                self.assertIsNone(self.state.probe(NODES[0], time.monotonic() + 10))
                probe.assert_called_once()
        for body in (b"invalid", b'{"serverTime":true}', b'{"serverTime":0}', b'{"serverTime":"123"}'):
            with mock.patch.object(self.state, "probe_response", side_effect=[b"loc=HK\n", body]):
                self.assertIsNone(self.state.probe(NODES[0], time.monotonic() + 10))
        with mock.patch.object(self.state, "probe_response", side_effect=socket.timeout("timeout")):
            self.assertIsNone(self.state.probe(NODES[0], time.monotonic() + 10))

    def test_overall_preflight_timeout_does_not_issue_route(self):
        release = threading.Event()
        def blocked_probe(node, deadline):
            release.wait(1)
            return None
        try:
            with mock.patch.object(self.state, "probe", side_effect=blocked_probe), mock.patch.object(gateway, "PREFLIGHT_TIMEOUT", 0.02):
                started = time.monotonic()
                with self.assertRaises(gateway.GatewayError):
                    self.state.preflight()
                self.assertLess(time.monotonic() - started, 0.8)
                self.assertEqual(len(self.state.tokens), 0)
        finally:
            release.set()

    def test_upstream_only_dials_fixed_listener_without_fallback(self):
        for target in (gateway.TARGET, "www.cloudflare.com:443"):
            sock = MemorySocket(b"HTTP/1.1 200 OK\r\n\r\n")
            with mock.patch.object(gateway.socket, "create_connection", return_value=sock) as dial:
                self.assertIs(self.state.upstream(NODES[1], time.monotonic() + 1, target), sock)
                self.assertEqual(dial.call_args.args[0], ("127.0.0.1", 18002))
                self.assertIn(("CONNECT " + target + " HTTP/1.1").encode(), sock.sent)
                self.assertNotIn(b"Authorization", sock.sent)
            self.state.close(sock)
        with mock.patch.object(gateway.socket, "create_connection") as dial:
            with self.assertRaises(gateway.GatewayError):
                self.state.upstream(NODES[0], time.monotonic() + 1, "evil.example:443")
            dial.assert_not_called()
        for error in (OSError("failed"), socket.timeout("timeout")):
            with mock.patch.object(gateway.socket, "create_connection", side_effect=error) as dial:
                with self.assertRaises(OSError):
                    self.state.upstream(NODES[0], time.monotonic() + 1)
                dial.assert_called_once()
                self.assertEqual(dial.call_args.args[0], ("127.0.0.1", 18001))
        for data in (b"HTTP/1.1 403 Forbidden\r\n\r\n", b"HTTP/1.1 200 OK\r\nContent-Length: 1\r\n\r\nx"):
            sock = MemorySocket(data)
            with mock.patch.object(gateway.socket, "create_connection", return_value=sock):
                with self.assertRaises(gateway.GatewayError):
                    self.state.upstream(NODES[0], time.monotonic() + 1)
            self.assertTrue(sock.closed)

    def test_probe_tls_verification_response_errors_and_timeout(self):
        self.assertTrue(self.state.tls.check_hostname)
        self.assertEqual(self.state.tls.verify_mode, ssl.CERT_REQUIRED)
        self.assertGreaterEqual(self.state.tls.minimum_version, ssl.TLSVersion.TLSv1_2)
        for status in (200, 403, 429, 500):
            plain, wrapped = MemorySocket(), MemorySocket(response(b'{"serverTime":1234}', status))
            tls = mock.Mock()
            tls.wrap_socket.return_value = wrapped
            with mock.patch.object(self.state, "upstream", return_value=plain), mock.patch.object(self.state, "tls", tls):
                if status == 200:
                    self.assertEqual(self.state.probe_response(NODES[0], "api.binance.com", "/api/v3/time", time.monotonic() + 1), b'{"serverTime":1234}')
                else:
                    with self.assertRaises(gateway.GatewayError):
                        self.state.probe_response(NODES[0], "api.binance.com", "/api/v3/time", time.monotonic() + 1)
                tls.wrap_socket.assert_called_once_with(plain, server_hostname="api.binance.com", do_handshake_on_connect=False)
            self.assertTrue(wrapped.closed)
        plain = MemorySocket()
        tls = mock.Mock()
        tls.wrap_socket.side_effect = ssl.SSLCertVerificationError("untrusted certificate")
        with mock.patch.object(self.state, "upstream", return_value=plain), mock.patch.object(self.state, "tls", tls):
            self.assertIsNone(self.state.probe(NODES[0], time.monotonic() + 1))
        self.assertTrue(plain.closed)
        stalled = MemorySocket()
        stalled.recv = mock.Mock(side_effect=socket.timeout("timeout"))
        with self.assertRaises(socket.timeout):
            gateway.read_headers(stalled, time.monotonic() + 1)
        with self.assertRaises(gateway.GatewayError):
            gateway.read_probe_body(MemorySocket(b"x"), {"content-length": "2"}, b"", time.monotonic() + 1)
        with self.assertRaises(gateway.GatewayError):
            gateway.read_probe_body(MemorySocket(), {"content-length": "9000"}, b"", time.monotonic() + 1)

    def test_configuration_cannot_add_public_or_dynamic_nodes(self):
        base = {"bind": "127.0.0.1", "port": 17990, "auth_username": "ci-user", "auth_password": "ci-password-with-32-characters!!", "nodes": [{"name": "hk-a", "host": "127.0.0.1", "port": 18001}]}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "gateway.json"
            def load(config, mode=0o600):
                path.write_text(json.dumps(config), encoding="utf-8")
                path.chmod(mode)
                return gateway.load_config(str(path))
            self.assertEqual(load(base)[3], (NODES[0],))
            for changed in (dict(base, bind="0.0.0.0"), dict(base, bind="8.8.8.8"), dict(base, auth_password="short"), dict(base, nodes=[]), dict(base, nodes=base["nodes"] * 2)):
                with self.assertRaises(gateway.GatewayError):
                    load(changed)
            for host in ("example.com", "8.8.8.8", "10.0.0.1"):
                with self.assertRaises((gateway.GatewayError, ValueError)):
                    load(dict(base, nodes=[dict(base["nodes"][0], host=host)]))
            with self.assertRaises(gateway.GatewayError):
                load(base, 0o644)


unittest.main(verbosity=2)
"""


if __name__ == "__main__":
    sys.exit(main())
