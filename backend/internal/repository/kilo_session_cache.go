package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var _ service.KiloGatewaySessionStore = (*gatewayCache)(nil)

// Response IDs are opaque provider data; hash them so control characters and
// unbounded values cannot become Redis key material or cross an identity scope.
func kiloGatewayWireSessionKey(accountID, apiKeyID int64, responseID string) (string, error) {
	if accountID <= 0 || apiKeyID <= 0 || strings.TrimSpace(responseID) == "" || len(responseID) > 4096 {
		return "", errors.New("invalid Kilo response session identity")
	}
	sum := sha256.Sum256([]byte(responseID))
	return fmt.Sprintf("kilo:wire-session:%d:%d:%s", accountID, apiKeyID, hex.EncodeToString(sum[:])), nil
}
func (c *gatewayCache) GetKiloGatewayResponseSession(ctx context.Context, accountID, apiKeyID int64, responseID string) (string, error) {
	key, err := kiloGatewayWireSessionKey(accountID, apiKeyID, responseID)
	if err != nil {
		return "", err
	}
	result, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return result, err
}
func (c *gatewayCache) SetKiloGatewayResponseSession(ctx context.Context, accountID, apiKeyID int64, responseID, sessionID string, ttl time.Duration) error {
	key, err := kiloGatewayWireSessionKey(accountID, apiKeyID, responseID)
	if err != nil {
		return err
	}
	if sessionID == "" || len(sessionID) > 256 || ttl <= 0 || ttl > 24*time.Hour {
		return errors.New("invalid Kilo wire session binding")
	}
	for _, r := range sessionID {
		if r < 33 || r > 126 {
			return errors.New("invalid Kilo wire session binding")
		}
	}
	// A response's wire session is immutable. Repeated completion may refresh
	// the same binding but must never overwrite another concurrent identity.
	result, err := kiloWireSessionBind.Run(ctx, c.rdb, []string{key}, sessionID, ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return errors.New("Kilo response session binding conflict")
	}
	return nil
}

var kiloWireSessionBind = redis.NewScript(`
local current = redis.call('GET',KEYS[1])
if current and current ~= ARGV[1] then return 0 end
redis.call('PSETEX',KEYS[1],ARGV[2],ARGV[1])
return 1
`)
