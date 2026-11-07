//go:build linux && unit

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateServiceLinuxStartupGuardOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard")
	require.NoError(t, os.WriteFile(path, []byte("fixture"), 0644))
	info, err := os.Lstat(path)
	require.NoError(t, err)
	if os.Geteuid() == 0 {
		require.NoError(t, verifyStartupGuardOwnership(path, info))
	} else {
		require.Error(t, verifyStartupGuardOwnership(path, info), "an application-owned guard is untrusted")
	}

	require.NoError(t, os.Chmod(path, 0666))
	info, err = os.Lstat(path)
	require.NoError(t, err)
	require.Error(t, verifyStartupGuardOwnership(path, info), "writable guard is rejected")
	require.NoError(t, os.Chmod(path, 0644))

	link := path + ".hardlink"
	require.NoError(t, os.Link(path, link))
	info, err = os.Lstat(path)
	require.NoError(t, err)
	require.Error(t, verifyStartupGuardOwnership(path, info), "a second name must not bypass inode protection")
	require.NoError(t, os.Remove(link))

	if os.Geteuid() == 0 {
		require.NoError(t, os.Chown(path, 65534, 65534))
		info, err = os.Lstat(path)
		require.NoError(t, err)
		require.Error(t, verifyStartupGuardOwnership(path, info), "non-root ownership must fail on Linux")
		require.NoError(t, os.Chown(path, 0, 0))
	}
}

func TestUpdateServiceLinuxStartupGuardNoFollow(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "guard")
	require.NoError(t, os.WriteFile(path, []byte("fixture"), 0644))
	link := filepath.Join(directory, "link")
	require.NoError(t, os.Symlink(path, link))
	opened, err := openStartupInspectionFile(link)
	require.Error(t, err)
	require.Nil(t, opened, "O_NOFOLLOW must reject the final symlink before reading")
	require.True(t, errors.Is(err, syscall.ELOOP))

	fifo := filepath.Join(directory, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0600))
	// The platform opener is nonblocking even if a file is replaced by a FIFO.
	opened, err = openStartupInspectionFile(fifo)
	require.NoError(t, err)
	require.NoError(t, opened.Close())
	_, _, err = readStableFile(context.Background(), fifo, maxStartupGuardSize, true)
	require.Error(t, err, "the stable reader refuses nonregular files")
}

func TestUpdateServiceLinuxStartupGuardRealTrust(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("positive ownership verification requires isolated Linux root fixtures")
	}
	// /tmp is intentionally world-writable and must not be accepted as a guard
	// ancestor. Use a unique private directory under root's home for this case.
	directory, err := os.MkdirTemp("/root", ".sub2api-startup-guard-test-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(directory)) })
	install := filepath.Join(directory, "sub2api")
	guardPath := filepath.Join(directory, "entrypoint.sh")
	require.NoError(t, os.WriteFile(install, []byte("current"), 0755))
	require.NoError(t, os.WriteFile(guardPath, []byte(updateGuardScript(updateGuardHash("current"))), 0644))
	svc := newUpdateServiceWithExecutable(nil, nil, "0.2.19-pk.57", "release", func() (string, error) {
		return install, nil
	})
	svc.startupGuardPath = guardPath
	// Leave startupGuardTrust nil: production Linux ownership checking executes.
	snapshot, err := svc.checkStartupAllowed(context.Background(), install)
	require.NoError(t, err)
	require.Equal(t, updateGuardHash("current"), snapshot.sha256)
	require.NoError(t, checkObservedPath(snapshot))

	require.NoError(t, os.Chmod(guardPath, 0666))
	_, err = svc.checkStartupAllowed(context.Background(), install)
	require.ErrorIs(t, err, ErrStartupGuardInvalid)
	require.NoError(t, os.Chmod(guardPath, 0644))
	require.NoError(t, os.Chmod(directory, 0777))
	_, err = svc.checkStartupAllowed(context.Background(), install)
	require.ErrorIs(t, err, ErrStartupGuardInvalid, "writable ancestors must be refused")
	require.NoError(t, os.Chmod(directory, 0700))
}
