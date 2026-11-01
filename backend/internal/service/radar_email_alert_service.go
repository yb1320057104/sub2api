package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/robfig/cron/v3"
)

const radarEmailAlertLockID int64 = 81003801

type RadarEmailAlertConditions struct {
	QuotaBelow100 bool `json:"quota_below_100"`
	Qualified     bool `json:"qualified"`
	Not401        bool `json:"not_401"`
}

type RadarEmailAlertCounts struct {
	TotalAccounts        int
	MatchedAccounts      int
	UnknownQuotaAccounts int
}

type RadarEmailAlertConfig struct {
	Enabled         bool                      `json:"enabled"`
	IntervalMinutes int                       `json:"interval_minutes"`
	MinimumCount    int                       `json:"minimum_count"`
	Conditions      RadarEmailAlertConditions `json:"conditions"`
	Recipients      []string                  `json:"recipients"`
	Revision        int64                     `json:"revision"`
	NextCheckAt     *time.Time                `json:"next_check_at"`
}

type RadarEmailAlertStatus struct {
	LastCheckedAt     *time.Time `json:"last_checked_at"`
	LastCount         *int       `json:"last_count"`
	TotalAccounts     *int       `json:"total_accounts"`
	UnknownQuotaCount *int       `json:"unknown_quota_count"`
	LastStatus        string     `json:"last_status"`
	LastError         string     `json:"last_error"`
	LastSentAt        *time.Time `json:"last_sent_at"`
	SentCount         int        `json:"sent_count"`
	FailedCount       int        `json:"failed_count"`
}

type RadarEmailAlertView struct {
	Config         RadarEmailAlertConfig `json:"config"`
	Status         RadarEmailAlertStatus `json:"status"`
	SMTPConfigured bool                  `json:"smtp_configured"`
	runToken       string
}

type RadarEmailAlertService struct {
	db                        *sql.DB
	accounts                  *AccountCandyTestService
	email                     *EmailService
	ctx                       context.Context
	cancel                    context.CancelFunc
	mu                        sync.Mutex
	started, stopped, running bool
	cron                      *cron.Cron
	workers                   sync.WaitGroup
}

func NewRadarEmailAlertService(db *sql.DB, accounts *AccountCandyTestService, email *EmailService) *RadarEmailAlertService {
	ctx, cancel := context.WithCancel(context.Background())
	return &RadarEmailAlertService{db: db, accounts: accounts, email: email, ctx: ctx, cancel: cancel}
}

func (s *AccountCandyTestService) EmailAlerts() *RadarEmailAlertService {
	if s == nil {
		return nil
	}
	return s.emailAlerts
}

func (s *RadarEmailAlertService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped || s.db == nil || s.accounts == nil || s.email == nil {
		return
	}
	c := cron.New()
	if _, err := c.AddFunc("* * * * *", s.wake); err != nil {
		return
	}
	s.cron, s.started = c, true
	c.Start()
}

func (s *RadarEmailAlertService) Stop() {
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

func (s *RadarEmailAlertService) wake() {
	s.mu.Lock()
	if s.stopped || s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.workers.Add(1)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock(); s.workers.Done() }()
	// Allow all 20 serial SMTP recipients their bounded dial/I/O deadlines,
	// plus counting and progress writes. Expiry prevents further admissions.
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Minute)
	defer cancel()
	if err := s.work(ctx); err != nil && s.ctx.Err() == nil {
		slog.Warn("radar_email_alert_incomplete", "message", "邮件提醒本轮未完成，将等待下一周期")
	}
}

const radarEmailAlertColumns = `enabled,interval_minutes,minimum_count,conditions,recipients,revision,next_check_at,
last_checked_at,last_count,total_accounts,unknown_quota_count,last_status,last_error,last_sent_at,sent_count,failed_count,active_run_token`

func scanRadarEmailAlert(row interface{ Scan(...any) error }) (*RadarEmailAlertView, error) {
	v := &RadarEmailAlertView{}
	var conditions, recipients []byte
	c, st := &v.Config, &v.Status
	err := row.Scan(&c.Enabled, &c.IntervalMinutes, &c.MinimumCount, &conditions, &recipients, &c.Revision, &c.NextCheckAt,
		&st.LastCheckedAt, &st.LastCount, &st.TotalAccounts, &st.UnknownQuotaCount, &st.LastStatus, &st.LastError, &st.LastSentAt, &st.SentCount, &st.FailedCount, &v.runToken)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(conditions, &c.Conditions); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(recipients, &c.Recipients); err != nil {
		return nil, err
	}
	if c.Recipients == nil {
		c.Recipients = []string{}
	}
	return v, nil
}

func (s *RadarEmailAlertService) View(ctx context.Context) (*RadarEmailAlertView, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "RADAR_EMAIL_ALERT_UNAVAILABLE", "邮件提醒服务暂不可用")
	}
	v, err := scanRadarEmailAlert(s.db.QueryRowContext(ctx, `SELECT `+radarEmailAlertColumns+` FROM pk_radar_email_alert WHERE id=1`))
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "RADAR_EMAIL_ALERT_READ_FAILED", "邮件提醒配置暂时无法读取")
	}
	_, v.SMTPConfigured = s.smtpConfig(ctx)
	return v, nil
}

func normalizeRadarEmailAlertConfig(c *RadarEmailAlertConfig) error {
	if c == nil || c.IntervalMinutes < 1 || c.IntervalMinutes > 10080 || c.MinimumCount < 1 || c.MinimumCount > 1000000 || c.Revision < 1 {
		return infraerrors.BadRequest("RADAR_EMAIL_ALERT_CONFIG", "提醒间隔须为1至10080分钟，账号阈值须为1至1000000，请提交有效配置版本")
	}
	if !c.Conditions.QuotaBelow100 && !c.Conditions.Qualified && !c.Conditions.Not401 {
		return infraerrors.BadRequest("RADAR_EMAIL_ALERT_CONDITIONS", "请至少选择一个账号条件")
	}
	recipients := make([]string, 0, len(c.Recipients))
	seen := make(map[string]bool, len(c.Recipients))
	for _, value := range c.Recipients {
		value = strings.TrimSpace(value)
		address, err := mail.ParseAddress(value)
		if err != nil || len(value) > 254 || strings.ContainsAny(value, "\r\n") || address.Name != "" || address.Address != value {
			return infraerrors.BadRequest("RADAR_EMAIL_ALERT_RECIPIENT", "收件人须为纯邮箱地址，不支持姓名、备注或地址列表")
		}
		key := strings.ToLower(address.Address)
		if !seen[key] {
			seen[key] = true
			recipients = append(recipients, address.Address)
		}
	}
	if len(recipients) > 20 || (c.Enabled && len(recipients) == 0) {
		return infraerrors.BadRequest("RADAR_EMAIL_ALERT_RECIPIENTS", "最多设置20个收件人，启用提醒时至少需要一个邮箱")
	}
	c.Recipients = recipients
	return nil
}

func (s *RadarEmailAlertService) smtpConfig(ctx context.Context) (*SMTPConfig, bool) {
	if s == nil || s.email == nil {
		return nil, false
	}
	c, err := s.email.GetSMTPConfig(ctx)
	if err != nil || c == nil || strings.TrimSpace(c.Host) == "" || c.Port < 1 || c.Port > 65535 || strings.TrimSpace(c.Username) == "" || c.Password == "" {
		return nil, false
	}
	if _, err = mail.ParseAddress(c.From); err != nil || strings.ContainsAny(c.From, "\r\n") {
		return nil, false
	}
	return c, true
}

func (s *RadarEmailAlertService) SaveConfig(ctx context.Context, input *RadarEmailAlertConfig) (*RadarEmailAlertView, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "RADAR_EMAIL_ALERT_UNAVAILABLE", "邮件提醒服务暂不可用")
	}
	if input == nil {
		return nil, infraerrors.BadRequest("RADAR_EMAIL_ALERT_CONFIG", "请提交完整的邮件提醒配置")
	}
	c := *input
	c.Recipients = append([]string(nil), input.Recipients...)
	if err := normalizeRadarEmailAlertConfig(&c); err != nil {
		return nil, err
	}
	_, smtpConfigured := s.smtpConfig(ctx)
	if c.Enabled && !smtpConfigured {
		return nil, infraerrors.BadRequest("RADAR_EMAIL_ALERT_SMTP", "请先在系统设置中完整配置SMTP，再启用邮件提醒")
	}
	conditions, err := json.Marshal(c.Conditions)
	if err != nil {
		return nil, err
	}
	recipients, err := json.Marshal(c.Recipients)
	if err != nil {
		return nil, err
	}
	// A save supersedes an in-flight snapshot. The server owns next_check_at;
	// accepting it in the DTO never permits a client to replay an older cycle.
	v, err := scanRadarEmailAlert(s.db.QueryRowContext(ctx, `UPDATE pk_radar_email_alert SET
enabled=$1::boolean,interval_minutes=$2::integer,minimum_count=$3::integer,conditions=$4::jsonb,recipients=$5::jsonb,
revision=revision+1,next_check_at=CASE WHEN $1::boolean THEN date_trunc('minute',NOW())+INTERVAL '1 minute' ELSE NULL::timestamptz END,
last_checked_at=NULL,last_count=NULL,total_accounts=NULL,unknown_quota_count=NULL,sent_count=0,failed_count=0,
last_error=CASE WHEN last_status='checking' THEN '配置已更新，旧周期已停止' ELSE '' END,
last_status=CASE WHEN last_status='checking' THEN 'superseded' ELSE 'idle' END,active_run_token='',updated_at=NOW()
WHERE id=1 AND revision=$6::bigint RETURNING `+radarEmailAlertColumns, c.Enabled, c.IntervalMinutes, c.MinimumCount, string(conditions), string(recipients), c.Revision))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.New(http.StatusConflict, "RADAR_EMAIL_ALERT_CONFIG_CHANGED", "邮件提醒配置已变化，请刷新后再保存")
	}
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "RADAR_EMAIL_ALERT_SAVE_FAILED", "邮件提醒配置保存失败，请稍后重试")
	}
	v.SMTPConfigured = smtpConfigured
	return v, nil
}
