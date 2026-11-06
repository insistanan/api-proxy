package converters

import (
	"context"
	"strings"
	"testing"
)

// collectGeminiResponsesEvents 逐行喂入 Gemini SSE，返回累积产出的 Responses 事件。
func collectGeminiResponsesEvents(t *testing.T, lines []string) []string {
	t.Helper()
	var state any
	var all []string
	for i, line := range lines {
		events, err := ConvertGeminiStreamToResponses(
			context.Background(), "gemini-2.5-pro",
			[]byte(`{"model":"gemini-2.5-pro"}`), []byte(line), &state,
		)
		if err != nil {
			t.Fatalf("第 %d 行转换失败: %v", i, err)
		}
		all = append(all, events...)
	}
	final, err := FinishUpstreamStreamToResponses(ResponsesUpstreamGemini, []byte(`{"model":"gemini-2.5-pro"}`), &state)
	if err != nil {
		t.Fatalf("流尾定稿失败: %v", err)
	}
	all = append(all, final...)
	return all
}

func countResponsesEvents(events []string, eventType string) int {
	n := 0
	for _, ev := range events {
		if strings.Contains(ev, `"type":"`+eventType+`"`) {
			n++
		}
	}
	return n
}

// Gemini 原生流的每个 chunk 都带累积 usageMetadata，只有最后一个带 finishReason。
// 定稿必须由 finishReason 触发，不能由"本 chunk 有 usage"触发，否则第一个 chunk 就会
// 发出 response.completed，把后续所有内容甩到定稿之后。
func TestConvertGeminiStreamToResponses_CumulativeUsageDoesNotCompleteEarly(t *testing.T) {
	events := collectGeminiResponsesEvents(t, []string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello! How"}],"role":"model"},"index":0}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`,
		`data: {"candidates":[{"content":{"parts":[{"text":" can I help?"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":9,"totalTokenCount":11}}`,
	})

	if got := countResponsesEvents(events, "response.completed"); got != 1 {
		t.Fatalf("response.completed 应恰好出现 1 次，实际 %d 次", got)
	}

	// 定稿必须在两段文本都发出之后。
	completedAt, lastDeltaAt := -1, -1
	for i, ev := range events {
		if strings.Contains(ev, `"type":"response.completed"`) {
			completedAt = i
		}
		if strings.Contains(ev, `"type":"response.output_text.delta"`) {
			lastDeltaAt = i
		}
	}
	if completedAt < lastDeltaAt {
		t.Fatalf("response.completed(%d) 早于最后一个文本增量(%d)，内容被甩到定稿之后", completedAt, lastDeltaAt)
	}

	// usage 取末次累积值，不是首个 chunk 的 3。
	if !strings.Contains(events[completedAt], `"output_tokens":9`) {
		t.Errorf("response.completed 应携带末次累积 output_tokens=9，实际: %s", events[completedAt])
	}
}

// Gemini 的 promptTokenCount 含 cachedContentTokenCount，Responses 的 input_tokens
// 必须是扣除后的新增输入，缓存量另由 input_tokens_details.cached_tokens 单列。
// 与 converters.ExtractUsageMetrics、providers/gemini.go 两条路径同一口径。
func TestConvertGeminiStreamToResponses_CachedPromptSubtractedFromInput(t *testing.T) {
	events := collectGeminiResponsesEvents(t, []string{
		`data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1000,"cachedContentTokenCount":900,"candidatesTokenCount":20,"thoughtsTokenCount":7,"totalTokenCount":1027}}`,
	})

	completed := ""
	for _, ev := range events {
		if strings.Contains(ev, `"type":"response.completed"`) {
			completed = ev
		}
	}
	if completed == "" {
		t.Fatal("未产出 response.completed")
	}

	for _, want := range []string{
		`"input_tokens":100`,
		// candidatesTokenCount(20) 不含 thoughtsTokenCount(7)，而 Responses 语义下
		// output_tokens 含 reasoning，所以出口必须是 27；reasoning_tokens 只是明细拆分，
		// 客户端不得再求和。改回 20 就是把 thinking token 从计费里抹掉。
		`"output_tokens":27`,
		`"cached_tokens":900`,
		`"reasoning_tokens":7`,
		`"total_tokens":127`,
	} {
		if !strings.Contains(completed, want) {
			t.Errorf("response.completed 缺少 %s，实际: %s", want, completed)
		}
	}
}

// 上游把 cachedContentTokenCount 报得比 promptTokenCount 还大时（异常回包），
// input_tokens 必须钳制到 0，不能让负数流进客户端与指标。
func TestConvertGeminiStreamToResponses_NegativeInputClampedToZero(t *testing.T) {
	events := collectGeminiResponsesEvents(t, []string{
		`data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":99,"candidatesTokenCount":5}}`,
	})

	for _, ev := range events {
		if strings.Contains(ev, `"type":"response.completed"`) {
			if !strings.Contains(ev, `"input_tokens":0`) {
				t.Errorf("input_tokens 应钳制为 0，实际: %s", ev)
			}
		}
	}
}

// 尾包终值必须进入唯一的 response.completed，不得提前按零 usage 定稿。
func TestConvertGeminiStreamToResponses_TrailingUsageChunkKeepsSingleCompleted(t *testing.T) {
	events := collectGeminiResponsesEvents(t, []string{
		`data: {"candidates":[{"content":{"parts":[{"text":"done"}],"role":"model"},"finishReason":"STOP","index":0}]}`,
		`data: {"usageMetadata":{"promptTokenCount":42,"candidatesTokenCount":8,"totalTokenCount":50}}`,
	})

	if got := countResponsesEvents(events, "response.completed"); got != 1 {
		t.Fatalf("response.completed 应恰好出现 1 次（completed 幂等），实际 %d 次", got)
	}
	completed := events[len(events)-1]
	if !strings.Contains(completed, `"input_tokens":42`) || !strings.Contains(completed, `"output_tokens":8`) {
		t.Fatalf("完成事件必须携带尾包 usage: %s", completed)
	}
}

func TestResponsesConverterFinalizationRequiresSignalAndIsIdempotent(t *testing.T) {
	for _, upstream := range []string{ResponsesUpstreamGemini, ResponsesUpstreamOpenAI} {
		t.Run(upstream, func(t *testing.T) {
			var state any
			prefix, terminal := `data: {"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`, `data: {"candidates":[{"finishReason":"STOP"}]}`
			if upstream == ResponsesUpstreamOpenAI {
				prefix = `data: {"choices":[{"delta":{"content":"hello"}}]}`
				terminal = `data: {"choices":[{"finish_reason":"stop"}]}`
			}
			_, err := ConvertUpstreamStreamLineToResponses(context.Background(), upstream, "m", nil, []byte(prefix), &state)
			if err != nil {
				t.Fatal(err)
			}
			before, err := FinishUpstreamStreamToResponses(upstream, nil, &state)
			if err != nil || len(before) != 0 {
				t.Fatalf("没有定稿信号不能补完成事件: %v, %v", before, err)
			}
			_, err = ConvertUpstreamStreamLineToResponses(context.Background(), upstream, "m", nil, []byte(terminal), &state)
			if err != nil {
				t.Fatal(err)
			}
			final, err := FinishUpstreamStreamToResponses(upstream, nil, &state)
			if err != nil || countResponsesEvents(final, "response.completed") != 1 {
				t.Fatalf("应恰好定稿一次: %v, %v", final, err)
			}
			again, err := FinishUpstreamStreamToResponses(upstream, nil, &state)
			if err != nil || len(again) != 0 {
				t.Fatalf("重复收尾不能补第二个完成事件: %v, %v", again, err)
			}
			extra, err := ConvertUpstreamStreamLineToResponses(context.Background(), upstream, "m", nil, []byte(prefix), &state)
			if err != nil || len(extra) != 0 {
				t.Fatalf("定稿后不能重开内容: %v, %v", extra, err)
			}
		})
	}
}
