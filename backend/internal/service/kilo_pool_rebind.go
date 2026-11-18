package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/google/uuid"
)

// A successful source re-login can still need a new delegated OAuth grant.
// Repair the same source in its occupied slot; never advertise it as idle.
func (s *KiloService) queueKiloRebind(ctx context.Context, p kiloPoolBinding, observed *Account) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var accountID, sourceID, revision, proxyID int64
	var org string
	var routing []byte
	var resume bool
	err = kiloScan(ctx, tx.Client(), `SELECT account_id,source_account_id,binding_revision,login_proxy_id,organization_id,routing_options,binding_resume FROM pk_kilo_prepared_accounts WHERE id=$1 AND binding_state='recovering' AND identity_status='active' FOR UPDATE`, []any{p.ID}, &accountID, &sourceID, &revision, &proxyID, &org, &routing, &resume)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if accountID != p.GatewayID || sourceID != p.SourceID || revision != p.Revision {
		return nil
	}
	var busy bool
	if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE prepared_id=$1 AND status IN ('queued','running','awaiting_authorization'))`, []any{p.ID}, &busy); err != nil {
		return err
	}
	if busy {
		return nil
	}
	txctx := dbent.NewTxContext(ctx, tx)
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return err
	}
	source, err := s.accounts.GetByID(txctx, sourceID)
	if err != nil {
		return err
	}
	if !source.IsActive() || OpenAIAuthRecoveryPending(source) || source.GetOpenAIAccessToken() != observed.GetOpenAIAccessToken() || kiloSourceIdentity(source) != kiloSourceIdentity(observed) {
		return nil
	}
	gateway, err := s.accounts.GetByID(txctx, accountID)
	if err != nil {
		return err
	}
	if int64(ParseExtraInt(source.Extra["kilo_source_prepared_id"])) != p.ID || int64(ParseExtraInt(gateway.Extra["kilo_prepared_id"])) != p.ID {
		return kiloConflict("恢复中的Kilo双向绑定已改变")
	}
	var options KiloRoutingOptions
	if json.Unmarshal(routing, &options) != nil {
		return errors.New("Kilo路由配置无效")
	}
	groups := make([]int64, 0, len(gateway.AccountGroups))
	for _, group := range gateway.AccountGroups {
		groups = append(groups, group.GroupID)
	}
	if len(groups) == 0 {
		return kiloBad("KILO_GROUP_INVALID", "Kilo原目标分组已不存在，恢复任务保持等待")
	}
	resume = resume && gateway.Status == StatusError && strings.HasPrefix(gateway.ErrorMessage, "Kilo source authorization")
	input := KiloBatchInput{RequestID: uuid.NewString(), OnboardingName: gateway.Name, SourceAccountIDs: []int64{sourceID}, GroupIDs: groups, ProxyGroupID: options.ProxyGroupID, ProxiesPerAccount: options.ProxiesPerAccount, ProxyConcurrency: options.ProxyConcurrency, AccountConcurrency: options.AccountConcurrency, Priority: options.Priority, AutoReleaseStatuses: options.AutoReleaseStatuses}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	material, err := s.kiloMaterialVersionTx(ctx, tx, sourceID)
	if err != nil {
		return err
	}
	generation := OpenAIAccessTokenDigest(source.GetOpenAIAccessToken())
	requestID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("kilo-repair:%d:%d:%s", p.ID, revision, generation))).String()
	var exists bool
	if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM pk_kilo_batches WHERE request_id=$1)`, []any{requestID}, &exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	batchID := uuid.NewString()
	if _, err = tx.Client().ExecContext(ctx, `INSERT INTO pk_kilo_batches(id,request_id,payload_hash,options) VALUES($1,$2,$3,$4::jsonb)`, batchID, requestID, kiloHash(input), string(raw)); err != nil {
		return err
	}
	var jobID int64
	if err = kiloScan(ctx, tx.Client(), `INSERT INTO pk_kilo_jobs(batch_id,job_kind,prepared_id,account_id,source_account_id,source_identity,stage,options,login_proxy_id,organization_id,activate_when_ready,material_version,quota_oauth_ready,reused_identity,binding_revision) VALUES($1,'onboard_source',$2,$3,$4,$5,'kilo_login',$6::jsonb,$7,$8,$9,$10,true,true,$11) RETURNING id`, []any{batchID, p.ID, accountID, sourceID, kiloSourceIdentity(source), string(raw), proxyID, org, resume, material.Version, revision}, &jobID); err != nil {
		return err
	}
	source.Extra["kilo_onboarding_job_id"], source.Extra["kilo_onboarding_proxy_id"] = jobID, proxyID
	if err = s.accounts.Update(txctx, source); err != nil {
		return err
	}
	gateway.Extra["kilo_onboarding_job_id"], gateway.Extra["kilo_onboarding_source_id"], gateway.Extra["kilo_binding_revision"] = jobID, sourceID, revision
	gateway.Status, gateway.Schedulable = StatusDisabled, false
	if err = s.accounts.Update(txctx, gateway); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='binding',binding_state='binding',binding_job_id=$2,binding_error='delegated_authorization_repair',updated_at=$3 WHERE id=$1`, p.ID, jobID, time.Now()); err != nil {
		return err
	}
	return tx.Commit()
}
