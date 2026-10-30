package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

const maxTurnstileResponseBytes = 1 << 20

type turnstileVerifier struct {
	httpClient *http.Client
	verifyURL  string
}

func NewTurnstileVerifier() service.TurnstileVerifier {
	sharedClient, err := httpclient.GetClient(httpclient.Options{
		Timeout:            10 * time.Second,
		ValidateResolvedIP: true,
	})
	if err != nil {
		sharedClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &turnstileVerifier{
		httpClient: sharedClient,
		verifyURL:  turnstileVerifyURL,
	}
}

func (v *turnstileVerifier) VerifyToken(ctx context.Context, secretKey, token, remoteIP string) (*service.TurnstileVerifyResponse, error) {
	formData := url.Values{}
	formData.Set("secret", secretKey)
	formData.Set("response", token)
	if remoteIP != "" {
		formData.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.verifyURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("turnstile verification returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTurnstileResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxTurnstileResponseBytes {
		return nil, fmt.Errorf("turnstile verification response exceeds %d bytes", maxTurnstileResponseBytes)
	}

	var result service.TurnstileVerifyResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	// Decode once more to reject a valid JSON object followed by arbitrary
	// trailing data. A bounded reader alone would otherwise accept a valid
	// prefix and silently ignore an oversized/malformed suffix.
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode response: trailing JSON data")
		}
		return nil, fmt.Errorf("decode response: trailing data: %w", err)
	}

	return &result, nil
}
