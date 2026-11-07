//go:build !linux

package service

import (
	"fmt"
	"os"
)

func openStartupInspectionFile(path string) (*os.File, error) { return os.Open(path) }

func verifyStartupGuardOwnership(_ string, _ os.FileInfo) error {
	return fmt.Errorf("managed startup guard requires Linux ownership verification")
}

func samePlatformFileMetadata(_, _ os.FileInfo) bool { return true }

func samePlatformDirectoryMetadata(_, _ os.FileInfo) bool { return true }
