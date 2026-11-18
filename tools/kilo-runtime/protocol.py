"""Kilo stages backed by pinned toSub2 helpers and the existing cfsolve library.

Only private pipes carry credentials/cookies/proxies. No login secret is put in
argv, environment, files or application logs. Go remains the device-token reader.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import threading
import time
from typing import Any

ROOT = Path(__file__).resolve().parent
MAX_PIPE = 2 * 1024 * 1024
ASSET_HASHES = {
    "sentinel_bridge.js": "4761034c18b39c16a210ede115f5361cfa72315efba3586dbf1a81401c3e41e0",
    "sentinel_bootstrap.js": "59cfca34c5f62ea2b1ddff8c3708c10b79c7d23716dd8113a44f5eb1a962f21a",
    "sentinel_sdk.js": "f21fa2a6859b320ef3157e3cf17d9a8da7fbf824aba55502b5fc3f421c03ef11",
}
PROFILE_HASH = "0238708edcd6bea3c1d380f51ada8e652e9bf6d63083349cc4fffe80a144da56"
PROFILE_LF_HASH = "2ee00ccbd66b7fc64a87927f7a08b8f040d4ff67c8fe2f93696590b4613f13fa"
PROFILE_ASSETS = {
    "safari-26-ios/answers.json": "dd2dc3ba9a26dfd084aa23e9cb1ae78852bc991fed7a5b492e187a5a405d9dda",
    "safari-26-ios/challenge-page-answers.json": "48f9a613f0b4492fd5d422785bc2847463b33f4f61a39531493bdba554a2f4ee",
}


def profile_files(profile: Path) -> dict[str, Path]:
    """Verify normalized bytes and every relative answer before any network."""
    raw = profile.read_bytes()
    if hashlib.sha256(raw.replace(b"\r\n", b"\n")).hexdigest() != PROFILE_LF_HASH:
        raise ValueError("profile checksum mismatch")
    value = json.loads(raw)
    refs = {value.get("answers"), value.get("challenge_page_answers")}
    if refs != set(PROFILE_ASSETS):
        raise ValueError("unsupported profile answer references")
    result = {}
    for name, digest in PROFILE_ASSETS.items():
        source = profile.parent / name
        if hashlib.sha256(source.read_bytes().replace(b"\r\n", b"\n")).hexdigest() != digest:
            raise ValueError("profile answer checksum mismatch")
        result[name] = source
    return result
TOSUB2_BLOB = "ff2db2507e401c29fa78705e44f5b40a3108a8c6"


class ProtocolError(Exception):
    def __init__(self, code: str, retryable: bool = False, next_action: str = ""):
        self.code, self.retryable, self.next_action = code, retryable, next_action
        super().__init__(code)


def parse_time(value: str) -> float:
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("timezone required")
    return parsed.timestamp()


def timestamp(value: float) -> str:
    return datetime.fromtimestamp(value, timezone.utc).isoformat().replace("+00:00", "Z")


@dataclass(frozen=True)
class ProtocolConfig:
    tosub2_root: Path
    node: Path
    python: Path
    sentinel_assets: Path
    cfsolve_bin: Path
    cfsolve_profile: Path

    @classmethod
    def from_env(cls) -> "ProtocolConfig":
        package = ROOT.parent
        return cls(Path(os.getenv("TOSUB2_ROOT", str(package / "tosub2"))).resolve(),
                   Path(os.getenv("NODE_EXECUTABLE", str(package / "node"))).resolve(),
                   Path(os.getenv("TOSUB2_PYTHON", sys.executable)).resolve(),
                   Path(os.getenv("KILO_SENTINEL_ASSETS", str(ROOT / "sentinel_assets"))).resolve(),
                   Path(os.getenv("KILO_CFSOLVE_BIN", str(ROOT / "bin/kilo-cfsolve"))).resolve(),
                   Path(os.getenv("KILO_CFSOLVE_PROFILE", str(ROOT / "profiles/safari-26-ios.json"))).resolve())

    def health(self) -> dict[str, Any]:
        checks = {"node": self.node.is_file(), "python": self.python.is_file(),
                  "cfsolve": self.cfsolve_bin.is_file(), "tosub2": False, "sentinel": False, "profile": False}
        try:
            source = (self.tosub2_root / "src/protocol-login.mjs").read_bytes()
            checks["tosub2"] = (hashlib.sha1(f"blob {len(source)}\0".encode() + source).hexdigest() == TOSUB2_BLOB
                                 and (self.tosub2_root / "src/tls-transport.mjs").is_file()
                                 and (self.tosub2_root / "node_modules/jsdom/lib/api.js").is_file())
            checks["sentinel"] = all(hashlib.sha256((self.sentinel_assets / name).read_bytes()).hexdigest() == digest for name, digest in ASSET_HASHES.items()) and (self.sentinel_assets / "sentinel_curl_fetch.py").is_file()
            profile_files(self.cfsolve_profile)
            checks["profile"] = True
        except (OSError, ValueError):
            pass
        return checks


class Protocol:
    def __init__(self, config: ProtocolConfig):
        self.config = config

    def health(self) -> dict[str, Any]:
        checks = self.config.health()
        checks["cfsolve_profile_load"] = False
        if checks["cfsolve"] and checks["profile"]:
            try:
                checked = subprocess.run([str(self.config.cfsolve_bin)], input=json.dumps({"profile": str(self.config.cfsolve_profile), "check_only": True}).encode(),
                                         stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=20, check=False)
                checks["cfsolve_profile_load"] = checked.returncode == 0 and json.loads(checked.stdout).get("ready") is True
            except (OSError, subprocess.SubprocessError, ValueError):
                pass
        return {"ready": all(checks.values()), "checks": checks,
                "tosub2_commit": "8548397e89bf80e508eda64a87e0d556d43abc84",
                "official_contract": "4f7acd6e99ebc66870c07324341eaf53d8b1fa1f"}

    def _call(self, command: list[str], payload: dict, cancel: threading.Event,
              deadline: float, register) -> dict:
        if cancel.is_set():
            raise ProtocolError("CANCELLED")
        if time.time() >= deadline:
            raise ProtocolError("DEADLINE_EXCEEDED")
        # A minimal environment: the service bearer and parent secrets never
        # reach helpers; Python location is configuration, not login material.
        child_env = {key: value for key, value in os.environ.items() if key in
                     {"PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "SSL_CERT_FILE", "CURL_CA_BUNDLE", "PYTHONHOME"}}
        child_env.update({"PYTHONDONTWRITEBYTECODE": "1", "PYTHONUNBUFFERED": "1",
                          "SENTINEL_PYTHON": str(self.config.python)})
        options = {"start_new_session": True} if os.name != "nt" else {"creationflags": subprocess.CREATE_NEW_PROCESS_GROUP}
        try:
            child = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                     stderr=subprocess.DEVNULL, env=child_env, **options)
        except OSError:
            raise ProtocolError("EXECUTOR_UNAVAILABLE", True) from None
        register(child)
        raw = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode()
        output = b""
        try:
            first = True
            while True:
                if cancel.is_set() or time.time() >= deadline:
                    self.stop(child)
                    raise ProtocolError("CANCELLED" if cancel.is_set() else "DEADLINE_EXCEEDED")
                try:
                    output, _ = child.communicate(raw if first else None, timeout=min(0.2, max(0.01, deadline - time.time())))
                    break
                except subprocess.TimeoutExpired:
                    first = False
            if len(output) > MAX_PIPE:
                raise ProtocolError("REMOTE_RESPONSE_TOO_LARGE")
            try:
                result = json.loads(output)
            except (ValueError, UnicodeDecodeError):
                raise ProtocolError("REMOTE_RESPONSE_INVALID") from None
            if child.returncode != 0 or result.get("error_code"):
                raise ProtocolError(str(result.get("error_code") or "EXECUTOR_FAILED"),
                                    bool(result.get("retryable")), str(result.get("next_action") or ""))
            return result
        finally:
            raw = b""
            output = b""
            register(None)
            # A completed Node parent can leave a timed-out transport grandchild
            # behind. Reap its isolated group even after the parent has exited.
            self.stop(child)

    @staticmethod
    def stop(child) -> None:
        if child is None:
            return
        try:
            if os.name == "nt":
                if child.poll() is not None:
                    return
                subprocess.run(["taskkill", "/PID", str(child.pid), "/T", "/F"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
            else:
                os.killpg(child.pid, signal.SIGKILL)
        except (OSError, subprocess.SubprocessError):
            try:
                child.kill()
            except OSError:
                pass
        try:
            child.wait(timeout=3)
        except subprocess.TimeoutExpired:
            pass

    def execute(self, request: dict, private_state: dict | None, cancel: threading.Event, register) -> dict:
        if not self.health()["ready"]:
            raise ProtocolError("EXECUTOR_UNAVAILABLE", True, "安装匹配版本的toSub2、cfsolve和Sentinel运行资产")
        deadline = parse_time(request["deadline_at"])
        payload = dict(request)
        payload["private_state"] = private_state or {}
        payload["config"] = {"tosub2_root": str(self.config.tosub2_root), "node": str(self.config.node),
                             "python": str(self.config.python), "sentinel_assets": str(self.config.sentinel_assets),
                             "cfsolve_bin": str(self.config.cfsolve_bin), "cfsolve_profile": str(self.config.cfsolve_profile)}
        if request["phase"] in {"login", "reauthorize_subscription"}:
            solved = self._call([str(self.config.cfsolve_bin)],
                                {"proxy": request["login_proxy"]["url"], "profile": str(self.config.cfsolve_profile)},
                                cancel, deadline, register)
            if not isinstance(solved.get("token"), str) or not solved["token"]:
                raise ProtocolError("TURNSTILE_UNAVAILABLE", True)
            payload["turnstile_token"] = solved.pop("token")
        try:
            result = self._call([str(self.config.node), str(ROOT / "tosub2_adapter.mjs")], payload, cancel, deadline, register)
            if result.pop("ok", False) is not True:
                raise ProtocolError("AUTH_PROTOCOL_ERROR")
            return result
        finally:
            payload.clear()
