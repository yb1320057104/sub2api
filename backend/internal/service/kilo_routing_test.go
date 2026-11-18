package service

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiloRoutingProxyReader struct{ ProxyRepository }

func (kiloRoutingProxyReader) GetByID(_ context.Context, id int64) (*Proxy, error) {
	return &Proxy{ID: id, Status: StatusActive}, nil
}

type kiloRoutingJSONMatcher func(driver.Value) bool

func (match kiloRoutingJSONMatcher) Match(value driver.Value) bool { return match(value) }

type kiloRoutingAssignmentRepo struct {
	AccountRepository
	AccountProxyBindingRepository
	assign func(context.Context, AccountProxyAssignmentInput) (*AccountProxyAssignmentResult, error)
}

func (r kiloRoutingAssignmentRepo) AssignAccountProxies(ctx context.Context, input AccountProxyAssignmentInput) (*AccountProxyAssignmentResult, error) {
	return r.assign(ctx, input)
}

type kiloFailureChainRepo struct {
	AccountRepository
	recorded             bool
	err                  error
	calls, genericWrites int
}

func (r *kiloFailureChainRepo) RecordKiloGatewayFailure(context.Context, *Account, int, string) (bool, error) {
	r.calls++
	return r.recorded, r.err
}
func (r *kiloFailureChainRepo) SetError(context.Context, int64, string) error {
	r.genericWrites++
	return nil
}
func (r *kiloFailureChainRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.genericWrites++
	return nil
}

func TestKiloFailureLifecycleOwnsStaleCASAndPersistenceErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		recorded  bool
		err       error
		status    int
		body      string
		wantCalls int
	}{
		{name: "stale_key_401", status: 401, body: `{"error":{"code":"invalid_api_key"}}`, wantCalls: 1},
		{name: "stale_binding_402", status: 402, body: `{"error":{"code":"account_restricted"}}`, wantCalls: 1},
		{name: "stale_identity_403", status: 403, body: `{"error":{"code":"account_banned"}}`, wantCalls: 1},
		{name: "current_identity_403", status: 403, body: `{"error":{"code":"account_banned"}}`, recorded: true, wantCalls: 1},
		{name: "database_error", status: 401, err: errors.New("synthetic unavailable"), wantCalls: 1},
		{name: "current_failure", status: 401, recorded: true, wantCalls: 1},
		{name: "provider_permission_403", status: 403, body: `{"error":{"code":"permission_denied"}}`},
	} {
		for _, boundary := range []string{"shared", "gateway", "stream"} {
			t.Run(test.name+"/"+boundary, func(t *testing.T) {
				repo := &kiloFailureChainRepo{recorded: test.recorded, err: test.err}
				rate := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
				gateway := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rate}
				account := &Account{ID: 124, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"api_key": "old-synthetic-key", "base_url": KiloGatewayBaseURL},
					Extra:       map[string]any{KiloManagedExtraKey: true}}
				var disabled bool
				switch boundary {
				case "shared":
					disabled = rate.HandleUpstreamError(context.Background(), account, test.status, http.Header{}, []byte(test.body))
				case "gateway":
					disabled = gateway.handleOpenAIAccountUpstreamError(context.Background(), account, test.status, http.Header{}, []byte(test.body))
				case "stream":
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					payload := []byte(`{"type":"response.failed","response":{"error":{"status":` + strconv.Itoa(test.status) + `,"code":"invalid_api_key"}}}`)
					if test.status == http.StatusForbidden {
						payload = []byte(`{"type":"response.failed","response":{"error":{"status":403,"code":"permission_denied"}}}`)
						if test.wantCalls > 0 {
							payload = []byte(`{"type":"response.failed","response":{"error":{"status":403,"code":"account_banned"}}}`)
						}
					}
					_, disabled = gateway.handleOpenAIStreamTerminalAccountSideEffects(c, account, payload, "", http.Header{})
				}
				require.Equal(t, test.recorded && test.err == nil, disabled)
				require.Equal(t, test.wantCalls, repo.calls)
				require.Zero(t, repo.genericWrites, "Kilo CAS no-op/errors cannot become generic SetError or cooldown")
				require.Equal(t, StatusActive, account.Status)
			})
		}
	}
}

func TestKiloRequestProxyPool(t *testing.T) {
	for _, tc := range []struct {
		name     string
		id       int64
		valid    bool
		filtered bool
	}{
		{"named_group", 9, true, true},
		{"all_eligible", kiloAllRequestProxies, true, false},
		{"unselected", 0, false, false},
		{"invalid", -2, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const requestID = "61a9de91-c725-43fd-a3ba-609261d53d08"
			loginProxyID := int64(41)
			onboarding := KiloOnboardingBatchInput{RequestID: requestID, GroupIDs: []int64{7},
				Items:   []KiloOnboardingItem{{SourceAccountID: 20, Name: "fixture", LoginProxyID: loginProxyID}},
				Routing: KiloRoutingOptions{ProxyGroupID: tc.id, ProxiesPerAccount: 2, ProxyConcurrency: 7, AccountConcurrency: 14}}
			batch := KiloBatchInput{RequestID: requestID, PreparedIDs: []int64{12}, GroupIDs: []int64{7},
				ProxyGroupID: tc.id, ProxiesPerAccount: 2, ProxyConcurrency: 7, AccountConcurrency: 14}
			if !tc.valid {
				require.Error(t, normalizeKiloOnboarding(&onboarding))
				require.Error(t, validateKiloBatch(batch))
				return
			}
			require.NoError(t, normalizeKiloOnboarding(&onboarding))
			require.Equal(t, tc.id, onboarding.Routing.ProxyGroupID)
			require.Equal(t, loginProxyID, onboarding.Items[0].LoginProxyID)
			require.NoError(t, validateKiloBatch(batch))

			svc, mock, accounts, _ := kiloCleanupFixture(t)
			job := &kiloClaim{ID: 1, Lease: "lease", Options: batch, LoginProxyID: &loginProxyID}
			mock.ExpectQuery("SELECT EXISTS").WithArgs(job.ID, job.Lease).
				WillReturnRows(sqlmock.NewRows([]string{"owned"}).AddRow(true))
			capacityErr := kiloBad("PROXY_POOL_CAPACITY", "fixture capacity")
			svc.accounts = kiloRoutingAssignmentRepo{AccountRepository: accounts, assign: func(ctx context.Context, input AccountProxyAssignmentInput) (*AccountProxyAssignmentResult, error) {
				id, filtered := ProxyGroupFilterFromContext(ctx)
				require.Equal(t, tc.filtered, filtered)
				if filtered {
					require.Equal(t, tc.id, id)
				}
				require.Equal(t, AccountProxyAssignmentInput{AccountIDs: []int64{42}, ProxiesPerAccount: 2, MaxAccountsPerProxy: 1, ProxyConcurrency: 7}, input)
				return nil, capacityErr
			}}
			require.ErrorIs(t, svc.prepareJobEgress(context.Background(), job, &Account{ID: 42}), capacityErr)
			require.Equal(t, int64(41), *job.LoginProxyID)
		})
	}
}

func TestKiloRoutingRecoveryPreservesCompletedAuthorization(t *testing.T) {
	for _, mode := range []string{"valid", "unclosed", "stale_revision", "missing_key", "other_gateway", "busy", "other_failure"} {
		t.Run(mode, func(t *testing.T) {
			svc, mock, accounts, job := kiloCleanupFixture(t)
			svc.proxies = kiloRoutingProxyReader{}
			const preparedID int64 = 3
			job.PreparedID, job.Stage, job.Outcome = ptrKiloCleanup(preparedID), "subscription_verified", "cleaned"
			job.CredentialReady, job.Connected = true, true
			account := accounts.items[*job.AccountID]
			account.Credentials["api_key"] = "synthetic-existing-key"
			account.Extra["kilo_prepared_id"] = preparedID
			state := &kiloOnboardingRuntime{Version: 1, Revision: job.Revision, RemoteClosed: true,
				SourceEmail: "synthetic@example.test", SourceWorkspace: "personal-workspace", SourcePlanType: "plus",
				SourceIssuer: "https://auth.openai.com", SourceSubject: "original-subject",
				ConnectionSubject: "original-subject", ConnectionIdentityEvidence: "personal_subject",
				ConnectionAt: time.Now().Add(-time.Minute), BYOKEnableEvidence: "fresh_delegated_authorization",
				kiloOnboardingState: kiloOnboardingState{RemoteUserID: "original-kilo-owner", BYOKEnabled: true}}
			switch mode {
			case "unclosed":
				state.RemoteClosed = false
			case "stale_revision":
				state.Revision++
			case "missing_key":
				delete(account.Credentials, "api_key")
			case "other_gateway":
				account.Extra["kilo_onboarding_source_id"] = int64(999)
			}
			raw, err := json.Marshal(state)
			require.NoError(t, err)
			job.Cipher, err = svc.encryptor.Encrypt(string(raw))
			require.NoError(t, err)
			input := KiloRoutingInput{LoginProxyID: job.LoginProxyID, KiloRoutingOptions: KiloRoutingOptions{
				ProxyGroupID: 1, ProxiesPerAccount: 4, ProxyConcurrency: 20, AccountConcurrency: 80, AutoReleaseStatuses: []int{}}}
			lockKey := preparedID ^ int64(-5424871661416316928)
			mock.ExpectExec("SELECT pg_advisory_lock").WithArgs(lockKey).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT status,account_id,login_proxy_id,binding_job_id").WithArgs(preparedID).
				WillReturnRows(sqlmock.NewRows([]string{"status", "account", "proxy", "job"}).AddRow("binding", *job.AccountID, *job.LoginProxyID, job.ID))
			expectKiloCleanupRead(mock, job)
			early := mode == "unclosed" || mode == "stale_revision" || mode == "missing_key" || mode == "other_gateway"
			if !early {
				code := "PROXY_POOL_CAPACITY"
				if mode == "other_failure" {
					code = "MFA_REJECTED"
				}
				originalOptions := `{"group_ids":[75],"source_account_ids":[131],"request_id":"original-import","proxies_per_account":5,"account_concurrency":100,"auto_release_statuses":[]}`
				mock.ExpectQuery("SELECT options,error_code,organization_id,quota_oauth_ready").WithArgs(job.ID).
					WillReturnRows(sqlmock.NewRows([]string{"options", "code", "organization", "quota_ready"}).AddRow(originalOptions, code, "", true))
				if mode != "other_failure" {
					mock.ExpectQuery("SELECT EXISTS.*pk_kilo_jobs.*openai_oauth_reauth_tasks.*pk_kilo_egress_leases").
						WithArgs(job.ID, job.SourceID, account.ID, preparedID).
						WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(mode == "busy"))
				}
			}
			if mode == "valid" {
				argument := kiloRoutingJSONMatcher(func(value driver.Value) bool {
					text, ok := value.(string)
					if !ok {
						return false
					}
					var options map[string]any
					if json.Unmarshal([]byte(text), &options) != nil {
						return false
					}
					return options["request_id"] == "original-import" && options["proxies_per_account"] == float64(4) &&
						options["account_concurrency"] == float64(80) && len(options["group_ids"].([]any)) == 1 &&
						len(options["source_account_ids"].([]any)) == 1 && len(options["auto_release_statuses"].([]any)) == 0
				})
				mock.ExpectExec("UPDATE pk_kilo_jobs SET options=").WithArgs(job.ID, job.Revision, argument).
					WillReturnResult(sqlmock.NewResult(0, 1))
				preparedOptions, marshalErr := json.Marshal(input.KiloRoutingOptions)
				require.NoError(t, marshalErr)
				mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET login_proxy_id=").
					WithArgs(preparedID, *input.LoginProxyID, string(preparedOptions)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(lockKey).
				WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
			err = svc.SetRouting(context.Background(), preparedID, input)
			if mode == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Empty(t, accounts.updated)
			require.Empty(t, accounts.deleted)
			require.Equal(t, "failed", job.Status)
			require.Equal(t, "subscription_verified", job.Stage)
			require.Equal(t, "original-kilo-owner", state.RemoteUserID)
			require.True(t, state.BYOKEnabled)
		})
	}
}

func TestKiloRoutingDefaultsAndExplicitPolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input KiloRoutingOptions
		want  KiloRoutingOptions
	}{
		{
			name:  "five_request_ips_with_twenty_slots_each",
			input: KiloRoutingOptions{},
			want:  KiloRoutingOptions{ProxiesPerAccount: 5, ProxyConcurrency: 20, AccountConcurrency: 100, AutoReleaseStatuses: []int{401, 402, 403}},
		},
		{
			name:  "derive_account_capacity_from_custom_request_pool",
			input: KiloRoutingOptions{ProxyGroupID: 12, ProxiesPerAccount: 3, ProxyConcurrency: 7, Priority: 19},
			want:  KiloRoutingOptions{ProxyGroupID: 12, ProxiesPerAccount: 3, ProxyConcurrency: 7, AccountConcurrency: 21, Priority: 19, AutoReleaseStatuses: []int{401, 402, 403}},
		},
		{
			name:  "preserve_explicit_account_cap_and_disabled_auto_release",
			input: KiloRoutingOptions{ProxyGroupID: 12, ProxiesPerAccount: 3, ProxyConcurrency: 7, AccountConcurrency: 9, AutoReleaseStatuses: []int{}},
			want:  KiloRoutingOptions{ProxyGroupID: 12, ProxiesPerAccount: 3, ProxyConcurrency: 7, AccountConcurrency: 9, AutoReleaseStatuses: []int{}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, normalizeKiloRouting(&tc.input))
			require.Equal(t, tc.want, tc.input)
		})
	}
}

func TestKiloRoutingBatchPersistsDisabledAutoRelease(t *testing.T) {
	input := KiloBatchInput{AutoReleaseStatuses: []int{}, ProxiesPerAccount: 5, ProxyConcurrency: 20, AccountConcurrency: 100}
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	var restored KiloBatchInput
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.NotNil(t, restored.AutoReleaseStatuses, "job persistence must distinguish an explicit disabled policy from a historical missing field")
	var options KiloRoutingOptions
	require.NoError(t, json.Unmarshal(kiloRoutingJSON(restored), &options))
	require.NoError(t, normalizeKiloRouting(&options))
	require.Empty(t, options.AutoReleaseStatuses)
}

func TestKiloRoutingRejectsInvalidAutoReleaseStatuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []int
	}{
		{name: "success_is_not_a_release_trigger", statuses: []int{200}},
		{name: "redirect_is_not_a_release_trigger", statuses: []int{399}},
		{name: "outside_http_error_range", statuses: []int{600}},
		{name: "negative_status", statuses: []int{-1}},
		{name: "duplicate_trigger", statuses: []int{401, 403, 401}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := KiloRoutingOptions{AutoReleaseStatuses: tc.statuses}
			require.Error(t, normalizeKiloRouting(&options))
		})
	}
	options := KiloRoutingOptions{AutoReleaseStatuses: []int{400, 429, 599}}
	require.NoError(t, normalizeKiloRouting(&options))
	require.Equal(t, []int{400, 429, 599}, options.AutoReleaseStatuses)
}

func TestKiloSourceBindingPreservesManualSchedulability(t *testing.T) {
	for _, tc := range []struct {
		name           string
		manualSchedule bool
	}{
		{name: "enabled_source_returns_after_release", manualSchedule: true},
		{name: "disabled_source_stays_disabled_after_release", manualSchedule: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{
				Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
				Schedulable: tc.manualSchedule,
				Credentials: map[string]any{"email": "synthetic-source@example.test", "access_token": "synthetic-source-token"},
				Extra:       map[string]any{"administrator_note": "preserve"},
			}
			require.Equal(t, tc.manualSchedule, account.IsSchedulable())
			account.Extra["kilo_source_prepared_id"] = int64(17)
			account.Extra["kilo_source_gateway_id"] = int64(23)
			// Repository JSON round-tripping changes the numeric representation.
			raw, err := json.Marshal(account.Extra)
			require.NoError(t, err)
			var persisted map[string]any
			require.NoError(t, json.Unmarshal(raw, &persisted))
			account.Extra = persisted
			require.False(t, account.IsSchedulable(), "a bound subscription must not receive direct OAuth traffic")
			require.False(t, account.IsCredentialUsableForShadow(), "shadow routes cannot bypass the bound subscription's Kilo mode")
			require.Equal(t, tc.manualSchedule, account.Schedulable)
			delete(account.Extra, "kilo_source_prepared_id")
			delete(account.Extra, "kilo_source_gateway_id")
			require.Equal(t, tc.manualSchedule, account.IsSchedulable())
			require.Equal(t, tc.manualSchedule, account.Schedulable)
			require.Equal(t, "preserve", account.Extra["administrator_note"])
			require.Equal(t, "synthetic-source-token", account.Credentials["access_token"])
		})
	}
}
