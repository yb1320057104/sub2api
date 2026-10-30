// Package browserruntime installs the browser carried by official Linux releases.
// The payload is part of the checksummed executable so older web updaters, which
// extract only sub2api, also deliver it without another download or root access.
package browserruntime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	footerMagic = "PK_BROWSER_V1!!!"
	footerSize  = 16 + 8 + sha256.Size
	maxPayload  = 350 << 20
	maxExpanded = 1536 << 20
	maxFile     = 512 << 20
)

var installed struct {
	sync.RWMutex
	path string
}

// Path is read-only: API polling and concurrent jobs never start extraction.
func Path() string {
	installed.RLock()
	defer installed.RUnlock()
	return installed.path
}

func ready(directory, checksum string) bool {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	marker, err := os.ReadFile(filepath.Join(directory, ".ready"))
	if err != nil || string(marker) != checksum {
		return false
	}
	for _, name := range []string{"launch", "lib/ld-musl-x86_64.so.1", "usr/lib/chromium/chrome"} {
		info, err := os.Lstat(filepath.Join(directory, filepath.FromSlash(name)))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return false
		}
	}
	return true
}

// Prepare runs before serving requests. Revisions use separate directories so
// binary rollback selects its own runtime and never rewrites a running browser.
// Source builds without a payload retain the existing system-browser fallback.
func Prepare(dataDir string) (string, error) {
	installed.Lock()
	defer installed.Unlock()
	if installed.path != "" {
		return installed.path, nil
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return "", nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	file, err := os.Open(executable)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() < footerSize {
		return "", nil
	}
	footer := make([]byte, footerSize)
	if _, err = file.ReadAt(footer, info.Size()-footerSize); err != nil {
		return "", err
	}
	if string(footer[:16]) != footerMagic {
		return "", nil
	}
	size := binary.BigEndian.Uint64(footer[16:24])
	if size == 0 || size > maxPayload || int64(size)+footerSize >= info.Size() {
		return "", errors.New("invalid bundled browser payload size")
	}
	offset := info.Size() - footerSize - int64(size)
	hash := sha256.New()
	if _, err = io.Copy(hash, io.NewSectionReader(file, offset, int64(size))); err != nil {
		return "", err
	}
	checksum := hex.EncodeToString(footer[24:])
	if hex.EncodeToString(hash.Sum(nil)) != checksum {
		return "", errors.New("bundled browser checksum mismatch")
	}
	root, err := filepath.Abs(filepath.Join(dataDir, "browser"))
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("create browser data directory: %w", err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("browser data directory must be a real directory")
	}
	target := filepath.Join(root, checksum)
	if !ready(target, checksum) {
		stage, err := os.MkdirTemp(root, ".install-")
		if err != nil {
			return "", err
		}
		defer func() { _ = os.RemoveAll(stage) }()
		if err = extract(io.NewSectionReader(file, offset, int64(size)), stage, target); err != nil {
			return "", fmt.Errorf("unpack bundled browser: %w", err)
		}
		if err = os.WriteFile(filepath.Join(stage, ".ready"), []byte(checksum), 0600); err != nil {
			return "", err
		}
		if !ready(stage, checksum) {
			return "", errors.New("bundled browser is incomplete")
		}
		if err = os.Rename(stage, target); err != nil && !ready(target, checksum) {
			return "", fmt.Errorf("activate bundled browser: %w", err)
		}
	}
	installed.path = filepath.Join(target, "launch")
	return installed.path, nil
}

func extract(source io.Reader, directory, target string) error {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	limited := &io.LimitedReader{R: gz, N: maxExpanded + 1}
	archive := tar.NewReader(limited)
	seen := make(map[string]bool)
	regular := make(map[string]os.FileMode)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := header.Name
		if name == "" || path.Clean(name) != name || path.IsAbs(name) || strings.Contains(name, "\\") ||
			name == ".." || strings.HasPrefix(name, "../") || name == ".ready" || seen[name] || len(seen) >= 25000 {
			return errors.New("unsafe or duplicate browser archive member")
		}
		seen[name] = true
		if header.Mode != 0644 && header.Mode != 0755 {
			return errors.New("invalid browser archive permissions")
		}
		destination := filepath.Join(directory, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		if header.Typeflag == tar.TypeLink {
			// Only alias an earlier regular file from this same private staging
			// directory. No symlinks, external targets or link chains are accepted.
			mode, ok := regular[header.Linkname]
			if !ok || mode != os.FileMode(header.Mode) || header.Size != 0 {
				return errors.New("invalid internal browser hardlink")
			}
			if err = os.Link(filepath.Join(directory, filepath.FromSlash(header.Linkname)), destination); err != nil {
				return err
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maxFile {
			return errors.New("browser archive contains an unsupported or oversized member")
		}
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode))
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, archive, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if header.Mode == 0755 {
			if err = relocate(destination, filepath.Join(target, "lib", "ld-musl-x86_64.so.1")); err != nil {
				return err
			}
		}
		regular[name] = os.FileMode(header.Mode)
	}
	// Consume the gzip trailer too; truncated or over-expanded data must not activate.
	if _, err = io.Copy(io.Discard, limited); err != nil {
		return err
	}
	if limited.N <= 0 {
		return errors.New("browser archive exceeds expanded size limit")
	}
	return nil
}

// Jenkins reserves space in PT_INTERP. Filling it here lets the kernel launch
// Chrome normally: /proc/self/exe and all browser resource paths remain correct.
// Directly invoking ld-musl would make Chrome mistake the loader for itself.
func relocate(filename, loader string) error {
	file, err := os.OpenFile(filename, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	magic := make([]byte, 4)
	if _, err = file.ReadAt(magic, 0); err != nil || string(magic) != "\x7fELF" {
		return nil
	}
	binary, err := elf.NewFile(file)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	for _, program := range binary.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		if program.Filesz == 0 || program.Filesz > 4096 || program.Off > uint64(info.Size()) || program.Filesz > uint64(info.Size())-program.Off {
			return errors.New("invalid browser interpreter segment")
		}
		original, err := io.ReadAll(program.Open())
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(original), "/__PK_BROWSER_LOADER__/") {
			return nil
		}
		if len(loader)+1 > len(original) {
			return errors.New("browser data path exceeds reserved loader path space")
		}
		replacement := make([]byte, len(original))
		copy(replacement, loader)
		_, err = file.WriteAt(replacement, int64(program.Off))
		return err
	}
	return nil
}

// Check renders a local data URL; Jenkins runs this with networking disabled in
// a bare old Alpine image which has no installed browser or browser libraries.
func Check(dataDir string) error {
	browser, err := Prepare(dataDir)
	if err != nil {
		return err
	}
	if browser == "" {
		return errors.New("executable does not contain a browser runtime")
	}
	profile, err := os.MkdirTemp("", "pk-browser-check-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(profile) }()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, browser, "--headless=new", "--no-sandbox", "--disable-gpu",
		"--disable-dev-shm-usage", "--disable-background-networking", "--no-first-run",
		"--no-default-browser-check", "--user-data-dir="+profile,
		"--dump-dom", "data:text/html,<title>pk-browser-ready</title><p>ready</p>")
	cmd.Env = append(os.Environ(), "HOME="+profile, "XDG_CACHE_HOME="+filepath.Join(profile, "cache"), "XDG_CONFIG_HOME="+filepath.Join(profile, "config"))
	cmd.WaitDelay = 3 * time.Second
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("bundled browser rendering failed: %w", err)
	}
	if !strings.Contains(string(output), "<title>pk-browser-ready</title>") {
		return errors.New("bundled browser did not render the local page")
	}
	return nil
}
