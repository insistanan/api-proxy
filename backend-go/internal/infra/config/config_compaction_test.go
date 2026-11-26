package config

import "testing"

func TestCompactionConfigurationBoundaries(t *testing.T) {
	valid := UpstreamConfig{ServiceType: "responses", BaseURL: "https://example.invalid/v1", APIKeys: []string{"key"},
		Compaction: CompactionConfig{NativeSourceModel: "main", NativeModel: "small", NativeCompatibilityConfirmed: true}}
	if err := validateCompactionConfig("responses", &valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*UpstreamConfig){
		func(up *UpstreamConfig) { up.Compaction.NativeCompatibilityConfirmed = false },
		func(up *UpstreamConfig) { up.APIKeys = []string{"one", "two"} },
		func(up *UpstreamConfig) { up.BaseURLs = []string{"https://one.invalid", "https://two.invalid"} },
		func(up *UpstreamConfig) { up.ServiceType = "openai" },
		func(up *UpstreamConfig) { up.StripThoughtSignature = true },
	} {
		up := valid.Clone()
		mutate(up)
		if err := validateCompactionConfig("responses", up); err == nil {
			t.Fatal("接受了不安全的原生压缩配置")
		}
	}
	summary := UpstreamConfig{ExcludeFromConversation: true, Compaction: CompactionConfig{SummaryCapable: true, SummaryModel: "small"}}
	if err := validateCompactionConfig("messages", &summary); err != nil {
		t.Fatal(err)
	}
	summary.ExcludeFromConversation = false
	if err := validateCompactionConfig("messages", &summary); err == nil {
		t.Fatal("摘要专池渠道不应参加常规调度")
	}
	if err := validateCompactionConfig("images", &valid); err == nil {
		t.Fatal("图片协议不应支持压缩配置")
	}
}
