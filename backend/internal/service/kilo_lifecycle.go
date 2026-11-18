package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// The original login source does not change when a later subscription attempt
// reserves the identity. In particular, an old failed reservation must not make
// its new GPT source inherit the original identity's retirement.
type kiloLifecyclePolicy struct {
	mode, status string
	origin       sql.NullInt64
}

func kiloReadLifecyclePolicy(ctx context.Context, q kiloQuerier, id int64) (kiloLifecyclePolicy, error) {
	var policy kiloLifecyclePolicy
	err := kiloScan(ctx, q, `SELECT identity_mode,identity_source_account_id,identity_status FROM pk_kilo_prepared_accounts WHERE id=$1`, []any{id}, &policy.mode, &policy.origin, &policy.status)
	return policy, err
}

func kiloRequireLifecycleSource(policy kiloLifecyclePolicy, sourceID *int64) error {
	if policy.status == "retired" {
		return kiloBad("KILO_IDENTITY_RETIRED", "此Kilo已随原GPT来源结束生命周期，不能重新启用或换绑")
	}
	if policy.status == "banned" {
		return kiloBad("KILO_IDENTITY_BANNED", "此Kilo身份已被上游禁用")
	}
	if sourceID == nil {
		return nil
	}
	switch policy.mode {
	case KiloIdentityModeIndependent:
		return nil
	case KiloIdentityModeCoupled:
		if policy.origin.Valid && policy.origin.Int64 == *sourceID {
			return nil
		}
		return kiloBad("KILO_IDENTITY_COUPLED", "此Kilo与原GPT共用生命周期，只能恢复原来源，不能换绑其他GPT")
	default:
		return kiloBad("KILO_IDENTITY_UNVERIFIED", "请先核验Kilo登录身份类型，再绑定GPT订阅")
	}
}

// Retirement is a local scheduling decision, not a claim that Kilo revoked the
// upstream account. Key and login-proxy ownership remain for audit/recovery;
// the identity can never be advertised as a reusable subscription slot.
func (s *KiloService) retireCoupledKiloIdentity(ctx context.Context, p kiloPoolBinding, reason string) error {
	operation, unlock, err := s.lockPreparedOperation(ctx, p.ID)
	if err != nil {
		return err
	}
	defer unlock()
	tx, err := s.client.Tx(operation)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(operation, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var gatewayID, origin, revision int64
	err = kiloScan(operation, tx.Client(), `SELECT account_id,identity_source_account_id,binding_revision FROM pk_kilo_prepared_accounts WHERE id=$1 AND identity_mode='gpt_coupled' AND identity_status='active' AND identity_source_account_id IS NOT NULL FOR UPDATE`, []any{p.ID}, &gatewayID, &origin, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if gatewayID != p.GatewayID || revision != p.Revision || origin != p.SourceID {
		return nil
	}
	var busy bool
	if err = kiloScan(operation, tx.Client(), `SELECT EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE prepared_id=$1 AND status IN ('queued','running','awaiting_authorization'))`, []any{p.ID}, &busy); err != nil {
		return err
	}
	if busy {
		return nil // The owned operation must finish before retirement publishes.
	}
	txctx := dbent.NewTxContext(operation, tx)
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return err
	}
	source, err := s.accounts.GetByID(txctx, origin)
	if err != nil {
		return err
	}
	if p.ExpectedTokenDigest != "" && (p.ExpectedTokenDigest != OpenAIAccessTokenDigest(source.GetOpenAIAccessToken()) || p.ExpectedRecoveryGeneration != OpenAIAuthRecoveryGeneration(source)) {
		return nil
	}
	if source.IsActive() && !OpenAIAuthRecoveryPending(source) {
		return nil
	}
	if reason == "workspace_deactivated" && !strings.HasPrefix(source.ErrorMessage, "Workspace deactivated (402):") {
		return nil
	}
	gateway, err := s.accounts.GetByID(txctx, gatewayID)
	if err != nil {
		return err
	}
	if !gateway.IsKiloGatewayAccount() || int64(ParseExtraInt(gateway.Extra["kilo_prepared_id"])) != p.ID {
		return kiloConflict("Kilo身份归属已改变，未执行生命周期退役")
	}
	source.Status, source.Schedulable = StatusDisabled, false
	if source.Extra == nil {
		source.Extra = map[string]any{}
	}
	source.Extra["kilo_lifecycle_ended"] = true
	if err = s.accounts.Update(txctx, source); err != nil {
		return err
	}
	gateway.Status, gateway.Schedulable = StatusDisabled, false
	gateway.ErrorMessage = "Kilo lifecycle ended with its original GPT source"
	gateway.Extra["kilo_identity_mode"] = KiloIdentityModeCoupled
	gateway.Extra["kilo_identity_source_account_id"] = origin
	clearKiloModelScope(gateway)
	if err = s.accounts.Update(txctx, gateway); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(operation, `UPDATE pk_kilo_prepared_accounts SET identity_status='retired',status='disabled',binding_state='blocked',binding_resume=false,binding_error='source_lifecycle_ended',last_error='kilo_release:source_lifecycle_ended',updated_at=NOW() WHERE id=$1 AND identity_mode='gpt_coupled' AND identity_source_account_id=$2 AND binding_revision=$3`, p.ID, origin, revision); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.releaseRequestExitsOwned(operation, p.ID, false)
}
