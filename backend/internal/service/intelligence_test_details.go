package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const intelligenceTestErrorDetailLimit = 2048
const intelligenceTestErrorBodyLimit = 32 * 1024

// ErrorDetail is deliberately excluded from ordinary JSON serialization.
// Only the administrator handler may explicitly add the sanitized diagnosis.
type IntelligenceTestDetails struct {
	Prompt      string `json:"prompt"`
	Answer      string `json:"answer"`
	ErrorCode   string `json:"error_code,omitempty"`
	ErrorDetail string `json:"-"`
}

func (s *IntelligenceTestService) GetDetails(ctx context.Context, groupID, resultID int64) (*IntelligenceTestDetails, error) {
	if groupID <= 0 || resultID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_INTELLIGENCE_TEST_QUERY", "请选择有效测试记录")
	}
	var mode, status string
	details := &IntelligenceTestDetails{}
	err := s.db.QueryRowContext(ctx, `SELECT r.mode,r.status,r.prompt_snapshot,r.answer_text,r.error_code,r.error_detail
		FROM pk_intelligence_test_results r JOIN groups g ON g.id=r.group_id
		WHERE r.id=$1 AND r.group_id=$2 AND g.status='active' AND g.deleted_at IS NULL`, resultID, groupID).
		Scan(&mode, &status, &details.Prompt, &details.Answer, &details.ErrorCode, &details.ErrorDetail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("INTELLIGENCE_TEST_RESULT_NOT_FOUND", "测试记录不存在或已清理")
	}
	if err != nil {
		return nil, err
	}
	// Reads cannot disclose running work or cause a model request. The list
	// already exposes the sole public in-progress state, "Agent工作中".
	if status == "running" {
		return &IntelligenceTestDetails{}, nil
	}
	if mode != "candy" {
		details.Prompt, details.Answer = "", ""
	}
	if details.ErrorCode == "" && (status == "failed" ||
		(mode == "candy" && details.Prompt == "" && details.Answer == "")) {
		details.ErrorCode = "historical_details_unavailable"
		details.ErrorDetail = "此历史记录未保存当次文案、最终回复或失败原因，无法还原具体详情。"
	}
	if details.ErrorCode != "" && !intelligenceTestErrorCodePattern.MatchString(details.ErrorCode) {
		details.ErrorCode = "internal_error"
	}
	details.ErrorDetail = intelligenceTestSanitizeErrorDetail(details.ErrorDetail, "")
	return details, nil
}

type intelligenceTestCallError struct {
	code   string
	detail string
}

func (err *intelligenceTestCallError) Error() string { return err.detail }

// Callers supply fixed diagnostic codes and descriptions. Unknown raw errors
// never get serialized: they may embed request URLs, credentials or payloads.
func intelligenceTestFailure(code, detail string) error {
	if !intelligenceTestErrorCodePattern.MatchString(code) {
		code = "internal_error"
	}
	return &intelligenceTestCallError{code: code, detail: intelligenceTestSanitizeErrorDetail(detail, "")}
}

func intelligenceTestErrorInfo(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	var failure *intelligenceTestCallError
	if errors.As(err, &failure) {
		return failure.code, failure.detail
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request_timeout", "测试达到请求时限，未收到完整最终回复。"
	}
	if errors.Is(err, context.Canceled) {
		return "request_cancelled", "测试已取消，未保存未完成的回答。"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "request_timeout", "请求或读取响应超时，未收到完整最终回复。"
	}
	return "internal_error", "测试未完成，未记录可安全展示的具体错误。"
}

var intelligenceTestErrorCodePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
var intelligenceTestErrorAuthorizationPattern = regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
var intelligenceTestErrorCredentialPattern = regexp.MustCompile(`(?i)(?:authorization|bearer|x[-_](?:api|goog[-_]api)[-_]key|api[-_]?key|access[-_]?token|refresh[-_]?token|id[-_]?token|session[-_]?token|cookie|set[-_]cookie|password|passwd|secret|client[-_]secret|private[-_]key|x[-_]sub2api[-_]intelligence[-_]token)["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;，；]+)`)
var intelligenceTestErrorKeyPattern = regexp.MustCompile(`(?i)\b(?:sk|sess|rk|pk|ak|api|key|token|secret)[_-][A-Za-z0-9._~+/=-]+`)
var intelligenceTestErrorOpaquePattern = regexp.MustCompile(`\b[A-Za-z0-9_+/=-]{24,}\b`)

func intelligenceTestSanitizeErrorDetail(detail, key string) string {
	if key != "" {
		detail = strings.ReplaceAll(detail, key, "[已脱敏]")
	}
	detail = strings.ToValidUTF8(detail, "")
	// Never display a nested request/header dump, even if a gateway placed one
	// inside its message field. Only a bounded diagnostic sentence is retained.
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "headers") || strings.Contains(lower, "curl ") ||
		strings.Contains(lower, "private key-----") {
		return "错误说明包含请求或凭据信息，详细内容已隐藏。"
	}
	detail = intelligenceTestErrorAuthorizationPattern.ReplaceAllString(detail, "[已脱敏]")
	detail = redactContentModerationSecrets(detail)
	detail = logredact.RedactText(detail, "authorization", "api_key", "x-api-key", "x-goog-api-key", "cookie", "secret", "token")
	detail = intelligenceTestErrorCredentialPattern.ReplaceAllString(detail, "[已脱敏]")
	detail = intelligenceTestErrorKeyPattern.ReplaceAllString(detail, "[已脱敏]")
	detail = intelligenceTestErrorOpaquePattern.ReplaceAllString(detail, "[已脱敏]")
	detail = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, detail)
	detail = strings.Join(strings.Fields(detail), " ")
	if len(detail) > intelligenceTestErrorDetailLimit {
		detail = detail[:intelligenceTestErrorDetailLimit-3]
		for !utf8.ValidString(detail) {
			detail = detail[:len(detail)-1]
		}
		detail += "..."
	}
	return detail
}

// Only selected message fields from a small JSON error envelope are considered.
// Raw bodies, HTML, headers, provider codes and nested payloads are discarded.
func intelligenceTestJSONErrorMessage(data []byte, key string) string {
	if len(data) == 0 || len(data) > intelligenceTestErrorBodyLimit {
		return ""
	}
	var envelope struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return ""
	}
	message := envelope.Message
	if len(envelope.Error) > 0 {
		var nested struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &nested) == nil && nested.Message != "" {
			message = nested.Message
		}
	}
	return intelligenceTestSanitizeErrorDetail(message, key)
}
