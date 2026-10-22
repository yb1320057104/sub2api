package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"html"
	"strings"
	"time"
)

type IntelligenceTestKeyGroupStatus struct {
	GroupID    int64  `json:"group_id"`
	GroupName  string `json:"group_name"`
	Configured bool   `json:"configured"`
	Error      string `json:"error,omitempty"`
}

type IntelligenceTestKeySyncResult struct {
	Created int                              `json:"created"`
	Updated int                              `json:"updated"`
	Deleted int                              `json:"deleted"`
	Failed  int                              `json:"failed"`
	Groups  []IntelligenceTestKeyGroupStatus `json:"groups"`
}

type intelligenceTestManagedKey struct {
	id      int64
	key     string
	deleted sql.NullTime
}

// SyncKeys fully rotates the keys explicitly owned by the feature mapping on
// every call. Human-readable names never make ordinary keys feature-owned.
// Revocation, creation and mapping replacement share one transaction and lock:
// write failures roll everything back, and concurrent clicks rotate serially.
func (s *IntelligenceTestService) SyncKeys(ctx context.Context, adminID int64) (*IntelligenceTestKeySyncResult, error) {
	admin, err := s.userRepo.GetByID(ctx, adminID)
	if err != nil || admin == nil || !admin.IsAdmin() || !admin.IsActive() {
		return nil, ErrInsufficientPerms
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(706832, 2)"); err != nil {
		return nil, err
	}
	groups, err := intelligenceTestSyncGroups(ctx, tx)
	if err != nil {
		return nil, err
	}
	managed, err := intelligenceTestMappedKeys(ctx, tx)
	if err != nil {
		return nil, err
	}
	result := &IntelligenceTestKeySyncResult{Groups: make([]IntelligenceTestKeyGroupStatus, 0, len(groups))}
	var invalidate []string
	var revoked []string
	for _, key := range managed {
		if !key.deleted.Valid && IsIntelligenceTestKey(key.key) {
			var random [32]byte
			if _, err := rand.Read(random[:]); err != nil {
				return nil, errors.New("cannot generate intelligence test deletion marker")
			}
			// A predictable tombstone could collide with a user-chosen ordinary
			// key. This independent random marker contains no old credential.
			tombstone := "__deleted__iq_" + hex.EncodeToString(random[:])
			changed, revokeErr := tx.ExecContext(ctx, `UPDATE api_keys SET key = $1,
				deleted_at = NOW(), updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL`, tombstone, key.id)
			if revokeErr != nil {
				return nil, revokeErr
			}
			n, countErr := changed.RowsAffected()
			if countErr != nil {
				return nil, countErr
			}
			if n != 1 {
				return nil, errors.New("cannot revoke intelligence test key")
			}
			invalidate = append(invalidate, key.key)
			revoked = append(revoked, key.key)
			result.Deleted++
		}
	}
	// Even a stale/corrupt mapping is removed. Only the mapped dedicated keys
	// above are revoked; an ordinary key is never changed based on its name.
	if _, err = tx.ExecContext(ctx, "DELETE FROM pk_intelligence_test_keys"); err != nil {
		return nil, err
	}
	for _, group := range groups {
		state := IntelligenceTestKeyGroupStatus{GroupID: group.ID, GroupName: group.Name}
		ownerActive, canBind, accessErr := intelligenceTestOwnerAccess(ctx, tx, adminID, group)
		if accessErr != nil {
			return nil, accessErr
		}
		if !ownerActive {
			state.Error = "当前管理员账号不可用，请重新登录后生成"
		} else if !canBind {
			if group.IsSubscriptionType() {
				state.Error = "请先为当前管理员开通此分组的有效订阅，再重新生成"
			} else {
				state.Error = "请先为当前管理员授权此分组，再重新生成"
			}
		} else {
			if limitErr := s.intelligenceTestCreateLimit(ctx, tx, adminID); limitErr != nil {
				if !errors.Is(limitErr, ErrAPIKeyCountExceeded) && !errors.Is(limitErr, ErrAPIKeyCreateLimited) {
					return nil, limitErr
				}
				state.Error = intelligenceTestCreateError(limitErr)
			} else {
				created, createErr := intelligenceTestCreateKey(ctx, tx, adminID, group.ID, intelligenceTestKeyName(group.Name))
				if createErr != nil {
					return nil, createErr
				}
				invalidate = append(invalidate, created)
				state.Configured = true
				result.Created++
			}
		}
		if state.Error != "" {
			result.Failed++
		}
		result.Groups = append(result.Groups, state)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	// Revocation preserves the old rows and their accounting history, but never
	// retains their credentials. Cancel old work only after the replacement is
	// committed; rolled-back rotations leave existing scheduled work intact.
	revokeIntelligenceTestCapabilities(revoked)
	s.cancelActiveIntelligenceTests()
	invalidationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, key := range invalidate {
		s.apiKeyService.InvalidateAuthCacheByKey(invalidationCtx, key)
	}
	return result, nil
}

func intelligenceTestSyncGroups(ctx context.Context, tx *sql.Tx) ([]Group, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, name, is_exclusive, subscription_type FROM groups
		WHERE deleted_at IS NULL ORDER BY id FOR SHARE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var groups []Group
	for rows.Next() {
		var group Group
		if err := rows.Scan(&group.ID, &group.Name, &group.IsExclusive, &group.SubscriptionType); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func intelligenceTestMappedKeys(ctx context.Context, tx *sql.Tx) ([]intelligenceTestManagedKey, error) {
	rows, err := tx.QueryContext(ctx, `SELECT k.id, k.key, k.deleted_at
		FROM pk_intelligence_test_keys m JOIN api_keys k ON k.id = m.api_key_id FOR UPDATE OF k`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var keys []intelligenceTestManagedKey
	for rows.Next() {
		var key intelligenceTestManagedKey
		if err := rows.Scan(&key.id, &key.key, &key.deleted); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *IntelligenceTestService) intelligenceTestCreateLimit(ctx context.Context, tx *sql.Tx, adminID int64) error {
	// Preserve normal creation safeguards. The transactional count additionally
	// includes keys created earlier in this same synchronization. All SQL uses
	// tx rather than a second pool connection while this transaction is open.
	if s.apiKeyService.cfg != nil && s.apiKeyService.cfg.APIKeyCreate.MaxActivePerUser > 0 {
		var count int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM api_keys WHERE user_id = $1 AND deleted_at IS NULL", adminID).Scan(&count); err != nil {
			return err
		}
		if count >= int64(s.apiKeyService.cfg.APIKeyCreate.MaxActivePerUser) {
			return ErrAPIKeyCountExceeded
		}
	}
	if s.apiKeyService.cfg != nil && s.apiKeyService.cache != nil {
		if max := s.apiKeyService.cfg.APIKeyCreate.MaxPerUserPerHour; max > 0 {
			count, err := s.apiKeyService.cache.IncrementCreateCount(ctx, adminID, apiKeyCreateCountWindow)
			// Match APIKeyService.checkAPIKeyCreateLimits: Redis failure does not
			// disable creation, and deletion/transaction rollback never refunds it.
			if err == nil && count > int64(max) {
				return ErrAPIKeyCreateLimited
			}
		}
	}
	return nil
}

func intelligenceTestOwnerAccess(ctx context.Context, tx *sql.Tx, ownerID int64, group Group) (bool, bool, error) {
	// Match APIKeyService.canUserBindGroup and its active-subscription lookup,
	// while keeping reads on this transaction's connection. No administrator
	// bypass is added for exclusive groups, restricted public groups or plans.
	user := &User{ID: ownerID}
	var explicitlyAllowed, subscribed bool
	err := tx.QueryRowContext(ctx, `SELECT u.role, u.status, u.restrict_public_groups,
		EXISTS (SELECT 1 FROM user_allowed_groups a WHERE a.user_id = u.id AND a.group_id = $2),
		(SELECT COUNT(*) = 1 FROM user_subscriptions s WHERE s.user_id = u.id AND s.group_id = $2
			AND s.status = 'active' AND s.deleted_at IS NULL AND s.expires_at > $3)
		FROM users u WHERE u.id = $1 AND u.deleted_at IS NULL`, ownerID, group.ID, time.Now()).
		Scan(&user.Role, &user.Status, &user.RestrictPublicGroups, &explicitlyAllowed, &subscribed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if explicitlyAllowed {
		user.AllowedGroups = []int64{group.ID}
	}
	allowed := user.CanBindGroup(group.ID, group.IsExclusive)
	if group.IsSubscriptionType() {
		allowed = subscribed
	}
	return user.IsAdmin() && user.IsActive(), allowed, nil
}

func intelligenceTestCreateError(err error) string {
	switch {
	case errors.Is(err, ErrAPIKeyCountExceeded):
		return "管理员的 API Key 数量已达上限，请清理不用的 Key 后重新同步"
	case errors.Is(err, ErrAPIKeyCreateLimited):
		return "管理员的 API Key 创建频率已达上限，请稍后重新同步"
	default:
		return "无法校验 API Key 创建限制，请稍后重新同步"
	}
}

func intelligenceTestCreateKey(ctx context.Context, tx *sql.Tx, ownerID, groupID int64, name string) (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("cannot generate intelligence test key")
	}
	key := intelligenceTestKeyPrefix + hex.EncodeToString(random[:])
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO api_keys (user_id, key, name, group_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', NOW(), NOW()) RETURNING id`, ownerID, key, name, groupID).Scan(&id); err != nil {
		return "", errors.New("cannot persist intelligence test key")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO pk_intelligence_test_keys (group_id, api_key_id) VALUES ($1, $2)", groupID, id); err != nil {
		return "", errors.New("cannot persist intelligence test key mapping")
	}
	return key, nil
}

func intelligenceTestKeyName(groupName string) string {
	// Keep the existing HTML-escaped storage convention and the 100-character
	// API key name limit, including for long group names containing entities.
	name := []rune(html.UnescapeString(groupName))
	for len(name) > 0 && len([]rune(html.EscapeString("智商测试-"+string(name)))) > 100 {
		name = name[:len(name)-1]
	}
	return html.EscapeString("智商测试-" + string(name))
}

// keyForGroup never returns credentials to a DTO. The live mapping and group
// binding are checked again before each scheduled request, without auth cache.
func (s *IntelligenceTestService) keyForGroup(ctx context.Context, groupID int64) (string, error) {
	var key string
	err := s.db.QueryRowContext(ctx, `SELECT k.key FROM pk_intelligence_test_keys m
		JOIN api_keys k ON k.id = m.api_key_id AND k.group_id = m.group_id AND k.deleted_at IS NULL
		JOIN groups g ON g.id = m.group_id AND g.deleted_at IS NULL AND g.status = 'active'
		JOIN users u ON u.id = k.user_id AND u.deleted_at IS NULL AND u.status = 'active' AND u.role = 'admin'
		WHERE m.group_id = $1`, groupID).Scan(&key)
	if err != nil || !IsIntelligenceTestKey(key) {
		return "", errors.New("分组未配置可用的智商测试 Key，请管理员先同步")
	}
	return strings.TrimSpace(key), nil
}
