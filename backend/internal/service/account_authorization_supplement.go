package service

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const authorizationSupplementBatchLimit = 200

type AuthorizationSupplementBatch struct {
	Accounts string `json:"accounts"`
}

type AuthorizationSupplementResult struct {
	Line      int    `json:"line"`
	Email     string `json:"email,omitempty"`
	AccountID int64  `json:"account_id,omitempty"`
	Status    string `json:"status"`
}

type AuthorizationSupplementBatchResult struct {
	Items     []AuthorizationSupplementResult `json:"items"`
	Saved     int                             `json:"saved"`
	Cancelled bool                            `json:"cancelled"`
}

type authorizationSupplementRow struct {
	result AuthorizationSupplementResult
	secret AuthorizationSecret
}

// Parse the entire batch before writing. A repeated valid email is never
// overwritten by a later row, including when the earlier row is invalid.
func parseAuthorizationSupplement(raw string) ([]authorizationSupplementRow, error) {
	if len(raw) > 2<<20 {
		return nil, infraerrors.BadRequest("AUTHORIZATION_BATCH_LIMIT", "每批最多200行，内容不得超过2 MiB")
	}
	rows := make([]authorizationSupplementRow, 0)
	seen := make(map[string]bool)
	for index, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(rows) >= authorizationSupplementBatchLimit {
			return nil, infraerrors.BadRequest("AUTHORIZATION_BATCH_LIMIT", "每批最多200行，内容不得超过2 MiB")
		}
		row := authorizationSupplementRow{result: AuthorizationSupplementResult{Line: index + 1}}
		parts := strings.Split(line, "----")
		if len(parts) != 3 {
			row.result.Status = "invalid_format"
			rows = append(rows, row)
			continue
		}
		email := strings.ToLower(strings.TrimSpace(parts[0]))
		addr, err := mail.ParseAddress(email)
		if err != nil || addr.Address != email || len(email) > 254 {
			row.result.Status = "invalid_email"
			rows = append(rows, row)
			continue
		}
		row.result.Email = email
		switch {
		case seen[email]:
			row.result.Status = "duplicate_email"
		case len(parts[1]) == 0 || len(parts[1]) > 1024:
			row.result.Status = "invalid_password"
		default:
			row.secret = AuthorizationSecret{Email: email, Password: parts[1], TwoFA: parts[2]}
			if validateAuthorizationSecret(&row.secret, true) != nil {
				row.result.Status = "invalid_twofa"
			}
		}
		seen[email] = true
		if row.result.Status != "" {
			row.secret = AuthorizationSecret{}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, infraerrors.BadRequest("AUTHORIZATION_BATCH_EMPTY", "请填写至少一行账号、密码和2FA密钥")
	}
	return rows, nil
}

// Match within the same history/account transaction used by single-account
// supplementation. Missing and ambiguous emails never create or pick an account.
func authorizationSupplementAccount(ctx context.Context, tx *sql.Tx, email string) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT a.id,`+authorizationSupportedSQL+` FROM accounts a WHERE a.deleted_at IS NULL AND a.platform='openai' AND `+authorizationEmailSQL+`=$1 ORDER BY a.id LIMIT 2 FOR UPDATE OF a`, email)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var id int64
	var supported bool
	count := 0
	for rows.Next() {
		if err = rows.Scan(&id, &supported); err != nil {
			return 0, err
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, sql.ErrNoRows
	}
	if count > 1 {
		return 0, infraerrors.Conflict("AUTHORIZATION_DUPLICATE_ACCOUNT", "存在多个同邮箱账号，请使用单账号补全")
	}
	if !supported {
		return 0, infraerrors.BadRequest("AUTHORIZATION_ACCOUNT_UNSUPPORTED", "该账号不支持密码授权巡检")
	}
	return id, nil
}

func (s *AccountAuthorizationService) SupplementBatch(ctx context.Context, input *AuthorizationSupplementBatch) (*AuthorizationSupplementBatchResult, error) {
	rows, err := parseAuthorizationSupplement(input.Accounts)
	input.Accounts = ""
	if err != nil {
		return nil, err
	}
	defer func() {
		for i := range rows {
			rows[i].secret = AuthorizationSecret{}
		}
	}()
	if err = s.requireAuthorizationEncryption(); err != nil {
		return nil, err
	}
	result := &AuthorizationSupplementBatchResult{Items: make([]AuthorizationSupplementResult, 0, len(rows))}
	for i := range rows {
		row := &rows[i]
		if row.result.Status == "" {
			if ctx.Err() != nil {
				row.result.Status = "cancelled"
				result.Cancelled = true
			} else {
				rowCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				err = s.supplementAuthorization(rowCtx, 0, &row.secret, &row.result.AccountID)
				cancel()
				row.result.Status = authorizationSupplementStatus(err)
				if row.result.Status == "saved" {
					result.Saved++
				}
				if ctx.Err() != nil {
					result.Cancelled = true
				}
			}
		}
		row.secret = AuthorizationSecret{}
		result.Items = append(result.Items, row.result)
	}
	return result, nil
}

// Only stable codes reach the browser. Database, cipher, and driver messages can
// contain sensitive input and must never be used as per-row error text.
func authorizationSupplementStatus(err error) string {
	switch {
	case err == nil:
		return "saved"
	case errors.Is(err, sql.ErrNoRows):
		return "not_found"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "interrupted"
	}
	switch infraerrors.Reason(err) {
	case "AUTHORIZATION_DUPLICATE_ACCOUNT":
		return "ambiguous_email"
	case "AUTHORIZATION_ACCOUNT_UNSUPPORTED":
		return "unsupported_account"
	case "AUTHORIZATION_EMAIL_CHANGED":
		return "account_changed"
	default:
		return "failed"
	}
}
