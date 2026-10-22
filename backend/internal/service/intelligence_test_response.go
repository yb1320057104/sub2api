package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// Only the terminal assistant answer is extracted. Reasoning events, deltas,
// upstream errors, headers and raw responses are never persisted or exposed.
type intelligenceTestResponse struct {
	Status string          `json:"status"`
	Error  json.RawMessage `json:"error"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Phase   string `json:"phase"`
		Status  string `json:"status"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (s *IntelligenceTestService) callModel(ctx context.Context, key, mode string, settings IntelligenceTestModeConfig) (string, error) {
	endpoint, err := s.intelligenceEndpoint()
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"model": settings.Model, "input": []any{map[string]any{"role": "user", "content": settings.Prompt}},
		"stream": true, "store": false, "max_output_tokens": 16384,
	}
	if settings.Effort != "" {
		payload["reasoning"] = map[string]any{"effort": settings.Effort}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "DP-API-Intelligence-Test/1.0")
	if err := AuthorizeIntelligenceTestRequest(req); err != nil {
		return "", err
	}
	response, err := s.client.Do(req)
	if err != nil {
		return "", errors.New("intelligence test request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("intelligence test gateway rejected request")
	}
	limited := &io.LimitedReader{R: response.Body, N: intelligenceTestResponseLimit + 1}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		data, err := io.ReadAll(limited)
		if err != nil || len(data) > intelligenceTestResponseLimit {
			return "", errors.New("intelligence test response exceeds limit")
		}
		var result intelligenceTestResponse
		if err := json.Unmarshal(data, &result); err != nil {
			return "", errors.New("invalid intelligence test response")
		}
		return intelligenceTestFinalText(&result)
	}
	if !strings.Contains(contentType, "text/event-stream") {
		return "", errors.New("unsupported intelligence test response type")
	}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var data strings.Builder
	consume := func() (string, bool, error) {
		if data.Len() == 0 {
			return "", false, nil
		}
		value := strings.TrimSpace(data.String())
		data.Reset()
		if value == "[DONE]" {
			return "", false, errors.New("response ended before completed event")
		}
		var event struct {
			Type     string                    `json:"type"`
			Response *intelligenceTestResponse `json:"response"`
		}
		if err := json.Unmarshal([]byte(value), &event); err != nil {
			return "", false, errors.New("invalid intelligence test event")
		}
		switch event.Type {
		case "error", "response.failed", "response.incomplete", "response.cancelled":
			return "", false, errors.New("intelligence test did not complete")
		case "response.completed":
			text, err := intelligenceTestFinalText(event.Response)
			return text, true, err
		default:
			return "", false, nil
		}
	}
	for scanner.Scan() {
		if limited.N <= 0 {
			return "", errors.New("intelligence test response exceeds limit")
		}
		line := strings.TrimPrefix(strings.TrimSuffix(scanner.Text(), "\r"), "\ufeff")
		if line == "" {
			text, completed, err := consume()
			if err != nil || completed {
				return text, err
			}
		} else if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				_ = data.WriteByte('\n')
			}
			_, _ = data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			if data.Len() > 1024*1024 {
				return "", errors.New("intelligence test event exceeds limit")
			}
		}
	}
	if scanner.Err() != nil || limited.N <= 0 {
		return "", errors.New("intelligence test stream interrupted")
	}
	text, completed, err := consume()
	if err != nil {
		return "", err
	}
	if !completed {
		return "", errors.New("intelligence test stream did not complete")
	}
	return text, nil
}

func intelligenceTestFinalText(response *intelligenceTestResponse) (string, error) {
	if response == nil || response.Status != "completed" || (len(response.Error) > 0 && string(response.Error) != "null") {
		return "", errors.New("intelligence test response was not completed")
	}
	var text strings.Builder
	for _, item := range response.Output {
		if item.Type != "message" || item.Role != "assistant" || item.Phase == "commentary" {
			continue
		}
		if item.Status != "" && item.Status != "completed" {
			return "", errors.New("intelligence test answer was not completed")
		}
		for _, part := range item.Content {
			if part.Type != "output_text" {
				continue
			}
			if text.Len() > 0 {
				_ = text.WriteByte('\n')
			}
			_, _ = text.WriteString(part.Text)
			if text.Len() > intelligenceTestArtifactLimit {
				return "", errors.New("intelligence test answer exceeds limit")
			}
		}
	}
	answer := strings.TrimSpace(text.String())
	if answer == "" {
		return "", errors.New("intelligence test returned no final answer")
	}
	return answer, nil
}
