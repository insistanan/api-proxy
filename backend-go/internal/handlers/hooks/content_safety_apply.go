// 内容安全片段检测与脱敏应用；白名单与检测规则的顺序保持一致。
package hooks

import (
	"context"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/sensitive"
)

// applyContentSafetySegments 对提取出的文本片段依次执行敏感词、凭据、
// 敏感信息与自定义规则检测，按配置 mask/block/audit 模式改写或拦截。
// segmentsPreReserved 表示段文本已被调用方整体登记过占位符冲突保护
// （JSON 路径的 hook.Run 对完整请求体做过一次全量 ReserveText，段文本是
// 请求体的子串，逐段重复扫描纯属性能浪费）；multipart 路径没有全量前置，
// 必须由本函数逐段登记。
func applyContentSafetySegments(
	ctx context.Context,
	metadata HookContext,
	segments []SafetySegment,
	snapshot *contentSafetySnapshot,
	recorder BlockedLogRecorder,
	prompts *[]string,
	segmentsPreReserved bool,
) (bool, error) {
	vault := metadata.redactionVault
	if vault == nil {
		vault = sensitive.NewVaultWithContext(ctx)
	}
	if !segmentsPreReserved {
		for index := range segments {
			vault.ReserveText(segments[index].Text)
		}
		if err := vault.Err(); err != nil {
			return false, err
		}
	}
	changed := false
	for index := range segments {
		segment := &segments[index]
		// 白名单放行：命中工具名的 tool_result / tool_argument 跳过全部检测维度，
		// 并记录一条 whitelist 审计事件，让拦截记录页可见"已放行"的条目。
		if isWhitelistedSegment(snapshot, segment) {
			if err := recordWhitelistAllowed(ctx, metadata, recorder, *segment); err != nil {
				return changed, err
			}
			if segment.Mutable && segment.Text != "" {
				segment.replace(segment.Text)
			}
			continue
		}
		// 放行字面量（整段相等）：segment 文本与某条目完全相等时跳过全部检测维度，
		// 写一条 allowlist 审计事件。覆盖"整个字段就是 localhost 这类值"的最常见场景。
		if allowlistSkipsWholeSegment(snapshot.settings.Allowlist, segment.Text) {
			if err := recordAllowlistAllowed(ctx, metadata, recorder, *segment); err != nil {
				return changed, err
			}
			if segment.Mutable && segment.Text != "" {
				segment.replace(segment.Text)
			}
			continue
		}
		if segment.Source == safetySourceUser || segment.Source == safetySourceSystem {
			if match, ok := snapshot.words.FindFirst(segment.Text); ok {
				return changed, &ContentSafetyError{
					BlockType: sensitive.BlockTypeSensitiveWord,
					RuleName:  string(match.Category),
					Snippet: safetyHitSnippet("block", *segment, segment.Text, safetyHit{
						rule:   string(match.Category),
						start:  match.Start,
						end:    match.End,
						reveal: match.Word,
					}),
				}
			}
		}

		credentialMode := snapshot.settings.SensitiveData.Mode

		// credentialSource 是凭据检测时的原文；掩码会改写 segment.Text，
		// 命中偏移只对原文有效，必须先留存。
		credentialSource := segment.Text
		credentialMatches := snapshot.credential.FindAll(segment.Text)
		credentialMatches = filterAllowlistedCredentialMatches(credentialMatches, credentialSource, snapshot.settings.Allowlist)
		if len(credentialMatches) > 0 {
			if credentialMode == config.ContentSafetyModeBlock || credentialMode == config.ContentSafetyModeMask && !segment.Mutable {
				first := credentialMatches[0]
				return changed, &ContentSafetyError{
					BlockType: sensitive.BlockTypeCredential,
					RuleName:  first.Rule,
					Snippet: safetyHitSnippet("block", *segment, credentialSource, safetyHit{
						rule:   first.Rule,
						start:  first.Start,
						end:    first.End,
						reveal: "[凭据已隐藏:" + first.Rule + "]",
					}),
				}
			}
			if credentialMode == config.ContentSafetyModeMask {
				masked, maskErr := vault.Mask(segment.Text, credentialRedactionMatches(credentialMatches))
				if maskErr != nil {
					return changed, maskErr
				}
				segment.Text = masked
				changed = true
			}
			if err := recordSafetyMatches(ctx, metadata, recorder, sensitive.BlockTypeCredential, credentialMode, *segment, credentialSource, credentialHits(credentialMatches)); err != nil {
				return changed, err
			}
		}

		infoChecked := segment.Source == safetySourceUser || segment.Source == safetySourceSystem || segment.Source == safetySourceAssistant ||
			segment.Source == safetySourceToolResult || segment.Source == safetySourceToolArgument
		if infoChecked {
			// 敏感信息检测在凭据掩码之后执行，命中偏移相对当前文本；
			// 以此为窗口基准，改写前再留存一份。
			infoSource := segment.Text
			infoMatches := snapshot.info.FindAll(segment.Text)
			infoMatches = filterAllowlistedInfoMatches(infoMatches, infoSource, snapshot.settings.Allowlist)
			if len(infoMatches) > 0 {
				mode := snapshot.settings.SensitiveData.Mode
				if mode == config.ContentSafetyModeBlock || mode == config.ContentSafetyModeMask && !segment.Mutable {
					first := infoMatches[0]
					return changed, &ContentSafetyError{
						BlockType: sensitive.BlockTypeSensitiveInfo,
						RuleName:  first.Rule,
						Snippet: safetyHitSnippet("block", *segment, infoSource, safetyHit{
							rule:   first.Rule,
							start:  first.Start,
							end:    first.End,
							reveal: first.Original,
						}),
					}
				}
				if mode == config.ContentSafetyModeMask {
					masked, maskErr := vault.Mask(segment.Text, infoRedactionMatches(infoMatches))
					if maskErr != nil {
						return changed, maskErr
					}
					segment.Text = masked
					changed = true
				}
				if err := recordSafetyMatches(ctx, metadata, recorder, sensitive.BlockTypeSensitiveInfo, mode, *segment, infoSource, infoHits(infoMatches)); err != nil {
					return changed, err
				}
			}
		}

		// 自定义规则（可逆脱敏，最低优先级）：仅 mask 模式参与，且在凭据/个人信息
		// 之后执行——内置规则先占住各自区间，自定义规则只处理剩余文本；且它是最后的
		// 掩码步骤，宽正则不会把已生成的占位符再包一层（R1-e：改写后不再复检）。
		// 放行字面量过滤同样生效（命中原文与条目相等则丢弃）。
		if snapshot.settings.SensitiveData.Mode == config.ContentSafetyModeMask && snapshot.custom != nil && snapshot.custom.Enabled() {
			customSource := segment.Text
			customMatches := snapshot.custom.FindAll(segment.Text)
			customMatches = filterAllowlistedCustomMatches(customMatches, snapshot.settings.Allowlist)
			if len(customMatches) > 0 {
				masked, maskErr := vault.Mask(segment.Text, customRedactionMatches(customMatches))
				if maskErr != nil {
					return changed, maskErr
				}
				segment.Text = masked
				changed = true
				if err := recordSafetyMatches(ctx, metadata, recorder, sensitive.BlockTypeCustomRule,
					config.ContentSafetyModeMask, *segment, customSource, customHits(customMatches)); err != nil {
					return changed, err
				}
			}
		}

		if segment.Mutable && segment.Text != "" {
			segment.replace(segment.Text)
		}
		if snapshot.settings.SensitiveData.Mode == config.ContentSafetyModeMask && !infoChecked {
			// 未分类来源没有经过前面的个人信息检测，保留原有末尾校验；
			// 已改写片段不再复检（R1-e：脱敏输出假定可信，避免整段二次扫描）。
			// 校验同样过放行字面量过滤：被放行的值（如 localhost）本来就应留在
			// 文本里，不能被此处误报为命中。
			if matches := snapshot.credential.FindAll(segment.Text); len(matches) > 0 {
				matches = filterAllowlistedCredentialMatches(matches, segment.Text, snapshot.settings.Allowlist)
				if len(matches) > 0 {
					return changed, fmt.Errorf("凭据脱敏后仍命中规则 %q", matches[0].Rule)
				}
			}
			if matches := snapshot.info.FindAll(segment.Text); len(matches) > 0 {
				matches = filterAllowlistedInfoMatches(matches, segment.Text, snapshot.settings.Allowlist)
				if len(matches) > 0 {
					return changed, fmt.Errorf("敏感信息脱敏后仍命中规则 %q", matches[0].Rule)
				}
			}
		}
		if (segment.Source == safetySourceUser || segment.Source == safetySourceSystem) && strings.TrimSpace(segment.Text) != "" {
			*prompts = append(*prompts, segment.Text)
		}
	}
	return changed, nil
}

func credentialRedactionMatches(matches []sensitive.CredentialMatch) []sensitive.RedactionMatch {
	result := make([]sensitive.RedactionMatch, 0, len(matches))
	for _, match := range matches {
		result = append(result, sensitive.RedactionMatch{
			Type: credentialPlaceholderType(match.Rule), Rule: match.Rule, Original: match.Original,
			Start: match.Start, End: match.End,
		})
	}
	return result
}

func infoRedactionMatches(matches []sensitive.InfoMatch) []sensitive.RedactionMatch {
	result := make([]sensitive.RedactionMatch, 0, len(matches))
	for _, match := range matches {
		kind := map[string]string{
			config.SensitiveInfoRulePhone: "PHONE", config.SensitiveInfoRuleIDCard: "ID_CARD",
			config.SensitiveInfoRuleEmail: "EMAIL", config.SensitiveInfoRuleIPAddress: "PRIVATE_IP",
			config.SensitiveInfoRuleBankCard: "BANK_CARD",
		}[match.Rule]
		result = append(result, sensitive.RedactionMatch{
			Type: kind, Rule: match.Rule, Original: match.Original, Start: match.Start, End: match.End,
		})
	}
	return result
}

func credentialPlaceholderType(rule string) string {
	switch rule {
	case config.CredentialRuleConnectionString:
		return "CONNECTION_STRING"
	case config.CredentialRulePrivateKey:
		return "PRIVATE_KEY"
	case config.CredentialRuleAPIKey:
		return "API_TOKEN"
	case config.CredentialRuleNamedSecret:
		return "SECRET"
	case config.CredentialRuleHighEntropy:
		return "TOKEN"
	default:
		return "CREDENTIAL"
	}
}

// credentialHits 把凭据命中转换为 safetyHit；reveal 只落规则名，明文不落库。
func credentialHits(matches []sensitive.CredentialMatch) []safetyHit {
	hits := make([]safetyHit, 0, len(matches))
	for _, match := range matches {
		hits = append(hits, safetyHit{
			rule:   match.Rule,
			start:  match.Start,
			end:    match.End,
			reveal: "[凭据已隐藏:" + match.Rule + "]",
		})
	}
	return hits
}

// infoHits 把敏感信息命中转换为 safetyHit；reveal 显示命中原文（管理员
// 已确认 PII 明文落库，用于拦截记录页审计定位）。
func infoHits(matches []sensitive.InfoMatch) []safetyHit {
	hits := make([]safetyHit, 0, len(matches))
	for _, match := range matches {
		hits = append(hits, safetyHit{
			rule:   match.Rule,
			start:  match.Start,
			end:    match.End,
			reveal: match.Original,
		})
	}
	return hits
}

// isWhitelistedSegment 判断片段是否命中白名单：仅 tool_result / tool_argument
// 来源、且携带的工具名出现在白名单 ToolNames 中时放行。白名单未启用或工具名
// 为空时一律不放行。
func isWhitelistedSegment(snapshot *contentSafetySnapshot, segment *SafetySegment) bool {
	if snapshot == nil || segment == nil {
		return false
	}
	whitelist := snapshot.settings.Whitelist
	if !whitelist.Enabled || len(whitelist.ToolNames) == 0 {
		return false
	}
	if segment.Source != safetySourceToolResult && segment.Source != safetySourceToolArgument {
		return false
	}
	toolName := strings.TrimSpace(segment.ToolName)
	if toolName == "" {
		return false
	}
	for _, allowed := range whitelist.ToolNames {
		if strings.TrimSpace(allowed) == toolName {
			return true
		}
	}
	return false
}
