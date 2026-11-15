package reauthruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func extract(data []byte, root string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(h.Name)
		if name == "." && h.Typeflag == tar.TypeDir {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return errors.New("unsafe archive path")
		}
		path := filepath.Join(root, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if h.Size < 0 || h.Size > maxExtracted-total {
				return errors.New("runtime too large")
			}
			total += h.Size
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if h.Mode&0111 != 0 {
				mode = 0700
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(f, tr, h.Size)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return errors.New("runtime archive must not contain links or devices")
		}
	}
}

// Revalidate installed bytes against the authenticated archive on each process
// start. Checking only a marker leaves modified scripts and shared libraries
// executable even when the new binary is pinned to an immutable release.
func verifyInstalledArchive(ctx context.Context, data []byte, root string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	allowed := map[string]bool{"ready": true}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if err == io.EOF {
			return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				name, err := filepath.Rel(root, path)
				if err != nil || (name != "." && !allowed[name]) || entry.Type()&os.ModeSymlink != 0 {
					return errors.New("runtime contains an unverified path")
				}
				return nil
			})
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(h.Name)
		if name == "." && h.Typeflag == tar.TypeDir {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || seen[name] {
			return errors.New("invalid runtime archive path")
		}
		seen[name] = true
		for allowedName := name; allowedName != "."; allowedName = filepath.Dir(allowedName) {
			allowed[allowedName] = true
		}
		path := root
		for _, component := range strings.Split(name, string(filepath.Separator)) {
			path = filepath.Join(path, component)
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("runtime path is missing or linked")
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if !info.IsDir() {
				return errors.New("runtime directory changed")
			}
		case tar.TypeReg:
			if h.Size < 0 || h.Size > maxExtracted-total || !info.Mode().IsRegular() || info.Size() != h.Size ||
				(h.Mode&0111 != 0) != (info.Mode().Perm()&0100 != 0) {
				return errors.New("runtime file metadata changed")
			}
			total += h.Size
			expected, actual := sha256.New(), sha256.New()
			if _, err := io.CopyN(expected, tr, h.Size); err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(actual, io.LimitReader(f, h.Size+1))
			closeErr := f.Close()
			if copyErr != nil || closeErr != nil || !bytes.Equal(expected.Sum(nil), actual.Sum(nil)) {
				return errors.New("runtime file bytes changed")
			}
		default:
			return errors.New("runtime archive contains links or devices")
		}
	}
}
