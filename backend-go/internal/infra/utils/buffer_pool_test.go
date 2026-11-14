package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBufferPool_BytesBuffer(t *testing.T) {
	buf := GetBytesBuffer()
	buf.WriteString("hello pool")
	assert.Equal(t, "hello pool", buf.String())
	PutBytesBuffer(buf)

	buf2 := GetBytesBuffer()
	assert.Equal(t, 0, buf2.Len())
	PutBytesBuffer(buf2)
}
