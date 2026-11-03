package securityaudit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type staticSettingRepository struct {
	values map[string]string
}

func (r staticSettingRepository) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (r staticSettingRepository) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}
func (r staticSettingRepository) Set(context.Context, string, string) error { return nil }
func (r staticSettingRepository) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = r.values[key]
	}
	return result, nil
}
func (r staticSettingRepository) SetMultiple(context.Context, map[string]string) error { return nil }
func (r staticSettingRepository) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r staticSettingRepository) Delete(context.Context, string) error { return nil }

func TestPromptServiceHasExplicitIdempotentLifecycle(t *testing.T) {
	config := NewConfigManager(nil, staticSettingRepository{values: map[string]string{
		SettingKeyPromptAuditConfig: "",
		SettingKeyRiskControl:       "false",
	}}, nil, prefixEncryptor{}, testTotpKeyConfig())
	service := NewPromptService(
		config,
		NewPostgreSQLRepository(nil),
		NewRedisPayloadStore(nil),
		NewOpenAICompatibleScanner(),
		NewAtomicMetrics(),
	)

	require.Nil(t, service.cancel, "construction must not start background work")
	require.NoError(t, service.Start(context.Background()))
	require.NotNil(t, service.cancel)
	require.NoError(t, service.Start(context.Background()), "Start must be idempotent")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, service.Shutdown(ctx))
	require.Nil(t, service.cancel)
	require.NoError(t, service.Shutdown(ctx), "Shutdown must be idempotent")
}

func TestPromptServiceStartReportsDependencyFailureWithoutPanic(t *testing.T) {
	service := &PromptService{}
	require.Error(t, service.Start(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, service.Shutdown(ctx))
}

func TestPromptServiceBlockingLatestTurnOnlyUsesNarrowSnapshot(t *testing.T) {
	seen := make([]string, 0, 2)
	evaluator := newGuardEvaluator(PromptScannerFunc(func(_ context.Context, _ ActiveEndpoint, chunk string, _ []string) (*NormalizedResult, error) {
		seen = append(seen, chunk)
		return &NormalizedResult{Decision: EventPass, RiskLevel: RiskLow, Action: ActionAllow, ScannerScores: map[string]float64{}, ScannerEvidence: map[string]string{}}, nil
	}), nil, NewAtomicMetrics(), 2, 2)
	service := &PromptService{
		config: &fakeConfigStore{active: true, cfg: ActiveConfig{
			RiskControlEnabled: true, Enabled: true, BlockingEnabled: true, BlockingLatestTurnOnly: true, GroupIDs: []int64{1},
			Scanners: AllScannerIDs, Endpoints: []ActiveEndpoint{{ID: "guard-1", Enabled: true, TimeoutMS: 1000, InputLimit: 4096}},
		}},
		evaluator: evaluator,
	}
	decision, err := service.Evaluate(context.Background(), Request{GroupID: promptTestGroupID(1), Protocol: "openai_chat_completions", Body: []byte(`{"messages":[{"role":"system","content":"system instruction"},{"role":"user","content":"older user input"},{"role":"assistant","content":"previous output"},{"role":"user","content":"latest user input"}]}`)})
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, decision.Kind)
	require.Equal(t, []string{"latest user input", "previous output"}, seen)
}

func TestPromptFastModeKeepsLocalBlockAndDoesNotWaitForRemote(t *testing.T) {
	for _, fast := range []bool{false, true} {
		cfg := guardConfig(ActiveEndpoint{ID: "node", Enabled: true, TimeoutMS: 1000, InputLimit: 4000})
		cfg.Engine, cfg.FastMode, cfg.GroupIDs = PromptEngineRemote, fast, []int64{1}
		cfg.LocalRules = []LocalRule{{ID: "fixture", Name: "Fixture", Kind: "keyword", Pattern: "forbidden", Category: "jailbreak", Action: "block", Enabled: true}}
		var err error
		cfg.compiledRules, err = compileLocalRules(cfg.LocalRules)
		require.NoError(t, err)
		calls := 0
		eval := NewGuardEvaluator(PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
			calls++
			return nil, &GuardError{Code: "audit_node_timeout", Retryable: true}
		}), nil, nil)
		svc := &PromptService{config: &fakeConfigStore{active: true, cfg: cfg}, evaluator: eval}
		d, err := svc.Evaluate(context.Background(), Request{GroupID: promptTestGroupID(1), Protocol: "openai_chat_completions", Body: []byte(`{"messages":[{"role":"user","content":"forbidden"}]}`)})
		if !fast {
			require.Error(t, err, "strict mode must still fail closed")
			require.Equal(t, 1, calls)
		} else {
			require.NoError(t, err)
			require.Equal(t, DecisionBlock, d.Kind)
			require.Zero(t, calls)
		}
	}
}

func TestPromptFastModeAllowsThenQueuesRemoteObservation(t *testing.T) {
	cfg := asyncConfig()
	cfg.Engine, cfg.FastMode, cfg.BlockingEnabled = PromptEngineRemote, true, true
	cfg.Endpoints[0].InputLimit = 4000
	cfg.LocalRules = []LocalRule{{ID: "fixture", Name: "Fixture", Kind: "keyword", Pattern: "forbidden", Category: "jailbreak", Action: "block", Enabled: true}}
	var err error
	cfg.compiledRules, err = compileLocalRules(cfg.LocalRules)
	require.NoError(t, err)
	store := &fakeConfigStore{active: true, cfg: cfg}
	repo, payload := &fakeJobRepository{}, &fakePayloadStore{}
	calls := 0
	scanner := PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
		calls++
		return &NormalizedResult{Decision: EventCritical, RiskLevel: RiskHigh, Action: ActionBlock}, nil
	})
	svc := &PromptService{
		config: store, enqueuer: NewEnqueuer(store, repo, payload),
		evaluator: NewGuardEvaluator(scanner, repo, nil), runner: NewRunner(store, repo, payload, scanner, nil),
		background: context.Background(), enqueueSlots: make(chan struct{}, 1),
	}
	d, err := svc.Evaluate(context.Background(), asyncRequest())
	require.NoError(t, err)
	require.True(t, d.AllowNextStage)
	require.Zero(t, calls, "preflight only evaluates local rules")
	svc.enqueueWG.Wait()
	require.NotNil(t, repo.createJob, "fast mode must queue despite effective blocking mode")
	require.NotEmpty(t, payload.values[repo.createJob.ID])
	require.NoError(t, svc.runner.processJob(context.Background(), 0, cfg, repo.createJob))
	require.Equal(t, 1, calls)
	require.Equal(t, 1, repo.completeCount, "remote result is completed through the async observation repository")
	require.Equal(t, 1, repo.eventCount)
}

func TestPromptServiceSelectedGroupOnlySkipsBeforeParsingOrQueueing(t *testing.T) {
	for _, engine := range []string{PromptEngineRemote, PromptEngineLocalRules} {
		for _, groups := range [][]int64{nil, {1}} {
			for _, requestGroup := range []*int64{nil, promptTestGroupID(0), promptTestGroupID(2)} {
				cfg := asyncConfig()
				cfg.Engine, cfg.GroupIDs, cfg.AllGroups = engine, groups, true
				store := &fakeConfigStore{active: true, cfg: cfg}
				service := &PromptService{config: store, enqueuer: &Enqueuer{}, evaluator: NewGuardEvaluator(nil, nil, nil)}
				req := Request{GroupID: requestGroup, Protocol: "openai_chat_completions", Body: []byte("invalid-json")}
				require.False(t, service.IncludesGroup(requestGroup))
				require.NoError(t, service.Enqueue(context.Background(), req), "must skip before accessing uninitialized queue")
				store.cfg.BlockingEnabled = true
				decision, err := service.Evaluate(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, DecisionAllow, decision.Kind)
			}
		}
	}
}

func TestPromptServiceDegradedBlockingRespectsKnownGroupScope(t *testing.T) {
	manager := &ConfigManager{}
	manager.snapshot.Store(&activeConfigSnapshot{active: ActiveConfig{RiskControlEnabled: true, Enabled: true, GroupIDs: []int64{1}}})
	manager.expectedBlocking.Store(true)
	manager.configUntrusted.Store(true)
	service := &PromptService{config: manager, evaluator: NewGuardEvaluator(nil, nil, nil)}
	req := Request{GroupID: promptTestGroupID(2), Body: []byte("invalid")}
	decision, err := service.Evaluate(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, decision.Kind)
	req.GroupID = promptTestGroupID(1)
	_, err = service.Evaluate(context.Background(), req)
	require.Error(t, err, "in-scope degraded blocking must remain fail-closed")
}

func TestPromptServiceNewBlockingScopeCannotBeBypassedByStaleSnapshot(t *testing.T) {
	manager := &ConfigManager{}
	manager.snapshot.Store(&activeConfigSnapshot{active: ActiveConfig{RiskControlEnabled: true, Enabled: true, ConfigVersion: 1, GroupIDs: []int64{1}}})
	manager.expected.Store(2)
	manager.expectedBlocking.Store(true)
	manager.configUntrusted.Store(true)
	service := &PromptService{config: manager, evaluator: NewGuardEvaluator(nil, nil, nil)}
	req := Request{GroupID: promptTestGroupID(2), Body: []byte("invalid")}
	require.True(t, service.IncludesGroup(req.GroupID), "unknown new scope must not use old group selection")
	_, err := service.Evaluate(context.Background(), req)
	require.Error(t, err)

	// A parsable new scope remains usable even if some other part of its
	// configuration cannot be activated: only the selected group is blocked.
	manager.observeExpectedState(`{"enabled":true,"blocking_enabled":true,"config_version":2,"group_ids":[2],"strategy":"invalid"}`, true)
	manager.configUntrusted.Store(false)
	previous := *manager.snapshot.Load()
	previous.active.BlockingEnabled = true
	manager.snapshot.Store(&previous)
	require.True(t, manager.BlockingActivationDegraded(), "stale blocking snapshots also require current activation")
	require.True(t, service.IncludesGroup(promptTestGroupID(2)))
	require.False(t, service.IncludesGroup(promptTestGroupID(1)))
	require.False(t, service.IncludesGroup(promptTestGroupID(3)))
	_, err = service.Evaluate(context.Background(), req)
	require.Error(t, err)
	req.GroupID = promptTestGroupID(3)
	decision, err := service.Evaluate(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, decision.Kind)
}

func TestPromptServiceRejectsInvalidDeleteConfirmationClaims(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	filter := EventFilter{Decision: string(EventCritical), StartAt: &start, EndAt: &end}
	const snapshotMaxID int64 = 10
	filterHash := FilterHash(filter, snapshotMaxID)
	validClaims := deleteClaims{
		FilterHash: filterHash, SnapshotMaxID: snapshotMaxID, AdminID: 7,
		IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}
	claimsToken := func(claims deleteClaims) string {
		raw, err := json.Marshal(claims)
		require.NoError(t, err)
		return string(raw)
	}
	validRequest := DeleteByFilterRequest{
		Filter: filter, SnapshotMaxID: snapshotMaxID, FilterHash: filterHash,
		ConfirmationToken: claimsToken(validClaims), Confirm: true,
	}

	tests := []struct {
		name    string
		request DeleteByFilterRequest
		adminID int64
	}{
		{name: "confirm false", request: func() DeleteByFilterRequest { value := validRequest; value.Confirm = false; return value }(), adminID: 7},
		{name: "malformed token", request: func() DeleteByFilterRequest {
			value := validRequest
			value.ConfirmationToken = "not-json"
			return value
		}(), adminID: 7},
		{name: "different administrator", request: validRequest, adminID: 8},
		{name: "filter hash mismatch", request: func() DeleteByFilterRequest {
			value := validRequest
			value.FilterHash = strings.Repeat("b", 64)
			return value
		}(), adminID: 7},
		{name: "snapshot mismatch", request: func() DeleteByFilterRequest { value := validRequest; value.SnapshotMaxID++; return value }(), adminID: 7},
		{name: "expired", request: func() DeleteByFilterRequest {
			value := validRequest
			claims := validClaims
			claims.ExpiresAt = now
			value.ConfirmationToken = claimsToken(claims)
			return value
		}(), adminID: 7},
	}

	service := &PromptService{config: &fakeConfigStore{}, clock: fixedClock{now: now}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := service.DeleteByFilter(context.Background(), test.request, test.adminID)
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
}
