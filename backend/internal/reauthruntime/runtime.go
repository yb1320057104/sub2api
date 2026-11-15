// Package reauthruntime installs and supervises the release-matched login
// runtime. Account credentials remain in the authenticated worker protocol.
package reauthruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"
)

const maxArchive = 256 << 20
const maxExtracted = 768 << 20

// These values are set together by the release build. The application version
// may contain an HA suffix, but the independently packaged runtime always uses
// this explicit release binding. An unbound binary cannot install executable
// code from the network.
var (
	ReleaseVersion      string
	ArchiveSHA256       string
	ArchiveArchitecture string
)

const runtimeRepository = "yb1320057104/sub2api"

type preparationError struct{ reason string }

func (e preparationError) Error() string { return e.reason }

func preparationFailure(reason string) error { return preparationError{reason: reason} }

func preparationReason(err error) string {
	var failure preparationError
	if errors.As(err, &failure) {
		return failure.reason
	}
	return "runtime_install_failed"
}

type Status struct {
	StartedAt int64  `json:"-"`
	Mode      string `json:"mode"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
}

type Manager struct {
	mu                            sync.Mutex
	status                        Status
	root, version, baseURL, token string
	digest, architecture          string
	ctx                           context.Context
	cancel                        context.CancelFunc
	done                          chan struct{}
	started                       bool
	stopping                      bool
	process                       *exec.Cmd
	retryAt                       time.Time
	client                        *http.Client
}

func New(root, _ string, baseURL, token string) *Manager {
	root, _ = filepath.Abs(root)
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{root: root, version: ReleaseVersion, digest: ArchiveSHA256, architecture: ArchiveArchitecture, baseURL: baseURL, token: token,
		ctx: ctx, cancel: cancel, client: &http.Client{Timeout: 5 * time.Minute},
		status: Status{Mode: "managed", State: "idle"}}
}

func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *Manager) set(state, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state == "running" {
		m.status.StartedAt = time.Now().UnixNano()
	}
	m.status.State = state
	m.status.Reason = reason
}

// Ensure never performs downloads on the request goroutine and retries failed
// preparation at most once per minute.
func (m *Manager) Ensure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.stopping || m.ctx.Err() != nil || time.Now().Before(m.retryAt) {
		return
	}
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		m.status.State = "unavailable"
		m.status.Reason = "unsupported_platform"
		return
	}
	if err := m.validateBinding(); err != nil {
		m.status.State = "unavailable"
		m.status.Reason = preparationReason(err)
		return
	}
	m.started = true
	m.status.State = "preparing"
	m.status.Reason = ""
	m.done = make(chan struct{})
	go m.run()
}

func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopping = true
	done := m.done
	process := m.process
	if process == nil {
		m.cancel() // Preparation has no claimed task and can be interrupted.
	}
	m.mu.Unlock()
	if process != nil {
		// The Python supervisor stops claiming and waits for its active slots.
		// Keep its callback HTTP server and command context alive until then.
		_ = stopProcess(process)
	}
	if done != nil {
		<-done
	}
	m.cancel()
}

func (m *Manager) run() {
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.started = false
		m.retryAt = time.Now().Add(time.Minute)
		close(m.done)
	}()
	prepareCtx, prepareCancel := context.WithTimeout(m.ctx, 10*time.Minute)
	dir, err := m.prepare(prepareCtx)
	prepareCancel()
	if err != nil {
		if m.ctx.Err() != nil {
			m.set("stopped", "")
		} else {
			m.set("unavailable", preparationReason(err))
		}
		return
	}
	cmd := exec.CommandContext(m.ctx, filepath.Join(dir, "python"), filepath.Join(dir, "worker.py"))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1",
		"SUB2API_BASE_URL=" + m.baseURL, "OPENAI_REAUTH_WORKER_TOKEN=" + m.token,
		"OPENAI_TOTP_JOURNAL_DIR=" + filepath.Join(m.root, "totp-recovery"),
		"OPENAI_REAUTH_WORKER_ID=managed-" + randomID(), "TOSUB2_ROOT=" + filepath.Join(dir, "tosub2"),
		"TOSUB2_PYTHON=" + filepath.Join(dir, "python"), "NODE_EXECUTABLE=" + filepath.Join(dir, "node")}
	// The managed child has a minimal environment. Explicitly pass the bounded
	// concurrency setting; Python validates it before claiming anything.
	if concurrency := os.Getenv("OPENAI_REAUTH_CONCURRENCY"); concurrency != "" {
		cmd.Env = append(cmd.Env, "OPENAI_REAUTH_CONCURRENCY="+concurrency)
	}
	cmd.Dir = dir
	// Worker/external runner output must never reach the application journal.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	configureProcess(cmd)
	m.mu.Lock()
	if m.stopping || m.ctx.Err() != nil {
		m.mu.Unlock()
		m.set("stopped", "")
		return
	}
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		m.set("unavailable", "worker_start_failed")
		return
	}
	m.process = cmd
	m.mu.Unlock()
	m.set("running", "")
	_ = cmd.Wait()
	m.mu.Lock()
	m.process = nil
	stopping := m.stopping
	m.mu.Unlock()
	if stopping || m.ctx.Err() != nil {
		m.set("stopped", "")
	} else {
		m.set("unavailable", "worker_exited")
	}
}

func randomID() string { return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()) }

func (m *Manager) validateBinding() error {
	if m.version == "" || m.digest == "" || m.architecture == "" {
		return preparationFailure("runtime_not_configured")
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+-pk\.[1-9][0-9]*$`).MatchString(m.version) ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(m.digest) ||
		m.architecture != runtime.GOARCH || (m.architecture != "amd64" && m.architecture != "arm64") {
		return preparationFailure("runtime_release_invalid")
	}
	return nil
}

func (m *Manager) archiveName() string {
	return "sub2api-reauth_" + m.version + "_linux_" + m.architecture + ".tar.gz"
}

// A ready marker belongs to a specific immutable archive. Older markers that
// only asserted existence must not make a new binary run a different runtime.
func (m *Manager) installed(dir string) (bool, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, preparationFailure("runtime_cache_invalid")
	}
	for _, name := range []string{"ready", "python", "node", "worker.py"} {
		info, err = os.Lstat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() || (name == "ready" && info.Size() != 64) {
			return false, preparationFailure("runtime_cache_invalid")
		}
		if (name == "python" || name == "node") && info.Mode().Perm()&0100 == 0 {
			return false, preparationFailure("runtime_cache_invalid")
		}
	}
	marker, err := os.ReadFile(filepath.Join(dir, "ready"))
	if err != nil || string(marker) != m.digest {
		return false, preparationFailure("runtime_cache_invalid")
	}
	return true, nil
}

func (m *Manager) loadArchive(ctx context.Context) ([]byte, error) {
	name := m.archiveName()
	local := filepath.Join(m.root, "packages", name)
	info, err := os.Lstat(local)
	var archive []byte
	fetched := false
	if err == nil {
		// A staged but invalid archive is a deployment error. Do not hide it by
		// falling back to a network download or following a local link.
		parent, parentErr := os.Lstat(filepath.Dir(local))
		if parentErr != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 ||
			!info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxArchive {
			return nil, preparationFailure("runtime_cache_invalid")
		}
		f, openErr := os.Open(local)
		if openErr != nil {
			return nil, preparationFailure("runtime_cache_invalid")
		}
		archive, err = io.ReadAll(io.LimitReader(f, maxArchive+1))
		closeErr := f.Close()
		if err != nil || closeErr != nil || int64(len(archive)) > maxArchive {
			return nil, preparationFailure("runtime_cache_invalid")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		fetched = true
		archive, err = m.get(ctx, "https://github.com/"+runtimeRepository+"/releases/download/v"+m.version+"/"+name, maxArchive)
		if err != nil {
			return nil, preparationFailure("runtime_download_failed")
		}
	} else {
		return nil, preparationFailure("runtime_cache_invalid")
	}
	hash := sha256.Sum256(archive)
	if hex.EncodeToString(hash[:]) != m.digest {
		return nil, preparationFailure("runtime_checksum_mismatch")
	}
	if fetched {
		if err := m.cacheArchive(local, archive); err != nil {
			return nil, err
		}
	}
	return archive, nil
}

func (m *Manager) cacheArchive(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return preparationFailure("runtime_cache_invalid")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return preparationFailure("runtime_cache_invalid")
	}
	tmp, err := os.CreateTemp(dir, ".download-")
	if err != nil {
		return preparationFailure("runtime_cache_invalid")
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		return preparationFailure("runtime_cache_invalid")
	}
	// Link commits only to a previously absent path; never replace a staged
	// archive. A concurrent installer is accepted only after checking its bytes.
	if err := os.Link(tmp.Name(), path); err != nil {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
			return preparationFailure("runtime_cache_invalid")
		}
		existing, readErr := os.ReadFile(path)
		hash := sha256.Sum256(existing)
		if readErr != nil || hex.EncodeToString(hash[:]) != m.digest {
			return preparationFailure("runtime_cache_invalid")
		}
	}
	return nil
}

func (m *Manager) prepare(ctx context.Context) (string, error) {
	if err := m.validateBinding(); err != nil {
		return "", err
	}
	dir := filepath.Join(m.root, m.version+"-linux-"+m.architecture)
	if ready, err := m.installed(dir); err != nil {
		return "", err
	} else if ready {
		archive, err := m.loadArchive(ctx)
		if err != nil {
			return "", err
		}
		if err := verifyInstalledArchive(ctx, archive, dir); err != nil {
			return "", preparationFailure("runtime_cache_invalid")
		}
		return dir, nil
	}
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return "", err
	}
	archive, err := m.loadArchive(ctx)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(m.root, ".prepare-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := extract(archive, tmp); err != nil {
		return "", preparationFailure("runtime_archive_invalid")
	}
	check := exec.CommandContext(ctx, filepath.Join(tmp, "python"), filepath.Join(tmp, "worker.py"), "--check")
	check.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "OPENAI_REAUTH_WORKER_TOKEN=" + m.token,
		"TOSUB2_ROOT=" + filepath.Join(tmp, "tosub2"), "NODE_EXECUTABLE=" + filepath.Join(tmp, "node"),
		"PYTHONDONTWRITEBYTECODE=1"}
	configureProcess(check)
	if err := check.Run(); err != nil {
		return "", preparationFailure("runtime_self_check_failed")
	}
	if err := os.WriteFile(filepath.Join(tmp, "ready"), []byte(m.digest), 0600); err != nil {
		return "", err
	}
	if _, err := m.installed(tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another instance sharing the data directory can finish first.
		if ready, otherErr := m.installed(dir); otherErr != nil || !ready {
			return "", err
		}
		if err := verifyInstalledArchive(ctx, archive, dir); err != nil {
			return "", preparationFailure("runtime_cache_invalid")
		}
	}
	return dir, nil
}

func (m *Manager) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Sub2API-Reauth")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("incomplete or oversized download")
	}
	return data, nil
}
