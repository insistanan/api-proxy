package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 测试用内置清单种子：模拟 `codex debug models` 的输出条目（含指令模板）。
const testBuiltinEntrySol = `{"slug":"gpt-5.6-sol","display_name":"GPT-5.6-Sol","context_window":400000,"max_context_window":400000,"default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low","description":"l"},{"effort":"high","description":"h"},{"effort":"max","description":"m"}],"visibility":"list","priority":1,"supports_search_tool":true,"model_messages":{"instructions_template":"You are Codex."}}`

const testBuiltinEntry55 = `{"slug":"gpt-5.5","display_name":"GPT-5.5","context_window":400000,"max_context_window":400000,"default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low","description":"l"},{"effort":"high","description":"h"}],"visibility":"list","priority":5,"model_messages":{"instructions_template":"You are GPT-5.5."}}`

// testCodexBuiltinCatalog 直接构造内置清单（单测用，不经过 fetch）。
func testCodexBuiltinCatalog(t *testing.T, rawEntries ...string) *codexBuiltinCatalog {
	t.Helper()
	catalog := &codexBuiltinCatalog{index: map[string]json.RawMessage{}}
	for _, raw := range rawEntries {
		var meta codexModelEntryMeta
		if err := json.Unmarshal([]byte(raw), &meta); err != nil {
			t.Fatalf("内置清单种子条目无效: %v", err)
		}
		catalog.entries = append(catalog.entries, json.RawMessage(raw))
		catalog.index[meta.Slug] = json.RawMessage(raw)
	}
	return catalog
}

// stubCodexBuiltinCatalog 替换 fetch 变量并重置缓存，供 handler 层测试注入；结束后还原。
func stubCodexBuiltinCatalog(t *testing.T, rawEntries ...string) {
	t.Helper()
	original := fetchCodexBuiltinCatalog
	fetchCodexBuiltinCatalog = func(ctx context.Context) (*codexBuiltinCatalog, error) {
		if len(rawEntries) == 0 {
			return nil, fmt.Errorf("codex 命令不可用（测试桩）")
		}
		return testCodexBuiltinCatalog(t, rawEntries...), nil
	}
	resetCodexBuiltinCacheForTest()
	t.Cleanup(func() {
		fetchCodexBuiltinCatalog = original
		resetCodexBuiltinCacheForTest()
	})
}

func resetCodexBuiltinCacheForTest() {
	codexBuiltinMu.Lock()
	codexBuiltinCache = nil
	codexBuiltinAt = time.Time{}
	codexBuiltinMu.Unlock()
}

func performCodexModelRequest(t *testing.T, handler gin.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	var payload []byte
	switch typed := body.(type) {
	case nil:
		payload = nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			t.Fatalf("构造请求体失败: %v", err)
		}
		payload = encoded
	}
	context.Request = httptest.NewRequest(method, target, bytes.NewReader(payload))
	context.Request.Header.Set("Content-Type", "application/json")
	handler(context)
	return recorder
}

func TestSaveCodexModelCatalogHandler(t *testing.T) {
	handler := SaveCodexModelCatalog()

	t.Run("upsert 新模型并写入 models.json", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol)
		recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action":        "upsert",
			"slug":          "my-model",
			"displayName":   "My Model",
			"contextWindow": 128000,
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		raw, err := os.ReadFile(filepath.Join(home, "models.json"))
		if err != nil {
			t.Fatalf("models.json 未写入: %v", err)
		}
		var doc struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("models.json 不是合法 JSON: %v", err)
		}
		if len(doc.Models) != 2 {
			t.Fatalf("应包含内置条目与新模型: %d", len(doc.Models))
		}
		bySlug := map[string]map[string]any{}
		for _, model := range doc.Models {
			bySlug[model["slug"].(string)] = model
		}
		if _, ok := bySlug["gpt-5.6-sol"]; !ok {
			t.Fatalf("内置条目未并入: %v", bySlug)
		}
		mine := bySlug["my-model"]
		if mine["display_name"] != "My Model" {
			t.Fatalf("slug 错误: %v", mine)
		}
		messages := mine["model_messages"].(map[string]any)
		if messages["instructions_template"] != "You are Codex." {
			t.Fatalf("新条目应从内置清单复制指令: %v", messages)
		}
	})

	t.Run("setDefault 写入根级模型字段且保留 provider 配置", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol)
		configSeed := "# codex config\nmodel_provider = \"ggb\"\n\n[model_providers.ggb]\nname = \"ggb\"\nbase_url = \"http://localhost:9996/v1\"\n"
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configSeed), 0600); err != nil {
			t.Fatalf("写入种子配置失败: %v", err)
		}
		upsertRecorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action":                "upsert",
			"slug":                  "my-ds",
			"supportsImage":         true,
			"contextWindow":         1048576,
			"defaultReasoningLevel": "high",
		})
		if upsertRecorder.Code != http.StatusOK {
			t.Fatalf("upsert 状态码错误: %d %s", upsertRecorder.Code, upsertRecorder.Body.String())
		}
		recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action":          "setDefault",
			"slug":            "my-ds",
			"reasoningEffort": "high",
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("setDefault 状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatalf("读取 config.toml 失败: %v", err)
		}
		updated := string(raw)
		for _, want := range []string{
			"model = 'my-ds'",
			"model_catalog_json = '" + filepath.Join(home, "models.json") + "'",
			"model_reasoning_effort = 'high'",
			"# codex config",
			"model_provider = \"ggb\"",
			"base_url = \"http://localhost:9996/v1\"",
		} {
			if !strings.Contains(updated, want) {
				t.Fatalf("config.toml 缺少 %q:\n%s", want, updated)
			}
		}
	})

	t.Run("setDefault 内置模型且目录非空时并入内置条目", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol)
		// seed 一个只有自定义条目、缺内置条目的旧版 catalog。
		seed := `{"models":[{"slug":"my-ds","display_name":"my-ds","input_modalities":["text"],"context_window":64000,"max_context_window":64000,"supported_reasoning_levels":[{"effort":"low","description":""},{"effort":"high","description":""}],"model_messages":{"instructions_template":"Custom."}}]}`
		if err := os.WriteFile(filepath.Join(home, "models.json"), []byte(seed), 0600); err != nil {
			t.Fatalf("写入种子文件失败: %v", err)
		}
		recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action":          "setDefault",
			"slug":            "gpt-5.6-sol",
			"reasoningEffort": "high",
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("setDefault 状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		raw, err := os.ReadFile(filepath.Join(home, "models.json"))
		if err != nil {
			t.Fatalf("读取 models.json 失败: %v", err)
		}
		var doc struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("models.json 不是合法 JSON: %v", err)
		}
		slugs := map[string]bool{}
		for _, model := range doc.Models {
			slugs[model["slug"].(string)] = true
		}
		if !slugs["gpt-5.6-sol"] || !slugs["my-ds"] {
			t.Fatalf("内置条目应并入且保留自定义条目: %v", slugs)
		}
		configRaw, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatalf("读取 config.toml 失败: %v", err)
		}
		if !strings.Contains(string(configRaw), "model = 'gpt-5.6-sol'") {
			t.Fatalf("model 键未更新:\n%s", configRaw)
		}
		if !strings.Contains(string(configRaw), "model_catalog_json = '"+filepath.Join(home, "models.json")+"'") {
			t.Fatalf("非空目录应写 model_catalog_json:\n%s", configRaw)
		}
	})

	t.Run("setDefault 内置模型且目录为空时不启用 catalog", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol)
		recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action": "setDefault",
			"slug":   "gpt-5.6-sol",
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("setDefault 状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		if _, err := os.Stat(filepath.Join(home, "models.json")); !os.IsNotExist(err) {
			t.Fatalf("空目录不应创建 models.json: %v", err)
		}
		configRaw, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatalf("读取 config.toml 失败: %v", err)
		}
		updated := string(configRaw)
		if !strings.Contains(updated, "model = 'gpt-5.6-sol'") {
			t.Fatalf("model 键未写入:\n%s", updated)
		}
		if strings.Contains(updated, "model_catalog_json") {
			t.Fatalf("不应写 model_catalog_json:\n%s", updated)
		}
	})

	t.Run("clearReasoningEffort 移除推理档位", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol)
		if err := performCodexUpsertAndDefault(t, handler, home); err != nil {
			t.Fatalf("准备阶段失败: %v", err)
		}
		recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action":               "setDefault",
			"slug":                 "my-ds",
			"clearReasoningEffort": true,
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatalf("读取 config.toml 失败: %v", err)
		}
		if strings.Contains(string(raw), "model_reasoning_effort") {
			t.Fatalf("推理档位应被移除:\n%s", raw)
		}
		if !strings.Contains(string(raw), "model = 'my-ds'") {
			t.Fatalf("model 字段应保留:\n%s", raw)
		}
	})

	t.Run("delete 移除条目且删除不存在的模型报错", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol)
		recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action":        "upsert",
			"slug":          "my-model",
			"contextWindow": 128000,
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("upsert 状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		recorder = performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action": "delete",
			"slug":   "my-model",
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("delete 状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		raw, err := os.ReadFile(filepath.Join(home, "models.json"))
		if err != nil {
			t.Fatalf("读取 models.json 失败: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("models.json 不是合法 JSON: %v", err)
		}
		// 剩余的内置条目不会被 delete 清掉。
		if models := doc["models"].([]any); len(models) != 1 {
			t.Fatalf("内置条目应保留: %v", models)
		}

		recorder = performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action": "delete",
			"slug":   "not-exist",
		})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("删除不存在的模型应返回 400: %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("delete 删空目录并清除默认模型时回退 config 键", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol)
		// seed 只有自定义条目的 catalog 与指向它的 config，模拟旧版功能留下的状态。
		seed := `{"models":[{"slug":"my-ds","display_name":"my-ds","input_modalities":["text"],"context_window":64000,"max_context_window":64000,"supported_reasoning_levels":[{"effort":"low","description":""},{"effort":"high","description":""}],"model_messages":{"instructions_template":"Custom."}}]}`
		if err := os.WriteFile(filepath.Join(home, "models.json"), []byte(seed), 0600); err != nil {
			t.Fatalf("写入种子文件失败: %v", err)
		}
		configSeed := "model = 'my-ds'\nmodel_catalog_json = '" + filepath.Join(home, "models.json") + "'\nmodel_reasoning_effort = 'high'\n"
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configSeed), 0600); err != nil {
			t.Fatalf("写入种子配置失败: %v", err)
		}
		recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action": "delete",
			"slug":   "my-ds",
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("delete 状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatalf("读取 config.toml 失败: %v", err)
		}
		updated := string(raw)
		for _, key := range []string{"model_catalog_json", "model_reasoning_effort"} {
			if strings.Contains(updated, key) {
				t.Fatalf("删除唯一自定义条目后 %q 应被清除:\n%s", key, updated)
			}
		}
		if strings.Contains(updated, "model = ") {
			t.Fatalf("被删模型是默认模型时 model 键应被清除:\n%s", updated)
		}
		modelsRaw, err := os.ReadFile(filepath.Join(home, "models.json"))
		if err != nil {
			t.Fatalf("读取 models.json 失败: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(modelsRaw, &doc); err != nil {
			t.Fatalf("models.json 不是合法 JSON: %v", err)
		}
		if models := doc["models"].([]any); len(models) != 0 {
			t.Fatalf("条目应已删空: %v", models)
		}
	})

	t.Run("获取内置清单失败时 upsert 报错", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t) // 无种子条目 = fetch 失败
		recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
			"action":        "upsert",
			"template":      "generic",
			"slug":          "my-model",
			"contextWindow": 128000,
		})
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("内置清单不可用时 upsert 应返回 500: %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("非法请求被拒绝", func(t *testing.T) {
		t.Setenv("CODEX_HOME", t.TempDir())
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol)
		tests := []struct {
			name string
			body map[string]any
		}{
			{name: "未知 action", body: map[string]any{"action": "hack", "slug": "x"}},
			{name: "slug 为空", body: map[string]any{"action": "delete", "slug": "  "}},
			{name: "slug 含非法字符", body: map[string]any{"action": "delete", "slug": "bad slug"}},
			{name: "上下文窗口越界", body: map[string]any{"action": "upsert", "slug": "x", "contextWindow": 0}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", tt.body)
				if recorder.Code != http.StatusBadRequest {
					t.Fatalf("应返回 400: %d %s", recorder.Code, recorder.Body.String())
				}
			})
		}
	})
}

func performCodexUpsertAndDefault(t *testing.T, handler gin.HandlerFunc, home string) error {
	t.Helper()
	recorder := performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
		"action":        "upsert",
		"slug":          "my-ds",
		"contextWindow": 128000,
	})
	if recorder.Code != http.StatusOK {
		return fmt.Errorf("upsert 失败: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = performCodexModelRequest(t, handler, http.MethodPut, "/settings/codex/model-catalog", map[string]any{
		"action":          "setDefault",
		"slug":            "my-ds",
		"reasoningEffort": "high",
	})
	if recorder.Code != http.StatusOK {
		return fmt.Errorf("setDefault 失败: %d %s", recorder.Code, recorder.Body.String())
	}
	return nil
}

func TestGetCodexSettingsWithCatalog(t *testing.T) {
	handler := GetCodexSettings()

	t.Run("返回模型目录、内置清单与 builtin 标记", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol, testBuiltinEntry55)
		modelsPath := filepath.Join(home, "models.json")
		seed := `{"models":[{"slug":"my-ds","display_name":"My DS","input_modalities":["text","image"],"context_window":1048576,"max_context_window":1048576,"default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low","description":"a"},{"effort":"high","description":"b"}]},{"slug":"gpt-5.5","display_name":"GPT-5.5","input_modalities":["text"],"context_window":400000,"max_context_window":400000,"default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low","description":"a"}]}]}`
		if err := os.WriteFile(modelsPath, []byte(seed), 0600); err != nil {
			t.Fatalf("写入种子文件失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = 'my-ds'\nmodel_reasoning_effort = \"high\"\n"), 0600); err != nil {
			t.Fatalf("写入种子配置失败: %v", err)
		}

		recorder := performCodexModelRequest(t, handler, http.MethodGet, "/settings/codex", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		var response codexSettingsResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if !response.ModelsExists || response.Model != "my-ds" || response.ModelReasoningEffort != "high" {
			t.Fatalf("根级模型字段错误: %+v", response)
		}
		if len(response.CatalogModels) != 2 {
			t.Fatalf("模型条目数量错误: %d", len(response.CatalogModels))
		}
		model := response.CatalogModels[0]
		if model.Slug != "gpt-5.5" || !model.Builtin {
			t.Fatalf("内置复制品应带 builtin 标记: %+v", model)
		}
		custom := response.CatalogModels[1]
		if custom.Slug != "my-ds" || custom.Builtin || !custom.SupportsImage || custom.ContextWindow != 1048576 {
			t.Fatalf("自定义条目视图错误: %+v", custom)
		}
		if len(custom.ReasoningLevels) != 2 || custom.ReasoningLevels[0] != "low" {
			t.Fatalf("推理档位视图错误: %+v", custom.ReasoningLevels)
		}
		if len(response.BuiltinModels) != 2 || response.BuiltinError != "" {
			t.Fatalf("内置清单视图错误: %d %q", len(response.BuiltinModels), response.BuiltinError)
		}
		if !response.BuiltinModels[0].Builtin || response.BuiltinModels[0].Visibility != "list" {
			t.Fatalf("内置清单视图字段错误: %+v", response.BuiltinModels[0])
		}
	})

	t.Run("models.json 损坏时通过 modelsError 透出", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t, testBuiltinEntrySol)
		if err := os.WriteFile(filepath.Join(home, "models.json"), []byte("{invalid"), 0600); err != nil {
			t.Fatalf("写入损坏文件失败: %v", err)
		}
		recorder := performCodexModelRequest(t, handler, http.MethodGet, "/settings/codex", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("损坏的 models.json 不应阻断设置页: %d %s", recorder.Code, recorder.Body.String())
		}
		var response codexSettingsResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if response.ModelsError == "" {
			t.Fatal("应包含 modelsError")
		}
		if len(response.CatalogModels) != 0 {
			t.Fatalf("损坏时应返回空模型列表: %v", response.CatalogModels)
		}
	})

	t.Run("内置清单获取失败时通过 builtinError 透出且不阻断", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		stubCodexBuiltinCatalog(t) // 无种子条目 = fetch 失败
		if err := os.WriteFile(filepath.Join(home, "models.json"), []byte(`{"models":[{"slug":"my-ds","display_name":"My DS","input_modalities":["text"],"context_window":1048576,"max_context_window":1048576,"supported_reasoning_levels":[]}],"models_note":"x"}`), 0600); err != nil {
			t.Fatalf("写入种子文件失败: %v", err)
		}
		recorder := performCodexModelRequest(t, handler, http.MethodGet, "/settings/codex", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("获取失败不应阻断设置页: %d %s", recorder.Code, recorder.Body.String())
		}
		var response codexSettingsResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if response.BuiltinError == "" {
			t.Fatal("应包含 builtinError")
		}
		if len(response.BuiltinModels) != 0 {
			t.Fatalf("获取失败时应返回空内置清单: %v", response.BuiltinModels)
		}
		if len(response.CatalogModels) != 1 || response.CatalogModels[0].Builtin {
			t.Fatalf("catalog 条目应正常返回且 builtin 标记为 false: %+v", response.CatalogModels)
		}
	})
}
