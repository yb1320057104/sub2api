package reauthruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func archive(t *testing.T, entries []*tar.Header, content []string) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for i, h := range entries {
		require.NoError(t, tw.WriteHeader(h))
		if h.Typeflag == tar.TypeReg {
			_, err := tw.Write([]byte(content[i]))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return data.Bytes()
}

func TestExtractRejectsEscapesAndLinks(t *testing.T) {
	for _, h := range []*tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg, Size: 1},
		{Name: "/escape", Typeflag: tar.TypeReg, Size: 1},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../escape"},
		{Name: "device", Typeflag: tar.TypeChar},
	} {
		t.Run(h.Name, func(t *testing.T) {
			require.Error(t, extract(archive(t, []*tar.Header{h}, []string{"x"}), t.TempDir()))
		})
	}
}

func TestExtractPreservesExecutableAndRejectsDuplicate(t *testing.T) {
	root := t.TempDir()
	h := &tar.Header{Name: "bin/run", Typeflag: tar.TypeReg, Size: 4, Mode: 0755}
	require.NoError(t, extract(archive(t, []*tar.Header{h}, []string{"test"}), root))
	info, err := os.Stat(filepath.Join(root, "bin/run"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	require.Error(t, extract(archive(t, []*tar.Header{h}, []string{"test"}), root))
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func bindTestRuntime(m *Manager, digest string) {
	m.version = "0.2.19-pk.66"
	m.digest = digest
	m.architecture = runtime.GOARCH
}

func runtimeArchive(t *testing.T, script string) []byte {
	t.Helper()
	return archive(t, []*tar.Header{
		{Name: "python", Typeflag: tar.TypeReg, Size: int64(len(script)), Mode: 0755},
		{Name: "node", Typeflag: tar.TypeReg, Size: int64(len(script)), Mode: 0755},
		{Name: "worker.py", Typeflag: tar.TypeReg, Size: 0, Mode: 0644},
	}, []string{script, script, ""})
}

func TestPrepareVerifiesDigestBeforeExecutingAndReusesCache(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux runtime")
	}
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{true: "valid", false: "tampered"}[valid], func(t *testing.T) {
			root := t.TempDir()
			script := "#!/bin/sh\nexit 0\n"
			data := runtimeArchive(t, script)
			sum := sha256.Sum256(data)
			digest := hex.EncodeToString(sum[:])
			if !valid {
				digest = strings.Repeat("0", 64)
			}
			requests := 0
			m := New(root, "0.2.19-pk.66-ha.20261115.1", "http://127.0.0.1:4040", strings.Repeat("x", 64))
			bindTestRuntime(m, digest)
			m.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				require.NotContains(t, req.URL.String(), m.token)
				require.Equal(t, "https://github.com/yb1320057104/sub2api/releases/download/v0.2.19-pk.66/sub2api-reauth_0.2.19-pk.66_linux_"+runtime.GOARCH+".tar.gz", req.URL.String())
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
			})}
			dir, err := m.prepare(context.Background())
			if !valid {
				require.EqualError(t, err, "runtime_checksum_mismatch")
				require.NoFileExists(t, filepath.Join(root, m.version+"-linux-"+runtime.GOARCH, "ready"))
				return
			}
			require.NoError(t, err)
			require.FileExists(t, filepath.Join(dir, "ready"))
			_, err = m.prepare(context.Background())
			require.NoError(t, err)
			require.Equal(t, 1, requests)
		})
	}
}

func TestStopCancelsPreparationAndCannotRestart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux runtime preparation")
	}
	m := New(t.TempDir(), "1.2.3", "http://127.0.0.1:4040", strings.Repeat("x", 64))
	bindTestRuntime(m, strings.Repeat("1", 64))
	entered := make(chan struct{})
	m.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		close(entered)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	m.Ensure()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("preparation did not start")
	}
	m.Stop()
	require.Equal(t, "stopped", m.Status().State)
	m.Ensure()
	require.False(t, m.started)
}

func TestManagedProcessGetsOnlyWorkerEnvironmentAndStops(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process supervision")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "0.2.19-pk.66-linux-"+runtime.GOARCH)
	require.NoError(t, os.MkdirAll(dir, 0700))
	script := "#!/bin/sh\nprintf '%s' \"$OPENAI_REAUTH_WORKER_TOKEN\" > worker-token\nprintf '%s' \"$DATABASE_PASSWORD\" > unrelated-secret\nprintf '%s' \"$OPENAI_REAUTH_CONCURRENCY\" > concurrency\nexec /bin/sleep 60\n"
	data := runtimeArchive(t, script)
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ready"), []byte(digest), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "python"), []byte(script), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node"), []byte(script), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "worker.py"), nil, 0600))
	t.Setenv("OPENAI_REAUTH_CONCURRENCY", "4")
	t.Setenv("DATABASE_PASSWORD", "must-not-inherit")
	m := New(root, "1.2.3", "http://127.0.0.1:4040", "synthetic-worker-token")
	bindTestRuntime(m, digest)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", m.archiveName()), data, 0600))
	m.Ensure()
	defer m.Stop()
	// Child output can precede the parent's running status update after Start.
	// Wait for both independently, including the last environment file write.
	require.Eventually(t, func() bool {
		return m.Status().State == "running"
	}, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		got, err := os.ReadFile(filepath.Join(dir, "concurrency"))
		return err == nil && string(got) == "4"
	}, 5*time.Second, 10*time.Millisecond)
	got, err := os.ReadFile(filepath.Join(dir, "worker-token"))
	require.NoError(t, err)
	require.Equal(t, "synthetic-worker-token", string(got))
	got, err = os.ReadFile(filepath.Join(dir, "unrelated-secret"))
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = os.ReadFile(filepath.Join(dir, "concurrency"))
	require.NoError(t, err)
	require.Equal(t, "4", string(got))
	m.Stop()
	require.Equal(t, "stopped", m.Status().State)
}

func TestReleaseBindingRejectsMissingAndInvalidPins(t *testing.T) {
	for _, tc := range []struct {
		name, version, digest, arch, reason string
	}{
		{"missing", "", "", "", "runtime_not_configured"},
		{"ha suffix", "0.2.19-pk.66-ha.20261115.1", strings.Repeat("1", 64), runtime.GOARCH, "runtime_release_invalid"},
		{"upstream version", "2.20.4", strings.Repeat("1", 64), runtime.GOARCH, "runtime_release_invalid"},
		{"bad digest", "0.2.19-pk.66", "not-a-digest", runtime.GOARCH, "runtime_release_invalid"},
		{"wrong arch", "0.2.19-pk.66", strings.Repeat("1", 64), "wrong", "runtime_release_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(t.TempDir(), "0.2.19-pk.66", "http://127.0.0.1:4040", "synthetic")
			m.version, m.digest, m.architecture = tc.version, tc.digest, tc.arch
			m.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid binding must not contact a release server")
				return nil, nil
			})}
			_, err := m.prepare(context.Background())
			require.EqualError(t, err, tc.reason)
		})
	}
}

func TestNewUsesExplicitRuntimeBindingInsteadOfApplicationVersion(t *testing.T) {
	version, digest, arch := ReleaseVersion, ArchiveSHA256, ArchiveArchitecture
	t.Cleanup(func() { ReleaseVersion, ArchiveSHA256, ArchiveArchitecture = version, digest, arch })
	ReleaseVersion, ArchiveSHA256, ArchiveArchitecture = "0.2.19-pk.66", strings.Repeat("1", 64), runtime.GOARCH
	m := New(t.TempDir(), "0.2.19-pk.66-ha.20261115.123456", "http://127.0.0.1:4040", "synthetic")
	require.Equal(t, "0.2.19-pk.66", m.version)
	require.Equal(t, strings.Repeat("1", 64), m.digest)
	require.NoError(t, m.validateBinding())
}

func TestStagedArchiveUsesBuildDigestAndNeverFallsBackWhenInvalid(t *testing.T) {
	data := runtimeArchive(t, "#!/bin/sh\nexit 0\n")
	sum := sha256.Sum256(data)
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{true: "valid", false: "tampered"}[valid], func(t *testing.T) {
			m := New(t.TempDir(), "ignored-ha-version", "http://127.0.0.1:4040", "synthetic")
			bindTestRuntime(m, hex.EncodeToString(sum[:]))
			path := filepath.Join(m.root, "packages", m.archiveName())
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			staged := data
			if !valid {
				staged = []byte("tampered")
			}
			require.NoError(t, os.WriteFile(path, staged, 0600))
			m.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("a staged archive must not contact any release server")
				return nil, nil
			})}
			got, err := m.loadArchive(context.Background())
			if valid {
				require.NoError(t, err)
				require.Equal(t, data, got)
			} else {
				require.EqualError(t, err, "runtime_checksum_mismatch")
			}
		})
	}
}

func TestReadyMarkerCannotSelectDifferentRuntime(t *testing.T) {
	m := New(t.TempDir(), "ignored", "http://127.0.0.1:4040", "synthetic")
	bindTestRuntime(m, strings.Repeat("1", 64))
	dir := filepath.Join(m.root, m.version+"-linux-"+m.architecture)
	require.NoError(t, os.MkdirAll(dir, 0700))
	for _, name := range []string{"python", "node", "worker.py"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ready"), []byte(strings.Repeat("2", 64)), 0600))
	_, err := m.prepare(context.Background())
	require.EqualError(t, err, "runtime_cache_invalid")
}

func TestRuntimeDownloadFailureIsSafeAndSpecific(t *testing.T) {
	m := New(t.TempDir(), "ignored", "http://127.0.0.1:4040", "synthetic-secret")
	bindTestRuntime(m, strings.Repeat("1", 64))
	m.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("private upstream diagnostic")), Header: http.Header{}}, nil
	})}
	_, err := m.loadArchive(context.Background())
	require.EqualError(t, err, "runtime_download_failed")
	require.Equal(t, "runtime_download_failed", preparationReason(err))
}

func TestInstalledArchiveRejectsChangedLibrariesAndMissingFiles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux runtime file permissions")
	}
	data := archive(t, []*tar.Header{
		{Name: "lib/library.so", Typeflag: tar.TypeReg, Size: 4, Mode: 0644},
		{Name: "worker.py", Typeflag: tar.TypeReg, Size: 6, Mode: 0644},
	}, []string{"good", "worker"})
	for _, mutate := range []string{"library", "missing", "symlink", "injected module", "extra directory"} {
		t.Run(mutate, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, extract(data, root))
			require.NoError(t, verifyInstalledArchive(context.Background(), data, root))
			switch mutate {
			case "library":
				require.NoError(t, os.WriteFile(filepath.Join(root, "lib", "library.so"), []byte("evil"), 0600))
			case "missing":
				require.NoError(t, os.Remove(filepath.Join(root, "worker.py")))
			case "symlink":
				require.NoError(t, os.Rename(filepath.Join(root, "lib"), filepath.Join(root, "original")))
				require.NoError(t, os.Symlink("original", filepath.Join(root, "lib")))
			case "injected module":
				require.NoError(t, os.WriteFile(filepath.Join(root, "sitecustomize.py"), []byte("# unexpected startup module"), 0600))
			case "extra directory":
				require.NoError(t, os.Mkdir(filepath.Join(root, "unverified"), 0700))
			}
			require.Error(t, verifyInstalledArchive(context.Background(), data, root))
		})
	}
}

func TestManagedStopWaitsForActiveWorkBeforeExit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux signal supervision")
	}
	root := t.TempDir()
	script := "#!/bin/sh\nif [ \"$2\" = \"--check\" ]; then exit 0; fi\ntrap ':' TERM\ntouch started\nwhile [ ! -f allow-finish ]; do sleep 0.05; done\nprintf callback-complete > completed\nexit 0\n"
	data := runtimeArchive(t, script)
	sum := sha256.Sum256(data)
	m := New(root, "ignored-ha-version", "http://127.0.0.1:4040", "synthetic")
	bindTestRuntime(m, hex.EncodeToString(sum[:]))
	stage := filepath.Join(root, "packages", m.archiveName())
	require.NoError(t, os.MkdirAll(filepath.Dir(stage), 0700))
	require.NoError(t, os.WriteFile(stage, data, 0600))
	dir := filepath.Join(root, m.version+"-linux-"+m.architecture)
	defer func() {
		_ = os.WriteFile(filepath.Join(dir, "allow-finish"), nil, 0600)
		m.Stop()
	}()
	m.Ensure()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "started"))
		return err == nil && m.Status().State == "running"
	}, 5*time.Second, 10*time.Millisecond)
	stopped := make(chan struct{})
	go func() { m.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("managed stop interrupted the active callback")
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "allow-finish"), nil, 0600))
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("managed stop did not finish after the active callback")
	}
	got, err := os.ReadFile(filepath.Join(dir, "completed"))
	require.NoError(t, err)
	require.Equal(t, "callback-complete", string(got))
	require.Equal(t, "stopped", m.Status().State)
}
