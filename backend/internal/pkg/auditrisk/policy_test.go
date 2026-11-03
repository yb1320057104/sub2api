package auditrisk

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRiskPolicyBoundariesAndNonMonotonicActions(t *testing.T) {
	p := &Policy{20, 40, 60, Block, ChargeAllow, Allow}
	require.NoError(t, p.Validate())
	for _, tc := range []struct {
		score         float64
		level, action string
	}{
		{0, "none", Allow}, {19, "none", Allow}, {20, "low", Block}, {39, "low", Block},
		{40, "medium", ChargeAllow}, {59, "medium", ChargeAllow}, {60, "high", Allow}, {100, "high", Allow},
	} {
		level, action := p.Decide(tc.score)
		require.Equal(t, tc.level, level)
		require.Equal(t, tc.action, action)
	}
	p.LowThreshold = 0
	level, action := p.Decide(0)
	require.Equal(t, "none", level)
	require.Equal(t, Allow, action)
}

func TestRiskPolicyUpdateOmissionNullAndStrictValidation(t *testing.T) {
	p := &Policy{20, 40, 60, Allow, Allow, Block}
	kept, err := Update(p, nil)
	require.NoError(t, err)
	require.Equal(t, p, kept)
	kept.LowThreshold = 10
	require.Equal(t, 20, p.LowThreshold)
	removed, err := Update(p, json.RawMessage("null"))
	require.NoError(t, err)
	require.Nil(t, removed)
	for _, raw := range []string{
		`{}`, `[]`, `{"low_threshold":20}`, `{"low_threshold":null,"medium_threshold":40,"high_threshold":60,"low_action":"allow","medium_action":"allow","high_action":"block"}`,
		`{"low_threshold":40,"medium_threshold":40,"high_threshold":60,"low_action":"allow","medium_action":"allow","high_action":"block"}`,
		`{"low_threshold":20.5,"medium_threshold":40,"high_threshold":60,"low_action":"allow","medium_action":"allow","high_action":"block"}`,
		`{"low_threshold":20,"medium_threshold":40,"high_threshold":101,"low_action":"allow","medium_action":"allow","high_action":"block"}`,
		`{"low_threshold":20,"medium_threshold":40,"high_threshold":60,"low_action":"warn","medium_action":"allow","high_action":"block"}`,
	} {
		_, err := Update(p, json.RawMessage(raw))
		require.Error(t, err, raw)
	}
}
