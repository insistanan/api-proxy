// 本文件锁定 streams 包导出函数的可观察行为（特征测试）：
// 缓存字段剥离、事件判定谓词、事件构造、响应头设置与响应体转发。
// 这些行为此前无测试覆盖，改动相关实现前先跑本文件。
package streams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
)

func TestProcessStreamEventCorrectsNestedMessageStartUsageWithoutChangingMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	streamContext := NewStreamContext(&config.EnvConfig{})
	requestBody := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":%q}]}`,
		strings.Repeat("a", 28000),
	))
	estimatedInputTokens := utils.EstimateRequestTokens(requestBody)
	event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":209736,\"output_tokens\":1}}}\n\n"

	if err := ProcessStreamEvent(
		ginContext,
		ginContext.Writer,
		recorder,
		event,
		streamContext,
		&config.EnvConfig{},
		requestBody,
	); err != nil {
		t.Fatalf("ProcessStreamEvent returned error: %v", err)
	}

	if streamContext.UpstreamUsage.InputTokens != 209736 {
		t.Fatalf("上游指标快照应保留原值, got %d", streamContext.UpstreamUsage.InputTokens)
	}
	writtenEvent := recorder.Body.String()
	writtenData, ok := ParseSSEEventData(writtenEvent)
	if !ok {
		t.Fatalf("客户端事件应保持可解析: %q", writtenEvent)
	}
	usage := writtenData["message"].(map[string]interface{})["usage"].(map[string]interface{})
	if got := int(usage["input_tokens"].(float64)); got != estimatedInputTokens {
		t.Fatalf("message_start 客户端 input_tokens = %d, want %d", got, estimatedInputTokens)
	}
}

func TestSanityCheckMessageStreamUsageClearsImpossibleCacheTuple(t *testing.T) {
	requestBody := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":%q}]}`,
		strings.Repeat("a", 28000),
	))
	estimatedInputTokens := utils.EstimateRequestTokens(requestBody)
	event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":500,\"output_tokens\":1,\"cache_read_input_tokens\":204800,\"cache_creation_input_tokens\":1000,\"cache_creation_5m_input_tokens\":400,\"cache_creation_1h_input_tokens\":600,\"cache_ttl\":\"mixed\",\"input_tokens_details\":{\"cached_tokens\":204800}}}}\n\n"

	correctedEvent, changed := sanityCheckMessageStreamInputTokens(event, requestBody, false)
	if !changed {
		t.Fatal("不可能的缓存元组应被校正")
	}
	data, ok := ParseSSEEventData(correctedEvent)
	if !ok {
		t.Fatalf("校正后事件应保持可解析: %q", correctedEvent)
	}
	usage := data["message"].(map[string]interface{})["usage"].(map[string]interface{})
	if got := int(usage["input_tokens"].(float64)); got != estimatedInputTokens {
		t.Fatalf("input_tokens = %d, want %d", got, estimatedInputTokens)
	}
	for _, key := range []string{
		"cache_read_input_tokens",
		"cache_creation_input_tokens",
		"cache_creation_5m_input_tokens",
		"cache_creation_1h_input_tokens",
		"cache_ttl",
		"input_tokens_details",
	} {
		if _, exists := usage[key]; exists {
			t.Fatalf("异常客户端 usage 不应保留 %s", key)
		}
	}
}

func TestProcessStreamEventPreservesTrustedCacheUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	requestBody := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":%q}]}`,
		strings.Repeat("a", 28000),
	))
	estimatedInputTokens := utils.EstimateRequestTokens(requestBody)
	uncachedInputTokens := estimatedInputTokens - 600
	event := fmt.Sprintf(
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":%d,\"output_tokens\":2,\"cache_read_input_tokens\":100,\"cache_creation_input_tokens\":500,\"cache_creation_5m_input_tokens\":200,\"cache_creation_1h_input_tokens\":300,\"cache_ttl\":\"mixed\"}}}\n\n",
		uncachedInputTokens,
	)

	if err := ProcessStreamEvent(
		ginContext,
		ginContext.Writer,
		recorder,
		event,
		NewStreamContext(&config.EnvConfig{}),
		&config.EnvConfig{},
		requestBody,
	); err != nil {
		t.Fatalf("ProcessStreamEvent returned error: %v", err)
	}

	data, ok := ParseSSEEventData(recorder.Body.String())
	if !ok {
		t.Fatalf("客户端事件应保持可解析: %q", recorder.Body.String())
	}
	usage := data["message"].(map[string]interface{})["usage"].(map[string]interface{})
	for key, want := range map[string]interface{}{
		"input_tokens":                   float64(uncachedInputTokens),
		"cache_read_input_tokens":        float64(100),
		"cache_creation_input_tokens":    float64(500),
		"cache_creation_5m_input_tokens": float64(200),
		"cache_creation_1h_input_tokens": float64(300),
		"cache_ttl":                      "mixed",
	} {
		if got := usage[key]; got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
}

func TestStripCacheFieldsFromClaudeSSE(t *testing.T) {
	t.Run("strips cache creation fields from nested message.usage", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":5,\"cache_creation_5m_input_tokens\":2,\"cache_creation_1h_input_tokens\":3,\"cache_ttl\":\"1h\",\"cache_read_input_tokens\":7}}}\n\n"
		got := StripCacheFieldsFromClaudeSSE(event)
		data, ok := ParseSSEEventData(got)
		if !ok {
			t.Fatalf("剥离后应仍是可解析的 SSE 事件: %q", got)
		}
		usage := data["message"].(map[string]interface{})["usage"].(map[string]interface{})
		for _, key := range []string{"cache_creation_input_tokens", "cache_creation_5m_input_tokens", "cache_creation_1h_input_tokens", "cache_ttl"} {
			if _, exists := usage[key]; exists {
				t.Fatalf("%s 应被剥离", key)
			}
		}
		if usage["input_tokens"] != float64(10) {
			t.Fatalf("input_tokens 应保留, got %v", usage["input_tokens"])
		}
		if usage["cache_read_input_tokens"] != float64(7) {
			t.Fatalf("cache_read_input_tokens 必须保留（客户端据此判断缓存上下文）, got %v", usage["cache_read_input_tokens"])
		}
	})

	t.Run("strips cache fields from top-level usage", func(t *testing.T) {
		event := "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3,\"cache_creation_input_tokens\":9}}\n\n"
		got := StripCacheFieldsFromClaudeSSE(event)
		data, ok := ParseSSEEventData(got)
		if !ok {
			t.Fatalf("解析失败: %q", got)
		}
		usage := data["usage"].(map[string]interface{})
		if _, exists := usage["cache_creation_input_tokens"]; exists {
			t.Fatal("顶层 usage 的 cache_creation_input_tokens 应被剥离")
		}
		if usage["output_tokens"] != float64(3) {
			t.Fatalf("output_tokens 应保留, got %v", usage["output_tokens"])
		}
	})

	t.Run("returns event without cache fields byte-identical", func(t *testing.T) {
		event := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		if got := StripCacheFieldsFromClaudeSSE(event); got != event {
			t.Fatalf("不含缓存字段的事件应逐字节原样返回:\nwant %q\ngot  %q", event, got)
		}
	})

	t.Run("empty event stays empty", func(t *testing.T) {
		if got := StripCacheFieldsFromClaudeSSE(""); got != "" {
			t.Fatalf("空事件应原样返回, got %q", got)
		}
	})
}

func TestIsStreamErrorEvent(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		isError bool
	}{
		{
			name:    "event line error",
			event:   "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"Overloaded\"}}\n\n",
			isError: true,
		},
		{
			name:    "data type error",
			event:   "data: {\"type\":\"error\",\"error\":{\"message\":\"Rate limit\"}}\n\n",
			isError: true,
		},
		{
			name:    "data error object without type",
			event:   "data: {\"error\":{\"message\":\"Invalid key\",\"code\":\"invalid_api_key\"}}\n\n",
			isError: true,
		},
		{
			name:    "data error string",
			event:   "data: {\"error\":\"Service unavailable\"}\n\n",
			isError: true,
		},
		{
			name:    "normal message_start",
			event:   "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"}}\n\n",
			isError: false,
		},
		{
			name:    "normal ping",
			event:   "event: ping\ndata: {\"type\":\"ping\"}\n\n",
			isError: false,
		},
		{
			name:    "empty event",
			event:   "",
			isError: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsStreamErrorEvent(tc.event); got != tc.isError {
				t.Errorf("IsStreamErrorEvent(%q) = %v, want %v", tc.event, got, tc.isError)
			}
		})
	}
}

func TestExtractStreamErrorMessage(t *testing.T) {
	t.Run("nested error.message", func(t *testing.T) {
		event := "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"Overloaded error\"}}\n\n"
		got := ExtractStreamErrorMessage(event)
		if got != "Overloaded error" {
			t.Fatalf("got %q, want %q", got, "Overloaded error")
		}
	})

	t.Run("top-level error string", func(t *testing.T) {
		event := "data: {\"error\":\"Quota exceeded\"}\n\n"
		got := ExtractStreamErrorMessage(event)
		if got != "Quota exceeded" {
			t.Fatalf("got %q, want %q", got, "Quota exceeded")
		}
	})

	t.Run("top-level message string", func(t *testing.T) {
		event := "data: {\"message\":\"Something went wrong\"}\n\n"
		got := ExtractStreamErrorMessage(event)
		if got != "Something went wrong" {
			t.Fatalf("got %q, want %q", got, "Something went wrong")
		}
	})
}

func TestStreamEventPredicates(t *testing.T) {
	cases := []struct {
		name  string
		event string
		start bool
		block bool
		stop  bool
		delta bool
	}{
		{
			name:  "message_start compact json",
			event: "data: {\"type\":\"message_start\"}",
			start: true,
		},
		{
			name:  "message_start spaced json",
			event: "data: {\"type\": \"message_start\"}",
			start: true,
		},
		{
			name:  "content_block_start",
			event: "data: {\"type\":\"content_block_start\",\"index\":0}",
			block: true,
		},
		{
			name:  "message_stop via event line",
			event: "event: message_stop\ndata: {}",
			stop:  true,
		},
		{
			name:  "message_stop via data json",
			event: "data: {\"type\":\"message_stop\"}",
			stop:  true,
		},
		{
			name:  "message_delta via event line",
			event: "event: message_delta\ndata: {}",
			delta: true,
		},
		{
			name:  "message_delta via data json",
			event: "data: {\"type\":\"message_delta\"}",
			delta: true,
		},
		{
			name:  "unrelated ping",
			event: "data: {\"type\":\"ping\"}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsMessageStartEvent(tc.event); got != tc.start {
				t.Errorf("IsMessageStartEvent = %v, want %v", got, tc.start)
			}
			if got := IsContentBlockStartEvent(tc.event); got != tc.block {
				t.Errorf("IsContentBlockStartEvent = %v, want %v", got, tc.block)
			}
			if got := IsMessageStopEvent(tc.event); got != tc.stop {
				t.Errorf("IsMessageStopEvent = %v, want %v", got, tc.stop)
			}
			if got := IsMessageDeltaEvent(tc.event); got != tc.delta {
				t.Errorf("IsMessageDeltaEvent = %v, want %v", got, tc.delta)
			}
		})
	}
}

func TestBuildStreamErrorEvent(t *testing.T) {
	got := BuildStreamErrorEvent(errors.New("boom"))
	if !strings.HasPrefix(got, "event: error\ndata: ") {
		t.Fatalf("应以 event: error 开头, got %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("应以空行结尾, got %q", got)
	}
	data, ok := ParseSSEEventData(got)
	if !ok {
		t.Fatalf("应为可解析的 SSE 事件: %q", got)
	}
	if data["type"] != "error" {
		t.Fatalf("type 应为 error, got %v", data["type"])
	}
	errObj := data["error"].(map[string]interface{})
	if errObj["type"] != "stream_error" {
		t.Fatalf("error.type 应为 stream_error, got %v", errObj["type"])
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "boom") {
		t.Fatalf("error.message 应包含原始错误文本, got %q", msg)
	}
}

func TestBuildMessageStartEvent(t *testing.T) {
	t.Run("uses given model", func(t *testing.T) {
		data, ok := ParseSSEEventData(BuildMessageStartEvent("claude-x"))
		if !ok {
			t.Fatal("应产出可解析的 message_start 事件")
		}
		if data["type"] != "message_start" {
			t.Fatalf("type = %v", data["type"])
		}
		msg := data["message"].(map[string]interface{})
		if msg["model"] != "claude-x" {
			t.Fatalf("model = %v", msg["model"])
		}
		if id, _ := msg["id"].(string); !strings.HasPrefix(id, "msg_") {
			t.Fatalf("id 应有 msg_ 前缀, got %q", id)
		}
		usage := msg["usage"].(map[string]interface{})
		if usage["input_tokens"] != float64(0) || usage["output_tokens"] != float64(1) {
			t.Fatalf("合成 usage 口径应为 input=0/output=1, got %v", usage)
		}
	})

	t.Run("empty model falls back to unknown", func(t *testing.T) {
		data, ok := ParseSSEEventData(BuildMessageStartEvent(""))
		if !ok {
			t.Fatal("应产出可解析事件")
		}
		if model := data["message"].(map[string]interface{})["model"]; model != "unknown" {
			t.Fatalf("空 model 应回退为 unknown, got %v", model)
		}
	})
}

func TestPatchMessageStartEvent(t *testing.T) {
	t.Run("non message_start passes through unchanged", func(t *testing.T) {
		event := "data: {\"type\":\"ping\"}\n\n"
		if got := PatchMessageStartEvent(event, "opus", true, false); got != event {
			t.Fatalf("非 message_start 事件应逐字节原样返回:\nwant %q\ngot  %q", event, got)
		}
	})

	t.Run("fills empty id", func(t *testing.T) {
		event := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"\",\"model\":\"claude-x\"}}\n\n"
		data, ok := ParseSSEEventData(PatchMessageStartEvent(event, "opus", false, false))
		if !ok {
			t.Fatal("修补后应可解析")
		}
		msg := data["message"].(map[string]interface{})
		if id, _ := msg["id"].(string); !strings.HasPrefix(id, "msg_") {
			t.Fatalf("空 id 应补全为 msg_ 前缀, got %q", id)
		}
		if msg["model"] != "claude-x" {
			t.Fatalf("rewriteModel=false 时不应改写 model, got %v", msg["model"])
		}
	})

	t.Run("rewrites model only when enabled", func(t *testing.T) {
		event := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-x\"}}\n\n"
		data, ok := ParseSSEEventData(PatchMessageStartEvent(event, "opus", true, false))
		if !ok {
			t.Fatal("修补后应可解析")
		}
		if model := data["message"].(map[string]interface{})["model"]; model != "opus" {
			t.Fatalf("rewriteModel=true 时应改写为请求模型, got %v", model)
		}

		data2, ok := ParseSSEEventData(PatchMessageStartEvent(event, "opus", false, false))
		if !ok {
			t.Fatal("修补后应可解析")
		}
		if model := data2["message"].(map[string]interface{})["model"]; model != "claude-x" {
			t.Fatalf("rewriteModel=false 时应保留上游模型, got %v", model)
		}
	})

	t.Run("nothing to patch returns byte-identical", func(t *testing.T) {
		event := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"opus\"}}\n\n"
		if got := PatchMessageStartEvent(event, "opus", true, false); got != event {
			t.Fatalf("无需修补时应逐字节原样返回:\nwant %q\ngot  %q", event, got)
		}
	})
}

func TestParseSSEEventData(t *testing.T) {
	cases := []struct {
		name   string
		event  string
		wantOK bool
	}{
		{"valid data line", "event: x\ndata: {\"a\":1}\n\n", true},
		{"done marker", "data: [DONE]\n\n", false},
		{"invalid json", "data: not-json\n\n", false},
		{"no data line", "event: x\n\n", false},
		{"empty input", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, ok := ParseSSEEventData(tc.event)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && data["a"] != float64(1) {
				t.Fatalf("data 解析错误: %v", data)
			}
		})
	}
}

func TestIsEventStreamResponse(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		want        bool
	}{
		{"plain sse", "text/event-stream", true},
		{"uppercase sse", "TEXT/EVENT-STREAM", true},
		{"sse with charset", "text/event-stream; charset=utf-8", true},
		{"json", "application/json", false},
		{"malformed with sse prefix falls back to prefix match", "text/event-stream;;bad", true},
		{"garbage", "###", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if tc.contentType != "" || tc.name != "nil header" {
				resp.Header.Set("Content-Type", tc.contentType)
			}
			if got := IsEventStreamResponse(resp); got != tc.want {
				t.Errorf("IsEventStreamResponse(%q) = %v, want %v", tc.contentType, got, tc.want)
			}
		})
	}

	t.Run("nil response", func(t *testing.T) {
		if IsEventStreamResponse(nil) {
			t.Fatal("nil 响应应返回 false")
		}
	})
}

func TestSetupStreamHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{Header: http.Header{}, StatusCode: 200}

	SetupStreamHeaders(c, resp)

	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := w.Header().Get("Connection"); got != "keep-alive" {
		t.Errorf("Connection = %q", got)
	}
	if got := w.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q", got)
	}
	if w.Code != 200 {
		t.Errorf("status = %d", w.Code)
	}
}

func TestForwardUpstreamResponseBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		return c, w
	}

	t.Run("forwards body and applies default content type", func(t *testing.T) {
		c, w := newCtx()
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("hello")),
		}
		if err := ForwardUpstreamResponseBody(c, resp, "application/json", false); err != nil {
			t.Fatalf("err = %v", err)
		}
		if w.Body.String() != "hello" {
			t.Fatalf("body = %q", w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("缺省 Content-Type 未生效, got %q", got)
		}
		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
	})

	t.Run("preserves upstream content type over default", func(t *testing.T) {
		c, w := newCtx()
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("x")),
		}
		resp.Header.Set("Content-Type", "image/png")
		if err := ForwardUpstreamResponseBody(c, resp, "application/json", false); err != nil {
			t.Fatalf("err = %v", err)
		}
		if got := w.Header().Get("Content-Type"); got != "image/png" {
			t.Fatalf("上游 Content-Type 应优先, got %q", got)
		}
	})

	t.Run("sse response forces streaming headers", func(t *testing.T) {
		c, w := newCtx()
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("data: {}\n\n")),
		}
		resp.Header.Set("Content-Type", "text/event-stream")
		if err := ForwardUpstreamResponseBody(c, resp, "", false); err != nil {
			t.Fatalf("err = %v", err)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("SSE 响应应强制 no-cache, got %q", got)
		}
		if got := w.Header().Get("X-Accel-Buffering"); got != "no" {
			t.Fatalf("SSE 响应应强制 X-Accel-Buffering: no, got %q", got)
		}
	})

	t.Run("propagates read error", func(t *testing.T) {
		c, _ := newCtx()
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body: io.NopCloser(io.MultiReader(
				strings.NewReader("partial"),
				errReader{},
			)),
		}
		if err := ForwardUpstreamResponseBody(c, resp, "", false); err == nil || err.Error() != "read failed" {
			t.Fatalf("读错误应向上传播, got %v", err)
		}
	})

	t.Run("nil arguments are safe", func(t *testing.T) {
		if err := ForwardUpstreamResponseBody(nil, nil, "", false); err != nil {
			t.Fatalf("nil 参数应返回 nil, got %v", err)
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestIsClientDisconnectError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"broken pipe", errors.New("write tcp: broken pipe"), true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"wrapped broken pipe", fmt.Errorf("write failed: %w", errors.New("broken pipe")), true},
		{"context canceled", context.Canceled, false},
		{"nil", nil, false},
		{"other error", errors.New("timeout"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsClientDisconnectError(tc.err); got != tc.want {
				t.Errorf("IsClientDisconnectError = %v, want %v", got, tc.want)
			}
		})
	}
}

// makeLargeMessagesRequestBody 造一个长上下文 messages 请求体，保证本地估算超过校验下限。
func makeLargeMessagesRequestBody(t *testing.T) []byte {
	t.Helper()
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"` + strings.Repeat("abcdefgh ", 17500) + `"}]}`)
	if estimated := utils.EstimateRequestTokens(body); estimated < utils.UsageSanityMinTokensForTest {
		t.Fatalf("测试请求体估算 %d 未达校验下限 %d", estimated, utils.UsageSanityMinTokensForTest)
	}
	return body
}

// TestSanityCheckMessageStreamInputTokens 锁定 messages 流式出口的 usage 合理性校验契约。
// 与 utils.SanityCheckedInputTokens 共用同一套判定，这里只验证 SSE 层改写不破坏结构、
// 缓存字段不被覆盖、客户端可读字段的语义与 responses 侧保持一致。
func TestSanityCheckMessageStreamInputTokens(t *testing.T) {
	largeBody := makeLargeMessagesRequestBody(t)
	estimated := utils.EstimateRequestTokens(largeBody)

	t.Run("少报: 校正后 input 回到估算量级", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":7723,\"output_tokens\":188,\"cache_read_input_tokens\":0}}\n\n"
		out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false)
		if !ok {
			t.Fatalf("应触发校正")
		}
		usage := extractStreamUsage(t, out)
		if got, _ := usage["input_tokens"].(float64); int(got) != estimated {
			t.Errorf("input_tokens 应校正为 %d, got %v", estimated, usage["input_tokens"])
		}
		if got, _ := usage["output_tokens"].(float64); int(got) != 188 {
			t.Errorf("output_tokens 不应被改动: %v", usage["output_tokens"])
		}
		if got, _ := usage["cache_read_input_tokens"].(float64); int(got) != 0 {
			t.Errorf("cache_read_input_tokens 不应被改动: %v", usage["cache_read_input_tokens"])
		}
	})

	t.Run("多报: 压缩后假大值应被压回估算量级", func(t *testing.T) {
		smallBody := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"summary + new"}]}`)
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":209736,\"output_tokens\":188}}\n\n"
		out, ok := sanityCheckMessageStreamInputTokens(event, smallBody, false)
		if !ok {
			t.Fatalf("多报场景应触发校正")
		}
		usage := extractStreamUsage(t, out)
		if got, _ := usage["input_tokens"].(float64); int(got) > 20000 {
			t.Errorf("假大值应被压回估算量级, got %v", usage["input_tokens"])
		}
	})

	t.Run("上游回报可信: 原样返回", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":" + strconv.Itoa(estimated) + ",\"output_tokens\":188}}\n\n"
		if out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false); ok || out != event {
			t.Fatalf("可信回报应原样返回 (changed=%v)", ok)
		}
	})

	t.Run("Anthropic 语义分开报缓存: 不得误判", func(t *testing.T) {
		cacheRead := estimated - 1000
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":271,\"output_tokens\":188,\"cache_read_input_tokens\":" + strconv.Itoa(cacheRead) + ",\"cache_creation_input_tokens\":500}}\n\n"
		if out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false); ok || out != event {
			t.Fatalf("Claude 语义分开报缓存应原样返回 (changed=%v)", ok)
		}
	})

	t.Run("上游确实少报且有缓存: 校正值扣掉缓存", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":100,\"output_tokens\":188,\"cache_read_input_tokens\":1000,\"cache_creation_input_tokens\":0}}\n\n"
		out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false)
		if !ok {
			t.Fatalf("应触发校正")
		}
		usage := extractStreamUsage(t, out)
		want := estimated - 1000
		if got, _ := usage["input_tokens"].(float64); int(got) != want {
			t.Errorf("校正值应扣掉缓存量: got %d, want %d", int(got), want)
		}
		if got, _ := usage["cache_read_input_tokens"].(float64); int(got) != 1000 {
			t.Errorf("cache_read 字段不应被改动: %v", usage["cache_read_input_tokens"])
		}
		// input + cache 之和回到真实上下文规模，不双倍
		if int(usage["input_tokens"].(float64))+int(usage["cache_read_input_tokens"].(float64)) != estimated {
			t.Errorf("input+cache 应等于估算总量 %d", estimated)
		}
	})

	t.Run("不带 input_tokens: 原样返回", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":188}}\n\n"
		if out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false); ok || out != event {
			t.Fatalf("无 input_tokens 时不应触发 (changed=%v)", ok)
		}
	})
}

func extractStreamUsage(t *testing.T, event string) map[string]interface{} {
	t.Helper()
	for _, line := range strings.Split(event, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimPrefix(strings.TrimPrefix(line, "data: "), "data:")
		var root map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &root); err != nil {
			continue
		}
		if u, ok := root["usage"].(map[string]interface{}); ok {
			return u
		}
	}
	t.Fatalf("未找到 usage 字段: %q", event)
	return nil
}
