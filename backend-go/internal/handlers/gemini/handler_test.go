package gemini

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
)

func TestHandler_RequiresProxyAccessKeyEvenWhenGeminiKeyProvided(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret-key",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.POST("/v1beta/models/*modelAction", Handler(envCfg, nil, nil))

	t.Run("x-goog-api-key does not bypass proxy auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", "any-gemini-key")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("query key does not bypass proxy auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent?key=any-gemini-key", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})
}

// TestStripThoughtSignature 测试 stripThoughtSignature 函数
func TestStripThoughtSignature(t *testing.T) {
	tests := []struct {
		name     string
		input    *types.GeminiRequest
		expected *types.GeminiRequest
	}{
		{
			name: "移除单个 functionCall 的 thought_signature",
			input: &types.GeminiRequest{
				Contents: []types.GeminiContent{
					{
						Parts: []types.GeminiPart{
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "test_function",
									Args:             map[string]interface{}{"arg1": "value1"},
									ThoughtSignature: "test_signature",
								},
							},
						},
					},
				},
			},
			expected: &types.GeminiRequest{
				Contents: []types.GeminiContent{
					{
						Parts: []types.GeminiPart{
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "test_function",
									Args:             map[string]interface{}{"arg1": "value1"},
									ThoughtSignature: "",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "移除多个 functionCall 的 thought_signature",
			input: &types.GeminiRequest{
				Contents: []types.GeminiContent{
					{
						Parts: []types.GeminiPart{
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "func1",
									Args:             map[string]interface{}{},
									ThoughtSignature: "sig1",
								},
							},
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "func2",
									Args:             map[string]interface{}{},
									ThoughtSignature: "sig2",
								},
							},
						},
					},
				},
			},
			expected: &types.GeminiRequest{
				Contents: []types.GeminiContent{
					{
						Parts: []types.GeminiPart{
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "func1",
									Args:             map[string]interface{}{},
									ThoughtSignature: "",
								},
							},
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "func2",
									Args:             map[string]interface{}{},
									ThoughtSignature: "",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "不影响非 functionCall 的 parts",
			input: &types.GeminiRequest{
				Contents: []types.GeminiContent{
					{
						Parts: []types.GeminiPart{
							{
								Text: "some text",
							},
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "func",
									Args:             map[string]interface{}{},
									ThoughtSignature: "sig",
								},
							},
						},
					},
				},
			},
			expected: &types.GeminiRequest{
				Contents: []types.GeminiContent{
					{
						Parts: []types.GeminiPart{
							{
								Text: "some text",
							},
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "func",
									Args:             map[string]interface{}{},
									ThoughtSignature: "",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "处理空 thought_signature",
			input: &types.GeminiRequest{
				Contents: []types.GeminiContent{
					{
						Parts: []types.GeminiPart{
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "func",
									Args:             map[string]interface{}{},
									ThoughtSignature: "",
								},
							},
						},
					},
				},
			},
			expected: &types.GeminiRequest{
				Contents: []types.GeminiContent{
					{
						Parts: []types.GeminiPart{
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "func",
									Args:             map[string]interface{}{},
									ThoughtSignature: "",
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stripThoughtSignature(tt.input)

			// 验证结果
			if len(tt.input.Contents) != len(tt.expected.Contents) {
				t.Fatalf("Contents length mismatch: got %d, want %d", len(tt.input.Contents), len(tt.expected.Contents))
			}

			for i := range tt.input.Contents {
				if len(tt.input.Contents[i].Parts) != len(tt.expected.Contents[i].Parts) {
					t.Fatalf("Parts length mismatch at content %d: got %d, want %d", i, len(tt.input.Contents[i].Parts), len(tt.expected.Contents[i].Parts))
				}

				for j := range tt.input.Contents[i].Parts {
					inputPart := &tt.input.Contents[i].Parts[j]
					expectedPart := &tt.expected.Contents[i].Parts[j]

					if inputPart.FunctionCall != nil {
						if expectedPart.FunctionCall == nil {
							t.Fatalf("FunctionCall mismatch at content %d, part %d: got non-nil, want nil", i, j)
						}
						// stripThoughtSignature 使用特殊标记而不是空字符串
						if inputPart.FunctionCall.ThoughtSignature != types.StripThoughtSignatureMarker {
							t.Errorf("ThoughtSignature mismatch at content %d, part %d: got %q, want %q",
								i, j, inputPart.FunctionCall.ThoughtSignature, types.StripThoughtSignatureMarker)
						}
					}
				}
			}
		})
	}
}

// TestParseModelAction 验证 URL 参数被拆成模型名与方法名，方法名不再被丢弃
func TestParseModelAction(t *testing.T) {
	tests := []struct {
		param  string
		model  string
		action string
	}{
		{"/gemini-2.0-flash:generateContent", "gemini-2.0-flash", "generateContent"},
		{"/gemini-2.0-flash:streamGenerateContent", "gemini-2.0-flash", "streamGenerateContent"},
		{"/gemini-2.0-flash:countTokens", "gemini-2.0-flash", "countTokens"},
		{"/free/vendor/model:free:generateContent", "free/vendor/model:free", "generateContent"},
		{"/free/vendor/model:free:streamGenerateContent", "free/vendor/model:free", "streamGenerateContent"},
		{"/gemini-2.0-flash", "gemini-2.0-flash", ""},
		{"", "", ""},
	}

	for _, tt := range tests {
		model, action := parseModelAction(tt.param)
		if model != tt.model || action != tt.action {
			t.Errorf("parseModelAction(%q) = (%q, %q), want (%q, %q)", tt.param, model, action, tt.model, tt.action)
		}
	}
}

// TestHandler_ActionDispatch 验证 /v1beta/models/*modelAction 按方法分发：
// 未代理的方法显式拒绝（不再静默改写成 generateContent），countTokens 本地估算。
func TestHandler_ActionDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret-key",
		MaxRequestBodySize: 1024 * 1024,
	}
	r := gin.New()
	r.POST("/v1beta/models/*modelAction", Handler(envCfg, nil, nil))

	doRequest := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", "secret-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("未支持的方法返回明确的 Gemini 错误", func(t *testing.T) {
		w := doRequest("/v1beta/models/gemini-2.0-flash:embedContent", `{"contents":[]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
		var geminiErr types.GeminiError
		if err := json.Unmarshal(w.Body.Bytes(), &geminiErr); err != nil {
			t.Fatalf("响应不是 Gemini 错误信封: %v, body=%s", err, w.Body.String())
		}
		if geminiErr.Error.Status != "INVALID_ARGUMENT" {
			t.Errorf("status = %q, want INVALID_ARGUMENT", geminiErr.Error.Status)
		}
		if geminiErr.Error.Code != http.StatusBadRequest {
			t.Errorf("code = %d, want %d", geminiErr.Error.Code, http.StatusBadRequest)
		}
		if !strings.Contains(geminiErr.Error.Message, "embedContent") {
			t.Errorf("错误消息应回显请求的方法名，实际: %s", geminiErr.Error.Message)
		}
	})

	t.Run("缺少方法名返回错误", func(t *testing.T) {
		w := doRequest("/v1beta/models/gemini-2.0-flash", `{"contents":[]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("countTokens 本地估算且不访问上游", func(t *testing.T) {
		w := doRequest("/v1beta/models/gemini-2.0-flash:countTokens",
			`{"contents":[{"parts":[{"text":"hello world"}]}]}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
		}
		var resp struct {
			TotalTokens int `json:"totalTokens"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("解析响应失败: %v, body=%s", err, w.Body.String())
		}
		if resp.TotalTokens <= 0 {
			t.Errorf("totalTokens = %d, want > 0", resp.TotalTokens)
		}
	})

	t.Run("countTokens 也需要代理访问密钥", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:countTokens", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("x-goog-api-key", "any-gemini-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("countTokens 支持完整生成请求", func(t *testing.T) {
		w := doRequest("/v1beta/models/gemini-2.0-flash:countTokens",
			`{"generateContentRequest":{"contents":[{"parts":[{"text":"hello world"}]}],"systemInstruction":{"parts":[{"text":"请用中文回答"}]},"tools":[{"functionDeclarations":[{"name":"search","parameters":{"type":"object"}}]}]}}`)
		if w.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，预期 200，响应 = %s", w.Code, w.Body.String())
		}
		var resp struct {
			TotalTokens int `json:"totalTokens"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if resp.TotalTokens < 150 {
			t.Fatalf("完整请求的工具声明未计入: totalTokens = %d", resp.TotalTokens)
		}
	})

	t.Run("countTokens 拒绝错误的请求结构", func(t *testing.T) {
		for _, body := range []string{
			``, `null`, `{}`, `{"contents":"hello"}`,
			`{"contents":[null]}`, `{"contents":[{"parts":"hello"}]}`,
			`{"contents":[{"parts":[null]}]}`, `{"contents":[{"parts":[{"text":123}]}]}`,
			`{"generateContentRequest":null}`, `{"generateContentRequest":"hello"}`,
			`{"generateContentRequest":{"contents":"hello"}}`,
		} {
			w := doRequest("/v1beta/models/gemini-2.0-flash:countTokens", body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("请求 %q 的状态码 = %d，预期 400，响应 = %s", body, w.Code, w.Body.String())
			}
			var geminiErr types.GeminiError
			if err := json.Unmarshal(w.Body.Bytes(), &geminiErr); err != nil {
				t.Fatalf("解析错误响应失败: %v", err)
			}
			if geminiErr.Error.Code != http.StatusBadRequest || geminiErr.Error.Status != "INVALID_ARGUMENT" || geminiErr.Error.Message == "" {
				t.Fatalf("请求 %q 未返回明确的 Gemini 错误: %+v", body, geminiErr.Error)
			}
		}
	})
}

// TestBuildProviderRequest_UsesParsedAction 验证上游 URL 使用真实方法名，而不是由流式标记反推
func TestBuildProviderRequest_UsesParsedAction(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)

	upstream := &config.UpstreamConfig{BaseURL: "https://test.example.com", ServiceType: "gemini"}

	for _, action := range []string{actionGenerateContent, actionStreamGenerateContent} {
		req, err := buildProviderRequest(c, upstream, upstream.BaseURL, "test-key", &types.GeminiRequest{}, "gemini-2.0-flash", action)
		if err != nil {
			t.Fatalf("buildProviderRequest(%s) failed: %v", action, err)
		}
		want := "https://test.example.com/v1beta/models/gemini-2.0-flash:" + action
		if action == actionStreamGenerateContent {
			want += "?alt=sse"
		}
		if req.URL.String() != want {
			t.Errorf("url = %q, want %q", req.URL.String(), want)
		}
	}
}

func TestBuildProviderRequest_StripThoughtSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name                     string
		stripThoughtSignature    bool
		injectDummyThoughtSig    bool
		inputThoughtSignature    string
		expectedThoughtSignature string
	}{
		{
			name:                     "StripThoughtSignature=true 移除字段",
			stripThoughtSignature:    true,
			injectDummyThoughtSig:    false,
			inputThoughtSignature:    "test_signature",
			expectedThoughtSignature: "",
		},
		{
			name:                     "默认行为：透传非空签名",
			stripThoughtSignature:    false,
			injectDummyThoughtSig:    false,
			inputThoughtSignature:    "test_signature",
			expectedThoughtSignature: "test_signature",
		},
		{
			name:                     "默认行为：完全透传空签名",
			stripThoughtSignature:    false,
			injectDummyThoughtSig:    false,
			inputThoughtSignature:    "",
			expectedThoughtSignature: "",
		},
		{
			name:                     "InjectDummyThoughtSignature=true 注入 dummy",
			stripThoughtSignature:    false,
			injectDummyThoughtSig:    true,
			inputThoughtSignature:    "",
			expectedThoughtSignature: types.DummyThoughtSignature,
		},
		{
			name:                     "StripThoughtSignature=true 优先于 InjectDummyThoughtSignature",
			stripThoughtSignature:    true,
			injectDummyThoughtSig:    true,
			inputThoughtSignature:    "test_signature",
			expectedThoughtSignature: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &config.UpstreamConfig{
				BaseURL:                     "https://test.example.com",
				ServiceType:                 "gemini",
				StripThoughtSignature:       tt.stripThoughtSignature,
				InjectDummyThoughtSignature: tt.injectDummyThoughtSig,
			}

			geminiReq := &types.GeminiRequest{
				Contents: []types.GeminiContent{
					{
						Parts: []types.GeminiPart{
							{
								FunctionCall: &types.GeminiFunctionCall{
									Name:             "test_function",
									Args:             map[string]interface{}{"arg1": "value1"},
									ThoughtSignature: tt.inputThoughtSignature,
								},
							},
						},
					},
				},
			}

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)

			req, err := buildProviderRequest(c, upstream, upstream.BaseURL, "test-key", geminiReq, "gemini-2.0-flash", actionGenerateContent)
			if err != nil {
				t.Fatalf("buildProviderRequest failed: %v", err)
			}

			// 解析请求体
			var resultReq types.GeminiRequest
			if err := json.NewDecoder(req.Body).Decode(&resultReq); err != nil {
				t.Fatalf("Failed to decode request body: %v", err)
			}

			// 验证 thought_signature
			if len(resultReq.Contents) == 0 || len(resultReq.Contents[0].Parts) == 0 {
				t.Fatal("Request body is empty")
			}

			part := resultReq.Contents[0].Parts[0]
			if part.FunctionCall == nil {
				t.Fatal("FunctionCall is nil")
			}

			if part.FunctionCall.ThoughtSignature != tt.expectedThoughtSignature {
				t.Errorf("ThoughtSignature mismatch: got %q, want %q",
					part.FunctionCall.ThoughtSignature, tt.expectedThoughtSignature)
			}

			// 验证原始请求未被修改（深拷贝机制）
			if geminiReq.Contents[0].Parts[0].FunctionCall.ThoughtSignature != tt.inputThoughtSignature {
				t.Errorf("Original request was modified: got %q, want %q",
					geminiReq.Contents[0].Parts[0].FunctionCall.ThoughtSignature, tt.inputThoughtSignature)
			}
		})
	}
}

func TestBuildProviderRequest_InjectDummyThoughtSignature_PreservesThoughtSignatureAtPartLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &config.UpstreamConfig{
		BaseURL:                     "https://test.example.com",
		ServiceType:                 "gemini",
		StripThoughtSignature:       false,
		InjectDummyThoughtSignature: true,
	}

	// 模拟 Gemini CLI：thoughtSignature 出现在 part 层级（而非 functionCall 内部）
	var geminiReq types.GeminiRequest
	if err := json.Unmarshal([]byte(`{
  "contents": [
    {
      "parts": [
        {
          "functionCall": {
            "name": "run_shell_command",
            "args": { "command": "ls -R" }
          },
          "thoughtSignature": "sig_from_cli"
        }
      ]
    }
  ]
}`), &geminiReq); err != nil {
		t.Fatalf("Unmarshal 请求失败: %v", err)
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)

	req, err := buildProviderRequest(c, upstream, upstream.BaseURL, "test-key", &geminiReq, "gemini-2.0-flash", actionGenerateContent)
	if err != nil {
		t.Fatalf("buildProviderRequest failed: %v", err)
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取请求体失败: %v", err)
	}

	// 解析为通用 map，验证字段名格式（thought_signature vs thoughtSignature）
	var raw map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("解析请求体 JSON 失败: %v", err)
	}

	contents, ok := raw["contents"].([]interface{})
	if !ok || len(contents) != 1 {
		t.Fatalf("contents 解析失败: %T, len=%d", raw["contents"], len(contents))
	}
	content0, ok := contents[0].(map[string]interface{})
	if !ok {
		t.Fatalf("contents[0] 类型=%T, want=map[string]interface{}", contents[0])
	}
	parts, ok := content0["parts"].([]interface{})
	if !ok || len(parts) != 1 {
		t.Fatalf("parts 解析失败: %T, len=%d", content0["parts"], len(parts))
	}
	part0, ok := parts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("parts[0] 类型=%T, want=map[string]interface{}", parts[0])
	}
	if v, exists := part0["thoughtSignature"]; !exists || v != "sig_from_cli" {
		t.Fatalf("part.thoughtSignature=%v, want=%v", v, "sig_from_cli")
	}
	if _, exists := part0["thought_signature"]; exists {
		t.Fatalf("不应在 part 层级输出 thought_signature: %v", part0)
	}

	fc, ok := part0["functionCall"].(map[string]interface{})
	if !ok {
		t.Fatalf("functionCall 类型=%T, want=map[string]interface{}", part0["functionCall"])
	}
	if _, exists := fc["thoughtSignature"]; exists {
		t.Fatalf("不应在 functionCall 内输出 thoughtSignature: %v", fc)
	}
	if _, exists := fc["thought_signature"]; exists {
		t.Fatalf("不应在 functionCall 内输出 thought_signature: %v", fc)
	}
}
