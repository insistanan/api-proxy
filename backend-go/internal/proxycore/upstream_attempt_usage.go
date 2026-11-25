package proxycore

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func recordAttemptUsage(c *gin.Context, observation *availabilityObservation, attemptID string, at time.Time, usage *types.Usage) {
	if c == nil || c.Request == nil || observation == nil || (!observation.sent && observation.upstreamStatusCode == 0) {
		return
	}
	record := tokenusage.RecorderFrom(c.Request.Context())
	if record == nil {
		return
	}
	if usage == nil && observation.body != "" {
		usage = tokenusage.ExtractJSONUsage([]byte(observation.body))
	}
	fact := tokenusage.Fact{AttemptID: attemptID, At: at, Protocol: observation.kind, Model: observation.model,
		Source: tokenusage.SourceUpstream}
	input, output := usageInputTokens(usage), usageOutputTokens(usage)
	creation, read := usageCacheCreationTokens(usage), usageCacheReadTokens(usage)
	if usage != nil && usage.UnattributedCacheInput != nil {
		input, read = *usage.UnattributedCacheInput, 0
	}
	switch {
	case usage == nil && rateLimitRejected(observation):
		// 明确的限流拒绝不属于执行后缺失用量的尝试。
		return
	case input < 0 || output < 0 || creation < 0 || read < 0,
		input == 0 && output == 0 && creation == 0 && read == 0:
		fact.Source = tokenusage.SourceMissing
	default:
		fact.Input, fact.Output = int64(input), int64(output)
		fact.CacheCreation, fact.CacheRead = int64(creation), int64(read)
		if usage.Estimated {
			fact.Source = tokenusage.SourceEstimated
		}
	}
	record(fact)
}

func rateLimitRejected(observation *availabilityObservation) bool {
	if observation.upstreamStatusCode != http.StatusTooManyRequests {
		return false
	}
	var body struct {
		Error json.RawMessage `json:"error"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(observation.body), &body); err != nil {
		return false
	}
	// 只识别标准拒绝信封；携带用量、截断或无法识别的响应仍标记缺失。
	return len(body.Error) > 0 && string(body.Error) != "null" && (len(body.Usage) == 0 || string(body.Usage) == "null")
}
