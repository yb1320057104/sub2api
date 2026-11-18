"""One HTTPS request over the task's fixed proxy; stdin/stdout are private pipes."""
from __future__ import annotations

import json
import sys
from urllib.parse import urlsplit

ALLOWED = {"app.kilo.ai", "kilo.ai", "api.kilo.ai", "auth.openai.com", "sentinel.openai.com"}
MAX_BODY = 1024 * 1024


def fetch(payload: dict) -> dict:
    import requests

    target = urlsplit(str(payload.get("url", "")))
    proxy = urlsplit(str(payload.get("proxy", "")))
    if (target.scheme != "https" or target.hostname not in ALLOWED or target.username or
            target.password or target.fragment or target.port not in (None, 443)):
        return {"error_code": "REMOTE_REDIRECT_REJECTED", "retryable": False}
    if proxy.scheme not in {"http", "https", "socks5", "socks5h"} or not proxy.hostname or not proxy.port:
        return {"error_code": "LOGIN_PROXY_INVALID", "retryable": False}
    headers = {str(k): str(v) for k, v in payload.get("headers", {}).items()}
    authorization = next((v for k, v in headers.items() if k.lower() == "authorization"), None)
    # Only the fixed profile read verifies a saved Kilo identity key. OAuth,
    # callbacks and device issuance must continue to use the web Cookie jar.
    if authorization is not None and (target.hostname != "api.kilo.ai" or target.path != "/api/profile"
            or target.query or payload.get("method", "GET") != "GET" or not authorization.startswith("Bearer ")
            or not 1 <= len(authorization[7:]) <= 16384 or any(ord(c) < 33 or ord(c) > 126 for c in authorization[7:])):
        return {"error_code": "REMOTE_REQUEST_INVALID", "retryable": False}
    timeout = max(1, min(120, float(payload.get("timeout", 45))))
    with requests.Session() as session:
        session.trust_env = False
        session.proxies = {"http": payload["proxy"], "https": payload["proxy"]}
        try:
            with session.request(str(payload.get("method", "GET")), payload["url"],
                                 headers=headers, data=payload.get("body"), timeout=timeout,
                                 allow_redirects=False, stream=True) as response:
                body = bytearray()
                for chunk in response.iter_content(65536):
                    body.extend(chunk)
                    if len(body) > MAX_BODY:
                        return {"error_code": "REMOTE_RESPONSE_TOO_LARGE", "retryable": False}
                raw = response.raw.headers
                return {"status": response.status_code, "headers": list(response.headers.items()),
                        "set_cookie": raw.getlist("Set-Cookie"),
                        "body": body.decode(response.encoding or "utf-8", errors="replace")}
        except requests.RequestException:
            return {"error_code": "REMOTE_UNAVAILABLE", "retryable": True}


def main() -> None:
    try:
        raw = sys.stdin.buffer.read(2 * MAX_BODY + 1)
        if len(raw) > 2 * MAX_BODY:
            raise ValueError
        result = fetch(json.loads(raw))
    except Exception:
        result = {"error_code": "TRANSPORT_ERROR", "retryable": False}
    sys.stdout.write(json.dumps(result, separators=(",", ":")))


if __name__ == "__main__":
    main()
