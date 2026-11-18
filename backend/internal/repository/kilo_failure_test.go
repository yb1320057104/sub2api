package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKiloFailureReleaseReason(t *testing.T) {
	for _, tc := range []struct {
		name, raw, code string
		status          int
		release         bool
		reason          string
	}{
		{"default_401", `{}`, "", 401, true, "http_401"},
		{"default_402", `{}`, "", 402, true, "http_402"},
		{"default_403", `{}`, "", 403, true, "http_403"},
		{"custom_status", `{"auto_release_statuses":[429]}`, "", 429, true, "http_429"},
		{"explicit_empty", `{"auto_release_statuses":[]}`, "", 401, false, ""},
		{"empty_disables_code", `{"auto_release_statuses":[]}`, "account_restricted", 400, false, ""},
		{"structured_restriction", `{}`, "account_restricted", 400, true, "account_restricted"},
		{"sse_restriction", `{}`, "account_restricted", 200, true, "account_restricted"},
		{"structured_sharing_limit", `{}`, "subscription_sharing_usage_limit_exceeded", 429, true, "subscription_sharing_usage_limit_exceeded"},
		{"unknown_code", `{}`, "quota_exceeded", 429, false, ""},
		{"body_substring", `{}`, "prefix account_restricted suffix", 400, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release, reason, err := kiloFailureReleaseReason([]byte(tc.raw), tc.status, tc.code)
			require.NoError(t, err)
			require.Equal(t, tc.release, release)
			require.Equal(t, tc.reason, reason)
		})
	}
}
