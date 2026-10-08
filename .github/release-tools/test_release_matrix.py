import argparse
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
    """Fail closed before publishing when a source or artifact binding changes."""

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
            'source_note_blob': 'e' * 40, 'validation_profile': control.VALIDATION_PROFILE,
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

    def test_missing_or_changed_sealed_report_is_rejected(self):
        for filename in ('source-validation.json', 'artifact-validation.json'):
            path = self.directory / filename
            original = path.read_bytes()
            for replacement in (None, original + b' '):
                if replacement is None:
                    path.unlink()
                else:
                    path.write_bytes(replacement)
                with self.subTest(filename=filename, missing=replacement is None), self.assertRaises(RuntimeError):
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

    def test_runtime_requires_cleanup_and_every_unique_check(self):
        original = copy.deepcopy(self.runtime)
        for changes in (dict(status='failed'), dict(checks=self.runtime['checks'][:-1]),
                        dict(checks=[self.runtime['checks'][0]] * len(control.RUNTIME_CHECKS)),
                        dict(checks=[dict(item, status='failed') if item['name'] == 'cleanup' else item
                                     for item in self.runtime['checks']])):
            self.runtime = {**copy.deepcopy(original), **changes}
            self.save_runtime()
            with self.subTest(changes=list(changes)), self.assertRaises(RuntimeError):
                control.package_evidence(self.directory, self.data)

    def test_candidate_never_reaches_release_api(self):
        with patch.object(control, 'request') as api:
            with self.assertRaises(RuntimeError):
                control.release_ready(dict(self.data, build_type='candidate'))
            api.assert_not_called()


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

    def archive(self, *, oci=True, bad_attestation=False, binary_mode=0o555, inline_attestation=False):
        self.files, self.paths = {}, {}
        binary = b'PK fixture binary; never executed'
        self.data = {'commit': 'a' * 40, 'version': '9.8.7-pk.1',
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
