package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupDSHHome 把 DSH_HOME 指到临时目录，避免读写真实 ~/.dsh/settings.yaml。
func setupDSHHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	return home
}

func performDSHSettingsRequest(t *testing.T, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/settings/dsh", GetDSHSettings())
	router.PUT("/settings/dsh", SaveDSHSettings())
	var reader *os.File
	if body == "" {
		reader = nil
	} else {
		tmp := filepath.Join(t.TempDir(), "body.json")
		require.NoError(t, os.WriteFile(tmp, []byte(body), 0600))
		f, err := os.Open(tmp)
		require.NoError(t, err)
		reader = f
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/settings/dsh", reader)
	if reader != nil {
		defer reader.Close()
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeDSHSettingsResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestGetDSHSettingsMissingFileReturnsEmptyArray(t *testing.T) {
	t.Run("settings.yaml 不存在时 providerKeys 必须是 [] 而不是 null", func(t *testing.T) {
		setupDSHHome(t)

		recorder := performDSHSettingsRequest(t, http.MethodGet, "")
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		response := decodeDSHSettingsResponse(t, recorder)

		require.Contains(t, response, "providerKeys")
		// 回归点：早期实现在这里返回 nil slice，json.Marshal 后是 "null"，
		// 前端 DshView 的 [...providerKeys] 会抛 "undefined is not iterable"。
		assert.Equal(t, "[]", string(response["providerKeys"]),
			"providerKeys 不能序列化为 null")
		assert.Equal(t, "{}", string(response["providers"]))
	})

	t.Run("providers 有值但文件顺序缺失时仍按稳定顺序补齐", func(t *testing.T) {
		home := setupDSHHome(t)
		// 写成标量而非 mapping，让 extractDSHProviderKeysOrdered 走 "取不到顺序" 分支。
		require.NoError(t, os.WriteFile(filepath.Join(home, "settings.yaml"),
			[]byte("llm-pi-ai:\n  providers: not-a-map\n"), 0600))

		recorder := performDSHSettingsRequest(t, http.MethodGet, "")
		require.Equal(t, http.StatusOK, recorder.Code)
		response := decodeDSHSettingsResponse(t, recorder)
		assert.Equal(t, "[]", string(response["providerKeys"]))
	})
}

func TestGetDSHSettingsOrderedProviderKeys(t *testing.T) {
	t.Run("按文件顺序返回 providerKeys 并保留未知字段", func(t *testing.T) {
		home := setupDSHHome(t)
		yamlContent := `ui-onboarding:
  welcomeNoticeVersion: 2026-08-13.1
llm-pi-ai:
  providers:
    local:
      apiKeyEnv: LOCAL_API_KEY
      api: anthropic-messages
      baseURL: http://127.0.0.1:9996
      models:
        - id: claude
          input:
            - text
            - image
          reasoningEfforts:
            off:
            medium: medium
            high: high
            max: max
          contextWindow: 220000
          maxTokens: 32000
    xing:
      apiKeyEnv: XING_API_KEY
      api: anthropic-messages
      baseURL: https://example.invalid
      displayName: 星辰
agent-default-model:
  provider: local
  model: claude
ui-theme:
  preference: dark
`
		require.NoError(t, os.WriteFile(filepath.Join(home, "settings.yaml"), []byte(yamlContent), 0600))

		recorder := performDSHSettingsRequest(t, http.MethodGet, "")
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		response := decodeDSHSettingsResponse(t, recorder)

		var keys []string
		require.NoError(t, json.Unmarshal(response["providerKeys"], &keys))
		assert.Equal(t, []string{"local", "xing"}, keys, "providerKeys 必须保持文件顺序")

		var providers map[string]struct {
			APIKeyEnv string `json:"apiKeyEnv"`
			API       string `json:"api"`
			BaseURL   string `json:"baseURL"`
			Models    []struct {
				ID               string            `json:"id"`
				Input            []string          `json:"input"`
				ReasoningEfforts map[string]string `json:"reasoningEfforts"`
				ContextWindow    *int              `json:"contextWindow"`
				MaxTokens        *int              `json:"maxTokens"`
			} `json:"models"`
		}
		require.NoError(t, json.Unmarshal(response["providers"], &providers))
		require.Contains(t, providers, "local")
		require.Len(t, providers["local"].Models, 1)
		assert.Equal(t, "claude", providers["local"].Models[0].ID)
		assert.Equal(t, []string{"text", "image"}, providers["local"].Models[0].Input)
		// YAML 里的 "off:" 空值读回成 JSON null；前端据此判定 "off" 等级已启用。
		assert.Contains(t, providers["local"].Models[0].ReasoningEfforts, "off")
		require.NotNil(t, providers["local"].Models[0].ContextWindow)
		assert.Equal(t, 220000, *providers["local"].Models[0].ContextWindow)
	})

	t.Run("providerKeys 保持文件顺序", func(t *testing.T) {
		home := setupDSHHome(t)
		// 顺序为 zeta、alpha，验证不会按字母序重排。
		yamlContent := `llm-pi-ai:
  providers:
    zeta:
      apiKeyEnv: ZETA_KEY
      api: anthropic-messages
    alpha:
      apiKeyEnv: ALPHA_KEY
      api: anthropic-messages
`
		require.NoError(t, os.WriteFile(filepath.Join(home, "settings.yaml"), []byte(yamlContent), 0600))

		recorder := performDSHSettingsRequest(t, http.MethodGet, "")
		require.Equal(t, http.StatusOK, recorder.Code)
		response := decodeDSHSettingsResponse(t, recorder)

		var keys []string
		require.NoError(t, json.Unmarshal(response["providerKeys"], &keys))
		assert.Equal(t, []string{"zeta", "alpha"}, keys, "providerKeys 必须保持文件顺序")
	})
}

func TestSaveDSHSettingsEmptyProviderKeysRoundTrip(t *testing.T) {
	t.Run("保存空配置后再读取仍返回 []，且文件被创建", func(t *testing.T) {
		home := setupDSHHome(t)
		path := filepath.Join(home, "settings.yaml")

		recorder := performDSHSettingsRequest(t, http.MethodPut,
			`{"providers":{},"providerKeys":[],"defaultModel":null}`)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

		assert.FileExists(t, path)
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		// 空 providers 不能序列化成 "providers: null"，否则前端 providers 变 undefined。
		require.NotContains(t, string(content), "providers: null", string(content))

		getRecorder := performDSHSettingsRequest(t, http.MethodGet, "")
		require.Equal(t, http.StatusOK, getRecorder.Code)
		response := decodeDSHSettingsResponse(t, getRecorder)
		assert.Equal(t, "[]", string(response["providerKeys"]))
		assert.Equal(t, "{}", string(response["providers"]))
		assert.True(t, response["exists"] != nil && string(response["exists"]) == "true")
	})

	t.Run("providerKeys 未传时按稳定顺序生成", func(t *testing.T) {
		home := setupDSHHome(t)
		path := filepath.Join(home, "settings.yaml")

		payload := `{
		  "providers": {
		    "zeta": {"apiKeyEnv": "ZETA_KEY", "api": "anthropic-messages", "baseURL": "http://127.0.0.1:9996", "models": []},
		    "alpha": {"apiKeyEnv": "ALPHA_KEY", "api": "openai-responses", "baseURL": "http://127.0.0.1:9996", "models": []}
		  },
		  "defaultModel": null
		}`
		recorder := performDSHSettingsRequest(t, http.MethodPut, payload)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

		// 读回文件，确认 provider 顺序稳定（不依赖 Go map 迭代）。
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(content), "alpha:", string(content))
		require.Contains(t, string(content), "zeta:", string(content))
		// alpha 必须排在 zeta 之前。
		require.Less(t,
			indexOf(string(content), "alpha:"), indexOf(string(content), "zeta:"),
			"缺省 providerKeys 应按字母序稳定输出")
	})
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
