package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type authorizationGroupQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Lock groups in a stable order. A nil account validates the submitted choices;
// admission is checked again against the saved account before binding any group.
func validateAuthorizationGateGroups(ctx context.Context, query authorizationGroupQuerier, ids []int64, account *Account) error {
	unique := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id < 1 {
			return infraerrors.BadRequest("AUTHORIZATION_GATE_GROUPS", "测智目标分组无效")
		}
		unique[id] = struct{}{}
	}
	ordered := make([]int64, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	if len(ordered) == 0 {
		return infraerrors.BadRequest("AUTHORIZATION_GATE_GROUPS", "请选择测智目标分组")
	}
	args := make([]any, len(ordered))
	placeholders := make([]string, len(ordered))
	for i, id := range ordered {
		args[i], placeholders[i] = id, fmt.Sprintf("$%d", i+1)
	}
	rows, err := query.QueryContext(ctx, `SELECT platform,status,require_oauth_only,require_privacy_set FROM groups WHERE deleted_at IS NULL AND id IN (`+strings.Join(placeholders, ",")+`) ORDER BY id FOR SHARE`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var platform, status string
		var oauthOnly, privacyRequired bool
		if err = rows.Scan(&platform, &status, &oauthOnly, &privacyRequired); err != nil {
			return err
		}
		if platform != PlatformOpenAI || status != StatusActive {
			return infraerrors.BadRequest("AUTHORIZATION_GATE_GROUPS", "测智目标分组必须是启用的 OpenAI 分组")
		}
		if account != nil && (account.Platform != platform || (oauthOnly && account.Type != AccountTypeOAuth && account.Type != AccountTypeSetupToken) || (privacyRequired && account.GetExtraString("privacy_mode") != PrivacyModeTrainingOff)) {
			return infraerrors.BadRequest("AUTHORIZATION_GATE_ADMISSION", "账号未满足测智目标分组的类型或隐私准入要求")
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count != len(ordered) {
		return infraerrors.BadRequest("AUTHORIZATION_GATE_GROUPS", "测智目标分组不存在或已删除")
	}
	return nil
}

// Manual submissions are quarantined without groups until the model round
// yields a verdict. Scheduled reauthorization retains existing group policy.
func (s *AccountAuthorizationService) runAuthorizationIntelligenceGate(ctx context.Context, job *authorizationJob, progress func(string, string) error) error {
	if job.accountID == nil {
		return authorizationBrowserFailure("intelligence_failed", "授权已完成，但测智账号不存在；未分组")
	}
	models, err := authorizationIntelligenceModels(job.settings)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.Model)
	}
	if err := progress("intelligence_testing", fmt.Sprintf("授权成功，暂不分组；正在并发测试 %d 个模型：%s", len(models), strings.Join(names, "、"))); err != nil {
		return err
	}
	account, err := s.accounts.GetByID(ctx, *job.accountID)
	if err != nil {
		return err
	}
	if len(account.GroupIDs) > 0 || account.Schedulable {
		return authorizationBrowserFailure("intelligence_account_changed", "账号分组或调度状态已被修改，未覆盖当前设置")
	}
	identity, err := accountCandySnapshotIdentity(account, account)
	if err != nil {
		return err
	}
	results, err := s.runAuthorizationIntelligenceModels(ctx, job, account, identity, models, progress)
	if err != nil {
		return err
	}
	if err := progress("", ""); err != nil {
		return err
	}
	passedResult, answer := authorizationIntelligenceVerdict(results)
	if passedResult == nil {
		return &authorizationBrowserError{stage: "intelligence_failed", message: "授权成功，但部分模型未取得完整测智结果，且没有已确认的不及格结果；账号保持未分组，请查看逐模型详情", needsInput: true}
	}
	passed := *passedResult
	target := job.settings.PassGroupIDs
	verdict := "不及格"
	if passed {
		verdict = "及格"
	}
	// Every completed model and the aggregate verdict are already persisted,
	// even if the following admission checks fail or the job is cancelled.
	if passed && len(target) == 0 {
		return &authorizationBrowserError{stage: "intelligence_groups_missing", message: "授权及测智已完成，但目标分组未配置；账号保持未分组", needsInput: true}
	}
	if err = progress("intelligence_grouping", "测智"+verdict+"，正在校验并写入目标分组"); err != nil {
		return err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	owned, err := tx.Client().ExecContext(txctx, `UPDATE pk_authorization_jobs SET stage=stage WHERE id=$1 AND status='running' AND owner_token=$2`, job.id, job.token)
	if err != nil {
		return err
	}
	n, err := owned.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return context.Canceled
	}
	if !passed {
		// Resolve the current shared failure destination at the grouping step,
		// keeping its setting locked until account groups have been committed.
		rows, queryErr := tx.Client().QueryContext(txctx, `SELECT target_group_id FROM pk_radar_account_config WHERE id=1 FOR SHARE`)
		if queryErr != nil {
			return queryErr
		}
		var targetGroupID sql.NullInt64
		if rows.Next() {
			queryErr = rows.Scan(&targetGroupID)
		}
		if queryErr == nil {
			queryErr = rows.Err()
		}
		_ = rows.Close()
		if queryErr != nil {
			return queryErr
		}
		if !targetGroupID.Valid || targetGroupID.Int64 < 1 {
			return &authorizationBrowserError{stage: "intelligence_groups_missing", message: "测智不及格，但糖果测试设置未配置有效的不合格账号移入分组；账号保持未分组", needsInput: true}
		}
		target = []int64{targetGroupID.Int64}
	}
	if _, err = tx.Client().ExecContext(txctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, account.ID); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(txctx, `SELECT id FROM proxies WHERE id IN (SELECT proxy_id FROM accounts WHERE id=$1) AND deleted_at IS NULL ORDER BY id FOR SHARE`, account.ID); err != nil {
		return err
	}
	current, err := s.accounts.GetByID(txctx, account.ID)
	if err != nil {
		return err
	}
	latestIdentity, err := accountCandySnapshotIdentity(current, current)
	if err != nil {
		return err
	}
	if latestIdentity != identity || len(current.GroupIDs) > 0 || current.Schedulable || current.Status != account.Status {
		return &authorizationBrowserError{stage: "intelligence_account_changed", message: "测智期间账号凭据、连接、分组或状态发生变化，未覆盖当前设置", needsInput: true}
	}
	if err = validateAuthorizationGateGroups(txctx, tx.Client(), target, current); err != nil {
		return &authorizationBrowserError{stage: "intelligence_group_failed", message: "授权及测智已完成，但目标分组已失效或账号未满足类型/隐私准入要求；未分组", needsInput: true}
	}
	// Native updates retain simple-mode and mixed-channel admission checks,
	// transactional group replacement and outbox invalidation.
	if _, err = s.admin.UpdateAccount(txctx, account.ID, &UpdateAccountInput{GroupIDs: &target}); err != nil {
		return &authorizationBrowserError{stage: "intelligence_group_failed", message: "授权及测智已完成，但目标分组已失效或账号未满足准入要求；未分组", needsInput: true}
	}
	if job.resumeScheduling {
		if _, err = s.admin.SetAccountSchedulable(txctx, account.ID, true); err != nil {
			return err
		}
	}
	grouped, err := s.accounts.GetByID(txctx, account.ID)
	if err != nil {
		return err
	}
	groupNames := make([]string, 0, len(grouped.Groups))
	for _, group := range grouped.Groups {
		if group != nil {
			groupNames = append(groupNames, group.Name)
		}
	}
	result, err := json.Marshal(map[string]any{"intelligence_passed": passed, "intelligence_answer": answer, "final_group_ids": target, "final_group_names": groupNames})
	if err != nil {
		return err
	}
	r, err := tx.Client().ExecContext(txctx, `UPDATE pk_authorization_jobs SET status='completed',stage=$3,message=$4,settings=settings||$5::jsonb,finished_at=NOW() WHERE id=$1 AND owner_token=$2 AND status='running'`, job.id, job.token, "intelligence_completed", "授权成功，测智"+verdict+"，已进入所选目标分组", string(result))
	if err != nil {
		return err
	}
	n, err = r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return context.Canceled
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.authorizationAccountCommitted(ctx, account.ID)
	return nil
}
