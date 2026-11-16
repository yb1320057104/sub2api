package service

import (
	"bytes"
	"context"

	"github.com/tidwall/gjson"
)

type openAIEncryptedReasoningAffinityKey struct{}

func (s *OpenAIGatewayService) encryptedAccountLockDisabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.DisableEncryptedAccountLock
}

// WithOpenAIEncryptedReasoningAffinity applies the operator's account-lock
// policy before selection. Ordinary sticky and response-anchor routing remain.
func (s *OpenAIGatewayService) WithOpenAIEncryptedReasoningAffinity(ctx context.Context, body []byte) context.Context {
	if s.encryptedAccountLockDisabled() {
		return context.WithValue(ctx, openAIEncryptedReasoningAffinityKey{}, false)
	}
	return WithOpenAIEncryptedReasoningAffinity(ctx, body)
}

// WithOpenAIEncryptedReasoningAffinity marks a Responses admission once, before
// account selection. It does not change hard admission or capacity decisions.
func WithOpenAIEncryptedReasoningAffinity(ctx context.Context, body []byte) context.Context {
	// Escaped keys may still contain the field, so retain the original lookup.
	if !bytes.Contains(body, []byte("encrypted_content")) && !bytes.Contains(body, []byte(`\u`)) {
		return context.WithValue(ctx, openAIEncryptedReasoningAffinityKey{}, false)
	}
	input := gjson.GetBytes(body, "input")
	found := false
	visit := func(item gjson.Result) {
		if item.Get("type").String() == "reasoning" {
			e := item.Get("encrypted_content")
			found = found || e.Type == gjson.String && e.String() != ""
		}
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool { visit(item); return !found })
	} else if input.IsObject() {
		visit(input)
	}
	return context.WithValue(ctx, openAIEncryptedReasoningAffinityKey{}, found)
}
