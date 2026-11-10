// 内容安全审计记录与脱敏摘要窗口。
package hooks

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/sensitive"
)

// recordSafetyMatches 写入 mask/audit 模式的内容安全事件。sourceText 是命中
// 时的原始片段文本（掩码改写之前），hits 携带每个命中规则的原始字节偏移；
// 凭据命中只落规则名，绝不把明文写入拦截记录。
func recordSafetyMatches(ctx context.Context, metadata HookContext, recorder BlockedLogRecorder, blockType, mode string, segment SafetySegment, sourceText string, hits []safetyHit) error {
	if len(hits) == 0 || mode == "" || mode == config.ContentSafetyModeBlock {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	if recorder == nil {
		if mode == config.ContentSafetyModeAudit {
			return fmt.Errorf("内容安全审计模式未配置记录器")
		}
		return nil
	}
	seen := make(map[string]struct{}, len(hits))
	contentHash := sha256.Sum256([]byte(sourceText))
	var safeContext string
	contextPrepared := false
	for _, hit := range hits {
		if _, exists := seen[hit.rule]; exists {
			continue
		}
		seen[hit.rule] = struct{}{}
		eventKey := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%x", blockType, mode, segment.Source, segment.Path, hit.rule, contentHash)
		if metadata.eventDeduper.contains(eventKey) {
			continue
		}
		// 先去重，再为确实需要落库的事件扫描并隐藏上下文中的凭据。
		if !contextPrepared {
			safeContext = sensitive.RedactAuditContext(sourceText)
			contextPrepared = true
		}
		if _, err := recorder.Record(ctx, sensitive.BlockedLog{
			APIType:        metadata.APIType,
			BlockType:      blockType,
			RuleName:       hit.rule,
			PromptSnippet:  fmt.Sprintf("mode=%s source=%s path=%s\n%s", mode, segment.Source, segment.Path, safetySnippetWindowRedacted(safeContext, hit)),
			ChannelName:    metadata.ChannelName,
			Model:          metadata.Model,
			RequestID:      metadata.RequestID,
			ConversationID: metadata.ConversationID,
		}); err != nil {
			return fmt.Errorf("写入内容安全%s记录失败: %w", mode, err)
		}
		if err := metadata.eventDeduper.mark(eventKey, metadata.ConversationID == ""); err != nil {
			return err
		}
	}
	return nil
}

// recordWhitelistAllowed 写入一条 whitelist 审计事件，让拦截记录页能看见"已放行"
// 的条目。同请求内同工具名 + 同路径去重，避免刷屏。片段展示工具输出的段首窗口，
// 该内容是管理员通过白名单认可的工具输出，直接展示原文。
func recordWhitelistAllowed(ctx context.Context, metadata HookContext, recorder BlockedLogRecorder, segment SafetySegment) error {
	if recorder == nil || metadata.eventDeduper == nil {
		return nil
	}
	toolName := strings.TrimSpace(segment.ToolName)
	if toolName == "" {
		return nil
	}
	eventKey := fmt.Sprintf("%s\x00%s\x00%s\x00%s", sensitive.BlockTypeWhitelist, segment.Source, segment.Path, toolName)
	if metadata.eventDeduper.contains(eventKey) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	if _, err := recorder.Record(ctx, sensitive.BlockedLog{
		APIType:        metadata.APIType,
		BlockType:      sensitive.BlockTypeWhitelist,
		RuleName:       toolName,
		PromptSnippet:  whitelistSnippet(segment),
		ChannelName:    metadata.ChannelName,
		Model:          metadata.Model,
		RequestID:      metadata.RequestID,
		ConversationID: metadata.ConversationID,
	}); err != nil {
		return fmt.Errorf("写入白名单放行记录失败: %w", err)
	}
	return metadata.eventDeduper.mark(eventKey, metadata.ConversationID == "")
}

// safetyHit 表示检测器返回的单个命中。Start/End 是原始片段文本
// （sourceText，掩码改写之前）的字节偏移，End 不包含在命中范围内。
// reveal 是写入拦截记录时该命中位置展示的内容：敏感词与个人信息按
// 管理员决策显示明文，凭据只显示规则名，明文绝不落库。
type safetyHit struct {
	rule   string
	start  int
	end    int
	reveal string
}

// safetySnippetWindowRunes 是命中上下文窗口在命中位置前后各取的字符数。
const safetySnippetWindowRunes = 60

// safetySnippetWindow 从 sourceText 截取以命中位置为中心的上下文窗口，
// 窗口内命中区间替换为 hit.reveal。偏移按 rune 对齐，避免截断多字节字符。
func safetySnippetWindow(sourceText string, hit safetyHit) string {
	return safetySnippetWindowRedacted(sensitive.RedactAuditContext(sourceText), hit)
}

func safetySnippetWindowRedacted(sourceText string, hit safetyHit) string {
	reveal := sensitive.RedactAuditText(hit.reveal)
	if hit.start < 0 || hit.end > len(sourceText) || hit.start >= hit.end {
		return reveal
	}
	windowStart, windowEnd := hit.start, hit.end
	for count := 0; count < safetySnippetWindowRunes && windowStart > 0; count++ {
		_, size := utf8.DecodeLastRuneInString(sourceText[:windowStart])
		windowStart -= size
	}
	for count := 0; count < safetySnippetWindowRunes && windowEnd < len(sourceText); count++ {
		_, size := utf8.DecodeRuneInString(sourceText[windowEnd:])
		windowEnd += size
	}
	var builder strings.Builder
	if windowStart > 0 {
		builder.WriteString("...")
	}
	builder.WriteString(sourceText[windowStart:hit.start])
	builder.WriteString(reveal)
	builder.WriteString(sourceText[hit.end:windowEnd])
	if windowEnd < len(sourceText) {
		builder.WriteString("...")
	}
	return builder.String()
}

// safetyHitSnippet 生成拦截记录的命中片段：第一行保留既有元数据
// （mode/source/path），第二行是命中上下文窗口，便于在拦截记录页
// 直接看到具体命中的内容。
func safetyHitSnippet(mode string, segment SafetySegment, sourceText string, hit safetyHit) string {
	return fmt.Sprintf("mode=%s source=%s path=%s\n%s",
		mode, segment.Source, segment.Path, safetySnippetWindow(sourceText, hit))
}

// whitelistSnippetWindowRunes 是白名单放行事件展示工具输出段首的字符数。
const whitelistSnippetWindowRunes = 120

// whitelistSnippet 生成白名单放行事件片段：元数据行 + 工具输出段首窗口。
func whitelistSnippet(segment SafetySegment) string {
	text := sensitive.RedactAuditText(segment.Text)
	window := text
	count := 0
	for index := range text {
		if count == whitelistSnippetWindowRunes {
			window = text[:index]
			break
		}
		count++
	}
	return fmt.Sprintf("mode=allow source=%s path=%s\n%s", segment.Source, segment.Path, window)
}
