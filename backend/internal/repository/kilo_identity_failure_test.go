package repository

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestKiloIdentityFailureSnapshotIsolation(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		status                         int
		source, revision               int64
		preparedStatus, bindingState   string
		options                        string
		accountMiss, outboxFailure     bool
		preparedMissing, missingMarker bool
		want                           bool
	}{
		{name: "current 401 without release", status: 401, source: 122, revision: 5, options: `{"auto_release_statuses":[]}`, want: true},
		{name: "current 402 without identity ban", status: 402, source: 122, revision: 5, want: true},
		{name: "late 401 after source replacement", status: 401, source: 123, revision: 6},
		{name: "late 402 after same source rebind", status: 402, source: 122, revision: 6},
		{name: "binding in progress", status: 401, source: 122, revision: 5, preparedStatus: "binding", bindingState: "binding"},
		{name: "403 follows same Key across sources", status: 403, source: 123, revision: 6, preparedStatus: "binding", bindingState: "binding", want: true},
		{name: "403 still bans with release disabled", status: 403, source: 122, revision: 5, options: `{"auto_release_statuses":[]}`, want: true},
		{name: "403 stops already disabled same Key", status: 403, source: 122, revision: 5, preparedStatus: "disabled", bindingState: "blocked", want: true},
		{name: "old Key cannot ban replacement", status: 403, source: 122, revision: 5, accountMiss: true},
		{name: "changed credential snapshot", status: 401, source: 122, revision: 5, accountMiss: true},
		{name: "changed prepared owner", status: 403, preparedMissing: true},
		{name: "missing revision fails closed", status: 401, missingMarker: true},
		{name: "outbox error rolls back identity ban", status: 403, source: 122, revision: 5, outboxFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, mock := newKiloSourceHealthTestRepository(t)
			snapshot := &service.Account{
				ID: 124, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "synthetic-kilo-key", "base_url": service.KiloGatewayBaseURL},
				Extra:       map[string]any{service.KiloManagedExtraKey: true, "kilo_prepared_id": 2, "kilo_onboarding_source_id": 122, "kilo_binding_revision": 5, service.KiloLoginProxyExtraKey: 77},
			}
			if tc.missingMarker {
				delete(snapshot.Extra, "kilo_binding_revision")
			} else {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).WithArgs(int64(70450001)).WillReturnResult(sqlmock.NewResult(0, 1))
				read := mock.ExpectQuery(`SELECT routing_options,status,COALESCE\(source_account_id,0\),binding_revision,binding_state FROM pk_kilo_prepared_accounts WHERE id=\$1 AND account_id=\$2 FOR UPDATE`).WithArgs(int64(2), int64(124))
				if tc.preparedMissing {
					read.WillReturnError(sql.ErrNoRows)
				} else {
					if tc.options == "" {
						tc.options = `{}`
					}
					if tc.preparedStatus == "" {
						tc.preparedStatus = "bound"
					}
					if tc.bindingState == "" {
						tc.bindingState = "bound"
					}
					read.WillReturnRows(sqlmock.NewRows([]string{"routing_options", "status", "source_account_id", "binding_revision", "binding_state"}).AddRow(tc.options, tc.preparedStatus, tc.source, tc.revision, tc.bindingState))
					identity := tc.status == http.StatusForbidden
					bindingMatches := tc.source == 122 && tc.revision == 5 && tc.preparedStatus == "bound" && tc.bindingState == "bound"
					if identity || bindingMatches {
						mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).WithArgs(accountProxyAssignmentLock).WillReturnResult(sqlmock.NewResult(0, 1))
						reason := "http_401"
						if tc.status == 402 {
							reason = "http_402"
						} else if identity {
							reason = service.KiloIdentityForbiddenReason
						}
						write := mock.ExpectQuery(regexp.QuoteMeta(kiloFailureAccountSQL)).WithArgs(int64(124), "synthetic-kilo-key", service.KiloGatewayBaseURL, reason, service.PlatformOpenAI, service.AccountTypeAPIKey, "2", identity, `{"api_key":"synthetic-kilo-key","base_url":"`+service.KiloGatewayBaseURL+`"}`, "122", "5")
						if tc.accountMiss {
							write.WillReturnError(sql.ErrNoRows)
						} else {
							write.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(124))
							prefix := "kilo_release:"
							if tc.options != `{}` {
								prefix = "kilo_block:"
							}
							mock.ExpectExec(regexp.QuoteMeta(kiloFailurePreparedSQL)).WithArgs(int64(2), prefix+reason, int64(124), identity, int64(122), int64(5)).WillReturnResult(sqlmock.NewResult(0, 1))
							outbox := mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, []byte(`{"account_ids":[124]}`))
							if tc.outboxFailure {
								outbox.WillReturnError(errors.New("synthetic outbox failure"))
							} else {
								outbox.WillReturnResult(sqlmock.NewResult(1, 1))
							}
						}
					}
				}
				if tc.want {
					mock.ExpectCommit()
				} else {
					mock.ExpectRollback()
				}
			}
			changed, err := repo.RecordKiloGatewayFailure(context.Background(), snapshot, tc.status, "")
			if tc.outboxFailure {
				require.ErrorContains(t, err, "synthetic outbox failure")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, changed)
			require.Equal(t, 77, snapshot.Extra[service.KiloLoginProxyExtraKey], "recording a failure never changes the login pin")
			require.NoError(t, mock.ExpectationsWereMet(), "no source, proxy or lease mutation may escape the snapshot CAS")
		})
	}
}
