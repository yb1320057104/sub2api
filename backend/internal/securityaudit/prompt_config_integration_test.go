package securityaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const promptAuditRedisTestEnv = "PROMPT_AUDIT_TEST_REDIS_ADDR"

// This test never falls back to the application's DSN. The caller supplies an
// isolated PostgreSQL instance, and the test creates/drops only its own schema.
func TestPK54AuditCenterAtomicIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("PK54_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("PK54_TEST_POSTGRES_DSN is required for isolated audit-center integration")
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := pq.ParseURL(dsn)
		require.NoError(t, err)
		dsn = parsed
	}
	adminDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer adminDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	schema := fmt.Sprintf("pk54_audit_center_%d", time.Now().UnixNano())
	_, err = adminDB.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, cleanupErr := adminDB.ExecContext(cleanupCtx, "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, cleanupErr)
	}()
	db, err := sql.Open("postgres", dsn+" search_path="+schema)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.ExecContext(ctx, `
		CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
		CREATE TABLE users(id BIGINT PRIMARY KEY,deleted_at TIMESTAMPTZ);
		CREATE TABLE groups(id BIGINT PRIMARY KEY,status TEXT NOT NULL,deleted_at TIMESTAMPTZ);
		CREATE TABLE cyber_policy_settings(id INTEGER PRIMARY KEY,multiplier NUMERIC NOT NULL DEFAULT 1,
			auto_disable_enabled BOOLEAN NOT NULL DEFAULT FALSE,auto_disable_threshold BIGINT NOT NULL DEFAULT 10,
			reset_on_manual_enable BOOLEAN NOT NULL DEFAULT TRUE,group_ids BIGINT[] NOT NULL DEFAULT '{}',
			settings_revision BIGINT NOT NULL DEFAULT 1,user_allowlist_ids BIGINT[] NOT NULL DEFAULT '{}',
			group_rules JSONB NOT NULL DEFAULT '[]',updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
		CREATE FUNCTION audit_center_fixture_revision() RETURNS TRIGGER LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.auto_disable_threshold=999 THEN RAISE EXCEPTION 'isolated ranking write failure'; END IF;
			NEW.settings_revision:=OLD.settings_revision+1; RETURN NEW;
		END;$body$;
		CREATE TRIGGER audit_center_fixture_revision BEFORE UPDATE ON cyber_policy_settings
			FOR EACH ROW EXECUTE FUNCTION audit_center_fixture_revision();
		CREATE TABLE cyber_policy_auto_charges(id BIGSERIAL PRIMARY KEY,status TEXT,reason TEXT,amount NUMERIC,evaluated_at TIMESTAMPTZ);
		CREATE TABLE audit_settlement_imports(id BIGSERIAL PRIMARY KEY,status TEXT,reason TEXT,amount NUMERIC,created_at TIMESTAMPTZ);
		CREATE TABLE cyber_policy_audit_observations(id BIGSERIAL PRIMARY KEY,source TEXT,created_at TIMESTAMPTZ);
		INSERT INTO users(id) VALUES(101);
		INSERT INTO groups(id,status) VALUES(201,'active'),(202,'active');
		INSERT INTO cyber_policy_settings(id,group_ids) VALUES(1,ARRAY[202]);
		INSERT INTO cyber_policy_auto_charges(status,reason,amount,evaluated_at) VALUES
			('deducted','',0.25,NOW()-INTERVAL '1 minute'),('skipped','user_exempt',99,NOW()-INTERVAL '1 minute');
		INSERT INTO audit_settlement_imports(status,reason,amount,created_at) VALUES('deducted','',0.5,NOW()-INTERVAL '1 minute');
		INSERT INTO cyber_policy_audit_observations(source,created_at) VALUES('prompt_observation',NOW()-INTERVAL '1 minute');
	`)
	require.NoError(t, err)
	prompt := DefaultStorageConfig()
	prompt.GroupIDs = []int64{202}
	prompt.Endpoints = []StorageEndpoint{{ID: "main", Name: "saved", Protocol: "openai_compatible", BaseURL: "https://audit.example.test", Model: "saved-model", TokenCiphertext: "enc:private-prompt-key", TimeoutMS: 1000, InputLimit: 4000}}
	promptRaw, err := json.Marshal(prompt)
	require.NoError(t, err)
	for key, value := range map[string]string{
		SettingKeyRiskControl: "true", SettingKeyPromptAuditConfig: string(promptRaw),
		service.SettingKeyContentModerationConfig: `{"api_keys":["private-content-key"],"group_ids":[201]}`,
	} {
		_, err = db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES($1,$2)`, key, value)
		require.NoError(t, err)
	}
	manager := NewConfigManager(db, nil, nil, prefixEncryptor{}, testTotpKeyConfig())
	center := &AuditCenterService{prompt: manager, content: &service.ContentModerationService{}, ranking: service.NewCyberPolicyService(db, nil, nil)}
	before, err := center.Get(ctx)
	require.NoError(t, err)
	require.Contains(t, before.Differences, "group_ids")
	require.Len(t, before.ObservationSummary, 1)
	require.Equal(t, "0", before.ChargeSummary[1].Amount, "skipped proposed amounts are never reported as actual debits")
	disabled := false
	after, err := center.Save(ctx, AuditCenterUpdate{ExpectedRevision: before.Revision, RiskControlEnabled: &disabled,
		Shared: &AuditCenterShared{GroupIDs: &[]int64{201}, UserAllowlistIDs: &[]int64{101}}}, 42)
	require.NoError(t, err)
	require.Equal(t, []int64{201}, after.Content.GroupIDs)
	require.Equal(t, after.Content.GroupIDs, after.Prompt.GroupIDs)
	require.Equal(t, after.Content.GroupIDs, after.Ranking.GroupIDs)
	require.Equal(t, []int64{101}, after.Prompt.UserAllowlistIDs)
	require.False(t, after.RiskControlEnabled)
	require.Equal(t, int64(2), after.Prompt.ConfigVersion)
	require.Equal(t, int64(2), after.Ranking.SettingsRevision)
	_, err = center.Save(ctx, AuditCenterUpdate{ExpectedRevision: before.Revision, RiskControlEnabled: &disabled}, 42)
	require.Equal(t, "AUDIT_CENTER_CONFIG_CONFLICT", infraerrors.Reason(err))
	var savedContent, savedPrompt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.SettingKeyContentModerationConfig).Scan(&savedContent))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, SettingKeyPromptAuditConfig).Scan(&savedPrompt))
	require.Contains(t, savedContent, "private-content-key")
	require.Contains(t, savedPrompt, "enc:private-prompt-key")
	enabled := true
	failingThreshold := int64(999)
	_, err = center.Save(ctx, AuditCenterUpdate{ExpectedRevision: after.Revision, RiskControlEnabled: &enabled,
		Shared:  &AuditCenterShared{UserAllowlistIDs: &[]int64{}},
		Ranking: &service.CyberPolicySettingsInput{AutoDisableThreshold: &failingThreshold}}, 42)
	require.ErrorContains(t, err, "isolated ranking write failure")
	final, err := center.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, after.Revision, final.Revision, "later ranking SQL failure must roll back both audit configs and the shared gate")
	require.False(t, manager.snapshot.Load().active.RiskControlEnabled)
	require.Equal(t, int64(2), manager.snapshot.Load().storage.ConfigVersion)
}

type postgresPromptAuditSettingRepository struct{ db *sql.DB }

func (r postgresPromptAuditSettingRepository) Get(ctx context.Context, key string) (*service.Setting, error) {
	var value string
	var updated time.Time
	err := r.db.QueryRowContext(ctx, `SELECT value,updated_at FROM settings WHERE key=$1`, key).Scan(&value, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSettingNotFound
	}
	if err != nil {
		return nil, err
	}
	return &service.Setting{Key: key, Value: value, UpdatedAt: updated}, nil
}

func (r postgresPromptAuditSettingRepository) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r postgresPromptAuditSettingRepository) Set(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW())
		ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`, key, value)
	return err
}

func (r postgresPromptAuditSettingRepository) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = ""
	}
	rows, err := r.db.QueryContext(ctx, `SELECT key,value FROM settings WHERE key=ANY($1)`, pq.Array(keys))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, rows.Err()
}

func (r postgresPromptAuditSettingRepository) SetMultiple(ctx context.Context, values map[string]string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW())
			ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`, key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r postgresPromptAuditSettingRepository) GetAll(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key,value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, rows.Err()
}

func (r postgresPromptAuditSettingRepository) Delete(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM settings WHERE key=$1`, key)
	return err
}

func promptAuditTestEncryptor(t *testing.T) service.SecretEncryptor {
	t.Helper()
	encryptor, err := repository.NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)}})
	require.NoError(t, err)
	return encryptor
}

func promptAuditUpdateRequest(version int64, workerCount int, token string) UpdateConfigRequest {
	return UpdateConfigRequest{
		ExpectedConfigVersion: version, Enabled: true, BlockingEnabled: false, StorePassEvents: false,
		Strategy: "priority", WorkerCount: workerCount, QueueCapacity: 64, Scanners: []string{"pii", "jailbreak"},
		AllGroups: true, Endpoints: []UpdateEndpoint{{
			ID: "guard-one", Name: "Guard One", Protocol: "openai_compatible",
			BaseURL: "http://127.0.0.1:18080", Model: "", Token: token,
			TimeoutMS: 1000, InputLimit: 1024, Enabled: true,
		}},
	}
}

func waitForConfigVersion(t *testing.T, manager *ConfigManager, version int64, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		active, ok := manager.Active()
		return ok && active.ConfigVersion == version
	}, timeout, 20*time.Millisecond)
}

func TestPromptAuditConfigCASSecretRoundTripInvalidationAndTTL(t *testing.T) {
	redisAddress := strings.TrimSpace(os.Getenv(promptAuditRedisTestEnv))
	if redisAddress == "" {
		t.Skip(promptAuditRedisTestEnv + " is not set")
	}
	db := openPromptAuditIntegrationDB(t)
	settingRepo := postgresPromptAuditSettingRepository{db: db}
	require.NoError(t, settingRepo.Set(context.Background(), SettingKeyRiskControl, "true"))
	encryptor := promptAuditTestEncryptor(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddress})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	require.NoError(t, redisClient.Ping(context.Background()).Err())

	managerOne := NewConfigManager(db, settingRepo, redisClient, encryptor, testTotpKeyConfig())
	managerTwo := NewConfigManager(db, settingRepo, redisClient, encryptor, testTotpKeyConfig())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, managerOne.Start(ctx))
	require.NoError(t, managerTwo.Start(ctx))
	t.Cleanup(func() {
		require.NoError(t, managerOne.Shutdown(context.Background()))
		require.NoError(t, managerTwo.Shutdown(context.Background()))
	})
	require.Eventually(t, func() bool {
		return redisClient.PubSubNumSub(context.Background(), ConfigInvalidationChannel).Val()[ConfigInvalidationChannel] >= 2
	}, 2*time.Second, 20*time.Millisecond)

	const canary = "GUARD_TOKEN_CANARY_SECRET_4_CONFIG"
	public, err := managerOne.Save(context.Background(), promptAuditUpdateRequest(1, 1, canary), 101)
	require.NoError(t, err)
	require.Equal(t, int64(2), public.ConfigVersion)
	require.True(t, public.Endpoints[0].HasToken)
	publicJSON, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(publicJSON), canary)
	waitForConfigVersion(t, managerTwo, 2, 2*time.Second)

	raw, err := settingRepo.GetValue(context.Background(), SettingKeyPromptAuditConfig)
	require.NoError(t, err)
	require.NotContains(t, raw, canary)
	stored, err := ParseStorageConfig(raw)
	require.NoError(t, err)
	require.NotEmpty(t, stored.Endpoints[0].TokenCiphertext)
	plain, err := encryptor.Decrypt(stored.Endpoints[0].TokenCiphertext)
	require.NoError(t, err)
	require.Equal(t, canary, plain)
	require.NotContains(t, stored.ChangeSummary, canary)
	require.NotContains(t, stored.ChangeSummary, stored.Endpoints[0].BaseURL)

	type saveResult struct {
		config PublicConfig
		err    error
	}
	start := make(chan struct{})
	results := make(chan saveResult, 2)
	var wg sync.WaitGroup
	for index, manager := range []*ConfigManager{managerOne, managerTwo} {
		wg.Add(1)
		go func(index int, manager *ConfigManager) {
			defer wg.Done()
			<-start
			cfg, saveErr := manager.Save(context.Background(), promptAuditUpdateRequest(2, index+2, ""), int64(201+index))
			results <- saveResult{config: cfg, err: saveErr}
		}(index, manager)
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for result := range results {
		if result.err == nil {
			succeeded++
			require.Equal(t, int64(3), result.config.ConfigVersion)
			continue
		}
		conflicted++
		require.Equal(t, ErrorCodeConfigConflict, infraerrors.Reason(result.err))
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, conflicted)
	waitForConfigVersion(t, managerOne, 3, 2*time.Second)
	waitForConfigVersion(t, managerTwo, 3, 2*time.Second)

	// A manager without Redis subscriptions must still converge through the
	// bounded five-second refresh loop.
	ttlManager := NewConfigManager(db, settingRepo, nil, encryptor, testTotpKeyConfig())
	require.NoError(t, ttlManager.Start(ctx))
	t.Cleanup(func() { require.NoError(t, ttlManager.Shutdown(context.Background())) })
	waitForConfigVersion(t, ttlManager, 3, time.Second)
	updated, err := managerOne.Save(context.Background(), promptAuditUpdateRequest(3, 5, ""), 301)
	require.NoError(t, err)
	require.Equal(t, int64(4), updated.ConfigVersion)
	waitForConfigVersion(t, ttlManager, 4, 7*time.Second)

	// Redis publication failure is observable degradation, not a rollback of a
	// successfully committed PostgreSQL config.
	deadRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: 0, DialTimeout: 30 * time.Millisecond, ReadTimeout: 30 * time.Millisecond, WriteTimeout: 30 * time.Millisecond})
	t.Cleanup(func() { _ = deadRedis.Close() })
	degraded := NewConfigManager(db, settingRepo, deadRedis, encryptor, testTotpKeyConfig())
	require.NoError(t, degraded.Reload(context.Background()))
	degradedSaved, err := degraded.Save(context.Background(), promptAuditUpdateRequest(4, 6, ""), 401)
	require.NoError(t, err)
	require.Equal(t, int64(5), degradedSaved.ConfigVersion)
	active, ok := degraded.Active()
	require.True(t, ok)
	require.Equal(t, int64(5), active.ConfigVersion)
}
