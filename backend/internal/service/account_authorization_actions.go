package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Delete only explicitly selected authorization/login proxies. Native account
// egress proxies live in a different table and are never touched here.
func (s *AccountAuthorizationService) DeleteAuthorizationProxies(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 || len(ids) > 200 {
		return 0, infraerrors.BadRequest("AUTHORIZATION_PROXY_IDS", "请勾选1至200条授权代理")
	}
	seen := map[int64]bool{}
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return 0, infraerrors.BadRequest("AUTHORIZATION_PROXY_IDS", "代理ID无效")
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize deletion with imports; a later explicit re-import creates a new row.
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", int64(70360003)); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM pk_authorization_proxies WHERE id=ANY($1::bigint[])`, pq.Array(unique))
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

type AuthorizationActionResult struct {
	JobID   int64  `json:"job_id"`
	BatchID string `json:"batch_id"`
}

func (s *AccountAuthorizationService) RetryAuthorizationJob(ctx context.Context, id int64) (*AuthorizationActionResult, error) {
	return s.enqueueAuthorizationAction(ctx, id, false)
}
func (s *AccountAuthorizationService) AuthorizeAccountNow(ctx context.Context, id int64) (*AuthorizationActionResult, error) {
	return s.enqueueAuthorizationAction(ctx, id, true)
}

// Buttons create a fresh durable job. The failed job and its evidence remain
// unchanged; passwords are taken from the latest non-deleted credential history.
func (s *AccountAuthorizationService) enqueueAuthorizationAction(ctx context.Context, id int64, accountAction bool) (*AuthorizationActionResult, error) {
	if err := s.requireAuthorizationEncryption(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockAuthorizationHistory(ctx, tx); err != nil {
		return nil, err
	}
	var email string
	var accountID *int64
	var options AuthorizationSubmit
	retryOf := int64(0)
	immediateAuthorization := accountAction
	reauthorizationRetry := false
	if accountAction {
		var eligible, supported, banned bool
		err = tx.QueryRowContext(ctx, `SELECT `+authorizationEmailSQL+`,`+authorizationErrorSQL+`,(a.platform='openai' AND `+authorizationSupportedSQL+`),EXISTS(SELECT 1 FROM pk_authorization_state st WHERE st.account_id=a.id AND st.status='banned' AND st.credential_version=md5(a.credentials::text)) FROM accounts a WHERE a.id=$1 AND a.deleted_at IS NULL FOR UPDATE OF a`, id).Scan(&email, &eligible, &supported, &banned)
		if err != nil {
			return nil, err
		}
		if !supported {
			return nil, infraerrors.BadRequest("AUTHORIZATION_UNSUPPORTED", "该账号类型不支持自动授权")
		}
		if banned {
			return nil, infraerrors.BadRequest("AUTHORIZATION_BANNED", "账号已确认封禁，不能立即授权")
		}
		if !eligible {
			return nil, infraerrors.Conflict("AUTHORIZATION_ACCOUNT_RECOVERED", "账号已恢复或当前错误码未命中重授权设置，请刷新列表")
		}
		accountID = &id
		var groups []byte
		err = tx.QueryRowContext(ctx, `SELECT authorization_concurrency,account_concurrency,account_priority,group_ids FROM pk_authorization_config WHERE id=1`).Scan(&options.AuthorizationConcurrency, &options.AccountConcurrency, &options.AccountPriority, &groups)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(groups, &options.GroupIDs); err != nil {
			return nil, err
		}
		workbench, e := authorizationWorkbenchTx(ctx, tx)
		if e != nil {
			return nil, e
		}
		options.IntelligenceGate = workbench.IntelligenceGate
		if options.IntelligenceGate {
			candy, e := authorizationCandyConfigTx(ctx, tx)
			if e != nil {
				return nil, e
			}
			groups, e := authorizationJobSourceGroups(ctx, tx, accountID, workbench.PassGroupIDs)
			if e != nil {
				return nil, e
			}
			configureAuthorizationGroupGate(&options, candy, groups, true)
			// This remains the source selection if a rule is removed before execution.
			options.GroupIDs = append([]int64{}, workbench.PassGroupIDs...)
		}
	} else {
		var status, source string
		var raw []byte
		var completed bool
		err = tx.QueryRowContext(ctx, `SELECT email,account_id,status,source,settings,COALESCE(settings->>'authorization_completed','false')='true' FROM pk_authorization_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&email, &accountID, &status, &source, &raw, &completed)
		if err != nil {
			return nil, err
		}
		if (status != "failed" && status != "needs_input") || completed {
			return nil, infraerrors.Conflict("AUTHORIZATION_NOT_RETRYABLE", "仅未完成授权的失败任务可以重试；已完成授权的任务请检查测智与分组")
		}
		if err = json.Unmarshal(raw, &options); err != nil {
			return nil, err
		}
		var metadata struct {
			ImmediateAuthorization bool  `json:"immediate_authorization"`
			ReauthorizationRetry   bool  `json:"reauthorization_retry"`
			RetryOfJobID           int64 `json:"retry_of_job_id"`
		}
		if err = json.Unmarshal(raw, &metadata); err != nil {
			return nil, err
		}
		immediateAuthorization = metadata.ImmediateAuthorization
		reauthorizationRetry = source == "reauthorization" || metadata.ReauthorizationRetry
		if !reauthorizationRetry && !immediateAuthorization && metadata.RetryOfJobID > 0 {
			reauthorizationRetry, err = authorizationRetryOrigin(ctx, tx, metadata.RetryOfJobID)
			if err != nil {
				return nil, err
			}
		}
		retryOf = id
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_authorization_jobs WHERE email=$1 AND status IN ('queued','running'))`, email).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		return nil, infraerrors.Conflict("AUTHORIZATION_RUNNING", "该账号已有待执行或执行中的授权任务，请等待完成")
	}
	// Finish an older pending archive before reading the current credentials.
	if err = settleAuthorizationHistoryTx(ctx, tx, email); err != nil {
		return nil, err
	}
	var historyID int64
	var encrypted, revision string
	var hasTwoFA bool
	err = tx.QueryRowContext(ctx, `SELECT id,secret_encrypted,has_twofa,`+authorizationHistoryRevisionSQL+` FROM (`+authorizationCurrentHistorySQL+`) h WHERE email=$1`, email).Scan(&historyID, &encrypted, &hasTwoFA, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.BadRequest("AUTHORIZATION_CREDENTIALS_MISSING", "账号凭据不够，请先补全账号密码和2FA")
	}
	if err != nil {
		return nil, err
	}
	var banned bool
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT status='banned' FROM pk_authorization_jobs WHERE email=$1 AND (status IN ('banned','completed') OR settings->>'authorization_completed'='true') ORDER BY id DESC LIMIT 1),false)`, email).Scan(&banned); err != nil {
		return nil, err
	}
	if banned {
		return nil, infraerrors.BadRequest("AUTHORIZATION_BANNED", "账号已确认封禁，请先处理封禁状态")
	}
	secret, err := s.decodeAuthorizationSecret(email, encrypted)
	if err != nil {
		return nil, err
	}
	defer func() { secret.Password = ""; secret.TwoFA = "" }()
	if err = validateAuthorizationSecret(secret, immediateAuthorization); err != nil {
		return nil, err
	}
	options.Accounts = ""
	if err = validateAuthorizationOptions(options.AuthorizationConcurrency, options.AccountConcurrency, options.AccountPriority, options.GroupIDs); err != nil {
		return nil, err
	}
	if err = normalizeAuthorizationProxyOptions(&options); err != nil {
		return nil, err
	}
	if options.IntelligenceGate {
		// Retrying creates a new task. Refresh destinations before validating;
		// the old task may still refer to a group that has since been replaced.
		if retryOf > 0 {
			candy, e := authorizationCandyConfigTx(ctx, tx)
			if e != nil {
				return nil, e
			}
			if options.IntelligenceSourceGroupID > 0 {
				configureAuthorizationGroupGate(&options, candy, []int64{options.IntelligenceSourceGroupID}, false)
			} else {
				// A retried legacy task adopts configured source rules while keeping
				// its original model snapshot and requested gate choice.
				groups, e := authorizationSavedPassGroups(ctx, tx, options.PassGroupIDs)
				if e != nil {
					return nil, e
				}
				if accountID == nil {
					accountID, e = authorizationSubmissionAccount(ctx, tx, email)
					if e != nil {
						return nil, e
					}
				}
				groups, e = authorizationJobSourceGroups(ctx, tx, accountID, groups)
				if e != nil {
					return nil, e
				}
				options.GroupIDs = append([]int64{}, groups...)
				configureAuthorizationGroupGate(&options, candy, groups, false)
			}
		}
	}
	if options.IntelligenceGate {
		if len(options.PassGroupIDs) == 0 {
			return nil, infraerrors.BadRequest("AUTHORIZATION_GATE_GROUPS", "请在授权工作台选择并保存至少一个及格分组")
		}
		if _, err = authorizationIntelligenceModels(options); err != nil {
			return nil, err
		}
		targets := append(append([]int64{}, options.PassGroupIDs...), options.FailGroupIDs...)
		if err = validateAuthorizationGateGroups(ctx, tx, targets, nil); err != nil {
			return nil, err
		}
	} else if accountID == nil {
		if len(options.GroupIDs) == 0 {
			return nil, infraerrors.BadRequest("AUTHORIZATION_GATE_GROUPS", "本任务不进行糖果测试，请先在授权工作台选择入库分组后重新提交")
		}
		if err = validateAuthorizationGateGroups(ctx, tx, options.GroupIDs, nil); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	snapshot, err := json.Marshal(authorizationCredentialSnapshot{Encrypted: encrypted, Revision: revision, HasTwoFA: hasTwoFA})
	if err != nil {
		return nil, err
	}
	out := &AuthorizationActionResult{BatchID: uuid.NewString()}
	// Immediate/retry is an explicit manual action, independent of the auto-reauth switch.
	err = tx.QueryRowContext(ctx, `INSERT INTO pk_authorization_jobs(batch_id,history_id,email,account_id,source,message,settings) VALUES($1,$2,$3,$4,'manual',$5,$6::jsonb||$7::jsonb||jsonb_build_object('manual_action',true,'retry_of_job_id',$8::bigint,'immediate_authorization',$9::boolean,'reauthorization_retry',$10::boolean)) ON CONFLICT(email) WHERE status IN ('queued','running') DO NOTHING RETURNING id`, out.BatchID, historyID, email, accountID, "管理员已提交，等待授权执行", string(raw), string(snapshot), retryOf, immediateAuthorization, reauthorizationRetry).Scan(&out.JobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.Conflict("AUTHORIZATION_RUNNING", "该账号已有授权任务，请刷新列表")
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.wakeAuthorizationWorker()
	return out, nil
}
