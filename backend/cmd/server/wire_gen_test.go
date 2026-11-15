package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCredentialWorkerHTTPRouteBoundary(t *testing.T) {
	const prefix = "/api/v1/internal/openai-reauth/"
	handler := credentialWorkerHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	allowed := []string{"claim", "session-export/claim", "totp-claim", "runtime-settings", "42/session-export/progress", "42/session-export/result", "42/session-export/fail", "42/totp-phase", "42/totp-finish", "42/totp-recover", "42/progress", "42/callback", "42/credentials", "42/fail"}
	for _, path := range allowed {
		t.Run(path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodOptions} {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(method, prefix+path, nil))
				want := http.StatusNotFound
				if method == http.MethodPost {
					want = http.StatusNoContent
				}
				require.Equal(t, want, recorder.Code, method)
			}
		})
	}
	for _, path := range []string{"/api/v1/admin/accounts", "/v1/responses", "/ready", prefix, prefix + "claim/", prefix + "claims", prefix + "0/callback", prefix + "-1/callback", prefix + "01/callback", prefix + "999999999999999999999/callback", prefix + "42/unknown", prefix + "42/callback/extra", prefix + "42/../claim", prefix + "%63laim", prefix + "42%2fcallback"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		require.Equal(t, http.StatusNotFound, recorder.Code, path)
	}
}

func TestCredentialWorkerHTTPRealAuthenticationAndShutdown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	token := strings.Repeat("w", 32)
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", token)
	router := gin.New()
	reauth := adminhandler.NewOpenAIOAuthReauthHandler(nil)
	router.POST("/api/v1/internal/openai-reauth/runtime-settings", reauth.RuntimeSettings)
	router.POST("/api/v1/admin/accounts", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{Addr: listener.Addr().String(), Handler: router, ReadHeaderTimeout: time.Second}
	require.NoError(t, configureCredentialWorkerHTTP(server))
	defer func() { _ = server.Close() }()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	client := &http.Client{Timeout: 2 * time.Second}
	for _, tc := range []struct {
		path, supplied string
		want           int
	}{
		{"/api/v1/internal/openai-reauth/runtime-settings", "", http.StatusUnauthorized},
		{"/api/v1/internal/openai-reauth/runtime-settings", "wrong", http.StatusUnauthorized},
		{"/api/v1/internal/openai-reauth/runtime-settings", token, http.StatusOK},
		{"/api/v1/admin/accounts", token, http.StatusNotFound},
	} {
		req, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+tc.path, strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("X-OpenAI-Reauth-Worker-Token", tc.supplied)
		resp, err := client.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, tc.want, resp.StatusCode)
		require.NotContains(t, string(body), token)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, server.Shutdown(ctx))
	require.ErrorIs(t, <-done, http.ErrServerClosed)
}

func TestCredentialWorkerHTTPListenAddress(t *testing.T) {
	for _, tc := range []struct{ input, output string }{
		{":8080", "127.0.0.1:8080"}, {"0.0.0.0:8080", "127.0.0.1:8080"},
		{"[::]:8080", "[::1]:8080"}, {"127.0.0.1:8080", "127.0.0.1:8080"},
		{"[::1]:8080", "[::1]:8080"},
	} {
		server := &http.Server{Addr: tc.input, Handler: http.NotFoundHandler()}
		require.NoError(t, configureCredentialWorkerHTTP(server), tc.input)
		require.Equal(t, tc.output, server.Addr)
	}
	for _, address := range []string{"0.0.0.0", "192.0.2.1:8080", "example.test:8080", "localhost:8080", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:http"} {
		server := &http.Server{Addr: address, Handler: http.NotFoundHandler()}
		require.Error(t, configureCredentialWorkerHTTP(server), address)
		require.Equal(t, address, server.Addr)
	}
	require.Error(t, configureCredentialWorkerHTTP(nil))
	require.Error(t, configureCredentialWorkerHTTP(&http.Server{Addr: "127.0.0.1:8080"}))
}

func TestProvideServiceBuildInfo(t *testing.T) {
	in := handler.BuildInfo{
		Version:   "v-test",
		BuildType: "release",
	}
	out := provideServiceBuildInfo(in)
	require.Equal(t, in.Version, out.Version)
	require.Equal(t, in.BuildType, out.BuildType)
}

func TestProvideCleanup_WithMinimalDependencies_NoPanic(t *testing.T) {
	cfg := &config.Config{}

	oauthSvc := service.NewOAuthService(nil, nil)
	openAIOAuthSvc := service.NewOpenAIOAuthService(nil, nil)
	geminiOAuthSvc := service.NewGeminiOAuthService(nil, nil, nil, nil, cfg)
	antigravityOAuthSvc := service.NewAntigravityOAuthService(nil)

	tokenRefreshSvc := service.NewTokenRefreshService(
		nil,
		oauthSvc,
		openAIOAuthSvc,
		geminiOAuthSvc,
		antigravityOAuthSvc,
		nil,
		nil,
		cfg,
		nil,
	)
	accountExpirySvc := service.NewAccountExpiryService(nil, time.Second)
	codexVersionSyncSvc := service.NewOpenAICodexVersionSyncService(nil, nil, nil, time.Second)
	claudeCodeVersionSyncSvc := service.NewClaudeCodeVersionSyncService(nil, nil, nil, time.Second)
	proxyExpirySvc := service.NewProxyExpiryService(nil, time.Second)
	subscriptionExpirySvc := service.NewSubscriptionExpiryService(nil, time.Second)
	pricingSvc := service.NewPricingService(cfg, nil)
	emailQueueSvc := service.NewEmailQueueService(nil, 1)
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	idempotencyCleanupSvc := service.NewIdempotencyCleanupService(nil, cfg)
	schedulerSnapshotSvc := service.NewSchedulerSnapshotService(nil, nil, nil, nil, cfg)
	opsSystemLogSinkSvc := service.NewOpsSystemLogSink(nil)
	accountPerformanceSvc := service.NewAccountPerformanceService(nil)

	cleanup := provideCleanup(
		nil, // balance forecast owner
		nil, // default/full runtime configuration
		nil, // request capture manager
		nil, // entClient
		nil, // redis
		&service.OpsMetricsCollector{},
		&service.OpsAggregationService{},
		&service.OpsAlertEvaluatorService{},
		&service.OpsCleanupService{},
		&service.OpsScheduledReportService{},
		opsSystemLogSinkSvc,
		nil, // opsService
		nil, // opsIngressRejectAggregator
		nil, // apiKeyService
		nil, // concurrencyService
		nil, // authCacheInvalidationWorker
		nil, // cyberPolicyBalanceWorker
		schedulerSnapshotSvc,
		tokenRefreshSvc,
		accountExpirySvc,
		nil, // cnProviderBalanceCheck
		codexVersionSyncSvc,
		claudeCodeVersionSyncSvc,
		proxyExpirySvc,
		subscriptionExpirySvc,
		&service.UsageCleanupService{},
		idempotencyCleanupSvc,
		service.NewGeminiCachedContentService(nil, nil, cfg),
		&service.BatchImageCleanupService{},
		nil, // batchImageWorker
		pricingSvc,
		emailQueueSvc,
		billingCacheSvc,
		&service.UsageRecordWorkerPool{},
		&service.SubscriptionService{},
		oauthSvc,
		openAIOAuthSvc,
		geminiOAuthSvc,
		antigravityOAuthSvc,
		nil, // grokOAuth
		nil, // openAIGateway
		nil, // rateLimitService
		nil, // scheduledTestRunner
		nil, // controlledExperiment
		nil, // accountOps
		accountPerformanceSvc,
		nil, // accountTokenGuard
		nil, // accountTokenGuardV2
		nil, // backupSvc
		nil, // paymentOrderExpiry
		nil, // binanceRecharge
		nil, // channelMonitorRunner
		service.NewIntelligenceTestService(nil, nil, nil, nil, nil, cfg),
		service.NewAccountCandyTestService(nil, nil, nil, nil, nil),
		nil, // accountAuthorization
		nil, // accountUsage
		nil, // channelMonitorV2Aggregator
		nil, // channelMonitorV3Prober
		nil, // quotaFlusher
		nil, // upstreamBillingProbe
		nil, // ollamaCloudUsage
		nil, // opencodeGoUsage
		nil, // auditLog
		nil, // openAIAutoReset
		nil, // promptAudit
		nil, // pluginManager
		nil, // cyberModeration
	)

	require.NotPanics(t, func() {
		cleanup()
	})
}
