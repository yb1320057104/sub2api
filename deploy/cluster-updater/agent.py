"""Root-owned forced-SSH node RPC; Python 3.6+, one request per process.

Only stdin JSON is interpreted. SSH_ORIGINAL_COMMAND is never executed. Existing
gate, Docker candidate and graceful rollout implementations remain the authority.
This file contains no deployment addresses or credentials.
"""
import argparse
import ast
import contextlib
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import sys
import time
import uuid

sys.dont_write_bytecode = True
NODES = ('primary', 'secondary')
HASH = r'[0-9a-f]{64}'
MAX_BINARY = 500 * 1024 * 1024
PUBLIC_RECEIPT = ('name', 'id', 'node', 'storage', 'role', 'revision', 'port',
                  'source_id', 'binary_sha256', 'spec_sha256', 'started', 'version', 'baseline')
PACKAGE_FILES = ('candidate_support.py', 'primary-ha-api-receipt.json',
                 'secondary-ha-api-receipt.json', 'acceptance.json')


class Refused(RuntimeError):
    pass


def need(value, code):
    if not value:
        raise Refused(code)


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def encoded(value):
    return (json.dumps(value, sort_keys=True, separators=(',', ':')) + '\n').encode()


def secure(path, directory=False, private=True):
    path = Path(path)
    need(path.is_absolute() and all(not p.is_symlink() for p in [path] + list(path.parents)), 'unsafe_path')
    info = path.lstat()
    need((stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode)) and
         info.st_uid == 0 and not info.st_mode & (0o077 if private else 0o022), 'unsafe_file')
    return path


def read(path):
    path = secure(path)
    need(path.stat().st_size <= 4 * 1024 * 1024, 'metadata_too_large')
    return path.read_bytes()


def put(path, raw, replace=False, mode=0o600):
    path = Path(path)
    secure(path.parent, directory=True)
    need(not path.is_symlink(), 'unsafe_path')
    if path.exists():
        secure(path, private=(mode == 0o600))
        need(replace, 'output_exists')
    temporary = path.with_name('.' + path.name + '.' + uuid.uuid4().hex)
    fd = os.open(str(temporary), os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
    try:
        with os.fdopen(fd, 'wb') as stream:
            os.fchmod(stream.fileno(), mode)
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(str(temporary), str(path))
        fd = os.open(str(path.parent), os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        if temporary.exists():
            temporary.unlink()


def mkdir(path):
    path = Path(path)
    if not path.exists():
        path.mkdir(mode=0o700)
    secure(path, directory=True)
    return path


def load(path, label):
    secure(path)
    spec = importlib.util.spec_from_file_location(label + '_' + uuid.uuid4().hex, str(path))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def artifact(value):
    need(isinstance(value, dict) and set(value) == {'version', 'baseline', 'sha256', 'bytes', 'revision'}, 'artifact_shape')
    need(isinstance(value['version'], str) and re.fullmatch(r'[A-Za-z0-9._-]{1,100}', value['version']), 'artifact_version')
    need(isinstance(value['baseline'], str) and re.fullmatch(r'[A-Za-z0-9._-]{1,100}', value['baseline']), 'artifact_baseline')
    need(isinstance(value['sha256'], str) and re.fullmatch(HASH, value['sha256']), 'artifact_hash')
    need(type(value['bytes']) is int and 4096 <= value['bytes'] <= MAX_BINARY, 'artifact_size')
    need(type(value['revision']) is int and 2 <= value['revision'] < 1000000, 'artifact_revision')
    return value


def receipt(value, node=None, role=None):
    need(isinstance(value, dict) and set(value).issubset(set(PUBLIC_RECEIPT)), 'receipt_shape')
    need(value.get('node') in NODES and value.get('storage') == 'ha' and value.get('role') in ('api', 'worker'), 'receipt_role')
    need(node is None or value['node'] == node, 'receipt_node')
    need(role is None or value['role'] == role, 'receipt_role')
    n = value.get('revision')
    need(type(n) is int and 1 <= n < 1000000, 'receipt_revision')
    need(value.get('name') == 'sub2api-ha-{}-ha-{}-r{}'.format(value['node'], value['role'], n), 'receipt_name')
    for key in ('id', 'binary_sha256', 'spec_sha256', 'source_id'):
        need(isinstance(value.get(key), str) and re.fullmatch(HASH, value[key]), 'receipt_identity')
    need(value.get('port') == (18092 if value['role'] == 'api' else None), 'receipt_port')
    return value


def public_receipt(value):
    return {key: value[key] for key in PUBLIC_RECEIPT if key in value}


def historical_receipt(value):
    # The initial r1 format omitted revision. The ordinary receipt validator
    # still requires its exact r1 name and all pinned identity fields.
    normalized = dict(value)
    if 'revision' not in normalized:
        normalized['revision'] = 1
    return receipt(normalized)


def baseline(version):
    return version.split('-ha.', 1)[0]


def config(path):
    value = json.loads(read(path))
    required = {'node', 'root', 'gate_runtime_dir', 'worker_runtime_dir', 'edge_rollout_module', 'candidate_root'}
    need(required.issubset(value) and set(value).issubset(required | {'schema', 'worker_unit', 'github_token_file'}), 'config_shape')
    need(type(value.get('schema', 1)) is int and value.get('schema', 1) == 1, 'config_schema')
    need(value['node'] in NODES, 'config_node')
    for key in required - {'node'}:
        need(isinstance(value[key], str) and Path(value[key]).is_absolute() and '..' not in Path(value[key]).parts, 'config_path')
        secure(value[key], directory=(key != 'edge_rollout_module'))
    value.setdefault('worker_unit', 'sub2api-storage-worker-runtime.service')
    if value.get('github_token_file') is not None:
        need(isinstance(value['github_token_file'], str) and Path(value['github_token_file']).is_absolute(), 'github_token_file_path')
        secure(value['github_token_file'])
    need(re.fullmatch(r'[a-zA-Z0-9_.-]+\.service', value['worker_unit']), 'worker_unit')
    return value


def checked_command(argv, seconds=20):
    result = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=seconds)
    need(result.returncode == 0, 'command_failed')
    return result.stdout


class Agent:
    def __init__(self, settings):
        self.c = settings
        self.node = settings['node']
        self.root = Path(settings['root'])
        self.runtime = Path(settings['gate_runtime_dir'])
        self.worker_root = Path(settings['worker_runtime_dir'])
        self.candidates = Path(settings['candidate_root'])
        self.job = None

    def gate_runtime(self):
        return load(self.runtime / 'gate_runtime.py', 'gate_runtime')

    def rollout(self):
        module = load(Path(self.c['edge_rollout_module']), 'rollout')
        module.ROOT = self.job
        module.ACCEPTED = self.runtime
        return module

    def log(self, phase, **values):
        need(self.job is not None, 'job_required')
        target = self.job / 'events.jsonl'
        if target.exists():
            secure(target)
        fd = os.open(str(target), os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'ab') as stream:
            stream.write(encoded(dict({'phase': phase, 'time': int(time.time())}, **values)))
            stream.flush()
            os.fsync(stream.fileno())

    def current(self):
        rt = self.gate_runtime()
        accepted, receipts = rt.acceptance()
        need(accepted['node'] == self.node, 'accepted_node_changed')
        support, current, row = rt.local_candidate(accepted, receipts)
        receipt(current, self.node, 'api')
        return rt, support, accepted, receipts, current, row

    def worker(self):
        value = json.loads(read(self.worker_root / 'config.json'))
        need(value.get('node') == self.node, 'worker_node_changed')
        module = load(self.worker_root / 'supervisor.py', 'worker_supervisor')
        module.validate_config(value)
        state = module.Docker(value).inspect()
        item = receipt(json.loads(read(self.candidates / value['container_name'] / 'receipt.json')), self.node, 'worker')
        need(item['id'] == value['container_id'] and item['spec_sha256'] == value['spec_sha256'] and
             item['binary_sha256'] == value['binary_sha256'], 'worker_receipt_changed')
        return value, module, item, state

    def observe(self, request):
        rt, support, accepted, receipts, api, row = self.current()
        wc, wm, worker, ws = self.worker()
        local_writer = wm.WriterProbe(wc)()
        live = rt.control()
        marker, unused = rt.known_release(rt.persistent())
        released = not live.get('held') and all(live.get(k) == marker.get(k) for k in ('boot_id', 'generation', 'target'))
        version = api.get('version', support.VERSIONS.get(api['revision'], 'unknown'))
        worker_version = worker.get('version', support.VERSIONS.get(worker['revision'], 'unknown'))
        staged_revisions = [api['revision'], worker['revision']]
        for path in self.candidates.iterdir():
            match = re.fullmatch(r'sub2api-ha-' + self.node + r'-ha-(?:api|worker)-r([1-9][0-9]{0,5})', path.name)
            if match:
                secure(path, directory=True)
                staged_revisions.append(int(match.group(1)))
        return {'node': self.node, 'version': version, 'baseline': api.get('baseline', baseline(version)),
                'highest_staged_revision': max(staged_revisions),
                'revision': api['revision'], 'ready': bool(released and rt.ready(accepted, api, support)),
                'api_receipt': public_receipt(api), 'worker_receipt': public_receipt(worker),
                'worker_running': bool(ws.get('Running')), 'local_writer': local_writer, 'worker_version': worker_version,
                'worker_revision': worker['revision'], 'gate': {k: live.get(k) for k in
                 ('boot_id', 'generation', 'target', 'held', 'active', 'closing', 'persistence_error')},
                'acceptance_sha256': sha(read(self.runtime / 'acceptance.json'))}

    def preflight(self, request):
        result = {'node': self.node, 'can_update': False, 'read_only': True, 'checks': {}}
        try:
            observed = self.observe(request)
            result['observed'] = observed
            need(observed['ready'], 'current_api_unready')
            rt, support, accepted, receipts, current, row = self.current()
            wc, wm, worker, state = self.worker()
            need(wc.get('automatic_start_enabled') is True, 'worker_automatic_start_disabled')
            need(checked_command(['systemctl', 'is-active', self.c['worker_unit']], 10).decode().strip() == 'active',
                 'worker_supervisor_not_active')
            self.worker_service_contract()
            compile(self.generic_supervisor(read(self.worker_root / 'supervisor.py')), 'supervisor.py', 'exec')
            result['checks']['worker'] = True
            for candidate in (current, worker):
                root = self.candidates / candidate['name']
                spec = json.loads(read(root / 'container-spec.private.json'))
                need(sha(json.dumps(spec, sort_keys=True).encode()) == candidate['spec_sha256'], 'source_spec_changed')
                env = dict(s.split('=', 1) for s in spec.get('Env', []) if '=' in s)
                need(env.get('SUB2API_GRACEFUL_DRAIN') == 'true', 'graceful_drain_required')
                need(env.get('SUB2API_WORKER_ONLY') == ('true' if candidate['role'] == 'worker' else 'false') and
                     env.get('SUB2API_BACKGROUND_MODE') == ('active' if candidate['role'] == 'worker' else 'disabled'),
                     'source_role_changed')
                binds = spec.get('HostConfig', {}).get('Binds', [])
                need(sum(':/app/sub2api:' in s for s in binds) == 1 and sum(':/app/data:' in s for s in binds) == 1,
                     'source_mount_contract')
                plan = json.loads(read(root / 'network-plan.json'))
                for name, identity in plan['networks'].items():
                    found = support.api('GET', '/networks/' + name)
                    need(found.get('Id') == identity and found.get('Name') == name, 'source_network_changed')
            result['checks']['candidate_sources'] = True
            old = self.rollout()
            for name in ('edge_context', 'route', 'route_keys', 'drain', 'terminate', 'wait_ready', 'gate', 'same_gate'):
                need(callable(getattr(old, name, None)), 'rollout_interface_missing')
            edge, edge_support, edge_row = old.edge_context(self.node)
            stats = old.stats(edge, self.node, edge_row)
            for target in NODES:
                need(all(stats.get(key, {}).get('status') == 'UP' for key in old.route_keys(self.node, target)), 'edge_target_not_up')
            result['checks']['edge_routes'] = True
            need(all(callable(getattr(support, name, None)) for name in ('api', 'existing', 'network_plan', 'connect_extra_networks')),
                 'candidate_interface_missing')
            result['checks']['gate_acceptance'] = True
            result['can_update'] = True
        except Exception as error:
            result['code'] = error.args[0] if isinstance(error, Refused) else 'preflight_dependency_failed'
        return result

    def upload(self, request, stream):
        meta = artifact(request.get('artifact'))
        root = mkdir(self.root / 'artifacts')
        output = root / meta['sha256']
        pending = root / ('.upload-' + uuid.uuid4().hex)
        fd = os.open(str(pending), os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        digest = hashlib.sha256()
        remaining = meta['bytes']
        magic = b''
        try:
            with os.fdopen(fd, 'wb') as target:
                while remaining:
                    chunk = stream.read(min(1024 * 1024, remaining))
                    need(bool(chunk), 'upload_truncated')
                    if not magic:
                        magic = chunk[:4]
                    remaining -= len(chunk)
                    digest.update(chunk)
                    target.write(chunk)
                need(stream.read(1) == b'', 'upload_extra_bytes')
                need(magic == b'\x7fELF' and digest.hexdigest() == meta['sha256'], 'upload_digest_or_format')
                target.flush()
                os.fsync(target.fileno())
            if output.exists():
                secure(output, private=False)
                need(output.stat().st_size == meta['bytes'] and file_sha(output) == meta['sha256'], 'existing_artifact_changed')
            else:
                os.chmod(str(pending), 0o755)
                os.rename(str(pending), str(output))
            self.log('artifact_uploaded', sha256=meta['sha256'], bytes=meta['bytes'])
            return {'binary_path': str(output), 'sha256': meta['sha256'], 'bytes': meta['bytes']}
        finally:
            if pending.exists():
                pending.unlink()

    def generic_support(self, source, versions, identities):
        # Existing validators are reused; the pinned source is per receipt rather
        # than one global source container which may since have been retired.
        suffix = '''\n# Generic receipt validation installed by the cluster updater.\nROOT=Path({root!r})\nVERSIONS.update({versions!r})\n_RECEIPT_SOURCES={identities!r}\n_legacy_existing=existing\ndef existing(node,storage,role,revision=1):\n    name=candidate_name(node,storage,role,revision)\n    need(name in _RECEIPT_SOURCES,'unpinned_receipt_source')\n    SOURCES[node]=_RECEIPT_SOURCES[name]\n    return _legacy_existing(node,storage,role,revision)\n'''.format(root=str(self.candidates), versions=versions, identities=identities)
        # Do not accumulate wrappers across releases (their globals would alias).
        text = source.decode().split('\n# Generic receipt validation installed by the cluster updater.\n', 1)[0]
        return (text + suffix).encode()

    def fill_release_credential(self, spec):
        path = self.c.get('github_token_file')
        if not path or any(entry.startswith('UPDATE_GITHUB_TOKEN=') and entry.split('=', 1)[1] for entry in spec['Env']):
            return
        raw = read(Path(path))
        need(len(raw) <= 16384, 'github_token_too_large')
        token = raw.decode('ascii').strip()
        need(bool(re.fullmatch(r'[A-Za-z0-9_]+', token)), 'github_token_invalid')
        spec['Env'] = [entry for entry in spec['Env'] if not entry.startswith('UPDATE_GITHUB_TOKEN=')]
        spec['Env'].append('UPDATE_GITHUB_TOKEN=' + token)

    def clone(self, support, previous, row, binary, meta):
        role = previous['role']
        source = self.candidates / previous['name']
        oldspec = json.loads(read(source / 'container-spec.private.json'))
        need(sha(json.dumps(oldspec, sort_keys=True).encode()) == previous['spec_sha256'], 'source_spec_changed')
        need(row['Id'] == previous['id'], 'source_container_changed')
        network = json.loads(read(source / 'network-plan.json'))
        primary, networks = network['primary'], network['networks']
        need(primary in networks, 'source_network_changed')
        for name, identifier in networks.items():
            observed = support.api('GET', '/networks/' + name)
            need(observed.get('Id') == identifier and observed.get('Name') == name, 'source_network_changed')
        # Live endpoints get the same network_plan validation as original prepare;
        # stopped workers legitimately have empty Docker endpoint identifiers.
        if row['State']['Running']:
            need(support.network_plan(row) == (primary, networks), 'source_network_changed')
        spec = copy.deepcopy(oldspec)
        binds = spec['HostConfig']['Binds']
        replaced = 0
        for i, binding in enumerate(binds):
            if ':/app/sub2api:' in binding:
                suffix = binding.split(':/app/sub2api:', 1)[1]
                binds[i] = str(binary) + ':/app/sub2api:' + suffix
                replaced += 1
        need(replaced == 1 and any(':/app/data:' in x for x in binds), 'source_mount_contract')
        # Data/config/pages/plugins/TLS binds remain live shared paths. An optional
        # private source may fill a missing API release credential only.
        if role == 'api':
            self.fill_release_credential(spec)
        spec['Labels']['sub2api.storage-ha.binary-sha256'] = meta['sha256']
        name = 'sub2api-ha-{}-ha-{}-r{}'.format(self.node, role, meta['revision'])
        output = self.candidates / name
        need(not output.exists(), 'candidate_exists')
        mkdir(output)
        put(output / 'container-spec.private.json', encoded(spec))
        put(output / 'network-plan.json', encoded({'primary': primary, 'networks': networks}))
        self.log('candidate_create_intent', name=name, source_id=previous['id'])
        created = support.api('POST', '/containers/create?name=' + name, spec)
        need(isinstance(created, dict) and re.fullmatch(HASH, created.get('Id', '')), 'candidate_create_failed')
        support.connect_extra_networks(created['Id'], primary, networks)
        result = {'name': name, 'id': created['Id'], 'node': self.node, 'storage': 'ha', 'role': role,
                  'revision': meta['revision'], 'port': 18092 if role == 'api' else None,
                  'source_id': previous['id'], 'binary_sha256': meta['sha256'],
                  'spec_sha256': sha(json.dumps(spec, sort_keys=True).encode()), 'started': False,
                  'version': meta['version'], 'baseline': meta['baseline']}
        put(output / 'receipt.json', encoded(result))
        now = support.api('GET', '/containers/' + result['id'] + '/json')
        need(not now['State']['Running'], 'candidate_unexpectedly_running')
        self.log('candidate_created', name=name, id=result['id'])
        return result

    def stage(self, request):
        meta = artifact(request.get('artifact'))
        binary = self.root / 'artifacts' / meta['sha256']
        need(request.get('binary_path') == str(binary), 'artifact_path')
        secure(binary, private=False)
        need(binary.stat().st_size == meta['bytes'] and binary.stat().st_mode & 0o005 == 0o005 and
             file_sha(binary) == meta['sha256'], 'artifact_changed')
        version_process = subprocess.run([str(binary), '--version'], stdin=subprocess.DEVNULL,
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
        need(version_process.returncode == 0 and len(version_process.stdout) + len(version_process.stderr) <= 16384,
             'binary_version_command_failed')
        output = (version_process.stdout + version_process.stderr).decode('utf8', 'replace')
        need(re.search(r'(?<![A-Za-z0-9._-])' + re.escape(meta['version']) + r'(?![A-Za-z0-9._-])', output), 'binary_version_mismatch')
        rt, support, accepted, receipts, oldapi, apirow = self.current()
        wc, wm, oldworker, workerstate = self.worker()
        need(apirow['State']['Running'] and rt.ready(accepted, oldapi, support), 'current_api_unready')
        need(meta['revision'] > max(oldapi['revision'], oldworker['revision']), 'revision_not_new')
        live = rt.control()
        need(live['held'] is False and not live.get('closing') and not live.get('persistence_error'), 'gate_not_released')
        put(self.job / 'old-candidate_support.py', read(self.runtime / 'candidate_support.py'))
        put(self.job / 'old-worker-config.json', read(self.worker_root / 'config.json'))
        put(self.job / 'old-supervisor.py', read(self.worker_root / 'supervisor.py'))
        workerrow = support.api('GET', '/containers/' + oldworker['id'] + '/json')
        newapi = self.clone(support, oldapi, apirow, binary, meta)
        newworker = self.clone(support, oldworker, workerrow, binary, meta)
        versions = dict(support.VERSIONS)
        versions[meta['revision']] = meta['version']
        identities = {}
        for path in self.candidates.glob('sub2api-ha-*-ha-*-r*/receipt.json'):
            candidate = historical_receipt(json.loads(read(path)))
            identities[candidate['name']] = candidate['source_id']
            if candidate.get('version'):
                versions[candidate['revision']] = candidate['version']
        generic = self.generic_support(read(self.runtime / 'candidate_support.py'), versions, identities)
        put(self.job / 'candidate_support.py', generic)
        module = load(self.job / 'candidate_support.py', 'new_candidates')
        for item in (newapi, newworker):
            observed, row = module.existing(self.node, 'ha', item['role'], item['revision'])
            need(observed == item and not row['State']['Running'], 'staged_candidate_changed')
        result = {'node': self.node, 'artifact': meta, 'api_receipt': newapi, 'worker_receipt': newworker,
                  'old_api_receipt': oldapi, 'old_worker_receipt': oldworker, 'gate': live,
                  'acceptance_sha256': sha(read(self.runtime / 'acceptance.json')),
                  'support_sha256': sha(generic), 'version': meta['version'], 'baseline': meta['baseline'],
                  'revision': meta['revision'], 'production_changed': False}
        put(self.job / 'staged.json', encoded(result))
        return result

    def route(self, request):
        target = request.get('target')
        state = request.get('state')
        need(target in NODES and state in ('drain', 'ready'), 'route_arguments')
        old = self.rollout()
        old.route(self.node, target, 'drain' if state == 'drain' else 'restore')
        journal = json.loads(read(self.job / ('edge-' + target + '.json')))
        return {'node': self.node, 'target': target, 'state': state, 'job_id': self.job.name,
                'edge_id': journal['edge_id'], 'config_sha256': journal['config_sha256']}

    def staged(self):
        value = json.loads(read(self.job / 'staged.json'))
        need(value['node'] == self.node, 'staged_node')
        artifact(value['artifact'])
        need(sha(read(self.job / 'candidate_support.py')) == value['support_sha256'], 'staged_support_changed')
        return value, load(self.job / 'candidate_support.py', 'staged_candidates')

    def validate_drains(self, values):
        need(isinstance(values, list) and len(values) == 2, 'both_drains_required')
        need({v.get('node') for v in values if isinstance(v, dict)} == set(NODES), 'both_drains_required')
        for value in values:
            need(value.get('job_id') == self.job.name and value.get('target') == self.node and
                 value.get('state') == 'drain' and re.fullmatch(HASH, value.get('edge_id', '')) and
                 re.fullmatch(HASH, value.get('config_sha256', '')), 'drain_evidence_invalid')
        old = self.rollout()
        e, support, row = old.edge_context(self.node)
        states = old.stats(e, self.node, row)
        evidence = next(v for v in values if v['node'] == self.node)
        need(row['Id'] == evidence['edge_id'] and sha(e.PATHS[self.node].read_bytes()) == evidence['config_sha256'] and
             all(states[k]['status'].startswith('DRAIN') for k in old.route_keys(self.node, self.node)), 'local_routes_not_drained')

    def package(self, rt, staged, peer):
        backup = mkdir(self.job / 'accepted-before')
        for name in PACKAGE_FILES:
            put(backup / name, read(self.runtime / name))
        local = staged['api_receipt']
        receipts = {self.node: local, peer['node']: peer}
        raw_receipts = {node: encoded(item) for node, item in receipts.items()}
        accepted = json.loads(read(backup / 'acceptance.json'))
        accepted['cutover_id'] = 'cluster-update-' + self.job.name
        accepted['receipts'] = {node: {'sha256': sha(raw)} for node, raw in raw_receipts.items()}
        accepted['code_sha256']['candidate_support.py'] = staged['support_sha256']
        rt.validate_acceptance(accepted, raw_receipts)
        target = {'candidate_support.py': read(self.job / 'candidate_support.py'), 'acceptance.json': encoded(accepted)}
        target.update({node + '-ha-api-receipt.json': raw for node, raw in raw_receipts.items()})
        return target

    def install_package(self, rt, old, owned, target):
        for name in PACKAGE_FILES:
            old.same_gate(old.gate(), owned, True)
            put(self.runtime / name, target[name], replace=True)
        rt.acceptance()

    def mark_release(self, rt, old, owned):
        old.same_gate(old.gate(), owned, True)
        current = old.gate('POST', '/release', {'boot_id': owned['boot_id'], 'generation': owned['generation'], 'target': owned['target']})
        need(current.get('held') is False and current.get('boot_id') == owned['boot_id'] and
             current.get('generation') == owned['generation'] + 1 and current.get('target') == owned['target'], 'release_identity_changed')
        rt.write_private(rt.RELEASED, rt.released_marker(current, sha(read(self.runtime / 'acceptance.json'))))
        rt.known_release(rt.persistent())
        return current

    def rollback_api(self, rt, old, support, staged, owned):
        # Only a hold acquired by this exact action can authorize recovery.
        old.same_gate(old.gate(), owned, True)
        newreceipt, newrow = support.existing(self.node, 'ha', 'api', staged['revision'])
        if newrow['State']['Running']:
            r, row = old.drain(self.node, support, newreceipt['revision'], owned)
            old.terminate(self.node, support, r, row, owned)
        previous = staged['old_api_receipt']
        r, row = support.existing(self.node, 'ha', 'api', previous['revision'])
        if not row['State']['Running']:
            need(row['State']['ExitCode'] == 0, 'old_api_unclean_exit')
            support.api('POST', '/containers/' + r['id'] + '/start')
        old.wait_ready(self.node, support, r)
        backup = self.job / 'accepted-before'
        for name in PACKAGE_FILES:
            old.same_gate(old.gate(), owned, True)
            put(self.runtime / name, read(backup / name), replace=True)
        rt.acceptance()
        live = self.mark_release(rt, old, owned)
        self.log('api_rollback_complete', generation=live['generation'])
        return live

    def replace_api(self, request):
        staged, support = self.staged()
        peer_node = next(n for n in NODES if n != self.node)
        peer = receipt(request.get('peer_receipt'), peer_node, 'api')
        need(peer['revision'] == staged['revision'] and peer['binary_sha256'] == staged['artifact']['sha256'], 'peer_artifact_mismatch')
        self.validate_drains(request.get('drains'))
        rt, accepted_support, accepted, receipts, current, row = self.current()
        need(current == staged['old_api_receipt'] and row['State']['Running'] and
             sha(read(self.runtime / 'acceptance.json')) == staged['acceptance_sha256'], 'accepted_api_changed')
        old = self.rollout()
        old.same_gate(old.gate(), staged['gate'], False)
        target = self.package(rt, staged, peer)
        owned = None
        try:
            before = old.gate()
            old.same_gate(before, staged['gate'], False)
            held = old.gate('POST', '/hold', {'boot_id': before['boot_id'], 'generation': before['generation']})
            need(held.get('held') is True and held.get('boot_id') == before['boot_id'] and
                 held.get('generation') == before['generation'] + 1 and held.get('target') == before['target'], 'hold_identity_changed')
            owned = held
            put(self.job / 'owned-hold.json', encoded(owned))
            self.log('api_held', generation=owned['generation'])
            previous, oldrow = old.drain(self.node, support, current['revision'], owned, seconds=900)
            old.terminate(self.node, support, previous, oldrow, owned)
            self.log('old_api_stopped', id=previous['id'])
            old.same_gate(old.gate(), owned, True)
            new, newrow = support.existing(self.node, 'ha', 'api', staged['revision'])
            need(not newrow['State']['Running'], 'new_api_already_running')
            support.api('POST', '/containers/' + new['id'] + '/start')
            old.wait_ready(self.node, support, new)
            self.install_package(rt, old, owned, target)
            next_accepted, local_receipts = rt.acceptance()
            rt.local_candidate(next_accepted, local_receipts)
            live = self.mark_release(rt, old, owned)
            owned = None
            self.log('api_replaced', revision=new['revision'], generation=live['generation'])
            return {'node': self.node, 'api_receipt': new, 'version': staged['version'], 'baseline': staged['baseline'], 'gate': live}
        except Exception:
            if owned is not None:
                try:
                    self.rollback_api(rt, old, support, staged, owned)
                except Exception:
                    self.log('api_rollback_pending', requires_operator=True)
                    raise Refused('api_failed_rollback_pending')
                raise Refused('api_failed_rolled_back')
            raise

    def generic_supervisor(self, raw):
        text = raw.decode()
        anchor = "config['revision'] in (1, 2, 3)"
        if anchor in text:
            need(text.count(anchor) == 1, 'supervisor_revision_anchor')
            text = text.replace(anchor, "config['revision'] >= 1")
        else:
            need("config['revision'] >= 1" in text, 'supervisor_revision_anchor')
        # Existing implementation uses path literals; derive the old artifact
        # check from its source without introducing a deployment address.
        old_artifacts = re.findall(r"binary\.parent == Path\(('([^']+/artifacts)')\)", text)
        if len(old_artifacts) == 1:
            old_expression = 'binary.parent == Path(' + old_artifacts[0][0] + ')'
            paths = [old_artifacts[0][1]]
        else:
            matches = re.findall(r'str\(binary.parent\) in (\([^\n]+\))\n', text)
            need(len(matches) == 1, 'supervisor_artifact_anchor')
            paths = ast.literal_eval(matches[0])
            need(isinstance(paths, tuple) and 1 <= len(paths) <= 2 and all(isinstance(p, str) and
                 PurePosixPath(p).is_absolute() and p.endswith('/artifacts') for p in paths), 'supervisor_artifact_paths')
            old_expression = 'str(binary.parent) in ' + matches[0]
        allowed = sorted(set(list(paths) + [str(self.root / 'artifacts')]))
        text = text.replace(old_expression, 'str(binary.parent) in ' + repr(tuple(allowed)), 1)
        # Repeated updates already have an allowlist; support that without widening
        # access beyond the previously pinned path and configured artifact store.
        compile(text, 'supervisor.py', 'exec')
        return text.encode()

    def worker_unit(self, action):
        return checked_command(['systemctl', action, self.c['worker_unit']], 100)

    def worker_service_contract(self):
        raw = checked_command(['systemctl', 'show', self.c['worker_unit'],
                               '--property=KillMode,KillSignal,SendSIGKILL'], 10).decode()
        values = dict(line.split('=', 1) for line in raw.splitlines() if '=' in line)
        need(values.get('KillMode') == 'process' and values.get('KillSignal') in ('15', 'SIGTERM') and
             values.get('SendSIGKILL') == 'no', 'worker_service_could_force_kill')

    def worker_stopped(self, module, value, seconds=100):
        until = time.monotonic() + seconds
        while True:
            state = module.Docker(value).inspect()
            if not state.get('Running'):
                need(state.get('ExitCode') == 0, 'worker_unclean_exit')
                return
            need(time.monotonic() < until, 'worker_shutdown_pending')
            time.sleep(.5)

    def verify_worker_role(self, module, value, seconds=25):
        until = time.monotonic() + seconds
        probe = module.WriterProbe(value)
        stable = 0
        while time.monotonic() < until:
            writer = probe()
            state = module.Docker(value).inspect()
            after = probe()
            stable = stable + 1 if writer == after and bool(state.get('Running')) == writer else 0
            if stable >= 3:
                return {'running': bool(state.get('Running')), 'local_writer': writer}
            time.sleep(2)
        raise Refused('worker_role_verification_timeout')

    def update_worker(self, request):
        staged, support = self.staged()
        rt, accepted_support, accepted, receipts, current, row = self.current()
        need(current['id'] == staged['api_receipt']['id'] and rt.ready(accepted, current, accepted_support), 'new_api_required')
        wc, wm, previous, state = self.worker()
        need(previous == staged['old_worker_receipt'] and read(self.worker_root / 'config.json') == read(self.job / 'old-worker-config.json') and
             read(self.worker_root / 'supervisor.py') == read(self.job / 'old-supervisor.py'), 'worker_changed_since_stage')
        new = staged['worker_receipt']
        newconfig = copy.deepcopy(wc)
        specpath = self.candidates / new['name'] / 'container-spec.private.json'
        spec = json.loads(read(specpath))
        newconfig.update({'revision': new['revision'], 'container_name': new['name'], 'container_id': new['id'],
                          'image_id': spec['Image'], 'spec_file': str(specpath), 'spec_sha256': new['spec_sha256'],
                          'binary_sha256': new['binary_sha256']})
        code = self.generic_supervisor(read(self.worker_root / 'supervisor.py'))
        put(self.job / 'next-supervisor.py', code)
        module = load(self.job / 'next-supervisor.py', 'next_worker')
        module.validate_config(newconfig)
        need(not module.Docker(newconfig).inspect().get('Running'), 'new_worker_already_running')
        active = checked_command(['systemctl', 'is-active', self.c['worker_unit']], 10).decode().strip()
        need(active == 'active', 'worker_supervisor_not_active')
        self.worker_service_contract()
        stopped = False
        changed = False
        try:
            self.log('worker_stop_intent', id=previous['id'])
            self.worker_unit('stop')
            self.worker_stopped(wm, wc)
            stopped = True
            self.log('worker_stopped', id=previous['id'])
            put(self.worker_root / 'supervisor.py', code, replace=True)
            changed = True
            put(self.worker_root / 'config.json', encoded(newconfig), replace=True)
            self.worker_unit('start')
            role = self.verify_worker_role(module, newconfig)
            self.log('worker_updated', revision=new['revision'])
            return dict({'node': self.node, 'worker_receipt': new, 'version': staged['version']}, **role)
        except Exception:
            if changed:
                try:
                    self.worker_unit('stop')
                    self.worker_stopped(module, newconfig)
                    put(self.worker_root / 'supervisor.py', read(self.job / 'old-supervisor.py'), replace=True)
                    put(self.worker_root / 'config.json', read(self.job / 'old-worker-config.json'), replace=True)
                    self.worker_unit('start')
                    self.verify_worker_role(wm, wc)
                    self.log('worker_rollback_complete')
                except Exception:
                    self.log('worker_rollback_pending', requires_operator=True)
                    raise Refused('worker_failed_rollback_pending')
                raise Refused('worker_failed_rolled_back')
            if stopped:
                self.worker_unit('start')
            raise

    def dispatch(self, request, stream=None):
        need(isinstance(request, dict), 'request_shape')
        action = request.get('action')
        need(action in ('observe', 'preflight', 'upload', 'stage', 'route', 'replace_api', 'update_worker'), 'unknown_action')
        if action in ('observe', 'preflight'):
            return getattr(self, action)(request)
        job_id = request.get('job_id')
        need(isinstance(job_id, str) and re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_-]{0,63}', job_id), 'job_id')
        jobs = mkdir(self.root / 'jobs')
        self.job = mkdir(jobs / job_id)
        suffix = '-' + str(request.get('target')) + '-' + str(request.get('state')) if action == 'route' else ''
        need(re.fullmatch(r'[a-z_-]{0,64}', suffix), 'action_key')
        journal = self.job / ('action-' + action + suffix + '.json')
        need(not journal.exists(), 'action_replay_refused')
        put(journal, encoded({'action': action, 'status': 'started', 'at': int(time.time())}))
        try:
            result = self.upload(request, stream) if action == 'upload' else getattr(self, action)(request)
            put(journal, encoded({'action': action, 'status': 'complete', 'result': result}), replace=True)
            return result
        except Exception as error:
            code = error.args[0] if isinstance(error, Refused) else 'operation_failed'
            put(journal, encoded({'action': action, 'status': 'failed', 'code': code}), replace=True)
            raise


def file_sha(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--config', required=True)
    args = parser.parse_args()
    need(os.geteuid() == 0, 'root_required')
    settings = config(args.config)
    # Read exactly one header. upload alone consumes the following binary body.
    raw = sys.stdin.buffer.readline(65537)
    need(len(raw) <= 65536 and raw.endswith(b'\n'), 'request_header')
    request = json.loads(raw)
    need(isinstance(request, dict), 'request_shape')
    import fcntl
    root = secure(settings['root'], directory=True)
    fd = os.open(str(root), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        fcntl.flock(fd, (fcntl.LOCK_SH if request.get('action') in ('observe', 'preflight') else fcntl.LOCK_EX) | fcntl.LOCK_NB)
        # Imported old modules print progress. Keep RPC stdout strictly one JSON,
        # and discard those outputs rather than risk exposing Docker env/spec.
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            result = Agent(settings).dispatch(request, sys.stdin.buffer)
        print(json.dumps({'ok': True, 'result': result}, sort_keys=True))
    finally:
        os.close(fd)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        code = error.args[0] if isinstance(error, Refused) and re.fullmatch(r'[a-z][a-z0-9_]{0,79}', str(error.args[0])) else 'operation_failed'
        print(json.dumps({'ok': False, 'code': code}, sort_keys=True))
        sys.exit(1)
