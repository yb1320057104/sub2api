//go:build linux

package service

import (
	"fmt"
	"os"
	"syscall"
)

func openStartupInspectionFile(path string) (*os.File, error) {
	// NOFOLLOW closes the lstat/open symlink race; NONBLOCK avoids hanging if
	// a regular file is swapped for a FIFO before the open. The caller still
	// verifies the opened descriptor's identity and regular-file mode.
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func verifyStartupGuardOwnership(_ string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("startup guard and ancestors must be root-owned and not group/world writable")
	}
	if info.Mode().IsRegular() && stat.Nlink != 1 {
		return fmt.Errorf("startup guard must not have hard links")
	}
	return nil
}

func samePlatformFileMetadata(a, b os.FileInfo) bool {
	left, leftOK := a.Sys().(*syscall.Stat_t)
	right, rightOK := b.Sys().(*syscall.Stat_t)
	return leftOK && rightOK && left.Ctim == right.Ctim && left.Uid == right.Uid && left.Gid == right.Gid && left.Nlink == right.Nlink
}

func samePlatformDirectoryMetadata(a, b os.FileInfo) bool {
	left, leftOK := a.Sys().(*syscall.Stat_t)
	right, rightOK := b.Sys().(*syscall.Stat_t)
	return leftOK && rightOK && left.Uid == right.Uid && left.Gid == right.Gid
}
