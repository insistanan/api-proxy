package upstreamreq

import "testing"

// TestClassifyUpstreamStatus 锁定"仅按状态码"的重试/配额分类表。
// 该表原先私有在 proxycore，visionlayer 因依赖方向无法复用而自行维护了
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
