//go:build unit

package service

import (
	"context"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPK52CandyRadarStateChangeStopsModelRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		shadow bool
		change func(*Account, *Account)
	}{
		{"account 401", false, func(a, _ *Account) { a.Status = StatusError }},
		{"account paused", false, func(a, _ *Account) { a.Schedulable = false }},
		{"shadow status", true, func(a, _ *Account) { a.Status = StatusError }},
		{"shadow paused", true, func(a, _ *Account) { a.Schedulable = false }},
		{"parent 401", true, func(_, p *Account) { p.Status = StatusError }},
		{"parent paused", true, func(_, p *Account) { p.Schedulable = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 52, Status: StatusActive, Schedulable: true}
			credential := account
			if tc.shadow {
				credential = &Account{ID: 53, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
				account.ParentAccountID = &credential.ID
			}
			svc := &AccountCandyTestService{accountRepo: &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account, credential.ID: credential}}}
			accountState, credentialState := radarCandyState(account), radarCandyState(credential)
			ctx, recovery := withAccountCandyRecovery(context.Background(), nil)
			calls, checks := 0, 0
			text, err := runAccountCandyResponseRecovery(ctx, func(checkCtx context.Context) error {
				checks++
				return svc.checkRadarRequestState(checkCtx, accountState, credentialState)
			}, func(context.Context) (string, error) {
				calls++
				tc.change(account, credential)
				return readAccountCandyReply(newJSONResponse(200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n"), false)
			})
			require.ErrorContains(t, err, "状态已变化")
			require.Empty(t, text)
			require.Equal(t, 2, checks)
			require.Equal(t, 1, calls, "state change must stop before a second POST")
			require.Equal(t, 1, recovery.attempts)
			require.NoError(t, ctx.Err(), "a model admission failure must not cancel sibling models")
		})
	}
}

func TestPK52CandyRadarManualInitialStateAndCompletedAnswerPreserved(t *testing.T) {
	account := &Account{ID: 52, Status: StatusError, Schedulable: false}
	credential := &Account{ID: 53, Status: StatusError, Schedulable: false}
	svc := &AccountCandyTestService{accountRepo: &mockAccountRepoForGemini{accountsByID: map[int64]*Account{52: account, 53: credential}}}
	accountState, credentialState := radarCandyState(account), radarCandyState(credential)
	calls, checks := 0, 0
	text, err := runAccountCandyResponseRecovery(context.Background(), func(ctx context.Context) error {
		checks++
		return svc.checkRadarRequestState(ctx, accountState, credentialState)
	}, func(context.Context) (string, error) {
		calls++
		account.Status, account.Schedulable = StatusActive, true
		return readAccountCandyReply(newJSONResponse(200, pk52CandyTerminal("22")), false)
	})
	require.NoError(t, err)
	require.Equal(t, "<final_answer>22</final_answer>", text)
	require.Equal(t, 1, checks, "completed answers do not enter retry admission again")
	require.Equal(t, 1, calls)
}

func TestPK52CandyRadarLateModelUsesClaimState(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		name := "ordinary account"
		if shadow {
			name = "credential parent"
		}
		t.Run(name, func(t *testing.T) {
			claim := &Account{ID: 52, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
			claimCredential := claim
			if shadow {
				claimCredential = &Account{ID: 53, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
				claim.ParentAccountID = &claimCredential.ID
			}
			current, currentCredential := *claim, *claimCredential
			if shadow {
				currentCredential.Status = StatusError
			} else {
				current.Status = StatusError
				currentCredential = current
			}
			repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{current.ID: &current, currentCredential.ID: &currentCredential}}
			run := &RadarRun{AccountCandyTestResult: AccountCandyTestResult{ID: 1, AccountID: 52, probeAccount: claim, credentialAccount: claimCredential}, groupRule: &RadarGroupRule{SourceGroupID: 7}, proxySession: newAccountCandyProxySession(nil)}
			var err error
			run.identity, err = accountCandySnapshotIdentity(claim, claimCredential)
			require.NoError(t, err)
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			// The late model can read the same credential identity, but the
			// claim-time state must reject it before any upstream call.
			mock.ExpectQuery("SELECT COALESCE.*jsonb_agg").WillReturnRows(sqlmock.NewRows([]string{"groups", "moved", "active"}).AddRow("[]", nil, true))
			mock.ExpectExec("UPDATE pk_radar_account_model_results").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT COALESCE.*jsonb_agg").WillReturnRows(sqlmock.NewRows([]string{"groups", "moved", "active"}).AddRow("[]", nil, true))
			mock.ExpectExec("UPDATE pk_radar_account_model_results").WillReturnResult(sqlmock.NewResult(0, 1))
			upstream := &queuedHTTPUpstream{}
			svc := &AccountCandyTestService{db: db, accountRepo: repo, testService: &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}}
			result := svc.executeRadarModel(context.Background(), run, &RadarConfig{}, RadarModelResult{ID: 2, Model: "gpt-5.1"}, &sync.Mutex{})
			require.NoError(t, result.err)
			require.Equal(t, "failed", result.model.Status)
			require.Contains(t, result.model.ErrorMessage, "状态已变化")
			require.Empty(t, upstream.requests, "late-started model must issue zero requests")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
