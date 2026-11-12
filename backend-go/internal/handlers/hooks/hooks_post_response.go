package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/insistanan/api-proxy/internal/dialogue/semantic"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/safety/sensitive"
)

type dangerousCmdSnapshot struct {
	settings   config.ContentSafetyConfig
	detector   *sensitive.CmdDetector
	credential *sensitive.CredentialDetector
}

type contentSafetyPostResponseHook struct {
	cfgManager *config.ConfigManager
	recorder   BlockedLogRecorder
	gate       *CmdGate
	semantic   *semantic.Service
	mu         sync.Mutex
	snapshot   atomic.Pointer[dangerousCmdSnapshot]
}

func (h *contentSafetyPostResponseHook) Run(ctx context.Context, metadata HookContext, result HookResult) (HookResult, error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if h == nil || h.cfgManager == nil {
		return result, fmt.Errorf("响应安全 Hook 未绑定配置管理器")
	}
	snapshot, err := h.currentSnapshot()
	if err != nil {
		return result, err
	}
	if !snapshot.settings.DangerousCmd.Enabled && !snapshot.settings.Credential.Enabled {
		return result, nil
	}
	if snapshot.detector == nil || len(result.ResponseBody) == 0 {
		return result, nil
	}
	reviewed := make(map[string]struct{})
	reviewMatches := func(matches []sensitive.CommandMatch) *ContentSafetyError {
		for _, match := range matches {
			key := sensitive.CommandIdentity(match)
			if _, ok := reviewed[key]; ok {
				continue
			}
			if safetyErr := h.reviewDangerousMatch(ctx, metadata, match); safetyErr != nil {
				return safetyErr
			}
			reviewed[key] = struct{}{}
		}
		return nil
	}
	if value, ok := decodeJSONValue(result.ResponseBody); ok {
		if snapshot.settings.DangerousCmd.Enabled {
			// 危险命令只检查执行工具，并审核还原后的参数；凭据检查仍使用上游原值，
			// 避免把请求中已脱敏、按契约还原的凭据误报为上游新生成的泄露。
			matches, err := findDangerousToolArguments(snapshot.detector, value, metadata.redactionVault)
			if err != nil {
				return result, err
			}
			if safetyErr := reviewMatches(matches); safetyErr != nil {
				return result, safetyErr
			}
			if policy := snapshot.settings.DangerousCmd.Semantic; policy != nil && policy.Enabled {
				commands, err := collectResponseToolCommands(value, metadata.redactionVault, sensitive.ExtractToolCommands)
				if err != nil {
					return result, err
				}
				pending := commands[:0]
				for _, command := range commands {
					if _, done := reviewed[sensitive.CommandIdentity(command)]; !done {
						pending = append(pending, command)
					}
				}
				if err := reviewSemanticCommands(ctx, h.semantic, h.recorder, snapshot.settings.DangerousCmd, metadata, pending, func(match sensitive.CommandMatch) *ContentSafetyError {
					return h.reviewDangerousMatch(ctx, metadata, match)
				}); err != nil {
					return result, err
				}
			}
		}
		if root, ok := value.(map[string]interface{}); ok {
			for _, segment := range extractResponseToolArgumentSegments(metadata.APIType, root) {
				if isWhitelistedSegment(&contentSafetySnapshot{settings: snapshot.settings}, &segment) {
					if err := recordWhitelistAllowed(ctx, metadata, h.recorder, segment); err != nil {
						return result, err
					}
					continue
				}
				if allowlistSkipsWholeSegment(snapshot.settings.Allowlist, segment.Text) {
					if err := recordAllowlistAllowed(ctx, metadata, h.recorder, segment); err != nil {
						return result, err
					}
					continue
				}
				matches := snapshot.credential.FindAll(segment.Text)
				matches = filterAllowlistedCredentialMatches(matches, segment.Text, snapshot.settings.Allowlist)
				if len(matches) == 0 {
					continue
				}
				// 处置模式统一读全局 sensitiveData.mode（与请求方向 / 流式同口径）。
				// block 才拦截；mask / audit 放行并写审计事件（响应方向段不可改写，
				// mask 在此等价于"放行 + 留痕"，与流式 scanAttachedStreamCredentials 一致）。
				if snapshot.settings.SensitiveData.Mode != config.ContentSafetyModeBlock {
					if err := recordSafetyMatches(ctx, metadata, h.recorder, sensitive.BlockTypeCredential,
						snapshot.settings.SensitiveData.Mode, segment, segment.Text, credentialHits(matches)); err != nil {
						return result, err
					}
					continue
				}
				first := matches[0]
				return result, &ContentSafetyError{
					BlockType: sensitive.BlockTypeCredential,
					RuleName:  first.Rule,
					Snippet: safetyHitSnippet("block", segment, segment.Text, safetyHit{
						rule:   first.Rule,
						start:  first.Start,
						end:    first.End,
						reveal: "[凭据已隐藏:" + first.Rule + "]",
					}),
				}
			}
		}
	}
	return result, nil
}

// reviewDangerousMatch 对非流式响应中的危险命令命中做闸门审查。
// 非流式响应在写回客户端前整段就绪，Review 的阻塞等待即对客户端"挂起"。
// AI 放行后响应立即返回，事后否决无法收回当前响应（onVeto 传 nil），
// 但否决写入的会话黑名单对同对话后续命令生效。
func (h *contentSafetyPostResponseHook) reviewDangerousMatch(ctx context.Context, metadata HookContext, match sensitive.CommandMatch) *ContentSafetyError {
	if h.gate == nil || !h.gate.Enabled() {
		return dangerousCommandError(match)
	}
	verdict := h.gate.Review(ctx, metadata, match, nil)
	if verdict.Allow {
		return nil
	}
	blockType := verdict.BlockType
	if blockType == "" {
		blockType = sensitive.BlockTypeDangerousCmdAI
	}
	return &ContentSafetyError{
		BlockType:   blockType,
		RuleName:    match.Rule,
		Snippet:     fmt.Sprintf("mode=ai_gate source=assistant context=%s\n%s\n裁决: %s", match.Context, match.Command, verdict.Reason),
		Command:     match.Command,
		CommandHash: sensitive.CommandIdentity(match),
		AIReview:    verdict.AIReview,
		audited:     verdict.audited,
	}
}

func (h *contentSafetyPostResponseHook) currentSnapshot() (*dangerousCmdSnapshot, error) {
	if h.cfgManager == nil {
		return nil, fmt.Errorf("响应安全 Hook 未绑定配置管理器")
	}
	settings := h.cfgManager.GetContentSafetyConfig()
	current := h.snapshot.Load()
	if current != nil && reflect.DeepEqual(current.settings, settings) {
		return current, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	current = h.snapshot.Load()
	if current != nil && reflect.DeepEqual(current.settings, settings) {
		return current, nil
	}
	detector, err := sensitive.NewCmdDetector(settings.DangerousCmd)
	if err != nil {
		return nil, fmt.Errorf("初始化危险命令检测器失败: %w", err)
	}
	credential, err := sensitive.NewCredentialDetector(settings.Credential)
	if err != nil {
		return nil, fmt.Errorf("初始化响应凭据检测器失败: %w", err)
	}
	next := &dangerousCmdSnapshot{settings: settings, detector: detector, credential: credential}
	h.snapshot.Store(next)
	return next, nil
}

func decodeJSONValue(body []byte) (interface{}, bool) {
	var value interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	return value, true
}

func findDangerousToolArguments(detector *sensitive.CmdDetector, value interface{}, vault *sensitive.Vault) ([]sensitive.CommandMatch, error) {
	if detector == nil {
		return nil, nil
	}
	return collectResponseToolCommands(value, vault, func(name string, arguments any, raw bool) []sensitive.CommandMatch {
		if raw {
			return detector.FindInRawToolInput(name, arguments.(string))
		}
		return detector.FindInNamedToolArguments(name, arguments)
	})
}

func collectResponseToolCommands(value interface{}, vault *sensitive.Vault, scan func(string, any, bool) []sensitive.CommandMatch) ([]sensitive.CommandMatch, error) {
	var matches []sensitive.CommandMatch
	var restoreErr error
	// 只还原执行工具的参数，不重复解析和复制整个响应，也不改动凭据检查使用的原值。
	restoreArguments := func(arguments interface{}) (interface{}, error) {
		if vault == nil || !vault.CanRestore() {
			return arguments, nil
		}
		encoded, isText := arguments.(string)
		if !isText {
			body, err := json.Marshal(arguments)
			if err != nil {
				return nil, fmt.Errorf("序列化待审核工具参数失败: %w", err)
			}
			encoded = string(body)
		}
		if len(encoded) > sensitive.MaxCommandBytes {
			return arguments, nil // 超限参数由检测器明确拦截，不继续还原大对象。
		}
		restored, err := vault.RestoreJSON([]byte(encoded))
		if err != nil {
			return nil, fmt.Errorf("还原待审核工具参数失败: %w", err)
		}
		if isText {
			return string(restored), nil
		}
		decoded, ok := decodeJSONValue(restored)
		if !ok {
			return nil, fmt.Errorf("已还原工具参数不是完整有效的 JSON")
		}
		return decoded, nil
	}
	var walk func(interface{}, bool)
	walk = func(current interface{}, toolContext bool) {
		if restoreErr != nil {
			return
		}
		switch typed := current.(type) {
		case []interface{}:
			for _, item := range typed {
				walk(item, toolContext)
			}
		case map[string]interface{}:
			if equalString(typed["type"], "custom_tool_call") {
				if !sensitive.IsExecutionTool(stringValue(typed["name"])) {
					return
				}
				if input, ok := typed["input"].(string); ok {
					if vault != nil && vault.CanRestore() && len(input) <= sensitive.MaxCommandBytes {
						input = vault.Restore(input)
						if err := vault.Err(); err != nil {
							restoreErr = fmt.Errorf("还原待审核工具输入失败: %w", err)
							return
						}
					}
					matches = append(matches, scan(stringValue(typed["name"]), input, true)...)
				} else {
					matches = append(matches, sensitive.CommandMatch{Rule: "invalid_tool_arguments", Context: "tool", ToolName: stringValue(typed["name"])})
				}
				return
			}
			isCall := toolContext || equalString(typed["type"], "tool_use") || equalString(typed["type"], "function_call")
			if isCall {
				for _, field := range []string{"input", "arguments", "args"} {
					if arguments, exists := typed[field]; exists {
						if !sensitive.IsExecutionTool(stringValue(typed["name"])) {
							return
						}
						arguments, restoreErr = restoreArguments(arguments)
						if restoreErr != nil {
							return
						}
						matches = append(matches, scan(stringValue(typed["name"]), arguments, false)...)
						return
					}
				}
			}
			keys := sortedMapKeys(typed)
			for _, key := range keys {
				// 只向实际工具容器传递标记，id/name/type 等结构字符串不是执行输入。
				walk(typed[key], isToolArgumentKey(key) || key == "function")
			}
		}
	}
	walk(value, false)
	if restoreErr != nil {
		return nil, restoreErr
	}
	return matches, nil
}

func sortedMapKeys(value map[string]interface{}) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func dangerousCommandError(match sensitive.CommandMatch) *ContentSafetyError {
	return &ContentSafetyError{
		BlockType:   sensitive.BlockTypeDangerousCmd,
		RuleName:    match.Rule,
		Snippet:     fmt.Sprintf("mode=block source=assistant context=%s\n%s", match.Context, match.Command),
		Command:     match.Command,
		CommandHash: sensitive.CommandIdentity(match),
	}
}

func isToolArgumentKey(key string) bool {
	switch strings.ToLower(key) {
	case "arguments", "input", "tool_use", "tool_call", "tool_calls", "function_call", "functioncall":
		return true
	default:
		return false
	}
}
