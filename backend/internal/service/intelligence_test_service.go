package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	intelligenceTestSettingKey    = "pk_intelligence_test_config"
	intelligenceTestTimeout       = 5 * time.Minute
	intelligenceTestLease         = 6 * time.Minute
	intelligenceTestArtifactLimit = 256 * 1024
	intelligenceTestResponseLimit = 4 * 1024 * 1024
)

const intelligenceTestCandyPrompt = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

形状    苹果味    桃子味    西瓜味
圆形      7         9         8
五角星形  7         6         4`

type IntelligenceTestGroupOverride struct {
	GroupID int64  `json:"group_id"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

type IntelligenceTestModeConfig struct {
	Enabled         bool                            `json:"enabled"`
	Prompt          string                          `json:"prompt"`
	Model           string                          `json:"model"`
	Effort          string                          `json:"effort"`
	IntervalMinutes int                             `json:"interval_minutes"`
	ExpectedAnswer  int                             `json:"expected_answer"`
	GroupOverrides  []IntelligenceTestGroupOverride `json:"group_overrides"`
}

type IntelligenceTestConfig struct {
	Enabled    bool                       `json:"enabled"`
	SVG        IntelligenceTestModeConfig `json:"svg"`
	Candy      IntelligenceTestModeConfig `json:"candy"`
	generation string
}

type IntelligenceTestGroup struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Platform      string `json:"platform"`
	Selected      bool   `json:"selected"`
	KeyConfigured bool   `json:"key_configured"`
}

type IntelligenceTestResult struct {
	ID          int64      `json:"id"`
	GroupID     int64      `json:"group_id"`
	Mode        string     `json:"mode"`
	Status      string     `json:"status"`
	Model       *string    `json:"model"`
	Effort      *string    `json:"effort"`
	Passed      *bool      `json:"passed,omitempty"`
	HasArtifact bool       `json:"has_artifact"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	DurationMS  *int64     `json:"duration_ms,omitempty"`
}

// IntelligenceTestService owns the only execution path. Public reads never enqueue work.
type IntelligenceTestService struct {
	db            *sql.DB
	groupRepo     GroupRepository
	settingRepo   SettingRepository
	apiKeyService *APIKeyService
	apiKeyRepo    APIKeyRepository
	userRepo      UserRepository
	cfg           *config.Config
	client        *http.Client
	mu            sync.Mutex
	cancel        context.CancelFunc
	done          chan struct{}
	wake          chan struct{}
	active        map[int64]context.CancelFunc
	inflight      map[intelligenceTestJob]*intelligenceTestWorker
	workers       sync.WaitGroup
}

func NewIntelligenceTestService(db *sql.DB, groupRepo GroupRepository, settingRepo SettingRepository, apiKeyService *APIKeyService, apiKeyRepo APIKeyRepository, cfg *config.Config) *IntelligenceTestService {
	s := &IntelligenceTestService{db: db, groupRepo: groupRepo, settingRepo: settingRepo, apiKeyService: apiKeyService, apiKeyRepo: apiKeyRepo, cfg: cfg, active: make(map[int64]context.CancelFunc), wake: make(chan struct{}, 1), inflight: make(map[intelligenceTestJob]*intelligenceTestWorker)}
	if apiKeyService != nil {
		s.userRepo = apiKeyService.userRepo
	}
	s.client = &http.Client{Timeout: intelligenceTestTimeout, Transport: &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns: 128, MaxIdleConnsPerHost: 64, IdleConnTimeout: 30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, MaxResponseHeaderBytes: 64 * 1024,
	}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return s
}

func defaultIntelligenceTestConfig() *IntelligenceTestConfig {
	base := IntelligenceTestModeConfig{Enabled: true, Model: "gpt-6-astra", Effort: "medium", IntervalMinutes: 5, ExpectedAnswer: 21, GroupOverrides: []IntelligenceTestGroupOverride{}}
	result := &IntelligenceTestConfig{SVG: base, Candy: base}
	result.SVG.Prompt = "使用SVG绘制鹈鹕骑自行车的2D动画；使用SVG/CSS动画，不依赖JavaScript或外部资源；只输出完整SVG或包含它的HTML。"
	result.Candy.Prompt = intelligenceTestCandyPrompt + "\n\n请给出你的解答，并在最后单独一行输出 <final_answer>整数</final_answer>，标签中只填写你求得的最少糖果数目。"
	return result
}

func (s *IntelligenceTestService) GetConfig(ctx context.Context) (*IntelligenceTestConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, intelligenceTestSettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		return defaultIntelligenceTestConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	return decodeIntelligenceTestConfig(raw)
}

func (s *IntelligenceTestService) Enabled(ctx context.Context) (bool, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return false, err
	}
	return cfg.Enabled, nil
}

func validateIntelligenceTestConfig(cfg *IntelligenceTestConfig) error {
	invalid := func(message string) error { return infraerrors.BadRequest("INVALID_INTELLIGENCE_TEST_CONFIG", message) }
	if cfg == nil {
		return invalid("智商测试配置不能为空")
	}
	for _, mode := range []*IntelligenceTestModeConfig{&cfg.SVG, &cfg.Candy} {
		mode.Model = strings.TrimSpace(mode.Model)
		mode.Effort = strings.TrimSpace(mode.Effort)
		if !utf8.ValidString(mode.Prompt) || len(mode.Prompt) > 100000 || strings.TrimSpace(mode.Prompt) == "" {
			return invalid("提示词不能为空，且最多 100,000 字节")
		}
		if strings.IndexByte(mode.Prompt, 0) >= 0 {
			return invalid("提示词不能包含 NUL 字符")
		}
		if !validIntelligenceTestModel(mode.Model) {
			return invalid("模型名称无效")
		}
		if !validIntelligenceTestEffort(mode.Effort) {
			return invalid("推理强度无效")
		}
		if mode.IntervalMinutes < 1 || mode.IntervalMinutes > 1440 {
			return invalid("测试间隔必须为 1 至 1440 分钟")
		}
		if len(mode.GroupOverrides) > 2000 {
			return invalid("分组配置数量过多")
		}
		if mode.GroupOverrides == nil {
			mode.GroupOverrides = []IntelligenceTestGroupOverride{}
		}
		seen := make(map[int64]bool, len(mode.GroupOverrides))
		for i := range mode.GroupOverrides {
			override := &mode.GroupOverrides[i]
			override.Model = strings.TrimSpace(override.Model)
			override.Effort = strings.TrimSpace(override.Effort)
			if override.GroupID <= 0 || seen[override.GroupID] {
				return invalid("分组配置重复或无效")
			}
			seen[override.GroupID] = true
			if override.Model != "" && !validIntelligenceTestModel(override.Model) {
				return invalid("分组模型名称无效")
			}
			if !validIntelligenceTestEffort(override.Effort) {
				return invalid("分组推理强度无效")
			}
		}
	}
	if cfg.Candy.ExpectedAnswer < 0 || cfg.Candy.ExpectedAnswer > 1000000 {
		return invalid("糖果题标准答案必须为 0 至 1000000 的整数")
	}
	return nil
}

func validIntelligenceTestModel(model string) bool {
	return model != "" && len(model) <= 200 && utf8.ValidString(model) && strings.IndexFunc(model, unicode.IsControl) < 0
}

func validIntelligenceTestEffort(effort string) bool {
	switch effort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent":
		return true
	}
	return false
}

func (s *IntelligenceTestService) ListGroups(ctx context.Context) ([]IntelligenceTestGroup, error) {
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	selected := map[int64]bool{}
	configured := map[int64]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT k.group_id,(a.id IS NOT NULL AND u.id IS NOT NULL) FROM pk_intelligence_test_keys k LEFT JOIN api_keys a ON a.id=k.api_key_id AND a.group_id=k.group_id AND a.deleted_at IS NULL AND a.status='active' AND a.key LIKE $1 LEFT JOIN users u ON u.id=a.user_id AND u.status='active' AND u.role='admin' AND u.deleted_at IS NULL`, intelligenceTestKeyPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var keyConfigured bool
		if err := rows.Scan(&id, &keyConfigured); err != nil {
			return nil, err
		}
		selected[id] = true
		configured[id] = keyConfigured
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]IntelligenceTestGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, IntelligenceTestGroup{ID: group.ID, Name: group.Name, Platform: group.Platform, Selected: selected[group.ID], KeyConfigured: configured[group.ID]})
	}
	return result, nil
}

func (s *IntelligenceTestService) ListResults(ctx context.Context, groupID int64, mode string) ([]IntelligenceTestResult, error) {
	if groupID <= 0 || (mode != "svg" && mode != "candy") {
		return nil, infraerrors.BadRequest("INVALID_INTELLIGENCE_TEST_QUERY", "分组或测试类型无效")
	}
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil || !group.IsActive() {
		return nil, ErrGroupNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,group_id,mode,status,request_model,request_effort,passed,(artifact<>''),started_at,finished_at,duration_ms FROM pk_intelligence_test_results WHERE group_id=$1 AND mode=$2 AND status<>'failed' ORDER BY id DESC LIMIT 30`, groupID, mode)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]IntelligenceTestResult, 0, 30)
	for rows.Next() {
		var result IntelligenceTestResult
		if err := rows.Scan(&result.ID, &result.GroupID, &result.Mode, &result.Status, &result.Model, &result.Effort, &result.Passed, &result.HasArtifact, &result.StartedAt, &result.FinishedAt, &result.DurationMS); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (s *IntelligenceTestService) GetArtifact(ctx context.Context, groupID, resultID int64) (string, error) {
	if groupID <= 0 || resultID <= 0 {
		return "", infraerrors.BadRequest("INVALID_INTELLIGENCE_TEST_QUERY", "分组或作品无效")
	}
	var artifact string
	err := s.db.QueryRowContext(ctx, `SELECT r.artifact FROM pk_intelligence_test_results r JOIN groups g ON g.id=r.group_id WHERE r.id=$1 AND r.group_id=$2 AND r.mode='svg' AND r.status='completed' AND r.artifact<>'' AND g.status='active' AND g.deleted_at IS NULL`, resultID, groupID).Scan(&artifact)
	if errors.Is(err, sql.ErrNoRows) {
		return "", infraerrors.NotFound("INTELLIGENCE_TEST_ARTIFACT_NOT_FOUND", "作品不存在或已清理")
	}
	return artifact, err
}

func (s *IntelligenceTestService) Start() {
	if s == nil || s.db == nil || s.settingRepo == nil || s.cfg == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.loop(ctx, s.done)
}

func (s *IntelligenceTestService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
}

// claimed reports whether a durable attempt was created. Failed claims do not
// wake the scheduler themselves and cannot create an immediate retry loop.
func (s *IntelligenceTestService) runOne(parent context.Context, groupID int64, mode string, cfg *IntelligenceTestConfig) (claimed bool) {
	if cfg == nil || (mode != "svg" && mode != "candy") {
		return
	}
	settings := cfg.SVG
	if mode == "candy" {
		settings = cfg.Candy
	}
	for _, override := range settings.GroupOverrides {
		if override.GroupID != groupID {
			continue
		}
		if override.Model != "" {
			settings.Model = override.Model
		}
		if override.Effort != "" {
			settings.Effort = override.Effort
		}
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	queryCtx, queryCancel := context.WithTimeout(ctx, 5*time.Second)
	tx, err := s.db.BeginTx(queryCtx, nil)
	if err != nil {
		queryCancel()
		return
	}
	defer func() { _ = tx.Rollback() }()
	// Different pairs may claim concurrently. The shared gate still excludes a
	// reset/planning transaction, and the pair's conditional UPDATE owns its lease.
	if _, err = tx.ExecContext(queryCtx, `SELECT pg_advisory_xact_lock_shared(706832,1)`); err != nil {
		queryCancel()
		return
	}
	// Reset and claims share this lock. A job planned before any save (including
	// an identical save) cannot insert another old-generation result afterward.
	latest, configErr := intelligenceTestConfigInTransaction(queryCtx, tx)
	if configErr != nil || latest == nil || !sameIntelligenceTestConfig(cfg, latest) || !latest.Enabled ||
		(mode == "svg" && !latest.SVG.Enabled) || (mode == "candy" && !latest.Candy.Enabled) {
		queryCancel()
		return
	}
	if _, err = tx.ExecContext(queryCtx, `SELECT pg_advisory_xact_lock_shared(706832,2)`); err != nil {
		queryCancel()
		return
	}
	var keyID int64
	if err = tx.QueryRowContext(queryCtx, `SELECT api_key_id FROM pk_intelligence_test_keys WHERE group_id=$1`, groupID).Scan(&keyID); err != nil {
		queryCancel()
		return
	}
	result, err := tx.ExecContext(queryCtx, `UPDATE pk_intelligence_test_state s SET next_run_at=NOW()+($3::integer*INTERVAL '1 minute'),lease_until=NOW()+($4::integer*INTERVAL '1 second') WHERE s.group_id=$1 AND s.mode=$2 AND s.next_run_at<=NOW() AND (s.lease_until IS NULL OR s.lease_until<=NOW()) AND EXISTS (SELECT 1 FROM pk_intelligence_test_keys k JOIN groups g ON g.id=k.group_id AND g.status='active' AND g.deleted_at IS NULL WHERE k.group_id=s.group_id)`, groupID, mode, settings.IntervalMinutes, int(intelligenceTestLease.Seconds()))
	if err != nil {
		queryCancel()
		return
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		queryCancel()
		return
	}
	var id int64
	started := time.Now()
	promptSnapshot := ""
	if mode == "candy" {
		promptSnapshot = settings.Prompt
	}
	// Snapshot the same resolved settings later passed to the local Responses
	// request; these are not the gateway's eventual upstream model mappings.
	err = tx.QueryRowContext(queryCtx, `INSERT INTO pk_intelligence_test_results(group_id,mode,status,started_at,prompt_snapshot,request_model,request_effort) VALUES($1,$2,'running',$3,$4,$5,$6) RETURNING id`, groupID, mode, started.UTC(), promptSnapshot, settings.Model, settings.Effort).Scan(&id)
	if err == nil {
		_, err = tx.ExecContext(queryCtx, `UPDATE pk_intelligence_test_state SET active_result_id=$3 WHERE group_id=$1 AND mode=$2`, groupID, mode, id)
	}
	if err == nil {
		_, err = tx.ExecContext(queryCtx, `DELETE FROM pk_intelligence_test_results WHERE group_id=$1 AND mode=$2 AND id NOT IN (SELECT id FROM pk_intelligence_test_results WHERE group_id=$1 AND mode=$2 ORDER BY id DESC LIMIT 30)`, groupID, mode)
	}
	if err == nil {
		err = tx.Commit()
	}
	queryCancel()
	if err != nil {
		return
	}
	claimed = true
	s.mu.Lock()
	s.active[id] = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.active, id); s.mu.Unlock() }()
	finalized := false
	defer func() {
		if !finalized {
			_ = s.abandonIntelligenceTestResult(groupID, mode, id)
		}
	}()
	outcome, attemptErr := s.runIntelligenceTestAttempts(ctx, groupID, mode, id, keyID, cfg, settings)
	if attemptErr != nil || ctx.Err() != nil {
		return claimed
	}
	finalized = true
	finished := time.Now()
	duration := finished.Sub(started).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	// HTTP is finished before releasing this worker's fenced lease. The helper
	// reports only fixed stage/identity metadata if persistence or release fails.
	_ = s.finalizeIntelligenceTestResult(intelligenceTestCompletion{
		id: id, groupID: groupID, mode: mode, status: "completed", passed: outcome.passed,
		artifact: outcome.artifact, answerText: outcome.answerText,
		finishedAt: finished, durationMS: duration,
	})
	return claimed
}

func (s *IntelligenceTestService) intelligenceEndpoint() (string, error) {
	if s.cfg == nil || s.cfg.Server.Port <= 0 || s.cfg.Server.Port > 65535 {
		return "", errors.New("invalid local server port")
	}
	return fmt.Sprintf("http://127.0.0.1:%d/v1/responses", s.cfg.Server.Port), nil
}
