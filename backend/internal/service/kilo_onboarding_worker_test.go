package service

import (
	"context"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type kiloWorkerCipher struct{}

func (kiloWorkerCipher) Encrypt(value string) (string, error) {
	return "cipher:" + base64.StdEncoding.EncodeToString([]byte(value)), nil
}
func (kiloWorkerCipher) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "cipher:") {
		return "", errors.New("not encrypted")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "cipher:"))
	return string(raw), err
}

func kiloWorkerLeaseExpectation(mock sqlmock.Sqlmock, job *kiloClaim, accepted bool) {
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
	rows := sqlmock.NewRows([]string{"id"})
	if accepted {
		rows.AddRow(job.ID)
	}
	if job.PreparedID == 0 {
		mock.ExpectQuery("SELECT j.id FROM pk_kilo_jobs j JOIN accounts src.*j.prepared_id IS NULL.*kilo_onboarding_job_id.*FOR SHARE").WithArgs(job.ID, job.Lease, job.Revision).WillReturnRows(rows)
	} else {
		mock.ExpectQuery("SELECT j.id FROM pk_kilo_jobs j JOIN pk_kilo_prepared_accounts p.*FOR SHARE").WithArgs(job.ID, job.PreparedID, job.Lease, job.Revision, job.BindingRevision).WillReturnRows(rows)
	}
}

func TestKiloOnboardingLeaseBeforeCredentialIsFenced(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		t.Run(map[bool]string{true: "owned_source_and_gateway", false: "revoked_source_or_revision"}[accepted], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			job := &kiloClaim{ID: 11, Revision: 3, Lease: uuid.NewString()}
			mock.ExpectBegin()
			kiloWorkerLeaseExpectation(mock, job, accepted)
			mock.ExpectRollback()
			tx, err := db.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			err = LockKiloOnboardingLease(withKiloClaimLease(context.Background(), job), tx)
			if accepted {
				require.NoError(t, err)
			} else {
				require.Equal(t, "KILO_CONFLICT", infraerrors.Reason(err))
			}
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloOnboardingRetryKeepsUnknownRemoteOperation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	state := &kiloOnboardingRuntime{Version: 1, Revision: 3, DeadlineAt: time.Now().Add(time.Minute), DeviceApproved: true, kiloOnboardingState: kiloOnboardingState{OperationID: uuid.NewString(), TaskID: "original-task", Phase: "approve_device", DeviceSessionID: uuid.NewString()}}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	cipher, err := (kiloWorkerCipher{}).Encrypt(string(raw))
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT prepared_id,status,stage,remote_state_encrypted,remote_outcome,quota_oauth_ready,quota_task_id").WithArgs(int64(14)).WillReturnRows(sqlmock.NewRows([]string{"prepared", "status", "stage", "state", "outcome", "quota_ready", "quota_task", "kind", "reused", "connected"}).AddRow(nil, "failed", "device_authorization", cipher, "unknown", true, 9, KiloJobOnboardSource, false, false))
	mock.ExpectExec("UPDATE pk_kilo_jobs SET status='queued',stage=.*revision=revision\\+CASE WHEN").WithArgs(int64(14), "device_authorization", true, int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	svc := &KiloService{db: db, encryptor: kiloWorkerCipher{}}
	require.NoError(t, svc.retrySourceOnboardingJob(context.Background(), 14))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloPersonalRetryObservesOnlyTheOriginalUnresolvedOperation(t *testing.T) {
	for _, reconcile := range []bool{false, true} {
		for _, outcome := range []string{"unknown", "pending", "failed", "confirmed"} {
			t.Run(map[bool]string{false: "connect_", true: "reconcile_"}[reconcile]+outcome, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				defer db.Close()
				state := &kiloOnboardingRuntime{Version: 1, Revision: 3, SourcePlanType: "plus", ReconcileSubscription: reconcile,
					kiloOnboardingState: kiloOnboardingState{RemoteUserID: "original-owner", OperationID: uuid.NewString(), TaskID: "original-task", Phase: "connect_subscription"}}
				if reconcile {
					state.Phase = "reconcile_subscription"
				}
				if outcome == "confirmed" {
					state.RemoteClosed, state.OperationID, state.TaskID, state.Phase = true, "", "", ""
				}
				raw, err := json.Marshal(state)
				require.NoError(t, err)
				cipher, err := (kiloWorkerCipher{}).Encrypt(string(raw))
				require.NoError(t, err)
				mock.ExpectBegin()
				mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT prepared_id,status,stage,remote_state_encrypted,remote_outcome,quota_oauth_ready,quota_task_id").WithArgs(int64(14)).WillReturnRows(sqlmock.NewRows([]string{"prepared", "status", "stage", "state", "outcome", "quota_ready", "quota_task", "kind", "reused", "connected"}).AddRow(nil, "failed", "subscription_authorization", cipher, outcome, true, 9, KiloJobOnboardSource, false, false))
				resume := outcome == "unknown" || outcome == "pending"
				if resume {
					mock.ExpectExec("UPDATE pk_kilo_jobs SET status='queued',stage=.*revision=revision\\+CASE WHEN").WithArgs(int64(14), "subscription_authorization", true, int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
				} else {
					mock.ExpectRollback()
				}
				err = (&KiloService{db: db, encryptor: kiloWorkerCipher{}}).retrySourceOnboardingJob(context.Background(), 14)
				if resume {
					require.NoError(t, err)
				} else {
					require.Equal(t, "KILO_CONFLICT", infraerrors.Reason(err))
				}
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestKiloOnboardingNeedsActionCanRetryAfterRemoteStopped(t *testing.T) {
	for _, outcome := range []string{"pending", "needs_action"} {
		t.Run(outcome, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			state := &kiloOnboardingRuntime{Version: 1, Revision: 3, DeadlineAt: time.Now().Add(time.Minute), kiloOnboardingState: kiloOnboardingState{OperationID: uuid.NewString(), TaskID: "original-task", Phase: "connect_subscription"}}
			raw, err := json.Marshal(state)
			require.NoError(t, err)
			cipher, err := (kiloWorkerCipher{}).Encrypt(string(raw))
			require.NoError(t, err)
			resume := outcome == "pending"
			stage := "kilo_login"
			if resume {
				stage = "subscription_authorization"
			}
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT prepared_id,status,stage,remote_state_encrypted,remote_outcome,quota_oauth_ready,quota_task_id").WithArgs(int64(14)).WillReturnRows(sqlmock.NewRows([]string{"prepared", "status", "stage", "state", "outcome", "quota_ready", "quota_task", "kind", "reused", "connected"}).AddRow(nil, "awaiting_authorization", "subscription_authorization", cipher, outcome, true, 9, KiloJobOnboardSource, false, false))
			mock.ExpectExec("UPDATE pk_kilo_jobs SET status='queued',stage=.*revision=revision\\+CASE WHEN").WithArgs(int64(14), stage, resume, int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			svc := &KiloService{db: db, encryptor: kiloWorkerCipher{}}
			require.NoError(t, svc.retrySourceOnboardingJob(context.Background(), 14))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloJobProjectsItsStoredQuotaFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	stamp := time.Now()
	columns := []string{
		"id", "batch_id", "prepared_id", "prepared_name", "source_account_id", "source_name", "account_id",
		"stage", "status", "last_error", "verification_url", "user_code", "created_at", "finished_at",
		"job_kind", "organization_id", "login_proxy_id", "error_code", "failed_stage", "next_action",
		"next_attempt_at", "credential_ready", "subscription_connected", "quota_oauth_ready", "quota_error",
		"revision", "remote_state", "outcome", "source_identity", "reused", "live_lease", "material_version",
	}
	rows := sqlmock.NewRows(columns).AddRow(
		int64(12), "fixture-batch", nil, "", int64(42), "Fixture source", int64(73),
		"quota_oauth", "failed", "generic error", "", "", stamp, stamp,
		KiloJobOnboardSource, "", int64(41), "KILO_QUOTA_OAUTH_FAILED", "quota_oauth", "review",
		stamp, false, false, false, "local_protocol_failed stage=web_login reason=interaction_required elapsed_ms=100 exit=1",
		int64(3), "", "", "", false, false, "",
	)
	mock.ExpectQuery("LEFT JOIN openai_oauth_reauth_tasks q ON q.id=j.quota_task_id AND q.account_id=j.source_account_id").
		WithArgs(int64(12)).WillReturnRows(rows)

	jobs, err := (&KiloService{db: db}).kiloJobs(context.Background(), "WHERE j.id=$1", int64(12))
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "KILO_QUOTA_OAUTH_INTERACTION_REQUIRED", jobs[0].ErrorCode)
	require.Equal(t, "failed", jobs[0].Status)
	require.False(t, jobs[0].QuotaOAuthReady)
	require.Equal(t, int64(3), jobs[0].Revision)
	require.Empty(t, jobs[0].RecoveryActions)
	require.NotContains(t, jobs[0].LastError, "local_protocol_failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloOnboardingNeedsActionPersistsTerminalOutcome(t *testing.T) {
	job := &kiloClaim{ID: 14, Revision: 3, Stage: "subscription_authorization", Lease: uuid.NewString(), RemoteOutcome: "pending"}
	state := &kiloOnboardingRuntime{Version: 1, Revision: 3, DeadlineAt: time.Now().Add(time.Minute), kiloOnboardingState: kiloOnboardingState{SessionHandle: "owned-session", OperationID: uuid.NewString(), TaskID: "owned-task", Phase: "connect_subscription", InstanceID: "owned-instance"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/health" {
			_ = json.NewEncoder(w).Encode(KiloExecutorHealth{OK: true, ProtocolVersion: 1, InstanceID: state.InstanceID, Capabilities: []string{"login", "connect_subscription", "approve_device", "fixed_proxy", "cancel", "ack", "session_ttl", "workspace_identity"}})
			return
		}
		require.Equal(t, "/v1/tasks/owned-task", r.URL.Path)
		_ = json.NewEncoder(w).Encode(KiloExecutorTask{TaskID: state.TaskID, InstanceID: state.InstanceID, JobID: job.ID, Revision: job.Revision, Phase: state.Phase, Status: "needs_action", ErrorCode: "ACTION_REQUIRED"})
	}))
	defer server.Close()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	for i := 0; i < 2; i++ {
		mock.ExpectBegin()
		kiloWorkerLeaseExpectation(mock, job, true)
		mock.ExpectExec("UPDATE pk_kilo_jobs SET stage=.*revision=.*lease_token=.*lease_until>NOW").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}
	svc := &KiloService{db: db, encryptor: kiloWorkerCipher{}, executor: &kiloExecutorClient{base: server.URL, token: strings.Repeat("x", 32), client: server.Client()}}
	require.NoError(t, svc.runOnboardingPhase(context.Background(), job, state, "connect_subscription", nil))
	require.Equal(t, "needs_action", job.RemoteOutcome)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloOnboardingCleanupAcceptsNeedsActionButNotRunning(t *testing.T) {
	for _, status := range []string{"needs_action", "running"} {
		t.Run(status, func(t *testing.T) {
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(KiloExecutorTask{Status: status})
			}))
			defer server.Close()
			svc := &KiloService{executor: &kiloExecutorClient{base: server.URL, token: strings.Repeat("x", 32), client: server.Client()}}
			state := &kiloOnboardingRuntime{DeadlineAt: time.Now().Add(time.Minute), kiloOnboardingState: kiloOnboardingState{TaskID: "owned-task", SessionHandle: "owned-session"}}
			stopped, err := svc.cleanupOnboardingRuntime(context.Background(), state)
			require.NoError(t, err)
			require.Equal(t, status == "needs_action", stopped)
			if stopped {
				require.Equal(t, []string{"POST /v1/tasks/owned-task/cancel", "GET /v1/tasks/owned-task", "POST /v1/tasks/owned-task/ack", "DELETE /v1/sessions/owned-session"}, calls)
			} else {
				require.Equal(t, []string{"POST /v1/tasks/owned-task/cancel", "GET /v1/tasks/owned-task"}, calls)
			}
		})
	}
}

func TestKiloOnboardingExecutorRequiresWorkspaceIdentity(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "old_executor_rejected", true: "workspace_identity_supported"}[supported], func(t *testing.T) {
			capabilities := []string{"login", "connect_subscription", "approve_device", "fixed_proxy", "cancel", "ack", "session_ttl"}
			if supported {
				capabilities = append(capabilities, "workspace_identity")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/health", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(KiloExecutorHealth{OK: true, ProtocolVersion: 1, InstanceID: "owned-instance", AvailableCapacity: 1, Capabilities: capabilities})
			}))
			defer server.Close()
			svc := &KiloService{executor: &kiloExecutorClient{base: server.URL, token: strings.Repeat("x", 32), client: server.Client()}}
			_, err := svc.ensureOnboardingExecutor(context.Background(), &kiloOnboardingRuntime{}, "login", true)
			if supported {
				require.NoError(t, err)
			} else {
				var failure *kiloOnboardingFailure
				require.ErrorAs(t, err, &failure)
				require.Equal(t, "EXECUTOR_UNAVAILABLE", failure.code)
			}
		})
	}
}

func TestKiloOnboardingExpectedIdentityUsesSourceWorkspaceAndPlan(t *testing.T) {
	for _, plan := range []string{"team", "business", "plus", "pro"} {
		t.Run(plan, func(t *testing.T) {
			source := &Account{Credentials: map[string]any{"email": "source@example.test", "chatgpt_account_id": "source-oauth-workspace", "plan_type": plan, "access_token": "synthetic-not-for-executor"}}
			state := &kiloOnboardingRuntime{SourceEmail: "stale@example.test", SourceWorkspace: "source-oauth-workspace"}
			input := KiloExecutorTaskInput{OrganizationID: "distinct-kilo-organization"}
			require.NoError(t, setKiloExecutorExpectedIdentity(&input, source, state))
			require.Equal(t, "source@example.test", input.ExpectedIdentity.SourceEmail)
			require.Equal(t, "source-oauth-workspace", input.ExpectedIdentity.SourceWorkspaceID)
			require.Equal(t, plan, input.ExpectedIdentity.SourcePlanType)
			require.Equal(t, "distinct-kilo-organization", input.OrganizationID)
			wire, err := json.Marshal(input.ExpectedIdentity)
			require.NoError(t, err)
			require.JSONEq(t, `{"source_email":"source@example.test","source_workspace_id":"source-oauth-workspace","source_plan_type":"`+plan+`"}`, string(wire))
		})
	}
	for _, test := range []struct {
		name      string
		workspace any
		confirmed string
	}{
		{name: "missing", workspace: nil},
		{name: "blank", workspace: " \t"},
		{name: "wrong_type", workspace: 42},
		{name: "changed", workspace: "other-workspace", confirmed: "confirmed-workspace"},
		{name: "removed", workspace: "", confirmed: "confirmed-workspace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &Account{Credentials: map[string]any{"email": "source@example.test", "chatgpt_account_id": test.workspace, "plan_type": "team"}}
			input := KiloExecutorTaskInput{OrganizationID: "distinct-kilo-organization"}
			err := setKiloExecutorExpectedIdentity(&input, source, &kiloOnboardingRuntime{SourceWorkspace: test.confirmed})
			require.Equal(t, "KILO_SOURCE_CHANGED", infraerrors.Reason(err))
			require.Empty(t, input.ExpectedIdentity.SourceWorkspaceID)
		})
	}
	for _, plan := range []any{nil, "", 42, "unknown-plan"} {
		source := &Account{Credentials: map[string]any{"email": "source@example.test", "chatgpt_account_id": "current-workspace", "plan_type": plan}}
		var input KiloExecutorTaskInput
		require.NoError(t, setKiloExecutorExpectedIdentity(&input, source, &kiloOnboardingRuntime{}))
		require.Equal(t, "current-workspace", input.ExpectedIdentity.SourceWorkspaceID)
		expectedPlan, _ := plan.(string)
		require.Equal(t, expectedPlan, input.ExpectedIdentity.SourcePlanType)
	}
	source := &Account{Credentials: map[string]any{"email": "source@example.test", "chatgpt_account_id": "current-workspace", "plan_type": "plus"}}
	var input KiloExecutorTaskInput
	state := &kiloOnboardingRuntime{}
	require.NoError(t, setKiloExecutorExpectedIdentity(&input, source, state))
	require.Equal(t, "current-workspace", input.ExpectedIdentity.SourceWorkspaceID)
	require.Equal(t, "current-workspace", state.SourceWorkspace)
}

func TestKiloOnboardingExecutorRejectsLateOrCrossPhaseReceipt(t *testing.T) {
	job := &kiloClaim{ID: 14, Revision: 5}
	state := &kiloOnboardingRuntime{kiloOnboardingState: kiloOnboardingState{TaskID: "task-owned", Phase: "connect_subscription", InstanceID: "instance-owned"}}
	good := KiloExecutorTask{TaskID: state.TaskID, InstanceID: state.InstanceID, JobID: job.ID, Revision: job.Revision, Phase: state.Phase}
	require.NoError(t, validateKiloExecutorTask(job, state, &good))
	for _, change := range []func(*KiloExecutorTask){
		func(result *KiloExecutorTask) { result.Revision-- },
		func(result *KiloExecutorTask) { result.JobID++ },
		func(result *KiloExecutorTask) { result.Phase = "login" },
		func(result *KiloExecutorTask) { result.InstanceID = "restarted" },
		func(result *KiloExecutorTask) { result.TaskID = "other-task" },
	} {
		bad := good
		change(&bad)
		var failure *kiloOnboardingFailure
		require.ErrorAs(t, validateKiloExecutorTask(job, state, &bad), &failure)
		require.Equal(t, "STALE_EXECUTION", failure.code)
	}
}

func TestKiloOnboardingSubscriptionEvidenceUsesCurrentSpaceAndOperation(t *testing.T) {
	now := time.Now().UTC()
	job := &kiloClaim{OrganizationID: "expected-organization"}
	state := &kiloOnboardingRuntime{DeadlineAt: now.Add(14 * time.Minute), SourceWorkspace: "source-oauth-workspace"}
	good := KiloExecutorTask{BYOKEnabled: true, ConnectionEmail: "source@example.test", ConnectionWorkspaceID: state.SourceWorkspace, OrganizationID: job.OrganizationID, BYOKEnableEvidence: "fresh_delegated_authorization", ConnectionSubject: "official-client-subject", ConnectionAt: now}
	require.NoError(t, validateKiloSubscriptionEvidence(job, state, &good, "source@example.test", now))
	for _, change := range []func(*KiloExecutorTask){
		func(task *KiloExecutorTask) { task.OrganizationID = "" },
		func(task *KiloExecutorTask) { task.ConnectionWorkspaceID = "" },
		func(task *KiloExecutorTask) { task.ConnectionWorkspaceID = "other-workspace" },
		func(task *KiloExecutorTask) { task.ConnectionWorkspaceID = job.OrganizationID },
		func(task *KiloExecutorTask) { task.ConnectionSubject = "" },
		func(task *KiloExecutorTask) { task.BYOKEnableEvidence = "connected" },
		func(task *KiloExecutorTask) { task.ConnectionAt = now.Add(-time.Hour) },
		func(task *KiloExecutorTask) { task.ConnectionAt = now.Add(5 * time.Minute) },
		func(task *KiloExecutorTask) { task.ConnectionEmail = "other@example.test" },
	} {
		bad := good
		change(&bad)
		require.Error(t, validateKiloSubscriptionEvidence(job, state, &bad, "source@example.test", now))
	}
}

func TestKiloOnboardingSubscriptionReceiptRetainsWorkspaceForActivation(t *testing.T) {
	job := &kiloClaim{OrganizationID: "distinct-kilo-organization"}
	state := &kiloOnboardingRuntime{Version: 1, SourceWorkspace: "source-oauth-workspace", BYOKEnableEvidence: "fresh_delegated_authorization", ConnectionSubject: "official-client-subject", ConnectionAt: time.Now().UTC(), ConnectionOrganization: job.OrganizationID, ConnectionWorkspaceID: "source-oauth-workspace", kiloOnboardingState: kiloOnboardingState{BYOKEnabled: true}}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	cipher, err := (kiloWorkerCipher{}).Encrypt(string(raw))
	require.NoError(t, err)
	stored, err := (&KiloService{encryptor: kiloWorkerCipher{}}).readOnboardingState(cipher)
	require.NoError(t, err)
	require.Equal(t, state.ConnectionWorkspaceID, stored.ConnectionWorkspaceID)
	require.NoError(t, validateKiloSubscriptionReceipt(job, stored))
	for _, workspace := range []string{"", "other-workspace", job.OrganizationID} {
		stored.ConnectionWorkspaceID = workspace
		require.Equal(t, "BYOK_ENABLE_UNVERIFIED", infraerrors.Reason(validateKiloSubscriptionReceipt(job, stored)))
	}
}

func TestKiloPersonalSubjectEvidenceDoesNotInventWorkspace(t *testing.T) {
	now := time.Now().UTC()
	job := &kiloClaim{}
	state := &kiloOnboardingRuntime{DeadlineAt: now.Add(14 * time.Minute), SourceWorkspace: "source-personal-workspace", SourcePlanType: "plus", SourceSubject: "auth0|original", SourceIssuer: "https://auth.openai.com"}
	good := KiloExecutorTask{BYOKEnabled: true, ConnectionEmail: "source@example.test", ConnectionIdentityEvidence: "personal_subject", ConnectionSubject: state.SourceSubject, ConnectionAt: now, BYOKEnableEvidence: "fresh_delegated_authorization"}
	require.NoError(t, validateKiloSubscriptionEvidence(job, state, &good, "source@example.test", now))
	for _, change := range []func(*KiloExecutorTask){
		func(result *KiloExecutorTask) { result.ConnectionSubject = "auth0|other" },
		func(result *KiloExecutorTask) { result.ConnectionSubject = "" },
		func(result *KiloExecutorTask) { result.ConnectionWorkspaceID = state.SourceWorkspace },
		func(result *KiloExecutorTask) { result.ConnectionIdentityEvidence = "unknown" },
	} {
		bad := good
		change(&bad)
		require.Error(t, validateKiloSubscriptionEvidence(job, state, &bad, "source@example.test", now))
	}
	state.BYOKEnabled, state.BYOKEnableEvidence, state.ConnectionSubject, state.ConnectionAt = true, good.BYOKEnableEvidence, good.ConnectionSubject, now
	state.ConnectionIdentityEvidence = good.ConnectionIdentityEvidence
	require.NoError(t, validateKiloSubscriptionReceipt(job, state))
	for _, plan := range []string{"business", "team", "self_serve_business_prolite", "", "unknown"} {
		state.SourcePlanType = plan
		require.Error(t, validateKiloSubscriptionEvidence(job, state, &good, "source@example.test", now))
		require.Error(t, validateKiloSubscriptionReceipt(job, state))
	}
}

func kiloWorkerAssociationFixture(now time.Time) (*kiloClaim, *kiloOnboardingRuntime, KiloExecutorTask) {
	job := &kiloClaim{OrganizationID: "kilo-organization"}
	state := &kiloOnboardingRuntime{
		Version: 1, DeadlineAt: now.Add(14 * time.Minute), SourceEmail: "source@example.test",
		SourceWorkspace: "source-workspace", SourceSubject: "codex-client-subject", SourceIssuer: "https://auth.openai.com", SourcePlanType: "plus",
		kiloOnboardingState: kiloOnboardingState{RemoteUserID: "original-kilo-owner"},
	}
	task := KiloExecutorTask{
		Phase: "connect_subscription", BYOKEnabled: true, ConnectionEmail: state.SourceEmail,
		ConnectionIdentityEvidence: "credential_snapshot", ConnectionSubject: "kilo-client-subject", ConnectionAt: now,
		OrganizationID: job.OrganizationID, BYOKEnableEvidence: "fresh_delegated_authorization",
		VerifiedIdentity: KiloExecutorIdentity{KiloUserID: state.RemoteUserID, OpenAIEmail: state.SourceEmail},
		IdentityAssociation: &KiloCredentialIdentityAssociation{
			Version: 1, Method: "credential_snapshot", SourceIssuer: state.SourceIssuer, SourceSubject: state.SourceSubject,
			SourceWorkspaceID: state.SourceWorkspace, SourcePlanType: state.SourcePlanType,
			KiloUserID: state.RemoteUserID, KiloClientID: "kilo-client", KiloSubject: "kilo-client-subject", VerifiedAt: now,
		},
	}
	return job, state, task
}

func TestKiloCredentialAssociationPreservesDistinctSubjectsAndReceiptTime(t *testing.T) {
	now := time.Now().UTC()
	job, state, task := kiloWorkerAssociationFixture(now)
	require.NotEqual(t, task.IdentityAssociation.SourceSubject, task.ConnectionSubject)
	require.NoError(t, validateKiloSubscriptionEvidence(job, state, &task, state.SourceEmail, now))
	for name, mutate := range map[string]func(*KiloCredentialIdentityAssociation){
		"version":         func(a *KiloCredentialIdentityAssociation) { a.Version++ },
		"method":          func(a *KiloCredentialIdentityAssociation) { a.Method = "email_match" },
		"issuer":          func(a *KiloCredentialIdentityAssociation) { a.SourceIssuer = "https://other.invalid" },
		"source":          func(a *KiloCredentialIdentityAssociation) { a.SourceSubject = task.ConnectionSubject },
		"workspace":       func(a *KiloCredentialIdentityAssociation) { a.SourceWorkspaceID = "other-workspace" },
		"plan":            func(a *KiloCredentialIdentityAssociation) { a.SourcePlanType = "team" },
		"owner":           func(a *KiloCredentialIdentityAssociation) { a.KiloUserID = "other-owner" },
		"client":          func(a *KiloCredentialIdentityAssociation) { a.KiloClientID = " \t" },
		"connection_sub":  func(a *KiloCredentialIdentityAssociation) { a.KiloSubject = a.SourceSubject },
		"unverified_time": func(a *KiloCredentialIdentityAssociation) { a.VerifiedAt = time.Time{} },
		"before_phase":    func(a *KiloCredentialIdentityAssociation) { a.VerifiedAt = now.Add(-2 * time.Minute) },
		"after_deadline":  func(a *KiloCredentialIdentityAssociation) { a.VerifiedAt = state.DeadlineAt.Add(time.Second) },
		"future":          func(a *KiloCredentialIdentityAssociation) { a.VerifiedAt = now.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := task
			association := *task.IdentityAssociation
			mutate(&association)
			bad.IdentityAssociation = &association
			require.Error(t, validateKiloSubscriptionEvidence(job, state, &bad, state.SourceEmail, now))
		})
	}
	wrongOwner := task
	wrongOwner.VerifiedIdentity.KiloUserID = "other-owner"
	require.Error(t, validateKiloSubscriptionEvidence(job, state, &wrongOwner, state.SourceEmail, now))
	for _, plan := range []string{"team", "business", "unknown"} {
		otherState, otherTask := *state, task
		association := *task.IdentityAssociation
		otherState.SourcePlanType, association.SourcePlanType = plan, plan
		otherTask.IdentityAssociation = &association
		require.Error(t, validateKiloSubscriptionEvidence(job, &otherState, &otherTask, state.SourceEmail, now))
	}
	missing := task
	missing.IdentityAssociation = nil
	require.Error(t, validateKiloSubscriptionEvidence(job, state, &missing, state.SourceEmail, now))
	inventedWorkspace := task
	inventedWorkspace.ConnectionWorkspaceID = state.SourceWorkspace
	require.Error(t, validateKiloSubscriptionEvidence(job, state, &inventedWorkspace, state.SourceEmail, now))

	state.BYOKEnabled, state.BYOKEnableEvidence = true, task.BYOKEnableEvidence
	state.ConnectionSubject, state.ConnectionAt, state.ConnectionOrganization = task.ConnectionSubject, task.ConnectionAt, task.OrganizationID
	state.ConnectionIdentityEvidence, state.IdentityAssociation = task.ConnectionIdentityEvidence, task.IdentityAssociation
	state.DeadlineAt = time.Time{}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	cipher, err := (kiloWorkerCipher{}).Encrypt(string(raw))
	require.NoError(t, err)
	stored, err := (&KiloService{encryptor: kiloWorkerCipher{}}).readOnboardingState(cipher)
	require.NoError(t, err)
	require.NoError(t, validateKiloSubscriptionReceipt(job, stored))
	require.True(t, task.IdentityAssociation.VerifiedAt.Equal(stored.IdentityAssociation.VerifiedAt))
	require.Equal(t, state.SourceSubject, stored.IdentityAssociation.SourceSubject)
	require.Equal(t, task.ConnectionSubject, stored.ConnectionSubject)
	for name, mutate := range map[string]func(*KiloCredentialIdentityAssociation){
		"owner":     func(a *KiloCredentialIdentityAssociation) { a.KiloUserID = "other-owner" },
		"source":    func(a *KiloCredentialIdentityAssociation) { a.SourceSubject = "other-source" },
		"workspace": func(a *KiloCredentialIdentityAssociation) { a.SourceWorkspaceID = "other-workspace" },
		"plan":      func(a *KiloCredentialIdentityAssociation) { a.SourcePlanType = "team" },
		"subject":   func(a *KiloCredentialIdentityAssociation) { a.KiloSubject = a.SourceSubject },
		"method":    func(a *KiloCredentialIdentityAssociation) { a.Method = "personal_subject" },
	} {
		t.Run("receipt_"+name, func(t *testing.T) {
			bad := *stored
			association := *stored.IdentityAssociation
			mutate(&association)
			bad.IdentityAssociation = &association
			require.Error(t, validateKiloSubscriptionReceipt(job, &bad))
		})
	}
}

func TestKiloExistingConnectionObservationCannotBecomeActivationReceipt(t *testing.T) {
	now := time.Now().UTC()
	job, state, task := kiloWorkerAssociationFixture(now)
	task.BYOKEnabled, task.BYOKEnableEvidence = false, "existing_connection_enablement_unknown"
	task.ConnectionAt = now.Add(-30 * 24 * time.Hour)
	require.NoError(t, validateKiloExistingConnectionObservation(job, state, &task, state.SourceEmail, now))
	require.Error(t, validateKiloSubscriptionEvidence(job, state, &task, state.SourceEmail, now))
	for name, mutate := range map[string]func(*KiloExecutorTask){
		"enabled":        func(task *KiloExecutorTask) { task.BYOKEnabled = true },
		"organization":   func(task *KiloExecutorTask) { task.OrganizationID = "other-organization" },
		"owner":          func(task *KiloExecutorTask) { task.VerifiedIdentity.KiloUserID = "other-owner" },
		"email":          func(task *KiloExecutorTask) { task.ConnectionEmail = "other@example.test" },
		"workspace":      func(task *KiloExecutorTask) { task.ConnectionWorkspaceID = state.SourceWorkspace },
		"subject":        func(task *KiloExecutorTask) { task.ConnectionSubject = state.SourceSubject },
		"no_association": func(task *KiloExecutorTask) { task.IdentityAssociation = nil },
		"future":         func(task *KiloExecutorTask) { task.ConnectionAt = now.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := task
			mutate(&bad)
			require.Error(t, validateKiloExistingConnectionObservation(job, state, &bad, state.SourceEmail, now))
		})
	}
	state.BYOKEnabled, state.BYOKEnableEvidence = false, task.BYOKEnableEvidence
	state.ConnectionSubject, state.ConnectionAt, state.ConnectionOrganization = task.ConnectionSubject, task.ConnectionAt, task.OrganizationID
	state.ConnectionIdentityEvidence, state.IdentityAssociation = task.ConnectionIdentityEvidence, task.IdentityAssociation
	require.Equal(t, "BYOK_ENABLE_UNVERIFIED", infraerrors.Reason(validateKiloSubscriptionReceipt(job, state)))
	state.BYOKEnabled = true
	require.Equal(t, "BYOK_ENABLE_UNVERIFIED", infraerrors.Reason(validateKiloSubscriptionReceipt(job, state)))
}

type kiloWorkerObservationAccounts struct {
	AccountRepository
	source *Account
}

func (r *kiloWorkerObservationAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.source == nil || r.source.ID != id {
		return nil, errors.New("unexpected source account")
	}
	return r.source, nil
}

func TestKiloExistingObservationPersistsFailedBeforeAckAndKeepsIdentity(t *testing.T) {
	for _, reconcile := range []bool{false, true} {
		t.Run(map[bool]string{false: "connect_unknown", true: "reconcile_read_only"}[reconcile], func(t *testing.T) {
			now := time.Now().UTC()
			job, state, task := kiloWorkerAssociationFixture(now)
			if reconcile {
				job, state, task = kiloWorkerReconcileFixture(now)
			}
			sourceID, proxyID := int64(133), int64(7)
			job.ID, job.Revision, job.Stage, job.Lease = 14, 5, "subscription_authorization", uuid.NewString()
			job.SourceID, job.LoginProxyID, job.ReusedIdentity = &sourceID, &proxyID, !reconcile
			job.Kind = KiloJobOnboardSource
			source := &Account{ID: sourceID, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"email": state.SourceEmail, "chatgpt_account_id": state.SourceWorkspace, "plan_type": state.SourcePlanType,
					"id_token": reauthTestJWT(map[string]any{"iss": state.SourceIssuer, "sub": state.SourceSubject, "email": state.SourceEmail, "aud": []string{"app_EMoamEEZ73f0CkXaXp7hrann"}, "exp": now.Add(time.Hour).Unix()})},
				Extra: map[string]any{"kilo_quota_only": true, "kilo_onboarding_job_id": job.ID, "kilo_onboarding_proxy_id": proxyID},
			}
			job.SourceIdentity = kiloSourceIdentity(source)
			state.TaskID, state.InstanceID, state.Phase = "owned-task", "owned-instance", task.Phase
			state.OperationID, state.SessionHandle, state.ExpiresAt = uuid.NewString(), "original-session", now.Add(-time.Minute)
			state.IdentitySessionMaterial = "original-private-session"
			state.IdentityCredential = &KiloExecutorCredential{Email: state.SourceEmail, Password: "synthetic-password", TOTPSecret: "synthetic-totp"}
			task.TaskID, task.InstanceID, task.JobID, task.Revision = state.TaskID, state.InstanceID, job.ID, job.Revision
			task.Status, task.BYOKEnabled = "succeeded", false
			if !reconcile {
				task.BYOKEnableEvidence = "existing_connection_enablement_unknown"
			}
			task.ConnectionAt, task.IdentitySessionMaterial = now.Add(-30*24*time.Hour), "must-not-replace-original"
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectBegin()
			kiloWorkerLeaseExpectation(mock, job, true)
			mock.ExpectExec("UPDATE pk_kilo_jobs SET stage=.*status=.*remote_state_encrypted=").WithArgs(
				job.ID, job.Revision, job.Lease, "subscription_authorization", "failed", sqlmock.AnyArg(), "confirmed", "", nil, false, false, false,
				"BYOK_ENABLE_UNVERIFIED", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), true, "", "",
			).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			acknowledged := make(chan bool, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				acknowledged <- r.Method == http.MethodPost && r.URL.Path == "/v1/tasks/owned-task/ack" && job.RemoteState != "" && mock.ExpectationsWereMet() == nil
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			svc := &KiloService{db: db, accounts: &kiloWorkerObservationAccounts{source: source}, encryptor: kiloWorkerCipher{}, executor: &kiloExecutorClient{base: server.URL, token: strings.Repeat("x", 32), client: server.Client()}}
			require.NoError(t, svc.acceptOnboardingTask(context.Background(), job, state, &task))
			select {
			case durable := <-acknowledged:
				require.True(t, durable, "the failed observation must commit before acknowledgement")
			default:
				t.Fatal("the durably stored task was not acknowledged")
			}
			stored, err := svc.readOnboardingState(job.RemoteState)
			require.NoError(t, err)
			require.False(t, job.SubscriptionConnected)
			require.False(t, job.CredentialReady)
			require.False(t, stored.BYOKEnabled)
			require.False(t, stored.DeviceApproved)
			require.Equal(t, "subscription_authorization", job.Stage)
			require.Equal(t, "original-private-session", stored.IdentitySessionMaterial)
			require.Equal(t, state.IdentityCredential, stored.IdentityCredential)
			require.Equal(t, "original-session", stored.SessionHandle)
			require.Equal(t, task.ConnectionEmail, stored.ConnectionEmail)
			require.Equal(t, task.ConnectionSubject, stored.ConnectionSubject)
			require.True(t, task.ConnectionAt.Equal(stored.ConnectionAt))
			require.True(t, task.IdentityAssociation.VerifiedAt.Equal(stored.IdentityAssociation.VerifiedAt))
			require.Empty(t, stored.ConnectionWorkspaceID)
			require.Empty(t, stored.TaskID)
			require.Equal(t, []string{task.TaskID}, stored.AckTaskIDs)
			storedJSON, err := svc.encryptor.Decrypt(job.RemoteState)
			require.NoError(t, err)
			require.NotContains(t, storedJSON, source.GetCredential("id_token"))
			require.Equal(t, "BYOK_ENABLE_UNVERIFIED", infraerrors.Reason(validateKiloSubscriptionReceipt(job, stored)))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func kiloWorkerReconcileFixture(now time.Time) (*kiloClaim, *kiloOnboardingRuntime, KiloExecutorTask) {
	job, state, task := kiloWorkerAssociationFixture(now)
	job.OrganizationID, task.OrganizationID = "", ""
	state.ReconcileSubscription = true
	task.Phase, task.Status, task.BYOKEnabled, task.BYOKEnableEvidence = "reconcile_subscription", "succeeded", false, ""
	task.ConnectionAt = now.Add(-30 * 24 * time.Hour)
	task.CredentialIdentityEvidence = &KiloCredentialIdentityEvidence{
		Version: 1, Method: "credential_snapshot_password_totp", KiloUserID: state.RemoteUserID, KiloClientID: task.IdentityAssociation.KiloClientID,
		SourceClientID: kiloSnapshotSourceClientID, SourceIssuer: state.SourceIssuer, SourceSubject: state.SourceSubject,
		SourceWorkspaceID: state.SourceWorkspace, SourcePlanType: state.SourcePlanType, ConnectionSubject: task.ConnectionSubject,
		ProviderCreatedAt: task.ConnectionAt.Add(-time.Hour), ProviderFingerprint: strings.Repeat("a", 64), ObservedAt: now,
		KiloPasswordTOTPVerified: true, SourcePasswordTOTPVerified: true, SourceSnapshotSignatureVerified: true, SourceSignatureVerified: true, NonceVerified: true, ProviderUnchanged: true,
	}
	task.ConnectionObservation = &KiloConnectionObservation{
		Version: 1, Method: "existing_delegated_connection", KiloUserID: state.RemoteUserID, OpenAIEmail: state.SourceEmail,
		OpenAISubject: task.ConnectionSubject, ConnectedAt: task.ConnectionAt, ObservedAt: now,
		ProviderCreatedAt: task.CredentialIdentityEvidence.ProviderCreatedAt, ProviderFingerprint: task.CredentialIdentityEvidence.ProviderFingerprint,
		ConnectionUnchanged: true, ProviderUnchanged: true, EnabledEvidence: "unverified",
	}
	return job, state, task
}

func TestKiloReconcileRequiresAssociationAndProviderContinuity(t *testing.T) {
	now := time.Now().UTC()
	job, state, task := kiloWorkerReconcileFixture(now)
	state.CredentialIdentityEvidence = task.CredentialIdentityEvidence
	require.NoError(t, validateKiloReconcileObservation(job, state, &task, state.SourceEmail, now))
	for name, mutate := range map[string]func(*KiloExecutorTask){
		"no_association":           func(task *KiloExecutorTask) { task.IdentityAssociation = nil },
		"no_evidence":              func(task *KiloExecutorTask) { task.CredentialIdentityEvidence = nil },
		"no_observation":           func(task *KiloExecutorTask) { task.ConnectionObservation = nil },
		"client_disagreement":      func(task *KiloExecutorTask) { task.IdentityAssociation.KiloClientID = "another-client" },
		"source_signature":         func(task *KiloExecutorTask) { task.CredentialIdentityEvidence.SourceSignatureVerified = false },
		"snapshot_signature":       func(task *KiloExecutorTask) { task.CredentialIdentityEvidence.SourceSnapshotSignatureVerified = false },
		"totp":                     func(task *KiloExecutorTask) { task.CredentialIdentityEvidence.KiloPasswordTOTPVerified = false },
		"nonce":                    func(task *KiloExecutorTask) { task.CredentialIdentityEvidence.NonceVerified = false },
		"provider_changed":         func(task *KiloExecutorTask) { task.ConnectionObservation.ProviderUnchanged = false },
		"connection_changed":       func(task *KiloExecutorTask) { task.ConnectionObservation.ConnectionUnchanged = false },
		"fingerprint_disagreement": func(task *KiloExecutorTask) { task.ConnectionObservation.ProviderFingerprint = strings.Repeat("b", 64) },
		"previous_provider_changed": func(task *KiloExecutorTask) {
			task.ConnectionObservation.ProviderFingerprint = strings.Repeat("b", 64)
			task.CredentialIdentityEvidence.ProviderFingerprint = task.ConnectionObservation.ProviderFingerprint
		},
		"future_association": func(task *KiloExecutorTask) { task.IdentityAssociation.VerifiedAt = now.Add(time.Second) },
		"future_observation": func(task *KiloExecutorTask) {
			task.ConnectionObservation.ObservedAt = now.Add(time.Second)
			task.CredentialIdentityEvidence.ObservedAt = task.ConnectionObservation.ObservedAt
		},
		"pretend_enabled": func(task *KiloExecutorTask) { task.BYOKEnabled = true },
	} {
		t.Run(name, func(t *testing.T) {
			bad := task
			association, evidence, observation := *task.IdentityAssociation, *task.CredentialIdentityEvidence, *task.ConnectionObservation
			bad.IdentityAssociation, bad.CredentialIdentityEvidence, bad.ConnectionObservation = &association, &evidence, &observation
			mutate(&bad)
			require.Error(t, validateKiloReconcileObservation(job, state, &bad, state.SourceEmail, now))
		})
	}
}

func TestKiloReconcileStaysKeylessAndCannotLeaveObservationPhase(t *testing.T) {
	job, state, _ := kiloWorkerReconcileFixture(time.Now().UTC())
	state.IdentitySessionMaterial = "original-private-session"
	identity, err := kiloReconcileIdentity(state)
	require.NoError(t, err)
	require.Empty(t, identity.APIKey)
	require.Equal(t, state.RemoteUserID, identity.UserID)
	require.Equal(t, state.IdentitySessionMaterial, identity.SessionMaterial)
	for _, phase := range []string{"login", "resume_identity", "connect_subscription", "approve_device"} {
		err = (&KiloService{}).runOnboardingPhase(context.Background(), job, state, phase, nil)
		var failure *kiloOnboardingFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "ONBOARDING_STAGE_INVALID", failure.code)
	}
	require.Zero(t, state.SessionRebuilds)
	require.False(t, state.DeviceApproved)
}

type kiloRecoveryStateArgument func(driver.Value) bool

func (match kiloRecoveryStateArgument) Match(value driver.Value) bool { return match(value) }

func TestKiloReconcileAdmissionRebindsOnlyClosedObservations(t *testing.T) {
	cases := []struct {
		name, code, pending              string
		observation, reject, reauthorize bool
	}{
		{name: "explicit_original_identity_failure", code: "IDENTITY_MISMATCH", reauthorize: true},
		{name: "explicit_enablement_unknown_rejected", code: "BYOK_ENABLE_UNVERIFIED", reauthorize: true, reject: true},
		{name: "original_identity_failure", code: "IDENTITY_MISMATCH"},
		{name: "unverified_enablement", code: "BYOK_ENABLE_UNVERIFIED"},
		{name: "closed_observation_session_lost", code: "SESSION_LOST", observation: true},
		{name: "other_session_lost", code: "SESSION_LOST", reject: true},
		{name: "expired_session", code: "AUTH_SESSION_EXPIRED", observation: true, reject: true},
		{name: "unknown_error", code: "REMOTE_OUTCOME_UNKNOWN", observation: true, reject: true},
		{name: "proxy_changed", code: "SESSION_LOST", observation: true, pending: "proxy", reject: true},
		{name: "material_changed", code: "SESSION_LOST", observation: true, pending: "material", reject: true},
	}
	for _, field := range []string{"remote_closed", "operation_id", "task_id", "phase", "remote_lease_id", "ack_task_ids", "deadline_at", "session_handle"} {
		cases = append(cases, struct {
			name, code, pending              string
			observation, reject, reauthorize bool
		}{name: field, code: "SESSION_LOST", observation: true, pending: field, reject: true})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			svc, mock, accounts, job := kiloCleanupFixture(t)
			source := accounts.items[*job.SourceID]
			source.Credentials = map[string]any{"email": "user@example.com", "chatgpt_account_id": "acct-1", "plan_type": "plus",
				"id_token": reauthTestJWT(map[string]any{"iss": "https://auth.openai.com", "sub": "auth0|original", "email": "user@example.com", "aud": []string{kiloSnapshotSourceClientID}, "exp": time.Now().Add(time.Hour).Unix()})}
			job.Stage, job.SourceIdentity = "subscription_authorization", kiloSourceIdentity(source)
			reauth, reader, repo, _, oauth, _ := newReauthTestService("acct-1")
			savePasswordReauthConfig(t, reauth)
			reader.account, repo.config.AccountID = source, source.ID
			reader.proxy = &Proxy{ID: *job.LoginProxyID, Protocol: "http", Host: "203.0.113.4", Port: 8080, Status: StatusActive}
			svc.reauth, svc.proxies = reauth, &reauthTestProxyRepo{reader: reader}
			material, err := reauth.ReadKiloLoginMaterial(context.Background(), source.ID)
			require.NoError(t, err)
			state := &kiloOnboardingRuntime{Version: 1, Revision: job.Revision, RemoteClosed: true, Failures: 2,
				SourceEmail: material.Email, SourceWorkspace: "acct-1", SourcePlanType: "plus", SourceSubject: "auth0|original", SourceIssuer: "https://auth.openai.com",
				IdentitySessionMaterial: "original-private-session", ReconcileSubscription: test.observation, ProxyHash: kiloOnboardingProxyHash(reader.proxy.URL())}
			state.RemoteUserID, state.InstanceID = "original-owner", "old-instance"
			switch test.pending {
			case "remote_closed":
				state.RemoteClosed = false
			case "operation_id":
				state.OperationID = uuid.NewString()
			case "task_id":
				state.TaskID = "old-task"
			case "phase":
				state.Phase = "reconcile_subscription"
			case "remote_lease_id":
				state.RemoteLeaseID = uuid.NewString()
			case "ack_task_ids":
				state.AckTaskIDs = []string{"old-task"}
			case "deadline_at":
				state.DeadlineAt = time.Now().Add(-time.Minute)
			case "session_handle":
				state.SessionHandle = "old-session"
			case "proxy":
				state.ProxyHash = "changed-proxy"
			}
			raw, err := json.Marshal(state)
			require.NoError(t, err)
			job.Cipher, err = svc.encryptor.Encrypt(string(raw))
			require.NoError(t, err)
			originalCipher := job.Cipher
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
			expectKiloCleanupRead(mock, job)
			early := test.pending != "" && test.pending != "proxy" && test.pending != "material"
			if !early {
				mock.ExpectQuery("SELECT error_code,organization_id,material_version,quota_oauth_ready").WithArgs(job.ID).
					WillReturnRows(sqlmock.NewRows([]string{"code", "organization", "version", "quota_ready"}).AddRow(test.code, "", material.Version, true))
				if !test.reject || test.pending == "proxy" || test.pending == "material" {
					mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloCleanupEgressLock).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectQuery("SELECT id FROM accounts WHERE id IN").WithArgs(job.SourceID, job.AccountID).
						WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(*job.SourceID).AddRow(*job.AccountID))
					mock.ExpectQuery("SELECT EXISTS.*pk_kilo_jobs.*openai_oauth_reauth_tasks.*pk_kilo_egress_leases.*pk_kilo_device_sessions").WithArgs(job.ID, job.SourceID, job.AccountID, job.PreparedID).
						WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(false))
					mock.ExpectQuery("SELECT EXISTS.*pk_kilo_device_sessions WHERE account_id").WithArgs(*job.AccountID).
						WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
					mock.ExpectQuery("SELECT id FROM proxies WHERE id").WithArgs(*job.LoginProxyID).
						WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(*job.LoginProxyID))
					if test.pending != "proxy" {
						mock.ExpectQuery("SELECT EXISTS.*pk_kilo_egress_routes").WithArgs(*job.AccountID, *job.LoginProxyID, state.ProxyHash, KiloEgressActive).
							WillReturnRows(sqlmock.NewRows([]string{"owned"}).AddRow(true))
					}
					if test.pending == "material" {
						repo.config.TOTPSecretCiphertext = "encrypted:changed-seed"
					}
				}
			}
			if test.reject {
				mock.ExpectRollback()
			} else {
				next := *state
				next.InstanceID, next.ReconcileSubscription, next.Revision, next.Failures = "", !test.reauthorize, job.Revision+1, 0
				next.ReauthorizeSubscription = test.reauthorize
				nextRaw, marshalErr := json.Marshal(&next)
				require.NoError(t, marshalErr)
				nextCipher, encryptErr := svc.encryptor.Encrypt(string(nextRaw))
				require.NoError(t, encryptErr)
				var stateArgument driver.Value = nextCipher
				if test.reauthorize {
					stateArgument = kiloRecoveryStateArgument(func(value driver.Value) bool {
						cipher, ok := value.(string)
						if !ok {
							return false
						}
						plain, decodeErr := svc.encryptor.Decrypt(cipher)
						var saved kiloOnboardingRuntime
						if decodeErr != nil || json.Unmarshal([]byte(plain), &saved) != nil || len(saved.ReauthorizationHistory) != 1 {
							return false
						}
						record := saved.ReauthorizationHistory[0]
						if record.Revision != job.Revision || record.ErrorCode != test.code || record.Outcome != job.Outcome || record.AuthorizedAt.IsZero() || record.AuthorizedAt.After(time.Now()) {
							return false
						}
						want := next
						want.ReauthorizationHistory = saved.ReauthorizationHistory
						return reflect.DeepEqual(want, saved)
					})
				}
				mock.ExpectExec("UPDATE pk_kilo_jobs SET status='queued',stage='subscription_authorization'").WithArgs(job.ID, job.Revision, job.Revision+1, stateArgument).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, http.MethodGet, r.Method)
					require.Equal(t, "/v1/health", r.URL.Path)
					_ = json.NewEncoder(w).Encode(KiloExecutorHealth{OK: true, ProtocolVersion: 1, InstanceID: "current-instance", AvailableCapacity: 1,
						Capabilities: []string{"login", "connect_subscription", "approve_device", "fixed_proxy", "cancel", "ack", "session_ttl", "workspace_identity", "reconcile_subscription"}})
				}))
				defer server.Close()
				svc.executor = &kiloExecutorClient{base: server.URL, token: strings.Repeat("x", 32), client: server.Client()}
				_, err = svc.ensureOnboardingExecutor(context.Background(), state, "reconcile_subscription", true)
				var failure *kiloOnboardingFailure
				require.ErrorAs(t, err, &failure)
				require.Equal(t, "SESSION_LOST", failure.code, "ongoing instance fencing must remain unchanged")
				_, err = svc.ensureOnboardingExecutor(context.Background(), &next, "reconcile_subscription", true)
				require.NoError(t, err, "the separately admitted observation may bind the current instance")
			}
			if test.reauthorize {
				err = svc.ReauthorizeSubscription(context.Background(), job.ID, job.Revision)
			} else {
				err = svc.ReconcileSubscription(context.Background(), job.ID, job.Revision)
			}
			if test.reject {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, "old-instance", state.InstanceID)
			require.Equal(t, originalCipher, job.Cipher)
			require.Empty(t, accounts.updated)
			require.Empty(t, accounts.deleted)
			require.Zero(t, repo.createCalls)
			require.Zero(t, oauth.exchanges)
		})
	}
}

func TestKiloJobRecoveryViewRequiresClosedOriginalOperation(t *testing.T) {
	id := int64(42)
	job := KiloJob{ID: 14, Revision: 3, JobKind: KiloJobOnboardSource, Stage: "subscription_authorization", Status: "failed", SourceAccountID: &id, AccountID: &id, LoginProxyID: &id, QuotaOAuthReady: true, ErrorCode: "IDENTITY_MISMATCH"}
	snapshot := kiloJobRecoverySnapshot{kiloFailedJob: kiloFailedJob{ID: job.ID, Revision: job.Revision, Kind: job.JobKind, Stage: job.Stage, Status: job.Status, SourceID: &id, AccountID: &id, LoginProxyID: &id}, MaterialVersion: "original-material"}
	state := &kiloOnboardingRuntime{Version: 1, Revision: job.Revision, RemoteClosed: true, SourceEmail: "fixture@example.test", SourcePlanType: "plus", IdentitySessionMaterial: "fixture-session", ProxyHash: "fixture-proxy"}
	state.RemoteUserID = "fixture-original-owner"
	require.Equal(t, []string{kiloJobRecoveryReconcile, kiloJobRecoveryReauthorize}, kiloJobSubscriptionRecoveryActions(&job, &snapshot, state))
	require.False(t, kiloJobRetryAllowed(&snapshot.kiloFailedJob, state))
	for _, field := range []string{"revision", "remote_closed", "operation", "task", "phase", "lease", "ack", "deadline", "session", "live_lease", "prepared", "connected", "material", "quota"} {
		t.Run(field, func(t *testing.T) {
			j, stored, receipt := job, snapshot, *state
			switch field {
			case "revision":
				receipt.Revision++
			case "remote_closed":
				receipt.RemoteClosed = false
			case "operation":
				receipt.OperationID = "original-operation"
			case "task":
				receipt.TaskID = "original-task"
			case "phase":
				receipt.Phase = "connect_subscription"
			case "lease":
				receipt.RemoteLeaseID = "original-lease"
			case "ack":
				receipt.AckTaskIDs = []string{"original-task"}
			case "deadline":
				receipt.DeadlineAt = time.Now().Add(-time.Minute)
			case "session":
				receipt.SessionHandle = "original-session"
			case "live_lease":
				stored.LiveLease = true
			case "prepared":
				stored.PreparedID = &id
			case "connected":
				stored.Connected = true
			case "material":
				stored.MaterialVersion = ""
			case "quota":
				j.QuotaOAuthReady = false
			}
			require.Empty(t, kiloJobSubscriptionRecoveryActions(&j, &stored, &receipt))
		})
	}
	for _, code := range []string{"BYOK_ENABLE_UNVERIFIED", "SESSION_LOST"} {
		job.ErrorCode, state.ReconcileSubscription = code, true
		require.Equal(t, []string{kiloJobRecoveryReconcile}, kiloJobSubscriptionRecoveryActions(&job, &snapshot, state))
	}
	job.ErrorCode, state.ReconcileSubscription = "SESSION_LOST", false
	require.Empty(t, kiloJobSubscriptionRecoveryActions(&job, &snapshot, state))
	state.OperationID, state.RemoteClosed, snapshot.Outcome = "original-operation", false, "unknown"
	require.True(t, kiloJobRetryAllowed(&snapshot.kiloFailedJob, state))
	require.Empty(t, kiloJobSubscriptionRecoveryActions(&job, &snapshot, state))
}

func TestKiloExpectedPersonalSubjectComesFromOfficialToken(t *testing.T) {
	source := &Account{Credentials: map[string]any{"email": "source@example.test", "chatgpt_account_id": "workspace", "plan_type": "plus"}}
	for _, test := range []struct {
		issuer, email, audience string
		valid                   bool
	}{
		{"https://auth.openai.com", "source@example.test", "app_EMoamEEZ73f0CkXaXp7hrann", true},
		{"https://other.invalid", "source@example.test", "app_EMoamEEZ73f0CkXaXp7hrann", false},
		{"https://auth.openai.com", "other@example.test", "app_EMoamEEZ73f0CkXaXp7hrann", false},
		{"https://auth.openai.com", "source@example.test", "other-client", false},
	} {
		source.Credentials["id_token"] = reauthTestJWT(map[string]any{"iss": test.issuer, "sub": "auth0|original", "email": test.email, "aud": []string{test.audience}, "exp": time.Now().Add(time.Hour).Unix()})
		var input KiloExecutorTaskInput
		state := &kiloOnboardingRuntime{}
		require.NoError(t, setKiloExecutorExpectedIdentity(&input, source, state))
		if test.valid {
			require.Equal(t, "auth0|original", input.ExpectedIdentity.SourceSubject)
			require.Equal(t, test.issuer, state.SourceIssuer)
		} else {
			require.Empty(t, input.ExpectedIdentity.SourceSubject)
			require.Empty(t, state.SourceSubject)
		}
	}
}

func TestKiloHistoricalSnapshotRequiresExistingSubject(t *testing.T) {
	for _, test := range []struct {
		name, subject, issuer string
		anchored, rejected    bool
	}{
		{name: "same_expired_snapshot", subject: "auth0|original", issuer: "https://auth.openai.com", anchored: true},
		{name: "unanchored_expired_snapshot", subject: "auth0|original", issuer: "https://auth.openai.com"},
		{name: "changed_subject", subject: "auth0|other", issuer: "https://auth.openai.com", anchored: true, rejected: true},
		{name: "changed_issuer", subject: "auth0|original", issuer: "https://other.invalid", anchored: true, rejected: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &Account{Credentials: map[string]any{"email": "source@example.test", "chatgpt_account_id": "workspace", "plan_type": "plus",
				"id_token": reauthTestJWT(map[string]any{"iss": test.issuer, "sub": test.subject, "email": "source@example.test", "aud": []string{kiloSnapshotSourceClientID}, "exp": time.Now().Add(-time.Hour).Unix()})}}
			state := &kiloOnboardingRuntime{}
			if test.anchored {
				state.SourceSubject, state.SourceIssuer = "auth0|original", "https://auth.openai.com"
			}
			input := KiloExecutorTaskInput{Phase: "reauthorize_subscription"}
			err := setKiloExecutorExpectedIdentity(&input, source, state)
			if test.rejected {
				require.Equal(t, "KILO_SOURCE_CHANGED", infraerrors.Reason(err))
				return
			}
			require.NoError(t, err)
			if test.anchored {
				require.Equal(t, "auth0|original", input.ExpectedIdentity.SourceSubject)
				require.Equal(t, "https://auth.openai.com", input.ExpectedIdentity.SourceIssuer)
			} else {
				require.Empty(t, input.ExpectedIdentity.SourceSubject)
			}
			require.Equal(t, source.GetCredential("id_token"), input.SourceIDToken)
		})
	}
}

func TestKiloOnboardingModelScopeCannotAllowAll(t *testing.T) {
	for _, scope := range [][]string{nil, {}, {"*"}, {"openai/*"}, {"openai/"}, {"anthropic/claude"}, {"openai/model", "openai/model"}, {" openai/model"}, {"openai/model\nother"}} {
		_, err := kiloOnboardingModelMapping(scope)
		require.Error(t, err, "invalid scope must never become an unrestricted mapping: %v", scope)
	}
	mapping, err := kiloOnboardingModelMapping([]string{"openai/model-a", "openai/model-b"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"openai/model-a": "openai/model-a", "openai/model-b": "openai/model-b"}, mapping)
}

func TestKiloOnboardingDeviceUnknownConsumptionDoesNotPollAgain(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	id := uuid.NewString()
	job := &kiloClaim{ID: 18, Revision: 2, Lease: uuid.NewString()}
	mock.ExpectBegin()
	kiloWorkerLeaseExpectation(mock, job, true)
	mock.ExpectQuery("SELECT d.status.*FROM pk_kilo_device_sessions d").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"status", "url", "user_code", "expires", "prepared", "email", "code", "token", "proxy", "account", "due", "onboarding"}).AddRow("approved", "https://kilo.example/authorize", "ABCD", time.Now().Add(time.Minute), nil, "", "cipher:old-device-code", "", 7, 8, true, true))
	mock.ExpectRollback()
	svc := &KiloService{db: db, encryptor: kiloWorkerCipher{}}
	_, err = svc.pollDevice(withKiloClaimLease(context.Background(), job), id)
	require.Equal(t, "DEVICE_RESULT_LOST", infraerrors.Reason(err))
	// No remote, account or proxy dependency is configured. Calling any of
	// those instead of rejecting the already-started consumption would panic.
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloOnboardingPersistsOperationBeforeLeaseRelease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	job := &kiloClaim{ID: 21, Kind: KiloJobOnboardSource, Stage: "kilo_login", Revision: 4, Lease: uuid.NewString(), MaterialVersion: "material-v1", RemoteOutcome: "pending"}
	operationID := uuid.NewString()
	state := &kiloOnboardingRuntime{Version: 1, Revision: job.Revision, DeadlineAt: time.Now().Add(time.Minute), kiloOnboardingState: kiloOnboardingState{OperationID: operationID, Phase: "login", InstanceID: "instance-1"}}
	mock.ExpectBegin()
	kiloWorkerLeaseExpectation(mock, job, true)
	mock.ExpectExec("UPDATE pk_kilo_jobs SET stage=.*revision=.*lease_token=.*lease_until>NOW").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	svc := &KiloService{db: db, encryptor: kiloWorkerCipher{}}
	require.NoError(t, svc.persistOnboarding(withKiloClaimLease(context.Background(), job), job, state, kiloOnboardingProgress{Release: true}))
	stored, err := svc.readOnboardingState(job.RemoteState)
	require.NoError(t, err)
	require.Equal(t, operationID, stored.OperationID)
	require.Equal(t, job.Revision, stored.Revision)
	require.Equal(t, "login", stored.Phase)
	require.NotContains(t, job.RemoteState, operationID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloOnboardingCancellationCannotSaveDeviceReceipt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	job := &kiloClaim{ID: 24, Revision: 7, Lease: uuid.NewString()}
	mock.ExpectBegin()
	kiloWorkerLeaseExpectation(mock, job, false)
	mock.ExpectRollback()
	svc := &KiloService{db: db}
	changed, err := svc.updateDeviceReceipt(withKiloClaimLease(context.Background(), job), uuid.NewString(), "approved", "approved", "cipher:device-token", true)
	require.Error(t, err)
	require.False(t, changed)
	require.NoError(t, mock.ExpectationsWereMet())
}

type kiloPreparedMarkerAccounts struct {
	AccountRepository
	account *Account
	writes  int
}

func (r *kiloPreparedMarkerAccounts) GetByID(ctx context.Context, id int64) (*Account, error) {
	if dbent.TxFromContext(ctx) == nil || r.account == nil || r.account.ID != id {
		return nil, errors.New("prepared account must be read through its transaction")
	}
	return r.account, nil
}

func (r *kiloPreparedMarkerAccounts) LockAccountMutation(ctx context.Context) error {
	if dbent.TxFromContext(ctx) == nil {
		return errors.New("prepared account mutation requires a transaction")
	}
	return nil
}

func (r *kiloPreparedMarkerAccounts) Create(ctx context.Context, account *Account) error {
	if err := r.LockAccountMutation(ctx); err != nil {
		return err
	}
	account.ID = 23
	r.account = account
	r.writes++
	return nil
}

func (r *kiloPreparedMarkerAccounts) Update(ctx context.Context, account *Account) error {
	if err := r.LockAccountMutation(ctx); err != nil {
		return err
	}
	r.account = account
	r.writes++
	return nil
}

func TestKiloPreparedAccountPersistsBindingFailureSnapshot(t *testing.T) {
	for _, test := range []struct {
		name        string
		existing    bool
		keyOnly     bool
		oldSource   int64
		oldRevision int64
		jobRevision int64
		conflict    bool
	}{
		{name: "new_key_source_binding", jobRevision: 4},
		{name: "existing_key_missing_markers", existing: true, jobRevision: 4},
		{name: "legacy_queued_job", existing: true},
		{name: "key_only_activation", keyOnly: true, jobRevision: 4},
		{name: "changed_source_rejected", existing: true, oldSource: 99, jobRevision: 4, conflict: true},
		{name: "changed_binding_rejected", existing: true, oldRevision: 3, jobRevision: 4, conflict: true},
		{name: "stale_job_rejected", jobRevision: 3, conflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB("postgres", db)))
			cipher, err := (kiloWorkerCipher{}).Encrypt("synthetic-original-kilo-key")
			require.NoError(t, err)
			accounts := &kiloPreparedMarkerAccounts{}
			var accountID, sourceID any
			if !test.keyOnly {
				sourceID = int64(42)
			}
			if test.existing {
				accountID = int64(23)
				accounts.account = &Account{ID: 23, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled,
					Credentials: map[string]any{"api_key": "synthetic-original-kilo-key", "base_url": KiloGatewayBaseURL, "kilo_machine_id": "machine-1", "kilo_organization_id": ""},
					Extra:       map[string]any{AccountExtraKiloBridgeEnabled: true, KiloManagedExtraKey: true, "kilo_gateway_profile": "cli", "kilo_prepared_id": int64(17), KiloLoginProxyExtraKey: int64(13), "kilo_onboarding_source_id": test.oldSource, "kilo_binding_revision": test.oldRevision}}
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT account_id,name,credential_encrypted,machine_id::text,organization_id,binding_revision,binding_job_id.*FOR UPDATE").WithArgs(int64(17)).
				WillReturnRows(sqlmock.NewRows([]string{"account", "name", "key", "machine", "organization", "revision", "job"}).AddRow(accountID, "imported-key", cipher, "machine-1", "", 4, 11))
			mock.ExpectQuery("SELECT source_account_id,binding_revision FROM pk_kilo_jobs WHERE id").WithArgs(int64(11), int64(17)).
				WillReturnRows(sqlmock.NewRows([]string{"source", "revision"}).AddRow(sourceID, test.jobRevision))
			if test.conflict {
				mock.ExpectRollback()
			} else {
				if !test.existing {
					mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET account_id").WithArgs(int64(17), int64(23)).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				mock.ExpectCommit()
			}
			svc := &KiloService{db: db, client: client, accounts: accounts, encryptor: kiloWorkerCipher{}}
			account, err := svc.ensurePreparedAccount(context.Background(), 17, nil)
			if test.conflict {
				require.Equal(t, "KILO_CONFLICT", infraerrors.Reason(err))
				require.Zero(t, accounts.writes)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(4), account.Extra["kilo_binding_revision"])
				if test.keyOnly {
					require.NotContains(t, account.Extra, "kilo_onboarding_source_id")
				} else {
					require.Equal(t, int64(42), account.Extra["kilo_onboarding_source_id"])
				}
				require.Equal(t, "synthetic-original-kilo-key", account.GetCredential("api_key"))
				require.False(t, account.Schedulable)
				require.Equal(t, 1, accounts.writes)
				if test.existing {
					require.Equal(t, int64(13), account.Extra[KiloLoginProxyExtraKey])
				}
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloOverviewReuseRequiresIdentityRecoveryMaterial(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "key_only_not_reusable", true: "recoverable_identity_reusable"}[ready], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			columns := []string{"id", "name", "email", "auth", "organization", "status", "account", "source", "has_key", "error", "created", "proxy", "proxy_name", "source_name", "gateway_name", "routing", "identity", "binding", "revision", "recovery", "binding_error", "identity_recovery_ready", "identity_mode", "identity_source", "identity_checked_at", "session_ready"}
			mock.ExpectQuery("SELECT p.id,p.name,p.email.*p.identity_session_encrypted.*p.identity_credential_encrypted").
				WillReturnRows(sqlmock.NewRows(columns).AddRow(17, "identity", "identity@example.test", "api_key", "", "ready", 23, nil, true, "", time.Now(), 13, "fixed", "", "gateway", []byte(`{}`), "active", "empty", 4, nil, "", ready, KiloIdentityModeIndependent, nil, time.Now(), ready))
			mock.ExpectQuery("SELECT j.id,j.batch_id::text").WillReturnRows(sqlmock.NewRows([]string{"id"}))
			overview, err := (&KiloService{db: db}).Overview(context.Background())
			require.NoError(t, err)
			require.Len(t, overview.Prepared, 1)
			require.Equal(t, ready, overview.Prepared[0].Reusable)
			require.True(t, overview.Prepared[0].HasCredential)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloOnboardingClaimKeepsStageAndNullablePrepared(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	options, err := json.Marshal(struct {
		KiloBatchInput
		OnboardingName string `json:"onboarding_name"`
	}{KiloBatchInput: KiloBatchInput{AccountConcurrency: 100}, OnboardingName: "reserved-name"})
	require.NoError(t, err)
	columns := []string{"id", "prepared", "source", "identity", "options", "kind", "stage", "revision", "account", "proxy", "organization", "models", "activate", "material", "quota", "credential_ready", "subscription_connected", "quota_ready", "remote", "outcome", "binding_revision", "reused_identity"}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,COALESCE\\(prepared_id,0\\).*FOR UPDATE SKIP LOCKED").WillReturnRows(sqlmock.NewRows(columns).AddRow(31, 0, 32, "source-fingerprint", options, KiloJobOnboardSource, "subscription_authorization", 6, 33, 34, "organization", []byte(`["openai/model-a"]`), true, "material-version", 35, false, false, true, "cipher:receipt", "pending", 0, false))
	mock.ExpectExec("UPDATE pk_kilo_jobs SET status='running',stage=CASE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	job, err := (&KiloService{db: db}).claimJob(context.Background())
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Zero(t, job.PreparedID)
	require.Equal(t, "subscription_authorization", job.Stage)
	require.EqualValues(t, 6, job.Revision)
	require.Equal(t, "reserved-name", job.Name)
	require.Equal(t, []string{"openai/model-a"}, job.VerifiedModels)
	require.NoError(t, mock.ExpectationsWereMet())
}
