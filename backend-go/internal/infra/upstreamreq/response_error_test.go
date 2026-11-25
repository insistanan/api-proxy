package upstreamreq

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEndpointFailure_SeparatesAddressFromBusinessRejection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		failed bool
	}{
		{"认证", 401, "unauthorized", false},
		{"余额", 402, "payment required", false},
		{"权限", 403, "forbidden", false},
		{"限流", 429, "too many requests", false},
		{"请求参数", 400, "bad request", false},
		{"未知模型", 404, `{"error":{"code":"model_not_found"}}`, false},
		{"请求超时", 408, "timeout", true},
		{"服务故障", 500, "internal server error", true},
		{"网关故障", 502, "bad gateway", true},
		{"服务不可用", 503, "unavailable", true},
		{"500内的额度拒绝", 500, `{"error":{"code":"insufficient_quota"}}`, false},
		{"500内的审核拒绝", 500, `{"error":{"code":"content_policy_violation"}}`, false},
		{"模型容量", 503, `{"error":{"message":"model is at capacity"}}`, false},
		{"2xx内的认证拒绝", 200, `{"error":{"type":"authentication_error"}}`, false},
		{"2xx内的未知错误", 200, `{"error":{"message":"upstream failed"}}`, true},
		{"正常响应", 200, `{"error":null,"output":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.failed, IsEndpointFailure(tc.status, []byte(tc.body)))
		})
	}
}

func TestResponseError_UsesExplicitCodesAcrossEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		body string
		kind ResponseErrorKind
	}{
		{`{"error":{"code":"model_not_found","param":"model"}}`, ErrorModelUnsupported},
		{`{"error":{"code":"model_not_found","param":"tool"}}`, ErrorRequestRejected},
		{`{"response":{"error":{"code":"invalid_api_key"}}}`, ErrorCredential},
		{`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, ErrorRateLimited},
		{`{"type":"authentication_error"}`, ErrorCredential},
		{`{"error":{"code":"context_length_exceeded"}}`, ErrorRequestRejected},
		{`{"error":{"code":"server_error"}}`, ErrorUnknown},
		{`not json`, ErrorUnknown},
	} {
		t.Run(tc.body, func(t *testing.T) {
			require.Equal(t, tc.kind, ClassifyResponseError([]byte(tc.body)))
		})
	}
}
