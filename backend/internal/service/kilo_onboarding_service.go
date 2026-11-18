package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// Called by the existing provider before Start; tests can inject a deterministic
// executor without changing the historical NewKiloService constructor.
func (s *KiloService) SetOnboardingDependencies(reauth *OpenAIOAuthReauthService, executor KiloOnboardingExecutor) {
	s.reauth = reauth
	if executor != nil {
		s.executor = executor
	}
}

const kiloWorkspaceModeExtraKey = "kilo_workspace_mode"

func kiloEffectiveWorkspaceMode(mode string) string {
	if mode == "" {
		return "auto"
	}
	return mode
}

func normalizeKiloOnboarding(input *KiloOnboardingBatchInput) error {
	if input.BindingStrategy == "" {
		input.BindingStrategy = KiloBindingNewIdentity
	}
	if input.BindingStrategy != KiloBindingNewIdentity && input.BindingStrategy != KiloBindingPreferIdle {
		return kiloBad("KILO_BINDING_STRATEGY", "Kilo绑定策略无效")
	}
	if err := kiloID(input.RequestID); err != nil {
		return err
	}
	if len(input.Items) < 1 || len(input.Items) > 100 {
		return kiloBad("KILO_ONBOARDING_LIMIT", "每批自动接入1至100个来源账号")
	}
	if err := kiloUniqueIDs(input.GroupIDs, 1000); err != nil {
		return err
	}
	if len(input.GroupIDs) == 0 {
		return kiloBad("KILO_GROUP_INVALID", "请选择目标分组")
	}
	sort.Slice(input.GroupIDs, func(i, j int) bool { return input.GroupIDs[i] < input.GroupIDs[j] })
	if err := normalizeKiloRouting(&input.Routing); err != nil {
		return err
	}
	if !validKiloRequestProxyPool(input.Routing.ProxyGroupID) {
		return kiloBad("KILO_PROXY_GROUP_REQUIRED", "请选择请求代理范围")
	}
	seen := map[int64]bool{}
	for i := range input.Items {
		item := &input.Items[i]
		item.Name = strings.TrimSpace(item.Name)
		item.OrganizationID = strings.TrimSpace(item.OrganizationID)
		item.WorkspaceMode = strings.ToLower(strings.TrimSpace(item.WorkspaceMode))
		switch item.WorkspaceMode {
		case "", "auto":
			// Preserve legacy request hashes while treating omission as automatic.
			item.WorkspaceMode = ""
		case "personal", "business":
		default:
			return kiloBad("KILO_WORKSPACE_MODE_INVALID", "GPT工作区模式须为auto、personal或business")
		}
		if item.SourceAccountID <= 0 || seen[item.SourceAccountID] || item.LoginProxyID < 0 || (item.LoginProxyID == 0 && input.BindingStrategy == KiloBindingNewIdentity && item.PreparedID == 0) || item.PreparedID < 0 || item.Name == "" || len([]rune(item.Name)) > 256 || !kiloValidOrganization(item.OrganizationID) {
			return kiloBad("KILO_ONBOARDING_INPUT", "来源、名称、固定登录代理或目标空间无效或重复")
		}
		seen[item.SourceAccountID] = true
		models := map[string]bool{}
		for _, m := range item.VerifiedModelIDs {
			m = strings.TrimSpace(m)
			if m == "" || len(m) > 256 || strings.ContainsAny(m, "*? \t\r\n") || !strings.HasPrefix(m, "openai/") {
				return kiloBad("KILO_MODEL_SCOPE", "请填写已核实的精确openai/订阅模型编号，不能使用通配符")
			}
			models[m] = true
		}
		// An empty selection means discover the authenticated Kilo BYOK catalog
		// after this source completes authorization; it never grants all models.
		item.VerifiedModelIDs = make([]string, 0, len(models))
		for m := range models {
			item.VerifiedModelIDs = append(item.VerifiedModelIDs, m)
		}
		sort.Strings(item.VerifiedModelIDs)
	}
	return nil
}

func (s *KiloService) SubmitOnboardingBatch(ctx context.Context, input KiloOnboardingBatchInput) (*KiloOnboardingBatchResult, error) {
	if err := normalizeKiloOnboarding(&input); err != nil {
		return nil, err
	}
	if !s.encryptionReady() {
		return nil, kiloBad("KILO_ENCRYPTION", "请先配置服务器凭据加密密钥")
	}
	if s.reauth == nil {
		return nil, infraerrors.ServiceUnavailable("EXECUTOR_UNAVAILABLE", "请先配置原2FA登录材料服务")
	}
	// Ingress persists work; a maintenance node owns the executor. Requiring
	// a local runtime here would make split-role deployments unable to queue.
	// The worker checks authenticated capabilities/capacity before any login.
	if err := s.admin.ValidateAccountGroupBindings(ctx, input.GroupIDs); err != nil {
		return nil, err
	}
	for _, id := range input.GroupIDs {
		g, err := s.groups.GetByIDLite(ctx, id)
		if err != nil {
			return nil, err
		}
		if g.Platform != PlatformOpenAI || g.Status != StatusActive {
			return nil, kiloBad("KILO_GROUP_INVALID", "目标分组须为启用的OpenAI分组")
		}
	}
	var oldID, oldHash string
	err := s.db.QueryRowContext(ctx, `SELECT id::text,payload_hash FROM pk_kilo_batches WHERE request_id=$1`, input.RequestID).Scan(&oldID, &oldHash)
	if err == nil {
		if oldHash != kiloHash(input) {
			return nil, kiloConflict("同一请求编号不能提交不同的自动接入内容")
		}
		return s.onboardingReceipt(ctx, s.db, oldID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err = s.preflightOnboardingProxies(ctx, input); err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := s.submitKiloOnboardingTx(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// Imports and existing-account submissions reserve the slot in the same
// transaction as their quota-only source, credentials and queue record.
func (s *KiloService) submitKiloOnboardingTx(ctx context.Context, tx *dbent.Tx, input KiloOnboardingBatchInput) (*KiloOnboardingBatchResult, error) {
	raw, _ := json.Marshal(input)
	hash := kiloHash(input)
	var err error
	txctx := dbent.NewTxContext(ctx, tx)
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return nil, err
	}
	var oldID, oldHash string
	err = kiloScan(ctx, tx.Client(), `SELECT id::text,payload_hash FROM pk_kilo_batches WHERE request_id=$1`, []any{input.RequestID}, &oldID, &oldHash)
	if err == nil {
		if oldHash != hash {
			return nil, kiloConflict("同一请求编号不能提交不同的自动接入内容")
		}
		view, readErr := s.onboardingReceipt(ctx, tx.Client(), oldID)
		return view, readErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	batchID := uuid.NewString()
	if _, err = tx.Client().ExecContext(ctx, `INSERT INTO pk_kilo_batches(id,request_id,payload_hash,options) VALUES($1,$2,$3,$4::jsonb)`, batchID, input.RequestID, hash, string(raw)); err != nil {
		return nil, err
	}
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return nil, errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return nil, err
	}
	result := &KiloOnboardingBatchResult{BatchID: batchID, Items: []KiloOnboardingItemResult{}}
	for i, item := range input.Items {
		source, readErr := s.accounts.GetByID(txctx, item.SourceAccountID)
		if readErr != nil {
			return nil, readErr
		}
		if !source.IsOpenAIOAuth() || source.ParentAccountID != nil || kiloSourceEmail(source) == "" {
			return nil, kiloBad("KILO_SOURCE_INVALID", "来源须为有邮箱的OpenAI OAuth主账号")
		}
		if ParseExtraInt(source.Extra["kilo_source_prepared_id"]) > 0 || ParseExtraInt(source.Extra["kilo_source_gateway_id"]) > 0 {
			return nil, kiloConflict("来源已绑定Kilo网关，请从现有关系管理")
		}
		var used bool
		if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE source_account_id=$1 AND status IN ('queued','running','awaiting_authorization'))`, []any{source.ID}, &used); err != nil {
			return nil, err
		}
		if used {
			return nil, kiloConflict("来源已有活跃Kilo任务")
		}
		idle, readErr := s.claimIdleKiloIdentity(ctx, tx, &item, input.BindingStrategy)
		if readErr != nil {
			return nil, readErr
		}
		proxy, readErr := s.proxies.GetByID(ctx, item.LoginProxyID)
		if readErr != nil {
			return nil, readErr
		}
		if !usableAssignedAuthProxy(proxy) {
			return nil, kiloBad("LOGIN_PROXY_CONFLICT", "固定登录代理不可用")
		}
		if source.ProxyID != nil && *source.ProxyID != item.LoginProxyID {
			return nil, kiloBad("LOGIN_PROXY_CONFLICT", "来源已指定的登录代理与本次选择不一致")
		}
		for _, key := range []string{assignedAuthProxyExtraKey, "kilo_onboarding_proxy_id"} {
			if id := int64(ParseExtraInt(source.Extra[key])); id > 0 && id != item.LoginProxyID {
				return nil, kiloBad("LOGIN_PROXY_CONFLICT", "来源已有固定登录身份，不能切换出口")
			}
		}
		if source.ProxyID == nil && ParseExtraInt(source.Extra[assignedAuthProxyExtraKey]) == 0 && len(source.ProxyBindings) > 0 {
			// Match the ordinary re-login resolver's deterministic first pin.
			// An onboarding request cannot silently choose a different identity.
			var first int64
			for _, binding := range source.ProxyBindings {
				if binding.Proxy != nil && usableAssignedAuthProxy(binding.Proxy) && (first == 0 || binding.ProxyID < first) {
					first = binding.ProxyID
				}
			}
			if first == 0 || first != item.LoginProxyID {
				return nil, kiloBad("LOGIN_PROXY_CONFLICT", "来源已有代理绑定，请沿用常规登录的固定出口")
			}
		}
		// The reader only decrypts stored login material. It does not claim or
		// create an OpenAI re-login task while this transaction holds a lock.
		material, readErr := s.kiloMaterialVersionTx(ctx, tx, source.ID)
		if readErr != nil {
			return nil, readErr
		}
		if !strings.EqualFold(material.Email, kiloSourceEmail(source)) {
			return nil, kiloBad("IDENTITY_MISMATCH", "登录配置邮箱与来源账号不一致")
		}
		options := KiloBatchInput{RequestID: input.RequestID, OnboardingName: item.Name, WorkspaceMode: item.WorkspaceMode, SourceAccountIDs: []int64{source.ID}, GroupIDs: input.GroupIDs, ProxyGroupID: input.Routing.ProxyGroupID, ProxiesPerAccount: input.Routing.ProxiesPerAccount, ProxyConcurrency: input.Routing.ProxyConcurrency, AccountConcurrency: input.Routing.AccountConcurrency, Priority: input.Routing.Priority, AutoReleaseStatuses: input.Routing.AutoReleaseStatuses}
		opts, _ := json.Marshal(options)
		models, _ := json.Marshal(item.VerifiedModelIDs)
		var jobID int64
		var preparedID, accountID any
		var bindingRevision int64
		if idle != nil {
			preparedID, accountID, bindingRevision = idle.ID, idle.AccountID, idle.Revision
		}
		if err = kiloScan(ctx, tx.Client(), `INSERT INTO pk_kilo_jobs(batch_id,job_kind,source_account_id,source_identity,stage,options,login_proxy_id,organization_id,verified_model_ids,activate_when_ready,material_version,prepared_id,account_id,reused_identity,binding_revision) VALUES($1,'onboard_source',$2,$3,'quota_oauth',$4::jsonb,$5,$6,$7::jsonb,$8,$9,$10,$11,$12,$13) RETURNING id`, []any{batchID, source.ID, kiloSourceIdentity(source), string(opts), item.LoginProxyID, item.OrganizationID, string(models), input.ActivateWhenReady, material.Version, preparedID, accountID, idle != nil, bindingRevision}, &jobID); err != nil {
			return nil, err
		}
		if idle != nil {
			if err = s.reserveKiloPoolBinding(ctx, tx, idle, source, jobID); err != nil {
				return nil, err
			}
		}
		if source.Extra == nil {
			source.Extra = map[string]any{}
		}
		source.Extra["kilo_quota_only"] = true
		source.Extra[kiloWorkspaceModeExtraKey] = kiloEffectiveWorkspaceMode(item.WorkspaceMode)
		source.Extra["kilo_onboarding_job_id"] = jobID
		source.Extra["kilo_onboarding_proxy_id"] = item.LoginProxyID
		if err = s.accounts.Update(txctx, source); err != nil {
			return nil, err
		}
		result.Items = append(result.Items, KiloOnboardingItemResult{Line: i + 1, SourceAccountID: source.ID, JobID: jobID, Status: "queued"})
	}
	return result, nil
}

func (s *KiloService) onboardingReceipt(ctx context.Context, q kiloQuerier, batchID string) (*KiloOnboardingBatchResult, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,source_account_id,status FROM pk_kilo_jobs WHERE batch_id=$1 ORDER BY id`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &KiloOnboardingBatchResult{BatchID: batchID, Items: []KiloOnboardingItemResult{}}
	for rows.Next() {
		item := KiloOnboardingItemResult{Line: len(out.Items) + 1}
		if err = rows.Scan(&item.JobID, &item.SourceAccountID, &item.Status); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}

func (s *KiloService) OnboardingBatch(ctx context.Context, id string) (*KiloOnboardingBatchView, error) {
	if err := kiloID(id); err != nil {
		return nil, err
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_kilo_batches WHERE id=$1)`, id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, infraerrors.New(http.StatusNotFound, "KILO_BATCH_NOT_FOUND", "接入批次不存在")
	}
	jobs, err := s.kiloJobs(ctx, "WHERE j.batch_id=$1 ORDER BY j.id LIMIT 1000", id)
	return &KiloOnboardingBatchView{BatchID: id, Items: jobs}, err
}
