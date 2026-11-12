package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBufferPool_ChunkBuffer32KB(t *testing.T) {
	buf := GetChunkBuffer32KB()
	assert.Equal(t, chunkSize32KB, len(buf))
	buf[0] = 0x42
	PutChunkBuffer32KB(buf)

	buf2 := GetChunkBuffer32KB()
	assert.Equal(t, chunkSize32KB, len(buf2))
	PutChunkBuffer32KB(buf2)
}

func TestBufferPool_ChunkBuffer64KB(t *testing.T) {
	buf := GetChunkBuffer64KB()
	assert.Equal(t, chunkSize64KB, len(buf))
	PutChunkBuffer64KB(buf)
}

func TestBufferPool_BytesBuffer(t *testing.T) {
	buf := GetBytesBuffer()
	buf.WriteString("hello pool")
	assert.Equal(t, "hello pool", buf.String())
	PutBytesBuffer(buf)

	buf2 := GetBytesBuffer()
	assert.Equal(t, 0, buf2.Len())
	PutBytesBuffer(buf2)
}
