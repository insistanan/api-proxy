package handlers

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
)

// performAgentPromptRequest 对 /settings/agent-prompt/:agent 发起请求；
// method 为 PUT 时以 body 作为 JSON 请求体。
func performAgentPromptRequest(t *testing.T, method, agent, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/settings/agent-prompt/:agent", GetAgentPrompt())
	router.PUT("/settings/agent-prompt/:agent", SaveAgentPrompt())
	target := "/settings/agent-prompt/" + agent
	var request *http.Request
	if method == http.MethodPut {
		request = httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	} else {
		request = httptest.NewRequest(http.MethodGet, target, nil)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// useAgentPromptHome 把指定智能体的配置目录重定向到临时目录，
// 返回提示词文件的预期路径。
func useAgentPromptHome(t *testing.T, agent string) string {
	t.Helper()
	home := t.TempDir()
	switch agent {
	case "claude-code":
		t.Setenv("CLAUDE_CONFIG_DIR", home)
	case "codex":
		t.Setenv("CODEX_HOME", home)
	case "opencode":
		t.Setenv("OPENCODE_CONFIG_DIR", home)
	case "pi":
		t.Setenv("PI_AGENT_CONFIG_DIR", home)
	case "dsh":
		t.Setenv("DSH_HOME", home)
	default:
		t.Fatalf("测试不认识的智能体: %s", agent)
	}
	return filepath.Join(home, agentPromptSpecs[agent].fileName)
}

func TestAgentPromptHandlers(t *testing.T) {
	t.Run("不支持的智能体返回 400", func(t *testing.T) {
		recorder := performAgentPromptRequest(t, http.MethodGet, "unknown-agent", "")
		assert.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	// 覆盖全部受支持智能体：路径解析、读取与保存走各自的环境变量约定。
	for _, agent := range []string{"claude-code", "codex", "opencode", "pi", "dsh"} {
		t.Run(agent, func(t *testing.T) {
			promptPath := useAgentPromptHome(t, agent)

			t.Run("文件不存在时 exists=false 且可写", func(t *testing.T) {
				recorder := performAgentPromptRequest(t, http.MethodGet, agent, "")
				require.Equal(t, http.StatusOK, recorder.Code)
				var response agentPromptFileResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				assert.False(t, response.Exists)
				assert.True(t, response.Writable)
				assert.Equal(t, promptPath, response.Path)
				assert.Empty(t, response.Content)
				assert.Empty(t, response.ReadError)
			})

			t.Run("保存后能读回内容且生成备份", func(t *testing.T) {
				recorder := performAgentPromptRequest(t, http.MethodPut, agent, `{"content":"# 全局提示词\n- 中文输出"}`)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

				recorder = performAgentPromptRequest(t, http.MethodGet, agent, "")
				require.Equal(t, http.StatusOK, recorder.Code)
				var response agentPromptFileResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				assert.True(t, response.Exists)
				assert.Equal(t, "# 全局提示词\n- 中文输出", response.Content)

				// 再次保存触发备份（首次保存无原文件，不产生 .bak）
				recorder = performAgentPromptRequest(t, http.MethodPut, agent, `{"content":"v2"}`)
				require.Equal(t, http.StatusOK, recorder.Code)
				backups, err := filepath.Glob(promptPath + ".*.bak")
				require.NoError(t, err)
				require.Len(t, backups, 1, "覆盖已有提示词时应生成一个 .bak 备份")
				backupContent, err := os.ReadFile(backups[0])
				require.NoError(t, err)
				assert.Equal(t, "# 全局提示词\n- 中文输出", string(backupContent))
			})
		})
	}

	t.Run("空内容保存后文件存在且为空", func(t *testing.T) {
		useAgentPromptHome(t, "dsh")
		recorder := performAgentPromptRequest(t, http.MethodPut, "dsh", `{"content":""}`)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		recorder = performAgentPromptRequest(t, http.MethodGet, "dsh", "")
		require.Equal(t, http.StatusOK, recorder.Code)
		var response agentPromptFileResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		assert.True(t, response.Exists)
		assert.Empty(t, response.Content)
	})

	t.Run("内容超过上限返回 400", func(t *testing.T) {
		useAgentPromptHome(t, "dsh")
		body := `{"content":"` + strings.Repeat("x", agentPromptContentMaxBytes+1) + `"}`
		recorder := performAgentPromptRequest(t, http.MethodPut, "dsh", body)
		assert.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	t.Run("只读文件保存返回 500 与详情", func(t *testing.T) {
		promptPath := useAgentPromptHome(t, "dsh")
		require.NoError(t, os.WriteFile(promptPath, []byte("旧内容"), 0600))
		require.NoError(t, os.Chmod(promptPath, 0444))
		t.Cleanup(func() { _ = os.Chmod(promptPath, 0600) })

		recorder := performAgentPromptRequest(t, http.MethodGet, "dsh", "")
		require.Equal(t, http.StatusOK, recorder.Code)
		var response agentPromptFileResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		assert.False(t, response.Writable, "只读文件应报告不可写")

		recorder = performAgentPromptRequest(t, http.MethodPut, "dsh", `{"content":"新内容"}`)
		assert.Equal(t, http.StatusInternalServerError, recorder.Code)
		assert.Contains(t, recorder.Body.String(), "不可写")
		assert.Contains(t, recorder.Body.String(), "detail")
	})
}
