package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestKiloProxyAssignmentMixedAccountsCannotShareAliases(t *testing.T) {
	for _, ids := range [][]int64{{10, 11}, {11, 10}} {
		policy := &kiloProxyAssignmentPolicy{managed: map[int64]bool{10: true}, fingerprints: map[int64]string{1: "same", 2: "same"}, owners: map[string]int64{}, liveOwners: map[string]map[int64]bool{}}
		input := service.AccountProxyAssignmentInput{ProxiesPerAccount: 1, MaxAccountsPerProxy: 1, ProxyConcurrency: 1}
		plan, _, err := planAccountProxyAssignments(ids, []int64{1, 2}, nil, nil, input, policy)
		require.Error(t, err, "a managed account must not share credential aliases with an ordinary account in either order")
		require.Nil(t, plan)
	}
}

func TestKiloProxyAssignmentOwnershipAndAliases(t *testing.T) {
	policy := &kiloProxyAssignmentPolicy{
		managed:      map[int64]bool{10: true, 11: true},
		fingerprints: map[int64]string{1: "free", 2: "own", 3: "foreign", 4: "own", 5: "sealed", 6: "live", 7: "other-free"},
		owners:       map[string]int64{"own": 10, "foreign": 11, "sealed": 0},
		liveOwners:   map[string]map[int64]bool{"live": {99: true}},
	}
	input := service.AccountProxyAssignmentInput{ProxiesPerAccount: 2, MaxAccountsPerProxy: 1, ProxyConcurrency: 5}
	plan, _, err := planAccountProxyAssignments([]int64{10}, []int64{1, 2, 3, 4, 5, 6, 7}, nil, nil, input, policy)
	require.NoError(t, err)
	require.Equal(t, int64(2), plan[10][0].ProxyID, "reuse the currently occupied credential before a free credential")
	require.Equal(t, int64(1), plan[10][1].ProxyID, "a transport alias cannot count as an independent exit")
	input.ProxiesPerAccount = 1
	plan, _, err = planAccountProxyAssignments([]int64{10, 11}, []int64{1, 7}, nil, nil, input, policy)
	require.NoError(t, err)
	require.NotEqual(t, plan[10][0].ProxyID, plan[11][0].ProxyID)
	policy.fingerprints[7] = "free"
	plan, _, err = planAccountProxyAssignments([]int64{10, 11}, []int64{1, 7}, nil, nil, input, policy)
	require.Error(t, err, "aliases cannot be allocated to two managed accounts in the same plan")
	require.Nil(t, plan)
	plan, _, err = planAccountProxyAssignments([]int64{10}, []int64{3, 5, 6}, nil, nil, input, policy)
	require.Error(t, err, "foreign, sealed and live aliases are unavailable")
	require.Nil(t, plan)
}
