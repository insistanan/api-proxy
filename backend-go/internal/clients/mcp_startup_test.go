package clients

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/insistanan/api-proxy/internal/clients/mcpsync"
	"github.com/stretchr/testify/require"
)

func TestMCPSyncOnStartup_ReportsEachAgentFailure(t *testing.T) {
	dir := t.TempDir()
	listPath := filepath.Join(dir, "mcp.json")
	require.NoError(t, os.WriteFile(listPath, []byte(`{"version":1,"servers":[]}`), 0600))
	api := &MCPAPI{service: mcpsync.NewService(mcpsync.NewStore(listPath),
		startupFailureAdapter{key: "claude", reason: "无法解析配置路径"},
		startupFailureAdapter{key: "codex", reason: "没有读取权限"},
	)}
	err := api.SyncOnStartup(filepath.Join(dir, "legacy.json"))
	require.ErrorContains(t, err, "claude (claude): 无法解析配置路径")
	require.ErrorContains(t, err, "codex (codex): 没有读取权限")
}

type startupFailureAdapter struct {
	key    mcpsync.AgentKey
	reason string
}

func (a startupFailureAdapter) Key() mcpsync.AgentKey { return a.key }
func (a startupFailureAdapter) Name() string          { return string(a.key) }
func (a startupFailureAdapter) Path() (string, error) { return "", errors.New(a.reason) }
func (a startupFailureAdapter) Read([]byte) ([]mcpsync.DetectedServer, error) {
	return nil, errors.New("路径解析失败后不应读取配置")
}
func (a startupFailureAdapter) Render([]byte, map[string]mcpsync.Server, map[string]struct{}) ([]byte, bool, error) {
	return nil, false, errors.New("路径解析失败后不应生成配置")
}
