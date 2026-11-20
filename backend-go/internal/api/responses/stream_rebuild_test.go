package responses

import (
	"encoding/json"
	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"strconv"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// 上游正值与全缓存输入不能因本地估算被覆盖。
func TestResponsesUsagePreservesOneTokenCounts(t *testing.T) {
	envCfg := &config.EnvConfig{}
	request := []byte(`{"input":"a long input should not replace a valid upstream count"}`)
	response := &types.ResponsesResponse{Usage: types.ResponsesUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
	patchResponsesUsage(response, request, envCfg)
	if response.Usage.InputTokens != 1 || response.Usage.OutputTokens != 1 || response.Usage.TotalTokens != 2 {
		t.Fatalf("有效的 1 token 不能被估算覆盖: %+v", response.Usage)
	}
	for _, usage := range []string{
		`{"input_tokens":1,"output_tokens":1,"total_tokens":2}`,
		`{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":1}}`,
	} {
		event := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":" + usage + "}}\n\n"
		found, needsPatch, _ := checkResponsesEventUsage(event, false)
		if !found || needsPatch {
			t.Fatalf("有效统计无需补全，全部输入命中缓存也一样: %s", event)
		}
	}
}

func TestResponsesUsageOutputEstimateUpdatesTotal(t *testing.T) {
	envCfg := &config.EnvConfig{}
	response := &types.ResponsesResponse{
		Usage:  types.ResponsesUsage{InputTokens: 42, TotalTokens: 42},
		Output: []types.ResponsesItem{{Type: "message", Content: "hello world"}},
	}
	patchResponsesUsage(response, nil, envCfg)
	if response.Usage.OutputTokens <= 0 || response.Usage.TotalTokens != 42+response.Usage.OutputTokens {
		t.Fatalf("仅补输出时也必须更新总量: %+v", response.Usage)
	}
	for _, cached := range []int{0, 42} {
		event := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":42,\"output_tokens\":0,\"total_tokens\":42,\"input_tokens_details\":{\"cached_tokens\":" + strconv.Itoa(cached) + "}}}}\n\n"
		_, _, collected := checkResponsesEventUsage(event, false)
		patched := patchResponsesCompletedEventUsageWithTokens(event, []byte(`{"input":"do not estimate this input"}`), 7, &collected, envCfg)
		assertResponsesUsage(t, patched, 42, 7)
		payload := extractCompletedPayload(t, patched)
		usage := payload["response"].(map[string]interface{})["usage"].(map[string]interface{})
		if total, _ := usage["total_tokens"].(float64); total != 49 {
			t.Fatalf("响应总量必须包含原始输入与补全输出: %v", total)
		}
		if collected.InputTokens != 42-cached || collected.TotalTokens != 49-cached {
			t.Fatalf("上游输入与采集的未缓存输入不能混用: cached=%d collected=%+v", cached, collected)
		}
	}
}

func TestResponsesUsageKeepsCacheOnlyInput(t *testing.T) {
	response := &types.ResponsesResponse{
		Usage:  types.ResponsesUsage{CacheReadInputTokens: 100},
		Output: []types.ResponsesItem{{Type: "message", Content: "hello world"}},
	}
	patchResponsesUsage(response, []byte(`{"input":"cached context"}`), &config.EnvConfig{})
	if response.Usage.InputTokens != 0 || response.Usage.CacheReadInputTokens != 100 || response.Usage.TotalTokens != response.Usage.OutputTokens {
		t.Fatalf("全缓存输入无需伪造未缓存输入: %+v", response.Usage)
	}
}

func TestFinalizeResponsesUsageKeepsCollectionSeparate(t *testing.T) {
	envCfg := &config.EnvConfig{CorrectResponsesInputTokens: true}
	request := []byte(`{"input":"` + strings.Repeat("context ", 1000) + `"}`)
	for _, multiline := range []bool{false, true} {
		t.Run(strconv.FormatBool(multiline), func(t *testing.T) {
			payload := `{"type":"response.completed","extension":"keep","response":{"id":"resp_1","custom":true,"usage":{"input_tokens":100,"output_tokens":0,"total_tokens":100,"input_tokens_details":{"cached_tokens":200},"context_details":{"input_tokens":50}}}}`
			event := "id: event_1\nevent: response.completed\ndata: " + payload + "\n\n"
			if multiline {
				event = strings.Replace(event, `,"response":`, ",\ndata: \"response\":", 1)
			}
			collected := responsesStreamUsage{InputTokens: 100}
			out := finalizeResponsesCompletedUsage(event, request, 7, &collected, true, true, true, envCfg)
			data := extractCompletedPayload(t, out)
			response := data["response"].(map[string]interface{})
			usage := response["usage"].(map[string]interface{})
			if usage["input_tokens"] != float64(50) || usage["output_tokens"] != float64(7) || usage["total_tokens"] != float64(57) {
				t.Fatalf("补全后的 int 必须能被后续规则读取，明确上下文值最后生效: %v", usage)
			}
			if _, exists := usage["input_tokens_details"]; exists {
				t.Fatal("累积缓存应从客户端副本剥离")
			}
			if collected.InputTokens != 100 || collected.OutputTokens != 7 || collected.TotalTokens != 107 {
				t.Fatalf("客户端上下文修正不得回写采集值: %+v", collected)
			}
			if data["extension"] != "keep" || response["custom"] != true || !strings.HasPrefix(out, "id: event_1\nevent: response.completed\n") || !strings.HasSuffix(out, "\n\n") {
				t.Fatalf("未知字段与 SSE 边界必须保留: %s", out)
			}
		})
	}
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
	if estimated := tokenusage.EstimateResponsesRequestTokens(body); estimated < tokenusage.UsageSanityMinTokensForTest {
		t.Fatalf("测试请求体估算 %d 未达校正下限 %d，需调大填充", estimated, tokenusage.UsageSanityMinTokensForTest)
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
	estimated := tokenusage.EstimateResponsesRequestTokens(largeBody)

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
