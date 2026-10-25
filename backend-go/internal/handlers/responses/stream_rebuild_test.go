package responses

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
)

// TestResponsesEventRebuildPreservesLineStructure 锁定 Responses 侧两个事件改写函数的保真契约：
// 收敛前它们逐行补写 "\n"，会让以 "\n\n" 结尾的事件多出一个换行。
func TestResponsesEventRebuildPreservesLineStructure(t *testing.T) {
	envCfg := &config.EnvConfig{}
	requestBody := []byte(`{"model":"m","input":"hello"}`)

	t.Run("injectResponsesUsageToCompletedEvent", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"
		out, in, outTok := injectResponsesUsageToCompletedEvent(event, requestBody, "hi there", envCfg)

		assertSameLineStructure(t, event, out)
		assertResponsesUsage(t, out, in, outTok)
	})

	t.Run("patchResponsesCompletedEventUsage", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"total_tokens\":0}}}\n\n"
		collected := &responsesStreamUsage{}
		out := patchResponsesCompletedEventUsage(event, requestBody, "hi there", collected, envCfg)

		assertSameLineStructure(t, event, out)
		if collected.InputTokens <= 0 || collected.OutputTokens <= 0 {
			t.Fatalf("collected usage 未被修补: %+v", collected)
		}
		assertResponsesUsage(t, out, collected.InputTokens, collected.OutputTokens)
	})

	t.Run("非 completed 事件原样返回", func(t *testing.T) {
		event := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
		out, _, _ := injectResponsesUsageToCompletedEvent(event, requestBody, "hi", envCfg)
		if out != event {
			t.Fatalf("事件被改动:\n got  %q\n want %q", out, event)
		}
	})
}

// TestInjectUsageIntoMultiLineDataEvent 覆盖 JSON 被拆到多条连续 data 行的兜底路径。
// 收敛前该分支的 jsonEnd 默认为 0：当 data 行区间后没有空行时，
// 重建会从第 0 行起把整个事件再追加一遍，客户端会收到一条被替换过的
// data 行 + 一份原始（usage 未注入）的重复事件。
func TestInjectUsageIntoMultiLineDataEvent(t *testing.T) {
	t.Run("data 行后有空行", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\ndata: \"response\":{\"id\":\"resp_1\"}}\n\n"
		out, ok := injectUsageIntoMultiLineDataEvent(event, 11, 7, 18)
		if !ok {
			t.Fatal("ok = false, want true")
		}

		want := "event: response.completed\ndata: {\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"total_tokens\":18}},\"type\":\"response.completed\"}\n\n"
		if out != want {
			t.Fatalf("got  %q\nwant %q", out, want)
		}
	})

	t.Run("data 行后无空行时不重复整段", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\ndata: \"response\":{\"id\":\"resp_1\"}}"
		out, ok := injectUsageIntoMultiLineDataEvent(event, 11, 7, 18)
		if !ok {
			t.Fatal("ok = false, want true")
		}

		if n := strings.Count(out, "data:"); n != 1 {
			t.Fatalf("data 行数量 = %d, want 1（多行载荷应合并为一行，且不重复原始事件）\n out %q", n, out)
		}
		assertResponsesUsage(t, out, 11, 7)
	})

	t.Run("无 data 行返回 false", func(t *testing.T) {
		if _, ok := injectUsageIntoMultiLineDataEvent("event: ping\n\n", 1, 2, 3); ok {
			t.Fatal("ok = true, want false")
		}
	})

	t.Run("拼不出合法 JSON 返回 false", func(t *testing.T) {
		if _, ok := injectUsageIntoMultiLineDataEvent("data: {broken\n\n", 1, 2, 3); ok {
			t.Fatal("ok = true, want false")
		}
	})
}

func assertSameLineStructure(t *testing.T, in, out string) {
	t.Helper()
	if gotN, wantN := strings.Count(out, "\n"), strings.Count(in, "\n"); gotN != wantN {
		t.Fatalf("换行数量 = %d, want %d\n in  %q\n out %q", gotN, wantN, in, out)
	}
	if strings.HasSuffix(out, "\n\n\n") {
		t.Fatalf("结果多出一个换行: %q", out)
	}
}

func assertResponsesUsage(t *testing.T, event string, wantInput, wantOutput int) {
	t.Helper()

	var payload string
	for _, line := range strings.Split(event, "\n") {
		if strings.HasPrefix(line, "data:") {
			payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			break
		}
	}
	if payload == "" {
		t.Fatalf("事件中没有 data 行: %q", event)
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		t.Fatalf("data 载荷无法解析: %v (%q)", err, payload)
	}
	response, ok := data["response"].(map[string]interface{})
	if !ok {
		t.Fatalf("缺少 response: %#v", data)
	}
	usage, ok := response["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("缺少 response.usage: %#v", response)
	}
	if got, _ := usage["input_tokens"].(float64); int(got) != wantInput {
		t.Errorf("input_tokens = %v, want %d", usage["input_tokens"], wantInput)
	}
	if got, _ := usage["output_tokens"].(float64); int(got) != wantOutput {
		t.Errorf("output_tokens = %v, want %d", usage["output_tokens"], wantOutput)
	}
}

// TestStripAccumulatedCache 锁定透传分支累积式缓存统计剥离契约：
// grok-4.6 等 OpenAI 兼容上游的 input_tokens_details.cached_tokens 是跨请求
// 单调递增的累积命中量，原样下发会让 Cursor 误判上下文一直满、反复触发压缩。
func TestStripAccumulatedCache(t *testing.T) {
	dummyBodyWithoutPreviousID := []byte(`{"model":"grok-4.6","input":"hello"}`)
	dummyBodyWithPreviousID := []byte(`{"model":"gpt-5.6-luna","input":"hello","previous_response_id":"resp_123"}`)

	t.Run("流式: 剥离 OpenAI 累积 cached_tokens 与派生 cache_read", func(t *testing.T) {
		// 上游返回 input_tokens=205071(含缓存), cached_tokens=204800 → 代理已减为 271
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":271,\"output_tokens\":188,\"total_tokens\":459,\"input_tokens_details\":{\"cached_tokens\":204800}}}}\n\n"
		out := stripAccumulatedCacheFromCompletedEvent(event, dummyBodyWithoutPreviousID)

		data := extractCompletedPayload(t, out)
		usage := data["response"].(map[string]interface{})["usage"].(map[string]interface{})
		if _, exists := usage["input_tokens_details"]; exists {
			t.Errorf("input_tokens_details 应被剥离")
		}
		if _, exists := usage["cache_read_input_tokens"]; exists {
			t.Errorf("派生的 cache_read_input_tokens 应被剥离")
		}
		if got, _ := usage["input_tokens"].(float64); int(got) != 271 {
			t.Errorf("input_tokens 被改动: %v, want 271", usage["input_tokens"])
		}
		if got, _ := usage["output_tokens"].(float64); int(got) != 188 {
			t.Errorf("output_tokens 被改动: %v, want 188", usage["output_tokens"])
		}
		// 行结构保持（换行数量不变，不产生多余换行）
		assertSameLineStructure(t, event, out)
	})

	t.Run("流式: 链式请求且缓存是 input 子集时保留", func(t *testing.T) {
		// OpenAI 语义下 input_tokens 已含缓存命中部分，cached ≤ input 说明上游报数正常
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":919612,\"output_tokens\":532,\"total_tokens\":920144,\"input_tokens_details\":{\"cached_tokens\":913152}}}}\n\n"
		out := stripAccumulatedCacheFromCompletedEvent(event, dummyBodyWithPreviousID)

		data := extractCompletedPayload(t, out)
		usage := data["response"].(map[string]interface{})["usage"].(map[string]interface{})
		if _, exists := usage["input_tokens_details"]; !exists {
			t.Errorf("缓存是 input 子集时 input_tokens_details 应保留")
		}
		if got, _ := usage["input_tokens"].(float64); int(got) != 919612 {
			t.Errorf("input_tokens 不应被改动: %v, want 919612", usage["input_tokens"])
		}
		assertSameLineStructure(t, event, out)
	})

	// 以下两例锁定本次修复的核心判据：cached_tokens > input_tokens 在 OpenAI 语义下
	// 不可能成立（缓存必须是 input 的子集），因此它自证了该字段是跨请求累积器。这个不等式
	// 不依赖本地估算、也不依赖请求是否链式，所以链式请求同样适用。
	// 收敛前两个剥离函数都以 hasPreviousResponseID 直接返回，而 Cursor 从第二轮起每次都带
	// previous_response_id——反累积逻辑只在第一轮生效，之后客户端一直看到满缓存、数字压缩
	// 后也不下降，压缩于是无限循环。
	t.Run("流式: 链式请求遇到累积式缓存仍然剥离", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":271,\"output_tokens\":188,\"total_tokens\":459,\"input_tokens_details\":{\"cached_tokens\":204800}}}}\n\n"
		out := stripAccumulatedCacheFromCompletedEvent(event, dummyBodyWithPreviousID)

		usage := extractCompletedPayload(t, out)["response"].(map[string]interface{})["usage"].(map[string]interface{})
		if _, exists := usage["input_tokens_details"]; exists {
			t.Errorf("cached_tokens 超过 input_tokens 时应剥离 input_tokens_details")
		}
		if _, exists := usage["cache_read_input_tokens"]; exists {
			t.Errorf("派生的 cache_read_input_tokens 应被剥离")
		}
		if got, _ := usage["input_tokens"].(float64); int(got) != 271 {
			t.Errorf("input_tokens 被改动: %v, want 271", usage["input_tokens"])
		}
		assertSameLineStructure(t, event, out)
	})

	t.Run("非流式: 链式请求遇到累积式缓存仍然剥离", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{
				InputTokens:          271,
				OutputTokens:         188,
				CacheReadInputTokens: 204800,
				InputTokensDetails:   &types.InputTokensDetails{CachedTokens: 204800},
			},
		}
		stripAccumulatedCacheFromResponse(resp, dummyBodyWithPreviousID)
		if resp.Usage.InputTokensDetails != nil {
			t.Errorf("cached_tokens 超过 input_tokens 时应剥离 InputTokensDetails")
		}
		if resp.Usage.CacheReadInputTokens != 0 {
			t.Errorf("派生的 CacheReadInputTokens 应被剥离: %d", resp.Usage.CacheReadInputTokens)
		}
		if resp.Usage.InputTokens != 271 {
			t.Errorf("InputTokens 被改动: %d", resp.Usage.InputTokens)
		}
	})

	t.Run("流式: Claude 原生缓存保留 cache_read", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":100,\"output_tokens\":50,\"cache_creation_input_tokens\":200,\"cache_read_input_tokens\":150}}}\n\n"
		out := stripAccumulatedCacheFromCompletedEvent(event, dummyBodyWithoutPreviousID)

		usage := extractCompletedPayload(t, out)["response"].(map[string]interface{})["usage"].(map[string]interface{})
		if got, _ := usage["cache_read_input_tokens"].(float64); int(got) != 150 {
			t.Errorf("Claude cache_read 应保留: %v", usage["cache_read_input_tokens"])
		}
		if got, _ := usage["cache_creation_input_tokens"].(float64); int(got) != 200 {
			t.Errorf("Claude cache_creation 应保留: %v", usage["cache_creation_input_tokens"])
		}
	})

	t.Run("流式: 无缓存字段原样返回", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}}\n\n"
		out := stripAccumulatedCacheFromCompletedEvent(event, dummyBodyWithoutPreviousID)
		if out != event {
			t.Fatalf("无缓存字段应原样返回:\n got  %q\n want %q", out, event)
		}
	})

	t.Run("流式: 非 completed 事件原样返回", func(t *testing.T) {
		event := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
		out := stripAccumulatedCacheFromCompletedEvent(event, dummyBodyWithoutPreviousID)
		if out != event {
			t.Fatalf("非 completed 事件应原样返回")
		}
	})

	t.Run("非流式: 剥离 OpenAI 累积缓存", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{
				InputTokens:          271,
				OutputTokens:         188,
				CacheReadInputTokens: 204800, // 从 cached_tokens 派生
				InputTokensDetails:   &types.InputTokensDetails{CachedTokens: 204800},
			},
		}
		stripAccumulatedCacheFromResponse(resp, dummyBodyWithoutPreviousID)
		if resp.Usage.InputTokensDetails != nil {
			t.Errorf("InputTokensDetails 应被剥离")
		}
		if resp.Usage.CacheReadInputTokens != 0 {
			t.Errorf("派生的 CacheReadInputTokens 应被剥离: %d", resp.Usage.CacheReadInputTokens)
		}
		if resp.Usage.InputTokens != 271 {
			t.Errorf("InputTokens 被改动: %d", resp.Usage.InputTokens)
		}
	})

	t.Run("非流式: 链式请求且缓存是 input 子集时保留", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{
				InputTokens:        919612,
				OutputTokens:       532,
				InputTokensDetails: &types.InputTokensDetails{CachedTokens: 913152},
			},
		}
		stripAccumulatedCacheFromResponse(resp, dummyBodyWithPreviousID)
		if resp.Usage.InputTokensDetails == nil || resp.Usage.InputTokensDetails.CachedTokens != 913152 {
			t.Errorf("缓存是 input 子集时 InputTokensDetails 应保留")
		}
		if resp.Usage.InputTokens != 919612 {
			t.Errorf("InputTokens 被改动: %d", resp.Usage.InputTokens)
		}
	})

	t.Run("非流式: Claude 原生缓存保留", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{
				InputTokens:              100,
				CacheCreationInputTokens: 200,
				CacheReadInputTokens:     150,
			},
		}
		stripAccumulatedCacheFromResponse(resp, dummyBodyWithoutPreviousID)
		if resp.Usage.CacheReadInputTokens != 150 {
			t.Errorf("Claude cache_read 应保留: %d", resp.Usage.CacheReadInputTokens)
		}
		if resp.Usage.CacheCreationInputTokens != 200 {
			t.Errorf("Claude cache_creation 应保留: %d", resp.Usage.CacheCreationInputTokens)
		}
	})
}

// TestExtractResponsesUsageFromMapCacheSemantics 锁定 OpenAI 缓存字段的拆分口径。
//
// OpenAI 语义下 input_tokens 已经包含缓存命中部分，代理要把它拆成
// input（未命中）+ cache_read（命中）以对齐 Anthropic 口径。但 grok-4.6 等上游的
// cached_tokens 是跨请求单调递增的累积器，会远超本次 input_tokens；按子集去减会把
// 上报输入量压到 0，指标与计费全部失真。cached > input 这个不等式在 OpenAI 语义下
// 不可能成立，正是"该字段不是子集"的自证证据。
func TestExtractResponsesUsageFromMapCacheSemantics(t *testing.T) {
	t.Run("子集语义: 从 input_tokens 中拆出缓存命中量", func(t *testing.T) {
		usage := map[string]interface{}{
			"input_tokens":  float64(205071),
			"output_tokens": float64(188),
			"total_tokens":  float64(205259),
			"input_tokens_details": map[string]interface{}{
				"cached_tokens": float64(204800),
			},
		}
		got := extractResponsesUsageFromMap(usage)
		if got.InputTokens != 271 {
			t.Errorf("InputTokens = %d, want 271", got.InputTokens)
		}
		if got.CacheReadInputTokens != 204800 {
			t.Errorf("CacheReadInputTokens = %d, want 204800", got.CacheReadInputTokens)
		}
		if got.TotalTokens != 459 {
			t.Errorf("TotalTokens = %d, want 459（须与拆分后的口径一致）", got.TotalTokens)
		}
		if got.HasClaudeCache {
			t.Errorf("OpenAI 格式不应置 HasClaudeCache")
		}
	})

	t.Run("累积式: cached 超过 input 时不做减法", func(t *testing.T) {
		usage := map[string]interface{}{
			"input_tokens":  float64(271),
			"output_tokens": float64(188),
			"total_tokens":  float64(459),
			"input_tokens_details": map[string]interface{}{
				"cached_tokens": float64(204800),
			},
		}
		got := extractResponsesUsageFromMap(usage)
		if got.InputTokens != 271 {
			t.Errorf("InputTokens = %d, want 271（不得被累积器减成 0）", got.InputTokens)
		}
		if got.TotalTokens != 459 {
			t.Errorf("TotalTokens = %d, want 459", got.TotalTokens)
		}
		if got.CacheReadInputTokens != 204800 {
			t.Errorf("CacheReadInputTokens = %d, want 204800", got.CacheReadInputTokens)
		}
	})

	t.Run("Anthropic 显式 cache_read 时 input_tokens 不再拆分", func(t *testing.T) {
		usage := map[string]interface{}{
			"input_tokens":            float64(271),
			"output_tokens":           float64(188),
			"cache_read_input_tokens": float64(204800),
			"input_tokens_details": map[string]interface{}{
				"cached_tokens": float64(204800),
			},
		}
		got := extractResponsesUsageFromMap(usage)
		if got.InputTokens != 271 {
			t.Errorf("InputTokens = %d, want 271", got.InputTokens)
		}
		if !got.HasClaudeCache {
			t.Errorf("非零 cache_read_input_tokens 应置 HasClaudeCache")
		}
	})
}

func extractCompletedPayload(t *testing.T, event string) map[string]interface{} {
	t.Helper()
	for _, line := range strings.Split(event, "\n") {
		payload, isData := utilsParseSSEData(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			t.Fatalf("data 载荷解析失败: %v", err)
		}
		return data
	}
	t.Fatalf("事件无 data 行: %q", event)
	return nil
}

func utilsParseSSEData(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	payload := strings.TrimPrefix(line, "data:")
	payload = strings.TrimPrefix(payload, " ")
	return payload, true
}

// makeLargeResponsesRequestBody 造一个长上下文请求体，保证本地估算超过校正下限。
// 用全英文填充（估算按 3.5 字符/token）：140000 字符约 40k tokens，稳超 20000 下限。
func makeLargeResponsesRequestBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"model": "grok-4.6",
		"input": []interface{}{
			map[string]interface{}{"role": "user", "content": strings.Repeat("abcdefgh", 17500)},
		},
	})
	if err != nil {
		t.Fatalf("构造请求体失败: %v", err)
	}
	if estimated := utils.EstimateResponsesRequestTokens(body); estimated < utils.UsageSanityMinTokensForTest {
		t.Fatalf("测试请求体估算 %d 未达校正下限 %d，需调大填充", estimated, utils.UsageSanityMinTokensForTest)
	}
	return body
}

// TestCorrectUnderreportedInputTokens 锁定"上游少报 input_tokens"的校正契约。
//
// 背景：grok-4.6 对长上下文只回报未命中缓存的增量（实测 549KB 请求体只报 7723）。
// 剥离累积式缓存字段后 input_tokens 是客户端判断上下文占用的唯一依据，这个假小值会让
// Cursor 认为上下文为空、永不触发压缩，一路堆到撞上游请求体大小限制返回 413。
//
// 校正必须严格：只在证据确凿（估算值达下限且超上游报数 2 倍）时改，其余一律不动。
func TestCorrectUnderreportedInputTokens(t *testing.T) {
	envCfg := &config.EnvConfig{CorrectResponsesInputTokens: true}
	largeBody := makeLargeResponsesRequestBody(t)
	estimated := utils.EstimateResponsesRequestTokens(largeBody)

	t.Run("Codex client metadata disables local correction", func(t *testing.T) {
		var request map[string]interface{}
		if err := json.Unmarshal(largeBody, &request); err != nil {
			t.Fatalf("解析测试请求失败: %v", err)
		}
		request["client_metadata"] = map[string]interface{}{
			"x-codex-window-id":     "window-1",
			"x-codex-turn-metadata": "{\"request_kind\":\"turn\"}",
		}
		codexBody, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("构造 Codex 测试请求失败: %v", err)
		}
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":294466,\"output_tokens\":188,\"total_tokens\":294654}}}\n\n"
		if out := correctUnderreportedInputTokensInCompletedEvent(event, codexBody, envCfg); out != event {
			t.Fatalf("Codex usage 不应被本地估算覆盖:\n got  %q\n want %q", out, event)
		}

		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{InputTokens: 294466, OutputTokens: 188, TotalTokens: 294654},
		}
		clientUsage := resp.Usage
		correctUnderreportedInputTokensInResponse(resp, &clientUsage, codexBody, envCfg)
		if clientUsage.InputTokens != 294466 || clientUsage.TotalTokens != 294654 {
			t.Fatalf("Codex 非流式 usage 不应被本地估算覆盖: %+v", clientUsage)
		}
	})

	t.Run("流式: 上游严重少报则校正并同步 total", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":7723,\"output_tokens\":188,\"total_tokens\":7911}}}\n\n"
		out := correctUnderreportedInputTokensInCompletedEvent(event, largeBody, envCfg)

		usage := extractCompletedPayload(t, out)["response"].(map[string]interface{})["usage"].(map[string]interface{})
		if got, _ := usage["input_tokens"].(float64); int(got) != estimated {
			t.Errorf("input_tokens 应校正为 %d, got %v", estimated, usage["input_tokens"])
		}
		if got, _ := usage["total_tokens"].(float64); int(got) != estimated+188 {
			t.Errorf("total_tokens 应同步为 %d, got %v", estimated+188, usage["total_tokens"])
		}
		if got, _ := usage["output_tokens"].(float64); int(got) != 188 {
			t.Errorf("output_tokens 不应被改动: %v", usage["output_tokens"])
		}
		assertSameLineStructure(t, event, out)
	})

	t.Run("流式: 上游多报也校正（压缩后假大值）", func(t *testing.T) {
		// 实测案例：压缩后 24KB 请求被上游报成 209736，客户端据此再次触发压缩
		smallBody := []byte(`{"model":"grok-4.6","input":"summary + new question"}`)
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":209736,\"output_tokens\":188,\"total_tokens\":209924}}}\n\n"
		out := correctUnderreportedInputTokensInCompletedEvent(event, smallBody, envCfg)
		if out == event {
			t.Fatalf("多报的假大值应被校正")
		}
		usage := extractCompletedPayload(t, out)["response"].(map[string]interface{})["usage"].(map[string]interface{})
		if got, _ := usage["input_tokens"].(float64); int(got) > 20000 {
			t.Errorf("假大值应被压回估算量级, got %v", usage["input_tokens"])
		}
	})

	t.Run("流式: 携带 previous_response_id 时跳过校正", func(t *testing.T) {
		// 链式增量请求：requestBody 极小，但服务端历史累计 919612 tokens，不能被误判为多报而改写
		chainBody := []byte(`{"model":"gpt-5.6-luna","input":"next question","previous_response_id":"resp_123"}`)
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":919612,\"output_tokens\":532,\"total_tokens\":920144}}}\n\n"
		out := correctUnderreportedInputTokensInCompletedEvent(event, chainBody, envCfg)
		if out != event {
			t.Fatalf("带 previous_response_id 的链式请求不应被改写:\n got  %q\n want %q", out, event)
		}
	})

	t.Run("非流式: 携带 previous_response_id 时跳过校正", func(t *testing.T) {
		chainBody := []byte(`{"model":"gpt-5.6-luna","input":"next question","previous_response_id":"resp_123"}`)
		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{InputTokens: 919612, OutputTokens: 532, TotalTokens: 920144},
		}
		clientUsage := resp.Usage
		correctUnderreportedInputTokensInResponse(resp, &clientUsage, chainBody, envCfg)
		if clientUsage.InputTokens != 919612 || clientUsage.TotalTokens != 920144 {
			t.Errorf("带 previous_response_id 的非流式请求不应被改写: got %+v", clientUsage)
		}
	})

	t.Run("流式: 上游回报可信则原样返回", func(t *testing.T) {
		// 上游报数与本地估算同量级（未达 2 倍差），不得改动
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":" +
			strconv.Itoa(estimated) + ",\"output_tokens\":188}}}\n\n"
		out := correctUnderreportedInputTokensInCompletedEvent(event, largeBody, envCfg)
		if out != event {
			t.Fatalf("可信回报应原样返回:\n got  %q\n want %q", out, event)
		}
	})

	t.Run("流式: 小请求体不进入校正路径", func(t *testing.T) {
		smallBody := []byte(`{"model":"grok-4.6","input":"hello"}`)
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":5}}}\n\n"
		out := correctUnderreportedInputTokensInCompletedEvent(event, smallBody, envCfg)
		if out != event {
			t.Fatalf("小请求体不应触发校正:\n got  %q\n want %q", out, event)
		}
	})

	t.Run("流式: 开关关闭则原样返回", func(t *testing.T) {
		off := &config.EnvConfig{CorrectResponsesInputTokens: false}
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":7723,\"output_tokens\":188}}}\n\n"
		out := correctUnderreportedInputTokensInCompletedEvent(event, largeBody, off)
		if out != event {
			t.Fatalf("开关关闭应原样返回:\n got  %q\n want %q", out, event)
		}
	})

	t.Run("非流式: 上游严重少报则校正副本并同步 total", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{InputTokens: 7723, OutputTokens: 188, TotalTokens: 7911},
		}
		clientUsage := resp.Usage
		correctUnderreportedInputTokensInResponse(resp, &clientUsage, largeBody, envCfg)
		if clientUsage.InputTokens != estimated {
			t.Errorf("客户端副本 InputTokens 应校正为 %d, got %d", estimated, clientUsage.InputTokens)
		}
		if clientUsage.TotalTokens != estimated+188 {
			t.Errorf("TotalTokens 应同步为 %d, got %d", estimated+188, clientUsage.TotalTokens)
		}
		// 副本隔离契约：原件保持上游原值，指标/计费不受校正影响
		if resp.Usage.InputTokens != 7723 {
			t.Errorf("原件 InputTokens 不应被改动: got %d, want 7723", resp.Usage.InputTokens)
		}
		if resp.Usage.TotalTokens != 7911 {
			t.Errorf("原件 TotalTokens 不应被改动: got %d, want 7911", resp.Usage.TotalTokens)
		}
		if clientUsage.OutputTokens != 188 {
			t.Errorf("OutputTokens 不应被改动: %d", clientUsage.OutputTokens)
		}
	})

	t.Run("非流式: 上游回报可信则不动", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{InputTokens: estimated, OutputTokens: 188, TotalTokens: estimated + 188},
		}
		clientUsage := resp.Usage
		correctUnderreportedInputTokensInResponse(resp, &clientUsage, largeBody, envCfg)
		if clientUsage.InputTokens != estimated {
			t.Errorf("可信回报不应被改动: got %d, want %d", clientUsage.InputTokens, estimated)
		}
		if clientUsage.TotalTokens != estimated+188 {
			t.Errorf("TotalTokens 不应被改动: got %d", clientUsage.TotalTokens)
		}
	})

	// 以下两例锁定「缓存必须计入判定基数」：Claude 语义下 input_tokens 不含缓存部分，
	// 若只拿 input_tokens 比对估算值，会把正确分开报数的上游误判为少报（271 vs 205071），
	// 校正后 input 与保留下来的 cache_read 相加就成了双倍上下文。
	t.Run("流式: Claude 语义分开报缓存不得误判为少报", func(t *testing.T) {
		// input 很小但 cache 很大，两者之和与估算同量级 → 上游其实报得准，必须原样返回
		cacheRead := estimated - 1000
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":271,\"output_tokens\":188," +
			"\"cache_read_input_tokens\":" + strconv.Itoa(cacheRead) + ",\"cache_creation_input_tokens\":500}}}\n\n"
		out := correctUnderreportedInputTokensInCompletedEvent(event, largeBody, envCfg)
		if out != event {
			t.Fatalf("Claude 语义分开报缓存应原样返回:\n got  %q\n want %q", out, event)
		}
	})

	t.Run("非流式: 校正值扣掉上游已单独回报的缓存量", func(t *testing.T) {
		// 缓存很小、确实少报 → 校正，但校正值必须扣掉缓存，避免与 cache 字段重叠
		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{
				InputTokens:          100,
				OutputTokens:         188,
				CacheReadInputTokens: 1000,
			},
		}
		clientUsage := resp.Usage
		correctUnderreportedInputTokensInResponse(resp, &clientUsage, largeBody, envCfg)
		want := estimated - 1000
		if clientUsage.InputTokens != want {
			t.Errorf("校正值应扣掉缓存量: got %d, want %d", clientUsage.InputTokens, want)
		}
		if clientUsage.CacheReadInputTokens != 1000 {
			t.Errorf("缓存字段不应被改动: %d", clientUsage.CacheReadInputTokens)
		}
		// input + cache 之和回到真实上下文规模，不双倍
		if clientUsage.InputTokens+clientUsage.CacheReadInputTokens != estimated {
			t.Errorf("input+cache 应等于估算总量 %d, got %d",
				estimated, clientUsage.InputTokens+clientUsage.CacheReadInputTokens)
		}
		if resp.Usage.InputTokens != 100 {
			t.Errorf("原件 InputTokens 不应被改动: got %d, want 100", resp.Usage.InputTokens)
		}
	})
}

func TestNormalizeContextDetailsInputTokens(t *testing.T) {
	t.Run("流式使用明确的当前上下文值", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":272884,\"output_tokens\":1484,\"total_tokens\":274368,\"context_details\":{\"input_tokens\":99463}}}}\n\n"
		out := normalizeContextDetailsInputTokensInCompletedEvent(event, false)
		usage := extractCompletedPayload(t, out)["response"].(map[string]interface{})["usage"].(map[string]interface{})
		if got := int(usage["input_tokens"].(float64)); got != 99463 {
			t.Fatalf("input_tokens = %d, want 99463", got)
		}
		if got := int(usage["total_tokens"].(float64)); got != 100947 {
			t.Fatalf("total_tokens = %d, want 100947", got)
		}
	})

	t.Run("缺少明确上下文值时保持原样", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":5,\"total_tokens\":105}}}\n\n"
		if out := normalizeContextDetailsInputTokensInCompletedEvent(event, false); out != event {
			t.Fatalf("缺少 context_details 时不应改写")
		}
	})

	t.Run("非流式修改客户端副本", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Usage: types.ResponsesUsage{InputTokens: 272884, OutputTokens: 1484, TotalTokens: 274368},
			Extra: map[string]interface{}{"usage": map[string]interface{}{
				"input_tokens": 272884, "output_tokens": 1484,
				"context_details": map[string]interface{}{"input_tokens": 99463.0},
			}},
		}
		clientUsage := resp.Usage
		normalizeContextDetailsInputTokensInResponse(resp, &clientUsage, false)
		if clientUsage.InputTokens != 99463 || clientUsage.TotalTokens != 100947 {
			t.Fatalf("客户端副本 usage = %+v", clientUsage)
		}
		if resp.Usage.InputTokens != 272884 {
			t.Fatalf("原始 usage 不应被修改: %+v", resp.Usage)
		}
	})
}
