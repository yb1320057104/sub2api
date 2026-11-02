package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

func (s *AccountAuthorizationService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.cron = cron.New()
	if _, err := s.cron.AddFunc("* * * * *", s.wake); err != nil {
		return
	}
	s.started = true
	s.cron.Start()
	// Resume durable manual jobs after activation. Automatic work uses persisted cron due times.
	go s.wake()
}

func (s *AccountAuthorizationService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	s.cancel()
	c := s.cron
	s.mu.Unlock()
	if c != nil {
		<-c.Stop().Done()
	}
	s.workers.Wait()
}

func (s *AccountAuthorizationService) wake() {
	s.wakeAuthorizationWorker()
	s.mu.Lock()
	if !s.started || s.stopped || s.coordinating {
		s.mu.Unlock()
		return
	}
	s.coordinating = true
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		defer func() { s.mu.Lock(); s.coordinating = false; s.mu.Unlock() }()
		defer func() {
			if recover() != nil {
				slog.Error("authorization_scheduler_panic")
			}
		}()
		if err := s.runAuthorizationCron(); err != nil && s.ctx.Err() == nil {
			slog.Warn("authorization_scheduler_incomplete", "message", "任务未完成；下次cron重试，请检查数据库或运行依赖")
		}
	}()
}

func (s *AccountAuthorizationService) runAuthorizationCron() error {
	ctx, cancel := context.WithTimeout(s.ctx, 45*time.Minute)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var locked bool
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", authorizationLockID).Scan(&locked); err != nil || !locked {
		return err
	}
	defer releaseAuthorizationLock(conn, authorizationLockID)
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	if err = s.retryAuthorizationCache(ctx); err != nil {
		return err
	}
	if cfg.InspectionEnabled && (cfg.NextInspectionAt == nil || !cfg.NextInspectionAt.After(time.Now())) {
		if err = s.inspectAuthorizationAccounts(ctx, cfg); err != nil {
			return err
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE pk_authorization_config SET next_inspection_at=$1 WHERE id=1 AND revision=$2`, s.next(authorizationInspectionCron, time.Now()), cfg.Revision); err != nil {
			return err
		}
	}
	if err = conn.PingContext(ctx); err != nil {
		return err
	}
	cfg, err = s.GetConfig(ctx)
	if err != nil {
		return err
	}
	if cfg.ReauthEnabled && (cfg.NextReauthAt == nil || !cfg.NextReauthAt.After(time.Now())) {
		if err = s.queueReauthorization(ctx, cfg); err != nil {
			return err
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE pk_authorization_config SET next_reauth_at=$1 WHERE id=1 AND revision=$2`, s.next(authorizationReauthCron, time.Now()), cfg.Revision); err != nil {
			return err
		}
	}
	s.wakeAuthorizationWorker()
	return nil
}

// A long browser login must not occupy the inspection coordinator. Both are
// dispatched by cron; a second advisory lock serializes durable job consumers.
func (s *AccountAuthorizationService) wakeAuthorizationWorker() {
	s.mu.Lock()
	if !s.started || s.stopped || s.authorizing {
		s.mu.Unlock()
		return
	}
	s.authorizing = true
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		defer func() { s.mu.Lock(); s.authorizing = false; s.mu.Unlock() }()
		defer func() {
			if recover() != nil {
				slog.Error("authorization_worker_panic")
			}
		}()
		if err := s.drainAuthorizationJobs(); err != nil && s.ctx.Err() == nil {
			slog.Warn("authorization_worker_incomplete", "message", "授权队列未完成，下次cron恢复")
		}
	}()
}

func releaseAuthorizationLock(conn *sql.Conn, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", id); err != nil {
		// Never return a connection with a possibly held session lock to the pool.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
}

func (s *AccountAuthorizationService) drainAuthorizationJobs() error {
	// Stop starting new waves after one job budget, but leave a full budget
	// for the final wave and keep all database operations deadline-bounded.
	ctx, cancel := context.WithTimeout(s.ctx, 2*authorizationJobTimeout)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var locked bool
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", authorizationWorkerLockID).Scan(&locked); err != nil || !locked {
		return err
	}
	defer releaseAuthorizationLock(conn, authorizationWorkerLockID)
	if _, err = s.db.ExecContext(ctx, `UPDATE pk_authorization_jobs SET status=CASE WHEN settings->>'authorization_completed'='true' THEN 'needs_input' ELSE 'failed' END,stage='interrupted',message=CASE WHEN settings->>'authorization_completed'='true' AND settings->>'intelligence_preserve_account'='true' THEN '进程中断；授权凭据已保存，未回退已有账号的分组与调度设置，测智或分组尚未完成，请检查任务' WHEN settings->>'authorization_completed'='true' THEN '进程中断；授权凭据已保存，测智或分组未完成，账号保持未分组，请检查后重新提交' ELSE '进程中断，凭据历史已保留；请重新提交或等待下轮重授权' END,finished_at=NOW() WHERE status='running'`); err != nil {
		return err
	}
	if err = s.recoverAuthorizationHistory(ctx); err != nil {
		return err
	}
	drainUntil := time.Now().Add(authorizationJobTimeout)
	for ctx.Err() == nil && time.Now().Before(drainUntil) {
		if err = conn.PingContext(ctx); err != nil {
			return err
		}
		var pending bool
		if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_authorization_jobs WHERE status='queued')`).Scan(&pending); err != nil || !pending {
			return err
		}
		cfg, e := s.GetConfig(ctx)
		if e != nil {
			return e
		}
		if err = s.runAuthorizationBatch(ctx, cfg, conn); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func authorizationMatchesCode(cfg *AuthorizationConfig, code int) bool {
	for _, v := range cfg.ReauthStatusCodes {
		if v == code {
			return true
		}
	}
	return false
}

var authorizationStoredCode = regexp.MustCompile(`[0-9]+`)

func authorizationAccountErrorCode(account *Account, cfg *AuthorizationConfig) int {
	if account.Status != StatusError {
		return 0
	}
	for _, match := range authorizationStoredCode.FindAllString(account.ErrorMessage, -1) {
		if len(match) != 3 {
			continue
		}
		code, _ := strconv.Atoi(match)
		if authorizationMatchesCode(cfg, code) {
			return code
		}
	}
	return 0
}

func authorizationWorkers(ctx context.Context, count, total int, run func(int)) {
	if total < count {
		count = total
	}
	var wg sync.WaitGroup
	queue := make(chan int)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range queue {
				if ctx.Err() == nil {
					run(index)
				}
			}
		}()
	}
	for i := 0; i < total; i++ {
		select {
		case queue <- i:
		case <-ctx.Done():
			close(queue)
			wg.Wait()
			return
		}
	}
	close(queue)
	wg.Wait()
}

func (s *AccountAuthorizationService) inspectAuthorizationAccounts(ctx context.Context, cfg *AuthorizationConfig) error {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM accounts a WHERE a.deleted_at IS NULL AND a.platform='openai' AND `+authorizationSupportedSQL+` ORDER BY a.id`)
	if err != nil {
		return err
	}
	ids := []int64{}
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
		return err
	}
	var resultMu sync.Mutex
	var firstErr error
	authorizationWorkers(ctx, cfg.InspectionConcurrency, len(ids), func(i int) {
		if err := s.inspectAuthorizationAccount(ctx, cfg, ids[i]); err != nil {
			resultMu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			resultMu.Unlock()
		}
	})
	return firstErr
}

func (s *AccountAuthorizationService) inspectAuthorizationAccount(ctx context.Context, cfg *AuthorizationConfig, id int64) error {
	account, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil
	}
	if account.Platform != PlatformOpenAI || !account.IsOAuth() || account.IsCredentialShadow() || account.IsOpenAIAgentIdentity() || account.IsOpenAIPersonalAccessToken() || account.IsSyntheticUITest() {
		return nil
	}
	raw, err := json.Marshal(account.Credentials)
	if err != nil {
		return err
	}
	started := time.Now()
	code := authorizationAccountErrorCode(account, cfg)
	var knownBanned bool
	if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_authorization_state st JOIN accounts a ON a.id=st.account_id WHERE a.id=$1 AND st.status='banned' AND st.credential_version=md5(a.credentials::text))`, id).Scan(&knownBanned); err != nil {
		return err
	}
	if knownBanned {
		return nil
	}
	// Native account management owns error detection. Inspection only mirrors
	// the latest persisted state; historical probe failures never trigger login.
	status, message := "unknown", "账号管理尚未记录授权错误，本轮未发送探测请求"
	if account.Status == StatusActive {
		status, message = "authorized", "账号管理状态正常（未发送额外探测）"
	} else if account.Status == StatusError {
		status, message = "error", "账号管理存在错误，但未命中配置的重授权状态码"
	}
	if authorizationMatchesCode(cfg, code) {
		status = "expired"
		message = fmt.Sprintf("授权已掉：HTTP %d，等待统一重授权", code)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current bool
	// Match the authorization writer's config-before-account lock ordering.
	err = tx.QueryRowContext(ctx, `SELECT revision=$1 AND inspection_enabled FROM pk_authorization_config WHERE id=1 FOR SHARE`, cfg.Revision).Scan(&current)
	if err != nil {
		return err
	}
	if !current {
		return nil
	}
	err = tx.QueryRowContext(ctx, `SELECT a.credentials=$2::jsonb AND a.status=$3 AND COALESCE(a.error_message,'')=$4 FROM accounts a WHERE a.id=$1 AND a.deleted_at IS NULL AND a.platform='openai' AND `+authorizationSupportedSQL+` FOR UPDATE OF a`, id, string(raw), account.Status, account.ErrorMessage).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !current {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pk_authorization_state(account_id,credential_version,status,http_status,message,checked_at) SELECT id,md5(credentials::text),$2,$3,$4,NOW() FROM accounts WHERE id=$1 ON CONFLICT(account_id) DO UPDATE SET credential_version=EXCLUDED.credential_version,status=EXCLUDED.status,http_status=EXCLUDED.http_status,message=EXCLUDED.message,checked_at=NOW(),updated_at=NOW() WHERE pk_authorization_state.updated_at<=$5`, id, status, code, message, started)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *AccountAuthorizationService) retryAuthorizationCache(ctx context.Context) error {
	if s.invalidator == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT st.account_id FROM pk_authorization_state st JOIN accounts a ON a.id=st.account_id AND a.deleted_at IS NULL WHERE st.status='cache_pending' LIMIT 200`)
	if err != nil {
		return err
	}
	ids := []int64{}
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
		return err
	}
	for _, id := range ids {
		a, e := s.accounts.GetByID(ctx, id)
		if e != nil {
			continue
		}
		if e = s.refreshAuthorizationAccountCache(ctx, a); e != nil {
			continue
		}
	}
	return nil
}

func (s *AccountAuthorizationService) queueReauthorization(ctx context.Context, cfg *AuthorizationConfig) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockAuthorizationHistory(ctx, tx); err != nil {
		return err
	}
	// The saved workbench switch applies to future automatic/one-click jobs.
	// A group without a rule never implicitly starts an intelligence request.
	workbench, err := authorizationWorkbenchTx(ctx, tx)
	if err != nil {
		return err
	}
	submit := AuthorizationSubmit{AuthorizationConcurrency: cfg.AuthorizationConcurrency, AccountConcurrency: cfg.AccountConcurrency, AccountPriority: cfg.AccountPriority, GroupIDs: append([]int64{}, workbench.PassGroupIDs...)}
	candy := &RadarConfig{GroupRules: []RadarGroupRule{}}
	if workbench.IntelligenceGate {
		candy, err = authorizationCandyConfigTx(ctx, tx)
		if err != nil {
			return err
		}
		submit.IntelligenceModels = candy.Models
		submit.IntelligenceGeneration = candy.Generation
		if len(candy.Models) > 0 {
			submit.IntelligenceModel, submit.IntelligenceEffort = candy.Models[0].Model, candy.Models[0].Effort
		}
	}
	settings, err := json.Marshal(submit)
	if err != nil {
		return err
	}
	if err = settleAuthorizationHistoryTx(ctx, tx, ""); err != nil {
		return err
	}
	// Non-nil arrays keep jsonb_array_elements safe even on fresh installations.
	rules := candy.GroupRules
	if rules == nil {
		rules = []RadarGroupRule{}
	}
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	// The source is the current account table, never the historical roster. Deleted
	// accounts and stale credential versions cannot be resurrected by this query.
	_, err = tx.ExecContext(ctx, `INSERT INTO pk_authorization_jobs(batch_id,history_id,email,account_id,source,settings)
 SELECT $1,h.id,`+authorizationEmailSQL+`,a.id,'reauthorization',$2::jsonb||jsonb_build_object('credential_snapshot_encrypted',h.secret_encrypted,'credential_has_twofa',h.has_twofa,'credential_history_revision',md5(h.secret_encrypted||h.source||(extract(epoch FROM h.created_at))::text),
 'intelligence_gate',rule.value IS NOT NULL,'intelligence_source_group_id',COALESCE((rule.value->>'source_group_id')::bigint,0),
 'pass_group_ids',CASE WHEN rule.value IS NOT NULL THEN rule.value->'pass_group_ids' ELSE '[]'::jsonb END,
 'fail_group_ids',CASE WHEN rule.value IS NOT NULL THEN jsonb_build_array((rule.value->>'fail_group_id')::bigint) ELSE '[]'::jsonb END) FROM accounts a
 JOIN (`+authorizationCurrentHistorySQL+`) h ON h.email=`+authorizationEmailSQL+` AND h.has_twofa=true
 LEFT JOIN pk_authorization_state st ON st.account_id=a.id
 LEFT JOIN LATERAL (
   SELECT candidate.value FROM jsonb_array_elements($4::jsonb) WITH ORDINALITY candidate(value,position)
   JOIN groups source_group ON source_group.id=(candidate.value->>'source_group_id')::bigint AND source_group.deleted_at IS NULL AND source_group.platform='openai' AND source_group.status='active'
   WHERE (candidate.value->>'source_group_id')::bigint IN (
     SELECT ag.group_id FROM account_groups ag JOIN groups g ON g.id=ag.group_id AND g.deleted_at IS NULL WHERE ag.account_id=a.id)
   ORDER BY candidate.position LIMIT 1
 ) rule ON true
 WHERE a.deleted_at IS NULL AND a.platform='openai' AND `+authorizationSupportedSQL+`
 AND NOT (COALESCE(st.status='banned' AND st.credential_version=md5(a.credentials::text),false))
 AND COALESCE((SELECT status FROM pk_authorization_jobs WHERE email=`+authorizationEmailSQL+` AND (status IN ('banned','completed') OR settings->>'authorization_completed'='true') ORDER BY id DESC LIMIT 1),'')<>'banned'
 AND `+authorizationErrorSQL+`
 AND NOT EXISTS(SELECT 1 FROM pk_authorization_jobs pending WHERE pending.email=`+authorizationEmailSQL+` AND pending.source='manual' AND pending.settings ? 'credential_snapshot_encrypted')
 AND EXISTS(SELECT 1 FROM pk_authorization_config c WHERE c.id=1 AND c.reauth_enabled AND c.revision=$3)
 ORDER BY a.id ON CONFLICT (email) WHERE status IN ('queued','running') DO NOTHING`, uuid.NewString(), string(settings), cfg.Revision, string(rulesJSON))
	if err != nil {
		return err
	}
	return tx.Commit()
}
