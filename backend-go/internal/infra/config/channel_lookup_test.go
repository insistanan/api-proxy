package config

import "testing"

func TestFindChannelByIDCrossSlice(t *testing.T) {
	cm := &ConfigManager{
		config: Config{
			Upstream:       []UpstreamConfig{{ID: "ch_msg", Name: "messages-one", ServiceType: "claude"}},
			GeminiUpstream: []UpstreamConfig{{ID: "ch_gem", Name: "gemini-one", ServiceType: "gemini"}},
		},
	}
	located, ok := cm.FindChannelByID("ch_gem")
	if !ok {
		t.Fatal("应按 UUID 找到 Gemini 渠道")
	}
	if located.Kind != "gemini" || located.Upstream.Name != "gemini-one" {
		t.Fatalf("查错渠道: %+v", located)
	}
	if _, ok := cm.FindChannelByID(""); ok {
		t.Fatal("空 ID 不应命中")
	}
}
