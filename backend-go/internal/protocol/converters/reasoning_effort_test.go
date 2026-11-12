package converters

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestNormalizeReasoningEffortForConstrainedUpstream(t *testing.T) {
	tests := []struct {
		name    string
		effort  string
		want    string
		wantErr bool
	}{
		{name: "标准等级", effort: "high", want: "high"},
		{name: "最大等级", effort: "max", want: "max"},
		{name: "超高等级", effort: "ultra", want: "max"},
		{name: "未知等级", effort: "future", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeReasoningEffortForConstrainedUpstream(tt.effort)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeReasoningEffortForConstrainedUpstream() 错误 = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("NormalizeReasoningEffortForConstrainedUpstream() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodexReasoningEffortConversions(t *testing.T) {
	t.Run("原始 Responses JSON 经 OpenAI Chat 校验和转换", func(t *testing.T) {
		for _, effort := range []string{"max", "ultra"} {
			input := []byte(`{"model":"gpt-5.6-terra","input":"hi","reasoning":{"effort":"` + effort + `"}}`)
			if err := ValidateResponsesToOpenAIChatRequest(input); err != nil {
				t.Fatalf("reasoning.effort=%q 校验失败: %v", effort, err)
			}
			converted := ConvertResponsesToOpenAIChatRequest("gpt-5.6-terra", input, false)
			if got := gjson.GetBytes(converted, "reasoning_effort").String(); got != "max" {
				t.Errorf("reasoning.effort=%q 转换为 %q, 期望 max", effort, got)
			}
		}
	})

	t.Run("OpenAI Chat 保留 max 并归一化 ultra", func(t *testing.T) {
		for _, effort := range []string{"max", "ultra"} {
			got, err := responsesReasoningEffortToOpenAIChat(map[string]interface{}{"effort": effort})
			if err != nil {
				t.Fatalf("reasoning.effort=%q 转换失败: %v", effort, err)
			}
			if got != "max" {
				t.Errorf("reasoning.effort=%q 转换为 %q, 期望 max", effort, got)
			}
		}
	})

	t.Run("Claude 将 max 和 ultra 映射为 xhigh 预算", func(t *testing.T) {
		for _, effort := range []string{"max", "ultra"} {
			thinking, err := ResponsesReasoningToClaudeThinking(map[string]interface{}{"effort": effort}, 10_000)
			if err != nil {
				t.Fatalf("reasoning.effort=%q 转换失败: %v", effort, err)
			}
			got, ok := thinking.(map[string]interface{})
			if !ok {
				t.Fatalf("reasoning.effort=%q 转换结果类型为 %T，期望 map[string]interface{}", effort, thinking)
			}
			// xhigh 与 high 区分：xhigh 使用 0.9 比例（high 为 0.8），max_output_tokens=10000 时得 9000。
			if got["budget_tokens"] != 9_000 {
				t.Errorf("reasoning.effort=%q budget_tokens = %v, 期望 9000", effort, got["budget_tokens"])
			}
		}
	})

	t.Run("Gemini 将 max 和 ultra 映射为 xhigh 预算", func(t *testing.T) {
		for _, effort := range []string{"max", "ultra"} {
			thinking, err := ResponsesReasoningToGeminiThinking("gemini-2.5-flash", map[string]interface{}{"effort": effort})
			if err != nil {
				t.Fatalf("reasoning.effort=%q 转换失败: %v", effort, err)
			}
			if thinking.ThinkingBudget == nil || *thinking.ThinkingBudget != 16_384 {
				t.Errorf("reasoning.effort=%q thinking budget = %v, 期望 16384", effort, thinking.ThinkingBudget)
			}
		}
	})
}
