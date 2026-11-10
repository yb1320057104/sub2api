//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPKCyberMigrationDefaultsAndExistingBalancesUnchanged(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	schema := fmt.Sprintf("pk_cyber_%d", time.Now().UnixNano())
	_, err := tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `CREATE TABLE users (id BIGINT PRIMARY KEY,balance NUMERIC(20,8),total_recharged NUMERIC(20,8)); CREATE TABLE usage_logs(id BIGINT,user_id BIGINT,created_at TIMESTAMPTZ,request_type SMALLINT); INSERT INTO users VALUES(1,12.12345678,99.12345678); INSERT INTO usage_logs VALUES(1,1,NOW(),4);`)
	require.NoError(t, err)
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "244_security_policy_ranking.sql"))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var defaults, unchanged bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT multiplier=6.7 AND NOT auto_disable_enabled AND auto_disable_threshold=10 AND reset_on_manual_enable FROM cyber_policy_settings WHERE id=1`).Scan(&defaults))
	require.True(t, defaults)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT balance=12.12345678 AND total_recharged=99.12345678 FROM users WHERE id=1`).Scan(&unchanged))
	require.True(t, unchanged)
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cyber_policy_charges`).Scan(&count))
	require.Zero(t, count, "migration must not retroactively debit historical safety records")
	_, err = tx.ExecContext(ctx, `INSERT INTO cyber_policy_deductions(id,actor_id,user_id,email,username,start_date,end_date,timezone,multiplier,trigger_count,balance) VALUES('00000000-0000-0000-0000-000000000001',1,1,'fixture','fixture','2026-10-03','2026-10-03','UTC',0.29,100,12.12345678)`)
	require.NoError(t, err)
	var amount string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT amount::text FROM cyber_policy_deductions`).Scan(&amount))
	require.Equal(t, "29.00000000", amount, "generated deduction uses decimal multiplication, not binary float")
	_, err = tx.ExecContext(ctx, `INSERT INTO cyber_policy_charges SELECT id,created_at,'00000000-0000-0000-0000-000000000001',user_id FROM usage_logs; DELETE FROM usage_logs; DELETE FROM users;`)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cyber_policy_charges`).Scan(&count))
	require.Equal(t, 1, count, "usage and user retention must not erase durable charge identity")
}

func TestPKBinanceMultiplierMigrationPreservesLegacyDataAndDefaults(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	schema := fmt.Sprintf("pk_binance_multiplier_%d", time.Now().UnixNano())
	_, err := tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	// Reuse the real balance/history column types while isolating pre-migration
	// Binance tables from the fully migrated integration database. Rollback of
	// testTx removes this schema and every fixture row.
	_, err = tx.ExecContext(ctx, `
CREATE TABLE users (LIKE public.users INCLUDING ALL);
CREATE TABLE redeem_codes (LIKE public.redeem_codes INCLUDING ALL);
INSERT INTO users(id,email,password_hash,role,status,balance,total_recharged,concurrency)
VALUES(101,'legacy-binance@example.com','fixture','user','active',12.24690678,10.12345,1);
INSERT INTO redeem_codes(id,code,type,value,status,used_by,used_at,notes)
VALUES(101,'legacy-binance-credit','balance',10.12345,'used',101,NOW(),'existing recharge history');`)
	require.NoError(t, err)
	legacySQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "242_binance_recharge.sql"))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(legacySQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
INSERT INTO binance_recharge_accounts(id,name,recipient_uid,api_key_encrypted,api_secret_encrypted,enabled,order_ttl_minutes)
VALUES(101,'legacy account','12345','encrypted-key-fixture','encrypted-secret-fixture',TRUE,15);
INSERT INTO binance_recharge_orders(id,order_no,user_id,account_id,account_name,recipient_uid,requested_amount,amount_units,credited_units,status,transaction_id,expires_at,paid_at,completed_at)
VALUES
(101,'legacy-completed',101,101,'legacy account','12345',10,1012345,1012345,'COMPLETED','legacy-transfer',NOW()+INTERVAL '15 minutes',NOW(),NOW()),
(102,'legacy-pending',101,101,'legacy account','12345',20,2054321,0,'PENDING','',NOW()+INTERVAL '15 minutes',NULL,NULL),
(103,'legacy-expired',101,101,'legacy account','12345',30,3067890,0,'EXPIRED','',NOW()-INTERVAL '15 minutes',NULL,NULL),
(104,'legacy-review',101,101,'legacy account','12345',40,4011111,0,'REVIEW','',NOW()+INTERVAL '15 minutes',NULL,NULL);
INSERT INTO binance_recharge_receipts(account_id,recipient_uid,transaction_id,amount_units,paid_at,order_id,status)
VALUES(101,'12345','legacy-transfer',1012345,NOW(),101,'COMPLETED');`)
	require.NoError(t, err)
	readJSON := func(query string) string {
		t.Helper()
		var value string
		require.NoError(t, tx.QueryRowContext(ctx, query).Scan(&value))
		return value
	}
	accountsBefore := readJSON(`SELECT jsonb_agg(to_jsonb(a) ORDER BY id)::text FROM binance_recharge_accounts a`)
	ordersBefore := readJSON(`SELECT jsonb_agg(to_jsonb(o) ORDER BY id)::text FROM binance_recharge_orders o`)
	usersBefore := readJSON(`SELECT jsonb_agg(to_jsonb(u) ORDER BY id)::text FROM users u`)
	ledgerBefore := readJSON(`SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM redeem_codes r`)
	receiptsBefore := readJSON(`SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM binance_recharge_receipts r`)
	migrationSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "243_binance_recharge_multiplier.sql"))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	require.JSONEq(t, accountsBefore, readJSON(`SELECT jsonb_agg(to_jsonb(a)-'credit_multiplier' ORDER BY id)::text FROM binance_recharge_accounts a`))
	require.JSONEq(t, ordersBefore, readJSON(`SELECT jsonb_agg(to_jsonb(o)-'credit_multiplier'-'system_amount'-'credited_amount' ORDER BY id)::text FROM binance_recharge_orders o`))
	require.JSONEq(t, usersBefore, readJSON(`SELECT jsonb_agg(to_jsonb(u) ORDER BY id)::text FROM users u`), "migration must never credit an existing user twice")
	require.JSONEq(t, ledgerBefore, readJSON(`SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM redeem_codes r`))
	require.JSONEq(t, receiptsBefore, readJSON(`SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM binance_recharge_receipts r`))
	var legacySnapshots, legacyCredits bool
	err = tx.QueryRowContext(ctx, `SELECT bool_and(credit_multiplier=1 AND system_amount=amount_units::numeric/100000),bool_and(credited_amount=CASE WHEN status='COMPLETED' THEN credited_units::numeric/100000 ELSE 0 END) FROM binance_recharge_orders`).Scan(&legacySnapshots, &legacyCredits)
	require.NoError(t, err)
	require.True(t, legacySnapshots)
	require.True(t, legacyCredits)
	var multiplier string
	err = tx.QueryRowContext(ctx, `INSERT INTO binance_recharge_accounts(name,recipient_uid) VALUES('new default account','54321') RETURNING credit_multiplier::text`).Scan(&multiplier)
	require.NoError(t, err)
	require.Equal(t, "1.000000", multiplier)
	var orderDefault sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='binance_recharge_orders' AND column_name='credit_multiplier'`, schema).Scan(&orderDefault)
	require.NoError(t, err)
	require.False(t, orderDefault.Valid, "new orders must explicitly snapshot the account multiplier")
	_, err = tx.ExecContext(ctx, `UPDATE binance_recharge_accounts SET credit_multiplier=6.7 WHERE id=101`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SAVEPOINT legacy_order_insert`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO binance_recharge_orders(order_no,user_id,account_id,account_name,recipient_uid,requested_amount,amount_units,expires_at) VALUES('old-binary-new-order',101,101,'legacy account','12345',50,5012345,NOW()+INTERVAL '15 minutes')`)
	requirePKBinanceSQLState(t, err, "23502")
	_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT legacy_order_insert`)
	require.NoError(t, err)
	var systemAmount string
	err = tx.QueryRowContext(ctx, `INSERT INTO binance_recharge_orders(order_no,user_id,account_id,account_name,recipient_uid,requested_amount,amount_units,credit_multiplier,expires_at) VALUES('new-binary-order',101,101,'legacy account','12345',50,5012345,6.7,NOW()+INTERVAL '15 minutes') RETURNING system_amount::text`).Scan(&systemAmount)
	require.NoError(t, err)
	require.Equal(t, "335.82711500", systemAmount)
	err = tx.QueryRowContext(ctx, `SELECT credit_multiplier::text FROM binance_recharge_orders WHERE id=102`).Scan(&multiplier)
	require.NoError(t, err)
	require.Equal(t, "1.000000", multiplier, "changing the account must preserve a legacy unpaid quote")
}

func requirePKBinanceSQLState(t *testing.T, err error, expected string) {
	t.Helper()
	require.Error(t, err)
	var state interface{ SQLState() string }
	require.ErrorAs(t, err, &state)
	require.Equal(t, expected, state.SQLState())
}

func TestPKBinanceAccountProxyMigrationPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	schema := fmt.Sprintf("pk_binance_proxy_%d", time.Now().UnixNano())
	_, err := tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `CREATE TABLE binance_recharge_accounts (id BIGINT PRIMARY KEY, name TEXT, recipient_uid TEXT, api_key_encrypted TEXT, api_secret_encrypted TEXT, enabled BOOLEAN); INSERT INTO binance_recharge_accounts VALUES (1,'legacy','12345','existing-key','existing-secret',TRUE);`)
	require.NoError(t, err)
	var before string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_jsonb(a)::text FROM binance_recharge_accounts a`).Scan(&before))
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "257_binance_recharge_account_proxy.sql"))
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err, "additive migration must be idempotent")
	}
	var after, proxy string
	var preflight bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT (to_jsonb(a)-'proxy_url_encrypted'-'proxy_preflight')::text,proxy_url_encrypted,proxy_preflight FROM binance_recharge_accounts a`).Scan(&after, &proxy, &preflight))
	require.JSONEq(t, before, after)
	require.Empty(t, proxy, "legacy accounts must retain the existing global route")
	require.False(t, preflight)
	// Old application writes name/enabled without deleting the saved encrypted
	// proxy. A rollback keeps the settings available for a later upgrade.
	_, err = tx.ExecContext(ctx, `UPDATE binance_recharge_accounts SET proxy_url_encrypted='opaque-cipher',proxy_preflight=TRUE WHERE id=1; UPDATE binance_recharge_accounts SET name='legacy-edit',enabled=FALSE WHERE id=1`)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT proxy_url_encrypted,proxy_preflight FROM binance_recharge_accounts WHERE id=1`).Scan(&proxy, &preflight))
	require.Equal(t, "opaque-cipher", proxy)
	require.True(t, preflight)
}

func TestPKBinanceBackupMigrationPreservesExistingRowsAndRollbackSettings(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	schema := fmt.Sprintf("pk_binance_backup_%d", time.Now().UnixNano())
	_, err := tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `CREATE TABLE binance_recharge_accounts (id BIGINT PRIMARY KEY,name TEXT,proxy_url_encrypted TEXT,proxy_preflight BOOLEAN); INSERT INTO binance_recharge_accounts VALUES (1,'legacy','opaque-primary',TRUE)`)
	require.NoError(t, err)
	var before string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_jsonb(a)::text FROM binance_recharge_accounts a`).Scan(&before))
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "262_binance_backup_proxy.sql"))
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
	}
	var after, backup, status string
	var preflight bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT (to_jsonb(a)-'backup_proxy_url_encrypted'-'backup_proxy_preflight'-'query_status')::text,backup_proxy_url_encrypted,backup_proxy_preflight,query_status::text FROM binance_recharge_accounts a`).Scan(&after, &backup, &preflight, &status))
	require.JSONEq(t, before, after)
	require.Empty(t, backup)
	require.False(t, preflight)
	require.JSONEq(t, `{}`, status)
	_, err = tx.ExecContext(ctx, `UPDATE binance_recharge_accounts SET backup_proxy_url_encrypted='opaque-backup',backup_proxy_preflight=TRUE,query_status='{"route":"backup","attempt":2,"state":"succeeded"}'; UPDATE binance_recharge_accounts SET name='old-version-edit',proxy_url_encrypted='new-primary'`)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT backup_proxy_url_encrypted,backup_proxy_preflight,query_status::text FROM binance_recharge_accounts`).Scan(&backup, &preflight, &status))
	require.Equal(t, "opaque-backup", backup)
	require.True(t, preflight)
	require.JSONEq(t, `{"route":"backup","attempt":2,"state":"succeeded"}`, status)
}

func TestMigrationsRunner_ConcurrentInstancesSerializeOnSessionLock(t *testing.T) {
	const instances = 2
	errorsByInstance := make([]error, instances)
	var wg sync.WaitGroup
	for i := 0; i < instances; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			errorsByInstance[index] = ApplyMigrations(ctx, integrationDB)
		}(i)
	}
	wg.Wait()
	for i, err := range errorsByInstance {
		require.NoErrorf(t, err, "migration instance %d", i)
	}
}

func TestMigrationsRunner_IsIdempotent_AndSchemaIsUpToDate(t *testing.T) {
	tx := testTx(t)

	// Re-apply migrations to verify idempotency (no errors, no duplicate rows).
	require.NoError(t, ApplyMigrations(context.Background(), integrationDB))

	// schema_migrations should have at least the current migration set.
	var applied int
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&applied))
	require.GreaterOrEqual(t, applied, 7, "expected schema_migrations to contain applied migrations")

	// users: columns required by repository queries
	requireColumn(t, tx, "users", "username", "character varying", 100, false)
	requireColumn(t, tx, "users", "notes", "text", 0, false)

	// accounts: schedulable and rate-limit fields
	requireColumn(t, tx, "accounts", "notes", "text", 0, true)
	requireColumn(t, tx, "accounts", "schedulable", "boolean", 0, false)
	requireColumn(t, tx, "accounts", "rate_limited_at", "timestamp with time zone", 0, true)
	requireColumn(t, tx, "accounts", "rate_limit_reset_at", "timestamp with time zone", 0, true)
	requireColumn(t, tx, "accounts", "overload_until", "timestamp with time zone", 0, true)
	requireColumn(t, tx, "accounts", "session_window_status", "character varying", 20, true)
	requireIndex(t, tx, "accounts", "idx_accounts_autopause_expiry_due")

	// groups: OpenAI Live 与 Fast 强制策略都默认关闭，管理员显式开启后才生效。
	requireColumn(t, tx, "groups", "allow_live", "boolean", 0, false)
	requireColumn(t, tx, "groups", "force_openai_fast", "boolean", 0, false)
	requireColumn(t, tx, "groups", "stream_only", "boolean", 0, false)
	requireColumn(t, tx, "groups", "free_openai_fast", "boolean", 0, false)

	// pelican_showcase_items: 鹈鹕测智用户展示快照，按分组 + 生成时间倒序读取和清理。
	requireColumn(t, tx, "pelican_showcase_items", "response_text", "text", 0, false)
	requireColumn(t, tx, "pelican_showcase_items", "generated_at", "timestamp with time zone", 0, false)
	requireIndex(t, tx, "pelican_showcase_items", "idx_pelican_showcase_items_group_generated")

	// api_keys: key length should be 128
	requireColumn(t, tx, "api_keys", "key", "character varying", 128, false)

	// redeem_codes: subscription fields
	requireColumn(t, tx, "redeem_codes", "group_id", "bigint", 0, true)
	requireColumn(t, tx, "redeem_codes", "validity_days", "integer", 0, false)

	// usage_logs: billing_type used by filters/stats
	requireColumn(t, tx, "usage_logs", "billing_type", "smallint", 0, false)
	requireColumn(t, tx, "usage_logs", "request_type", "smallint", 0, false)
	requireColumn(t, tx, "usage_logs", "openai_ws_mode", "boolean", 0, false)
	requireColumn(t, tx, "usage_logs", "native_compaction_v2", "boolean", 0, false)
	requireColumnDefaultContains(t, tx, "usage_logs", "native_compaction_v2", "false")
	requireColumn(t, tx, "usage_logs", "image_input_size", "character varying", 32, true)
	requireColumn(t, tx, "usage_logs", "image_output_size", "character varying", 32, true)
	requireColumn(t, tx, "usage_logs", "image_size_source", "character varying", 16, true)
	requireColumn(t, tx, "usage_logs", "image_size_breakdown", "jsonb", 0, true)
	requireColumn(t, tx, "usage_logs", "video_count", "integer", 0, false)
	requireColumn(t, tx, "usage_logs", "video_resolution", "character varying", 10, true)
	requireColumn(t, tx, "usage_logs", "video_duration_seconds", "integer", 0, true)
	requireColumn(t, tx, "usage_logs", "upstream_response_model", "character varying", 200, true)
	requireColumn(t, tx, "usage_logs", "upstream_model_mismatch", "boolean", 0, true)
	requireIndex(t, tx, "usage_logs", usageLogsUpstreamModelMismatchIndex)

	var mismatchIndexDef string
	require.NoError(t, tx.QueryRowContext(context.Background(), `
SELECT pg_get_indexdef(i.indexrelid)
FROM pg_class idx
JOIN pg_index i ON i.indexrelid = idx.oid
JOIN pg_class tbl ON tbl.oid = i.indrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
WHERE ns.nspname = 'public'
  AND tbl.relname = 'usage_logs'
  AND idx.relname = $1
`, usageLogsUpstreamModelMismatchIndex).Scan(&mismatchIndexDef))
	require.Contains(t, mismatchIndexDef, "created_at DESC")
	require.Contains(t, mismatchIndexDef, "id DESC")
	require.Contains(t, mismatchIndexDef, "WHERE (upstream_model_mismatch IS TRUE)")
	requireConstraintDefinitionContains(
		t,
		tx,
		"usage_logs",
		"usage_logs_image_size_source_check",
		"image_size_source",
		"'output'",
		"'input'",
		"'default'",
		"'legacy'",
	)
	requireConstraintDefinitionContains(
		t,
		tx,
		"usage_logs",
		"usage_logs_image_billing_size_check",
		"image_count",
		"billing_mode",
		"'video'",
		"video_count",
		"image_size IS NOT NULL",
		"'1K'",
		"'2K'",
		"'4K'",
		"'mixed'",
	)

	// usage_billing_dedup: billing idempotency narrow table
	var usageBillingDedupRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.usage_billing_dedup')").Scan(&usageBillingDedupRegclass))
	require.True(t, usageBillingDedupRegclass.Valid, "expected usage_billing_dedup table to exist")
	requireColumn(t, tx, "usage_billing_dedup", "request_fingerprint", "character varying", 64, false)
	requireIndex(t, tx, "usage_billing_dedup", "idx_usage_billing_dedup_request_api_key")
	requireIndex(t, tx, "usage_billing_dedup", "idx_usage_billing_dedup_created_at_brin")

	var usageBillingDedupArchiveRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.usage_billing_dedup_archive')").Scan(&usageBillingDedupArchiveRegclass))
	require.True(t, usageBillingDedupArchiveRegclass.Valid, "expected usage_billing_dedup_archive table to exist")
	requireColumn(t, tx, "usage_billing_dedup_archive", "request_fingerprint", "character varying", 64, false)
	requireIndex(t, tx, "usage_billing_dedup_archive", "usage_billing_dedup_archive_pkey")

	// settings table should exist
	var settingsRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.settings')").Scan(&settingsRegclass))
	require.True(t, settingsRegclass.Valid, "expected settings table to exist")

	// security_secrets table should exist
	var securitySecretsRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.security_secrets')").Scan(&securitySecretsRegclass))
	require.True(t, securitySecretsRegclass.Valid, "expected security_secrets table to exist")

	// scheduler_outbox pending dedup support
	requireColumn(t, tx, "scheduler_outbox", "dedup_key", "text", 0, true)
	requireIndex(t, tx, "scheduler_outbox", "idx_scheduler_outbox_pending_dedup_key")

	// ops_system_logs: API key id index for operational log triage
	requireColumn(t, tx, "ops_system_logs", "api_key_id", "bigint", 0, true)
	requireIndex(t, tx, "ops_system_logs", "idx_ops_system_logs_api_key_id_created_at")

	// Bounded ingress rejection security aggregates.
	requireColumn(t, tx, "ops_ingress_reject_aggregates", "bucket_start", "timestamp with time zone", 0, false)
	requireColumn(t, tx, "ops_ingress_reject_aggregates", "client_ip", "inet", 0, false)
	requireColumn(t, tx, "ops_ingress_reject_aggregates", "request_count", "bigint", 0, false)
	requireIndex(t, tx, "ops_ingress_reject_aggregates", "idx_ops_ingress_reject_aggregates_bucket")
	requireIndex(t, tx, "ops_ingress_reject_aggregates", "idx_ops_ingress_reject_aggregates_ip_bucket")

	// user_allowed_groups table should exist
	var uagRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.user_allowed_groups')").Scan(&uagRegclass))
	require.True(t, uagRegclass.Valid, "expected user_allowed_groups table to exist")

	// user_subscriptions: deleted_at for soft delete support (migration 012)
	requireColumn(t, tx, "user_subscriptions", "deleted_at", "timestamp with time zone", 0, true)

	// orphan_allowed_groups_audit table should exist (migration 013)
	var orphanAuditRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.orphan_allowed_groups_audit')").Scan(&orphanAuditRegclass))
	require.True(t, orphanAuditRegclass.Valid, "expected orphan_allowed_groups_audit table to exist")

	// account_groups: created_at should be timestamptz
	requireColumn(t, tx, "account_groups", "created_at", "timestamp with time zone", 0, false)

	// user_allowed_groups: created_at should be timestamptz
	requireColumn(t, tx, "user_allowed_groups", "created_at", "timestamp with time zone", 0, false)
}

func TestMigrationsRunner_AuthIdentityAndPaymentSchemaStayAligned(t *testing.T) {
	tx := testTx(t)

	requireColumn(t, tx, "auth_identity_migration_reports", "report_type", "character varying", 80, false)
	requireColumn(t, tx, "users", "signup_source", "character varying", 20, false)
	requireColumnDefaultContains(t, tx, "users", "signup_source", "email")
	requireConstraintDefinitionContains(
		t,
		tx,
		"users",
		"users_signup_source_check",
		"signup_source",
		"'email'",
		"'linuxdo'",
		"'wechat'",
		"'oidc'",
	)

	requireForeignKeyOnDelete(t, tx, "auth_identities", "user_id", "users", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "auth_identity_channels", "identity_id", "auth_identities", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "pending_auth_sessions", "target_user_id", "users", "SET NULL")
	requireForeignKeyOnDelete(t, tx, "identity_adoption_decisions", "pending_auth_session_id", "pending_auth_sessions", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "identity_adoption_decisions", "identity_id", "auth_identities", "SET NULL")

	requireIndex(t, tx, "payment_orders", "paymentorder_out_trade_no")
	requirePartialUniqueIndexDefinition(t, tx, "payment_orders", "paymentorder_out_trade_no", "out_trade_no", "WHERE")
	requireIndexAbsent(t, tx, "payment_orders", "paymentorder_out_trade_no_unique")
}

func requireIndex(t *testing.T, tx *sql.Tx, table, index string) {
	t.Helper()

	var exists bool
	err := tx.QueryRowContext(context.Background(), `
SELECT EXISTS (
	SELECT 1
	FROM pg_indexes
	WHERE schemaname = 'public'
	  AND tablename = $1
	  AND indexname = $2
)
`, table, index).Scan(&exists)
	require.NoError(t, err, "query pg_indexes for %s.%s", table, index)
	require.True(t, exists, "expected index %s on %s", index, table)
}

func requireIndexAbsent(t *testing.T, tx *sql.Tx, table, index string) {
	t.Helper()

	var exists bool
	err := tx.QueryRowContext(context.Background(), `
SELECT EXISTS (
	SELECT 1
	FROM pg_indexes
	WHERE schemaname = 'public'
	  AND tablename = $1
	  AND indexname = $2
)
`, table, index).Scan(&exists)
	require.NoError(t, err, "query pg_indexes for %s.%s", table, index)
	require.False(t, exists, "expected index %s on %s to be absent", index, table)
}

func requirePartialUniqueIndexDefinition(t *testing.T, tx *sql.Tx, table, index string, fragments ...string) {
	t.Helper()

	var (
		unique bool
		def    string
	)

	err := tx.QueryRowContext(context.Background(), `
SELECT
	i.indisunique,
	pg_get_indexdef(i.indexrelid)
FROM pg_class idx
JOIN pg_index i ON i.indexrelid = idx.oid
JOIN pg_class tbl ON tbl.oid = i.indrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
WHERE ns.nspname = 'public'
  AND tbl.relname = $1
  AND idx.relname = $2
`, table, index).Scan(&unique, &def)
	require.NoError(t, err, "query index definition for %s.%s", table, index)
	require.True(t, unique, "expected index %s on %s to be unique", index, table)

	for _, fragment := range fragments {
		require.Contains(t, def, fragment, "expected index definition for %s.%s to contain %q", table, index, fragment)
	}
}

func requireForeignKeyOnDelete(t *testing.T, tx *sql.Tx, table, column, refTable, expected string) {
	t.Helper()

	var actual string
	err := tx.QueryRowContext(context.Background(), `
SELECT CASE c.confdeltype
	WHEN 'a' THEN 'NO ACTION'
	WHEN 'r' THEN 'RESTRICT'
	WHEN 'c' THEN 'CASCADE'
	WHEN 'n' THEN 'SET NULL'
	WHEN 'd' THEN 'SET DEFAULT'
END
FROM pg_constraint c
JOIN pg_class tbl ON tbl.oid = c.conrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
JOIN pg_class ref_tbl ON ref_tbl.oid = c.confrelid
JOIN pg_attribute attr ON attr.attrelid = tbl.oid AND attr.attnum = ANY(c.conkey)
WHERE ns.nspname = 'public'
  AND c.contype = 'f'
  AND tbl.relname = $1
  AND attr.attname = $2
  AND ref_tbl.relname = $3
LIMIT 1
`, table, column, refTable).Scan(&actual)
	require.NoError(t, err, "query foreign key action for %s.%s -> %s", table, column, refTable)
	require.Equal(t, expected, actual, "unexpected ON DELETE action for %s.%s -> %s", table, column, refTable)
}

func requireConstraintDefinitionContains(t *testing.T, tx *sql.Tx, table, constraint string, fragments ...string) {
	t.Helper()

	var def string
	err := tx.QueryRowContext(context.Background(), `
SELECT pg_get_constraintdef(c.oid)
FROM pg_constraint c
JOIN pg_class tbl ON tbl.oid = c.conrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
WHERE ns.nspname = 'public'
  AND tbl.relname = $1
  AND c.conname = $2
`, table, constraint).Scan(&def)
	require.NoError(t, err, "query constraint definition for %s.%s", table, constraint)

	for _, fragment := range fragments {
		require.Contains(t, def, fragment, "expected constraint definition for %s.%s to contain %q", table, constraint, fragment)
	}
}

func requireColumnDefaultContains(t *testing.T, tx *sql.Tx, table, column string, fragments ...string) {
	t.Helper()

	var columnDefault sql.NullString
	err := tx.QueryRowContext(context.Background(), `
SELECT column_default
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name = $1
  AND column_name = $2
`, table, column).Scan(&columnDefault)
	require.NoError(t, err, "query column_default for %s.%s", table, column)
	require.True(t, columnDefault.Valid, "expected column_default for %s.%s", table, column)

	for _, fragment := range fragments {
		require.Contains(t, columnDefault.String, fragment, "expected default for %s.%s to contain %q", table, column, fragment)
	}
}

func requireColumn(t *testing.T, tx *sql.Tx, table, column, dataType string, maxLen int, nullable bool) {
	t.Helper()

	var row struct {
		DataType string
		MaxLen   sql.NullInt64
		Nullable string
	}

	err := tx.QueryRowContext(context.Background(), `
SELECT
  data_type,
  character_maximum_length,
  is_nullable
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name = $1
  AND column_name = $2
`, table, column).Scan(&row.DataType, &row.MaxLen, &row.Nullable)
	require.NoError(t, err, "query information_schema.columns for %s.%s", table, column)
	require.Equal(t, dataType, row.DataType, "data_type mismatch for %s.%s", table, column)

	if maxLen > 0 {
		require.True(t, row.MaxLen.Valid, "expected maxLen for %s.%s", table, column)
		require.Equal(t, int64(maxLen), row.MaxLen.Int64, "maxLen mismatch for %s.%s", table, column)
	}

	if nullable {
		require.Equal(t, "YES", row.Nullable, "nullable mismatch for %s.%s", table, column)
	} else {
		require.Equal(t, "NO", row.Nullable, "nullable mismatch for %s.%s", table, column)
	}
}
