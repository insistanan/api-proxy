package utils

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecompressBody_Gzip(t *testing.T) {
	testPayload := []byte(`{"message":"hello world from gzip"}`)
	var compressedBuffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuffer)
	_, writeErr := gzipWriter.Write(testPayload)
	require.NoError(t, writeErr)
	require.NoError(t, gzipWriter.Close())

	decompressedBytes, wasDecompressed, decompressErr := DecompressBodyWithLimit("gzip", compressedBuffer.Bytes(), 1024*1024)
	require.NoError(t, decompressErr)
	assert.True(t, wasDecompressed)
	assert.Equal(t, testPayload, decompressedBytes)
}

func TestDecompressBody_ZlibDeflate(t *testing.T) {
	// RFC 9110 规范的 deflate = zlib 包裹格式
	testPayload := []byte(`{"message":"hello world from zlib deflate"}`)
	var compressedBuffer bytes.Buffer
	zlibWriter := zlib.NewWriter(&compressedBuffer)
	_, writeErr := zlibWriter.Write(testPayload)
	require.NoError(t, writeErr)
	require.NoError(t, zlibWriter.Close())

	decompressedBytes, wasDecompressed, decompressErr := DecompressBodyWithLimit("deflate", compressedBuffer.Bytes(), 1024*1024)
	require.NoError(t, decompressErr)
	assert.True(t, wasDecompressed)
	assert.Equal(t, testPayload, decompressedBytes)
}

func TestDecompressBody_RawDeflateFallback(t *testing.T) {
	// 违规但常见的 raw deflate 流回退测试
	testPayload := []byte(`{"message":"hello world from raw deflate"}`)
	var compressedBuffer bytes.Buffer
	flateWriter, flateErr := flate.NewWriter(&compressedBuffer, flate.DefaultCompression)
	require.NoError(t, flateErr)
	_, writeErr := flateWriter.Write(testPayload)
	require.NoError(t, writeErr)
	require.NoError(t, flateWriter.Close())

	decompressedBytes, wasDecompressed, decompressErr := DecompressBodyWithLimit("deflate", compressedBuffer.Bytes(), 1024*1024)
	require.NoError(t, decompressErr)
	assert.True(t, wasDecompressed)
	assert.Equal(t, testPayload, decompressedBytes)
}

func TestDecompressBody_Brotli(t *testing.T) {
	testPayload := []byte(`{"message":"hello world from brotli"}`)
	var compressedBuffer bytes.Buffer
	brotliWriter := brotli.NewWriter(&compressedBuffer)
	_, writeErr := brotliWriter.Write(testPayload)
	require.NoError(t, writeErr)
	require.NoError(t, brotliWriter.Close())

	decompressedBytes, wasDecompressed, decompressErr := DecompressBodyWithLimit("br", compressedBuffer.Bytes(), 1024*1024)
	require.NoError(t, decompressErr)
	assert.True(t, wasDecompressed)
	assert.Equal(t, testPayload, decompressedBytes)
}

func TestDecompressBody_Zstd(t *testing.T) {
	// Codex Desktop 登录态常发送的 zstd 压缩体
	testPayload := []byte(`{"message":"hello world from zstd","token":12345}`)
	var compressedBuffer bytes.Buffer
	zstdEncoder, encoderErr := zstd.NewWriter(&compressedBuffer)
	require.NoError(t, encoderErr)
	_, writeErr := zstdEncoder.Write(testPayload)
	require.NoError(t, writeErr)
	require.NoError(t, zstdEncoder.Close())

	decompressedBytes, wasDecompressed, decompressErr := DecompressBodyWithLimit("zstd", compressedBuffer.Bytes(), 1024*1024)
	require.NoError(t, decompressErr)
	assert.True(t, wasDecompressed)
	assert.Equal(t, testPayload, decompressedBytes)
}

func TestDecompressBody_StackedGzipThenZstd(t *testing.T) {
	// Content-Encoding: gzip, zstd 表示先经过 gzip 压缩、再经过 zstd 压缩
	// 解压必须按反向（先解 zstd，再解 gzip）
	testPayload := []byte(`{"stacked":true,"content":"reverse order decoding"}`)

	var gzipBuffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipBuffer)
	_, writeGzipErr := gzipWriter.Write(testPayload)
	require.NoError(t, writeGzipErr)
	require.NoError(t, gzipWriter.Close())

	var zstdBuffer bytes.Buffer
	zstdEncoder, encoderErr := zstd.NewWriter(&zstdBuffer)
	require.NoError(t, encoderErr)
	_, writeZstdErr := zstdEncoder.Write(gzipBuffer.Bytes())
	require.NoError(t, writeZstdErr)
	require.NoError(t, zstdEncoder.Close())

	decompressedBytes, wasDecompressed, decompressErr := DecompressBodyWithLimit("gzip, zstd", zstdBuffer.Bytes(), 1024*1024)
	require.NoError(t, decompressErr)
	assert.True(t, wasDecompressed)
	assert.Equal(t, testPayload, decompressedBytes)
}

func TestDecompressBody_OutputLimit_RejectsCompressionBomb(t *testing.T) {
	// 压缩炸弹防御测试：生成 8MB 的全零字节，压缩后只有几 KB
	uncompressedBombSize := 8 * 1024 * 1024
	bombPayload := make([]byte, uncompressedBombSize)
	var compressedBuffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuffer)
	_, writeErr := gzipWriter.Write(bombPayload)
	require.NoError(t, writeErr)
	require.NoError(t, gzipWriter.Close())

	// 压缩后体积极小
	assert.Less(t, compressedBuffer.Len(), 64*1024)

	// 限制解压上限为 1MB，解压时必须在预算耗尽处截停并返回 ErrDecompressedBodyTooLarge
	maximumAllowedLimit := int64(1024 * 1024)
	_, _, decompressErr := DecompressBodyWithLimit("gzip", compressedBuffer.Bytes(), maximumAllowedLimit)
	require.Error(t, decompressErr)
	assert.True(t, errors.Is(decompressErr, ErrDecompressedBodyTooLarge))
}

func TestDecompressBody_UnsupportedEncoding(t *testing.T) {
	testPayload := []byte("some compressed bytes")
	_, wasDecompressed, decompressErr := DecompressBodyWithLimit("snappy", testPayload, 1024*1024)
	require.Error(t, decompressErr)
	assert.False(t, wasDecompressed)
	assert.True(t, errors.Is(decompressErr, ErrUnsupportedContentEncoding))
}

func TestGetContentEncoding(t *testing.T) {
	headers := http.Header{}
	headers.Add("Content-Encoding", "gzip")
	headers.Add("Content-Encoding", "identity, zstd")
	headers.Add("Content-Encoding", "")

	normalizedEncoding := GetContentEncoding(headers)
	assert.Equal(t, "gzip, zstd", normalizedEncoding)
	assert.True(t, IsSupportedContentEncoding(normalizedEncoding))
}

func TestDecompressResponseBodyIfNeeded(t *testing.T) {
	testPayload := []byte(`{"status":"ok"}`)
	var compressedBuffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuffer)
	_, _ = gzipWriter.Write(testPayload)
	_ = gzipWriter.Close()

	httpResponse := &http.Response{
		Header: http.Header{
			"Content-Encoding": []string{"gzip"},
		},
	}

	decompressedBytes, wasDecoded, err := DecompressResponseBodyIfNeeded(httpResponse, compressedBuffer.Bytes(), 1024*1024)
	require.NoError(t, err)
	assert.True(t, wasDecoded)
	assert.Equal(t, testPayload, decompressedBytes)
}

func TestWrapStreamReaderIfNeeded(t *testing.T) {
	testPayload := []byte("data: {\"test\":true}\n\n")
	var compressedBuffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuffer)
	_, _ = gzipWriter.Write(testPayload)
	_ = gzipWriter.Close()

	httpResponse := &http.Response{
		Header: http.Header{
			"Content-Encoding": []string{"gzip"},
		},
		Body: ioNopCloserWithReader(&compressedBuffer),
	}

	wrappedBody, wasWrapped, wrapErr := WrapStreamReaderIfNeeded(httpResponse)
	require.NoError(t, wrapErr)
	assert.True(t, wasWrapped)
	defer wrappedBody.Close()

	readBytes, readErr := io.ReadAll(wrappedBody)
	require.NoError(t, readErr)
	assert.Equal(t, testPayload, readBytes)
}

func ioNopCloserWithReader(reader *bytes.Buffer) io.ReadCloser {
	return io.NopCloser(reader)
}
