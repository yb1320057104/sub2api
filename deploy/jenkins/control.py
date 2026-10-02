#!/usr/bin/env python3
"""Trusted Jenkins packaging metadata, local-evidence gate and private publisher.

Load from the reviewed pipeline checkout, never from parameter-selected source.
No test or application process is executed by this script.
"""
from __future__ import annotations

import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.parse
import urllib.request

REPOSITORY = "pengkang0322/sub2api"
IMAGE = "ghcr.io/pengkang0322/sub2api"
API = "https://api.github.com"
TAG_PATTERN = r"v[0-9]+\.[0-9]+\.[0-9]+-pk\.[1-9][0-9]*"
PACKAGE_PATTERN = r"[1-9][0-9]*-[0-9a-f]{12}"
REQUIRED_SUITES = {
    "backend-unit", "backend-integration", "backend-race", "backend-lint",
    "frontend", "shell", "binance-proxy", "release-helpers", "security",
    "artifact-runtime",
}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError("Unexpected GitHub API redirect; credentials were not forwarded")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def command(*args: str, cwd: Path | None = None, capture: bool = True,
            input_data: bytes | None = None, env: dict | None = None) -> str:
    result = subprocess.run(args, cwd=cwd, input=input_data, check=True, env=env,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout.decode().strip() if capture else ""


def git(*args: str) -> str:
    return command("git", *args, cwd=Path("source"))


def sha256(path: Path) -> str:
    require(path.is_file() and not path.is_symlink(), f"Not a regular artifact: {path.name}")
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def write_json(path: Path, data: dict) -> None:
    path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def request(path: str, method: str = "GET", data=None, token: str | None = None,
            allow_404: bool = False):
    require(path.startswith("/"), "GitHub API path must be absolute")
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "sub2api-jenkins",
               "X-GitHub-Api-Version": "2022-11-28"}
    if token:
        headers["Authorization"] = "Bearer " + token
    body = None
    if data is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(data).encode()
    req = urllib.request.Request(API + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.build_opener(NoRedirect()).open(req, timeout=60) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        if allow_404 and error.code == 404:
            return None
        raise RuntimeError(f"GitHub API {method} failed with HTTP {error.code}") from None


def token() -> str:
    value = os.environ.get("PK_API_TOKEN", "")
    require(bool(value), "The dedicated private-release credential is not configured")
    return value


def parameters() -> None:
    action = os.environ.get("ACTION", "package")
    require(action in {"package", "publish"}, "Unknown Jenkins action")
    require(os.environ.get("PK_REPOSITORY") == REPOSITORY, "Only the protected private repository is supported")
    require(os.environ.get("PK_IMAGE") == IMAGE, "Only the protected private image is supported")
    if action == "package":
        ref = os.environ.get("SOURCE_REF", "")
        require(bool(re.fullmatch(r"[0-9a-f]{40}|refs/(heads|tags)/[A-Za-z0-9._/-]+", ref)),
                "SOURCE_REF must be a full SHA or refs/heads/... or refs/tags/...")
        require(".." not in ref and "//" not in ref and not ref.endswith(("/", ".lock")), "Invalid Git ref")
    else:
        require(bool(re.fullmatch(PACKAGE_PATTERN, os.environ.get("PACKAGE_ID", ""))), "Invalid package ID")


def prepare() -> None:
    parameters()
    require(not git("status", "--porcelain"), "The source checkout is not clean")
    require(git("remote", "get-url", "origin") == "git@github.com:" + REPOSITORY + ".git",
            "Unexpected source repository")
    # The dedicated deploy key proves source access. Anonymous API must not expose
    # its metadata; an HTTP/network error other than 404 fails closed.
    public = request("/repos/" + REPOSITORY, allow_404=True)
    require(public is None, "Repository metadata is publicly accessible; refuse private packaging")
    ref = os.environ["SOURCE_REF"]
    commit = git("rev-parse", "HEAD")
    tree = git("rev-parse", "HEAD^{tree}")
    build = os.environ.get("BUILD_NUMBER", "")
    require(bool(re.fullmatch(r"[1-9][0-9]*", build)), "Invalid Jenkins build number")
    if re.fullmatch(r"[0-9a-f]{40}", ref):
        require(commit == ref, "Checkout did not resolve to the requested commit")
    elif ref.startswith("refs/heads/"):
        require(commit == git("rev-parse", "refs/remotes/origin/" + ref.removeprefix("refs/heads/")),
                "Checkout did not resolve to the requested branch")
    if ref.startswith("refs/tags/"):
        tag = ref.removeprefix("refs/tags/")
        require(bool(re.fullmatch(TAG_PATTERN, tag)), "Release packaging requires an unused vX.Y.Z-pk.N tag")
        require(git("rev-parse", ref + "^{commit}") == commit, "Tag and source commit differ")
        git("merge-base", "--is-ancestor", commit, "refs/remotes/origin/main")
        version, build_type = tag[1:], "release"
    else:
        base = Path("source/backend/cmd/server/VERSION").read_text().strip()
        require(bool(re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-pk\.[1-9][0-9]*)?", base)), "Invalid source VERSION")
        version, build_type = f"{base}-pk.candidate.{build}", "candidate"
    output = Path("output")
    output.mkdir()
    manifest = {
        "schema_version": 1, "repository": REPOSITORY, "commit": commit, "tree": tree,
        "source_ref": ref, "version": version, "build_type": build_type,
        "platform": "linux/amd64", "package_id": f"{build}-{commit[:12]}",
        "built_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "jenkins_build_url": os.environ.get("BUILD_URL", ""),
        "image_tag": IMAGE + ":" + version,
        "binary_archive": f"sub2api_{version}_linux_amd64.tar.gz",
        "image_archive": f"sub2api_{version}_linux_amd64.docker.tar.gz",
        "validation": "Pending local source and artifact-runtime tests; this package is not published",
    }
    write_json(output / "manifest.json", manifest)
    print(f"Packaging {commit} as {version}; no GitHub Actions build or production deployment")


def manifest(directory: Path) -> dict:
    path = directory / "manifest.json"
    require(path.is_file() and not path.is_symlink(), "Missing regular manifest")
    data = json.loads(path.read_text(encoding="utf-8"))
    require(data.get("schema_version") == 1 and data.get("repository") == REPOSITORY, "Unexpected package provenance")
    require(bool(re.fullmatch(PACKAGE_PATTERN, data.get("package_id", ""))), "Invalid manifest package ID")
    require(bool(re.fullmatch(r"[0-9a-f]{40}", data.get("commit", ""))), "Invalid manifest commit")
    require(bool(re.fullmatch(r"[0-9a-f]{40}", data.get("tree", ""))), "Invalid manifest tree")
    require(data.get("platform") == "linux/amd64", "Unsupported platform")
    version = data.get("version", "")
    require(bool(re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-pk\.[1-9][0-9]*)?(?:-pk\.candidate\.[1-9][0-9]*)?", version)), "Invalid package version")
    require(data.get("image_tag") == IMAGE + ":" + version, "Unexpected package image")
    require(data.get("binary_archive") == f"sub2api_{version}_linux_amd64.tar.gz", "Unexpected binary archive name")
    require(data.get("image_archive") == f"sub2api_{version}_linux_amd64.docker.tar.gz", "Unexpected image archive name")
    return data


def verify_artifacts(directory: Path, data: dict) -> None:
    for key in ("binary_archive", "image_archive"):
        require(sha256(directory / data[key]) == data.get(key + "_sha256"), f"Artifact checksum mismatch: {key}")
    require(bool(re.fullmatch(r"[0-9a-f]{64}", data.get("binary_sha256", ""))), "Invalid binary digest")
    with tarfile.open(directory / data["binary_archive"], "r:gz") as archive:
        members = archive.getmembers()
        require(len(members) == 1 and members[0].name == "sub2api" and members[0].isfile(),
                "Update archive must contain exactly the regular sub2api binary")
        stream = archive.extractfile(members[0])
        require(stream is not None, "Update archive binary is missing")
        with stream:
            require(hashlib.file_digest(stream, "sha256").hexdigest() == data["binary_sha256"],
                    "Update archive does not contain the recorded binary")
    require(bool(re.fullmatch(r"sha256:[0-9a-f]{64}", data.get("image_id", ""))), "Invalid image ID")
    expected = data["binary_archive_sha256"] + "  " + data["binary_archive"] + "\n"
    require((directory / "checksums.txt").read_text() == expected, "Unexpected release checksum file")


def package_path(package_id: str) -> Path:
    require(bool(re.fullmatch(PACKAGE_PATTERN, package_id)), "Invalid package path")
    root = Path(os.environ["PK_PACKAGE_ROOT"]).resolve(strict=True)
    require(root.is_dir(), "Package root missing")
    path = root / package_id
    require(not path.is_symlink(), "Package path must not be a symlink")
    return path


def seal() -> None:
    output = Path("output")
    data = manifest(output)
    for key in ("binary_archive", "image_archive"):
        data[key + "_sha256"] = sha256(output / data[key])
    data["binary_sha256"] = (output / "binary-sha256.txt").read_text().strip()
    data["image_id"] = (output / "image-id.txt").read_text().strip()
    (output / "checksums.txt").write_text(data["binary_archive_sha256"] + "  " + data["binary_archive"] + "\n")
    write_json(output / "manifest.json", data)
    verify_artifacts(output, data)
    destination = package_path(data["package_id"])
    require(not destination.exists(), "A package ID may never be overwritten")
    # A hidden sibling is renamed only after all artifact copies are complete.
    with tempfile.TemporaryDirectory(prefix=".sealing-", dir=destination.parent) as temporary:
        staged = Path(temporary) / data["package_id"]
        shutil.copytree(output, staged)
        for file in staged.iterdir():
            file.chmod(0o440)
        staged.chmod(0o550)
        staged.rename(destination)
    print("PACKAGE_ID=" + data["package_id"])
    print("Download the artifacts, run local validation, then upload the matching evidence before publish.")


def restore() -> None:
    parameters()
    directory = package_path(os.environ["PACKAGE_ID"])
    data = manifest(directory)
    require(data["package_id"] == os.environ["PACKAGE_ID"], "Package ID does not match directory")
    verify_artifacts(directory, data)
    shutil.copytree(directory, "output")
    Path("output").chmod(0o750)
    for file in Path("output").iterdir():
        file.chmod(0o640)


def evidence(data: dict) -> dict:
    root = Path(os.environ["PK_EVIDENCE_ROOT"]).resolve(strict=True)
    path = root / (data["package_id"] + ".json")
    require(path.is_file() and not path.is_symlink(), "Missing local test evidence for this package")
    proof = json.loads(path.read_text(encoding="utf-8-sig"))
    require(proof.get("schema_version") == 1 and proof.get("tests_profile") == "pk-local-v1", "Unsupported local validation profile")
    for key in ("repository", "commit", "tree"):
        require(proof.get(key) == data[key], f"Local evidence does not match package {key}")
    require(proof.get("status") == "passed", "Local tests did not pass")
    suites = proof.get("suites", [])
    names = [suite.get("name") for suite in suites]
    require(len(names) == len(set(names)) and REQUIRED_SUITES.issubset(names), "Local evidence is missing required suites or duplicates them")
    for suite in suites:
        require(suite.get("status") == "passed" and suite.get("exit_code") == 0, "Local suite failed or was skipped: " + str(suite.get("name")))
    for key in ("package_id", "binary_sha256", "image_archive_sha256"):
        require(proof.get("artifact", {}).get(key) == data[key], f"Runtime evidence does not match package {key}")
    return proof


def release_ready(data: dict) -> None:
    require(data.get("build_type") == "release", "Candidate packages cannot become formal releases")
    tag = "v" + data["version"]
    require(bool(re.fullmatch(TAG_PATTERN, tag)) and data.get("source_ref") == "refs/tags/" + tag, "Package lacks a formal reviewed tag")
    credential = token()
    repo = request("/repos/" + REPOSITORY, token=credential)
    require(repo.get("private") is True and repo.get("full_name") == REPOSITORY, "Release repository must remain private")
    ref = request("/repos/" + REPOSITORY + "/git/ref/tags/" + tag, token=credential)["object"]
    for _ in range(8):
        if ref["type"] == "commit":
            break
        require(ref["type"] == "tag", "Unsupported Git tag object")
        ref = request("/repos/" + REPOSITORY + "/git/tags/" + ref["sha"], token=credential)["object"]
    require(ref["type"] == "commit" and ref["sha"] == data["commit"], "The release tag moved after packaging")
    comparison = request("/repos/" + REPOSITORY + "/compare/" + data["commit"] + "...main", token=credential)
    require(comparison.get("status") in {"ahead", "identical"}, "Release commit is not in current private main history")
    existing = request("/repos/" + REPOSITORY + "/releases/tags/" + tag, token=credential, allow_404=True)
    require(existing is None, "This release already exists; never overwrite it")


def ready() -> None:
    data = manifest(Path("output"))
    verify_artifacts(Path("output"), data)
    proof = evidence(data)
    release_ready(data)
    write_json(Path("output/local-test-evidence.json"), proof)
    print("Local source and exact-artifact validation passed; private tag and main ancestry verified.")


def package_registry_ready(data: dict, credential: str) -> None:
    path = "/users/pengkang0322/packages/container/sub2api"
    package = request(path, token=credential)
    require(package.get("visibility") == "private", "Existing GHCR package must be private")
    page = 1
    while True:
        versions = request(path + f"/versions?per_page=100&page={page}", token=credential)
        for version in versions:
            tags = version.get("metadata", {}).get("container", {}).get("tags", [])
            require(data["version"] not in tags, "This image version already exists; never overwrite it")
        if len(versions) < 100:
            break
        page += 1


def upload_asset(release_id: int, path: Path, credential: str) -> None:
    # GitHub's upload endpoint is fixed here; never follow untrusted upload URLs.
    import http.client
    connection = http.client.HTTPSConnection("uploads.github.com", timeout=120)
    url = f"/repos/{REPOSITORY}/releases/{release_id}/assets?name=" + urllib.parse.quote(path.name, safe="")
    connection.putrequest("POST", url)
    connection.putheader("Authorization", "Bearer " + credential)
    connection.putheader("User-Agent", "sub2api-jenkins")
    connection.putheader("Accept", "application/vnd.github+json")
    connection.putheader("Content-Type", "application/octet-stream")
    connection.putheader("Content-Length", str(path.stat().st_size))
    connection.endheaders()
    with path.open("rb") as stream:
        while block := stream.read(1024 * 1024):
            connection.send(block)
    response = connection.getresponse()
    require(response.status == 201, f"Uploading {path.name} failed with HTTP {response.status}; draft retained")
    response.read()
    connection.close()


def publish() -> None:
    ready()
    data = manifest(Path("output"))
    credential = token()
    registry_token = os.environ.get("PK_REGISTRY_TOKEN", "")
    registry_user = os.environ.get("PK_REGISTRY_USER", "")
    require(bool(registry_token and registry_user), "Dedicated GHCR publishing credential is missing")
    require(os.environ.get("DOCKER_HOST", "").startswith("tcp://"), "Use the isolated build daemon")
    package_registry_ready(data, registry_token)
    # Loading an image does not execute its entrypoint or application code.
    command("docker", "load", "--input", str(Path("output") / data["image_archive"]), capture=False)
    image_id = command("docker", "image", "inspect", data["image_tag"], "--format", "{{.Id}}")
    require(image_id == data["image_id"], "Loaded image digest does not match locally tested image")
    with tempfile.TemporaryDirectory(prefix="pk-registry-") as config:
        environment = dict(os.environ, DOCKER_CONFIG=config)
        command("docker", "login", "ghcr.io", "--username", registry_user, "--password-stdin",
                input_data=registry_token.encode(), capture=False, env=environment)
        command("docker", "push", data["image_tag"], capture=False, env=environment)
        package = request("/users/pengkang0322/packages/container/sub2api", token=registry_token)
        require(package.get("visibility") == "private", "GHCR package privacy changed; refuse release")
        tag = "v" + data["version"]
        notes = (f"PK出版。\n\nSource: `{data['commit']}`\nImage: `{data['image_tag']}`\n"
                 f"Binary SHA256: `{data['binary_sha256']}`\nJenkins package: `{data['package_id']}`\n\n"
                 "Built by Jenkins; local source and exact-artifact runtime validation passed. "
                 "The update archive and image contain the same binary. Publishing does not deploy production.")
        release = request("/repos/" + REPOSITORY + "/releases", method="POST", token=credential,
                          data={"tag_name": tag, "target_commitish": data["commit"],
                                "name": data["version"] + " PK出版", "body": notes,
                                "draft": True, "prerelease": False})
        for name in (data["binary_archive"], "checksums.txt", "manifest.json", "local-test-evidence.json"):
            upload_asset(release["id"], Path("output") / name, credential)
        released = request("/repos/" + REPOSITORY + "/releases/" + str(release["id"]),
                           method="PATCH", token=credential,
                           data={"draft": False, "prerelease": False, "make_latest": "true"})
        command("docker", "tag", data["image_tag"], IMAGE + ":latest", capture=False, env=environment)
        command("docker", "push", IMAGE + ":latest", capture=False, env=environment)
    write_json(Path("output/published.json"), {"release_url": released["html_url"], "package_id": data["package_id"],
                                               "commit": data["commit"], "production_deployed": False})
    print("Published " + released["html_url"] + "; production was not changed")


if __name__ == "__main__":
    operations = {"parameters": parameters, "prepare": prepare, "seal": seal, "restore": restore,
                  "ready": ready, "publish": publish}
    try:
        require(len(sys.argv) == 2 and sys.argv[1] in operations, "Unknown control operation")
        operations[sys.argv[1]]()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print("PK Jenkins: " + str(error), file=sys.stderr)
        sys.exit(1)
