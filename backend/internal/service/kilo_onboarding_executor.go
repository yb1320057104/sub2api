package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type kiloExecutorClient struct {
	base, token string
	client      *http.Client
}

// Deployment supplies a loopback/HTTPS endpoint and a dedicated bearer. No
// default unauthenticated connection to the earlier staging login prototype.
func newKiloExecutorFromEnv() KiloOnboardingExecutor {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("KILO_EXECUTOR_URL")), "/")
	token := strings.TrimSpace(os.Getenv("KILO_EXECUTOR_TOKEN"))
	if file := strings.TrimSpace(os.Getenv("KILO_EXECUTOR_TOKEN_FILE")); file != "" {
		if raw, err := os.ReadFile(file); err == nil {
			token = strings.TrimSpace(string(raw))
		} else {
			return nil
		}
	}
	if endpoint == "" || len(token) < 32 {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil
	}
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Scheme != "http" || !(strings.EqualFold(parsed.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())) {
			return nil
		}
	}
	return &kiloExecutorClient{base: endpoint, token: token, client: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *kiloExecutorClient) request(ctx context.Context, method, path string, input, out any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return &kiloExecutorError{code: "EXECUTOR_UNAVAILABLE", retryable: true}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &kiloExecutorError{code: "REMOTE_OUTCOME_UNKNOWN", retryable: true}
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		return &kiloExecutorError{code: "EXECUTOR_BUSY", retryable: true}
	}
	if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
		return &kiloExecutorError{code: "SESSION_LOST", retryable: true}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &kiloExecutorError{code: "EXECUTOR_UNAVAILABLE", retryable: false}
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return errors.New("执行器响应格式无效")
	}
	return nil
}
func (c *kiloExecutorClient) Health(ctx context.Context) (*KiloExecutorHealth, error) {
	out := &KiloExecutorHealth{}
	err := c.request(ctx, http.MethodGet, "/v1/health", nil, out)
	return out, err
}
func (c *kiloExecutorClient) Submit(ctx context.Context, in KiloExecutorTaskInput) (*KiloExecutorTask, error) {
	out := &KiloExecutorTask{}
	err := c.request(ctx, http.MethodPost, "/v1/tasks", in, out)
	return out, err
}
func (c *kiloExecutorClient) Task(ctx context.Context, id string) (*KiloExecutorTask, error) {
	out := &KiloExecutorTask{}
	err := c.request(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(id), nil, out)
	return out, err
}
func (c *kiloExecutorClient) Cancel(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(id)+"/cancel", struct{}{}, nil)
}
func (c *kiloExecutorClient) Ack(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(id)+"/ack", struct{}{}, nil)
}
func (c *kiloExecutorClient) CloseSession(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/v1/sessions/"+url.PathEscape(id), nil, nil)
}

type kiloExecutorError struct {
	code      string
	retryable bool
}

func (e *kiloExecutorError) Error() string { return fmt.Sprintf("Kilo执行器: %s", e.code) }
func kiloExecutorErrorInfo(err error) (string, bool) {
	var e *kiloExecutorError
	if errors.As(err, &e) {
		return e.code, e.retryable
	}
	return "EXECUTOR_UNAVAILABLE", true
}
