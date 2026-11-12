package config

import (
	"encoding/json"
	"testing"
)

func TestReasoningOutputMode_ConfigValidation(t *testing.T) {
	for _, mode := range []string{"", "preserve", "hide", "unknown"} {
		body, err := json.Marshal(map[string]interface{}{"reasoningOutputMode": mode, "parseEmbeddedReasoning": true})
		if err != nil {
			t.Fatal(err)
		}
		var upstream UpstreamConfig
		err = json.Unmarshal(body, &upstream)
		if mode == "unknown" {
			if err == nil {
				t.Fatal("未知输出模式必须拒绝")
			}
			continue
		}
		if err != nil || upstream.ReasoningOutputMode != mode || !upstream.ParseEmbeddedReasoning {
			t.Fatalf("mode=%q upstream=%+v err=%v", mode, upstream, err)
		}
	}
}

func TestReasoningOutputMode_PartialUpdateKeepsAndClearsOptions(t *testing.T) {
	upstream := UpstreamConfig{ReasoningOutputMode: "hide", ParseEmbeddedReasoning: true}
	if _, err := applyCommonUpdates(&upstream, 0, UpstreamUpdate{}, "test"); err != nil {
		t.Fatal(err)
	}
	if upstream.ReasoningOutputMode != "hide" || !upstream.ParseEmbeddedReasoning {
		t.Fatal("未传选项时必须保留现有值")
	}
	mode, parse := "preserve", false
	if _, err := applyCommonUpdates(&upstream, 0, UpstreamUpdate{ReasoningOutputMode: &mode, ParseEmbeddedReasoning: &parse}, "test"); err != nil {
		t.Fatal(err)
	}
	if upstream.ReasoningOutputMode != "preserve" || upstream.ParseEmbeddedReasoning {
		t.Fatal("明确传入的 preserve/false 必须生效")
	}
}

func TestReasoningOutputMode_ManagementDTORetainsOptions(t *testing.T) {
	upstream := UpstreamConfig{ParseEmbeddedReasoning: true, RequireReasoningContent: true}
	dto := ChannelToDTO(&upstream, 0)
	if dto["reasoningOutputMode"] != "preserve" || dto["parseEmbeddedReasoning"] != true || dto["requireReasoningContent"] != true {
		t.Fatalf("管理 API 必须返回有效默认值及严格能力，避免编辑渠道时覆盖：%+v", dto)
	}
	upstream.ReasoningOutputMode = "hide"
	if ChannelToDTO(&upstream, 0)["reasoningOutputMode"] != "hide" {
		t.Fatal("隐藏模式必须可读回")
	}
}
