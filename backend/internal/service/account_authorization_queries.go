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
const authorizationExpiredSQL = authorizationErrorSQL

// All list projections are explicit: ciphertext, passwords, OTP seeds, proxy auth,
// tokens and OAuth callbacks can never leak through these endpoints.
func (s *AccountAuthorizationService) ListAuthorization(ctx context.Context, kind, search, status string, page, size int) (map[string]any, error) {
	if page < 1 || page > 1000000 || size < 1 || size > 100 || len(search) > 254 {
		return nil, errors.New("分页或搜索参数无效")
	}
	var projection, order string
	switch kind {
	case "history":
		projection = `SELECT h.id,h.email,h.has_twofa,h.source,h.created_at,md5(h.secret_encrypted||h.source||(extract(epoch FROM h.created_at))::text) AS revision,CASE WHEN verdict.status='banned' THEN 'banned' ELSE 'saved' END AS status,CASE WHEN verdict.status='banned' THEN verdict.message ELSE '' END AS message FROM (` + authorizationCurrentHistorySQL + `) h LEFT JOIN LATERAL(SELECT status,message FROM pk_authorization_jobs j WHERE j.email=h.email AND (j.status IN ('banned','completed') OR j.settings->>'authorization_completed'='true') ORDER BY id DESC LIMIT 1)verdict ON true`
		order = "created_at DESC,id DESC"
	case "proxies":
		projection = `SELECT id,endpoint AS email,endpoint,enabled,last_checked_at,last_status AS status,last_message AS message,created_at FROM pk_authorization_proxies WHERE enabled=true`
		order = "id DESC"
	case "jobs":
		projection = `SELECT id,batch_id,email,account_id,source,status,stage,message,created_at,started_at,finished_at,COALESCE(settings->'final_group_names','[]'::jsonb) AS groups,settings->'intelligence_passed' AS intelligence_passed,settings->'intelligence_answer' AS intelligence_answer,` + authorizationIntelligenceResultsSQL("settings", false) + ` AS intelligence_results,COALESCE((settings->>'authorization_completed')::boolean,false) AS authorization_completed,settings->'retry_of_job_id' AS retry_of_job_id,settings->'immediate_authorization' AS immediate_authorization,settings->'authorization_proxy_id' AS authorization_proxy_id,settings->'authorization_proxy_scanned' AS authorization_proxy_scanned,settings->'authorization_proxy_passed' AS authorization_proxy_passed,settings->'authorization_proxy_limit' AS authorization_proxy_limit FROM pk_authorization_jobs`
		order = "id DESC"
	case "accounts":
		projection = `SELECT a.id,a.name,` + authorizationEmailSQL + ` AS email,a.type,a.status AS account_status,
   CASE WHEN NOT ` + authorizationSupportedSQL + ` THEN 'unsupported'
    WHEN j.status='running' THEN 'authorizing' WHEN j.status='queued' THEN 'queued'
    WHEN st.status='banned' AND st.credential_version=md5(a.credentials::text) THEN 'banned'
    WHEN ` + authorizationExpiredSQL + ` THEN CASE WHEN COALESCE(h.has_twofa,false)=false THEN 'missing_credentials' WHEN h.source IN ('supplement','updated') AND h.created_at>COALESCE(st.authorized_at,'epoch'::timestamptz) AND (j.created_at IS NULL OR j.created_at<h.created_at) THEN 'credentials_completed' ELSE 'expired' END
    WHEN st.credential_version=md5(a.credentials::text) AND st.status='cache_pending' THEN 'cache_pending'
    WHEN a.status='active' THEN 'authorized' WHEN a.status='error' THEN 'error' ELSE 'unknown' END AS status,
   CASE WHEN st.credential_version=md5(a.credentials::text) THEN st.message ELSE '' END AS message,
   st.checked_at,st.authorized_at,st.http_status,
   COALESCE(h.has_twofa,false) AS has_history,
   j.id AS job_id,CASE WHEN j.status IN ('queued','running') OR (j.status IN ('failed','needs_input','banned') AND (` + authorizationExpiredSQL + ` OR st.status='banned')) THEN j.message ELSE '' END AS job_message,
   COALESCE((SELECT jsonb_agg(g.name ORDER BY g.id) FROM account_groups ag JOIN groups g ON g.id=ag.group_id AND g.deleted_at IS NULL WHERE ag.account_id=a.id),'[]') AS groups
   FROM accounts a LEFT JOIN pk_authorization_state st ON st.account_id=a.id
   LEFT JOIN (` + authorizationCurrentHistorySQL + `) h ON h.email=` + authorizationEmailSQL + `
   LEFT JOIN LATERAL (SELECT id,history_id,status,message,created_at FROM pk_authorization_jobs WHERE email=` + authorizationEmailSQL + ` AND (account_id=a.id OR account_id IS NULL) ORDER BY id DESC LIMIT 1) j ON true
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
