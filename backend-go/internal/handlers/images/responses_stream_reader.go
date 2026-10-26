package images

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/utils"
)

// readResponsesImageEvent 使用固定大小的 ReadSlice 缓冲逐片收集单个相关事件。
// 不缓存整条 SSE 流，不为大行另外分配完整字符串；追加前检查事件预算。
func readResponsesImageEvent(ctx context.Context, reader *bufio.Reader, maximumBytes int64) (string, string, error) {
	if maximumBytes <= 0 {
		return "", "", fmt.Errorf("Responses 图片事件大小限制必须为正数")
	}
	var data strings.Builder
	var eventName string
	var lineStarted, dataLine bool
	for {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		fragment, err := reader.ReadSlice('\n')
		lastFragment := err != bufio.ErrBufferFull
		fragment = bytes.TrimSuffix(fragment, []byte{'\n'})
		if lastFragment {
			fragment = bytes.TrimSuffix(fragment, []byte{'\r'})
		}
		if !lineStarted && len(fragment) == 0 && lastFragment {
			if err != nil || data.Len() > 0 || eventName != "" {
				return eventName, data.String(), err
			}
			continue
		}
		if !lineStarted {
			_, dataLine = utils.ParseSSEDataLineBytes(fragment)
			if bytes.HasPrefix(fragment, []byte("event:")) {
				if !lastFragment || len(fragment) > 1024 {
					return "", "", fmt.Errorf("Responses SSE 事件名称过长")
				}
				eventName = strings.TrimSpace(string(fragment[len("event:"):]))
			}
			if dataLine {
				fragment = fragment[len("data:"):]
				if len(fragment) > 0 && fragment[0] == ' ' {
					fragment = fragment[1:]
				}
				if data.Len() > 0 && collectResponsesImageEvent(eventName) {
					data.WriteByte('\n')
				}
			}
		}
		if dataLine && collectResponsesImageEvent(eventName) {
			if int64(data.Len())+int64(len(fragment)) > maximumBytes {
				return "", "", fmt.Errorf("Responses 图片事件超过大小限制（%d 字节）", maximumBytes)
			}
			data.Write(fragment)
		}
		lineStarted = !lastFragment
		if err != nil && err != bufio.ErrBufferFull {
			return eventName, data.String(), err
		}
	}
}

// 文本、推理及生命周期 delta 不参与 Images 返回值，直接丢弃其正文。
// 无 event 字段的兼容上游仍需按 JSON type 判定。
func collectResponsesImageEvent(name string) bool {
	switch name {
	case "", "response.image_generation_call.partial_image", "response.output_item.done",
		"response.completed", "response.failed", "response.incomplete", "error":
		return true
	default:
		return false
	}
}
