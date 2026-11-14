package admin

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupRequestsDecodeForceOpenAIFast(t *testing.T) {
	var createReq CreateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"fast","force_openai_fast":true,"force_openai_ultrafast":true,"free_openai_fast":true}`), &createReq))
	require.True(t, createReq.ForceOpenAIFast)
	require.True(t, createReq.ForceOpenAIUltrafast)
	require.True(t, createReq.FreeOpenAIFast)

	var updateReq UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"force_openai_fast":false,"force_openai_ultrafast":false,"free_openai_fast":false}`), &updateReq))
	require.NotNil(t, updateReq.ForceOpenAIFast)
	require.False(t, *updateReq.ForceOpenAIFast)
	require.NotNil(t, updateReq.ForceOpenAIUltrafast)
	require.False(t, *updateReq.ForceOpenAIUltrafast)
	require.NotNil(t, updateReq.FreeOpenAIFast)
	require.False(t, *updateReq.FreeOpenAIFast)

	var omitted UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omitted))
	require.Nil(t, omitted.ForceOpenAIFast)
	require.Nil(t, omitted.ForceOpenAIUltrafast)
	require.Nil(t, omitted.FreeOpenAIFast)
}

func TestGroupUltrafastAdminExportImportCompatibility(t *testing.T) {
	payload, err := json.Marshal(dto.GroupFromServiceAdmin(&service.Group{
		Name: "ultrafast-export", Platform: service.PlatformOpenAI, ForceOpenAIUltrafast: true,
	}))
	require.NoError(t, err)
	var createReq CreateGroupRequest
	require.NoError(t, json.Unmarshal(payload, &createReq))
	require.True(t, createReq.ForceOpenAIUltrafast)
	var updateReq UpdateGroupRequest
	require.NoError(t, json.Unmarshal(payload, &updateReq))
	require.NotNil(t, updateReq.ForceOpenAIUltrafast)
	require.True(t, *updateReq.ForceOpenAIUltrafast)

	var legacy CreateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"legacy","platform":"openai"}`), &legacy))
	require.False(t, legacy.ForceOpenAIUltrafast)
}
