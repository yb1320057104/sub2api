package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Disconnect removes the replaceable ChatGPT grant, not Kilo's login provider.
// cloud@4f7acd6e routes this mutation through baseProcedure (Gateway Bearer).
func (c *kiloRemoteClient) Disconnect(ctx context.Context, key, organization, proxy string) error {
	key, err := kiloRemoteCredential(key)
	if err != nil {
		return err
	}
	organization = strings.TrimSpace(organization)
	if !kiloValidOrganization(organization) {
		return kiloBad("KILO_ORGANIZATION_INVALID", "Kilo组织编号无效")
	}
	input := struct {
		OrganizationID string `json:"organizationId,omitempty"`
	}{organization}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	status, raw, err := c.request(ctx, http.MethodPost, kiloRemoteAPIBase+"/api/trpc/openAiChatGpt.disconnect", key, proxy, string(payload))
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return kiloRemoteResponseError(status, raw, false)
	}
	var envelope struct {
		Result *struct {
			Data struct {
				State string `json:"state"`
			} `json:"data"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Result == nil || len(envelope.Error) > 0 && string(envelope.Error) != "null" || envelope.Result.Data.State != "disconnected" {
		return kiloRemoteShapeError()
	}
	return nil
}

var _ KiloConnectionMutator = (*kiloRemoteClient)(nil)
