import argparse
import contextlib
import copy
import gzip
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import yaml

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('release_matrix', Path(__file__).with_name('release_matrix.py'))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
control_spec = importlib.util.spec_from_file_location('jenkins_control', ROOT / 'deploy/jenkins/control.py')
control = importlib.util.module_from_spec(control_spec)
control_spec.loader.exec_module(control)
runtime_spec = importlib.util.spec_from_file_location('jenkins_runtime', ROOT / 'deploy/jenkins/verify-runtime.py')
runtime = importlib.util.module_from_spec(runtime_spec)
runtime_spec.loader.exec_module(runtime)


class ReleaseMatrixTest(unittest.TestCase):
    def setUp(self):
        self.previous = Path.cwd()
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        os.chdir(self.temp.name)
        self.addCleanup(os.chdir, self.previous)
        for name in ('.goreleaser.yaml', '.goreleaser.simple.yaml'):
            shutil.copyfile(ROOT / name, name)
        Path('backend/cmd/server').mkdir(parents=True)
        release.VERSION_FILE.write_text('9.8.7\n')

    def fixture_artifacts(self, simple=False):
        directory = Path('release-input')
        directory.mkdir()
        for target in release.targets(simple):
            name = release.archive_name('9.8.7', target)
            archive = directory / name
            if target['goos'] == 'linux':
                with tarfile.open(archive, 'w:gz') as out:
                    info = tarfile.TarInfo('sub2api')
                    info.size = 7
                    info.mode = 0o755
                    out.addfile(info, io.BytesIO(b'fixture'))
            else:
                archive.write_bytes(b'fixture archive')
            metadata = {'version': '9.8.7', 'sha': 'a' * 40, 'target': target,
                        'archive': name, 'sha256': release.sha256(archive)}
            (directory / f"manifest-{target['goos']}-{target['goarch']}.json").write_text(json.dumps(metadata))
        return argparse.Namespace(input='release-input', version='9.8.7', sha='a' * 40, simple=simple, output='contexts')

    def test_full_and_simple_matrix_match_existing_targets(self):
        full = release.targets()
        self.assertEqual(len(full), 5)
        self.assertNotIn({'goos': 'windows', 'goarch': 'arm64'}, full)
        self.assertEqual(release.targets(True), [{'goos': 'linux', 'goarch': 'amd64'}])

    def test_leaf_keeps_packaging_and_selects_only_one_target(self):
        original = release.config()
        release.generate_config(argparse.Namespace(mode='build', simple=False, goos='darwin', goarch='arm64', output='leaf.yaml'))
        leaf = yaml.safe_load(Path('leaf.yaml').read_text())
        self.assertEqual(leaf['builds'][0]['goos'], ['darwin'])
        self.assertEqual(leaf['builds'][0]['goarch'], ['arm64'])
        self.assertEqual(leaf['builds'][0]['ignore'], [])
        self.assertEqual(leaf['archives'], original['archives'])
        self.assertEqual(leaf['release'], original['release'])
        self.assertFalse(leaf['dockers'])
        self.assertIn('{{ .Env.RELEASE_DATE }}', '\n'.join(leaf['builds'][0]['ldflags']))

    def test_publication_config_has_no_compilation_or_docker_work(self):
        for simple in (False, True):
            with self.subTest(simple=simple):
                original = release.config(simple)
                release.generate_config(argparse.Namespace(mode='publish', simple=simple, output='publisher.yaml'))
                data = yaml.safe_load(Path('publisher.yaml').read_text())
                self.assertTrue(data['builds'][0]['skip'])
                self.assertFalse(data['archives'])
                self.assertFalse(data['dockers'])
                self.assertEqual(data['release']['header'], original['release']['header'])
                self.assertEqual(data['release']['footer'], original['release']['footer'])
                if simple:
                    self.assertTrue(data['checksum']['disable'])
                    self.assertTrue(data['release']['skip_upload'])
                else:
                    self.assertEqual(data['checksum']['extra_files'], data['release']['extra_files'])

    def test_collect_and_verify_hash_and_source_binding(self):
        args = self.fixture_artifacts()
        release.verify(args)
        file = next(Path(args.input).glob('*.tar.gz'))
        file.write_bytes(b'corrupted')
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            release.verify(args)

    def test_missing_extra_and_wrong_commit_artifacts_are_rejected(self):
        args = self.fixture_artifacts(True)
        args.sha = 'b' * 40
        with self.assertRaises(ValueError):
            release.verify(args)
        args.sha = 'a' * 40
        Path('release-input/unexpected').write_text('not an asset')
        with self.assertRaises(ValueError):
            release.verify(args)
        Path('release-input/unexpected').unlink()
        next(Path('release-input').glob('*.tar.gz')).unlink()
        with self.assertRaises(FileNotFoundError):
            release.verify(args)

    def test_linux_context_preserves_binary_executable_mode(self):
        args = self.fixture_artifacts()
        Path('Dockerfile.goreleaser').write_text('FROM scratch\nCOPY sub2api /sub2api\n')
        Path('deploy').mkdir()
        Path('deploy/docker-entrypoint.sh').write_text('#!/bin/sh\nexec /app/sub2api\n')
        Path('backend/resources').mkdir()
        Path('backend/resources/data').write_text('fixture')
        release.contexts(args)
        for arch in ('amd64', 'arm64'):
            binary = Path('contexts') / arch / 'sub2api'
            self.assertEqual(binary.read_bytes(), b'fixture')
            self.assertEqual(binary.stat().st_mode & 0o777, 0o755)

    def test_plan_requires_a_tag_for_publication(self):
        args = argparse.Namespace(ref='main', dry_run=False, simple=False)
        with patch.object(subprocess, 'check_output', return_value='a' * 40 + '\n'):
            with self.assertRaisesRegex(ValueError, 'version tag'):
                release.plan(args)
        args.ref = 'v9.8.7'
        with patch.object(subprocess, 'check_output', side_effect=['a' * 40 + '\n', 'b' * 40 + '\n']):
            with self.assertRaisesRegex(ValueError, 'does not match'):
                release.plan(args)

    def test_dry_run_plan_resolves_matrix_without_a_new_tag(self):
        with patch.dict(os.environ, {'GITHUB_OUTPUT': 'outputs', 'GITHUB_REPOSITORY_OWNER': 'ExampleOwner'}), patch.object(subprocess, 'check_output', return_value='a' * 40 + '\n'):
            release.plan(argparse.Namespace(ref='feature/matrix', dry_run=True, simple=False))
        output = dict(line.split('=', 1) for line in Path('outputs').read_text().splitlines())
        self.assertEqual(output['dry_run'], 'true')
        self.assertEqual(output['owner_lower'], 'exampleowner')
        self.assertEqual(len(json.loads(output['matrix'])['include']), 5)

    def test_docker_commands_do_not_publish_during_dry_run(self):
        fake_bin = Path('bin')
        fake_bin.mkdir()
        docker = fake_bin / 'docker'
        docker.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$DOCKER_LOG"\n')
        docker.chmod(0o755)
        env = {**os.environ, 'PATH': str(fake_bin.resolve()) + os.pathsep + os.environ['PATH'],
               'DOCKER_LOG': str(Path('docker.log').resolve()), 'RUNNER_TEMP': self.temp.name,
               'RELEASE_VERSION': '9.8.7', 'RELEASE_SHA': 'a' * 40, 'GITHUB_REPOSITORY': 'ExampleOwner/sub2api',
               'DRY_RUN': 'true', 'SIMPLE_RELEASE': 'false', 'DOCKERHUB_USERNAME': 'skip'}
        subprocess.run(['bash', str(ROOT / '.github/release-tools/release-images.sh')], env=env, check=True)
        log = Path('docker.log').read_text()
        self.assertEqual(log.count('buildx build'), 2)
        self.assertIn('linux/arm64', log)
        self.assertNotIn('--push', log)
        self.assertNotIn('imagetools', log)
        self.assertNotIn('skip/sub2api', log)
        self.assertIn('ghcr.io/exampleowner/sub2api', log)


    def test_published_full_and_simple_image_tags(self):
        fake_bin = Path('bin')
        fake_bin.mkdir()
        docker = fake_bin / 'docker'
        docker.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$DOCKER_LOG"\n')
        docker.chmod(0o755)
        for simple in (False, True):
            with self.subTest(simple=simple):
                log_path = Path(f'docker-{simple}.log').resolve()
                env = {**os.environ, 'PATH': str(fake_bin.resolve()) + os.pathsep + os.environ['PATH'],
                       'DOCKER_LOG': str(log_path), 'RUNNER_TEMP': self.temp.name,
                       'RELEASE_VERSION': '9.8.7', 'RELEASE_SHA': 'a' * 40, 'GITHUB_REPOSITORY': 'ExampleOwner/sub2api',
                       'DRY_RUN': 'false', 'SIMPLE_RELEASE': str(simple).lower(), 'DOCKERHUB_USERNAME': 'fixturehub'}
                subprocess.run(['bash', str(ROOT / '.github/release-tools/release-images.sh')], env=env, check=True)
                log = log_path.read_text()
                self.assertIn('--push', log)
                self.assertEqual(log.count('buildx build'), 1 if simple else 2)
                if simple:
                    self.assertNotIn('fixturehub', log)
                    self.assertNotIn('imagetools', log)
                    self.assertIn('ghcr.io/exampleowner/sub2api:latest', log)
                else:
                    self.assertEqual(log.count('imagetools create'), 2)
                    self.assertIn('fixturehub/sub2api:9.8', log)
                    self.assertIn('ghcr.io/exampleowner/sub2api:9', log)



class JenkinsValidationTest(unittest.TestCase):
    """Preserve exact-artifact gates and the evidence on legacy sealed packages."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.data = {
            'repository': control.REPOSITORY, 'commit': 'a' * 40, 'tree': 'b' * 40,
            'package_id': '5-' + 'a' * 12, 'version': '9.8.7-pk.1',
            'binary_sha256': 'c' * 64, 'binary_archive_sha256': 'd' * 64,
            'image_archive_sha256': 'e' * 64, 'image_id': 'sha256:' + 'f' * 64,
            'source_notes_ref': control.NOTES_REF, 'source_notes_commit': 'd' * 40,
            'source_note_blob': 'e' * 40, 'validation_profile': control.LEGACY_VALIDATION_PROFILE,
        }
        snapshot = {'head': 'c' * 40, 'tree': self.data['tree'], 'worktree_matches_index': True, 'status': 'M staged'}
        self.source = {
            **{key: self.data[key] for key in ('repository', 'commit', 'tree')},
            'schema_version': 2, 'tests_profile': 'pk-source-v2', 'status': 'passed',
            'execution_environment': 'server-isolated',
            'source_eligible': True, 'bound_at': '2026-10-08T00:00:00Z', 'base_commit': 'c' * 40,
            'workspace_before': copy.deepcopy(snapshot), 'workspace_after': copy.deepcopy(snapshot),
            'preflight': self.suite('preflight'),
            'suites': [self.suite(name) for name in sorted(control.SOURCE_SUITES)],
        }
        self.save_source()
        self.runtime = {
            **{key: self.data[key] for key in ('repository', 'commit', 'tree', 'package_id', 'version',
                'binary_sha256', 'binary_archive_sha256', 'image_archive_sha256', 'image_id', 'source_validation_sha256')},
            'schema_version': 2, 'tests_profile': 'pk-artifact-v2', 'status': 'passed',
            'checks': [dict(name=name, status='passed', exit_code=0) for name in sorted(control.RUNTIME_CHECKS)],
        }
        self.save_runtime()

    @staticmethod
    def suite(name):
        return dict(name=name, status='passed', exit_code=0, commands=['fixture command'], log_sha256='f' * 64)

    def save_source(self):
        control.write_json(self.directory / 'source-validation.json', self.source)
        self.data['source_validation_sha256'] = control.sha256(self.directory / 'source-validation.json')

    def save_runtime(self):
        control.write_json(self.directory / 'artifact-validation.json', self.runtime)
        self.data['artifact_validation_sha256'] = control.sha256(self.directory / 'artifact-validation.json')

    def use_artifact_only_profile(self):
        for field in control.SOURCE_EVIDENCE_FIELDS:
            self.data.pop(field)
        (self.directory / 'source-validation.json').unlink()
        self.runtime.pop('source_validation_sha256')
        self.data['validation_profile'] = control.VALIDATION_PROFILE
        self.runtime['validation_profile'] = control.VALIDATION_PROFILE
        self.save_runtime()

    def test_release_requires_formal_tag_without_commit_override(self):
        env = {'ACTION': 'release', 'PK_REPOSITORY': control.REPOSITORY, 'PK_IMAGE': control.IMAGE,
               'SOURCE_REF': 'refs/tags/v9.8.7-pk.1', 'COMMIT_SHA': ''}
        with patch.dict(os.environ, env, clear=True):
            control.parameters()
            for ref in ('refs/heads/main', 'a' * 40, 'refs/tags/v9.8.7', 'refs/tags/v9.8.7-pk.candidate.5'):
                with self.subTest(ref=ref), patch.dict(os.environ, SOURCE_REF=ref):
                    with self.assertRaises(RuntimeError):
                        control.parameters()
            with patch.dict(os.environ, COMMIT_SHA='a' * 40):
                with self.assertRaises(RuntimeError):
                    control.parameters()
        with patch.dict(os.environ, {**env, 'ACTION': 'package', 'SOURCE_REF': 'refs/heads/main'}, clear=True):
            control.parameters()

    def test_release_without_source_reports_keeps_artifact_checks_through_publication(self):
        self.exercise_release(reinstall=False)

    def test_reinstalled_dependencies_remain_bound_through_seal_restore_and_publication(self):
        self.exercise_release(reinstall=True)

    def exercise_release(self, reinstall):
        package_root = self.directory / 'packages'
        package_root.mkdir()
        env = {'ACTION': 'release', 'PK_REPOSITORY': control.REPOSITORY, 'PK_IMAGE': control.IMAGE,
               'SOURCE_REF': 'refs/tags/v9.8.7-pk.1', 'COMMIT_SHA': '', 'BUILD_NUMBER': '5',
               'PK_PACKAGE_ROOT': str(package_root), 'PK_API_TOKEN': 'fixture-api-token',
               'PK_REGISTRY_TOKEN': 'fixture-registry-token', 'PK_REGISTRY_USER': 'fixture-user',
               'DOCKER_HOST': 'tcp://fixture:2376'}
        if reinstall:
            env['REINSTALL_NODE_MODULES'] = '是'
            frontend = self.directory / 'source/frontend'
            frontend.mkdir(parents=True)
            (frontend / 'pnpm-lock.yaml').write_bytes(b'original source lock\n')
            (frontend / 'package.json').write_bytes(b'{"name":"fixture"}\n')

        def fake_git(*args):
            values = {
                ('status', '--porcelain'): '',
                ('remote', 'get-url', 'origin'): 'git@github.com:' + control.REPOSITORY + '.git',
                ('rev-parse', 'HEAD'): self.data['commit'],
                ('rev-parse', 'HEAD^{tree}'): self.data['tree'],
                ('rev-parse', env['SOURCE_REF'] + '^{commit}'): self.data['commit'],
                ('merge-base', '--is-ancestor', self.data['commit'], 'refs/remotes/origin/main'): '',
            }
            self.assertIn(args, values, 'No notes or other Git operations are required')
            return values[args]

        def fake_api(path, method='GET', data=None, token=None, allow_404=False):
            repository_path = '/repos/' + control.REPOSITORY
            if path == repository_path:
                return {'private': True, 'full_name': control.REPOSITORY} if token else None
            if path == repository_path + '/git/ref/tags/v9.8.7-pk.1':
                return {'object': {'type': 'commit', 'sha': self.data['commit']}}
            if path == repository_path + '/compare/' + self.data['commit'] + '...main':
                return {'status': 'identical'}
            if path == repository_path + '/releases/tags/v9.8.7-pk.1':
                return None
            if path == '/users/yb1320057104/packages/container/sub2api':
                return {'visibility': 'private'}
            if path == '/users/yb1320057104/packages/container/sub2api/versions?per_page=100&page=1':
                return []
            if path == repository_path + '/releases' and method == 'POST':
                self.assertIn('source validation is not required', data['body'])
                self.assertNotIn('source tests', data['body'])
                return {'id': 7}
            if path == repository_path + '/releases/7' and method == 'PATCH':
                return {'html_url': 'https://github.com/' + control.REPOSITORY + '/releases/tag/v9.8.7-pk.1'}
            self.fail('Unexpected API call: ' + method + ' ' + path)

        with contextlib.chdir(self.directory), patch.dict(os.environ, env, clear=True), \
                patch.object(control, 'git', side_effect=fake_git), \
                patch.object(control, 'request', side_effect=fake_api), \
                patch.object(control, 'source_evidence') as source_gate, \
                patch.object(control, 'evidence') as manual_gate:
            control.prepare()
            output = Path('output')
            data = control.manifest(output)
            self.assertEqual(data['validation_profile'], control.VALIDATION_PROFILE)
            self.assertFalse((output / 'source-validation.json').exists())
            self.assertTrue(all(field not in data for field in control.SOURCE_EVIDENCE_FIELDS))
            if reinstall:
                JenkinsDependencyTest.write_evidence(output, data)
                control.write_json(output / 'manifest.json', data)
            else:
                self.assertEqual(data['schema_version'], 1)
                self.assertNotIn('frontend_dependencies', data)

            # Build outputs and the runtime check outcomes are unit-test fixtures;
            # the real inventory, integrity, seal and publication bindings run below.
            binary = b'fixture binary, never executed'
            with tarfile.open(output / data['binary_archive'], 'w:gz') as archive:
                member = tarfile.TarInfo('sub2api')
                member.size = len(binary)
                member.mode = 0o755
                archive.addfile(member, io.BytesIO(binary))
            (output / data['image_archive']).write_bytes(b'fixture image, never loaded')
            (output / 'binary-sha256.txt').write_text(hashlib.sha256(binary).hexdigest())
            (output / 'image-id.txt').write_text(self.data['image_id'])
            control.inventory()
            report = {'schema_version': 2, 'tests_profile': 'pk-artifact-v2', 'status': 'passed',
                      'checks': copy.deepcopy(self.runtime['checks'])}
            runtime.Validator(output, report).artifacts()
            self.assertEqual(report['validation_profile'], control.VALIDATION_PROFILE)
            self.assertNotIn('source_validation_sha256', report)
            if reinstall:
                for key in control.dependencies.BINDINGS:
                    self.assertEqual(report[key], data[key])
                    self.assertEqual(report['artifact'][key], data[key])
            control.write_json(output / 'artifact-validation.json', report)
            control.seal()
            self.assertIn('source validation is not required', control.manifest(output)['validation'])

            shutil.rmtree(output)
            with patch.dict(os.environ, ACTION='publish', PACKAGE_ID=data['package_id'], REINSTALL_NODE_MODULES='否'):
                control.restore()
            control.ready()
            with patch.object(control, 'command', return_value=self.data['image_id']) as commands, \
                    patch.object(control, 'upload_asset') as upload:
                control.publish()
            names = [call.args[1].name for call in upload.call_args_list]
            expected = [data['binary_archive'], 'checksums.txt', 'manifest.json', 'artifact-validation.json']
            if reinstall:
                expected += [control.dependencies.REPORT, *control.dependencies.FILES.values()]
            self.assertEqual(names, expected)
            self.assertTrue(all(call.args[0] == 'docker' for call in commands.call_args_list))
            self.assertTrue(all(call.args[1] not in ('build', 'run', 'start') for call in commands.call_args_list))
            source_gate.assert_not_called()
            manual_gate.assert_not_called()
            self.assertFalse(json.loads((output / 'published.json').read_text())['production_deployed'])

    def test_source_report_must_be_complete_passed_and_bound(self):
        control.source_evidence(self.source, self.data)
        changes = [dict(commit='f' * 40), dict(tree='f' * 40), dict(repository='other/repo'),
                   dict(status='failed'), dict(source_eligible=False), dict(bound_at=None),
                   dict(execution_environment='unknown'),
                   dict(tests_profile='pk-local-v1'), dict(base_commit=''), dict(suites=self.source['suites'][:-1]),
                   dict(suites=[self.source['suites'][0]] * 9), dict(preflight=self.suite('wrong'))]
        for change in changes:
            with self.subTest(change=list(change)), self.assertRaises(RuntimeError):
                control.source_evidence({**self.source, **change}, self.data)

    def test_skipped_failed_or_empty_source_commands_cannot_pass(self):
        for target in ('preflight', 'suite'):
            for change in (dict(status='skipped'), dict(exit_code=1), dict(exit_code=False), dict(commands=[]),
                           dict(log_sha256=''), dict(skipped_tests=['TestPKUsageFirstResponsePersistence'])):
                proof = copy.deepcopy(self.source)
                (proof['preflight'] if target == 'preflight' else proof['suites'][0]).update(change)
                with self.subTest(target=target, change=change), self.assertRaises(RuntimeError):
                    control.source_evidence(proof, self.data)

    def test_changed_workspace_cannot_be_bound_to_tested_tree(self):
        proof = copy.deepcopy(self.source)
        proof['workspace_after']['tree'] = 'f' * 40
        with self.assertRaises(RuntimeError):
            control.source_evidence(proof, self.data)
        for key in ('commit', 'tree', 'status'):
            def fake_git(*args):
                if args == ('status', '--porcelain'):
                    return ' M changed.py' if key == 'status' else ''
                field = 'tree' if args[-1] == 'HEAD^{tree}' else 'commit'
                return 'f' * 40 if key == field else self.data[field]
            with self.subTest(key=key), patch.object(control, 'git', side_effect=fake_git), self.assertRaises(RuntimeError):
                control.verify_source_checkout(self.data)

    def test_sealed_source_and_runtime_reports_are_accepted_together(self):
        control.package_evidence(self.directory, self.data)

    def test_previous_repository_is_verifiable_but_cannot_be_relabelled_or_published(self):
        previous = control.provenance.LEGACY_REPOSITORY
        self.data.update(repository=previous, schema_version=1, platform='linux/amd64',
                         image_tag='ghcr.io/' + previous + ':' + self.data['version'],
                         binary_archive='sub2api_' + self.data['version'] + '_linux_amd64.tar.gz',
                         image_archive='sub2api_' + self.data['version'] + '_linux_amd64.docker.tar.gz')
        self.source['repository'] = previous
        self.save_source()
        self.runtime.update(repository=previous, source_validation_sha256=self.data['source_validation_sha256'])
        self.save_runtime()
        control.write_json(self.directory / 'manifest.json', self.data)
        with self.assertRaisesRegex(RuntimeError, 'Unexpected package provenance'):
            control.manifest(self.directory)
        restored = control.manifest(self.directory, allow_legacy=True)
        self.assertEqual(restored['repository'], previous)
        self.assertEqual(restored['validation_profile'], control.LEGACY_VALIDATION_PROFILE)
        control.package_evidence(self.directory, restored)
        with patch.object(control, 'request') as request:
            with self.assertRaisesRegex(RuntimeError, 'Unexpected package provenance'):
                control.release_ready(restored)
            request.assert_not_called()
        self.runtime['repository'] = control.REPOSITORY
        self.save_runtime()
        with self.assertRaisesRegex(RuntimeError, 'does not match package repository'):
            control.package_evidence(self.directory, self.data)
        control.write_json(self.directory / 'manifest.json', dict(self.data, repository='other/repo'))
        with self.assertRaisesRegex(RuntimeError, 'Unexpected package provenance'):
            control.manifest(self.directory, allow_legacy=True)

    def test_new_profile_accepts_only_artifact_runtime_evidence(self):
        self.use_artifact_only_profile()
        with patch.object(control, 'source_evidence') as source_gate:
            control.package_evidence(self.directory, self.data)
        source_gate.assert_not_called()
        self.assertFalse((self.directory / 'source-validation.json').exists())

    def test_source_evidence_cannot_be_discarded_by_changing_profiles(self):
        for profile in (control.VALIDATION_PROFILE, None, 'unknown'):
            with self.subTest(profile=profile), self.assertRaises(RuntimeError):
                control.package_evidence(self.directory, dict(self.data, validation_profile=profile))
        # Removing manifest fields is insufficient while its sealed source report remains.
        data = {key: value for key, value in self.data.items() if key not in control.SOURCE_EVIDENCE_FIELDS}
        data['validation_profile'] = control.VALIDATION_PROFILE
        with self.assertRaisesRegex(RuntimeError, 'Source evidence requires the legacy'):
            control.package_evidence(self.directory, data)
        (self.directory / 'source-validation.json').unlink()
        self.runtime['validation_profile'] = control.VALIDATION_PROFILE
        self.save_runtime()
        data['artifact_validation_sha256'] = self.data['artifact_validation_sha256']
        with self.assertRaisesRegex(RuntimeError, 'Runtime source evidence requires the legacy'):
            control.package_evidence(self.directory, data)

    def test_artifact_only_profile_must_match_runtime_report(self):
        self.use_artifact_only_profile()
        for profile in (None, control.LEGACY_VALIDATION_PROFILE, 'unknown'):
            self.runtime['validation_profile'] = profile
            self.save_runtime()
            with self.subTest(profile=profile), self.assertRaisesRegex(RuntimeError, 'Runtime validation profile'):
                control.package_evidence(self.directory, self.data)

    def test_missing_or_changed_sealed_report_is_rejected(self):
        for profile in (control.LEGACY_VALIDATION_PROFILE, control.VALIDATION_PROFILE):
            if profile == control.VALIDATION_PROFILE:
                self.use_artifact_only_profile()
            filenames = (('source-validation.json', 'artifact-validation.json')
                         if profile == control.LEGACY_VALIDATION_PROFILE else ('artifact-validation.json',))
            for filename in filenames:
                path = self.directory / filename
                original = path.read_bytes()
                for replacement in (None, original + b' '):
                    if replacement is None:
                        path.unlink()
                    else:
                        path.write_bytes(replacement)
                    with self.subTest(profile=profile, filename=filename, missing=replacement is None):
                        with self.assertRaises(RuntimeError):
                            control.package_evidence(self.directory, self.data)
                    path.write_bytes(original)

    def test_runtime_must_match_all_source_and_artifact_bindings(self):
        for key in ('repository', 'commit', 'tree', 'package_id', 'version', 'binary_sha256',
                    'binary_archive_sha256', 'image_archive_sha256', 'image_id', 'source_validation_sha256'):
            original = self.runtime[key]
            self.runtime[key] = 'different'
            self.save_runtime()
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                control.package_evidence(self.directory, self.data)
            self.runtime[key] = original

    def test_new_profile_runtime_bindings_cannot_be_missing_null_or_empty(self):
        self.use_artifact_only_profile()
        original_data, original_runtime = copy.deepcopy(self.data), copy.deepcopy(self.runtime)
        for key in control.RUNTIME_BINDINGS:
            for value in (None, '', 'different', 'missing'):
                self.data, self.runtime = copy.deepcopy(original_data), copy.deepcopy(original_runtime)
                if value == 'missing':
                    self.data.pop(key)
                    self.runtime.pop(key)
                elif value == 'different':
                    self.runtime[key] = value
                else:
                    self.data[key] = self.runtime[key] = value
                self.save_runtime()
                with self.subTest(key=key, value=value), self.assertRaises(RuntimeError):
                    control.package_evidence(self.directory, self.data)

    def test_runtime_requires_cleanup_and_every_unique_check(self):
        for profile in (control.LEGACY_VALIDATION_PROFILE, control.VALIDATION_PROFILE):
            if profile == control.VALIDATION_PROFILE:
                self.use_artifact_only_profile()
            original = copy.deepcopy(self.runtime)
            for changes in (dict(status='failed'), dict(checks=original['checks'][:-1]),
                            dict(checks=[original['checks'][0]] * len(control.RUNTIME_CHECKS)),
                            dict(checks=[dict(item, status='failed') if item['name'] == 'cleanup' else item
                                         for item in original['checks']]),
                            dict(checks=[dict(item, exit_code=False) if item['name'] == 'cleanup' else item
                                         for item in original['checks']])):
                self.runtime = {**copy.deepcopy(original), **changes}
                self.save_runtime()
                with self.subTest(profile=profile, changes=list(changes)), self.assertRaises(RuntimeError):
                    control.package_evidence(self.directory, self.data)
            self.runtime = original

    def test_manual_package_keeps_legacy_evidence_path(self):
        data = {key: value for key, value in self.data.items()
                if key != 'validation_profile' and key not in control.SOURCE_EVIDENCE_FIELDS}
        with patch.object(control, 'manifest', return_value=data), \
                patch.object(control, 'verify_artifacts'), patch.object(control, 'write_json'), \
                patch.object(control, 'release_ready'), \
                patch.object(control, 'package_evidence') as automatic_gate, \
                patch.object(control, 'evidence', return_value={'status': 'passed'}) as manual_gate:
            control.ready()
        automatic_gate.assert_not_called()
        manual_gate.assert_called_once_with(data)

    def test_candidate_never_reaches_release_api(self):
        with patch.object(control, 'request') as api:
            with self.assertRaises(RuntimeError):
                control.release_ready(dict(self.data, build_type='candidate'))
            api.assert_not_called()


class JenkinsDependencyTest(unittest.TestCase):
    """Exercise lock removal/restoration and immutable effective-input evidence."""

    @staticmethod
    def audit_report():
        return {'advisories': {}, 'metadata': {'vulnerabilities': {
            name: 0 for name in ('info', 'low', 'moderate', 'high', 'critical')}}}

    @staticmethod
    def write_evidence(directory, data):
        deps = control.dependencies
        contents = {'source_lock_sha256': b'original source lock\n',
                    'package_json_sha256': b'{"name":"fixture"}\n',
                    'effective_lock_sha256': b'effective re-resolved lock\n',
                    'audit_sha256': json.dumps(JenkinsDependencyTest.audit_report()).encode(),
                    'audit_policy_sha256': b'version: 1\nexceptions: []\n',
                    'audit_checker_sha256': b'# trusted checker fixture; not executed\n'}
        proof = {key: data[key] for key in ('repository', 'commit', 'tree', 'package_id')}
        proof.update(schema_version=1, profile=deps.PROFILE, mode='reinstall', status='passed',
                     node_image='node:24-bookworm-slim', node_version='v24.0.0', pnpm_version='9.15.9',
                     install_flags=['--no-frozen-lockfile', '--prefer-offline=false', '--offline=false'], audit_scope='production',
                     audit_exit_code=0, policy_exit_code=0)
        for key, content in contents.items():
            path = directory / deps.FILES[key]
            path.write_bytes(content)
            proof[key] = deps.digest(path)
        deps.write_json(directory / deps.REPORT, proof)
        data.update(frontend_dependencies_sha256=deps.digest(directory / deps.REPORT),
                    frontend_effective_lock_sha256=proof['effective_lock_sha256'],
                    frontend_audit_sha256=proof['audit_sha256'])

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='pk-dependency-fixture-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / 'source'
        self.frontend = self.source / 'frontend'
        self.frontend.mkdir(parents=True)
        self.output = self.root / 'output'
        self.output.mkdir()
        self.deps = control.dependencies
        self.original = b'original source lock\n'
        self.lock = self.frontend / 'pnpm-lock.yaml'
        self.lock.write_bytes(self.original)
        (self.frontend / 'package.json').write_bytes(b'{"name":"fixture"}\n')
        (self.source / '.gitignore').write_text('frontend/node_modules/\n')
        self.unrelated = self.source / 'source.txt'
        self.unrelated.write_text('original source\n')
        self.env = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
        self.env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT='0')
        self.git('init', '--quiet', '--template=')
        self.git('add', '--', '.')
        self.git('commit', '--quiet', '-m', 'Dependency fixture')
        self.data = {'schema_version': 2, 'repository': control.REPOSITORY,
                     'commit': self.git('rev-parse', 'HEAD'), 'tree': self.git('rev-parse', 'HEAD^{tree}'),
                     'package_id': '5-' + self.git('rev-parse', 'HEAD')[:12],
                     'validation_profile': control.VALIDATION_PROFILE,
                     'frontend_dependencies': {'schema_version': 1, 'mode': 'reinstall',
                        'source_lock_sha256': self.deps.digest(self.lock),
                        'package_json_sha256': self.deps.digest(self.frontend / 'package.json')}}
        self.deps.write_json(self.output / 'manifest.json', self.data)

    def git(self, *args):
        return subprocess.run(['git', '-c', 'user.name=PK dependency fixture',
            '-c', 'user.email=fixture@example.invalid', '-c', 'commit.gpgsign=false',
            '-c', 'core.autocrlf=false', *args], cwd=self.source, env=self.env,
            check=True, capture_output=True, text=True).stdout.strip()

    def installed(self, audit_report=None):
        self.deps.start()
        raw = self.root / '.frontend-dependency-raw'
        self.lock.write_bytes(b'effective re-resolved lock\n')
        (raw / 'effective-lock.yaml').write_bytes(self.lock.read_bytes())
        self.deps.write_json(raw / 'audit.json', self.audit_report() if audit_report is None else audit_report)
        for name, content in (('node-version.txt', 'v24.0.0'), ('pnpm-version.txt', '9.15.9'),
                              ('audit-exit-code.txt', '0')):
            (raw / name).write_text(content)
        self.deps.audit()
        self.data = json.loads((self.output / 'manifest.json').read_bytes())

    def test_parameter_defaults_and_publish_rejects_reinstall(self):
        env = {'ACTION': 'package', 'SOURCE_REF': 'refs/heads/main',
               'PK_REPOSITORY': control.REPOSITORY, 'PK_IMAGE': control.IMAGE}
        with patch.dict(os.environ, env, clear=True):
            control.parameters()
            for value in ('否', '是'):
                with patch.dict(os.environ, REINSTALL_NODE_MODULES=value):
                    control.parameters()
            for value in ('true', 'yes', '', '否; exit 0'):
                with patch.dict(os.environ, REINSTALL_NODE_MODULES=value), self.assertRaises(RuntimeError):
                    control.parameters()
            with patch.dict(os.environ, ACTION='publish', PACKAGE_ID='5-' + 'a' * 12):
                control.parameters()
                with patch.dict(os.environ, REINSTALL_NODE_MODULES='是'), self.assertRaises(RuntimeError):
                    control.parameters()
        for path in ('Jenkinsfile', 'deploy/jenkins/bootstrap.groovy'):
            text = (ROOT / path).read_text(encoding='utf-8')
            self.assertIn("'REINSTALL_NODE_MODULES'", text)
            self.assertIn("['否', '是']", text)

    def test_start_removes_actual_and_implicit_locks_and_restore_is_exact(self):
        implicit = self.frontend / 'node_modules/.pnpm/lock.yaml'
        implicit.parent.mkdir(parents=True)
        implicit.write_text('old implicit lock')
        with contextlib.chdir(self.root), patch.dict(os.environ, self.env, clear=True):
            self.deps.start()
            self.assertFalse(self.lock.exists())
            self.assertFalse((self.frontend / 'node_modules').exists())
            self.lock.write_bytes(b'partial install lock\n')
            self.deps.restore()
            self.deps.restore()
        self.assertEqual(self.lock.read_bytes(), self.original)
        self.assertEqual(self.git('status', '--porcelain'), '')

    def test_finish_keeps_effective_evidence_and_restores_original_source(self):
        with contextlib.chdir(self.root), patch.dict(os.environ, self.env, clear=True):
            self.installed()
            self.deps.finish()
            self.deps.verify(self.output, self.data)
            control.verify_source_checkout(self.data)
        self.assertEqual(self.lock.read_bytes(), self.original)
        self.assertNotEqual(self.deps.digest(self.lock), self.data['frontend_effective_lock_sha256'])
        self.assertEqual((self.output / 'frontend-effective-lock.yaml').read_bytes(), b'effective re-resolved lock\n')

    def test_finish_rejects_changed_effective_lock_and_does_not_hide_other_changes(self):
        with contextlib.chdir(self.root), patch.dict(os.environ, self.env, clear=True):
            self.installed()
            self.lock.write_bytes(b'unexpected build mutation\n')
            self.unrelated.write_text('unrelated change must remain\n')
            with self.assertRaisesRegex(RuntimeError, 'audited effective lock'):
                self.deps.finish()
            self.deps.restore()
            with self.assertRaisesRegex(RuntimeError, 'modified the source checkout'):
                control.verify_source_checkout(self.data)
        self.assertEqual(self.lock.read_bytes(), self.original)
        self.assertEqual(self.unrelated.read_text(), 'unrelated change must remain\n')

    def test_tampered_backup_cannot_be_restored(self):
        with contextlib.chdir(self.root), patch.dict(os.environ, self.env, clear=True):
            self.deps.start()
            backup = self.root / '.frontend-dependency-state/pnpm-lock.yaml'
            backup.chmod(0o600)
            backup.write_bytes(b'tampered backup\n')
            with self.assertRaisesRegex(RuntimeError, 'backup changed'):
                self.deps.restore()
            self.assertFalse(self.lock.exists())

    def test_frontend_symlink_cannot_escape_build_checkout(self):
        target = self.root / 'outside'
        self.frontend.rename(target)
        self.frontend.symlink_to(target, target_is_directory=True)
        with contextlib.chdir(self.root), self.assertRaisesRegex(RuntimeError, 'inside this build'):
            self.deps.start()
        self.assertEqual((target / 'pnpm-lock.yaml').read_bytes(), self.original)

    def test_audit_errors_and_incomplete_results_fail_closed(self):
        for report in ({}, {'error': {'code': 'ECONNRESET'}},
                       {'advisories': {}, 'metadata': {'vulnerabilities': {'high': 0}}},
                       {'advisories': {}, 'metadata': {'vulnerabilities': {
                           'info': 0, 'low': 0, 'moderate': 0, 'high': 1, 'critical': 0}}}):
            with self.subTest(report=report), self.assertRaises(RuntimeError):
                self.deps.validate_audit(report)

    def test_unapproved_high_vulnerability_blocks_build_and_lock_is_restorable(self):
        report = self.audit_report()
        report['metadata']['vulnerabilities']['high'] = 1
        report['advisories']['fixture'] = {'module_name': 'pk-unapproved-fixture', 'severity': 'high',
            'github_advisory_id': 'GHSA-fixture-unapproved', 'title': 'Fixture vulnerability'}
        with contextlib.chdir(self.root), patch.dict(os.environ, self.env, clear=True):
            with self.assertRaises(subprocess.CalledProcessError):
                self.installed(report)
            self.deps.restore()
        self.assertEqual(self.lock.read_bytes(), self.original)
        self.assertFalse((self.output / self.deps.REPORT).exists())

    def test_install_package_json_mutation_remains_visible(self):
        with contextlib.chdir(self.root), patch.dict(os.environ, self.env, clear=True):
            self.deps.start()
            package = self.frontend / 'package.json'
            package.write_text('{"name":"changed"}\n')
            with self.assertRaisesRegex(RuntimeError, 'modified package.json'):
                self.deps.audit()
            self.deps.restore()
            with self.assertRaisesRegex(RuntimeError, 'modified the source checkout'):
                control.verify_source_checkout(self.data)
        self.assertEqual(self.lock.read_bytes(), self.original)

    def test_evidence_tampering_missing_inputs_and_schema_downgrade_are_rejected(self):
        self.write_evidence(self.output, self.data)
        self.deps.verify(self.output, self.data)
        for filename in (*self.deps.FILES.values(), self.deps.REPORT):
            path, original = self.output / filename, (self.output / filename).read_bytes()
            path.write_bytes(original + b'tampered')
            with self.subTest(filename=filename), self.assertRaises(RuntimeError):
                self.deps.verify(self.output, self.data)
            path.write_bytes(original)
        for key in self.deps.BINDINGS:
            changed = dict(self.data)
            changed.pop(key)
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                self.deps.verify(self.output, changed)
        downgraded = {key: value for key, value in self.data.items()
                      if key != 'frontend_dependencies' and key not in self.deps.BINDINGS}
        downgraded['schema_version'] = 1
        with self.assertRaisesRegex(RuntimeError, 'schema 2'):
            self.deps.declaration(self.output, downgraded)
        with tempfile.TemporaryDirectory() as clean:
            self.deps.verify(Path(clean), {'schema_version': 1})
            with self.assertRaises(RuntimeError):
                self.deps.declaration(Path(clean), dict(self.data, schema_version=1))

    def test_runtime_must_bind_effective_lock_and_audit(self):
        self.write_evidence(self.output, self.data)
        self.data.update({key: 'bound-' + key for key in control.RUNTIME_BINDINGS if key not in self.data})
        proof = {key: self.data[key] for key in (*control.RUNTIME_BINDINGS, *self.deps.BINDINGS)}
        proof.update(schema_version=2, tests_profile='pk-artifact-v2', status='passed',
                     validation_profile=control.VALIDATION_PROFILE,
                     checks=[{'name': name, 'status': 'passed', 'exit_code': 0}
                             for name in sorted(control.RUNTIME_CHECKS)])
        path = self.output / 'artifact-validation.json'
        for key in self.deps.BINDINGS:
            mutated = dict(proof)
            mutated[key] = 'f' * 64
            self.deps.write_json(path, mutated)
            self.data['artifact_validation_sha256'] = self.deps.digest(path)
            with self.subTest(key=key), self.assertRaisesRegex(RuntimeError, 'runtime evidence'):
                control.package_evidence(self.output, self.data)
        self.deps.write_json(path, proof)
        self.data['artifact_validation_sha256'] = self.deps.digest(path)
        control.package_evidence(self.output, self.data)
        downgraded = {key: value for key, value in self.data.items()
                      if key != 'frontend_dependencies' and key not in self.deps.BINDINGS}
        downgraded['schema_version'] = 1
        for filename in (*self.deps.FILES.values(), self.deps.REPORT):
            (self.output / filename).unlink()
        with self.assertRaisesRegex(RuntimeError, 'cannot be downgraded'):
            control.package_evidence(self.output, downgraded)

    def test_manual_package_requires_the_same_dependency_evidence_bindings(self):
        self.write_evidence(self.output, self.data)
        self.data.update(binary_sha256='a' * 64, image_archive_sha256='b' * 64)
        proof = {key: self.data[key] for key in ('repository', 'commit', 'tree')}
        proof.update(schema_version=1, tests_profile='pk-local-v1', status='passed',
                     suites=[{'name': name, 'status': 'passed', 'exit_code': 0}
                             for name in sorted(control.REQUIRED_SUITES)],
                     artifact={key: self.data[key] for key in
                         ('package_id', 'binary_sha256', 'image_archive_sha256', *self.deps.BINDINGS)})
        path = self.root / (self.data['package_id'] + '.json')
        with contextlib.chdir(self.root), patch.dict(os.environ, PK_EVIDENCE_ROOT=str(self.root)):
            for key in self.deps.BINDINGS:
                changed = copy.deepcopy(proof)
                changed['artifact'].pop(key)
                self.deps.write_json(path, changed)
                with self.subTest(key=key), self.assertRaisesRegex(RuntimeError, 'dependency binding'):
                    control.evidence(self.data)
            self.deps.write_json(path, proof)
            self.assertEqual(control.evidence(self.data), proof)

    def test_actual_shell_install_failure_restores_lock_without_building(self):
        script = (ROOT / 'deploy/jenkins/package.sh').read_text()
        start = script.index('if [[ "$reinstall_dependencies" == yes ]]')
        end = script.index('docker run "' + '$' + '{common[@]}" --workdir /src/backend', start)
        header = ('set -euo pipefail\nroot=$1\nsource_dir=$root/source\n'
                  'dependency_helper=$2\nreinstall_dependencies=yes\ncommon=()\n'
                  'pnpm_cache=fixture\nnpm_cache=fixture\npnpm_version=9.15.9\nnode_image=fixture\n'
                  'docker() { return 17; }\n')
        result = subprocess.run(['bash', '-c', header + script[start:end], 'pk-install-failure',
                                 str(self.root), str(ROOT / 'deploy/jenkins/frontend_dependencies.py')],
                                cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 17, result.stderr)
        self.assertEqual(self.lock.read_bytes(), self.original)
        self.assertEqual(self.git('status', '--porcelain'), '')


class JenkinsPackageCleanupTest(unittest.TestCase):
    """Exercise the packaging cleanup against real, disposable Git histories."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='pk-package-cleanup-')
        self.addCleanup(self.temp.cleanup)
        self.source = Path(self.temp.name) / 'source with spaces'
        self.source.mkdir()
        self.env = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
        self.env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT='0')
        self.git('init', '--quiet', '--template=')
        (self.source / 'backend').mkdir()
        self.other = self.source / 'backend/source.go'
        self.staged = self.source / 'backend/staged.go'
        self.other.write_bytes(b'package fixture\n// original source\n')
        self.staged.write_bytes(b'package fixture\n// original staged source\n')
        self.git('add', '--', 'backend/source.go', 'backend/staged.go')
        self.git('commit', '--quiet', '-m', 'Fixture without a placeholder')
        self.placeholder = self.source / 'backend/internal/web/dist/.keep'
        self.placeholder_bytes = b'original tracked placeholder\n'

    def git(self, *arguments):
        return subprocess.run(
            ['git', '-c', 'user.name=PK cleanup fixture', '-c', 'user.email=fixture@example.invalid',
             '-c', 'commit.gpgsign=false', '-c', 'core.autocrlf=false', *arguments],
            cwd=self.source, env=self.env, check=True, capture_output=True, text=True,
        ).stdout.strip()

    def track_placeholder(self):
        self.placeholder.parent.mkdir(parents=True)
        self.placeholder.write_bytes(self.placeholder_bytes)
        self.git('add', '--', 'backend/internal/web/dist/.keep')
        self.git('commit', '--quiet', '-m', 'Fixture with a tracked placeholder')

    def cleanup(self):
        script = (ROOT / 'deploy/jenkins/package.sh').read_text()
        marker = '# Vite clears'
        self.assertEqual(script.count(marker), 1, 'Keep one explicit packaging cleanup boundary')
        section = script[script.index(marker):]
        # Execute the actual production section, never the Docker/build stages or a copy of its logic.
        return subprocess.run(
            ['bash', '-c', 'set -euo pipefail\nsource_dir=$1\n' + section,
             'pk-package-cleanup', str(self.source)],
            cwd=self.source, env=self.env, capture_output=True, text=True,
        )

    def test_absent_placeholder_succeeds_without_creating_or_overwriting_it(self):
        head = self.git('rev-parse', 'HEAD')
        result = self.cleanup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.placeholder.exists())
        self.assertFalse(self.placeholder.parent.exists())
        self.assertEqual(self.git('status', '--porcelain'), '')
        self.assertEqual(self.git('rev-parse', 'HEAD'), head)

        self.placeholder.parent.mkdir(parents=True)
        self.placeholder.write_bytes(b'untracked placeholder must stay untouched\n')
        result = self.cleanup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.placeholder.read_bytes(), b'untracked placeholder must stay untouched\n')
        self.assertEqual(self.git('ls-files', '--', 'backend/internal/web/dist/.keep'), '')

    def test_tracked_placeholder_deleted_by_vite_is_restored_exactly(self):
        self.track_placeholder()
        head = self.git('rev-parse', 'HEAD')
        self.placeholder.unlink()
        result = self.cleanup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.placeholder.read_bytes(), self.placeholder_bytes)
        self.assertEqual(self.git('status', '--porcelain'), '')
        self.assertEqual(self.git('rev-parse', 'HEAD'), head)

    def test_cleanup_preserves_unrelated_staged_unstaged_and_untracked_source(self):
        self.track_placeholder()
        self.placeholder.unlink()
        self.other.write_bytes(b'package fixture\n// modified source must remain visible to seal\n')
        self.staged.write_bytes(b'package fixture\n// staged source must remain visible to seal\n')
        self.git('add', '--', 'backend/staged.go')
        untracked = self.source / 'backend/untracked.go'
        untracked.write_bytes(b'package fixture\n// untracked source must not be cleaned\n')
        expected = {path: path.read_bytes() for path in (self.other, self.staged, untracked)}
        index_tree = self.git('write-tree')
        result = self.cleanup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.placeholder.read_bytes(), self.placeholder_bytes)
        for path, contents in expected.items():
            self.assertEqual(path.read_bytes(), contents)
        self.assertEqual(self.git('write-tree'), index_tree)
        self.assertEqual(self.git('diff', '--name-only'), 'backend/source.go')
        self.assertEqual(self.git('diff', '--cached', '--name-only'), 'backend/staged.go')
        self.assertEqual(self.git('ls-files', '--others', '--exclude-standard'), 'backend/untracked.go')

    def test_real_git_restore_failure_remains_a_packaging_failure(self):
        self.track_placeholder()
        self.placeholder.unlink()
        # A real Git lock permits HEAD inspection but prevents restoring the worktree.
        index = self.source / '.git/index'
        original_index = index.read_bytes()
        lock = self.source / '.git/index.lock'
        lock.write_bytes(b'held by another fixture operation\n')
        self.assertEqual(self.git('ls-tree', '--name-only', 'HEAD', '--',
                                  'backend/internal/web/dist/.keep'), 'backend/internal/web/dist/.keep')
        result = self.cleanup()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('index.lock', result.stderr)
        self.assertFalse(self.placeholder.exists())
        self.assertEqual(index.read_bytes(), original_index)
        self.assertEqual(lock.read_bytes(), b'held by another fixture operation\n')
        lock.unlink()
        result = self.cleanup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.placeholder.read_bytes(), self.placeholder_bytes)
        self.assertEqual(self.git('status', '--porcelain'), '')


class JenkinsRuntimeTest(unittest.TestCase):
    """Check archive identity chains and isolation without a Docker daemon."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='pk-runtime-fixture-')
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)

    @staticmethod
    def encode(value):
        return json.dumps(value, separators=(',', ':')).encode()

    def test_runtime_version_accepts_current_and_legacy_brand_with_exact_identity(self):
        validator = runtime.Validator(self.directory, {'checks': []})
        validator.data = {'version': '9.8.7-pk.2', 'commit': 'a' * 40}
        for brand in ('DP API', 'Sub2API'):
            output = f"{brand} 9.8.7-pk.2 (commit: {'a' * 40}, built: 2026-10-21)"
            with self.subTest(brand=brand), patch.object(validator, 'container', return_value=output):
                validator.version()
            for invalid in (output.replace('9.8.7-pk.2', '9.8.7-pk.1'),
                            output.replace('a' * 40, 'b' * 40),
                            output.replace(brand, 'OtherAPI')):
                with self.subTest(output=invalid), patch.object(validator, 'container', return_value=invalid):
                    with self.assertRaisesRegex(RuntimeError, 'Runtime version or commit mismatch'):
                        validator.version()

    def archive(self, *, oci=True, bad_attestation=False, binary_mode=0o555, inline_attestation=False):
        self.files, self.paths = {}, {}
        binary = b'PK fixture binary; never executed'
        self.data = {'repository': runtime.REPOSITORY, 'commit': 'a' * 40, 'version': '9.8.7-pk.1',
                     'image_tag': 'ghcr.io/' + runtime.REPOSITORY + ':9.8.7-pk.1',
                     'image_archive': 'image.tar.gz', 'binary_sha256': hashlib.sha256(binary).hexdigest()}
        layer_stream = io.BytesIO()
        with tarfile.open(fileobj=layer_stream, mode='w') as layer:
            member = tarfile.TarInfo('app/sub2api')
            member.size, member.mode = len(binary), binary_mode
            member.uid = member.gid = 0
            layer.addfile(member, io.BytesIO(binary))
        layer_bytes = layer_stream.getvalue()

        def blob(payload, media_type, label):
            checksum = hashlib.sha256(payload).hexdigest()
            name = 'blobs/sha256/' + checksum
            self.files[name] = payload
            self.paths[label] = name
            return {'mediaType': media_type, 'digest': 'sha256:' + checksum, 'size': len(payload)}

        layer = blob(gzip.compress(layer_bytes, mtime=0), 'application/vnd.oci.image.layer.v1.tar+gzip', 'layer')
        config_type = 'application/vnd.oci.image.config.v1+json'
        manifest_type = 'application/vnd.oci.image.manifest.v1+json'
        index_type = 'application/vnd.oci.image.index.v1+json'
        config = blob(self.encode({
            'os': 'linux', 'architecture': 'amd64',
            'config': {'Labels': {
                'org.opencontainers.image.source': 'https://github.com/' + runtime.REPOSITORY,
                'org.opencontainers.image.version': self.data['version'],
                'org.opencontainers.image.revision': self.data['commit']},
                'Entrypoint': ['/app/docker-entrypoint.sh'], 'Cmd': ['/app/sub2api'],
                'Env': ['SUB2API_DOCKER_SELF_UPDATE=true']},
            'rootfs': {'type': 'layers', 'diff_ids': ['sha256:' + hashlib.sha256(layer_bytes).hexdigest()]},
        }), config_type, 'config')
        self.data['image_id'] = config['digest']
        self.files['manifest.json'] = self.encode([{'Config': self.paths['config'],
            'RepoTags': [self.data['image_tag']], 'Layers': [self.paths['layer']]}])
        if oci:
            image = blob(self.encode({'schemaVersion': 2, 'mediaType': manifest_type,
                                     'config': config, 'layers': [layer]}), manifest_type, 'image')
            image['platform'] = {'os': 'linux', 'architecture': 'amd64'}
            attestation_config = blob(b'{}', 'application/vnd.oci.empty.v1+json', 'attestation_config')
            attestation_config['data'] = 'e30='
            if inline_attestation:
                del self.files[self.paths['attestation_config']]
            attestation_layer = blob(b'{"predicateType":"fixture"}', 'application/vnd.in-toto+json', 'attestation_layer')
            attestation = blob(self.encode({'schemaVersion': 2, 'mediaType': manifest_type,
                                           'config': attestation_config, 'layers': [attestation_layer]}),
                               manifest_type, 'attestation')
            attestation.update(platform={'os': 'unknown', 'architecture': 'unknown'}, annotations={
                'vnd.docker.reference.type': 'attestation-manifest',
                'vnd.docker.reference.digest': 'sha256:' + 'f' * 64 if bad_attestation else image['digest'],
            })
            root = blob(self.encode({'schemaVersion': 2, 'mediaType': index_type,
                                     'manifests': [image, attestation]}), index_type, 'root')
            self.data['image_id'] = root['digest']
            root['annotations'] = {'io.containerd.image.name': self.data['image_tag'],
                                   'org.opencontainers.image.ref.name': self.data['version']}
            self.files['index.json'] = self.encode({'schemaVersion': 2, 'mediaType': index_type, 'manifests': [root]})
        self.write_archive()
        validator = runtime.Validator(self.directory, {'checks': []})
        validator.data = dict(self.data)
        return validator

    def write_archive(self):
        with tarfile.open(self.directory / self.data['image_archive'], 'w:gz') as archive:
            for name, payload in self.files.items():
                member = tarfile.TarInfo(name)
                member.size = len(payload)
                archive.addfile(member, io.BytesIO(payload))

    def test_classic_config_and_docker29_index_identities_are_verified(self):
        for oci in (False, True):
            with self.subTest(oci=oci):
                self.archive(oci=oci).provenance()

    def test_recorded_image_identity_cannot_be_ignored(self):
        for oci in (False, True):
            validator = self.archive(oci=oci)
            validator.data['image_id'] = 'sha256:' + 'f' * 64
            with self.subTest(oci=oci), self.assertRaises(RuntimeError):
                validator.provenance()

    def test_buildkit_empty_attestation_config_can_be_authenticated_inline(self):
        self.archive(inline_attestation=True).provenance()

    def test_oci_root_layers_and_attestation_blobs_are_authenticated(self):
        for label in ('root', 'layer', 'attestation_layer'):
            validator = self.archive()
            path = self.paths[label]
            self.files[path] = bytes([self.files[path][0] ^ 1]) + self.files[path][1:]
            self.write_archive()
            with self.subTest(label=label), self.assertRaisesRegex(RuntimeError, 'digest mismatch'):
                validator.provenance()

    def test_oci_descriptor_size_and_compatibility_paths_must_match(self):
        validator = self.archive()
        index = json.loads(self.files['index.json'])
        index['manifests'][0]['size'] += 1
        self.files['index.json'] = self.encode(index)
        self.write_archive()
        with self.assertRaisesRegex(RuntimeError, 'size mismatch'):
            validator.provenance()
        validator = self.archive()
        compatibility = json.loads(self.files['manifest.json'])
        self.files['alias-config.json'] = self.files[self.paths['config']]
        compatibility[0]['Config'] = 'alias-config.json'
        self.files['manifest.json'] = self.encode(compatibility)
        self.write_archive()
        with self.assertRaisesRegex(RuntimeError, 'packaged image config'):
            validator.provenance()

    def test_attestation_cannot_refer_to_another_image(self):
        with self.assertRaisesRegex(RuntimeError, 'Attestation refers'):
            self.archive(bad_attestation=True).provenance()

    def test_binary_digest_and_permissions_remain_protected(self):
        validator = self.archive()
        validator.data['binary_sha256'] = 'f' * 64
        with self.assertRaisesRegex(RuntimeError, 'exact update binary'):
            validator.provenance()
        with self.assertRaisesRegex(RuntimeError, 'root-owned and read-only'):
            self.archive(binary_mode=0o755).provenance()

    def test_container_isolation_and_default_entrypoint_capabilities(self):
        validator = self.archive()
        for default_user in (False, True):
            with patch.object(validator, 'docker', return_value='created') as docker:
                validator.create('sh', '-ec', 'test "$(id -u)" = 1000', default_user=default_user)
                command = docker.call_args.args
            for flag in ('--network=none', '--cap-drop=ALL', '--security-opt=no-new-privileges', '--read-only', '--no-healthcheck'):
                self.assertIn(flag, command)
            added = {flag for flag in command if flag.startswith('--cap-add=')}
            self.assertEqual(added, {'--cap-add=CHOWN', '--cap-add=SETUID', '--cap-add=SETGID'} if default_user else set())
            self.assertEqual('--user=1000:1000' in command, not default_user)
            self.assertIn('type=volume,src=' + validator.volume + ',dst=/app/data', command)

    def test_fixture_changes_seed_without_running_or_building(self):
        validator = self.archive()
        validator.data['binary_archive'] = 'binary.tar.gz'
        with tarfile.open(self.directory / 'binary.tar.gz', 'w:gz') as archive:
            member = tarfile.TarInfo('sub2api')
            member.size, member.mode = 5, 0o555
            archive.addfile(member, io.BytesIO(b'image'))
        calls = []
        def docker(*args, **kwargs):
            calls.append(args)
            if args[0] == 'image':
                return 'sha256:' + 'a' * 64
            if args[0] == 'cp':
                self.assertTrue(Path(args[1]).is_file())
            return 'created'
        with patch.object(validator, 'docker', side_effect=docker):
            self.assertEqual(validator.fixture_image(), 'sha256:' + 'a' * 64)
        self.assertEqual([args[0] for args in calls], ['create', 'cp', 'commit', 'image', 'rm'])
        self.assertFalse(validator.containers)
        self.assertNotIn('--mount', calls[0])

    def test_cleanup_keeps_trying_and_never_removes_original_image(self):
        validator = self.archive()
        validator.docker_started = validator.volume_created = validator.fixture_created = True
        validator.containers = ['pk-owned-one', 'pk-owned-two']
        calls = []
        def docker(*args, **kwargs):
            calls.append(args)
            if args[-1] == 'pk-owned-one':
                raise RuntimeError('fixture cleanup error')
            return ''
        with patch.object(validator, 'docker', side_effect=docker), self.assertRaisesRegex(RuntimeError, 'Runtime cleanup failed'):
            validator.cleanup()
        self.assertEqual(calls, [('rm', '--force', 'pk-owned-one'), ('rm', '--force', 'pk-owned-two'),
                                 ('image', 'rm', '--force', validator.fixture), ('volume', 'rm', validator.volume)])
        self.assertFalse(any(validator.data['image_id'] in args for args in calls))


if __name__ == '__main__':
    unittest.main()
