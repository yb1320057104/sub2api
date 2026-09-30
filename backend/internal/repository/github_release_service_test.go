package repository

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GitHubReleaseServiceSuite struct {
	suite.Suite
	srv     *httptest.Server
	client  *githubReleaseClient
	tempDir string
}

// testTransport redirects requests to the test server
type testTransport struct {
	testServerURL string
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point to our test server
	testURL := t.testServerURL + req.URL.Path
	if req.URL.RawQuery != "" {
		testURL += "?" + req.URL.RawQuery
	}
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, testURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return http.DefaultTransport.RoundTrip(newReq)
}

func newTestGitHubReleaseClient() *githubReleaseClient {
	return &githubReleaseClient{
		httpClient:         &http.Client{},
		downloadHTTPClient: &http.Client{},
	}
}

const githubReleaseTestAssetURL = "https://api.github.com/repos/pengkang0322/sub2api/releases/assets/101"

func newLocalGitHubReleaseClient(serverURL string) *githubReleaseClient {
	client := newTestGitHubReleaseClient()
	transport := &testTransport{testServerURL: serverURL}
	client.httpClient.Transport = transport
	client.downloadHTTPClient.Transport = transport
	return client
}

func TestGitHubReleaseClientAPIRequestAuthorization(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantAuth string
	}{
		{name: "exact HTTPS authority", url: "https://api.github.com/repos/test/repo", wantAuth: "Bearer update-secret"},
		{name: "HTTP", url: "http://api.github.com/repos/test/repo"},
		{name: "subdomain", url: "https://sub.api.github.com/repos/test/repo"},
		{name: "userinfo", url: "https://user@api.github.com/repos/test/repo"},
		{name: "explicit default port", url: "https://api.github.com:443/repos/test/repo"},
		{name: "custom port", url: "https://api.github.com:8443/repos/test/repo"},
		{name: "different host", url: "https://github.com/test/repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestGitHubReleaseClient()
			client.updateGitHubToken = "update-secret"
			req, err := client.newAPIRequest(context.Background(), tt.url)
			require.NoError(t, err)
			require.Equal(t, tt.wantAuth, req.Header.Get("Authorization"))
		})
	}

	client := newTestGitHubReleaseClient()
	req, err := client.newAPIRequest(context.Background(), "https://api.github.com/repos/test/repo")
	require.NoError(t, err)
	require.Empty(t, req.Header.Get("Authorization"))
}

func TestGitHubReleaseClientRedirectAuthorization(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantAuth string
	}{
		{name: "same HTTPS authority", url: "https://api.github.com/redirected", wantAuth: "Bearer update-secret"},
		{name: "HTTP", url: "http://api.github.com/redirected"},
		{name: "subdomain", url: "https://sub.api.github.com/redirected"},
		{name: "userinfo", url: "https://user@api.github.com/redirected"},
		{name: "custom port", url: "https://api.github.com:8443/redirected"},
		{name: "different host", url: "https://example.com/redirected"},
	}

	checkRedirect := githubAPICheckRedirect(nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tt.url, nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer update-secret")

			require.NoError(t, checkRedirect(req, nil))
			require.Equal(t, tt.wantAuth, req.Header.Get("Authorization"))
		})
	}
}

func TestGitHubReleaseClientDoesNotAuthorizeDownloads(t *testing.T) {
	client := newTestGitHubReleaseClient()
	client.updateGitHubToken = "update-secret"

	var headers []http.Header
	transport := githubReleaseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		headers = append(headers, req.Header.Clone())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("checksum")),
			Request:    req,
		}, nil
	})
	client.httpClient.Transport = transport
	client.downloadHTTPClient.Transport = transport

	dest := filepath.Join(t.TempDir(), "asset")
	require.NoError(t, client.DownloadFile(context.Background(), "https://objects.githubusercontent.com/asset", dest, 100))
	_, err := client.FetchChecksumFile(context.Background(), "https://github.com/pengkang0322/sub2api/releases/download/v1/checksums.txt")
	require.NoError(t, err)
	require.Len(t, headers, 2)
	for _, header := range headers {
		require.Empty(t, header.Get("Authorization"))
	}
}

type githubReleaseRoundTripFunc func(*http.Request) (*http.Response, error)

func (f githubReleaseRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (s *GitHubReleaseServiceSuite) SetupTest() {
	s.tempDir = s.T().TempDir()
}

func (s *GitHubReleaseServiceSuite) TearDownTest() {
	if s.srv != nil {
		s.srv.Close()
		s.srv = nil
	}
}

func (s *GitHubReleaseServiceSuite) TestDownloadFile_EnforcesMaxSize_ContentLength() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("a"), 100))
	}))

	s.client = newLocalGitHubReleaseClient(s.srv.URL)

	dest := filepath.Join(s.tempDir, "file1.bin")
	err := s.client.DownloadFile(context.Background(), githubReleaseTestAssetURL, dest, 10)
	require.Error(s.T(), err, "expected error for oversized download with Content-Length")

	_, statErr := os.Stat(dest)
	require.Error(s.T(), statErr, "expected file to not exist for rejected download")
}

func (s *GitHubReleaseServiceSuite) TestDownloadFile_EnforcesMaxSize_Chunked() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Force chunked encoding (unknown Content-Length) by flushing headers before writing.
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		for i := 0; i < 10; i++ {
			_, _ = w.Write(bytes.Repeat([]byte("b"), 10))
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}))

	s.client = newLocalGitHubReleaseClient(s.srv.URL)

	dest := filepath.Join(s.tempDir, "file2.bin")
	err := s.client.DownloadFile(context.Background(), githubReleaseTestAssetURL, dest, 10)
	require.Error(s.T(), err, "expected error for oversized chunked download")

	_, statErr := os.Stat(dest)
	require.Error(s.T(), statErr, "expected file to be cleaned up for oversized chunked download")
}

func (s *GitHubReleaseServiceSuite) TestDownloadFile_Success() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		for i := 0; i < 10; i++ {
			_, _ = w.Write(bytes.Repeat([]byte("b"), 10))
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}))

	s.client = newLocalGitHubReleaseClient(s.srv.URL)

	dest := filepath.Join(s.tempDir, "file3.bin")
	err := s.client.DownloadFile(context.Background(), githubReleaseTestAssetURL, dest, 200)
	require.NoError(s.T(), err, "expected success")

	b, err := os.ReadFile(dest)
	require.NoError(s.T(), err, "read")
	require.True(s.T(), strings.HasPrefix(string(b), "b"), "downloaded content should start with 'b'")
	require.Len(s.T(), b, 100, "downloaded content length mismatch")
}

func (s *GitHubReleaseServiceSuite) TestDownloadFile_404() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	s.client = newLocalGitHubReleaseClient(s.srv.URL)

	dest := filepath.Join(s.tempDir, "notfound.bin")
	err := s.client.DownloadFile(context.Background(), githubReleaseTestAssetURL, dest, 100)
	require.Error(s.T(), err, "expected error for 404")

	_, statErr := os.Stat(dest)
	require.Error(s.T(), statErr, "expected file to not exist for 404")
}

func (s *GitHubReleaseServiceSuite) TestFetchChecksumFile_Success() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("sum"))
	}))

	s.client = newLocalGitHubReleaseClient(s.srv.URL)

	body, err := s.client.FetchChecksumFile(context.Background(), githubReleaseTestAssetURL)
	require.NoError(s.T(), err, "FetchChecksumFile")
	require.Equal(s.T(), "sum", string(body), "checksum body mismatch")
}

func (s *GitHubReleaseServiceSuite) TestFetchChecksumFile_Non200() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	s.client = newLocalGitHubReleaseClient(s.srv.URL)

	_, err := s.client.FetchChecksumFile(context.Background(), githubReleaseTestAssetURL)
	require.Error(s.T(), err, "expected error for non-200")
}

func (s *GitHubReleaseServiceSuite) TestDownloadFile_ContextCancel() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))

	s.client = newLocalGitHubReleaseClient(s.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dest := filepath.Join(s.tempDir, "cancelled.bin")
	err := s.client.DownloadFile(ctx, githubReleaseTestAssetURL, dest, 100)
	require.Error(s.T(), err, "expected error for cancelled context")
}

func (s *GitHubReleaseServiceSuite) TestDownloadFile_InvalidURL() {
	s.client = newTestGitHubReleaseClient()

	dest := filepath.Join(s.tempDir, "invalid.bin")
	err := s.client.DownloadFile(context.Background(), "://invalid-url", dest, 100)
	require.Error(s.T(), err, "expected error for invalid URL")
}

func (s *GitHubReleaseServiceSuite) TestDownloadFile_InvalidDestPath() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("content"))
	}))

	s.client = newLocalGitHubReleaseClient(s.srv.URL)

	// Use a path that cannot be created (directory doesn't exist)
	dest := filepath.Join(s.tempDir, "nonexistent", "subdir", "file.bin")
	err := s.client.DownloadFile(context.Background(), githubReleaseTestAssetURL, dest, 100)
	require.Error(s.T(), err, "expected error for invalid destination path")
}

func (s *GitHubReleaseServiceSuite) TestFetchChecksumFile_InvalidURL() {
	s.client = newTestGitHubReleaseClient()

	_, err := s.client.FetchChecksumFile(context.Background(), "://invalid-url")
	require.Error(s.T(), err, "expected error for invalid URL")
}

func (s *GitHubReleaseServiceSuite) TestFetchLatestRelease_Success() {
	releaseJSON := `{
		"tag_name": "v1.0.0",
		"name": "Release 1.0.0",
		"body": "Release notes",
		"html_url": "https://github.com/pengkang0322/sub2api/releases/v1.0.0",
		"assets": [
			{
				"name": "app-linux-amd64.tar.gz",
				"url": "https://api.github.com/repos/pengkang0322/sub2api/releases/assets/101",
				"browser_download_url": "https://github.com/pengkang0322/sub2api/releases/download/v1.0.0/app-linux-amd64.tar.gz"
			}
		]
	}`

	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "/repos/pengkang0322/sub2api/releases/latest", r.URL.Path)
		require.Equal(s.T(), "application/vnd.github.v3+json", r.Header.Get("Accept"))
		require.Equal(s.T(), "Sub2API-Updater", r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(releaseJSON))
	}))

	// Use custom transport to redirect requests to test server
	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	release, err := s.client.FetchLatestRelease(context.Background(), "pengkang0322/sub2api")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "v1.0.0", release.TagName)
	require.Equal(s.T(), "Release 1.0.0", release.Name)
	require.Len(s.T(), release.Assets, 1)
	require.Equal(s.T(), "app-linux-amd64.tar.gz", release.Assets[0].Name)
	require.Equal(s.T(), githubReleaseTestAssetURL, release.Assets[0].APIURL)
}

func (s *GitHubReleaseServiceSuite) TestFetchRecentReleases_Success() {
	releasesJSON := `[
		{
			"tag_name": "v1.0.1",
			"name": "Release 1.0.1",
			"html_url": "https://github.com/pengkang0322/sub2api/releases/v1.0.1",
			"published_at": "2026-07-08T00:00:00Z",
			"prerelease": false,
			"assets": [
				{
					"name": "app-linux-amd64.tar.gz",
					"url": "https://api.github.com/repos/pengkang0322/sub2api/releases/assets/101",
					"browser_download_url": "https://github.com/pengkang0322/sub2api/releases/download/v1.0.1/app-linux-amd64.tar.gz"
				}
			]
		},
		{
			"tag_name": "v1.0.1-rc1",
			"name": "Release 1.0.1-rc1",
			"prerelease": true
		},
		{
			"tag_name": "v1.0.0",
			"name": "Release 1.0.0"
		}
	]`

	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "/repos/pengkang0322/sub2api/releases", r.URL.Path)
		require.Equal(s.T(), "15", r.URL.Query().Get("per_page"))
		require.Equal(s.T(), "application/vnd.github.v3+json", r.Header.Get("Accept"))
		require.Equal(s.T(), "Sub2API-Updater", r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(releasesJSON))
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	releases, err := s.client.FetchRecentReleases(context.Background(), "pengkang0322/sub2api", 15)
	require.NoError(s.T(), err)
	require.Len(s.T(), releases, 3)
	require.Equal(s.T(), "v1.0.1", releases[0].TagName)
	require.False(s.T(), releases[0].Prerelease)
	require.Len(s.T(), releases[0].Assets, 1)
	require.Equal(s.T(), githubReleaseTestAssetURL, releases[0].Assets[0].APIURL)
	require.True(s.T(), releases[1].Prerelease)
	require.Equal(s.T(), "v1.0.0", releases[2].TagName)
}

func (s *GitHubReleaseServiceSuite) TestFetchRecentReleases_Non200() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	_, err := s.client.FetchRecentReleases(context.Background(), "test/repo", 15)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "403")
}

func (s *GitHubReleaseServiceSuite) TestFetchLatestRelease_Non200() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	_, err := s.client.FetchLatestRelease(context.Background(), "test/repo")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "404")
}

func (s *GitHubReleaseServiceSuite) TestFetchLatestRelease_InvalidJSON() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not valid json"))
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	_, err := s.client.FetchLatestRelease(context.Background(), "test/repo")
	require.Error(s.T(), err)
}

func (s *GitHubReleaseServiceSuite) TestFetchLatestRelease_ContextCancel() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.client.FetchLatestRelease(ctx, "test/repo")
	require.Error(s.T(), err)
}

func (s *GitHubReleaseServiceSuite) TestFetchChecksumFile_ContextCancel() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))

	s.client = newLocalGitHubReleaseClient(s.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.client.FetchChecksumFile(ctx, githubReleaseTestAssetURL)
	require.Error(s.T(), err)
}

func TestGitHubReleaseServiceSuite(t *testing.T) {
	suite.Run(t, new(GitHubReleaseServiceSuite))
}

func newPrivateAssetTestClient(t *testing.T, transport http.RoundTripper) *githubReleaseClient {
	t.Helper()
	t.Setenv("UPDATE_GITHUB_TOKEN", "test-private-release-token")
	client, ok := NewGitHubReleaseClient("", false).(*githubReleaseClient)
	require.True(t, ok)
	client.httpClient.Transport = transport
	client.downloadHTTPClient.Transport = transport
	return client
}

func fetchTestReleaseAsset(t *testing.T, client *githubReleaseClient, operation, rawURL string) ([]byte, error) {
	t.Helper()
	if operation == "checksum" {
		return client.FetchChecksumFile(context.Background(), rawURL)
	}
	dest := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := client.DownloadFile(context.Background(), rawURL, dest, 1024); err != nil {
		return nil, err
	}
	return os.ReadFile(dest)
}

func TestGitHubReleaseClientPrivateAssetDownloads(t *testing.T) {
	for _, operation := range []string{"archive", "checksum"} {
		for _, redirect := range []bool{false, true} {
			name := operation + "/direct"
			if redirect {
				name = operation + "/redirect"
			}
			t.Run(name, func(t *testing.T) {
				const cdnURL = "https://release-assets.githubusercontent.com/github-production-release-asset/asset?signed=example"
				requests := 0
				transport := githubReleaseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					requests++
					if requests == 1 {
						require.Equal(t, githubReleaseTestAssetURL, req.URL.String())
						require.Equal(t, "Bearer test-private-release-token", req.Header.Get("Authorization"))
						require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
						if redirect {
							return &http.Response{
								StatusCode: http.StatusFound, Header: http.Header{"Location": {cdnURL}},
								Body: io.NopCloser(strings.NewReader("")), Request: req,
							}, nil
						}
					} else {
						require.Equal(t, cdnURL, req.URL.String())
						require.Empty(t, req.Header.Get("Authorization"), "the private repository token must not reach the CDN")
					}
					return &http.Response{
						StatusCode: http.StatusOK, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader("private asset content")), Request: req,
					}, nil
				})
				client := newPrivateAssetTestClient(t, transport)
				body, err := fetchTestReleaseAsset(t, client, operation, githubReleaseTestAssetURL)
				require.NoError(t, err)
				require.Equal(t, "private asset content", string(body))
				if redirect {
					require.Equal(t, 2, requests)
				} else {
					require.Equal(t, 1, requests)
				}
			})
		}
	}
}

func TestGitHubReleaseClientPrivateAssetRejectsUntrustedURLs(t *testing.T) {
	for _, target := range []struct{ name, url string }{
		{"other_repository", "https://api.github.com/repos/Wei-Shaw/sub2api/releases/assets/101"},
		{"non_asset_API", "https://api.github.com/repos/pengkang0322/sub2api/releases/latest"},
		{"third_party", "https://example.com/release.tar.gz"},
		{"lookalike_host", "https://api.github.com.example.com/repos/pengkang0322/sub2api/releases/assets/101"},
		{"API_subdomain", "https://sub.api.github.com/repos/pengkang0322/sub2api/releases/assets/101"},
		{"insecure_API", "http://api.github.com/repos/pengkang0322/sub2api/releases/assets/101"},
		{"insecure_CDN", "http://release-assets.githubusercontent.com/asset"},
		{"userinfo", "https://user@api.github.com/repos/pengkang0322/sub2api/releases/assets/101"},
		{"alternate_port", "https://api.github.com:8443/repos/pengkang0322/sub2api/releases/assets/101"},
	} {
		for _, operation := range []string{"archive", "checksum"} {
			for _, redirect := range []bool{false, true} {
				stage := "initial"
				if redirect {
					stage = "redirect"
				}
				t.Run(operation+"/"+stage+"/"+target.name, func(t *testing.T) {
					requests := 0
					transport := githubReleaseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
						requests++
						require.True(t, redirect, "an untrusted initial URL must not reach the transport")
						require.Equal(t, 1, requests, "the redirected URL must not reach the transport")
						require.Equal(t, githubReleaseTestAssetURL, req.URL.String())
						return &http.Response{
							StatusCode: http.StatusFound, Header: http.Header{"Location": {target.url}},
							Body: io.NopCloser(strings.NewReader("")), Request: req,
						}, nil
					})
					client := newPrivateAssetTestClient(t, transport)
					rawURL := target.url
					if redirect {
						rawURL = githubReleaseTestAssetURL
					}
					_, err := fetchTestReleaseAsset(t, client, operation, rawURL)
					require.Error(t, err)
					if redirect {
						require.Equal(t, 1, requests)
					} else {
						require.Zero(t, requests)
					}
				})
			}
		}
	}
}

func TestGitHubReleaseClientPrivateAssetRedirectLimit(t *testing.T) {
	for _, operation := range []string{"archive", "checksum"} {
		t.Run(operation, func(t *testing.T) {
			requests := 0
			client := newPrivateAssetTestClient(t, githubReleaseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{
					StatusCode: http.StatusFound, Header: http.Header{"Location": {githubReleaseTestAssetURL}},
					Body: io.NopCloser(strings.NewReader("")), Request: req,
				}, nil
			}))
			_, err := fetchTestReleaseAsset(t, client, operation, githubReleaseTestAssetURL)
			require.ErrorContains(t, err, "stopped after 10 redirects")
			require.Equal(t, 10, requests)
		})
	}
}

func TestGitHubReleaseClientPrivateChecksumSizeLimit(t *testing.T) {
	client := newPrivateAssetTestClient(t, githubReleaseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: -1,
			Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 1024*1024+1))), Request: req,
		}, nil
	}))
	_, err := client.FetchChecksumFile(context.Background(), githubReleaseTestAssetURL)
	require.ErrorContains(t, err, "checksums file exceeds maximum size")
}
