package httpclient

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func BenchmarkIdleTimeoutRead(b *testing.B) {
	data, buffer := bytes.Repeat([]byte("x"), 1<<20), make([]byte, 4096)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		reader := NewIdleTimeoutReader(io.NopCloser(bytes.NewReader(data)), time.Minute, "benchmark")
		for {
			_, err := reader.Read(buffer)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
		if err := reader.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
