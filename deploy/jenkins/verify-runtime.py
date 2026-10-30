#!/usr/bin/env python3
"""Validate a Jenkins package in its isolated build daemon, without starting a server.

Run this trusted script from the pipeline checkout before binding publish credentials.
The package is read-only input. Only the validation report and disposable Docker
objects are created; no production socket, network, data or source is mounted.
"""
from __future__ import annotations

import argparse
import base64
import contextlib
import datetime as dt
import gzip
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parent))
import frontend_dependencies as dependencies
import deployment_bundle as deployment
import browser_payload as browser

REPOSITORY = "pengkang0322/sub2api"
PROFILE = "pk-artifact-v2"
REQUIRED_CHECKS = (
    "artifact-integrity", "image-provenance", "image-loaded", "binary-identity",
    "runtime-nonroot", "same-seed-preservation", "interrupted-update-recovery",
    "fixture-preparation", "changed-seed-replacement", "original-image-rollback", "cleanup",
)
BINDINGS = (
    "repository", "commit", "tree", "package_id", "binary_sha256",
    "binary_archive_sha256", "image_archive_sha256", "image_id", "version",
)


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def digest(stream):
    return hashlib.file_digest(stream, "sha256").hexdigest()


def regular(path):
    require(path.is_file() and not path.is_symlink(), "Not a regular file: " + path.name)
    return path


def file_digest(path):
    with regular(path).open("rb") as stream:
        return digest(stream)


def member_name(name):
    if name in (".", "./"):
        return "."
    while name.startswith("./"):
        name = name[2:]
    require(name and not name.startswith("/") and ".." not in PurePosixPath(name).parts,
            "Unsafe archive member")
    return PurePosixPath(name).as_posix()


def tar_members(archive):
    members = {}
    for member in archive:
        name = member_name(member.name)
        if name == ".":
            require(member.isdir(), "Archive root must be a directory")
            continue
        require(name not in members, "Duplicate archive member: " + name)
        members[name] = member
    return members


def tar_stream(archive, members, name):
    require(isinstance(name, str) and member_name(name) == name, "Invalid image archive reference")
    member = members.get(name)
    require(member is not None and member.isfile(), "Missing regular image archive member: " + name)
    stream = archive.extractfile(member)
    require(stream is not None, "Unreadable image archive member: " + name)
    return stream


def tar_json(archive, members, name):
    require(members.get(name) is not None and members[name].size <= 4 * 1024 * 1024,
            "Missing or oversized image metadata")
    with tar_stream(archive, members, name) as stream:
        return json.load(stream)


class Validator:
    def __init__(self, directory, report):
        self.directory, self.report = directory, report
        self.data = {}
        suffix = uuid.uuid4().hex
        self.volume = "pk-jenkins-runtime-" + suffix
        self.fixture = "pk-jenkins-seed:" + suffix
        self.label = "pk.runtime.validation=" + suffix
        self.containers = []
        self.volume_created = False
        self.fixture_created = False
        self.docker_started = False
        self.environment = None

    @contextlib.contextmanager
    def check(self, name):
        result = {"name": name, "status": "failed", "exit_code": 1, "started_at": now()}
        self.report["checks"].append(result)
        try:
            yield
            result.update(status="passed", exit_code=0)
        except BaseException as error:
            result["error"] = str(error)
            raise
        finally:
            result["finished_at"] = now()

    def docker(self, *args, timeout=120):
        result = subprocess.run(["docker", *args], env=self.environment, stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                text=True, timeout=timeout, check=False)
        require(result.returncode == 0,
                "Docker " + args[0] + " failed (" + str(result.returncode) + "): " + result.stdout[-4000:])
        return result.stdout.strip()

    def daemon(self):
        endpoint = os.environ.get("DOCKER_HOST", "")
        expected = os.environ.get("PK_RUNTIME_DOCKER_HOST", "tcp://docker:2376")
        require(re.fullmatch(r"tcp://[A-Za-z0-9][A-Za-z0-9.-]*:2376", expected) is not None
                and endpoint == expected, "A dedicated TLS Docker daemon is required; host sockets are forbidden")
        require(os.environ.get("DOCKER_TLS_VERIFY") == "1", "Docker TLS verification must be enabled")
        certificates = Path(os.environ.get("DOCKER_CERT_PATH", "/certs/client"))
        require(certificates.is_absolute(), "Docker certificate path must be absolute")
        for name in ("ca.pem", "cert.pem", "key.pem"):
            regular(certificates / name)
        # Docker CLI gets only its transport configuration, never release or
        # application credentials inherited from the Jenkins process.
        self.environment = {key: os.environ[key] for key in ("PATH", "HOME", "TMPDIR") if key in os.environ}
        self.environment.update(DOCKER_HOST=endpoint, DOCKER_TLS_VERIFY="1",
                                DOCKER_CERT_PATH=str(certificates), DOCKER_CONFIG=self.docker_config)
        self.docker_started = True
        require(self.docker("info", "--format", "{{.OSType}}") == "linux", "Docker daemon must run Linux containers")

    def artifacts(self):
        manifest = regular(self.directory / "manifest.json")
        data = json.loads(manifest.read_text(encoding="utf-8"))
        require(isinstance(data, dict), "Invalid artifact manifest")
        self.data = data
        require(data.get("schema_version") in (1, 2) and data.get("repository") == REPOSITORY,
                "Unexpected package provenance")
        dependencies.verify(self.directory, data)
        deployment.verify(self.directory, data)
        browser.verify(self.directory, data)
        require(data.get("platform") == "linux/amd64", "Unsupported artifact platform")
        for key in ("commit", "tree"):
            require(re.fullmatch(r"[0-9a-f]{40}", data.get(key, "")) is not None, "Invalid " + key)
        require(re.fullmatch(r"[1-9][0-9]*-" + data["commit"][:12], data.get("package_id", "")) is not None,
                "Invalid package ID")
        version = data.get("version", "")
        require(re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-pk\.[1-9][0-9]*)?(?:-pk\.candidate\.[1-9][0-9]*)?",
                             version) is not None, "Invalid package version")
        require(data.get("image_tag") == "ghcr.io/" + REPOSITORY + ":" + version, "Unexpected private image tag")
        for key, suffix in (("binary_archive", ".tar.gz"), ("image_archive", ".docker.tar.gz")):
            require(data.get(key) == "sub2api_" + version + "_linux_amd64" + suffix, "Unexpected archive name")
            require(file_digest(self.directory / data[key]) == data.get(key + "_sha256"), "Archive digest mismatch: " + key)
        require(re.fullmatch(r"[0-9a-f]{64}", data.get("binary_sha256", "")) is not None, "Invalid binary digest")
        require(re.fullmatch(r"sha256:[0-9a-f]{64}", data.get("image_id", "")) is not None, "Invalid image ID")
        with tarfile.open(self.directory / data["binary_archive"], "r:gz") as archive:
            members = tar_members(archive)
            require(list(members) == ["sub2api"] and members["sub2api"].isfile(), "Unexpected update archive layout")
            with tar_stream(archive, members, "sub2api") as stream:
                require(digest(stream) == data["binary_sha256"], "Update binary digest mismatch")
        expected = deployment.checksums(data)
        require(regular(self.directory / "checksums.txt").read_text() == expected, "Unexpected release checksums")
        bindings = BINDINGS + (dependencies.BINDINGS if data["schema_version"] == 2 else ())
        if deployment.declaration(data):
            bindings += deployment.BINDINGS
        if browser.declaration(data):
            bindings += browser.BINDINGS
        self.report.update({key: data[key] for key in bindings})
        self.report["artifact"] = {key: data[key] for key in bindings}
        if "validation_profile" in data:
            self.report["validation_profile"] = data["validation_profile"]
        if "source_validation_sha256" in data:
            self.report["source_validation_sha256"] = data["source_validation_sha256"]

    def config(self, config):
        labels = config.get("Labels") or {}
        for key, expected in (("source", "https://github.com/" + REPOSITORY),
                              ("version", self.data["version"]), ("revision", self.data["commit"])):
            require(labels.get("org.opencontainers.image." + key) == expected, "Image label mismatch: " + key)
        require(config.get("Entrypoint") == ["/app/docker-entrypoint.sh"], "Unexpected image entrypoint")
        require(config.get("Cmd") == ["/app/sub2api"], "Unexpected image default command")
        require(not config.get("Volumes"), "Image must not create anonymous volumes")
        settings = [item for item in config.get("Env", []) if item.startswith("SUB2API_DOCKER_SELF_UPDATE=")]
        require(settings == ["SUB2API_DOCKER_SELF_UPDATE=true"], "Self-update must be enabled in the image")
        if deployment.declaration(self.data):
            environment = dict(item.split("=", 1) for item in config.get("Env", []) if "=" in item)
            require(environment.get("PK_AUTH_BROWSER_PATH") == "/usr/bin/chromium"
                    and environment.get("PK_AUTH_BROWSER_NO_SANDBOX") == "true", "Browser defaults must be embedded in the image")

    def image_identity(self, archive, members, compatibility, config_digest):
        """Accept classic config IDs and Docker 29's authenticated OCI index IDs."""
        if "index.json" not in members:
            require(config_digest == self.data["image_id"], "Image config digest mismatch")
            return
        index_types = {"application/vnd.oci.image.index.v1+json",
                       "application/vnd.docker.distribution.manifest.list.v2+json"}
        manifest_types = {"application/vnd.oci.image.manifest.v1+json",
                          "application/vnd.docker.distribution.manifest.v2+json"}
        config_types = {"application/vnd.oci.image.config.v1+json",
                        "application/vnd.docker.container.image.v1+json"}
        index = tar_json(archive, members, "index.json")
        roots = index.get("manifests", [])
        require(index.get("schemaVersion") == 2 and len(roots) == 1,
                "Expected exactly one OCI image root")
        root = roots[0]
        require(root.get("digest") == self.data["image_id"] or config_digest == self.data["image_id"],
                "OCI root does not match the recorded image identity")
        annotations = root.get("annotations", {})
        require(annotations.get("io.containerd.image.name") == self.data["image_tag"]
                and annotations.get("org.opencontainers.image.ref.name") == self.data["version"],
                "OCI image tag mismatch")
        runtime_manifests, attestations = [], []
        visited = set()

        def blob(descriptor):
            require(isinstance(descriptor, dict), "Invalid OCI descriptor")
            value, size = descriptor.get("digest", ""), descriptor.get("size")
            require(re.fullmatch(r"sha256:[0-9a-f]{64}", value) is not None
                    and type(size) is int and size >= 0, "Invalid OCI descriptor identity")
            name = "blobs/sha256/" + value.removeprefix("sha256:")
            inline = descriptor.get("data")
            if inline is not None:
                require(isinstance(inline, str), "Invalid inline OCI descriptor")
                decoded = base64.b64decode(inline, validate=True)
                require(len(decoded) == size and "sha256:" + hashlib.sha256(decoded).hexdigest() == value,
                        "Inline OCI descriptor digest or size mismatch")
            require(name in members or inline is not None, "Missing OCI descriptor blob")
            if name in members:
                require(members[name].size == size, "OCI descriptor size mismatch")
            if value not in visited:
                if name in members:
                    with tar_stream(archive, members, name) as stream:
                        require("sha256:" + digest(stream) == value, "OCI descriptor digest mismatch")
                visited.add(value)
            return name

        def walk(descriptor, depth=0):
            require(depth <= 8 and len(visited) < 128, "OCI image graph exceeds validation limits")
            name = blob(descriptor)
            kind = descriptor.get("mediaType")
            require(kind in index_types | manifest_types, "Unsupported OCI image descriptor")
            document = tar_json(archive, members, name)
            require(document.get("schemaVersion") == 2 and document.get("mediaType") == kind,
                    "OCI descriptor media type mismatch")
            if kind in index_types:
                children = document.get("manifests", [])
                require(children, "Empty OCI image index")
                for child in children:
                    walk(child, depth + 1)
                return
            attributes = descriptor.get("annotations", {})
            is_attestation = attributes.get("vnd.docker.reference.type") == "attestation-manifest"
            configuration = document.get("config", {})
            if configuration.get("mediaType") == "application/vnd.oci.empty.v1+json":
                require(is_attestation and configuration.get("digest") == "sha256:" + hashlib.sha256(b"{}").hexdigest()
                        and configuration.get("size") == 2, "Only attestations may use the OCI empty config")
            else:
                require(configuration.get("mediaType") in config_types, "Unsupported OCI image config")
            configuration_path = blob(configuration)
            layers = document.get("layers", [])
            require(isinstance(layers, list) and layers, "Missing OCI image layers")
            layer_paths = [blob(layer) for layer in layers]
            platform = descriptor.get("platform", {})
            if is_attestation:
                require(platform.get("os") == "unknown" and platform.get("architecture") == "unknown",
                        "Unexpected attestation platform")
                attestations.append(attributes.get("vnd.docker.reference.digest"))
            else:
                require(not platform or (platform.get("os") == "linux" and platform.get("architecture") == "amd64"),
                        "Unexpected OCI runtime platform")
                runtime_manifests.append((descriptor["digest"], configuration, configuration_path, layer_paths))

        walk(root)
        require(len(runtime_manifests) == 1, "Expected exactly one OCI runtime manifest")
        runtime_digest, configuration, configuration_path, layer_paths = runtime_manifests[0]
        require(all(reference == runtime_digest for reference in attestations), "Attestation refers to a different image")
        require(configuration["digest"] == config_digest and configuration_path == compatibility["Config"],
                "OCI manifest does not reference the packaged image config")
        require(layer_paths == compatibility["Layers"], "OCI manifest does not reference the packaged layers")

    def provenance(self):
        # Check config identity and every uncompressed layer hash before Docker
        # parses the archive. Read layer contents without extracting host paths.
        with tarfile.open(self.directory / self.data["image_archive"], "r:gz") as archive:
            members = tar_members(archive)
            index = tar_json(archive, members, "manifest.json")
            require(isinstance(index, list) and len(index) == 1, "Expected exactly one packaged Docker image")
            item = index[0]
            require(item.get("RepoTags") == [self.data["image_tag"]], "Image archive tag mismatch")
            with tar_stream(archive, members, item["Config"]) as stream:
                config_digest = "sha256:" + digest(stream)
            self.image_identity(archive, members, item, config_digest)
            config = tar_json(archive, members, item["Config"])
            require(config.get("os") == "linux" and config.get("architecture") == "amd64", "Image architecture mismatch")
            self.config(config.get("config", {}))
            layers, diffs = item.get("Layers", []), config.get("rootfs", {}).get("diff_ids", [])
            require(layers and len(layers) == len(diffs) and len(set(layers)) == len(layers), "Invalid image layer chain")
            binary = None
            for layer, expected in zip(layers, diffs):
                with tar_stream(archive, members, layer) as raw, tempfile.SpooledTemporaryFile(max_size=16 * 1024 * 1024) as content:
                    magic = raw.read(2)
                    raw.seek(0)
                    if magic == b"\x1f\x8b":
                        with gzip.GzipFile(fileobj=raw) as uncompressed:
                            shutil.copyfileobj(uncompressed, content)
                    else:
                        shutil.copyfileobj(raw, content)
                    content.seek(0)
                    require("sha256:" + digest(content) == expected, "Image layer digest mismatch")
                    content.seek(0)
                    with tarfile.open(fileobj=content, mode="r:") as inner:
                        records = tar_members(inner)
                        if any(name in records for name in (".wh.app", ".wh..wh..opq", "app/.wh..wh..opq", "app/.wh.sub2api")):
                            binary = None
                        require("app" not in records or records["app"].isdir(), "Image /app must be a directory")
                        if "app/sub2api" in records:
                            with tar_stream(inner, records, "app/sub2api") as stream:
                                binary = digest(stream)
                            binary_member = records["app/sub2api"]
                            require(binary_member.uid == 0 and binary_member.gid == 0 and binary_member.mode == 0o555,
                                    "Image binary must be root-owned and read-only executable")
            require(binary == self.data["binary_sha256"], "Image archive does not contain the exact update binary")

    def create(self, *args, image=None, volume=True, readonly=True, entrypoint=None, default_user=False, browser=False):
        name = "pk-jenkins-check-" + uuid.uuid4().hex
        self.containers.append(name)  # Also clean up if create returns after a timeout.
        flags = ["create", "--name", name, "--label", self.label, "--network=none",
                 "--cap-drop=ALL", "--security-opt=no-new-privileges",
                 "--cpus=1", "--memory=1g" if browser else "--memory=512m",
                 "--pids-limit=256" if browser else "--pids-limit=64", "--no-healthcheck",
                 "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=128m" if browser else "/tmp:rw,noexec,nosuid,nodev,size=16m"]
        if default_user:
            # Exercise the real default entrypoint's root -> su-exec path once.
            # Only its existing chown/setgroups/setgid/setuid operations need
            # these capabilities; the application must still execute as 1000.
            flags += ["--cap-add=CHOWN", "--cap-add=SETUID", "--cap-add=SETGID"]
        else:
            flags.append("--user=1000:1000")
        if readonly:
            flags.append("--read-only")
        if volume:
            flags += ["--mount", "type=volume,src=" + self.volume + ",dst=/app/data"]
        if entrypoint:
            flags += ["--entrypoint", entrypoint]
        self.docker(*flags, image or self.data["image_id"], *args)
        return name

    def container(self, *args, image=None, volume=True, entrypoint=None, default_user=False, browser=False):
        name = self.create(*args, image=image, volume=volume, entrypoint=entrypoint, default_user=default_user, browser=browser)
        output = self.docker("start", "--attach", name, timeout=60)
        status = json.loads(self.docker("inspect", "--format", "{{json .State}}", name))
        require(status.get("Running") is False and status.get("ExitCode") == 0 and not status.get("OOMKilled"),
                "Runtime check failed: " + output[-4000:])
        self.docker("rm", "--force", name)
        self.containers.remove(name)
        return output

    def version(self, image=None):
        output = self.container("-version", image=image)
        require(re.search(r"(?:^|\s)(?:DP API|Sub2API) " + re.escape(self.data["version"]) +
                          r" \(commit: " + re.escape(self.data["commit"]) + r", built: [^\r\n]+\)", output) is not None,
                "Runtime version or commit mismatch")

    def fixture_image(self):
        # Edit an inert container, then commit it. No root process, Dockerfile,
        # compiler, source mount or network is needed to change the image seed.
        name = self.create(volume=False, readonly=False)
        with tempfile.TemporaryDirectory(prefix="pk-runtime-seed-") as directory:
            binary = Path(directory) / "sub2api"
            with tarfile.open(self.directory / self.data["binary_archive"], "r:gz") as archive:
                members = tar_members(archive)
                with tar_stream(archive, members, "sub2api") as source, binary.open("wb") as target:
                    shutil.copyfileobj(source, target)
                    target.write(b"\nPK_JENKINS_IMAGE_SEED\n")
            binary.chmod(0o555)
            self.docker("cp", str(binary), name + ":/app/sub2api")
            # Record the name before commit so timeout/partial completion is cleaned.
            self.fixture_created = True
            self.docker("commit", "--change", "LABEL " + self.label, name, self.fixture)
        fixture_id = self.docker("image", "inspect", "--format", "{{.Id}}", self.fixture)
        require(fixture_id != self.data["image_id"] and re.fullmatch(r"sha256:[0-9a-f]{64}", fixture_id),
                "Fixture did not change the image")
        self.docker("rm", "--force", name)
        self.containers.remove(name)
        return fixture_id

    def browser_web_update(self):
        # The only input copied into a bare old base is the update executable.
        # No Chromium, browser libraries, compiler, network or root process exists.
        self.docker("image", "inspect", "alpine:3.21")
        with tempfile.TemporaryDirectory(prefix="pk-web-browser-") as directory:
            binary = Path(directory) / "sub2api"
            with tarfile.open(self.directory / self.data["binary_archive"], "r:gz") as archive:
                with archive.extractfile("sub2api") as source, binary.open("wb") as target:
                    shutil.copyfileobj(source, target)
            binary.chmod(0o555)
            for _ in range(2):
                script = ('test "$(id -u)" = 1000; test ! -e /usr/bin/chromium; '
                          'test ! -e /usr/lib/chromium; '
                          'export DATA_DIR=/app/data/web-browser-check; '
                          '/app/data/browser-check-sub2api --browser-runtime-check; '
                          'test "$(cat "$DATA_DIR/browser/' + self.data["browser_payload_sha256"] +
                          '/.ready")" = "' + self.data["browser_payload_sha256"] + '"')
                name = self.create("-ec", script, image="alpine:3.21", entrypoint="/bin/sh", browser=True)
                self.docker("cp", str(binary), name + ":/app/data/browser-check-sub2api")
                output = self.docker("start", "--attach", name, timeout=180)
                state = json.loads(self.docker("inspect", "--format", "{{json .State}}", name))
                require(not state.get("Running") and state.get("ExitCode") == 0 and not state.get("OOMKilled")
                        and "Bundled browser ready" in output, "Legacy-image browser update failed: " + output[-4000:])
                self.docker("rm", "--force", name)
                self.containers.remove(name)

    def cleanup(self):
        if not self.docker_started:
            return
        errors = []
        for name in list(self.containers):
            try:
                # The exact random names are private to this invocation.
                self.docker("rm", "--force", name)
            except Exception as error:
                errors.append(str(error))
        if self.fixture_created:
            try:
                self.docker("image", "rm", "--force", self.fixture)
            except Exception as error:
                errors.append(str(error))
        if self.volume_created:
            try:
                self.docker("volume", "rm", self.volume)
            except Exception as error:
                errors.append(str(error))
        require(not errors, "Runtime cleanup failed: " + "; ".join(errors))

    def run(self):
        with tempfile.TemporaryDirectory(prefix="pk-runtime-docker-config-") as config:
            self.docker_config = config
            try:
                with self.check("artifact-integrity"):
                    self.artifacts()
                with self.check("image-provenance"):
                    self.provenance()
                with self.check("image-loaded"):
                    self.daemon()
                    self.docker("load", "--input", str(self.directory / self.data["image_archive"]), timeout=600)
                    loaded = json.loads(self.docker("image", "inspect", self.data["image_tag"]))[0]
                    require(loaded.get("Id") == self.data["image_id"], "Loaded image identity mismatch")
                    self.config(loaded.get("Config", {}))
                with self.check("binary-identity"):
                    value = self.container("/app/sub2api", volume=False, entrypoint="sha256sum").split()[0]
                    require(value == self.data["binary_sha256"], "Loaded image binary digest mismatch")
                with self.check("runtime-nonroot"):
                    self.volume_created = True
                    self.docker("volume", "create", "--label", self.label, self.volume)
                    self.container("sh", "-ec", 'test "$(id -u)" = 1000; test -w /app/data', default_user=True)
                    self.version()
                    self.version()
                    self.container("sh", "-ec", 'test "$(id -u)" = 1000; test -x /app/data/bin/sub2api; '
                                   'test -w /app/data/bin; test ! -w /app/sub2api; '
                                   'test -s /app/data/bin/.image-seed.sha256; cmp /app/sub2api /app/data/bin/sub2api; '
                                   'test "$(cat /app/data/bin/.image-seed.sha256)" = "$(sha256sum /app/sub2api | cut -d " " -f 1)"')
                with self.check("same-seed-preservation"):
                    updated = self.container("sh", "-ec", 'printf "\\nPK_JENKINS_UPDATED\\n" >> /app/data/bin/sub2api; '
                                             'sha256sum /app/data/bin/sub2api')
                    self.version()
                    require(self.container("sha256sum", "/app/data/bin/sub2api") == updated,
                            "Same image seed overwrote a web update")
                with self.check("interrupted-update-recovery"):
                    self.container("mv", "/app/data/bin/sub2api", "/app/data/bin/sub2api.backup")
                    self.version()
                    require(self.container("sha256sum", "/app/data/bin/sub2api") == updated,
                            "Interrupted update did not recover its backup")
                    self.container("test", "!", "-e", "/app/data/bin/sub2api.backup")
                with self.check("fixture-preparation"):
                    fixture = self.fixture_image()
                with self.check("changed-seed-replacement"):
                    self.version(fixture)
                    self.container("cmp", "/app/sub2api", "/app/data/bin/sub2api", image=fixture)
                    require(self.container("sha256sum", "/app/data/bin/sub2api", image=fixture).split()[0]
                            != self.data["binary_sha256"], "Changed image seed did not replace the runtime binary")
                with self.check("original-image-rollback"):
                    self.version()
                    self.container("cmp", "/app/sub2api", "/app/data/bin/sub2api")
                if deployment.declaration(self.data):
                    with self.check("browser-runtime"):
                        output = self.container("-ec", deployment.BROWSER_CHECK,
                                                volume=False, entrypoint="/bin/sh", browser=True)
                        require("<title>pk-browser-ready</title>" in output,
                                "Bundled Chromium could not render the isolated local page")
                if browser.declaration(self.data):
                    with self.check("browser-web-update"):
                        self.browser_web_update()
            finally:
                with self.check("cleanup"):
                    self.cleanup()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifact-dir", type=Path, default=Path("output"))
    parser.add_argument("--output", type=Path, help="Report file; defaults to artifact-dir/artifact-validation.json")
    args = parser.parse_args()
    directory = args.artifact_dir.resolve(strict=True)
    output = args.output or directory / "artifact-validation.json"
    require(not output.is_symlink(), "Report path must not be a symlink")
    require(output.name == "artifact-validation.json", "Report must be named artifact-validation.json")
    report = {"schema_version": 2, "tests_profile": PROFILE, "profile": PROFILE,
              "repository": REPOSITORY, "status": "failed", "started_at": now(), "checks": []}
    def interrupted(signum, _frame):
        raise RuntimeError("Runtime validation interrupted by signal " + str(signum))
    for name in ("SIGTERM", "SIGHUP"):
        if hasattr(signal, name):
            signal.signal(getattr(signal, name), interrupted)
    try:
        Validator(directory, report).run()
        expected_checks = list(REQUIRED_CHECKS)
        if report.get("deployment_profile") == deployment.PROFILE:
            expected_checks.insert(-1, "browser-runtime")
        if report.get("browser_profile") == browser.PROFILE:
            expected_checks.insert(-1, "browser-web-update")
        require([item["name"] for item in report["checks"]] == expected_checks, "Incomplete runtime checks")
        require(all(item["status"] == "passed" and item["exit_code"] == 0 for item in report["checks"]),
                "Runtime checks failed")
        report["status"] = "passed"
    except BaseException as error:
        report["error"] = str(error)
        print("Artifact validation failed: " + str(error), file=sys.stderr)
    finally:
        report["finished_at"] = now()
        output.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print("Artifact validation " + report["status"] + ": " + str(output))
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())
