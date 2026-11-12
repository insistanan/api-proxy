package cacheinject

import (
	"bytes"
	"encoding/json"

	"github.com/insistanan/api-proxy/internal/infra/utils"
)

// MaxCacheBreakpoints 是 Anthropic / Bedrock Prompt Caching 允许的最大断点数。
const MaxCacheBreakpoints = 4

// InjectionStats 记录断点注入的结果与详情。
type InjectionStats struct {
	ExistingCount        int
	InjectedCount        int
	InjectedTools        bool
	InjectedSystem       bool
	InjectedLatestMsg    bool
	InjectedPriorUserMsg bool
}

func makeCacheControl() map[string]string {
	return map[string]string{"type": "ephemeral"}
}

// CountExistingBreakpoints 计算请求体中已有的 cache_control 断点总数。
func CountExistingBreakpoints(payload map[string]interface{}) int {
	count := 0

	if rawTools, exists := payload["tools"]; exists {
		if toolsSlice, ok := rawTools.([]interface{}); ok {
			for _, rawTool := range toolsSlice {
				if toolMap, ok := rawTool.(map[string]interface{}); ok {
					if _, has := toolMap["cache_control"]; has {
						count++
					}
				}
			}
		}
	}

	if rawSystem, exists := payload["system"]; exists {
		if systemSlice, ok := rawSystem.([]interface{}); ok {
			for _, rawBlock := range systemSlice {
				if blockMap, ok := rawBlock.(map[string]interface{}); ok {
					if _, has := blockMap["cache_control"]; has {
						count++
					}
				}
			}
		}
	}

	if rawMessages, exists := payload["messages"]; exists {
		if messagesSlice, ok := rawMessages.([]interface{}); ok {
			for _, rawMsg := range messagesSlice {
				if msgMap, ok := rawMsg.(map[string]interface{}); ok {
					if rawContent, has := msgMap["content"]; has {
						if blocksSlice, ok := rawContent.([]interface{}); ok {
							for _, rawBlock := range blocksSlice {
								if blockMap, ok := rawBlock.(map[string]interface{}); ok {
									if _, has := blockMap["cache_control"]; has {
										count++
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return count
}

// InjectPromptCacheBreakpoints 在请求体关键位置智能注入 cache_control: {"type": "ephemeral"} 断点：
// 1. tools 末尾（稳定工具集定义）
// 2. system 末尾（稳定系统提示词）
// 3. 最后一条可缓存消息（user/tool_result/assistant）的最后一个非 thinking block
// 4. 倒数第二条 user 消息（覆盖长工具轮次中的历史前缀）
//
// 若请求体已有断点 >= 4 则原样返回，保留调用方自主设定的断点。
func InjectPromptCacheBreakpoints(requestBodyBytes []byte) ([]byte, bool, InjectionStats) {
	var stats InjectionStats
	if len(requestBodyBytes) == 0 {
		return requestBodyBytes, false, stats
	}

	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(requestBodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return requestBodyBytes, false, stats
	}

	stats.ExistingCount = CountExistingBreakpoints(payload)
	if stats.ExistingCount >= MaxCacheBreakpoints {
		return requestBodyBytes, false, stats
	}

	budget := MaxCacheBreakpoints - stats.ExistingCount
	var modified bool

	// (a) tools 末尾
	if budget > 0 {
		if rawTools, exists := payload["tools"]; exists {
			if toolsSlice, ok := rawTools.([]interface{}); ok && len(toolsSlice) > 0 {
				lastToolIdx := len(toolsSlice) - 1
				if toolMap, ok := toolsSlice[lastToolIdx].(map[string]interface{}); ok {
					if _, has := toolMap["cache_control"]; !has {
						toolMap["cache_control"] = makeCacheControl()
						budget--
						stats.InjectedCount++
						stats.InjectedTools = true
						modified = true
					}
				}
			}
		}
	}

	// (b) system 末尾
	if budget > 0 {
		if rawSystem, exists := payload["system"]; exists {
			if systemStr, isStr := rawSystem.(string); isStr && systemStr != "" {
				payload["system"] = []interface{}{
					map[string]interface{}{
						"type":          "text",
						"text":          systemStr,
						"cache_control": makeCacheControl(),
					},
				}
				budget--
				stats.InjectedCount++
				stats.InjectedSystem = true
				modified = true
			} else if systemSlice, isSlice := rawSystem.([]interface{}); isSlice && len(systemSlice) > 0 {
				lastBlockIdx := len(systemSlice) - 1
				if blockMap, ok := systemSlice[lastBlockIdx].(map[string]interface{}); ok {
					if _, has := blockMap["cache_control"]; !has {
						blockMap["cache_control"] = makeCacheControl()
						budget--
						stats.InjectedCount++
						stats.InjectedSystem = true
						modified = true
					}
				}
			}
		}
	}

	// (c) & (d) messages 断点注入
	if budget > 0 {
		if rawMessages, exists := payload["messages"]; exists {
			if messagesSlice, ok := rawMessages.([]interface{}); ok && len(messagesSlice) > 0 {
				// (c) 最后一条可缓存消息的最后一个非 thinking 块
				for i := len(messagesSlice) - 1; i >= 0; i-- {
					if msgMap, ok := messagesSlice[i].(map[string]interface{}); ok {
						if injectMessageBreakpoint(msgMap) {
							budget--
							stats.InjectedCount++
							stats.InjectedLatestMsg = true
							modified = true
							break
						}
					}
				}

				// (d) 倒数第 2 条 user 消息
				if budget > 0 && len(messagesSlice) >= 4 {
					userCount := 0
					for i := len(messagesSlice) - 1; i >= 0; i-- {
						if msgMap, ok := messagesSlice[i].(map[string]interface{}); ok {
							if role, _ := msgMap["role"].(string); role == "user" {
								userCount++
								if userCount == 2 {
									if injectMessageBreakpoint(msgMap) {
										budget--
										stats.InjectedCount++
										stats.InjectedPriorUserMsg = true
										modified = true
									}
									break
								}
							}
						}
					}
				}
			}
		}
	}

	if !modified {
		return requestBodyBytes, false, stats
	}

	modifiedBytes, err := utils.MarshalJSONNoEscape(payload)
	if err != nil {
		return requestBodyBytes, false, stats
	}

	return modifiedBytes, true, stats
}

func injectMessageBreakpoint(msgMap map[string]interface{}) bool {
	rawContent, hasContent := msgMap["content"]
	if !hasContent {
		return false
	}

	// 字符串 content 转换为 block array
	if strContent, isStr := rawContent.(string); isStr {
		msgMap["content"] = []interface{}{
			map[string]interface{}{
				"type":          "text",
				"text":          strContent,
				"cache_control": makeCacheControl(),
			},
		}
		return true
	}

	// block 数组
	blocksSlice, isSlice := rawContent.([]interface{})
	if !isSlice || len(blocksSlice) == 0 {
		return false
	}

	// 倒序寻找第一个非 thinking / redacted_thinking 块
	for i := len(blocksSlice) - 1; i >= 0; i-- {
		blockMap, ok := blocksSlice[i].(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := blockMap["type"].(string)
		if blockType == "thinking" || blockType == "redacted_thinking" {
			continue
		}
		if _, has := blockMap["cache_control"]; has {
			return false
		}
		blockMap["cache_control"] = makeCacheControl()
		return true
	}

	return false
}
