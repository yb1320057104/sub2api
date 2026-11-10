"""Bind the complete source distribution to the same private release commit."""
from __future__ import annotations

import gzip
import hashlib
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile


def require(value, message):
    if not value:
        raise RuntimeError(message)


def git_object_digest(kind, content):
    return hashlib.sha1(kind + b" " + str(len(content)).encode() + b"\0" + content).digest()


def git_tree_digest(entries):
    # Git sorts directories as if their names ended in '/', rather than using
    # an ordinary lexical sort. This also verifies restored Jenkins packages
    # without requiring another source checkout or trusting git archive attrs.
    items = sorted(entries.items(), key=lambda item: item[0] + (b"/" if isinstance(item[1], dict) else b""))
    content = bytearray()
    for name, value in items:
        mode, digest = (b"40000", git_tree_digest(value)) if isinstance(value, dict) else value
        content.extend(mode + b" " + name + b"\0" + digest)
    return git_object_digest(b"tree", bytes(content))


def required(data):
    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)-pk\.([1-9]\d*)", data.get("version", ""))
    return match is not None and tuple(map(int, match.groups())) >= (0, 2, 19, 61)


def declaration(data):
    if "source_archive" not in data:
        require(not required(data), "This combined release requires complete corresponding source")
        require(not any(key.startswith("source_archive") for key in data), "Incomplete source archive declaration")
        return False
    require(re.fullmatch(r"\d+\.\d+\.\d+-pk\.[1-9]\d*", data.get("version", "")), "Invalid source archive version")
    require(data["source_archive"] == f"sub2api_{data['version']}_source.tar.gz", "Unexpected source archive name")
    for field in ("source_archive_sha256", "source_archive_tar_sha256"):
        require(re.fullmatch(r"[0-9a-f]{64}", data.get(field, "")), "Invalid source archive digest")
    return True


def archive_bytes(data, checkout):
    require(re.fullmatch(r"[0-9a-f]{40}", data.get("commit", "")), "Invalid source commit")
    require(re.fullmatch(r"[0-9a-f]{40}", data.get("tree", "")), "Invalid source tree")
    tree = subprocess.check_output(["git", "rev-parse", data["commit"] + "^{tree}"], cwd=checkout).decode().strip()
    require(tree == data["tree"], "Corresponding source tree differs from release")
    # Keep host checkout preferences from rewriting committed blob bytes on
    # Windows. Any archive attributes that still rewrite/omit files are rejected
    # below by reconstructing the exact Git tree from the archive contents.
    return subprocess.check_output(["git", "-c", "core.autocrlf=false", "-c", "core.eol=lf",
                                    "archive", "--format=tar", f"--prefix=sub2api_{data['version']}/", data["commit"]], cwd=checkout)


def create(directory, data, checkout):
    if not required(data):
        return
    raw = archive_bytes(data, checkout)
    compressed = gzip.compress(raw, mtime=0)
    name = f"sub2api_{data['version']}_source.tar.gz"
    path = directory / name
    require(not path.exists(), "Never overwrite a corresponding source archive")
    path.write_bytes(compressed)
    data.update(source_archive=name, source_archive_sha256=hashlib.sha256(compressed).hexdigest(),
                source_archive_tar_sha256=hashlib.sha256(raw).hexdigest())
    verify(directory, data)


def verify(directory, data, *, checkout=None):
    if not declaration(data):
        return
    path = directory / data["source_archive"]
    require(path.is_file() and not path.is_symlink(), "Missing regular corresponding source archive")
    with path.open("rb") as stream:
        require(hashlib.file_digest(stream, "sha256").hexdigest() == data["source_archive_sha256"], "Source archive checksum mismatch")
    with gzip.open(path, "rb") as stream:
        require(hashlib.file_digest(stream, "sha256").hexdigest() == data["source_archive_tar_sha256"], "Source tar checksum mismatch")
    prefix = f"sub2api_{data['version']}/"
    with tarfile.open(path, "r:gz") as archive:
        require(archive.pax_headers.get("comment") == data["commit"], "Source archive commit mismatch")
        names = set()
        tree = {}
        for member in archive:
            require((member.name == prefix.rstrip("/") or member.name.startswith(prefix)) and ".." not in PurePosixPath(member.name).parts
                    and (member.isfile() or member.isdir()), "Unsafe source archive member")
            require(member.name not in names, "Duplicate source archive member")
            names.add(member.name)
            if member.name == prefix.rstrip("/"):
                require(member.isdir(), "Source archive root must be a directory")
                continue
            parts = PurePosixPath(member.name.removeprefix(prefix)).parts
            parent = tree
            for part in parts[:-1]:
                parent = parent.setdefault(part.encode("utf-8", "surrogateescape"), {})
                require(isinstance(parent, dict), "Source archive path conflicts with a file")
            name = parts[-1].encode("utf-8", "surrogateescape")
            if member.isdir():
                require(isinstance(parent.setdefault(name, {}), dict), "Source archive directory conflicts with a file")
            else:
                require(name not in parent, "Source archive file conflicts with a directory")
                stream = archive.extractfile(member)
                require(stream is not None, "Missing source archive file content")
                digest = hashlib.sha1(b"blob " + str(member.size).encode() + b"\0")
                with stream:
                    for block in iter(lambda: stream.read(1024 * 1024), b""):
                        digest.update(block)
                parent[name] = (b"100755" if member.mode & 0o111 else b"100644", digest.digest())
        for required_path in ("THIRD_PARTY_NOTICES.md", "LICENSES/BEpusdt-GPL-3.0.txt", "README.md",
                              "backend/go.mod", "frontend/pnpm-lock.yaml", "backend/third_party/bepusdt/README.sub2api.md"):
            require(prefix + required_path in names, "Incomplete corresponding source: " + required_path)
        require(git_tree_digest(tree).hex() == data.get("tree"),
                "Source archive tree differs from the complete release Git tree")
    if checkout is not None:
        require(hashlib.sha256(archive_bytes(data, checkout)).hexdigest() == data["source_archive_tar_sha256"],
                "Source archive does not equal the complete release Git tree")


def assets(data):
    return [data["source_archive"]] if declaration(data) else []
