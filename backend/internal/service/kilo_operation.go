package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"time"
)

// The session lock drains healthy workers before a release returns. Durable
// lease checks in each mutation transaction also fence workers whose lock
// connection disappeared before the heartbeat could cancel their context.
func (s *KiloService) lockPreparedOperation(ctx context.Context, id int64) (context.Context, func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	key := id ^ int64(-5424871661416316928)
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
		return nil, nil, err
	}
	operationCtx, cancel := context.WithCancel(ctx)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		timer := time.NewTicker(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-operationCtx.Done():
				return
			case <-timer.C:
				pingCtx, pingCancel := context.WithTimeout(operationCtx, 3*time.Second)
				pingErr := conn.PingContext(pingCtx)
				pingCancel()
				if pingErr != nil {
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			close(done)
			<-stopped
			unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer unlockCancel()
			var unlocked bool
			unlockErr := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, key).Scan(&unlocked)
			if unlockErr != nil || !unlocked {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			_ = conn.Close()
		})
	}
	return operationCtx, release, nil
}

// KiloAccountMutationLocker lets onboarding follow the account repository's
// transaction lock order before it locks source or gateway rows.
type KiloAccountMutationLocker interface {
	LockAccountMutation(ctx context.Context) error
}

type kiloOnboardingLeaseKey struct{}
type kiloOnboardingLease struct {
	jobID, preparedID int64
	revision          int64
	bindingRevision   int64
	token             string
}

// WithKiloOnboardingLease carries a worker's durable ownership to repository
// mutations. Repositories must validate it inside the transaction that writes.
func WithKiloOnboardingLease(ctx context.Context, jobID, preparedID int64, token string) context.Context {
	return context.WithValue(ctx, kiloOnboardingLeaseKey{}, kiloOnboardingLease{jobID: jobID, preparedID: preparedID, token: token})
}

func withKiloClaimLease(ctx context.Context, job *kiloClaim) context.Context {
	return context.WithValue(ctx, kiloOnboardingLeaseKey{}, kiloOnboardingLease{jobID: job.ID, preparedID: job.PreparedID, revision: job.Revision, bindingRevision: job.BindingRevision, token: job.Lease})
}

type kiloOnboardingTransaction interface {
	kiloQuerier
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// LockKiloOnboardingLease serializes revocation with a short mutation transaction.
// Holding these locks until commit makes a lost session lock harmless: either
// the mutation commits before cancellation, or its stale ownership is rejected.
// Call only with an open transaction, before acquiring account/proxy/egress locks.
func LockKiloOnboardingLease(ctx context.Context, tx kiloOnboardingTransaction) error {
	lease, fenced := ctx.Value(kiloOnboardingLeaseKey{}).(kiloOnboardingLease)
	if !fenced {
		return nil
	}
	if lease.jobID <= 0 || lease.preparedID < 0 || lease.token == "" {
		return kiloConflict("任务执行身份无效")
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var owned int64
	var err error
	if lease.preparedID > 0 {
		err = kiloScan(ctx, tx, `SELECT j.id FROM pk_kilo_jobs j JOIN pk_kilo_prepared_accounts p ON p.id=j.prepared_id WHERE j.id=$1 AND j.prepared_id=$2 AND j.status='running' AND j.lease_token=$3 AND j.lease_until>NOW() AND ($4::bigint=0 OR j.revision=$4) AND p.status='binding' AND p.binding_job_id=j.id AND ($5::bigint=0 OR p.binding_revision=$5 AND j.binding_revision=$5) FOR SHARE OF j,p`, []any{lease.jobID, lease.preparedID, lease.token, lease.revision, lease.bindingRevision}, &owned)
	} else {
		// Zero is only the credential-acquisition branch. Source reservation and,
		// once created, the inactive gateway must still belong to this exact job.
		err = kiloScan(ctx, tx, `SELECT j.id FROM pk_kilo_jobs j JOIN accounts src ON src.id=j.source_account_id WHERE j.id=$1 AND j.prepared_id IS NULL AND j.job_kind='onboard_source' AND j.status='running' AND j.lease_token=$2 AND j.lease_until>NOW() AND ($3::bigint=0 OR j.revision=$3) AND src.deleted_at IS NULL AND src.platform='openai' AND src.type='oauth' AND src.parent_account_id IS NULL AND src.extra->'kilo_quota_only'='true'::jsonb AND src.extra->'kilo_onboarding_job_id'=to_jsonb(j.id) AND src.extra->'kilo_onboarding_proxy_id'=to_jsonb(j.login_proxy_id) AND (j.account_id IS NULL OR EXISTS(SELECT 1 FROM accounts a WHERE a.id=j.account_id AND a.deleted_at IS NULL AND a.platform='openai' AND a.type='apikey' AND a.parent_account_id IS NULL AND NOT a.schedulable AND a.extra->'kilo_managed'='true'::jsonb AND a.extra->'kilo_onboarding_job_id'=to_jsonb(j.id) AND a.extra->'kilo_onboarding_source_id'=to_jsonb(j.source_account_id) AND a.extra->'kilo_login_proxy_id'=to_jsonb(j.login_proxy_id))) FOR SHARE OF j`, []any{lease.jobID, lease.token, lease.revision}, &owned)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return kiloConflict("任务已取消、执行租约已失效或账号归属已改变")
	}
	return err
}
