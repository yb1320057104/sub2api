package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// SetGroupAntiDistillationService installs the shared group policy at startup.
func (s *OpenAIGatewayService) SetGroupAntiDistillationService(protection *GroupAntiDistillationService) {
	s.groupAntiDistillation = protection
}

// ObserveGroupAntiDistillationUsage runs independently of asynchronous billing:
// a saturated usage worker pool must not silently skip protection evidence.
// Call once after a successful HTTP request or websocket turn, never on retry.
func (s *OpenAIGatewayService) ObserveGroupAntiDistillationUsage(ctx context.Context, key *APIKey, account *Account, result *OpenAIForwardResult, startedAt time.Time, requestBody []byte) {
	if s == nil || s.groupAntiDistillation == nil || key == nil || key.Group == nil || key.Group.Platform != PlatformOpenAI || account == nil || account.Platform != PlatformOpenAI || result == nil {
		return
	}
	if !openAIGroupAntiDistillationUsageEligible(result) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Disabled groups do not scan potentially large request bodies. This uses
	// the same bounded configuration cache as admission, without a SQL hot path.
	configCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	cfg, err := s.groupAntiDistillation.GetConfig(configCtx, key.Group.ID)
	cancel()
	if err != nil {
		slog.Warn("group zero-cache protection config unavailable", "group_id", key.Group.ID)
		return
	}
	if !cfg.Enabled || !gjson.ValidBytes(requestBody) ||
		openAIGroupAntiDistillationRequestHasOpaqueHistory(requestBody) || openAIGroupAntiDistillationRequestHasMedia(requestBody) {
		return
	}
	if startedAt.IsZero() {
		startedAt, _ = ctx.Value(groupAntiDistillationRequestStartKey{}).(time.Time)
	}
	// Internal callers without ingress timing use the measured request duration.
	if startedAt.IsZero() {
		startedAt = time.Now().Add(-result.Duration)
	}
	// Never trust caller-controlled request IDs for sample deduplication.
	if result.antiDistillationRequestID == "" {
		result.antiDistillationRequestID = uuid.NewString()
	}
	// A delivery callback retains only policy identifiers and scalar evidence,
	// never request contents, the mutable forwarding result or billing context.
	sampleKey := &APIKey{UserID: key.UserID, Group: &Group{ID: key.Group.ID, Platform: key.Group.Platform}}
	requestID := result.antiDistillationRequestID
	cacheReadTokens := result.Usage.CacheReadInputTokens
	protection := s.groupAntiDistillation
	observe := func() {
		sampleCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sampleCtx = WithGroupAntiDistillationRequestStart(sampleCtx, startedAt)
		protection.Observe(sampleCtx, sampleKey, requestID, cacheReadTokens)
	}
	if result.antiDistillationDelivery != nil {
		result.antiDistillationDelivery.register(observe)
		return
	}
	observe()
}

// A reference can hide prior media even when the current turn only sends text.
// Do not fetch or persist conversation contents to classify protection samples.
// Session/cache identity fields alone do not imply an upstream history reference.
func openAIGroupAntiDistillationRequestHasOpaqueHistory(body []byte) bool {
	view := openAIRequestPayloadView(body)
	for _, field := range []string{"previous_response_id", "conversation", "conversation_id", "thread_id"} {
		reference := view.Get(field)
		if reference.Exists() && reference.Type != gjson.Null &&
			(reference.Type != gjson.String || strings.TrimSpace(reference.String()) != "") {
			return true
		}
	}
	return false
}

// Upstreams may count image/audio input only in the total input tokens. Inspect
// the request too: an omitted media breakdown is not proof of a text request.
// Keep this evidence local; no request content is stored in protection state.
func openAIGroupAntiDistillationRequestHasMedia(body []byte) bool {
	view := openAIRequestPayloadView(body)
	if audio := view.Get("audio"); audio.Exists() && audio.Type != gjson.Null {
		return true
	}
	for _, field := range []string{"modalities", "response_modalities"} {
		for _, modality := range view.Get(field).Array() {
			switch strings.ToLower(strings.TrimSpace(modality.String())) {
			case "audio", "image", "video":
				return true
			}
		}
	}
	for _, field := range []string{"input", "messages"} {
		content := view.Get(field)
		if openAIJSONValueMayContainImageInput(content) || openAIGroupAntiDistillationContentHasMedia(content) {
			return true
		}
	}
	return false
}

func openAIGroupAntiDistillationContentHasMedia(value gjson.Result) bool {
	if value.IsArray() {
		for _, part := range value.Array() {
			if openAIGroupAntiDistillationContentHasMedia(part) {
				return true
			}
		}
	} else if value.IsObject() {
		if value.Get("image_url").Exists() {
			return true
		}
		switch strings.ToLower(strings.TrimSpace(value.Get("type").String())) {
		case "image", "image_url", "input_image", "output_image", "input_audio", "audio", "output_audio", "input_video", "video", "video_url":
			return true
		}
		for _, field := range []string{"content", "output"} {
			if openAIGroupAntiDistillationContentHasMedia(value.Get(field)) {
				return true
			}
		}
	}
	return false
}

func openAIGroupAntiDistillationUsageEligible(result *OpenAIForwardResult) bool {
	return result != nil && result.Usage.CacheReadKnown && !result.Usage.cacheReadMediaObserved && !result.ClientDisconnect &&
		result.SucceededForScheduling() && result.Usage.InputTokens > 0 && result.Usage.CacheReadInputTokens >= 0 &&
		!result.ImageEndpoint && result.ImageCount == 0 && result.VideoCount == 0 && result.AudioUsage == nil &&
		result.Usage.ImageInputTokens == 0 && result.Usage.ImageOutputTokens == 0
}

// openAIGroupAntiDistillationDelivery joins upstream completion with downstream
// terminal delivery without changing the relay's billing/concurrency callbacks.
// Registration and completion may race; at most one observation is executed.
type openAIGroupAntiDistillationDelivery struct {
	mu         sync.Mutex
	registered bool
	settled    bool
	delivered  bool
	observe    func()
}

func (d *openAIGroupAntiDistillationDelivery) register(observe func()) {
	if d == nil || observe == nil {
		return
	}
	d.mu.Lock()
	if d.registered {
		d.mu.Unlock()
		return
	}
	d.registered = true
	if !d.settled {
		d.observe = observe
		d.mu.Unlock()
		return
	}
	delivered := d.delivered
	d.mu.Unlock()
	if delivered {
		observe()
	}
}

func (d *openAIGroupAntiDistillationDelivery) complete(delivered bool) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	d.settled = true
	d.delivered = delivered
	observe := d.observe
	d.observe = nil
	d.mu.Unlock()
	if delivered && observe != nil {
		observe()
	}
}
