"""One administrator confirmation builds and rolls both HA APIs and workers."""
import argparse
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import secrets
import threading
import time
from urllib.parse import urlsplit

import builder
import cluster_rollout

VERSION = re.compile(r'(\d+\.\d+\.\d+-pk\.\d+)(?:-(?:ha|secondary)\.\d{8}\.\d+)?')
BUSY = {'building', 'deploying', 'interrupted'}
NODES = ('primary', 'secondary')


def need(value, code):
    if not value:
        raise ValueError(code)


def baseline(value):
    match = VERSION.fullmatch(value) if isinstance(value, str) else None
    need(match is not None, 'unrecognized_version')
    return match.group(1)


def ordinal(value):
    return tuple(int(part) for part in re.findall(r'\d+', baseline(value)))


def safe_error(error):
    value = str(error)
    return value if re.fullmatch(r'[a-zA-Z][a-zA-Z0-9_.:-]{0,159}', value) else type(error).__name__


def atomic(path, value):
    temporary = path.with_name('.' + path.name + '.' + secrets.token_hex(8))
    fd = os.open(str(temporary), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2)
            stream.write('\n'); stream.flush(); os.fsync(stream.fileno())
        os.replace(str(temporary), str(path))
        if os.name == 'posix':
            directory = os.open(str(path.parent), os.O_RDONLY | os.O_DIRECTORY)
            try: os.fsync(directory)
            finally: os.close(directory)
    finally:
        if temporary.exists(): temporary.unlink()


def release_target(value):
    need(isinstance(value, dict), 'release_invalid')
    target = {key: value.get(key) for key in ('baseline', 'tag', 'commit')}
    need(target['baseline'] == baseline(target['baseline']), 'release_baseline_invalid')
    need(isinstance(target['tag'], str) and re.fullmatch(r'v?\d+\.\d+\.\d+-pk\.\d+', target['tag']) and
         target['tag'].lstrip('v') == target['baseline'], 'release_tag_invalid')
    need(isinstance(target['commit'], str) and re.fullmatch(r'[a-f0-9]{40}', target['commit']), 'release_commit_invalid')
    return target


def observations(config):
    rows = cluster_rollout.observe(config)
    need(isinstance(rows, dict) and set(rows) == set(NODES), 'both_node_observations_required')
    result = {}
    for node in NODES:
        row = rows[node]
        need(isinstance(row, dict) and type(row.get('ready')) is bool and
             type(row.get('worker_running')) is bool, 'node_state_invalid')
        version, worker = row.get('version'), row.get('worker_version')
        result[node] = {'version': version, 'baseline': baseline(version), 'ready': row['ready'],
                        'worker_version': worker, 'worker_baseline': baseline(worker),
                        'worker_running': row['worker_running']}
    return result


def identities(nodes):
    return {node: {key: row[key] for key in ('version', 'worker_version')} for node, row in nodes.items()}


def get(config, path, token):
    connection = http.client.HTTPConnection('127.0.0.1', int(config.get('auth_port', 18092)), timeout=10)
    try:
        connection.request('GET', path, headers={'Host': config['auth_host'], 'Authorization': token, 'Connection': 'close'})
        response = connection.getresponse()
        raw = response.read(1024 * 1024 + 1)
        need(len(raw) <= 1024 * 1024, 'auth_response_too_large')
        return response.status, json.loads(raw)
    finally:
        connection.close()


class Controller:
    def __init__(self, config):
        self.config = config
        self.root = Path(config['root'])
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        need(not self.root.is_symlink(), 'controller_root_symlink')
        self.state_file = self.root / 'state-v2.json'
        self.lock = threading.RLock()
        self.poll_lock = threading.Lock()
        self.worker = None
        self.state = {'schema': 2, 'status': 'starting', 'pending': None, 'job': None, 'nodes': {}, 'latest': None}
        if self.state_file.exists():
            need(not self.state_file.is_symlink(), 'state_symlink')
            self.state = json.loads(self.state_file.read_text())
            need(self.state.get('schema') == 2, 'state_schema')
            self.state['pending'] = None
            if (self.state.get('job') or {}).get('status') in BUSY:
                self.state['status'] = 'interrupted'
                self.state['job'].update(status='interrupted', error='updater_restarted_review_required')
        self.save()

    def save(self):
        atomic(self.state_file, self.state)

    def snapshot(self):
        with self.lock:
            return dict(json.loads(json.dumps(self.state)), updates_enabled=self.config.get('rollout_enabled') is True,
                        update_scope=['primary_api', 'secondary_api', 'primary_worker', 'secondary_worker'])

    def check(self, locked=False):
        if not locked and not self.poll_lock.acquire(False): return self.snapshot()
        try:
            with self.lock:
                if (self.state.get('job') or {}).get('status') in BUSY: return self.snapshot()
            nodes = observations(self.config)
            # Publish actual observations even when GitHub is temporarily unavailable.
            with self.lock: self.state['nodes'] = nodes
            need(all(row['ready'] for row in nodes.values()), 'both_nodes_must_be_ready')
            target = release_target(builder.resolve_latest(self.config))
            current = [ordinal(row[key]) for row in nodes.values() for key in ('version', 'worker_version')]
            desired = ordinal(target['baseline'])
            pending = None
            if any(value > desired for value in current):
                status = 'newer_than_latest'
            elif min(current) < desired:
                candidate = dict(target, observed_versions=identities(nodes))
                with self.lock: prior = self.state.get('pending') or {}
                confirmation = prior.get('confirmation_id') if all(prior.get(key) == value for key, value in candidate.items()) else None
                pending = dict(candidate, confirmation_id=confirmation or secrets.token_hex(16))
                status = 'pending_confirmation'
            else:
                status = 'in_sync'
            with self.lock:
                self.state.update(status=status, pending=pending, latest=target, checked_at=int(time.time()), error=None)
                self.save()
        except Exception as error:
            with self.lock:
                self.state.update(status='check_failed', pending=None, latest=None, checked_at=int(time.time()), error=safe_error(error))
                self.save()
        finally:
            if not locked: self.poll_lock.release()
        return self.snapshot()

    def approve(self, payload, actor):
        with self.poll_lock:
            self.check(locked=True)
            return self.approve_checked(payload, actor)

    def approve_checked(self, payload, actor):
        with self.lock:
            need(self.config.get('rollout_enabled') is True, 'rollout_disabled')
            need(not self.worker or not self.worker.is_alive(), 'update_in_progress')
            need((self.state.get('job') or {}).get('status') not in BUSY, 'interrupted_job_requires_review')
            pending = self.state.get('pending')
            need(self.state.get('status') == 'pending_confirmation' and pending, 'no_confirmable_update')
            need(all(payload.get(key) == pending.get(key) for key in ('baseline', 'tag', 'commit', 'confirmation_id')), 'confirmation_stale')
            need(type(actor) is int and actor > 0, 'admin_actor_invalid')
            self.state['job'] = {'id': secrets.token_hex(12), 'status': 'building', 'phase': 'approved',
                                 'target': dict(pending), 'approved_by': actor, 'approved_at': int(time.time())}
            self.state.update(status='building', pending=None)
            self.save()
            self.worker = threading.Thread(target=self.execute, args=(dict(pending),), daemon=True)
            self.worker.start()
            return self.snapshot()

    def report(self, event):
        with self.lock:
            phase = event if isinstance(event, str) else (event.get('phase') or event.get('stage') or event.get('event') or 'working')
            self.state['job']['phase'] = re.sub(r'[^a-zA-Z0-9_.-]', '_', str(phase))[:100]
            self.state['job']['updated_at'] = int(time.time())
            if isinstance(event, dict) and event.get('node') in NODES:
                self.state['job']['node'] = event['node']
            self.save()

    def execute(self, target):
        deploying = False
        try:
            release = release_target(target)
            need(release_target(builder.resolve_latest(self.config)) == release, 'approved_release_changed')
            artifact = builder.build(self.config, release, self.report)
            need(artifact.get('baseline') == release['baseline'] and artifact.get('commit') == release['commit'], 'artifact_target_mismatch')
            need(baseline(artifact.get('version')) == release['baseline'], 'artifact_version_mismatch')
            need(release_target(builder.resolve_latest(self.config)) == release, 'approved_release_changed_during_build')
            observed = observations(self.config)
            need(identities(observed) == target['observed_versions'], 'cluster_versions_changed_during_build')
            need(all(row['ready'] for row in observed.values()), 'both_nodes_must_be_ready')
            need(all(ordinal(row[key]) <= ordinal(release['baseline']) for row in observed.values()
                     for key in ('version', 'worker_version')), 'cluster_downgrade_refused')
            with self.lock:
                self.state['job']['status'] = 'deploying'; self.state['status'] = 'deploying'; self.save()
            deploying = True
            result = cluster_rollout.deploy(self.config, artifact, self.report)
            need(result.get('phase') == 'completed', 'cluster_rollout_incomplete_review_required')
            nodes = observations(self.config)
            need(all(row['ready'] and row['version'] == artifact['version'] and row['worker_version'] == artifact['version']
                     for row in nodes.values()), 'final_cluster_versions_not_converged')
            with self.lock:
                self.state['job'].update(status='completed', phase='finished', finished_at=int(time.time()),
                                         version=artifact['version'], sha256=artifact['sha256'])
                self.state.update(status='completed', nodes=nodes); self.save()
        except Exception as error:
            with self.lock:
                status = 'interrupted' if deploying else 'failed'
                self.state['job'].update(status=status, error=safe_error(error), finished_at=int(time.time()))
                self.state.update(status=status, pending=None); self.save()
        self.check()

    def loop(self):
        while True:
            self.check()
            time.sleep(max(30, int(self.config.get('poll_interval_seconds', 300))))


class Handler(BaseHTTPRequestHandler):
    server_version = 'Sub2ClusterUpdaterV2'

    def log_message(self, *args): pass

    def send(self, status, value, mime='application/json'):
        raw = json.dumps(value, ensure_ascii=False).encode() if mime == 'application/json' else value
        self.send_response(status)
        self.send_header('Content-Type', mime + '; charset=utf-8')
        self.send_header('Content-Length', str(len(raw)))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.send_header('Content-Security-Policy', "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
        self.send_header('Connection', 'close')
        self.end_headers(); self.wfile.write(raw)

    def admin(self):
        token = self.headers.get('Authorization', '')
        if not token.startswith('Bearer ') or not 8 < len(token) <= 16000 or '\r' in token or '\n' in token:
            self.send(401, {'error': '请先登录项目管理员账号'}); return None
        try:
            code, response = get(self.server.controller.config, '/api/v1/auth/me', token)
            user = response.get('data', {})
            if code != 200 or user.get('role') != 'admin' or type(user.get('id')) is not int or user['id'] <= 0:
                self.send(403, {'error': '需要有效的管理员登录'}); return None
            return user['id']
        except Exception:
            self.send(503, {'error': '管理员身份验证暂不可用'}); return None

    def do_GET(self):
        if self.path in ('/cluster-update', '/cluster-update/', '/cluster-update/app.js'):
            name = 'app.js' if self.path.endswith('app.js') else 'index.html'
            return self.send(200, Path(__file__).with_name(name).read_bytes(), 'application/javascript' if name.endswith('.js') else 'text/html')
        if self.path == '/health': return self.send(200, {'status': 'ok', 'schema': 2})
        if self.path != '/cluster-update/api/status': return self.send(404, {'error': 'not_found'})
        if self.admin() is not None: self.send(200, self.server.controller.snapshot())

    def do_POST(self):
        if self.path not in ('/cluster-update/api/check', '/cluster-update/api/approve'):
            return self.send(404, {'error': 'not_found'})
        origin = self.headers.get('Origin', '')
        if origin not in self.server.controller.config.get('allowed_origins', []) or urlsplit(origin).netloc != self.headers.get('Host'):
            return self.send(403, {'error': 'origin_rejected'})
        actor = self.admin()
        if actor is None: return
        try:
            need(not self.headers.get('Transfer-Encoding'), 'body_invalid')
            size = int(self.headers.get('Content-Length', '0'))
            need(0 <= size <= 4096, 'body_too_large')
            raw = self.rfile.read(size)
            need(len(raw) == size, 'body_truncated')
            payload = json.loads(raw or b'{}')
            need(isinstance(payload, dict), 'body_invalid')
            controller = self.server.controller
            self.send(200, controller.check() if self.path.endswith('/check') else controller.approve(payload, actor))
        except Exception as error:
            code = safe_error(error)
            self.send(409, {'error': code, 'code': code})


def main():
    import fcntl
    parser = argparse.ArgumentParser(); parser.add_argument('--config', required=True); parser.add_argument('--once', action='store_true')
    args = parser.parse_args(); config = json.loads(Path(args.config).read_text())
    need(isinstance(config.get('auth_host'), str) and re.fullmatch(r'[A-Za-z0-9.-]+', config['auth_host']), 'auth_host_invalid')
    origins = config.get('allowed_origins', [])
    need(isinstance(origins, list) and bool(origins), 'allowed_origins_required')
    for origin in origins:
        parsed = urlsplit(origin)
        need(parsed.scheme == 'https' and parsed.netloc and not parsed.username and not parsed.path and not parsed.query and not parsed.fragment,
             'allowed_origin_invalid')
    for key in ('auth_port', 'listen_port'):
        need(type(config.get(key, 18092 if key == 'auth_port' else 18090)) is int and 1 <= config.get(key, 18092 if key == 'auth_port' else 18090) <= 65535,
             'listen_or_auth_port_invalid')
    root = Path(config['root']); root.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(str(root / 'controller-v2.lock'), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        controller = Controller(config)
        if args.once: print(json.dumps(controller.check())); return
        threading.Thread(target=controller.loop, daemon=True).start()
        httpd = ThreadingHTTPServer(('127.0.0.1', config.get('listen_port', 18090)), Handler)
        httpd.controller = controller; httpd.serve_forever()


if __name__ == '__main__': main()
