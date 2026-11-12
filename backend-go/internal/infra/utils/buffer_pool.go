package utils

import (
	"bytes"
	"sync"
)

const (
	chunkSize32KB = 32 * 1024
	chunkSize64KB = 64 * 1024
)

var pool32KB = sync.Pool{
	New: func() interface{} {
		b := make([]byte, chunkSize32KB)
		return &b
	},
}

var pool64KB = sync.Pool{
	New: func() interface{} {
		b := make([]byte, chunkSize64KB)
		return &b
	},
}

var bytesBufferPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

// GetChunkBuffer32KB 从池中获取 32KB 切片用于流式搬运
func GetChunkBuffer32KB() []byte {
	ptr := pool32KB.Get().(*[]byte)
	return (*ptr)[:chunkSize32KB]
}

// PutChunkBuffer32KB 将 32KB 切片归还到池中
func PutChunkBuffer32KB(buf []byte) {
	if cap(buf) < chunkSize32KB {
		return
	}
	slice := buf[:chunkSize32KB]
	pool32KB.Put(&slice)
}

// GetChunkBuffer64KB 从池中获取 64KB 切片用于 Scanner 缓冲
func GetChunkBuffer64KB() []byte {
	ptr := pool64KB.Get().(*[]byte)
	return (*ptr)[:chunkSize64KB]
}

// PutChunkBuffer64KB 将 64KB 切片归还到池中
func PutChunkBuffer64KB(buf []byte) {
	if cap(buf) < chunkSize64KB {
		return
	}
	slice := buf[:chunkSize64KB]
	pool64KB.Put(&slice)
}

// GetBytesBuffer 从池中获取 bytes.Buffer
func GetBytesBuffer() *bytes.Buffer {
	buf := bytesBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

// PutBytesBuffer 将 bytes.Buffer 重置后归还到池中（防止异常超大 buffer 占用过多内存）
func PutBytesBuffer(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	// 若缓冲区已扩张超过 2MB，则不放回池中，让 GC 回收
	if buf.Cap() > 2*1024*1024 {
		return
	}
	buf.Reset()
	bytesBufferPool.Put(buf)
}
