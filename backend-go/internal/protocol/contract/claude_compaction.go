package contract

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

func IsClaudeOnDemandCompaction(body []byte) bool {
	return gjson.GetBytes(body, "compaction.type").String() == "summarize"
}

func ValidateClaudeCompactionResponse(body []byte) error {
	if !gjson.ValidBytes(body) {
		return fmt.Errorf("Claude 压缩响应不是完整 JSON")
	}
	root := gjson.ParseBytes(body)
	blocks := root.Get("content").Array()
	if root.Get("stop_reason").String() != "compaction" || len(blocks) != 1 {
		return fmt.Errorf("Claude 按需压缩必须返回单个 compaction 块和 compaction 停止原因")
	}
	if blocks[0].Get("type").String() != "compaction" || strings.TrimSpace(blocks[0].Get("content").String()) == "" || blocks[0].Get("signature").String() == "" {
		return fmt.Errorf("Claude 按需压缩缺少完整摘要或签名，不能替换历史")
	}
	return nil
}

// ClaudeCompactionStream 验证按需压缩的完整块、关闭事件及终态；不合成成功事件。
type ClaudeCompactionStream struct {
	started  bool
	closed   bool
	stopped  bool
	Complete bool
	index    float64
}

func (s *ClaudeCompactionStream) Observe(data map[string]interface{}) error {
	switch data["type"] {
	case "content_block_start":
		index, validIndex := data["index"].(float64)
		if !validIndex || index != 0 {
			return fmt.Errorf("Claude 按需压缩块索引必须为 0")
		}
		block, ok := data["content_block"].(map[string]interface{})
		if !ok || s.started || block["type"] != "compaction" {
			return fmt.Errorf("Claude 按需压缩必须仅返回一个 compaction 块")
		}
		content, _ := block["content"].(string)
		signature, _ := block["signature"].(string)
		if strings.TrimSpace(content) == "" || signature == "" {
			return fmt.Errorf("Claude 压缩块缺少完整摘要或签名")
		}
		s.started, s.index = true, index
	case "content_block_delta":
		return fmt.Errorf("Claude 按需压缩收到非完整摘要增量，拒绝交付")
	case "content_block_stop":
		index, validIndex := data["index"].(float64)
		if !s.started || s.closed || !validIndex || index != s.index {
			return fmt.Errorf("Claude 压缩块关闭事件不匹配")
		}
		s.closed = true
	case "message_delta":
		if delta, ok := data["delta"].(map[string]interface{}); ok && delta["stop_reason"] != nil {
			if delta["stop_reason"] != "compaction" {
				return fmt.Errorf("Claude 压缩未正常完成")
			}
			s.stopped = true
		}
	case "message_stop":
		if !s.closed || !s.stopped {
			return fmt.Errorf("Claude 压缩响应缺少完整终态，保留旧历史")
		}
		s.Complete = true
	}
	return nil
}
