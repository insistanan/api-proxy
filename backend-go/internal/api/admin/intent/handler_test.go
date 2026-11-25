package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/routing/intent"
	"github.com/insistanan/api-proxy/internal/dialogue/routing/semantic"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
)

func TestIntentAndDatasetEndpointsShareVectorReadinessError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	cm, err := config.NewConfigManager(filepath.Join(dir, "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cm.Close()) })
	local, err := intent.NewLocal(filepath.Join(dir, "intent-embedding"))
	require.NoError(t, err)
	service, err := intent.New(cm, nil, filepath.Join(dir, "intent.db"), local)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	dataset, err := service.Datasets.Save(context.Background(), semantic.Dataset{ID: "samples", Name: "样本", Kind: "intent", Samples: []semantic.Sample{{ID: "a", Label: "writing", Text: "润色邮件", Split: "index", Family: "mail", Approved: true, Source: "test"}}})
	require.NoError(t, err)
	router := gin.New()
	RegisterIntentAPI(router.Group("/api"), service)
	RegisterDatasetAPI(router.Group("/api"), service.Datasets)
	cfg := config.DefaultIntentRouting()
	cfg.Strategy, cfg.EmbeddingSource, cfg.EmbeddingModel = "semantic", "local", "vector"
	cfg.Enabled = true
	cfg.DatasetIDs = []string{dataset.ID}
	cfg.Routes = []config.IntentRoute{{ID: "writing", PoolID: "default", Label: "writing"}}
	before := cm.GetIntentRouting("messages")
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/intent/messages", cfg},
		{http.MethodPost, "/api/intent/messages/index", cfg},
		{http.MethodPost, "/api/intent/messages/preview", gin.H{"config": cfg, "text": "润色邮件"}},
		{http.MethodPost, "/api/datasets/samples/build", gin.H{"revision": dataset.Revision, "profile": config.SemanticEmbeddingConfig{Source: "local", Model: "vector", TimeoutMS: 1000}}},
	} {
		body, err := json.Marshal(tc.body)
		require.NoError(t, err)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, bytes.NewReader(body)))
		require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
		var result map[string]string
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.Equal(t, "VECTOR_MODEL_REQUIRED", result["code"])
		require.NotEmpty(t, result["message"])
		require.Equal(t, local.Status().InstallPath, result["installPath"])
		require.Equal(t, intent.DefaultPresetsURL, result["presetsUrl"])
	}
	require.Equal(t, before, cm.GetIntentRouting("messages"), "阻断保存不能改动已有配置")
	// 关闭配置无需等待向量模型就绪，仍应允许保存。
	cfg.Enabled = false
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/intent/messages", bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.False(t, cm.GetIntentRouting("messages").Enabled)
	cfg.Strategy, cfg.FailurePolicy = "regex", "error"
	body, err = json.Marshal(cfg)
	require.NoError(t, err)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/intent/messages", bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "error", cm.GetIntentRouting("messages").FailurePolicy, "旧配置的显式错误策略不得被新默认迁移")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/datasets/builtin", nil))
	var builtins []semantic.Dataset
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &builtins))
	require.Equal(t, semantic.RecommendedIntentDatasetID, builtins[0].ID)
	require.Greater(t, len(builtins[0].Samples), 96, "保留基础句并补充边界与拒识样本")
	require.Less(t, len(builtins[0].Samples), 192, "移除礼貌前缀重复样本")
	require.Len(t, builtins[1].Samples, 300)
	stored, err := service.Datasets.Get(dataset.ID)
	require.NoError(t, err)
	require.Equal(t, dataset, stored, "更新内置样本不修改已导入数据")
}
