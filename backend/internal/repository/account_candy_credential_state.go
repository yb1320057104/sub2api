package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountRepository) SetAccountCandyCredentialErrorIfMatch(ctx context.Context, snapshot service.AccountCandyCredentialStateSnapshot) (bool, error) {
	if snapshot.AccountID <= 0 || snapshot.CredentialAccountID <= 0 ||
		snapshot.AccountStatus != service.StatusActive || snapshot.CredentialStatus != service.StatusActive ||
		!json.Valid([]byte(snapshot.CredentialsJSON)) ||
		(snapshot.AccountParentID == nil && snapshot.AccountID != snapshot.CredentialAccountID) ||
		(snapshot.AccountParentID != nil && (*snapshot.AccountParentID != snapshot.CredentialAccountID || snapshot.CredentialType != service.AccountTypeOAuth)) {
		return false, nil
	}
	// Lock both identities in ID order before checking their current values.
	// This also serializes parent reassignment and simultaneous model failures;
	// updating credentials, state and the outbox is one atomic statement.
	result, err := r.sql.ExecContext(ctx, `WITH locked AS MATERIALIZED (
		SELECT id,platform,type,status,schedulable,parent_account_id,credentials,proxy_id
		FROM accounts WHERE id IN ($1,$2) AND deleted_at IS NULL ORDER BY id FOR UPDATE
	), updated AS (
		UPDATE accounts a SET status='error',schedulable=false,error_message=$3,updated_at=NOW()
		FROM locked owner,locked probe
		WHERE a.id=owner.id AND owner.id=$2 AND probe.id=$1
		AND owner.platform='openai' AND probe.platform='openai'
		AND owner.type=$4 AND owner.status=$5 AND owner.schedulable=$6
		AND owner.credentials=$7::jsonb AND owner.proxy_id IS NOT DISTINCT FROM $8::bigint
		AND owner.parent_account_id IS NULL
		AND probe.type=$9 AND probe.status=$10 AND probe.schedulable=$11
		AND probe.parent_account_id IS NOT DISTINCT FROM $12::bigint
		RETURNING a.id
	)
	INSERT INTO scheduler_outbox(event_type,account_id,group_id,payload)
	SELECT $13,id,NULL,NULL FROM updated`,
		snapshot.AccountID, snapshot.CredentialAccountID, service.AccountCandyCredentialErrorMessage,
		snapshot.CredentialType, snapshot.CredentialStatus, snapshot.CredentialSchedulable,
		snapshot.CredentialsJSON, snapshot.CredentialProxyID,
		snapshot.AccountType, snapshot.AccountStatus, snapshot.AccountSchedulable, snapshot.AccountParentID,
		service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, snapshot.CredentialAccountID)
	return true, nil
}
