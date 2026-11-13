package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const ZhongcaiImportProtocol = "zhongcai-import-v1"
const ZhongcaiImportMaxBody = 256 * 1024

type ScopedZhongcaiImports interface {
	ScopedZhongcaiImport(context.Context, []byte) (*ZhongcaiImportReceipt, error)
	ScopedZhongcaiReceipt(context.Context, string, string) (*ZhongcaiImportReceipt, error)
}

type ZhongcaiImportAccount struct {
	ID            int64                      `json:"id"`
	Platform      string                     `json:"platform"`
	Type          string                     `json:"type"`
	Concurrency   int                        `json:"concurrency"`
	ProxyID       *int64                     `json:"proxy_id"`
	ProxyBindings []AccountProxyBindingInput `json:"proxy_bindings"`
}

type ZhongcaiImportReceipt struct {
	Protocol      string                `json:"protocol"`
	SourceID      string                `json:"source_id"`
	OperationID   string                `json:"operation_id"`
	RequestSHA256 string                `json:"request_sha256"`
	AccountID     int64                 `json:"account_id"`
	Platform      string                `json:"platform"`
	Type          string                `json:"type"`
	State         string                `json:"state"`
	Account       ZhongcaiImportAccount `json:"account"`
}

type zhongcaiImportRequest struct {
	requiresNetwork bool
	SourceID        string `json:"source_id"`
	OperationID     string `json:"operation_id"`
	Account         struct {
		Name        string `json:"name"`
		Platform    string `json:"platform"`
		Type        string `json:"type"`
		Credentials struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			Email        string `json:"email"`
			AccountID    string `json:"chatgpt_account_id"`
			UserID       string `json:"chatgpt_user_id"`
			ExpiresAt    *int64 `json:"expires_at"`
		} `json:"credentials"`
		GroupIDs    []int64 `json:"group_ids"`
		Concurrency *int    `json:"concurrency"`
		Priority    *int    `json:"priority"`
		ProxyID     *int64  `json:"proxy_id"`
		Extra       struct {
			ImportID string `json:"zhongcai_import_id"`
			Source   string `json:"zhongcai_source"`
		} `json:"extra"`
	} `json:"account"`
}

func zhongcaiInputError() error {
	return infraerrors.BadRequest("ZHONGCAI_IMPORT_INVALID", "导入参数无效")
}

func validZhongcaiUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

// Reject duplicate keys as well as unknown fields; raw bytes are the operation fingerprint.
func validateZhongcaiJSON(dec *json.Decoder, depth int) error {
	if depth > 8 {
		return zhongcaiInputError()
	}
	token, err := dec.Token()
	if err != nil {
		return zhongcaiInputError()
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			name, ok := key.(string)
			if err != nil || !ok || name != strings.ToLower(name) || seen[name] {
				return zhongcaiInputError()
			}
			seen[name] = true
			if err := validateZhongcaiJSON(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := validateZhongcaiJSON(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return zhongcaiInputError()
	}
	_, err = dec.Token()
	return err
}

func parseZhongcaiImport(raw []byte) (*zhongcaiImportRequest, *CreateAccountInput, error) {
	if len(raw) == 0 || len(raw) > ZhongcaiImportMaxBody || !utf8.Valid(raw) {
		return nil, nil, zhongcaiInputError()
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	if err := validateZhongcaiJSON(tokens, 0); err != nil {
		return nil, nil, zhongcaiInputError()
	}
	if _, err := tokens.Token(); err != io.EOF {
		return nil, nil, zhongcaiInputError()
	}
	req := new(zhongcaiImportRequest)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(req); err != nil {
		return nil, nil, zhongcaiInputError()
	}
	var presence struct {
		Account map[string]json.RawMessage `json:"account"`
	}
	if err := json.Unmarshal(raw, &presence); err != nil {
		return nil, nil, zhongcaiInputError()
	}
	_, req.requiresNetwork = presence.Account["proxy_id"]
	a, c := &req.Account, &req.Account.Credentials
	if !validZhongcaiUUID(req.SourceID) || !validZhongcaiUUID(req.OperationID) || a.Extra.ImportID != req.OperationID || a.Extra.Source != req.SourceID {
		return nil, nil, zhongcaiInputError()
	}
	if a.Platform != PlatformOpenAI || a.Type != AccountTypeOAuth || strings.TrimSpace(a.Name) == "" || utf8.RuneCountInString(a.Name) > maxAccountNameRunes || a.GroupIDs == nil || len(a.GroupIDs) > 1000 {
		return nil, nil, zhongcaiInputError()
	}
	if a.Concurrency == nil || *a.Concurrency < 0 || *a.Concurrency > 10000 || a.Priority == nil || *a.Priority < 0 {
		return nil, nil, zhongcaiInputError()
	}
	groups := map[int64]bool{}
	for _, id := range a.GroupIDs {
		if id <= 0 || groups[id] {
			return nil, nil, zhongcaiInputError()
		}
		groups[id] = true
	}
	for _, value := range []string{c.AccessToken, c.RefreshToken, c.Email, c.AccountID, c.UserID} {
		if strings.TrimSpace(value) == "" || len(value) > 65536 {
			return nil, nil, zhongcaiInputError()
		}
	}
	if len(c.IDToken) > 65536 || len(c.Email) > 320 || len(c.AccountID) > 256 || len(c.UserID) > 256 {
		return nil, nil, zhongcaiInputError()
	}
	if c.ExpiresAt != nil && (*c.ExpiresAt <= 0 || *c.ExpiresAt > 253402300799) {
		return nil, nil, zhongcaiInputError()
	}
	if _, err := NormalizeAccountProxyMode("", a.ProxyID, false); err != nil {
		return nil, nil, zhongcaiInputError()
	}
	credentials := map[string]any{"access_token": c.AccessToken, "refresh_token": c.RefreshToken, "email": c.Email, "chatgpt_account_id": c.AccountID, "chatgpt_user_id": c.UserID}
	if c.IDToken != "" {
		credentials["id_token"] = c.IDToken
	}
	if c.ExpiresAt != nil {
		credentials["expires_at"] = time.Unix(*c.ExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	input := &CreateAccountInput{Name: a.Name, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: credentials, GroupIDs: a.GroupIDs, Concurrency: *a.Concurrency, Priority: *a.Priority, ProxyID: a.ProxyID, SkipDefaultGroupBind: true}
	// Correlation is stored only in the immutable receipt, not in writable account extra.
	return req, input, nil
}

func zhongcaiSHA256(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func zhongcaiIdentity(credentials map[string]any) string {
	values := make([]string, 0, 3)
	for _, key := range []string{"email", "chatgpt_account_id", "chatgpt_user_id"} {
		value, _ := credentials[key].(string)
		values = append(values, value)
	}
	raw, _ := json.Marshal(values)
	return zhongcaiSHA256(raw)
}

func authorizeZhongcaiImport(ctx context.Context, resource adminaccess.Account, network bool) error {
	p := adminaccess.FromContext(ctx)
	permissions := []string{"accounts.create", "accounts.credentials", "accounts.schedule", "accounts.read"}
	if network {
		permissions = append(permissions, "accounts.network")
	}
	for _, permission := range permissions {
		if p == nil || !p.Policy.AllowsAccount(permission, resource) {
			return infraerrors.Forbidden("ZHONGCAI_IMPORT_FORBIDDEN", "导入权限不足")
		}
	}
	for _, group := range resource.GroupIDs {
		if !p.Policy.AllowsGroup("accounts.create", resource, group) || !p.Policy.AllowsGroup("accounts.read", resource, group) {
			return infraerrors.Forbidden("ZHONGCAI_IMPORT_FORBIDDEN", "导入分组未授权")
		}
	}
	return nil
}

func zhongcaiPrincipal(ctx context.Context) (*adminaccess.Principal, error) {
	p := adminaccess.FromContext(ctx)
	if p == nil || p.KeyID == "" || len(p.KeyID) > 128 {
		return nil, infraerrors.Forbidden("ZHONGCAI_IMPORT_FORBIDDEN", "导入未授权")
	}
	return p, nil
}

type zhongcaiStoredReceipt struct {
	digest, identity string
	accountID        int64
	network          bool
}

func loadZhongcaiReceipt(ctx context.Context, client *dbent.Client, key, source, operation string) (*zhongcaiStoredReceipt, error) {
	rows, err := client.QueryContext(ctx, `SELECT request_sha256, identity_sha256, account_id, requires_network FROM zhongcai_import_receipts WHERE key_id=$1 AND source_id=$2::uuid AND operation_id=$3::uuid`, key, source, operation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	result := new(zhongcaiStoredReceipt)
	if err := rows.Scan(&result.digest, &result.identity, &result.accountID, &result.network); err != nil {
		return nil, err
	}
	return result, rows.Err()
}

func (s *adminServiceImpl) zhongcaiReceiptAccount(ctx context.Context, client *dbent.Client, source, operation string, stored *zhongcaiStoredReceipt, replay bool) (*ZhongcaiImportReceipt, error) {
	// Account/group row locks stabilize authorization and identity until the receipt is formed.
	if _, err := client.ExecContext(ctx, `SELECT id FROM accounts WHERE id=$1 FOR SHARE`, stored.accountID); err != nil {
		return nil, err
	}
	if _, err := client.ExecContext(ctx, `SELECT account_id FROM account_groups WHERE account_id=$1 FOR SHARE`, stored.accountID); err != nil {
		return nil, err
	}
	account, err := s.accountRepo.GetByID(ctx, stored.accountID)
	if err != nil {
		if infraerrors.Code(err) == 404 {
			return nil, infraerrors.NotFound("ZHONGCAI_IMPORT_ACCOUNT_UNAVAILABLE", "原导入账号不可用")
		}
		return nil, err
	}
	p, err := zhongcaiPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if account == nil || !p.Policy.AllowsAccount("accounts.read", scopedAccountResource(account)) {
		return nil, infraerrors.Forbidden("ZHONGCAI_IMPORT_FORBIDDEN", "原导入账号不可访问")
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.IsShadow() || zhongcaiIdentity(account.Credentials) != stored.identity {
		return nil, infraerrors.Conflict("ZHONGCAI_IMPORT_ACCOUNT_CHANGED", "原导入账号身份已改变")
	}
	if replay {
		if err := authorizeZhongcaiImport(ctx, scopedAccountResource(account), stored.network); err != nil {
			return nil, err
		}
	}
	bindings := make([]AccountProxyBindingInput, 0, len(account.ProxyBindings))
	for _, binding := range account.ProxyBindings {
		bindings = append(bindings, AccountProxyBindingInput{ProxyID: binding.ProxyID, MaxConcurrency: binding.MaxConcurrency, MaxAccounts: binding.MaxAccounts})
	}
	return &ZhongcaiImportReceipt{Protocol: ZhongcaiImportProtocol, SourceID: source, OperationID: operation, RequestSHA256: stored.digest, AccountID: account.ID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, State: "succeeded", Account: ZhongcaiImportAccount{ID: account.ID, Platform: account.Platform, Type: account.Type, Concurrency: account.Concurrency, ProxyID: account.ProxyID, ProxyBindings: bindings}}, nil
}

func (s *adminServiceImpl) ScopedZhongcaiImport(ctx context.Context, raw []byte) (*ZhongcaiImportReceipt, error) {
	p, err := zhongcaiPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	req, input, err := parseZhongcaiImport(raw)
	if err != nil {
		return nil, err
	}
	network := req.requiresNetwork
	resource := adminaccess.Account{Platform: input.Platform, Type: input.Type, GroupIDs: input.GroupIDs}
	if err := authorizeZhongcaiImport(ctx, resource, network); err != nil {
		return nil, err
	}
	if s.entClient == nil {
		return nil, fmt.Errorf("external import transaction unavailable")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txctx, client := dbent.NewTxContext(ctx, tx), tx.Client()
	lock := "zhongcai-import:" + p.KeyID + ":" + req.SourceID + ":" + req.OperationID
	if _, err := client.ExecContext(txctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock); err != nil {
		return nil, err
	}
	stored, err := loadZhongcaiReceipt(txctx, client, p.KeyID, req.SourceID, req.OperationID)
	if err != nil {
		return nil, err
	}
	digest := zhongcaiSHA256(raw)
	created := false
	if stored != nil {
		if stored.digest != digest {
			return nil, infraerrors.Conflict("ZHONGCAI_IMPORT_CONFLICT", "同一导入操作的请求内容不一致")
		}
	} else {
		if input.ProxyID != nil && *input.ProxyID == 0 {
			input.ProxyID = nil
		}
		identity := zhongcaiIdentity(input.Credentials)
		account, err := s.CreateAccount(txctx, input)
		if err != nil {
			return nil, err
		}
		// Global OAuth initialization may change groups; validate its final result before committing.
		if err := authorizeZhongcaiImport(txctx, adminaccess.Account{ID: account.ID, Platform: account.Platform, Type: account.Type, GroupIDs: input.GroupIDs}, network); err != nil {
			return nil, err
		}
		stored = &zhongcaiStoredReceipt{digest: digest, identity: identity, accountID: account.ID, network: network}
		_, err = client.ExecContext(txctx, `INSERT INTO zhongcai_import_receipts(key_id,source_id,operation_id,request_sha256,identity_sha256,account_id,requires_network) VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7)`, p.KeyID, req.SourceID, req.OperationID, digest, identity, account.ID, network)
		if err != nil {
			return nil, err
		}
		created = true
	}
	result, err := s.zhongcaiReceiptAccount(txctx, client, req.SourceID, req.OperationID, stored, true)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if created {
		if repo, ok := s.accountRepo.(interface {
			ScopedAdminCommitted(context.Context, int64, bool)
		}); ok {
			repo.ScopedAdminCommitted(ctx, stored.accountID, false)
		}
	}
	return result, nil
}

func (s *adminServiceImpl) ScopedZhongcaiReceipt(ctx context.Context, source, operation string) (*ZhongcaiImportReceipt, error) {
	p, err := zhongcaiPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !validZhongcaiUUID(source) || !validZhongcaiUUID(operation) {
		return nil, zhongcaiInputError()
	}
	if s.entClient == nil {
		return nil, fmt.Errorf("external receipt transaction unavailable")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txctx, client := dbent.NewTxContext(ctx, tx), tx.Client()
	stored, err := loadZhongcaiReceipt(txctx, client, p.KeyID, source, operation)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, infraerrors.NotFound("ZHONGCAI_IMPORT_NOT_FOUND", "未找到此导入操作")
	}
	result, err := s.zhongcaiReceiptAccount(txctx, client, source, operation, stored, false)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
