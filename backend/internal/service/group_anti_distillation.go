package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"golang.org/x/sync/singleflight"
)

const (
	groupAntiDistillationSettingPrefix  = "group_anti_distillation:"
	groupAntiDistillationConfigTTL      = 10 * time.Second
	groupAntiDistillationCacheLimit     = 512
	groupAntiDistillationAllowlistLimit = 500
	groupAntiDistillationSaveAttempts   = 3
)

// GroupAntiDistillationConfig belongs to a group, never to an upstream account.
// The zero-cache signal is a protection heuristic, not proof of distillation.
type GroupAntiDistillationConfig struct {
	Enabled          bool    `json:"enabled"`
	PauseMinutes     int     `json:"pause_minutes"`
	UserAllowlistIDs []int64 `json:"user_allowlist_ids"`
}

func DefaultGroupAntiDistillationConfig() GroupAntiDistillationConfig {
	return GroupAntiDistillationConfig{PauseMinutes: 5, UserAllowlistIDs: []int64{}}
}

func cloneGroupAntiDistillationConfig(cfg GroupAntiDistillationConfig) GroupAntiDistillationConfig {
	cfg.UserAllowlistIDs = append([]int64{}, cfg.UserAllowlistIDs...)
	return cfg
}

func (cfg GroupAntiDistillationConfig) allowsUser(userID int64) bool {
	index := sort.Search(len(cfg.UserAllowlistIDs), func(i int) bool { return cfg.UserAllowlistIDs[i] >= userID })
	return index < len(cfg.UserAllowlistIDs) && cfg.UserAllowlistIDs[index] == userID
}

type groupAntiDistillationCachedConfig struct {
	config    GroupAntiDistillationConfig
	expiresAt time.Time
}

// GroupAntiDistillationCache owns atomic per-user evidence and pause state.
// Adapters must deduplicate logical request IDs and never extend active pauses.
type GroupAntiDistillationCache interface {
	PauseRemaining(ctx context.Context, userID int64) (time.Duration, error)
	Observe(ctx context.Context, userID int64, requestID string, zero bool, pause time.Duration, startedAt time.Time) (bool, time.Time, error)
}

// Omitted allowlists require an atomic update so a legacy client cannot restore
// exemptions removed concurrently by another administrator or application node.
type groupAntiDistillationSettingCAS interface {
	CompareAndSwap(ctx context.Context, key string, expected *string, value string) (bool, error)
}

type GroupAntiDistillationService struct {
	settings SettingRepository
	groups   GroupRepository
	cache    GroupAntiDistillationCache
	users    UserRepository
	email    *EmailService

	mu            sync.Mutex
	configs       map[int64]groupAntiDistillationCachedConfig
	configEpoch   uint64
	configLoads   singleflight.Group
	notifications chan struct{}
}

func NewGroupAntiDistillationService(settings SettingRepository, groups GroupRepository, cache GroupAntiDistillationCache, users UserRepository, email *EmailService) *GroupAntiDistillationService {
	return &GroupAntiDistillationService{
		settings: settings, groups: groups, cache: cache, users: users, email: email,
		configs: make(map[int64]groupAntiDistillationCachedConfig), notifications: make(chan struct{}, 8),
	}
}

// WithGroupAntiDistillationRequestStart marks the original request/WS turn start.
// This allows late completions from before a pause to be discarded after expiry.
func WithGroupAntiDistillationRequestStart(ctx context.Context, startedAt time.Time) context.Context {
	return context.WithValue(ctx, groupAntiDistillationRequestStartKey{}, startedAt)
}

type groupAntiDistillationRequestStartKey struct{}

func groupAntiDistillationSettingKey(groupID int64) string {
	return groupAntiDistillationSettingPrefix + strconv.FormatInt(groupID, 10)
}

// GetConfig uses a bounded, short-lived cache independent of API-key auth cache.
// A disabled group never checks a user's shared pause state.
func (s *GroupAntiDistillationService) GetConfig(ctx context.Context, groupID int64) (*GroupAntiDistillationConfig, error) {
	if s == nil || s.settings == nil || s.groups == nil {
		return nil, infraerrors.ServiceUnavailable("GROUP_PROTECTION_UNAVAILABLE", "分组保护暂时不可用")
	}
	if groupID <= 0 {
		return nil, ErrGroupNotFound
	}
	now := time.Now()
	s.mu.Lock()
	if cached, ok := s.configs[groupID]; ok && now.Before(cached.expiresAt) {
		cfg := cloneGroupAntiDistillationConfig(cached.config)
		s.mu.Unlock()
		return &cfg, nil
	}
	s.mu.Unlock()
	// A waiting caller can cancel independently; unrelated groups never wait on
	// each other's SQL while holding the cache lock.
	loaded := s.configLoads.DoChan(strconv.FormatInt(groupID, 10), func() (any, error) {
		s.mu.Lock()
		epoch := s.configEpoch
		if cached, ok := s.configs[groupID]; ok && time.Now().Before(cached.expiresAt) {
			cfg := cloneGroupAntiDistillationConfig(cached.config)
			s.mu.Unlock()
			return cfg, nil
		}
		s.mu.Unlock()
		loadedAt := time.Now()
		cfg, err := s.loadConfig(ctx, groupID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		if epoch == s.configEpoch {
			s.cacheConfigLocked(groupID, cfg, loadedAt)
		}
		s.mu.Unlock()
		return cfg, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-loaded:
		if result.Err != nil {
			return nil, result.Err
		}
		cfg, ok := result.Val.(GroupAntiDistillationConfig)
		if !ok {
			return nil, errors.New("invalid group protection config result")
		}
		cfg = cloneGroupAntiDistillationConfig(cfg)
		return &cfg, nil
	}
}

func (s *GroupAntiDistillationService) loadConfig(ctx context.Context, groupID int64) (GroupAntiDistillationConfig, error) {
	group, err := s.groups.GetByIDLite(ctx, groupID)
	if err != nil {
		return GroupAntiDistillationConfig{}, err
	}
	if group == nil {
		return GroupAntiDistillationConfig{}, ErrGroupNotFound
	}
	cfg := DefaultGroupAntiDistillationConfig()
	if group.Platform == PlatformOpenAI {
		raw, err := s.settings.GetValue(ctx, groupAntiDistillationSettingKey(groupID))
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			return cfg, fmt.Errorf("read group protection config: %w", err)
		}
		if err == nil {
			return parseGroupAntiDistillationConfig(raw)
		}
	}
	return cfg, nil
}

func parseGroupAntiDistillationConfig(raw string) (GroupAntiDistillationConfig, error) {
	cfg := DefaultGroupAntiDistillationConfig()
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return cfg, errors.New("invalid stored group protection config")
		}
		if err := validateGroupAntiDistillationConfig(&cfg); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

func validateGroupAntiDistillationConfig(cfg *GroupAntiDistillationConfig) error {
	if cfg.PauseMinutes < 1 || cfg.PauseMinutes > 1440 {
		return infraerrors.BadRequest("INVALID_GROUP_ANTI_DISTILLATION", "暂停请求时长必须为 1–1440 分钟")
	}
	if len(cfg.UserAllowlistIDs) > groupAntiDistillationAllowlistLimit {
		return infraerrors.BadRequest("INVALID_GROUP_ANTI_DISTILLATION_ALLOWLIST", "用户白名单最多包含 500 项")
	}
	ids := append([]int64{}, cfg.UserAllowlistIDs...)
	for _, id := range ids {
		if id <= 0 {
			return infraerrors.BadRequest("INVALID_GROUP_ANTI_DISTILLATION_ALLOWLIST", "用户白名单必须为有效的正整数用户 ID")
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	unique := ids[:0]
	for _, id := range ids {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	cfg.UserAllowlistIDs = unique
	return nil
}

// SaveConfig updates only this group's independent setting. It does not save the
// rest of the group form or invalidate unrelated users or accounts.
func (s *GroupAntiDistillationService) SaveConfig(ctx context.Context, groupID int64, cfg GroupAntiDistillationConfig) error {
	_, err := s.SaveConfigWithResult(ctx, groupID, cfg)
	return err
}

// SaveConfigWithResult returns this save's normalized value, without a subsequent
// read that could report a concurrent administrator's different save. A nil list
// preserves the stored allowlist for older clients; an explicit empty list clears it.
func (s *GroupAntiDistillationService) SaveConfigWithResult(ctx context.Context, groupID int64, cfg GroupAntiDistillationConfig) (*GroupAntiDistillationConfig, error) {
	allowlistProvided := cfg.UserAllowlistIDs != nil
	if err := validateGroupAntiDistillationConfig(&cfg); err != nil {
		return nil, err
	}
	if s == nil || s.groups == nil || s.settings == nil {
		return nil, infraerrors.ServiceUnavailable("GROUP_PROTECTION_UNAVAILABLE", "分组保护暂时不可用")
	}
	group, err := s.groups.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, ErrGroupNotFound
	}
	if group.Platform != PlatformOpenAI {
		return nil, infraerrors.BadRequest("GROUP_PROTECTION_OPENAI_ONLY", "仅 OpenAI 分组支持此保护")
	}
	if allowlistProvided && len(cfg.UserAllowlistIDs) > 0 {
		if s.users == nil {
			return nil, infraerrors.ServiceUnavailable("GROUP_PROTECTION_UNAVAILABLE", "用户白名单校验暂时不可用")
		}
		// UserRepository has no batch-by-ID reader. This bounded validation runs
		// only on an administrator save, never on gateway admission/observation.
		for _, id := range cfg.UserAllowlistIDs {
			user, err := s.users.GetByID(ctx, id)
			if err != nil && !errors.Is(err, ErrUserNotFound) {
				return nil, fmt.Errorf("validate group protection allowlist: %w", err)
			}
			if err != nil || user == nil || user.ID != id || user.DeletedAt != nil {
				return nil, infraerrors.BadRequest("INVALID_GROUP_ANTI_DISTILLATION_ALLOWLIST", fmt.Sprintf("用户 #%d 不存在或已删除，请从白名单移除后重试", id))
			}
		}
	}
	if !allowlistProvided {
		if err := s.saveConfigPreservingAllowlist(ctx, groupID, &cfg); err != nil {
			return nil, err
		}
	} else {
		raw, err := json.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		if err := s.settings.Set(ctx, groupAntiDistillationSettingKey(groupID), string(raw)); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.configEpoch++
	delete(s.configs, groupID)
	s.mu.Unlock()
	// In-flight reads from before the save may finish, but cannot repopulate the
	// cache. Invalidation instead of setting avoids concurrent-save cache races.
	s.configLoads.Forget(strconv.FormatInt(groupID, 10))
	result := cloneGroupAntiDistillationConfig(cfg)
	return &result, nil
}

func (s *GroupAntiDistillationService) saveConfigPreservingAllowlist(ctx context.Context, groupID int64, cfg *GroupAntiDistillationConfig) error {
	writer, ok := s.settings.(groupAntiDistillationSettingCAS)
	if !ok {
		return infraerrors.ServiceUnavailable("GROUP_PROTECTION_UNAVAILABLE", "分组保护配置暂不支持安全更新，请稍后重试")
	}
	key := groupAntiDistillationSettingKey(groupID)
	for attempt := 0; attempt < groupAntiDistillationSaveAttempts; attempt++ {
		// Read storage directly, not the local TTL cache. Comparing the original
		// text detects any concurrent explicit clear/change before this save.
		raw, err := s.settings.GetValue(ctx, key)
		var expected *string
		stored := DefaultGroupAntiDistillationConfig()
		if err == nil {
			expected = &raw
			stored, err = parseGroupAntiDistillationConfig(raw)
		} else if errors.Is(err, ErrSettingNotFound) {
			err = nil
		}
		if err != nil {
			return fmt.Errorf("read group protection config: %w", err)
		}
		cfg.UserAllowlistIDs = append([]int64{}, stored.UserAllowlistIDs...)
		value, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		updated, err := writer.CompareAndSwap(ctx, key, expected, string(value))
		if err != nil {
			return fmt.Errorf("save group protection config: %w", err)
		}
		if updated {
			return nil
		}
	}
	return infraerrors.Conflict("GROUP_PROTECTION_CONFIG_CHANGED", "分组保护配置已被其他管理员修改，请刷新后重试")
}

func (s *GroupAntiDistillationService) cacheConfigLocked(groupID int64, cfg GroupAntiDistillationConfig, now time.Time) {
	if s.configs == nil {
		s.configs = make(map[int64]groupAntiDistillationCachedConfig)
	}
	if len(s.configs) >= groupAntiDistillationCacheLimit {
		var oldestID int64
		var oldest time.Time
		for id, cached := range s.configs {
			if !now.Before(cached.expiresAt) {
				delete(s.configs, id)
			} else if oldest.IsZero() || cached.expiresAt.Before(oldest) {
				oldestID, oldest = id, cached.expiresAt
			}
		}
		if len(s.configs) >= groupAntiDistillationCacheLimit {
			delete(s.configs, oldestID)
		}
	}
	s.configs[groupID] = groupAntiDistillationCachedConfig{config: cloneGroupAntiDistillationConfig(cfg), expiresAt: now.Add(groupAntiDistillationConfigTTL)}
}

// Check returns the remaining pause for enabled groups only. Errors must be
// treated as fail-open by admission; storage failures never establish a pause.
func (s *GroupAntiDistillationService) Check(ctx context.Context, userID, groupID int64) (time.Duration, error) {
	if s == nil || userID <= 0 || groupID <= 0 {
		return 0, nil
	}
	cfg, err := s.GetConfig(ctx, groupID)
	if err != nil {
		return 0, err
	}
	if !cfg.Enabled || cfg.allowsUser(userID) {
		return 0, nil
	}
	if s.cache == nil {
		return 0, errors.New("group protection cache unavailable")
	}
	return s.cache.PauseRemaining(ctx, userID)
}

// Observe is intentionally separate from billing's lossy usage queue. Callers
// supply only final, successful text-generation results with an explicit valid
// cached-token count. Unknown counts, failures and synthetic defaults are not
// samples. requestID must identify one logical gateway request or WS turn.
func (s *GroupAntiDistillationService) Observe(ctx context.Context, key *APIKey, requestID string, cacheReadTokens int) {
	if s == nil || key == nil || key.UserID <= 0 || key.Group == nil || key.Group.Platform != PlatformOpenAI || cacheReadTokens < 0 || strings.TrimSpace(requestID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cfg, err := s.GetConfig(ctx, key.Group.ID)
	if err != nil {
		slog.Warn("group zero-cache protection config unavailable", "group_id", key.Group.ID)
		return
	}
	if !cfg.Enabled || cfg.allowsUser(key.UserID) || s.cache == nil {
		return
	}
	triggered, until, err := s.observe(ctx, key.UserID, requestID, cacheReadTokens, time.Duration(cfg.PauseMinutes)*time.Minute)
	if err != nil {
		slog.Warn("group zero-cache protection observation unavailable", "group_id", key.Group.ID)
		return
	}
	if triggered {
		slog.Info("group zero-cache protection paused new requests", "user_id", key.UserID, "group_id", key.Group.ID, "pause_minutes", cfg.PauseMinutes)
		s.notify(key.UserID, until)
	}
}

func (s *GroupAntiDistillationService) observe(ctx context.Context, userID int64, requestID string, cacheReadTokens int, pause time.Duration) (bool, time.Time, error) {
	startedAt, _ := ctx.Value(groupAntiDistillationRequestStartKey{}).(time.Time)
	return s.cache.Observe(ctx, userID, requestID, cacheReadTokens == 0, pause, startedAt)
}

func (s *GroupAntiDistillationService) notify(userID int64, until time.Time) {
	if s.email == nil || s.users == nil {
		slog.Warn("group zero-cache protection email unavailable", "user_id", userID)
		return
	}
	// No idle goroutines or unbounded request-triggered work. The pause remains
	// authoritative even when SMTP is unavailable or notification capacity is full.
	select {
	case s.notifications <- struct{}{}:
	default:
		slog.Warn("group zero-cache protection email capacity exceeded", "user_id", userID)
		return
	}
	go func() {
		defer func() { <-s.notifications }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		user, err := s.users.GetByID(ctx, userID)
		if err != nil || user == nil || strings.TrimSpace(user.Email) == "" {
			slog.Warn("group zero-cache protection email recipient unavailable", "user_id", userID)
			return
		}
		body := fmt.Sprintf("<p>您最近的 20 条有效请求中，至少 18 条明确报告缓存读取为 0，已触发分组的零缓存保护。</p><p>新请求暂停至 %s（UTC）；仅影响启用此保护的 OpenAI 分组，已有响应继续完成。可在恢复后重试，或选择未启用此保护的分组。</p><p>零缓存只是保护信号，不代表已认定蒸馏。如有疑问请联系站点管理员。</p>", until.UTC().Format("2006-01-02 15:04:05"))
		if s.email.SendEmail(ctx, user.Email, "请求暂时暂停：分组零缓存保护", body) == nil {
			return
		}
		// An uncertain SMTP acknowledgement must not cause a duplicate email.
		// SMTP errors may include recipient addresses, so do not log raw errors.
		slog.Warn("group zero-cache protection email delivery failed", "user_id", userID)
	}()
}
