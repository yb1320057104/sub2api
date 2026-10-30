package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/google/uuid"
)

type authorizationJob struct {
	id, historyID        int64
	accountID            *int64
	email, source, token string
	settings             AuthorizationSubmit
}
type authorizationProxy struct {
	id  int64
	raw string
}

func (s *AccountAuthorizationService) runAuthorizationBatch(ctx context.Context, cfg *AuthorizationConfig, conn *sql.Conn) error {
	var batch string
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT batch_id,settings FROM pk_authorization_jobs WHERE status='queued' ORDER BY id LIMIT 1`).Scan(&batch, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var settings AuthorizationSubmit
	if err = json.Unmarshal(raw, &settings); err != nil {
		return err
	}
	concurrency := settings.AuthorizationConcurrency
	if concurrency < 1 || concurrency > 50 {
		return errors.New("invalid persisted concurrency")
	}
	// Each wave respects the submitted concurrency. The independent coordinator
	// continues inspecting the latest roster while these browser sessions run.
	rows, err := s.db.QueryContext(ctx, `SELECT id,history_id,account_id,email,source,settings FROM pk_authorization_jobs WHERE batch_id=$1 AND status='queued' ORDER BY id LIMIT $2`, batch, concurrency)
	if err != nil {
		return err
	}
	jobs := []authorizationJob{}
	for rows.Next() {
		var job authorizationJob
		var data []byte
		if err = rows.Scan(&job.id, &job.historyID, &job.accountID, &job.email, &job.source, &data); err != nil {
			break
		}
		if err = json.Unmarshal(data, &job.settings); err != nil {
			break
		}
		job.token = uuid.NewString()
		jobs = append(jobs, job)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, job := range jobs {
		if err = conn.PingContext(ctx); err != nil {
			break
		}
		result, e := s.db.ExecContext(ctx, `UPDATE pk_authorization_jobs SET status='running',stage='starting',message='正在准备授权',started_at=NOW(),owner_token=$2 WHERE id=$1 AND status='queued'`, job.id, job.token)
		if e != nil {
			err = e
			break
		}
		n, e := result.RowsAffected()
		if e != nil {
			err = e
			break
		}
		if n != 1 {
			continue
		}
		wg.Add(1)
		go func(j authorizationJob) { defer wg.Done(); s.runAuthorizationJob(ctx, cfg, &j, conn) }(job)
	}
	wg.Wait()
	return err
}

func (s *AccountAuthorizationService) jobProgress(ctx context.Context, job *authorizationJob, cfg *AuthorizationConfig, stage, message string) error {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT j.status='running' AND j.owner_token=$2 AND (j.source='manual' OR (c.reauth_enabled AND c.revision=$3)) FROM pk_authorization_jobs j CROSS JOIN pk_authorization_config c WHERE j.id=$1 AND c.id=1`, job.id, job.token, cfg.Revision).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return context.Canceled
	}
	if stage != "" {
		_, err = s.db.ExecContext(ctx, `UPDATE pk_authorization_jobs SET stage=$3,message=$4 WHERE id=$1 AND owner_token=$2 AND status='running' AND (stage<>$3 OR message<>$4)`, job.id, job.token, stage, message)
	}
	return err
}

func authorizationEmail(account *Account) string {
	for _, value := range []string{account.GetCredential("email"), account.GetExtraString("email_address"), account.GetExtraString("email")} {
		if strings.TrimSpace(value) != "" {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func (s *AccountAuthorizationService) findAuthorizationAccount(ctx context.Context, job *authorizationJob) (*Account, error) {
	if job.accountID != nil {
		a, err := s.accounts.GetByID(ctx, *job.accountID)
		if err != nil {
			return nil, errors.New("原账号已删除或不可用，未新建替代账号")
		}
		if authorizationEmail(a) != job.email {
			return nil, errors.New("原账号邮箱已变化，未覆盖凭据")
		}
		return a, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM accounts a WHERE a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth' AND a.parent_account_id IS NULL AND `+authorizationEmailSQL+`=$1 ORDER BY a.id LIMIT 2`, job.email)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 1 {
		return nil, errors.New("存在多个同邮箱账号，请通过巡检绑定具体账号后重授权")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	job.accountID = &ids[0]
	return s.accounts.GetByID(ctx, ids[0])
}

func (s *AccountAuthorizationService) loadAuthorizationProxies(ctx context.Context) ([]authorizationProxy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,secret_encrypted FROM pk_authorization_proxies WHERE enabled=true ORDER BY id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []authorizationProxy{}
	for rows.Next() {
		var p authorizationProxy
		var encrypted string
		if err = rows.Scan(&p.id, &encrypted); err != nil {
			return nil, err
		}
		p.raw, err = s.encryptor.Decrypt(encrypted)
		if err != nil {
			return nil, errors.New("授权代理解密失败")
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *AccountAuthorizationService) runAuthorizationJob(parent context.Context, cfg *AuthorizationConfig, job *authorizationJob, lease *sql.Conn) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	var resultErr error
	var before *Account
	defer func() {
		if recover() != nil {
			resultErr = errors.New("授权任务异常中断")
		}
		if resultErr == nil {
			return
		}
		status, stage, message := "failed", "failed", "授权失败，请检查账号、代理及运行依赖后重试"
		var failure *authorizationBrowserError
		if errors.As(resultErr, &failure) {
			stage = failure.stage
			message = failure.message
			if failure.needsInput {
				status = "needs_input"
			}
			if failure.stage == "account_banned" {
				status = "banned"
			}
		}
		if errors.Is(resultErr, context.Canceled) {
			status = "cancelled"
			message = "任务已取消或自动授权配置发生变化"
		}
		// Only fixed messages are persisted. Browser, proxy, OAuth and database errors
		// may contain sensitive material and are never copied to a job or a log.
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = s.finishAuthorizationFailure(cleanup, job, before, status, stage, message)
	}()
	progress := func(stage, message string) error {
		if err := lease.PingContext(ctx); err != nil {
			return context.Canceled
		}
		return s.jobProgress(ctx, job, cfg, stage, message)
	}
	if resultErr = progress("starting", "正在读取最近提交的凭据"); resultErr != nil {
		return
	}
	// The cron round pins the latest history. A later supplement waits for the
	// next round and cannot silently alter a job already queued by an older round.
	secret, err := s.Reveal(ctx, job.historyID)
	if err != nil {
		resultErr = authorizationBrowserFailure("history_unavailable", "历史凭据无法读取或解密，请重新提交账号密码和2FA")
		return
	}
	defer func() { secret.Password = ""; secret.TwoFA = "" }()
	if job.source == "reauthorization" && (secret.Password == "" || secret.TwoFA == "") {
		resultErr = authorizationBrowserFailure("missing_credentials", "账号凭据不够，已跳过授权，请补全凭证")
		return
	}
	before, err = s.findAuthorizationAccount(ctx, job)
	if err != nil {
		resultErr = authorizationBrowserFailure("account_changed", "原账号不存在、邮箱发生变化或存在多个同邮箱账号，未写入凭据")
		return
	}
	if before != nil && (!before.IsOAuth() || before.Platform != PlatformOpenAI || before.IsCredentialShadow() || before.IsOpenAIAgentIdentity() || before.IsOpenAIPersonalAccessToken() || before.IsSyntheticUITest()) {
		resultErr = authorizationBrowserFailure("unsupported", "该账号类型不支持密码与2FA重新授权")
		return
	}
	if job.source == "reauthorization" {
		var eligible bool
		err = s.db.QueryRowContext(ctx, `SELECT `+authorizationErrorSQL+` OR (st.status='expired' AND st.credential_version=md5(a.credentials::text) AND EXISTS(SELECT 1 FROM pk_authorization_config c,jsonb_array_elements_text(c.reauth_status_codes) code WHERE c.id=1 AND code::int=st.http_status)) FROM accounts a LEFT JOIN pk_authorization_state st ON st.account_id=a.id WHERE a.id=$1 AND a.deleted_at IS NULL`, *job.accountID).Scan(&eligible)
		if err != nil || !eligible {
			resultErr = authorizationBrowserFailure("account_changed", "账号已恢复或不再命中重授权状态码，本次任务跳过")
			return
		}
	}
	proxies, err := s.loadAuthorizationProxies(ctx)
	if err != nil {
		resultErr = authorizationBrowserFailure("proxy_failed", "无法读取授权代理")
		return
	}
	if len(proxies) == 0 {
		resultErr = authorizationBrowserFailure("proxy_missing", "请先在授权代理页添加可用代理")
		return
	}
	var token *OpenAITokenInfo
	for i := 0; i < len(proxies); i++ {
		proxy := proxies[(int(job.id)+i)%len(proxies)]
		var enabled bool
		if err = s.db.QueryRowContext(ctx, `SELECT enabled FROM pk_authorization_proxies WHERE id=$1`, proxy.id).Scan(&enabled); err != nil || !enabled {
			continue
		}
		session, e := s.oauth.generateWorkbenchAuthURL(ctx, proxy.raw)
		if e != nil {
			resultErr = authorizationBrowserFailure("session_failed", "无法生成授权链接")
			return
		}
		callback, e := runAuthorizationBrowser(ctx, proxy.raw, session.AuthURL, secret, progress)
		if e != nil {
			s.oauth.sessionStore.Delete(session.SessionID)
			var failure *authorizationBrowserError
			if errors.As(e, &failure) && (failure.stage == "proxy_precheck_failed" || failure.stage == "proxy_failed") {
				_, _ = s.db.ExecContext(ctx, `UPDATE pk_authorization_proxies SET last_checked_at=NOW(),last_status='failed',last_message=$2 WHERE id=$1`, proxy.id, failure.message)
				resultErr = e
				continue
			}
			resultErr = e
			return
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE pk_authorization_proxies SET last_checked_at=NOW(),last_status='passed',last_message='chatgpt.com GET预检通过' WHERE id=$1`, proxy.id)
		u, e := url.Parse(callback)
		if e != nil {
			s.oauth.sessionStore.Delete(session.SessionID)
			resultErr = e
			return
		}
		if resultErr = progress("exchange", "正在交换并校验授权凭据"); resultErr != nil {
			s.oauth.sessionStore.Delete(session.SessionID)
			return
		}
		token, e = s.oauth.ExchangeCode(ctx, &OpenAIExchangeCodeInput{SessionID: session.SessionID, Code: u.Query().Get("code"), State: u.Query().Get("state")})
		s.oauth.sessionStore.Delete(session.SessionID)
		if e != nil {
			if authorizationBanText(e.Error()) {
				resultErr = authorizationBannedFailure()
				return
			}
			resultErr = authorizationBrowserFailure("exchange_failed", "授权码交换失败，请检查授权出口或重新提交任务")
			return
		}
		resultErr = nil
		break
	}
	if token == nil {
		if resultErr == nil {
			resultErr = authorizationBrowserFailure("proxy_failed", "没有可用授权出口")
		}
		return
	}
	if token.AccessToken == "" || token.RefreshToken == "" || strings.ToLower(strings.TrimSpace(token.Email)) != job.email {
		resultErr = authorizationBrowserFailure("identity_mismatch", "授权凭据不完整或邮箱不匹配，未写入账号")
		return
	}
	if before != nil && before.GetCredential("chatgpt_account_id") != "" && before.GetCredential("chatgpt_account_id") != token.ChatGPTAccountID {
		resultErr = authorizationBrowserFailure("identity_mismatch", "授权工作区与原账号不同，未覆盖原账号")
		return
	}
	if resultErr = progress("saving", "正在保存新凭据并保留原账号设置"); resultErr != nil {
		return
	}
	resultErr = s.applyAuthorization(ctx, cfg, job, before, token)
}

func (s *AccountAuthorizationService) finishAuthorizationFailure(ctx context.Context, job *authorizationJob, before *Account, status, stage, message string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := tx.ExecContext(ctx, `UPDATE pk_authorization_jobs SET status=$3,stage=$4,message=$5,finished_at=NOW() WHERE id=$1 AND owner_token=$2 AND status='running'`, job.id, job.token, status, stage, message)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return nil
	}
	if status == "banned" && before != nil {
		raw, e := json.Marshal(before.Credentials)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO pk_authorization_state(account_id,credential_version,status,message) SELECT id,md5(credentials::text),'banned',$2 FROM accounts WHERE id=$1 AND deleted_at IS NULL AND credentials=$3::jsonb ON CONFLICT(account_id) DO UPDATE SET credential_version=EXCLUDED.credential_version,status='banned',message=EXCLUDED.message,updated_at=NOW()`, before.ID, message, string(raw))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *AccountAuthorizationService) applyAuthorization(ctx context.Context, cfg *AuthorizationConfig, job *authorizationJob, before *Account, token *OpenAITokenInfo) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	// Lock the durable job before account mutation; cancellation and recovery cannot
	// race a successful credentials commit or allow an obsolete worker to commit.
	result, err := tx.Client().ExecContext(txctx, `UPDATE pk_authorization_jobs SET stage=stage WHERE id=$1 AND status='running' AND owner_token=$2`, job.id, job.token)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return context.Canceled
	}
	if job.source == "reauthorization" {
		r, e := tx.Client().ExecContext(txctx, `UPDATE pk_authorization_config SET revision=revision WHERE id=1 AND reauth_enabled AND revision=$1`, cfg.Revision)
		if e != nil {
			return e
		}
		n, e = r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return context.Canceled
		}
	}
	credentials := s.oauth.BuildAccountCredentials(token)
	var account *Account
	if before != nil {
		if _, err = tx.Client().ExecContext(txctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, before.ID); err != nil {
			return err
		}
		current, e := s.accounts.GetByID(txctx, before.ID)
		if e != nil {
			return e
		}
		if current.Platform != before.Platform || current.Type != before.Type || current.IsCredentialShadow() || current.IsSyntheticUITest() || authorizationEmail(current) != job.email || !reflect.DeepEqual(current.Credentials, before.Credentials) || current.Status != before.Status || current.Schedulable != before.Schedulable || current.ErrorMessage != before.ErrorMessage {
			return authorizationBrowserFailure("account_changed", "账号凭据或启用状态在授权期间发生变化，未覆盖")
		}
		input := &UpdateAccountInput{Credentials: SanitizeStoredCredentials(PlatformOpenAI, MergeCredentials(current.Credentials, credentials))}
		// Clear system error state atomically; inactive accounts keep their status
		// and scheduling switch. The error path below explicitly restores scheduling.
		if current.Status == StatusError {
			input.Status = StatusActive
		}
		account, err = s.admin.UpdateAccount(txctx, before.ID, input)
		if err != nil {
			return err
		}
		if current.Status == StatusError {
			account, err = s.admin.ClearAccountError(txctx, before.ID)
			if err != nil {
				return err
			}
			// Native SetError disables scheduling. Recover that error block only;
			// inactive accounts never enter this branch and remain disabled.
			account, err = s.admin.SetAccountSchedulable(txctx, before.ID, true)
			if err != nil {
				return err
			}
		}
	} else {
		// Recheck under the email lock immediately before creation. Native account
		// service supplies admission validation, default extra and transactional groups.
		if _, err = tx.Client().ExecContext(txctx, `SELECT pg_advisory_xact_lock(hashtextextended('pk_authorization:'||$1,0))`, job.email); err != nil {
			return err
		}
		candidates, e := s.accounts.ListAllWithFilters(txctx, PlatformOpenAI, AccountTypeOAuth, "", "", 0, "")
		if e != nil {
			return e
		}
		for i := range candidates {
			if authorizationEmail(&candidates[i]) == job.email && !candidates[i].IsCredentialShadow() {
				return authorizationBrowserFailure("account_changed", "授权期间出现同邮箱账号，请重新提交以更新原账号")
			}
		}
		load := job.settings.AccountConcurrency
		name := []rune(job.email)
		if len(name) > 100 {
			name = name[:100]
		}
		account, err = s.admin.CreateAccount(txctx, &CreateAccountInput{Name: string(name), Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: credentials, Concurrency: load, LoadFactor: &load, Priority: job.settings.AccountPriority, GroupIDs: job.settings.GroupIDs, SkipDefaultGroupBind: true})
		if err != nil {
			return authorizationBrowserFailure("admission_failed", "入库被拒绝，请检查分组有效性及准入要求")
		}
	}
	if _, err = tx.Client().ExecContext(txctx, `INSERT INTO pk_authorization_state(account_id,credential_version,status,http_status,message,authorized_at) SELECT id,md5(credentials::text),'authorized',200,'授权成功',NOW() FROM accounts WHERE id=$1 ON CONFLICT(account_id) DO UPDATE SET credential_version=EXCLUDED.credential_version,status='authorized',http_status=200,message='授权成功',authorized_at=NOW(),updated_at=NOW()`, account.ID); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(txctx, `UPDATE pk_authorization_jobs SET account_id=$3,history_id=$4,status='completed',stage='completed',message='授权成功，凭据已写入账号',finished_at=NOW() WHERE id=$1 AND owner_token=$2`, job.id, job.token, account.ID, job.historyID); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.authorizationAccountCommitted(ctx, account.ID)
	if s.invalidator != nil {
		if err = s.invalidator.InvalidateToken(ctx, account); err != nil {
			raw, marshalErr := json.Marshal(account.Credentials)
			if marshalErr == nil {
				_, _ = s.db.ExecContext(ctx, `UPDATE pk_authorization_state st SET status='cache_pending',message='凭据已保存，等待缓存刷新',updated_at=NOW() FROM accounts a WHERE st.account_id=$1 AND a.id=st.account_id AND a.credentials=$2::jsonb AND st.status='authorized' AND st.credential_version=md5(a.credentials::text)`, account.ID, string(raw))
			}
		}
	}
	return nil
}

func (s *AccountAuthorizationService) authorizationAccountCommitted(ctx context.Context, id int64) {
	if repo, ok := s.accounts.(interface {
		ScopedAdminCommitted(context.Context, int64, bool)
	}); ok {
		repo.ScopedAdminCommitted(ctx, id, false)
	}
}

func (s *AccountAuthorizationService) CheckAuthorizationProxy(ctx context.Context, id int64) error {
	var encrypted string
	if err := s.db.QueryRowContext(ctx, `SELECT secret_encrypted FROM pk_authorization_proxies WHERE id=$1 AND enabled=true`, id).Scan(&encrypted); err != nil {
		return err
	}
	raw, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return errors.New("代理解密失败")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	_, err = runAuthorizationBrowser(probeCtx, raw, "", nil, func(string, string) error { return probeCtx.Err() })
	status, message := "passed", "chatgpt.com GET预检通过"
	if err != nil {
		status = "failed"
		message = "代理预检失败，请检查出口和浏览器运行依赖"
		var e *authorizationBrowserError
		if errors.As(err, &e) {
			message = e.message
		}
	}
	_, saveErr := s.db.ExecContext(ctx, `UPDATE pk_authorization_proxies SET last_checked_at=NOW(),last_status=$2,last_message=$3 WHERE id=$1`, id, status, message)
	return saveErr
}
