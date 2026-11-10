#!/usr/bin/env python3
"""Publish already validated local builds to the fixed private GitHub repository.

Requires Python 3.11+ and PK_GITHUB_TOKEN. This tool neither builds nor deploys,
runs no Docker commands, and never publishes to a container registry. The image
archive is a Release asset for docker load, not evidence of a GHCR push.

  python deploy/local-release.py check --artifact-dir <sealed-package-directory>
  python deploy/local-release.py publish --artifact-dir <sealed-package-directory>

Both commands enforce the shared manifest, artifact and eleven-check evidence
gates. check performs read-only GitHub checks. publish stages a private copy,
creates a new draft, uploads and verifies every asset, rechecks source provenance,
then publishes. Any failure leaves the draft untouched for manual investigation;
rerunning never overwrites, resumes or deletes an existing Release or asset.
"""
from __future__ import annotations

import argparse
from dataclasses import dataclass
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import shutil
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parent / "jenkins"))
import control
import startup_guard_publication as startup_guard

REPOSITORY = control.REPOSITORY
API_ROOT = "/repos/" + REPOSITORY
MAX_JSON_BYTES = 4 * 1024 * 1024
OPTIONAL_EVIDENCE = (
    "local-build.json", "build-audit.json", "local-frontend-audit.json",
    "local-frontend-audit-npm.json", "review.json",
)
require = control.require


@dataclass(frozen=True)
class Asset:
    name: str
    size: int
    sha256: str


def token() -> str:
    value = os.environ.get("PK_GITHUB_TOKEN", "")
    require(bool(value) and value.strip() == value and "\n" not in value and "\r" not in value,
            "PK_GITHUB_TOKEN is missing or invalid")
    return value


def request(path: str, credential: str, *, method: str = "GET", data=None):
    require(path.startswith(API_ROOT + "/") or path == API_ROOT, "Unexpected GitHub API path")
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "sub2api-local-release",
               "X-GitHub-Api-Version": "2022-11-28", "Authorization": "Bearer " + credential}
    body = None
    if data is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(data).encode("utf-8")
    req = urllib.request.Request("https://api.github.com" + path, data=body,
                                 headers=headers, method=method)
    try:
        with urllib.request.build_opener(control.NoRedirect()).open(req, timeout=60) as response:
            raw = response.read(MAX_JSON_BYTES + 1)
            require(len(raw) <= MAX_JSON_BYTES, "GitHub JSON response is too large")
            return json.loads(raw)
    except urllib.error.HTTPError as error:
        request_id = error.headers.get("X-GitHub-Request-Id", "unavailable")
        raise RuntimeError(f"GitHub API {method}: HTTP {error.code}; request ID {request_id}") from None


def pages(path: str, credential: str) -> list[dict]:
    output = []
    for page in range(1, 1001):
        batch = request(path + f"?per_page=100&page={page}", credential)
        require(isinstance(batch, list) and all(isinstance(item, dict) for item in batch),
                "Unexpected GitHub paginated response")
        output.extend(batch)
        if len(batch) < 100:
            return output
    raise RuntimeError("GitHub pagination limit reached; refusing incomplete verification")


def verify_source(data: dict, credential: str, own_draft: int | None = None) -> None:
    """Recheck provenance after upload without treating our own draft as unused."""
    repository = request(API_ROOT, credential)
    require(repository.get("private") is True and repository.get("full_name") == REPOSITORY,
            "The release repository must remain private")
    tag = "v" + data["version"]
    ref = request(API_ROOT + "/git/ref/tags/" + tag, credential)["object"]
    for _ in range(8):
        if ref.get("type") == "commit":
            break
        require(ref.get("type") == "tag" and re.fullmatch(r"[0-9a-f]{40}", ref.get("sha", "")),
                "Unexpected Git tag object")
        ref = request(API_ROOT + "/git/tags/" + ref["sha"], credential)["object"]
    require(ref.get("type") == "commit" and ref.get("sha") == data["commit"],
            "The formal tag no longer matches the validated commit")
    commit = request(API_ROOT + "/git/commits/" + data["commit"], credential)
    require(commit.get("tree", {}).get("sha") == data["tree"], "Source tree does not match the formal commit")
    comparison = request(API_ROOT + "/compare/" + data["commit"] + "...main", credential)
    require(comparison.get("status") in {"ahead", "identical"}, "The commit is not in private main history")
    matches = [release for release in pages(API_ROOT + "/releases", credential)
               if release.get("tag_name") == tag]
    if own_draft is None:
        require(not matches, "This tag already has a Release or draft; never overwrite or resume it")
    else:
        require(len(matches) == 1 and matches[0].get("id") == own_draft
                and matches[0].get("draft") is True and matches[0].get("prerelease") is False,
                "Draft identity or tag ownership changed; refusing publication")


def regular(directory: Path, name: str) -> Path:
    require(isinstance(name, str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", name),
            "Invalid release asset name")
    path = directory / name
    require(path.is_file() and not path.is_symlink() and path.resolve().parent == directory,
            "Missing regular release asset: " + name)
    require(path.stat().st_size <= 2 * 1024 * 1024 * 1024, "Release asset exceeds 2 GiB: " + name)
    return path


def check_review(value) -> None:
    """Do not publish the operational review's retained paths or server identity."""
    if isinstance(value, dict):
        for key, item in value.items():
            require(not str(key).lower().startswith("retained_")
                    and str(key).lower() not in {"host", "hostname", "server", "server_ip", "ssh_user",
                                                  "username", "password", "token", "private_key"},
                    "review.json contains operational identity fields; supply a sanitized review")
            check_review(item)
    elif isinstance(value, list):
        for item in value:
            check_review(item)
    elif isinstance(value, str):
        require(not value.startswith(("/", "\\\\")) and not re.match(r"^[A-Za-z]:[\\/]", value),
                "review.json contains an absolute operational path; supply a sanitized review")


def validate(directory: Path) -> tuple[dict, list[Asset]]:
    data = control.manifest(directory)
    require(data.get("validation_profile") == control.VALIDATION_PROFILE,
            "Local releases require the existing exact-artifact validation profile")
    require(data.get("build_environment") == "local"
            and data.get("validation_environment") == "server-isolated"
            and not data.get("jenkins_build_url"),
            "Manifest must identify local compilation and server-isolated validation truthfully")
    control.verify_artifacts(directory, data)
    control.corresponding_source.verify(directory, data, checkout=Path(__file__).resolve().parents[1])
    control.package_evidence(directory, data)
    proof = json.loads(regular(directory, "artifact-validation.json").read_bytes())
    require(len(proof["checks"]) == 11 and {item["name"] for item in proof["checks"]} == control.RUNTIME_CHECKS,
            "All eleven isolated exact-artifact checks are required")
    names = [data["binary_archive"], data["image_archive"], "checksums.txt",
             "manifest.json", "artifact-validation.json"]
    names.extend(control.corresponding_source.assets(data))
    if data.get("schema_version") == 2:
        names.extend([control.dependencies.REPORT, *control.dependencies.FILES.values()])
    for name in OPTIONAL_EVIDENCE:
        path = directory / name
        if path.exists() or path.is_symlink():
            path = regular(directory, name)
            payload = json.loads(path.read_bytes())
            require(isinstance(payload, dict), "Evidence must be a JSON object: " + name)
            if name == "review.json":
                check_review(payload)
            names.append(name)
    require(len(names) == len(set(names)), "Duplicate release asset names")
    assets = []
    for name in names:
        path = regular(directory, name)
        before = path.stat()
        digest = control.sha256(path)
        after = path.stat()
        require((before.st_size, before.st_mtime_ns) == (after.st_size, after.st_mtime_ns),
                "Release asset changed during validation: " + name)
        assets.append(Asset(name, after.st_size, digest))
    return data, assets


def verify_asset(remote: dict, expected: Asset) -> None:
    require(remote.get("name") == expected.name and remote.get("state") == "uploaded"
            and type(remote.get("size")) is int and remote["size"] == expected.size
            and remote.get("digest") == "sha256:" + expected.sha256,
            "Uploaded asset name/state/size/SHA256 mismatch: " + expected.name)


def upload(directory: Path, expected: Asset, release_id: int, credential: str) -> None:
    path = regular(directory, expected.name)
    require(path.stat().st_size == expected.size and control.sha256(path) == expected.sha256,
            "Staged asset changed before upload: " + expected.name)
    connection = http.client.HTTPSConnection("uploads.github.com", timeout=180)
    try:
        url = f"{API_ROOT}/releases/{release_id}/assets?name=" + urllib.parse.quote(expected.name, safe="")
        connection.putrequest("POST", url)
        for name, value in {"Authorization": "Bearer " + credential,
                            "User-Agent": "sub2api-local-release", "Accept": "application/vnd.github+json",
                            "X-GitHub-Api-Version": "2022-11-28", "Content-Type": "application/octet-stream",
                            "Content-Length": str(expected.size)}.items():
            connection.putheader(name, value)
        connection.endheaders()
        checksum, sent = hashlib.sha256(), 0
        with path.open("rb") as stream:
            while block := stream.read(1024 * 1024):
                sent += len(block)
                require(sent <= expected.size, "Staged asset grew while uploading: " + expected.name)
                checksum.update(block)
                connection.send(block)
        require(sent == expected.size and checksum.hexdigest() == expected.sha256,
                "Staged asset changed while uploading: " + expected.name)
        response = connection.getresponse()
        require(response.status == 201,
                f"Asset upload HTTP {response.status}; request ID "
                + response.getheader("X-GitHub-Request-Id", "unavailable"))
        raw = response.read(MAX_JSON_BYTES + 1)
        require(len(raw) <= MAX_JSON_BYTES, "Upload response is too large")
        verify_asset(json.loads(raw), expected)
    finally:
        connection.close()


def verify_assets(release_id: int, assets: list[Asset], credential: str) -> None:
    uploaded = pages(f"{API_ROOT}/releases/{release_id}/assets", credential)
    expected = {asset.name: asset for asset in assets}
    require(len(uploaded) == len(expected) and {item.get("name") for item in uploaded} == set(expected),
            "Release assets are missing, duplicated or unexpected")
    for item in uploaded:
        verify_asset(item, expected[item["name"]])


def notes(data: dict, assets: list[Asset]) -> str:
    lines = ["PK 本机构建发行", "", "本机编译；同一程序与镜像在服务器隔离环境完成全部十一项制品验收。",
             "未使用 Jenkins，未发布 GHCR 镜像。本 Release 的 Docker 镜像归档须下载后通过 docker load 导入。",
             "发布 Release 本身不会部署或重启生产环境。未声称执行完整源码回归。", "",
             f"Source: `{data['commit']}`", f"Tree: `{data['tree']}`", f"Package: `{data['package_id']}`",
             f"Binary SHA256: `{data['binary_sha256']}`", f"Image identity: `{data['image_id']}`", "",
             "Chromium 沿用外置运行环境；程序更新归档不包含浏览器。", "", "Assets (bytes, SHA256):"]
    for asset in assets:
        lines.append(f"- `{asset.name}` · {asset.size} · `{asset.sha256}`")
    return "\n".join(lines)


def publish(directory: Path, data: dict, assets: list[Asset], credential: str) -> None:
    # Isolate the bytes that have passed validation. Never upload arbitrary files
    # from the build directory, credentials, or an operational server config.
    with tempfile.TemporaryDirectory(prefix="sub2api-local-release-") as temporary:
        staged = Path(temporary).resolve()
        for asset in assets:
            shutil.copyfile(regular(directory, asset.name), staged / asset.name)
        staged_data, staged_assets = validate(staged)
        require(staged_data == data and staged_assets == assets, "Package changed while staging")
        control.release_ready(data, credential=credential)
        verify_source(data, credential)
        # Authorize only bytes already validated against the formal source and
        # artifact evidence. Never expose a release the managed host cannot run.
        startup_guard.authorize(staged, data, "prepare")
        release = request(API_ROOT + "/releases", credential, method="POST", data={
            "tag_name": "v" + data["version"], "target_commitish": data["commit"],
            "name": data["version"] + " PK出版", "body": notes(data, assets),
            "draft": True, "prerelease": False,
        })
        release_id = release.get("id")
        require(type(release_id) is int and release_id > 0 and release.get("draft") is True,
                "Unexpected draft creation response; inspect the private repository before retrying")
        print(f"Created draft {release_id} for v{data['version']}; failures leave this draft in place.", flush=True)
        try:
            for asset in assets:
                upload(staged, asset, release_id, credential)
            verify_assets(release_id, assets, credential)
            verify_source(data, credential, own_draft=release_id)
            draft = request(f"{API_ROOT}/releases/{release_id}", credential)
            require(draft.get("id") == release_id and draft.get("draft") is True
                    and draft.get("tag_name") == "v" + data["version"]
                    and draft.get("body") == notes(data, assets), "Draft metadata changed during upload")
            startup_guard.authorize(staged, data, "check")
            released = request(f"{API_ROOT}/releases/{release_id}", credential, method="PATCH",
                               data={"draft": False, "prerelease": False, "make_latest": "true"})
            require(released.get("id") == release_id and released.get("draft") is False
                    and released.get("prerelease") is False
                    and released.get("tag_name") == "v" + data["version"],
                    "Unexpected publication response; inspect the Release before any retry")
        except BaseException:
            print(f"Release {release_id} was not deleted or overwritten. If the final PATCH was sent, "
                  "check its state before retrying; otherwise the draft remains unpublished.", file=sys.stderr)
            raise
        print(f"Published https://github.com/{REPOSITORY}/releases/tag/v{data['version']}; "
              "GHCR was not published; the application binary and service were not changed. "
              "Managed startup authorization was checked before publication.")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("check", "publish"):
        command = commands.add_parser(name)
        command.add_argument("--artifact-dir", type=Path, required=True)
    args = parser.parse_args()
    require(not args.artifact_dir.is_symlink(), "Artifact directory must not be a symlink")
    directory = args.artifact_dir.resolve(strict=True)
    require(directory.is_dir(), "Artifact directory is missing")
    data, assets = validate(directory)
    credential = token()
    if args.command == "check":
        control.release_ready(data, credential=credential)
        verify_source(data, credential)
        startup_guard.authorize(directory, data, "plan")
        print(json.dumps({"repository": REPOSITORY, "version": data["version"], "commit": data["commit"],
                          "package_id": data["package_id"], "status": "ready", "checks": 11,
                          "assets": [asset.__dict__ for asset in assets], "published": False}, indent=2))
    else:
        publish(directory, data, assets, credential)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (RuntimeError, OSError, ValueError, KeyError, TypeError, http.client.HTTPException) as error:
        print("PK local release: " + str(error), file=sys.stderr)
        sys.exit(1)
