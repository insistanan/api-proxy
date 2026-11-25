package proxycore

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/storage/logger"
)

func TestUsageFactDoesNotRequireRequestLogging(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	var facts []tokenusage.Fact
	ctx := tokenusage.WithRecorder(req.Context(), func(fact tokenusage.Fact) { facts = append(facts, fact) })
	c.Request = req.WithContext(logger.ContextWithAttemptID(ctx, "attempt-1"))
	now := time.Now()
	c.Set(availabilityObservationKey, &availabilityObservation{startedAt: now, model: "mapped-model", kind: "messages", sent: true})
	usage := &types.Usage{InputTokens: 200, OutputTokens: 5, CacheReadInputTokens: 500, CacheCreation5mInputTokens: 100}
	for range 2 {
		finishAttempt(c, AttemptLogContext{}, nil, "Messages", "request", "", "", "cancelled", 200, false, now, "cancelled", "", false, true, usage)
	}
	if len(facts) != 1 {
		t.Fatalf("同一次尝试预期只发出一条用量，得到 %d 条", len(facts))
	}
	fact := facts[0]
	if fact.AttemptID != "attempt-1" || fact.Model != "mapped-model" || fact.Source != tokenusage.SourceUpstream ||
		fact.Input != 200 || fact.Output != 5 || fact.CacheRead != 500 || fact.CacheCreation != 100 {
		t.Fatalf("用量快照与上游数据不符: %+v", fact)
	}
}

func TestUsageFactSeparatesUnattributedCacheAndRateLimitRejections(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	var facts []tokenusage.Fact
	c.Request = req.WithContext(tokenusage.WithRecorder(req.Context(), func(fact tokenusage.Fact) { facts = append(facts, fact) }))
	now := time.Now()
	usage := tokenusage.ExtractJSONUsage([]byte(`{"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":1000}}}`))
	recordAttemptUsage(c, &availabilityObservation{sent: true}, "累积缓存", now, usage)
	recordAttemptUsage(c, &availabilityObservation{upstreamStatusCode: 429, body: `{"error":{"message":"限流"}}`}, "限流拒绝", now, nil)
	recordAttemptUsage(c, &availabilityObservation{upstreamStatusCode: 429, body: `{"error":{"message":"限流"},"usage":{"input_tokens":5,"output_tokens":3}}`}, "报告用量", now, nil)
	recordAttemptUsage(c, &availabilityObservation{upstreamStatusCode: 429}, "未知结果", now, nil)
	if len(facts) != 3 {
		t.Fatalf("预期保留可核实用量及未知结果，得到 %+v", facts)
	}
	if facts[0].Input != 100 || facts[0].CacheRead != 0 || facts[0].Output != 20 || facts[0].Source != tokenusage.SourceUpstream {
		t.Fatalf("账本计入了无法归属本次请求的缓存: %+v", facts[0])
	}
	if facts[1].AttemptID != "报告用量" || facts[1].Input != 5 || facts[1].Output != 3 || facts[2].Source != tokenusage.SourceMissing {
		t.Fatalf("明确拒绝、有用量和未知结果未区分: %+v", facts)
	}
}

func TestUsageFactCountsCacheExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		input, read int64
	}{
		{"缓存子集", `{"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":70}}}`, 30, 70},
		{"全部缓存", `{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":100}}}`, 0, 100},
		{"独立缓存", `{"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":500}}`, 100, 500},
		{"累积缓存", `{"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":1000}}}`, 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			var got tokenusage.Fact
			c.Request = req.WithContext(tokenusage.WithRecorder(req.Context(), func(fact tokenusage.Fact) { got = fact }))
			usage := tokenusage.ExtractJSONUsage([]byte(tc.body))
			recordAttemptUsage(c, &availabilityObservation{sent: true}, tc.name, time.Now(), usage)
			if got.Source != tokenusage.SourceUpstream || got.Input != tc.input || got.CacheRead != tc.read || got.Output != 20 {
				t.Fatalf("用量归属不符: %+v", got)
			}
		})
	}
}
