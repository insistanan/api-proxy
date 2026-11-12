package proxycore

import (
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// FailoverError 封装故障转移错误信息
type FailoverError struct {
	Status int
	Body   []byte
}

// mergeFailoverFailure 保留真实候选失败，避免末尾协议不支持覆盖上游错误。
// 只有全部有效候选都本地不支持时，最终错误才保持 UnsupportedProtocol。
func mergeFailoverFailure(previous *FailoverError, previousError error, current *FailoverError, currentError error) (*FailoverError, error) {
	if current != nil && currentError == nil {
		currentError = fmt.Errorf("上游候选返回错误状态 %d", current.Status)
	}
	if types.IsUnsupportedProtocol(currentError) && previousError != nil && !types.IsUnsupportedProtocol(previousError) {
		return previous, previousError
	}
	if current != nil {
		previous = current
	} else if currentError != nil && types.IsUnsupportedProtocol(previousError) && !types.IsUnsupportedProtocol(currentError) {
		previous = nil
	}
	if currentError != nil {
		previousError = currentError
	}
	return previous, previousError
}

// HandleAllChannelsFailed 处理所有渠道都失败的情况
// fuzzyMode: 是否启用模糊模式（返回通用错误）
// lastFailoverError: 最后一个故障转移错误
// lastError: 最后一个错误
// apiType: API 类型（用于错误消息）
func HandleAllChannelsFailed(c *gin.Context, fuzzyMode bool, lastFailoverError *FailoverError, lastError error, apiType string) {
	if types.IsUnsupportedProtocol(lastError) {
		c.JSON(400, gin.H{"error": gin.H{"message": lastError.Error(), "type": "unsupported_protocol", "code": "unsupported_protocol"}})
		return
	}
	if upstreamcontrol.IsLocalError(lastError) {
		localAdmissionResult(c, lastError, false)
		return
	}
	// Fuzzy 模式下返回通用错误，不透传上游详情
	if fuzzyMode {
		c.JSON(503, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "service_unavailable",
				"message": "All upstream channels are currently unavailable",
			},
		})
		return
	}

	// 非 Fuzzy 模式：透传最后一个错误的详情
	if lastFailoverError != nil {
		status := lastFailoverError.Status
		if status == 0 {
			status = 503
		}
		body, err := hooks.RestoreAttachedResponseBody(c, lastFailoverError.Body)
		if err != nil {
			c.JSON(500, gin.H{"error": "敏感信息还原失败", "code": "CONTENT_SAFETY_HOOK_ERROR"})
			return
		}
		var errBody map[string]interface{}
		if err := json.Unmarshal(body, &errBody); err == nil {
			c.JSON(status, errBody)
		} else {
			c.JSON(status, gin.H{"error": string(body)})
		}
	} else {
		errMsg := "所有渠道都不可用"
		if lastError != nil {
			errMsg = lastError.Error()
		}
		c.JSON(503, gin.H{
			"error":   "所有" + apiType + "渠道都不可用",
			"details": errMsg,
		})
	}
}

// HandleAllKeysFailed 处理所有密钥都失败的情况（单渠道模式）
func HandleAllKeysFailed(c *gin.Context, fuzzyMode bool, lastFailoverError *FailoverError, lastError error, apiType string) {
	if types.IsUnsupportedProtocol(lastError) {
		c.JSON(400, gin.H{"error": gin.H{"message": lastError.Error(), "type": "unsupported_protocol", "code": "unsupported_protocol"}})
		return
	}
	if upstreamcontrol.IsLocalError(lastError) {
		localAdmissionResult(c, lastError, false)
		return
	}
	// Fuzzy 模式下返回通用错误
	if fuzzyMode {
		c.JSON(503, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "service_unavailable",
				"message": "All upstream channels are currently unavailable",
			},
		})
		return
	}

	// 非 Fuzzy 模式：透传最后一个错误的详情
	if lastFailoverError != nil {
		status := lastFailoverError.Status
		if status == 0 {
			status = 500
		}
		body, err := hooks.RestoreAttachedResponseBody(c, lastFailoverError.Body)
		if err != nil {
			c.JSON(500, gin.H{"error": "敏感信息还原失败", "code": "CONTENT_SAFETY_HOOK_ERROR"})
			return
		}
		var errBody map[string]interface{}
		if err := json.Unmarshal(body, &errBody); err == nil {
			c.JSON(status, errBody)
		} else {
			c.JSON(status, gin.H{"error": string(body)})
		}
	} else {
		errMsg := "未知错误"
		if lastError != nil {
			errMsg = lastError.Error()
		}
		c.JSON(500, gin.H{
			"error":   "所有上游" + apiType + "API密钥都不可用",
			"details": errMsg,
		})
	}
}
