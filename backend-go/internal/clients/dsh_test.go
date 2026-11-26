package clients

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"insistanan/api-proxy/internal/clients/filetxn"
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
	if method == http.MethodPut {
		var payload map[string]any
		if json.Unmarshal([]byte(body), &payload) == nil && payload != nil {
			if _, provided := payload["revision"]; !provided {
				settings, err := filetxn.Read(resolveDSHSettingsPath())
				require.NoError(t, err)
				credentials, err := filetxn.Read(resolveDSHCredentialsPath())
				require.NoError(t, err)
				payload["revision"] = filetxn.RevisionSet(settings, credentials)
				encoded, err := json.Marshal(payload)
				require.NoError(t, err)
				body = string(encoded)
			}
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/settings/dsh", GetDSHSettings())
	router.PUT("/settings/dsh", SaveDSHSettings())
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, "/settings/dsh", nil)
	} else {
		request = httptest.NewRequest(method, "/settings/dsh", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeDSHSettingsResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestDSHSettings(t *testing.T) {
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

	t.Run("保存空配置后读取仍返回空对象与空数组，并创建文件", func(t *testing.T) {
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

	t.Run("未传 providerKeys 时按字母序稳定输出", func(t *testing.T) {
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

		content, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(content), "alpha:", string(content))
		require.Contains(t, string(content), "zeta:", string(content))
		require.Less(t,
			strings.Index(string(content), "alpha:"), strings.Index(string(content), "zeta:"),
			"缺省 providerKeys 应按字母序稳定输出")
	})
}

func TestDSHSharedCredentialsUseIndependentReplacement(t *testing.T) {
	home := setupDSHHome(t)
	path := filepath.Join(home, "credentials.yaml")
	original := []byte("version: 1\nrefs:\n  SHARED: original-value\n")
	require.NoError(t, os.WriteFile(path, original, 0600))
	req := saveDSHSettingsRequest{
		Providers: map[string]DSHProvider{
			"first":  {APIKeyEnv: "SHARED"},
			"second": {APIKeyEnv: "SHARED"},
		},
		CredentialUpdates: map[string]dshCredentialUpdate{
			"first": {Action: dshCredentialActionReplace, APIKey: "new-value"},
		},
	}
	prepared, cleaned, err := prepareDSHCredentials(path, &req)
	require.NoError(t, err)
	require.NotEqual(t, "SHARED", req.Providers["first"].APIKeyEnv)
	require.Equal(t, "SHARED", req.Providers["second"].APIKeyEnv)
	require.Contains(t, string(prepared), "SHARED: original-value")
	require.Contains(t, string(prepared), "new-value")
	require.Nil(t, cleaned, "另一个 provider 仍在引用旧值")
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, actual, "准备阶段不得写凭据文件")
}
