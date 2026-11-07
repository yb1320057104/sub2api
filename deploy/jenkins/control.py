#!/usr/bin/env python3
"""Trusted Jenkins packaging metadata, validation gates and private publisher.

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

sys.path.insert(0, str(Path(__file__).resolve().parent))
import frontend_dependencies as dependencies
import deployment_bundle as deployment
import browser_payload as browser
import repository_policy as provenance
import startup_guard_publication as startup_guard

REPOSITORY = provenance.REPOSITORY
IMAGE = provenance.image(REPOSITORY)
API = "https://api.github.com"
TAG_PATTERN = r"v[0-9]+\.[0-9]+\.[0-9]+-pk\.[1-9][0-9]*"
PACKAGE_PATTERN = r"[1-9][0-9]*-[0-9a-f]{12}"
REQUIRED_SUITES = {
    "backend-unit", "backend-integration", "backend-race", "backend-lint",
    "frontend", "shell", "binance-proxy", "release-helpers", "security",
    "artifact-runtime",
}
SOURCE_SUITES = REQUIRED_SUITES - {"artifact-runtime"}
NOTES_REF = "refs/notes/pk-source-validation"
VALIDATION_PROFILE = "pk-artifact-only-v1"
LEGACY_VALIDATION_PROFILE = "pk-source-artifact-v2"
JENKINS_VALIDATION_PROFILES = {VALIDATION_PROFILE, LEGACY_VALIDATION_PROFILE}
SOURCE_EVIDENCE_FIELDS = (
    "source_validation_sha256", "source_notes_ref", "source_notes_commit", "source_note_blob",
)
RUNTIME_BINDINGS = (
    "repository", "commit", "tree", "package_id", "binary_sha256", "binary_archive_sha256",
    "image_archive_sha256", "image_id", "version",
)
RUNTIME_CHECKS = {
    "artifact-integrity", "image-provenance", "image-loaded", "binary-identity",
    "runtime-nonroot", "same-seed-preservation", "interrupted-update-recovery",
    "fixture-preparation", "changed-seed-replacement", "original-image-rollback", "cleanup",
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
    action = os.environ.get("ACTION", "release")
    require(action in {"release", "package", "publish"}, "Unknown Jenkins action")
    reinstall = os.environ.get("REINSTALL_NODE_MODULES", "否")
    require(reinstall in {"否", "是"}, "REINSTALL_NODE_MODULES must be 否 or 是")
    require(action != "publish" or reinstall == "否", "publish cannot reinstall or rebuild sealed dependencies")
    require(os.environ.get("PK_REPOSITORY") == REPOSITORY, "Only the protected private repository is supported")
    require(os.environ.get("PK_IMAGE") == IMAGE, "Only the protected private image is supported")
    if action in {"release", "package"}:
        ref = os.environ.get("SOURCE_REF", "")
        require(bool(re.fullmatch(r"[0-9a-f]{40}|refs/(heads|tags)/[A-Za-z0-9._/-]+", ref)),
                "SOURCE_REF must be a full SHA or refs/heads/... or refs/tags/...")
        require(".." not in ref and "//" not in ref and not ref.endswith(("/", ".lock")), "Invalid Git ref")
        if action == "release":
            require(not os.environ.get("COMMIT_SHA", "").strip(), "release does not accept a COMMIT_SHA override")
            require(bool(re.fullmatch("refs/tags/" + TAG_PATTERN, ref)),
                    "release requires a formal vX.Y.Z-pk.N tag; branches and candidate packages cannot publish")
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
        "validation": "Pending legacy local source and artifact-runtime validation; this package is not published",
    }
    # New packages use an external browser. Historical sealed profiles remain
    # mandatory for their original versions and are still verified on restore.
    deployment.declaration(manifest)
    browser.declaration(manifest)
    if os.environ.get("ACTION", "release") == "release":
        manifest.update(validation_profile=VALIDATION_PROFILE,
                        validation="Pending Jenkins exact-artifact runtime validation; source validation is not required")
    if os.environ.get("REINSTALL_NODE_MODULES", "否") == "是":
        manifest.update(schema_version=2, frontend_dependencies={
            "schema_version": 1, "mode": "reinstall",
            "source_lock_sha256": sha256(Path("source/frontend/pnpm-lock.yaml")),
            "package_json_sha256": sha256(Path("source/frontend/package.json")),
        })
    write_json(output / "manifest.json", manifest)
    print(f"Packaging {commit} as {version}; no GitHub Actions build or production deployment")


def manifest(directory: Path, *, allow_legacy: bool = False) -> dict:
    path = directory / "manifest.json"
    require(path.is_file() and not path.is_symlink(), "Missing regular manifest")
    data = json.loads(path.read_text(encoding="utf-8"))
    repository = provenance.package_repository(data, allow_legacy=allow_legacy)
    require(data.get("schema_version") in (1, 2), "Unexpected package provenance")
    dependencies.declaration(directory, data)
    deployment.declaration(data)
    browser.declaration(data)
    require(bool(re.fullmatch(PACKAGE_PATTERN, data.get("package_id", ""))), "Invalid manifest package ID")
    require(bool(re.fullmatch(r"[0-9a-f]{40}", data.get("commit", ""))), "Invalid manifest commit")
    require(bool(re.fullmatch(r"[0-9a-f]{40}", data.get("tree", ""))), "Invalid manifest tree")
    require(data.get("platform") == "linux/amd64", "Unsupported platform")
    version = data.get("version", "")
    require(bool(re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-pk\.[1-9][0-9]*)?(?:-pk\.candidate\.[1-9][0-9]*)?", version)), "Invalid package version")
    require(data.get("image_tag") == provenance.image(repository) + ":" + version, "Unexpected package image")
    require(data.get("binary_archive") == f"sub2api_{version}_linux_amd64.tar.gz", "Unexpected binary archive name")
    require(data.get("image_archive") == f"sub2api_{version}_linux_amd64.docker.tar.gz", "Unexpected image archive name")
    profile = data.get("validation_profile")
    require(profile in (None, *JENKINS_VALIDATION_PROFILES), "Unsupported package validation profile")
    if profile != LEGACY_VALIDATION_PROFILE:
        require(not any(field in data for field in SOURCE_EVIDENCE_FIELDS)
                and not (directory / "source-validation.json").exists()
                and not (directory / "source-validation.json").is_symlink(),
                "Source evidence requires the legacy validation profile; it may not be discarded")
    return data


def verify_artifacts(directory: Path, data: dict) -> None:
    dependencies.verify(directory, data)
    deployment.verify(directory, data)
    browser.verify(directory, data)
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
    expected = deployment.checksums(data)
    require((directory / "checksums.txt").read_text() == expected, "Unexpected release checksum file")


def package_path(package_id: str) -> Path:
    require(bool(re.fullmatch(PACKAGE_PATTERN, package_id)), "Invalid package path")
    root = Path(os.environ["PK_PACKAGE_ROOT"]).resolve(strict=True)
    require(root.is_dir(), "Package root missing")
    path = root / package_id
    require(not path.is_symlink(), "Package path must not be a symlink")
    return path


def inventory() -> None:
    """Record build outputs before running the exact-artifact checks."""
    output = Path("output")
    data = manifest(output)
    for key in ("binary_archive", "image_archive"):
        data[key + "_sha256"] = sha256(output / data[key])
    data["binary_sha256"] = (output / "binary-sha256.txt").read_text().strip()
    data["image_id"] = (output / "image-id.txt").read_text().strip()
    browser.record(output, data)
    deployment.create(output, data, Path(__file__).resolve().parents[2])
    (output / "checksums.txt").write_text(deployment.checksums(data))
    write_json(output / "manifest.json", data)
    verify_artifacts(output, data)


def verify_source_checkout(data: dict) -> None:
    require(git("rev-parse", "HEAD") == data["commit"] and git("rev-parse", "HEAD^{tree}") == data["tree"],
            "Source commit or tree changed during packaging")
    require(not git("status", "--porcelain"), "Packaging modified the source checkout")


def seal() -> None:
    output = Path("output")
    data = manifest(output)
    verify_artifacts(output, data)
    verify_source_checkout(data)
    if data.get("validation_profile") in JENKINS_VALIDATION_PROFILES:
        data["artifact_validation_sha256"] = sha256(output / "artifact-validation.json")
        package_evidence(output, data)
        data["validation"] = ("Pre-commit source and Jenkins exact-artifact validation passed"
                              if data["validation_profile"] == LEGACY_VALIDATION_PROFILE else
                              "Jenkins exact-artifact runtime validation passed; source validation is not required")
        write_json(output / "manifest.json", data)
    destination = package_path(data["package_id"])
    require(not destination.exists(), "A package ID may never be overwritten")
    # A hidden sibling is renamed only after all artifact copies are complete.
    with tempfile.TemporaryDirectory(prefix=".sealing-", dir=destination.parent) as temporary:
        staged = Path(temporary) / data["package_id"]
        shutil.copytree(output, staged)
        for file in staged.iterdir():
            file.chmod(0o440)
        # Moving a directory between parents updates its '..' entry. Keep it
        # owner-writable until the rename completes, then seal the final path.
        staged.chmod(0o750)
        staged.rename(destination)
        destination.chmod(0o550)
    print("PACKAGE_ID=" + data["package_id"])
    if data.get("validation_profile") in JENKINS_VALIDATION_PROFILES:
        print("Exact-artifact evidence sealed; the same package is ready for the private release gate.")
    else:
        print("Manual package: complete legacy local source/runtime validation before publish.")


def restore() -> None:
    parameters()
    directory = package_path(os.environ["PACKAGE_ID"])
    data = manifest(directory, allow_legacy=True)
    require(data["package_id"] == os.environ["PACKAGE_ID"], "Package ID does not match directory")
    verify_artifacts(directory, data)
    if data["repository"] != REPOSITORY:
        # Old packages retain their original profile and all original evidence.
        # Restoring them does not authorize publishing them into the new repo.
        if data.get("validation_profile") in JENKINS_VALIDATION_PROFILES:
            package_evidence(directory, data)
        else:
            evidence(data, directory)
    shutil.copytree(directory, "output")
    Path("output").chmod(0o750)
    for file in Path("output").iterdir():
        file.chmod(0o640)


def source_evidence(proof: dict, data: dict) -> None:
    require(isinstance(proof, dict) and proof.get("schema_version") == 2
            and proof.get("tests_profile") == "pk-source-v2", "Unsupported source validation profile")
    for key in ("repository", "commit", "tree"):
        require(proof.get(key) == data[key], f"Source evidence does not match package {key}")
    require(proof.get("status") == "passed" and proof.get("source_eligible") is True,
            "Pre-commit source validation did not pass")
    require(proof.get("execution_environment") in {"local", "server-isolated"}, "Unknown source test environment")
    require(isinstance(proof.get("bound_at"), str) and bool(proof["bound_at"]), "Source report is not bound to a commit")
    require(bool(re.fullmatch(r"[0-9a-f]{40}", str(proof.get("base_commit", "")))), "Invalid tested base commit")
    before, after = proof.get("workspace_before"), proof.get("workspace_after")
    require(isinstance(before, dict) and before == after and before.get("tree") == data["tree"]
            and before.get("head") == proof["base_commit"] and before.get("worktree_matches_index") is True,
            "Source evidence does not prove a stable tested tree")
    suites = proof.get("suites")
    require(isinstance(suites, list) and len(suites) == len(SOURCE_SUITES)
            and all(isinstance(suite, dict) for suite in suites), "Source evidence must contain nine suites")
    require({suite.get("name") for suite in suites} == SOURCE_SUITES, "Source suites are missing or duplicated")
    preflight = proof.get("preflight")
    require(isinstance(preflight, dict) and preflight.get("name") == "preflight", "Missing source preflight")
    for suite in [preflight, *suites]:
        require(suite.get("status") == "passed" and type(suite.get("exit_code")) is int
                and suite["exit_code"] == 0 and isinstance(suite.get("commands"), list) and bool(suite["commands"]),
                "Source suite failed, skipped, or has no commands: " + str(suite.get("name")))
        require(bool(re.fullmatch(r"[0-9a-f]{64}", str(suite.get("log_sha256", "")))), "Missing source log digest")
        require(not any(str(name).startswith("TestPK") for name in suite.get("skipped_tests", [])),
                "Protected source tests were skipped")


def package_evidence(directory: Path, data: dict) -> None:
    """Validate runtime evidence, retaining source checks for legacy sealed packages."""
    profile = data.get("validation_profile")
    require(profile in JENKINS_VALIDATION_PROFILES, "Unsupported Jenkins validation profile")
    bindings = RUNTIME_BINDINGS
    required_checks = RUNTIME_CHECKS
    if deployment.declaration(data):
        deployment.verify(directory, data)
        bindings += deployment.BINDINGS
        required_checks = RUNTIME_CHECKS | {"browser-runtime"}
    if browser.declaration(data):
        browser.verify(directory, data)
        bindings += browser.BINDINGS
        required_checks = required_checks | {"browser-web-update"}
    if data.get("schema_version") == 2:
        dependencies.verify(directory, data)
        bindings += dependencies.BINDINGS
    if profile == LEGACY_VALIDATION_PROFILE:
        require(data.get("source_notes_ref") == NOTES_REF, "Unexpected source evidence notes ref")
        for field in ("source_notes_commit", "source_note_blob"):
            require(bool(re.fullmatch(r"[0-9a-f]{40}", str(data.get(field, "")))), "Missing source note provenance")
        require(sha256(directory / "source-validation.json") == data.get("source_validation_sha256"),
                "Sealed validation report changed: source-validation.json")
        source_evidence(json.loads((directory / "source-validation.json").read_bytes()), data)
        bindings += ("source_validation_sha256",)
    else:
        require(not any(field in data for field in SOURCE_EVIDENCE_FIELDS)
                and not (directory / "source-validation.json").exists()
                and not (directory / "source-validation.json").is_symlink(),
                "Source evidence requires the legacy validation profile; it may not be discarded")
    require(sha256(directory / "artifact-validation.json") == data.get("artifact_validation_sha256"),
            "Sealed validation report changed: artifact-validation.json")
    proof = json.loads((directory / "artifact-validation.json").read_bytes())
    require(isinstance(proof, dict) and proof.get("schema_version") == 2
            and proof.get("tests_profile") == "pk-artifact-v2" and proof.get("status") == "passed",
            "Jenkins artifact validation did not pass")
    if not deployment.declaration(data):
        require(not any(key in proof or key in (proof.get("artifact") or {}) for key in deployment.BINDINGS),
                "Deployment evidence cannot be stripped from a sealed package")
    if not browser.declaration(data):
        require(not any(key in proof or key in (proof.get("artifact") or {}) for key in browser.BINDINGS),
                "Embedded browser evidence cannot be stripped from a sealed package")
    if data.get("schema_version", 1) == 1:
        require(not any(key in proof or key in (proof.get("artifact") or {}) for key in dependencies.BINDINGS),
                "Runtime dependency evidence cannot be downgraded to schema 1")
    if profile == VALIDATION_PROFILE:
        require(proof.get("validation_profile") == profile, "Runtime validation profile does not match package")
        require("source_validation_sha256" not in proof, "Runtime source evidence requires the legacy validation profile")
    elif "validation_profile" in proof:
        require(proof["validation_profile"] == profile, "Runtime validation profile does not match package")
    for key in bindings:
        require(isinstance(data.get(key), str) and bool(data[key]), f"Missing package runtime binding: {key}")
        require(proof.get(key) == data[key], f"Jenkins runtime evidence does not match package {key}")
    checks = proof.get("checks")
    require(isinstance(checks, list) and len(checks) == len(required_checks)
            and all(isinstance(check, dict) for check in checks), "Missing Jenkins runtime checks")
    require({check.get("name") for check in checks} == required_checks, "Jenkins runtime checks are missing or duplicated")
    require(all(check.get("status") == "passed" and type(check.get("exit_code")) is int
                and check["exit_code"] == 0 for check in checks), "Jenkins runtime check failed or was skipped")


def evidence(data: dict, directory: Path | None = None) -> dict:
    directory = directory or Path("output")
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
    if browser.declaration(data):
        browser.verify(directory, data)
        for key in browser.BINDINGS:
            require(proof.get("artifact", {}).get(key) == data[key], "Manual browser binding mismatch: " + key)
        require(proof.get("artifact", {}).get("browser_web_update_ready") is True,
                "Embedded browser was not verified in a browser-free image")
    else:
        require(not any(key in proof.get("artifact", {}) for key in browser.BINDINGS),
                "Manual embedded browser evidence cannot be stripped")
    if deployment.declaration(data):
        deployment.verify(directory, data)
        for key in deployment.BINDINGS:
            require(proof.get("artifact", {}).get(key) == data[key], "Manual deployment binding mismatch: " + key)
        require(proof.get("artifact", {}).get("browser_runtime_ready") is True, "Bundled browser was not verified")
    else:
        require(not any(key in proof.get("artifact", {}) for key in deployment.BINDINGS),
                "Manual deployment evidence cannot be stripped")
    if data.get("schema_version") == 2:
        dependencies.verify(directory, data)
        for key in dependencies.BINDINGS:
            require(proof.get("artifact", {}).get(key) == data[key], "Manual runtime dependency binding mismatch: " + key)
    else:
        require(not any(key in proof.get("artifact", {}) for key in dependencies.BINDINGS),
                "Manual dependency evidence cannot be downgraded to schema 1")
    return proof


def release_ready(data: dict, *, credential: str | None = None) -> None:
    provenance.package_repository(data)
    require(data.get("build_type") == "release", "Candidate packages cannot become formal releases")
    tag = "v" + data["version"]
    require(bool(re.fullmatch(TAG_PATTERN, tag)) and data.get("source_ref") == "refs/tags/" + tag, "Package lacks a formal reviewed tag")
    credential = token() if credential is None else credential
    require(bool(credential), "The private-release credential is not configured")
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
    if data.get("validation_profile") in JENKINS_VALIDATION_PROFILES:
        package_evidence(Path("output"), data)
    else:
        proof = evidence(data)
        write_json(Path("output/local-test-evidence.json"), proof)
    release_ready(data)
    startup_guard.authorize(Path("output"), data, "plan")
    print("Required package evidence passed; private tag and main ancestry verified.")


def package_registry_ready(data: dict, credential: str) -> None:
    path = "/users/yb1320057104/packages/container/sub2api"
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
    startup_guard.authorize(Path("output"), data, "prepare")
    # Loading an image does not execute its entrypoint or application code.
    command("docker", "load", "--input", str(Path("output") / data["image_archive"]), capture=False)
    image_id = command("docker", "image", "inspect", data["image_tag"], "--format", "{{.Id}}")
    require(image_id == data["image_id"], "Loaded image digest does not match tested image")
    with tempfile.TemporaryDirectory(prefix="pk-registry-") as config:
        environment = dict(os.environ, DOCKER_CONFIG=config)
        command("docker", "login", "ghcr.io", "--username", registry_user, "--password-stdin",
                input_data=registry_token.encode(), capture=False, env=environment)
        command("docker", "push", data["image_tag"], capture=False, env=environment)
        package = request("/users/yb1320057104/packages/container/sub2api", token=registry_token)
        require(package.get("visibility") == "private", "GHCR package privacy changed; refuse release")
        tag = "v" + data["version"]
        if data.get("validation_profile") == LEGACY_VALIDATION_PROFILE:
            validation = "Pre-commit source tests and Jenkins exact-artifact runtime validation passed. "
            reports = ["source-validation.json", "artifact-validation.json"]
        elif data.get("validation_profile") == VALIDATION_PROFILE:
            validation = "Jenkins exact-artifact runtime validation passed; source validation is not required. "
            reports = ["artifact-validation.json"]
        else:
            validation = "Local source and exact-artifact runtime validation passed. "
            reports = ["local-test-evidence.json"]
        if data.get("schema_version") == 2:
            reports += [dependencies.REPORT, *dependencies.FILES.values()]
            validation += ("Frontend dependencies were re-resolved and audited; the effective lock and audit are sealed. "
                           + "Effective lock SHA256: " + data["frontend_effective_lock_sha256"] + ". ")
        notes = (f"PK出版。\n\nSource: `{data['commit']}`\nImage: `{data['image_tag']}`\n"
                 f"Binary SHA256: `{data['binary_sha256']}`\nJenkins package: `{data['package_id']}`\n\n"
                 "Built by Jenkins; " + validation +
                 "The update archive and image contain the same binary. Publishing does not deploy production.")
        if deployment.declaration(data):
            notes += ("\n\nFull deployment bundle: " + data["deployment_archive"] +
                      ". Includes the exact validated image with Chromium and browser libraries, "
                      "an installer and Compose configuration. No separate browser installation is needed. "
                      "The original single-executable archive layout remains compatible with older web updaters.")
        if browser.declaration(data):
            notes += ("\n\nThe executable also carries the browser, loader, fonts and libraries. "
                      "After a web update and the normal restart, it installs its verified runtime in the "
                      "existing data directory. Existing official Linux amd64 containers need no image "
                      "replacement, browser download, root installation or path configuration.")
        elif deployment.declaration(data):
            notes += " Older image environments must update their application container once to acquire the browser."
        elif tuple(map(int, data["version"].replace("-pk.", ".").split("."))) >= (0, 2, 19, 14):
            notes += ("\n\nChromium is external to the application image and update archive. "
                      "Install it in the application runtime using deploy/install-browser.sh; "
                      "reapply after recreating the container. Web updates do not install a browser.")
        release = request("/repos/" + REPOSITORY + "/releases", method="POST", token=credential,
                          data={"tag_name": tag, "target_commitish": data["commit"],
                                "name": data["version"] + " PK出版", "body": notes,
                                "draft": True, "prerelease": False})
        deployment_assets = [data["deployment_archive"]] if deployment.declaration(data) else []
        for name in [data["binary_archive"], *deployment_assets, "checksums.txt", "manifest.json", *reports]:
            upload_asset(release["id"], Path("output") / name, credential)
        startup_guard.authorize(Path("output"), data, "check")
        released = request("/repos/" + REPOSITORY + "/releases/" + str(release["id"]),
                           method="PATCH", token=credential,
                           data={"draft": False, "prerelease": False, "make_latest": "true"})
        command("docker", "tag", data["image_tag"], IMAGE + ":latest", capture=False, env=environment)
        command("docker", "push", IMAGE + ":latest", capture=False, env=environment)
    write_json(Path("output/published.json"), {"release_url": released["html_url"], "package_id": data["package_id"],
                                               "commit": data["commit"], "production_deployed": False})
    print("Published " + released["html_url"] + "; application binary and service were not changed; startup authorization verified")


if __name__ == "__main__":
    operations = {"parameters": parameters, "prepare": prepare, "inventory": inventory, "seal": seal, "restore": restore,
                  "ready": ready, "publish": publish}
    try:
        require(len(sys.argv) == 2 and sys.argv[1] in operations, "Unknown control operation")
        operations[sys.argv[1]]()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print("PK Jenkins: " + str(error), file=sys.stderr)
        sys.exit(1)
