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

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
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
	mu         sync.Mutex
	snapshot   atomic.Pointer[dangerousCmdSnapshot]
}

func (h *contentSafetyPostResponseHook) Name() string     { return "response-content-safety" }
func (h *contentSafetyPostResponseHook) Stage() HookStage { return HookStagePostResponse }
func (h *contentSafetyPostResponseHook) Priority() int    { return 100 }

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
			if safetyErr := reviewMatches(findDangerousCodeBlocks(snapshot.detector, value)); safetyErr != nil {
				return result, safetyErr
			}
			if safetyErr := reviewMatches(findDangerousToolArguments(snapshot.detector, value)); safetyErr != nil {
				return result, safetyErr
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
	} else if safetyErr := reviewMatches(snapshot.detector.Find(string(result.ResponseBody))); safetyErr != nil {
		return result, safetyErr
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
	settings := h.cfgManager.GetSettings().ContentSafety
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

func findDangerousToolArguments(detector *sensitive.CmdDetector, value interface{}) []sensitive.CommandMatch {
	if detector == nil {
		return nil
	}
	var matches []sensitive.CommandMatch
	var walk func(interface{}, bool)
	walk = func(current interface{}, toolContext bool) {
		switch typed := current.(type) {
		case []interface{}:
			for _, item := range typed {
				walk(item, toolContext)
			}
		case map[string]interface{}:
			if equalString(typed["type"], "custom_tool_call") {
				if input, ok := typed["input"].(string); ok {
					matches = append(matches, detector.FindInRawToolInput(stringValue(typed["name"]), input)...)
				} else {
					matches = append(matches, sensitive.CommandMatch{Rule: "invalid_tool_arguments", Context: "tool", ToolName: stringValue(typed["name"])})
				}
				return
			}
			isCall := toolContext || equalString(typed["type"], "tool_use") || equalString(typed["type"], "function_call")
			if isCall {
				for _, field := range []string{"input", "arguments", "args"} {
					if arguments, exists := typed[field]; exists {
						matches = append(matches, detector.FindInNamedToolArguments(stringValue(typed["name"]), arguments)...)
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
	return matches
}

func findDangerousCodeBlocks(detector *sensitive.CmdDetector, value interface{}) []sensitive.CommandMatch {
	if detector == nil {
		return nil
	}
	var matches []sensitive.CommandMatch
	var walk func(interface{})
	walk = func(current interface{}) {
		switch typed := current.(type) {
		case []interface{}:
			for _, item := range typed {
				walk(item)
			}
		case map[string]interface{}:
			for _, key := range sortedMapKeys(typed) {
				if isToolArgumentKey(key) || key == "args" || key == "function" {
					continue
				}
				walk(typed[key])
			}
		case string:
			matches = append(matches, detector.Find(typed)...)
		}
	}
	walk(value)
	return matches
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
