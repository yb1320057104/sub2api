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
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/idna"
)

const (
	kiloAlphaSearchURL              = "https://api.kilo.ai/api/exa/search"
	kiloAlphaSearchPath             = "/api/exa/search"
	kiloAlphaSearchTimeout          = 25 * time.Second
	kiloAlphaSearchMaxRequestBytes  = 64 << 20
	kiloAlphaSearchMaxResponseBytes = 1 << 20
)

type kiloAlphaSearchPayload struct {
	Query              string                  `json:"query"`
	Type               string                  `json:"type"`
	NumResults         int                     `json:"numResults"`
	Contents           kiloAlphaSearchContents `json:"contents"`
	IncludeDomains     []string                `json:"includeDomains,omitempty"`
	StartPublishedDate string                  `json:"startPublishedDate,omitempty"`
}

type kiloAlphaSearchContents struct {
	Highlights bool `json:"highlights"`
}

type kiloAlphaSearchPlan struct {
	model         string
	payloads      []kiloAlphaSearchPayload
	maxCharacters int
}

type kiloAlphaSearchResult struct {
	URL           string   `json:"url"`
	Title         string   `json:"title,omitempty"`
	Author        string   `json:"author,omitempty"`
	PublishedDate string   `json:"publishedDate,omitempty"`
	Highlights    []string `json:"highlights,omitempty"`
}

type kiloAlphaSearchGroup struct {
	Results []kiloAlphaSearchResult `json:"results"`
}

type kiloAlphaSearchCitation struct {
	Type  string `json:"type"`
	RefID string `json:"ref_id"`
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

type kiloAlphaSearchResponse struct {
	Output  string                    `json:"output"`
	Results []kiloAlphaSearchCitation `json:"results,omitempty"`
}

func kiloAlphaSearchInvalid(message string) error {
	return infraerrors.BadRequest("KILO_SEARCH_INVALID_REQUEST", message)
}

// ValidateKiloAlphaSearchBody 校验独立搜索信封，并返回实际 Exa 查询数，供入口
// 在选路前拒绝不支持的命令、预留按次预算。model/input/reasoning/id 只属本地元数据。
func ValidateKiloAlphaSearchBody(body []byte) (queryCount int, err error) {
	plan, err := prepareKiloAlphaSearch(body, time.Now())
	if err != nil {
		return 0, err
	}
	return len(plan.payloads), nil
}

// kiloAlphaSearchObject 拒绝重复键和尾随 JSON；错误中不带字段名或原文，避免
// 用户输入、凭据及上游错误页沿日志错误链泄漏。
func kiloAlphaSearchObject(raw []byte) (map[string]json.RawMessage, error) {
	invalid := errors.New("Invalid search JSON object.")
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, invalid
		}
		if _, exists := object[name]; exists {
			return nil, invalid
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, invalid
		}
		object[name] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid
	}
	var trailing json.RawMessage
	if decoder.Decode(&trailing) != io.EOF {
		return nil, invalid
	}
	return object, nil
}

func kiloAlphaSearchOnlyFields(object map[string]json.RawMessage, fields ...string) bool {
	for name := range object {
		found := false
		for _, allowed := range fields {
			if name == allowed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func kiloAlphaSearchAbsent(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func kiloAlphaSearchEmptyCommand(raw json.RawMessage) bool {
	if kiloAlphaSearchAbsent(raw) {
		return true
	}
	var entries []json.RawMessage
	return json.Unmarshal(raw, &entries) == nil && len(entries) == 0
}

func prepareKiloAlphaSearch(body []byte, now time.Time) (*kiloAlphaSearchPlan, error) {
	invalid := kiloAlphaSearchInvalid("Unsupported search request.")
	if len(body) == 0 || len(body) > kiloAlphaSearchMaxRequestBytes {
		return nil, invalid
	}
	envelope, err := kiloAlphaSearchObject(body)
	if err != nil || !kiloAlphaSearchOnlyFields(envelope, "id", "model", "reasoning", "input", "commands", "settings", "max_output_tokens") {
		return nil, invalid
	}
	plan := &kiloAlphaSearchPlan{}
	if model, present := envelope["model"]; present {
		if json.Unmarshal(model, &plan.model) != nil || kiloAlphaSearchAbsent(model) {
			return nil, invalid
		}
		plan.model = strings.TrimSpace(plan.model)
	}
	commands, err := kiloAlphaSearchObject(envelope["commands"])
	if err != nil || !kiloAlphaSearchOnlyFields(commands, "search_query", "response_length", "open", "find") {
		return nil, kiloAlphaSearchInvalid("Only search_query commands are supported by Kilo.")
	}
	// Codex 可将未使用的可选命令序列化为空数组/null；有实际 open/find 时必须拒绝。
	for _, name := range []string{"open", "find"} {
		if !kiloAlphaSearchEmptyCommand(commands[name]) {
			return nil, kiloAlphaSearchInvalid("Kilo search does not support open or find commands.")
		}
	}
	length := "short"
	if raw := commands["response_length"]; !kiloAlphaSearchEmptyCommand(raw) {
		if json.Unmarshal(raw, &length) != nil {
			return nil, invalid
		}
	}
	count, capCharacters := 3, 8000
	switch length {
	case "short":
	case "medium":
		count, capCharacters = 5, 16000
	case "long":
		count, capCharacters = 10, 32000
	default:
		return nil, kiloAlphaSearchInvalid("Invalid response_length.")
	}
	if raw := envelope["settings"]; !kiloAlphaSearchAbsent(raw) {
		settings, err := kiloAlphaSearchObject(raw)
		if err != nil || !kiloAlphaSearchOnlyFields(settings, "external_web_access", "allowed_callers") {
			return nil, kiloAlphaSearchInvalid("Unsupported search settings.")
		}
		if external, present := settings["external_web_access"]; present && !bytes.Equal(bytes.TrimSpace(external), []byte("true")) {
			return nil, kiloAlphaSearchInvalid("Cached-only search is not supported by Kilo.")
		}
		if callers := settings["allowed_callers"]; !kiloAlphaSearchAbsent(callers) {
			var values []string
			if json.Unmarshal(callers, &values) != nil {
				return nil, invalid
			}
		}
	}
	budget := int64(2500)
	if raw, present := envelope["max_output_tokens"]; present {
		if json.Unmarshal(raw, &budget) != nil || kiloAlphaSearchAbsent(raw) || budget < 1 {
			return nil, kiloAlphaSearchInvalid("max_output_tokens must be a positive integer.")
		}
	}
	// 仅作为字符上界，不是 token 计数；先截上界，避免乘法溢出。
	plan.maxCharacters = capCharacters
	if budget < int64(capCharacters/4) {
		plan.maxCharacters = int(budget) * 4
	}
	var queries []json.RawMessage
	if json.Unmarshal(commands["search_query"], &queries) != nil || len(queries) < 1 || len(queries) > 4 {
		return nil, kiloAlphaSearchInvalid("Expected one to four search queries.")
	}
	for _, raw := range queries {
		query, err := kiloAlphaSearchObject(raw)
		if err != nil || !kiloAlphaSearchOnlyFields(query, "q", "domains", "recency") {
			return nil, invalid
		}
		payload := kiloAlphaSearchPayload{Type: "auto", NumResults: count, Contents: kiloAlphaSearchContents{Highlights: true}}
		if json.Unmarshal(query["q"], &payload.Query) != nil || strings.TrimSpace(payload.Query) == "" || utf8.RuneCountInString(payload.Query) > 16384 {
			return nil, kiloAlphaSearchInvalid("Expected a nonempty search query of at most 16384 characters.")
		}
		payload.IncludeDomains, err = normalizeKiloAlphaSearchDomains(query["domains"])
		if err != nil {
			return nil, err
		}
		if recency := query["recency"]; !kiloAlphaSearchAbsent(recency) {
			var days int64
			if json.Unmarshal(recency, &days) != nil || days < 1 || days > 36500 {
				return nil, kiloAlphaSearchInvalid("recency must be an integer from 1 to 36500 days.")
			}
			payload.StartPublishedDate = now.UTC().Add(-time.Duration(days) * 24 * time.Hour).Format("2006-01-02T15:04:05.000Z")
		}
		plan.payloads = append(plan.payloads, payload)
	}
	return plan, nil
}

func normalizeKiloAlphaSearchDomains(raw json.RawMessage) ([]string, error) {
	if kiloAlphaSearchAbsent(raw) {
		return nil, nil
	}
	invalid := kiloAlphaSearchInvalid("Expected at most 100 valid domain names.")
	var domains []string
	if json.Unmarshal(raw, &domains) != nil || len(domains) > 100 {
		return nil, invalid
	}
	result := make([]string, 0, len(domains))
	seen := make(map[string]bool)
	for _, domain := range domains {
		if domain == "" || utf8.RuneCountInString(domain) > 253 {
			return nil, invalid
		}
		domain, err := idna.Lookup.ToASCII(strings.TrimSuffix(domain, "."))
		if err != nil || len(domain) > 253 {
			return nil, invalid
		}
		domain = strings.ToLower(domain)
		labels := strings.Split(domain, ".")
		if len(labels) < 2 {
			return nil, invalid
		}
		for _, label := range labels {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, invalid
			}
			for _, char := range label {
				if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
					return nil, invalid
				}
			}
		}
		if !seen[domain] {
			seen[domain] = true
			result = append(result, domain)
		}
	}
	return result, nil
}

func validateKiloAlphaSearchAccount(account *Account) error {
	if account == nil || !account.IsKiloGatewayAccount() {
		return kiloInferenceDenied("KILO_SEARCH_ACCOUNT_INVALID", "Kilo search requires a managed Gateway Key account; official OAuth is not supported.")
	}
	return ValidateKiloInferenceAccount(account)
}

func buildKiloAlphaSearchRequest(ctx context.Context, account *Account, payload kiloAlphaSearchPayload) (*http.Request, error) {
	if err := validateKiloAlphaSearchAccount(account); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("Cannot encode Kilo search request.")
	}
	ctx = WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI)
	ctx = WithHTTPUpstreamRedirectsDisabled(ctx)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, kiloAlphaSearchURL, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("Cannot build Kilo search request.")
	}
	// 不复制客户端、OpenAI 身份或账号覆写头；唯一 bearer 来自保存的 Gateway Key。
	request.Header.Set("Authorization", "Bearer "+account.GetCredential("api_key"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	if err := ValidateKiloAlphaSearchRequest(request, account); err != nil {
		return nil, err
	}
	return request, nil
}

// ValidateKiloAlphaSearchRequest 只校验固定搜索端点、Gateway Key 和最小 Exa 载荷。
// 主线须在现有 Kilo 最终发送守卫中显式调用它，并保留最新账号/来源健康校验；
// 本函数不会给原有 Responses 端点边界增设隐式例外，也不会绕过代理/并发链。
func ValidateKiloAlphaSearchRequest(request *http.Request, account *Account) error {
	if err := validateKiloAlphaSearchAccount(account); err != nil {
		return err
	}
	invalid := kiloInferenceDenied("KILO_SEARCH_REQUEST_INVALID", "Kilo search requires the fixed HTTPS Exa endpoint, saved Gateway Key and search-only payload.")
	if request == nil || request.URL == nil || request.Method != http.MethodPost || request.URL.String() != kiloAlphaSearchURL || (request.Host != "" && request.Host != "api.kilo.ai") || request.GetBody == nil {
		return invalid
	}
	var authorizations []string
	for name, values := range request.Header {
		if strings.EqualFold(name, "Authorization") {
			authorizations = append(authorizations, values...)
		}
		if strings.EqualFold(name, "Cookie") || strings.EqualFold(name, "Proxy-Authorization") || strings.EqualFold(name, "Chatgpt-Account-Id") || strings.EqualFold(name, "OpenAI-Organization") {
			return invalid
		}
	}
	if len(authorizations) != 1 || authorizations[0] != "Bearer "+account.GetCredential("api_key") {
		return invalid
	}
	reader, err := request.GetBody()
	if err != nil {
		return invalid
	}
	body, err := io.ReadAll(io.LimitReader(reader, 256<<10+1))
	_ = reader.Close()
	if err != nil || len(body) > 256<<10 {
		return invalid
	}
	object, err := kiloAlphaSearchObject(body)
	if err != nil || !kiloAlphaSearchOnlyFields(object, "query", "type", "numResults", "contents", "includeDomains", "startPublishedDate") {
		return invalid
	}
	var payload kiloAlphaSearchPayload
	if json.Unmarshal(body, &payload) != nil || strings.TrimSpace(payload.Query) == "" || utf8.RuneCountInString(payload.Query) > 16384 || payload.Type != "auto" || payload.NumResults < 1 || payload.NumResults > 10 {
		return invalid
	}
	contents, err := kiloAlphaSearchObject(object["contents"])
	if err != nil || !kiloAlphaSearchOnlyFields(contents, "highlights") || !payload.Contents.Highlights {
		return invalid
	}
	domains, err := normalizeKiloAlphaSearchDomains(object["includeDomains"])
	if err != nil || len(domains) != len(payload.IncludeDomains) {
		return invalid
	}
	for index, domain := range domains {
		if domain != payload.IncludeDomains[index] {
			return invalid
		}
	}
	if payload.StartPublishedDate != "" {
		date, err := time.Parse("2006-01-02T15:04:05.000Z", payload.StartPublishedDate)
		if err != nil || date.Format("2006-01-02T15:04:05.000Z") != payload.StartPublishedDate {
			return invalid
		}
	}
	return nil
}

type kiloAlphaSearchSender func(*http.Request, string, *Account) (*http.Response, error)

// forwardKiloAlphaSearch 由 ForwardAlphaSearch 在任何 OpenAI 身份/token 准备前分流。
// 入口持有用户/账号/RPM admission；多查询串行共享同一账号、代理链及 25 秒预算。
func (s *OpenAIGatewayService) forwardKiloAlphaSearch(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	if s == nil {
		return nil, errors.New("Kilo search service is unavailable.")
	}
	return forwardKiloAlphaSearchWithSender(ctx, c, account, body, func(request *http.Request, proxy string, selected *Account) (*http.Response, error) {
		response, err := s.doOpenAIUpstream(request, proxy, selected)
		if err == nil && response != nil && response.Body != nil && response.StatusCode >= 400 {
			// Preserve the original bytes and lease for normal error handling.
			// Only the exact saved Key snapshot may report account rejection.
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body = &kiloReplayPrefixBody{Reader: io.MultiReader(bytes.NewReader(raw), response.Body), closer: response.Body}
			if readErr == nil && s.rateLimitService != nil {
				s.rateLimitService.observeKiloGatewayFailure(request.Context(), selected, response.StatusCode, raw)
			}
		}
		return response, err
	})
}

func forwardKiloAlphaSearchWithSender(ctx context.Context, c *gin.Context, account *Account, body []byte, send kiloAlphaSearchSender) (*OpenAIForwardResult, error) {
	if c == nil || send == nil || ctx == nil {
		return nil, errors.New("Kilo search context and sender are required.")
	}
	plan, err := prepareKiloAlphaSearch(body, time.Now())
	if err != nil {
		writeKiloAlphaSearchError(c, http.StatusBadRequest, infraerrors.Message(err))
		return nil, nil
	}
	if err := validateKiloAlphaSearchAccount(account); err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			return nil, errors.New("Kilo search proxy metadata is unavailable.")
		}
		proxyURL = account.Proxy.URL()
	}
	ctx, cancel := context.WithTimeout(ctx, kiloAlphaSearchTimeout)
	defer cancel()
	start := time.Now()
	groups := make([]kiloAlphaSearchGroup, 0, len(plan.payloads))
	var headers http.Header
	SetActualOpenAIUpstreamEndpoint(c, kiloAlphaSearchPath)
	for _, payload := range plan.payloads {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		request, err := buildKiloAlphaSearchRequest(ctx, account, payload)
		if err != nil {
			return nil, err
		}
		response, err := send(request, proxyURL, account)
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(start).Milliseconds())
		if err != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if _, _, policy := KiloInferencePolicyError(err); policy {
				return nil, err
			}
			// 不包装底层异常；其中可能含代理密码、查询或响应正文。
			writeKiloAlphaSearchError(c, http.StatusBadGateway, "Kilo search transport failed.")
			return nil, nil
		}
		if response == nil || response.Body == nil {
			writeKiloAlphaSearchError(c, http.StatusBadGateway, "Invalid Kilo search response.")
			return nil, nil
		}
		headers = kiloAlphaSearchSafeResponseHeaders(response.Header)
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			status := response.StatusCode
			if status < http.StatusBadRequest || status > 599 {
				status = http.StatusBadGateway
			}
			// 有成功子查询后不交回整批 failover，避免重放已经执行的查询。
			if len(groups) == 0 && (response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 429 || response.StatusCode >= 500 && response.StatusCode <= 599) {
				return nil, &UpstreamFailoverError{StatusCode: status, ResponseBody: kiloAlphaSearchErrorBody(status, "Kilo search upstream request failed.")}
			}
			writeKiloAlphaSearchError(c, status, "Kilo search upstream request failed.")
			return nil, nil
		}
		group, err := readKiloAlphaSearchResponse(response)
		if err != nil {
			writeKiloAlphaSearchError(c, http.StatusBadGateway, "Invalid Kilo search response.")
			return nil, nil
		}
		groups = append(groups, group)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	formatted := formatKiloAlphaSearchResults(groups, plan.maxCharacters)
	c.JSON(http.StatusOK, formatted)
	return &OpenAIForwardResult{
		RequestID: headers.Get("X-Request-Id"), UpstreamHeaders: headers,
		ResponseHeaders: headers.Clone(), Model: plan.model,
		UpstreamEndpoint: kiloAlphaSearchPath, Duration: time.Since(start),
		// 只有全部子查询返回可验证的 2xx 结果，才提交实际 Exa 调用次数。
		// 此协议没有 token usage；保持 Usage 零值，沿既有按次分支计费。
		WebSearchCalls: len(groups),
	}, nil
}

func kiloAlphaSearchErrorBody(status int, message string) []byte {
	kind := "upstream_error"
	if status == http.StatusBadRequest {
		kind = "invalid_request_error"
	}
	body, _ := json.Marshal(gin.H{"error": gin.H{"type": kind, "message": message}})
	return body
}

func writeKiloAlphaSearchError(c *gin.Context, status int, message string) {
	c.Data(status, "application/json", kiloAlphaSearchErrorBody(status, message))
}

func kiloAlphaSearchSafeResponseHeaders(source http.Header) http.Header {
	result := make(http.Header)
	id := source.Get("X-Request-Id")
	if len(id) > 0 && len(id) <= 128 {
		valid := true
		for _, char := range id {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && !strings.ContainsRune("-_.:", char) {
				valid = false
				break
			}
		}
		if valid {
			result.Set("X-Request-Id", id)
		}
	}
	return result
}

func readKiloAlphaSearchResponse(response *http.Response) (kiloAlphaSearchGroup, error) {
	defer func() { _ = response.Body.Close() }()
	invalid := errors.New("Invalid Kilo search response.")
	var reader io.Reader = response.Body
	switch strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		compressed, err := gzip.NewReader(response.Body)
		if err != nil {
			return kiloAlphaSearchGroup{}, invalid
		}
		defer func() { _ = compressed.Close() }()
		reader = compressed
	default:
		return kiloAlphaSearchGroup{}, invalid
	}
	body, err := io.ReadAll(io.LimitReader(reader, kiloAlphaSearchMaxResponseBytes+1))
	if err != nil || len(body) > kiloAlphaSearchMaxResponseBytes {
		return kiloAlphaSearchGroup{}, invalid
	}
	object, err := kiloAlphaSearchObject(body)
	if err != nil {
		return kiloAlphaSearchGroup{}, invalid
	}
	var entries []json.RawMessage
	if kiloAlphaSearchAbsent(object["results"]) || json.Unmarshal(object["results"], &entries) != nil || len(entries) > 10 {
		return kiloAlphaSearchGroup{}, invalid
	}
	group := kiloAlphaSearchGroup{Results: make([]kiloAlphaSearchResult, 0, len(entries))}
	for _, entry := range entries {
		item, err := kiloAlphaSearchObject(entry)
		if err != nil {
			return kiloAlphaSearchGroup{}, invalid
		}
		var result kiloAlphaSearchResult
		if json.Unmarshal(item["url"], &result.URL) != nil || result.URL == "" || strings.ContainsAny(result.URL, "\r\n\t ") {
			return kiloAlphaSearchGroup{}, invalid
		}
		parsed, err := url.Parse(result.URL)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return kiloAlphaSearchGroup{}, invalid
		}
		for name, destination := range map[string]*string{"title": &result.Title, "author": &result.Author, "publishedDate": &result.PublishedDate} {
			if raw := item[name]; !kiloAlphaSearchAbsent(raw) && json.Unmarshal(raw, destination) != nil {
				return kiloAlphaSearchGroup{}, invalid
			}
		}
		if raw := item["highlights"]; !kiloAlphaSearchAbsent(raw) && json.Unmarshal(raw, &result.Highlights) != nil {
			return kiloAlphaSearchGroup{}, invalid
		}
		group.Results = append(group.Results, result)
	}
	return group, nil
}

func kiloAlphaSearchRuneLength(text string) int { return utf8.RuneCountInString(text) }

func kiloAlphaSearchPrefix(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

func formatKiloAlphaSearchResults(groups []kiloAlphaSearchGroup, maxCharacters int) kiloAlphaSearchResponse {
	response := kiloAlphaSearchResponse{}
	if len(groups) == 0 || maxCharacters <= 0 {
		return response
	}
	separatorSize := 2 * (len(groups) - 1)
	if maxCharacters <= separatorSize {
		response.Output = "…"
		return response
	}
	groupBudget, extra := (maxCharacters-separatorSize)/len(groups), (maxCharacters-separatorSize)%len(groups)
	blocks := make([]string, 0, len(groups))
	number := 0
	for index, group := range groups {
		budget := groupBudget
		if index < extra {
			budget++
		}
		heading := "Search " + strconv.Itoa(index+1) + ":"
		if len(group.Results) == 0 {
			text := heading + "\nNo search results found."
			if kiloAlphaSearchRuneLength(text) > budget {
				text = kiloAlphaSearchPrefix("…", budget)
			}
			blocks = append(blocks, text)
			continue
		}
		available := budget - kiloAlphaSearchRuneLength(heading) - 2*len(group.Results)
		if available < len(group.Results) {
			blocks = append(blocks, kiloAlphaSearchPrefix("…", budget))
			number += len(group.Results)
			continue
		}
		resultBudget, remainder := available/len(group.Results), available%len(group.Results)
		rendered := []string{heading}
		for position, result := range group.Results {
			number++
			limit := resultBudget
			if position < remainder {
				limit++
			}
			title := strings.Join(strings.Fields(result.Title), " ")
			if title == "" {
				title = result.URL
			}
			if kiloAlphaSearchRuneLength(title) > 256 {
				title = kiloAlphaSearchPrefix(title, 255) + "…"
			}
			header := fmt.Sprintf("[%d] %s\nURL: %s", number, title, result.URL)
			if kiloAlphaSearchRuneLength(header) > limit {
				header = fmt.Sprintf("[%d]\nURL: %s", number, result.URL)
			}
			if kiloAlphaSearchRuneLength(header) > limit {
				omitted := "[Result omitted: output limit]"
				if kiloAlphaSearchRuneLength(omitted) > limit {
					omitted = kiloAlphaSearchPrefix("…", limit)
				}
				rendered = append(rendered, omitted)
				continue
			}
			details := make([]string, 0, len(result.Highlights)+2)
			if result.Author != "" {
				details = append(details, "Author: "+result.Author)
			}
			if result.PublishedDate != "" {
				details = append(details, "Published: "+result.PublishedDate)
			}
			details = append(details, result.Highlights...)
			detail := ""
			if len(details) > 0 {
				detail = "\n" + strings.Join(details, "\n")
			}
			space := limit - kiloAlphaSearchRuneLength(header)
			if kiloAlphaSearchRuneLength(detail) > space {
				marker := "\n[Results truncated]"
				if space >= kiloAlphaSearchRuneLength(marker) {
					detail = kiloAlphaSearchPrefix(detail, space-kiloAlphaSearchRuneLength(marker)) + marker
				} else {
					detail = kiloAlphaSearchPrefix("…", space)
				}
			}
			rendered = append(rendered, header+detail)
			response.Results = append(response.Results, kiloAlphaSearchCitation{Type: "text_result", RefID: fmt.Sprintf("turn0search%d", number-1), URL: result.URL, Title: title})
		}
		blocks = append(blocks, strings.Join(rendered, "\n\n"))
	}
	response.Output = strings.Join(blocks, "\n\n")
	return response
}
