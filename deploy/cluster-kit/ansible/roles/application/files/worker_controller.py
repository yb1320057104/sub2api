#!/usr/bin/env python3
"""Start the private worker only on the observed writer; no database mutations.

The application database leases still own individual scheduled jobs. This small
controller chooses a worker host, follows ordinary Patroni failover and drains
the old process. It does not promise exactly-once external side effects during
a network partition or a machine freeze.
"""
import argparse
import http.client
import ipaddress
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import threading
import time


def need(value, code):
    if not value:
        raise RuntimeError(code)


def event(name, **fields):
    print(json.dumps(dict(event=name, at=int(time.time()), **fields)), flush=True)


def private_read(path):
    path = Path(path)
    need(not path.is_symlink(), 'private_file_symlink')
    metadata = path.stat()
    need(stat.S_ISREG(metadata.st_mode) and metadata.st_uid == 0 and
         metadata.st_mode & 0o077 == 0, 'private_file_required')
    return path.read_bytes()


def validate_config(config):
    need(config.get('schema') == 1, 'config_schema')
    site = config.get('site_id', '')
    need(re.fullmatch(r'[a-z][a-z0-9-]{1,23}', site), 'site_identity')
    need(config.get('node_role') in ('primary', 'secondary'), 'node_identity')
    need(config.get('mode') in ('single', 'ha'), 'site_mode')
    address = ipaddress.ip_address(config.get('mesh_ip', ''))
    need(address.version == 4 and (address.is_private or address.is_loopback), 'private_mesh_required')
    if config['mode'] == 'single':
        need(address.is_loopback and config['node_role'] == 'primary', 'single_host_identity')
    else:
        need(not address.is_loopback, 'ha_mesh_identity')
    need(re.fullmatch(r'[a-f0-9]{64}', config.get('container_id', '')), 'worker_id_required')
    need(re.fullmatch(r'sha256:[a-f0-9]{64}', config.get('image_id', '')), 'worker_image_required')
    need(config.get('container_name') == site + '-worker', 'worker_name_required')
    need(config.get('lock_file') == '/run/sub2api-' + site + '-worker.lock', 'lock_scope')
    need(config.get('status_file') == '/opt/sub2api-sites/' + site + '/application/worker-status.json', 'status_scope')
    return config


def writer_document(value, config):
    if config['mode'] == 'single':
        return (value.get('readiness_schema') == 1 and value.get('status') == 'ready' and
                value.get('dependencies') == {'postgres': 'primary', 'redis': 'master'})
    patroni = value.get('patroni') or {}
    return (value.get('state') == 'running' and value.get('role') in ('primary', 'master') and
            patroni.get('scope') == config['site_id'] and patroni.get('name') == config['node_role'])


def probe_writer(config):
    # Direct HTTPConnection never inherits the operator's HTTP_PROXY setting.
    ha = config['mode'] == 'ha'
    connection = http.client.HTTPConnection(config['mesh_ip'], 8008 if ha else 18080, timeout=1)
    try:
        connection.request('GET', '/primary' if ha else '/ready', headers={'Connection': 'close'})
        response = connection.getresponse()
        raw = response.read(16385)
        return response.status == 200 and len(raw) <= 16384 and writer_document(json.loads(raw), config)
    except (OSError, ValueError, http.client.HTTPException, AttributeError):
        return False
    finally:
        connection.close()


def command(argv):
    value = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
    need(value.returncode == 0, 'docker_command_failed')
    return value.stdout


class Docker:
    def __init__(self, config):
        self.config = config

    def inspect(self):
        config = self.config
        rows = json.loads(command(['/usr/bin/docker', 'inspect', '--type', 'container', config['container_id']]))
        need(len(rows) == 1, 'worker_missing')
        row = rows[0]
        need(row['Id'] == config['container_id'] and row['Image'] == config['image_id'] and
             row['Name'] == '/' + config['container_name'], 'worker_identity_changed')
        labels = row['Config'].get('Labels') or {}
        need(labels.get('sub2api.site') == config['site_id'] and
             labels.get('sub2api.node') == config['node_role'] and
             labels.get('sub2api.role') == 'worker', 'worker_labels_changed')
        env = dict(value.split('=', 1) for value in row['Config'].get('Env', []) if '=' in value)
        need(env.get('SUB2API_WORKER_ONLY') == 'true' and env.get('SUB2API_BACKGROUND_MODE') == 'active'
             and env.get('SUB2API_DOCKER_SELF_UPDATE') == 'false', 'worker_role_changed')
        host = row['HostConfig']
        need(host.get('RestartPolicy', {}).get('Name') in ('no', '') and not host.get('AutoRemove') and
             not host.get('Privileged'), 'worker_restart_policy_changed')
        state = row['State']
        need(not state.get('Paused') and not state.get('Restarting'), 'worker_state_ambiguous')
        return state

    def start(self):
        need(not self.inspect().get('Running'), 'worker_already_running')
        command(['/usr/bin/docker', 'start', self.config['container_id']])
        need(self.inspect().get('Running'), 'worker_start_failed')

    def terminate(self):
        if self.inspect().get('Running'):
            command(['/usr/bin/docker', 'kill', '--signal=TERM', self.config['container_id']])


class Controller:
    def __init__(self, config, docker, probe=probe_writer):
        self.config, self.docker, self.probe = config, docker, probe
        self.positive = 0
        self.stopping = False
        self.last = None

    def step(self):
        writer = self.probe(self.config)
        state = self.docker.inspect()
        running = state.get('Running') is True
        if not writer:
            self.positive = 0
            if running and not self.stopping:
                self.docker.terminate()
                self.stopping = True
                event('worker_term_writer_unavailable')
            if not running:
                self.stopping = False
        elif self.stopping:
            # A returning writer must wait until its prior TERM really exits.
            self.positive = 0
            if not running:
                self.stopping = False
        else:
            self.positive += 1
            if not running and self.positive >= 2:
                self.docker.start()
                running = True
                event('worker_started_after_writer_confirmation')
        observed = {'writer': writer, 'running': running, 'stopping': self.stopping}
        if observed != self.last:
            event('worker_state', **observed)
            self.last = observed
        return observed

    def close(self):
        if self.docker.inspect().get('Running'):
            if not self.stopping:
                self.docker.terminate()
            event('worker_waiting_for_ordered_shutdown')
            while self.docker.inspect().get('Running'):
                time.sleep(1)
        event('worker_stopped')


def write_status(config, observed):
    path = Path(config['status_file'])
    temporary = path.with_suffix('.pending')
    need(not temporary.is_symlink() and not path.is_symlink(), 'status_symlink')
    raw = json.dumps(dict(observed, at=int(time.time()), container_id=config['container_id'])).encode()
    fd = os.open(str(temporary), os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(raw)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(str(temporary), str(path))


def check(config):
    state = json.loads(private_read(config['status_file']))
    need(state.get('container_id') == config['container_id'] and
         0 <= time.time() - state.get('at', 0) < 15, 'supervisor_status_stale')
    writer = probe_writer(config)
    running = Docker(config).inspect().get('Running') is True
    need(writer == state.get('writer') and running == state.get('running') and
         not state.get('stopping') and running == writer, 'worker_not_settled_on_writer')
    return {'ok': True, 'writer': writer, 'worker_running': running}


def main():
    import fcntl
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('config')
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    need(os.geteuid() == 0, 'root_required')
    config = validate_config(json.loads(private_read(args.config)))
    if args.check:
        print(json.dumps(check(config)))
        return
    lock = os.open(config['lock_file'], os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    stop = threading.Event()
    for number in (signal.SIGTERM, signal.SIGINT):
        signal.signal(number, lambda *_: stop.set())
    controller = Controller(config, Docker(config))
    try:
        while not stop.is_set():
            # An unknown Docker state stops the supervisor; it never guesses a
            # replacement container identity. systemd retries this exact config.
            observed = controller.step()
            write_status(config, observed)
            stop.wait(2)
    finally:
        try:
            controller.close()
        finally:
            os.close(lock)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        event('supervisor_failed', reason=str(error) if type(error) is RuntimeError else type(error).__name__)
        sys.exit(1)
