package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func accountCandyGroups(ctx context.Context, tx *sql.Tx, accountID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT group_id FROM account_groups WHERE account_id=$1 ORDER BY group_id FOR UPDATE`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *AccountCandyTestService) claim(ctx context.Context, accountID int64, source string) (*AccountCandyTestResult, *AccountCandyTestConfig, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, nil, err
	}
	if account.Platform != PlatformOpenAI || account.IsSyntheticUITest() {
		return nil, nil, infraerrors.BadRequest("ACCOUNT_CANDY_UNSUPPORTED", "糖果测试仅支持真实 OpenAI 账号")
	}
	credentialAccount := account
	if account.IsCredentialShadow() {
		credentialAccount, err = resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, nil, err
		}
	}
	snapshotIdentity, err := accountCandySnapshotIdentity(account, credentialAccount)
	if err != nil {
		return nil, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountCandyLockID); err != nil {
		return nil, nil, err
	}
	cfg, err := accountCandyConfigTx(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	var platform, status string
	var schedulable, notExpired bool
	err = tx.QueryRowContext(ctx, `SELECT platform,status,schedulable,(expires_at IS NULL OR expires_at>NOW() OR auto_pause_on_expired=false) FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID).Scan(&platform, &status, &schedulable, &notExpired)
	if err != nil {
		return nil, nil, err
	}
	if platform != PlatformOpenAI {
		return nil, nil, infraerrors.BadRequest("ACCOUNT_CANDY_UNSUPPORTED", "糖果测试仅支持 OpenAI 账号")
	}
	if source == "scheduled" && (!cfg.Enabled || status != StatusActive || !schedulable || !notExpired) {
		return nil, nil, infraerrors.Conflict("ACCOUNT_CANDY_NOT_DUE", "账号当前不参与定时测试")
	}
	groups, err := accountCandyGroups(ctx, tx, accountID)
	if err != nil {
		return nil, nil, err
	}
	if source == "scheduled" && cfg.TargetGroupID != nil {
		for _, id := range groups {
			if id == *cfg.TargetGroupID {
				return nil, nil, infraerrors.Conflict("ACCOUNT_CANDY_TARGET_GROUP", "目标分组中的账号不参与定时测试")
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pk_account_candy_state(account_id) VALUES($1) ON CONFLICT DO NOTHING`, accountID); err != nil {
		return nil, nil, err
	}
	var oldID sql.NullInt64
	var leased, due bool
	err = tx.QueryRowContext(ctx, `SELECT active_result_id,COALESCE(lease_until>NOW(),false),next_run_at<=NOW() FROM pk_account_candy_state WHERE account_id=$1 FOR UPDATE`, accountID).Scan(&oldID, &leased, &due)
	if err != nil {
		return nil, nil, err
	}
	if leased {
		return nil, nil, infraerrors.Conflict("ACCOUNT_CANDY_RUNNING", "该账号正在测试")
	}
	if source == "scheduled" && !due {
		return nil, nil, infraerrors.Conflict("ACCOUNT_CANDY_NOT_DUE", "账号尚未到测试时间")
	}
	if oldID.Valid {
		if _, err = tx.ExecContext(ctx, `UPDATE pk_account_candy_results SET status='failed',error_message='上次测试未正常结束，租约已到期。',finished_at=NOW() WHERE id=$1 AND status='running'`, oldID.Int64); err != nil {
			return nil, nil, err
		}
	}
	result := &AccountCandyTestResult{AccountID: accountID, Source: source, Status: "running", Model: cfg.Model, Effort: cfg.Effort, OriginalGroupIDs: groups, Prompt: intelligenceTestCandyPrompt + "\n\n请给出你的解答，并在最后单独一行输出 <final_answer>整数</final_answer>，标签中只填写你求得的最少糖果数目。"}
	result.identity, err = accountCandyIdentity(ctx, tx, accountID)
	if err != nil {
		return nil, nil, err
	}
	// Validate the already-loaded request snapshot against locked database
	// identities. Never request a second connection while holding this tx.
	if result.identity != snapshotIdentity {
		return nil, nil, infraerrors.Conflict("ACCOUNT_CANDY_ACCOUNT_CHANGED", "账号配置刚刚发生变化，请重新测试")
	}
	result.probeAccount = account
	result.credentialAccount = credentialAccount
	groupJSON, err := json.Marshal(groups)
	if err != nil {
		return nil, nil, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO pk_account_candy_results(account_id,source,status,generation,model,effort,prompt,original_group_ids,target_group_id) VALUES($1,$2,'running',$3,$4,$5,$6,$7::jsonb,$8) RETURNING id,started_at`, accountID, source, cfg.generation, cfg.Model, cfg.Effort, result.Prompt, string(groupJSON), cfg.TargetGroupID).Scan(&result.ID, &result.StartedAt)
	if err != nil {
		return nil, nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_account_candy_state SET active_result_id=$2,lease_until=NOW()+INTERVAL '6 minutes',next_run_at=NOW()+make_interval(mins=>$3) WHERE account_id=$1`, accountID, result.ID, cfg.IntervalMinutes); err != nil {
		return nil, nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM pk_account_candy_results WHERE account_id=$1 AND id NOT IN(SELECT id FROM pk_account_candy_results WHERE account_id=$1 ORDER BY id DESC LIMIT 30)`, accountID); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	return result, cfg, nil
}

func (s *AccountCandyTestService) finish(ctx context.Context, result *AccountCandyTestResult, cfg *AccountCandyTestConfig, text string, callErr error, elapsed int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountCandyLockID); err != nil {
		return err
	}
	current, err := accountCandyConfigTx(ctx, tx)
	if err != nil {
		return err
	}
	var owned bool
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(active_result_id=$2 AND lease_until>NOW(),false) FROM pk_account_candy_state WHERE account_id=$1 FOR UPDATE`, result.AccountID, result.ID).Scan(&owned)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	status := "completed"
	var passed *bool
	var answer *int
	var moved *int64
	failure, moveFailure := "", ""
	if current.generation != cfg.generation || errors.Is(callErr, context.Canceled) {
		status = "cancelled"
		failure = "测试已取消或配置已更新。"
		text = ""
	} else if callErr != nil {
		status = "failed"
		failure = accountCandySafeError(callErr)
		text = ""
	} else if strings.TrimSpace(text) == "" {
		status = "failed"
		failure = "未取得完整的最终回复。"
	} else {
		answer = intelligenceExtractAnswer(text)
		ok := answer != nil && *answer == 21
		passed = &ok
	}
	if status == "completed" && passed != nil && !*passed && cfg.TargetGroupID != nil {
		moved, moveFailure, err = s.moveAccount(ctx, tx, result, *cfg.TargetGroupID)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_account_candy_results SET status=$2,passed=$3,answer=$4,response_text=$5,moved_to_group_id=$6,error_message=$7,move_error=$8,finished_at=NOW(),duration_ms=$9 WHERE id=$1 AND status='running'`, result.ID, status, passed, answer, text, moved, failure, moveFailure, elapsed); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_account_candy_state SET active_result_id=NULL,lease_until=NULL,next_run_at=CASE WHEN $3 THEN NOW() ELSE NOW()+make_interval(mins=>$4) END WHERE account_id=$1 AND active_result_id=$2`, result.AccountID, result.ID, current.generation != cfg.generation, current.IntervalMinutes); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *AccountCandyTestService) moveAccount(ctx context.Context, tx *sql.Tx, result *AccountCandyTestResult, targetID int64) (*int64, string, error) {
	var platform, accountType, privacy string
	err := tx.QueryRowContext(ctx, `SELECT a.platform,a.type,COALESCE((CASE WHEN a.parent_account_id IS NULL THEN a.extra ELSE p.extra END)->>'privacy_mode','') FROM accounts a LEFT JOIN accounts p ON p.id=a.parent_account_id AND p.deleted_at IS NULL WHERE a.id=$1 AND a.deleted_at IS NULL FOR UPDATE OF a`, result.AccountID).Scan(&platform, &accountType, &privacy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "账号已删除，未转组。", nil
	}
	if err != nil {
		return nil, "", err
	}
	if platform != PlatformOpenAI {
		return nil, "账号平台已更改，未转组。", nil
	}
	identity, err := accountCandyIdentity(ctx, tx, result.AccountID)
	if err != nil {
		return nil, "", err
	}
	if identity != result.identity {
		return nil, "账号凭据、模型映射或连接配置已更改，保留当前分组。", nil
	}
	groups, err := accountCandyGroups(ctx, tx, result.AccountID)
	if err != nil {
		return nil, "", err
	}
	if len(groups) != len(result.OriginalGroupIDs) {
		return nil, "账号分组已被修改，保留当前分组。", nil
	}
	for i, id := range groups {
		if id != result.OriginalGroupIDs[i] {
			return nil, "账号分组已被修改，保留当前分组。", nil
		}
	}
	var targetPlatform, targetStatus string
	var oauthOnly, privacyRequired bool
	err = tx.QueryRowContext(ctx, `SELECT platform,status,require_oauth_only,require_privacy_set FROM groups WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, targetID).Scan(&targetPlatform, &targetStatus, &oauthOnly, &privacyRequired)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "目标分组已删除，保留原分组。", nil
	}
	if err != nil {
		return nil, "", err
	}
	if targetPlatform != PlatformOpenAI || targetStatus != StatusActive {
		return nil, "目标分组当前不可用，保留原分组。", nil
	}
	if oauthOnly && accountType != AccountTypeOAuth && accountType != AccountTypeSetupToken {
		return nil, "目标分组仅允许 OAuth 账号，保留原分组。", nil
	}
	if privacyRequired && privacy != PrivacyModeTrainingOff {
		return nil, "账号未满足目标分组的隐私要求，保留原分组。", nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1`, result.AccountID); err != nil {
		return nil, "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1)`, result.AccountID, targetID); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(map[string]any{"group_ids": append(groups, targetID)})
	if err != nil {
		return nil, "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id,payload) VALUES($1,$2,$3::jsonb)`, SchedulerOutboxEventAccountGroupsChanged, result.AccountID, string(payload)); err != nil {
		return nil, "", err
	}
	return &targetID, "", nil
}

// No credential material or digest is persisted or returned by the API.
// JSONB has deterministic text ordering. Include the resolved parent identity
// for credential shadows without comparing unrelated mutable usage metadata.
func accountCandyIdentity(ctx context.Context, tx *sql.Tx, accountID int64) ([32]byte, error) {
	var parentID sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT parent_account_id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID).Scan(&parentID)
	if err != nil {
		return [32]byte{}, err
	}
	if parentID.Valid {
		var id int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, parentID.Int64).Scan(&id); err != nil {
			return [32]byte{}, err
		}
	}
	proxyRows, err := tx.QueryContext(ctx, `SELECT id FROM proxies WHERE id IN (SELECT proxy_id FROM accounts WHERE id=$1 OR id=$2) ORDER BY id FOR SHARE`, accountID, parentID)
	if err != nil {
		return [32]byte{}, err
	}
	for proxyRows.Next() {
		var id int64
		if err = proxyRows.Scan(&id); err != nil {
			_ = proxyRows.Close()
			return [32]byte{}, err
		}
	}
	err = proxyRows.Err()
	_ = proxyRows.Close()
	if err != nil {
		return [32]byte{}, err
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT jsonb_build_array(a.platform,a.type,a.credentials,a.proxy_id,a.parent_account_id,
		a.extra->'kilo_bridge_enabled',a.extra->'openai_responses_mode',a.extra->'openai_responses_supported',
		p.platform,p.type,p.credentials,p.proxy_id,p.extra->'kilo_bridge_enabled',
		CASE WHEN proxy.id IS NULL THEN NULL ELSE jsonb_build_array(proxy.protocol,proxy.host,proxy.port,COALESCE(proxy.username,''),COALESCE(proxy.password,'')) END,
		CASE WHEN parent_proxy.id IS NULL THEN NULL ELSE jsonb_build_array(parent_proxy.protocol,parent_proxy.host,parent_proxy.port,COALESCE(parent_proxy.username,''),COALESCE(parent_proxy.password,'')) END,
		a.extra->'synthetic_ui_test',a.extra->'enable_tls_fingerprint',a.extra->'tls_fingerprint_profile_id')::text
		FROM accounts a LEFT JOIN accounts p ON p.id=a.parent_account_id AND p.deleted_at IS NULL
		LEFT JOIN proxies proxy ON proxy.id=a.proxy_id AND proxy.deleted_at IS NULL LEFT JOIN proxies parent_proxy ON parent_proxy.id=p.proxy_id AND parent_proxy.deleted_at IS NULL
		WHERE a.id=$1 AND a.deleted_at IS NULL`, accountID).Scan(&raw)
	if err != nil {
		return [32]byte{}, err
	}
	return accountCandyCanonicalIdentity([]byte(raw))
}

func accountCandyCanonicalIdentity(raw []byte) ([32]byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return [32]byte{}, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func accountCandySnapshotIdentity(account, credentialAccount *Account) ([32]byte, error) {
	credentials := func(a *Account) map[string]any {
		if a.Credentials == nil {
			return map[string]any{}
		}
		return a.Credentials
	}
	proxy := func(p *Proxy) any {
		if p == nil {
			return nil
		}
		return []any{p.Protocol, p.Host, p.Port, p.Username, p.Password}
	}
	values := []any{account.Platform, account.Type, credentials(account), account.ProxyID, account.ParentAccountID,
		account.Extra["kilo_bridge_enabled"], account.Extra["openai_responses_mode"], account.Extra["openai_responses_supported"],
		nil, nil, nil, nil, nil, proxy(account.Proxy), nil,
		account.Extra["synthetic_ui_test"], account.Extra["enable_tls_fingerprint"], account.Extra["tls_fingerprint_profile_id"]}
	if account.ParentAccountID != nil {
		values[8], values[9], values[10], values[11], values[12] = credentialAccount.Platform, credentialAccount.Type, credentials(credentialAccount), credentialAccount.ProxyID, credentialAccount.Extra["kilo_bridge_enabled"]
		values[14] = proxy(credentialAccount.Proxy)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return [32]byte{}, err
	}
	return accountCandyCanonicalIdentity(raw)
}

// This cleanup is independent of the scheduling switch. A crashed manual
// probe must become retryable even while automatic tests remain disabled.
func (s *AccountCandyTestService) expireAbandoned(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountCandyLockID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `WITH expired AS MATERIALIZED (
		SELECT account_id,active_result_id FROM pk_account_candy_state WHERE active_result_id IS NOT NULL AND lease_until<=NOW() FOR UPDATE
	), closed AS (
		UPDATE pk_account_candy_results r SET status='failed',error_message='上次测试未正常结束，租约已到期。',finished_at=NOW()
		FROM expired e WHERE r.id=e.active_result_id AND r.status='running' RETURNING r.id
	) UPDATE pk_account_candy_state st SET active_result_id=NULL,lease_until=NULL,next_run_at=NOW()
	FROM expired e WHERE st.account_id=e.account_id AND st.active_result_id=e.active_result_id AND st.lease_until<=NOW()`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const accountCandyResultColumns = `r.id,r.account_id,r.source,r.status,r.passed,r.answer,r.model,r.effort,r.original_group_ids,r.moved_to_group_id,r.error_message,r.move_error,r.started_at,r.finished_at,r.duration_ms`

type accountCandyScanner interface{ Scan(...any) error }

func scanAccountCandyResult(row accountCandyScanner, details bool) (AccountCandyTestResult, error) {
	var r AccountCandyTestResult
	var raw []byte
	fields := []any{&r.ID, &r.AccountID, &r.Source, &r.Status, &r.Passed, &r.Answer, &r.Model, &r.Effort, &raw, &r.MovedToGroupID, &r.ErrorMessage, &r.MoveError, &r.StartedAt, &r.FinishedAt, &r.DurationMS}
	if details {
		fields = append(fields, &r.Prompt, &r.ResponseText)
	}
	if err := row.Scan(fields...); err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r.OriginalGroupIDs); err != nil {
		return r, err
	}
	return r, nil
}

func (s *AccountCandyTestService) ListLatest(ctx context.Context, accountIDs []int64) ([]AccountCandyTestResult, error) {
	result := make([]AccountCandyTestResult, 0)
	if len(accountIDs) == 0 {
		return result, nil
	}
	if len(accountIDs) > 200 {
		return nil, infraerrors.BadRequest("ACCOUNT_CANDY_TOO_MANY_IDS", "一次最多读取 200 个账号")
	}
	args := make([]any, len(accountIDs))
	placeholders := make([]string, len(accountIDs))
	for i, id := range accountIDs {
		if id <= 0 {
			return nil, infraerrors.BadRequest("INVALID_ACCOUNT_ID", "账号 ID 无效")
		}
		args[i] = id
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ON(r.account_id) `+accountCandyResultColumns+` FROM pk_account_candy_results r JOIN accounts a ON a.id=r.account_id AND a.deleted_at IS NULL WHERE r.account_id IN(`+strings.Join(placeholders, ",")+`) ORDER BY r.account_id,r.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		r, err := scanAccountCandyResult(rows, false)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *AccountCandyTestService) GetResult(ctx context.Context, accountID, resultID int64) (*AccountCandyTestResult, error) {
	r, err := scanAccountCandyResult(s.db.QueryRowContext(ctx, `SELECT `+accountCandyResultColumns+`,r.prompt,r.response_text FROM pk_account_candy_results r JOIN accounts a ON a.id=r.account_id AND a.deleted_at IS NULL WHERE r.account_id=$1 AND r.id=$2`, accountID, resultID), true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("ACCOUNT_CANDY_RESULT_NOT_FOUND", "账号测试记录不存在")
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Transport errors can contain credentials or raw upstream bodies. Persist
// only fixed classifications; final assistant text is stored separately.
func accountCandySafeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "账号测试超时，未转组。"
	}
	var failure *accountCandyProbeError
	if errors.As(err, &failure) {
		return failure.message
	}
	return "账号测试请求失败，请检查账号状态、模型与代理配置；未转组。"
}
