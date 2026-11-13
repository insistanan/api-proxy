package proxycore

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadRequestBody_Plaintext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testPayload := []byte(`{"model":"gpt-4o","stream":true}`)

	recordingContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	recordingContext.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(testPayload))

	bodyBytes, readErr := ReadRequestBody(recordingContext, 1024*1024)
	require.NoError(t, readErr)
	assert.Equal(t, testPayload, bodyBytes)
}

func TestReadRequestBody_ZstdCompressed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testPayload := []byte(`{"model":"gpt-5-codex","stream":false,"prompt":"hello from codex desktop"}`)

	var compressedBuffer bytes.Buffer
	zstdWriter, encoderErr := zstd.NewWriter(&compressedBuffer)
	require.NoError(t, encoderErr)
	_, writeErr := zstdWriter.Write(testPayload)
	require.NoError(t, writeErr)
	require.NoError(t, zstdWriter.Close())

	responseRecorder := httptest.NewRecorder()
	recordingContext, _ := gin.CreateTestContext(responseRecorder)
	recordingContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(compressedBuffer.Bytes()))
	recordingContext.Request.Header.Set("Content-Encoding", "zstd")
	recordingContext.Request.Header.Set("Content-Length", "999")

	bodyBytes, readErr := ReadRequestBody(recordingContext, 1024*1024)
	require.NoError(t, readErr)
	assert.Equal(t, testPayload, bodyBytes)
	// 验证失真的实体头已从请求头中剥除
	assert.Empty(t, recordingContext.Request.Header.Get("Content-Encoding"))
	assert.Empty(t, recordingContext.Request.Header.Get("Content-Length"))
	assert.Equal(t, int64(len(testPayload)), recordingContext.Request.ContentLength)

	// 验证请求体已恢复，后续读取仍然可以读到明文
	restoredBytes, err := io.ReadAll(recordingContext.Request.Body)
	require.NoError(t, err)
	assert.Equal(t, testPayload, restoredBytes)
}

func TestReadRequestBody_GzipCompressed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testPayload := []byte(`{"model":"claude-3-7-sonnet","messages":[{"role":"user","content":"test"}]}`)

	var compressedBuffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuffer)
	_, writeErr := gzipWriter.Write(testPayload)
	require.NoError(t, writeErr)
	require.NoError(t, gzipWriter.Close())

	responseRecorder := httptest.NewRecorder()
	recordingContext, _ := gin.CreateTestContext(responseRecorder)
	recordingContext.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(compressedBuffer.Bytes()))
	recordingContext.Request.Header.Set("Content-Encoding", "gzip")

	bodyBytes, readErr := ReadRequestBody(recordingContext, 1024*1024)
	require.NoError(t, readErr)
	assert.Equal(t, testPayload, bodyBytes)
	assert.Empty(t, recordingContext.Request.Header.Get("Content-Encoding"))
}

func TestReadRequestBody_UnsupportedContentEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testPayload := []byte(`{"model":"test"}`)

	responseRecorder := httptest.NewRecorder()
	recordingContext, _ := gin.CreateTestContext(responseRecorder)
	recordingContext.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(testPayload))
	recordingContext.Request.Header.Set("Content-Encoding", "lz4")

	bodyBytes, readErr := ReadRequestBody(recordingContext, 1024*1024)
	require.Error(t, readErr)
	assert.Nil(t, bodyBytes)
	assert.Equal(t, http.StatusBadRequest, responseRecorder.Code)
}

func TestReadRequestBody_DecompressionBomb(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 2MB 的全零测试数据压缩后只有几百字节
	uncompressedBombSize := 2 * 1024 * 1024
	bombPayload := make([]byte, uncompressedBombSize)
	var compressedBuffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuffer)
	_, _ = gzipWriter.Write(bombPayload)
	_ = gzipWriter.Close()

	responseRecorder := httptest.NewRecorder()
	recordingContext, _ := gin.CreateTestContext(responseRecorder)
	recordingContext.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(compressedBuffer.Bytes()))
	recordingContext.Request.Header.Set("Content-Encoding", "gzip")

	// 限制解压上限为 1MB
	bodyBytes, readErr := ReadRequestBody(recordingContext, 1024*1024)
	require.Error(t, readErr)
	assert.Nil(t, bodyBytes)
	assert.Equal(t, http.StatusRequestEntityTooLarge, responseRecorder.Code)
}

func TestShouldForceIdentityEncoding(t *testing.T) {
	// 1. isStream = true 必须强制 identity
	streamReq, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", nil)
	assert.True(t, ShouldForceIdentityEncoding(streamReq, true))

	// 2. Gemini 流式 URL (streamGenerateContent) 必须强制 identity
	geminiReq, _ := http.NewRequest(http.MethodPost, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse", nil)
	assert.True(t, ShouldForceIdentityEncoding(geminiReq, false))

	// 3. Accept 包含 text/event-stream 必须强制 identity
	sseAcceptReq, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", nil)
	sseAcceptReq.Header.Set("Accept", "text/event-stream")
	assert.True(t, ShouldForceIdentityEncoding(sseAcceptReq, false))

	// 4. 普通非流式请求不强制 identity
	normalReq, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", nil)
	normalReq.Header.Set("Accept", "application/json")
	assert.False(t, ShouldForceIdentityEncoding(normalReq, false))
}
