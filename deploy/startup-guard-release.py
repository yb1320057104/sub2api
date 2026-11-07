#!/usr/bin/env python3
"""Trusted publisher startup-guard preparation. Linux root, Python 3.6+ stdlib.

stdin is a release binding from the trusted publisher, NOT an application API.
--config points to an independently installed root-private deployment policy.
plan/check only read; prepare can append one executable hash to the reviewed
guard and update its evidence. This program never replaces application binaries,
executes a guard, signals a process, or starts/restarts/stops a service.

The canonical scaffold replaces the unique hash alternation in the exact
``case "$guard_hash" in`` block with ``<ALLOWED_EXECUTABLE_SHA256>``. Its SHA256
must be pinned independently in the root configuration. A guard mounted into
the running container is unsupported (atomic host rename cannot retarget it).
"""
from __future__ import print_function

import argparse
import copy
import hashlib
import json
import os
import re
import stat
import subprocess
import sys
import time
import uuid
from contextlib import contextmanager

sys.dont_write_bytecode = True

MAX_DOCUMENT = 1024 * 1024
ENTRYPOINT = '/app/docker-entrypoint.sh'
APP_PATHS = ('/app/sub2api', '/app/data/bin/sub2api')
REPOSITORY = 'yb1320057104/sub2api'
TARGET = 'production-primary'
DOCKER = '/usr/bin/docker'
SYSTEMCTL = '/usr/bin/systemctl'
ARMED_PATH = '/root/sub2api-project-guardian/private/armed.json'
INACTIVE_UNITS = ('sub2api-project-guardian.service',
                  'sub2api-project-guardian-armed.service',
                  'sub2api-project-fence.service')
SHA_RE = re.compile(r'^[0-9a-f]{64}$')
GIT_RE = re.compile(r'^[0-9a-f]{40}$')
NAME_RE = re.compile(r'^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$')
VERSION_RE = re.compile(r'^0\.2\.19-pk\.([1-9][0-9]*)$')
UTC_RE = re.compile(r'^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$')
BOOT_RE = re.compile(r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
ALLOWLIST_RE = re.compile(
    br'case "\$guard_hash" in\n    ([0-9a-f]{64}(?:\|[0-9a-f]{64})*)\) ;;\n'
    br'    \*\) guard_fail ;;\nesac\n')


class Refused(Exception):
    """A fixed public error code; never include command output or input values."""


def require(value, code):
    if not value:
        raise Refused(code)


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def encode(value):
    return (json.dumps(value, sort_keys=True, separators=(',', ':'),
                       ensure_ascii=True, allow_nan=False) + '\n').encode('ascii')


def strict_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, 'duplicate_json_key')
            result[key] = value
        return result
    def invalid(unused):
        raise Refused('invalid_json_number')
    try:
        return json.loads(raw.decode('utf-8') if isinstance(raw, bytes) else raw,
                          object_pairs_hook=pairs, parse_constant=invalid)
    except (ValueError, TypeError, UnicodeError):
        raise Refused('invalid_json')


def keys(value, expected, code):
    require(isinstance(value, dict) and set(value) == set(expected), code)


def is_sha(value):
    return isinstance(value, str) and SHA_RE.fullmatch(value) is not None


def absolute_path(value):
    return (isinstance(value, str) and value.startswith('/') and value != '/'
            and all(part not in ('', '.', '..') for part in value.split('/')[1:])
            and not any(ord(char) < 32 or ord(char) == 127 for char in value))


def validate_request(value):
    keys(value, ('schema_version', 'operation', 'target', 'release'), 'invalid_request_schema')
    require(type(value['schema_version']) is int and value['schema_version'] == 1,
            'unsupported_request_schema')
    require(value['operation'] in ('plan', 'prepare', 'check') and value['target'] == TARGET,
            'unsupported_operation_or_target')
    release = value['release']
    keys(release, ('repository', 'version', 'commit', 'tree', 'package_id',
                   'binary_sha256', 'manifest_sha256'), 'invalid_release_schema')
    require(release['repository'] == REPOSITORY, 'untrusted_repository')
    version = VERSION_RE.fullmatch(release['version']) if isinstance(release['version'], str) else None
    require(version is not None and int(version.group(1)) >= 57, 'unsupported_release_version')
    require(all(isinstance(release[key], str) and GIT_RE.fullmatch(release[key])
                for key in ('commit', 'tree')), 'invalid_source_identity')
    require(isinstance(release['package_id'], str) and
            re.fullmatch(r'[1-9][0-9]*-' + release['commit'][:12], release['package_id']),
            'invalid_package_identity')
    require(all(is_sha(release[key]) for key in ('binary_sha256', 'manifest_sha256')),
            'invalid_release_hash')
    return value


def validate_config(value):
    keys(value, ('schema_version', 'target', 'repository', 'container_name', 'container_id',
                 'project', 'service', 'working_dir', 'guard_path', 'receipt_path',
                 'journal_path', 'lock_path', 'backup_dir', 'guard_scaffold_sha256'),
         'invalid_config_schema')
    require(type(value['schema_version']) is int and value['schema_version'] == 1
            and value['target'] == TARGET and value['repository'] == REPOSITORY,
            'unsupported_config')
    require(all(isinstance(value[key], str) and NAME_RE.fullmatch(value[key])
                for key in ('container_name', 'project', 'service')), 'invalid_config_identity')
    require(is_sha(value['container_id']) and is_sha(value['guard_scaffold_sha256']),
            'invalid_config_hash')
    for key in ('working_dir', 'guard_path', 'receipt_path', 'journal_path', 'lock_path', 'backup_dir'):
        require(absolute_path(value[key]), 'invalid_config_path')
    directory = value['working_dir'] + '/ha-startup-guard'
    require(value['guard_path'] == directory + '/entrypoint.sh'
            and value['receipt_path'] == directory + '/receipt.json'
            and value['journal_path'] == directory + '/install-state.json'
            and value['backup_dir'] == directory + '/releases'
            and value['lock_path'] == value['working_dir'] + '/.ha-startup-guard-install.lock',
            'config_path_contract_mismatch')
    return value


def guard_parts(raw):
    require(len(raw) <= MAX_DOCUMENT and raw.count(b'case "$guard_hash" in') == 1,
            'ambiguous_guard_allowlist')
    matches = list(ALLOWLIST_RE.finditer(raw))
    require(len(matches) == 1, 'unsupported_guard_allowlist')
    match = matches[0]
    hashes = match.group(1).decode('ascii').split('|')
    require(1 <= len(hashes) <= 256 and len(set(hashes)) == len(hashes), 'invalid_guard_allowlist')
    scaffold = raw[:match.start(1)] + b'<ALLOWED_EXECUTABLE_SHA256>' + raw[match.end(1):]
    return hashes, sha(scaffold), match.span(1)


def append_hash(raw, binary_sha256):
    hashes, unused, span = guard_parts(raw)
    if binary_sha256 in hashes:
        return raw
    require(len(hashes) < 256, 'guard_allowlist_capacity_exceeded')
    return raw[:span[1]] + b'|' + binary_sha256.encode('ascii') + raw[span[1]:]


def file_identity(info):
    return (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid,
            info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def owned(info, directory=False, mode=None):
    require(stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode), 'unsafe_file_type')
    require(info.st_uid == 0 and info.st_gid == 0 and not info.st_mode & 0o7022,
            'unsafe_file_owner_or_mode')
    if mode is not None:
        require(stat.S_IMODE(info.st_mode) == mode, 'unexpected_file_mode')


def secure_directory(path):
    require(absolute_path(path) or path == '/', 'invalid_directory_path')
    fd = os.open('/', os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        owned(os.fstat(fd), directory=True)
        for component in path.split('/')[1:]:
            if not component:
                continue
            child = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                            dir_fd=fd)
            os.close(fd)
            fd = child
            owned(os.fstat(fd), directory=True)
        return fd
    except Exception:
        os.close(fd)
        raise


def read_at(parent, name, mode, optional=False):
    require('/' not in name and name not in ('', '.', '..'), 'invalid_basename')
    try:
        fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK, dir_fd=parent)
    except FileNotFoundError:
        require(optional, 'required_file_missing')
        return None
    try:
        before = os.fstat(fd)
        owned(before, mode=mode)
        require(before.st_size <= MAX_DOCUMENT, 'file_too_large')
        chunks, length = [], 0
        while True:
            part = os.read(fd, min(65536, MAX_DOCUMENT + 1 - length))
            if not part:
                break
            chunks.append(part)
            length += len(part)
            require(length <= MAX_DOCUMENT, 'file_too_large')
        require(file_identity(before) == file_identity(os.fstat(fd)) ==
                file_identity(os.stat(name, dir_fd=parent, follow_symlinks=False)), 'file_changed_during_read')
        return b''.join(chunks)
    finally:
        os.close(fd)


def secure_read(path, mode=None, optional=False):
    parent = secure_directory(os.path.dirname(path))
    try:
        return read_at(parent, os.path.basename(path), mode, optional)
    finally:
        os.close(parent)


def atomic_write_at(parent, name, raw, mode, expected):
    """Compare bytes before/after staging, then rename in the pinned parent.

    Concurrent privileged writers must share the lock. We never truncate a
    destination inode (including a mounted guard) or overwrite an unknown hash.
    """
    def compare():
        current = read_at(parent, name, mode, optional=expected is None)
        require((current is None if expected is None else current is not None and sha(current) == expected),
                'destination_compare_failed')
    compare()
    temp = '.guard-release-' + uuid.uuid4().hex
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
                 mode, dir_fd=parent)
    renamed = False
    try:
        os.fchmod(fd, mode)
        os.fchown(fd, 0, 0)
        view = memoryview(raw)
        while view:
            count = os.write(fd, view)
            require(count > 0, 'short_write')
            view = view[count:]
        os.fsync(fd)
        compare()
        os.rename(temp, name, src_dir_fd=parent, dst_dir_fd=parent)
        renamed = True
        os.fsync(parent)
    finally:
        os.close(fd)
        if not renamed:
            os.unlink(temp, dir_fd=parent)


class LinuxHost(object):
    def __init__(self, config):
        self.config = config

    def run(self, args):
        # Defense in depth: all process execution is restricted to read-only
        # Docker inspect/top or systemd property queries, never a shell.
        require((len(args) >= 3 and args[0] == DOCKER and args[1] in ('inspect', 'top'))
                or (len(args) >= 3 and args[:2] == [SYSTEMCTL, 'show']), 'forbidden_command')
        environment = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LANG': 'C', 'LC_ALL': 'C',
                       'HOME': '/root', 'DOCKER_HOST': 'unix:///var/run/docker.sock'}
        try:
            result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    env=environment, timeout=15)
        except (OSError, subprocess.TimeoutExpired):
            raise Refused('readonly_command_failed')
        require(result.returncode == 0 and len(result.stdout) <= 8 * MAX_DOCUMENT, 'readonly_command_failed')
        return result.stdout

    def inspect(self):
        template = ('{"Id":{{json .Id}},"Image":{{json .Image}},"State":{"Pid":{{json .State.Pid}},'
                    '"StartedAt":{{json .State.StartedAt}},"Running":{{json .State.Running}},'
                    '"Paused":{{json .State.Paused}},"Restarting":{{json .State.Restarting}},'
                    '"Health":{{if .State.Health}}{{json .State.Health.Status}}{{else}}null{{end}}},'
                    '"RestartCount":{{json .RestartCount}},"Path":{{json .Path}},"Args":{{json .Args}},'
                    '"Entrypoint":{{json .Config.Entrypoint}},"Cmd":{{json .Config.Cmd}},"User":{{json .Config.User}},'
                    '"Init":{{json .HostConfig.Init}},"RestartPolicy":{{json .HostConfig.RestartPolicy}},'
                    '"Mounts":{{json .Mounts}},'
                    '"Project":{{json (index .Config.Labels "com.docker.compose.project")}},'
                    '"Service":{{json (index .Config.Labels "com.docker.compose.service")}},'
                    '"ConfigFiles":{{json (index .Config.Labels "com.docker.compose.project.config_files")}},'
                    '"WorkingDir":{{json (index .Config.Labels "com.docker.compose.project.working_dir")}}}')
        return strict_json(self.run([DOCKER, 'inspect', '--format', template, self.config['container_name']]))

    def proc_read(self, path, limit):
        with open(path, 'rb') as source:
            raw = source.read(limit + 1)
        require(len(raw) <= limit, 'proc_file_too_large')
        return raw

    def proc_stat(self, pid):
        require(type(pid) is int and pid > 0, 'invalid_pid')
        fields = self.proc_read('/proc/{0}/stat'.format(pid), 8192).decode('ascii').rsplit(')', 1)[1].split()
        return {'pid': pid, 'parent_pid': int(fields[1]), 'start_ticks': int(fields[19])}

    def process(self, pid, app=False):
        before = self.proc_stat(pid)
        prefix = '/proc/{0}'.format(pid)
        executable = os.readlink(prefix + '/exe')
        require(executable in (APP_PATHS if app else ('/sbin/docker-init', '/usr/bin/docker-init')),
                'unexpected_process_executable')
        result = dict(before, exe_path=executable)
        result['args'] = [arg.decode('utf-8') for arg in self.proc_read(prefix + '/cmdline', MAX_DOCUMENT).split(b'\0') if arg]
        if app:
            values = [item for item in self.proc_read(prefix + '/environ', 4 * MAX_DOCUMENT).split(b'\0')
                      if item.startswith(b'SUB2API_SKIP_STARTUP_SLOT_CLEANUP=')]
            require(values == [b'SUB2API_SKIP_STARTUP_SLOT_CLEANUP=true'], 'startup_cleanup_not_disabled')
            digest = hashlib.sha256()
            with open(prefix + '/exe', 'rb') as source:
                info = os.fstat(source.fileno())
                require(stat.S_ISREG(info.st_mode) and info.st_size <= 512 * MAX_DOCUMENT, 'unsafe_executable')
                for block in iter(lambda: source.read(MAX_DOCUMENT), b''):
                    digest.update(block)
                require(file_identity(info) == file_identity(os.fstat(source.fileno())), 'executable_changed')
            result.update(exe_sha256=digest.hexdigest(), exe_device=info.st_dev, exe_inode=info.st_ino)
            require(result['args'] == ['/proc/self/fd/9'], 'unexpected_application_command')
        else:
            require(result['args'] == [executable, '--', ENTRYPOINT, '/app/sub2api'], 'unexpected_init_command')
        require(before == self.proc_stat(pid) and executable == os.readlink(prefix + '/exe'), 'process_changed')
        return result

    def quiescent(self):
        for unit in INACTIVE_UNITS:
            raw = self.run([SYSTEMCTL, 'show', unit, '--property=ActiveState', '--property=SubState',
                            '--property=MainPID', '--property=LoadState']).decode('ascii')
            pairs = [line.split('=', 1) for line in raw.splitlines() if '=' in line]
            values = dict(pairs)
            require(len(values) == len(pairs) and set(values) == {'ActiveState', 'SubState', 'MainPID', 'LoadState'}
                    and values['ActiveState'] == 'inactive' and values['MainPID'] == '0'
                    and values['SubState'] == 'dead' and values['LoadState'] == 'loaded',
                    'guardian_or_fence_not_inactive')
        parent = secure_directory(os.path.dirname(ARMED_PATH))
        try:
            try:
                os.stat(os.path.basename(ARMED_PATH), dir_fd=parent, follow_symlinks=False)
            except FileNotFoundError:
                return
            raise Refused('armed_marker_present')
        finally:
            os.close(parent)

    def snapshot(self):
        self.quiescent()
        container = self.inspect()
        state = container['State']
        require(container['Id'] == self.config['container_id'] and state['Running'] is True
                and state['Paused'] is False and state['Restarting'] is False and state['Health'] == 'healthy',
                'container_not_healthy')
        require(container['Project'] == self.config['project'] and container['Service'] == self.config['service']
                and container['WorkingDir'] == self.config['working_dir'], 'container_identity_mismatch')
        require(container['Entrypoint'] == [ENTRYPOINT] and container['Path'] == ENTRYPOINT
                and container['Cmd'] == ['/app/sub2api'] and container['Args'] == ['/app/sub2api']
                and container['Init'] is True and container['User'] in ('', '0', 'root'), 'launch_contract_changed')
        require(container['RestartPolicy'] == {'Name': 'unless-stopped', 'MaximumRetryCount': 0},
                'restart_policy_changed')
        for mount in container['Mounts']:
            destination = mount['Destination'].rstrip('/') or '/'
            require(destination not in ('/', '/app', ENTRYPOINT), 'mounted_guard_requires_coordination')
        init = self.process(state['Pid'])
        rows = self.run([DOCKER, 'top', self.config['container_id'], '-eo', 'pid,ppid,comm']).decode('ascii').splitlines()[1:]
        found = []
        for row in rows:
            values = row.split(None, 2)
            require(len(values) == 3, 'invalid_process_inventory')
            pid, parent = int(values[0]), int(values[1])
            try:
                executable = os.readlink('/proc/{0}/exe'.format(pid))
            except FileNotFoundError:
                require(parent != init['pid'], 'process_inventory_changed')
                continue
            if executable in APP_PATHS:
                require(parent == init['pid'], 'unexpected_application_parent')
                found.append(pid)
            else:
                require(os.path.basename(executable.replace(' (deleted)', '')) not in ('sub2api', 'sub2api.backup'),
                        'unmanaged_application_process')
        require(len(found) == 1, 'ambiguous_application_process')
        app = self.process(found[0], app=True)
        require(app['parent_pid'] == init['pid'], 'unexpected_application_parent')
        boot = self.proc_read('/proc/sys/kernel/random/boot_id', 128).decode('ascii').strip()
        require(container == self.inspect() and init == self.process(init['pid'])
                and app == self.process(app['pid'], app=True), 'sampling_unstable')
        return {'container': container, 'init': init, 'app': app, 'boot_id': boot}

    @contextmanager
    def entry_parent(self, snapshot):
        require(self.proc_stat(snapshot['init']['pid']) == {key: snapshot['init'][key]
                for key in ('pid', 'parent_pid', 'start_ticks')}, 'init_epoch_changed')
        root = os.open('/proc/{0}/root'.format(snapshot['init']['pid']), os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
        try:
            owned(os.fstat(root), directory=True)
            app = os.open('app', os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=root)
            try:
                owned(os.fstat(app), directory=True)
                yield app
            finally:
                os.close(app)
        finally:
            os.close(root)

    def read(self, key, snapshot):
        if key == 'container_guard':
            with self.entry_parent(snapshot) as parent:
                return read_at(parent, 'docker-entrypoint.sh', 0o755)
        return secure_read(self.config[key + '_path'], mode=0o755 if key == 'guard' else 0o600)

    def compose_matches(self, receipt):
        return all(sha(secure_read(item['path'])) == item['sha256'] for item in receipt['compose']['files'])

    def write(self, key, raw, expected, snapshot):
        if key == 'container_guard':
            with self.entry_parent(snapshot) as parent:
                atomic_write_at(parent, 'docker-entrypoint.sh', raw, 0o755, expected)
        else:
            self.write_path(self.config[key + '_path'], raw, expected, 0o755 if key == 'guard' else 0o600)

    def write_path(self, path, raw, expected, mode=0o600):
        parent = secure_directory(os.path.dirname(path))
        try:
            atomic_write_at(parent, os.path.basename(path), raw, mode, expected)
        finally:
            os.close(parent)

    @contextmanager
    def lock(self, write=False):
        import fcntl
        path = self.config['lock_path']
        parent = secure_directory(os.path.dirname(path))
        fd = None
        try:
            fd = os.open(os.path.basename(path), (os.O_RDWR | os.O_CREAT if write else os.O_RDONLY)
                         | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=parent)
            owned(os.fstat(fd), mode=0o600)
            try:
                fcntl.flock(fd, (fcntl.LOCK_EX if write else fcntl.LOCK_SH) | fcntl.LOCK_NB)
            except OSError:
                raise Refused('guard_installation_lock_busy')
            require(file_identity(os.fstat(fd)) ==
                    file_identity(os.stat(os.path.basename(path), dir_fd=parent, follow_symlinks=False)), 'lock_identity_changed')
            yield
        finally:
            if fd is not None:
                os.close(fd)
            os.close(parent)

    def record_path(self, request):
        return self.config['backup_dir'] + '/' + request['release']['version'] + '.json'

    def read_record(self, request):
        try:
            parent = secure_directory(self.config['backup_dir'])
        except FileNotFoundError:
            return None
        try:
            owned(os.fstat(parent), directory=True, mode=0o700)
            return read_at(parent, os.path.basename(self.record_path(request)), 0o600, optional=True)
        finally:
            os.close(parent)

    def mkdir(self, path, existing=False):
        parent = secure_directory(os.path.dirname(path))
        try:
            try:
                os.mkdir(os.path.basename(path), 0o700, dir_fd=parent)
                os.fsync(parent)
            except FileExistsError:
                require(existing, 'backup_directory_exists')
            child = os.open(os.path.basename(path), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                            dir_fd=parent)
            try:
                owned(os.fstat(child), directory=True, mode=0o700)
            finally:
                os.close(child)
        finally:
            os.close(parent)

    def backup(self, request, baseline):
        self.mkdir(self.config['backup_dir'], existing=True)
        path = self.config['backup_dir'] + '/attempt-' + uuid.uuid4().hex
        self.mkdir(path)
        for key, raw in baseline['files'].items():
            self.write_path(path + '/' + key + '.original', raw, None)
        self.write_path(path + '/request.json', encode(request), None)
        self.write_path(path + '/epoch.json', encode(baseline['snapshot']), None)
        return path

    def failure(self, path, code, restored):
        self.write_path(path + '/failure.json', encode({'schema_version': 1, 'failure_code': code,
                        'restored': restored, 'restart_performed': False}), None)


def validate_receipt(receipt, journal, files, hashes, config, snapshot):
    keys(receipt, ('schema_version', 'container', 'original_epoch', 'installed', 'compose',
                   'allowed_executable_sha256', 'expected', 'installed_at'), 'invalid_install_receipt')
    require(type(receipt['schema_version']) is int and receipt['schema_version'] == 1,
            'unsupported_install_receipt')
    require(receipt['container'] == {'name': config['container_name'], 'id': config['container_id']},
            'receipt_container_mismatch')
    original = receipt['original_epoch']
    keys(original, ('started_at', 'pid', 'start_ticks', 'exe_sha256', 'init_pid', 'init_start_ticks',
                    'restart_count', 'boot_id'), 'invalid_original_epoch')
    require(all(type(original[key]) is int and original[key] > 0
                for key in ('pid', 'start_ticks', 'init_pid', 'init_start_ticks'))
            and type(original['restart_count']) is int and original['restart_count'] >= 0
            and isinstance(original['started_at'], str) and UTC_RE.fullmatch(original['started_at'])
            and isinstance(original['boot_id'], str) and BOOT_RE.fullmatch(original['boot_id'])
            and original['exe_sha256'] in hashes, 'invalid_original_epoch')
    require(isinstance(receipt['installed_at'], str) and UTC_RE.fullmatch(receipt['installed_at']),
            'invalid_installed_at')
    installed = receipt['installed']
    keys(installed, ('entrypoint_path', 'entrypoint_sha256', 'host_guard_path', 'host_guard_sha256', 'assets'),
         'invalid_installed_contract')
    require(installed == {'entrypoint_path': ENTRYPOINT, 'entrypoint_sha256': sha(files['guard']),
                         'host_guard_path': config['guard_path'], 'host_guard_sha256': sha(files['guard']),
                         'assets': []}, 'receipt_guard_mismatch')
    require(receipt['allowed_executable_sha256'] == hashes, 'receipt_allowlist_mismatch')
    compose = receipt['compose']
    keys(compose, ('files', 'project', 'service', 'normalized_sha256', 'working_dir'), 'invalid_compose_receipt')
    require(compose['project'] == config['project'] and compose['service'] == config['service']
            and compose['working_dir'] == config['working_dir'] and is_sha(compose['normalized_sha256']),
            'receipt_compose_mismatch')
    require(isinstance(compose['files'], list) and 1 <= len(compose['files']) <= 8, 'invalid_compose_files')
    for item in compose['files']:
        keys(item, ('path', 'sha256'), 'invalid_compose_file')
        require(absolute_path(item['path']) and is_sha(item['sha256']), 'invalid_compose_file')
    require(len(set(item['path'] for item in compose['files'])) == len(compose['files']), 'duplicate_compose_file')
    container = snapshot['container']
    require(container['ConfigFiles'] == ','.join(item['path'] for item in compose['files']), 'compose_labels_changed')
    expected = receipt['expected']
    keys(expected, ('entrypoint', 'cmd', 'restart_policy', 'legacy_mounts', 'recreated_mounts'), 'invalid_expected_contract')
    require(expected['entrypoint'] == container['Entrypoint'] and expected['cmd'] == container['Cmd']
            and expected['restart_policy'] == container['RestartPolicy'], 'receipt_launch_mismatch')
    for name in ('legacy_mounts', 'recreated_mounts'):
        require(isinstance(expected[name], list) and 1 <= len(expected[name]) <= 32, 'invalid_receipt_mounts')
        destinations = set()
        for mount in expected[name]:
            keys(mount, ('Type', 'Source', 'Destination', 'RW', 'Propagation'), 'invalid_receipt_mount')
            require(mount['Type'] in ('bind', 'volume') and absolute_path(mount['Source'])
                    and absolute_path(mount['Destination']) and mount['Destination'] not in destinations
                    and type(mount['RW']) is bool
                    and mount['Propagation'] in ('', 'private', 'rprivate', 'slave', 'rslave', 'shared', 'rshared'),
                    'invalid_receipt_mount')
            destinations.add(mount['Destination'])
    persistent = [mount for mount in expected['recreated_mounts'] if mount['Destination'] == ENTRYPOINT]
    require(len(persistent) == 1 and persistent[0]['Type'] == 'bind'
            and persistent[0]['Source'] == config['guard_path'] and persistent[0]['RW'] is False,
            'missing_persistent_guard_contract')
    require(not any(mount['Destination'] in ('/', '/app', ENTRYPOINT)
                    for mount in expected['legacy_mounts']), 'unsupported_legacy_guard_mount')
    mounts = [{key: mount.get(key) for key in ('Type', 'Source', 'Destination', 'RW', 'Propagation')}
              for mount in container['Mounts']]
    require(sorted(mounts, key=lambda m: m['Destination']) ==
            sorted(expected['legacy_mounts'], key=lambda m: m['Destination']), 'mount_contract_changed')
    keys(journal, ('schema_version', 'status', 'phase', 'container_id', 'receipt_sha256'), 'invalid_install_journal')
    require(type(journal['schema_version']) is int and journal == {'schema_version': 1, 'status': 'complete',
            'phase': 'complete', 'container_id': config['container_id'], 'receipt_sha256': sha(files['receipt'])},
            'incomplete_or_unbound_install_journal')


def verify_binding(record, request, config, authorized=False):
    keys(record, ('schema_version', 'status', 'target', 'release', 'container_id', 'guard_scaffold_sha256',
                  'backup_path', 'prepared_at_unix', 'before_guard_sha256', 'after_guard_sha256', 'epoch_sha256'),
         'invalid_release_receipt')
    require(type(record['schema_version']) is int and record['schema_version'] == 1
            and record['target'] == request['target'] and record['release'] == request['release']
            and record['container_id'] == config['container_id']
            and record['guard_scaffold_sha256'] == config['guard_scaffold_sha256'], 'release_binding_conflict')
    require(record['status'] in ('preparing', 'authorized', 'rolled_back', 'recovery_required'), 'invalid_release_receipt')
    require(absolute_path(record['backup_path']) and record['backup_path'].startswith(config['backup_dir'] + '/attempt-')
            and type(record['prepared_at_unix']) is int and record['prepared_at_unix'] > 0
            and all(is_sha(record[key]) for key in ('before_guard_sha256', 'after_guard_sha256', 'epoch_sha256')),
            'invalid_release_receipt')
    if authorized:
        require(record['status'] == 'authorized', 'release_preparation_incomplete')


def baseline(request, config, host):
    first = host.snapshot()
    files = {key: host.read(key, first) for key in ('guard', 'container_guard', 'receipt', 'journal')}
    require(files['guard'] == files['container_guard'], 'host_container_guard_mismatch')
    hashes, scaffold, unused = guard_parts(files['guard'])
    require(scaffold == config['guard_scaffold_sha256'], 'unreviewed_guard_scaffold')
    require(first['app']['exe_sha256'] in hashes, 'unapproved_running_executable')
    receipt, journal = strict_json(files['receipt']), strict_json(files['journal'])
    validate_receipt(receipt, journal, files, hashes, config, first)
    require(host.compose_matches(receipt), 'compose_file_changed')
    record_raw = host.read_record(request)
    record = strict_json(record_raw) if record_raw is not None else None
    if record is not None:
        verify_binding(record, request, config)
    require(host.snapshot() == first, 'epoch_changed')
    require(all(host.read(key, first) == raw for key, raw in files.items()), 'evidence_changed')
    require(host.read_record(request) == record_raw, 'release_receipt_changed')
    require(host.compose_matches(receipt), 'compose_file_changed')
    return {'snapshot': first, 'files': files, 'hashes': hashes, 'receipt': receipt,
            'journal': journal, 'record': record, 'record_raw': record_raw}


def result(request, host, state, read_only):
    allowed = request['release']['binary_sha256'] in state['hashes']
    authorized = state['record'] is not None and state['record']['status'] == 'authorized' and allowed
    return {'schema_version': 1, 'operation': request['operation'], 'target': request['target'],
            'release': request['release'], 'status': 'authorized' if authorized else 'ready',
            'allowed': allowed, 'healthy': True, 'epoch_unchanged': True, 'receipt_binding_verified': True,
            'host_guard_sha256': sha(state['files']['guard']),
            'container_guard_sha256': sha(state['files']['container_guard']),
            'receipt_sha256': sha(state['files']['receipt']), 'journal_sha256': sha(state['files']['journal']),
            'epoch_sha256': sha(encode(state['snapshot'])), 'release_receipt_path': host.record_path(request),
            'read_only': read_only, 'binary_replaced': False, 'restart_performed': False,
            'ha_authority_grant': False}


def prepare(request, config, host, before):
    require(host.snapshot() == before['snapshot'], 'epoch_changed_before_prepare')
    backup_path = host.backup(request, before)
    snapshot = before['snapshot']
    old = before['files']
    new_guard = append_hash(old['guard'], request['release']['binary_sha256'])
    new_receipt = copy.deepcopy(before['receipt'])
    new_receipt['allowed_executable_sha256'] = guard_parts(new_guard)[0]
    new_receipt['installed']['entrypoint_sha256'] = sha(new_guard)
    new_receipt['installed']['host_guard_sha256'] = sha(new_guard)
    receipt_raw = encode(new_receipt)
    incomplete = encode({'schema_version': 1, 'status': 'incomplete', 'phase': 'release_prepare',
                         'container_id': config['container_id'], 'receipt_sha256': sha(old['receipt'])})
    complete = encode(dict(before['journal'], receipt_sha256=sha(receipt_raw)))
    record = {'schema_version': 1, 'status': 'preparing', 'target': request['target'], 'release': request['release'],
              'container_id': config['container_id'], 'guard_scaffold_sha256': config['guard_scaffold_sha256'],
              'backup_path': backup_path, 'prepared_at_unix': int(time.time()),
              'before_guard_sha256': sha(old['guard']), 'after_guard_sha256': sha(new_guard),
              'epoch_sha256': sha(encode(snapshot))}
    record_expected = sha(before['record_raw']) if before['record_raw'] is not None else None
    attempted = {}
    current = dict(old)
    record_known = set([record_expected])

    def stable():
        require(host.snapshot() == snapshot, 'epoch_changed_during_prepare')

    def write(key, raw):
        stable()
        attempted.setdefault(key, set()).add(sha(raw))
        host.write(key, raw, sha(current[key]), snapshot)
        current[key] = raw

    def save_record():
        nonlocal record_expected
        stable()
        raw = encode(record)
        record_known.add(sha(raw))
        host.write_path(host.record_path(request), raw, record_expected)
        record_expected = sha(raw)

    try:
        # Incomplete journal precedes every permission mutation. The complete
        # journal is last among the installation files, after stable postchecks.
        write('journal', incomplete)
        save_record()
        if new_guard != old['guard']:
            write('guard', new_guard)
            write('container_guard', new_guard)
        stable()
        require(host.read('guard', snapshot) == host.read('container_guard', snapshot) == new_guard,
                'guard_postcheck_failed')
        write('receipt', receipt_raw)
        stable()
        require(host.read('receipt', snapshot) == receipt_raw, 'receipt_postcheck_failed')
        write('journal', complete)
        verified = baseline(request, config, host)
        require(verified['snapshot'] == snapshot, 'epoch_changed_after_prepare')
        record['status'] = 'authorized'
        save_record()
        verified = baseline(request, config, host)
        require(verified['snapshot'] == snapshot, 'epoch_changed_after_receipt')
        return result(request, host, verified, False)
    except Exception as error:
        code = str(error) if isinstance(error, Refused) else 'prepare_io_failure'
        restored = False
        try:
            stable()
            # Re-mark incomplete before rollback if complete was already
            # published. A failed fsync can occur after rename, hence both
            # attempted and original bytes are recognized explicitly.
            for key in ('guard', 'container_guard', 'receipt', 'journal'):
                observed = host.read(key, snapshot)
                require(sha(observed) in attempted.get(key, set()) | {sha(old[key])}, 'rollback_external_drift')
            observed_journal = host.read('journal', snapshot)
            if observed_journal != incomplete:
                host.write('journal', incomplete, sha(observed_journal), snapshot)
            for key in ('guard', 'container_guard', 'receipt'):
                stable()
                observed = host.read(key, snapshot)
                if observed != old[key]:
                    require(sha(observed) in attempted.get(key, set()), 'rollback_external_drift')
                    host.write(key, old[key], sha(observed), snapshot)
            stable()
            require(all(host.read(key, snapshot) == old[key] for key in ('guard', 'container_guard', 'receipt')),
                    'rollback_postcheck_failed')
            observed = host.read('journal', snapshot)
            require(observed == incomplete, 'rollback_journal_changed')
            host.write('journal', old['journal'], sha(observed), snapshot)
            stable()
            restored = all(host.read(key, snapshot) == raw for key, raw in old.items())
        except Exception:
            # Unknown concurrent bytes or a new epoch are never overwritten.
            # Leave/mark only our journal incomplete; recovery needs inspection.
            try:
                observed = host.read('journal', snapshot)
                if sha(observed) in attempted.get('journal', set()) | {sha(old['journal'])}:
                    host.write('journal', incomplete, sha(observed), snapshot)
            except Exception:
                pass
        try:
            observed = host.read_record(request)
            observed_sha = sha(observed) if observed is not None else None
            if observed_sha in record_known:
                record['status'] = 'rolled_back' if restored else 'recovery_required'
                host.write_path(host.record_path(request), encode(record), observed_sha)
            host.failure(backup_path, code, restored)
        except Exception:
            pass
        raise Refused('prepare_failed_restored' if restored else 'prepare_failed_recovery_required')


def execute(request, config, host):
    validate_request(request)
    validate_config(config)
    # No file/directory creation even for a prepare request until read-only
    # gates have passed. Every invocation subsequently shares the installer lock.
    initial = baseline(request, config, host)
    with host.lock(write=request['operation'] == 'prepare'):
        state = baseline(request, config, host)
        require(state['snapshot'] == initial['snapshot'], 'epoch_changed_before_lock')
        if request['operation'] == 'plan':
            return result(request, host, state, True)
        if request['operation'] == 'check':
            require(state['record'] is not None, 'release_not_prepared')
            verify_binding(state['record'], request, config, authorized=True)
            require(request['release']['binary_sha256'] in state['hashes'], 'candidate_not_allowed')
            return result(request, host, state, True)
        if state['record'] is not None and state['record']['status'] == 'authorized':
            require(request['release']['binary_sha256'] in state['hashes'], 'prepared_candidate_removed')
            return result(request, host, state, False)
        return prepare(request, config, host, state)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True, help='Absolute root-private deployment policy')
    args = parser.parse_args()
    try:
        require(hasattr(os, 'geteuid') and os.geteuid() == 0 and sys.platform.startswith('linux'), 'linux_root_required')
        require(absolute_path(args.config), 'invalid_config_path')
        parent = secure_directory(os.path.dirname(args.config))
        try:
            owned(os.fstat(parent), directory=True, mode=0o700)
            config = validate_config(strict_json(read_at(parent, os.path.basename(args.config), 0o600)))
        finally:
            os.close(parent)
        require(not args.config.startswith(config['working_dir'] + '/'), 'config_must_be_outside_application')
        raw = sys.stdin.buffer.read(65537)
        require(len(raw) <= 65536, 'request_too_large')
        request = validate_request(strict_json(raw))
        output = execute(request, config, LinuxHost(config))
        code = 0
    except Exception as error:
        output = {'schema_version': 1, 'status': 'rejected', 'allowed': False, 'healthy': False,
                  'epoch_unchanged': False, 'receipt_binding_verified': False, 'binary_replaced': False,
                  'restart_performed': False, 'ha_authority_grant': False,
                  'failure_code': str(error) if isinstance(error, Refused) else 'unexpected_guard_release_failure'}
        code = 1
    sys.stdout.write(encode(output).decode('ascii'))
    return code


if __name__ == '__main__':
    sys.exit(main())
