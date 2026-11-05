package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPK52CandyCredentialCASUnit(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows int64
		err  error
	}{
		{"updated", 1, nil}, {"stale", 0, nil}, {"database_failure", 0, errors.New("db unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := newAccountRepositoryWithSQL(nil, db, nil)
			expect := mock.ExpectExec("WITH locked AS MATERIALIZED").WithArgs(int64(52), int64(52), service.AccountCandyCredentialErrorMessage,
				service.AccountTypeOAuth, service.StatusActive, true, `{"access_token":"old"}`, nil,
				service.AccountTypeOAuth, service.StatusActive, true, nil, service.SchedulerOutboxEventAccountChanged)
			if tc.err != nil {
				expect.WillReturnError(tc.err)
			} else {
				expect.WillReturnResult(sqlmock.NewResult(0, tc.rows))
			}
			updated, err := repo.SetAccountCandyCredentialErrorIfMatch(context.Background(), service.AccountCandyCredentialStateSnapshot{
				AccountID: 52, CredentialAccountID: 52, AccountType: service.AccountTypeOAuth, CredentialType: service.AccountTypeOAuth,
				AccountStatus: service.StatusActive, CredentialStatus: service.StatusActive, AccountSchedulable: true, CredentialSchedulable: true, CredentialsJSON: `{"access_token":"old"}`,
			})
			require.Equal(t, tc.rows == 1, updated)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
