package hooks

import (
	"context"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
)

type benchmarkHookContext struct {
	attached *attachedHookPipeline
}

func (c benchmarkHookContext) Get(key string) (any, bool) {
	return c.attached, key == contentSafetyPipelineKey
}

func BenchmarkContentSafetyCleanHistory(b *testing.B) {
	settings := config.DefaultContentSafetyConfig()
	settings.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.Credential.Enabled = true
	settings.Credential.EnabledRules = []string{
		config.CredentialRuleAPIKey, config.CredentialRuleNamedSecret,
		config.CredentialRuleConnectionString, config.CredentialRulePrivateKey, config.CredentialRuleHighEntropy,
	}
	settings.SensitiveInfo.Enabled = true
	settings.SensitiveInfo.EnabledRules = []string{
		config.SensitiveInfoRulePhone, config.SensitiveInfoRuleIDCard, config.SensitiveInfoRuleEmail,
		config.SensitiveInfoRuleIPAddress, config.SensitiveInfoRuleBankCard,
	}
	snapshot := newAllowlistTestSnapshot(b, settings)
	text := strings.Repeat("普通项目说明与历史对话，请继续整理函数的输入输出。\n", 1024)
	segments := []SafetySegment{{
		Protocol: "messages", Source: safetySourceUser, Path: "messages[0].content",
		Text: text, Mutable: true, replace: func(string) {},
	}}
	metadata := HookContext{APIType: "messages", redactionVault: sensitive.NewVault()}
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		var prompts []string
		changed, err := applyContentSafetySegments(context.Background(), metadata, segments, snapshot, nil, &prompts)
		if err != nil || changed {
			b.Fatalf("无敏感信息的历史不应被改写: %v", err)
		}
	}
}

func BenchmarkRedactionSSEEvent(b *testing.B) {
	vault := sensitive.NewVault()
	original := "synthetic-secret"
	if _, err := vault.Mask(original, []sensitive.RedactionMatch{{
		Type: "SECRET", Rule: "test", Original: original, Start: 0, End: len(original),
	}}); err != nil {
		b.Fatal(err)
	}
	c := benchmarkHookContext{attached: &attachedHookPipeline{pipeline: NewPipeline(), vault: vault}}
	event := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"普通的回答内容\"}}\n\n"
	b.ReportAllocs()
	for b.Loop() {
		got, err := RestoreAttachedSSEEvent(c, event)
		if err != nil || got != event {
			b.Fatalf("普通 SSE 事件不应被改写: %v", err)
		}
	}
}

func BenchmarkSafetyAuditRepeated(b *testing.B) {
	credential := "sk-ant-api03-abcdefghijklmnop"
	text := strings.Repeat("无敏感内容的历史上下文。\n", 1024) + credential
	metadata := HookContext{APIType: "messages", eventDeduper: newSafetyEventDeduper()}
	segment := SafetySegment{Source: safetySourceToolResult, Path: "messages[0].content", Text: text}
	hits := []safetyHit{{rule: "api_key", start: len(text) - len(credential), end: len(text), reveal: "[凭据已隐藏:api_key]"}}
	recorder := &gateTestRecorder{}
	if err := recordSafetyMatches(context.Background(), metadata, recorder, sensitive.BlockTypeCredential,
		config.ContentSafetyModeMask, segment, text, hits); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := recordSafetyMatches(context.Background(), metadata, recorder, sensitive.BlockTypeCredential,
			config.ContentSafetyModeMask, segment, text, hits); err != nil {
			b.Fatal(err)
		}
	}
	if len(recorder.entries) != 1 {
		b.Fatalf("同一请求的重复审计应只写一次，实际 %d 次", len(recorder.entries))
	}
}
