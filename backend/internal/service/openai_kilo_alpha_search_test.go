package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func kiloAlphaSearchTestAccount() *Account {
	return &Account{
		ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 3,
		Credentials: map[string]any{"api_key": "synthetic-kilo-search-key", "base_url": KiloGatewayBaseURL},
		Extra:       map[string]any{KiloManagedExtraKey: true, AccountExtraKiloBridgeEnabled: true, KiloGatewayProfileExtra: KiloGatewayCLIProfile},
	}
}

func kiloAlphaSearchTestContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search?PRIVATE=inbound", strings.NewReader(body))
	c.Request.Header.Set("Authorization", "Bearer PRIVATE-local-client-key")
	c.Request.Header.Set("Cookie", "PRIVATE-client-cookie")
	c.Request.Header.Set("X-Codex-Turn-Metadata", "PRIVATE-client-turn")
	return c, recorder
}

func TestKiloAlphaSearchProjectionAndFilters(t *testing.T) {
	now := time.Date(2026, 11, 14, 18, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	body := []byte(`{"id":"PRIVATE-session","model":"gpt-test","input":[{"text":"PRIVATE history"}],"reasoning":{"effort":"PRIVATE"},"commands":{"search_query":[{"q":"example","domains":["EXAMPLE.com.","example.com","子域.example.com"],"recency":7},{"q":"second"}],"response_length":"long"},"settings":{"allowed_callers":["direct"],"external_web_access":true},"max_output_tokens":500}`)
	plan, err := prepareKiloAlphaSearch(body, now)
	require.NoError(t, err)
	require.Equal(t, "gpt-test", plan.model)
	require.Equal(t, 2000, plan.maxCharacters)
	require.Len(t, plan.payloads, 2)
	payload, err := json.Marshal(plan.payloads[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"query":"example","type":"auto","numResults":10,"contents":{"highlights":true},"includeDomains":["example.com","xn--cjsx3f.example.com"],"startPublishedDate":"2026-11-07T10:30:00.000Z"}`, string(payload))
	require.NotContains(t, string(payload), "PRIVATE")
	require.Empty(t, plan.payloads[1].IncludeDomains)
	require.Empty(t, plan.payloads[1].StartPublishedDate)
	count, err := ValidateKiloAlphaSearchBody(body)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestKiloAlphaSearchOptionalFieldsAndBudgets(t *testing.T) {
	for _, length := range []struct {
		name       string
		count, cap int
	}{{"short", 3, 8000}, {"medium", 5, 16000}, {"long", 10, 32000}} {
		t.Run(length.name, func(t *testing.T) {
			for _, tokens := range []int64{1, 20, 2500, 9223372036854775807} {
				body := fmt.Sprintf(`{"commands":{"search_query":[{"q":"示例","domains":[],"recency":null}],"response_length":%q,"open":[],"find":null},"settings":null,"max_output_tokens":%d}`, length.name, tokens)
				plan, err := prepareKiloAlphaSearch([]byte(body), time.Now())
				require.NoError(t, err)
				require.Equal(t, length.count, plan.payloads[0].NumResults)
				want := length.cap
				if tokens < int64(length.cap/4) {
					want = int(tokens) * 4
				}
				require.Equal(t, want, plan.maxCharacters)
			}
		})
	}
	plan, err := prepareKiloAlphaSearch([]byte(`{"commands":{"search_query":[{"q":"x"}],"response_length":null}}`), time.Now())
	require.NoError(t, err)
	require.Equal(t, 8000, plan.maxCharacters)
	require.Empty(t, plan.model)
}

func TestKiloAlphaSearchRejectsUnknownUnsafeAndInvalidArguments(t *testing.T) {
	bodies := []string{
		`[]`, `null`, `{}`, `{"commands":[]}`, `{"commands":{"search_query":[]}}`,
		`{"commands":{"search_query":[{"q":"x"},{"q":"x"},{"q":"x"},{"q":"x"},{"q":"x"}]}}`,
		`{"commands":{"search_query":[{"q":""}]}}`, `{"commands":{"search_query":[{"q":"  "}]}}`,
		`{"commands":{"search_query":[{"q":null}]}}`, `{"commands":{"search_query":[{"q":true}]}}`,
		`{"commands":{"search_query":[{"q":"x","unknown":"PRIVATE"}]}}`,
		`{"commands":{"search_query":[{"q":"x"}],"unknown":null}}`,
		`{"commands":{"search_query":[{"q":"x"}],"open":[{"ref_id":"PRIVATE"}]}}`,
		`{"commands":{"search_query":[{"q":"x"}],"find":[{"pattern":"PRIVATE"}]}}`,
		`{"commands":{"search_query":[{"q":"x"}],"response_length":"PRIVATE"}}`,
		`{"commands":{"search_query":[{"q":"x"}]},"settings":{"external_web_access":false}}`,
		`{"commands":{"search_query":[{"q":"x"}]},"settings":{"external_web_access":null}}`,
		`{"commands":{"search_query":[{"q":"x"}]},"settings":{"unknown":"PRIVATE"}}`,
		`{"commands":{"search_query":[{"q":"x"}]},"settings":[]}`,
		`{"commands":{"search_query":[{"q":"x"}]},"settings":{"allowed_callers":"PRIVATE"}}`,
		`{"commands":{"search_query":[{"q":"x"}]},"max_output_tokens":true}`,
		`{"commands":{"search_query":[{"q":"x"}]},"max_output_tokens":null}`,
		`{"commands":{"search_query":[{"q":"x"}]},"max_output_tokens":0}`,
		`{"commands":{"search_query":[{"q":"x"}]},"max_output_tokens":1.5}`,
		`{"commands":{"search_query":[{"q":"x"}]},"max_output_tokens":1e6}`,
		`{"commands":{"search_query":[{"q":"x"}]},"PRIVATE-secret":"PRIVATE-value"}`,
		`{"commands":{"search_query":[{"q":"x"}]},"prompt_cache_key":"PRIVATE"}`,
		`{"commands":{"search_query":[{"q":"x"}]},"model":null}`,
		`{"commands":{"search_query":[{"q":"x"}]},"commands":{"search_query":[{"q":"PRIVATE"}]}}`,
		`{"commands":{"search_query":[{"q":"x","q":"PRIVATE"}]}}`,
		`{"commands":{"search_query":[{"q":"x"}]}} {"PRIVATE":true}`,
		`{"commands":{"search_query":[{"q":"` + strings.Repeat("中", 16385) + `"}]}}`,
	}
	for index, body := range bodies {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			count, err := ValidateKiloAlphaSearchBody([]byte(body))
			require.Error(t, err)
			require.Equal(t, 0, count)
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.NotContains(t, err.Error(), "PRIVATE")
		})
	}
	for index, fields := range []string{
		`"domains":"example.com"`, `"domains":[null]`, `"domains":[""]`, `"domains":["https://example.com"]`,
		`"domains":["example.com/path"]`, `"domains":["*.example.com"]`, `"domains":["example.com:443"]`,
		`"domains":["bad domain.com"]`, `"domains":["example.com.."]`, `"domains":["-bad.example.com"]`,
		`"domains":["` + strings.Repeat("a", 64) + `.com"]`, `"domains":[` + strings.Repeat(`"example.com",`, 100) + `"example.com"]`,
		`"recency":true`, `"recency":0`, `"recency":"7"`, `"recency":1.5`, `"recency":36501`, `"recency":-1`,
	} {
		t.Run("filter_"+strconv.Itoa(index), func(t *testing.T) {
			_, err := ValidateKiloAlphaSearchBody([]byte(`{"commands":{"search_query":[{"q":"x",` + fields + `}]}}`))
			require.Error(t, err)
		})
	}
}

func TestKiloAlphaSearchFixedRequestAndCredentialBoundary(t *testing.T) {
	account := kiloAlphaSearchTestAccount()
	plan, err := prepareKiloAlphaSearch([]byte(`{"commands":{"search_query":[{"q":"example"}]}}`), time.Now())
	require.NoError(t, err)
	request, err := buildKiloAlphaSearchRequest(context.Background(), account, plan.payloads[0])
	require.NoError(t, err)
	require.Equal(t, kiloAlphaSearchURL, request.URL.String())
	require.Equal(t, "Bearer synthetic-kilo-search-key", request.Header.Get("Authorization"))
	require.Equal(t, "identity", request.Header.Get("Accept-Encoding"))
	require.NoError(t, ValidateKiloAlphaSearchRequest(request, account))
	for _, endpoint := range []string{
		"https://api.openai.com/v1/responses", "https://chatgpt.com/backend-api/codex/alpha/search",
		"http://api.kilo.ai/api/exa/search", "https://api.kilo.ai.evil.invalid/api/exa/search",
		kiloAlphaSearchURL + "?PRIVATE=value", kiloAlphaSearchURL + "/", "https://api.kilo.ai:443/api/exa/search",
	} {
		tampered, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{}`))
		require.NoError(t, err)
		tampered.Header = request.Header.Clone()
		require.Error(t, ValidateKiloAlphaSearchRequest(tampered, account))
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer PRIVATE-source-oauth") },
		func(r *http.Request) { r.Header["authorization"] = []string{"Bearer PRIVATE-source-oauth"} },
		func(r *http.Request) { r.Header.Set("Cookie", "PRIVATE") },
		func(r *http.Request) { r.Host = "api.openai.com" },
		func(r *http.Request) { r.Method = http.MethodGet },
		func(r *http.Request) { r.GetBody = nil },
		func(r *http.Request) {
			r.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(`{"query":"x","type":"auto","numResults":3,"contents":{"highlights":true},"input":"PRIVATE"}`)), nil
			}
		},
	} {
		r := request.Clone(context.Background())
		mutate(r)
		err := ValidateKiloAlphaSearchRequest(r, account)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "PRIVATE")
	}
	for _, mutate := range []func(*Account){
		func(a *Account) { a.Type = AccountTypeOAuth },
		func(a *Account) { a.Extra = map[string]any{"kilo_quota_only": true} },
		func(a *Account) { a.Credentials["access_token"] = a.GetCredential("api_key") },
		func(a *Account) { a.Credentials["base_url"] = "https://api.openai.com/v1" },
		func(a *Account) { a.Credentials["api_key"] = "" },
	} {
		a := kiloAlphaSearchTestAccount()
		mutate(a)
		_, err := buildKiloAlphaSearchRequest(context.Background(), a, plan.payloads[0])
		require.Error(t, err)
	}
}

type kiloAlphaSearchTestBody struct {
	io.Reader
	closed int
}

func (b *kiloAlphaSearchTestBody) Close() error { b.closed++; return nil }

func TestKiloAlphaSearchSenderBatchNoHistoryAndRealCallCount(t *testing.T) {
	account := kiloAlphaSearchTestAccount()
	proxyID := int64(9)
	account.ProxyID = &proxyID
	account.Proxy = &Proxy{ID: 9, Protocol: "socks5", Host: "synthetic.invalid", Port: 1080, Username: "synthetic", Password: "PRIVATE-proxy-password"}
	body := `{"model":"gpt-test","input":["PRIVATE history"],"commands":{"search_query":[{"q":"one"},{"q":"two"}]}}`
	c, recorder := kiloAlphaSearchTestContext(body)
	var closes []*kiloAlphaSearchTestBody
	calls := 0
	var batchDeadline time.Time
	send := func(request *http.Request, proxy string, selected *Account) (*http.Response, error) {
		calls++
		require.Same(t, account, selected)
		require.Equal(t, 3, selected.Concurrency)
		require.Equal(t, account.Proxy.URL(), proxy)
		require.NoError(t, ValidateKiloAlphaSearchRequest(request, selected))
		deadline, ok := request.Context().Deadline()
		require.True(t, ok)
		if calls == 1 {
			batchDeadline = deadline
		} else {
			require.Equal(t, batchDeadline, deadline)
		}
		require.LessOrEqual(t, time.Until(deadline), kiloAlphaSearchTimeout)
		raw, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "PRIVATE")
		require.Empty(t, request.Header.Get("Cookie"))
		require.Empty(t, request.Header.Get("X-Codex-Turn-Metadata"))
		require.Empty(t, request.Header.Get("Session_ID"))
		require.Empty(t, request.Header.Get("OpenAI-Beta"))
		reply := `{"results":[{"url":"https://example.com/full?x=1&y=2#fragment","title":"Example","highlights":["summary"],"internal":"PRIVATE"}],"usage":{"input_tokens":9999},"internal":"PRIVATE"}`
		if calls == 2 {
			reply = `{"results":[]}`
		}
		reader := &kiloAlphaSearchTestBody{Reader: strings.NewReader(reply)}
		closes = append(closes, reader)
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": []string{"search-test-id"}, "Set-Cookie": []string{"PRIVATE"}}, Body: reader}, nil
	}
	result, err := forwardKiloAlphaSearchWithSender(context.Background(), c, account, []byte(body), send)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, calls)
	require.Equal(t, 2, result.WebSearchCalls)
	require.Equal(t, OpenAIUsage{}, result.Usage)
	require.Empty(t, result.UpstreamModel)
	require.Equal(t, kiloAlphaSearchPath, result.UpstreamEndpoint)
	require.Equal(t, "search-test-id", result.RequestID)
	require.Empty(t, result.UpstreamHeaders.Get("Set-Cookie"))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "PRIVATE")
	var response kiloAlphaSearchResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Contains(t, response.Output, "Search 2:")
	require.Contains(t, response.Output, "No search results found.")
	require.Len(t, response.Results, 1)
	require.Equal(t, "https://example.com/full?x=1&y=2#fragment", response.Results[0].URL)
	for _, reader := range closes {
		require.Equal(t, 1, reader.closed)
	}
}

func TestKiloAlphaSearchSenderFailureDoesNotLeakOrReplay(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, status := range []int{302, 400, 401, 403, 429, 500} {
			t.Run(fmt.Sprintf("partial_%t_status_%d", partial, status), func(t *testing.T) {
				body := `{"commands":{"search_query":[{"q":"first"}]}}`
				if partial {
					body = `{"commands":{"search_query":[{"q":"first"},{"q":"second"}]}}`
				}
				c, recorder := kiloAlphaSearchTestContext(body)
				calls := 0
				var readers []*kiloAlphaSearchTestBody
				send := func(*http.Request, string, *Account) (*http.Response, error) {
					calls++
					code, reply := status, "PRIVATE upstream page"
					if partial && calls == 1 {
						code, reply = 200, `{"results":[{"url":"https://example.com/partial"}]}`
					}
					reader := &kiloAlphaSearchTestBody{Reader: strings.NewReader(reply)}
					readers = append(readers, reader)
					return &http.Response{StatusCode: code, Header: make(http.Header), Body: reader}, nil
				}
				result, err := forwardKiloAlphaSearchWithSender(context.Background(), c, kiloAlphaSearchTestAccount(), []byte(body), send)
				require.Nil(t, result)
				if !partial && (status == 401 || status == 403 || status == 429 || status >= 500) {
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.Equal(t, status, failover.StatusCode)
					require.NotContains(t, string(failover.ResponseBody), "PRIVATE")
					require.Empty(t, recorder.Body.String())
				} else {
					require.NoError(t, err)
					want := status
					if want < 400 {
						want = 502
					}
					require.Equal(t, want, recorder.Code)
				}
				require.NotContains(t, recorder.Body.String(), "PRIVATE")
				require.NotContains(t, recorder.Body.String(), "partial")
				for _, reader := range readers {
					require.Equal(t, 1, reader.closed)
				}
			})
		}
	}
	for _, kind := range []string{"transport", "invalid_response", "cancelled", "bad_request"} {
		t.Run(kind, func(t *testing.T) {
			body := `{"commands":{"search_query":[{"q":"example"}]}}`
			if kind == "bad_request" {
				body = `{"commands":{"open":[{"ref_id":"PRIVATE"}]}}`
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			c, recorder := kiloAlphaSearchTestContext(body)
			calls := 0
			reader := &kiloAlphaSearchTestBody{Reader: strings.NewReader(`{"error":"PRIVATE"}`)}
			send := func(*http.Request, string, *Account) (*http.Response, error) {
				calls++
				if kind == "transport" {
					return &http.Response{Body: reader}, errors.New("PRIVATE transport including password")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader}, nil
			}
			result, err := forwardKiloAlphaSearchWithSender(ctx, c, kiloAlphaSearchTestAccount(), []byte(body), send)
			require.Nil(t, result)
			if kind == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
			}
			if kind == "bad_request" || kind == "cancelled" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
				require.Equal(t, 1, reader.closed)
				require.Equal(t, 502, recorder.Code)
			}
			require.NotContains(t, recorder.Body.String(), "PRIVATE")
		})
	}
}

func TestKiloAlphaSearchResponseValidationEncodingAndLimit(t *testing.T) {
	valid := `{"results":[{"url":"https://example.com","title":null,"author":null,"publishedDate":null,"highlights":null}]}`
	for _, encoding := range []string{"identity", "gzip"} {
		raw := []byte(valid)
		if encoding == "gzip" {
			var buffer bytes.Buffer
			writer := gzip.NewWriter(&buffer)
			_, err := writer.Write(raw)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			raw = buffer.Bytes()
		}
		reader := &kiloAlphaSearchTestBody{Reader: bytes.NewReader(raw)}
		group, err := readKiloAlphaSearchResponse(&http.Response{Header: http.Header{"Content-Encoding": []string{encoding}}, Body: reader})
		require.NoError(t, err)
		require.Len(t, group.Results, 1)
		require.Empty(t, group.Results[0].Title)
		require.Equal(t, 1, reader.closed)
	}
	for index, raw := range []string{
		`null`, `[]`, `{}`, `{"results":null}`, `{"results":{}}`, `{"results":[{}]}`,
		`{"results":[{"url":3}]}`, `{"results":[{"url":"javascript:PRIVATE"}]}`,
		`{"results":[{"url":"https://user:PRIVATE@example.com"}]}`,
		`{"results":[{"url":"https://example.com","highlights":"PRIVATE"}]}`,
		`{"results":[{"url":"https://example.com","author":false}]}`,
		strings.Repeat("x", kiloAlphaSearchMaxResponseBytes+1),
	} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			reader := &kiloAlphaSearchTestBody{Reader: strings.NewReader(raw)}
			_, err := readKiloAlphaSearchResponse(&http.Response{Header: make(http.Header), Body: reader})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "PRIVATE")
			require.Equal(t, 1, reader.closed)
		})
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write(bytes.Repeat([]byte("x"), kiloAlphaSearchMaxResponseBytes+1))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	_, err = readKiloAlphaSearchResponse(&http.Response{Header: http.Header{"Content-Encoding": []string{"gzip"}}, Body: io.NopCloser(bytes.NewReader(compressed.Bytes()))})
	require.Error(t, err)
	for _, encoding := range []string{"br", "gzip"} {
		_, err = readKiloAlphaSearchResponse(&http.Response{Header: http.Header{"Content-Encoding": []string{encoding}}, Body: io.NopCloser(strings.NewReader("PRIVATE malformed"))})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "PRIVATE")
	}
}

func TestKiloAlphaSearchSharedBudgetKeepsWholeSourceURLs(t *testing.T) {
	groups := make([]kiloAlphaSearchGroup, 4)
	for i := range groups {
		for j := 0; j < 3; j++ {
			groups[i].Results = append(groups[i].Results, kiloAlphaSearchResult{URL: fmt.Sprintf("https://example.com/%d/%d?q=full#fragment", i, j), Title: "示例 Example", Highlights: []string{strings.Repeat("内容", 10000)}})
		}
	}
	response := formatKiloAlphaSearchResults(groups, 2000)
	require.LessOrEqual(t, utf8.RuneCountInString(response.Output), 2000)
	require.Equal(t, 12, strings.Count(response.Output, "[Results truncated]"))
	require.Len(t, response.Results, 12)
	for i, group := range groups {
		require.Contains(t, response.Output, fmt.Sprintf("Search %d:", i+1))
		for _, result := range group.Results {
			require.Contains(t, response.Output, result.URL)
		}
	}
	longURL := "https://example.com/" + strings.Repeat("x", 100)
	groups = []kiloAlphaSearchGroup{{Results: []kiloAlphaSearchResult{{URL: longURL, Title: strings.Repeat("Title", 100), Highlights: []string{strings.Repeat("y", 500)}}}}, {Results: []kiloAlphaSearchResult{}}}
	for _, budget := range []int{0, 1, 4, 20, 80, 400} {
		response := formatKiloAlphaSearchResults(groups, budget)
		require.LessOrEqual(t, utf8.RuneCountInString(response.Output), budget)
		if strings.Contains(response.Output, "https://") {
			require.Contains(t, response.Output, longURL)
		}
		for _, citation := range response.Results {
			require.Equal(t, longURL, citation.URL)
		}
	}
}
