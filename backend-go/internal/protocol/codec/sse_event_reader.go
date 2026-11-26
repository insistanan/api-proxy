package codec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SSEEvent 保留原始事件供透传，Data 只合并 SSE data 字段，不重编码 JSON。
type SSEEvent struct {
	Raw  string
	Name string
	Data string
}

// DataLine 将完整 JSON 载荷交给现有转换器，不让多行 data 被当作多个片段。
func (e SSEEvent) DataLine() (string, error) {
	if e.Data == "" {
		return "", nil
	}
	if strings.TrimSpace(e.Data) == SSEDoneMarker {
		return SSEDataLinePrefix + SSEDoneMarker, nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(e.Data)); err != nil {
		return "", fmt.Errorf("上游 SSE 事件包含无效 JSON: %w", err)
	}
	return SSEDataLinePrefix + compact.String(), nil
}

// Normalized 合并 data 字段供交付钩子读取，其余 SSE 字段和注释保留。
func (e SSEEvent) Normalized() (string, error) {
	data, err := e.DataLine()
	if err != nil {
		return "", err
	}
	return e.WithDataLine(data), nil
}

// WithDataLine 用已校验的规范 data 行重建完整事件。
func (e SSEEvent) WithDataLine(data string) string {
	var lines []string
	written := false
	for _, line := range strings.Split(strings.TrimRight(e.Raw, "\r\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if _, ok := ParseSSEDataLine(line); ok {
			if !written && data != "" {
				lines = append(lines, data)
			} else if !written {
				lines = append(lines, "data:")
			}
			written = true
		} else {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n") + "\n\n"
}

// SSEEventReader 保留与最终事件同时出现的读取错误，交由协议终态判定处理。
type SSEEventReader struct {
	ctx    context.Context
	reader *bufio.Reader
	event  SSEEvent
	err    error
}

func NewSSEEventReader(ctx context.Context, reader io.Reader) *SSEEventReader {
	return &SSEEventReader{ctx: ctx, reader: bufio.NewReaderSize(reader, 64*1024)}
}

func (r *SSEEventReader) Scan() bool {
	if r.err != nil {
		return false
	}
	r.event, r.err = ReadSSEEvent(r.ctx, r.reader)
	return r.event.Raw != ""
}

func (r *SSEEventReader) Event() SSEEvent { return r.event }
func (r *SSEEventReader) DataLine() (string, error) {
	line, err := r.event.DataLine()
	if err != nil {
		return "", errors.Join(err, r.Err())
	}
	return line, nil
}
func (r *SSEEventReader) Err() error {
	if errors.Is(r.err, io.EOF) {
		return nil
	}
	return r.err
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
