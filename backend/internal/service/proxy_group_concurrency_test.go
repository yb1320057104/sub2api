package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPK52ProxyAccountConcurrencyValidation(t *testing.T) {
	s := &adminServiceImpl{}
	group := int64(1)
	for _, value := range []int{-1, 0, 10001} {
		_, err := s.AssignAccountProxyGroup(context.Background(), AccountProxyGroupAssignmentInput{AccountIDs: []int64{1}, ProxyGroupID: 1, ProxyConcurrency: 10, AccountConcurrency: &value})
		require.ErrorContains(t, err, "账号总并发")
		_, err = s.UpdateGroupProxyPriorityPolicies(context.Background(), GroupProxyPriorityUpdateInput{GroupIDs: []int64{1}, ProxyGroupID: &group, AssignProxies: true, ProxyConcurrency: 10, AccountConcurrency: &value})
		require.ErrorContains(t, err, "账号总并发")
	}
	value := 100
	_, err := s.UpdateGroupProxyPriorityPolicies(context.Background(), GroupProxyPriorityUpdateInput{GroupIDs: []int64{1}, AccountConcurrency: &value})
	require.ErrorContains(t, err, "同时勾选")
	for _, value := range []int{1, 100, 10000} {
		require.NoError(t, ValidateProxyGroupAccountConcurrency(&value, true))
	}
	require.NoError(t, ValidateProxyGroupAccountConcurrency(nil, false))
}

func TestPK52ProxyAccountConcurrencyJSONCompatibility(t *testing.T) {
	var legacy GroupProxyPriorityUpdateInput
	require.NoError(t, json.Unmarshal([]byte(`{"group_ids":[1],"strict_priority":false}`), &legacy))
	require.Nil(t, legacy.AccountConcurrency)
	var input GroupProxyPriorityUpdateInput
	require.NoError(t, json.Unmarshal([]byte(`{"group_ids":[1],"strict_priority":true,"assign_proxies":true,"proxy_group_id":2,"proxy_concurrency":10,"account_concurrency":100}`), &input))
	require.NotNil(t, input.AccountConcurrency)
	require.Equal(t, 100, *input.AccountConcurrency)
	for _, raw := range []string{`1.5`, `"100"`, `true`} {
		require.Error(t, json.Unmarshal([]byte(`{"strict_priority":true,"account_concurrency":`+raw+`}`), &input))
	}
}
