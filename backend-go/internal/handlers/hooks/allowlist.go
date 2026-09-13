package hooks

import (
	"context"
	"fmt"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
)

// 放行字面量（字符串级白名单）与自定义规则命中在 hooks 层的辅助实现。
// 检测器保持纯粹（sensitive 包不感知策略），过滤与审计都在策略层完成。

// allowlistIndex 是放行字面量的进程内索引；settings 变化时随快照重建。
type allowlistIndex struct {
	entries map[string]struct{}
}

func buildAllowlistIndex(settings config.AllowlistConfig) allowlistIndex {
	index := allowlistIndex{entries: make(map[string]struct{}, len(settings.Entries))}
	if !settings.Enabled {
		return index
	}
	for _, entry := range settings.Entries {
		if entry != "" {
			index.entries[entry] = struct{}{}
		}
	}
	return index
}

// allows 判断精确字符串是否被放行。
func (a allowlistIndex) allows(value string) bool {
	if len(a.entries) == 0 {
		return false
	}
	_, ok := a.entries[value]
	return ok
}

// allowlistSkipsWholeSegment 判断 segment 文本是否与某条放行字面量完全相等
// （整段放行，跳过该 segment 的全部检测）。
func allowlistSkipsWholeSegment(settings config.AllowlistConfig, text string) bool {
	if !settings.Enabled || text == "" {
		return false
	}
	index := buildAllowlistIndex(settings)
	return index.allows(text)
}

// filterAllowlistedCredentialMatches 丢弃原文与放行字面量精确相等的凭据命中。
func filterAllowlistedCredentialMatches(matches []sensitive.CredentialMatch, source string, settings config.AllowlistConfig) []sensitive.CredentialMatch {
	if !settings.Enabled || len(matches) == 0 {
		return matches
	}
	index := buildAllowlistIndex(settings)
	kept := make([]sensitive.CredentialMatch, 0, len(matches))
	for _, match := range matches {
		if match.Start < 0 || match.End > len(source) {
			kept = append(kept, match)
			continue
		}
		if index.allows(source[match.Start:match.End]) {
			continue
		}
		kept = append(kept, match)
	}
	return kept
}

// filterAllowlistedInfoMatches 丢弃原文与放行字面量精确相等的个人信息命中。
func filterAllowlistedInfoMatches(matches []sensitive.InfoMatch, source string, settings config.AllowlistConfig) []sensitive.InfoMatch {
	if !settings.Enabled || len(matches) == 0 {
		return matches
	}
	index := buildAllowlistIndex(settings)
	kept := make([]sensitive.InfoMatch, 0, len(matches))
	for _, match := range matches {
		if match.Start < 0 || match.End > len(source) {
			kept = append(kept, match)
			continue
		}
		if index.allows(source[match.Start:match.End]) {
			continue
		}
		kept = append(kept, match)
	}
	return kept
}

// filterAllowlistedCustomMatches 丢弃原文与放行字面量精确相等的自定义规则命中。
func filterAllowlistedCustomMatches(matches []sensitive.CustomMatch, settings config.AllowlistConfig) []sensitive.CustomMatch {
	if !settings.Enabled || len(matches) == 0 {
		return matches
	}
	index := buildAllowlistIndex(settings)
	kept := make([]sensitive.CustomMatch, 0, len(matches))
	for _, match := range matches {
		if index.allows(match.Original) {
			continue
		}
		kept = append(kept, match)
	}
	return kept
}

// recordAllowlistAllowed 写一条放行字面量审计事件（整段放行留痕，
// 语义对齐 recordWhitelistAllowed 的"已放行"条目）。
func recordAllowlistAllowed(ctx context.Context, metadata HookContext, recorder BlockedLogRecorder, segment SafetySegment) error {
	if recorder == nil {
		return nil
	}
	key := fmt.Sprintf("allowlist\x00%s\x00%s\x00%s", segment.Source, segment.Path, segment.Text)
	if metadata.eventDeduper.contains(key) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	_, err := recorder.Record(ctx, sensitive.BlockedLog{
		APIType:        metadata.APIType,
		BlockType:      sensitive.BlockTypeAllowlist,
		RuleName:       "allowlist",
		PromptSnippet:  fmt.Sprintf("mode=allowlist source=%s\n整段文本与放行字面量精确相等，跳过全部检测", segment.Source),
		ChannelName:    metadata.ChannelName,
		Model:          metadata.Model,
		RequestID:      metadata.RequestID,
		ConversationID: metadata.ConversationID,
	})
	if err != nil {
		return err
	}
	return metadata.eventDeduper.mark(key, metadata.ConversationID == "")
}

// customRedactionMatches 把自定义规则命中转换为 Vault 掩码区间。
func customRedactionMatches(matches []sensitive.CustomMatch) []sensitive.RedactionMatch {
	result := make([]sensitive.RedactionMatch, 0, len(matches))
	for _, match := range matches {
		result = append(result, sensitive.RedactionMatch{
			Type:     match.Type,
			Rule:     match.Rule,
			Original: match.Original,
			Start:    match.Start,
			End:      match.End,
		})
	}
	return result
}

// customHits 把自定义规则命中转换为审计安全命中（不暴露原文）。
func customHits(matches []sensitive.CustomMatch) []safetyHit {
	hits := make([]safetyHit, 0, len(matches))
	for _, match := range matches {
		hits = append(hits, safetyHit{
			rule:   match.Rule,
			start:  match.Start,
			end:    match.End,
			reveal: "[自定义规则已脱敏:" + match.Rule + "]",
		})
	}
	return hits
}
