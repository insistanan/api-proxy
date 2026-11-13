package rectifier

import (
	"bytes"
	"encoding/json"
	"strings"
)

const (
	// DefaultThinkingBudgetTokens 是整流时设置的合法默认 thinking budget。
	DefaultThinkingBudgetTokens = 32000
	// DefaultMaxTokensForBudget 是整流时设置的合法默认 max_tokens（保证大于 budget_tokens）。
	DefaultMaxTokensForBudget = 64000
)

// BudgetRectifyStats 记录 Budget 整流的影响统计。
type BudgetRectifyStats struct {
	PreviousThinkingType string
	NewThinkingType      string
	PreviousBudgetTokens int64
	NewBudgetTokens      int64
	PreviousMaxTokens    int64
	NewMaxTokens         int64
}

// ShouldRectifyThinkingBudget 检测上游错误响应是否由于 thinking budget 参数约束失败引起。
func ShouldRectifyThinkingBudget(responseBodyBytes []byte) bool {
	if len(responseBodyBytes) == 0 {
		return false
	}

	lowerMessage := strings.ToLower(string(responseBodyBytes))

	hasBudgetTokensReference := strings.Contains(lowerMessage, "budget_tokens") || strings.Contains(lowerMessage, "budget tokens")
	hasThinkingReference := strings.Contains(lowerMessage, "thinking")

	has1024Constraint := strings.Contains(lowerMessage, "greater than or equal to 1024") ||
		strings.Contains(lowerMessage, ">= 1024") ||
		(strings.Contains(lowerMessage, "1024") && strings.Contains(lowerMessage, "input should be"))

	hasMaxTokensConstraint := strings.Contains(lowerMessage, "max_tokens must be greater than") ||
		strings.Contains(lowerMessage, "must be greater than budget_tokens") ||
		strings.Contains(lowerMessage, "budget_tokens exceeds max_tokens")

	if hasBudgetTokensReference && (has1024Constraint || hasMaxTokensConstraint) {
		return true
	}

	if hasThinkingReference && hasBudgetTokensReference && has1024Constraint {
		return true
	}

	return false
}

// RectifyThinkingBudget 对 Anthropic 格式的请求体进行 Thinking Budget 约束就地整流。
// 若请求为 adaptive 思考模式则跳过不改动。
func RectifyThinkingBudget(requestBodyBytes []byte) ([]byte, bool, BudgetRectifyStats) {
	var stats BudgetRectifyStats
	if len(requestBodyBytes) == 0 {
		return requestBodyBytes, false, stats
	}

	var payloadMap map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(requestBodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payloadMap); err != nil {
		return requestBodyBytes, false, stats
	}

	var thinkingMap map[string]interface{}
	if rawThinking, exists := payloadMap["thinking"]; exists {
		if castMap, ok := rawThinking.(map[string]interface{}); ok {
			thinkingMap = castMap
		}
	}

	if thinkingMap != nil {
		if rawType, ok := thinkingMap["type"].(string); ok {
			stats.PreviousThinkingType = rawType
			// 遵循官方规范，自适应思考（adaptive）不强制覆盖为固定 budget
			if rawType == "adaptive" {
				return requestBodyBytes, false, stats
			}
		}
		if rawBudget, ok := thinkingMap["budget_tokens"]; ok {
			if num, ok := rawBudget.(json.Number); ok {
				val, _ := num.Int64()
				stats.PreviousBudgetTokens = val
			}
		}
	} else {
		thinkingMap = make(map[string]interface{})
		payloadMap["thinking"] = thinkingMap
	}

	// 记录原始 max_tokens
	if rawMaxTokens, exists := payloadMap["max_tokens"]; exists {
		if num, ok := rawMaxTokens.(json.Number); ok {
			val, _ := num.Int64()
			stats.PreviousMaxTokens = val
		}
	}

	thinkingMap["type"] = "enabled"
	stats.NewThinkingType = "enabled"

	thinkingMap["budget_tokens"] = int64(DefaultThinkingBudgetTokens)
	stats.NewBudgetTokens = DefaultThinkingBudgetTokens

	if stats.PreviousMaxTokens < int64(DefaultThinkingBudgetTokens+1) {
		payloadMap["max_tokens"] = int64(DefaultMaxTokensForBudget)
		stats.NewMaxTokens = DefaultMaxTokensForBudget
	} else {
		stats.NewMaxTokens = stats.PreviousMaxTokens
	}

	modifiedBytes, err := json.Marshal(payloadMap)
	if err != nil {
		return requestBodyBytes, false, stats
	}

	return modifiedBytes, true, stats
}
