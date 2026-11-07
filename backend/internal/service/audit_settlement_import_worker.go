package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

// Polling is independent of the model stream and survives process restarts.
// Only pending network queries retry; every financial outcome is terminal.
type auditSettlementImportWorker struct {
	db       *sql.DB
	accounts AccountRepository
	upstream HTTPUpstream
	cfg      *config.Config
	owner    string
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func newAuditSettlementImportWorker(db *sql.DB, accounts AccountRepository, upstream HTTPUpstream, cfg *config.Config) *auditSettlementImportWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &auditSettlementImportWorker{db: db, accounts: accounts, upstream: upstream, cfg: cfg, owner: uuid.NewString(), ctx: ctx, cancel: cancel}
}

func (w *auditSettlementImportWorker) Start() {
	if w == nil || w.db == nil || w.accounts == nil || w.upstream == nil {
		return
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		timer := time.NewTicker(2 * time.Second)
		defer timer.Stop()
		for {
			ctx, cancel := context.WithTimeout(w.ctx, 25*time.Second)
			count, err := w.processBatch(ctx)
			if err != nil && w.ctx.Err() == nil {
				slog.Warn("audit_settlement.import_worker_unavailable")
			}
			cancel()
			if count == 8 && err == nil && w.ctx.Err() == nil {
				continue
			}
			select {
			case <-w.ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
}

func (w *auditSettlementImportWorker) Stop() {
	if w != nil {
		w.cancel()
		w.wg.Wait()
	}
}

type auditImportPending struct {
	id, accountID     int64
	identity, receipt string
	attempts          int
	createdAt         time.Time
}

func (w *auditSettlementImportWorker) processBatch(ctx context.Context) (int, error) {
	// A process crash after financial admission has an unknown outcome. Never
	// restart that debit; committed transactions already have a terminal status.
	if _, err := w.db.ExecContext(ctx, `UPDATE audit_settlement_imports SET status='failed',reason='settlement_interrupted',settled_at=NOW()
 WHERE status='pending' AND charge_started_at IS NOT NULL AND claimed_at<NOW()-INTERVAL '60 seconds'`); err != nil {
		return 0, err
	}

	rows, err := w.db.QueryContext(ctx, `WITH pending AS (
		SELECT id FROM audit_settlement_imports WHERE status='pending' AND charge_started_at IS NULL AND available_at<=NOW()
		AND (claimed_at IS NULL OR claimed_at<NOW()-INTERVAL '60 seconds') ORDER BY available_at,id LIMIT 8 FOR UPDATE SKIP LOCKED
	) UPDATE audit_settlement_imports i SET claimed_at=NOW(),claimed_by=$1,attempts=i.attempts+1
	FROM pending p WHERE i.id=p.id RETURNING i.id,i.account_id,i.upstream_identity,i.receipt_id::text,i.attempts,i.created_at`, w.owner)
	if err != nil {
		return 0, err
	}
	var items []auditImportPending
	for rows.Next() {
		var p auditImportPending
		if err = rows.Scan(&p.id, &p.accountID, &p.identity, &p.receipt, &p.attempts, &p.createdAt); err != nil {
			_ = rows.Close()
			return 0, err
		}
		items = append(items, p)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return 0, err
	}
	// Bound outbound concurrency; each request keeps the existing proxy router.
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, p := range items {
		sem <- struct{}{}
		wg.Add(1)
		go func(p auditImportPending) { defer wg.Done(); defer func() { <-sem }(); w.processOne(ctx, p) }(p)
	}
	wg.Wait()
	return len(items), nil
}

func (w *auditSettlementImportWorker) finish(ctx context.Context, p auditImportPending, status, reason string) {
	_, err := w.db.ExecContext(ctx, `UPDATE audit_settlement_imports SET status=$3,reason=$4,settled_at=NOW(),amount=0
		WHERE id=$1 AND claimed_by=$2 AND status='pending'`, p.id, w.owner, status, reason)
	if err != nil {
		slog.Warn("audit_settlement.import_evidence_unavailable")
	}
}

func (w *auditSettlementImportWorker) retry(ctx context.Context, p auditImportPending, reason string) {
	if time.Since(p.createdAt) >= 23*time.Hour || p.attempts >= 100 {
		w.finish(ctx, p, "unavailable", "receipt_timeout")
		return
	}
	delay := time.Duration(1<<min(p.attempts, 9)) * time.Second
	_, err := w.db.ExecContext(ctx, `UPDATE audit_settlement_imports SET reason=$3,available_at=NOW()+($4*INTERVAL '1 second'),claimed_at=NULL,claimed_by=NULL
		WHERE id=$1 AND claimed_by=$2 AND status='pending'`, p.id, w.owner, reason, int(delay.Seconds()))
	if err != nil {
		slog.Warn("audit_settlement.import_retry_unavailable")
	}
}

func (w *auditSettlementImportWorker) processOne(ctx context.Context, p auditImportPending) {
	if time.Since(p.createdAt) >= 23*time.Hour || p.attempts > 100 {
		w.finish(ctx, p, "unavailable", "receipt_timeout")
		return
	}
	account, err := w.accounts.GetByID(ctx, p.accountID)
	if err != nil {
		w.retry(ctx, p, "account_unavailable")
		return
	}
	identity, base, enabled := auditSettlementAccountIdentity(account)
	if !enabled {
		w.finish(ctx, p, "skipped", "account_settlement_disabled")
		return
	}
	if identity != p.identity {
		w.finish(ctx, p, "unavailable", "upstream_identity_changed")
		return
	}
	// Same URL allowlist validation as model forwarding; redirects are forbidden.
	base, err = (&OpenAIGatewayService{cfg: w.cfg}).validateOutboundURL(base)
	if err != nil {
		w.finish(ctx, p, "unavailable", "upstream_url_invalid")
		return
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			w.retry(ctx, p, "proxy_unavailable")
			return
		}
		proxyURL = account.Proxy.URL()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	requestCtx = WithHTTPUpstreamRedirectsDisabled(WithAccountProxyRouting(requestCtx))
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, buildOpenAIEndpointURL(base, "/v1/sub2api/audit-settlements/"+p.receipt), nil)
	if err != nil {
		w.finish(ctx, p, "unavailable", "upstream_url_invalid")
		return
	}
	req.Header.Set("Authorization", "Bearer "+account.GetCredential("api_key"))
	req.Header.Set("Accept", "application/json")
	resp, err := w.upstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		w.retry(ctx, p, "receipt_transport_failed")
		return
	}
	if resp == nil || resp.Body == nil {
		w.retry(ctx, p, "receipt_empty")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		w.retry(ctx, p, "receipt_pending")
		return
	}
	if resp.StatusCode != http.StatusOK {
		w.finish(ctx, p, "unavailable", "receipt_http_rejected")
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil {
		w.retry(ctx, p, "receipt_read_failed")
		return
	}
	if len(body) > 64*1024 {
		w.finish(ctx, p, "unavailable", "receipt_invalid")
		return
	}
	status, amount, err := parseAuditSettlementReceipt(body, p.receipt)
	if err != nil {
		w.finish(ctx, p, "unavailable", "receipt_invalid")
		return
	}
	if status == "pending" {
		w.retry(ctx, p, "receipt_pending")
		return
	}
	if status != "ready" {
		w.finish(ctx, p, "unavailable", "upstream_unavailable")
		return
	}
	// Commit the financial admission marker before starting the money transaction.
	armed, armErr := w.db.ExecContext(ctx, `UPDATE audit_settlement_imports SET charge_started_at=NOW()
 WHERE id=$1 AND claimed_by=$2 AND status='pending' AND charge_started_at IS NULL`, p.id, w.owner)
	if armErr != nil {
		return
	}
	armedCount, armErr := armed.RowsAffected()
	if armErr != nil || armedCount != 1 {
		return
	}
	var settled string
	if err = w.db.QueryRowContext(ctx, `SELECT settle_audit_settlement_import($1,$2,$3::numeric)`, p.id, w.owner, amount).Scan(&settled); err != nil {
		// An unknown commit result must never be replayed as a second charge.
		// If it committed the terminal-row predicate makes this a no-op.
		failureCtx, failureCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer failureCancel()
		w.finish(failureCtx, p, "failed", "settlement_commit_unknown")
	}
}

var auditSettlementDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,6})\.[0-9]{8}$`)

func auditSettlementUnits(value string) (int64, error) {
	if !auditSettlementDecimal.MatchString(value) {
		return 0, errors.New("invalid settlement decimal")
	}
	units, err := strconv.ParseInt(strings.ReplaceAll(value, ".", ""), 10, 64)
	if err != nil || units > 1000000*100000000 {
		return 0, errors.New("settlement amount exceeds limit")
	}
	return units, nil
}

func parseAuditSettlementReceipt(body []byte, receipt string) (status, amount string, err error) {
	var result struct {
		Object   string  `json:"object"`
		Version  int     `json:"schema_version"`
		Receipt  string  `json:"receipt_id"`
		Status   string  `json:"status"`
		Currency string  `json:"currency"`
		Amount   *string `json:"additional_cost"`
		Items    []struct {
			ID     string `json:"id"`
			Amount string `json:"amount"`
		} `json:"items"`
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return "", "", err
	}
	invalid := errors.New("invalid settlement receipt")
	if result.Object != "sub2api.audit_settlement" || result.Version != 1 || result.Receipt != receipt || result.Currency != "USD" {
		return "", "", invalid
	}
	if result.Status == "pending" || result.Status == "unavailable" {
		if result.Amount != nil || len(result.Items) > 0 {
			return "", "", invalid
		}
		return result.Status, "", nil
	}
	if result.Status != "ready" || result.Amount == nil || len(result.Items) > 128 {
		return "", "", invalid
	}
	units, err := auditSettlementUnits(*result.Amount)
	if err != nil {
		return "", "", err
	}
	var sum int64
	seen := map[string]bool{}
	for _, item := range result.Items {
		if item.ID == "" || len(item.ID) > 200 || seen[item.ID] {
			return "", "", invalid
		}
		seen[item.ID] = true
		value, err := auditSettlementUnits(item.Amount)
		if err != nil || value <= 0 {
			return "", "", invalid
		}
		sum += value
		if sum > 1000000*100000000 {
			return "", "", invalid
		}
	}
	if sum != units {
		return "", "", invalid
	}
	return "ready", *result.Amount, nil
}
