package proxycore

import (
	"bytes"
	"encoding/json"
	"log"

	"github.com/insistanan/api-proxy/internal/infra/utils"
)

// RemoveEmptySignatures 移除请求体中 messages[*].content[*].signature 的空值
// 用于预防 Claude API 返回 400 错误
// 仅处理已知路径：messages 数组中各消息的 content 数组中的 signature 字段
// enableLog: 是否输出日志（由 envCfg.Current().EnableRequestLogs 控制）
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
func RemoveEmptySignatures(bodyBytes []byte, enableLog bool, apiType string) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber() // 保留数字精度

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes, false
	}

	modified, removedCount := removeEmptySignaturesInMessages(data)
	if !modified {
		return bodyBytes, false
	}

	if enableLog && removedCount > 0 {
		log.Printf("[%s-Preprocess] 已移除 %d 个空 signature 字段", apiType, removedCount)
	}

	// 使用 Encoder 并禁用 HTML 转义，保持原始格式
	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes, false
	}
	return newBytes, true
}

// removeEmptySignaturesInMessages 仅处理 messages[*].content[*].signature 路径
// 返回 (是否有修改, 移除的字段数)
func removeEmptySignaturesInMessages(data map[string]interface{}) (bool, int) {
	modified := false
	removedCount := 0

	messages, ok := data["messages"].([]interface{})
	if !ok {
		return false, 0
	}

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}

		content, ok := msgMap["content"].([]interface{})
		if !ok {
			continue
		}

		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				continue
			}

			if sig, exists := blockMap["signature"]; exists {
				if sig == nil {
					delete(blockMap, "signature")
					modified = true
					removedCount++
				} else if str, isStr := sig.(string); isStr && str == "" {
					delete(blockMap, "signature")
					modified = true
					removedCount++
				}
			}
		}
	}

	return modified, removedCount
}
