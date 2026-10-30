"""Bundle the exact reviewed browser image; never install or execute software."""
from __future__ import annotations

import hashlib
import io
import json
from pathlib import Path
import re
import tarfile

PROFILE = "pk-browser-deployment-v1"
BINDINGS = ("deployment_profile", "deployment_archive_sha256")
FILES = {"install.sh", "README.md", "docker-compose.yml", "image.txt", "image-id.txt", "image-config-id.txt",
         "images/sub2api.docker.tar.gz"}
MAX_ARCHIVE = 2 * 1024 * 1024 * 1024
BROWSER_CHECK = r'''
test "$(id -u)" = 1000
test "$PK_AUTH_BROWSER_PATH" = /usr/bin/chromium
test "$PK_AUTH_BROWSER_NO_SANDBOX" = true
test -x "$PK_AUTH_BROWSER_PATH"
profile=$(mktemp -d /tmp/pk-browser.XXXXXX)
trap 'rm -rf "$profile"' EXIT
export HOME="$profile" XDG_CACHE_HOME="$profile/cache" XDG_CONFIG_HOME="$profile/config"
"$PK_AUTH_BROWSER_PATH" --headless=new --no-sandbox --disable-gpu \
    --disable-dev-shm-usage --disable-background-networking --no-first-run \
    --no-default-browser-check --user-data-dir="$profile/profile" \
    --dump-dom 'data:text/html,<title>pk-browser-ready</title><p>ready</p>'
'''


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def digest(path: Path) -> str:
    require(path.is_file() and not path.is_symlink(), "Missing regular deployment artifact")
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def image_config_id(path: Path) -> str:
    # Docker may identify the same saved image by its OCI index or config ID.
    # Both identities come from the sealed archive, never the destination host.
    with tarfile.open(path, "r:gz") as archive:
        member = archive.getmember("manifest.json")
        require(member.isfile() and member.size <= 1024 * 1024, "Invalid Docker compatibility manifest")
        with archive.extractfile(member) as stream:
            records = json.load(stream)
        require(isinstance(records, list) and len(records) == 1, "Expected one bundled application image")
        config = archive.getmember(records[0]["Config"])
        require(config.isfile() and config.size <= 1024 * 1024, "Invalid Docker image config")
        with archive.extractfile(config) as stream:
            return "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()


def declaration(data: dict) -> bool:
    version = data.get("version", "")
    require(isinstance(version, str) and re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-pk\.[1-9][0-9]*)?(?:-pk\.candidate\.[1-9][0-9]*)?", version),
            "Invalid deployment version")
    formal = re.fullmatch(r"([0-9]+)\.([0-9]+)\.([0-9]+)-pk\.([1-9][0-9]*)", version)
    required = formal is not None and tuple(map(int, formal.groups())) >= (0, 2, 19, 10)
    if "deployment_profile" not in data:
        require(not required, "This release requires its bundled browser deployment profile")
        require(not any(key.startswith("deployment_") for key in data),
                "Deployment evidence must not lose its profile")
        return False
    require(data["deployment_profile"] == PROFILE, "Unknown deployment bundle profile")
    # Excludes linux_amd64: older web updaters select by that substring.
    require(data.get("deployment_archive") == f"sub2api-deployment-{data['version']}-amd64.tgz",
            "Unexpected deployment archive name")
    return True


def checksums(data: dict) -> str:
    keys = ["binary_archive"]
    if declaration(data):
        keys.append("deployment_archive")
    return "".join(data[key + "_sha256"] + "  " + data[key] + "\n" for key in keys)


def create(directory: Path, data: dict, pipeline: Path) -> None:
    if not declaration(data):
        return
    content = {}
    for name in ("install.sh", "README.md"):
        path = pipeline / "deploy/bundle" / name
        require(path.is_file() and not path.is_symlink(), "Missing trusted deployment entrypoint")
        content[name] = path.read_bytes().replace(b"\r\n", b"\n")
    compose = (pipeline / "deploy/docker-compose.local.yml").read_text(encoding="utf-8")
    # Compose scopes names to the new target directory, avoiding collisions.
    compose = "\n".join(line for line in compose.splitlines()
                        if not re.fullmatch(r"\s+container_name:.*", line)) + "\n"
    anchor = "    image: ghcr.io/pengkang0322/sub2api:${SUB2API_PK_VERSION:-latest}\n"
    require(compose.count(anchor) == 1, "Unexpected protected deployment template")
    compose = compose.replace(anchor, anchor + "    pull_policy: never\n")
    content["docker-compose.yml"] = compose.encode("utf-8")
    content["image.txt"] = (data["image_tag"] + "\n").encode()
    content["image-id.txt"] = (data["image_id"] + "\n").encode()
    image_name = "images/sub2api.docker.tar.gz"
    image = directory / data["image_archive"]
    require(digest(image) == data["image_archive_sha256"], "Image changed before deployment packaging")
    content["image-config-id.txt"] = (image_config_id(image) + "\n").encode()
    hashes = {name: hashlib.sha256(value).hexdigest() for name, value in content.items()}
    hashes[image_name] = data["image_archive_sha256"]
    content["SHA256SUMS"] = "".join(hashes[name] + "  " + name + "\n" for name in sorted(hashes)).encode()
    archive_path = directory / data["deployment_archive"]
    require(not archive_path.exists() and not archive_path.is_symlink(), "Deployment package already exists")
    with tarfile.open(archive_path, "w:gz", compresslevel=1) as archive:
        for name in sorted(content):
            value = content[name]
            entry = tarfile.TarInfo(name)
            entry.size = len(value)
            entry.mode = 0o755 if name == "install.sh" else 0o644
            archive.addfile(entry, io.BytesIO(value))
        entry = tarfile.TarInfo(image_name)
        entry.size = image.stat().st_size
        entry.mode = 0o644
        with image.open("rb") as stream:
            archive.addfile(entry, stream)
    require(archive_path.stat().st_size < MAX_ARCHIVE, "Deployment package exceeds GitHub's asset size limit")
    data["deployment_files_sha256"] = hashes
    data["deployment_archive_sha256"] = digest(archive_path)


def verify(directory: Path, data: dict) -> None:
    if not declaration(data):
        return
    hashes = data.get("deployment_files_sha256")
    require(isinstance(hashes, dict) and set(hashes) == FILES, "Incomplete deployment inventory")
    require(all(isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value)
                for value in hashes.values()), "Invalid deployment file digest")
    require(hashes["images/sub2api.docker.tar.gz"] == data["image_archive_sha256"],
            "Deployment package must reuse the validated image")
    path = directory / data["deployment_archive"]
    require(digest(path) == data.get("deployment_archive_sha256") and path.stat().st_size < MAX_ARCHIVE,
            "Deployment package digest or size is invalid")
    with tarfile.open(path, "r:gz") as archive:
        seen = set()
        small = {}
        for member in archive:
            name = member.name
            require(name in FILES | {"SHA256SUMS"} and name not in seen and member.isfile()
                    and not member.issym() and not member.islnk(), "Unsafe deployment archive member")
            seen.add(name)
            require(member.mode == (0o755 if name == "install.sh" else 0o644), "Unexpected deployment file mode")
            stream = archive.extractfile(member)
            require(stream is not None, "Missing deployment member")
            with stream:
                if name == "images/sub2api.docker.tar.gz":
                    require(member.size == (directory / data["image_archive"]).stat().st_size,
                            "Bundled image size differs from the validated image")
                    require(hashlib.file_digest(stream, "sha256").hexdigest() == hashes[name],
                            "Bundled image differs from the validated image")
                else:
                    require(member.size <= 1024 * 1024, "Deployment metadata is too large")
                    value = stream.read()
                    small[name] = value
                    if name != "SHA256SUMS":
                        require(hashlib.sha256(value).hexdigest() == hashes[name], "Deployment metadata digest mismatch")
        require(seen == FILES | {"SHA256SUMS"}, "Deployment package members are incomplete")
        expected = "".join(hashes[name] + "  " + name + "\n" for name in sorted(hashes)).encode()
        require(small["SHA256SUMS"] == expected, "Deployment checksum file differs")
        require(small["image.txt"] == (data["image_tag"] + "\n").encode()
                and small["image-id.txt"] == (data["image_id"] + "\n").encode(), "Deployment image identity differs")
        require(small["image-config-id.txt"] == (image_config_id(directory / data["image_archive"]) + "\n").encode(),
                "Deployment image config identity differs")
