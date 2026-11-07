"""Real Linux openat/rename/lock tests in a unique root-private local directory.

No Docker, network, production configuration, processes, or services are used.
Run explicitly as Linux root; ordinary cross-platform suite reports skips.
"""
import importlib.util
import os
from pathlib import Path
import stat
import sys
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location('startup_guard_release', str(
    Path(__file__).resolve().parents[1] / 'startup-guard-release.py'))
guard = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(guard)


@unittest.skipUnless(sys.platform.startswith('linux') and os.geteuid() == 0,
                     'requires isolated local Linux root filesystem')
class LinuxFileTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='.guard-release-test-', dir='/root')
        self.directory = self.temporary.name
        self.parent = guard.secure_directory(self.directory)

    def tearDown(self):
        os.close(self.parent)
        self.temporary.cleanup()

    def test_atomic_rename_preserves_open_old_inode_and_publishes_new_inode(self):
        guard.atomic_write_at(self.parent, 'guard', b'old', 0o755, None)
        fd = os.open('guard', os.O_RDONLY, dir_fd=self.parent)
        try:
            previous_inode = os.fstat(fd).st_ino
            guard.atomic_write_at(self.parent, 'guard', b'new', 0o755, guard.sha(b'old'))
            self.assertEqual(os.read(fd, 3), b'old')
            self.assertEqual(guard.read_at(self.parent, 'guard', 0o755), b'new')
            info = os.stat('guard', dir_fd=self.parent)
            self.assertNotEqual(previous_inode, info.st_ino)
            self.assertEqual((info.st_uid, info.st_gid, stat.S_IMODE(info.st_mode)), (0, 0, 0o755))
        finally:
            os.close(fd)

    def test_compare_failure_does_not_change_existing_file(self):
        guard.atomic_write_at(self.parent, 'guard', b'external', 0o755, None)
        before = os.stat('guard', dir_fd=self.parent)
        with self.assertRaisesRegex(guard.Refused, 'destination_compare_failed'):
            guard.atomic_write_at(self.parent, 'guard', b'new', 0o755, guard.sha(b'expected'))
        self.assertEqual(guard.file_identity(before), guard.file_identity(os.stat('guard', dir_fd=self.parent)))
        self.assertEqual(guard.read_at(self.parent, 'guard', 0o755), b'external')
        self.assertEqual(os.listdir(self.directory), ['guard'])

    def test_symlink_target_and_parent_rejected(self):
        guard.atomic_write_at(self.parent, 'target', b'original', 0o600, None)
        os.symlink('target', self.directory + '/link')
        with self.assertRaises((OSError, guard.Refused)):
            guard.secure_read(self.directory + '/link', 0o600)
        with self.assertRaises((OSError, guard.Refused)):
            guard.atomic_write_at(self.parent, 'link', b'bad', 0o600, guard.sha(b'original'))
        os.mkdir(self.directory + '/real', 0o700)
        os.symlink('real', self.directory + '/linked-directory')
        with self.assertRaises((OSError, guard.Refused)):
            guard.secure_directory(self.directory + '/linked-directory')
        self.assertEqual(guard.read_at(self.parent, 'target', 0o600), b'original')

    def test_private_mode_and_fifo_rejected(self):
        guard.atomic_write_at(self.parent, 'receipt', b'{}', 0o600, None)
        os.chmod(self.directory + '/receipt', 0o644)
        with self.assertRaisesRegex(guard.Refused, 'unexpected_file_mode'):
            guard.secure_read(self.directory + '/receipt', 0o600)
        os.chmod(self.directory + '/receipt', 0o666)
        with self.assertRaisesRegex(guard.Refused, 'unsafe_file_owner_or_mode'):
            guard.secure_read(self.directory + '/receipt')
        os.mkfifo(self.directory + '/fifo', 0o600)
        with self.assertRaisesRegex(guard.Refused, 'unsafe_file_type'):
            guard.secure_read(self.directory + '/fifo')

    def test_readonly_lock_does_not_create_and_conflicts_with_writer(self):
        host = guard.LinuxHost({'lock_path': self.directory + '/lock'})
        with self.assertRaises(FileNotFoundError):
            with host.lock(write=False):
                self.fail('missing readonly lock unexpectedly created')
        self.assertEqual(os.listdir(self.directory), [])
        with host.lock(write=True):
            with self.assertRaisesRegex(guard.Refused, 'guard_installation_lock_busy'):
                with host.lock(write=False):
                    self.fail('shared reader admitted during exclusive transaction')
        before = os.stat(self.directory + '/lock')
        with host.lock(write=False):
            pass
        self.assertEqual(guard.file_identity(before), guard.file_identity(os.stat(self.directory + '/lock')))

    def test_publish_fsync_failure_is_observable_and_bytes_recoverable(self):
        guard.atomic_write_at(self.parent, 'guard', b'old', 0o755, None)
        real_fsync = os.fsync
        def fail_directory(fd):
            if fd == self.parent:
                raise OSError('simulated directory fsync failure after rename')
            return real_fsync(fd)
        with mock.patch.object(guard.os, 'fsync', side_effect=fail_directory):
            with self.assertRaises(OSError):
                guard.atomic_write_at(self.parent, 'guard', b'new', 0o755, guard.sha(b'old'))
        self.assertEqual(guard.read_at(self.parent, 'guard', 0o755), b'new')
        guard.atomic_write_at(self.parent, 'guard', b'old', 0o755, guard.sha(b'new'))
        self.assertEqual(guard.read_at(self.parent, 'guard', 0o755), b'old')
        self.assertEqual(os.listdir(self.directory), ['guard'])


if __name__ == '__main__':
    unittest.main()
