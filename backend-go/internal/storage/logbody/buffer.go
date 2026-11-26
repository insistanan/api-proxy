package logbody

import "strings"

// Buffer 仅用于日志正文；超过预算后继续记账，不再保存内容。
type Buffer struct {
	Limit         int
	Truncated     bool
	OriginalBytes int
	data          strings.Builder
}

func (b *Buffer) WriteString(value string) (int, error) {
	n := len(value)
	b.OriginalBytes += n
	remaining := max(0, b.Limit-b.data.Len())
	if len(value) > remaining {
		b.Truncated = true
		value = value[:remaining]
	}
	b.data.WriteString(value)
	return n, nil
}
func (b *Buffer) Write(value []byte) (int, error) { return b.WriteString(string(value)) }
func (b *Buffer) Len() int                        { return b.data.Len() }
func (b *Buffer) String() string                  { return b.data.String() }
