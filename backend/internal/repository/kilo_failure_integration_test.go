//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestKiloGatewayFailureSQLSnapshotAndReleaseIntent(t *testing.T) {
	db, client := newBillingAuditDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo := NewAccountRepository(client, db, nil)
	recorder := repo.(service.KiloGatewayFailureRepository)
	for _, tc := range []struct {
		name, options, code, mutation string
		status                        int
		want                          bool
	}{
		{"default_401", `{}`, "", "", 401, true},
		{"default_402", `{}`, "", "", 402, true},
		{"default_403", `{}`, "", "", 403, true},
		{"sse_code", `{}`, "account_restricted", "", 200, true},
		{"sharing_code", `{}`, "subscription_sharing_usage_limit_exceeded", "", 429, true},
		{"empty_disables_all", `{"auto_release_statuses":[]}`, "account_restricted", "", 403, false},
		{"ordinary_429", `{}`, "", "", 429, false},
		{"new_key", `{}`, "", `credentials=jsonb_set(credentials,'{api_key}','"replacement"'::jsonb)`, 401, false},
		{"new_base", `{}`, "", `credentials=jsonb_set(credentials,'{base_url}','"https://replacement.invalid"'::jsonb)`, 401, false},
		{"manual_pause", `{}`, "", `schedulable=FALSE`, 401, false},
		{"manual_disable", `{}`, "", `status='disabled',error_message='manual'`, 401, false},
		{"manual_error_preserved", `{}`, "", `status='error',error_message='manual'`, 401, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := mustCreateAccount(t, client, &service.Account{Name: "source-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{"unchanged": true}})
			gateway := mustCreateAccount(t, client, &service.Account{Name: "gateway-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "https://api.kilo.invalid"}, Extra: map[string]any{service.KiloManagedExtraKey: true}})
			_, err := db.ExecContext(ctx, `UPDATE accounts SET schedulable=TRUE,status='active',error_message='' WHERE id=$1`, gateway.ID)
			require.NoError(t, err)
			var preparedID int64
			require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO pk_kilo_prepared_accounts(name,credential_encrypted,credential_fingerprint,auth_method,machine_id,status,account_id,source_account_id,routing_options) VALUES($1,'synthetic',repeat(md5($1),2),'api_key',$2::uuid,'bound',$3,$4,$5::jsonb) RETURNING id`, gateway.Name, uuid.NewString(), gateway.ID, source.ID, tc.options).Scan(&preparedID))
			var originalSourceExtra string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT extra::text FROM accounts WHERE id=$1`, source.ID).Scan(&originalSourceExtra))
			snapshot, err := repo.GetByID(ctx, gateway.ID)
			require.NoError(t, err)
			if tc.mutation != "" {
				_, err = db.ExecContext(ctx, `UPDATE accounts SET `+tc.mutation+` WHERE id=$1`, gateway.ID)
				require.NoError(t, err)
			}
			changed, err := recorder.RecordKiloGatewayFailure(ctx, snapshot, tc.status, tc.code)
			require.NoError(t, err)
			require.Equal(t, tc.want, changed)
			var preparedStatus, lastError string
			var sourceID int64
			require.NoError(t, db.QueryRowContext(ctx, `SELECT status,last_error,source_account_id FROM pk_kilo_prepared_accounts WHERE id=$1`, preparedID).Scan(&preparedStatus, &lastError, &sourceID))
			require.Equal(t, source.ID, sourceID, "automatic release intent preserves source pairing")
			if tc.want {
				require.Equal(t, "disabled", preparedStatus)
				require.Contains(t, lastError, "kilo_release:")
				current, err := repo.GetByID(ctx, gateway.ID)
				require.NoError(t, err)
				require.False(t, current.Schedulable)
				require.Equal(t, service.StatusError, current.Status)
				if tc.name == "manual_error_preserved" {
					require.Equal(t, "manual", current.ErrorMessage)
				}
			} else {
				require.Equal(t, "bound", preparedStatus)
				require.Empty(t, lastError)
			}
			var sourceExtra string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT extra::text FROM accounts WHERE id=$1`, source.ID).Scan(&sourceExtra))
			require.JSONEq(t, originalSourceExtra, sourceExtra, "recording a gateway release intent must not mutate the persisted source snapshot")
		})
	}
}
