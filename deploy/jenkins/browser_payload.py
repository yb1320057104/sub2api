"""Attach a self-contained browser to the exact executable shipped by Jenkins."""
from __future__ import annotations

import gzip
import hashlib
import io
from pathlib import Path, PurePosixPath
import posixpath
import re
import shutil
import struct
import sys
import tarfile

PROFILE = "pk-browser-in-binary-v1"
BINDINGS = ("browser_profile", "browser_payload_sha256")
MAGIC = b"PK_BROWSER_V1!!!"
FOOTER = struct.Struct(">16sQ32s")
MAX_BINARY = 500 * 1024 * 1024
MAX_PAYLOAD = 350 * 1024 * 1024
MAX_EXPANDED = 1536 * 1024 * 1024
LAUNCH = b'''#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
export LD_LIBRARY_PATH="$root/lib:$root/usr/lib:$root/usr/lib/chromium"
export FONTCONFIG_PATH="$root/etc/fonts" FONTCONFIG_FILE="$root/etc/fonts/fonts.conf"
export XDG_DATA_DIRS="$root/usr/share" XKB_CONFIG_ROOT="$root/usr/share/X11/xkb"
export SSL_CERT_FILE="$root/etc/ssl/certs/ca-certificates.crt"
if [ "${PK_AUTH_BROWSER_NO_SANDBOX:-true}" = true ]; then set -- --no-sandbox "$@"; fi
exec "$root/usr/lib/chromium/chrome" --disable-breakpad \
    --browser-subprocess-path="$root/launch" "$@"
'''
FONTS = b'''<?xml version="1.0"?>
<fontconfig><dir prefix="relative">../../usr/share/fonts</dir>
<cachedir prefix="xdg">fontconfig</cachedir></fontconfig>
'''


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def declaration(data):
    version = data.get("version", "")
    formal = re.fullmatch(r"([0-9]+)\.([0-9]+)\.([0-9]+)-pk\.([1-9][0-9]*)", version)
    # pk.14 removes the embedded runtime; pk.11/12/13 retain their original gates.
    required = formal is not None and (0, 2, 19, 11) <= tuple(map(int, formal.groups())) < (0, 2, 19, 14)
    if "browser_profile" not in data:
        require(not required and not any(key.startswith("browser_payload_") for key in data),
                "Bundled browser declaration cannot be removed")
        return False
    require(data["browser_profile"] == PROFILE, "Unknown embedded browser profile")
    return True


def inspect(stream, size):
    require(FOOTER.size < size <= MAX_BINARY, "Executable exceeds the old web updater's 500 MiB limit")
    stream.seek(0)
    require(stream.read(4) == b"\x7fELF", "Expected a Linux ELF executable")
    stream.seek(size - FOOTER.size)
    magic, length, checksum = FOOTER.unpack(stream.read(FOOTER.size))
    require(magic == MAGIC and 0 < length <= MAX_PAYLOAD and length + FOOTER.size < size,
            "Missing or invalid embedded browser payload")
    stream.seek(size - FOOTER.size - length)
    digest = hashlib.sha256()
    remaining = length
    while remaining:
        chunk = stream.read(min(1024 * 1024, remaining))
        require(chunk, "Truncated embedded browser")
        digest.update(chunk)
        remaining -= len(chunk)
    require(digest.digest() == checksum, "Embedded browser checksum differs")
    return {"browser_payload_sha256": checksum.hex(), "browser_payload_size": length}


def record(directory, data):
    if not declaration(data):
        return
    archive_path = directory / data["binary_archive"]
    require(archive_path.is_file() and not archive_path.is_symlink()
            and archive_path.stat().st_size <= MAX_BINARY, "Invalid or oversized web update archive")
    with tarfile.open(archive_path, "r:gz") as archive:
        members = archive.getmembers()
        require(len(members) == 1 and members[0].name == "sub2api" and members[0].isfile(),
                "Legacy updater requires a single regular executable")
        with archive.extractfile(members[0]) as stream:
            data.update(inspect(stream, members[0].size))


def verify(directory, data):
    if not declaration(data):
        return
    actual = dict(data)
    record(directory, actual)
    for key in ("browser_payload_sha256", "browser_payload_size"):
        require(data.get(key) == actual[key], "Browser payload binding mismatch: " + key)


def build(rootfs, output):
    require(rootfs.is_file() and not rootfs.is_symlink(), "Missing exported browser image")
    require(not output.exists(), "Browser payload already exists")
    with tarfile.open(rootfs, "r:") as source:
        members = {}
        for member in source:
            name = member.name.removeprefix("./").rstrip("/")
            if name == "." and member.isdir():
                continue
            require(name and not name.startswith("/") and ".." not in PurePosixPath(name).parts
                    and name not in members, "Unsafe exported image member")
            members[name] = member

        def resolve(name, visited=None):
            visited = set() if visited is None else visited
            require(name not in visited and len(visited) < 32, "Cyclic browser file link")
            visited.add(name)
            member = members.get(name)
            if member is None:
                # A link may traverse another directory symlink in the image.
                parts = name.split("/")
                for end in range(len(parts) - 1, 0, -1):
                    parent = members.get("/".join(parts[:end]))
                    if parent is not None and parent.issym():
                        directory = resolve("/".join(parts[:end]), set(visited))
                        require(directory.isdir(), "Linked browser parent is not a directory")
                        return resolve(directory.name.rstrip("/").removeprefix("./") + "/" + "/".join(parts[end:]), visited)
                raise RuntimeError("Missing linked browser file: " + name)
            if member.issym() or member.islnk():
                target = member.linkname
                if target.startswith("/"):
                    target = target.lstrip("/")
                elif member.issym():
                    target = posixpath.join(posixpath.dirname(name), target)
                target = posixpath.normpath(target)
                require(not target.startswith("../"), "Browser link escapes image")
                return resolve(target, visited)
            require(member.isfile() or member.isdir(), "Browser link must resolve to a file or directory")
            return member

        prefixes = ("lib/", "usr/lib/", "usr/share/fonts/", "usr/share/fontconfig/",
                    "usr/share/X11/xkb/", "usr/share/licenses/", "etc/fonts/")
        names = sorted(name for name, member in members.items()
                       if not member.isdir() and (name.startswith(prefixes)
                                                  or name == "etc/ssl/certs/ca-certificates.crt"))
        expanded_names = {}
        for name in names:
            member = resolve(name)
            if member.isdir():
                prefix = member.name.removeprefix("./").rstrip("/") + "/"
                for child, entry in members.items():
                    if child.startswith(prefix) and not entry.isdir():
                        actual = resolve(child)
                        if actual.isfile():
                            expanded_names[name + "/" + child[len(prefix):]] = actual
            else:
                expanded_names[name] = member
        names = sorted(expanded_names)
        require(all(name in names for name in ("lib/ld-musl-x86_64.so.1", "usr/lib/chromium/chrome",
                                               "etc/ssl/certs/ca-certificates.crt")), "Browser image is incomplete")
        expanded = 0
        written = {}
        with output.open("xb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", mtime=0, compresslevel=6) as compressed:
            with tarfile.open(fileobj=compressed, mode="w|") as archive:
                for name in sorted(set(names) | {"launch", "etc/fonts/fonts.conf"}):
                    if name in ("launch", "etc/fonts/fonts.conf"):
                        content = LAUNCH if name == "launch" else FONTS
                        stream = io.BytesIO(content)
                        size, mode = len(content), 0o755 if name == "launch" else 0o644
                    else:
                        member = expanded_names[name]
                        size = member.size
                        mode = 0o755 if member.mode & 0o111 else 0o644
                        identity = (member.name, mode)
                        if identity in written:
                            # Library soname aliases must not duplicate large LLVM,
                            # Mesa or ICU files in the legacy 500 MiB update binary.
                            entry = tarfile.TarInfo(name)
                            entry.type, entry.linkname, entry.mode = tarfile.LNKTYPE, written[identity], mode
                            archive.addfile(entry)
                            expanded += 1024
                            require(expanded < MAX_EXPANDED, "Expanded browser is too large")
                            continue
                        written[identity] = name
                        stream = source.extractfile(member)
                    require(stream is not None and 0 <= size <= 512 * 1024 * 1024,
                            "Invalid browser file size")
                    expanded += size + 1024
                    require(expanded < MAX_EXPANDED and len(names) < 25000, "Expanded browser is too large")
                    entry = tarfile.TarInfo(name)
                    entry.size, entry.mode = size, mode
                    with stream:
                        archive.addfile(entry, stream)
        require(output.stat().st_size <= MAX_PAYLOAD, "Compressed browser is too large")


def attach(binary, payload):
    require(binary.is_file() and not binary.is_symlink() and payload.is_file() and not payload.is_symlink(),
            "Expected regular build artifacts")
    length = payload.stat().st_size
    require(0 < length <= MAX_PAYLOAD and binary.stat().st_size + length + FOOTER.size <= MAX_BINARY,
            "Browser executable would exceed the legacy web updater size limit")
    with binary.open("rb") as stream:
        require(stream.read(4) == b"\x7fELF", "Expected compiled ELF")
        stream.seek(-FOOTER.size, 2)
        require(stream.read(16) != MAGIC, "Executable already contains a browser")
    with payload.open("rb") as stream:
        checksum = hashlib.file_digest(stream, "sha256").digest()
        stream.seek(0)
        with binary.open("ab") as target:
            shutil.copyfileobj(stream, target)
            target.write(FOOTER.pack(MAGIC, length, checksum))
    with binary.open("rb") as stream:
        inspect(stream, binary.stat().st_size)


if __name__ == "__main__":
    require(len(sys.argv) == 4 and sys.argv[1] in ("build", "attach"), "Expected build ROOTFS PAYLOAD or attach BINARY PAYLOAD")
    (build if sys.argv[1] == "build" else attach)(Path(sys.argv[2]), Path(sys.argv[3]))
