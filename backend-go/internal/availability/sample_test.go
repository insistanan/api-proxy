package availability

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClassifySeparatesUpstreamFailuresFromRequestRejection(t *testing.T) {
	cases := []struct {
		name, errorType, body, want string
		status                      int
		success                     bool
	}{
		{"完整响应", "", "", Success, 200, true},
		{"流中断不能按200成功", "response_processing", "unexpected EOF", Failure, 200, false},
		{"客户端取消", "client_cancelled", "", Ignored, 502, false},
		{"本地排队", "local_admission", "", Ignored, 503, false},
		{"模型明确不存在", "upstream_4xx", `{"error":{"code":"model_not_found"}}`, Unsupported, 404, false},
		{"普通404不推断模型", "upstream_4xx", `{"error":{"message":"route not found"}}`, Ignored, 404, false},
		{"上下文超限", "upstream", `{"error":{"code":"context_length_exceeded"}}`, Ignored, 400, false},
		{"流内审核拒绝", "response_processing", `{"response":{"error":{"code":"content_policy_violation"}}}`, Ignored, 502, false},
		{"无效凭据", "auth", "", Credential, 401, false},
		{"额度耗尽区别于限流", "quota", `{"error":{"code":"insufficient_quota"}}`, Credential, 429, false},
		{"限流可恢复", "rate_limit", "", RateLimited, 429, false},
		{"数字错误码仍识别状态", "upstream", `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, RateLimited, 429, false},
		{"工具模型错误不否定主模型", "upstream", `{"error":{"code":"model_not_found","param":"tools[0].model"}}`, Ignored, 400, false},
		{"上游超时", "upstream", "", Failure, 408, false},
		{"服务不可用", "upstream_5xx", "", Failure, 503, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, reason := Classify(tc.success, tc.status, tc.errorType, tc.body)
			require.Equal(t, tc.want, outcome)
			require.NotEmpty(t, reason)
		})
	}
}

func TestCooldownUsesSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	require.Equal(t, now.Add(90*time.Second).UnixMilli(), Cooldown("90", now))
	require.Equal(t, now.Add(time.Minute).UnixMilli(), Cooldown(now.Add(time.Minute).Format(http.TimeFormat), now))
	require.Equal(t, now.Add(30*time.Second).UnixMilli(), Cooldown("invalid", now))
}
