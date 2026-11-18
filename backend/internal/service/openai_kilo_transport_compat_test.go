package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type kiloCloseEvidenceBody struct {
	io.Reader
	closed *bool
}

func (b *kiloCloseEvidenceBody) Close() error { *b.closed = true; return nil }

func TestKiloCompatibilityRetriesOnlyExplicitSameKeyRejection(t *testing.T) {
	_, account, _ := kiloPolicyTestAccounts()
	body := `{"model":"gpt-test","input":"hi","tools":[{"type":"image_generation"},{"type":"function","name":"keep","parameters":{"type":"object"}}]}`
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, KiloGatewayBaseURL+"/responses", strings.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+account.GetCredential("api_key"))
	firstClosed := false
	calls := 0
	response, err := doKiloCompatibleUpstream(request, account, true, nil, func(sent *http.Request) (*http.Response, error) {
		calls++
		raw, readErr := io.ReadAll(sent.Body)
		require.NoError(t, readErr)
		require.Equal(t, request.Header.Get("Authorization"), sent.Header.Get("Authorization"))
		require.Equal(t, request.URL.String(), sent.URL.String())
		require.True(t, gjson.GetBytes(raw, "stream").Bool())
		if calls == 1 {
			return &http.Response{StatusCode: 400, Header: make(http.Header), Body: &kiloCloseEvidenceBody{Reader: strings.NewReader(`{"error":{"code":"subscription_sharing_unsupported_capability","param":"tools"}}`), closed: &firstClosed}}, nil
		}
		require.True(t, firstClosed, "close and release the refused attempt before retry")
		require.Equal(t, "keep", gjson.GetBytes(raw, "tools.0.name").String())
		require.Len(t, gjson.GetBytes(raw, "tools").Array(), 1)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3}}}\n\n"))}, nil
	})
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 2, calls)
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"input_tokens":3`)
	for _, status := range []int{401, 402, 429, 502} {
		count := 0
		response, err := doKiloCompatibleUpstream(request, account, true, nil, func(*http.Request) (*http.Response, error) {
			count++
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"token_revoked"}}`))}, nil
		})
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, 1, count, "account failures never generate another POST")
	}
	calls = 0
	_, err = doKiloCompatibleUpstream(request, account, true, nil, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("synthetic disconnect after POST")
	})
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestKiloCompatibilitySchemaBeforeSearchRestoration(t *testing.T) {
	_, account, _ := kiloPolicyTestAccounts()
	original := `{"model":"gpt-test","input":"hi","tools":[{"type":"tool_search","execution":"client","parameters":{"type":"object"}}],"text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"}}}}`
	for _, stream := range []bool{false, true} {
		request, err := http.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", strings.NewReader(original))
		require.NoError(t, err)
		count := 0
		response, err := doKiloCompatibleUpstream(request, account, true, nil, func(*http.Request) (*http.Response, error) {
			count++
			if count == 1 {
				return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_json_schema","param":"text.format.schema"}}`))}, nil
			}
			raw := `{"status":"completed","output":[{"type":"function_call","name":"__kilo2api_tool_search","call_id":"call-search","arguments":"{\"query\":\"tools\"}"}],"usage":{"input_tokens":5}}`
			contentType := "application/json"
			if stream {
				contentType = "text/event-stream"
				raw = "data: {\"type\":\"response.completed\",\"response\":" + raw + "}\n\n"
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(raw))}, nil
		})
		require.NoError(t, err)
		require.Equal(t, 2, count)
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		require.NoError(t, err)
		require.Contains(t, string(raw), `"tool_search_call"`)
		require.Contains(t, string(raw), `"input_tokens":5`)
	}
}

func TestKiloCompatibilityBuffersSchemaAndReportsStreamAccountError(t *testing.T) {
	_, account, _ := kiloPolicyTestAccounts()
	body := `{"model":"gpt-test","input":"hi","text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object","properties":{"ok":{"const":true}},"required":["ok"],"additionalProperties":false}}}}`
	request, err := http.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", strings.NewReader(body))
	require.NoError(t, err)
	calls := 0
	response, err := doKiloCompatibleUpstream(request, account, true, nil, func(sent *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_json_schema","param":"text.format.schema"}}`))}, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"{\\\"ok\\\":true}\"}]}],\"usage\":{\"input_tokens\":4}}}\n\n"))}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"json_schema"`)
	require.Contains(t, string(raw), `"input_tokens":4`)
	observed := 0
	request, err = http.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", strings.NewReader(`{"model":"gpt-test","input":"hi"}`))
	require.NoError(t, err)
	response, err = doKiloCompatibleUpstream(request, account, false, func(status int, _ http.Header, raw []byte) {
		observed++
		require.Equal(t, 402, status)
		require.Contains(t, string(raw), "balance")
	}, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"status\":402,\"message\":\"balance exhausted\"}}}\n\n"))}, nil
	})
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, 1, observed)
}
