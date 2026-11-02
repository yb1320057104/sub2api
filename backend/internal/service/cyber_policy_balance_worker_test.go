package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type cyberBalanceCacheStub struct {
	err error
	ids []int64
}

func (c *cyberBalanceCacheStub) InvalidateUserBalance(_ context.Context, id int64) error {
	c.ids = append(c.ids, id)
	return c.err
}

func TestCyberPolicyBalanceWorkerRetriesAndUsesRevisionBoundAck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage int
		err   error
		query string
	}{
		{"retry-cache-failure", 0, errors.New("redis unavailable"), `UPDATE cyber_policy_auto_charge_outbox SET claimed_at=NULL`},
		{"delayed-second-pass", 0, nil, `UPDATE cyber_policy_auto_charge_outbox SET stage=1`},
		{"ack-second-pass", 1, nil, `DELETE FROM cyber_policy_auto_charge_outbox`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			cache := &cyberBalanceCacheStub{err: tc.err}
			w := newCyberPolicyBalanceWorker(db, cache)
			defer w.Stop()
			mock.ExpectQuery(`WITH pending AS`).WithArgs(w.owner).
				WillReturnRows(sqlmock.NewRows([]string{"user_id", "revision", "stage"}).AddRow(7, 13, tc.stage)).RowsWillBeClosed()
			mock.ExpectExec(tc.query+`[\s\S]*WHERE user_id=\$1 AND revision=\$2 AND claimed_by=\$3`).
				WithArgs(int64(7), int64(13), w.owner).WillReturnResult(sqlmock.NewResult(0, 0))
			require.NoError(t, w.processBatch(context.Background()))
			require.Equal(t, []int64{7}, cache.ids)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
