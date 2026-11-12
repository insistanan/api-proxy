package logger

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"
)

const maxAppLineBytes = 64 << 10

type lineWriter struct {
	store     *Store
	mu        sync.Mutex
	buf       []byte
	continued bool
}

func newLineWriter(store *Store) *lineWriter {
	return &lineWriter{store: store}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	if w == nil || w.store == nil {
		return 0, fmt.Errorf("日志写入器不可用")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	written := 0
	for len(p) > 0 {
		take := min(len(p), maxAppLineBytes-len(w.buf))
		index := bytes.IndexByte(p[:take], '\n')
		if index >= 0 {
			take = index
		}
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		written += take
		complete := index >= 0 && take == index
		if complete {
			p = p[1:]
			written++
			if w.continued && len(w.buf) == 0 {
				w.continued = false
				continue
			}
		}
		if complete || len(w.buf) == maxAppLineBytes {
			// 超长单行分段落库，保留全部文本，同时避免无换行输入无限占用内存。
			var entry AppLog
			if w.continued || !complete {
				entry = AppLog{Tag: "Logger-LongLine", Message: string(w.buf)}
			} else {
				entry = parseStdLogLine(string(w.buf))
			}
			w.continued = !complete
			w.buf = nil
			if err := w.store.RecordAppLog(entry); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func parseStdLogLine(line string) AppLog {
	line = strings.TrimRight(line, "\r")
	entry := AppLog{Message: line}
	const prefixLayout = "2006/01/02 15:04:05.000000"
	if len(line) >= len(prefixLayout) {
		if parsed, err := time.ParseInLocation(prefixLayout, line[:len(prefixLayout)], time.Local); err == nil {
			entry.UnixMilli = parsed.UnixMilli()
			entry.Timestamp = parsed.UTC().Format(time.RFC3339Nano)
			entry.Message = strings.TrimSpace(line[len(prefixLayout):])
		}
	}
	if entry.UnixMilli == 0 {
		now := time.Now()
		entry.UnixMilli = now.UnixMilli()
		entry.Timestamp = now.UTC().Format(time.RFC3339Nano)
	}
	entry.Tag, entry.Message = splitLogTag(entry.Message)
	return entry
}

func splitLogTag(message string) (string, string) {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, "[") {
		return "", message
	}
	end := strings.IndexByte(message, ']')
	if end <= 1 {
		return "", message
	}
	return message[1:end], strings.TrimSpace(message[end+1:])
}
