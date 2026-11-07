package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/sjson"
)

const (
	AuditSettlementCapabilityHeader = "X-Sub2api-Audit-Settlement"
	AuditSettlementReceiptHeader    = "X-Sub2api-Audit-Receipt"
	AuditSettlementVersion          = "v1"
	AuditSettlementPathPrefix       = "/v1/sub2api/audit-settlements/"
)

var ErrAuditSettlementNotFound = errors.New("audit settlement not found")

func auditSettlementCyberCode(c *gin.Context) string {
	if c != nil && c.Request != nil && HasAuditSettlement(c.Request.Context()) && GetOpsCyberPolicy(c) != nil {
		return "cyber_policy"
	}
	return ""
}

func auditSettlementClientError(c *gin.Context, errorType, message string) gin.H {
	result := gin.H{"type": errorType, "message": message}
	if code := auditSettlementCyberCode(c); code != "" {
		result["code"] = code
	}
	return result
}

// Keep only the safe machine code needed to settle an error through another
// negotiated gateway; sanitized messages and all native response bytes remain.
func auditSettlementCyberEvent(c *gin.Context, payload []byte, eventType string) []byte {
	code := auditSettlementCyberCode(c)
	if code == "" {
		return payload
	}
	field := ""
	if eventType == "response.failed" {
		field = "response.error.code"
	} else if eventType == "error" {
		field = "error.code"
	}
	if field == "" {
		return payload
	}
	updated, err := sjson.SetBytes(payload, field, code)
	if err != nil {
		return payload
	}
	return updated
}

// Only the versioned, authenticated read route receives relaxed billing enforcement.
func IsAuditSettlementRead(method, path string) bool {
	if method != http.MethodGet || !strings.HasPrefix(path, AuditSettlementPathPrefix) {
		return false
	}
	id := strings.TrimPrefix(path, AuditSettlementPathPrefix)
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

// V1 settles one completed HTTP call, including SSE, without adding protocol frames.
// WebSocket turns and deferred media jobs require their own durable identity contract.
func SupportsAuditSettlementRequest(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/chat/completions", "/chat/completions", "/v1/messages", "/messages", "/antigravity/v1/messages":
		return true
	default:
		return false
	}
}

type AuditSettlementItem struct {
	ID     string `json:"id"`
	Amount string `json:"amount"`
}

type AuditSettlementReceipt struct {
	Object         string                `json:"object"`
	SchemaVersion  int                   `json:"schema_version"`
	ReceiptID      string                `json:"receipt_id"`
	Status         string                `json:"status"`
	Currency       string                `json:"currency"`
	AdditionalCost *string               `json:"additional_cost,omitempty"`
	Items          []AuditSettlementItem `json:"items,omitempty"`
}

type auditSettlementStore interface {
	linkAuditSettlementUsage(context.Context, string, int64, int64, *UsageLog) error
	finishAuditSettlement(context.Context, string, int64, int64, bool) error
}

type auditSettlementContextKey struct{}
type auditSettlementTaskKey struct{}
type auditSettlementUsageObserverKey struct{}

// The importing gateway registers its durable upstream receipt linkage here.
// It runs after the ordinary usage and local audit charge are committed, before
// the outgoing receipt may become ready. It must not perform network polling.
type AuditSettlementUsageObserver func(context.Context, *UsageLog) error

func WithAuditSettlementUsageObserver(ctx context.Context, observer AuditSettlementUsageObserver) context.Context {
	return context.WithValue(ctx, auditSettlementUsageObserverKey{}, observer)
}

func auditSettlementUsageObserver(ctx context.Context) AuditSettlementUsageObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(auditSettlementUsageObserverKey{}).(AuditSettlementUsageObserver)
	return observer
}

func HasAuditSettlementUsageObserver(ctx context.Context) bool {
	return auditSettlementUsageObserver(ctx) != nil
}

func recordAuditSettlementUsageObserver(ctx context.Context, usage *UsageLog) {
	if observer := auditSettlementUsageObserver(ctx); observer != nil {
		if err := observer(ctx, usage); err != nil {
			FailAuditSettlement(ctx)
			slog.Warn("audit_settlement.import_link_unavailable")
		}
	}
}

type auditSettlementTracker struct {
	store                     auditSettlementStore
	id                        string
	userID, apiKeyID          int64
	mu                        sync.Mutex
	pending                   int
	closed, failed, finalized bool
}

type auditSettlementTask struct {
	tracker  *auditSettlementTracker
	recorded atomic.Bool
	once     sync.Once
}

func (s *CyberPolicyService) BeginAuditSettlement(ctx context.Context, userID, apiKeyID int64) (context.Context, string, error) {
	if s == nil || s.db == nil || userID <= 0 || apiKeyID <= 0 {
		return ctx, "", errors.New("audit settlement unavailable")
	}
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_settlement_receipts(id,user_id,api_key_id) VALUES($1,$2,$3)`, id, userID, apiKeyID)
	if err != nil {
		return ctx, "", err
	}
	t := &auditSettlementTracker{store: s, id: id, userID: userID, apiKeyID: apiKeyID}
	return context.WithValue(ctx, auditSettlementContextKey{}, t), id, nil
}

func auditSettlementFromContext(ctx context.Context) *auditSettlementTracker {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(auditSettlementContextKey{}).(*auditSettlementTracker)
	return t
}

func HasAuditSettlement(ctx context.Context) bool { return auditSettlementFromContext(ctx) != nil }

func CopyAuditSettlement(parent, target context.Context) context.Context {
	if observer := auditSettlementUsageObserver(parent); observer != nil {
		target = WithAuditSettlementUsageObserver(target, observer)
	}
	if t := auditSettlementFromContext(parent); t != nil {
		return context.WithValue(target, auditSettlementContextKey{}, t)
	}
	return target
}

// Called before enqueueing, so a response cannot advertise ready while billing is queued.
func BeginAuditSettlementTask(parent context.Context) (func(context.Context) context.Context, func()) {
	t := auditSettlementFromContext(parent)
	if t == nil {
		return func(ctx context.Context) context.Context { return ctx }, func() {}
	}
	t.mu.Lock()
	t.pending++
	t.mu.Unlock()
	task := &auditSettlementTask{tracker: t}
	return func(ctx context.Context) context.Context {
			ctx = context.WithValue(ctx, auditSettlementContextKey{}, t)
			return context.WithValue(ctx, auditSettlementTaskKey{}, task)
		}, func() {
			task.once.Do(func() {
				t.mu.Lock()
				t.pending--
				t.failed = t.failed || !task.recorded.Load()
				t.mu.Unlock()
				t.finalizeIfComplete()
			})
		}
}

func EndAuditSettlementRequest(ctx context.Context) {
	if t := auditSettlementFromContext(ctx); t != nil {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		t.finalizeIfComplete()
	}
}

func FailAuditSettlement(ctx context.Context) {
	if t := auditSettlementFromContext(ctx); t != nil {
		t.mu.Lock()
		t.failed = true
		t.mu.Unlock()
	}
}

func (t *auditSettlementTracker) finalizeIfComplete() {
	t.mu.Lock()
	if !t.closed || t.pending != 0 || t.finalized {
		t.mu.Unlock()
		return
	}
	t.finalized = true
	failed := t.failed
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := t.store.finishAuditSettlement(ctx, t.id, t.userID, t.apiKeyID, failed); err != nil {
		slog.Warn("audit_settlement.finalization_unavailable")
	}
}

// Snapshot only actual committed debits, after ordinary billing and audit evaluation.
// Failure keeps the receipt unavailable; it never repeats a financial operation.
func RecordAuditSettlementUsage(ctx context.Context, usage *UsageLog) {
	t := auditSettlementFromContext(ctx)
	if t == nil {
		return
	}
	if usage == nil || usage.ID <= 0 || usage.CreatedAt.IsZero() || usage.UserID != t.userID || usage.APIKeyID != t.apiKeyID {
		FailAuditSettlement(ctx)
		return
	}
	if err := t.store.linkAuditSettlementUsage(ctx, t.id, t.userID, t.apiKeyID, usage); err != nil {
		FailAuditSettlement(ctx)
		slog.Warn("audit_settlement.usage_link_unavailable")
		return
	}
	if task, _ := ctx.Value(auditSettlementTaskKey{}).(*auditSettlementTask); task != nil {
		task.recorded.Store(true)
	}
}

func (s *CyberPolicyService) linkAuditSettlementUsage(ctx context.Context, receipt string, userID, apiKeyID int64, usage *UsageLog) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO audit_settlement_receipt_items(receipt_id,usage_id,usage_created_at,amount)
		SELECT r.id,l.id,l.created_at,COALESCE((SELECT ac.amount FROM cyber_policy_auto_charges ac
		WHERE ac.usage_id=l.id AND ac.usage_created_at=l.created_at AND ac.user_id=l.user_id AND ac.status='deducted'),0)
		FROM audit_settlement_receipts r JOIN usage_logs l ON l.id=$4 AND l.created_at=$5
		WHERE r.id=$1 AND r.user_id=$2 AND r.api_key_id=$3 AND r.status='pending'
		AND l.user_id=r.user_id AND l.api_key_id=r.api_key_id
		ON CONFLICT(receipt_id,usage_id,usage_created_at) DO UPDATE SET amount=audit_settlement_receipt_items.amount`, receipt, userID, apiKeyID, usage.ID, usage.CreatedAt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("audit settlement usage identity unavailable")
	}
	return err
}

func (s *CyberPolicyService) finishAuditSettlement(ctx context.Context, receipt string, userID, apiKeyID int64, failed bool) error {
	status := "ready"
	if failed {
		status = "unavailable"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE audit_settlement_receipts SET status=$4,completed_at=NOW()
		WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND status='pending'`, receipt, userID, apiKeyID, status)
	return err
}

func (s *CyberPolicyService) AuditSettlement(ctx context.Context, userID, apiKeyID int64, id string) (*AuditSettlementReceipt, error) {
	if !IsAuditSettlementRead(http.MethodGet, AuditSettlementPathPrefix+id) {
		return nil, ErrAuditSettlementNotFound
	}
	result := &AuditSettlementReceipt{Object: "sub2api.audit_settlement", SchemaVersion: 1, ReceiptID: id, Currency: "USD"}
	err := s.db.QueryRowContext(ctx, `SELECT CASE
		WHEN r.status='pending' AND r.created_at<NOW()-INTERVAL '24 hours' THEN 'unavailable'
		WHEN r.status='ready' AND EXISTS (
			SELECT 1 FROM audit_settlement_receipt_items item JOIN audit_settlement_imports imp
			ON imp.usage_id=item.usage_id AND imp.usage_created_at=item.usage_created_at
			WHERE item.receipt_id=r.id AND imp.status IN ('unavailable','failed')) THEN 'unavailable'
		WHEN r.status='ready' AND EXISTS (
			SELECT 1 FROM audit_settlement_receipt_items item JOIN audit_settlement_imports imp
			ON imp.usage_id=item.usage_id AND imp.usage_created_at=item.usage_created_at
			WHERE item.receipt_id=r.id AND imp.status='pending')
			THEN CASE WHEN r.created_at<NOW()-INTERVAL '24 hours' THEN 'unavailable' ELSE 'pending' END
		ELSE r.status END
		FROM audit_settlement_receipts r WHERE r.id=$1 AND r.user_id=$2 AND r.api_key_id=$3 AND r.expires_at>NOW()`, id, userID, apiKeyID).Scan(&result.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuditSettlementNotFound
	}
	if err != nil {
		return nil, err
	}
	if result.Status != "ready" {
		return result, nil
	}
	var total string
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(item.amount + CASE WHEN imp.status='deducted' THEN imp.amount ELSE 0 END),0)::NUMERIC(30,8)::TEXT
		FROM audit_settlement_receipt_items item LEFT JOIN audit_settlement_imports imp
		ON imp.usage_id=item.usage_id AND imp.usage_created_at=item.usage_created_at WHERE item.receipt_id=$1`, id).Scan(&total)
	if err != nil {
		return nil, err
	}
	result.AdditionalCost = &total
	rows, err := s.db.QueryContext(ctx, `SELECT 'local:' || usage_id::TEXT || ':' || to_char(usage_created_at AT TIME ZONE 'UTC','YYYYMMDDHH24MISS.US') AS id,amount::TEXT
		FROM audit_settlement_receipt_items WHERE receipt_id=$1 AND amount>0
		UNION ALL
		SELECT 'import:' || imp.id::TEXT AS id,imp.amount::TEXT
		FROM audit_settlement_receipt_items item JOIN audit_settlement_imports imp
		ON imp.usage_id=item.usage_id AND imp.usage_created_at=item.usage_created_at
		WHERE item.receipt_id=$1 AND imp.status='deducted' AND imp.amount>0 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item AuditSettlementItem
		if err := rows.Scan(&item.ID, &item.Amount); err != nil {
			return nil, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}
