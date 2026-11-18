package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	KiloIdentityModeUnknown     = "unknown"
	KiloIdentityModeCoupled     = "gpt_coupled"
	KiloIdentityModeIndependent = "independent"

	KiloIdentityModeExtraKey   = "kilo_identity_mode"
	KiloIdentitySourceExtraKey = "kilo_identity_source_account_id"
)

// KiloIdentityPolicy describes login independence, separately from upstream
// identity health. The original source survives later subscription replacement.
type KiloIdentityPolicy struct {
	PreparedID              int64      `json:"prepared_id"`
	IdentityMode            string     `json:"identity_mode"`
	IdentitySourceAccountID *int64     `json:"identity_source_account_id"`
	IdentityCheckedAt       *time.Time `json:"identity_checked_at"`
	IdentityStatus          string     `json:"identity_status"`
	Reason                  string     `json:"reason"`
}

// ClassifyKiloIdentityProviders uses only official login methods. Equal email
// addresses do not make an independent login depend on the GPT subscription.
func ClassifyKiloIdentityProviders(providers []KiloAuthProvider) string {
	onlyOpenAI := len(providers) > 0
	for _, provider := range providers {
		if kiloIndependentAuthProvider(provider.Provider) {
			return KiloIdentityModeIndependent
		}
		if provider.Provider != "openai" {
			onlyOpenAI = false
		}
	}
	if onlyOpenAI {
		return KiloIdentityModeCoupled
	}
	return KiloIdentityModeUnknown
}

func kiloIndependentAuthProvider(provider string) bool {
	switch provider {
	case "email", "google", "github", "gitlab", "apple", "discord", "linkedin", "workos", "anaconda", "claude":
		return true
	default:
		return false
	}
}

const kiloIdentitySnapshotSQL = `SELECT p.account_id,p.login_proxy_id,p.email,p.remote_user_id,p.credential_encrypted,p.binding_revision,p.identity_mode,p.identity_source_account_id,p.identity_checked_at,p.identity_status,EXISTS(SELECT 1 FROM pk_kilo_jobs j WHERE j.prepared_id=p.id AND j.status IN ('queued','running','awaiting_authorization')) FROM pk_kilo_prepared_accounts p WHERE p.id=$1`

// A succeeded, non-reused onboarding job identifies who created the login.
// Never infer this from the currently bound source, which may be a replacement.
const kiloIdentityOriginSQL = `SELECT CASE WHEN COUNT(DISTINCT j.source_account_id)=1 THEN MIN(j.source_account_id) END FROM pk_kilo_jobs j JOIN accounts a ON a.id=j.source_account_id WHERE j.prepared_id=$1 AND j.job_kind='onboard_source' AND j.status='succeeded' AND NOT j.reused_identity AND j.source_account_id IS NOT NULL AND LOWER(COALESCE(NULLIF(BTRIM(a.credentials->>'email'),''),NULLIF(BTRIM(a.credentials->>'chatgpt_email'),''),BTRIM(a.name)))=LOWER(BTRIM($2))`

type kiloIdentitySnapshot struct {
	GatewayID, LoginProxyID *int64
	Email, RemoteID, Cipher string
	Revision                int64
	Policy                  KiloIdentityPolicy
	Busy                    bool
}

func (p *kiloIdentitySnapshot) destinations() []any {
	return []any{&p.GatewayID, &p.LoginProxyID, &p.Email, &p.RemoteID, &p.Cipher, &p.Revision,
		&p.Policy.IdentityMode, &p.Policy.IdentitySourceAccountID, &p.Policy.IdentityCheckedAt, &p.Policy.IdentityStatus, &p.Busy}
}

// RefreshIdentityPolicy reads the original Key through its pinned login route.
// It updates classification only; it never logs in, unlinks a provider, changes
// the current subscription source, or clears a local retirement/upstream ban.
func (s *KiloService) RefreshIdentityPolicy(ctx context.Context, id int64) (*KiloIdentityPolicy, error) {
	if id <= 0 {
		return nil, kiloBad("KILO_PREPARED_INVALID", "Kilo身份编号无效")
	}
	remote, ok := s.remote.(KiloIdentityRemote)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("KILO_IDENTITY_UNAVAILABLE", "Kilo身份分类接口暂不可用")
	}
	operation, unlock, err := s.lockPreparedOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ctx = operation

	var snapshot kiloIdentitySnapshot
	if err = s.db.QueryRowContext(ctx, kiloIdentitySnapshotSQL, id).Scan(snapshot.destinations()...); errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("KILO_PREPARED_NOT_FOUND", "Kilo身份不存在")
	} else if err != nil {
		return nil, err
	}
	if snapshot.Busy {
		return nil, kiloConflict("Kilo身份仍有进行中的任务，请完成后刷新")
	}
	if snapshot.GatewayID == nil || *snapshot.GatewayID <= 0 || snapshot.LoginProxyID == nil || *snapshot.LoginProxyID <= 0 {
		return nil, kiloBad("KILO_LOGIN_PROXY_REQUIRED", "请先为Kilo身份保存网关与固定登录代理")
	}
	if strings.TrimSpace(snapshot.RemoteID) == "" || strings.TrimSpace(snapshot.Email) == "" {
		return nil, kiloConflict("Kilo原身份尚未核验，不能刷新登录分类")
	}
	key, err := s.encryptor.Decrypt(snapshot.Cipher)
	if err != nil {
		return nil, errors.New("Kilo凭据解密失败")
	}
	route, err := s.acquireLoginRoute(ctx, *snapshot.GatewayID)
	if err != nil {
		return nil, err
	}
	defer route.Close()
	if route.ProxyID != *snapshot.LoginProxyID {
		return nil, kiloConflict("Kilo固定登录代理已改变")
	}
	profile, err := s.remote.Profile(route.Context, key, route.URL)
	if err != nil {
		return nil, err
	}
	if profile == nil || profile.ID != snapshot.RemoteID || !strings.EqualFold(strings.TrimSpace(profile.Email), strings.TrimSpace(snapshot.Email)) {
		return nil, kiloConflict("Kilo官方身份与原身份不一致，未更新分类")
	}
	providers, err := remote.AuthProviders(route.Context, key, route.URL)
	if err != nil {
		return nil, err
	}
	mode := ClassifyKiloIdentityProviders(providers)

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return nil, err
	}
	var fresh kiloIdentitySnapshot
	if err = kiloScan(ctx, tx.Client(), kiloIdentitySnapshotSQL+" FOR UPDATE", []any{id}, fresh.destinations()...); errors.Is(err, sql.ErrNoRows) {
		return nil, kiloConflict("Kilo身份已改变，未保存旧分类结果")
	} else if err != nil {
		return nil, err
	}
	if fresh.Busy {
		return nil, kiloConflict("Kilo身份已有新的进行中任务，未保存旧分类结果")
	}
	if fresh.GatewayID == nil || *fresh.GatewayID != *snapshot.GatewayID || fresh.LoginProxyID == nil || *fresh.LoginProxyID != route.ProxyID ||
		fresh.Cipher != snapshot.Cipher || fresh.RemoteID != snapshot.RemoteID || fresh.Email != snapshot.Email || fresh.Revision != snapshot.Revision {
		return nil, kiloConflict("Kilo身份或绑定代次在核验期间已改变")
	}
	if fresh.Policy.IdentitySourceAccountID == nil && mode == KiloIdentityModeCoupled {
		if err = kiloScan(ctx, tx.Client(), kiloIdentityOriginSQL, []any{id, snapshot.Email}, &fresh.Policy.IdentitySourceAccountID); err != nil {
			return nil, err
		}
	}
	txctx := dbent.NewTxContext(ctx, tx)
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return nil, errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return nil, err
	}
	gateway, err := s.accounts.GetByID(txctx, *fresh.GatewayID)
	if err != nil {
		return nil, err
	}
	if !gateway.IsKiloGatewayAccount() || gateway.GetCredential("api_key") != key ||
		int64(ParseExtraInt(gateway.Extra["kilo_prepared_id"])) != id || int64(ParseExtraInt(gateway.Extra[KiloLoginProxyExtraKey])) != route.ProxyID {
		return nil, kiloConflict("Kilo网关凭据或固定代理在核验期间已改变")
	}
	if err = kiloScan(ctx, tx.Client(), `UPDATE pk_kilo_prepared_accounts SET identity_mode=$2,identity_source_account_id=COALESCE(identity_source_account_id,$3::bigint),identity_checked_at=NOW(),updated_at=NOW() WHERE id=$1 RETURNING identity_source_account_id,identity_checked_at,identity_status`, []any{id, mode, fresh.Policy.IdentitySourceAccountID}, &fresh.Policy.IdentitySourceAccountID, &fresh.Policy.IdentityCheckedAt, &fresh.Policy.IdentityStatus); err != nil {
		return nil, err
	}
	gateway.Extra[KiloIdentityModeExtraKey] = mode
	if fresh.Policy.IdentitySourceAccountID == nil {
		delete(gateway.Extra, KiloIdentitySourceExtraKey)
	} else {
		gateway.Extra[KiloIdentitySourceExtraKey] = *fresh.Policy.IdentitySourceAccountID
	}
	if err = s.accounts.Update(txctx, gateway); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	fresh.Policy.PreparedID, fresh.Policy.IdentityMode = id, mode
	switch mode {
	case KiloIdentityModeIndependent:
		fresh.Policy.Reason = "KILO_IDENTITY_INDEPENDENT"
	case KiloIdentityModeCoupled:
		fresh.Policy.Reason = "KILO_IDENTITY_COUPLED"
	default:
		fresh.Policy.Reason = "KILO_IDENTITY_UNKNOWN"
	}
	return &fresh.Policy, nil
}
