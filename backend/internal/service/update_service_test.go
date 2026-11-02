//go:build unit

package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	release        *GitHubRelease
	latestErr      error
	latestRepo     string
	recentReleases []*GitHubRelease
	recentErr      error
	recentRepo     string
	download       func(context.Context, string, string, int64) error
	checksum       func(context.Context, string) ([]byte, error)
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(_ context.Context, repo string) (*GitHubRelease, error) {
	s.latestRepo = repo
	return s.release, s.latestErr
}

func (s *updateServiceGitHubClientStub) FetchRecentReleases(_ context.Context, repo string, _ int) ([]*GitHubRelease, error) {
	s.recentRepo = repo
	return s.recentReleases, s.recentErr
}

func (s *updateServiceGitHubClientStub) DownloadFile(ctx context.Context, url, dest string, maxSize int64) error {
	if s.download != nil {
		return s.download(ctx, url, dest, maxSize)
	}
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(ctx context.Context, url string) ([]byte, error) {
	if s.checksum != nil {
		return s.checksum(ctx, url)
	}
	panic("FetchChecksumFile should not be called when no update is available")
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.132-pk.1",
				Name:    "v0.1.132-pk.1",
			},
		},
		"0.1.132-pk.1",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoUpdateAvailable))
	require.ErrorIs(t, err, ErrNoUpdateAvailable)
}

func newRollbackTestService(current string, releases []*GitHubRelease) *UpdateService {
	return NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"release",
	)
}

func TestUpdateServiceRejectsSourceBuildMutations(t *testing.T) {
	installPath := filepath.Join(t.TempDir(), "sub2api")
	releases := []*GitHubRelease{{TagName: "v0.2.19-pk.7"}}
	svc := newUpdateServiceWithExecutable(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		"0.2.19-pk.8",
		"source",
		func() (string, error) { return installPath, nil },
	)
	require.ErrorIs(t, svc.PerformUpdate(context.Background()), ErrUpdateNotAllowed)
	_, err := svc.ListRollbackVersions(context.Background())
	require.ErrorIs(t, err, ErrUpdateNotAllowed)
	require.ErrorIs(t, svc.RollbackToVersion(context.Background(), "0.2.19-pk.7"), ErrUpdateNotAllowed)
	require.ErrorIs(t, svc.Rollback(), ErrUpdateNotAllowed)
}

func TestUpdateServiceRollbackFiltersNonPrivateTags(t *testing.T) {
	svc := newRollbackTestService("0.2.19-pk.8", []*GitHubRelease{
		{TagName: "v0.2.19"},
		{TagName: "0.2.19-pk.7"},
		{TagName: "v0.2.19-pk.7-rc1"},
		{TagName: "v0.2.19-pk.7"},
		{TagName: "v0.2.18-pk.2"},
	})
	versions, err := svc.ListRollbackVersions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []RollbackVersion{{Version: "0.2.19-pk.7"}, {Version: "0.2.18-pk.2"}}, versions)
}

func TestUpdateServiceRejectsMalformedLatestTags(t *testing.T) {
	for _, tag := range []string{
		"",
		"v0.2.19-pk.8 ",
		" v0.2.19-pk.8",
		"vv0.2.19-pk.8",
		"v999999999999999999999999.2.19-pk.1",
		"v0.2.19-pk.18446744073709551616",
	} {
		t.Run(fmt.Sprintf("%q", tag), func(t *testing.T) {
			svc := NewUpdateService(
				&updateServiceCacheStub{},
				&updateServiceGitHubClientStub{release: &GitHubRelease{TagName: tag}},
				"0.2.19-pk.7",
				"release",
			)
			info, err := svc.CheckUpdate(context.Background(), true)
			require.Error(t, err)
			require.NotNil(t, info)
			require.False(t, info.HasUpdate)
		})
	}
}

func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148-pk.1", PublishedAt: "2026-07-09T00:00:00Z"},                  // newer than current: excluded
		{TagName: "v0.1.147-pk.1", PublishedAt: "2026-07-08T00:00:00Z"},                  // current: excluded
		{TagName: "v0.1.146-rc1", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // prerelease: excluded
		{TagName: "v0.1.146-pk.1", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "v0.1.145-pk.1", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // draft: excluded
		{TagName: "v0.1.144-pk.1", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "v0.1.144-pk.1", PublishedAt: "2026-07-05T00:00:00Z"}, // duplicate: excluded
		{TagName: "v0.1.143-pk.1", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "v0.1.142-pk.1", PublishedAt: "2026-07-03T00:00:00Z"}, // beyond cap of 3: excluded
	}
	svc := newRollbackTestService("0.1.147-pk.1", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146-pk.1", versions[0].Version)
	require.Equal(t, "0.1.144-pk.1", versions[1].Version)
	require.Equal(t, "0.1.143-pk.1", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.144-pk.1"},
		{TagName: "v0.1.146-pk.1"},
		{TagName: "v0.1.145-pk.1"},
	}
	svc := newRollbackTestService("0.1.147-pk.1", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146-pk.1", versions[0].Version)
	require.Equal(t, "0.1.145-pk.1", versions[1].Version)
	require.Equal(t, "0.1.144-pk.1", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.147-pk.1"},
		{TagName: "v0.1.148-pk.1"},
	}
	svc := newRollbackTestService("0.1.147-pk.1", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.147-pk.1",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148-pk.1"},
		{TagName: "v0.1.147-pk.1"},
		{TagName: "v0.1.146-pk.1"},
		{TagName: "v0.1.145-pk.1"},
		{TagName: "v0.1.144-pk.1"},
		{TagName: "v0.1.143-pk.1"},
		{TagName: "v0.1.142-pk.1"},
	}
	svc := newRollbackTestService("0.1.147-pk.1", releases)

	for _, target := range []string{
		"",               // empty
		"0.1.147-pk.1",   // current version
		"v0.1.147-pk.1",  // current version with prefix
		"0.1.148-pk.1",   // newer than current
		"0.1.142-pk.1",   // older than the 3 most recent
		"9.9.9",          // nonexistent
		"0.1.146-pk.1 ",  // whitespace is not a formal version
		"vv0.1.146-pk.1", // duplicate v prefix is not a formal version
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// No platform asset in the release: the target passes the allowlist check
	// and fails later at asset lookup, proving the version itself was accepted.
	releases := []*GitHubRelease{
		{TagName: "v0.1.147-pk.1"},
		{TagName: "v0.1.146-pk.1"},
	}
	svc := newRollbackTestService("0.1.147-pk.1", releases)

	err := svc.RollbackToVersion(context.Background(), "v0.1.146-pk.1")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRollbackVersionNotAllowed)
	require.Contains(t, err.Error(), "no compatible release found")
}

func TestUpdateServicePrivateRevisionUpdates(t *testing.T) {
	for _, tt := range []struct {
		current, latest string
		wantUpdate      bool
	}{
		{"0.2.10", "0.2.10-pk.1", true},
		{"0.2.10-pk.1", "0.2.10-pk.2", true},
		{"0.2.10-pk.2", "0.2.10-pk.10", true},
		{"0.2.10-pk.10", "0.2.10-pk.2", false},
		{"0.2.10-pk.2", "0.2.10-pk.2", false},
		{"0.2.10-pk.2", "0.2.10-pk.1", false},
		{"0.2.10-pk.99", "0.2.11-pk.1", true},
		{"0.2.11-pk.1", "0.2.10-pk.99", false},
		{"0.2.10-pk.1", "0.2.10-pk.1", false},
	} {
		t.Run(tt.current+"_to_"+tt.latest, func(t *testing.T) {
			client := &updateServiceGitHubClientStub{release: &GitHubRelease{TagName: "v" + tt.latest}}
			svc := NewUpdateService(&updateServiceCacheStub{}, client, tt.current, "release")
			info, err := svc.CheckUpdate(context.Background(), true)
			require.NoError(t, err)
			require.Equal(t, tt.wantUpdate, info.HasUpdate)
			require.Equal(t, "yb1320057104/sub2api", client.latestRepo)
		})
	}
}

func TestUpdateServicePrivateRevisionRollbackOrder(t *testing.T) {
	client := &updateServiceGitHubClientStub{recentReleases: []*GitHubRelease{
		{TagName: "v0.2.10-pk.2"},
		{TagName: "v0.2.10-pk.11"},
		{TagName: "v0.2.10"},
		{TagName: "v0.2.10-pk.1"},
		{TagName: "v0.2.11"},
		{TagName: "v0.2.10-pk.10"},
		{TagName: "v0.2.10-pk.9", Prerelease: true},
	}}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.2.10-pk.11", "release")
	versions, err := svc.ListRollbackVersions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []RollbackVersion{
		{Version: "0.2.10-pk.10"},
		{Version: "0.2.10-pk.2"},
		{Version: "0.2.10-pk.1"},
	}, versions)
	require.Equal(t, "yb1320057104/sub2api", client.recentRepo)
}

func TestUpdateServicePrivateAssetAPIPreservesChecksumFilename(t *testing.T) {
	const archiveURL = "https://api.github.com/repos/yb1320057104/sub2api/releases/assets/101"
	const checksumURL = "https://api.github.com/repos/yb1320057104/sub2api/releases/assets/102"
	archiveName := fmt.Sprintf("sub2api_0.2.10-pk.2_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	// Deliberately invalid archive: checksum validation must pass before extraction
	// rejects it, leaving the isolated installation unchanged.
	archive := []byte("this is not a gzip archive")
	digest := sha256.Sum256(archive)
	for _, operation := range []string{"update", "rollback"} {
		t.Run(operation, func(t *testing.T) {
			release := &GitHubRelease{TagName: "v0.2.10-pk.2", Assets: []GitHubAsset{
				{Name: archiveName, APIURL: archiveURL, BrowserDownloadURL: "https://github.com/yb1320057104/sub2api/releases/download/v0.2.10-pk.2/" + archiveName},
				{Name: "checksums.txt", APIURL: checksumURL, BrowserDownloadURL: "https://github.com/yb1320057104/sub2api/releases/download/v0.2.10-pk.2/checksums.txt"},
			}}
			downloaded, checksumRead := false, false
			client := &updateServiceGitHubClientStub{
				release: release, recentReleases: []*GitHubRelease{release},
				download: func(_ context.Context, url, dest string, _ int64) error {
					downloaded = true
					require.Equal(t, archiveURL, url)
					require.Equal(t, archiveName, filepath.Base(dest))
					return os.WriteFile(dest, archive, 0600)
				},
				checksum: func(_ context.Context, url string) ([]byte, error) {
					checksumRead = true
					require.Equal(t, checksumURL, url)
					return []byte(fmt.Sprintf("%x  %s\n", digest, archiveName)), nil
				},
			}
			installPath := filepath.Join(t.TempDir(), "sub2api")
			require.NoError(t, os.WriteFile(installPath, []byte("previous version"), 0755))
			svc := newUpdateServiceWithExecutable(&updateServiceCacheStub{}, client, "0.2.10-pk.1", "release", func() (string, error) {
				return installPath, nil
			})
			var err error
			if operation == "rollback" {
				svc.currentVersion = "0.2.10-pk.3"
				err = svc.RollbackToVersion(context.Background(), "0.2.10-pk.2")
			} else {
				err = svc.PerformUpdate(context.Background())
			}
			require.ErrorContains(t, err, "extraction failed")
			require.True(t, downloaded)
			require.True(t, checksumRead)
			installed, readErr := os.ReadFile(installPath)
			require.NoError(t, readErr)
			require.Equal(t, "previous version", string(installed))
		})
	}
}

func TestUpdateServiceRejectsReleaseWithoutChecksum(t *testing.T) {
	client := &updateServiceGitHubClientStub{release: &GitHubRelease{
		TagName: "v0.2.10-pk.2",
		Assets: []GitHubAsset{{
			Name:   fmt.Sprintf("sub2api_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH),
			APIURL: "https://api.github.com/repos/yb1320057104/sub2api/releases/assets/101",
		}},
	}}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.2.10-pk.1", "release")
	require.ErrorContains(t, svc.PerformUpdate(context.Background()), "missing checksums.txt")
}

func TestUpdateServicePrivateRepositoryCacheIsolation(t *testing.T) {
	for _, repository := range []string{"", "Wei-Shaw/sub2api", "pengkang0322/sub2api", "yb1320057104/sub2api"} {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force=%v", repository, force), func(t *testing.T) {
				cached := map[string]any{
					"latest": "0.2.10-pk.2", "timestamp": time.Now().Unix(),
					"release_info": ReleaseInfo{Name: "cached release"},
				}
				if repository != "" {
					cached["repository"] = repository
				}
				data, err := json.Marshal(cached)
				require.NoError(t, err)
				client := &updateServiceGitHubClientStub{latestErr: errors.New("GitHub unavailable")}
				svc := NewUpdateService(&updateServiceCacheStub{data: string(data)}, client, "0.2.10-pk.1", "release")
				info, err := svc.CheckUpdate(context.Background(), force)
				if repository == "yb1320057104/sub2api" && !force {
					require.NoError(t, err)
					require.NotNil(t, info)
					require.True(t, info.HasUpdate)
					require.True(t, info.Cached)
					require.Equal(t, "0.2.10-pk.2", info.LatestVersion)
				} else {
					require.Error(t, err)
					require.NotNil(t, info)
					require.False(t, info.HasUpdate)
					require.False(t, info.Cached)
					require.Nil(t, info.ReleaseInfo)
					require.Contains(t, info.Warning, "GitHub unavailable")
				}
			})
		}
	}
}

func TestUpdateServicePrivateCacheRoundTrip(t *testing.T) {
	cache := &updateServiceCacheStub{}
	client := &updateServiceGitHubClientStub{release: &GitHubRelease{TagName: "v0.2.10-pk.2"}}
	svc := NewUpdateService(cache, client, "0.2.10-pk.1", "release")
	_, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	client.latestErr = errors.New("GitHub unavailable")
	info, err := svc.CheckUpdate(context.Background(), false)
	require.NoError(t, err)
	require.True(t, info.Cached)
	require.True(t, info.HasUpdate)
	require.Equal(t, "0.2.10-pk.2", info.LatestVersion)
	_, err = svc.CheckUpdate(context.Background(), true)
	require.Error(t, err)
}

func TestUpdateServiceNilDependenciesDoNotPanic(t *testing.T) {
	svc := NewUpdateService(nil, nil, "0.2.19-pk.8", "release")
	info, err := svc.CheckUpdate(context.Background(), true)
	require.Error(t, err)
	require.NotNil(t, info)
	require.Contains(t, info.Warning, "unavailable")
	_, err = svc.ListRollbackVersions(context.Background())
	require.Error(t, err)
}

func TestUpdateServiceRepeatedUpdatesAndRollbacksKeepInstallPath(t *testing.T) {
	installPath := filepath.Join(t.TempDir(), "sub2api")
	require.NoError(t, os.WriteFile(installPath, []byte("original pk.3"), 0755))
	files := make(map[string][]byte)
	makeRelease := func(assetID int, version, binary string) *GitHubRelease {
		var archive bytes.Buffer
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "sub2api", Mode: 0755, Size: int64(len(binary))}))
		_, err := tw.Write([]byte(binary))
		require.NoError(t, err)
		require.NoError(t, tw.Close())
		require.NoError(t, gz.Close())
		name := fmt.Sprintf("sub2api_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
		archiveURL := fmt.Sprintf("https://api.github.com/repos/yb1320057104/sub2api/releases/assets/%d", assetID)
		checksumURL := fmt.Sprintf("https://api.github.com/repos/yb1320057104/sub2api/releases/assets/%d", assetID+1)
		files[archiveURL] = archive.Bytes()
		files[checksumURL] = []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(archive.Bytes()), name))
		return &GitHubRelease{TagName: "v" + version, Assets: []GitHubAsset{
			{Name: name, APIURL: archiveURL},
			{Name: "checksums.txt", APIURL: checksumURL},
		}}
	}
	first := makeRelease(101, "0.2.10-pk.4", "updated pk.4")
	second := makeRelease(201, "0.2.10-pk.5", "updated pk.5")
	older := makeRelease(301, "0.2.10-pk.2", "older pk.2")
	client := &updateServiceGitHubClientStub{
		release: first, recentReleases: []*GitHubRelease{older},
		download: func(_ context.Context, url, dest string, _ int64) error {
			data, ok := files[url]
			require.True(t, ok)
			return os.WriteFile(dest, data, 0600)
		},
		checksum: func(_ context.Context, url string) ([]byte, error) {
			data, ok := files[url]
			require.True(t, ok)
			return data, nil
		},
	}
	// Linux reports the renamed running inode as .backup after the first update.
	// The injected resolver lets this exercise real file replacement safely.
	reportedPath := installPath
	resolveCalls := 0
	svc := newUpdateServiceWithExecutable(&updateServiceCacheStub{}, client, "0.2.10-pk.3", "release", func() (string, error) {
		resolveCalls++
		return reportedPath, nil
	})
	assertFiles := func(installed, backup string) {
		t.Helper()
		data, err := os.ReadFile(installPath)
		require.NoError(t, err)
		require.Equal(t, installed, string(data))
		if backup == "" {
			require.NoFileExists(t, installPath+".backup")
		} else {
			data, err = os.ReadFile(installPath + ".backup")
			require.NoError(t, err)
			require.Equal(t, backup, string(data))
		}
		require.NoFileExists(t, installPath+".backup.backup")
		require.Equal(t, 1, resolveCalls)
	}

	require.NoError(t, svc.PerformUpdate(context.Background()))
	assertFiles("updated pk.4", "original pk.3")
	reportedPath = installPath + ".backup"
	client.release = second
	require.NoError(t, svc.PerformUpdate(context.Background()))
	assertFiles("updated pk.5", "updated pk.4")
	require.NoError(t, svc.Rollback())
	assertFiles("updated pk.4", "")
	require.NoError(t, svc.RollbackToVersion(context.Background(), "0.2.10-pk.2"))
	assertFiles("older pk.2", "updated pk.4")
	require.NoError(t, svc.Rollback())
	assertFiles("updated pk.4", "")
}

func TestUpdateServiceRollbackRestoresBackupWhenCurrentIsMissing(t *testing.T) {
	installPath := filepath.Join(t.TempDir(), "sub2api")
	backupPath := installPath + ".backup"
	require.NoError(t, os.WriteFile(installPath, []byte("current"), 0755))
	require.NoError(t, os.WriteFile(backupPath, []byte("previous"), 0755))
	svc := newUpdateServiceWithExecutable(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{},
		"0.2.19-pk.8",
		"release",
		func() (string, error) { return installPath, nil },
	)
	require.NoError(t, os.Remove(installPath))

	require.NoError(t, svc.Rollback())
	data, err := os.ReadFile(installPath)
	require.NoError(t, err)
	require.Equal(t, "previous", string(data))
	require.NoFileExists(t, backupPath)
}

func TestUpdateServiceRollbackRejectsUnsafeBackupsWithoutChangingInstall(t *testing.T) {
	for _, kind := range []string{"directory", "non-executable"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "non-executable" && runtime.GOOS == "windows" {
				t.Skip("Windows executable bits are not represented by file mode")
			}
			installPath := filepath.Join(t.TempDir(), "sub2api")
			require.NoError(t, os.WriteFile(installPath, []byte("current"), 0755))
			backupPath := installPath + ".backup"
			if kind == "directory" {
				require.NoError(t, os.Mkdir(backupPath, 0755))
			} else {
				require.NoError(t, os.WriteFile(backupPath, []byte("unsafe"), 0600))
			}
			svc := newUpdateServiceWithExecutable(
				&updateServiceCacheStub{},
				&updateServiceGitHubClientStub{},
				"0.2.19-pk.8",
				"release",
				func() (string, error) { return installPath, nil },
			)

			err := svc.Rollback()
			require.Error(t, err)
			data, readErr := os.ReadFile(installPath)
			require.NoError(t, readErr)
			require.Equal(t, "current", string(data))
			if kind == "directory" {
				info, statErr := os.Stat(backupPath)
				require.NoError(t, statErr)
				require.True(t, info.IsDir())
			} else {
				data, readErr = os.ReadFile(backupPath)
				require.NoError(t, readErr)
				require.Equal(t, "unsafe", string(data))
			}
		})
	}
}

func TestUpdateServiceRemovePreviousBackupRejectsNonExecutableAndPreservesIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows executable bits are not represented by file mode")
	}
	backupPath := filepath.Join(t.TempDir(), "sub2api.backup")
	require.NoError(t, os.WriteFile(backupPath, []byte("backup"), 0600))

	err := removePreviousBackup(backupPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not executable")
	data, readErr := os.ReadFile(backupPath)
	require.NoError(t, readErr)
	require.Equal(t, "backup", string(data))
}

func TestUpdateServiceExecutableResolutionFailurePreventsReplacement(t *testing.T) {
	for _, failure := range []string{"lookup", "symlink resolution", "empty path"} {
		t.Run(failure, func(t *testing.T) {
			installPath := filepath.Join(t.TempDir(), "sub2api")
			require.NoError(t, os.WriteFile(installPath, []byte("original"), 0755))
			assets := []GitHubAsset{
				{Name: fmt.Sprintf("sub2api_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH), APIURL: "https://api.github.com/repos/yb1320057104/sub2api/releases/assets/101"},
				{Name: "checksums.txt", APIURL: "https://api.github.com/repos/yb1320057104/sub2api/releases/assets/102"},
			}
			client := &updateServiceGitHubClientStub{
				release:        &GitHubRelease{TagName: "v0.2.10-pk.4", Assets: assets},
				recentReleases: []*GitHubRelease{{TagName: "v0.2.10-pk.2", Assets: assets}},
			}
			resolveCalls := 0
			lookupErr := errors.New("executable lookup unavailable")
			svc := newUpdateServiceWithExecutable(&updateServiceCacheStub{}, client, "0.2.10-pk.3", "release", func() (string, error) {
				resolveCalls++
				switch failure {
				case "lookup":
					return "", lookupErr
				case "symlink resolution":
					return installPath + ".missing", nil
				default:
					return "", nil
				}
			})
			for _, operation := range []func() error{
				func() error { return svc.PerformUpdate(context.Background()) },
				func() error { return svc.RollbackToVersion(context.Background(), "0.2.10-pk.2") },
				svc.Rollback,
			} {
				err := operation()
				require.Error(t, err)
				if failure == "lookup" {
					require.ErrorIs(t, err, lookupErr)
				}
			}
			require.Equal(t, 1, resolveCalls)
			data, err := os.ReadFile(installPath)
			require.NoError(t, err)
			require.Equal(t, "original", string(data))
			require.NoFileExists(t, installPath+".backup")
		})
	}
}
