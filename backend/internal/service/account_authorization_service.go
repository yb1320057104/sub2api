package service

import (
	"context"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

const authorizationLockID int64 = 70360001
const authorizationWorkerLockID int64 = 70360002
const authorizationInspectionCron = "*/3 * * * *"
const authorizationReauthCron = "*/10 * * * *"
const authorizationWorkbenchSettingKey = "pk_authorization_workbench"

type AuthorizationConfig struct {
	WorkbenchRevision                 string `json:"workbench_revision"`
	WorkbenchAuthorizationConcurrency int    `json:"workbench_authorization_concurrency"`
	WorkbenchAccountConcurrency       int    `json:"workbench_account_concurrency"`
	WorkbenchAccountPriority          int    `json:"workbench_account_priority"`

	AccountProxyMode    string `json:"account_proxy_mode"`
	MaxAccountsPerProxy int    `json:"max_accounts_per_proxy"`

	IntelligenceModels       []RadarModel     `json:"intelligence_models"`
	IntelligenceGate         bool             `json:"intelligence_gate"`
	IntelligenceGroupRules   []RadarGroupRule `json:"intelligence_group_rules"`
	PassGroupIDs             []int64          `json:"pass_group_ids"`
	FailGroupIDs             []int64          `json:"fail_group_ids"`
	IntelligenceModel        string           `json:"intelligence_model"`
	IntelligenceEffort       string           `json:"intelligence_effort"`
	InspectionEnabled        bool             `json:"inspection_enabled"`
	ReauthEnabled            bool             `json:"reauth_enabled"`
	InspectionConcurrency    int              `json:"inspection_concurrency"`
	AuthorizationConcurrency int              `json:"authorization_concurrency"`
	AccountConcurrency       int              `json:"account_concurrency"`
	AccountPriority          int              `json:"account_priority"`
	GroupIDs                 []int64          `json:"group_ids"`
	ReauthStatusCodes        []int            `json:"reauth_status_codes"`
	Revision                 int64            `json:"revision"`
	NextInspectionAt         *time.Time       `json:"next_inspection_at"`
	NextReauthAt             *time.Time       `json:"next_reauth_at"`
	InspectionCron           string           `json:"inspection_cron"`
	ReauthCron               string           `json:"reauth_cron"`
	BrowserAvailable         bool             `json:"browser_available"`
	EncryptionKeyConfigured  bool             `json:"encryption_key_configured"`
}

type AuthorizationSecret struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TwoFA    string `json:"twofa"`
}

type AuthorizationSubmit struct {
	WorkbenchRevision         string `json:"workbench_revision,omitempty"`
	IntelligenceSourceGroupID int64  `json:"intelligence_source_group_id,omitempty"`
	AccountProxyMode          string `json:"account_proxy_mode"`
	MaxAccountsPerProxy       int    `json:"max_accounts_per_proxy"`

	IntelligenceGeneration   string       `json:"intelligence_generation,omitempty"`
	IntelligenceModels       []RadarModel `json:"intelligence_models,omitempty"`
	PassGroupIDs             []int64      `json:"pass_group_ids"`
	FailGroupIDs             []int64      `json:"fail_group_ids"`
	IntelligenceGate         bool         `json:"intelligence_gate"`
	IntelligenceModel        string       `json:"intelligence_model"`
	IntelligenceEffort       string       `json:"intelligence_effort"`
	Accounts                 string       `json:"accounts"`
	AuthorizationConcurrency int          `json:"authorization_concurrency"`
	AccountConcurrency       int          `json:"account_concurrency"`
	AccountPriority          int          `json:"account_priority"`
	GroupIDs                 []int64      `json:"group_ids"`
}

type AccountAuthorizationService struct {
	db                        *sql.DB
	client                    *dbent.Client
	accounts                  AccountRepository
	admin                     AdminService
	oauth                     *OpenAIOAuthService
	tests                     *AccountTestService
	encryptor                 SecretEncryptor
	invalidator               TokenCacheInvalidator
	cfg                       *config.Config
	mu                        sync.Mutex
	cron                      *cron.Cron
	ctx                       context.Context
	cancel                    context.CancelFunc
	started, stopped          bool
	coordinating, authorizing bool
	workers                   sync.WaitGroup
}

func NewAccountAuthorizationService(db *sql.DB, client *dbent.Client, accounts AccountRepository, admin AdminService, oauth *OpenAIOAuthService, tests *AccountTestService, encryptor SecretEncryptor, invalidator TokenCacheInvalidator, cfg *config.Config) *AccountAuthorizationService {
	ctx, cancel := context.WithCancel(context.Background())
	// Inside the container this process is PID 1, so adopted browser helper
	// processes become its children; start collecting them with the service
	// instead of waiting for the first authorization.
	authorizationStartReaper()
	return &AccountAuthorizationService{db: db, client: client, accounts: accounts, admin: admin, oauth: oauth, tests: tests, encryptor: encryptor, invalidator: invalidator, cfg: cfg, ctx: ctx, cancel: cancel}
}

// Authorization uses the same persisted settings as the candy settings dialog.
// One admission round snapshots every enabled model. A single query keeps the
// models and target consistent while settings are being saved.
func authorizationCandyConfigTx(ctx context.Context, tx *sql.Tx) (*RadarConfig, error) {
	cfg := &RadarConfig{Models: make([]RadarModel, 0)}
	var modelsJSON, rulesJSON []byte
	err := tx.QueryRowContext(ctx, `SELECT c.enabled,c.interval_minutes,c.target_group_id,c.generation,
		COALESCE((SELECT jsonb_agg(jsonb_build_object('model',model,'effort',effort,'enabled',enabled) ORDER BY sort_order)
			FROM pk_radar_account_models WHERE config_id=c.id AND enabled=true),'[]'::jsonb),
		COALESCE((SELECT value::jsonb FROM settings WHERE key=$1),'[]'::jsonb)
		FROM pk_radar_account_config c
		WHERE c.id=1`, radarGroupRulesSettingKey).Scan(&cfg.Enabled, &cfg.IntervalMinutes, &cfg.TargetGroupID, &cfg.Generation, &modelsJSON, &rulesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		// Before radar initialization, expose an unconfigured destination. Never
		// revive a stale legacy target after the current target has been cleared.
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(modelsJSON, &cfg.Models); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(rulesJSON, &cfg.GroupRules); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *AccountAuthorizationService) GetConfig(ctx context.Context) (*AuthorizationConfig, error) {
	c := &AuthorizationConfig{InspectionCron: authorizationInspectionCron, ReauthCron: authorizationReauthCron, BrowserAvailable: authorizationBrowserPath() != "", EncryptionKeyConfigured: s.cfg != nil && s.cfg.Totp.EncryptionKeyConfigured}
	var raw, codes []byte
	err := s.db.QueryRowContext(ctx, `SELECT inspection_enabled,reauth_enabled,inspection_concurrency,authorization_concurrency,account_concurrency,account_priority,group_ids,reauth_status_codes,revision,next_inspection_at,next_reauth_at FROM pk_authorization_config WHERE id=1`).Scan(&c.InspectionEnabled, &c.ReauthEnabled, &c.InspectionConcurrency, &c.AuthorizationConcurrency, &c.AccountConcurrency, &c.AccountPriority, &raw, &codes, &c.Revision, &c.NextInspectionAt, &c.NextReauthAt)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &c.GroupIDs); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(codes, &c.ReauthStatusCodes); err != nil {
		return nil, err
	}
	c.PassGroupIDs, c.FailGroupIDs = []int64{}, []int64{}
	// Workbench defaults are independent from the inspection/reauthorization
	// configuration. Saved values override them, including an explicit priority 0.
	workbench := authorizationWorkbenchDefaults()
	var saved string
	if err = s.db.QueryRowContext(ctx, `SELECT value,md5(value) FROM settings WHERE key=$1`, authorizationWorkbenchSettingKey).Scan(&saved, &c.WorkbenchRevision); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if saved != "" {
		if err = json.Unmarshal([]byte(saved), &workbench); err != nil {
			return nil, err
		}
		c.PassGroupIDs = workbench.PassGroupIDs
	}
	if err = normalizeAuthorizationProxyOptions(&workbench); err != nil {
		return nil, err
	}
	c.WorkbenchAccountConcurrency, c.WorkbenchAccountPriority = workbench.AccountConcurrency, workbench.AccountPriority
	c.WorkbenchAuthorizationConcurrency = workbench.AuthorizationConcurrency
	c.IntelligenceGate = workbench.IntelligenceGate
	c.AccountProxyMode, c.MaxAccountsPerProxy = workbench.AccountProxyMode, workbench.MaxAccountsPerProxy
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	candy, err := authorizationCandyConfigTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	c.IntelligenceModels = candy.Models
	c.IntelligenceGroupRules = candy.GroupRules
	if len(candy.Models) > 0 {
		// Keep the old scalar fields for clients predating multi-model admission.
		c.IntelligenceModel, c.IntelligenceEffort = candy.Models[0].Model, candy.Models[0].Effort
	}
	if candy.TargetGroupID != nil {
		c.FailGroupIDs = []int64{*candy.TargetGroupID}
	}
	return c, nil
}

func validateAuthorizationOptions(concurrency, accountConcurrency, priority int, groups []int64) error {
	if concurrency < 1 || concurrency > 50 || accountConcurrency < 1 || accountConcurrency > 1000 || priority < 0 || priority > 1000 || len(groups) > 100 {
		return infraerrors.BadRequest("AUTHORIZATION_OPTIONS", "并发、优先级或分组参数超出范围")
	}
	seen := map[int64]bool{}
	for _, id := range groups {
		if id < 1 || seen[id] {
			return infraerrors.BadRequest("AUTHORIZATION_GROUPS", "分组无效或重复")
		}
		seen[id] = true
	}
	return nil
}

func (s *AccountAuthorizationService) SaveConfig(ctx context.Context, c *AuthorizationConfig) error {
	if c.ReauthEnabled {
		if err := s.requireAuthorizationEncryption(); err != nil {
			return err
		}
	}
	if err := validateAuthorizationOptions(c.AuthorizationConcurrency, c.AccountConcurrency, c.AccountPriority, c.GroupIDs); err != nil {
		return err
	}
	if c.InspectionConcurrency < 1 || c.InspectionConcurrency > 50 {
		return infraerrors.BadRequest("AUTHORIZATION_CONCURRENCY", "巡检并发必须为1至50")
	}
	if len(c.ReauthStatusCodes) == 0 || len(c.ReauthStatusCodes) > 200 {
		return infraerrors.BadRequest("AUTHORIZATION_STATUS_CODES", "至少配置一个400至599的HTTP错误码")
	}
	codesSeen := map[int]bool{}
	for _, code := range c.ReauthStatusCodes {
		if code < 400 || code > 599 || codesSeen[code] {
			return infraerrors.BadRequest("AUTHORIZATION_STATUS_CODES", "状态码必须为400至599且不能重复")
		}
		codesSeen[code] = true
	}
	codes, err := json.Marshal(c.ReauthStatusCodes)
	if err != nil {
		return err
	}
	if err := s.admin.ValidateAccountGroupBindings(ctx, c.GroupIDs); err != nil {
		return err
	}
	if c.GroupIDs == nil {
		c.GroupIDs = []int64{}
	}
	raw, err := json.Marshal(c.GroupIDs)
	if err != nil {
		return err
	}
	now := time.Now()
	nextProbe := s.next(authorizationInspectionCron, now)
	nextAuth := s.next(authorizationReauthCron, now)
	result, err := s.db.ExecContext(ctx, `UPDATE pk_authorization_config SET inspection_enabled=$1,reauth_enabled=$2,inspection_concurrency=$3,authorization_concurrency=$4,account_concurrency=$5,account_priority=$6,group_ids=$7,reauth_status_codes=$11,revision=revision+1,next_inspection_at=CASE WHEN $1 THEN COALESCE(next_inspection_at,$9) ELSE NULL END,next_reauth_at=CASE WHEN $2 THEN COALESCE(next_reauth_at,$10) ELSE NULL END,updated_at=NOW() WHERE id=1 AND revision=$8`, c.InspectionEnabled, c.ReauthEnabled, c.InspectionConcurrency, c.AuthorizationConcurrency, c.AccountConcurrency, c.AccountPriority, string(raw), c.Revision, nextProbe, nextAuth, string(codes))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return infraerrors.Conflict("AUTHORIZATION_CONFIG_CHANGED", "配置已变化，请刷新后保存")
	}
	return nil
}

func (s *AccountAuthorizationService) next(spec string, now time.Time) time.Time {
	location := time.Local
	if s.cfg != nil {
		if loc, err := time.LoadLocation(s.cfg.Timezone); err == nil {
			location = loc
		}
	}
	schedule, _ := cron.ParseStandard(spec)
	return schedule.Next(now.In(location))
}

func parseAuthorizationAccounts(raw string) ([]AuthorizationSecret, error) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := make([]AuthorizationSecret, 0)
	seen := map[string]bool{}
	if len(raw) > 2<<20 || len(lines) > 1000 {
		return nil, infraerrors.BadRequest("AUTHORIZATION_INPUT", "每批最多1000行，内容最多2MiB")
	}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sep := "----"
		if !strings.Contains(line, sep) {
			sep = "\t"
		}
		parts := strings.Split(line, sep)
		if len(parts) < 2 || len(parts) > 3 {
			return nil, infraerrors.BadRequest("AUTHORIZATION_INPUT", fmt.Sprintf("第%d行格式错误：邮箱----密码----2FA", i+1))
		}
		email := strings.ToLower(strings.TrimSpace(parts[0]))
		addr, err := mail.ParseAddress(email)
		if err != nil || addr.Address != email || len(email) > 254 || parts[1] == "" || len(parts[1]) > 1024 || seen[email] {
			return nil, infraerrors.BadRequest("AUTHORIZATION_INPUT", fmt.Sprintf("第%d行邮箱、密码无效或邮箱重复", i+1))
		}
		secret := AuthorizationSecret{Email: email, Password: parts[1]}
		if len(parts) == 3 {
			secret.TwoFA = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(parts[2]), " ", ""))
			if secret.TwoFA != "" {
				decoded, decodeErr := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(secret.TwoFA, "="))
				if decodeErr != nil || len(decoded) < 10 || len(secret.TwoFA) > 256 {
					return nil, infraerrors.BadRequest("AUTHORIZATION_INPUT", fmt.Sprintf("第%d行2FA必须为TOTP密钥，不是六位验证码", i+1))
				}
				secret.TwoFA = strings.TrimRight(secret.TwoFA, "=")
			}
		}
		seen[email] = true
		out = append(out, secret)
	}
	if len(out) == 0 {
		return nil, infraerrors.BadRequest("AUTHORIZATION_INPUT", "请填写账号")
	}
	return out, nil
}

func (s *AccountAuthorizationService) Submit(ctx context.Context, input *AuthorizationSubmit) (string, error) {
	if input == nil {
		return "", infraerrors.BadRequest("AUTHORIZATION_OPTIONS", "授权参数不能为空")
	}
	if err := s.requireAuthorizationEncryption(); err != nil {
		return "", err
	}
	settings, err := s.normalizeAuthorizationWorkbench(ctx, input)
	if err != nil {
		return "", err
	}
	if len(settings.PassGroupIDs) == 0 {
		return "", infraerrors.BadRequest("AUTHORIZATION_GATE_GROUPS", "请选择至少一个入库分组")
	}
	secrets, err := parseAuthorizationAccounts(input.Accounts)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockAuthorizationHistory(ctx, tx); err != nil {
		return "", err
	}
	if err = saveAuthorizationWorkbenchTx(ctx, tx, settings); err != nil {
		return "", err
	}
	candy := &RadarConfig{}
	if settings.IntelligenceGate {
		candy, err = authorizationCandyConfigTx(ctx, tx)
		if err != nil {
			return "", err
		}
	}
	batch := uuid.NewString()
	for _, secret := range secrets {
		// Resolve a completing prior job before reading its history revision.
		// Holding an active row means the batch must wait, never replace it.
		var activeID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM pk_authorization_jobs WHERE email=$1 AND status IN ('queued','running') FOR UPDATE`, secret.Email).Scan(&activeID)
		if err == nil {
			return "", infraerrors.Conflict("AUTHORIZATION_RUNNING", "本批包含正在授权的邮箱，请等待原任务结束")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if err = settleAuthorizationHistoryTx(ctx, tx, secret.Email); err != nil {
			return "", err
		}
		data, err := json.Marshal(secret)
		if err != nil {
			return "", err
		}
		encrypted, err := s.encryptor.Encrypt(string(data))
		if err != nil {
			return "", errors.New("账号凭据加密失败")
		}
		historyID, revision, err := authorizationHistorySlot(ctx, tx, secret.Email)
		if err != nil {
			return "", err
		}
		snapshot, err := json.Marshal(authorizationCredentialSnapshot{Encrypted: encrypted, Revision: revision, HasTwoFA: secret.TwoFA != ""})
		if err != nil {
			return "", err
		}
		accountID, err := authorizationSubmissionAccount(ctx, tx, secret.Email)
		if err != nil {
			return "", err
		}
		jobSettings := settings
		jobSettings.WorkbenchRevision = ""
		if jobSettings.IntelligenceGate {
			groups, groupErr := authorizationJobSourceGroups(ctx, tx, accountID, settings.GroupIDs)
			if groupErr != nil {
				return "", groupErr
			}
			configureAuthorizationGroupGate(&jobSettings, candy, groups, true)
			if jobSettings.IntelligenceGate {
				if _, err = authorizationIntelligenceModels(jobSettings); err != nil {
					return "", err
				}
				targets := append(append([]int64{}, jobSettings.PassGroupIDs...), jobSettings.FailGroupIDs...)
				if err = validateAuthorizationGateGroups(ctx, tx, targets, nil); err != nil {
					return "", err
				}
			}
		}
		raw, err := json.Marshal(jobSettings)
		if err != nil {
			return "", err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO pk_authorization_jobs(batch_id,history_id,email,account_id,source,settings) VALUES($1,$2,$3,$6,'manual',$4::jsonb||$5::jsonb) ON CONFLICT (email) WHERE status IN ('queued','running') DO NOTHING`, batch, historyID, secret.Email, string(raw), string(snapshot), accountID)
		if err != nil {
			return "", err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return "", err
		}
		if n != 1 {
			return "", infraerrors.Conflict("AUTHORIZATION_RUNNING", "本批包含正在授权的邮箱，请等待原任务结束")
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	s.wake()
	return batch, nil
}

func (s *AccountAuthorizationService) Reveal(ctx context.Context, id int64) (*AuthorizationSecret, error) {
	var encrypted, email string
	if err := s.db.QueryRowContext(ctx, `SELECT email,secret_encrypted FROM (`+authorizationCurrentHistorySQL+`) h WHERE id=$1`, id).Scan(&email, &encrypted); err != nil {
		return nil, err
	}
	return s.decodeAuthorizationSecret(email, encrypted)
}

func (s *AccountAuthorizationService) AddProxies(ctx context.Context, raw string) error {
	if err := s.requireAuthorizationEncryption(); err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(lines) > 200 || len(raw) > 128<<10 {
		return infraerrors.BadRequest("AUTHORIZATION_PROXY", "一次最多200条代理")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", int64(70360003)); err != nil {
		return err
	}
	count := 0
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\\"))
		if line == "" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Hostname() == "" || u.Port() == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "socks5" && u.Scheme != "socks5h" && u.Scheme != "http" && u.Scheme != "https") {
			return infraerrors.BadRequest("AUTHORIZATION_PROXY", "代理必须为带端口的socks5、socks5h、http或https地址")
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return infraerrors.BadRequest("AUTHORIZATION_PROXY", "代理端口无效")
		}
		encrypted, err := s.encryptor.Encrypt(line)
		if err != nil {
			return errors.New("代理加密失败")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO pk_authorization_proxies(endpoint,secret_encrypted) VALUES($1,$2)`, u.Scheme+"://"+u.Host, encrypted); err != nil {
			return err
		}
		count++
	}
	if count == 0 {
		return infraerrors.BadRequest("AUTHORIZATION_PROXY", "请填写代理")
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM pk_authorization_proxies WHERE enabled=true`).Scan(&active); err != nil {
		return err
	}
	if active > 200 {
		return infraerrors.BadRequest("AUTHORIZATION_PROXY_LIMIT", "最多启用200条授权代理，请先停用旧代理")
	}
	return tx.Commit()
}

func (s *AccountAuthorizationService) DisableProxy(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pk_authorization_proxies SET enabled=false WHERE id=$1`, id)
	return err
}

func (s *AccountAuthorizationService) CancelJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pk_authorization_jobs SET status='cancelled',stage='cancelled',message='管理员已取消',finished_at=NOW() WHERE id=$1 AND status IN ('queued','running')`, id)
	if err != nil {
		return err
	}
	return s.archiveAuthorizationCredentials(ctx, id)
}

// Supplement updates the current encrypted credential record. It never queues
// authorization; existing jobs keep their snapshots and the next round reads the edit.
func (s *AccountAuthorizationService) Supplement(ctx context.Context, id int64, secret *AuthorizationSecret) error {
	if err := s.requireAuthorizationEncryption(); err != nil {
		return err
	}
	if err := validateAuthorizationSecret(secret, true); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockAuthorizationHistory(ctx, tx); err != nil {
		return err
	}
	if err = pinLegacyAuthorizationCredentials(ctx, tx, secret.Email); err != nil {
		return err
	}
	var email, version string
	if err = tx.QueryRowContext(ctx, `SELECT `+authorizationEmailSQL+`,md5(a.credentials::text) FROM accounts a WHERE a.id=$1 AND a.deleted_at IS NULL AND a.platform='openai' AND `+authorizationSupportedSQL+` FOR UPDATE`, id).Scan(&email, &version); err != nil {
		return err
	}
	if email != "" && email != secret.Email {
		return infraerrors.Conflict("AUTHORIZATION_EMAIL_CHANGED", "补全邮箱必须与账号管理中的邮箱一致")
	}
	if email == "" {
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{email}',to_jsonb($2::text)),updated_at=NOW() WHERE id=$1`, id, secret.Email); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pk_authorization_state st SET credential_version=md5(a.credentials::text) FROM accounts a WHERE st.account_id=$1 AND a.id=st.account_id AND st.credential_version=$2`, id, version); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id) VALUES($1,$2)`, SchedulerOutboxEventAccountChanged, id); err != nil {
			return err
		}
	}
	data, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	encrypted, err := s.encryptor.Encrypt(string(data))
	if err != nil {
		return errors.New("凭据加密失败")
	}
	historyID, _, err := authorizationHistorySlot(ctx, tx, secret.Email)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_authorization_history SET secret_encrypted=$2,has_twofa=true,source='supplement',created_at=NOW() WHERE id=$1`, historyID, encrypted); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if email == "" {
		s.authorizationAccountCommitted(ctx, id)
	}
	return nil
}

func (s *AccountAuthorizationService) requireAuthorizationEncryption() error {
	if s.cfg == nil || !s.cfg.Totp.EncryptionKeyConfigured {
		return infraerrors.BadRequest("AUTHORIZATION_ENCRYPTION_KEY_REQUIRED", "请先固定 TOTP_ENCRYPTION_KEY 后再保存凭据与代理，避免重启后历史无法解密")
	}
	return nil
}

func validateAuthorizationSecret(secret *AuthorizationSecret, requireTwoFA bool) error {
	secret.Email = strings.ToLower(strings.TrimSpace(secret.Email))
	secret.TwoFA = strings.TrimRight(strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret.TwoFA), " ", "")), "=")
	addr, err := mail.ParseAddress(secret.Email)
	if err != nil || addr.Address != secret.Email || len(secret.Email) > 254 || len(secret.Password) == 0 || len(secret.Password) > 1024 {
		return infraerrors.BadRequest("AUTHORIZATION_CREDENTIALS", "请填写有效邮箱和密码")
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret.TwoFA)
	if (secret.TwoFA != "" && (err != nil || len(decoded) < 10 || len(secret.TwoFA) > 256)) || (requireTwoFA && secret.TwoFA == "") {
		return infraerrors.BadRequest("AUTHORIZATION_CREDENTIALS", "请填写完整的2FA密钥，不是六位动态验证码")
	}
	return nil
}
