package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeSaveRevisionContract(t *testing.T) {
	for _, scenario := range []string{"missing-revision", "external-edit", "new-file", "save", "delete-all"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opencode.json")
			t.Setenv("OPENCODE_CONFIG", path)
			original := []byte(`{"provider":{"old":{"npm":"@ai-sdk/openai-compatible","models":{}}},"theme":"dark"}`)
			if scenario != "new-file" {
				require.NoError(t, os.WriteFile(path, original, 0600))
			}
			router := gin.New()
			router.GET("/config", GetOpenCodeConfig())
			router.PUT("/config", SaveOpenCodeConfig())
			get := httptest.NewRecorder()
			router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/config", nil))
			require.Equal(t, http.StatusOK, get.Code)
			var loaded openCodeConfigResponse
			require.NoError(t, json.Unmarshal(get.Body.Bytes(), &loaded))
			payload := map[string]interface{}{
				"revision":  loaded.Revision,
				"providers": []map[string]interface{}{{"id": "local", "protocol": "chat", "apiKeyAction": "keep", "models": []interface{}{}}},
			}
			wantStatus := http.StatusOK
			wantCode := ""
			if scenario == "missing-revision" {
				delete(payload, "revision")
				wantStatus, wantCode = http.StatusPreconditionRequired, "revision_required"
			} else if scenario == "external-edit" {
				original = []byte(`{"provider":{},"theme":"external"}`)
				require.NoError(t, os.WriteFile(path, original, 0600))
				wantStatus, wantCode = http.StatusConflict, "revision_conflict"
			} else if scenario == "delete-all" {
				payload["providers"] = []interface{}{}
			}
			body, err := json.Marshal(payload)
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPut, "/config", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			result := httptest.NewRecorder()
			router.ServeHTTP(result, request)
			require.Equal(t, wantStatus, result.Code, result.Body.String())
			var response map[string]interface{}
			require.NoError(t, json.Unmarshal(result.Body.Bytes(), &response))
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			if wantStatus != http.StatusOK {
				require.Equal(t, wantCode, response["code"])
				require.Equal(t, original, raw)
				return
			}
			require.Equal(t, sha256HexOrEmpty(raw), response["revision"])
			var saved map[string]interface{}
			require.NoError(t, json.Unmarshal(raw, &saved))
			if scenario != "new-file" {
				require.Equal(t, "dark", saved["theme"])
			}
			if scenario == "delete-all" {
				require.Empty(t, saved["provider"])
			} else {
				require.Contains(t, saved["provider"], "local")
			}
		})
	}
}

func TestOpenCodeReplacementChecksPreviousBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	current := []byte(`{"external":true}`)
	require.NoError(t, os.WriteFile(path, current, 0600))
	err := writeOpenCodeConfig(path, []byte(`{}`), []byte(`{"replacement":true}`))
	require.ErrorIs(t, err, errOpenCodeRevisionConflict)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, current, raw)
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".opencode-*.tmp"))
	require.NoError(t, err)
	require.Empty(t, temps)
}
