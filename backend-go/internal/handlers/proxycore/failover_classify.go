package proxycore

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/utils"
)

// ShouldRetryWithNextKey 判断是否应该使用下一个密钥重试
// 返回: (shouldFailover bool, isQuotaRelated bool)
//
// apiType 用于五协议日志标签；Fuzzy 放宽状态码分类，但仍排除请求尺寸和不可重试错误。
//
// HTTP 状态码分类策略（非 fuzzy 模式）：
//   - 4xx 客户端错误：部分应触发 failover（密钥/配额问题）
//   - 5xx 服务端错误：应触发 failover（上游临时故障）
//   - 2xx/3xx：不应触发 failover（成功或重定向）
//
// isQuotaRelated 标记用于调度器优先级调整：
//   - true: 额度/配额相关，降低密钥优先级
//   - false: 临时错误，不影响优先级
func ShouldRetryWithNextKey(statusCode int, bodyBytes []byte, fuzzyMode bool, apiType string) (bool, bool) {
	log.Printf("[%s-Failover-Entry] ShouldRetryWithNextKey 入口: statusCode=%d, bodyLen=%d, fuzzyMode=%v",
		apiType, statusCode, len(bodyBytes), fuzzyMode)
	if fuzzyMode {
		return shouldRetryWithNextKeyFuzzy(statusCode, bodyBytes, apiType)
	}
	return shouldRetryWithNextKeyNormal(statusCode, bodyBytes, apiType)
}

// shouldRetryWithNextKeyFuzzy 排除请求尺寸和不可重试错误后，对其余非 2xx 响应允许 failover。
// 配额关键词与 402/429 状态共同决定是否降级 Key。
func shouldRetryWithNextKeyFuzzy(statusCode int, bodyBytes []byte, apiType string) (bool, bool) {
	log.Printf("[%s-Failover-Fuzzy] 进入 Fuzzy 模式处理: statusCode=%d, bodyLen=%d", apiType, statusCode, len(bodyBytes))
	if statusCode >= 200 && statusCode < 300 {
		return false, false
	}

	// 请求尺寸类 4xx：请求体/URI 过大，换 Key 或换渠道不会改变请求体大小，
	// failover 纯属浪费并把无辜 Key 标记失败。直接返回，让客户端收到原始错误。
	// 411=Length Required, 413=Payload Too Large, 414=URI Too Long, 431=Request Headers Too Large
	if statusCode == 411 || statusCode == 413 || statusCode == 414 || statusCode == 431 {
		log.Printf("[%s-Failover-Fuzzy] 状态码 %d 为请求尺寸类错误，不进行 failover", apiType, statusCode)
		return false, false
	}

	// 1. 检查是否为不可重试错误（内容审核等），无论是 500 还是其他状态码，这类错误都不应重试
	if len(bodyBytes) > 0 {
		if utils.IsNonRetryableUpstreamErrorBody(bodyBytes) {
			log.Printf("[%s-Failover-Fuzzy] 检测到不可重试错误，不进行 failover", apiType)
			return false, false
		}
	}

	// 2. 优先检查消息体是否包含配额相关关键词，或者状态码直接为配额相关
	// 这样 500 + "Quota exceeded" 或者是 403 + "预扣费额度" 消息 → isQuotaRelated=true
	if statusCode == 402 || statusCode == 429 {
		log.Printf("[%s-Failover-Fuzzy] 状态码 %d 直接标记为配额相关", apiType, statusCode)
		return true, true
	}
	if len(bodyBytes) > 0 {
		_, msgQuota := classifyByErrorMessage(bodyBytes, apiType)
		if msgQuota {
			log.Printf("[%s-Failover-Fuzzy] 消息体包含配额相关关键词，标记为配额相关", apiType)
			return true, true
		}
	}

	// 3. 服务端错误，进行 failover (非配额相关)
	if statusCode >= 500 {
		log.Printf("[%s-Failover-Fuzzy] 状态码 %d 为上游服务端错误，进行 failover", apiType, statusCode)
		return true, false
	}

	// 4. Fuzzy 模式下，其他所有非 2xx 状态码都进行 failover (非配额相关)
	log.Printf("[%s-Failover-Fuzzy] Fuzzy 模式结果: shouldFailover=true, isQuotaRelated=false", apiType)
	return true, false
}

// shouldRetryWithNextKeyNormal 原有的精确错误分类逻辑
func shouldRetryWithNextKeyNormal(statusCode int, bodyBytes []byte, apiType string) (bool, bool) {
	// 先检查是否为不可重试错误（内容审核等），这类错误无论状态码如何都不应重试
	if len(bodyBytes) > 0 && utils.IsNonRetryableUpstreamErrorBody(bodyBytes) {
		log.Printf("[%s-Failover-Debug] 检测到不可重试错误，不进行 failover", apiType)
		return false, false
	}

	shouldFailover, isQuotaRelated := utils.ClassifyUpstreamStatus(statusCode)

	log.Printf("[%s-Failover-Debug] shouldRetryWithNextKeyNormal: statusCode=%d, bodyLen=%d, shouldFailover=%v, isQuotaRelated=%v",
		apiType, statusCode, len(bodyBytes), shouldFailover, isQuotaRelated)

	if shouldFailover {
		// 如果状态码已标记为 quota 相关，直接返回
		if isQuotaRelated {
			return true, true
		}
		// 否则，仍检查消息体是否包含 quota 相关关键词
		// 这样 403 + "预扣费额度" 消息 → isQuotaRelated=true
		log.Printf("[%s-Failover-Debug] 调用 classifyByErrorMessage, body=%s", apiType, utils.RedactSensitivePlaceholdersForLog(string(bodyBytes)))
		_, msgQuota := classifyByErrorMessage(bodyBytes, apiType)
		log.Printf("[%s-Failover-Debug] classifyByErrorMessage 返回: msgQuota=%v", apiType, msgQuota)
		if msgQuota {
			return true, true
		}
		return true, false
	}

	// statusCode 不触发 failover 时，完全依赖消息体判断
	return classifyByErrorMessage(bodyBytes, apiType)
}

// classifyByErrorMessage 基于错误消息内容分类
func classifyByErrorMessage(bodyBytes []byte, apiType string) (bool, bool) {
	var errResp map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
		log.Printf("[%s-Failover-Debug] JSON解析失败: %v, body长度=%d", apiType, err, len(bodyBytes))
		return false, false
	}

	errObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		log.Printf("[%s-Failover-Debug] 未找到error对象, keys=%v", apiType, getMapKeys(errResp))
		return false, false
	}

	// 检查 error.code 字段，某些错误码不应重试（内容审核、无效请求等）
	if errCode, ok := errObj["code"].(string); ok {
		if utils.IsNonRetryableUpstreamErrorCode(errCode) {
			log.Printf("[%s-Failover-Debug] 检测到不可重试错误码: %s", apiType, errCode)
			return false, false
		}
	}

	// 尝试多个可能的消息字段: message, upstream_error, detail
	messageFields := []string{"message", "upstream_error", "detail"}
	for _, field := range messageFields {
		if msg, ok := errObj[field].(string); ok {
			log.Printf("[%s-Failover-Debug] 提取到消息 (字段: %s): %s", apiType, field, utils.RedactSensitivePlaceholdersForLog(msg))
			if failover, quota := classifyMessage(msg); failover {
				log.Printf("[%s-Failover-Debug] 消息分类结果: failover=%v, quota=%v", apiType, failover, quota)
				return true, quota
			}
		}
	}

	// 如果 upstream_error 是嵌套对象，尝试提取其中的消息
	if upstreamErr, ok := errObj["upstream_error"].(map[string]interface{}); ok {
		if msg, ok := upstreamErr["message"].(string); ok {
			log.Printf("[%s-Failover-Debug] 提取到嵌套 upstream_error.message: %s", apiType, utils.RedactSensitivePlaceholdersForLog(msg))
			if failover, quota := classifyMessage(msg); failover {
				log.Printf("[%s-Failover-Debug] 消息分类结果: failover=%v, quota=%v", apiType, failover, quota)
				return true, quota
			}
		}
	}

	// 检查 type 字段
	if errType, ok := errObj["type"].(string); ok {
		if failover, quota := classifyErrorType(errType); failover {
			return true, quota
		}
	}

	log.Printf("[%s-Failover-Debug] 未匹配任何关键词, errObj keys=%v", apiType, getMapKeys(errObj))
	return false, false
}

// classifyMessage 基于错误消息内容分类
func classifyMessage(msg string) (bool, bool) {
	msgLower := strings.ToLower(msg)

	// 配额/余额相关关键词 (failover + quota)
	quotaKeywords := []string{
		"insufficient", "quota", "credit", "balance",
		"rate limit", "limit exceeded", "exceeded",
		"billing", "payment", "subscription",
		"积分不足", "余额不足", "请求数限制", "额度", "预扣费",
	}
	for _, keyword := range quotaKeywords {
		if strings.Contains(msgLower, keyword) {
			return true, true
		}
	}

	// 认证/授权相关关键词 (failover + 非 quota)
	authKeywords := []string{
		"invalid", "unauthorized", "authentication",
		"api key", "apikey", "token", "expired",
		"permission", "forbidden", "denied",
		"密钥无效", "认证失败", "权限不足",
	}
	for _, keyword := range authKeywords {
		if strings.Contains(msgLower, keyword) {
			return true, false
		}
	}

	// 临时错误关键词 (failover + 非 quota)
	transientKeywords := []string{
		"timeout", "timed out", "temporarily",
		"overloaded", "unavailable", "retry",
		"server error", "internal error",
		"selected model is at capacity", "model is at capacity",
		"超时", "暂时", "重试",
	}
	for _, keyword := range transientKeywords {
		if strings.Contains(msgLower, keyword) {
			return true, false
		}
	}

	return false, false
}

// classifyErrorType 基于错误类型分类
func classifyErrorType(errType string) (bool, bool) {
	typeLower := strings.ToLower(errType)

	// 配额相关的错误类型 (failover + quota)
	quotaTypes := []string{
		"over_quota", "quota_exceeded", "rate_limit",
		"billing", "insufficient", "payment",
	}
	for _, t := range quotaTypes {
		if strings.Contains(typeLower, t) {
			return true, true
		}
	}

	// 认证相关的错误类型 (failover + 非 quota)
	authTypes := []string{
		"authentication", "authorization", "permission",
		"invalid_api_key", "invalid_token", "expired",
	}
	for _, t := range authTypes {
		if strings.Contains(typeLower, t) {
			return true, false
		}
	}

	// 服务端错误类型 (failover + 非 quota)
	serverTypes := []string{
		"server_error", "internal_error", "service_unavailable",
		"timeout", "overloaded",
	}
	for _, t := range serverTypes {
		if strings.Contains(typeLower, t) {
			return true, false
		}
	}

	return false, false
}

// getMapKeys 获取 map 的所有 key（用于调试日志）
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
