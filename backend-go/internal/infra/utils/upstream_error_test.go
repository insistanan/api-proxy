package utils

import "testing"

// TestClassifyUpstreamStatus 锁定"仅按状态码"的重试/配额分类表。
// 该表原先私有在 handlers/proxycore，visionlayer 因依赖方向无法复用而自行维护了
// 一份只看状态码的判断；下沉到 utils 后此表是唯一出处，两条链路共用。
func TestClassifyUpstreamStatus(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		wantFailover bool
		wantQuota    bool
	}{
		// 认证/授权错误
		{"401 Unauthorized", 401, true, false},
		{"403 Forbidden", 403, true, false},

		// 配额/计费错误
		{"402 Payment Required", 402, true, true},
		{"429 Too Many Requests", 429, true, true},

		// 超时错误
		{"408 Request Timeout", 408, true, false},

		// 服务端错误
		{"500 Internal Server Error", 500, true, false},
		{"502 Bad Gateway", 502, true, false},
		{"503 Service Unavailable", 503, true, false},
		{"504 Gateway Timeout", 504, true, false},

		// 不应 failover 的客户端错误
		{"400 Bad Request", 400, false, false},
		{"404 Not Found", 404, false, false},
		{"405 Method Not Allowed", 405, false, false},
		{"413 Payload Too Large", 413, false, false},
		{"422 Unprocessable Entity", 422, false, false},

		// 成功状态码
		{"200 OK", 200, false, false},
		{"201 Created", 201, false, false},
		{"204 No Content", 204, false, false},

		// 重定向
		{"301 Moved Permanently", 301, false, false},
		{"302 Found", 302, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ClassifyUpstreamStatus(tt.statusCode)
			if gotFailover != tt.wantFailover {
				t.Errorf("ClassifyUpstreamStatus(%d) failover = %v, want %v", tt.statusCode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("ClassifyUpstreamStatus(%d) quota = %v, want %v", tt.statusCode, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestIsNonRetryableUpstreamErrorCode 测试不可重试错误码判断
func TestIsNonRetryableUpstreamErrorCode(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		// 内容审核相关 - 不应重试
		{"sensitive_words_detected", true},
		{"content_policy_violation", true},
		{"content_filter", true},
		{"content_blocked", true},
		{"moderation_blocked", true},
		// 请求内容无效 - 不应重试
		{"invalid_request", true},
		{"invalid_request_error", true},
		{"bad_request", true},
		// 大小写不敏感
		{"SENSITIVE_WORDS_DETECTED", true},
		{"Content_Policy_Violation", true},
		// 其他错误码 - 应该重试
		{"server_error", false},
		{"rate_limit", false},
		{"authentication_error", false},
		{"unknown_error", false},
		{"", false},
	}

	for _, tt := range tests {
		name := tt.code
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			got := IsNonRetryableUpstreamErrorCode(tt.code)
			if got != tt.want {
				t.Errorf("IsNonRetryableUpstreamErrorCode(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

// TestUpstreamErrorBodyPredicates 锁定响应体谓词的解析行为：
// 只有 error.code 命中才判 true；解析失败、字段缺失、空体一律 false
// （"没有证据说明不可重试"，由调用方的状态码判断决定，不在谓词里兜底）。
func TestUpstreamErrorBodyPredicates(t *testing.T) {
	tests := []struct {
		name              string
		body              string
		wantContentPolicy bool
		wantNonRetryable  bool
	}{
		{
			name:              "内容审核错误码",
			body:              `{"error":{"message":"sensitive words detected","code":"sensitive_words_detected"}}`,
			wantContentPolicy: true,
			wantNonRetryable:  true,
		},
		{
			name:              "请求内容无效不算内容审核",
			body:              `{"error":{"code":"invalid_request"}}`,
			wantContentPolicy: false,
			wantNonRetryable:  true,
		},
		{
			name:              "可重试错误码",
			body:              `{"error":{"code":"server_error"}}`,
			wantContentPolicy: false,
			wantNonRetryable:  false,
		},
		{
			name:              "无 error 对象",
			body:              `{"message":"boom"}`,
			wantContentPolicy: false,
			wantNonRetryable:  false,
		},
		{
			name:              "非 JSON",
			body:              `upstream exploded`,
			wantContentPolicy: false,
			wantNonRetryable:  false,
		},
		{
			name:              "空响应体",
			body:              "",
			wantContentPolicy: false,
			wantNonRetryable:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContentPolicyErrorBody([]byte(tt.body)); got != tt.wantContentPolicy {
				t.Errorf("IsContentPolicyErrorBody() = %v, want %v", got, tt.wantContentPolicy)
			}
			if got := IsNonRetryableUpstreamErrorBody([]byte(tt.body)); got != tt.wantNonRetryable {
				t.Errorf("IsNonRetryableUpstreamErrorBody() = %v, want %v", got, tt.wantNonRetryable)
			}
		})
	}
}

// 切片 A 反证：2xx 错误信封谓词的判定边界。
func TestIsUpstreamErrorEnvelope(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"对象信封命中", `{"error":{"message":"boom","type":"server_error"}}`, true},
		{"error:null 不命中（Responses 正常响应形态）", `{"id":"resp_1","error":null,"output":[]}`, false},
		{"error 为字符串不命中", `{"error":"something went wrong"}`, false},
		{"无 error 字段不命中", `{"id":"msg_1","role":"assistant"}`, false},
		{"顶层 error + choices 同样命中（Chat 错误 chunk 形态）", `{"error":{"message":"quota exceeded"},"choices":[]}`, true},
		{"非 JSON 不命中", `<html>Server Error</html>`, false},
		{"空 body 不命中", ``, false},
		{"JSON 数组顶层不命中", `[1,2,3]`, false},
		{"前导空白仍命中", "\n  {\"error\":{\"code\":\"x\"}}", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUpstreamErrorEnvelope([]byte(tt.body)); got != tt.want {
				t.Fatalf("IsUpstreamErrorEnvelope(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}
