//go:build integration

package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/ent/authidentitychannel"
	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type pkCyberDeductionFixture struct {
	svc     *service.CyberPolicyService
	user    *service.User
	key     *service.APIKey
	account *service.Account
	repo    *usageLogRepository
	window  service.CyberPolicyWindowInput
}

func newPKCyberDeductionFixture(t *testing.T) *pkCyberDeductionFixture {
	t.Helper()
	configurePKCyberAutoDisable(t, false, 10, true)
	ctx := context.Background()
	svc := service.NewCyberPolicyService(integrationDB, nil, nil)
	old, err := svc.Settings(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := svc.SaveSettings(ctx, service.CyberPolicySettingsInput{Multiplier: &old.Multiplier})
		require.NoError(t, err)
	})
	multiplier := "6.7"
	_, err = svc.SaveSettings(ctx, service.CyberPolicySettingsInput{Multiplier: &multiplier})
	require.NoError(t, err)
	u, k, a, repo := pkCyberAutoDisableFixture(t, service.RoleUser)
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET balance=100.12345678,total_recharged=50,username='中文用户名' WHERE id=$1`, u.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM cyber_policy_charges WHERE user_id=$1`, u.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM cyber_policy_deductions WHERE user_id=$1`, u.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM redeem_codes WHERE used_by=$1`, u.ID)
		require.NoError(t, err)
	})
	return &pkCyberDeductionFixture{svc: svc, user: u, key: k, account: a, repo: repo, window: service.CyberPolicyWindowInput{StartDate: "2006-02-03", EndDate: "2006-02-03", Timezone: "Asia/Shanghai"}}
}

func (f *pkCyberDeductionFixture) insert(t *testing.T, at time.Time, kind service.RequestType) *service.UsageLog {
	t.Helper()
	log := &service.UsageLog{UserID: f.user.ID, APIKeyID: f.key.ID, AccountID: f.account.ID, RequestID: uuid.NewString(), Model: "cyber-ranking-fixture", RequestType: kind, CreatedAt: at}
	_, err := f.repo.createSingle(context.Background(), integrationDB, log)
	require.NoError(t, err)
	return log
}

func (f *pkCyberDeductionFixture) withinDay() time.Time {
	return time.Date(2006, 2, 3, 5, 0, 0, 0, time.UTC)
}

func (f *pkCyberDeductionFixture) assertAccounting(t *testing.T, balance string, charges, entries int) {
	t.Helper()
	var exact bool
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `SELECT balance=$2::numeric AND total_recharged=50 FROM users WHERE id=$1`, f.user.ID, balance).Scan(&exact))
	require.True(t, exact, "exact balance and original cumulative recharge")
	var count int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM cyber_policy_charges WHERE user_id=$1`, f.user.ID).Scan(&count))
	require.Equal(t, charges, count)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM redeem_codes WHERE used_by=$1 AND type='admin_balance'`, f.user.ID).Scan(&count))
	require.Equal(t, entries, count)
}

func TestPKCyberRankingDatesPublicMaskAndSettings(t *testing.T) {
	f := newPKCyberDeductionFixture(t)
	ctx := context.Background()
	start := time.Date(2006, 2, 2, 16, 0, 0, 0, time.UTC)
	f.insert(t, start.Add(-time.Microsecond), service.RequestTypeCyberBlocked)
	f.insert(t, start, service.RequestTypeCyberBlocked)
	f.insert(t, start.Add(24*time.Hour-time.Microsecond), service.RequestTypeCyberBlocked)
	f.insert(t, start.Add(24*time.Hour), service.RequestTypeCyberBlocked)
	f.insert(t, start.Add(time.Hour), service.RequestTypeStream)
	r, err := f.svc.Ranking(ctx, f.window, 1, 50)
	require.NoError(t, err)
	require.Equal(t, int64(2), r.TotalTriggers)
	require.Equal(t, int64(1), r.Total)
	require.Len(t, r.Items, 1)
	require.Equal(t, int64(2), r.Items[0].TriggerCount)
	require.Equal(t, "13.40000000", r.Items[0].PendingAmount)
	require.Equal(t, "中文用户名", r.Items[0].Username)
	require.Equal(t, f.user.Email, r.Items[0].Email)
	public, err := f.svc.PublicRanking(ctx, f.window, 1, 50)
	require.NoError(t, err)
	require.Len(t, public.Items, 1)
	require.Equal(t, "中文用***", public.Items[0].DisplayName)
	encoded, err := json.Marshal(public)
	require.NoError(t, err)
	for _, field := range []string{f.user.Email, "中文用户名", "user_id", "email", "balance", "multiplier", "pending_count", "auto_disable_count"} {
		require.NotContains(t, string(encoded), field)
	}
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET username='' WHERE id=$1`, f.user.ID)
	require.NoError(t, err)
	public, err = f.svc.PublicRanking(ctx, f.window, 1, 50)
	require.NoError(t, err)
	require.Equal(t, string([]rune(f.user.Email)[:3])+"***", public.Items[0].DisplayName)
	threshold := int64(42)
	enabled := true
	reset := false
	settings, err := f.svc.SaveSettings(ctx, service.CyberPolicySettingsInput{AutoDisableEnabled: &enabled, AutoDisableThreshold: &threshold, ResetOnManualEnable: &reset})
	require.NoError(t, err)
	require.Equal(t, "6.700000", settings.Multiplier)
	require.True(t, settings.AutoDisableEnabled)
	require.False(t, settings.ResetOnManualEnable)
	multiplier := "0.29"
	settings, err = f.svc.SaveSettings(ctx, service.CyberPolicySettingsInput{Multiplier: &multiplier})
	require.NoError(t, err)
	require.Equal(t, int64(42), settings.AutoDisableThreshold)
	require.True(t, settings.AutoDisableEnabled)
	require.False(t, settings.ResetOnManualEnable)
}

func TestPKCyberGroupRecordsRankingAndDeductionsRemainIndependent(t *testing.T) {
	f := newPKCyberDeductionFixture(t)
	ctx := context.Background()
	enabled := true
	_, err := f.svc.SaveSettings(ctx, service.CyberPolicySettingsInput{AutoDisableEnabled: &enabled})
	require.NoError(t, err)
	insert := func(offset time.Duration, group *int64) *service.UsageLog {
		t.Helper()
		log := pkCyberAutoDisableLog(f.user, f.key, f.account)
		log.CreatedAt = f.withinDay().Add(offset)
		log.GroupID = group
		_, err := f.repo.createSingle(ctx, integrationDB, log)
		require.NoError(t, err)
		return log
	}
	counted := insert(0, f.key.GroupID)
	unknown := insert(time.Second, nil)
	// Missing evidence is the legacy shape: never infer it from current scope.
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM cyber_policy_count_events WHERE usage_id=$1 AND usage_created_at=$2`, unknown.ID, unknown.CreatedAt)
	require.NoError(t, err)
	_, err = f.svc.SaveSettings(ctx, service.CyberPolicySettingsInput{GroupIDs: &[]int64{}})
	require.NoError(t, err)
	skipped := insert(2*time.Second, f.key.GroupID)
	f.insert(t, f.withinDay().Add(3*time.Second), service.RequestTypeStream)
	requirePKCyberAutoDisableState(t, f.user.ID, service.StatusActive, 1)

	var originalGroupName string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT name FROM groups WHERE id=$1`, *f.key.GroupID).Scan(&originalGroupName))
	_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET name='renamed-after-insertion' WHERE id=$1`, *f.key.GroupID)
	require.NoError(t, err)
	_, err = f.svc.SaveSettings(ctx, service.CyberPolicySettingsInput{GroupIDs: &[]int64{*f.key.GroupID}})
	require.NoError(t, err)
	result, err := f.svc.Records(ctx, f.user.ID, f.window, 1, 2)
	require.NoError(t, err)
	require.Equal(t, int64(3), result.Total)
	require.Equal(t, int64(2), result.Pages)
	require.Len(t, result.Items, 2)
	require.Equal(t, skipped.ID, result.Items[0].UsageID)
	require.Equal(t, "skipped", result.Items[0].CountStatus)
	require.Equal(t, "group_not_enabled", result.Items[0].CountReason)
	require.Equal(t, originalGroupName, result.Items[0].GroupName)
	require.False(t, *result.Items[0].ScopeMatched)
	require.Equal(t, unknown.ID, result.Items[1].UsageID)
	require.Equal(t, "unknown", result.Items[1].CountStatus)
	require.Equal(t, "historical_unknown", result.Items[1].CountReason)
	require.Nil(t, result.Items[1].ScopeMatched)
	require.Nil(t, result.Items[1].SettingsRevision)
	require.Nil(t, result.Items[1].EvaluatedAt)
	second, err := f.svc.Records(ctx, f.user.ID, f.window, 2, 2)
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.Equal(t, counted.ID, second.Items[0].UsageID)
	require.Equal(t, "counted", second.Items[0].CountStatus)
	require.Equal(t, originalGroupName, second.Items[0].GroupName)
	require.Less(t, *second.Items[0].SettingsRevision, *result.Items[0].SettingsRevision)
	require.True(t, second.Items[0].EvaluatedAt.After(second.Items[0].OccurredAt), "insertion-time evidence is distinct from the original event time")

	ranking, err := f.svc.Ranking(ctx, f.window, 1, 50)
	require.NoError(t, err)
	require.Equal(t, int64(3), ranking.TotalTriggers)
	require.Equal(t, int64(1), ranking.TotalCounted)
	require.Equal(t, int64(1), ranking.TotalSkipped)
	require.Equal(t, int64(1), ranking.TotalUnknown)
	require.Len(t, ranking.Items, 1)
	require.Equal(t, int64(1), ranking.Items[0].AutoDisableCount)
	require.Equal(t, int64(3), ranking.Items[0].PendingCount)
	public, err := f.svc.PublicRanking(ctx, f.window, 1, 50)
	require.NoError(t, err)
	require.Equal(t, ranking.TotalCounted, public.TotalCounted)
	require.Equal(t, ranking.TotalSkipped, public.TotalSkipped)
	require.Equal(t, ranking.TotalUnknown, public.TotalUnknown)
	encoded, err := json.Marshal(public)
	require.NoError(t, err)
	for _, field := range []string{"user_id", "group_id", "group_name", "request_id", "usage_id", f.user.Email, originalGroupName} {
		require.NotContains(t, string(encoded), field)
	}
	preview, err := f.svc.Preview(ctx, 91, f.user.ID, f.window)
	require.NoError(t, err)
	require.Equal(t, int64(3), preview.TriggerCount, "manual deductions include counted, skipped and historical safety events")
	require.Equal(t, "20.10000000", preview.Amount)
	_, err = f.svc.Deduct(ctx, 91, f.user.ID, preview.ID)
	require.NoError(t, err)
	f.assertAccounting(t, "80.02345678", 3, 1)
	requirePKCyberAutoDisableState(t, f.user.ID, service.StatusActive, 1)
}

func TestPKCyberDeductionSnapshotConcurrentRetryAndNormalBilling(t *testing.T) {
	f := newPKCyberDeductionFixture(t)
	ctx := context.Background()
	at := f.withinDay()
	f.insert(t, at, service.RequestTypeCyberBlocked)
	f.insert(t, at.Add(time.Second), service.RequestTypeCyberBlocked)
	preview, err := f.svc.Preview(ctx, 91, f.user.ID, f.window)
	require.NoError(t, err)
	require.Equal(t, "13.40000000", preview.Amount)
	overlap, err := f.svc.Preview(ctx, 92, f.user.ID, f.window)
	require.NoError(t, err)
	multiplier := "0.29"
	_, err = f.svc.SaveSettings(ctx, service.CyberPolicySettingsInput{Multiplier: &multiplier})
	require.NoError(t, err)
	f.insert(t, at.Add(2*time.Second), service.RequestTypeCyberBlocked)
	var wg sync.WaitGroup
	errs := make([]error, 9)
	results := make([]*service.CyberPolicyDeduction, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			instance := service.NewCyberPolicyService(integrationDB, nil, nil)
			results[i], errs[i] = instance.Deduct(ctx, 91, f.user.ID, preview.ID)
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, errs[8] = integrationDB.ExecContext(ctx, `UPDATE users SET balance=balance-0.12345678 WHERE id=$1`, f.user.ID)
	}()
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	for _, result := range results {
		require.Equal(t, "completed", result.Status)
		require.Equal(t, "13.40000000", result.Amount)
		require.Equal(t, preview.ID, result.ID)
		require.Equal(t, *results[0].BalanceAfter, *result.BalanceAfter)
	}
	f.assertAccounting(t, "86.60000000", 2, 1)
	_, err = f.svc.Deduct(ctx, 92, f.user.ID, overlap.ID)
	require.ErrorContains(t, err, "CYBER_PREVIEW_STALE")
	f.assertAccounting(t, "86.60000000", 2, 1)
	next, err := f.svc.Preview(ctx, 91, f.user.ID, f.window)
	require.NoError(t, err)
	require.Equal(t, int64(1), next.TriggerCount)
	require.Equal(t, "0.29000000", next.Amount)
	_, err = f.svc.Deduct(ctx, 91, f.user.ID, next.ID)
	require.NoError(t, err)
	f.assertAccounting(t, "86.31000000", 3, 2)
	var exactLedger bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT SUM(value)=-13.69 AND bool_and(length(code)<=32) FROM redeem_codes WHERE used_by=$1 AND type='admin_balance'`, f.user.ID).Scan(&exactLedger))
	require.True(t, exactLedger)
	r, err := f.svc.Ranking(ctx, f.window, 1, 50)
	require.NoError(t, err)
	require.Equal(t, int64(3), r.Items[0].DeductedCount)
	require.Zero(t, r.Items[0].PendingCount)
}

func TestPKCyberDeductionFailuresRollbackAndRetentionDedup(t *testing.T) {
	f := newPKCyberDeductionFixture(t)
	ctx := context.Background()
	log := f.insert(t, f.withinDay(), service.RequestTypeCyberBlocked)
	preview, err := f.svc.Preview(ctx, 91, f.user.ID, f.window)
	require.NoError(t, err)
	_, err = f.svc.Deduct(ctx, 92, f.user.ID, preview.ID)
	require.ErrorContains(t, err, "CYBER_PREVIEW_NOT_FOUND")
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET balance=1 WHERE id=$1`, f.user.ID)
	require.NoError(t, err)
	_, err = f.svc.Deduct(ctx, 91, f.user.ID, preview.ID)
	require.ErrorContains(t, err, "CYBER_INSUFFICIENT_BALANCE")
	f.assertAccounting(t, "1", 0, 0)
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET balance=100.12345678 WHERE id=$1`, f.user.ID)
	require.NoError(t, err)
	// A history write failure must undo both the debit and every identity claim.
	parsed := uuid.MustParse(preview.ID)
	code := "CP-" + base64.RawURLEncoding.EncodeToString(parsed[:])
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO redeem_codes(code,type,value,status,used_by) VALUES($1,'balance',0,'used',$2)`, code, f.user.ID)
	require.NoError(t, err)
	_, err = f.svc.Deduct(ctx, 91, f.user.ID, preview.ID)
	require.Error(t, err)
	f.assertAccounting(t, "100.12345678", 0, 0)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM redeem_codes WHERE code=$1`, code)
	require.NoError(t, err)
	stale, err := f.svc.Preview(ctx, 92, f.user.ID, f.window)
	require.NoError(t, err)
	var saved string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT row_to_json(l)::text FROM usage_logs l WHERE id=$1`, log.ID).Scan(&saved))
	_, err = f.svc.Deduct(ctx, 91, f.user.ID, preview.ID)
	require.NoError(t, err)
	f.assertAccounting(t, "93.42345678", 1, 1)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM usage_logs WHERE id=$1`, log.ID)
	require.NoError(t, err)
	_, err = f.svc.Deduct(ctx, 92, f.user.ID, stale.ID)
	require.ErrorContains(t, err, "CYBER_PREVIEW_STALE")
	_, err = f.svc.Deduct(ctx, 91, f.user.ID, preview.ID)
	require.NoError(t, err)
	f.assertAccounting(t, "93.42345678", 1, 1)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs SELECT * FROM json_populate_record(NULL::usage_logs,$1::json)`, saved)
	require.NoError(t, err)
	_, err = f.svc.Preview(ctx, 91, f.user.ID, f.window)
	require.ErrorContains(t, err, "CYBER_NOTHING_TO_DEDUCT")
	f.assertAccounting(t, "93.42345678", 1, 1)
	f.insert(t, f.withinDay().Add(time.Second), service.RequestTypeCyberBlocked)
	expired, err := f.svc.Preview(ctx, 91, f.user.ID, f.window)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE cyber_policy_deductions SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, expired.ID)
	require.NoError(t, err)
	_, err = f.svc.Deduct(ctx, 91, f.user.ID, expired.ID)
	require.ErrorContains(t, err, "CYBER_PREVIEW_EXPIRED")
	f.assertAccounting(t, "93.42345678", 1, 1)
	_, err = f.svc.Preview(ctx, 91, f.user.ID, f.window)
	require.NoError(t, err)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM cyber_policy_preview_items WHERE deduction_id=$1`, expired.ID).Scan(&count))
	require.Zero(t, count, "bounded preview cleanup must remove abandoned item snapshots")
}

type UserRepoSuite struct {
	suite.Suite
	ctx    context.Context
	client *dbent.Client
	repo   *userRepository
}

func (s *UserRepoSuite) SetupTest() {
	s.ctx = context.Background()
	s.client = testEntClient(s.T())
	s.repo = newUserRepositoryWithSQL(s.client, integrationDB)

	// 清理测试数据，确保每个测试从干净状态开始
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM auth_identity_channels")
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM auth_identities")
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM user_subscriptions")
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM user_allowed_groups")
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM users")
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM registration_ip_quotas")
}

func TestUserRepoSuite(t *testing.T) {
	suite.Run(t, new(UserRepoSuite))
}

func (s *UserRepoSuite) mustCreateUser(u *service.User) *service.User {
	s.T().Helper()

	if u.Email == "" {
		u.Email = "user-" + time.Now().Format(time.RFC3339Nano) + "@example.com"
	}
	if u.PasswordHash == "" {
		u.PasswordHash = "test-password-hash"
	}
	if u.Role == "" {
		u.Role = service.RoleUser
	}
	if u.Status == "" {
		u.Status = service.StatusActive
	}
	if u.Concurrency == 0 {
		u.Concurrency = 5
	}

	s.Require().NoError(s.repo.Create(s.ctx, u), "create user")
	return u
}

func (s *UserRepoSuite) TestCreateWithEmailAliasGuardAndDomainLimitConcurrent() {
	domain := "race-" + strings.ToLower(strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")) + ".example"
	users := []*service.User{
		{Email: "first@" + domain, PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive},
		{Email: "second@" + domain, PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive},
	}

	errs := make(chan error, len(users))
	var wg sync.WaitGroup
	for _, user := range users {
		wg.Add(1)
		go func(user *service.User) {
			defer wg.Done()
			errs <- s.repo.CreateWithEmailAliasGuardAndDomainLimit(s.ctx, user, domain)
		}(user)
	}
	wg.Wait()
	close(errs)

	var success, limited int
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, service.ErrEmailDomainRegistrationLimit):
			limited++
		default:
			s.Require().NoError(err)
		}
	}
	s.Require().Equal(1, success)
	s.Require().Equal(1, limited)

	count, err := s.repo.CountUsersByEmailDomain(s.ctx, domain)
	s.Require().NoError(err)
	s.Require().Equal(1, count)
}

func (s *UserRepoSuite) mustCreateGroup(name string) *service.Group {
	s.T().Helper()

	g, err := s.client.Group.Create().
		SetName(name).
		SetStatus(service.StatusActive).
		Save(s.ctx)
	s.Require().NoError(err, "create group")
	return groupEntityToService(g)
}

func (s *UserRepoSuite) mustCreateSubscription(userID, groupID int64, mutate func(*dbent.UserSubscriptionCreate)) *dbent.UserSubscription {
	s.T().Helper()

	now := time.Now()
	create := s.client.UserSubscription.Create().
		SetUserID(userID).
		SetGroupID(groupID).
		SetStartsAt(now.Add(-1 * time.Hour)).
		SetExpiresAt(now.Add(24 * time.Hour)).
		SetStatus(service.SubscriptionStatusActive).
		SetAssignedAt(now).
		SetNotes("")

	if mutate != nil {
		mutate(create)
	}

	sub, err := create.Save(s.ctx)
	s.Require().NoError(err, "create subscription")
	return sub
}

// --- Create / GetByID / GetByEmail / Update / Delete ---

func (s *UserRepoSuite) TestCreate() {
	user := s.mustCreateUser(&service.User{
		Email:        "create@test.com",
		Username:     "testuser",
		PasswordHash: "test-password-hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	})

	s.Require().NotZero(user.ID, "expected ID to be set")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal("create@test.com", got.Email)
}

func (s *UserRepoSuite) TestGetByID_NotFound() {
	_, err := s.repo.GetByID(s.ctx, 999999)
	s.Require().Error(err, "expected error for non-existent ID")
}

func (s *UserRepoSuite) TestGetByEmail() {
	user := s.mustCreateUser(&service.User{Email: "byemail@test.com"})

	got, err := s.repo.GetByEmail(s.ctx, user.Email)
	s.Require().NoError(err, "GetByEmail")
	s.Require().Equal(user.ID, got.ID)
}

func (s *UserRepoSuite) TestGetByEmail_NormalizesSpacingAndCaseOnPostgres() {
	user := s.mustCreateUser(&service.User{Email: " Legacy@Example.com "})

	got, err := s.repo.GetByEmail(s.ctx, "  legacy@example.com  ")
	s.Require().NoError(err, "GetByEmail normalized lookup")
	s.Require().Equal(user.ID, got.ID)
}

func (s *UserRepoSuite) TestGetByEmail_NotFound() {
	_, err := s.repo.GetByEmail(s.ctx, "nonexistent@test.com")
	s.Require().Error(err, "expected error for non-existent email")
}

func (s *UserRepoSuite) TestExistsByEmail_NormalizesSpacingAndCaseOnPostgres() {
	s.mustCreateUser(&service.User{Email: " Legacy@Example.com "})

	exists, err := s.repo.ExistsByEmail(s.ctx, "  LEGACY@example.com  ")
	s.Require().NoError(err, "ExistsByEmail normalized lookup")
	s.Require().True(exists)
}

func (s *UserRepoSuite) TestUpdate() {
	user := s.mustCreateUser(&service.User{Email: "update@test.com", Username: "original"})

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	got.Username = "updated"
	s.Require().NoError(s.repo.Update(s.ctx, got, service.UserUpdateFields{Username: true}), "Update")

	updated, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err, "GetByID after update")
	s.Require().Equal("updated", updated.Username)
}

func (s *UserRepoSuite) TestBatchUpdateLimitsUpdatesOnlyProvidedFields() {
	user := s.mustCreateUser(&service.User{
		Email:       "batch-limits-one-field@test.com",
		Concurrency: 4,
		RPMLimit:    20,
	})
	concurrency := 9

	affected, err := s.repo.BatchUpdateLimits(s.ctx, []int64{user.ID}, &concurrency, nil)
	s.Require().NoError(err)
	s.Equal(1, affected)

	updated, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Equal(9, updated.Concurrency)
	s.Equal(20, updated.RPMLimit)
}

func (s *UserRepoSuite) TestBatchUpdateLimitsUpdatesBothFieldsToZero() {
	user := s.mustCreateUser(&service.User{
		Email:       "batch-limits-zero@test.com",
		Concurrency: 4,
		RPMLimit:    20,
	})
	zero := 0

	affected, err := s.repo.BatchUpdateLimits(s.ctx, []int64{user.ID}, &zero, &zero)
	s.Require().NoError(err)
	s.Equal(1, affected)

	updated, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Zero(updated.Concurrency)
	s.Zero(updated.RPMLimit)
}

func (s *UserRepoSuite) TestBatchUpdateLimitsIgnoresDeletedUsersAndReturnsAffectedRows() {
	active := s.mustCreateUser(&service.User{Email: "batch-limits-active@test.com", RPMLimit: 10})
	deleted := s.mustCreateUser(&service.User{Email: "batch-limits-deleted@test.com", RPMLimit: 10})
	s.Require().NoError(s.client.User.DeleteOneID(deleted.ID).Exec(s.ctx))
	rpmLimit := 45

	affected, err := s.repo.BatchUpdateLimits(s.ctx, []int64{active.ID, deleted.ID}, nil, &rpmLimit)
	s.Require().NoError(err)
	s.Equal(1, affected)

	updatedActive, err := s.repo.GetByID(s.ctx, active.ID)
	s.Require().NoError(err)
	s.Equal(45, updatedActive.RPMLimit)
	updatedDeleted, err := s.repo.GetByIDIncludeDeleted(s.ctx, deleted.ID)
	s.Require().NoError(err)
	s.Equal(10, updatedDeleted.RPMLimit)
}

func (s *UserRepoSuite) TestUpdateIgnoresNoRowsFromConflictingEmailIdentityUpsert() {
	user := s.mustCreateUser(&service.User{Email: "update-existing-identity@test.com", Username: "original"})

	identityCount, err := s.client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("update-existing-identity@test.com"),
		).
		Count(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(1, identityCount)

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	got.Username = "updated"
	s.Require().NoError(s.repo.Update(s.ctx, got, service.UserUpdateFields{Username: true}), "Update should tolerate ON CONFLICT DO NOTHING returning no rows")

	updated, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal("updated", updated.Username)
}

func (s *UserRepoSuite) TestDelete() {
	user := s.mustCreateUser(&service.User{Email: "delete@test.com"})

	err := s.repo.Delete(s.ctx, user.ID)
	s.Require().NoError(err, "Delete")

	_, err = s.repo.GetByID(s.ctx, user.ID)
	s.Require().Error(err, "expected error after delete")
}

func (s *UserRepoSuite) TestDeleteRemovesAuthIdentitiesAndChannels() {
	user := s.mustCreateUser(&service.User{Email: "delete-oauth@test.com"})

	identity, err := s.client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("delete-oauth-subject").
		Save(s.ctx)
	s.Require().NoError(err)

	_, err = s.client.AuthIdentityChannel.Create().
		SetIdentityID(identity.ID).
		SetProviderType("wechat").
		SetProviderKey("wechat").
		SetChannel("open").
		SetChannelAppID("app-id").
		SetChannelSubject("openid-123").
		Save(s.ctx)
	s.Require().NoError(err)

	err = s.repo.Delete(s.ctx, user.ID)
	s.Require().NoError(err)

	identityCount, err := s.client.AuthIdentity.Query().Where(authidentity.UserIDEQ(user.ID)).Count(s.ctx)
	s.Require().NoError(err)
	s.Require().Zero(identityCount)

	channelCount, err := s.client.AuthIdentityChannel.Query().Where(authidentitychannel.IdentityIDEQ(identity.ID)).Count(s.ctx)
	s.Require().NoError(err)
	s.Require().Zero(channelCount)
}

// --- List / ListWithFilters ---

func (s *UserRepoSuite) TestList() {
	s.mustCreateUser(&service.User{Email: "list1@test.com"})
	s.mustCreateUser(&service.User{Email: "list2@test.com"})

	users, page, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err, "List")
	s.Require().Len(users, 2)
	s.Require().Equal(int64(2), page.Total)
}

func (s *UserRepoSuite) TestListWithFilters_Status() {
	s.mustCreateUser(&service.User{Email: "active@test.com", Status: service.StatusActive})
	s.mustCreateUser(&service.User{Email: "disabled@test.com", Status: service.StatusDisabled})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.UserListFilters{Status: service.StatusActive})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal(service.StatusActive, users[0].Status)
}

func (s *UserRepoSuite) TestListWithFilters_Role() {
	s.mustCreateUser(&service.User{Email: "user@test.com", Role: service.RoleUser})
	s.mustCreateUser(&service.User{Email: "admin@test.com", Role: service.RoleAdmin})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.UserListFilters{Role: service.RoleAdmin})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal(service.RoleAdmin, users[0].Role)
}

func (s *UserRepoSuite) TestListWithFilters_Search() {
	s.mustCreateUser(&service.User{Email: "alice@test.com", Username: "Alice"})
	s.mustCreateUser(&service.User{Email: "bob@test.com", Username: "Bob"})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.UserListFilters{Search: "alice"})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Contains(users[0].Email, "alice")
}

func (s *UserRepoSuite) TestListWithFilters_SearchByUsername() {
	s.mustCreateUser(&service.User{Email: "u1@test.com", Username: "JohnDoe"})
	s.mustCreateUser(&service.User{Email: "u2@test.com", Username: "JaneSmith"})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.UserListFilters{Search: "john"})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal("JohnDoe", users[0].Username)
}

func (s *UserRepoSuite) TestListWithFilters_LoadsActiveSubscriptions() {
	user := s.mustCreateUser(&service.User{Email: "sub@test.com", Status: service.StatusActive})
	groupActive := s.mustCreateGroup("g-sub-active")
	groupExpired := s.mustCreateGroup("g-sub-expired")

	_ = s.mustCreateSubscription(user.ID, groupActive.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStatus(service.SubscriptionStatusActive)
		c.SetExpiresAt(time.Now().Add(1 * time.Hour))
	})
	_ = s.mustCreateSubscription(user.ID, groupExpired.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStatus(service.SubscriptionStatusExpired)
		c.SetExpiresAt(time.Now().Add(-1 * time.Hour))
	})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.UserListFilters{Search: "sub@"})
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Len(users, 1, "expected 1 user")
	s.Require().Len(users[0].Subscriptions, 1, "expected 1 active subscription")
	s.Require().NotNil(users[0].Subscriptions[0].Group, "expected subscription group preload")
	s.Require().Equal(groupActive.ID, users[0].Subscriptions[0].Group.ID, "group ID mismatch")
}

func (s *UserRepoSuite) TestListWithFilters_CombinedFilters() {
	s.mustCreateUser(&service.User{
		Email:    "a@example.com",
		Username: "Alice",
		Role:     service.RoleUser,
		Status:   service.StatusActive,
		Balance:  10,
	})
	target := s.mustCreateUser(&service.User{
		Email:    "b@example.com",
		Username: "Bob",
		Role:     service.RoleAdmin,
		Status:   service.StatusActive,
		Balance:  1,
	})
	s.mustCreateUser(&service.User{
		Email:  "c@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusDisabled,
	})

	users, page, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.UserListFilters{Status: service.StatusActive, Role: service.RoleAdmin, Search: "b@"})
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Equal(int64(1), page.Total, "ListWithFilters total mismatch")
	s.Require().Len(users, 1, "ListWithFilters len mismatch")
	s.Require().Equal(target.ID, users[0].ID, "ListWithFilters result mismatch")
}

// --- Balance operations ---

func (s *UserRepoSuite) TestUpdateBalance() {
	user := s.mustCreateUser(&service.User{Email: "bal@test.com", Balance: 10})

	err := s.repo.UpdateBalance(s.ctx, user.ID, 2.5)
	s.Require().NoError(err, "UpdateBalance")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(12.5, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestUpdateBalance_Negative() {
	user := s.mustCreateUser(&service.User{Email: "balneg@test.com", Balance: 10})

	err := s.repo.UpdateBalance(s.ctx, user.ID, -3)
	s.Require().NoError(err, "UpdateBalance with negative")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(7.0, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestApplyRedeemBalanceAdjustment_ConcurrentNeverNegative() {
	user := s.mustCreateUser(&service.User{Email: "redeem-bal-concurrent@test.com", Balance: 10})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.repo.ApplyRedeemBalanceAdjustment(context.Background(), user.ID, -7)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(0, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestDeductBalance() {
	user := s.mustCreateUser(&service.User{Email: "deduct@test.com", Balance: 10})

	err := s.repo.DeductBalance(s.ctx, user.ID, 5)
	s.Require().NoError(err, "DeductBalance")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(5.0, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestDeductBalance_InsufficientFunds() {
	user := s.mustCreateUser(&service.User{Email: "insuf@test.com", Balance: 5})

	// 透支策略：允许扣除超过余额的金额
	err := s.repo.DeductBalance(s.ctx, user.ID, 999)
	s.Require().NoError(err, "DeductBalance should allow overdraft")

	// 验证余额变为负数
	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(-994.0, got.Balance, 1e-6, "Balance should be negative after overdraft")
}

func (s *UserRepoSuite) TestDeductBalance_ExactAmount() {
	user := s.mustCreateUser(&service.User{Email: "exact@test.com", Balance: 10})

	err := s.repo.DeductBalance(s.ctx, user.ID, 10)
	s.Require().NoError(err, "DeductBalance exact amount")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(0.0, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestDeductBalance_AllowsOverdraft() {
	user := s.mustCreateUser(&service.User{Email: "overdraft@test.com", Balance: 5.0})

	// 扣除超过余额的金额 - 应该成功
	err := s.repo.DeductBalance(s.ctx, user.ID, 10.0)
	s.Require().NoError(err, "DeductBalance should allow overdraft")

	// 验证余额为负
	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(-5.0, got.Balance, 1e-6, "Balance should be -5.0 after overdraft")
}

func (s *UserRepoSuite) TestDeductAvailableBalance_ClampsToNonnegativeBalance() {
	for _, tc := range []struct {
		name        string
		balance     float64
		requested   float64
		wantDeduct  float64
		wantBalance float64
	}{
		{name: "enough balance", balance: 10, requested: 4, wantDeduct: 4, wantBalance: 6},
		{name: "insufficient balance", balance: 5, requested: 10, wantDeduct: 5, wantBalance: 0},
		{name: "negative balance unchanged", balance: -3, requested: 10, wantDeduct: 0, wantBalance: -3},
	} {
		s.Run(tc.name, func() {
			user := s.mustCreateUser(&service.User{Email: "available-" + strings.ReplaceAll(tc.name, " ", "-") + "@test.com", Balance: tc.balance})
			deducted, err := s.repo.DeductAvailableBalance(s.ctx, user.ID, tc.requested)
			s.Require().NoError(err)
			s.Require().InDelta(tc.wantDeduct, deducted, 1e-6)
			got, err := s.repo.GetByID(s.ctx, user.ID)
			s.Require().NoError(err)
			s.Require().InDelta(tc.wantBalance, got.Balance, 1e-6)
		})
	}
}

// --- Concurrency ---

func (s *UserRepoSuite) TestUpdateConcurrency() {
	user := s.mustCreateUser(&service.User{Email: "conc@test.com", Concurrency: 5})

	err := s.repo.UpdateConcurrency(s.ctx, user.ID, 3)
	s.Require().NoError(err, "UpdateConcurrency")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(8, got.Concurrency)
}

func (s *UserRepoSuite) TestUpdateConcurrency_Negative() {
	user := s.mustCreateUser(&service.User{Email: "concneg@test.com", Concurrency: 5})

	err := s.repo.UpdateConcurrency(s.ctx, user.ID, -2)
	s.Require().NoError(err, "UpdateConcurrency negative")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(3, got.Concurrency)
}

func (s *UserRepoSuite) TestApplyRedeemConcurrencyAdjustment_ConcurrentNeverNegative() {
	user := s.mustCreateUser(&service.User{Email: "redeem-concurrency-concurrent@test.com", Concurrency: 10})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.repo.ApplyRedeemConcurrencyAdjustment(context.Background(), user.ID, -7)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(0, got.Concurrency)
}

// --- ExistsByEmail ---

func (s *UserRepoSuite) TestExistsByEmail() {
	s.mustCreateUser(&service.User{Email: "exists@test.com"})

	exists, err := s.repo.ExistsByEmail(s.ctx, "exists@test.com")
	s.Require().NoError(err, "ExistsByEmail")
	s.Require().True(exists)

	notExists, err := s.repo.ExistsByEmail(s.ctx, "notexists@test.com")
	s.Require().NoError(err)
	s.Require().False(notExists)
}

// --- RemoveGroupFromAllowedGroups ---

func (s *UserRepoSuite) TestRemoveGroupFromAllowedGroups() {
	target := s.mustCreateGroup("target-42")
	other := s.mustCreateGroup("other-7")

	userA := s.mustCreateUser(&service.User{
		Email:         "a1@example.com",
		AllowedGroups: []int64{target.ID, other.ID},
	})
	s.mustCreateUser(&service.User{
		Email:         "a2@example.com",
		AllowedGroups: []int64{other.ID},
	})

	affected, err := s.repo.RemoveGroupFromAllowedGroups(s.ctx, target.ID)
	s.Require().NoError(err, "RemoveGroupFromAllowedGroups")
	s.Require().Equal(int64(1), affected, "expected 1 affected row")

	got, err := s.repo.GetByID(s.ctx, userA.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().NotContains(got.AllowedGroups, target.ID)
	s.Require().Contains(got.AllowedGroups, other.ID)
}

func (s *UserRepoSuite) TestRemoveGroupFromAllowedGroups_NoMatch() {
	groupA := s.mustCreateGroup("nomatch-a")
	groupB := s.mustCreateGroup("nomatch-b")

	s.mustCreateUser(&service.User{
		Email:         "nomatch@test.com",
		AllowedGroups: []int64{groupA.ID, groupB.ID},
	})

	affected, err := s.repo.RemoveGroupFromAllowedGroups(s.ctx, 999999)
	s.Require().NoError(err)
	s.Require().Zero(affected, "expected no affected rows")
}

// --- GetFirstAdmin ---

func (s *UserRepoSuite) TestGetFirstAdmin() {
	admin1 := s.mustCreateUser(&service.User{
		Email:  "admin1@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusActive,
	})
	s.mustCreateUser(&service.User{
		Email:  "admin2@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusActive,
	})

	got, err := s.repo.GetFirstAdmin(s.ctx)
	s.Require().NoError(err, "GetFirstAdmin")
	s.Require().Equal(admin1.ID, got.ID, "GetFirstAdmin mismatch")
}

func (s *UserRepoSuite) TestGetFirstAdmin_NoAdmin() {
	s.mustCreateUser(&service.User{
		Email:  "user@example.com",
		Role:   service.RoleUser,
		Status: service.StatusActive,
	})

	_, err := s.repo.GetFirstAdmin(s.ctx)
	s.Require().Error(err, "expected error when no admin exists")
}

func (s *UserRepoSuite) TestGetFirstAdmin_DisabledAdminIgnored() {
	s.mustCreateUser(&service.User{
		Email:  "disabled@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusDisabled,
	})
	activeAdmin := s.mustCreateUser(&service.User{
		Email:  "active@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusActive,
	})

	got, err := s.repo.GetFirstAdmin(s.ctx)
	s.Require().NoError(err, "GetFirstAdmin")
	s.Require().Equal(activeAdmin.ID, got.ID, "should return only active admin")
}

// --- Combined ---

func (s *UserRepoSuite) TestCRUD_And_Filters_And_AtomicUpdates() {
	user1 := s.mustCreateUser(&service.User{
		Email:    "a@example.com",
		Username: "Alice",
		Role:     service.RoleUser,
		Status:   service.StatusActive,
		Balance:  10,
	})
	user2 := s.mustCreateUser(&service.User{
		Email:    "b@example.com",
		Username: "Bob",
		Role:     service.RoleAdmin,
		Status:   service.StatusActive,
		Balance:  1,
	})
	s.mustCreateUser(&service.User{
		Email:  "c@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusDisabled,
	})

	got, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal(user1.Email, got.Email, "GetByID email mismatch")

	gotByEmail, err := s.repo.GetByEmail(s.ctx, user2.Email)
	s.Require().NoError(err, "GetByEmail")
	s.Require().Equal(user2.ID, gotByEmail.ID, "GetByEmail ID mismatch")

	got.Username = "Alice2"
	s.Require().NoError(s.repo.Update(s.ctx, got, service.UserUpdateFields{Username: true}), "Update")
	got2, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after update")
	s.Require().Equal("Alice2", got2.Username, "Update did not persist")

	s.Require().NoError(s.repo.UpdateBalance(s.ctx, user1.ID, 2.5), "UpdateBalance")
	got3, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after UpdateBalance")
	s.Require().InDelta(12.5, got3.Balance, 1e-6)

	s.Require().NoError(s.repo.DeductBalance(s.ctx, user1.ID, 5), "DeductBalance")
	got4, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after DeductBalance")
	s.Require().InDelta(7.5, got4.Balance, 1e-6)

	// 透支策略：允许扣除超过余额的金额
	err = s.repo.DeductBalance(s.ctx, user1.ID, 999)
	s.Require().NoError(err, "DeductBalance should allow overdraft")
	gotOverdraft, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after overdraft")
	s.Require().Less(gotOverdraft.Balance, 0.0, "Balance should be negative after overdraft")

	s.Require().NoError(s.repo.UpdateConcurrency(s.ctx, user1.ID, 3), "UpdateConcurrency")
	got5, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after UpdateConcurrency")
	s.Require().Equal(user1.Concurrency+3, got5.Concurrency)

	params := pagination.PaginationParams{Page: 1, PageSize: 10}
	users, page, err := s.repo.ListWithFilters(s.ctx, params, service.UserListFilters{Status: service.StatusActive, Role: service.RoleAdmin, Search: "b@"})
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Equal(int64(1), page.Total, "ListWithFilters total mismatch")
	s.Require().Len(users, 1, "ListWithFilters len mismatch")
	s.Require().Equal(user2.ID, users[0].ID, "ListWithFilters result mismatch")
}

// --- UpdateBalance/UpdateConcurrency 影响行数校验测试 ---

func (s *UserRepoSuite) TestUpdateBalance_NotFound() {
	err := s.repo.UpdateBalance(s.ctx, 999999, 10.0)
	s.Require().Error(err, "expected error for non-existent user")
	s.Require().ErrorIs(err, service.ErrUserNotFound)
}

func (s *UserRepoSuite) TestUpdateConcurrency_NotFound() {
	err := s.repo.UpdateConcurrency(s.ctx, 999999, 5)
	s.Require().Error(err, "expected error for non-existent user")
	s.Require().ErrorIs(err, service.ErrUserNotFound)
}

func (s *UserRepoSuite) TestDeductBalance_NotFound() {
	err := s.repo.DeductBalance(s.ctx, 999999, 5)
	s.Require().Error(err, "expected error for non-existent user")
	// DeductBalance 在用户不存在时返回 ErrUserNotFound
	s.Require().ErrorIs(err, service.ErrUserNotFound)
}

// These tests exercise the actual PostgreSQL recharge transaction with a fake
// authenticated Binance history source. No real credentials or funds are used.
type pkBinanceHistoryStub struct {
	mu       sync.Mutex
	receipts []binancepay.Receipt
	err      error
}

func (s *pkBinanceHistoryStub) History(_ context.Context, _, _, _ string, from, to time.Time) ([]binancepay.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	result := make([]binancepay.Receipt, 0)
	for _, receipt := range s.receipts {
		if !receipt.Time.Before(from) && !receipt.Time.After(to) {
			result = append(result, receipt)
		}
	}
	return result, nil
}
func (*pkBinanceHistoryStub) Probe(context.Context, string, string) error { return nil }
func (*pkBinanceHistoryStub) CloseIdleConnections()                       {}

func (*pkBinanceHistoryStub) Bills(context.Context, string, string, string, time.Time, time.Time) (*binancepay.BillsPage, error) {
	return &binancepay.BillsPage{Items: []binancepay.Bill{{ID: "display-only", Amount: "10.12345678", Currency: "USDT", Direction: "incoming"}}, Limit: 100, Total: 1}, nil
}

type pkBinanceFixture struct {
	svc      *service.BinanceRechargeService
	provider *pkBinanceHistoryStub
	user     *service.User
	account  *service.BinanceRechargeAccount
	order    *service.BinanceRechargeOrder
}

func newPKBinanceFixture(t *testing.T, multiplier ...string) *pkBinanceFixture {
	t.Helper()
	ctx := context.Background()
	provider := &pkBinanceHistoryStub{}
	svc := service.NewBinanceRechargeService(integrationDB, &AESEncryptor{key: make([]byte, 32)}, provider, nil, nil)
	user := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("binance-%d@example.com", time.Now().UnixNano()), Balance: 2.12345678})
	input := service.BinanceRechargeAccountInput{
		Name: "Binance integration", RecipientUID: fmt.Sprint(time.Now().UnixNano()),
		APIKey: "fixture-key", APISecret: "fixture-secret", Enabled: true, OrderTTLMinutes: 30,
	}
	if len(multiplier) > 0 {
		input.CreditMultiplier = &multiplier[0]
	}
	account, err := svc.SaveAccount(ctx, 0, input)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM binance_recharge_receipts WHERE account_id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM binance_recharge_orders WHERE account_id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM binance_recharge_accounts WHERE id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM redeem_codes WHERE used_by=$1`, user.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	})
	order, err := svc.CreateOrder(ctx, user.ID, account.ID, 10)
	require.NoError(t, err)
	// Put creation safely before the millisecond provider timestamp, without
	// sleeping or altering the production clock.
	_, err = integrationDB.ExecContext(ctx, `UPDATE binance_recharge_orders SET created_at=NOW()-INTERVAL '5 seconds' WHERE id=$1`, order.ID)
	require.NoError(t, err)
	return &pkBinanceFixture{svc: svc, provider: provider, user: user, account: account, order: order}
}

func (f *pkBinanceFixture) receipt(t *testing.T, id string, at time.Time) binancepay.Receipt {
	t.Helper()
	units, err := binancepay.ParseAmount(f.order.PayAmount)
	require.NoError(t, err)
	return binancepay.Receipt{TransactionID: id, AmountUnits: units, Time: at.UTC().Truncate(time.Millisecond)}
}

func (f *pkBinanceFixture) releasePoll(t *testing.T) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), `UPDATE binance_recharge_accounts SET next_poll_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, f.account.ID)
	require.NoError(t, err)
}

func TestPKBinanceExactCreditAndDuplicatePolling(t *testing.T) {
	f := newPKBinanceFixture(t)
	ctx := context.Background()
	f.provider.receipts = []binancepay.Receipt{f.receipt(t, "same-transfer", time.Now().Add(-time.Second))}
	second := service.NewBinanceRechargeService(integrationDB, &AESEncryptor{key: make([]byte, 32)}, f.provider, nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		instance := f.svc
		if i%2 == 1 {
			instance = second
		}
		go func(svc *service.BinanceRechargeService) { defer wg.Done(); _, _ = svc.CheckAccount(ctx, f.account.ID) }(instance)
	}
	wg.Wait()
	order, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargeCompleted, order.Status)
	require.Equal(t, f.order.PayAmount+"000", order.CreditedAmount)
	require.Equal(t, "1.000000", order.CreditMultiplier)
	for i := 0; i < 2; i++ {
		f.releasePoll(t)
		_, err = f.svc.CheckAccount(ctx, f.account.ID)
		require.NoError(t, err)
	}
	var balanceExact, totalExact bool
	err = integrationDB.QueryRowContext(ctx, `SELECT balance=2.12345678+$2::numeric, total_recharged=$2::numeric FROM users WHERE id=$1`, f.user.ID, f.order.PayAmount).Scan(&balanceExact, &totalExact)
	require.NoError(t, err)
	require.True(t, balanceExact, "credit must retain the full suffix and existing eight-decimal balance")
	require.True(t, totalExact, "cumulative recharge must be credited once")
	var count int
	err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM redeem_codes WHERE used_by=$1 AND type='balance' AND status='used' AND value=$2::numeric`, f.user.ID, f.order.PayAmount).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count, "existing balance history must contain one exact recharge entry")
	err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM binance_recharge_receipts WHERE account_id=$1 AND status='COMPLETED'`, f.account.ID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestPKBinanceLiveBillsNeverCreditAndDoNotDelayCollection(t *testing.T) {
	f := newPKBinanceFixture(t)
	ctx := context.Background()
	var scheduled time.Time
	err := integrationDB.QueryRowContext(ctx, `UPDATE binance_recharge_accounts SET next_poll_at=NOW()+INTERVAL '1 minute' WHERE id=$1 RETURNING next_poll_at`, f.account.ID).Scan(&scheduled)
	require.NoError(t, err)
	page, err := f.svc.Bills(ctx, f.account.ID, time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	require.Equal(t, "10.12345678", page.Items[0].Amount)
	var unchanged bool
	err = integrationDB.QueryRowContext(ctx, `SELECT balance=2.12345678 AND total_recharged=0 FROM users WHERE id=$1`, f.user.ID).Scan(&unchanged)
	require.NoError(t, err)
	require.True(t, unchanged, "reading provider bills must never credit a balance")
	var receipts int
	err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM binance_recharge_receipts WHERE account_id=$1`, f.account.ID).Scan(&receipts)
	require.NoError(t, err)
	require.Zero(t, receipts)
	var after time.Time
	err = integrationDB.QueryRowContext(ctx, `SELECT next_poll_at FROM binance_recharge_accounts WHERE id=$1`, f.account.ID).Scan(&after)
	require.NoError(t, err)
	require.True(t, scheduled.Equal(after), "bill browsing must preserve the collection schedule")
	second := service.NewBinanceRechargeService(integrationDB, &AESEncryptor{key: make([]byte, 32)}, f.provider, nil, nil)
	_, err = second.Bills(ctx, f.account.ID, time.Now().Add(-time.Hour), time.Now())
	require.Error(t, err, "bill cooldown must also apply to another service instance")
	f.provider.receipts = []binancepay.Receipt{f.receipt(t, "collect-after-view", time.Now().Add(-time.Second))}
	f.releasePoll(t)
	_, err = second.CheckAccount(ctx, f.account.ID)
	require.NoError(t, err, "bill cooldown must not prevent due collection")
	order, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargeCompleted, order.Status)
}

func TestPKBinanceExpiredOrderAcceptsOnTimeReceipt(t *testing.T) {
	f := newPKBinanceFixture(t)
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `UPDATE binance_recharge_orders SET created_at=NOW()-INTERVAL '20 minutes', expires_at=NOW()-INTERVAL '5 minutes', status='EXPIRED' WHERE id=$1`, f.order.ID)
	require.NoError(t, err)
	f.provider.receipts = []binancepay.Receipt{f.receipt(t, "delayed-history", time.Now().Add(-10*time.Minute))}
	// Disabling new orders must not abandon payments already in flight.
	_, err = f.svc.SaveAccount(ctx, f.account.ID, service.BinanceRechargeAccountInput{Name: f.account.Name, RecipientUID: f.account.RecipientUID, Enabled: false, OrderTTLMinutes: 30})
	require.NoError(t, err)
	_, err = f.svc.CheckAccount(ctx, f.account.ID)
	require.NoError(t, err)
	order, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargeCompleted, order.Status)
	require.Equal(t, f.order.PayAmount+"000", order.CreditedAmount)
	_, err = f.svc.CreateOrder(ctx, f.user.ID, f.account.ID, 10)
	require.Error(t, err, "disabled account must not issue new quotes")
}

func TestPKBinanceLateReceiptRequiresReview(t *testing.T) {
	f := newPKBinanceFixture(t)
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `UPDATE binance_recharge_orders SET created_at=NOW()-INTERVAL '20 minutes', expires_at=NOW()-INTERVAL '5 minutes', status='EXPIRED' WHERE id=$1`, f.order.ID)
	require.NoError(t, err)
	f.provider.receipts = []binancepay.Receipt{f.receipt(t, "late-payment", time.Now().Add(-time.Minute))}
	_, err = f.svc.CheckAccount(ctx, f.account.ID)
	require.NoError(t, err)
	order, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargeReview, order.Status)
	var unchanged bool
	err = integrationDB.QueryRowContext(ctx, `SELECT balance=2.12345678 AND total_recharged=0 FROM users WHERE id=$1`, f.user.ID).Scan(&unchanged)
	require.NoError(t, err)
	require.True(t, unchanged)
}

func TestPKBinanceAccountSecrecyAndOrderOwnership(t *testing.T) {
	f := newPKBinanceFixture(t)
	ctx := context.Background()
	_, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID+1000000)
	require.Error(t, err, "another user must not read the order")
	_, err = f.svc.SaveAccount(ctx, f.account.ID, service.BinanceRechargeAccountInput{Name: "Changed", RecipientUID: f.account.RecipientUID + "1", Enabled: true, OrderTTLMinutes: 30})
	require.Error(t, err, "a receiving UID with existing quotes is immutable")
	updated, err := f.svc.SaveAccount(ctx, f.account.ID, service.BinanceRechargeAccountInput{Name: "Renamed", RecipientUID: f.account.RecipientUID, Enabled: true, OrderTTLMinutes: 15})
	require.NoError(t, err)
	require.True(t, updated.APIKeyConfigured)
	require.True(t, updated.APISecretConfigured)
	var key, secret string
	err = integrationDB.QueryRowContext(ctx, `SELECT api_key_encrypted,api_secret_encrypted FROM binance_recharge_accounts WHERE id=$1`, f.account.ID).Scan(&key, &secret)
	require.NoError(t, err)
	require.NotContains(t, key, "fixture-key")
	require.NotContains(t, secret, "fixture-secret")
	_, err = f.svc.CreateOrder(ctx, f.user.ID, f.account.ID, 10)
	require.Error(t, err, "rapid creation is rate limited inside the database transaction")
	_, err = integrationDB.ExecContext(ctx, `UPDATE binance_recharge_orders SET created_at=NOW()-INTERVAL '1 minute',status='EXPIRED' WHERE id=$1`, f.order.ID)
	require.NoError(t, err)
	next, err := f.svc.CreateOrder(ctx, f.user.ID, f.account.ID, 10)
	require.NoError(t, err)
	require.NotEqual(t, f.order.PayAmount, next.PayAmount, "historic full quotes must not be reused")
}

func TestPKBinanceStatsCountOnlyCompletedAndScopeUsers(t *testing.T) {
	f := newPKBinanceFixture(t)
	ctx := context.Background()
	f.setQuoteAmount(t, 1012345)
	multiplier := "2.5"
	f.saveMultiplier(t, &multiplier)
	other := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("binance-other-%d@example.com", time.Now().UnixNano())})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM binance_recharge_receipts WHERE account_id=$1`, f.account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM binance_recharge_orders WHERE user_id=$1`, other.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM redeem_codes WHERE used_by=$1`, other.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, other.ID)
	})
	otherOrder, err := f.svc.CreateOrder(ctx, other.ID, f.account.ID, 20)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE binance_recharge_orders SET amount_units=2054321 WHERE id=$1`, otherOrder.ID)
	require.NoError(t, err)
	otherOrder, err = f.svc.GetOrder(ctx, otherOrder.ID, other.ID)
	require.NoError(t, err)
	require.Equal(t, "2.500000", otherOrder.CreditMultiplier)
	_, err = integrationDB.ExecContext(ctx, `UPDATE binance_recharge_orders SET created_at=NOW()-INTERVAL '1 minute' WHERE account_id=$1`, f.account.ID)
	require.NoError(t, err)
	otherUnits, err := binancepay.ParseAmount(otherOrder.PayAmount)
	require.NoError(t, err)
	first := f.receipt(t, "stats-one", time.Now().Add(-time.Second))
	f.provider.receipts = []binancepay.Receipt{first, {TransactionID: "stats-two", AmountUnits: otherUnits, Time: first.Time}}
	_, err = f.svc.CheckAccount(ctx, f.account.ID)
	require.NoError(t, err)
	_, err = f.svc.CreateOrder(ctx, f.user.ID, f.account.ID, 30)
	require.NoError(t, err, "pending quote should exist but never enter completed totals")
	all, err := f.svc.Stats(ctx, service.BinanceRechargeListParams{AccountID: f.account.ID})
	require.NoError(t, err)
	require.Equal(t, int64(2), all.TotalOrders)
	require.Equal(t, binancepay.FormatAmount(first.AmountUnits+otherUnits), all.TotalAmount)
	require.Equal(t, "61.48147500", all.TotalCreditedAmount)
	require.Len(t, all.Items, 2)
	for _, item := range all.Items {
		if item.UserID == f.user.ID {
			require.Equal(t, "10.12345", item.TotalAmount)
			require.Equal(t, "10.12345000", item.TotalCreditedAmount)
		} else {
			require.Equal(t, other.ID, item.UserID)
			require.Equal(t, "20.54321", item.TotalAmount)
			require.Equal(t, "51.35802500", item.TotalCreditedAmount)
		}
	}
	foreign := newPKBinanceFixture(t, "6.7")
	foreign.provider.receipts = []binancepay.Receipt{foreign.receipt(t, "foreign-account", time.Now().Add(-time.Second))}
	_, err = foreign.svc.CheckAccount(ctx, foreign.account.ID)
	require.NoError(t, err)
	self, err := f.svc.Stats(ctx, service.BinanceRechargeListParams{AccountID: f.account.ID, UserID: f.user.ID})
	require.NoError(t, err)
	require.Equal(t, int64(1), self.TotalOrders)
	require.Equal(t, f.order.PayAmount, self.TotalAmount)
	require.Equal(t, "10.12345000", self.TotalCreditedAmount)
	require.Len(t, self.Items, 1)
	require.Equal(t, f.user.ID, self.Items[0].UserID)
	require.Empty(t, self.Items[0].UserEmail)
	require.Equal(t, "10.12345000", self.Items[0].TotalCreditedAmount)
	require.Len(t, self.AccountOptions, 1)
	require.Equal(t, f.account.ID, self.AccountOptions[0].ID)
	require.Equal(t, "2.500000", self.AccountOptions[0].CreditMultiplier, "account options reflect current configuration, while orders retain snapshots")
	foreignStats, err := f.svc.Stats(ctx, service.BinanceRechargeListParams{AccountID: foreign.account.ID, UserID: f.user.ID})
	require.NoError(t, err)
	require.Zero(t, foreignStats.TotalOrders)
	require.Equal(t, "0.00000000", foreignStats.TotalCreditedAmount)
	require.Empty(t, foreignStats.Items)
	orders, total, err := f.svc.ListOrders(ctx, service.BinanceRechargeListParams{AccountID: f.account.ID, UserID: f.user.ID})
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	for _, order := range orders {
		require.Equal(t, f.user.ID, order.UserID)
	}
}

func TestPKBinanceCreditTransactionRollsBackAndRetries(t *testing.T) {
	f := newPKBinanceFixture(t)
	ctx := context.Background()
	code := fmt.Sprintf("BN-%d", f.order.ID)
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO redeem_codes(code,type,value,status,used_by) VALUES($1,'balance',1,'used',$2)`, code, f.user.ID)
	require.NoError(t, err)
	f.provider.receipts = []binancepay.Receipt{f.receipt(t, "rollback-retry", time.Now().Add(-time.Second))}
	_, err = f.svc.CheckAccount(ctx, f.account.ID)
	require.Error(t, err, "failed balance-history insert must fail the whole credit")
	var unchanged bool
	err = integrationDB.QueryRowContext(ctx, `SELECT balance=2.12345678 AND total_recharged=0 FROM users WHERE id=$1`, f.user.ID).Scan(&unchanged)
	require.NoError(t, err)
	require.True(t, unchanged)
	var claimed int
	err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM binance_recharge_receipts WHERE account_id=$1`, f.account.ID).Scan(&claimed)
	require.NoError(t, err)
	require.Zero(t, claimed, "failed transaction must not consume the transfer")
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM redeem_codes WHERE code=$1`, code)
	require.NoError(t, err)
	f.releasePoll(t)
	_, err = f.svc.CheckAccount(ctx, f.account.ID)
	require.NoError(t, err)
	order, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargeCompleted, order.Status)
}

func TestPKBinanceOlderUnrelatedTransferDoesNotPoisonQuote(t *testing.T) {
	f := newPKBinanceFixture(t)
	ctx := context.Background()
	f.provider.receipts = []binancepay.Receipt{
		f.receipt(t, "before-quote", time.Now().Add(-time.Hour)),
		f.receipt(t, "valid-for-quote", time.Now().Add(-time.Second)),
	}
	_, err := f.svc.CheckAccount(ctx, f.account.ID)
	require.NoError(t, err)
	order, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargeCompleted, order.Status)
	require.Equal(t, "valid-for-quote", order.TransactionID)
	var unassigned bool
	err = integrationDB.QueryRowContext(ctx, `SELECT order_id IS NULL AND status='REVIEW' FROM binance_recharge_receipts WHERE account_id=$1 AND transaction_id='before-quote'`, f.account.ID).Scan(&unassigned)
	require.NoError(t, err)
	require.True(t, unassigned)
}

func TestPKBinanceManualCheckReadsOriginalHistoricalWindow(t *testing.T) {
	f := newPKBinanceFixture(t)
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `UPDATE binance_recharge_orders SET created_at=NOW()-INTERVAL '3 days',expires_at=NOW()-INTERVAL '3 days'+INTERVAL '30 minutes',status='EXPIRED' WHERE id=$1`, f.order.ID)
	require.NoError(t, err)
	f.provider.receipts = []binancepay.Receipt{f.receipt(t, "old-on-time-payment", time.Now().Add(-72*time.Hour+10*time.Minute))}
	_, err = f.svc.CheckAccount(ctx, f.account.ID)
	require.NoError(t, err)
	unpaid, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargeExpired, unpaid.Status)
	f.releasePoll(t)
	paid, err := f.svc.CheckOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargeCompleted, paid.Status)
	require.Equal(t, f.order.PayAmount+"000", paid.CreditedAmount)
}

// Set a deterministic full amount in a freshly created fixture. The production
// service still creates the order and snapshots its account multiplier.
func (f *pkBinanceFixture) setQuoteAmount(t *testing.T, units int64) {
	t.Helper()
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `UPDATE binance_recharge_orders SET requested_amount=$2,amount_units=$3 WHERE id=$1`, f.order.ID, units/binancepay.MoneyScale, units)
	require.NoError(t, err)
	f.order, err = f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
}

func (f *pkBinanceFixture) saveMultiplier(t *testing.T, multiplier *string) *service.BinanceRechargeAccount {
	t.Helper()
	a, err := f.svc.SaveAccount(context.Background(), f.account.ID, service.BinanceRechargeAccountInput{
		Name: f.account.Name, RecipientUID: f.account.RecipientUID, Enabled: true, OrderTTLMinutes: 30,
		CreditMultiplier: multiplier,
	})
	require.NoError(t, err)
	return a
}

func TestPKBinanceMultiplierCreditsFullUSDTWithEightDecimalRounding(t *testing.T) {
	for _, tc := range []struct {
		name       string
		multiplier string
		units      int64
		want       string
	}{
		{name: "requested example retains suffix", multiplier: "6.7", units: 1012345, want: "67.82711500"},
		{name: "minimum multiplier", multiplier: "0.000001", units: 1012345, want: "0.00001012"},
		{name: "half rounds up", multiplier: "0.0001", units: 100005, want: "0.00010001"},
		{name: "below half rounds down", multiplier: "0.0001", units: 100004, want: "0.00010000"},
		{name: "smallest quote remains positive", multiplier: "0.000001", units: 100001, want: "0.00000100"},
		{name: "maximum quote and multiplier", multiplier: "1000", units: 100000099999, want: "1000000999.99000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPKBinanceFixture(t, tc.multiplier)
			ctx := context.Background()
			f.setQuoteAmount(t, tc.units)
			require.Equal(t, tc.want, f.order.SystemAmount)
			require.Equal(t, f.account.CreditMultiplier, f.order.CreditMultiplier)
			require.Equal(t, "0.00000000", f.order.CreditedAmount, "a quote must not report funds as already credited")
			f.provider.receipts = []binancepay.Receipt{f.receipt(t, "multiplier-exact", time.Now().Add(-time.Second))}
			_, err := f.svc.CheckAccount(ctx, f.account.ID)
			require.NoError(t, err)
			paid, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
			require.NoError(t, err)
			require.Equal(t, service.BinanceRechargeCompleted, paid.Status)
			require.Equal(t, binancepay.FormatAmount(tc.units), paid.PayAmount)
			require.Equal(t, tc.want, paid.SystemAmount)
			require.Equal(t, tc.want, paid.CreditedAmount)
			var exact bool
			err = integrationDB.QueryRowContext(ctx, `SELECT balance=2.12345678+$2::numeric AND total_recharged=$2::numeric FROM users WHERE id=$1`, f.user.ID, tc.want).Scan(&exact)
			require.NoError(t, err)
			require.True(t, exact, "balance and cumulative recharge must use the rounded system amount")
			var ledgerCount int
			err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM redeem_codes WHERE used_by=$1 AND type='balance' AND status='used' AND value=$2::numeric`, f.user.ID, tc.want).Scan(&ledgerCount)
			require.NoError(t, err)
			require.Equal(t, 1, ledgerCount)
			stats, err := f.svc.Stats(ctx, service.BinanceRechargeListParams{AccountID: f.account.ID, UserID: f.user.ID})
			require.NoError(t, err)
			require.Equal(t, paid.PayAmount, stats.TotalAmount, "USDT statistics keep their existing meaning")
			require.Equal(t, tc.want, stats.TotalCreditedAmount)
			require.Len(t, stats.Items, 1)
			require.Equal(t, tc.want, stats.Items[0].TotalCreditedAmount)
		})
	}
}

func TestPKBinanceMultiplierSnapshotSurvivesAccountChangesAndConcurrentChecks(t *testing.T) {
	f := newPKBinanceFixture(t, "6.7")
	ctx := context.Background()
	f.setQuoteAmount(t, 1012345)
	require.Equal(t, "6.700000", f.order.CreditMultiplier)
	changed := "9"
	updated := f.saveMultiplier(t, &changed)
	require.Equal(t, "9.000000", updated.CreditMultiplier)
	updated = f.saveMultiplier(t, nil)
	require.Equal(t, "9.000000", updated.CreditMultiplier, "omitting the field must preserve an existing account's multiplier")
	require.True(t, updated.APIKeyConfigured)
	require.True(t, updated.APISecretConfigured)
	config, err := f.svc.Config(ctx)
	require.NoError(t, err)
	found := false
	for _, a := range config.Accounts {
		if a.ID == f.account.ID {
			found = true
			require.Equal(t, "9.000000", a.CreditMultiplier)
		}
	}
	require.True(t, found, "the public account configuration must expose the current multiplier")
	_, err = integrationDB.ExecContext(ctx, `UPDATE binance_recharge_orders SET created_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, f.order.ID)
	require.NoError(t, err)
	next, err := f.svc.CreateOrder(ctx, f.user.ID, f.account.ID, 20)
	require.NoError(t, err)
	require.Equal(t, "9.000000", next.CreditMultiplier)
	require.Equal(t, "0.00000000", next.CreditedAmount)
	prior, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, "6.700000", prior.CreditMultiplier)
	require.Equal(t, "67.82711500", prior.SystemAmount)
	f.provider.receipts = []binancepay.Receipt{f.receipt(t, "snapshot-duplicate", time.Now().Add(-time.Second))}
	second := service.NewBinanceRechargeService(integrationDB, &AESEncryptor{key: make([]byte, 32)}, f.provider, nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		svc := f.svc
		if i%2 == 1 {
			svc = second
		}
		go func(instance *service.BinanceRechargeService) {
			defer wg.Done()
			_, _ = instance.CheckAccount(ctx, f.account.ID)
		}(svc)
	}
	wg.Wait()
	f.releasePoll(t)
	_, err = second.CheckAccount(ctx, f.account.ID)
	require.NoError(t, err)
	paid, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargeCompleted, paid.Status)
	require.Equal(t, "67.82711500", paid.CreditedAmount)
	require.Equal(t, "6.700000", paid.CreditMultiplier)
	var exact bool
	err = integrationDB.QueryRowContext(ctx, `SELECT balance=69.95057178 AND total_recharged=67.827115 FROM users WHERE id=$1`, f.user.ID).Scan(&exact)
	require.NoError(t, err)
	require.True(t, exact, "two service instances must credit the original snapshot exactly once")
	var ledger, receipts int
	err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM redeem_codes WHERE used_by=$1 AND value=67.827115 AND code=$2`, f.user.ID, fmt.Sprintf("BN-%d", f.order.ID)).Scan(&ledger)
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM binance_recharge_receipts WHERE account_id=$1 AND status='COMPLETED'`, f.account.ID).Scan(&receipts)
	require.NoError(t, err)
	require.Equal(t, 1, ledger)
	require.Equal(t, 1, receipts)
	next, err = f.svc.GetOrder(ctx, next.ID, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, service.BinanceRechargePending, next.Status)
	require.Equal(t, "0.00000000", next.CreditedAmount)
}

func TestPKBinanceMultiplierRejectsOldSettlementAtomically(t *testing.T) {
	for _, multiplier := range []string{"1", "6.7"} {
		t.Run(multiplier, func(t *testing.T) {
			f := newPKBinanceFixture(t, multiplier)
			ctx := context.Background()
			f.setQuoteAmount(t, 1012345)
			tx, err := integrationDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			// This is the released pre-multiplier write sequence. The final
			// order constraint must reject it and all preceding money writes.
			_, err = tx.ExecContext(ctx, `INSERT INTO binance_recharge_receipts(account_id,recipient_uid,transaction_id,amount_units,paid_at,status) VALUES($1,$2,'old-settlement',1012345,NOW(),'REVIEW')`, f.account.ID, f.account.RecipientUID)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, `UPDATE users SET balance=balance+10.12345,total_recharged=total_recharged+10.12345 WHERE id=$1`, f.user.ID)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, `INSERT INTO redeem_codes(code,type,value,status,used_by) VALUES($1,'balance',10.12345,'used',$2)`, fmt.Sprintf("BN-%d", f.order.ID), f.user.ID)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, `UPDATE binance_recharge_orders SET status='COMPLETED',credited_units=amount_units,transaction_id='old-settlement',paid_at=NOW(),completed_at=NOW() WHERE id=$1`, f.order.ID)
			requirePKBinanceSQLState(t, err, "23514")
			require.NoError(t, tx.Rollback())
			var unchanged bool
			err = integrationDB.QueryRowContext(ctx, `SELECT balance=2.12345678 AND total_recharged=0 FROM users WHERE id=$1`, f.user.ID).Scan(&unchanged)
			require.NoError(t, err)
			require.True(t, unchanged)
			var ledger, receipts int
			err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM redeem_codes WHERE used_by=$1`, f.user.ID).Scan(&ledger)
			require.NoError(t, err)
			err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM binance_recharge_receipts WHERE account_id=$1`, f.account.ID).Scan(&receipts)
			require.NoError(t, err)
			require.Zero(t, ledger)
			require.Zero(t, receipts)
			pending, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
			require.NoError(t, err)
			require.Equal(t, service.BinanceRechargePending, pending.Status)
			require.Equal(t, "0.00000000", pending.CreditedAmount)
			f.provider.receipts = []binancepay.Receipt{f.receipt(t, "old-settlement", time.Now().Add(-time.Second))}
			_, err = f.svc.CheckAccount(ctx, f.account.ID)
			require.NoError(t, err, "an aborted old binary must not consume the provider transaction")
			paid, err := f.svc.GetOrder(ctx, f.order.ID, f.user.ID)
			require.NoError(t, err)
			require.Equal(t, service.BinanceRechargeCompleted, paid.Status)
			require.Equal(t, f.order.SystemAmount, paid.CreditedAmount)
		})
	}
}
