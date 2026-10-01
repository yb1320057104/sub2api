#!/usr/bin/env python3
"""Authenticated, fail-closed Binance CONNECT gateway (Python 3 standard library).

Configuration is an absolute-path JSON file containing bind, port,
auth_username, auth_password, and 1..8 nodes with name/host/port. Node hosts must
be loopback IP addresses: each listener must be pinned to one approved Hong
Kong node by the separately managed proxy service. No subscription is loaded.

POST /_binance/preflight with Authorization: Basic returns a route_token after
probing every candidate through its own listener: Cloudflare trace must report
loc=HK, then Binance's time endpoint must succeed within the same six seconds.
CONNECT api.binance.com:443
requires the same Proxy-Authorization: Basic and X-Binance-Route-Token. Tokens
last 240 seconds, are reusable within a query, and never change their node.
GET /health requires Authorization: Basic. All HTTP responses close the
connection, except a successful CONNECT. No request/exception/token is logged.

The gateway's HTTP endpoint belongs on a trusted private host/container link;
Basic authentication on that link is not encrypted. Binance application bytes
are forwarded as opaque end-to-end TLS, without interception or inspection.
"""

import argparse
import base64
import binascii
import concurrent.futures
import hashlib
import hmac
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import select
import signal
import socket
import socketserver
import ssl
import stat
import sys
import threading
import time


TARGET = "api.binance.com:443"
GEO_HOST = "www.cloudflare.com"
TOKEN_TTL = 240.0
NODE_TIMEOUT = 6.0
PREFLIGHT_TIMEOUT = 12.0
HEADER_TIMEOUT = 8.0
CONNECT_TIMEOUT = 8.0
MAX_HEADERS = 16384
MAX_HEADER_FIELDS = 64
MAX_PROBE_BODY = 8192
MAX_CONNECTIONS = 64
# The recharge scheduler runs three account workers; reserve another slot for
# an administrator query so normal polling does not repeatedly reject one account.
MAX_PREFLIGHTS = 4
MAX_TOKENS = 512
MAX_RELAY_BUFFER = 65536
MAX_TUNNEL_BYTES = 64 * 1024 * 1024
TUNNEL_IDLE_TIMEOUT = 30.0
TUNNEL_MAX_LIFETIME = 240.0
HEADER_NAME = re.compile(r"^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
TOKEN_PATTERN = re.compile(r"^[A-Za-z0-9_-]{32,256}$")


class GatewayError(Exception):
    """An internal failure; its text is never sent to clients or logs."""


def remaining(deadline):
    duration = deadline - time.monotonic()
    if duration <= 0:
        raise GatewayError("deadline")
    return duration


def recv_before(sock, size, deadline):
    sock.settimeout(remaining(deadline))
    return sock.recv(size)


def send_before(sock, data, deadline):
    sock.settimeout(remaining(deadline))
    sock.sendall(data)


def read_headers(sock, deadline):
    """Bound the whole header block, preserving any subsequent opaque bytes."""
    data = bytearray()
    while True:
        boundary = data.find(b"\r\n\r\n")
        if boundary >= 0:
            if boundary + 4 > MAX_HEADERS:
                raise GatewayError("headers too large")
            lines = bytes(data[:boundary]).split(b"\r\n")
            if not lines or len(lines) > MAX_HEADER_FIELDS + 1:
                raise GatewayError("header count")
            first = lines[0].decode("ascii")
            if len(first) > 4096:
                raise GatewayError("request line")
            headers = {}
            for line in lines[1:]:
                if len(line) > 4096 or b":" not in line:
                    raise GatewayError("header format")
                name, value = line.split(b":", 1)
                name = name.decode("ascii")
                if not HEADER_NAME.fullmatch(name):
                    raise GatewayError("header name")
                value = value.strip(b" \t")
                if any(ch < 32 or ch == 127 for ch in value):
                    raise GatewayError("header value")
                name = name.lower()
                if name in headers:
                    raise GatewayError("duplicate header")
                headers[name] = value.decode("latin-1")
            return first, headers, bytes(data[boundary + 4:])
        if len(data) >= MAX_HEADERS:
            raise GatewayError("headers too large")
        chunk = recv_before(sock, min(4096, MAX_HEADERS - len(data)), deadline)
        if not chunk:
            raise GatewayError("incomplete headers")
        data.extend(chunk)


def status_code(first):
    parts = first.split(" ", 2)
    if len(parts) < 2 or parts[0] not in ("HTTP/1.0", "HTTP/1.1"):
        raise GatewayError("response status")
    if len(parts[1]) != 3 or not parts[1].isdigit():
        raise GatewayError("response status")
    return int(parts[1])


class BodyReader:
    def __init__(self, sock, prefix, deadline):
        self.sock = sock
        self.data = bytearray(prefix)
        self.deadline = deadline
        self.received = len(prefix)

    def fill(self):
        # Include chunk framing/trailers in the wire-size bound.
        if self.received >= MAX_PROBE_BODY + 4096:
            raise GatewayError("probe body too large")
        chunk = recv_before(self.sock, min(4096, MAX_PROBE_BODY + 4096 - self.received), self.deadline)
        self.received += len(chunk)
        self.data.extend(chunk)
        return bool(chunk)

    def take(self, size):
        while len(self.data) < size:
            if not self.fill():
                raise GatewayError("incomplete body")
        result = bytes(self.data[:size])
        del self.data[:size]
        return result

    def line(self, limit=1024):
        while True:
            end = self.data.find(b"\r\n")
            if end >= 0:
                if end > limit:
                    raise GatewayError("body line too large")
                return self.take(end + 2)[:-2]
            if len(self.data) > limit or not self.fill():
                raise GatewayError("invalid body line")


def read_probe_body(sock, headers, prefix, deadline):
    if headers.get("content-encoding", "identity").lower() != "identity":
        raise GatewayError("encoded probe response")
    reader = BodyReader(sock, prefix, deadline)
    transfer = headers.get("transfer-encoding", "").lower()
    length = headers.get("content-length")
    if transfer:
        if transfer != "chunked" or length is not None:
            raise GatewayError("response framing")
        body = bytearray()
        chunks = 0
        while True:
            line = reader.line(128)
            # Extensions are unnecessary for this fixed public JSON endpoint.
            if not re.fullmatch(rb"[0-9a-fA-F]{1,8}", line):
                raise GatewayError("invalid chunk")
            size = int(line, 16)
            chunks += 1
            if chunks > 128 or size > MAX_PROBE_BODY - len(body):
                raise GatewayError("probe body too large")
            if size == 0:
                # No trailers are needed by this endpoint; fail closed on them.
                if reader.line() != b"":
                    raise GatewayError("unexpected trailers")
                return bytes(body)
            body.extend(reader.take(size))
            if reader.take(2) != b"\r\n":
                raise GatewayError("invalid chunk ending")
    if length is not None:
        if not re.fullmatch(r"[0-9]{1,6}", length):
            raise GatewayError("invalid length")
        size = int(length)
        if size <= 0 or size > MAX_PROBE_BODY or len(prefix) > size:
            raise GatewayError("probe body too large")
        return reader.take(size)
    while reader.fill():
        if len(reader.data) > MAX_PROBE_BODY:
            raise GatewayError("probe body too large")
    if len(reader.data) > MAX_PROBE_BODY:
        raise GatewayError("probe body too large")
    return bytes(reader.data)


def load_config(filename):
    path = Path(filename)
    if not path.is_absolute():
        raise GatewayError("absolute configuration path required")
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_size > 65536:
        raise GatewayError("configuration file")
    if os.name == "posix" and info.st_mode & 0o077:
        raise GatewayError("configuration must be owner-only")
    with path.open("r", encoding="utf-8") as handle:
        config = json.load(handle)
    if not isinstance(config, dict):
        raise GatewayError("configuration object")
    bind = ipaddress.ip_address(config.get("bind", "172.19.0.1"))
    if bind.version != 4 or bind.is_unspecified or bind.is_multicast or not (bind.is_private or bind.is_loopback):
        raise GatewayError("private IPv4 bind required")
    port = config.get("port", 17990)
    if type(port) is not int or not 1 <= port <= 65535:
        raise GatewayError("bind port")
    username, password = config.get("auth_username"), config.get("auth_password")
    if not isinstance(username, str) or not isinstance(password, str):
        raise GatewayError("authentication required")
    if not 1 <= len(username) <= 128 or ":" in username or not 24 <= len(password) <= 512:
        raise GatewayError("authentication length")
    if any(ord(ch) < 33 or ord(ch) > 126 for ch in username + password):
        raise GatewayError("authentication characters")
    nodes = config.get("nodes")
    if not isinstance(nodes, list) or not 1 <= len(nodes) <= 8:
        raise GatewayError("node count")
    checked = []
    names, endpoints = set(), set()
    for node in nodes:
        if not isinstance(node, dict):
            raise GatewayError("node object")
        name, host, node_port = node.get("name"), node.get("host"), node.get("port")
        if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z0-9_.-]{1,64}", name):
            raise GatewayError("node name")
        if not isinstance(host, str) or not ipaddress.ip_address(host).is_loopback:
            raise GatewayError("loopback node listener required")
        if type(node_port) is not int or not 1 <= node_port <= 65535:
            raise GatewayError("node port")
        if name in names or (host, node_port) in endpoints:
            raise GatewayError("duplicate node")
        names.add(name)
        endpoints.add((host, node_port))
        checked.append((name, host, node_port))
    principal = hashlib.sha256((username + ":" + password).encode("ascii")).digest()
    return str(bind), port, principal, tuple(checked)


class GatewayState:
    def __init__(self, principal, nodes):
        self.principal = principal
        self.nodes = nodes
        self.lock = threading.Lock()
        self.tokens = {}
        self.sockets = set()
        self.stopping = threading.Event()
        self.force_stop = threading.Event()
        self.preflights = threading.BoundedSemaphore(MAX_PREFLIGHTS)
        self.executor = concurrent.futures.ThreadPoolExecutor(max_workers=MAX_PREFLIGHTS * 8, thread_name_prefix="node-probe")
        self.tls = ssl.create_default_context()
        self.tls.minimum_version = ssl.TLSVersion.TLSv1_2
        self.tls.set_alpn_protocols(["http/1.1"])

    def authenticated(self, header):
        candidate = b""
        valid = False
        if isinstance(header, str) and len(header) <= 1024:
            parts = header.split(" ", 1)
            if len(parts) == 2 and parts[0].lower() == "basic":
                try:
                    candidate = base64.b64decode(parts[1], validate=True)
                    valid = bool(candidate)
                except (ValueError, binascii.Error):
                    pass
        matches = hmac.compare_digest(hashlib.sha256(candidate).digest(), self.principal)
        return matches and valid

    def track(self, sock):
        with self.lock:
            if self.force_stop.is_set():
                sock.close()
                raise GatewayError("stopping")
            self.sockets.add(sock)

    def close(self, sock):
        if sock is None:
            return
        with self.lock:
            self.sockets.discard(sock)
        try:
            sock.close()
        except OSError:
            pass

    def purge_locked(self):
        now = time.monotonic()
        self.tokens = {key: value for key, value in self.tokens.items() if value[0] > now}

    def issue(self, node):
        token = secrets.token_urlsafe(32)
        key = hashlib.sha256(token.encode("ascii")).digest()
        with self.lock:
            self.purge_locked()
            if len(self.tokens) >= MAX_TOKENS or self.stopping.is_set():
                raise GatewayError("token capacity")
            self.tokens[key] = (time.monotonic() + TOKEN_TTL, node, self.principal)
        return token

    def route(self, token):
        if not isinstance(token, str) or not TOKEN_PATTERN.fullmatch(token):
            return None
        key = hashlib.sha256(token.encode("ascii")).digest()
        with self.lock:
            self.purge_locked()
            entry = self.tokens.get(key)
            if entry is None or not hmac.compare_digest(entry[2], self.principal):
                return None
            return entry[1], entry[0]

    def lookup(self, token):
        route = self.route(token)
        return None if route is None else route[0]

    def upstream(self, node, deadline, target=TARGET):
        if target not in (TARGET, GEO_HOST + ":443"):
            raise GatewayError("invalid internal destination")
        sock = socket.create_connection((node[1], node[2]), timeout=remaining(deadline))
        self.track(sock)
        try:
            send_before(sock, ("CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n\r\n").encode("ascii"), deadline)
            first, headers, extra = read_headers(sock, deadline)
            if status_code(first) != 200 or extra or headers.get("transfer-encoding") or headers.get("content-length", "0") != "0":
                raise GatewayError("upstream CONNECT rejected")
            return sock
        except Exception:
            self.close(sock)
            raise

    def probe_response(self, node, host, path, deadline):
        sock = None
        try:
            sock = self.upstream(node, deadline, host + ":443")
            sock.settimeout(remaining(deadline))
            plain = sock
            sock = self.tls.wrap_socket(plain, server_hostname=host, do_handshake_on_connect=False)
            with self.lock:
                self.sockets.discard(plain)
            self.track(sock)
            sock.settimeout(remaining(deadline))
            sock.do_handshake()
            request = "GET " + path + " HTTP/1.1\r\nHost: " + host + "\r\nAccept: */*\r\nAccept-Encoding: identity\r\nConnection: close\r\n\r\n"
            send_before(sock, request.encode("ascii"), deadline)
            first, headers, prefix = read_headers(sock, deadline)
            if status_code(first) != 200:
                raise GatewayError("probe response status")
            return read_probe_body(sock, headers, prefix, deadline)
        finally:
            self.close(sock)

    def probe(self, node, overall_deadline):
        started = time.monotonic()
        deadline = min(started + NODE_TIMEOUT, overall_deadline)
        try:
            trace = self.probe_response(node, GEO_HOST, "/cdn-cgi/trace", deadline).decode("ascii")
            countries = [line.partition("=")[2] for line in trace.splitlines() if line.partition("=")[0] == "loc"]
            if countries != ["HK"]:
                return None
            payload = json.loads(self.probe_response(node, "api.binance.com", "/api/v3/time", deadline))
            if not isinstance(payload, dict) or type(payload.get("serverTime")) is not int or not 0 < payload["serverTime"] < 2**63:
                return None
            return time.monotonic() - started, node
        except Exception:
            return None

    def preflight(self):
        deadline = time.monotonic() + PREFLIGHT_TIMEOUT
        futures = [self.executor.submit(self.probe, node, deadline) for node in self.nodes]
        successes = []
        try:
            for future in concurrent.futures.as_completed(futures, timeout=remaining(deadline)):
                result = future.result()
                if result is not None:
                    successes.append(result)
        except concurrent.futures.TimeoutError:
            pass
        finally:
            for future in futures:
                future.cancel()
        if not successes or self.stopping.is_set():
            raise GatewayError("no available node")
        return self.issue(min(successes, key=lambda item: item[0])[1])

    def close_all(self):
        self.force_stop.set()
        with self.lock:
            sockets = tuple(self.sockets)
            self.tokens.clear()
        for sock in sockets:
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            self.close(sock)


def relay(client, upstream, prefix, state, route_expires):
    client.setblocking(False)
    upstream.setblocking(False)
    peers = {client: upstream, upstream: client}
    pending = {client: bytearray(), upstream: bytearray(prefix)}
    readable = set(peers)
    write_closed = set()
    started = last_activity = time.monotonic()
    total = len(prefix)
    while not state.force_stop.is_set():
        now = time.monotonic()
        if now >= route_expires or now - started >= TUNNEL_MAX_LIFETIME or now - last_activity >= TUNNEL_IDLE_TIMEOUT:
            return
        for source, destination in peers.items():
            if source not in readable and not pending[destination] and destination not in write_closed:
                try:
                    destination.shutdown(socket.SHUT_WR)
                except OSError:
                    pass
                write_closed.add(destination)
        if not readable and not any(pending.values()):
            return
        reads = [sock for sock in readable if len(pending[peers[sock]]) < MAX_RELAY_BUFFER]
        writes = [sock for sock in peers if pending[sock]]
        ready_read, ready_write, _ = select.select(reads, writes, [], 1.0)
        for sock in ready_write:
            try:
                count = sock.send(pending[sock])
            except BlockingIOError:
                continue
            if count <= 0:
                return
            del pending[sock][:count]
            last_activity = time.monotonic()
        for sock in ready_read:
            try:
                chunk = sock.recv(min(16384, MAX_RELAY_BUFFER - len(pending[peers[sock]])))
            except BlockingIOError:
                continue
            if not chunk:
                readable.discard(sock)
                continue
            total += len(chunk)
            if total > MAX_TUNNEL_BYTES:
                return
            pending[peers[sock]].extend(chunk)
            last_activity = time.monotonic()


class GatewayHandler(socketserver.BaseRequestHandler):
    def reply(self, status, body, proxy_auth=False):
        labels = {200: "OK", 400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 407: "Proxy Authentication Required", 429: "Too Many Requests", 502: "Bad Gateway", 503: "Service Unavailable"}
        payload = json.dumps(body, separators=(",", ":")).encode("utf-8")
        headers = ["HTTP/1.1 %d %s" % (status, labels[status]), "Content-Type: application/json", "Cache-Control: no-store", "Connection: close", "Content-Length: %d" % len(payload)]
        if status in (401, 407):
            headers.append(("Proxy-Authenticate" if proxy_auth else "WWW-Authenticate") + ': Basic realm="binance-gateway"')
        if status in (429, 503):
            headers.append("Retry-After: 15")
        send_before(self.request, ("\r\n".join(headers) + "\r\n\r\n").encode("ascii") + payload, time.monotonic() + 2.0)

    def handle(self):
        state = self.server.state
        upstream = None
        tunnel_started = False
        try:
            first, headers, prefix = read_headers(self.request, time.monotonic() + HEADER_TIMEOUT)
            parts = first.split(" ")
            if len(parts) != 3 or parts[2] not in ("HTTP/1.0", "HTTP/1.1"):
                self.reply(400, {"error": "invalid_request"})
                return
            method, path, _ = parts
            connect = method == "CONNECT"
            if not state.authenticated(headers.get("proxy-authorization" if connect else "authorization")):
                self.reply(407 if connect else 401, {"error": "authentication_required"}, proxy_auth=connect)
                return
            if state.stopping.is_set():
                self.reply(503, {"error": "stopping"})
                return
            if "transfer-encoding" in headers or headers.get("content-length", "0") != "0" or "expect" in headers:
                self.reply(400, {"error": "request_body_not_supported"})
                return
            if connect:
                if path != TARGET or headers.get("host", TARGET).lower() != TARGET:
                    self.reply(403, {"error": "destination_forbidden"})
                    return
                route = state.route(headers.get("x-binance-route-token"))
                if route is None:
                    self.reply(403, {"error": "route_token_invalid"})
                    return
                node, route_expires = route
                try:
                    upstream = state.upstream(node, min(route_expires, time.monotonic() + CONNECT_TIMEOUT))
                except Exception:
                    self.reply(502, {"error": "selected_node_unavailable"})
                    return
                send_before(self.request, b"HTTP/1.1 200 Connection Established\r\n\r\n", min(route_expires, time.monotonic() + 2.0))
                tunnel_started = True
                relay(self.request, upstream, prefix, state, route_expires)
                return
            if prefix:
                self.reply(400, {"error": "request_body_not_supported"})
                return
            if method == "GET" and path == "/health":
                self.reply(200, {"status": "ok", "configured_nodes": len(state.nodes)})
                return
            if method != "POST" or path != "/_binance/preflight":
                self.reply(404, {"error": "not_found"})
                return
            if not state.preflights.acquire(blocking=False):
                self.reply(429, {"error": "preflight_busy"})
                return
            try:
                token = state.preflight()
            except Exception:
                self.reply(503, {"error": "no_available_node"})
            else:
                self.reply(200, {"route_token": token})
            finally:
                state.preflights.release()
        except Exception:
            if not tunnel_started:
                try:
                    self.reply(400, {"error": "request_failed"})
                except Exception:
                    pass
        finally:
            state.close(upstream)


class GatewayServer(socketserver.ThreadingMixIn, socketserver.TCPServer):
    allow_reuse_address = True
    daemon_threads = False
    block_on_close = True
    request_queue_size = 64

    def __init__(self, address, state):
        self.state = state
        self.connection_slots = threading.BoundedSemaphore(MAX_CONNECTIONS)
        super().__init__(address, GatewayHandler)

    def process_request(self, request, client_address):
        if self.state.stopping.is_set() or not self.connection_slots.acquire(blocking=False):
            self.shutdown_request(request)
            return
        try:
            self.state.track(request)
            super().process_request(request, client_address)
        except Exception:
            self.state.close(request)
            self.connection_slots.release()
            self.shutdown_request(request)

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.state.close(request)
            self.connection_slots.release()

    def handle_error(self, request, client_address):
        # socketserver's default traceback can expose request/exception details.
        pass


def main():
    parser = argparse.ArgumentParser(description="Private authenticated Binance CONNECT gateway")
    parser.add_argument("--config", required=True, help="absolute path to owner-only JSON configuration")
    args = parser.parse_args()
    state = None
    server = None
    try:
        bind, port, principal, nodes = load_config(args.config)
        state = GatewayState(principal, nodes)
        server = GatewayServer((bind, port), state)
    except Exception:
        if state is not None:
            state.executor.shutdown(wait=True)
        print("binance_gateway: startup failed; check private configuration and listener availability", file=sys.stderr)
        return 2
    stop_requested = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stop_requested.set())
    signal.signal(signal.SIGINT, lambda *_: stop_requested.set())
    serving = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.25}, name="gateway-accept")
    serving.start()
    print("binance_gateway: ready", flush=True)
    try:
        stop_requested.wait()
    finally:
        state.stopping.set()
        server.shutdown()
        serving.join()
        # Stop accepting immediately, then allow in-flight traffic a short drain.
        deadline = time.monotonic() + 5.0
        while time.monotonic() < deadline:
            with state.lock:
                active = bool(state.sockets)
            if not active:
                break
            time.sleep(0.1)
        state.close_all()
        server.server_close()
        state.executor.shutdown(wait=True)
        print("binance_gateway: stopped", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
