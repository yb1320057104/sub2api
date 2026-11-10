package repository

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/upstreamstats"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Real transport -> bounded writer -> existing batched performance reader.
// SQL mocks supply unrelated TPS/probe rows, never HTTP evidence. Both
// platforms and a no-sample account share exactly one reader call and query.
func TestAccountPerformanceUpstreamWriterReader(t *testing.T) {
	original := upstreamstats.Default
	t.Cleanup(func() { upstreamstats.Default = original })
	for _, scenario := range []string{"503_then_200_success", "200_stream_error_excluded"} {
		t.Run(scenario, func(t *testing.T) {
			upstreamstats.Default = upstreamstats.NewStore(100, time.Now().Add(-3*time.Hour))
			for _, platform := range []string{"openai", "anthropic"} {
				path, id := "/v1/responses", int64(7)
				success := "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_fixture\",\"object\":\"response\",\"created_at\":0,\"status\":\"in_progress\",\"model\":\"synthetic-model\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_fixture\",\"output_index\":0,\"content_index\":0,\"delta\":\"OK\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_fixture\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"model\":\"synthetic-model\",\"output\":[{\"id\":\"msg_fixture\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
				if platform == "anthropic" {
					path, id = "/v1/messages", 8
					success = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"synthetic-model\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					require.Equal(t, path, r.URL.Path)
					if scenario == "503_then_200_success" && calls == 1 {
						w.WriteHeader(503)
						_, _ = io.WriteString(w, "synthetic unavailable")
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if scenario == "200_stream_error_excluded" {
						_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"synthetic failure\"}}\n\n")
					} else {
						_, _ = io.WriteString(w, success)
					}
				}))
				t.Cleanup(srv.Close)
				client := NewHTTPUpstream(nil)
				attempts := 2
				if scenario == "200_stream_error_excluded" {
					attempts = 1
				}
				for range attempts {
					req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader("{}"))
					require.NoError(t, err)
					resp, err := client.Do(req, "", id, 1)
					require.NoError(t, err)
					_, err = io.Copy(io.Discard, resp.Body)
					require.NoError(t, err)
					require.NoError(t, resp.Body.Close())
				}
			}
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { mock.ExpectClose(); require.NoError(t, db.Close()) }()
			now := time.Now()
			since := now.Add(-120 * time.Minute)
			mock.ExpectQuery("SELECT value FROM settings").WithArgs("account_performance_window_minutes").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("120"))
			rows := sqlmock.NewRows(performanceColumns)
			for _, id := range []int64{7, 8, 9} {
				rows.AddRow(id, nil, 0, nil, 0, nil, 0, "{}", 0, false, 0, nil, "")
			}
			mock.ExpectQuery("WITH source").WithArgs("{7,8,9}", since, now).WillReturnRows(rows)
			data, err := (&accountPerformanceRepository{db: db}).Read(context.Background(), []int64{7, 8, 9}, now)
			require.NoError(t, err)
			for _, id := range []int64{7, 8} {
				raw, err := json.Marshal(data[id])
				require.NoError(t, err)
				var fields map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(raw, &fields))
				require.Contains(t, fields, "upstream_error")
				var evidence upstreamstats.Evidence
				require.NoError(t, json.Unmarshal(fields["upstream_error"], &evidence))
				require.True(t, evidence.WindowComplete)
				require.Equal(t, since, data[id].WindowStart)
				if scenario == "503_then_200_success" {
					require.Equal(t, int64(2), evidence.Total)
					require.Equal(t, int64(1), evidence.Failures)
					require.NotNil(t, evidence.Percent)
					require.Equal(t, 50.0, *evidence.Percent)
				} else {
					require.Equal(t, int64(1), evidence.Total)
					require.Zero(t, evidence.Failures, "HTTP 200 stream errors are outside this HTTP status metric")
					require.NotNil(t, evidence.Percent)
					require.Zero(t, *evidence.Percent)
				}
			}
			require.Nil(t, data[9].UpstreamError.Percent)
			require.NoError(t, mock.ExpectationsWereMet(), "one query batches all visible IDs; no N+1")
		})
	}
}

func TestAccountPerformanceSharedHTTPWriterAndWorkerReader(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { upstreamstats.ConfigureShared(nil); _ = client.Close() })
	since := time.Now().Add(-120 * time.Minute)
	for _, status := range []int{503, 200} {
		writer := upstreamstats.NewRedis(client)
		upstreamstats.ConfigureShared(writer)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader("{}"))
		require.NoError(t, err)
		response, err := NewHTTPUpstream(nil).Do(req, "", 7, 1)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		server.Close()
		require.NoError(t, upstreamstats.CloseShared(context.Background()), "request replica must flush before exit")
	}
	reader := upstreamstats.NewRedis(client)
	upstreamstats.ConfigureShared(reader)
	t.Cleanup(func() { _ = reader.Close(context.Background()) })
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now()
	since = now.Add(-120 * time.Minute)
	mock.ExpectQuery("SELECT value FROM settings").WithArgs("account_performance_window_minutes").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("120"))
	mock.ExpectQuery("WITH source").WithArgs("{7,9}", since, now).WillReturnRows(sqlmock.NewRows(performanceColumns).
		AddRow(7, nil, 0, nil, 0, nil, 0, "{}", 0, false, 0, nil, "").AddRow(9, nil, 0, nil, 0, nil, 0, "{}", 0, false, 0, nil, ""))
	metrics, err := (&accountPerformanceRepository{db: db}).Read(context.Background(), []int64{7, 9}, now)
	require.NoError(t, err)
	require.EqualValues(t, 2, metrics[7].UpstreamError.Total)
	require.EqualValues(t, 1, metrics[7].UpstreamError.Failures)
	require.Equal(t, "shared_http_responses", metrics[7].UpstreamError.Source)
	require.NotNil(t, metrics[7].UpstreamError.Percent)
	require.Equal(t, 50.0, *metrics[7].UpstreamError.Percent)
	require.Nil(t, metrics[9].UpstreamError.Percent)
	require.NoError(t, mock.ExpectationsWereMet())
}
