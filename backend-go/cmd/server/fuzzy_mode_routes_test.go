package main

import (
	"net/http"
	"testing"

	"github.com/insistanan/api-proxy/internal/handlers/admin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/middleware"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestFuzzyModeAdminProtocolIsolation(t *testing.T) {
	r, app := newChannelAdminTestRouter(t)
	group := r.Group("/api", middleware.AdminAuthMiddleware(&config.EnvConfig{ProxyAccessKey: "test-admin"}))
	group.GET("/settings/fuzzy-mode", admin.GetFuzzyMode(app.cfgManager))
	group.PUT("/settings/fuzzy-mode", admin.SetFuzzyMode(app.cfgManager))
	require.NoError(t, app.cfgManager.SetFuzzyModeEnabled(true))
	channelAdminRequest(t, r, http.MethodPut, "/api/settings/fuzzy-mode", `{"protocol":"messages","enabled":false}`, http.StatusOK)
	state := channelAdminRequest(t, r, http.MethodGet, "/api/settings/fuzzy-mode", "", http.StatusOK).Body.String()
	require.False(t, gjson.Get(state, "fuzzyModeByProtocol.messages").Bool())
	for _, protocol := range []string{"responses", "chat", "gemini", "images", "embeddings", "decisions"} {
		require.True(t, gjson.Get(state, "fuzzyModeByProtocol."+protocol).Bool())
	}
	for _, body := range []string{`{"protocol":"messages"}`, `{"protocol":"typo","enabled":true}`, `{"protocol":"","enabled":false}`} {
		channelAdminRequest(t, r, http.MethodPut, "/api/settings/fuzzy-mode", body, http.StatusBadRequest)
	}
	require.False(t, app.cfgManager.GetFuzzyModeEnabledForProtocol("messages"))
	// 旧客户端未传协议时仍保持明确的全局设置语义。
	channelAdminRequest(t, r, http.MethodPut, "/api/settings/fuzzy-mode", `{"enabled":true}`, http.StatusOK)
	require.True(t, app.cfgManager.GetFuzzyModeEnabledForProtocol("messages"))
}
