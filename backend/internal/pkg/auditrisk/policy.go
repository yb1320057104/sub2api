// Package auditrisk defines deterministic audit risk bands, not probabilities.
package auditrisk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

const (
	Allow       = "allow"
	Block       = "block"
	ChargeAllow = "charge_allow"
)

type Policy struct {
	LowThreshold    int    `json:"low_threshold"`
	MediumThreshold int    `json:"medium_threshold"`
	HighThreshold   int    `json:"high_threshold"`
	LowAction       string `json:"low_action"`
	MediumAction    string `json:"medium_action"`
	HighAction      string `json:"high_action"`
}

func (p *Policy) Validate() error {
	if p == nil {
		return nil
	}
	if p.LowThreshold < 0 || p.LowThreshold >= p.MediumThreshold || p.MediumThreshold >= p.HighThreshold || p.HighThreshold > 100 {
		return fmt.Errorf("风险阈值必须为 0～100 的整数，且低危 < 中危 < 高危")
	}
	for _, action := range []string{p.LowAction, p.MediumAction, p.HighAction} {
		if action != Allow && action != Block && action != ChargeAllow {
			return fmt.Errorf("风险等级处理方式必须为拦截、放行或扣费放行")
		}
	}
	return nil
}

// Decide returns the band and action for a complete, maximum-match score.
// No match and zero scores always pass, including when the low threshold is 0.
func (p *Policy) Decide(score float64) (level, action string) {
	if p == nil || score <= 0 || math.IsNaN(score) || math.IsInf(score, 0) {
		return "none", Allow
	}
	switch {
	case score >= float64(p.HighThreshold):
		return "high", p.HighAction
	case score >= float64(p.MediumThreshold):
		return "medium", p.MediumAction
	case score >= float64(p.LowThreshold):
		return "low", p.LowAction
	default:
		return "none", Allow
	}
}

func Clone(p *Policy) *Policy {
	if p == nil {
		return nil
	}
	clone := *p
	return &clone
}

// Update preserves omitted fields and treats an explicit null as disabling the
// policy. Strict object decoding also prevents partial policies becoming active.
func Update(current *Policy, raw json.RawMessage) (*Policy, error) {
	if len(raw) == 0 {
		return Clone(current), nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("风险分级配置格式无效")
	}
	for _, key := range []string{"low_threshold", "medium_threshold", "high_threshold", "low_action", "medium_action", "high_action"} {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("风险分级配置必须包含全部阈值和处理方式")
		}
	}
	var policy Policy
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("风险分级配置格式无效")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &policy, nil
}
