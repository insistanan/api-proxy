package codec

import (
	"bufio"
	"context"
	"strings"
)

// SSEEvent 保留原始事件供透传，Data 只合并 SSE data 字段，不重编码 JSON。
type SSEEvent struct {
	Raw  string
	Name string
	Data string
}

// ReadSSEEvent 每次只读取一个事件，无固定行长上限。
// EOF 或读取错误可与最后一个事件同时返回，调用方先判定事件再处理读错误。
func ReadSSEEvent(ctx context.Context, reader *bufio.Reader) (SSEEvent, error) {
	var raw strings.Builder
	for {
		if err := ctx.Err(); err != nil {
			return SSEEvent{}, err
		}
		line, err := reader.ReadString('\n')
		if cancelErr := ctx.Err(); cancelErr != nil {
			return SSEEvent{}, cancelErr
		}
		raw.WriteString(line)
		if err != nil || strings.TrimRight(line, "\r\n") == "" {
			event := SSEEvent{Raw: raw.String()}
			var data []string
			for _, field := range strings.Split(event.Raw, "\n") {
				field = strings.TrimSuffix(field, "\r")
				if payload, ok := ParseSSEDataLine(field); ok {
					data = append(data, payload)
				} else if name, ok := strings.CutPrefix(field, "event:"); ok {
					event.Name = strings.TrimSpace(name)
				}
			}
			event.Data = strings.Join(data, "\n")
			return event, err
		}
	}
}
