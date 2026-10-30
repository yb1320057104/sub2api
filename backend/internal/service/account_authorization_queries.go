package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const authorizationEmailSQL = `lower(trim(COALESCE(NULLIF(a.credentials->>'email',''),NULLIF(a.extra->>'email_address',''),NULLIF(a.extra->>'email',''),'')))`
const authorizationSupportedSQL = `(a.type='oauth' AND a.parent_account_id IS NULL AND COALESCE(a.extra->>'synthetic_ui_test','false')<>'true' AND lower(trim(COALESCE(a.credentials->>'auth_mode',''))) NOT IN ('agentidentity','agent_identity','personalaccesstoken','personal_access_token') AND lower(trim(COALESCE(a.credentials->>'openai_auth_mode',''))) NOT IN ('personalaccesstoken','personal_access_token') AND COALESCE(a.credentials->>'access_token','') NOT LIKE 'at-%')`
const authorizationErrorSQL = `(a.status='error' AND EXISTS(SELECT 1 FROM pk_authorization_config cfg,jsonb_array_elements_text(cfg.reauth_status_codes) code WHERE cfg.id=1 AND COALESCE(a.error_message,'') ~ ('(^|[^0-9])'||code||'([^0-9]|$)')))`
const authorizationExpiredSQL = `(` + authorizationErrorSQL + ` OR (st.status='expired' AND st.credential_version=md5(a.credentials::text) AND EXISTS(SELECT 1 FROM pk_authorization_config cfg,jsonb_array_elements_text(cfg.reauth_status_codes) code WHERE cfg.id=1 AND code::int=st.http_status)))`

// All list projections are explicit: ciphertext, passwords, OTP seeds, proxy auth,
// tokens and OAuth callbacks can never leak through these endpoints.
func (s *AccountAuthorizationService) ListAuthorization(ctx context.Context, kind, search, status string, page, size int) (map[string]any, error) {
	if page < 1 || page > 1000000 || size < 1 || size > 100 || len(search) > 254 {
		return nil, errors.New("分页或搜索参数无效")
	}
	var projection, order string
	switch kind {
	case "history":
		projection = `SELECT h.id,h.email,h.has_twofa,h.source,h.created_at,CASE WHEN verdict.status='banned' THEN 'banned' ELSE 'saved' END AS status,CASE WHEN verdict.status='banned' THEN verdict.message ELSE '' END AS message FROM pk_authorization_history h LEFT JOIN LATERAL(SELECT status,message FROM pk_authorization_jobs j WHERE j.email=h.email AND j.status IN ('banned','completed') ORDER BY id DESC LIMIT 1)verdict ON true`
		order = "id DESC"
	case "proxies":
		projection = `SELECT id,endpoint AS email,endpoint,enabled,last_checked_at,last_status AS status,last_message AS message,created_at FROM pk_authorization_proxies WHERE enabled=true`
		order = "id DESC"
	case "jobs":
		projection = `SELECT id,batch_id,email,account_id,source,status,stage,message,settings,created_at,started_at,finished_at FROM pk_authorization_jobs`
		order = "id DESC"
	case "accounts":
		projection = `SELECT a.id,a.name,` + authorizationEmailSQL + ` AS email,a.type,a.status AS account_status,
   CASE WHEN NOT ` + authorizationSupportedSQL + ` THEN 'unsupported'
    WHEN j.status='running' THEN 'authorizing' WHEN j.status='queued' THEN 'queued'
    WHEN st.status='banned' AND st.credential_version=md5(a.credentials::text) THEN 'banned'
    WHEN ` + authorizationExpiredSQL + ` THEN CASE WHEN COALESCE(h.has_twofa,false)=false THEN 'missing_credentials' WHEN h.source='supplement' AND h.created_at>COALESCE(st.authorized_at,'epoch'::timestamptz) AND (j.history_id IS NULL OR j.history_id<>h.id) THEN 'credentials_completed' ELSE 'expired' END
    WHEN st.credential_version=md5(a.credentials::text) THEN CASE WHEN st.status='expired' THEN 'error' ELSE st.status END ELSE 'unknown' END AS status,
   CASE WHEN st.credential_version=md5(a.credentials::text) THEN st.message ELSE '' END AS message,
   st.checked_at,st.authorized_at,st.http_status,
   COALESCE(h.has_twofa,false) AS has_history,
   j.id AS job_id,CASE WHEN j.status IN ('queued','running') OR (j.status IN ('failed','needs_input','banned') AND (` + authorizationExpiredSQL + ` OR st.status='banned')) THEN j.message ELSE '' END AS job_message,
   COALESCE((SELECT jsonb_agg(g.name ORDER BY g.id) FROM account_groups ag JOIN groups g ON g.id=ag.group_id AND g.deleted_at IS NULL WHERE ag.account_id=a.id),'[]') AS groups
   FROM accounts a LEFT JOIN pk_authorization_state st ON st.account_id=a.id
   LEFT JOIN LATERAL (SELECT id,has_twofa,source,created_at FROM pk_authorization_history WHERE email=` + authorizationEmailSQL + ` ORDER BY id DESC LIMIT 1) h ON true
   LEFT JOIN LATERAL (SELECT id,history_id,status,message FROM pk_authorization_jobs WHERE email=` + authorizationEmailSQL + ` AND (account_id=a.id OR account_id IS NULL) ORDER BY id DESC LIMIT 1) j ON true
   WHERE a.deleted_at IS NULL AND a.platform='openai'`
		order = "id DESC"
	default:
		return nil, errors.New("列表类型无效")
	}
	search = strings.TrimSpace(search)
	query := ` FROM (` + projection + `) x WHERE ($1='' OR position(lower($1) in lower(x.email))>0) AND ($2='' OR $2='all' OR x.status=$2)`
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT count(*)"+query, search, status).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT to_jsonb(x)"+query+fmt.Sprintf(" ORDER BY %s LIMIT $3 OFFSET $4", order), search, status, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]json.RawMessage, 0)
	for rows.Next() {
		var item json.RawMessage
		if err = rows.Scan(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, nil
}
