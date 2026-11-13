package proxycore

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestIsPromptCacheKeyUnsupported(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{
			name:       "兼容网关拒绝未知参数",
			statusCode: 400,
			body:       `{"error":{"message":"Validation: Unsupported parameter(s): \u0060prompt_cache_key\u0060","type":"bad_response_status_code"}}`,
			want:       true,
		},
		{
			name:       "参数必填不是不支持",
			statusCode: 400,
			body:       `{"error":{"message":"prompt_cache_key is required"}}`,
			want:       false,
		},
		{
			name:       "其他字段不触发",
			statusCode: 400,
			body:       `{"error":{"message":"Unsupported parameter: temperature"}}`,
			want:       false,
		},
		{
			name:       "服务端错误不触发",
			statusCode: 500,
			body:       `{"error":{"message":"Unsupported parameter: prompt_cache_key"}}`,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPromptCacheKeyUnsupported(tt.statusCode, []byte(tt.body)); got != tt.want {
				t.Fatalf("IsPromptCacheKeyUnsupported() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsReasoningContentRequired(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{
			name:       "DeepSeek strict thinking error",
			statusCode: 400,
			body:       `{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API."}}`,
			want:       true,
		},
		{
			name:       "missing required phrase",
			statusCode: 400,
			body:       `{"error":{"message":"reasoning_content is invalid"}}`,
			want:       false,
		},
		{
			name:       "server failure is not capability",
			statusCode: 500,
			body:       `{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API."}}`,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReasoningContentRequired(tt.statusCode, []byte(tt.body)); got != tt.want {
				t.Fatalf("IsReasoningContentRequired() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClassifyMessage 测试基于错误消息的分类
func TestClassifyMessage(t *testing.T) {
	tests := []struct {
		name         string
		message      string
		wantFailover bool
		wantQuota    bool
	}{
		// 配额相关
		{"insufficient credits", "You have insufficient credits", true, true},
		{"quota exceeded", "API quota exceeded for this month", true, true},
		{"rate limit", "Rate limit exceeded, please retry later", true, true},
		{"balance", "Account balance is zero", true, true},
		{"billing", "Billing issue detected", true, true},
		{"中文-积分不足", "您的积分不足，请充值", true, true},
		{"中文-余额不足", "账户余额不足", true, true},
		{"中文-请求数限制", "已达到请求数限制", true, true},

		// 认证相关
		{"invalid api key", "Invalid API key provided", true, false},
		{"unauthorized", "Unauthorized access", true, false},
		{"token expired", "Your token has expired", true, false},
		{"permission denied", "Permission denied for this resource", true, false},
		{"中文-密钥无效", "密钥无效，请检查", true, false},

		// 临时错误
		{"timeout", "Request timeout, please retry", true, false},
		{"server overloaded", "Server is overloaded", true, false},
		{"temporarily unavailable", "Service temporarily unavailable", true, false},
		{"中文-超时", "请求超时", true, false},

		// 不应 failover
		{"normal error", "Something went wrong", false, false},
		{"validation error", "Field 'name' is required", false, false},
		{"empty message", "", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := classifyMessage(tt.message)
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyMessage(%q) failover = %v, want %v", tt.message, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyMessage(%q) quota = %v, want %v", tt.message, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestClassifyErrorType 测试基于错误类型的分类
func TestClassifyErrorType(t *testing.T) {
	tests := []struct {
		name         string
		errType      string
		wantFailover bool
		wantQuota    bool
	}{
		// 配额相关
		{"over_quota", "over_quota", true, true},
		{"quota_exceeded", "quota_exceeded", true, true},
		{"rate_limit_exceeded", "rate_limit_exceeded", true, true},
		{"billing_error", "billing_error", true, true},
		{"insufficient_funds", "insufficient_funds", true, true},

		// 认证相关
		{"authentication_error", "authentication_error", true, false},
		{"invalid_api_key", "invalid_api_key", true, false},
		{"permission_denied", "permission_denied", true, false},

		// 服务端错误
		{"server_error", "server_error", true, false},
		{"internal_error", "internal_error", true, false},
		{"service_unavailable", "service_unavailable", true, false},

		// 不应 failover
		{"invalid_request", "invalid_request", false, false},
		{"validation_error", "validation_error", false, false},
		{"unknown_error", "unknown_error", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := classifyErrorType(tt.errType)
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyErrorType(%q) failover = %v, want %v", tt.errType, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyErrorType(%q) quota = %v, want %v", tt.errType, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestClassifyByErrorMessage 测试基于响应体的分类
func TestClassifyByErrorMessage(t *testing.T) {
	tests := []struct {
		name         string
		body         map[string]interface{}
		wantFailover bool
		wantQuota    bool
	}{
		{
			name: "quota error in message",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "You have exceeded your quota",
					"type":    "error",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "auth error in message",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Invalid API key",
					"type":    "error",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "quota error in type",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Error occurred",
					"type":    "over_quota",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "server error in type",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Error occurred",
					"type":    "server_error",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "no failover keywords",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Bad request format",
					"type":    "invalid_request",
				},
			},
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:         "empty body",
			body:         map[string]interface{}{},
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name: "no error field",
			body: map[string]interface{}{
				"status": "error",
			},
			wantFailover: false,
			wantQuota:    false,
		},
		// upstream_error 字段支持（Responses API 错误格式）
		{
			name: "upstream_error string field - auth error",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":           "upstream_error",
					"upstream_error": "Invalid API key provided",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "upstream_error string field - quota error",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":           "upstream_error",
					"upstream_error": "Rate limit exceeded, please retry later",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "upstream_error nested object with message",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type": "upstream_error",
					"upstream_error": map[string]interface{}{
						"message": "Insufficient credits",
					},
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "detail field - auth error",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":   "error",
					"detail": "Token expired, please refresh",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.body)
			gotFailover, gotQuota := classifyByErrorMessage(bodyBytes, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyByErrorMessage() failover = %v, want %v", gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyByErrorMessage() quota = %v, want %v", gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestClassifyByErrorMessage_InvalidJSON 测试无效 JSON 的处理
func TestClassifyByErrorMessage_InvalidJSON(t *testing.T) {
	invalidBodies := [][]byte{
		[]byte("not json"),
		[]byte("{invalid}"),
		[]byte(""),
		nil,
	}

	for _, body := range invalidBodies {
		gotFailover, gotQuota := classifyByErrorMessage(body, "Messages")
		if gotFailover || gotQuota {
			t.Errorf("classifyByErrorMessage(%q) should return (false, false) for invalid JSON", string(body))
		}
	}
}

// TestShouldRetryWithNextKey_403WithPredeductQuotaError 测试 403 + 预扣费额度失败的场景
// 这是生产环境实际发生的错误格式
func TestShouldRetryWithNextKey_403WithPredeductQuotaError(t *testing.T) {
	// 使用生产环境的精确 JSON 格式
	body := []byte(`{"error":{"type":"new_api_error","message":"预扣费额度失败, 用户剩余额度: ¥0.053950, 需要预扣费额度: ¥0.191160, 下次重置时间: 2025-01-01 00:00:00"},"type":"error"}`)

	gotFailover, gotQuota := ShouldRetryWithNextKey(403, body, false, "Messages")

	if !gotFailover {
		t.Errorf("ShouldRetryWithNextKey(403, prededuct_error, false) failover = %v, want true", gotFailover)
	}
	if !gotQuota {
		t.Errorf("ShouldRetryWithNextKey(403, prededuct_error, false) quota = %v, want true", gotQuota)
	}
}

// TestClassifyMessage_ChineseQuotaKeywords 测试中文额度关键词
func TestClassifyMessage_ChineseQuotaKeywords(t *testing.T) {
	tests := []struct {
		name         string
		message      string
		wantFailover bool
		wantQuota    bool
	}{
		{"预扣费额度失败", "预扣费额度失败, 用户剩余额度: ¥0.053950", true, true},
		{"额度不足", "账户额度不足", true, true},
		{"预扣费失败", "预扣费失败，请充值", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := classifyMessage(tt.message)
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyMessage(%q) failover = %v, want %v", tt.message, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyMessage(%q) quota = %v, want %v", tt.message, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestShouldRetryWithNextKey 测试完整的重试判断逻辑
func TestShouldRetryWithNextKey(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         map[string]interface{}
		wantFailover bool
		wantQuota    bool
	}{
		// 403 + 中文配额相关消息
		{
			name:       "403 with chinese quota message",
			statusCode: 403,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":    "new_api_error",
					"message": "预扣费额度失败, 用户剩余额度: ¥0.053950",
				},
				"type": "error",
			},
			wantFailover: true,
			wantQuota:    true,
		},
		// 状态码优先
		{
			name:         "401 always failover",
			statusCode:   401,
			body:         map[string]interface{}{},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "402 always failover with quota",
			statusCode:   402,
			body:         map[string]interface{}{},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:         "408 always failover",
			statusCode:   408,
			body:         map[string]interface{}{},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "500 always failover",
			statusCode:   500,
			body:         map[string]interface{}{},
			wantFailover: true,
			wantQuota:    false,
		},
		// 400 需要检查消息体
		{
			name:       "400 with quota message",
			statusCode: 400,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Quota exceeded",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:       "400 with auth message",
			statusCode: 400,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Invalid API key",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:       "400 without failover keywords",
			statusCode: 400,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Bad request",
				},
			},
			wantFailover: false,
			wantQuota:    false,
		},
		// 404 不应 failover
		{
			name:         "404 never failover",
			statusCode:   404,
			body:         map[string]interface{}{},
			wantFailover: false,
			wantQuota:    false,
		},
		// 200 不应 failover
		{
			name:         "200 never failover",
			statusCode:   200,
			body:         map[string]interface{}{},
			wantFailover: false,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.body)
			// 测试非 Fuzzy 模式（精确错误分类）
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, bodyBytes, false, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("shouldRetryWithNextKey(%d, ..., false) failover = %v, want %v", tt.statusCode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("shouldRetryWithNextKey(%d, ..., false) quota = %v, want %v", tt.statusCode, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestShouldRetryWithNextKeyFuzzyMode 测试 Fuzzy 模式下的错误分类
// Fuzzy 模式：所有非 2xx 错误都触发 failover
func TestShouldRetryWithNextKeyFuzzyMode(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		wantFailover bool
		wantQuota    bool
	}{
		// 2xx 成功响应不 failover
		{
			name:         "200 OK - no failover",
			statusCode:   200,
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:         "201 Created - no failover",
			statusCode:   201,
			wantFailover: false,
			wantQuota:    false,
		},
		// 3xx 重定向在 Fuzzy 模式下触发 failover
		{
			name:         "301 Redirect - failover in fuzzy mode",
			statusCode:   301,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "302 Found - failover in fuzzy mode",
			statusCode:   302,
			wantFailover: true,
			wantQuota:    false,
		},
		// 4xx 客户端错误在 Fuzzy 模式下都触发 failover
		{
			name:         "400 Bad Request - failover in fuzzy mode",
			statusCode:   400,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "401 Unauthorized - failover in fuzzy mode",
			statusCode:   401,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "402 Payment Required - failover with quota",
			statusCode:   402,
			wantFailover: true,
			wantQuota:    true, // 配额相关
		},
		{
			name:         "403 Forbidden - failover in fuzzy mode",
			statusCode:   403,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "404 Not Found - failover in fuzzy mode",
			statusCode:   404,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "422 Unprocessable Entity - failover in fuzzy mode",
			statusCode:   422,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "429 Too Many Requests - failover with quota",
			statusCode:   429,
			wantFailover: true,
			wantQuota:    true, // 配额相关
		},
		// 请求尺寸类 4xx：换 Key/换渠道不会改变请求体大小，不 failover
		{
			name:         "411 Length Required - no failover (request size error)",
			statusCode:   411,
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:         "413 Payload Too Large - no failover (request size error)",
			statusCode:   413,
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:         "414 URI Too Long - no failover (request size error)",
			statusCode:   414,
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:         "431 Request Header Fields Too Large - no failover (request size error)",
			statusCode:   431,
			wantFailover: false,
			wantQuota:    false,
		},
		// 5xx 服务端错误在 Fuzzy 模式下触发 failover
		{
			name:         "500 Internal Server Error - failover in fuzzy mode",
			statusCode:   500,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "502 Bad Gateway - failover in fuzzy mode",
			statusCode:   502,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "503 Service Unavailable - failover in fuzzy mode",
			statusCode:   503,
			wantFailover: true,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 Fuzzy 模式（所有非 2xx 都 failover）
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, nil, true, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("shouldRetryWithNextKey(%d, nil, true) failover = %v, want %v", tt.statusCode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("shouldRetryWithNextKey(%d, nil, true) quota = %v, want %v", tt.statusCode, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestShouldRetryWithNextKey_FuzzyMode_403WithQuotaMessage 测试 Fuzzy 模式下 403 + 预扣费消息
// 验证修复：Fuzzy 模式下也会检查消息体中的配额相关关键词
func TestShouldRetryWithNextKey_FuzzyMode_403WithQuotaMessage(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         []byte
		wantFailover bool
		wantQuota    bool
	}{
		{
			name:         "403 with prededuct quota error in fuzzy mode",
			statusCode:   403,
			body:         []byte(`{"error":{"type":"new_api_error","message":"预扣费额度失败, 用户剩余额度: ¥0.053950, 需要预扣费额度: ¥0.191160"},"type":"error"}`),
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:         "403 with insufficient balance in fuzzy mode",
			statusCode:   403,
			body:         []byte(`{"error":{"message":"余额不足，请充值"}}`),
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:         "403 without quota keywords in fuzzy mode",
			statusCode:   403,
			body:         []byte(`{"error":{"message":"Access denied"}}`),
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "403 with empty body in fuzzy mode",
			statusCode:   403,
			body:         nil,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "500 with quota message in fuzzy mode",
			statusCode:   500,
			body:         []byte(`{"error":{"message":"Quota exceeded"}}`),
			wantFailover: true,
			wantQuota:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, tt.body, true, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("ShouldRetryWithNextKey(%d, body, true) failover = %v, want %v", tt.statusCode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("ShouldRetryWithNextKey(%d, body, true) quota = %v, want %v", tt.statusCode, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestShouldRetryWithNextKey_SensitiveWordsDetected 测试敏感词检测错误不应重试
// 这是修复的核心场景：500 + sensitive_words_detected 不应触发无限重试
func TestShouldRetryWithNextKey_SensitiveWordsDetected(t *testing.T) {
	// 模拟生产环境的敏感词检测错误
	body := []byte(`{"error":{"message":"sensitive words detected","type":"new_api_error","param":"","code":"sensitive_words_detected"}}`)

	tests := []struct {
		name         string
		statusCode   int
		fuzzyMode    bool
		wantFailover bool
		wantQuota    bool
	}{
		{
			name:         "500 with sensitive_words_detected - normal mode",
			statusCode:   500,
			fuzzyMode:    false,
			wantFailover: false, // 不应重试
			wantQuota:    false,
		},
		{
			name:         "500 with sensitive_words_detected - fuzzy mode",
			statusCode:   500,
			fuzzyMode:    true,
			wantFailover: false, // 即使在 fuzzy 模式下也不应重试
			wantQuota:    false,
		},
		{
			name:         "400 with sensitive_words_detected - normal mode",
			statusCode:   400,
			fuzzyMode:    false,
			wantFailover: false,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, body, tt.fuzzyMode, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("ShouldRetryWithNextKey(%d, sensitive_words_body, %v) failover = %v, want %v",
					tt.statusCode, tt.fuzzyMode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("ShouldRetryWithNextKey(%d, sensitive_words_body, %v) quota = %v, want %v",
					tt.statusCode, tt.fuzzyMode, gotQuota, tt.wantQuota)
			}
		})
	}
}

func TestShouldFailoverToNextChannel_ContentPolicyOnly(t *testing.T) {
	contentPolicyBody := []byte(`{"error":{"code":"sensitive_words_detected"}}`)
	invalidRequestBody := []byte(`{"error":{"code":"invalid_request"}}`)

	if !shouldFailoverToNextChannel(contentPolicyBody, 2) {
		t.Fatal("存在其他渠道时，内容审核错误应跳过当前渠道")
	}
	if shouldFailoverToNextChannel(contentPolicyBody, 1) {
		t.Fatal("只有一个渠道时，不应伪造渠道 failover")
	}
	if shouldFailoverToNextChannel(invalidRequestBody, 2) {
		t.Fatal("普通无效请求不应切换渠道")
	}
}

func TestBuildContentPolicyCompatibilityBodyPreservesJSONValue(t *testing.T) {
	body := []byte(`{"input":[{"type":"input_text","text":"工资发放与交通补助"},{"type":"function_call_output","output":"{\"error\":{\"code\":\"sensitive_words_detected\"}}"}],"note":"content_policy_violation"}`)
	escaped, changed, err := buildContentPolicyCompatibilityBody(body)
	if err != nil {
		t.Fatalf("构建兼容请求失败: %v", err)
	}
	if !changed {
		t.Fatal("包含非 ASCII 文本或审核错误码时应生成转义请求")
	}
	if bytes.Contains(escaped, []byte("sensitive_words_detected")) || bytes.Contains(escaped, []byte("content_policy_violation")) {
		t.Fatalf("转义请求仍包含连续审核错误码: %s", escaped)
	}
	if bytes.Contains(escaped, []byte("工资发放")) || bytes.Contains(escaped, []byte("交通补助")) {
		t.Fatalf("转义请求仍包含原始非 ASCII 文本: %s", escaped)
	}

	var originalValue interface{}
	var escapedValue interface{}
	if err := json.Unmarshal(body, &originalValue); err != nil {
		t.Fatalf("解析原始 JSON 失败: %v", err)
	}
	if err := json.Unmarshal(escaped, &escapedValue); err != nil {
		t.Fatalf("解析转义 JSON 失败: %v", err)
	}
	if !reflect.DeepEqual(originalValue, escapedValue) {
		t.Fatalf("转义改变了 JSON 语义\noriginal: %#v\n escaped: %#v", originalValue, escapedValue)
	}
}

func TestBuildContentPolicyCompatibilityBodyNoMatch(t *testing.T) {
	body := []byte(`{"input":"normal request"}`)
	escaped, changed, err := buildContentPolicyCompatibilityBody(body)
	if err != nil {
		t.Fatalf("构建兼容请求失败: %v", err)
	}
	if changed || !bytes.Equal(escaped, body) {
		t.Fatal("不含非 ASCII 文本或审核错误码的请求不应被修改")
	}
}

func TestBuildContentPolicyCompatibilityBodyPreservesSupplementaryRune(t *testing.T) {
	body := []byte(`{"input":"测试😀"}`)
	escaped, changed, err := buildContentPolicyCompatibilityBody(body)
	if err != nil {
		t.Fatalf("构建兼容请求失败: %v", err)
	}
	if !changed || !bytes.Contains(escaped, []byte(`\ud83d\ude00`)) {
		t.Fatalf("补充平面字符未转成 JSON 代理对: %s", escaped)
	}

	var originalValue interface{}
	var escapedValue interface{}
	if err := json.Unmarshal(body, &originalValue); err != nil {
		t.Fatalf("解析原始 JSON 失败: %v", err)
	}
	if err := json.Unmarshal(escaped, &escapedValue); err != nil {
		t.Fatalf("解析转义 JSON 失败: %v", err)
	}
	if !reflect.DeepEqual(originalValue, escapedValue) {
		t.Fatalf("转义改变了补充平面字符语义: %#v != %#v", originalValue, escapedValue)
	}
}

func TestCleanAndExtractRealPrompt(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "empty",
			input:  "",
			expect: "",
		},
		{
			name:   "normal user prompt",
			input:  "Who are you?",
			expect: "Who are you?",
		},
		{
			name:   "cursor environment tags",
			input:  "<user_info>\nOS Version: win32 10.0.26100\nShell: powershell\n</user_info>\n<user_query>\nWho are you?\n</user_query>",
			expect: "Who are you?",
		},
		{
			name:   "contains system reminder and user query",
			input:  "<system_reminder>\nBe polite.\n</system_reminder>\n<user_query>\nHello!\n</user_query>",
			expect: "Hello!",
		},
		{
			name:   "nested tags stripped",
			input:  "<user_info>some system info</user_info>Who are you?",
			expect: "Who are you?",
		},
		{
			name:   "nested agent notification stripped",
			input:  "<agent_notification>something</agent_notification>Tell me a joke",
			expect: "Tell me a joke",
		},
		{
			name:   "codex agents.md instructions filtered",
			input:  "# AGENTS.md instructions for D:\\code\\project\\eeeee\\OA\n\n<INSTRUCTIONS>\n请始终使用中文与我对话\n</INSTRUCTIONS>",
			expect: "",
		},
		{
			name:   "codex environment_context filtered",
			input:  "<environment_context>\n  <cwd>D:\\code</cwd>\n</environment_context>",
			expect: "",
		},
		{
			name:   "codex compaction summary filtered",
			input:  "Another language model started to solve this problem and produced a summary of its thinking process.",
			expect: "",
		},
		{
			name:   "claude caveat filtered",
			input:  "Caveat: The messages below were generated by the user while running local commands. DO NOT respond to these messages.",
			expect: "",
		},
		{
			name:   "claude continued session filtered",
			input:  "This session is being continued from a previous conversation that ran out of context.",
			expect: "",
		},
		{
			name:   "claude local command output filtered",
			input:  "<local-command-stdout>some output</local-command-stdout>",
			expect: "",
		},
		{
			name:   "claude command name filtered",
			input:  "<command-name>/clear</command-name>",
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanAndExtractRealPrompt(tt.input)
			if got != tt.expect {
				t.Errorf("cleanAndExtractRealPrompt(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}
