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

func TestQuoteTOMLString(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "普通值用字面量字符串", value: "deepseek-flash", want: "'deepseek-flash'"},
		{name: "Windows 路径无需转义", value: `C:\Users\insis\.codex\models.json`, want: `'C:\Users\insis\.codex\models.json'`},
		{name: "含单引号退回基本字符串", value: `O'Brien`, want: `"O'Brien"`},
		{name: "含双引号与反斜杠仍用字面量", value: `a"b\c`, want: `'a"b\c'`},
		{name: "含制表符转义", value: "a\tb", want: `"a\tb"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quoteTOMLString(tt.value); got != tt.want {
				t.Fatalf("quoteTOMLString(%q) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}

func TestMergeCodexRootKey(t *testing.T) {
	value := "deepseek-flash"
	empty := ""
	tests := []struct {
		name    string
		input   string
		key     string
		value   *string
		want    string
		wantErr bool
	}{
		{
			name:  "空文件新增根级键",
			input: "",
			key:   "model",
			value: &value,
			want:  "model = 'deepseek-flash'\n",
		},
		{
			name:  "已有根级键时替换",
			input: "model = 'old'\nmodel_provider = \"ggb\"\n",
			key:   "model",
			value: &value,
			want:  "model = 'deepseek-flash'\nmodel_provider = \"ggb\"\n",
		},
		{
			name:  "插入到首个表头之前",
			input: "model_provider = \"ggb\"\n\n[model_providers.ggb]\nname = \"ggb\"\n",
			key:   "model",
			value: &value,
			want:  "model_provider = \"ggb\"\nmodel = 'deepseek-flash'\n\n[model_providers.ggb]\nname = \"ggb\"\n",
		},
		{
			name:  "插入位置跳过紧贴表头的注释",
			input: "# providers\n[model_providers.ggb]\nname = \"ggb\"\n",
			key:   "model",
			value: &value,
			want:  "model = 'deepseek-flash'\n# providers\n[model_providers.ggb]\nname = \"ggb\"\n",
		},
		{
			name:  "删除已存在的根级键",
			input: "model = 'old'\nmodel_provider = \"ggb\"\n",
			key:   "model",
			value: nil,
			want:  "model_provider = \"ggb\"\n",
		},
		{
			name:  "删除不存在的键为空操作",
			input: "model_provider = \"ggb\"\n",
			key:   "model",
			value: nil,
			want:  "model_provider = \"ggb\"\n",
		},
		{
			name:  "表内同名字段不被误改",
			input: "[model_providers.ggb]\nmodel = 'inner'\n",
			key:   "model",
			value: &value,
			want:  "model = 'deepseek-flash'\n[model_providers.ggb]\nmodel = 'inner'\n",
		},
		{
			name:  "CRLF 行尾保留",
			input: "model = 'old'\r\n\r\n[model_providers.ggb]\r\nname = \"ggb\"\r\n",
			key:   "model",
			value: &value,
			want:  "model = 'deepseek-flash'\r\n\r\n[model_providers.ggb]\r\nname = \"ggb\"\r\n",
		},
		{
			name:  "值为空字符串时写入空字面量",
			input: "model_reasoning_effort = \"high\"\n",
			key:   "model_reasoning_effort",
			value: &empty,
			want:  "model_reasoning_effort = ''\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeCodexRootKey([]byte(tt.input), tt.key, tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际成功: %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("mergeCodexRootKey 失败: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("结果不符:\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}

	t.Run("白名单外的键被拒绝", func(t *testing.T) {
		if _, err := mergeCodexRootKey([]byte("a = 1\n"), "arbitrary_key", &value); err == nil {
			t.Fatal("期望拒绝白名单外的键")
		}
	})
}

func TestCodexModelCatalogUpsert(t *testing.T) {
	t.Run("新增条目包含完整元数据并复制指令模板", func(t *testing.T) {
		builtin := testCodexBuiltinCatalog(t, testBuiltinEntrySol, testBuiltinEntry55)
		catalog, err := readCodexModelCatalog(filepath.Join(t.TempDir(), "models.json"))
		if err != nil {
			t.Fatalf("读取空目录失败: %v", err)
		}
		err = catalog.upsertModel(codexModelUpsertInput{
			Slug:          "my-ds",
			SupportsImage: true,
			ContextWindow: 1048576,
		}, builtin)
		if err != nil {
			t.Fatalf("upsert 失败: %v", err)
		}
		if len(catalog.entries) != 1 {
			t.Fatalf("条目数量错误: %d", len(catalog.entries))
		}
		var entry codexCatalogModelEntry
		if err := json.Unmarshal(catalog.entries[0], &entry); err != nil {
			t.Fatalf("解析条目失败: %v", err)
		}
		if entry.Slug != "my-ds" || entry.DisplayName != "my-ds" {
			t.Fatalf("slug/display_name 错误: %+v", entry)
		}
		if entry.InputModalities[0] != "text" || entry.InputModalities[1] != "image" || !entry.SupportsImageDetailOriginal {
			t.Fatalf("图片输入元数据错误: %+v", entry.InputModalities)
		}
		if entry.ContextWindow != 1048576 || entry.MaxContextWindow != 1048576 {
			t.Fatalf("上下文窗口错误: %d/%d", entry.ContextWindow, entry.MaxContextWindow)
		}
		if entry.DefaultReasoningLevel != "high" {
			t.Fatalf("默认推理档位错误: %s", entry.DefaultReasoningLevel)
		}
		if len(entry.SupportedReasoningLevels) != 3 || entry.TruncationPolicy.Mode != "tokens" || entry.TruncationPolicy.Limit != 10000 {
			t.Fatalf("骨架常量字段错误: %+v", entry)
		}
		if entry.ApplyPatchToolType != "freeform" || entry.ShellType != "shell_command" || entry.Priority != 99 || entry.SupportsSearchTool {
			t.Fatalf("骨架常量字段错误: %+v", entry)
		}
		var messages codexEntryModelMessages
		if err := json.Unmarshal(entry.ModelMessages, &messages); err != nil || messages.InstructionsTemplate == "" {
			t.Fatalf("model_messages 未从内置清单复制: %s", entry.ModelMessages)
		}
		if !strings.Contains(string(entry.ModelMessages), "You are Codex.") {
			t.Fatalf("应优先复制 gpt-5.6-* 的指令模板: %s", entry.ModelMessages)
		}

		if !catalog.ensureBuiltinEntries(builtin) {
			t.Fatal("空目录并入内置条目应报告变更")
		}
		if len(catalog.entries) != 3 {
			t.Fatalf("内置条目应并入: %d", len(catalog.entries))
		}
		if catalog.ensureBuiltinEntries(builtin) {
			t.Fatal("重复并入应报告无变更")
		}
		if len(catalog.entries) != 3 {
			t.Fatalf("内置条目不应重复并入: %d", len(catalog.entries))
		}
	})

	t.Run("内置清单不可用时新增报错", func(t *testing.T) {
		catalog, _ := readCodexModelCatalog(filepath.Join(t.TempDir(), "models.json"))
		err := catalog.upsertModel(codexModelUpsertInput{Slug: "x", ContextWindow: 128000}, nil)
		if err == nil {
			t.Fatal("期望内置清单不可用时新增报错")
		}
	})

	t.Run("更新仅覆盖可调字段并保留未知字段", func(t *testing.T) {
		modelsPath := filepath.Join(t.TempDir(), "models.json")
		seed := `{"models":[{"slug":"my-ds","display_name":"Old","description":"old desc","custom_field":42,"input_modalities":["text"],"context_window":64000,"max_context_window":64000,"default_reasoning_level":"low","supported_reasoning_levels":[{"effort":"low","description":""},{"effort":"high","description":""}]}],"extra":"keep"}`
		if err := os.WriteFile(modelsPath, []byte(seed), 0600); err != nil {
			t.Fatalf("写入种子文件失败: %v", err)
		}
		catalog, err := readCodexModelCatalog(modelsPath)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		builtin := testCodexBuiltinCatalog(t, testBuiltinEntrySol)
		err = catalog.upsertModel(codexModelUpsertInput{
			Slug:                  "my-ds",
			DisplayName:           "New Name",
			SupportsImage:         true,
			ContextWindow:         128000,
			DefaultReasoningLevel: "high",
		}, builtin)
		if err != nil {
			t.Fatalf("upsert 失败: %v", err)
		}
		var entry map[string]any
		if err := json.Unmarshal(catalog.entries[0], &entry); err != nil {
			t.Fatalf("解析条目失败: %v", err)
		}
		if entry["display_name"] != "New Name" {
			t.Fatalf("display_name 未更新: %v", entry["display_name"])
		}
		if entry["description"] != "old desc" {
			t.Fatalf("未提供的描述应保留原值: %v", entry["description"])
		}
		if entry["custom_field"] != float64(42) {
			t.Fatalf("未知字段被丢弃: %v", entry["custom_field"])
		}
		modalities := entry["input_modalities"].([]any)
		if len(modalities) != 2 || modalities[1] != "image" || entry["supports_image_detail_original"] != true {
			t.Fatalf("图片元数据未更新: %v", modalities)
		}
		if entry["context_window"] != float64(128000) || entry["max_context_window"] != float64(128000) {
			t.Fatalf("上下文窗口未同步提升: %v/%v", entry["context_window"], entry["max_context_window"])
		}
		if entry["default_reasoning_level"] != "high" {
			t.Fatalf("推理档位未更新: %v", entry["default_reasoning_level"])
		}
		messages := entry["model_messages"].(map[string]any)
		if messages["instructions_template"] != "You are Codex." {
			t.Fatalf("坏条目应补全指令模板: %v", messages)
		}

		encoded, err := catalog.encode()
		if err != nil {
			t.Fatalf("编码失败: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(encoded, &doc); err != nil {
			t.Fatalf("编码结果不是合法 JSON: %v", err)
		}
		if doc["extra"] != "keep" {
			t.Fatalf("顶层未知字段被丢弃: %v", doc["extra"])
		}
		if !strings.Contains(string(encoded), "\n  \"models\"") {
			t.Fatalf("输出应为缩进 JSON: %s", encoded)
		}
	})

	t.Run("更新健康条目时保留原有指令", func(t *testing.T) {
		modelsPath := filepath.Join(t.TempDir(), "models.json")
		seed := `{"models":[{"slug":"my-ds","input_modalities":["text"],"context_window":64000,"max_context_window":64000,"supported_reasoning_levels":[{"effort":"low","description":""}],"model_messages":{"instructions_template":"Custom prompt."}}]}`
		if err := os.WriteFile(modelsPath, []byte(seed), 0600); err != nil {
			t.Fatalf("写入种子文件失败: %v", err)
		}
		catalog, err := readCodexModelCatalog(modelsPath)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		err = catalog.upsertModel(codexModelUpsertInput{
			Slug:          "my-ds",
			ContextWindow: 64000,
		}, testCodexBuiltinCatalog(t, testBuiltinEntrySol))
		if err != nil {
			t.Fatalf("upsert 失败: %v", err)
		}
		var entry map[string]any
		if err := json.Unmarshal(catalog.entries[0], &entry); err != nil {
			t.Fatalf("解析条目失败: %v", err)
		}
		messages := entry["model_messages"].(map[string]any)
		if messages["instructions_template"] != "Custom prompt." {
			t.Fatalf("健康条目的指令不应被覆盖: %v", messages)
		}
	})

	t.Run("更新时使用不支持的推理档位报错", func(t *testing.T) {
		modelsPath := filepath.Join(t.TempDir(), "models.json")
		seed := `{"models":[{"slug":"my-ds","supported_reasoning_levels":[{"effort":"low","description":""}],"context_window":64000}]}`
		if err := os.WriteFile(modelsPath, []byte(seed), 0600); err != nil {
			t.Fatalf("写入种子文件失败: %v", err)
		}
		catalog, err := readCodexModelCatalog(modelsPath)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		err = catalog.upsertModel(codexModelUpsertInput{
			Slug:                  "my-ds",
			ContextWindow:         64000,
			DefaultReasoningLevel: "max",
		}, testCodexBuiltinCatalog(t, testBuiltinEntrySol))
		if err == nil {
			t.Fatal("期望不支持的推理档位报错")
		}
	})
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
