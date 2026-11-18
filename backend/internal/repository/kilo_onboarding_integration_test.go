//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestKiloNativeImportAndBatchContracts(t *testing.T) {
	f := newKiloNativeFixture(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	firstKey, secondKey := "synthetic-kilo-import-first", "synthetic-kilo-import-second"
	input := service.KiloPreparedImportInput{
		RequestID: uuid.NewString(),
		Items: []service.KiloPreparedImportItem{
			{Name: "prepared-first", APIKey: firstKey, OrganizationID: f.organization},
			{Name: "prepared-second", APIKey: secondKey, OrganizationID: f.organization},
			{Name: "same-key-again", APIKey: firstKey, OrganizationID: f.organization},
			{Name: "", APIKey: "synthetic-kilo-invalid-entry"},
		},
	}
	svc := f.service()
	invalidID := input
	invalidID.RequestID = "not-a-uuid"
	_, err := svc.ImportPrepared(ctx, invalidID)
	require.Error(t, err)
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_prepared_accounts`))

	imported, err := svc.ImportPrepared(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 2, imported.Imported)
	require.Equal(t, 1, imported.Duplicates)
	require.Equal(t, 1, imported.Failed)
	require.Len(t, imported.Items, 4)
	require.Equal(t, []string{"imported", "imported", "duplicate", "failed"}, []string{imported.Items[0].Status, imported.Items[1].Status, imported.Items[2].Status, imported.Items[3].Status})
	require.NotNil(t, imported.Items[0].ID)
	require.NotNil(t, imported.Items[1].ID)
	require.Equal(t, imported.Items[0].ID, imported.Items[2].ID)
	require.Nil(t, imported.Items[3].ID)

	replayed, err := f.service().ImportPrepared(ctx, input)
	require.NoError(t, err)
	require.Equal(t, imported, replayed, "retry must return the original per-entry receipt")
	changed := input
	changed.Items = append([]service.KiloPreparedImportItem(nil), input.Items...)
	changed.Items[0].Name = "different-request-payload"
	_, err = svc.ImportPrepared(ctx, changed)
	require.Error(t, err, "a request UUID cannot identify a different payload")

	duplicate, err := svc.ImportPrepared(ctx, service.KiloPreparedImportInput{
		RequestID: uuid.NewString(),
		Items:     []service.KiloPreparedImportItem{input.Items[0]},
	})
	require.NoError(t, err)
	require.Equal(t, 0, duplicate.Imported)
	require.Equal(t, 1, duplicate.Duplicates)
	require.Equal(t, 0, duplicate.Failed)
	require.Equal(t, imported.Items[0].ID, duplicate.Items[0].ID)
	require.EqualValues(t, 2, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_prepared_accounts`))
	require.EqualValues(t, 2, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_import_requests`))
	var storedCredential string
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT credential_encrypted FROM pk_kilo_prepared_accounts WHERE id=$1`, *imported.Items[0].ID).Scan(&storedCredential))
	require.True(t, strings.HasPrefix(storedCredential, kiloNativeCipherPrefix), "import must use the configured credential owner")
	require.NotEqual(t, firstKey, storedCredential)
	kiloNativeNoSecrets(t, imported, firstKey, secondKey)

	firstSource := f.source(t, "source-first", "first-source@example.test")
	secondSource := f.source(t, "source-second", "second-source@example.test")
	// Reverse prepared IDs so a repository sort cannot silently change the
	// administrator's one-to-one selection order.
	batch := f.batch([]int64{*imported.Items[1].ID, *imported.Items[0].ID}, []int64{firstSource.ID, secondSource.ID})
	mismatch := batch
	mismatch.RequestID = uuid.NewString()
	mismatch.SourceAccountIDs = mismatch.SourceAccountIDs[:1]
	_, err = svc.SubmitBatch(ctx, mismatch)
	require.Error(t, err)
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_batches`))
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_jobs`))
	require.EqualValues(t, 2, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_prepared_accounts WHERE status='ready' AND binding_job_id IS NULL`))

	type submission struct {
		id  string
		err error
	}
	start := make(chan struct{})
	results := make(chan submission, 4)
	for range cap(results) {
		go func() {
			<-start
			id, submitErr := f.service().SubmitBatch(ctx, batch)
			results <- submission{id: id, err: submitErr}
		}()
	}
	close(start)
	var batchID string
	for range cap(results) {
		select {
		case result := <-results:
			require.NoError(t, result.err)
			require.NoError(t, uuid.Validate(result.id))
			if batchID == "" {
				batchID = result.id
			}
			require.Equal(t, batchID, result.id, "independent ingress instances must recover the same batch")
		case <-ctx.Done():
			t.Fatal("concurrent batch submissions did not finish")
		}
	}
	require.EqualValues(t, 1, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_batches`))
	require.EqualValues(t, 2, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_jobs`))

	rows, err := f.db.QueryContext(ctx, `SELECT j.prepared_id,j.source_account_id,j.source_identity,j.options,p.binding_job_id=j.id,p.status FROM pk_kilo_jobs j JOIN pk_kilo_prepared_accounts p ON p.id=j.prepared_id WHERE j.batch_id=$1 ORDER BY j.id`, batchID)
	require.NoError(t, err)
	defer rows.Close()
	var preparedIDs, sourceIDs []int64
	for rows.Next() {
		var preparedID, sourceID int64
		var identity, status string
		var options []byte
		var ownsPrepared bool
		require.NoError(t, rows.Scan(&preparedID, &sourceID, &identity, &options, &ownsPrepared, &status))
		preparedIDs = append(preparedIDs, preparedID)
		sourceIDs = append(sourceIDs, sourceID)
		require.Len(t, identity, 64, "the persisted source identity must survive a new worker instance")
		require.True(t, ownsPrepared)
		require.Equal(t, "binding", status)
		var saved service.KiloBatchInput
		require.NoError(t, json.Unmarshal(options, &saved))
		require.Equal(t, batch, saved)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, batch.PreparedIDs, preparedIDs)
	require.Equal(t, batch.SourceAccountIDs, sourceIDs)
	changedBatch := batch
	changedBatch.Priority++
	_, err = svc.SubmitBatch(ctx, changedBatch)
	require.Error(t, err)
	require.EqualValues(t, 1, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_batches`))
	require.EqualValues(t, 2, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_jobs`))
	overview, err := svc.Overview(ctx)
	require.NoError(t, err)
	require.True(t, overview.EncryptionReady)
	require.Len(t, overview.Prepared, 2)
	require.Len(t, overview.Jobs, 2)
	kiloNativeNoSecrets(t, overview, firstKey, secondKey, "synthetic-openai-source-first", "synthetic-openai-source-second")
}

func TestKiloNativeWorkerPersistenceAndActivationGuards(t *testing.T) {
	f := newKiloNativeFixture(t, 12)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	t.Run("resumes_disconnected_job_once", func(t *testing.T) {
		key := "synthetic-kilo-resume"
		preparedID := f.prepared(t, ctx, "resume", key)
		source := f.source(t, "resume", "expected-resume@example.test")
		f.remote.expect(key, "resume", f.organization, service.KiloConnectionStatus{})
		batch := f.batch([]int64{preparedID}, []int64{source.ID})
		_, err := f.service().SubmitBatch(ctx, batch)
		require.NoError(t, err)

		processed, err := f.service().ProcessBatch(ctx, 20)
		require.NoError(t, err)
		require.Equal(t, 1, processed)
		prepared, job := f.snapshot(t, ctx, preparedID)
		require.Equal(t, "binding", prepared.Status)
		require.Equal(t, "awaiting_authorization", job.Status)
		require.Equal(t, "official_authorization", job.Stage)
		require.Equal(t, "https://app.kilo.ai/organizations/"+f.organization+"/byok", job.VerificationURL)
		require.Nil(t, prepared.SourceAccountID, "an expected local pairing is not remote ownership evidence")
		account := f.disabledAccount(t, ctx, prepared)
		accountID := account.ID
		require.NotNil(t, prepared.LoginProxyID)
		require.Equal(t, batch.LoginProxyIDs[preparedID], *prepared.LoginProxyID)
		require.EqualValues(t, batch.LoginProxyIDs[preparedID], service.ParseExtraInt(account.Extra[service.KiloLoginProxyExtraKey]))
		bindings, err := f.bindings.GetAccountProxyBindings(ctx, accountID)
		require.NoError(t, err)
		require.Empty(t, bindings, "request exits must not be allocated while official authorization is incomplete")
		waitingSource, err := f.accounts.GetByID(ctx, source.ID)
		require.NoError(t, err)
		require.NotContains(t, waitingSource.Extra, "kilo_source_prepared_id")
		require.True(t, waitingSource.IsSchedulable())
		waitingEffects := f.effects(t)
		require.Equal(t, 1, waitingEffects.loginVerifications)
		require.Zero(t, waitingEffects.verifications)
		require.Equal(t, 1, waitingEffects.loginAcquires)
		require.Zero(t, waitingEffects.acquires, "login calls must bypass the random request-pool router")
		require.EqualValues(t, 1, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM accounts WHERE extra->>'kilo_managed'='true'`))

		f.remote.setConnection(key, service.KiloConnectionStatus{Connected: true, Email: " EXPECTED-RESUME@EXAMPLE.TEST "})
		require.NoError(t, f.service().RetryJob(ctx, job.ID))
		processed, err = f.service().ProcessBatch(ctx, 20)
		require.NoError(t, err)
		require.Equal(t, 1, processed)
		prepared, job = f.snapshot(t, ctx, preparedID)
		require.Equal(t, "bound", prepared.Status)
		require.Equal(t, "succeeded", job.Status)
		require.NotNil(t, job.FinishedAt)
		require.Empty(t, job.LastError)
		require.Empty(t, job.VerificationURL)
		require.Equal(t, &accountID, prepared.AccountID)
		require.Equal(t, &accountID, job.AccountID)
		require.Equal(t, &source.ID, prepared.SourceAccountID)
		account, err = f.accounts.GetByID(ctx, accountID)
		require.NoError(t, err)
		require.Equal(t, service.StatusActive, account.Status)
		require.True(t, account.Schedulable)
		require.Equal(t, service.PlatformOpenAI, account.Platform)
		require.Equal(t, service.AccountTypeAPIKey, account.Type)
		require.Equal(t, batch.AccountConcurrency, account.Concurrency)
		require.Equal(t, batch.Priority, account.Priority)
		require.ElementsMatch(t, batch.GroupIDs, account.GroupIDs)
		require.Equal(t, batch.LoginProxyIDs[preparedID], *prepared.LoginProxyID, "retry must retain the fixed login identity")
		f.assertBindings(t, ctx, accountID, batch)
		boundSource, err := f.accounts.GetByID(ctx, source.ID)
		require.NoError(t, err)
		require.EqualValues(t, preparedID, service.ParseExtraInt(boundSource.Extra["kilo_source_prepared_id"]))
		require.EqualValues(t, accountID, service.ParseExtraInt(boundSource.Extra["kilo_source_gateway_id"]))
		require.Equal(t, source.Credentials, boundSource.Credentials)
		require.True(t, boundSource.Schedulable, "successful binding must preserve the administrator's scheduling switch")
		require.False(t, boundSource.IsSchedulable(), "the bound source must no longer receive direct OAuth traffic")
		require.EqualValues(t, 1, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM accounts WHERE extra->>'kilo_managed'='true'`))
		before := f.effects(t)
		require.Equal(t, 2, before.loginVerifications)
		require.Equal(t, batch.ProxiesPerAccount, before.verifications)
		require.Equal(t, 2, before.loginAcquires)
		require.Zero(t, before.acquires)
		processed, err = f.service().ProcessBatch(ctx, 20)
		require.NoError(t, err)
		require.Zero(t, processed)
		require.Equal(t, before, f.effects(t), "a terminal job must not repeat remote checks, allocation or activation")
		overview, err := f.service().Overview(ctx)
		require.NoError(t, err)
		kiloNativeNoSecrets(t, overview, key, "synthetic-openai-resume")
	})

	t.Run("rejects_mismatched_remote_identity", func(t *testing.T) {
		before := f.effects(t)
		key := "synthetic-kilo-mismatched-owner"
		preparedID := f.prepared(t, ctx, "mismatch", key)
		source := f.source(t, "mismatch", "expected-owner@example.test")
		f.remote.expect(key, "mismatch", f.organization, service.KiloConnectionStatus{Connected: true, Email: "another-owner@example.test"})
		_, err := f.service().SubmitBatch(ctx, f.batch([]int64{preparedID}, []int64{source.ID}))
		require.NoError(t, err)
		processed, err := f.service().ProcessBatch(ctx, 20)
		require.NoError(t, err)
		require.Equal(t, 1, processed)
		prepared, job := f.snapshot(t, ctx, preparedID)
		require.Equal(t, "failed", job.Status)
		require.Equal(t, "failed", job.Stage)
		require.Contains(t, job.LastError, "身份")
		require.Equal(t, "ready", prepared.Status)
		require.Nil(t, prepared.SourceAccountID)
		account := f.disabledAccount(t, ctx, prepared)
		bindings, err := f.bindings.GetAccountProxyBindings(ctx, account.ID)
		require.NoError(t, err)
		require.Empty(t, bindings, "a mismatched remote identity cannot reserve request exits")
		effects := f.effects(t)
		require.Equal(t, before.verifications, effects.verifications)
		require.Equal(t, before.loginVerifications+1, effects.loginVerifications)
	})

	t.Run("rejects_source_identity_drift_during_connection", func(t *testing.T) {
		key := "synthetic-kilo-source-drift"
		preparedID := f.prepared(t, ctx, "drift", key)
		source := f.source(t, "drift", "expected-drift@example.test")
		f.remote.expect(key, "drift", f.organization, service.KiloConnectionStatus{Connected: true, Email: "expected-drift@example.test"})
		barrier, entered, release := kiloNativeBarrier(t)
		f.remote.setHooks(key, nil, barrier)
		_, err := f.service().SubmitBatch(ctx, f.batch([]int64{preparedID}, []int64{source.ID}))
		require.NoError(t, err)
		result := kiloNativeStartWorker(t, ctx, f.service())
		kiloNativeWait(t, entered)
		// Connection has already received the original expected source. A
		// changed source must be revalidated in the activation transaction.
		changed, err := f.accounts.GetByID(ctx, source.ID)
		require.NoError(t, err)
		changed.Credentials["chatgpt_account_id"] = "synthetic-replaced-subscription"
		require.NoError(t, f.accounts.Update(ctx, changed))
		release()
		kiloNativeWorkerFinished(t, result)
		prepared, job := f.snapshot(t, ctx, preparedID)
		require.Equal(t, "failed", job.Status)
		require.Contains(t, job.LastError, "变化")
		require.Equal(t, "ready", prepared.Status)
		require.Nil(t, prepared.SourceAccountID)
		f.disabledAccount(t, ctx, prepared)
		f.effects(t)
	})

	t.Run("cancelled_claim_cannot_activate", func(t *testing.T) {
		key := "synthetic-kilo-cancelled-claim"
		preparedID := f.prepared(t, ctx, "cancel", key)
		source := f.source(t, "cancel", "expected-cancel@example.test")
		f.remote.expect(key, "cancel", f.organization, service.KiloConnectionStatus{Connected: true, Email: "expected-cancel@example.test"})
		barrier, entered, release := kiloNativeBarrier(t)
		f.remote.setHooks(key, barrier, nil)
		_, err := f.service().SubmitBatch(ctx, f.batch([]int64{preparedID}, []int64{source.ID}))
		require.NoError(t, err)
		result := kiloNativeStartWorker(t, ctx, f.service())
		kiloNativeWait(t, entered)
		prepared, job := f.snapshot(t, ctx, preparedID)
		require.Equal(t, "running", job.Status)
		accountID := f.disabledAccount(t, ctx, prepared).ID
		cancelled := make(chan error, 1)
		go func() { cancelled <- f.service().CancelJob(ctx, job.ID) }()
		// Cancellation commits before waiting for the prepared operation lock;
		// observe that durable fence before allowing the old worker to resume.
		require.Eventually(t, func() bool {
			var status string
			err := f.db.QueryRowContext(ctx, `SELECT status FROM pk_kilo_jobs WHERE id=$1`, job.ID).Scan(&status)
			return err == nil && status == "cancelled"
		}, 10*time.Second, 10*time.Millisecond)
		select {
		case cancelErr := <-cancelled:
			t.Fatalf("cancel returned before the claimed worker drained: %v", cancelErr)
		default:
		}
		release()
		kiloNativeWorkerFinished(t, result)
		select {
		case cancelErr := <-cancelled:
			require.NoError(t, cancelErr)
		case <-time.After(10 * time.Second):
			t.Fatal("cancel did not finish after the claimed worker drained")
		}
		prepared, job = f.snapshot(t, ctx, preparedID)
		require.Equal(t, "cancelled", job.Status)
		require.Equal(t, "cancelled", job.Stage)
		require.NotNil(t, job.FinishedAt)
		require.Equal(t, "ready", prepared.Status)
		require.Nil(t, prepared.SourceAccountID)
		require.Equal(t, accountID, f.disabledAccount(t, ctx, prepared).ID)
		before := f.effects(t)
		processed, err := f.service().ProcessBatch(ctx, 20)
		require.NoError(t, err)
		require.Zero(t, processed)
		require.Equal(t, before, f.effects(t))
	})

	t.Run("rejects_gateway_credential_drift_before_activation", func(t *testing.T) {
		key := "synthetic-kilo-gateway-drift"
		preparedID := f.prepared(t, ctx, "gateway-drift", key)
		source := f.source(t, "gateway-drift", "expected-gateway-drift@example.test")
		f.remote.expect(key, "gateway-drift", f.organization, service.KiloConnectionStatus{Connected: true, Email: "expected-gateway-drift@example.test"})
		barrier, entered, release := kiloNativeBarrier(t)
		f.remote.setHooks(key, barrier, nil)
		_, err := f.service().SubmitBatch(ctx, f.batch([]int64{preparedID}, []int64{source.ID}))
		require.NoError(t, err)
		result := kiloNativeStartWorker(t, ctx, f.service())
		kiloNativeWait(t, entered)
		prepared, job := f.snapshot(t, ctx, preparedID)
		require.Equal(t, "running", job.Status)
		account := f.disabledAccount(t, ctx, prepared)
		account.Credentials["api_key"] = "synthetic-replaced-gateway-key"
		require.NoError(t, f.accounts.Update(ctx, account))
		release()
		kiloNativeWorkerFinished(t, result)
		prepared, job = f.snapshot(t, ctx, preparedID)
		require.Equal(t, "failed", job.Status)
		require.NotEmpty(t, job.LastError)
		require.Equal(t, "ready", prepared.Status)
		require.Nil(t, prepared.SourceAccountID)
		account = f.disabledAccount(t, ctx, prepared)
		require.Equal(t, "synthetic-replaced-gateway-key", account.Credentials["api_key"], "remote verification of the prepared key cannot authorize a different gateway key")
		f.effects(t)
	})

	t.Run("rejects_target_group_disabled_before_activation", func(t *testing.T) {
		key := "synthetic-kilo-disabled-target"
		preparedID := f.prepared(t, ctx, "disabled-target", key)
		source := f.source(t, "disabled-target", "expected-disabled-target@example.test")
		f.remote.expect(key, "disabled-target", f.organization, service.KiloConnectionStatus{Connected: true, Email: "expected-disabled-target@example.test"})
		barrier, entered, release := kiloNativeBarrier(t)
		f.remote.setHooks(key, barrier, nil)
		_, err := f.service().SubmitBatch(ctx, f.batch([]int64{preparedID}, []int64{source.ID}))
		require.NoError(t, err)
		result := kiloNativeStartWorker(t, ctx, f.service())
		kiloNativeWait(t, entered)
		require.NoError(t, f.client.Group.UpdateOneID(f.groupIDs[0]).SetStatus(service.StatusDisabled).Exec(ctx))
		release()
		kiloNativeWorkerFinished(t, result)
		prepared, job := f.snapshot(t, ctx, preparedID)
		require.Equal(t, "failed", job.Status)
		require.Contains(t, job.LastError, "分组")
		require.Equal(t, "ready", prepared.Status)
		require.Nil(t, prepared.SourceAccountID)
		f.disabledAccount(t, ctx, prepared)
		currentSource, err := f.accounts.GetByID(ctx, source.ID)
		require.NoError(t, err)
		require.NotContains(t, currentSource.Extra, "kilo_source_prepared_id", "failed activation must roll back the source marker in the same transaction")
		require.NotContains(t, currentSource.Extra, "kilo_source_gateway_id")
		require.True(t, currentSource.Schedulable)
		require.True(t, currentSource.IsSchedulable())
		f.effects(t)
	})
}

const kiloNativeCipherPrefix = "synthetic-kilo-fixture:"

// This reversible wrapper accepts synthetic fixture values only; it is not an
// encryption implementation and never reads configured production credentials.
type kiloNativeEncryptor struct{}

func (kiloNativeEncryptor) Encrypt(value string) (string, error) {
	return kiloNativeCipherPrefix + base64.StdEncoding.EncodeToString([]byte(value)), nil
}

func (kiloNativeEncryptor) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, kiloNativeCipherPrefix) {
		return "", errors.New("unexpected fixture ciphertext")
	}
	plain, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, kiloNativeCipherPrefix))
	return string(plain), err
}

type kiloNativeAdmin struct{ service.AdminService }

func (*kiloNativeAdmin) ValidateAccountGroupBindings(context.Context, []int64) error { return nil }

type kiloNativeFixture struct {
	t             *testing.T
	db            *sql.DB
	client        *dbent.Client
	accounts      service.AccountRepository
	bindings      service.AccountProxyBindingRepository
	groups        service.GroupRepository
	proxies       service.ProxyRepository
	groupIDs      []int64
	proxyGroupID  int64
	organization  string
	selected      map[int64]string
	loginProxyIDs map[int64]int64
	outsideID     int64
	remote        *kiloNativeRemote
	egress        *kiloNativeEgress
	cache         *kiloNativeProxyCache
}

func newKiloNativeFixture(t *testing.T, proxyCount int) *kiloNativeFixture {
	t.Helper()
	db, client := newBillingAuditDatabase(t)
	f := &kiloNativeFixture{
		t: t, db: db, client: client, organization: uuid.NewString(), selected: map[int64]string{}, loginProxyIDs: map[int64]int64{},
		accounts: NewAccountRepository(client, db, nil), groups: NewGroupRepository(client, db),
		proxies: NewProxyRepository(client, db), cache: &kiloNativeProxyCache{leases: map[string]int64{}},
	}
	f.bindings = f.accounts.(service.AccountProxyBindingRepository)
	for _, name := range []string{"kilo-native-target-first", "kilo-native-target-second"} {
		group := mustCreateGroup(t, client, &service.Group{Name: name, Platform: service.PlatformOpenAI})
		f.groupIDs = append(f.groupIDs, group.ID)
	}
	// An older, active proxy outside the selected pool exposes accidental
	// allocation from the global proxy table instead of the requested group.
	f.outsideID = mustCreateProxy(t, client, &service.Proxy{Name: "outside-kilo-pool", Host: "outside.invalid", Port: 18080}).ID
	proxyGroups := f.proxies.(service.ProxyGroupRepository)
	pool, err := proxyGroups.SaveProxyGroup(context.Background(), 0, service.ProxyGroupInput{Name: "kilo-native-selected-pool"})
	require.NoError(t, err)
	f.proxyGroupID = pool.ID
	var proxyIDs []int64
	for i := range proxyCount {
		proxy := mustCreateProxy(t, client, &service.Proxy{Name: fmt.Sprintf("kilo-native-proxy-%d", i), Host: fmt.Sprintf("kilo-proxy-%d.invalid", i), Port: 18080 + i})
		proxyIDs = append(proxyIDs, proxy.ID)
		f.selected[proxy.ID] = proxy.URL()
	}
	_, err = proxyGroups.SetProxyGroupMembers(context.Background(), service.ProxyGroupMembersInput{ProxyIDs: proxyIDs, ProxyGroupID: &pool.ID})
	require.NoError(t, err)
	f.remote = &kiloNativeRemote{cases: map[string]kiloNativeRemoteCase{}, proxyURLs: map[string]bool{}}
	f.egress = &kiloNativeEgress{accounts: f.accounts, reader: f.bindings, selected: f.selected, login: map[int64]string{}, verified: map[[2]int64]int{}, loginVerified: map[[2]int64]int{}}
	return f
}

func (f *kiloNativeFixture) service() *service.KiloService {
	router := service.NewAccountProxyRouter(f.bindings, f.cache, f.egress)
	return service.NewKiloService(f.db, f.client, f.accounts, &kiloNativeAdmin{}, f.groups, f.proxies, kiloNativeEncryptor{}, f.remote, f.egress, router, &config.Config{})
}

func (f *kiloNativeFixture) source(t *testing.T, name, email string) *service.Account {
	t.Helper()
	return mustCreateAccount(t, f.client, &service.Account{
		Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"email": email, "access_token": "synthetic-openai-" + name, "chatgpt_account_id": "synthetic-subscription-" + name},
		Extra:       map[string]any{"fixture_note": "preserve-source-preference"},
	})
}

func (f *kiloNativeFixture) prepared(t *testing.T, ctx context.Context, name, key string) int64 {
	t.Helper()
	result, err := f.service().ImportPrepared(ctx, service.KiloPreparedImportInput{RequestID: uuid.NewString(), Items: []service.KiloPreparedImportItem{{Name: name, APIKey: key, OrganizationID: f.organization}}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Imported)
	require.Len(t, result.Items, 1)
	require.NotNil(t, result.Items[0].ID)
	preparedID := *result.Items[0].ID
	options := f.batch([]int64{preparedID}, nil)
	loginID := options.LoginProxyIDs[preparedID]
	require.NoError(t, f.service().SetRouting(ctx, preparedID, service.KiloRoutingInput{
		LoginProxyID: &loginID,
		KiloRoutingOptions: service.KiloRoutingOptions{
			ProxyGroupID: options.ProxyGroupID, ProxiesPerAccount: options.ProxiesPerAccount,
			ProxyConcurrency: options.ProxyConcurrency, AccountConcurrency: options.AccountConcurrency,
			Priority: options.Priority, AutoReleaseStatuses: options.AutoReleaseStatuses,
		},
	}))
	return preparedID
}

func (f *kiloNativeFixture) batch(preparedIDs, sourceIDs []int64) service.KiloBatchInput {
	loginIDs := make(map[int64]int64, len(preparedIDs))
	for _, preparedID := range preparedIDs {
		loginID, exists := f.loginProxyIDs[preparedID]
		if !exists {
			// Keep authentication proxies outside the selected request group so
			// the old pool-size and one-to-one allocation assertions stay exact.
			proxy := mustCreateProxy(f.t, f.client, &service.Proxy{Name: fmt.Sprintf("kilo-native-login-%d", preparedID), Host: fmt.Sprintf("kilo-login-%d.invalid", preparedID), Port: 28080})
			loginID = proxy.ID
			f.loginProxyIDs[preparedID] = loginID
			f.egress.mu.Lock()
			f.egress.login[loginID] = proxy.URL()
			f.egress.mu.Unlock()
			f.remote.mu.Lock()
			f.remote.proxyURLs[proxy.URL()] = true
			f.remote.mu.Unlock()
		}
		loginIDs[preparedID] = loginID
	}
	return service.KiloBatchInput{
		RequestID: uuid.NewString(), PreparedIDs: preparedIDs, SourceAccountIDs: sourceIDs,
		GroupIDs: f.groupIDs, ProxyGroupID: f.proxyGroupID, ProxiesPerAccount: 2,
		ProxyConcurrency: 3, AccountConcurrency: 7, Priority: 19,
		LoginProxyIDs: loginIDs, AutoReleaseStatuses: []int{401, 402, 403}, BYOKEnabledConfirmed: true,
	}
}

func (f *kiloNativeFixture) snapshot(t *testing.T, ctx context.Context, preparedID int64) (service.KiloPrepared, service.KiloJob) {
	t.Helper()
	overview, err := f.service().Overview(ctx)
	require.NoError(t, err)
	var prepared service.KiloPrepared
	var job service.KiloJob
	for _, item := range overview.Prepared {
		if item.ID == preparedID {
			prepared = item
		}
	}
	for _, item := range overview.Jobs {
		if item.PreparedID != nil && *item.PreparedID == preparedID {
			job = item
			break
		}
	}
	require.Equal(t, preparedID, prepared.ID)
	require.NotZero(t, job.ID)
	return prepared, job
}

func (f *kiloNativeFixture) disabledAccount(t *testing.T, ctx context.Context, prepared service.KiloPrepared) *service.Account {
	t.Helper()
	require.NotNil(t, prepared.AccountID, "the worker must persist one disabled account before external checks")
	account, err := f.accounts.GetByID(ctx, *prepared.AccountID)
	require.NoError(t, err)
	require.Equal(t, true, account.Extra[service.KiloManagedExtraKey])
	require.Equal(t, service.StatusDisabled, account.Status)
	require.False(t, account.Schedulable)
	require.Empty(t, account.GroupIDs, "group publication belongs to successful activation only")
	return account
}

func (f *kiloNativeFixture) assertBindings(t *testing.T, ctx context.Context, accountID int64, batch service.KiloBatchInput) {
	t.Helper()
	bindings, err := f.bindings.GetAccountProxyBindings(ctx, accountID)
	require.NoError(t, err)
	require.Len(t, bindings, batch.ProxiesPerAccount)
	account, err := f.accounts.GetByID(ctx, accountID)
	require.NoError(t, err)
	loginID := int64(service.ParseExtraInt(account.Extra[service.KiloLoginProxyExtraKey]))
	require.Positive(t, loginID)
	_, loginInRequestPool := f.selected[loginID]
	require.False(t, loginInRequestPool)
	seen := map[int64]bool{}
	for _, binding := range bindings {
		_, selected := f.selected[binding.ProxyID]
		require.True(t, selected, "all bound proxies must belong to the selected real proxy group")
		require.NotEqual(t, f.outsideID, binding.ProxyID)
		require.NotEqual(t, loginID, binding.ProxyID, "fixed login ownership must never become an ordinary request binding")
		require.False(t, seen[binding.ProxyID], "each requested exit must be a different proxy")
		seen[binding.ProxyID] = true
		require.Equal(t, batch.ProxyConcurrency, binding.MaxConcurrency)
		f.egress.mu.Lock()
		verified := f.egress.verified[[2]int64{accountID, binding.ProxyID}]
		f.egress.mu.Unlock()
		require.Positive(t, verified, "each persisted binding must pass the egress boundary before activation")
	}
}

func kiloNativeCount(t *testing.T, ctx context.Context, db *sql.DB, query string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.QueryRowContext(ctx, query).Scan(&count))
	return count
}

func kiloNativeNoSecrets(t *testing.T, value any, secrets ...string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	for _, secret := range secrets {
		require.NotContains(t, string(encoded), secret)
	}
	for _, field := range []string{`"api_key":`, `"access_token":`, `"credential_encrypted":`, `"credential_fingerprint":`} {
		require.NotContains(t, string(encoded), field)
	}
}

type kiloNativeRemoteCase struct {
	profile        service.KiloRemoteProfile
	connection     service.KiloConnectionStatus
	organization   string
	profileHook    func(context.Context) error
	connectionHook func(context.Context) error
}

type kiloNativeRemote struct {
	mu              sync.Mutex
	cases           map[string]kiloNativeRemoteCase
	proxyURLs       map[string]bool
	profileCalls    int
	connectionCalls int
	violations      []string
}

func (r *kiloNativeRemote) expect(key, name, organization string, connection service.KiloConnectionStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cases[key] = kiloNativeRemoteCase{
		profile:    service.KiloRemoteProfile{ID: uuid.NewString(), Email: "prepared-" + name + "@example.test", HasPersonalAccount: true, Organizations: []service.KiloOrganization{{ID: organization, Name: "fixture organization"}}},
		connection: connection, organization: organization,
	}
}

func (r *kiloNativeRemote) setConnection(key string, connection service.KiloConnectionStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.cases[key]
	item.connection = connection
	r.cases[key] = item
}

func (r *kiloNativeRemote) setHooks(key string, profile, connection func(context.Context) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.cases[key]
	item.profileHook, item.connectionHook = profile, connection
	r.cases[key] = item
}

func (r *kiloNativeRemote) Profile(ctx context.Context, key, proxyURL string) (*service.KiloRemoteProfile, error) {
	r.mu.Lock()
	r.profileCalls++
	item, exists := r.cases[key]
	permit, leased := ctx.Value(kiloNativeLoginContextKey{}).(kiloNativeLoginRoute)
	validRoute := r.proxyURLs[proxyURL] && leased && permit.proxyURL == proxyURL && ctx.Err() == nil
	if !exists || !validRoute {
		r.violations = append(r.violations, "profile used an unexpected credential or an unleased/non-login route")
	}
	r.mu.Unlock()
	if !exists || !validRoute {
		return nil, errors.New("unexpected synthetic profile request")
	}
	if item.profileHook != nil {
		if err := item.profileHook(ctx); err != nil {
			return nil, err
		}
	}
	return &item.profile, nil
}

func (r *kiloNativeRemote) Connection(ctx context.Context, key, organization, proxyURL string) (*service.KiloConnectionStatus, error) {
	r.mu.Lock()
	r.connectionCalls++
	item, exists := r.cases[key]
	permit, leased := ctx.Value(kiloNativeLoginContextKey{}).(kiloNativeLoginRoute)
	validRoute := r.proxyURLs[proxyURL] && leased && permit.proxyURL == proxyURL && ctx.Err() == nil
	if !exists || !validRoute || item.organization != organization {
		r.violations = append(r.violations, "connection used an unexpected credential, organization or unleased/non-login route")
	}
	r.mu.Unlock()
	if !exists || !validRoute || item.organization != organization {
		return nil, errors.New("unexpected synthetic connection request")
	}
	if item.connectionHook != nil {
		if err := item.connectionHook(ctx); err != nil {
			return nil, err
		}
	}
	return &item.connection, nil
}

func (*kiloNativeRemote) StartDevice(context.Context, string) (*service.KiloRemoteDevice, error) {
	return nil, errors.New("device authorization is outside the onboarding fixture")
}

func (*kiloNativeRemote) PollDevice(context.Context, string, string) (*service.KiloRemoteDevice, error) {
	return nil, errors.New("device polling is outside the onboarding fixture")
}

type kiloNativeLoginContextKey struct{}

type kiloNativeLoginRoute struct{ proxyURL string }

type kiloNativeEgress struct {
	mu            sync.Mutex
	accounts      service.AccountRepository
	reader        service.AccountProxyBindingReader
	selected      map[int64]string
	login         map[int64]string
	verified      map[[2]int64]int
	loginVerified map[[2]int64]int
	acquires      int
	releases      int
	livePermits   int
}

func (e *kiloNativeEgress) Verify(ctx context.Context, accountID, proxyID int64) (*service.KiloEgressRoute, error) {
	account, err := e.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	bindings, err := e.reader.GetAccountProxyBindings(ctx, accountID)
	if err != nil {
		return nil, err
	}
	loginID := int64(service.ParseExtraInt(account.Extra[service.KiloLoginProxyExtraKey]))
	e.mu.Lock()
	defer e.mu.Unlock()
	if proxyID == loginID && e.login[proxyID] != "" {
		for _, binding := range bindings {
			if binding.ProxyID == proxyID {
				return nil, errors.New("login ownership must remain outside ordinary bindings")
			}
		}
		e.loginVerified[[2]int64{accountID, proxyID}]++
		return &service.KiloEgressRoute{AccountID: accountID, ProxyID: proxyID, EgressIP: "1.1.1.1", State: service.KiloEgressActive, Purpose: "login"}, nil
	}
	_, selected := e.selected[proxyID]
	for _, binding := range bindings {
		if binding.ProxyID == proxyID && selected && proxyID != loginID {
			e.verified[[2]int64{accountID, proxyID}]++
			return &service.KiloEgressRoute{AccountID: accountID, ProxyID: proxyID, EgressIP: fmt.Sprintf("8.8.%d.%d", proxyID/250%250, proxyID%250+1), State: service.KiloEgressActive, Purpose: "request"}, nil
		}
	}
	return nil, errors.New("verification requires fixed login ownership or a real binding in the selected fixture pool")
}

// This permit replaces the external boundary only; it never opens a socket.
func (e *kiloNativeEgress) Acquire(ctx context.Context, accountID, proxyID int64, proxyURL string) (*service.KiloEgressPermit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	account, err := e.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	bindings, err := e.reader.GetAccountProxyBindings(ctx, accountID)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if binding.ProxyID == proxyID {
			return nil, errors.New("login acquisition cannot borrow an ordinary request binding")
		}
	}
	e.mu.Lock()
	if int64(service.ParseExtraInt(account.Extra[service.KiloLoginProxyExtraKey])) != proxyID || e.login[proxyID] != proxyURL || proxyURL == "" || e.loginVerified[[2]int64{accountID, proxyID}] == 0 {
		e.mu.Unlock()
		return nil, service.ErrKiloEgressUnavailable
	}
	e.acquires++
	e.livePermits++
	e.mu.Unlock()
	permitCtx, cancel := context.WithCancel(ctx)
	permitCtx = context.WithValue(permitCtx, kiloNativeLoginContextKey{}, kiloNativeLoginRoute{proxyURL: proxyURL})
	return service.NewKiloEgressPermit(permitCtx, func() {
		cancel()
		e.mu.Lock()
		e.releases++
		e.livePermits--
		e.mu.Unlock()
	}), nil
}

func (*kiloNativeEgress) List(context.Context, int64) ([]service.KiloEgressRoute, error) {
	return nil, errors.New("egress management is outside the onboarding fixture")
}

func (*kiloNativeEgress) Release(context.Context, int64) error {
	return errors.New("egress release is outside the onboarding fixture")
}

type kiloNativeProxyCache struct {
	mu       sync.Mutex
	leases   map[string]int64
	acquires int
	releases int
}

func (c *kiloNativeProxyCache) AcquireProxy(ctx context.Context, proxyID int64, limit int, lease string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, current := range c.leases {
		if current == proxyID {
			count++
		}
	}
	if limit > 0 && count >= limit {
		return false, nil
	}
	c.leases[lease] = proxyID
	c.acquires++
	return true, nil
}

func (c *kiloNativeProxyCache) RefreshProxy(ctx context.Context, proxyID int64, lease string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.leases[lease] == proxyID, nil
}

func (c *kiloNativeProxyCache) ReleaseProxy(_ context.Context, proxyID int64, lease string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.leases[lease] == proxyID {
		delete(c.leases, lease)
		c.releases++
	}
	return nil
}

func (c *kiloNativeProxyCache) GetProxyConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := map[int64]int{}
	for _, id := range ids {
		for _, proxyID := range c.leases {
			if proxyID == id {
				counts[id]++
			}
		}
	}
	return counts, nil
}

type kiloNativeEffects struct {
	profiles, connections, verifications, acquires, releases int
	loginVerifications, loginAcquires, loginReleases         int
}

func (f *kiloNativeFixture) effects(t *testing.T) kiloNativeEffects {
	t.Helper()
	var out kiloNativeEffects
	f.remote.mu.Lock()
	out.profiles, out.connections = f.remote.profileCalls, f.remote.connectionCalls
	violations := append([]string(nil), f.remote.violations...)
	f.remote.mu.Unlock()
	require.Empty(t, violations)
	f.egress.mu.Lock()
	for _, count := range f.egress.verified {
		out.verifications += count
	}
	for _, count := range f.egress.loginVerified {
		out.loginVerifications += count
	}
	out.loginAcquires, out.loginReleases = f.egress.acquires, f.egress.releases
	livePermits := f.egress.livePermits
	f.egress.mu.Unlock()
	require.Zero(t, livePermits, "every worker call must close its fixed login permit")
	require.Equal(t, out.loginAcquires, out.loginReleases)
	f.cache.mu.Lock()
	out.acquires, out.releases = f.cache.acquires, f.cache.releases
	live := len(f.cache.leases)
	f.cache.mu.Unlock()
	require.Zero(t, live, "every completed worker call must release its proxy lease")
	require.Equal(t, out.acquires, out.releases)
	return out
}

func kiloNativeBarrier(t *testing.T) (func(context.Context) error, <-chan struct{}, func()) {
	t.Helper()
	entered, proceed := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	return func(ctx context.Context) error {
		enterOnce.Do(func() { close(entered) })
		select {
		case <-proceed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, entered, release
}

type kiloNativeWorkerResult struct {
	processed int
	err       error
}

func kiloNativeStartWorker(t *testing.T, ctx context.Context, svc *service.KiloService) <-chan kiloNativeWorkerResult {
	t.Helper()
	workerCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	results, done := make(chan kiloNativeWorkerResult, 1), make(chan struct{})
	go func() {
		defer close(done)
		processed, err := svc.ProcessBatch(workerCtx, 1)
		results <- kiloNativeWorkerResult{processed: processed, err: err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("fixture worker did not stop before database cleanup")
		}
	})
	return results
}

func kiloNativeWait(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not reach the synthetic remote barrier")
	}
}

func kiloNativeWorkerFinished(t *testing.T, results <-chan kiloNativeWorkerResult) {
	t.Helper()
	select {
	case result := <-results:
		require.NoError(t, result.err)
		require.Equal(t, 1, result.processed)
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not finish after releasing the synthetic remote barrier")
	}
}

var (
	_ service.SecretEncryptor       = kiloNativeEncryptor{}
	_ service.KiloRemote            = (*kiloNativeRemote)(nil)
	_ service.KiloEgressManager     = (*kiloNativeEgress)(nil)
	_ service.KiloEgressGuard       = (*kiloNativeEgress)(nil)
	_ service.ProxyConcurrencyCache = (*kiloNativeProxyCache)(nil)
)
