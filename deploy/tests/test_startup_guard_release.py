"""Host transaction fixtures; no Docker, production, network, or process signals."""
import ast
import copy
import importlib.util
import os
from pathlib import Path
import unittest
from contextlib import contextmanager
from unittest import mock

SCRIPT = Path(__file__).resolve().parents[1] / 'startup-guard-release.py'
SPEC = importlib.util.spec_from_file_location('startup_guard_release', str(SCRIPT))
guard = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(guard)

OLD = '1' * 64
NEW = '2' * 64
SOURCE = (b'#!/bin/sh\n# reviewed fixture\ncase "$guard_hash" in\n    ' + OLD.encode() +
          b') ;;\n    *) guard_fail ;;\nesac\nexec /proc/self/fd/9 "$@"\n')


def fixture():
    config = {'schema_version': 1, 'target': guard.TARGET, 'repository': guard.REPOSITORY,
              'container_name': 'sub2api-team', 'container_id': 'c' * 64, 'project': 'sub2api-team',
              'service': 'sub2api', 'working_dir': '/data/sub2api',
              'guard_path': '/data/sub2api/ha-startup-guard/entrypoint.sh',
              'receipt_path': '/data/sub2api/ha-startup-guard/receipt.json',
              'journal_path': '/data/sub2api/ha-startup-guard/install-state.json',
              'lock_path': '/data/sub2api/.ha-startup-guard-install.lock',
              'backup_dir': '/data/sub2api/ha-startup-guard/releases',
              'guard_scaffold_sha256': guard.guard_parts(SOURCE)[1]}
    request = {'schema_version': 1, 'operation': 'prepare', 'target': guard.TARGET,
               'release': {'repository': guard.REPOSITORY, 'version': '0.2.19-pk.57',
                           'commit': 'a' * 40, 'tree': 'b' * 40, 'package_id': '123-' + 'a' * 12,
                           'binary_sha256': NEW, 'manifest_sha256': 'd' * 64}}
    container = {'Id': config['container_id'], 'State': {'Running': True, 'Paused': False,
                 'Restarting': False, 'Health': 'healthy', 'StartedAt': '2026-11-08T00:00:00Z', 'Pid': 100},
                 'RestartCount': 0, 'Image': 'sha256:' + 'e' * 64, 'Entrypoint': [guard.ENTRYPOINT],
                 'Path': guard.ENTRYPOINT, 'Cmd': ['/app/sub2api'], 'Args': ['/app/sub2api'],
                 'Init': True, 'User': '', 'Project': config['project'], 'Service': config['service'],
                 'WorkingDir': config['working_dir'], 'ConfigFiles': '/data/sub2api/compose.yml',
                 'RestartPolicy': {'Name': 'unless-stopped', 'MaximumRetryCount': 0},
                 'Mounts': [{'Type': 'bind', 'Source': '/data/sub2api/data', 'Destination': '/app/data',
                             'RW': True, 'Propagation': 'rprivate'}]}
    epoch = {'container': container, 'init': {'pid': 100, 'start_ticks': 10, 'parent_pid': 1},
             'app': {'pid': 101, 'start_ticks': 12, 'parent_pid': 100, 'exe_sha256': OLD,
                     'exe_device': 20, 'exe_inode': 30}, 'boot_id': 'fixture-boot'}
    receipt = {'schema_version': 1, 'container': {'name': config['container_name'], 'id': config['container_id']},
               'original_epoch': {'started_at': '2026-11-06T00:00:00Z', 'pid': 50, 'start_ticks': 5,
                                  'exe_sha256': OLD, 'init_pid': 49, 'init_start_ticks': 4,
                                  'restart_count': 0, 'boot_id': '12345678-1234-1234-1234-123456789abc'},
               'installed': {'entrypoint_path': guard.ENTRYPOINT, 'entrypoint_sha256': guard.sha(SOURCE),
                             'host_guard_path': config['guard_path'], 'host_guard_sha256': guard.sha(SOURCE), 'assets': []},
               'compose': {'files': [{'path': '/data/sub2api/compose.yml', 'sha256': 'f' * 64}],
                           'project': config['project'], 'service': config['service'],
                           'working_dir': config['working_dir'], 'normalized_sha256': '9' * 64},
               'allowed_executable_sha256': [OLD], 'expected': {'entrypoint': [guard.ENTRYPOINT],
               'cmd': ['/app/sub2api'], 'restart_policy': container['RestartPolicy'],
               'legacy_mounts': container['Mounts'], 'recreated_mounts': container['Mounts'] + [
                   {'Type': 'bind', 'Source': config['guard_path'], 'Destination': guard.ENTRYPOINT,
                    'RW': False, 'Propagation': 'rprivate'}]}, 'installed_at': '2026-11-06T00:00:00Z'}
    receipt_raw = guard.encode(receipt)
    journal = {'schema_version': 1, 'status': 'complete', 'phase': 'complete',
               'container_id': config['container_id'], 'receipt_sha256': guard.sha(receipt_raw)}
    files = {'guard': SOURCE, 'container_guard': SOURCE, 'receipt': receipt_raw, 'journal': guard.encode(journal)}
    return config, request, epoch, files


class FakeHost(object):
    def __init__(self):
        self.config, self.request, self.epoch, self.files = fixture()
        self.original = copy.deepcopy(self.files)
        self.records = {}
        self.writes = []
        self.backups = []
        self.fail_key = None
        self.fail_occurrence = 1
        self.write_counts = {}
        self.fail_after = False
        self.external_drift = False
        self.epoch_drift = False
        self.snapshot_calls = 0
        self.locked = False
        self.compose_ok = True
        self.guardian = False

    def snapshot(self):
        self.snapshot_calls += 1
        guard.require(not self.guardian, 'guardian_or_fence_not_inactive')
        guard.require(self.epoch['container']['State']['Health'] == 'healthy', 'container_not_healthy')
        return copy.deepcopy(self.epoch)

    def read(self, key, snapshot):
        return self.files[key]

    def compose_matches(self, receipt):
        return self.compose_ok

    def write(self, key, raw, expected, snapshot):
        assert self.locked
        guard.require(guard.sha(self.files[key]) == expected, 'destination_compare_failed')
        self.writes.append((key, raw))
        self.write_counts[key] = self.write_counts.get(key, 0) + 1
        if key == self.fail_key and self.write_counts[key] == self.fail_occurrence:
            self.fail_key = None
            if self.fail_after:
                self.files[key] = raw
            if self.external_drift:
                self.files['guard'] = b'externally changed bytes'
            if self.epoch_drift:
                self.epoch['container']['RestartCount'] += 1
            raise OSError('injected failure with sensitive details never serialized')
        self.files[key] = raw

    def record_path(self, request):
        return self.config['backup_dir'] + '/' + request['release']['version'] + '.json'

    def read_record(self, request):
        return self.records.get(self.record_path(request))

    def write_path(self, path, raw, expected, mode=0o600):
        assert self.locked
        current = self.records.get(path)
        guard.require((current is None if expected is None else current is not None and guard.sha(current) == expected),
                      'destination_compare_failed')
        self.writes.append(('record', raw))
        self.write_counts['record'] = self.write_counts.get('record', 0) + 1
        if self.fail_key == 'record' and self.write_counts['record'] == self.fail_occurrence:
            self.fail_key = None
            if self.fail_after:
                self.records[path] = raw
            raise OSError('injected release receipt failure')
        self.records[path] = raw

    def backup(self, request, baseline):
        assert self.locked
        self.backups.append(copy.deepcopy(baseline))
        return self.config['backup_dir'] + '/attempt-' + str(len(self.backups))

    def failure(self, path, code, restored):
        self.records[path + '/failure.json'] = guard.encode({'failure_code': code, 'restored': restored})

    @contextmanager
    def lock(self, write=False):
        assert not self.locked
        self.locked = True
        try:
            yield
        finally:
            self.locked = False

    def execute(self, operation='prepare'):
        self.request['operation'] = operation
        return guard.execute(self.request, self.config, self)


class ProtocolTests(unittest.TestCase):
    def test_python36_syntax(self):
        ast.parse(SCRIPT.read_text(encoding='utf-8'), feature_version=(3, 6))

    def test_duplicate_and_nonfinite_json_rejected(self):
        for raw in (b'{"a":1,"a":2}', b'{"a":NaN}', b'{"a":Infinity}', b'\xff'):
            with self.subTest(raw=raw), self.assertRaises(guard.Refused):
                guard.strict_json(raw)

    def test_release_schema_pins_every_artifact_field(self):
        config, request, unused, unused2 = fixture()
        self.assertEqual(guard.validate_request(request), request)
        self.assertEqual(guard.validate_config(config), config)
        changes = {'repository': 'other/sub2api', 'version': '0.2.19-pk.56', 'commit': 'bad',
                   'tree': 'f' * 41, 'package_id': '123-ffffffffffff', 'binary_sha256': 'F' * 64,
                   'manifest_sha256': 'z' * 64}
        for key, value in changes.items():
            candidate = copy.deepcopy(request)
            candidate['release'][key] = value
            with self.subTest(key=key), self.assertRaises(guard.Refused):
                guard.validate_request(candidate)
        request['path'] = '/tmp/anything'
        with self.assertRaisesRegex(guard.Refused, 'invalid_request_schema'):
            guard.validate_request(request)

    def test_config_cannot_supply_other_paths(self):
        for key in ('guard_path', 'receipt_path', 'journal_path', 'lock_path', 'backup_dir'):
            config = fixture()[0]
            config[key] = '/root/other'
            with self.subTest(key=key), self.assertRaises(guard.Refused):
                guard.validate_config(config)

    def test_guard_normalization_only_removes_hash_payload(self):
        altered = guard.append_hash(SOURCE, NEW)
        self.assertEqual(guard.guard_parts(SOURCE)[1], guard.guard_parts(altered)[1])
        self.assertEqual(guard.guard_parts(altered)[0], [OLD, NEW])
        self.assertEqual(guard.append_hash(altered, NEW), altered)
        self.assertNotEqual(guard.guard_parts(SOURCE.replace(b'reviewed fixture', b'changed code'))[1],
                            guard.guard_parts(SOURCE)[1])
        for invalid in (SOURCE + SOURCE, SOURCE.replace(OLD.encode(), (OLD + '|' + OLD).encode()),
                        SOURCE.replace(b') ;;', b') echo allowed ;;', 1)):
            with self.assertRaises(guard.Refused):
                guard.guard_parts(invalid)

    def test_no_active_process_commands(self):
        host = guard.LinuxHost(fixture()[0])
        for args in ([guard.DOCKER, 'restart', 'id'], [guard.DOCKER, 'cp', 'x', 'y'],
                     [guard.DOCKER, 'exec', 'id', 'true'], [guard.SYSTEMCTL, 'start', 'unit'],
                     ['/bin/sh', '-c', 'true'], ['/bin/kill', '-9', '123']):
            with self.subTest(args=args), mock.patch.object(guard.subprocess, 'run') as run:
                with self.assertRaisesRegex(guard.Refused, 'forbidden_command'):
                    host.run(args)
                run.assert_not_called()
        tree = ast.parse(SCRIPT.read_text(encoding='utf-8'))
        self.assertFalse(any(isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
                             and node.func.attr in ('kill', 'killpg', 'system', 'popen') for node in ast.walk(tree)))


class TransactionTests(unittest.TestCase):
    def test_plan_reads_without_creating_backups_or_receipts(self):
        host = FakeHost()
        result = host.execute('plan')
        self.assertEqual(result['status'], 'ready')
        self.assertFalse(result['allowed'])
        self.assertTrue(result['read_only'])
        self.assertEqual(host.writes, [])
        self.assertEqual(host.backups, [])
        self.assertEqual(host.files, host.original)

    def test_check_before_prepare_is_readonly_refusal(self):
        host = FakeHost()
        with self.assertRaisesRegex(guard.Refused, 'release_not_prepared'):
            host.execute('check')
        self.assertEqual(host.writes, [])

    def test_prepare_then_check_and_idempotent_prepare(self):
        host = FakeHost()
        result = host.execute()
        self.assertEqual(result['status'], 'authorized')
        for key in ('allowed', 'healthy', 'epoch_unchanged', 'receipt_binding_verified'):
            self.assertIs(result[key], True)
        for key in ('restart_performed', 'binary_replaced', 'ha_authority_grant'):
            self.assertIs(result[key], False)
        self.assertEqual(result['release'], host.request['release'])
        self.assertEqual(host.files['guard'], host.files['container_guard'])
        self.assertEqual(guard.guard_parts(host.files['guard'])[0], [OLD, NEW])
        receipt = guard.strict_json(host.files['receipt'])
        original_receipt = guard.strict_json(host.original['receipt'])
        for key in ('container', 'original_epoch', 'compose', 'expected', 'installed_at'):
            self.assertEqual(receipt[key], original_receipt[key])
        self.assertEqual(guard.strict_json(host.files['journal'])['receipt_sha256'], guard.sha(host.files['receipt']))
        count = len(host.writes)
        self.assertEqual(host.execute('check')['status'], 'authorized')
        self.assertEqual(host.execute('prepare')['status'], 'authorized')
        self.assertEqual(len(host.writes), count)
        self.assertEqual(len(host.backups), 1)
        file_writes = [(key, raw) for key, raw in host.writes if key != 'record']
        self.assertEqual(file_writes[0][0], 'journal')
        self.assertEqual(guard.strict_json(file_writes[0][1])['status'], 'incomplete')
        self.assertEqual(file_writes[-1][0], 'journal')
        self.assertEqual(guard.strict_json(file_writes[-1][1])['status'], 'complete')

    def test_every_source_identity_conflict_rejected_before_writes(self):
        for key in ('tree', 'package_id', 'binary_sha256', 'manifest_sha256', 'commit'):
            host = FakeHost()
            host.execute()
            count = len(host.writes)
            if key == 'package_id':
                host.request['release'][key] = '124-' + 'a' * 12
            elif key == 'commit':
                host.request['release'][key] = 'e' * 40
                host.request['release']['package_id'] = '123-' + 'e' * 12
            else:
                host.request['release'][key] = 'e' * (40 if key == 'tree' else 64)
            with self.subTest(key=key), self.assertRaisesRegex(guard.Refused, 'release_binding_conflict'):
                host.execute()
            self.assertEqual(len(host.writes), count)

    def test_failed_phase_restores_all_four_original_documents(self):
        for key in ('journal', 'guard', 'container_guard', 'receipt'):
            for committed in (False, True):
                host = FakeHost()
                host.fail_key, host.fail_after = key, committed
                with self.subTest(key=key, committed=committed), self.assertRaisesRegex(guard.Refused, 'prepare_failed_restored'):
                    host.execute()
                self.assertEqual(host.files, host.original)
                record = guard.strict_json(host.read_record(host.request))
                self.assertEqual(record['status'], 'rolled_back')
                self.assertEqual(host.execute()['status'], 'authorized')

    def test_external_bytes_never_overwritten_by_failure_restore(self):
        host = FakeHost()
        host.fail_key = 'container_guard'
        host.external_drift = True
        with self.assertRaisesRegex(guard.Refused, 'prepare_failed_recovery_required'):
            host.execute()
        self.assertEqual(host.files['guard'], b'externally changed bytes')
        self.assertEqual(guard.strict_json(host.files['journal'])['status'], 'incomplete')
        self.assertEqual(guard.strict_json(host.read_record(host.request))['status'], 'recovery_required')

    def test_complete_journal_and_authorized_receipt_failure_revoke_permission(self):
        for key, occurrence in (('journal', 2), ('record', 1), ('record', 2)):
            for committed in (False, True):
                host = FakeHost()
                host.fail_key, host.fail_occurrence, host.fail_after = key, occurrence, committed
                with self.subTest(key=key, occurrence=occurrence, committed=committed), \
                        self.assertRaisesRegex(guard.Refused, 'prepare_failed_restored'):
                    host.execute()
                self.assertEqual(host.files, host.original)
                record = guard.strict_json(host.read_record(host.request))
                self.assertEqual(record['status'], 'rolled_back')
                with self.assertRaisesRegex(guard.Refused, 'release_preparation_incomplete'):
                    host.execute('check')

    def test_epoch_drift_requires_recovery_without_signal(self):
        host = FakeHost()
        host.fail_key = 'container_guard'
        host.epoch_drift = True
        with self.assertRaisesRegex(guard.Refused, 'prepare_failed_recovery_required'):
            host.execute()
        self.assertEqual(host.epoch['container']['RestartCount'], 1)
        self.assertEqual(guard.strict_json(host.files['journal'])['status'], 'incomplete')

    def test_guard_receipt_health_and_compose_drift_refuse_before_writes(self):
        changes = (
            lambda host: host.files.update(container_guard=SOURCE + b'changed'),
            lambda host: host.config.update(guard_scaffold_sha256='f' * 64),
            lambda host: host.epoch['app'].update(exe_sha256='9' * 64),
            lambda host: host.files.update(journal=guard.encode(dict(guard.strict_json(host.files['journal']), status='incomplete'))),
            lambda host: host.epoch['container']['State'].update(Health='unhealthy'),
            lambda host: setattr(host, 'compose_ok', False),
            lambda host: setattr(host, 'guardian', True))
        for index, change in enumerate(changes):
            host = FakeHost()
            change(host)
            with self.subTest(index=index), self.assertRaises(guard.Refused):
                host.execute()
            self.assertEqual(host.writes, [])
            self.assertEqual(host.backups, [])

    def test_five_epoch_fields_are_compared(self):
        changes = (
            lambda host: host.epoch['container'].update(Id='9' * 64),
            lambda host: host.epoch['container']['State'].update(StartedAt='changed'),
            lambda host: host.epoch['container'].update(RestartCount=1),
            lambda host: host.epoch['init'].update(start_ticks=11),
            lambda host: host.epoch['app'].update(exe_inode=99))
        for index, change in enumerate(changes):
            host = FakeHost()
            original = host.snapshot
            def drift():
                if host.snapshot_calls == 1:
                    change(host)
                return original()
            host.snapshot = drift
            with self.subTest(index=index), self.assertRaisesRegex(guard.Refused, 'epoch_changed'):
                host.execute()
            self.assertEqual(host.writes, [])


class LinuxBoundaryTests(unittest.TestCase):
    def test_bind_guard_or_parent_mount_fails_before_proc_reads(self):
        config, unused, epoch, unused2 = fixture()
        for destination in (guard.ENTRYPOINT, '/app', '/'):
            host = guard.LinuxHost(config)
            container = copy.deepcopy(epoch['container'])
            container['Mounts'].append({'Destination': destination})
            with self.subTest(destination=destination), mock.patch.object(host, 'quiescent'), \
                    mock.patch.object(host, 'inspect', return_value=container), mock.patch.object(host, 'process') as process:
                with self.assertRaisesRegex(guard.Refused, 'mounted_guard_requires_coordination'):
                    host.snapshot()
                process.assert_not_called()

    def test_healthy_state_required(self):
        config, unused, epoch, unused2 = fixture()
        for key, value in (('Health', 'starting'), ('Running', False), ('Restarting', True), ('Paused', True)):
            host = guard.LinuxHost(config)
            container = copy.deepcopy(epoch['container'])
            container['State'][key] = value
            with self.subTest(key=key), mock.patch.object(host, 'quiescent'), mock.patch.object(host, 'inspect', return_value=container):
                with self.assertRaisesRegex(guard.Refused, 'container_not_healthy'):
                    host.snapshot()

    def test_guardian_not_inactive_rejected(self):
        host = guard.LinuxHost(fixture()[0])
        for state in ('active', 'activating', 'failed'):
            raw = ('ActiveState=' + state + '\nSubState=dead\nMainPID=0\nLoadState=loaded\n').encode()
            with self.subTest(state=state), mock.patch.object(host, 'run', return_value=raw):
                with self.assertRaisesRegex(guard.Refused, 'guardian_or_fence_not_inactive'):
                    host.quiescent()


if __name__ == '__main__':
    unittest.main()
