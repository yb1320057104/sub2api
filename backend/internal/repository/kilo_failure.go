package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// RecordKiloGatewayFailure isolates the binding that received the response.
// A confirmed identity-level 403 follows the same Key across source changes;
// every other failure requires the exact binding and credential snapshot.
// Maintenance owns request-egress draining; login pins and sources stay intact.
func (r *accountRepository) RecordKiloGatewayFailure(ctx context.Context, snapshot *service.Account, status int, code string) (bool, error) {
	if snapshot == nil || snapshot.ID <= 0 || snapshot.Extra[service.KiloManagedExtraKey] != true || snapshot.Type != service.AccountTypeAPIKey || status < 200 || status > 599 {
		return false, nil
	}
	apiKey, _ := snapshot.Credentials["api_key"].(string)
	baseURL, _ := snapshot.Credentials["base_url"].(string)
	if apiKey == "" || baseURL == "" {
		return false, nil
	}
	preparedID := int64(service.ParseExtraInt(snapshot.Extra["kilo_prepared_id"]))
	sourceID := int64(service.ParseExtraInt(snapshot.Extra["kilo_onboarding_source_id"]))
	revision := int64(service.ParseExtraInt(snapshot.Extra["kilo_binding_revision"]))
	// The caller must attribute a real HTTP 403 to the Kilo identity first;
	// permission-scope, provider and WAF rejections must not reach this boundary.
	identityForbidden := status == http.StatusForbidden
	if preparedID <= 0 || !identityForbidden && (sourceID <= 0 || revision < 1) {
		return false, nil
	}
	credentials, err := json.Marshal(snapshot.Credentials)
	if err != nil {
		return false, err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	// Match onboarding's batch/prepared/account lock order before checking the
	// response snapshot, so a stale response cannot disable a new pairing.
	if _, err = client.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(70450001)); err != nil {
		return false, err
	}
	var currentSource, currentRevision int64
	var preparedStatus, bindingState string
	var raw []byte
	err = scanSingleRow(ctx, client, `SELECT routing_options,status,COALESCE(source_account_id,0),binding_revision,binding_state FROM pk_kilo_prepared_accounts WHERE id=$1 AND account_id=$2 FOR UPDATE`, []any{preparedID, snapshot.ID}, &raw, &preparedStatus, &currentSource, &currentRevision, &bindingState)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !identityForbidden && (preparedStatus != "bound" || bindingState != "bound" || currentSource != sourceID || currentRevision != revision) {
		return false, nil
	}
	release, reason, err := kiloFailureReleaseReason(raw, status, code)
	if err != nil {
		return false, err
	}
	// Automatic proxy release is optional. Credential/payment rejection must
	// still stop scheduling when an operator disabled automatic IP release.
	blocked := identityForbidden || status == http.StatusUnauthorized || status == http.StatusPaymentRequired || code == "account_restricted" || code == "subscription_sharing_usage_limit_exceeded"
	if !release && !blocked {
		return false, nil
	}
	if identityForbidden {
		reason = service.KiloIdentityForbiddenReason
	}
	if reason == "" {
		if code == "account_restricted" || code == "subscription_sharing_usage_limit_exceeded" {
			reason = code
		} else {
			reason = fmt.Sprintf("http_%d", status)
		}
	}
	prefix := "kilo_block:"
	if release {
		prefix = "kilo_release:"
	}
	txctx := dbent.NewTxContext(ctx, tx)
	if err = r.LockAccountMutation(txctx); err != nil {
		return false, err
	}
	// Retain the existing binding-failure scheduling and error precedence.
	// Identity bans are also recorded for paused accounts, preserving errors.
	var accountID int64
	err = scanSingleRow(ctx, client, kiloFailureAccountSQL, []any{snapshot.ID, apiKey, baseURL, reason, snapshot.Platform, snapshot.Type, strconv.FormatInt(preparedID, 10), identityForbidden, string(credentials), strconv.FormatInt(sourceID, 10), strconv.FormatInt(revision, 10)}, &accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result, err := client.ExecContext(ctx, kiloFailurePreparedSQL, preparedID, prefix+reason, accountID, identityForbidden, sourceID, revision)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed != 1 {
		return false, nil
	}
	if err = enqueueProxyProbeAccountChanges(ctx, client, []int64{accountID}); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	r.syncSchedulerAccountSnapshots(ctx, []int64{accountID})
	return true, nil
}

const kiloFailureAccountSQL = `UPDATE accounts SET schedulable=FALSE,
 status=CASE WHEN status='active' THEN 'error' ELSE status END,
 error_message=CASE WHEN error_message='' THEN $4 ELSE error_message END,updated_at=NOW()
 WHERE id=$1 AND deleted_at IS NULL AND parent_account_id IS NULL AND platform=$5 AND type=$6
 AND extra->'kilo_managed'='true'::jsonb AND extra->>'kilo_prepared_id'=$7
 AND credentials->>'api_key'=$2 AND credentials->>'base_url'=$3
 AND ($8 OR (credentials=$9::jsonb AND COALESCE(extra->>'kilo_onboarding_source_id','0')=$10
 AND extra->>'kilo_binding_revision'=$11 AND (schedulable=TRUE OR status='active') AND status IN ('active','error')))
 RETURNING id`

const kiloFailurePreparedSQL = `UPDATE pk_kilo_prepared_accounts SET status='disabled',
 identity_status=CASE WHEN $4 THEN 'banned' ELSE identity_status END,
 binding_state=CASE WHEN $4 THEN 'blocked' ELSE binding_state END,
 last_error=$2,updated_at=NOW() WHERE id=$1 AND account_id=$3
 AND ($4 OR (status='bound' AND binding_state='bound' AND COALESCE(source_account_id,0)=$5 AND binding_revision=$6))`

func kiloFailureReleaseReason(raw []byte, status int, code string) (bool, string, error) {
	var options struct {
		Statuses json.RawMessage `json:"auto_release_statuses"`
	}
	if err := json.Unmarshal(raw, &options); err != nil {
		return false, "", err
	}
	statuses := []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden}
	if len(options.Statuses) > 0 {
		if err := json.Unmarshal(options.Statuses, &statuses); err != nil {
			return false, "", err
		}
	}
	if len(statuses) == 0 {
		return false, "", nil
	}
	// Only the caller's parsed structured code is accepted; neither arbitrary
	// response text nor a body substring can trigger a release.
	if code == "subscription_sharing_usage_limit_exceeded" || code == "account_restricted" {
		return true, code, nil
	}
	for _, candidate := range statuses {
		if status == candidate {
			return true, fmt.Sprintf("http_%d", status), nil
		}
	}
	return false, "", nil
}

var _ service.KiloGatewayFailureRepository = (*accountRepository)(nil)
