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

func TestChannelReasoningEffortPartialUpdateAndValidation(t *testing.T) {
	upstream := UpstreamConfig{ReasoningEffort: "max"}
	_, err := applyCommonUpdates(&upstream, 0, UpstreamUpdate{}, "test")
	if err != nil || upstream.ReasoningEffort != "max" {
		t.Fatalf("未传思考等级应保持原值: effort=%s err=%v", upstream.ReasoningEffort, err)
	}
	inherit := "inherit"
	_, err = applyCommonUpdates(&upstream, 0, UpstreamUpdate{ReasoningEffort: &inherit}, "test")
	if err != nil || ChannelToDTO(&upstream, 0)["reasoningEffort"] != "inherit" {
		t.Fatalf("恢复跟随请求必须可读回: effort=%s err=%v", upstream.ReasoningEffort, err)
	}
	auto := "auto"
	_, err = applyCommonUpdates(&upstream, 0, UpstreamUpdate{ReasoningEffort: &auto}, "test")
	if err != nil || ChannelToDTO(&upstream, 0)["reasoningEffort"] != "auto" {
		t.Fatalf("自动思考必须可保存并读回: effort=%s err=%v", upstream.ReasoningEffort, err)
	}
	var loaded UpstreamConfig
	if err := json.Unmarshal([]byte(`{"reasoningEffort":"auto"}`), &loaded); err != nil || loaded.ReasoningEffort != "auto" {
		t.Fatalf("自动思考必须可从配置重新载入: effort=%s err=%v", loaded.ReasoningEffort, err)
	}
	for _, effort := range []string{"future", "medium", "ultra"} {
		body, err := json.Marshal(map[string]interface{}{"reasoningEffort": effort})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &UpstreamConfig{}); err == nil {
			t.Fatalf("渠道不支持的等级 %s 必须明确拒绝", effort)
		}
	}
}
