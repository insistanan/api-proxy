package proxycore

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/storage/logger"
)

func TestRequestCaptureSnapshotSurvivesConfigurationChanges(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	env := &config.EnvConfig{EnableRequestLogs: true, EnableResponseLogs: true, LogCaptureMode: "bounded", LogBodyLimitKB: 32}
	initial := bindCapturePolicy(c, env)
	env.EnableRequestLogs, env.LogCaptureMode, env.LogBodyLimitKB = false, "metadata", 16
	require.Equal(t, initial, capturePolicy(c.Request.Context(), env))
	req := AttachRequestLogID(c, httptest.NewRequest("POST", "https://example.invalid/v1/responses", nil))
	// 绑定请求 ID 后，独立构造的上游请求必须同时继承采集快照。
	BindRequestLogID(c)
	req = AttachRequestLogID(c, req)
	upstream, ok := logger.CapturePolicyFromContext(req.Context())
	require.True(t, ok)
	require.Equal(t, initial, upstream)
	copy := RequestLogEnv(c, env)
	require.True(t, copy.EnableRequestLogs)
	require.True(t, copy.EnableResponseLogs)
	require.Equal(t, "bounded", copy.LogCaptureMode)
	require.Equal(t, 32, copy.LogBodyLimitKB)
}

func TestMetadataPolicyDisablesLegacyBodyLogging(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	env := &config.EnvConfig{EnableRequestLogs: true, EnableResponseLogs: true, LogCaptureMode: "metadata", LogBodyLimitKB: 16}
	bindCapturePolicy(c, env)
	copy := RequestLogEnv(c, env)
	require.False(t, copy.EnableRequestLogs)
	require.False(t, copy.EnableResponseLogs)
	require.True(t, env.EnableRequestLogs)
	require.True(t, env.EnableResponseLogs)
}

func TestDebugRuntimeLogDoesNotDuplicateBodyOrHeaders(t *testing.T) {
	store := setupLifecycleLogStore(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set("X-Debug-Test", "private-header-marker")
	env := &config.EnvConfig{EnableRequestLogs: true, EnableResponseLogs: true, LogLevel: "debug", LogCaptureMode: "bounded", LogBodyLimitKB: 32}
	LogOriginalRequest(c, []byte(`{"messages":[{"role":"user","content":"private-body-marker"}]}`), env, "Messages")
	require.NoError(t, store.Flush())
	page, err := store.ListRuntimeLogs(context.Background(), logger.RuntimeLogOptions{Limit: 100})
	require.NoError(t, err)
	for _, entry := range page.Logs {
		require.NotContains(t, entry.Message, "private-body-marker")
		require.NotContains(t, entry.Message, "private-header-marker")
	}
	traffic, err := store.QueryTrafficByRequestID(context.Background(), RequestLogID(c))
	require.NoError(t, err)
	require.Contains(t, traffic[0].Body, "private-body-marker")
	require.Contains(t, traffic[0].HeadersJSON, "private-header-marker")
}

func TestDisabledBodyCollectionStillExplainsMissingBody(t *testing.T) {
	store := setupLifecycleLogStore(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{}`))
	LogOriginalRequest(c, []byte(`{"messages":[{"role":"user","content":"must-not-store"}]}`), &config.EnvConfig{}, "Messages")
	require.NoError(t, store.Flush())
	traffic, err := store.QueryTrafficByRequestID(context.Background(), RequestLogID(c))
	require.NoError(t, err)
	require.Len(t, traffic, 1)
	require.Empty(t, traffic[0].Body)
	require.Equal(t, "disabled", traffic[0].BodyState)
}
