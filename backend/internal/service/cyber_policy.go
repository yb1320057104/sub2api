package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// CyberPolicyService ranks upstream safety-policy usage and local audit blocks.
// Only request_type=4 usage is eligible for the existing manual deductions.
type CyberPolicyService struct {
	db              *sql.DB
	authInvalidator APIKeyAuthCacheInvalidator
	billingCache    *BillingCacheService
}

func NewCyberPolicyService(db *sql.DB, authInvalidator APIKeyAuthCacheInvalidator, billingCache *BillingCacheService) *CyberPolicyService {
	return &CyberPolicyService{db: db, authInvalidator: authInvalidator, billingCache: billingCache}
}

type CyberPolicySettings struct {
	Multiplier           string                 `json:"multiplier"`
	AutoDisableEnabled   bool                   `json:"auto_disable_enabled"`
	AutoDisableThreshold int64                  `json:"auto_disable_threshold"`
	ResetOnManualEnable  bool                   `json:"reset_on_manual_enable"`
	GroupIDs             []int64                `json:"group_ids"`
	UserAllowlistIDs     []int64                `json:"user_allowlist_ids"`
	GroupRules           []CyberPolicyGroupRule `json:"group_rules"`
	SettingsRevision     int64                  `json:"settings_revision"`
}

type CyberPolicySettingsInput struct {
	Multiplier           *string                 `json:"multiplier"`
	AutoDisableEnabled   *bool                   `json:"auto_disable_enabled"`
	AutoDisableThreshold *int64                  `json:"auto_disable_threshold"`
	ResetOnManualEnable  *bool                   `json:"reset_on_manual_enable"`
	GroupIDs             *[]int64                `json:"group_ids"`
	UserAllowlistIDs     *[]int64                `json:"user_allowlist_ids"`
	GroupRules           *[]CyberPolicyGroupRule `json:"group_rules"`
}

type CyberPolicyGroupRule struct {
	GroupID           int64  `json:"group_id"`
	Multiplier        string `json:"multiplier"`
	AutoDeductEnabled bool   `json:"auto_deduct_enabled"`
}

func normalizeCyberPolicyMultiplier(value string) (string, error) {
	normalized, err := normalizeBinanceCreditMultiplier(value)
	if err != nil {
		return "", infraerrors.BadRequest("CYBER_MULTIPLIER_INVALID", "扣除倍率必须为 0.000001 至 1000 的普通正小数，最多 6 位小数")
	}
	return normalized, nil
}

const cyberSettingsColumns = `multiplier::text,auto_disable_enabled,auto_disable_threshold,reset_on_manual_enable,group_ids,settings_revision,user_allowlist_ids,group_rules`

func scanCyberSettings(row interface{ Scan(...any) error }) (*CyberPolicySettings, error) {
	settings := &CyberPolicySettings{GroupIDs: []int64{}, UserAllowlistIDs: []int64{}, GroupRules: []CyberPolicyGroupRule{}}
	var groups, exemptUsers pq.Int64Array
	var rules []byte
	err := row.Scan(&settings.Multiplier, &settings.AutoDisableEnabled, &settings.AutoDisableThreshold, &settings.ResetOnManualEnable, &groups, &settings.SettingsRevision, &exemptUsers, &rules)
	settings.GroupIDs = append(settings.GroupIDs, groups...)
	settings.UserAllowlistIDs = append(settings.UserAllowlistIDs, exemptUsers...)
	if err == nil {
		err = json.Unmarshal(rules, &settings.GroupRules)
	}
	return settings, err
}

func (s *CyberPolicyService) Settings(ctx context.Context) (*CyberPolicySettings, error) {
	return scanCyberSettings(s.db.QueryRowContext(ctx, `SELECT `+cyberSettingsColumns+` FROM cyber_policy_settings WHERE id=1`))
}

func (s *CyberPolicyService) SaveSettings(ctx context.Context, input CyberPolicySettingsInput) (*CyberPolicySettings, error) {
	if input.Multiplier != nil {
		value, err := normalizeCyberPolicyMultiplier(*input.Multiplier)
		if err != nil {
			return nil, err
		}
		input.Multiplier = &value
	}
	if input.AutoDisableThreshold != nil && (*input.AutoDisableThreshold < 1 || *input.AutoDisableThreshold > 1000000000) {
		return nil, infraerrors.BadRequest("CYBER_THRESHOLD_INVALID", "自动禁用阈值必须为 1 至 1000000000 的整数")
	}
	var groupValue, exemptUserValue, ruleValue any
	var validateGroups []int64
	if input.GroupIDs != nil {
		if len(*input.GroupIDs) > 1000 {
			return nil, infraerrors.BadRequest("CYBER_GROUPS_INVALID", "检测分组最多 1000 个")
		}
		for _, id := range *input.GroupIDs {
			if id <= 0 {
				return nil, infraerrors.BadRequest("CYBER_GROUPS_INVALID", "检测分组 ID 必须为正整数")
			}
		}
		ids := normalizeInt64IDs(*input.GroupIDs)
		if ids == nil {
			ids = []int64{}
		}
		input.GroupIDs = &ids
		groupValue = pq.Array(ids)
		validateGroups = append(validateGroups, ids...)
	}
	if input.UserAllowlistIDs != nil {
		if len(*input.UserAllowlistIDs) > 1000 {
			return nil, infraerrors.BadRequest("CYBER_USERS_INVALID", "豁免用户最多 1000 个")
		}
		for _, id := range *input.UserAllowlistIDs {
			if id <= 0 {
				return nil, infraerrors.BadRequest("CYBER_USERS_INVALID", "豁免用户 ID 必须为正整数")
			}
		}
		ids := normalizeInt64IDs(*input.UserAllowlistIDs)
		input.UserAllowlistIDs = &ids
		exemptUserValue = pq.Array(ids)
	}
	if input.GroupRules != nil {
		if len(*input.GroupRules) > 1000 {
			return nil, infraerrors.BadRequest("CYBER_RULES_INVALID", "分组自动扣款规则最多 1000 条")
		}
		rules := append([]CyberPolicyGroupRule{}, (*input.GroupRules)...)
		seen := make(map[int64]bool, len(rules))
		for i := range rules {
			if rules[i].GroupID <= 0 || seen[rules[i].GroupID] {
				return nil, infraerrors.BadRequest("CYBER_RULES_INVALID", "自动扣款规则分组 ID 必须为正整数且不能重复")
			}
			seen[rules[i].GroupID] = true
			value, err := normalizeCyberPolicyMultiplier(rules[i].Multiplier)
			if err != nil {
				return nil, err
			}
			rules[i].Multiplier = value
			validateGroups = append(validateGroups, rules[i].GroupID)
		}
		sort.Slice(rules, func(i, j int) bool { return rules[i].GroupID < rules[j].GroupID })
		encoded, err := json.Marshal(rules)
		if err != nil {
			return nil, err
		}
		ruleValue = string(encoded)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if input.UserAllowlistIDs != nil && len(*input.UserAllowlistIDs) > 0 {
		// Lock user identities in a stable order before group/settings writes.
		// Disabled users remain selectable; this does not re-enable any user.
		rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE id=ANY($1::bigint[]) AND deleted_at IS NULL ORDER BY id FOR SHARE`, exemptUserValue)
		if err != nil {
			return nil, err
		}
		found := 0
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			found++
		}
		readErr := rows.Err()
		closeErr := rows.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if found != len(*input.UserAllowlistIDs) {
			return nil, infraerrors.BadRequest("CYBER_USERS_INVALID", "豁免用户不存在或已删除，请刷新用户后重试")
		}
	}
	validateGroups = normalizeInt64IDs(validateGroups)
	if len(validateGroups) > 0 {
		// Keep every selected group valid until the settings write commits.
		rows, err := tx.QueryContext(ctx, `SELECT id FROM groups WHERE id=ANY($1::bigint[]) AND deleted_at IS NULL AND status='active' ORDER BY id FOR SHARE`, pq.Array(validateGroups))
		if err != nil {
			return nil, err
		}
		found := 0
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			found++
		}
		readErr := rows.Err()
		closeErr := rows.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if found != len(validateGroups) {
			return nil, infraerrors.BadRequest("CYBER_GROUPS_INVALID", "检测分组不存在、已删除或已停用，请刷新分组后重试")
		}
	}
	settings, err := scanCyberSettings(tx.QueryRowContext(ctx, `UPDATE cyber_policy_settings SET multiplier=COALESCE($1::numeric,multiplier),auto_disable_enabled=COALESCE($2::boolean,auto_disable_enabled),auto_disable_threshold=COALESCE($3::bigint,auto_disable_threshold),reset_on_manual_enable=COALESCE($4::boolean,reset_on_manual_enable),group_ids=COALESCE($5::bigint[],group_ids),user_allowlist_ids=COALESCE($6::bigint[],user_allowlist_ids),group_rules=COALESCE($7::jsonb,group_rules),updated_at=NOW() WHERE id=1 RETURNING `+cyberSettingsColumns, input.Multiplier, input.AutoDisableEnabled, input.AutoDisableThreshold, input.ResetOnManualEnable, groupValue, exemptUserValue, ruleValue))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return settings, nil
}

type CyberPolicyWindowInput struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Timezone  string `json:"timezone"`
}

type CyberPolicyWindow struct {
	CyberPolicyWindowInput
	From time.Time `json:"-"`
	To   time.Time `json:"-"`
}

// Dates are inclusive in the UI, with a DST-safe half-open SQL interval.
func ParseCyberPolicyWindow(input CyberPolicyWindowInput, now time.Time) (CyberPolicyWindow, error) {
	loc := timezone.Location()
	if input.Timezone != "" {
		var err error
		loc, err = time.LoadLocation(input.Timezone)
		if err != nil {
			return CyberPolicyWindow{}, infraerrors.BadRequest("CYBER_TIMEZONE_INVALID", "时区无效")
		}
	}
	input.Timezone = loc.String()
	if input.StartDate == "" && input.EndDate == "" {
		input.StartDate = now.In(loc).Format("2006-01-02")
		input.EndDate = input.StartDate
	}
	from, err := time.ParseInLocation("2006-01-02", input.StartDate, loc)
	if err != nil {
		return CyberPolicyWindow{}, infraerrors.BadRequest("CYBER_DATE_INVALID", "开始日期必须为 YYYY-MM-DD")
	}
	end, err := time.ParseInLocation("2006-01-02", input.EndDate, loc)
	if err != nil || end.Before(from) {
		return CyberPolicyWindow{}, infraerrors.BadRequest("CYBER_DATE_INVALID", "结束日期无效或早于开始日期")
	}
	if end.After(from.AddDate(1, 0, 0)) {
		return CyberPolicyWindow{}, infraerrors.BadRequest("CYBER_DATE_INVALID", "单次统计时间范围最多一年")
	}
	return CyberPolicyWindow{CyberPolicyWindowInput: input, From: from, To: end.AddDate(0, 0, 1)}, nil
}

type CyberPolicyRank struct {
	Rank             int64  `json:"rank"`
	UserID           int64  `json:"user_id"`
	Email            string `json:"email"`
	Username         string `json:"username"`
	Balance          string `json:"balance"`
	TriggerCount     int64  `json:"trigger_count"`
	DeductedCount    int64  `json:"deducted_count"`
	PendingCount     int64  `json:"pending_count"`
	PendingAmount    string `json:"pending_amount"`
	Status           string `json:"status"`
	AutoDisableCount int64  `json:"auto_disable_count"`
	CountedCount     int64  `json:"counted_count"`
	SkippedCount     int64  `json:"skipped_count"`
	UnknownCount     int64  `json:"unknown_count"`
	AuditBlockCount  int64  `json:"audit_block_count"`
}

type CyberPolicyRanking struct {
	CyberPolicyWindowInput
	Items            []CyberPolicyRank `json:"items"`
	Total            int64             `json:"total"`
	TotalTriggers    int64             `json:"total_triggers"`
	TotalPending     int64             `json:"total_pending"`
	TotalCounted     int64             `json:"total_counted"`
	TotalSkipped     int64             `json:"total_skipped"`
	TotalUnknown     int64             `json:"total_unknown"`
	TotalAuditBlocks int64             `json:"total_audit_blocks"`
	Multiplier       string            `json:"multiplier"`
	Page             int               `json:"page"`
	PageSize         int               `json:"page_size"`
	GeneratedAt      time.Time         `json:"generated_at"`
}

// Public DTO intentionally contains neither contact details nor financial data.
type CyberPolicyPublicRank struct {
	Rank            int64  `json:"rank"`
	DisplayName     string `json:"display_name"`
	TriggerCount    int64  `json:"trigger_count"`
	CountedCount    int64  `json:"counted_count"`
	SkippedCount    int64  `json:"skipped_count"`
	UnknownCount    int64  `json:"unknown_count"`
	AuditBlockCount int64  `json:"audit_block_count"`
}
type CyberPolicyPublicRanking struct {
	CyberPolicyWindowInput
	Items            []CyberPolicyPublicRank `json:"items"`
	Total            int64                   `json:"total"`
	TotalTriggers    int64                   `json:"total_triggers"`
	TotalCounted     int64                   `json:"total_counted"`
	TotalSkipped     int64                   `json:"total_skipped"`
	TotalUnknown     int64                   `json:"total_unknown"`
	TotalAuditBlocks int64                   `json:"total_audit_blocks"`
	Page             int                     `json:"page"`
	PageSize         int                     `json:"page_size"`
	GeneratedAt      time.Time               `json:"generated_at"`
}

// Mask on the server for every public row, including the first three ranks.
// A fixed suffix does not reveal the remaining identifier's length.
func maskCyberPolicyIdentity(name string) string {
	if name == "" {
		return "用户***"
	}
	runes := []rune(name)
	if len(runes) > 3 {
		runes = runes[:3]
	}
	return string(runes) + "***"
}

func (s *CyberPolicyService) PublicRanking(ctx context.Context, input CyberPolicyWindowInput, page, size int) (*CyberPolicyPublicRanking, error) {
	window, err := ParseCyberPolicyWindow(input, time.Now())
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 100 {
		size = 100
	}
	if page > 1000000 {
		return nil, infraerrors.BadRequest("CYBER_PAGE_INVALID", "页码过大")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result := &CyberPolicyPublicRanking{CyberPolicyWindowInput: window.CyberPolicyWindowInput, Items: make([]CyberPolicyPublicRank, 0), Page: page, PageSize: size, GeneratedAt: time.Now()}
	const counts = cyberPublicRankingCTE
	if err = tx.QueryRowContext(ctx, counts+`SELECT COUNT(*),COALESCE(SUM(trigger_count),0),COALESCE(SUM(counted_count),0),COALESCE(SUM(skipped_count),0),COALESCE(SUM(unknown_count),0),COALESCE(SUM(audit_block_count),0) FROM counts`, window.From, window.To).Scan(&result.Total, &result.TotalTriggers, &result.TotalCounted, &result.TotalSkipped, &result.TotalUnknown, &result.TotalAuditBlocks); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, counts+`SELECT ROW_NUMBER() OVER(ORDER BY c.trigger_count DESC,c.user_id),COALESCE(NULLIF(u.username,''),u.email),c.trigger_count,c.counted_count,c.skipped_count,c.unknown_count,c.audit_block_count FROM counts c JOIN users u ON u.id=c.user_id ORDER BY c.trigger_count DESC,c.user_id LIMIT $3 OFFSET $4`, window.From, window.To, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item CyberPolicyPublicRank
		if err = rows.Scan(&item.Rank, &item.DisplayName, &item.TriggerCount, &item.CountedCount, &item.SkippedCount, &item.UnknownCount, &item.AuditBlockCount); err != nil {
			return nil, err
		}
		item.DisplayName = maskCyberPolicyIdentity(item.DisplayName)
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

const cyberCountColumns = `COUNT(*) FILTER (WHERE e.count_status='counted') AS counted_count,
 COUNT(*) FILTER (WHERE e.count_status='skipped') AS skipped_count,
 COUNT(*) FILTER (WHERE e.usage_id IS NULL) AS unknown_count`

const cyberAuditCountColumns = `COUNT(*) FILTER (WHERE b.count_status='counted') AS counted_count,
 COUNT(*) FILTER (WHERE b.count_status='skipped') AS skipped_count,
 0::bigint AS unknown_count`

const cyberPublicRankingCTE = `WITH source_counts AS (
 SELECT l.user_id,COUNT(*) AS trigger_count,` + cyberCountColumns + `,0::bigint AS audit_block_count
 FROM usage_logs l JOIN users u ON u.id=l.user_id AND u.deleted_at IS NULL
 LEFT JOIN cyber_policy_count_events e ON e.usage_id=l.id AND e.usage_created_at=l.created_at
 WHERE l.request_type=4 AND l.created_at >= $1 AND l.created_at < $2 GROUP BY l.user_id
 UNION ALL
 SELECT b.user_id,COUNT(*) AS trigger_count,` + cyberAuditCountColumns + `,COUNT(*) AS audit_block_count
 FROM cyber_policy_audit_blocks b JOIN users u ON u.id=b.user_id AND u.deleted_at IS NULL
 WHERE b.created_at >= $1 AND b.created_at < $2 GROUP BY b.user_id
), counts AS (
 SELECT user_id,SUM(trigger_count) AS trigger_count,SUM(counted_count) AS counted_count,
 SUM(skipped_count) AS skipped_count,SUM(unknown_count) AS unknown_count,SUM(audit_block_count) AS audit_block_count
 FROM source_counts GROUP BY user_id
) `

const cyberRankingCTE = `WITH source_counts AS (
 SELECT l.user_id,COUNT(*) AS trigger_count,COUNT(*) AS usage_count,COUNT(c.usage_id) AS deducted_count,` + cyberCountColumns + `,0::bigint AS audit_block_count
 FROM usage_logs l JOIN users u ON u.id=l.user_id AND u.deleted_at IS NULL
 LEFT JOIN cyber_policy_charges c ON c.usage_id=l.id AND c.usage_created_at=l.created_at
 LEFT JOIN cyber_policy_count_events e ON e.usage_id=l.id AND e.usage_created_at=l.created_at
 WHERE l.request_type=4 AND l.created_at >= $1 AND l.created_at < $2 GROUP BY l.user_id
 UNION ALL
 SELECT b.user_id,COUNT(*) AS trigger_count,0::bigint AS usage_count,0::bigint AS deducted_count,` + cyberAuditCountColumns + `,COUNT(*) AS audit_block_count
 FROM cyber_policy_audit_blocks b JOIN users u ON u.id=b.user_id AND u.deleted_at IS NULL
 WHERE b.created_at >= $1 AND b.created_at < $2 GROUP BY b.user_id
), counts AS (
 SELECT user_id,SUM(trigger_count) AS trigger_count,SUM(usage_count) AS usage_count,SUM(deducted_count) AS deducted_count,
 SUM(counted_count) AS counted_count,SUM(skipped_count) AS skipped_count,SUM(unknown_count) AS unknown_count,SUM(audit_block_count) AS audit_block_count
 FROM source_counts GROUP BY user_id
) `

func (s *CyberPolicyService) Ranking(ctx context.Context, input CyberPolicyWindowInput, page, size int) (*CyberPolicyRanking, error) {
	window, err := ParseCyberPolicyWindow(input, time.Now())
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 100 {
		size = 100
	}
	if page > 1000000 {
		return nil, infraerrors.BadRequest("CYBER_PAGE_INVALID", "页码过大")
	}
	// One MVCC snapshot prevents refreshes from mixing totals and rows.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	r := &CyberPolicyRanking{CyberPolicyWindowInput: window.CyberPolicyWindowInput, Items: make([]CyberPolicyRank, 0), Page: page, PageSize: size, GeneratedAt: time.Now()}
	if err := tx.QueryRowContext(ctx, `SELECT multiplier::text FROM cyber_policy_settings WHERE id=1`).Scan(&r.Multiplier); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, cyberRankingCTE+`SELECT COUNT(*),COALESCE(SUM(trigger_count),0),COALESCE(SUM(usage_count-deducted_count),0),COALESCE(SUM(counted_count),0),COALESCE(SUM(skipped_count),0),COALESCE(SUM(unknown_count),0),COALESCE(SUM(audit_block_count),0) FROM counts`, window.From, window.To).Scan(&r.Total, &r.TotalTriggers, &r.TotalPending, &r.TotalCounted, &r.TotalSkipped, &r.TotalUnknown, &r.TotalAuditBlocks); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, cyberRankingCTE+`SELECT ROW_NUMBER() OVER(ORDER BY c.trigger_count DESC,c.user_id),c.user_id,u.email,COALESCE(u.username,''),u.balance::text,c.trigger_count,c.deducted_count,c.usage_count-c.deducted_count,((c.usage_count-c.deducted_count)*$3::numeric)::numeric(20,8)::text,u.status,COALESCE(a.trigger_count,0),c.counted_count,c.skipped_count,c.unknown_count,c.audit_block_count FROM counts c JOIN users u ON u.id=c.user_id LEFT JOIN cyber_policy_user_counters a ON a.user_id=c.user_id ORDER BY c.trigger_count DESC,c.user_id LIMIT $4 OFFSET $5`, window.From, window.To, r.Multiplier, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item CyberPolicyRank
		if err := rows.Scan(&item.Rank, &item.UserID, &item.Email, &item.Username, &item.Balance, &item.TriggerCount, &item.DeductedCount, &item.PendingCount, &item.PendingAmount, &item.Status, &item.AutoDisableCount, &item.CountedCount, &item.SkippedCount, &item.UnknownCount, &item.AuditBlockCount); err != nil {
			return nil, err
		}
		r.Items = append(r.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}

type CyberPolicyCountRecord struct {
	UsageID          int64      `json:"usage_id"`
	AuditID          *int64     `json:"audit_id"`
	RecordKey        string     `json:"record_key"`
	Source           string     `json:"source"`
	OccurredAt       time.Time  `json:"occurred_at"`
	GroupID          *int64     `json:"group_id"`
	GroupName        string     `json:"group_name"`
	RequestID        string     `json:"request_id"`
	Model            string     `json:"model"`
	CountStatus      string     `json:"count_status"`
	CountReason      string     `json:"count_reason"`
	ScopeMatched     *bool      `json:"scope_matched"`
	SettingsRevision *int64     `json:"settings_revision"`
	EvaluatedAt      *time.Time `json:"evaluated_at"`
	AutoChargeStatus string     `json:"auto_charge_status"`
	AutoChargeReason string     `json:"auto_charge_reason"`
	ActualCost       *string    `json:"actual_cost"`
	AuditMultiplier  *string    `json:"audit_multiplier"`
	AutoChargeAmount *string    `json:"auto_charge_amount"`
}

type CyberPolicyCountRecords struct {
	CyberPolicyWindowInput
	Items    []CyberPolicyCountRecord `json:"items"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
	Pages    int64                    `json:"pages"`
}

const cyberRecordsCountQuery = `SELECT
 (SELECT COUNT(*) FROM usage_logs l WHERE l.user_id=$1 AND l.request_type=4 AND l.created_at >= $2 AND l.created_at < $3)
 +(SELECT COUNT(*) FROM cyber_policy_audit_blocks b WHERE b.user_id=$1 AND b.created_at >= $2 AND b.created_at < $3)`

const cyberRecordsQuery = `WITH records AS (
 SELECT l.id AS usage_id,l.created_at AS occurred_at,
 CASE WHEN e.usage_id IS NOT NULL THEN e.group_id ELSE l.group_id END AS group_id,
 CASE WHEN e.usage_id IS NOT NULL THEN e.group_name ELSE COALESCE(g.name,'') END AS group_name,
 COALESCE(l.request_id,'') AS request_id,l.model,COALESCE(e.count_status,'unknown') AS count_status,
 COALESCE(e.count_reason,'historical_unknown') AS count_reason,e.scope_matched,e.settings_revision,e.evaluated_at,
 'upstream_policy'::text AS source,NULL::bigint AS audit_id,
 COALESCE(ac.status,'historical_unknown') AS auto_charge_status,COALESCE(ac.reason,'') AS auto_charge_reason,
 ac.actual_cost::text AS actual_cost,ac.multiplier::text AS audit_multiplier,ac.amount::text AS auto_charge_amount
 FROM usage_logs l
 LEFT JOIN cyber_policy_count_events e ON e.usage_id=l.id AND e.usage_created_at=l.created_at
 LEFT JOIN groups g ON g.id=l.group_id
 LEFT JOIN cyber_policy_auto_charges ac ON ac.usage_id=l.id AND ac.usage_created_at=l.created_at
 WHERE l.user_id=$1 AND l.request_type=4 AND l.created_at >= $2 AND l.created_at < $3
 UNION ALL
 SELECT 0::bigint AS usage_id,b.created_at AS occurred_at,b.group_id,b.group_name,b.request_id,b.model,
 b.count_status,b.count_reason,b.scope_matched,b.settings_revision,b.evaluated_at,b.source,b.id AS audit_id,
 COALESCE(ac.status,'historical_unknown'),COALESCE(ac.reason,''),ac.actual_cost::text,ac.multiplier::text,ac.amount::text
 FROM cyber_policy_audit_blocks b
 LEFT JOIN cyber_policy_auto_charges ac ON ac.audit_id=b.id
 WHERE b.user_id=$1 AND b.created_at >= $2 AND b.created_at < $3
)
 SELECT usage_id,occurred_at,group_id,group_name,request_id,model,count_status,count_reason,
 scope_matched,settings_revision,evaluated_at,source,audit_id,auto_charge_status,auto_charge_reason,actual_cost,audit_multiplier,auto_charge_amount FROM records
 ORDER BY occurred_at DESC,source,usage_id DESC,audit_id DESC NULLS LAST LIMIT $4 OFFSET $5`

// Records is administrator-only at the handler. Decisions come from immutable
// insertion-time evidence; missing old evidence is never inferred from today's
// configuration. Request bodies, API-key values and credentials are not read.
func (s *CyberPolicyService) Records(ctx context.Context, userID int64, input CyberPolicyWindowInput, page, size int) (*CyberPolicyCountRecords, error) {
	if userID <= 0 {
		return nil, infraerrors.BadRequest("CYBER_USER_INVALID", "用户无效")
	}
	window, err := ParseCyberPolicyWindow(input, time.Now())
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 100 {
		size = 100
	}
	if page > 1000000 {
		return nil, infraerrors.BadRequest("CYBER_PAGE_INVALID", "页码过大")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL)`, userID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrUserNotFound
	}
	result := &CyberPolicyCountRecords{CyberPolicyWindowInput: window.CyberPolicyWindowInput, Items: []CyberPolicyCountRecord{}, Page: page, PageSize: size}
	if err := tx.QueryRowContext(ctx, cyberRecordsCountQuery, userID, window.From, window.To).Scan(&result.Total); err != nil {
		return nil, err
	}
	result.Pages = (result.Total + int64(size) - 1) / int64(size)
	rows, err := tx.QueryContext(ctx, cyberRecordsQuery, userID, window.From, window.To, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item CyberPolicyCountRecord
		var groupID, revision, auditID sql.NullInt64
		var scope sql.NullBool
		var evaluated sql.NullTime
		if err := rows.Scan(&item.UsageID, &item.OccurredAt, &groupID, &item.GroupName, &item.RequestID, &item.Model, &item.CountStatus, &item.CountReason, &scope, &revision, &evaluated, &item.Source, &auditID, &item.AutoChargeStatus, &item.AutoChargeReason, &item.ActualCost, &item.AuditMultiplier, &item.AutoChargeAmount); err != nil {
			return nil, err
		}
		if auditID.Valid {
			item.AuditID = &auditID.Int64
			item.RecordKey = fmt.Sprintf("audit:%d", auditID.Int64)
		} else {
			item.RecordKey = fmt.Sprintf("usage:%d:%s", item.UsageID, item.OccurredAt.UTC().Format(time.RFC3339Nano))
		}
		if groupID.Valid {
			item.GroupID = &groupID.Int64
		}
		if scope.Valid {
			item.ScopeMatched = &scope.Bool
		}
		if revision.Valid {
			item.SettingsRevision = &revision.Int64
		}
		if evaluated.Valid {
			item.EvaluatedAt = &evaluated.Time
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

type CyberPolicyDeduction struct {
	CyberPolicyWindowInput
	ID           string     `json:"id"`
	UserID       int64      `json:"user_id"`
	Email        string     `json:"email"`
	Username     string     `json:"username"`
	TriggerCount int64      `json:"trigger_count"`
	Multiplier   string     `json:"multiplier"`
	Amount       string     `json:"amount"`
	Balance      string     `json:"balance"`
	BalanceAfter *string    `json:"balance_after,omitempty"`
	Status       string     `json:"status"`
	ExpiresAt    time.Time  `json:"expires_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

const cyberDeductionColumns = `id::text,user_id,email,username,start_date,end_date,timezone,trigger_count,multiplier::text,amount::text,balance::text,balance_after::text,status,expires_at,completed_at`

func scanCyberDeduction(row interface{ Scan(...any) error }) (*CyberPolicyDeduction, error) {
	var d CyberPolicyDeduction
	err := row.Scan(&d.ID, &d.UserID, &d.Email, &d.Username, &d.StartDate, &d.EndDate, &d.Timezone, &d.TriggerCount, &d.Multiplier, &d.Amount, &d.Balance, &d.BalanceAfter, &d.Status, &d.ExpiresAt, &d.CompletedAt)
	return &d, err
}

func (s *CyberPolicyService) Preview(ctx context.Context, actorID, userID int64, input CyberPolicyWindowInput) (*CyberPolicyDeduction, error) {
	window, err := ParseCyberPolicyWindow(input, time.Now())
	if err != nil {
		return nil, err
	}
	if actorID <= 0 || userID <= 0 {
		return nil, infraerrors.BadRequest("CYBER_USER_INVALID", "用户无效")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var email, name, balance, multiplier string
	if err = tx.QueryRowContext(ctx, `SELECT email,COALESCE(username,''),balance::text FROM users WHERE id=$1 AND deleted_at IS NULL FOR NO KEY UPDATE`, userID).Scan(&email, &name, &balance); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	} else if err != nil {
		return nil, err
	}
	// Bounded cleanup retains completed deduction evidence indefinitely while
	// avoiding abandoned confirmation dialogs accumulating usage snapshots.
	if _, err = tx.ExecContext(ctx, `DELETE FROM cyber_policy_deductions WHERE id IN (SELECT id FROM cyber_policy_deductions WHERE user_id=$1 AND status='pending' AND expires_at<NOW() ORDER BY expires_at LIMIT 100 FOR UPDATE SKIP LOCKED)`, userID); err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT multiplier::text FROM cyber_policy_settings WHERE id=1`).Scan(&multiplier); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO cyber_policy_deductions(id,actor_id,user_id,email,username,start_date,end_date,timezone,multiplier,balance) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::numeric,$10::numeric)`, id, actorID, userID, email, name, window.StartDate, window.EndDate, window.Timezone, multiplier, balance); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO cyber_policy_preview_items(deduction_id,usage_id,usage_created_at) SELECT $1,l.id,l.created_at FROM usage_logs l WHERE l.user_id=$2 AND l.request_type=4 AND l.created_at >= $3 AND l.created_at < $4 AND NOT EXISTS(SELECT 1 FROM cyber_policy_charges c WHERE c.usage_id=l.id AND c.usage_created_at=l.created_at)`, id, userID, window.From, window.To)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, infraerrors.Conflict("CYBER_NOTHING_TO_DEDUCT", "所选时间内没有尚未扣除的安全策略记录")
	}
	d, err := scanCyberDeduction(tx.QueryRowContext(ctx, `UPDATE cyber_policy_deductions SET trigger_count=$2 WHERE id=$1 RETURNING `+cyberDeductionColumns, id, count))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *CyberPolicyService) Deduct(ctx context.Context, actorID, userID int64, previewID string) (*CyberPolicyDeduction, error) {
	if _, err := uuid.Parse(previewID); err != nil {
		return nil, infraerrors.BadRequest("CYBER_PREVIEW_INVALID", "扣除预览无效，请重新预览")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Every deduction for a user uses the same database lock, across instances.
	var currentBalance string
	if err = tx.QueryRowContext(ctx, `SELECT balance::text FROM users WHERE id=$1 AND deleted_at IS NULL FOR NO KEY UPDATE`, userID).Scan(&currentBalance); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	} else if err != nil {
		return nil, err
	}
	d, err := scanCyberDeduction(tx.QueryRowContext(ctx, `SELECT `+cyberDeductionColumns+` FROM cyber_policy_deductions WHERE id=$1 AND actor_id=$2 AND user_id=$3 FOR UPDATE`, previewID, actorID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CYBER_PREVIEW_NOT_FOUND", "扣除预览不存在，请重新预览")
	}
	if err != nil {
		return nil, err
	}
	if d.Status == "completed" {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		s.invalidateCyberBalance(ctx, userID)
		return d, nil
	}
	if !time.Now().Before(d.ExpiresAt) {
		return nil, infraerrors.Conflict("CYBER_PREVIEW_EXPIRED", "扣除预览已过期，请重新预览")
	}
	// Insert the frozen set, never re-count a live interval. A unique identity
	// protects overlapping ranges, simultaneous admins and process restarts.
	result, err := tx.ExecContext(ctx, `INSERT INTO cyber_policy_charges(usage_id,usage_created_at,deduction_id,user_id) SELECT usage_id,usage_created_at,$1,$2 FROM cyber_policy_preview_items WHERE deduction_id=$1 ON CONFLICT(usage_id,usage_created_at) DO NOTHING`, previewID, userID)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != d.TriggerCount {
		return nil, infraerrors.Conflict("CYBER_PREVIEW_STALE", "部分记录已被扣除，请重新预览")
	}
	var after string
	err = tx.QueryRowContext(ctx, `UPDATE users SET balance=balance-$2::numeric,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL AND balance >= $2::numeric RETURNING balance::text`, userID, d.Amount).Scan(&after)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.BadRequest("CYBER_INSUFFICIENT_BALANCE", "余额不足，未扣除余额；请调整倍率或时间范围")
	}
	if err != nil {
		return nil, err
	}
	notes := fmt.Sprintf("耻辱排行榜扣除 / 管理员 #%d / %s 至 %s (%s) / %d 次 × %s = %s / %s", actorID, d.StartDate, d.EndDate, d.Timezone, d.TriggerCount, d.Multiplier, d.Amount, d.ID)
	codeID, _ := uuid.Parse(d.ID)
	if _, err = tx.ExecContext(ctx, `INSERT INTO redeem_codes(code,type,value,status,used_by,used_at,notes) VALUES($1,'admin_balance',-$2::numeric,'used',$3,NOW(),$4)`, "CP-"+base64.RawURLEncoding.EncodeToString(codeID[:]), d.Amount, userID, notes); err != nil {
		return nil, err
	}
	d, err = scanCyberDeduction(tx.QueryRowContext(ctx, `UPDATE cyber_policy_deductions SET status='completed',balance=$2::numeric,balance_after=$3::numeric,completed_at=NOW() WHERE id=$1 RETURNING `+cyberDeductionColumns, d.ID, currentBalance, after))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateCyberBalance(ctx, userID)
	return d, nil
}

func (s *CyberPolicyService) invalidateCyberBalance(ctx context.Context, userID int64) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if s.authInvalidator != nil {
		s.authInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billingCache != nil {
		_ = s.billingCache.InvalidateUserBalance(ctx, userID)
	}
}
