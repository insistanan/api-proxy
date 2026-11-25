package visionlayer

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"github.com/stretchr/testify/require"
)

func TestVisionResponse_ContentEncodingErrorsRemainVisible(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write([]byte(visionSuccessBody))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	corrupted := bytes.Clone(compressed.Bytes())
	corrupted[len(corrupted)-8] ^= 0xff // 保持压缩数据完整，仅破坏校验和。

	for _, testCase := range []struct {
		name     string
		encoding string
		body     []byte
		wantErr  error
	}{
		{name: "未压缩", body: []byte(visionSuccessBody)},
		{name: "正常 gzip", encoding: "gzip", body: compressed.Bytes()},
		{name: "损坏的 gzip 头", encoding: "gzip", body: []byte(visionSuccessBody), wantErr: gzip.ErrHeader},
		{name: "错误校验和", encoding: "gzip", body: corrupted, wantErr: gzip.ErrChecksum},
		{name: "不支持的编码不能按明文继续", encoding: "unknown", body: []byte(visionSuccessBody), wantErr: codec.ErrUnsupportedContentEncoding},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
			response.Header.Set("Content-Encoding", testCase.encoding)
			result := processVisionResponse(&providers.ClaudeProvider{}, response, testCase.body, []visionImage{{id: "image_1"}})
			if testCase.wantErr != nil {
				require.ErrorIs(t, result.err, testCase.wantErr)
				require.ErrorContains(t, result.err, "解压图片理解响应失败")
				require.Equal(t, "decode_response", result.errorType)
				require.Empty(t, result.descriptions)
				require.True(t, result.retryable)
				return
			}
			require.NoError(t, result.err)
			require.Equal(t, "图片中有一只猫", result.descriptions["image_1"])
			require.Equal(t, visionHealthSuccess, result.health)
		})
	}
}

func TestVisionResponse_HTTP200ErrorsPreserveReasonAndPolicy(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		body            string
		wantReason      string
		retryable       bool
		health          visionHealthOutcome
		endpointFailure bool
		rateLimited     bool
	}{
		{name: "上游服务异常", body: `{"error":{"type":"overloaded_error","message":"service overloaded"}}`, wantReason: "overloaded_error", retryable: true, health: visionHealthFailure, endpointFailure: true},
		{name: "限流", body: `{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`, wantReason: "slow down", retryable: true, health: visionHealthNeutral, rateLimited: true},
		{name: "数字错误码与状态", body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"busy"}}`, wantReason: "RESOURCE_EXHAUSTED", retryable: true, health: visionHealthNeutral, rateLimited: true},
		{name: "内容拒绝", body: `{"error":{"code":"content_policy_violation","message":"policy rejected"}}`, wantReason: "policy rejected", health: visionHealthNeutral},
		{name: "按 type 拒绝", body: `{"error":{"type":"invalid_request_error","message":"invalid input"}}`, wantReason: "invalid input", health: visionHealthNeutral},
		{name: "凭据失效", body: `{"error":{"code":"invalid_api_key","message":"invalid key"}}`, wantReason: "invalid key", retryable: true, health: visionHealthFailure},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := &http.Response{StatusCode: http.StatusOK}
			result := processVisionResponse(&providers.ClaudeProvider{}, response, []byte(testCase.body), []visionImage{{id: "image_1"}})
			require.ErrorContains(t, result.err, testCase.wantReason)
			require.NotEqual(t, "empty_response", result.errorType)
			require.Equal(t, testCase.retryable, result.retryable)
			require.Equal(t, testCase.health, result.health)
			require.Equal(t, testCase.endpointFailure, result.endpointFailure)
			require.Equal(t, testCase.rateLimited, result.rateLimited)
			require.Empty(t, result.descriptions)
			require.Equal(t, http.StatusOK, response.StatusCode, "不能伪造实际收到的 HTTP 状态")
		})
	}
}

func TestVisionResponse_NullErrorDoesNotRejectSuccess(t *testing.T) {
	body := strings.Replace(visionSuccessBody, `"stop_reason":"end_turn"`, `"error":null,"stop_reason":"end_turn"`, 1)
	result := processVisionResponse(&providers.ClaudeProvider{}, &http.Response{StatusCode: http.StatusOK}, []byte(body), []visionImage{{id: "image_1"}})
	require.NoError(t, result.err)
	require.Equal(t, "图片中有一只猫", result.descriptions["image_1"])
}

func TestVisionResponse_RefusalAndTruncationRemainDistinct(t *testing.T) {
	for _, testCase := range []struct {
		reason string
		health visionHealthOutcome
	}{
		{reason: "refusal", health: visionHealthNeutral},
		{reason: "max_tokens", health: visionHealthFailure},
	} {
		t.Run(testCase.reason, func(t *testing.T) {
			body := strings.Replace(visionSuccessBody, `"end_turn"`, `"`+testCase.reason+`"`, 1)
			result := processVisionResponse(&providers.ClaudeProvider{}, &http.Response{StatusCode: http.StatusOK}, []byte(body), []visionImage{{id: "image_1"}})
			require.ErrorContains(t, result.err, testCase.reason)
			require.Equal(t, testCase.health, result.health)
			require.False(t, result.retryable)
			require.False(t, result.endpointFailure)
			require.Empty(t, result.descriptions, "拒绝或截断不能落入可缓存的成功结果")
			require.NotNil(t, result.usage)
			require.Equal(t, 1, result.usage.OutputTokens, "失败日志仍保留上游已报告的用量")
		})
	}
}
