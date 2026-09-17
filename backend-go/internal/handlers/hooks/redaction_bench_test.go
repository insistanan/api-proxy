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
		changed, err := applyContentSafetySegments(context.Background(), metadata, segments, snapshot, nil, &prompts, false)
		if err != nil || changed {
			b.Fatalf("无敏感信息的历史不应被改写: %v", err)
		}
	}
}

// BenchmarkContentSafetyCodeLikeHistory 用代码风格文本（Claude Code 场景的
// 典型载荷：日志、配置、命令行）对照干净中文历史的检测开销。
// 旧 IPv6 宽正则对这类文本的候选数与文本长度成正比，是此前的主要开销点。
func BenchmarkContentSafetyCodeLikeHistory(b *testing.B) {
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
	code := strings.Repeat("2026-09-17T08:00:00Z [info] GET /api/v1/users?page=2&limit=50 200 12ms\nexport PATH=/usr/local/bin:/usr/bin:/bin:$PATH\nversion 1.2.3.4, deadline:2026-09-30T23:59:59Z, ratio 2:1:3\nuuid=550e8400-e29b-41d4-a716-446655440000 color=#ff00aa\n", 128)
	segments := []SafetySegment{{
		Protocol: "messages", Source: safetySourceUser, Path: "messages[0].content",
		Text: code, Mutable: true, replace: func(string) {},
	}}
	metadata := HookContext{APIType: "messages", redactionVault: sensitive.NewVault()}
	b.SetBytes(int64(len(code)))
	b.ReportAllocs()
	for b.Loop() {
		var prompts []string
		changed, err := applyContentSafetySegments(context.Background(), metadata, segments, snapshot, nil, &prompts, false)
		if err != nil || changed {
			b.Fatalf("无敏感信息的代码历史不应被改写: %v", err)
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
