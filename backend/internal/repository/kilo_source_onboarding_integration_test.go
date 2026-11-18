//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// All accounts, credentials, proxies and executor results in this file are
// synthetic. The real repositories/worker commit only to newBillingAuditDatabase;
// neither the executor nor KiloRemote opens a network connection.
func TestKiloSourceOnboardingSchemaAndIdempotentRows(t *testing.T) {
	t.Setenv("KILO_EXECUTOR_URL", "")
	t.Setenv("KILO_EXECUTOR_TOKEN", "")
	f := newKiloSourceFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	first, firstProxy := f.source(t, "first", "first-source@example.test")
	second, secondProxy := f.source(t, "second", "second-source@example.test")
	input := f.input([]service.KiloOnboardingItem{
		f.item(second, secondProxy), f.item(first, firstProxy),
	}, true)
	// An ingress node has login-material access but no local executor. It must
	// still persist the same queue and source reservation for maintenance.
	svc := service.NewKiloService(f.db, f.client, f.accounts, &kiloNativeAdmin{}, f.groups, f.proxies, kiloNativeEncryptor{}, f.remote, f.egress, service.NewAccountProxyRouter(f.bindings, f.cache, f.egress), &config.Config{})
	svc.SetOnboardingDependencies(f.reauth, nil)
	receipt, err := svc.SubmitOnboardingBatch(ctx, input)
	require.NoError(t, err)
	replayed, err := f.service().SubmitOnboardingBatch(ctx, input)
	require.NoError(t, err)
	require.Equal(t, receipt, replayed)
	require.Len(t, receipt.Items, 2)
	require.Equal(t, []int64{second.ID, first.ID}, []int64{receipt.Items[0].SourceAccountID, receipt.Items[1].SourceAccountID}, "row source/proxy association must survive normalization")
	changed := input
	changed.Items = append([]service.KiloOnboardingItem(nil), input.Items...)
	changed.Items[0].Name = "changed-idempotent-payload"
	_, err = svc.SubmitOnboardingBatch(ctx, changed)
	require.Equal(t, "KILO_CONFLICT", infraerrors.Reason(err))
	duplicate := input
	duplicate.RequestID = uuid.NewString()
	_, err = svc.SubmitOnboardingBatch(ctx, duplicate)
	require.Equal(t, "KILO_CONFLICT", infraerrors.Reason(err), "active source reservation is independent of request UUID")

	var nullable string
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_schema='public' AND table_name='pk_kilo_jobs' AND column_name='prepared_id'`).Scan(&nullable))
	require.Equal(t, "YES", nullable, "migration 383 must permit jobs before a Gateway credential exists")
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_prepared_accounts`))
	require.EqualValues(t, 2, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_jobs WHERE job_kind='onboard_source' AND prepared_id IS NULL`))
	// The legacy kind still cannot use the new NULL allowance.
	_, err = f.db.ExecContext(ctx, `INSERT INTO pk_kilo_jobs(batch_id,job_kind,options) VALUES($1,'activate_prepared','{}')`, receipt.BatchID)
	require.Error(t, err)

	view, err := f.service().OnboardingBatch(ctx, receipt.BatchID)
	require.NoError(t, err)
	overview, err := f.service().Overview(ctx)
	require.NoError(t, err)
	require.Len(t, view.Items, 2)
	require.Len(t, overview.Jobs, 2)
	for i, job := range view.Items {
		item := input.Items[i]
		require.Nil(t, job.PreparedID)
		require.Nil(t, job.AccountID)
		require.Equal(t, service.KiloJobOnboardSource, job.JobKind)
		require.Equal(t, item.SourceAccountID, *job.SourceAccountID)
		require.Equal(t, item.LoginProxyID, *job.LoginProxyID)
		require.Equal(t, item.OrganizationID, job.OrganizationID)
		source, readErr := f.accounts.GetByID(ctx, item.SourceAccountID)
		require.NoError(t, readErr)
		require.Equal(t, true, source.Extra["kilo_quota_only"])
		require.EqualValues(t, job.ID, service.ParseExtraInt(source.Extra["kilo_onboarding_job_id"]))
		require.EqualValues(t, item.LoginProxyID, service.ParseExtraInt(source.Extra["kilo_onboarding_proxy_id"]))
		require.False(t, source.IsSchedulable(), "stored direct OAuth credential must never become a generation route")
		require.NotEmpty(t, source.Credentials["access_token"], "quota-only preserves the original OAuth credential")
	}
	kiloNativeNoSecrets(t, overview, "synthetic-password-first", "synthetic-password-second", kiloSourceSyntheticTOTP)
	require.Zero(t, f.executor.submissionCount())

	f.step(t, ctx, svc, receipt.Items[0].JobID)
	waiting := f.job(t, ctx, receipt.Items[0].JobID)
	require.Equal(t, "awaiting_authorization", waiting.Status)
	require.Equal(t, "EXECUTOR_UNAVAILABLE", waiting.ErrorCode)
	require.Nil(t, waiting.FinishedAt)
	require.Nil(t, waiting.PreparedID)
	require.Nil(t, waiting.AccountID)
	source, err := f.accounts.GetByID(ctx, *waiting.SourceAccountID)
	require.NoError(t, err)
	require.Equal(t, true, source.Extra["kilo_quota_only"])
	require.False(t, source.IsSchedulable(), "executor absence cannot restore direct OAuth inference")
	require.Zero(t, f.executor.submissionCount())
}

func TestKiloSourceOnboardingQuotaHandoffSerializesOfficialAuth(t *testing.T) {
	f := newKiloSourceFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	source, proxy := f.source(t, "quota-handoff", "quota-source@example.test")
	// Codex recovery may fill this initially absent id without changing the
	// selected mailbox or login-material generation.
	delete(source.Credentials, "chatgpt_account_id")
	require.NoError(t, f.accounts.Update(ctx, source))
	receipt, err := f.service().SubmitOnboardingBatch(ctx, f.input([]service.KiloOnboardingItem{f.item(source, proxy)}, true))
	require.NoError(t, err)
	jobID := receipt.Items[0].JobID
	f.step(t, ctx, f.service(), jobID)
	job := f.job(t, ctx, jobID)
	require.Equal(t, "quota_oauth", job.Stage)
	require.NotNil(t, job.AccountID)
	f.completeQuota(t, ctx, jobID)
	source, err = f.accounts.GetByID(ctx, source.ID)
	require.NoError(t, err)
	source.Credentials["chatgpt_account_id"] = "synthetic-recovered-workspace"
	require.NoError(t, f.accounts.Update(ctx, source))

	// A second official task is visible after Ensure's quota receipt succeeds.
	// The worker must check all official profiles again before its stage update.
	official, err := f.reauthRepo.CreateTask(ctx, source.ID, strings.Repeat("a", 64))
	require.NoError(t, err)
	f.step(t, ctx, f.service(), jobID)
	job = f.job(t, ctx, jobID)
	require.Equal(t, "quota_oauth", job.Stage)
	require.Equal(t, "QUOTA_OAUTH_BUSY", job.ErrorCode)
	require.Zero(t, f.executor.submissionCount())
	f.finishOfficial(t, ctx, official.ID)

	resume := make(chan struct{})
	barrier := &kiloSourceHandoffBarrier{AccountRepository: f.accounts, AccountProxyBindingRepository: f.bindings, locker: f.accounts.(service.KiloAccountMutationLocker), sourceID: source.ID, entered: make(chan int, 1), resume: resume}
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	t.Cleanup(release)
	f.due(t, ctx, jobID)
	worker := kiloNativeStartWorker(t, ctx, f.serviceWithAccounts(barrier))
	var workerPID int
	select {
	case workerPID = <-barrier.entered:
	case <-ctx.Done():
		t.Fatal("quota handoff did not reach its transaction boundary")
	}
	// This repository opens another SQL connection and must block on the same
	// batch lock until the worker publishes kilo_login, then reject creation.
	type createResult struct {
		task *service.OpenAIOAuthReauthTaskRecord
		err  error
	}
	created := make(chan createResult, 1)
	go func() {
		task, createErr := f.reauthRepo.CreateTask(ctx, source.ID, strings.Repeat("b", 64))
		created <- createResult{task: task, err: createErr}
	}()
	require.Eventually(t, func() bool {
		var blocked bool
		err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))`, workerPID).Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	release()
	kiloNativeWorkerFinished(t, worker)
	select {
	case result := <-created:
		require.Nil(t, result.task)
		require.Equal(t, "OPENAI_REAUTH_KILO_BUSY", infraerrors.Reason(result.err))
	case <-ctx.Done():
		t.Fatal("official task creator did not leave handoff lock")
	}
	job = f.job(t, ctx, jobID)
	require.Equal(t, "kilo_login", job.Stage)
	require.True(t, job.QuotaOAuthReady)
	var identity string
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT source_identity FROM pk_kilo_jobs WHERE id=$1`, jobID).Scan(&identity))
	raw, err := json.Marshal([]any{source.ID, source.Platform, source.Type, source.ParentAccountID, "quota-source@example.test", "synthetic-recovered-workspace"})
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	require.Equal(t, hex.EncodeToString(digest[:]), identity, "quota recovery refreshes the source snapshot only after the constrained handoff")
	require.Zero(t, f.executor.submissionCount(), "the handoff transaction must not start remote login")
}

func TestKiloSourceOnboardingUnknownAndCancellationStayFenced(t *testing.T) {
	f := newKiloSourceFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	source, proxy := f.source(t, "unknown-cancel", "unknown-source@example.test")
	receipt, err := f.service().SubmitOnboardingBatch(ctx, f.input([]service.KiloOnboardingItem{f.item(source, proxy)}, true))
	require.NoError(t, err)
	jobID := receipt.Items[0].JobID
	f.step(t, ctx, f.service(), jobID)
	f.completeQuota(t, ctx, jobID)
	f.step(t, ctx, f.service(), jobID)
	f.executor.mu.Lock()
	f.executor.loseSubmitOnce = true
	f.executor.pending = true
	f.executor.mu.Unlock()
	f.step(t, ctx, f.service(), jobID)
	job := f.job(t, ctx, jobID)
	require.Equal(t, "kilo_login", job.Stage)
	require.Equal(t, "awaiting_authorization", job.Status)
	var beforeCipher, beforeOutcome string
	var beforeRevision int64
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT remote_state_encrypted,remote_outcome,revision FROM pk_kilo_jobs WHERE id=$1`, jobID).Scan(&beforeCipher, &beforeOutcome, &beforeRevision))
	require.Equal(t, "unknown", beforeOutcome)
	state := kiloSourceState(t, beforeCipher)
	operation, ok := state["operation_id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, operation)
	require.EqualValues(t, 1, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_egress_leases`))
	require.NoError(t, f.service().RetryJob(ctx, jobID))
	var retriedCipher string
	var retriedRevision int64
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT remote_state_encrypted,revision FROM pk_kilo_jobs WHERE id=$1`, jobID).Scan(&retriedCipher, &retriedRevision))
	require.Equal(t, beforeRevision, retriedRevision)
	require.Equal(t, beforeCipher, retriedCipher)
	f.step(t, ctx, f.service(), jobID)
	require.Equal(t, 1, f.executor.submissionCount(), "response loss must recover the exact operation instead of a second login")
	state = kiloSourceStateForJob(t, ctx, f.db, jobID)
	require.Equal(t, operation, state["operation_id"])
	require.NotEmpty(t, state["task_id"])
	job = f.job(t, ctx, jobID)
	require.Nil(t, job.PreparedID)
	require.False(t, job.CredentialReady)
	gateway, err := f.accounts.GetByID(ctx, *job.AccountID)
	require.NoError(t, err)
	require.False(t, gateway.Schedulable)

	lease := uuid.NewString()
	_, err = f.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='running',lease_token=$2,lease_until=NOW()+INTERVAL '4 minutes' WHERE id=$1`, jobID, lease)
	require.NoError(t, err)
	stale := service.WithKiloOnboardingLease(ctx, jobID, 0, lease)
	tx, err := f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, service.LockKiloOnboardingLease(stale, tx), "zero prepared must validate the reserved source and inactive gateway")
	require.NoError(t, tx.Rollback())
	require.NoError(t, f.service().CancelJob(ctx, jobID))
	tx, err = f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.Error(t, service.LockKiloOnboardingLease(stale, tx), "cancelled nullable-prepared work cannot reuse its old lease token")
	require.NoError(t, tx.Rollback())
	_, err = f.bindings.AssignAccountProxies(service.WithProxyGroupFilter(stale, f.proxyGroupID), service.AccountProxyAssignmentInput{AccountIDs: []int64{gateway.ID}, ProxiesPerAccount: 1, MaxAccountsPerProxy: 1, ProxyConcurrency: 3})
	require.Error(t, err)
	processed, err := f.service().ProcessBatch(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, processed)
	job = f.job(t, ctx, jobID)
	require.Equal(t, "cancelled", job.Status)
	require.Nil(t, job.PreparedID)
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_prepared_accounts`))
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_egress_leases`))
	source, err = f.accounts.GetByID(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, true, source.Extra["kilo_quota_only"])
	require.False(t, source.IsSchedulable())
	require.Positive(t, f.executor.cancelCount())
}

func TestKiloSourceOnboardingSyntheticLifecycle(t *testing.T) {
	for _, activate := range []bool{true, false} {
		t.Run(map[bool]string{true: "activate", false: "import_only"}[activate], func(t *testing.T) {
			f := newKiloSourceFixture(t, 3)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			source, proxy := f.source(t, "lifecycle", "lifecycle-source@example.test")
			input := f.input([]service.KiloOnboardingItem{f.item(source, proxy)}, activate)
			receipt, err := f.service().SubmitOnboardingBatch(ctx, input)
			require.NoError(t, err)
			jobID := receipt.Items[0].JobID
			f.step(t, ctx, f.service(), jobID)
			f.completeQuota(t, ctx, jobID)
			for range 12 {
				job := f.job(t, ctx, jobID)
				if job.Status == "succeeded" || job.Status == "failed" {
					break
				}
				f.step(t, ctx, f.service(), jobID)
			}
			job := f.job(t, ctx, jobID)
			require.Equal(t, "succeeded", job.Status, "stage=%s code=%s error=%s", job.Stage, job.ErrorCode, job.LastError)
			require.Equal(t, map[bool]string{true: "bound", false: "imported"}[activate], job.Stage)
			require.NotNil(t, job.PreparedID)
			require.NotNil(t, job.AccountID)
			require.True(t, job.CredentialReady)
			require.True(t, job.SubscriptionConnected)
			require.True(t, job.QuotaOAuthReady)
			gateway, err := f.accounts.GetByID(ctx, *job.AccountID)
			require.NoError(t, err)
			require.Equal(t, activate, gateway.Schedulable)
			require.Equal(t, map[bool]string{true: service.StatusActive, false: service.StatusDisabled}[activate], gateway.Status)
			require.True(t, gateway.IsModelSupported("openai/synthetic-subscription-model"))
			require.False(t, gateway.IsModelSupported("openai/unverified-model"))
			require.EqualValues(t, *job.PreparedID, service.ParseExtraInt(gateway.Extra["kilo_prepared_id"]))
			require.Equal(t, "synthetic-kilo-source-device-token", gateway.Credentials["api_key"])
			require.ElementsMatch(t, f.groupIDs, gateway.GroupIDs)
			bindings, err := f.bindings.GetAccountProxyBindings(ctx, gateway.ID)
			require.NoError(t, err)
			require.Len(t, bindings, map[bool]int{true: 2, false: 0}[activate])
			require.Equal(t, 3, f.executor.submissionCount())
			require.Equal(t, 1, f.remote.pollCount())
			require.EqualValues(t, 1, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_prepared_accounts`))
			require.EqualValues(t, 1, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM accounts WHERE extra->'kilo_managed'='true'::jsonb`))
			require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_egress_leases`))
			var savedStatus, code, token string
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT status,code_encrypted,token_encrypted FROM pk_kilo_device_sessions WHERE account_id=$1`, gateway.ID).Scan(&savedStatus, &code, &token))
			require.Equal(t, "saved", savedStatus)
			require.Empty(t, code)
			require.Empty(t, token)
			source, err = f.accounts.GetByID(ctx, source.ID)
			require.NoError(t, err)
			require.Equal(t, true, source.Extra["kilo_quota_only"])
			require.False(t, source.IsSchedulable())
			require.EqualValues(t, gateway.ID, service.ParseExtraInt(source.Extra["kilo_source_gateway_id"]))
			require.EqualValues(t, *job.PreparedID, service.ParseExtraInt(source.Extra["kilo_source_prepared_id"]))
			f.remote.mu.Lock()
			require.Empty(t, f.remote.violations)
			f.remote.mu.Unlock()
			view, err := f.service().OnboardingBatch(ctx, receipt.BatchID)
			require.NoError(t, err)
			kiloNativeNoSecrets(t, view, "synthetic-kilo-source-device-token", "synthetic-password-lifecycle", kiloSourceSyntheticTOTP)
		})
	}
}

const kiloSourceSyntheticTOTP = "JBSWY3DPEHPK3PXP"

type kiloSourceFixture struct {
	*kiloNativeFixture
	reauthRepo service.OpenAIOAuthReauthRepository
	reauth     *service.OpenAIOAuthReauthService
	executor   *kiloSourceExecutor
	remote     *kiloSourceRemote
	egress     *KiloEgressRepository
}

type kiloSourceReader struct {
	accounts service.AccountRepository
	proxies  service.ProxyRepository
}

func (r kiloSourceReader) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	return r.accounts.GetByID(ctx, id)
}
func (r kiloSourceReader) GetProxy(ctx context.Context, id int64) (*service.Proxy, error) {
	return r.proxies.GetByID(ctx, id)
}

func newKiloSourceFixture(t *testing.T, proxies int) *kiloSourceFixture {
	t.Helper()
	native := newKiloNativeFixture(t, proxies)
	f := &kiloSourceFixture{kiloNativeFixture: native, reauthRepo: NewOpenAIOAuthReauthRepository(native.db), egress: NewKiloEgressGuard(native.db)}
	f.reauth = service.NewOpenAIOAuthReauthService(f.reauthRepo, kiloSourceReader{native.accounts, native.proxies}, native.accounts.(service.OpenAIOAuthReauthCredentialUpdater), service.NewOpenAIOAuthService(nil, NewOpenAIOAuthClient()), kiloNativeEncryptor{}, true, nil, nil)
	f.executor = &kiloSourceExecutor{instance: "synthetic-kilo-executor", tasks: map[string]*service.KiloExecutorTask{}, operations: map[string]string{}}
	f.remote = &kiloSourceRemote{gatewayUser: "synthetic-kilo-user", gatewayEmail: "kilo-profile@example.test", key: "synthetic-kilo-source-device-token", organization: native.organization}
	return f
}

func (f *kiloSourceFixture) service() *service.KiloService { return f.serviceWithAccounts(f.accounts) }
func (f *kiloSourceFixture) serviceWithAccounts(accounts service.AccountRepository) *service.KiloService {
	svc := service.NewKiloService(f.db, f.client, accounts, &kiloNativeAdmin{}, f.groups, f.proxies, kiloNativeEncryptor{}, f.remote, f.egress, service.NewAccountProxyRouter(f.bindings, f.cache, f.egress), &config.Config{})
	svc.SetOnboardingDependencies(f.reauth, f.executor)
	return svc
}
func (f *kiloSourceFixture) source(t *testing.T, name, email string) (*service.Account, *service.Proxy) {
	t.Helper()
	source := f.kiloNativeFixture.source(t, name, email)
	source.Credentials["chatgpt_account_id"] = "synthetic-workspace-" + name
	source.Credentials["plan_type"] = "plus"
	require.NoError(t, f.accounts.Update(context.Background(), source))
	proxy := mustCreateProxy(t, f.client, &service.Proxy{Name: "kilo-source-login-" + name, Host: "synthetic-" + name + ".invalid", Port: 28080})
	password, err := (kiloNativeEncryptor{}).Encrypt("synthetic-password-" + name)
	require.NoError(t, err)
	totp, err := (kiloNativeEncryptor{}).Encrypt(kiloSourceSyntheticTOTP)
	require.NoError(t, err)
	require.NoError(t, f.reauthRepo.UpsertConfig(context.Background(), &service.OpenAIOAuthReauthStoredConfig{AccountID: source.ID, LoginEmail: email, CredentialMode: service.OpenAIOAuthReauthModePasswordTOTP, Engine: service.OpenAIOAuthReauthEngineLocal, ProxySource: service.OpenAIOAuthReauthProxySourceAccount, PasswordCiphertext: password, TOTPSecretCiphertext: totp}))
	f.remote.mu.Lock()
	f.remote.sourceEmail = email
	f.remote.mu.Unlock()
	return source, proxy
}
func (f *kiloSourceFixture) item(source *service.Account, proxy *service.Proxy) service.KiloOnboardingItem {
	return service.KiloOnboardingItem{SourceAccountID: source.ID, Name: "Kilo " + source.Name, LoginProxyID: proxy.ID, OrganizationID: f.organization, VerifiedModelIDs: []string{"openai/synthetic-subscription-model"}}
}
func (f *kiloSourceFixture) input(items []service.KiloOnboardingItem, activate bool) service.KiloOnboardingBatchInput {
	return service.KiloOnboardingBatchInput{RequestID: uuid.NewString(), Items: items, GroupIDs: append([]int64(nil), f.groupIDs...), ActivateWhenReady: activate, Routing: service.KiloRoutingOptions{ProxyGroupID: f.proxyGroupID, ProxiesPerAccount: 2, ProxyConcurrency: 3, AccountConcurrency: 7, Priority: 19, AutoReleaseStatuses: []int{401, 402, 403}}}
}
func (f *kiloSourceFixture) job(t *testing.T, ctx context.Context, id int64) service.KiloJob {
	t.Helper()
	overview, err := f.service().Overview(ctx)
	require.NoError(t, err)
	for _, job := range overview.Jobs {
		if job.ID == id {
			return job
		}
	}
	t.Fatalf("job %d missing from overview", id)
	return service.KiloJob{}
}
func (f *kiloSourceFixture) due(t *testing.T, ctx context.Context, jobID int64) {
	t.Helper()
	_, err := f.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET next_attempt_at=NOW()-INTERVAL '1 second' WHERE id=$1`, jobID)
	require.NoError(t, err)
}
func (f *kiloSourceFixture) step(t *testing.T, ctx context.Context, svc *service.KiloService, jobID int64) {
	t.Helper()
	f.due(t, ctx, jobID)
	n, err := svc.ProcessBatch(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}
func (f *kiloSourceFixture) completeQuota(t *testing.T, ctx context.Context, jobID int64) {
	t.Helper()
	var taskID int64
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT quota_task_id FROM pk_kilo_jobs WHERE id=$1`, jobID).Scan(&taskID))
	f.finishOfficial(t, ctx, taskID)
}
func (f *kiloSourceFixture) finishOfficial(t *testing.T, ctx context.Context, taskID int64) {
	t.Helper()
	worker := "synthetic-quota-worker-" + uuid.NewString()
	task, err := f.reauthRepo.ClaimNextTask(ctx, worker, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, taskID, task.ID)
	_, claimed, err := f.reauthRepo.BeginDirectCallback(ctx, taskID, worker)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, f.reauthRepo.MarkSucceeded(ctx, taskID, worker))
}
func kiloSourceState(t *testing.T, cipher string) map[string]any {
	t.Helper()
	plain, err := (kiloNativeEncryptor{}).Decrypt(cipher)
	require.NoError(t, err)
	var state map[string]any
	require.NoError(t, json.Unmarshal([]byte(plain), &state))
	return state
}
func kiloSourceStateForJob(t *testing.T, ctx context.Context, db *sql.DB, jobID int64) map[string]any {
	t.Helper()
	var cipher string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT remote_state_encrypted FROM pk_kilo_jobs WHERE id=$1`, jobID).Scan(&cipher))
	return kiloSourceState(t, cipher)
}

// Pauses at the handoff transaction after LockKiloOnboardingLease and the active
// official-task query, while both the batch and account-mutation locks are held.
type kiloSourceHandoffBarrier struct {
	service.AccountRepository
	service.AccountProxyBindingRepository
	locker   service.KiloAccountMutationLocker
	sourceID int64
	entered  chan int
	resume   <-chan struct{}
	once     sync.Once
}

func (r *kiloSourceHandoffBarrier) LockAccountMutation(ctx context.Context) error {
	return r.locker.LockAccountMutation(ctx)
}
func (r *kiloSourceHandoffBarrier) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	if tx := dbent.TxFromContext(ctx); tx != nil && id == r.sourceID {
		var pauseErr error
		r.once.Do(func() {
			rows, err := tx.Client().QueryContext(ctx, `SELECT pg_backend_pid()`)
			if err != nil {
				pauseErr = err
				return
			}
			var pid int
			if rows.Next() {
				pauseErr = rows.Scan(&pid)
			}
			if pauseErr == nil {
				pauseErr = rows.Err()
			}
			_ = rows.Close()
			if pauseErr != nil {
				return
			}
			r.entered <- pid
			select {
			case <-r.resume:
			case <-ctx.Done():
				pauseErr = ctx.Err()
			}
		})
		if pauseErr != nil {
			return nil, pauseErr
		}
	}
	return r.AccountRepository.GetByID(ctx, id)
}

type kiloSourceExecutor struct {
	mu                         sync.Mutex
	instance                   string
	tasks                      map[string]*service.KiloExecutorTask
	operations                 map[string]string
	submissions, cancellations int
	loseSubmitOnce, pending    bool
}

func (e *kiloSourceExecutor) Health(context.Context) (*service.KiloExecutorHealth, error) {
	return &service.KiloExecutorHealth{OK: true, ProtocolVersion: 1, InstanceID: e.instance, AvailableCapacity: 1, Capabilities: []string{"login", "connect_subscription", "approve_device", "cancel", "ack", "fixed_proxy", "session_ttl", "workspace_identity"}}, nil
}
func (e *kiloSourceExecutor) Submit(ctx context.Context, input service.KiloExecutorTaskInput) (*service.KiloExecutorTask, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if taskID := e.operations[input.OperationID]; taskID != "" {
		result := *e.tasks[taskID]
		return &result, nil
	}
	if input.OperationID == "" || input.JobID <= 0 || input.Revision <= 0 || input.LoginProxy.ID <= 0 || !strings.Contains(input.LoginProxy.URL, ".invalid:") || input.ExpectedIdentity.SourceEmail == "" || !input.DeadlineAt.After(time.Now()) {
		return nil, errors.New("invalid synthetic executor input")
	}
	if input.Phase == "login" && (input.Credential == nil || !strings.HasPrefix(input.Credential.Password, "synthetic-password-") || input.Credential.Email != input.ExpectedIdentity.SourceEmail || input.Credential.TOTPSecret != kiloSourceSyntheticTOTP) {
		return nil, errors.New("synthetic login material mismatch")
	}
	if input.Phase != "login" && input.SessionHandle != "synthetic-session" {
		return nil, errors.New("executor session continuity lost")
	}
	if input.Phase == "approve_device" && (input.Device == nil || input.Device.UserCode != "SYNTHETIC" || input.Device.VerificationURL != "https://kilo.invalid/verify") {
		return nil, errors.New("device confirmation does not match issued device")
	}
	task := &service.KiloExecutorTask{TaskID: uuid.NewString(), InstanceID: e.instance, JobID: input.JobID, Revision: input.Revision, Phase: input.Phase, Status: "succeeded", SessionHandle: "synthetic-session", ExpiresAt: time.Now().Add(20 * time.Minute), VerifiedIdentity: service.KiloExecutorIdentity{KiloUserID: "synthetic-kilo-user", OpenAIEmail: input.ExpectedIdentity.SourceEmail}, OrganizationID: input.OrganizationID}
	if e.pending {
		task.Status = "running"
	}
	if input.Phase == "connect_subscription" {
		task.BYOKEnabled = true
		task.BYOKEnableEvidence = "fresh_delegated_authorization"
		task.ConnectionEmail = input.ExpectedIdentity.SourceEmail
		task.ConnectionWorkspaceID = input.ExpectedIdentity.SourceWorkspaceID
		task.ConnectionSubject = "synthetic-openai-client-subject"
		task.ConnectionAt = time.Now().UTC()
	}
	e.tasks[task.TaskID], e.operations[input.OperationID] = task, task.TaskID
	e.submissions++
	if e.loseSubmitOnce {
		e.loseSubmitOnce = false
		return nil, errors.New("synthetic response lost after task registration")
	}
	copy := *task
	return &copy, nil
}
func (e *kiloSourceExecutor) Task(ctx context.Context, taskID string) (*service.KiloExecutorTask, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	task := e.tasks[taskID]
	if task == nil {
		return nil, errors.New("unknown synthetic task")
	}
	copy := *task
	return &copy, nil
}
func (e *kiloSourceExecutor) Cancel(_ context.Context, taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancellations++
	if task := e.tasks[taskID]; task != nil {
		task.Status = "cancelled"
	}
	return nil
}
func (*kiloSourceExecutor) Ack(context.Context, string) error          { return nil }
func (*kiloSourceExecutor) CloseSession(context.Context, string) error { return nil }
func (e *kiloSourceExecutor) submissionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.submissions
}
func (e *kiloSourceExecutor) cancelCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cancellations
}

type kiloSourceRemote struct {
	mu                                                        sync.Mutex
	gatewayUser, gatewayEmail, sourceEmail, key, organization string
	polls                                                     int
	violations                                                []string
}

func (r *kiloSourceRemote) check(ctx context.Context, proxyURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !strings.Contains(proxyURL, ".invalid:") {
		return errors.New("only synthetic login proxy is accepted")
	}
	return nil
}
func (r *kiloSourceRemote) Profile(ctx context.Context, key, proxy string) (*service.KiloRemoteProfile, error) {
	if err := r.check(ctx, proxy); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if key != r.key {
		r.violations = append(r.violations, "wrong gateway key")
		return nil, errors.New("wrong synthetic gateway key")
	}
	return &service.KiloRemoteProfile{ID: r.gatewayUser, Email: r.gatewayEmail, HasPersonalAccount: true, Organizations: []service.KiloOrganization{{ID: r.organization, Name: "synthetic organization"}}}, nil
}
func (r *kiloSourceRemote) Connection(ctx context.Context, key, organization, proxy string) (*service.KiloConnectionStatus, error) {
	if err := r.check(ctx, proxy); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if key != r.key || organization != r.organization {
		r.violations = append(r.violations, "wrong connection scope")
		return nil, errors.New("wrong synthetic scope")
	}
	return &service.KiloConnectionStatus{Connected: true, Email: r.sourceEmail}, nil
}
func (r *kiloSourceRemote) StartDevice(ctx context.Context, proxy string) (*service.KiloRemoteDevice, error) {
	if err := r.check(ctx, proxy); err != nil {
		return nil, err
	}
	return &service.KiloRemoteDevice{Code: "synthetic-device-code", VerificationURL: "https://kilo.invalid/verify", UserCode: "SYNTHETIC", ExpiresIn: 10 * time.Minute, Status: "pending"}, nil
}
func (r *kiloSourceRemote) PollDevice(ctx context.Context, code, proxy string) (*service.KiloRemoteDevice, error) {
	if err := r.check(ctx, proxy); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.polls++
	if code != "synthetic-device-code" || r.polls != 1 {
		r.violations = append(r.violations, "device token replay")
		return nil, errors.New("device token consumed more than once")
	}
	return &service.KiloRemoteDevice{Status: "approved", Token: r.key, Email: r.gatewayEmail}, nil
}
func (r *kiloSourceRemote) pollCount() int { r.mu.Lock(); defer r.mu.Unlock(); return r.polls }

var _ service.KiloOnboardingExecutor = (*kiloSourceExecutor)(nil)
var _ service.KiloRemote = (*kiloSourceRemote)(nil)
