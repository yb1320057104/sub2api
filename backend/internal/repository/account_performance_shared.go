package repository

import (
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/pkg/upstreamstats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// The producer is installed on every runtime role, including API-only replicas.
// Only AccountPerformanceService's evaluator is subject to writer activation.
func ProvideAccountPerformanceRepository(db *sql.DB, client *redis.Client) service.AccountPerformanceRepository {
	if client != nil {
		upstreamstats.ConfigureShared(upstreamstats.NewRedis(client))
	}
	return NewAccountPerformanceRepository(db)
}
