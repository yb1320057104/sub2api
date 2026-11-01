package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The connection owns a session lock, not a transaction. Other instances cannot
// send the same cycle while SMTP is in flight; no SQL transaction spans mail I/O.
func (s *RadarEmailAlertService) work(ctx context.Context) error {
	// Resolve settings before pinning a connection: a one-connection pool must
	// not wait for itself while this cycle owns the session advisory lock.
	smtp, configured := s.smtpConfig(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1::bigint)`, radarEmailAlertLockID).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock($1::bigint)`, radarEmailAlertLockID); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	// A previous process may have sent some recipients before it stopped. Never
	// replay that cycle: retain its counts, mark interruption and wait a period.
	recovered, err := conn.ExecContext(ctx, `UPDATE pk_radar_email_alert SET
last_status='interrupted',last_error='上次检查或发送被中断，等待下一周期',active_run_token='',
next_check_at=CASE WHEN enabled THEN GREATEST(COALESCE(next_check_at,NOW()),NOW()+interval_minutes*INTERVAL '1 minute') ELSE NULL::timestamptz END,
updated_at=NOW() WHERE id=1 AND last_status='checking'`)
	if err != nil {
		return err
	}
	if n, err := recovered.RowsAffected(); err != nil || n > 0 {
		return err
	}
	// Claim and advance next_check_at atomically before counting or sending.
	// A failed query/SMTP cycle therefore cannot send again every cron minute.
	run, err := scanRadarEmailAlert(conn.QueryRowContext(ctx, `UPDATE pk_radar_email_alert SET
next_check_at=NOW()+interval_minutes*INTERVAL '1 minute',last_checked_at=NOW(),
last_count=NULL,total_accounts=NULL,unknown_quota_count=NULL,last_status='checking',last_error='',
sent_count=0,failed_count=0,active_run_token=$1::text,updated_at=NOW()
WHERE id=1 AND enabled AND next_check_at IS NOT NULL AND next_check_at<=NOW() RETURNING `+radarEmailAlertColumns, uuid.NewString()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.runCycle(ctx, conn, run, smtp, configured)
}

func (s *RadarEmailAlertService) runCycle(ctx context.Context, conn *sql.Conn, run *RadarEmailAlertView, smtp *SMTPConfig, configured bool) (resultErr error) {
	status, message := "check_failed", "账号统计失败，本轮未发送邮件；等待下一周期"
	defer func() {
		if ctx.Err() != nil && status == "check_failed" {
			status, message = "interrupted", "本轮检查或发送已中断，等待下一周期"
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := finishRadarEmailAlert(cleanup, conn, run, status, message); err != nil && resultErr == nil {
			resultErr = err
		}
	}()
	if err := normalizeRadarEmailAlertConfig(&run.Config); err != nil {
		return err
	}
	countCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	counts, err := s.accounts.countRadarEmailAccounts(countCtx, conn, run.Config.Conditions)
	cancel()
	if err != nil {
		return err
	}
	if counts == nil || counts.TotalAccounts < 0 || counts.MatchedAccounts < 0 || counts.MatchedAccounts > counts.TotalAccounts || counts.UnknownQuotaAccounts < 0 || counts.UnknownQuotaAccounts > counts.TotalAccounts {
		return errors.New("invalid radar account count")
	}
	result, err := conn.ExecContext(ctx, `UPDATE pk_radar_email_alert SET last_count=$3::integer,total_accounts=$4::integer,unknown_quota_count=$5::integer,updated_at=NOW()
WHERE id=1 AND enabled AND revision=$1::bigint AND active_run_token=$2::text AND last_status='checking'`, run.Config.Revision, run.runToken, counts.MatchedAccounts, counts.TotalAccounts, counts.UnknownQuotaAccounts)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		status, message = "superseded", "配置已变化，本轮已停止"
		return err
	}
	if counts.MatchedAccounts >= run.Config.MinimumCount {
		status, message = "healthy", ""
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !configured {
		run.Status.FailedCount = len(run.Config.Recipients)
		status, message = "send_failed", "SMTP配置不可用，本轮未发送；请检查系统设置"
		return nil
	}
	subject, body := radarEmailAlertMessage(run, counts)
	for _, recipient := range run.Config.Recipients {
		if err := ctx.Err(); err != nil {
			status, message = "interrupted", "本轮发送已中断，等待下一周期"
			return err
		}
		var allowed bool
		// This also verifies that the session holding our advisory lock remains
		// alive immediately before each recipient. A changed config stops sends.
		err := conn.QueryRowContext(ctx, `SELECT enabled AND revision=$1::bigint AND active_run_token=$2::text AND last_status='checking' FROM pk_radar_email_alert WHERE id=1`, run.Config.Revision, run.runToken).Scan(&allowed)
		if err != nil {
			status, message = "interrupted", "发送前状态无法确认，本轮已停止；等待下一周期"
			return err
		}
		if !allowed {
			status, message = "superseded", "配置已变化，本轮已停止"
			return nil
		}
		attempted, sendErr := s.sendRecipient(ctx, smtp, recipient, subject, body)
		if !attempted {
			status, message = "interrupted", "本轮发送已中断，等待下一周期"
			return sendErr
		}
		if sendErr != nil {
			run.Status.FailedCount++
		} else {
			run.Status.SentCount++
		}
		// Record an SMTP result even if cancellation arrived while the existing
		// non-context-aware SMTP call was finishing. Do not expose its raw error.
		progressCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		result, err := conn.ExecContext(progressCtx, `UPDATE pk_radar_email_alert SET
sent_count=$3::integer,failed_count=$4::integer,last_sent_at=CASE WHEN $5::boolean THEN NOW() ELSE last_sent_at END,updated_at=NOW()
WHERE id=1 AND revision=$1::bigint AND active_run_token=$2::text AND last_status='checking'`, run.Config.Revision, run.runToken, run.Status.SentCount, run.Status.FailedCount, sendErr == nil)
		done()
		if err != nil {
			status, message = "interrupted", "发送结果保存失败，已停止后续发送；等待下一周期"
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			status, message = "superseded", "配置已变化，本轮已停止"
			return err
		}
	}
	switch {
	case run.Status.FailedCount == 0:
		status, message = "sent", ""
	case run.Status.SentCount > 0:
		status, message = "partial", "部分收件人发送失败；请检查SMTP设置，等待下一周期"
	default:
		status, message = "send_failed", "邮件发送失败；请检查SMTP设置，等待下一周期"
	}
	return nil
}

func finishRadarEmailAlert(ctx context.Context, conn *sql.Conn, run *RadarEmailAlertView, status, message string) error {
	_, err := conn.ExecContext(ctx, `UPDATE pk_radar_email_alert SET
last_status=$3::text,last_error=$4::text,sent_count=$5::integer,failed_count=$6::integer,active_run_token='',
next_check_at=CASE WHEN $3::text='interrupted' AND enabled THEN GREATEST(COALESCE(next_check_at,NOW()),NOW()+interval_minutes*INTERVAL '1 minute') ELSE next_check_at END,
updated_at=NOW() WHERE id=1 AND revision=$1::bigint AND active_run_token=$2::text AND last_status='checking'`, run.Config.Revision, run.runToken, status, message, run.Status.SentCount, run.Status.FailedCount)
	return err
}

func (s *RadarEmailAlertService) sendRecipient(ctx context.Context, smtp *SMTPConfig, to, subject, body string) (bool, error) {
	// Serialize admission with Stop, without holding the mutex during SMTP.
	// Stop immediately closes later admissions; an admitted call may complete.
	s.mu.Lock()
	if s.stopped || ctx.Err() != nil {
		s.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return false, context.Canceled
	}
	s.mu.Unlock()
	return true, s.email.SendEmailWithConfig(smtp, to, subject, body)
}

func radarEmailAlertMessage(run *RadarEmailAlertView, counts *RadarEmailAlertCounts) (string, string) {
	conditions := make([]string, 0, 3)
	if run.Config.Conditions.QuotaBelow100 {
		conditions = append(conditions, "额度使用率低于100%")
	}
	if run.Config.Conditions.Qualified {
		conditions = append(conditions, "测智合格")
	}
	if run.Config.Conditions.Not401 {
		conditions = append(conditions, "当前未出现401授权错误")
	}
	checkedAt := ""
	if run.Status.LastCheckedAt != nil {
		checkedAt = run.Status.LastCheckedAt.Format(time.RFC3339)
	}
	subject := fmt.Sprintf("[雷达观测站] 符合条件账号%d个，低于阈值%d个", counts.MatchedAccounts, run.Config.MinimumCount)
	body := fmt.Sprintf(`<html><body><h2>雷达账号数量提醒</h2><p>本次符合全部已选条件的账号为 <strong>%d</strong> 个，低于设置阈值 <strong>%d</strong> 个。</p><p>已选条件：%s（同时满足）。</p><p>账号总数：%d；额度状态未知：%d（仅启用额度条件时统计）。</p><p>检查时间：%s</p><p>请前往雷达观测站查看账号。下一周期将在约%d分钟后检查。</p></body></html>`,
		counts.MatchedAccounts, run.Config.MinimumCount, html.EscapeString(strings.Join(conditions, "、")), counts.TotalAccounts, counts.UnknownQuotaAccounts, html.EscapeString(checkedAt), run.Config.IntervalMinutes)
	return subject, body
}
