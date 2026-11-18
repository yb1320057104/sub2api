import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

from tools import openai_codex_oauth_adapter as adapter
from tools import openai_oauth_reauth_worker as worker


class CodexDiagnosticTests(unittest.TestCase):
    def diagnostic(self, **changes):
        return {**dict(version=1, task_id=7, stage='token_exchange', reason='token_exchange_failed', elapsed_ms=120), **changes}

    def test_workspace_modes_and_transport_diagnostics(self):
        script = '''
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {selectCodexWorkspace, createProtocolDiagnostics} from './tools/openai_codex_diagnostics.mjs';
const workspaces = [{id:'z-work',kind:'organization'}, {id:'person',kind:'personal'}, {id:'a-work',kind:'organization'}];
assert.equal(selectCodexWorkspace(workspaces), 'a-work');
assert.equal(selectCodexWorkspace([...workspaces].reverse()), 'a-work');
assert.equal(selectCodexWorkspace(workspaces, {mode:'personal'}), 'person');
assert.equal(selectCodexWorkspace(workspaces, {mode:'business'}), 'a-work');
assert.equal(selectCodexWorkspace(workspaces, {mode:'auto',expected:'z-work'}), 'z-work');
assert.throws(() => selectCodexWorkspace(workspaces, {mode:'personal',expected:'a-work'}));
assert.throws(() => selectCodexWorkspace([{id:'person',kind:'personal'}], {mode:'business'}));
assert.throws(() => selectCodexWorkspace([...workspaces,workspaces[0]]));
const file=process.argv[1];
for (const message of ['curl: (35) TLS connect error', 'Failed to perform, curl: (60) SSL certificate problem', 'SSLError: private-proxy-url', 'ProxyError: private-proxy-url']) {
  createProtocolDiagnostics(file,7).fail(new Error(message));
  const safe=fs.readFileSync(file,'utf8');
  assert.equal(JSON.parse(safe).reason,'transport_error');
  assert(!safe.includes('private'));
}
createProtocolDiagnostics(file,7).fail(new Error('arbitrary private failure'));
assert.equal(JSON.parse(fs.readFileSync(file,'utf8')).reason,'protocol_error');
'''
        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run([os.getenv('NODE_EXECUTABLE', 'node'), '--input-type=module', '-e', script,
                                     str(Path(directory) / 'diagnostic.json')], capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_unknown_layout_is_rejected_without_mutating_runtime(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'runtime'
            (root / 'src').mkdir(parents=True)
            original = root / 'src/protocol-login.mjs'
            original.write_text('// unrecognized layout')
            with self.assertRaisesRegex(ValueError, '^unsupported_codex_runtime_layout$'):
                adapter.prepare_codex_runtime(root, Path(directory) / 'target')
            self.assertEqual(original.read_text(), '// unrecognized layout')
            self.assertFalse((Path(directory) / 'target').exists())

    def test_side_channel_is_bounded_task_bound_and_private(self):
        valid = self.diagnostic()
        for changes in ({'stage': 'synthetic-private'}, {'reason': 'synthetic-private'},
                        {'task_id': 8}, {'task_id': True}, {'version': True},
                        {'elapsed_ms': -1}, {'elapsed_ms': 3600001}, {'elapsed_ms': True},
                        {'message': 'synthetic-private'}, {'stage': []}, {'reason': {}}):
            with self.subTest(changes=changes), tempfile.TemporaryDirectory() as directory:
                file = Path(directory) / 'diagnostic.json'
                file.write_text(json.dumps({**valid, **changes}))
                with self.assertRaisesRegex(ValueError, '^invalid_protocol_diagnostic$'):
                    adapter.read_protocol_diagnostic(file, 7)
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / 'diagnostic.json'
            self.assertIsNone(adapter.read_protocol_diagnostic(file, 7))
            file.write_text(json.dumps(valid))
            self.assertEqual(adapter.read_protocol_diagnostic(file, 7), valid)
            for data in ('x' * 1025, '[]', '{bad', json.dumps(valid).replace('"version": 1', '"version": 0, "version": 1')):
                file.write_text(data)
                with self.assertRaisesRegex(ValueError, '^invalid_protocol_diagnostic$'):
                    adapter.read_protocol_diagnostic(file, 7)
            file.unlink()
            target = Path(directory) / 'private.txt'
            target.write_text('synthetic-private')
            file.symlink_to(target)
            with self.assertRaisesRegex(ValueError, '^invalid_protocol_diagnostic$'):
                adapter.read_protocol_diagnostic(file, 7)

    def run_worker(self, *, exit_code=1, diagnostic=True, payload=None):
        api = SimpleNamespace(config=SimpleNamespace(tosub2_root=Path('synthetic-runtime')),
                              progress=Mock(), credentials=Mock(return_value={'status': 'succeeded'}))
        claim = dict(task_id=7, account_id=42, oauth_profile='codex', credential_mode='password_totp',
                     login_email='test@example.test', password='synthetic-private', totp_secret='synthetic-private')
        def run(command, **kwargs):
            self.assertEqual(kwargs['stdout'], subprocess.DEVNULL)
            self.assertEqual(kwargs['stderr'], subprocess.DEVNULL)
            self.assertNotIn('OPENAI_REAUTH_WORKER_TOKEN', kwargs['env'])
            if diagnostic:
                value = diagnostic if isinstance(diagnostic, dict) else self.diagnostic()
                Path(kwargs['env']['SUB2API_PROTOCOL_DIAGNOSTIC_PATH']).write_text(json.dumps(value))
            if payload is not None:
                Path(command[command.index('--sub2api-out') + 1]).write_text(json.dumps(payload))
            return SimpleNamespace(returncode=exit_code, stdout='synthetic-private', stderr='synthetic-private')
        with patch.object(Path, 'is_file', return_value=True), patch.object(worker.shutil, 'which', return_value='node'), patch.object(
            adapter, 'prepare_codex_runtime', return_value=Path('synthetic-runner.mjs')
        ), patch.object(worker.subprocess, 'run', side_effect=run):
            try:
                worker.process_password_claim(api, claim)
            except worker.WorkerError as exc:
                return api, str(exc)
        return api, None

    def test_nonzero_and_unsafe_side_channel_never_submit_or_leak(self):
        api, error = self.run_worker()
        self.assertIn('stage=token_exchange reason=token_exchange_failed', error)
        self.assertNotIn('synthetic', error)
        api.credentials.assert_not_called()
        api, error = self.run_worker(diagnostic={'message': 'synthetic-private'})
        self.assertIn('diagnostic_invalid', error)
        self.assertNotIn('synthetic', error)
        api.credentials.assert_not_called()

    def test_email_verification_stops_without_submitting_credentials(self):
        api, error = self.run_worker(diagnostic=self.diagnostic(stage='web_login', reason='email_verification_required'))
        self.assertIn('stage=web_login reason=email_verification_required', error)
        self.assertEqual(worker.safe_worker_failure(worker.WorkerError(error)), error)
        api.credentials.assert_not_called()

    def test_missing_side_channel_preserves_old_failure_and_complete_output_contract(self):
        api, error = self.run_worker(diagnostic=False)
        self.assertEqual(error, 'password/TOTP protocol failed (exit 1)')
        api.credentials.assert_not_called()
        credentials = {k: 'synthetic-private' for k in ('access_token', 'refresh_token', 'id_token')}
        api, error = self.run_worker(exit_code=0, diagnostic=False, payload={'accounts': [{'credentials': credentials}]})
        self.assertIsNone(error)
        api.credentials.assert_called_once_with(7, credentials, {})

    def test_completed_progress_does_not_override_nonzero_exit_or_half_credentials(self):
        completed = {**self.diagnostic(), 'stage': 'completed', 'reason': 'none'}
        api, error = self.run_worker(diagnostic=completed)
        self.assertIn('reason=process_failed', error)
        api.credentials.assert_not_called()
        api, error = self.run_worker(exit_code=0, diagnostic=completed,
                                     payload={'accounts': [{'credentials': {'access_token': 'synthetic-private'}}]})
        self.assertIn('incomplete OAuth', error)
        api.credentials.assert_not_called()
        api, error = self.run_worker(exit_code=0, diagnostic=self.diagnostic(stage='mfa_accepted', reason='none'))
        self.assertIn('reason=output_invalid', error)
        api.credentials.assert_not_called()


@unittest.skipUnless(os.getenv('EXCEL_TEST_TOSUB2_ROOT'), 'pinned runtime supplied by packaging CI')
class PinnedCodexProtocolTests(unittest.TestCase):
    def test_structured_failure_does_not_send_mfa_or_tokens(self):
        dependency = Path(os.environ['EXCEL_TEST_TOSUB2_ROOT'])
        cases = json.loads((Path(__file__).parent / 'fixtures/credential-operation-errors.json').read_text())
        for case in cases:
            with self.subTest(name=case['name']), tempfile.TemporaryDirectory() as directory:
                temporary = Path(directory)
                script = adapter.prepare_codex_runtime(dependency, temporary)
                script.with_name('tls-transport.mjs').write_bytes((Path(__file__).parent / 'fixtures/codex-http-transport.mjs').read_bytes())
                diagnostic, routes, output = (temporary / n for n in ('diagnostic.json', 'routes.txt', 'oauth.json'))
                env = dict(os.environ, SUB2API_PROTOCOL_TASK_ID='7', SUB2API_PROTOCOL_DIAGNOSTIC_PATH=str(diagnostic),
                           CHATGPT_LOGIN_PASSWORD='synthetic-password', CODEX_TEST_MODE='operation-error',
                           CODEX_TEST_ERROR=json.dumps(case['payload']), CODEX_TEST_ROUTES=str(routes))
                result = subprocess.run([os.getenv('NODE_EXECUTABLE', 'node'), str(script), '--email',
                                         'fixture', '--output-mode', 'sub2api', '--sub2api-out', str(output)],
                                        cwd=temporary, env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=20)
                safe = adapter.read_protocol_diagnostic(diagnostic, 7)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout + result.stderr, '')
                self.assertEqual(safe['reason'], case['code'] if case['code'] != 'unknown' else 'protocol_error')
                trace = routes.read_text()
                self.assertEqual(trace.count('POST /api/accounts/password/verify'), 1)
                self.assertNotIn('POST /api/accounts/mfa/', trace)
                self.assertNotIn('POST /oauth/token', trace)
                self.assertFalse(output.exists())

    def test_real_pinned_cli_with_only_mock_http(self):
        dependency = Path(os.environ['EXCEL_TEST_TOSUB2_ROOT'])
        original = (dependency / 'src/protocol-login.mjs').read_bytes()
        for mode, expected in [('success', None), ('workspace-success', None), ('missing-callback', 'callback_missing'),
                               ('wrong-state', 'callback_state_mismatch'), ('exchange-failed', 'token_exchange_failed'),
                               ('incomplete', 'credentials_incomplete'), ('session-rejected', 'session_unavailable'),
                               ('workspace-mismatch', 'workspace_mismatch'),
                               ('direct-success', None), ('direct-page-payload', None), ('direct-consent-payload', None),
                               ('direct-email-required', 'email_verification_required'),
                               ('direct-wrong-state', 'callback_state_mismatch'),
                               ('direct-workspace-mismatch', 'workspace_mismatch'),
                               ('direct-exchange-failed', 'token_exchange_failed')]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                temporary = Path(directory)
                script = adapter.prepare_codex_runtime(dependency, temporary)
                script.with_name('tls-transport.mjs').write_bytes(
                    (Path(__file__).parent / 'fixtures/codex-http-transport.mjs').read_bytes())
                output, diagnostic, routes = (temporary / n for n in ('oauth.json', 'diagnostic.json', 'routes.txt'))
                env = dict(os.environ, SUB2API_PROTOCOL_TASK_ID='7', SUB2API_PROTOCOL_DIAGNOSTIC_PATH=str(diagnostic),
                           CHATGPT_LOGIN_PASSWORD='synthetic-password', CHATGPT_TOTP_SECRET='JBSWY3DPEHPK3PXP',
                           CODEX_TEST_MODE=mode, CODEX_TEST_ROUTES=str(routes))
                if mode.startswith(('workspace-', 'direct-')):
                    env['OPENAI_CODEX_EXPECTED_WORKSPACE'] = 'expected-workspace'
                if mode.startswith('direct-'):
                    env['OPENAI_CODEX_WORKSPACE_MODE'] = 'auto'
                else:
                    env.pop('OPENAI_CODEX_WORKSPACE_MODE', None)
                result = subprocess.run([os.getenv('NODE_EXECUTABLE', 'node'), str(script), '--email',
                                         'test@example.test', '--output-mode', 'sub2api', '--sub2api-out', str(output)],
                                        cwd=temporary, env=env, stdin=subprocess.DEVNULL,
                                        capture_output=True, text=True, timeout=20)
                safe = adapter.read_protocol_diagnostic(diagnostic, 7)
                self.assertEqual(result.stdout + result.stderr, '')
                self.assertEqual(safe['task_id'], 7)
                if expected is None:
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(safe['stage'], 'completed')
                    self.assertEqual(set(json.loads(output.read_text())['accounts'][0]['credentials']) &
                                     {'access_token', 'refresh_token', 'id_token'}, {'access_token', 'refresh_token', 'id_token'})
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(safe['reason'], expected)
                    self.assertFalse(output.exists())
                trace = routes.read_text()
                password_count = 0 if mode == 'direct-email-required' else 1
                self.assertEqual(trace.count('POST /api/accounts/password/verify'), password_count)
                self.assertEqual(trace.count('POST /api/accounts/mfa/verify'), password_count)
                if mode.startswith('direct-'):
                    self.assertNotIn('POST /api/auth/signin/openai', trace)
                    self.assertEqual(trace.count('GET /oauth/authorize'), 1)
                if mode in ('missing-callback', 'wrong-state', 'session-rejected', 'workspace-mismatch',
                            'direct-email-required', 'direct-wrong-state', 'direct-workspace-mismatch'):
                    self.assertNotIn('POST /oauth/token', trace)
        self.assertEqual((dependency / 'src/protocol-login.mjs').read_bytes(), original)


if __name__ == '__main__':
    unittest.main()
