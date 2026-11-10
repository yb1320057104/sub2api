package upstreamstats

import (
	"context"
	"sync/atomic"
	"time"
)

// SharedBackend preserves the existing HTTP-response metric across request
// replicas and the singleton evaluator. Transport calls must never wait for IO.
type SharedBackend interface {
	Record(int64, int, time.Time)
	Snapshot(context.Context, []int64, time.Time, time.Time) (map[int64]Evidence, error)
	Close(context.Context) error
}

type sharedBackend struct{ store SharedBackend }

var shared atomic.Pointer[sharedBackend]

func ConfigureShared(store SharedBackend) {
	if store == nil {
		shared.Store(nil)
		return
	}
	shared.Store(&sharedBackend{store: store})
}

func Record(accountID int64, status int, at time.Time) {
	if backend := shared.Load(); backend != nil {
		backend.store.Record(accountID, status, at)
		return
	}
	Default.Record(accountID, status, at)
}

func Snapshot(ctx context.Context, ids []int64, since, now time.Time) map[int64]Evidence {
	if backend := shared.Load(); backend != nil {
		result, err := backend.store.Snapshot(ctx, ids, since, now)
		if err == nil {
			return result
		}
		// A Redis outage is unknown. Never silently use this worker's local
		// requests or fabricate a zero-percent value for the whole cluster.
		result = make(map[int64]Evidence, len(ids))
		for _, id := range ids {
			result[id] = Evidence{Source: "shared_http_responses_unavailable"}
		}
		return result
	}
	return Default.Snapshot(ids, since, now)
}

// CloseShared is called after HTTP/probe drains and before infrastructure closes.
func CloseShared(ctx context.Context) error {
	if backend := shared.Load(); backend != nil {
		return backend.store.Close(ctx)
	}
	return nil
}
