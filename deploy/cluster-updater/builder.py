"""Build a confirmed formal release with the audited HA r4 overlay.

Only this module's caller decides whether an update is approved. No production
container, proxy, database, SSH credential or Docker socket is mounted into a
build container. Requires Python 3.9+, Docker and git on the build host.
"""
import base64
import contextlib
import datetime
import hashlib
import hmac
import http.server
import ipaddress
import json
import os
from pathlib import Path, PurePosixPath
import re
import secrets
import select
import socket
import socketserver
import stat
import subprocess
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


REPOSITORY = "yb1320057104/sub2api"
API = "https://api.github.com/repos/" + REPOSITORY
BASE_COMMIT = "fd80b8314fc2ddb9ec6470755abed0f741b27680"
# The audited source baseline includes documentation recorded after the release.
# Its application tree is identical to this immutable formal pk.58 commit.
BASE_RELEASE_COMMIT = "00af06c1e9a610cb6a59a2fbfb633ce003c0468a"
BASELINE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+-pk\.[1-9][0-9]*$")
COMMIT = re.compile(r"^[a-f0-9]{40}$")
DIGEST = re.compile(r"^[a-f0-9]{64}$")
MAX_JSON = 4 * 1024 * 1024
MAX_ARCHIVE = 256 * 1024 * 1024
MAX_SOURCE = 1024 * 1024 * 1024
DEPENDENCY_HOSTS = frozenset(("registry.npmjs.org", "proxy.golang.org", "sum.golang.org", "storage.googleapis.com"))



class BuildError(RuntimeError):
    """Only fixed machine-readable codes may cross the API boundary."""


def need(value, code):
    if not value:
        raise BuildError(code)


def digest(path):
    result = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(chunk)
    return result.hexdigest()


def private_write(path, data):
    path = Path(path)
    with os.fdopen(os.open(str(path), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "wb") as stream:
        stream.write(data)


def json_write(path, value):
    private_write(path, (json.dumps(value, sort_keys=True, indent=2) + "\n").encode())


def private_token(config):
    path = Path(config["github_token_file"])
    value = path.lstat()
    need(stat.S_ISREG(value.st_mode) and not path.is_symlink(), "token_file_not_regular")
    if os.name == "posix":
        need(value.st_mode & 0o077 == 0 and value.st_uid == os.geteuid(), "token_file_permissions")
    need(value.st_size <= 16384, "token_file_too_large")
    token = path.read_text(encoding="utf-8").strip()
    need(bool(token) and re.fullmatch(r"[A-Za-z0-9_]+", token), "token_format_invalid")
    return token


def checked_url(value):
    parsed = urllib.parse.urlsplit(value)
    need(parsed.scheme == "https" and parsed.hostname in ("api.github.com", "codeload.github.com")
         and parsed.username is None and parsed.password is None and parsed.port in (None, 443)
         and not parsed.fragment, "source_download_host_rejected")
    if parsed.hostname == "api.github.com":
        need(parsed.path == "/repos/" + REPOSITORY
             or parsed.path.startswith("/repos/" + REPOSITORY + "/"), "source_repository_rejected")
    else:
        need(parsed.path.startswith("/" + REPOSITORY + "/"), "source_repository_rejected")
    return parsed


class SourceRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, url):
        parsed = checked_url(url)
        result = super().redirect_request(request, response, code, message, headers, url)
        if result is not None and parsed.hostname != "api.github.com":
            result.remove_header("Authorization")
        return result


def request(config, url, *, limit=MAX_JSON, destination=None):
    parsed = checked_url(url)
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "sub2api-cluster-builder",
               "X-GitHub-Api-Version": "2022-11-28"}
    if parsed.hostname == "api.github.com":
        headers["Authorization"] = "Bearer " + private_token(config)
    opener = urllib.request.build_opener(SourceRedirect())
    output = None
    try:
        with opener.open(urllib.request.Request(url, headers=headers), timeout=60) as response:
            need(response.status == 200, "github_request_failed")
            size = 0
            chunks = []
            if destination is not None:
                output = os.fdopen(os.open(str(destination), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "wb")
            while True:
                chunk = response.read(min(1024 * 1024, limit + 1 - size))
                if not chunk:
                    break
                size += len(chunk)
                need(size <= limit, "source_response_too_large")
                if output is not None:
                    output.write(chunk)
                else:
                    chunks.append(chunk)
            if output is None:
                return json.loads(b"".join(chunks))
            return size
    except BuildError:
        raise
    except Exception:
        # HTTP errors can contain signed URLs. Do not expose their string form.
        raise BuildError("github_request_failed") from None
    finally:
        if output is not None:
            output.close()


def resolve_latest(config):
    release = request(config, API + '/releases/latest')
    tag = release.get('tag_name', '')
    need(isinstance(tag, str) and tag.startswith('v') and BASELINE.fullmatch(tag[1:]), 'latest_release_not_formal_pk')
    need(release.get('draft') is False and release.get('prerelease') is False and bool(release.get('published_at')), 'formal_release_not_published')
    return resolve_target(config, tag[1:])


def resolve_target(config, baseline):
    need(isinstance(baseline, str) and BASELINE.fullmatch(baseline), "invalid_target_baseline")
    repository = request(config, API)
    need(repository.get("private") is True and repository.get("full_name") == REPOSITORY,
         "source_repository_not_private")
    tag = "v" + baseline
    release = request(config, API + "/releases/tags/" + tag)
    need(release.get("tag_name") == tag and release.get("draft") is False
         and release.get("prerelease") is False and bool(release.get("published_at")),
         "formal_release_not_published")
    ref = request(config, API + "/git/ref/tags/" + tag).get("object", {})
    for _ in range(8):
        need(COMMIT.fullmatch(str(ref.get("sha", ""))), "invalid_tag_object")
        if ref.get("type") == "commit":
            break
        need(ref.get("type") == "tag", "invalid_tag_type")
        ref = request(config, API + "/git/tags/" + ref["sha"]).get("object", {})
    need(ref.get("type") == "commit" and COMMIT.fullmatch(str(ref.get("sha", ""))), "tag_resolution_limit")
    commit = ref["sha"]
    comparison = request(config, API + "/compare/" + commit + "...main")
    need(comparison.get("status") in ("ahead", "identical"), "release_outside_main_history")
    comparison = request(config, API + '/compare/' + BASE_COMMIT + '...' + commit)
    need(comparison.get('status') in ('ahead', 'identical') or
         (baseline == '0.2.19-pk.58' and commit == BASE_RELEASE_COMMIT), 'release_before_cluster_base')
    return {"baseline": baseline, "tag": tag, "commit": commit}


def extract_source(archive, destination):
    """Extract one GitHub root directory with no links, duplicates or traversal."""
    destination = Path(destination)
    need(not destination.exists(), "source_destination_exists")
    members = []
    seen = set()
    total = 0
    root = None
    with tarfile.open(archive, "r:gz") as bundle:
        for member in bundle:
            need(len(members) < 100000 and "\\" not in member.name and "\x00" not in member.name,
                 "unsafe_archive_member")
            parts = PurePosixPath(member.name).parts
            need(parts and not member.name.startswith("/") and ".." not in parts
                 and all(part not in ("", ".") for part in parts), "unsafe_archive_path")
            need(member.isdir() or member.isfile(), "archive_link_or_device_rejected")
            if root is None:
                root = parts[0]
            need(parts[0] == root, "archive_multiple_roots")
            relative = PurePosixPath(*parts[1:])
            if len(parts) == 1:
                need(member.isdir(), "archive_root_not_directory")
                continue
            name = relative.as_posix()
            need(name not in seen, "archive_duplicate_path")
            seen.add(name)
            total += member.size
            need(member.size >= 0 and total <= MAX_SOURCE, "source_unpacked_too_large")
            members.append((member, relative))
        need(any(path.as_posix() == "backend/go.mod" for _, path in members), "source_backend_missing")
        destination.mkdir(mode=0o700)
        for member, relative in members:
            target = destination / str(relative)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True, mode=0o700)
                continue
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            data = bundle.extractfile(member)
            need(data is not None, "archive_member_unreadable")
            with data, os.fdopen(os.open(str(target), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "wb") as stream:
                remaining = member.size
                while remaining:
                    chunk = data.read(min(1024 * 1024, remaining))
                    need(bool(chunk), "archive_truncated")
                    stream.write(chunk)
                    remaining -= len(chunk)
            # Source scripts may be executed by the frozen package scripts.
            if member.mode & 0o111:
                target.chmod(0o700)


def overlay_manifest(config):
    root = Path(config["overlay_dir"])
    need(root.is_dir() and not root.is_symlink(), "overlay_directory_invalid")
    value = json.loads((root / "manifest.json").read_text())
    need(value.get("schema") == 1 and value.get("base_commit") == BASE_COMMIT and value.get("build_type") == "cluster-ha"
         and isinstance(value.get("files"), list) and value["files"], "overlay_manifest_invalid")
    migrations = value.get('base_migrations')
    need(isinstance(migrations, dict) and bool(migrations), 'overlay_migrations_missing')
    for name, expected in migrations.items():
        need(isinstance(name, str) and name.startswith('backend/migrations/')
             and '..' not in PurePosixPath(name).parts and '\\' not in name and ':' not in name
             and DIGEST.fullmatch(str(expected)), 'overlay_migration_pin_invalid')
    tests = value.get('targeted_tests')
    need(isinstance(tests, list) and tests, 'overlay_tests_missing')
    names = set()
    for test in tests:
        need(isinstance(test, dict) and re.fullmatch(r'[a-z][a-z0-9-]*', str(test.get('name', '')))
             and test['name'] not in names and isinstance(test.get('packages'), list) and test['packages']
             and isinstance(test.get('selection'), str) and test['selection'].startswith('^Test'), 'overlay_test_invalid')
        names.add(test['name'])
        for package in test['packages']:
            need(isinstance(package, str) and re.fullmatch(r'\./[a-zA-Z0-9_/-]+', package)
                 and '..' not in package[2:], 'overlay_test_package_invalid')
    seen = set()
    for row in value["files"]:
        name = row.get("path", "")
        path = PurePosixPath(name)
        need((name.startswith("backend/") or name == "deploy/.env.example") and "\\" not in name and ":" not in name and ".." not in path.parts
             and not name.startswith("backend/migrations/")
             and not name.startswith("backend/internal/web/dist/") and name not in seen,
             "overlay_path_invalid")
        seen.add(name)
        for kind in ("base", "ours"):
            expected = row.get(kind + "_sha256")
            file = root / kind / name
            need(root.resolve() in file.resolve().parents, "overlay_path_outside_root")
            need(expected is None or DIGEST.fullmatch(str(expected)), "overlay_digest_invalid")
            if expected is None:
                need(not file.exists(), "overlay_absence_changed")
            else:
                need(file.is_file() and not file.is_symlink() and digest(file) == expected,
                     "overlay_file_changed")
    return root, value


def merge_bytes(ours, base, theirs, directory, index):
    if ours == base:
        return theirs
    if theirs == base or theirs == ours:
        return ours
    need(ours is not None and base is not None and theirs is not None, "cluster_overlay_conflict")
    need(all(b"\x00" not in item for item in (ours, base, theirs)), "cluster_binary_conflict")
    paths = []
    for label, data in (("ours", ours), ("base", base), ("theirs", theirs)):
        path = directory / (str(index) + "." + label)
        private_write(path, data)
        paths.append(str(path))
    result = subprocess.run(["git", "merge-file", "-p", *paths], capture_output=True, timeout=30)
    if result.returncode:
        private_write(directory / (str(index) + ".conflict"), result.stdout)
        raise BuildError("cluster_overlay_conflict")
    return result.stdout


def apply_overlay(config, source, directory):
    root, manifest = overlay_manifest(config)
    directory.mkdir(mode=0o700)
    for index, row in enumerate(manifest["files"]):
        name = row["path"]
        target = source / name
        need(not target.is_symlink() and (not target.exists() or target.is_file()), "overlay_target_invalid")
        inputs = [(root / kind / name).read_bytes() if row[kind + "_sha256"] is not None else None
                  for kind in ("ours", "base")]
        inputs.append(target.read_bytes() if target.is_file() else None)
        result = merge_bytes(*inputs, directory, index)
        if result is None:
            if target.exists():
                target.unlink()
        else:
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            target.write_bytes(result)
            target.chmod(0o600)
    return digest(root / "manifest.json")


def run_logged(argv, logfile, *, timeout=1800):
    with os.fdopen(os.open(str(logfile), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "wb") as log:
        try:
            result = subprocess.run(argv, stdout=log, stderr=subprocess.STDOUT, timeout=timeout)
        except subprocess.TimeoutExpired:
            if argv[:2] == ["docker", "run"] and "--name" in argv:
                container = argv[argv.index("--name") + 1]
                if re.fullmatch(r"sub2api-cluster-build-[a-f0-9]{32}", container):
                    subprocess.run(["docker", "rm", "--force", container], stdout=subprocess.DEVNULL,
                                   stderr=subprocess.DEVNULL, timeout=30)
            raise BuildError("build_step_timeout") from None
    need(result.returncode == 0, "build_step_failed")


def docker_command(image, source, caches, command, *, network="none", workdir="/src/backend", env=None):
    mounts = [(source, "/src")] + list(caches)
    argv = ["docker", "run", "--rm", "--name", "sub2api-cluster-build-" + uuid.uuid4().hex,
            "--init", "--user", "0:0", "--cpus", "2", "--memory", "4g", "--memory-swap", "4g",
            "--pids-limit", "512", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
            "--network", network, "--workdir", workdir]
    for host, target in mounts:
        host_path = str(Path(host).resolve())
        need("\n" not in host_path and "\r" not in host_path and "," not in host_path
             and (os.name != "posix" or ":" not in host_path), "build_path_invalid")
        need(target.startswith("/") and not any(char in target for char in (":", "\n", "\r", ",")),
             "build_target_invalid")
        # --mount does not support SELinux relabeling. Each source/cache mount
        # belongs exclusively to this build job and containers run sequentially,
        # so :Z can assign a private MCS label on enforcing SELinux hosts. Never
        # mount/relabel the updater root, credentials or a production directory.
        argv.extend(["--volume", host_path + ":" + target + ":Z"])
    for key, value in (env or {}).items():
        argv.extend(["--env", key + "=" + str(value)])
    return argv + [image] + list(command)


def bridge_address():
    """Read the existing default bridge; never create a network/firewall rule."""
    result = subprocess.run(["docker", "network", "inspect", "bridge"], capture_output=True, timeout=15)
    need(result.returncode == 0, "build_bridge_unavailable")
    try:
        rows = json.loads(result.stdout)
        need(len(rows) == 1 and rows[0].get("Name") == "bridge"
             and rows[0].get("Driver") == "bridge", "build_bridge_invalid")
        candidates = []
        for entry in rows[0]["IPAM"]["Config"]:
            subnet = ipaddress.ip_network(entry["Subnet"])
            if subnet.version == 4:
                address = ipaddress.ip_address(entry["Gateway"])
                need(address in subnet and address.is_private and not address.is_loopback
                     and not address.is_unspecified and not address.is_link_local,
                     "build_bridge_invalid")
                candidates.append((str(address), subnet))
        need(len(candidates) == 1, "build_bridge_invalid")
        return candidates[0]
    except (KeyError, ValueError, TypeError):
        raise BuildError("build_bridge_invalid") from None


def dependency_addresses(authority):
    """Resolve once, then connect only these validated numeric addresses."""
    need(authority in {host + ":443" for host in DEPENDENCY_HOSTS}, "dependency_host_rejected")
    host = authority[:-4]
    result = socket.getaddrinfo(host, 443, socket.AF_INET, socket.SOCK_STREAM)
    addresses = sorted({item[4][0] for item in result})
    parsed = [ipaddress.ip_address(address) for address in addresses]
    need(addresses and all(address.is_global and not address.is_multicast and not address.is_reserved
                           and not address.is_unspecified for address in parsed),
         "dependency_address_rejected")
    return addresses


class _DependencyProxy(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    block_on_close = False
    request_queue_size = 64

    def __init__(self, address, subnet, token):
        self.subnet = subnet
        self.authorization = "Basic " + base64.b64encode(("build:" + token).encode()).decode()
        self.stopping = threading.Event()
        self.slots = threading.BoundedSemaphore(64)
        self.active = set()
        self.active_lock = threading.Lock()
        super().__init__((address, 0), _DependencyTunnel)

    def verify_request(self, request, client_address):
        return ipaddress.ip_address(client_address[0]) in self.subnet

    def process_request(self, request, client_address):
        if not self.slots.acquire(blocking=False):
            self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except BaseException:
            self.slots.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            if not self.register(request):
                return
            super().process_request_thread(request, client_address)
        finally:
            with self.active_lock:
                self.active.discard(request)
            self.slots.release()

    def register(self, connection):
        with self.active_lock:
            if self.stopping.is_set():
                connection.close()
                return False
            self.active.add(connection)
            return True

    def handle_error(self, request, client_address):
        # Never log request headers or the temporary proxy credential.
        pass

    def close_tunnels(self):
        self.stopping.set()
        with self.active_lock:
            sockets = list(self.active)
        for connection in sockets:
            with contextlib.suppress(OSError):
                connection.shutdown(socket.SHUT_RDWR)
            connection.close()


class _ProxyHeaderReader:
    def __init__(self, stream):
        self.stream = stream
        self.remaining = 32768

    def readline(self, limit=-1):
        value = self.stream.readline(min(limit if limit >= 0 else 32769, self.remaining + 1))
        self.remaining -= len(value)
        if self.remaining < 0:
            raise ValueError("proxy_headers_too_large")
        return value

    def close(self):
        self.stream.close()


class _DependencyTunnel(http.server.BaseHTTPRequestHandler):
    # Avoid read-ahead eating bytes that belong to the TLS tunnel.
    rbufsize = 0
    timeout = 15

    def setup(self):
        super().setup()
        self.rfile = _ProxyHeaderReader(self.rfile)

    def log_message(self, format, *args):
        pass

    def do_CONNECT(self):
        upstream = None
        try:
            values = self.headers.get_all("Proxy-Authorization", [])
            if len(values) != 1 or not hmac.compare_digest(values[0].encode(), self.server.authorization.encode()):
                self.send_error(407, "Proxy authorization required")
                return
            if self.headers.get("Transfer-Encoding") or self.headers.get("Content-Length") not in (None, "0"):
                self.send_error(400, "CONNECT body rejected")
                return
            try:
                addresses = dependency_addresses(self.path)
            except (BuildError, OSError, ValueError):
                self.send_error(403, "Dependency target rejected")
                return
            for address in addresses[:4]:
                if self.server.stopping.is_set():
                    return
                candidate = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                if not self.server.register(candidate):
                    return
                try:
                    candidate.settimeout(10)
                    candidate.connect((address, 443))
                    upstream = candidate
                    break
                except OSError:
                    with self.server.active_lock:
                        self.server.active.discard(candidate)
                    candidate.close()
                    continue
            if upstream is None:
                self.send_error(502, "Dependency connection failed")
                return
            if self.server.stopping.is_set():
                return
            self.connection.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
            deadline = time.monotonic() + 120
            while not self.server.stopping.is_set() and time.monotonic() < deadline:
                readable, _, _ = select.select([self.connection, upstream], [], [], 1)
                for source in readable:
                    data = source.recv(65536)
                    if not data:
                        return
                    destination = upstream if source is self.connection else self.connection
                    destination.sendall(data)
                    deadline = time.monotonic() + 120
        except (OSError, ValueError):
            return
        finally:
            self.close_connection = True
            if upstream is not None:
                with self.server.active_lock:
                    self.server.active.discard(upstream)
                upstream.close()


@contextlib.contextmanager
def dependency_proxy():
    """Temporary download-only egress via host OUTPUT, without Docker forwarding.

    Bind only the default bridge gateway, require a per-stage credential and
    permit public TLS endpoints of the fixed package registries. Production
    networking and credentials never enter a build container. Offline stages
    run after this listener and all its tunnels have been closed.
    """
    address, subnet = bridge_address()
    token = secrets.token_urlsafe(32)
    proxy = _DependencyProxy(address, subnet, token)
    thread = threading.Thread(target=proxy.serve_forever, kwargs={"poll_interval": 0.1}, daemon=True)
    thread.start()
    try:
        url = "http://build:" + token + "@" + address + ":" + str(proxy.server_port)
        yield {"HTTP_PROXY": url, "HTTPS_PROXY": url, "http_proxy": url, "https_proxy": url,
               "NO_PROXY": "", "no_proxy": "", "npm_config_proxy": url, "npm_config_https_proxy": url,
               "npm_config_registry": "https://registry.npmjs.org/"}
    finally:
        proxy.close_tunnels()
        proxy.shutdown()
        proxy.server_close()
        thread.join(timeout=5)


def verify_tests(path, expected_packages=None):
    passed = failed = skipped = 0
    packages = set()
    with path.open(encoding="utf-8", errors="replace") as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("Test"):
                passed += event.get("Action") == "pass"
                failed += event.get("Action") == "fail"
                skipped += event.get("Action") == "skip"
                if event.get('Action') == 'pass':
                    packages.add(event.get('Package', ''))
    need(passed > 0 and failed == 0 and skipped == 0, "cluster_tests_incomplete")
    for package in expected_packages or ():
        need(any(name.endswith('/' + package[2:]) for name in packages), 'cluster_test_package_uncovered')
    return {"passed": passed, "log_sha256": digest(path)}


def go_environment():
    # The service package is large. Reduce compiler heap growth and build one
    # package at a time while keeping the existing 2 CPU / 4 GiB container cap.
    return {"GOTOOLCHAIN": "local", "GOWORK": "off", "CGO_ENABLED": "0", "GOMAXPROCS": "2",
            "GOGC": "20", "GOMEMLIMIT": "2500MiB", "GOOS": "linux", "GOARCH": "amd64",
            "GOCACHE": "/root/.cache/go-build"}


def migration_snapshot(source):
    root = source / 'backend/migrations'
    need(root.is_dir() and not root.is_symlink(), 'cluster_migrations_missing')
    result = {}
    for path in root.rglob('*'):
        need(not path.is_symlink(), 'cluster_migration_link_rejected')
        if path.is_file():
            result[path.relative_to(source).as_posix()] = digest(path)
    return result


def build_tail(config, actual, job, logs, overlay_sha, migration_files, report):
    """Run the offline Go checks and binary build, including on validated resume."""
    source = job / "source"
    archive = job / "source.tar.gz"
    go_image = config.get("go_image", "golang:1.27.3-bookworm")
    go_env = go_environment()
    caches = [(job / "go-mod", "/go/pkg/mod"), (job / "go-cache", "/root/.cache/go-build")]
    go_inputs = {name: digest(source / "backend" / name) for name in ("go.mod", "go.sum")}
    report("verifying_go_dependencies")
    run_logged(docker_command(go_image, source, caches, ["go", "mod", "verify"], env=go_env), logs / "go-verify.log")
    overlay_root, overlay = overlay_manifest(config)
    need(digest(overlay_root / 'manifest.json') == overlay_sha, 'overlay_changed_during_build')
    tests = {}
    for test in overlay['targeted_tests']:
        name, packages, selection = test['name'], test['packages'], test['selection']
        report('testing_cluster_' + name)
        log = logs / ('test-' + name + '.log')
        run_logged(docker_command(go_image, source, caches,
                                  ['go', 'test', '-mod=readonly', '-p', '1', '-tags', 'embed,unit', '-count=1',
                                   '-json', '-timeout=180s', '-run', selection] + packages, env=go_env), log)
        tests[name] = verify_tests(log, packages)
        tests[name].update({'packages': packages, 'selection': selection})
    now = datetime.datetime.now(datetime.timezone.utc)
    version = actual["baseline"] + "-ha." + now.strftime("%Y%m%d") + "." + str(int(now.timestamp()))
    flags = ("-s -w -X main.Version=" + version + " -X main.Commit=" + actual["commit"]
             + "+cluster-ha-r4 -X main.Date=" + now.isoformat() + " -X main.BuildType=cluster-ha")
    report("building_binary")
    run_logged(docker_command(go_image, source, caches, ["go", "build", "-trimpath", "-buildvcs=false", "-mod=readonly",
                                                       "-p", "1", "-tags", "embed", "-ldflags=" + flags,
                                                       "-o", "/src/sub2api", "./cmd/server"], env=go_env), logs / "go-build.log")
    need(go_inputs == {name: digest(source / "backend" / name) for name in go_inputs}, "go_dependency_inputs_changed")
    binary = source / "sub2api"
    need(binary.is_file() and 1024 * 1024 < binary.stat().st_size < 500 * 1024 * 1024, "built_binary_invalid")
    with binary.open("rb") as stream:
        need(stream.read(4) == b"\x7fELF", "built_binary_not_elf")
    run_logged(docker_command(go_image, source, [], ["/src/sub2api", "--version"]), logs / "binary-version.log", timeout=60)
    need(version in (logs / "binary-version.log").read_text(), "built_version_mismatch")
    need(resolve_target(config, actual["baseline"]) == actual, "release_changed_during_build")
    need(migration_snapshot(source) == overlay['base_migrations'], 'cluster_migrations_changed_during_build')
    need(digest(overlay_root / 'manifest.json') == overlay_sha, 'overlay_changed_during_build')
    binary.chmod(0o755)
    artifact = {"binary": str(binary), "sha256": digest(binary), "bytes": binary.stat().st_size,
                "version": version, "baseline": actual["baseline"], "commit": actual["commit"],
                "role": "cluster", "build_type": "cluster-ha", "capabilities": ["ha-r4"], "overlay_sha256": overlay_sha,
                "source_sha256": digest(archive), "tests": tests, "migration_files": migration_files}
    json_write(logs / "manifest.json", artifact)
    artifact["manifest"] = str(logs / "manifest.json")
    report("artifact_ready")
    return artifact


def source_files(root, *, exclude_dist=False):
    result = {}
    for path in root.rglob("*"):
        name = path.relative_to(root).as_posix()
        if exclude_dist and (name == "internal/web/dist" or name.startswith("internal/web/dist/")):
            continue
        need(not path.is_symlink(), "resume_source_link_rejected")
        if path.is_file():
            result[name] = digest(path)
    return result


def resume_verified_build(*args, **kwargs):
    raise BuildError('cluster_resume_requires_fresh_build')


def build(config, target, report):
    """Resolve, download, merge, isolate builds/tests and return a local artifact."""
    report("resolving_source")
    actual = resolve_target(config, target.get("baseline"))
    need(actual == target, "confirmed_target_changed")
    work = Path(config["work_dir"]).resolve()
    work.mkdir(parents=True, exist_ok=True, mode=0o700)
    work.chmod(0o700)
    job = Path(tempfile.mkdtemp(prefix=actual["baseline"] + "-", dir=str(work)))
    source = job / "source"
    archive = job / "source.tar.gz"
    try:
        report("downloading_source")
        request(config, API + "/tarball/" + actual["commit"], limit=MAX_ARCHIVE, destination=archive)
        extract_source(archive, source)
        # This repository retains the upstream version in VERSION and embeds
        # the private pk release through linker flags in its official pipeline.
        need((source / "backend/cmd/server/VERSION").read_text().strip() in
             (actual["baseline"], actual["baseline"].split("-pk.", 1)[0]),
             "source_version_mismatch")
        _, overlay = overlay_manifest(config)
        before_migrations = migration_snapshot(source)
        need(before_migrations == overlay['base_migrations'], 'cluster_migrations_require_compatibility_review')
        report('merging_cluster')
        overlay_sha = apply_overlay(config, source, job / 'merge')
        need(before_migrations == migration_snapshot(source), 'cluster_overlay_changed_migrations')
        node_image = config.get("node_image", "node:24-bookworm-slim")
        go_image = config.get("go_image", "golang:1.27.3-bookworm")
        # These images contain tools only. No image receives private GitHub access.
        npm_cache, go_mod, go_cache = (job / name for name in ("npm-cache", "go-mod", "go-cache"))
        for directory in (npm_cache, go_mod, go_cache):
            directory.mkdir(mode=0o700)
        frontend_inputs = {name: digest(source / "frontend" / name) for name in ("package.json", "pnpm-lock.yaml")}
        frontend_env = {"CI": "true", "VITEST_MAX_THREADS": "2", "VITEST_MIN_THREADS": "1",
                        "VITEST_MAX_FORKS": "2", "VITEST_MIN_FORKS": "1"}
        report("installing_frontend_dependencies")
        with dependency_proxy() as proxy_env:
            run_logged(docker_command(node_image, source, [(npm_cache, "/root/.npm")],
                                      ["npx", "--yes", "pnpm@9.15.9", "install", "--frozen-lockfile"],
                                      network="bridge", workdir="/src/frontend", env=dict(frontend_env, **proxy_env)),
                       job / "frontend-install.log")
        report("building_frontend")
        run_logged(docker_command(node_image, source, [(npm_cache, "/root/.npm")],
                                  ["npx", "--offline", "--yes", "pnpm@9.15.9", "run", "build"],
                                  workdir="/src/frontend", env=frontend_env), job / "frontend-build.log")
        need(frontend_inputs == {name: digest(source / "frontend" / name) for name in frontend_inputs},
             "frontend_dependency_inputs_changed")
        need((source / "backend/internal/web/dist/index.html").is_file(), "frontend_output_missing")
        go_inputs = {name: digest(source / "backend" / name) for name in ("go.mod", "go.sum")}
        go_env = go_environment()
        caches = [(go_mod, "/go/pkg/mod"), (go_cache, "/root/.cache/go-build")]
        report("installing_go_dependencies")
        with dependency_proxy() as proxy_env:
            run_logged(docker_command(go_image, source, caches, ["go", "mod", "download"], network="bridge",
                                      env=dict(go_env, **proxy_env, GOPROXY="https://proxy.golang.org",
                                               GOSUMDB="sum.golang.org")), job / "go-download.log")
        need(go_inputs == {name: digest(source / "backend" / name) for name in go_inputs}, "go_dependency_inputs_changed")
        return build_tail(config, actual, job, job, overlay_sha, len(before_migrations), report)
    except BuildError as error:
        json_write(job / "failed.json", {"code": str(error), "production_changed": False})
        raise
    except Exception:
        json_write(job / "failed.json", {"code": "build_failed", "production_changed": False})
        raise BuildError("build_failed") from None
