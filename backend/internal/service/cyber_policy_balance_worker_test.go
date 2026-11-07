package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type cyberBalanceCacheStub struct {
	err error
	ids []int64
}

func (c *cyberBalanceCacheStub) InvalidateUserBalance(_ context.Context, id int64) error {
	c.ids = append(c.ids, id)
	return c.err
}

func TestCyberPolicyBalanceWorkerRetriesAndUsesRevisionBoundAck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage int
		err   error
		query string
	}{
		{"retry-cache-failure", 0, errors.New("redis unavailable"), `UPDATE cyber_policy_auto_charge_outbox SET claimed_at=NULL`},
		{"delayed-second-pass", 0, nil, `UPDATE cyber_policy_auto_charge_outbox SET stage=1`},
		{"ack-second-pass", 1, nil, `DELETE FROM cyber_policy_auto_charge_outbox`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			cache := &cyberBalanceCacheStub{err: tc.err}
			w := newCyberPolicyBalanceWorker(db, cache)
			defer w.Stop()
			mock.ExpectQuery(`WITH pending AS`).WithArgs(w.owner).
				WillReturnRows(sqlmock.NewRows([]string{"user_id", "revision", "stage"}).AddRow(7, 13, tc.stage)).RowsWillBeClosed()
			mock.ExpectExec(tc.query+`[\s\S]*WHERE user_id=\$1 AND revision=\$2 AND claimed_by=\$3`).
				WithArgs(int64(7), int64(13), w.owner).WillReturnResult(sqlmock.NewResult(0, 0))
			require.NoError(t, w.processBatch(context.Background()))
			require.Equal(t, []int64{7}, cache.ids)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAuditSettlementImportReceiptValidation(t *testing.T) {
	receipt := "2561ce22-94f1-44bc-a2a5-984c29ee5f49"
	base := func() map[string]any {
		return map[string]any{"object": "sub2api.audit_settlement", "schema_version": 1, "receipt_id": receipt, "status": "ready", "currency": "USD", "additional_cost": "0.30000000", "items": []map[string]string{{"id": "local:1", "amount": "0.10000000"}, {"id": "import:2", "amount": "0.20000000"}}}
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		valid  bool
	}{
		{"exact decimal sum", func(m map[string]any) {}, true},
		{"pending is not settled zero", func(m map[string]any) { m["status"] = "pending"; delete(m, "additional_cost"); delete(m, "items") }, true},
		{"zero cost", func(m map[string]any) { m["additional_cost"] = "0.00000000"; m["items"] = []any{} }, true},
		{"wrong receipt", func(m map[string]any) { m["receipt_id"] = "another" }, false},
		{"wrong currency", func(m map[string]any) { m["currency"] = "CNY" }, false},
		{"sum mismatch", func(m map[string]any) { m["additional_cost"] = "0.40000000" }, false},
		{"pending with cost", func(m map[string]any) { m["status"] = "pending" }, false},
		{"duplicate item", func(m map[string]any) {
			m["items"] = []map[string]string{{"id": "a", "amount": "0.10000000"}, {"id": "a", "amount": "0.20000000"}}
		}, false},
		{"negative", func(m map[string]any) { m["additional_cost"] = "-0.30000000" }, false},
		{"exponent", func(m map[string]any) { m["additional_cost"] = "3e-1" }, false},
		{"float instead of decimal", func(m map[string]any) { m["additional_cost"] = 0.3 }, false},
		{"unbounded", func(m map[string]any) { m["additional_cost"] = "1000000.00000001" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			tc.mutate(m)
			body, err := json.Marshal(m)
			require.NoError(t, err)
			_, _, err = parseAuditSettlementReceipt(body, receipt)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestAuditSettlementImportNegotiationRequiresTrustAndExactEndpoint(t *testing.T) {
	account := &Account{ID: 12, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://relay.example/v1", "api_key": "fixture"}, Extra: map[string]any{AuditSettlementEnabledExtraKey: true}}
	for _, tc := range []struct {
		name, url, method string
		enabled, expect   bool
	}{
		{"responses", "https://relay.example/v1/responses", "POST", true, true},
		{"messages", "https://relay.example/v1/messages", "POST", true, true},
		{"messages beta", "https://relay.example/v1/messages?beta=true", "POST", true, true},
		{"messages arbitrary query", "https://relay.example/v1/messages?beta=true&route=other", "POST", true, false},
		{"responses arbitrary query", "https://relay.example/v1/responses?beta=true", "POST", true, false},
		{"chat", "https://relay.example/v1/chat/completions", "POST", true, true},
		{"native disabled", "https://relay.example/v1/responses", "POST", false, false},
		{"other origin", "https://other.example/v1/responses", "POST", true, false},
		{"images", "https://relay.example/v1/images/generations", "POST", true, false},
		{"query route", "https://relay.example/v1/sub2api/audit-settlements/x", "GET", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account.Extra[AuditSettlementEnabledExtraKey] = tc.enabled
			req, err := http.NewRequest(tc.method, tc.url, nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer fixture")
			req.Header.Set(auditImportCapabilityHeader, "attacker")
			req.Header.Set(auditImportReceiptHeader, "attacker")
			prepareAuditSettlementRequest(req, account)
			require.Equal(t, tc.expect, req.Header.Get(auditImportCapabilityHeader) == "v1")
			require.Empty(t, req.Header.Get(auditImportReceiptHeader))
		})
	}
}

type auditImportAccountStub struct {
	AccountRepository
	account *Account
}

func (r auditImportAccountStub) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

type auditImportTransportStub struct {
	t     *testing.T
	body  string
	calls int
}

func (s *auditImportTransportStub) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	s.calls++
	require.True(s.t, HTTPUpstreamRedirectsDisabled(req.Context()))
	require.Equal(s.t, "https://relay.example/v1/sub2api/audit-settlements/2561ce22-94f1-44bc-a2a5-984c29ee5f49", req.URL.String())
	require.Equal(s.t, "Bearer fixture", req.Header.Get("Authorization"))
	require.Equal(s.t, int64(12), id)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(s.body))}, nil
}
func (s *auditImportTransportStub) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxy, id, concurrency)
}

func TestAuditSettlementImportWorkerOnlySettlesVerifiedReadyAmount(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		changed      bool
	}{{"ready", "ready", false}, {"pending", "pending", false}, {"unavailable", "unavailable", false}, {"identity changed", "ready", true}} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			a := &Account{ID: 12, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://relay.example/v1", "api_key": "fixture"}, Extra: map[string]any{AuditSettlementEnabledExtraKey: true}}
			identity, _, ok := auditSettlementAccountIdentity(a)
			require.True(t, ok)
			receipt := "2561ce22-94f1-44bc-a2a5-984c29ee5f49"
			m := map[string]any{"object": "sub2api.audit_settlement", "schema_version": 1, "receipt_id": receipt, "currency": "USD", "status": tc.status}
			if tc.status == "ready" {
				m["additional_cost"] = "0.30000000"
				m["items"] = []map[string]string{{"id": "local:1", "amount": "0.30000000"}}
			}
			body, err := json.Marshal(m)
			require.NoError(t, err)
			transport := &auditImportTransportStub{t: t, body: string(body)}
			w := newAuditSettlementImportWorker(db, auditImportAccountStub{account: a}, transport, nil)
			defer w.Stop()
			p := auditImportPending{id: 8, accountID: 12, identity: identity, receipt: receipt, attempts: 1, createdAt: time.Now()}
			switch {
			case tc.changed:
				a.Credentials["api_key"] = "changed"
				mock.ExpectExec(`UPDATE audit_settlement_imports SET status`).WithArgs(p.id, w.owner, "unavailable", "upstream_identity_changed").WillReturnResult(sqlmock.NewResult(0, 1))
			case tc.status == "ready":
				mock.ExpectExec(`UPDATE audit_settlement_imports SET charge_started_at`).WithArgs(p.id, w.owner).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`SELECT settle_audit_settlement_import`).WithArgs(p.id, w.owner, "0.30000000").WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("deducted"))
			case tc.status == "pending":
				mock.ExpectExec(`UPDATE audit_settlement_imports SET reason`).WithArgs(p.id, w.owner, "receipt_pending", 2).WillReturnResult(sqlmock.NewResult(0, 1))
			default:
				mock.ExpectExec(`UPDATE audit_settlement_imports SET status`).WithArgs(p.id, w.owner, "unavailable", "upstream_unavailable").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			w.processOne(context.Background(), p)
			require.NoError(t, mock.ExpectationsWereMet())
			if tc.changed {
				require.Zero(t, transport.calls)
			} else {
				require.Equal(t, 1, transport.calls)
			}
		})
	}
}

func TestAuditSettlementImportRejectsCredentialOverride(t *testing.T) {
	a := &Account{ID: 12, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://relay.example/v1", "api_key": "fixture"}, Extra: map[string]any{AuditSettlementEnabledExtraKey: true}}
	req, err := http.NewRequest("POST", "https://relay.example/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer another-key")
	prepareAuditSettlementRequest(req, a)
	require.Empty(t, req.Header.Get(auditImportCapabilityHeader))
	require.False(t, auditSettlementWasNegotiated(&http.Response{Request: req}))
	req.Header.Set("Authorization", "Bearer fixture")
	prepareAuditSettlementRequest(req, a)
	require.True(t, auditSettlementWasNegotiated(&http.Response{Request: req}))
	require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
}
