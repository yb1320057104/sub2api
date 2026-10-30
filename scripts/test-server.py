#!/usr/bin/env python3
"""Test the exact staged tree in a disposable Docker-in-Docker server environment.

Uses an explicitly supplied, Git-ignored local SSH credential file. Credentials
remain on this computer; only the source bundle, staged Git objects and test logs
travel to the selected server. This never builds an application release.
"""
from __future__ import annotations

import argparse
import codecs
import datetime as dt
import importlib.util
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import time
import uuid

import paramiko

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("source_validation", ROOT / "scripts/test-local.py")
local = importlib.util.module_from_spec(spec)
spec.loader.exec_module(local)
REMOTE_ROOT = "/srv/pk-source-validation/runs"
IMAGE = "pk-source-tests:go1.27.3-20261021"


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def execute(client, args, *, stdin=None, log=None, timeout=14400, stream=True):
    channel = client.get_transport().open_session(timeout=30)
    channel.exec_command(shlex.join(args))
    if stdin is not None:
        channel.sendall(stdin.encode())
    channel.shutdown_write()
    started = time.monotonic()
    decoders = [codecs.getincrementaldecoder("utf-8")(errors="replace") for _ in range(2)]
    def emit(text):
        if stream:
            print(text, end="", flush=True)
        if log:
            log.write(text)
            log.flush()
    try:
        while not channel.exit_status_ready() or channel.recv_ready() or channel.recv_stderr_ready():
            for decoder, (ready, recv) in zip(decoders, ((channel.recv_ready, channel.recv), (channel.recv_stderr_ready, channel.recv_stderr))):
                if ready():
                    emit(decoder.decode(recv(65536)))
            if time.monotonic() - started > timeout:
                raise RuntimeError("Server validation command timed out")
            time.sleep(0.1)
        for decoder in decoders:
            emit(decoder.decode(b"", final=True))
        return channel.recv_exit_status()
    finally:
        channel.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--credentials", type=Path, default=ROOT / ".codex/servers/sub2api-test.json")
    parser.add_argument("--output-dir", type=Path)
    args = parser.parse_args()
    credentials = args.credentials.resolve(strict=True)
    require(ROOT in credentials.parents, "Credential file must be inside this checkout and Git-ignored")
    relative = credentials.relative_to(ROOT).as_posix()
    ignored = subprocess.run(["git", "check-ignore", "--quiet", "--", relative], cwd=ROOT)
    require(ignored.returncode == 0 and not local.git("ls-files", "--", relative),
            "Credential file must be ignored and must never be tracked")
    login = json.loads(credentials.read_text(encoding="utf-8-sig"))
    require(isinstance(login, dict), "Credential file must contain a JSON object")
    require(all(isinstance(login.get(field), str) and login[field].strip()
                for field in ("host", "username", "password")),
            "Credential file must provide host, username and password")
    # The operator selects the authorized test server in this ignored local
    # file. No deployment address or SSH username is embedded in the source.
    snapshot = local.workspace_snapshot()
    run_id = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%S-") + uuid.uuid4().hex[:12]
    output = (args.output_dir or Path(tempfile.gettempdir()) / "sub2api-server-validation" / run_id).resolve()
    require(output != ROOT and ROOT not in output.parents, "Evidence output must be outside the checkout")
    output.mkdir(parents=True, exist_ok=False)
    print("Testing staged tree " + snapshot["tree"] + "; evidence directory: " + str(output), flush=True)
    remote = REMOTE_ROOT + "/" + run_id
    container = "pk-source-tests-" + run_id
    client = paramiko.SSHClient()
    client.load_host_keys(str(Path.home() / ".ssh/known_hosts"))
    client.set_missing_host_key_policy(paramiko.RejectPolicy())
    created = connected = False
    test_code = 1
    validation_complete = False
    cleanup_errors = []
    final_report = output / "local-validation.json"
    report = None
    try:
        client.connect(login["host"], username=login["username"], password=login["password"],
                       timeout=20, auth_timeout=20, look_for_keys=False, allow_agent=False)
        connected = True
        client.get_transport().set_keepalive(20)
        del login
        with (output / "server.log").open("w", encoding="utf-8") as log, tempfile.TemporaryDirectory(prefix="pk-source-upload-") as temporary:
            bundle, staged = Path(temporary) / "source.bundle", Path(temporary) / "staged.pack"
            subprocess.run(["git", "bundle", "create", str(bundle), "HEAD"], cwd=ROOT, check=True)
            # Transfer objects directly: re-adding a tar checkout can normalize
            # historical line endings and change its tree even without edits.
            with staged.open("wb") as stream:
                subprocess.run(["git", "pack-objects", "--stdout", "--revs"], cwd=ROOT,
                               input=(snapshot["tree"] + "\n^" + snapshot["head"] + "\n").encode(),
                               stdout=stream, check=True)
            require(local.workspace_snapshot() == snapshot, "Local source changed while preparing upload")
            require(execute(client, ["mkdir", "-p", "-m", "700", remote], log=log) == 0, "Cannot create server test directory")
            created = True
            with client.open_sftp() as sftp:
                sftp.put(str(bundle), remote + "/source.bundle")
                sftp.put(str(staged), remote + "/staged.pack")
            require(execute(client, ["docker", "run", "--detach", "--pull=never", "--name", container,
                "--label", "pk.source.validation=" + run_id, "--privileged", "--cpus=8", "--memory=16g", "--memory-swap=16g",
                "--pids-limit=4096", "--env", "DOCKER_TLS_CERTDIR=", "--env", "DOCKER_HOST=unix:///var/run/docker.sock",
                "--mount", "type=bind,src=" + remote + ",dst=/validation", IMAGE,
                "dockerd", "--host=unix:///var/run/docker.sock"], log=log) == 0, "Cannot start isolated source-test daemon")
            # No host socket, production volume, environment file or SSH credential
            # is mounted. Testcontainers connects to this container's own daemon.
            prepare = '''import pathlib,subprocess,time
root=pathlib.Path('/validation'); repo=root/'source'
for attempt in range(60):
    if subprocess.run(['docker','info'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0: break
    time.sleep(1)
else: raise SystemExit('Isolated Docker daemon did not become ready')
subprocess.run(['git','clone','--no-checkout',str(root/'source.bundle'),str(repo)],check=True)
with (root/'staged.pack').open('rb') as stream:
    subprocess.run(['git','unpack-objects','-q'],cwd=repo,stdin=stream,check=True)
subprocess.run(['git','read-tree','--reset','-u',TREE],cwd=repo,check=True)
tree=subprocess.check_output(['git','write-tree'],cwd=repo,text=True).strip()
head=subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()
if tree != TREE or head != HEAD: raise SystemExit('Uploaded source tree/base differs: tree='+tree+' head='+head)
'''
            prepare = "TREE=" + repr(snapshot["tree"]) + "\nHEAD=" + repr(snapshot["head"]) + "\n" + prepare
            require(execute(client, ["docker", "exec", "--interactive", container, "python3", "-"],
                            stdin=prepare, log=log, timeout=120) == 0, "Server source preparation failed")
            print("Running nine source suites; live log: " + str(output / "server.log"), flush=True)
            test_code = execute(client, ["docker", "exec", container, "python3", "/validation/source/scripts/test-local.py",
                "--execution-environment", "server-isolated", "--output-dir", "/validation/evidence"], log=log, stream=False)
            with client.open_sftp() as sftp:
                expected_logs = {name + ".log" for name in ("preflight", *local.SOURCE_SUITES)}
                for name in sftp.listdir(remote + "/evidence"):
                    require(name == "local-validation.json" or name in expected_logs,
                            "Unexpected server evidence filename")
                    if name == "local-validation.json":
                        with sftp.open(remote + "/evidence/" + name, "rb") as stream:
                            report = json.load(stream)
                    else:
                        sftp.get(remote + "/evidence/" + name, str(output / name))
            require(local.workspace_snapshot() == snapshot, "Local source changed while server tests ran; rerun validation")
            require(isinstance(report, dict), "Missing source report from server")
            require(report.get("tree") == snapshot["tree"] and report.get("base_commit") == snapshot["head"]
                    and report.get("execution_environment") == "server-isolated", "Returned report does not match uploaded source")
            if test_code == 0:
                local.validate_source_report(report, final_report)
                validation_complete = True
    finally:
        if connected and created:
            # The random container and its anonymous daemon volume are owned by
            # this invocation. Removing them also removes all testcontainers.
            with (output / "server.log").open("a", encoding="utf-8") as log:
                cleanup = "import pathlib,shutil,sys; p=pathlib.Path(sys.argv[1]); root=pathlib.Path(sys.argv[2]); assert p.parent==root and p.name==sys.argv[3] and not p.is_symlink(); shutil.rmtree(p)"
                for command in (["docker", "rm", "--force", "--volumes", container],
                                ["python3", "-c", cleanup, remote, REMOTE_ROOT, run_id]):
                    try:
                        if execute(client, command, log=log, timeout=120) != 0:
                            cleanup_errors.append("Cleanup command failed: " + command[0])
                    except Exception as error:
                        cleanup_errors.append("Cleanup transport failed: " + str(error))
                for error in cleanup_errors:
                    log.write(error + "\n")
        try:
            client.close()
        except Exception as error:
            cleanup_errors.append("Closing test connection failed: " + str(error))
        finally:
            # A passed report only becomes available after successful transport,
            # local tree verification and cleanup. Failure cannot leave a passed
            # report that a later bind command would mistakenly accept.
            if isinstance(report, dict):
                if not validation_complete or cleanup_errors:
                    report.update(status="failed", source_eligible=False,
                                  error="Server validation or cleanup incomplete; review server.log")
                local.write_report(final_report, report)
    require(not cleanup_errors, "Server cleanup failed; inspect " + str(output / "server.log"))
    print("Server source evidence: " + str(final_report))
    require(test_code == 0, "Source tests failed; no commit or publish evidence is eligible")
    print("Commit the tested staged tree, then bind/publish its report using scripts/test-local.py.")
    return 0


if __name__ == "__main__":
    # Windows terminals may default to GBK; remote Go JSON/logs are UTF-8.
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8", errors="replace")
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError, paramiko.SSHException) as error:
        print("Server validation: " + str(error), file=sys.stderr)
        sys.exit(1)
