//go:build unit

package service

import (
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
				TagName: "v0.1.132",
				Name:    "v0.1.132",
			},
		},
		"0.1.132",
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

func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148", PublishedAt: "2026-07-09T00:00:00Z"},                       // newer than current: excluded
		{TagName: "v0.1.147", PublishedAt: "2026-07-08T00:00:00Z"},                       // current: excluded
		{TagName: "v0.1.146-rc1", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // prerelease: excluded
		{TagName: "v0.1.146", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "v0.1.145", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // draft: excluded
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"}, // duplicate: excluded
		{TagName: "v0.1.143", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "v0.1.142", PublishedAt: "2026-07-03T00:00:00Z"}, // beyond cap of 3: excluded
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.144", versions[1].Version)
	require.Equal(t, "0.1.143", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.144"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.145", versions[1].Version)
	require.Equal(t, "0.1.144", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.148"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.147",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148"},
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
		{TagName: "v0.1.144"},
		{TagName: "v0.1.143"},
		{TagName: "v0.1.142"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	for _, target := range []string{
		"",         // empty
		"0.1.147",  // current version
		"v0.1.147", // current version with prefix
		"0.1.148",  // newer than current
		"0.1.142",  // older than the 3 most recent
		"9.9.9",    // nonexistent
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// No platform asset in the release: the target passes the allowlist check
	// and fails later at asset lookup, proving the version itself was accepted.
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	err := svc.RollbackToVersion(context.Background(), "v0.1.146")

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
		{"0.2.10-pk.2", "0.2.10", false},
		{"0.2.10-pk.99", "0.2.11", true},
		{"0.2.11", "0.2.10-pk.99", false},
		{"0.2.10-rc1", "0.2.10", false},
		{"0.2.10", "0.2.10-rc1", false},
	} {
		t.Run(tt.current+"_to_"+tt.latest, func(t *testing.T) {
			client := &updateServiceGitHubClientStub{release: &GitHubRelease{TagName: "v" + tt.latest}}
			svc := NewUpdateService(&updateServiceCacheStub{}, client, tt.current, "release")
			info, err := svc.CheckUpdate(context.Background(), true)
			require.NoError(t, err)
			require.Equal(t, tt.wantUpdate, info.HasUpdate)
			require.Equal(t, "pengkang0322/sub2api", client.latestRepo)
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
	require.Equal(t, "pengkang0322/sub2api", client.recentRepo)
}

func TestUpdateServicePrivateAssetAPIPreservesChecksumFilename(t *testing.T) {
	const archiveURL = "https://api.github.com/repos/pengkang0322/sub2api/releases/assets/101"
	const checksumURL = "https://api.github.com/repos/pengkang0322/sub2api/releases/assets/102"
	archiveName := fmt.Sprintf("sub2api_0.2.10-pk.2_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	// Deliberately invalid archive: checksum validation must pass, then extraction
	// must stop the operation before the test executable can ever be replaced.
	archive := []byte("this is not a gzip archive")
	digest := sha256.Sum256(archive)
	for _, operation := range []string{"update", "rollback"} {
		t.Run(operation, func(t *testing.T) {
			release := &GitHubRelease{TagName: "v0.2.10-pk.2", Assets: []GitHubAsset{
				{Name: archiveName, APIURL: archiveURL, BrowserDownloadURL: "https://github.com/pengkang0322/sub2api/releases/download/v0.2.10-pk.2/" + archiveName},
				{Name: "checksums.txt", APIURL: checksumURL, BrowserDownloadURL: "https://github.com/pengkang0322/sub2api/releases/download/v0.2.10-pk.2/checksums.txt"},
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
			svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.2.10-pk.1", "release")
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
		})
	}
}

func TestUpdateServiceRejectsReleaseWithoutChecksum(t *testing.T) {
	client := &updateServiceGitHubClientStub{release: &GitHubRelease{
		TagName: "v0.2.10-pk.2",
		Assets: []GitHubAsset{{
			Name:   fmt.Sprintf("sub2api_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH),
			APIURL: "https://api.github.com/repos/pengkang0322/sub2api/releases/assets/101",
		}},
	}}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.2.10-pk.1", "release")
	require.ErrorContains(t, svc.PerformUpdate(context.Background()), "missing checksums.txt")
}

func TestUpdateServicePrivateRepositoryCacheIsolation(t *testing.T) {
	for _, repository := range []string{"", "Wei-Shaw/sub2api", "pengkang0322/sub2api"} {
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
				require.NoError(t, err)
				if repository == "pengkang0322/sub2api" {
					require.True(t, info.HasUpdate)
					require.True(t, info.Cached)
					require.Equal(t, "0.2.10-pk.2", info.LatestVersion)
				} else {
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
}
