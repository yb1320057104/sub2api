package service

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cacheconversion"
	"github.com/gin-gonic/gin"
)

func firstCacheConversionAccount(accounts []*Account) *Account {
	if len(accounts) > 0 {
		return accounts[0]
	}
	return nil
}

// Serialize a separate client copy; billing must retain the original usage.
func writeCacheConvertedJSON(c *gin.Context, account *Account, value any, protocol cacheconversion.Protocol) error {
	if !account.IsCacheConversionEnabled() {
		c.JSON(http.StatusOK, value)
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	body, err = cacheconversion.RewriteJSON(body, OpenAICacheConversionConfig(account), protocol)
	if err != nil {
		return err
	}
	c.Writer.Header().Del("Content-Length")
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
	return nil
}

// Compat serializers emit one JSON data line per event. Preserve event names,
// comments, blank lines and the DONE sentinel while rewriting that JSON copy.
func rewriteCacheConvertedSSE(account *Account, sse string, protocol cacheconversion.Protocol) (string, error) {
	if !account.IsCacheConversionEnabled() {
		return sse, nil
	}
	lines := strings.Split(sse, "\n")
	for i, line := range lines {
		payload, ok := extractOpenAISSEDataLine(line)
		if !ok || strings.TrimSpace(payload) == "" || strings.TrimSpace(payload) == "[DONE]" {
			continue
		}
		body, err := cacheconversion.RewriteJSON([]byte(payload), OpenAICacheConversionConfig(account), protocol)
		if err != nil {
			return "", err
		}
		if string(body) != payload {
			lines[i] = "data: " + string(body)
		}
	}
	return strings.Join(lines, "\n"), nil
}
