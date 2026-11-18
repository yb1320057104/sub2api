package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const kiloSourceBatchLock int64 = 70450001

// The rejection belongs to the current bearer, not an old recovery generation.
// PostgreSQL sha256 is also used by the existing auth invalidation outbox.
const kiloSourceRecoveryPendingSQL = `(COALESCE(s.extra->'openai_auth_recovery'->>'access_token_digest','')<>''
 AND COALESCE(s.credentials->>'access_token','')<>''
 AND s.extra->'openai_auth_recovery'->>'access_token_digest'=encode(sha256(convert_to(s.credentials->>'access_token','UTF8')),'hex'))`

const kiloSourceStatusSQL = `CASE WHEN ` + kiloSourceRecoveryPendingSQL + ` THEN 'error' ELSE COALESCE(s.status,'') END`

const kiloSourceFailureReasonSQL = `CASE
 WHEN s.error_message LIKE 'Workspace deactivated (402):%' THEN 'workspace_deactivated'
 WHEN s.extra->'openai_auth_recovery'->>'requires_relogin'='true' THEN 'requires_relogin'
 ELSE 'authorization_unavailable' END`

const kiloSourcePairExistsSQL = `SELECT EXISTS (SELECT 1 FROM ` + kiloAccountPairFrom + `
 WHERE s.id=$1 AND ` + kiloAccountPairPredicate + `)`

const kiloSourcePreparedLockSQL = `SELECT p.account_id FROM ` + kiloAccountPairFrom + `
 WHERE s.id=$1 AND ` + kiloAccountPairPredicate + ` ORDER BY p.id FOR UPDATE OF p`

// This is deliberately independent of auto_release_statuses: stopping a Key
// whose source is rejected does not authorize returning its request exits.
const kiloSourceIsolationSQL = `WITH previous AS MATERIALIZED (
 SELECT g.id,(g.status='active' AND g.schedulable) AS can_resume FROM pk_kilo_prepared_accounts p
 JOIN accounts g ON g.id=p.account_id JOIN accounts s ON s.id=p.source_account_id
 WHERE s.id=$1 AND g.id=ANY($2::bigint[]) AND ` + kiloAccountPairPredicate + ` FOR UPDATE OF g
), blocked AS (
 UPDATE accounts g SET schedulable=FALSE,
 status=CASE WHEN g.status='active' THEN 'error' ELSE g.status END,
 error_message=CASE WHEN COALESCE(g.error_message,'')='' THEN 'Kilo source authorization unavailable: ' || ` + kiloSourceFailureReasonSQL + ` ELSE g.error_message END,
 updated_at=clock_timestamp()
 FROM pk_kilo_prepared_accounts p JOIN accounts s ON s.id=p.source_account_id
 WHERE s.id=$1 AND g.id=ANY($2::bigint[]) AND EXISTS(SELECT 1 FROM previous WHERE previous.id=g.id) AND p.account_id=g.id AND ` + kiloAccountPairPredicate + `
 AND (s.status<>'active' OR ` + kiloSourceRecoveryPendingSQL + `)
 RETURNING g.id,p.id AS prepared_id,` + kiloSourceFailureReasonSQL + ` AS source_reason,(SELECT can_resume FROM previous WHERE previous.id=g.id) AS can_resume
), disabled_prepared AS (
 UPDATE pk_kilo_prepared_accounts p SET status='disabled',
 binding_resume=p.binding_resume OR blocked.can_resume,
 binding_state=CASE WHEN p.identity_status='active' AND p.binding_state IN ('bound','recovering') THEN 'recovering' ELSE p.binding_state END,
 binding_error='source_authorization_unavailable',
 last_error=CASE WHEN p.status='bound' OR COALESCE(p.last_error,'')='' OR p.last_error LIKE 'kilo_source:%'
 THEN 'kilo_source:' || blocked.source_reason ELSE p.last_error END,updated_at=clock_timestamp()
 FROM blocked WHERE p.id=blocked.prepared_id AND p.account_id=blocked.id
 RETURNING p.account_id
), published AS (
 INSERT INTO scheduler_outbox(event_type,account_id) SELECT $3,account_id FROM disabled_prepared RETURNING account_id
) SELECT account_id FROM published`

// Ordinary accounts keep their original mutation and source-cache path. A new
// binding after the unlocked preflight is still checked by the send-time guard.
// Existing pairs are rechecked under Kilo ownership before credential/account
// locks, and their isolation commits with the callback's successful CAS.
func (r *accountRepository) mutateKiloSourceHealth(ctx context.Context, id int64, mutate func(context.Context, *accountRepository, bool, bool) (bool, error)) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("account mutation executor is unavailable")
	}
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		tx, _ = r.sql.(*dbent.Tx)
	}
	scoped := *r.accountTransactionScope(ctx)
	scoped.schedulerCache = nil
	opCtx := ctx
	if tx != nil {
		scoped.client, scoped.sql = tx.Client(), tx.Client()
		opCtx = dbent.NewTxContext(ctx, tx)
	}
	var paired bool
	if err := scanSingleRow(opCtx, scoped.sql, kiloSourcePairExistsSQL, []any{id}, &paired); err != nil {
		return false, err
	}
	if !paired {
		// Scoped repositories may retain only tx.Client(), without a Tx in
		// context. Their native executor must also use a nonblocking guard.
		_, clientExecutor := scoped.sql.(*dbent.Client)
		callerOwned := tx != nil || clientExecutor
		changed, err := mutate(opCtx, &scoped, callerOwned, false)
		if err != nil || !changed {
			return changed, err
		}
		if !callerOwned {
			r.publishKiloSourceHealth([]int64{id})
		} else if tx != nil {
			// A transaction-scoped repository retains the ordinary account's
			// immediate cache contract; a root repository waits for outer commit.
			if r.client == tx.Client() {
				r.syncSchedulerAccountSnapshot(opCtx, id)
			} else {
				r.publishKiloSourceHealthOnCommit(tx, []int64{id})
			}
		}
		return true, nil
	}
	if r.client == nil {
		return false, errors.New("account mutation transaction is unavailable")
	}
	owned := false
	if tx == nil {
		var err error
		tx, err = r.client.Tx(ctx)
		if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
			return false, err
		}
		owned = err == nil
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	if tx != nil {
		scoped.client, scoped.sql = tx.Client(), tx.Client()
		opCtx = dbent.NewTxContext(ctx, tx)
	}
	// A caller-owned transaction may already hold account locks. Do not wait
	// backwards for Kilo ownership: fail the operation before mutating anything.
	if err := lockKiloSourceAdvisory(opCtx, scoped.sql, kiloSourceBatchLock, !owned); err != nil {
		return false, err
	}
	lockQuery := kiloSourcePreparedLockSQL
	if !owned {
		lockQuery += " NOWAIT"
	}
	rows, err := scoped.sql.QueryContext(opCtx, lockQuery, id)
	if err != nil {
		return false, err
	}
	var gateways []int64
	for rows.Next() {
		var gatewayID int64
		if err := rows.Scan(&gatewayID); err != nil {
			_ = rows.Close()
			return false, err
		}
		gateways = append(gateways, gatewayID)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := lockKiloSourceAdvisory(opCtx, scoped.sql, accountProxyAssignmentLock, !owned); err != nil {
		return false, err
	}
	changed, err := mutate(opCtx, &scoped, !owned, true)
	if err != nil {
		return false, err
	}
	changedIDs := []int64{id}
	if changed && len(gateways) > 0 {
		blocked, err := scoped.isolateKiloSourceGateways(opCtx, id, gateways)
		if err != nil {
			return false, err
		}
		changedIDs = append(changedIDs, blocked...)
	}
	if owned {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		if changed {
			r.publishKiloSourceHealth(changedIDs)
		}
	} else if changed && tx != nil {
		r.publishKiloSourceHealthOnCommit(tx, changedIDs)
	}
	return changed, nil
}

func (r *accountRepository) publishKiloSourceHealthOnCommit(tx *dbent.Tx, ids []int64) {
	if r.client == tx.Client() || r.schedulerCache == nil {
		return
	}
	// Reread through the root client only after the outer commit succeeds.
	tx.OnCommit(func(next dbent.Committer) dbent.Committer {
		return dbent.CommitFunc(func(commitCtx context.Context, committed *dbent.Tx) error {
			if err := next.Commit(commitCtx, committed); err != nil {
				return err
			}
			r.publishKiloSourceHealth(ids)
			return nil
		})
	})
}

func lockKiloSourceAdvisory(ctx context.Context, exec sqlExecutor, id int64, nonblocking bool) error {
	if !nonblocking {
		_, err := exec.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", id)
		return err
	}
	var locked bool
	if err := scanSingleRow(ctx, exec, "SELECT pg_try_advisory_xact_lock($1)", []any{id}, &locked); err != nil {
		return err
	}
	if !locked {
		return errors.New("Kilo source mutation ownership is busy; retry the transaction")
	}
	return nil
}

func (r *accountRepository) isolateKiloSourceGateways(ctx context.Context, id int64, gateways []int64) ([]int64, error) {
	rows, err := r.sql.QueryContext(ctx, kiloSourceIsolationSQL, id, pq.Array(gateways), service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var blocked []int64
	for rows.Next() {
		var gatewayID int64
		if err := rows.Scan(&gatewayID); err != nil {
			return nil, err
		}
		blocked = append(blocked, gatewayID)
	}
	return blocked, rows.Err()
}

func (r *accountRepository) publishKiloSourceHealth(ids []int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if len(ids) == 1 {
		r.syncSchedulerAccountSnapshot(ctx, ids[0])
		return
	}
	r.syncSchedulerAccountSnapshots(ctx, ids)
}

const kiloSourceSendHealthSQL = `SELECT p.source_account_id,
 COALESCE((` + kiloAccountPairPredicate + `),FALSE),
 COALESCE(s.status='active' AND NOT ` + kiloSourceRecoveryPendingSQL + `,FALSE)
 FROM pk_kilo_prepared_accounts p JOIN accounts g ON g.id=p.account_id AND g.deleted_at IS NULL
 LEFT JOIN accounts s ON s.id=p.source_account_id
 WHERE p.id=$2 AND g.id=$1 AND p.status IN ('bound','disabled')`

// Only the returned routing projection changes. The source's raw status and
// manual scheduling choice remain owned by the original OAuth lifecycle.
func (r *accountRepository) projectKiloSourceHealth(ctx context.Context, gateway *service.Account) error {
	if !gateway.IsKiloGatewayAccount() || gateway.Extra[service.KiloManagedExtraKey] != true {
		return nil
	}
	preparedID := int64(service.ParseExtraInt(gateway.Extra["kilo_prepared_id"]))
	if preparedID <= 0 {
		return nil
	}
	var sourceID sql.NullInt64
	var reciprocal, healthy bool
	err := scanSingleRow(ctx, r.sql, kiloSourceSendHealthSQL, []any{gateway.ID, preparedID}, &sourceID, &reciprocal, &healthy)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && !sourceID.Valid && service.ParseExtraInt(gateway.Extra["kilo_onboarding_source_id"]) <= 0 {
		return nil // Manually prepared gateways can predate source onboarding.
	}
	if err != nil || !sourceID.Valid || !reciprocal || !healthy {
		if gateway.Status == service.StatusActive {
			gateway.Status = service.StatusError
		}
		gateway.Schedulable = false
		if gateway.ErrorMessage == "" {
			gateway.ErrorMessage = "Kilo source authorization or reciprocal pairing is unavailable"
		}
	}
	return nil
}
