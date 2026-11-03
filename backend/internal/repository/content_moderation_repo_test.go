package repository

import (
	"context"
	"database/sql/driver"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestContentModerationRepositoryEngineMetaInsert(t *testing.T) {
	for _, meta := range []*service.ContentModerationEngineMeta{nil, {Engine: "typesafe", Model: "jev-fixed", RulesVersion: "rules-v1", SkippedImages: 1}} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		args := make([]driver.Value, 29)
		for i := range args {
			args[i] = sqlmock.AnyArg()
		}
		if meta == nil {
			args[25] = nil
		} else {
			args[25] = `{"engine":"typesafe","model":"jev-fixed","rules_version":"rules-v1","skipped_images":1}`
		}
		mock.ExpectQuery("INSERT INTO content_moderation_logs").WithArgs(args...).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, time.Now()))
		err = NewContentModerationRepository(db).CreateLog(context.Background(), &service.ContentModerationLog{EngineMeta: meta})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}

func TestContentModerationRepositoryEngineMetaRead(t *testing.T) {
	for _, meta := range []any{nil, `{"engine":"typesafe","model":"jev-fixed","rules_version":"rules-v1","skipped_images":1}`} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		columns := []string{"id", "request_id", "user_id", "user_email", "api_key_id", "api_key_name", "group_id", "group_name", "endpoint", "provider", "model", "mode", "action", "flagged", "highest_category", "highest_score", "category_scores", "threshold_snapshot", "input_excerpt", "upstream_latency_ms", "error", "violation_count", "auto_banned", "email_sent", "status", "queue_delay_ms", "matched_keyword", "created_at", "engine_meta"}
		columns = append(columns, "risk_score", "risk_level", "policy_action")
		mock.ExpectQuery("SELECT[\\s\\S]*l.engine_meta").WillReturnRows(sqlmock.NewRows(columns).AddRow(1, "req", nil, "", nil, "", nil, "", "/v1/responses", "business", "gpt-model", "observe", "allow", false, "sexual", 0.1, `{"sexual":0.1}`, `{"sexual":0.8}`, "sample", 10, "", 0, false, false, "active", nil, "", time.Now(), meta, nil, "", ""))
		logs, _, err := NewContentModerationRepository(db).ListLogs(context.Background(), service.ContentModerationLogFilter{})
		require.NoError(t, err)
		require.Len(t, logs, 1)
		require.Equal(t, "gpt-model", logs[0].Model)
		require.Nil(t, logs[0].RiskScore, "historical records must not become invented zero-score evidence")
		if meta == nil {
			require.Nil(t, logs[0].EngineMeta)
		} else {
			require.Equal(t, "jev-fixed", logs[0].EngineMeta.Model)
			require.Equal(t, 1, logs[0].EngineMeta.SkippedImages)
		}
		require.NoError(t, mock.ExpectationsWereMet())
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}

func TestBuildContentModerationLogWhere_BlockedIncludesAllBlockActions(t *testing.T) {
	where, args := buildContentModerationLogWhere(service.ContentModerationLogFilter{Result: "blocked"})

	require.Empty(t, args)
	sql := strings.Join(where, " AND ")
	require.Contains(t, sql, "l.action IN ('block', 'keyword_block', 'hash_block')")
	require.NotContains(t, sql, "l.action = 'block'")
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesHashBlockAndObservations(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND action <> 'hash_block'\n  AND action <> 'allow'\n  AND mode <> 'observe'")).
		WithArgs(int64(1001), since, false).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	count, err := repo.CountFlaggedByUserSince(context.Background(), 1001, since, false)

	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesCyberPolicyWhenRequested(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND ($3::bool IS FALSE OR action <> 'cyber_policy')")).
		WithArgs(int64(1001), since, true).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	count, err := repo.CountFlaggedByUserSince(context.Background(), 1001, since, true)

	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesCyberLogOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND mode <> 'cyber_log_only'\n  AND mode <> 'risk_control_log_only'")).
		WithArgs(int64(12), since, false).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	count, err := repo.CountFlaggedByUserSince(context.Background(), 12, since, false)
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryRiskLogInsertPreservesUnknownAndZero(t *testing.T) {
	zero, high := 0.0, 73.0
	for _, tc := range []struct {
		name          string
		score         *float64
		level, action string
	}{
		{"legacy", nil, "", ""}, {"zero", &zero, "none", "allow"},
		{"graded_allow", &high, "high", "allow"}, {"graded_block", &high, "high", "block"},
		{"graded_charge", &high, "high", "charge_allow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			args := make([]driver.Value, 29)
			for index := range args {
				args[index] = sqlmock.AnyArg()
			}
			args[12] = tc.action == ""
			args[26] = nil
			if tc.score != nil {
				args[26] = *tc.score
			}
			args[27], args[28] = tc.level, tc.action
			mock.ExpectQuery("INSERT INTO content_moderation_logs[\\s\\S]*risk_score, risk_level, policy_action").WithArgs(args...).
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, time.Now()))
			log := &service.ContentModerationLog{Flagged: true, RiskScore: tc.score, RiskLevel: tc.level, PolicyAction: tc.action}
			err = NewContentModerationRepository(db).CreateLog(context.Background(), log)
			require.NoError(t, err)
			require.True(t, log.Flagged, "storage compatibility must not mutate the current decision or notification evidence")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestContentModerationRepositoryRiskHitReadRestoresAllActions(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		score        any
		stored, hit  bool
	}{
		{"legacy-hit", "", nil, true, true}, {"legacy-pass", "", nil, false, false},
		{"graded-zero", "allow", 0.0, false, false}, {"graded-allow", "allow", 30.0, false, true},
		{"graded-block", "block", 80.0, false, true}, {"graded-charge", "charge_allow", 40.0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			columns := []string{"id", "request_id", "user_id", "user_email", "api_key_id", "api_key_name", "group_id", "group_name", "endpoint", "provider", "model", "mode", "action", "flagged", "highest_category", "highest_score", "category_scores", "threshold_snapshot", "input_excerpt", "upstream_latency_ms", "error", "violation_count", "auto_banned", "email_sent", "status", "queue_delay_ms", "matched_keyword", "created_at", "engine_meta", "risk_score", "risk_level", "policy_action"}
			mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			mock.ExpectQuery("SELECT[\\s\\S]*l.risk_score").WillReturnRows(sqlmock.NewRows(columns).AddRow(
				1, "req", nil, "", nil, "", nil, "", "", "", "", "pre_block", "allow", tc.stored, "", 0,
				`{}`, `{}`, "", nil, "", 0, false, false, "", nil, "", time.Now(), nil, tc.score, "", tc.action))
			logs, _, err := NewContentModerationRepository(db).ListLogs(context.Background(), service.ContentModerationLogFilter{})
			require.NoError(t, err)
			require.Len(t, logs, 1)
			require.Equal(t, tc.hit, logs[0].Flagged)
			require.Equal(t, tc.action, logs[0].PolicyAction)
			if tc.score == nil {
				require.Nil(t, logs[0].RiskScore)
			} else {
				require.NotNil(t, logs[0].RiskScore)
				require.Equal(t, tc.score, *logs[0].RiskScore)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBuildContentModerationLogWhere_RiskHitAndPass(t *testing.T) {
	for _, result := range []string{"hit", "flagged", "pass", "allow"} {
		where, args := buildContentModerationLogWhere(service.ContentModerationLogFilter{Result: result})
		require.Empty(t, args)
		query := strings.Join(where, " AND ")
		require.Contains(t, query, "CASE WHEN l.policy_action IS NOT NULL THEN COALESCE(l.risk_score > 0, FALSE) ELSE l.flagged END")
		if result == "hit" || result == "flagged" {
			require.Contains(t, query, "END) = TRUE")
		} else {
			require.Contains(t, query, "END) = FALSE AND l.error = ''")
		}
	}
}

func TestContentModerationRepositoryRiskCleanupKeepsHitRetention(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	hitBefore, nonHitBefore := time.Now().Add(-30*24*time.Hour), time.Now().Add(-7*24*time.Hour)
	for index, cutoff := range []time.Time{hitBefore, nonHitBefore} {
		flag := "TRUE"
		if index == 1 {
			flag = "FALSE"
		}
		mock.ExpectExec(regexp.QuoteMeta("WHERE (CASE WHEN policy_action IS NOT NULL THEN COALESCE(risk_score > 0, FALSE) ELSE flagged END) = " + flag + " AND created_at < $1")).
			WithArgs(cutoff).WillReturnResult(sqlmock.NewResult(0, int64(index+2)))
	}
	result, err := NewContentModerationRepository(db).CleanupExpiredLogs(context.Background(), hitBefore, nonHitBefore)
	require.NoError(t, err)
	require.EqualValues(t, 2, result.DeletedHit)
	require.EqualValues(t, 3, result.DeletedNonHit)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryRiskLogsExcludedFromLegacyBanCounter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND policy_action IS NULL")).WithArgs(int64(8), since, false).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	count, err := NewContentModerationRepository(db).CountFlaggedByUserSince(context.Background(), 8, since, false)
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, mock.ExpectationsWereMet())
}
