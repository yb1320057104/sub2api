package repository

import (
	"context"
	"database/sql"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"

	entsql "entgo.io/ent/dialect/sql"
)

var _ service.KiloAccountViewReader = (*accountRepository)(nil)

// Keep the same ownership proof as GetKiloSourceForGateway: the prepared row
// owns both IDs and both extras must point back. Pair markers also make a source
// quota-only under Account.IsKiloQuotaOnly, including older persisted pairs.
const kiloAccountPairPredicate = `p.status IN ('bound','disabled')
 AND g.deleted_at IS NULL AND s.deleted_at IS NULL
 AND g.platform='openai' AND g.type='apikey'
 AND g.extra->>'kilo_managed'='true'
 AND g.extra->>'kilo_prepared_id'=p.id::text
 AND s.platform='openai' AND s.type='oauth' AND s.parent_account_id IS NULL
 AND s.extra->>'kilo_source_gateway_id'=p.account_id::text
 AND s.extra->>'kilo_source_prepared_id'=p.id::text`

const kiloAccountPairFrom = `pk_kilo_prepared_accounts p
 JOIN accounts g ON g.id=p.account_id
 JOIN accounts s ON s.id=p.source_account_id`

const kiloAccountSourceEmail = `COALESCE(NULLIF(s.credentials->>'email',''),s.credentials->>'chatgpt_email','')`

func (r *accountRepository) kiloAccountListFilteredQuery(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) *dbent.AccountQuery {
	if !service.IsKiloAccountList(ctx) {
		return r.accountListFilteredQuery(platform, accountType, status, search, groupID, privacyMode)
	}
	q := r.accountListFilteredQuery(platform, accountType, status, "", groupID, privacyMode)
	observerGroups, observer := service.ObserverGroupIDs(ctx)
	q = q.Where(func(selector *entsql.Selector) {
		selector.Where(entsql.P(func(b *entsql.Builder) {
			b.WriteString(`NOT EXISTS (SELECT 1 FROM ` + kiloAccountPairFrom + ` WHERE s.id=` + selector.C(dbaccount.FieldID) + ` AND ` + kiloAccountPairPredicate)
			if observer {
				// A source visible to an observer must survive if its gateway is in a
				// different group; no hidden identity can stand in for that source.
				b.WriteString(` AND EXISTS (SELECT 1 FROM account_groups kg WHERE kg.account_id=g.id AND kg.group_id=ANY(`).Arg(pq.Array(observerGroups)).WriteString(`))`)
			}
			b.WriteString(`)`)
		}))
	})
	if search != "" {
		name := dbaccount.NameContainsFold(search)
		if observer {
			return q.Where(name)
		}
		q = q.Where(dbaccount.Or(name, func(selector *entsql.Selector) {
			// The source only widens search for a proven gateway; sorting and
			// pagination still belong to the physical Key row. strpos keeps
			// wildcard characters in a source name or email literal.
			selector.Where(entsql.P(func(b *entsql.Builder) {
				b.WriteString(`EXISTS (SELECT 1 FROM ` + kiloAccountPairFrom + ` WHERE g.id=` + selector.C(dbaccount.FieldID) + ` AND ` + kiloAccountPairPredicate + ` AND (strpos(lower(s.name),lower(`).Arg(search).WriteString(`))>0 OR strpos(lower(` + kiloAccountSourceEmail + `),lower(`).Arg(search).WriteString(`))>0))`)
			}))
		}))
	}
	return q
}

// GetKiloAccountViews performs one narrow read for the requested Key IDs. Only
// identity strings and key-presence booleans cross the repository boundary.
func (r *accountRepository) GetKiloAccountViews(ctx context.Context, ids []int64) (map[int64]service.KiloAccountView, error) {
	views := make(map[int64]service.KiloAccountView)
	ids = uniquePositiveInt64s(ids)
	if len(ids) == 0 {
		return views, nil
	}
	// Source summaries can refer to a different group than their gateway.
	// The logical identity projection is therefore restricted to full admins.
	if _, observer := service.ObserverGroupIDs(ctx); observer {
		return views, nil
	}
	if r == nil || r.sql == nil {
		return nil, errors.New("Kilo account view reader is unavailable")
	}
	if scoped := r.accountTransactionScope(ctx); scoped != r {
		return scoped.GetKiloAccountViews(ctx, ids)
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT g.id,p.id,
 COALESCE(NULLIF(p.name,''),g.name),
 COALESCE(NULLIF(`+kiloAccountSourceEmail+`,''),p.email,g.credentials->>'email',''),
 COALESCE(NULLIF(p.organization_id,''),g.credentials->>'kilo_organization_id',''),
 COALESCE(g.credentials->>'api_key','')<>'',
 s.id,COALESCE(s.name,''),`+kiloAccountSourceEmail+`,
 COALESCE(s.credentials->>'plan_type',''),
 COALESCE(NULLIF(s.credentials->>'chatgpt_account_id',''),s.credentials->>'workspace_id',''),
 `+kiloSourceStatusSQL+`,s.id IS NOT NULL
 FROM accounts g
 LEFT JOIN pk_kilo_prepared_accounts p ON p.account_id=g.id AND g.extra->>'kilo_prepared_id'=p.id::text
 LEFT JOIN accounts s ON s.id=p.source_account_id AND `+kiloAccountPairPredicate+`
 WHERE g.id=ANY($1) AND g.deleted_at IS NULL AND g.platform='openai' AND g.type='apikey' AND g.extra->>'kilo_managed'='true'`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var preparedID, sourceID sql.NullInt64
		var view service.KiloAccountView
		var source service.KiloAccountSourceView
		if err := rows.Scan(&id, &preparedID, &view.Name, &view.Email, &view.OrganizationID, &view.HasKey, &sourceID, &source.Name, &source.Email, &source.PlanType, &source.WorkspaceID, &source.Status, &source.QuotaOnly); err != nil {
			return nil, err
		}
		if preparedID.Valid {
			view.PreparedID = &preparedID.Int64
		}
		if sourceID.Valid && source.QuotaOnly {
			source.ID = sourceID.Int64
			view.Source = &source
		}
		views[id] = view
	}
	return views, rows.Err()
}
