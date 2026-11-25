package codec

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
)

// SSELineReader 按行读取 SSE，不为合法事件设置任意的单行大小限制。
// 正文关闭由 BindStreamBody 绑定取消；读取保持同步，以消费者速度形成背压。
type SSELineReader struct {
	ctx    context.Context
	reader *bufio.Reader
	line   string
	err    error
}

func NewSSELineReader(ctx context.Context, reader io.Reader) *SSELineReader {
	return &SSELineReader{ctx: ctx, reader: bufio.NewReaderSize(reader, 64*1024)}
}

func (r *SSELineReader) Scan() bool {
	if r.err != nil {
		return false
	}
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return false
	}
	line, err := r.reader.ReadString('\n')
	if cancelErr := r.ctx.Err(); cancelErr != nil {
		r.err = cancelErr
		return false
	}
	r.err = err
	r.line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	return len(line) > 0
}

func (r *SSELineReader) Text() string { return r.line }

func (r *SSELineReader) Err() error {
	if errors.Is(r.err, io.EOF) {
		return nil
	}
	return r.err
}
