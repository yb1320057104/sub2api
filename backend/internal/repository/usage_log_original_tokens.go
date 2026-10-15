package repository

import (
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

func originalUsageJSON(usage *usagestats.OriginalTokenUsage) any {
	if usage == nil {
		return nil
	}
	// The snapshot contains only int64 counters, so serialization cannot fail.
	data, _ := json.Marshal(usage)
	return string(data)
}

// originalUsageSumSQL aggregates only recorded upstream snapshots. Missing rows
// are counted separately; converted billing tokens must never fill those gaps.
// column is a fixed internal SQL identifier, not a client-supplied expression.
func originalUsageSumSQL(column string) string {
	return fmt.Sprintf(`jsonb_build_object(
		'input_tokens', COALESCE(SUM((%[1]s->>'input_tokens')::bigint), 0),
		'output_tokens', COALESCE(SUM((%[1]s->>'output_tokens')::bigint), 0),
		'cache_creation_tokens', COALESCE(SUM((%[1]s->>'cache_creation_tokens')::bigint), 0),
		'cache_read_tokens', COALESCE(SUM((%[1]s->>'cache_read_tokens')::bigint), 0)
	)`, column)
}

func parseOriginalUsageJSON(data string) (*usagestats.OriginalTokenUsage, error) {
	var usage usagestats.OriginalTokenUsage
	if err := json.Unmarshal([]byte(data), &usage); err != nil {
		return nil, fmt.Errorf("decode original usage snapshot: %w", err)
	}
	return &usage, nil
}
