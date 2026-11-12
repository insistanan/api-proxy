// 本文件是 Claude SSE 单个事件的判定、构造与文本提取：事件类型判定
// （message_start / content_block_start / message_stop / message_delta）、
// message_start 的构造与模型名改写、stream_error 事件构造、文本增量提取与日志截断。
// data 行的解析/改写骨架复用 utils（ParseSSEDataLine / RewriteSSEDataLines）。
package streams

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/insistanan/api-proxy/internal/infra/utils"
)

// 还原器可能一次释放多个 SSE 事件，只记录成功写给客户端的文本与思考块。
func (ctx *Context) trackDeliveredBlocks(events string) {
	for _, line := range strings.Split(events, "\n") {
		payload, ok := utils.ParseSSEDataLine(line)
		if !ok {
			continue
		}
		var data struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Block struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}
		if json.Unmarshal([]byte(payload), &data) != nil {
			continue
		}
		switch data.Type {
		case "content_block_start":
			if data.Block.Type == "thinking" || data.Block.Type == "text" {
				if ctx.DeliveredBlocks == nil {
					ctx.DeliveredBlocks = make(map[int]string)
				}
				ctx.DeliveredBlocks[data.Index] = data.Block.Type
			}
		case "content_block_stop":
			delete(ctx.DeliveredBlocks, data.Index)
		}
	}
}

// 流失败前关闭已显示的正文和思考，避免客户端重试后遗留转圈状态。
// 不补成功终止事件，也不把尚未完成的工具参数定稿为可执行调用。
func (ctx *Context) closeDeliveredBlocks(w gin.ResponseWriter, flusher http.Flusher) error {
	indexes := make([]int, 0, len(ctx.DeliveredBlocks))
	for index := range ctx.DeliveredBlocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		event := fmt.Sprintf("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", index)
		if _, err := w.Write([]byte(event)); err != nil {
			ctx.ClientGone = true
			return fmt.Errorf("关闭中断的内容块失败: %w", err)
		}
		delete(ctx.DeliveredBlocks, index)
		flusher.Flush()
	}
	return nil
}

// ParseSSEEventData 解析单个 SSE 事件的第一段 JSON data。
func ParseSSEEventData(event string) (map[string]interface{}, bool) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}
		if jsonStr == "" || jsonStr == utils.SSEDoneMarker {
			return nil, false
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			return nil, false
		}
		return data, true
	}
	return nil, false
}

func ReplaceSSEData(event string, data map[string]interface{}) string {
	patchedJSON, err := json.Marshal(data)
	if err != nil {
		return event
	}

	// 只替换第一条 data 行；其余行（含后续 data 行）原样保留
	replaced := false
	result, _ := utils.RewriteSSEDataLines(event, func(string) (string, bool) {
		if replaced {
			return "", false
		}
		replaced = true
		return string(patchedJSON), true
	})
	if !replaced {
		return event
	}
	return result
}

// BuildStreamErrorEvent 构建流错误 SSE 事件
func BuildStreamErrorEvent(err error) string {
	errorEvent := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "stream_error",
			"message": fmt.Sprintf("Stream processing error: %v", err),
		},
	}
	eventJSON, _ := json.Marshal(errorEvent)
	return fmt.Sprintf("event: error\ndata: %s\n\n", eventJSON)
}

// IsMessageStartEvent 检测是否为 message_start 事件
func IsMessageStartEvent(event string) bool {
	return strings.Contains(event, "\"type\":\"message_start\"") ||
		strings.Contains(event, "\"type\": \"message_start\"")
}

// IsContentBlockStartEvent 检测是否为 content_block_start 事件
func IsContentBlockStartEvent(event string) bool {
	return strings.Contains(event, "\"type\":\"content_block_start\"") ||
		strings.Contains(event, "\"type\": \"content_block_start\"")
}

// IsMessageStopEvent 检测是否为 message_stop 事件
func IsMessageStopEvent(event string) bool {
	if strings.Contains(event, "event: message_stop") {
		return true
	}

	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		if data["type"] == "message_stop" {
			return true
		}
	}
	return false
}

// IsMessageDeltaEvent 检测是否为 message_delta 事件
func IsMessageDeltaEvent(event string) bool {
	if strings.Contains(event, "event: message_delta") {
		return true
	}
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		if data["type"] == "message_delta" {
			return true
		}
	}
	return false
}

// IsStreamErrorEvent 判断 SSE 事件是否为上游错误事件。
func IsStreamErrorEvent(event string) bool {
	if strings.Contains(event, "event: error") {
		return true
	}

	data, hasData := ParseSSEEventData(event)
	if !hasData {
		return false
	}

	if typeStr, ok := data["type"].(string); ok && typeStr == "error" {
		return true
	}

	if errVal, exists := data["error"]; exists && errVal != nil {
		switch v := errVal.(type) {
		case map[string]interface{}:
			return len(v) > 0
		case string:
			return strings.TrimSpace(v) != ""
		}
	}

	return false
}

// ExtractStreamErrorMessage 提取流错误事件中的错误详情。
func ExtractStreamErrorMessage(event string) string {
	data, hasData := ParseSSEEventData(event)
	if hasData {
		if errObj, ok := data["error"].(map[string]interface{}); ok {
			if msg, ok := errObj["message"].(string); ok && msg != "" {
				return msg
			}
		} else if errStr, ok := data["error"].(string); ok && errStr != "" {
			return errStr
		}
		if msg, ok := data["message"].(string); ok && msg != "" {
			return msg
		}
	}
	return strings.TrimSpace(event)
}

// BuildMessageStartEvent 构造一个合成的 message_start 事件
func BuildMessageStartEvent(model string) string {
	if model == "" {
		model = "unknown"
	}
	event := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            fmt.Sprintf("msg_%s", uuid.New().String()),
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]interface{}{"input_tokens": 0, "output_tokens": 1},
		},
	}
	eventJSON, _ := json.Marshal(event)
	return fmt.Sprintf("event: message_start\ndata: %s\n\n", eventJSON)
}

// PatchMessageStartEvent 修补 message_start 事件中的 id 和 model 字段
func PatchMessageStartEvent(event string, requestModel string, rewriteModel bool, enableLog bool) string {
	if !IsMessageStartEvent(event) {
		return event
	}

	// patched 跨行保持（与收敛前一致）：一旦某行触发过修补，后续 data 行也走重写分支
	patched := false
	result, _ := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return "", false
		}

		msg, ok := data["message"].(map[string]interface{})
		if !ok {
			return "", false
		}

		// 补全空 id
		if id, _ := msg["id"].(string); id == "" {
			msg["id"] = fmt.Sprintf("msg_%s", uuid.New().String())
			patched = true
			if enableLog {
				log.Printf("[Messages-Stream-Patch] 补全空 message.id: %s", utils.RedactSensitivePlaceholdersForLog(fmt.Sprint(msg["id"])))
			}
		}

		// 检查 model 一致性（仅在配置启用时改写）
		if rewriteModel {
			if responseModel, _ := msg["model"].(string); responseModel != "" && requestModel != "" && responseModel != requestModel {
				msg["model"] = requestModel
				patched = true
				if enableLog {
					log.Printf("[Messages-Stream-Patch] 改写 message.model: %s -> %s", responseModel, requestModel)
				}
			}
		}

		if !patched {
			return "", false
		}

		patchedJSON, err := json.Marshal(data)
		if err != nil {
			return "", false
		}
		return string(patchedJSON), true
	})

	return result
}

// ExtractTextFromEvent 从 SSE 事件中提取文本内容
func ExtractTextFromEvent(event string, buf *bytes.Buffer) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// Claude SSE: delta.text
		if delta, ok := data["delta"].(map[string]interface{}); ok {
			if text, ok := delta["text"].(string); ok {
				buf.WriteString(text)
			}
			if partialJSON, ok := delta["partial_json"].(string); ok {
				buf.WriteString(partialJSON)
			}
		}

		// content_block_start 中的初始文本
		if cb, ok := data["content_block"].(map[string]interface{}); ok {
			if text, ok := cb["text"].(string); ok {
				buf.WriteString(text)
			}
		}
	}
}

func ExtractTextFromEventData(data map[string]interface{}, buf *bytes.Buffer) {
	if data == nil {
		return
	}

	// Claude SSE: delta.text
	if delta, ok := data["delta"].(map[string]interface{}); ok {
		if text, ok := delta["text"].(string); ok {
			buf.WriteString(text)
		}
		if partialJSON, ok := delta["partial_json"].(string); ok {
			buf.WriteString(partialJSON)
		}
	}

	// content_block_start 中的初始文本
	if cb, ok := data["content_block"].(map[string]interface{}); ok {
		if text, ok := cb["text"].(string); ok {
			buf.WriteString(text)
		}
	}
}

// extractSSEEventInfo 从 SSE 事件中提取事件类型、block 索引和 block 类型
func extractSSEEventInfo(event string) (eventType string, blockIndex int, blockType string) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ = data["type"].(string)
		if idx, ok := data["index"].(float64); ok {
			blockIndex = int(idx)
		}

		// 从 content_block 中提取类型
		if cb, ok := data["content_block"].(map[string]interface{}); ok {
			blockType, _ = cb["type"].(string)
		}

		return
	}
	return
}

// truncateForLog 截断字符串用于日志输出
func truncateForLog(s string, maxLen int) string {
	s = utils.RedactSensitivePlaceholdersForLog(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
