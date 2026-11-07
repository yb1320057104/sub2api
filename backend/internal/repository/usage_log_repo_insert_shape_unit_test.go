//go:build unit

package repository

import (
	"context"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

var (
	usageLogStaticInsertShapeRe = regexp.MustCompile(`(?s)INSERT INTO usage_logs \((.*?)\) VALUES \((.*?)\)`)
	usageLogPlaceholderRe       = regexp.MustCompile(`\$(\d+)`)
)

func TestAuditSettlementImportedEventInsertPaths(t *testing.T) {
	for _, requestType := range []service.RequestType{service.RequestTypeCyberBlocked, service.RequestTypeStream} {
		log := &service.UsageLog{UserID: 1, APIKeyID: 2, AccountID: 3, RequestID: "audit-import", Model: "gpt-5", RequestType: requestType,
			AuditSettlementImportedEvent: true, CreatedAt: time.Now().UTC()}
		prepared := prepareUsageLogInsert(log)
		require.Len(t, prepared.args, len(usageLogInsertArgTypes))
		require.Equal(t, "boolean", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-1])
		require.Equal(t, requestType == service.RequestTypeCyberBlocked, prepared.args[len(prepared.args)-1])
		key := usageLogBatchKey(log.RequestID, log.APIKeyID)
		batch, batchArgs := buildUsageLogBatchInsertQuery([]string{key}, map[string]usageLogInsertPrepared{key: prepared})
		best, bestArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
		for _, query := range []string{batch, best} {
			require.Equal(t, 3, strings.Count(query, "audit_settlement_imported_event"))
		}
		require.Equal(t, prepared.args[len(prepared.args)-1], batchArgs[len(batchArgs)-1])
		require.Equal(t, prepared.args[len(prepared.args)-1], bestArgs[len(bestArgs)-1])
	}
}

// newSQLCapturingMock 返回把实际下发 SQL 记录到 captured 的 sqlmock；语句一律视为匹配，
// 参数仍由 WithArgs 校验。
func newSQLCapturingMock(t *testing.T, captured *[]string) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	matcher := sqlmock.QueryMatcherFunc(func(_, actualSQL string) error {
		*captured = append(*captured, actualSQL)
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// requireStaticInsertMatchesArgTypes 断言手写的 INSERT：列清单长度与 VALUES 占位符数量
// 都等于 usageLogInsertArgTypes，且占位符恰为 $1..$N 各出现一次。
func requireStaticInsertMatchesArgTypes(t *testing.T, query string) {
	t.Helper()
	m := usageLogStaticInsertShapeRe.FindStringSubmatch(query)
	require.Len(t, m, 3, "unrecognised INSERT shape:\n%s", query)

	want := len(usageLogInsertArgTypes)
	columns := 0
	for _, col := range strings.Split(m[1], ",") {
		if strings.TrimSpace(col) != "" {
			columns++
		}
	}
	require.Equal(t, want, columns, "INSERT column list must match usageLogInsertArgTypes")

	seen := make(map[int]struct{}, want)
	for _, ph := range usageLogPlaceholderRe.FindAllStringSubmatch(m[2], -1) {
		n, err := strconv.Atoi(ph[1])
		require.NoError(t, err)
		_, dup := seen[n]
		require.False(t, dup, "duplicate placeholder $%d", n)
		seen[n] = struct{}{}
	}
	require.Len(t, seen, want, "VALUES placeholder count must match usageLogInsertArgTypes")
	for i := 1; i <= want; i++ {
		_, ok := seen[i]
		require.True(t, ok, "missing placeholder $%d", i)
	}
}

// TestUsageLogStaticInsertShape_PlaceholdersMatchArgTypes 覆盖两条不经占位符生成器、
// 直接手写 $1..$N 的 INSERT 路径，防止加列后漏补占位符只在集成测试才暴露。
func TestUsageLogStaticInsertShape_PlaceholdersMatchArgTypes(t *testing.T) {
	upstreamRequestID := "20260902080329-oneapi"
	log := &service.UsageLog{
		UserID:            1,
		APIKeyID:          2,
		AccountID:         3,
		RequestID:         "client:insert-shape",
		UpstreamRequestID: &upstreamRequestID,
		Model:             "claude-3",
		InputTokens:       10,
		CreatedAt:         time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}
	prepared := prepareUsageLogInsert(log)
	args := anySliceToDriverValues(prepared.args)

	t.Run("createSingle", func(t *testing.T) {
		var captured []string
		db, mock := newSQLCapturingMock(t, &captured)
		repo := &usageLogRepository{sql: db}

		mock.ExpectQuery("INSERT INTO usage_logs").
			WithArgs(args...).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(1), log.CreatedAt))

		inserted, err := repo.Create(context.Background(), log)
		require.NoError(t, err)
		require.True(t, inserted)
		require.NoError(t, mock.ExpectationsWereMet())
		require.Len(t, captured, 1)
		requireStaticInsertMatchesArgTypes(t, captured[0])
	})

	t.Run("execUsageLogInsertNoResult", func(t *testing.T) {
		var captured []string
		db, mock := newSQLCapturingMock(t, &captured)

		mock.ExpectExec("INSERT INTO usage_logs").
			WithArgs(args...).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, execUsageLogInsertNoResult(context.Background(), db, prepared))
		require.NoError(t, mock.ExpectationsWereMet())
		require.Len(t, captured, 1)
		requireStaticInsertMatchesArgTypes(t, captured[0])
	})
}

// TestPrepareUsageLogInsert_UpstreamRequestIDArgWiring 把 upstream_request_id 钉在
// session_id 之前，与参数类型表保持同位；缺失时落 NULL 而不是空串。
func TestPrepareUsageLogInsert_UpstreamRequestIDArgWiring(t *testing.T) {
	upstreamRequestID := "req_upstream_123"
	prepared := prepareUsageLogInsert(&service.UsageLog{
		UserID:            1,
		APIKeyID:          2,
		RequestID:         "client:wiring",
		Model:             "gpt-5",
		UpstreamRequestID: &upstreamRequestID,
		CreatedAt:         time.Now().UTC(),
	})
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))

	idx := len(prepared.args) - 6
	arg, ok := prepared.args[idx].(sql.NullString)
	require.True(t, ok, "upstream_request_id arg should be sql.NullString, got %T", prepared.args[idx])
	require.True(t, arg.Valid)
	require.Equal(t, upstreamRequestID, arg.String)
	require.Equal(t, "text", usageLogInsertArgTypes[idx])

	absent := prepareUsageLogInsert(&service.UsageLog{UserID: 1, APIKeyID: 2, RequestID: "client:absent", Model: "gpt-5", CreatedAt: time.Now().UTC()})
	nullArg, ok := absent.args[idx].(sql.NullString)
	require.True(t, ok)
	require.False(t, nullArg.Valid, "absent upstream request id must be NULL")

	require.Contains(t, usageLogSelectColumns, "upstream_request_id")
}

func TestPKUsageFirstResponseInsertPaths(t *testing.T) {
	firstToken, firstResponse := 9876, 17
	log := &service.UsageLog{
		UserID: 1, APIKeyID: 2, AccountID: 3, RequestID: "pk-first-response", Model: "gpt-5",
		FirstTokenMs: &firstToken, FirstResponseMs: &firstResponse,
		InputTokens: 100, OutputTokens: 20, CacheReadTokens: 80, CacheCreationTokens: 5,
		TotalCost: 0.25, ActualCost: 0.5, CreatedAt: time.Now().UTC(),
	}
	prepared := prepareUsageLogInsert(log)
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))
	require.Equal(t, "integer", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-2])
	require.Equal(t, sql.NullInt64{Int64: 17, Valid: true}, prepared.args[len(prepared.args)-2])
	require.Equal(t, sql.NullInt64{Int64: 9876, Valid: true}, prepared.args[34], "upstream TTFT keeps its existing position")
	require.Equal(t, 100, prepared.args[11])
	require.Equal(t, 20, prepared.args[12])
	require.Equal(t, 5, prepared.args[13])
	require.Equal(t, 80, prepared.args[14])
	require.Equal(t, 0.25, prepared.args[25])
	require.Equal(t, 0.5, prepared.args[26])
	key := usageLogBatchKey(log.RequestID, log.APIKeyID)
	batch, batchArgs := buildUsageLogBatchInsertQuery([]string{key}, map[string]usageLogInsertPrepared{key: prepared})
	bestEffort, bestEffortArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	for _, query := range []string{batch, bestEffort} {
		require.Equal(t, 3, strings.Count(query, "first_response_ms"), "input CTE, INSERT and SELECT must agree")
	}
	require.Equal(t, prepared.args[len(prepared.args)-2], batchArgs[len(batchArgs)-2])
	require.Equal(t, prepared.args[len(prepared.args)-2], bestEffortArgs[len(bestEffortArgs)-2])
	for _, response := range []*int{nil, new(int)} {
		log.FirstResponseMs = response
		args := prepareUsageLogInsert(log).args
		value := args[len(args)-2].(sql.NullInt64)
		require.Equal(t, response != nil, value.Valid, "unrecorded and measured zero must remain distinct")
	}
}
