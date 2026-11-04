package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPK51ProxyGroupPolicyJSONPreservesOmittedSelection(t *testing.T) {
	var malformed GroupProxyPriorityUpdateInput
	require.Error(t, json.Unmarshal([]byte(`{"group_ids":[1]}`), &malformed))
	require.Error(t, json.Unmarshal([]byte(`{"group_ids":[1],"strict_priority":null}`), &malformed))
	var members ProxyGroupMembersInput
	require.Error(t, json.Unmarshal([]byte(`{"proxy_ids":[1]}`), &members))
	require.NoError(t, json.Unmarshal([]byte(`{"proxy_ids":[1],"proxy_group_id":null}`), &members))
	for _, tc := range []struct {
		name, body string
		set        bool
		id         *int64
	}{
		{"omitted", `{"group_ids":[1],"strict_priority":false}`, false, nil},
		{"clear", `{"group_ids":[1],"strict_priority":false,"proxy_group_id":null}`, true, nil},
		{"selected", `{"group_ids":[1],"strict_priority":true,"proxy_group_id":4}`, true, func() *int64 { v := int64(4); return &v }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input GroupProxyPriorityUpdateInput
			require.NoError(t, json.Unmarshal([]byte(tc.body), &input))
			require.Equal(t, tc.set, input.ProxyGroupIDSet)
			require.Equal(t, tc.id, input.ProxyGroupID)
		})
	}
}

func TestPK51ProxyGroupValidationStopsBeforeRepositories(t *testing.T) {
	s := &adminServiceImpl{}
	for _, input := range []ProxyGroupInput{{Name: " "}, {Name: strings.Repeat("字", 101)}, {Name: "ok", Description: strings.Repeat("字", 1001)}} {
		_, err := s.SaveProxyGroup(context.Background(), 0, input)
		require.Error(t, err)
		require.Contains(t, err.Error(), "IP分组名称")
	}
	input := ProxyGroupInput{Name: " 静态IP ", Description: " notes "}
	require.NoError(t, ValidateProxyGroupInput(&input))
	require.Equal(t, "静态IP", input.Name)
	require.Equal(t, "notes", input.Description)
	for _, input := range []AccountProxyGroupAssignmentInput{{ProxyGroupID: 1, ProxyConcurrency: 10}, {AccountIDs: []int64{1}, ProxyGroupID: 1, ProxyConcurrency: 0}, {AccountIDs: []int64{1}, ProxyGroupID: 0, ProxyConcurrency: 10}, {AccountIDs: []int64{-1}, ProxyGroupID: 1, ProxyConcurrency: 10}} {
		_, err := s.AssignAccountProxyGroup(context.Background(), input)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "unavailable")
	}
	for _, input := range []GroupProxyPriorityUpdateInput{{StrictPriority: true}, {GroupIDs: []int64{1}, AssignProxies: true}, {GroupIDs: []int64{1}, ProxyGroupID: func() *int64 { v := int64(-1); return &v }()}} {
		_, err := s.UpdateGroupProxyPriorityPolicies(context.Background(), input)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "unavailable")
	}
}
