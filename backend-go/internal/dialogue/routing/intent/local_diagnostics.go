package intent

import "sync"

// processDiagnostic 只保留有限尾部，便于定位 CPU、系统库或模型加载失败。
type processDiagnostic struct {
	mu   sync.Mutex
	tail []byte
}

func (d *processDiagnostic) Write(data []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	const limit = 8192
	n := len(data)
	if len(data) >= limit {
		d.tail = append(d.tail[:0], data[len(data)-limit:]...)
	} else {
		if excess := len(d.tail) + len(data) - limit; excess > 0 {
			d.tail = append(d.tail[:0], d.tail[excess:]...)
		}
		d.tail = append(d.tail, data...)
	}
	return n, nil
}

func (d *processDiagnostic) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return safeTraceText(string(d.tail))
}
