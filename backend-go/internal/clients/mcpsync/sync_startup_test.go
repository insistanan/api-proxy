package mcpsync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMigrateLegacyAndSyncToAgents(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, ".config", "mcp.json")
	projectDir := filepath.Join(root, "skills")
	targetPath := filepath.Join(projectDir, "mcp.json")
	claudePath := filepath.Join(root, ".claude.json")
	brokenPath := filepath.Join(root, "broken.json")

	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0700))
	require.NoError(t, os.WriteFile(brokenPath, []byte("{bad json"), 0600))

	// 写入一份 legacy mcp.json（启用 claude-code 和 codex）
	legacyStore := NewStore(legacyPath)
	_, err := legacyStore.write([]Server{
		{
			Name:      "demo",
			Transport: TransportStdio,
			Command:   "npx",
			Args:      []string{"-y", "demo-mcp"},
			Agents:    []AgentKey{AgentClaudeCode},
		},
	})
	require.NoError(t, err)

	activePath := targetPath
	svc := NewService(
		NewDynamicStore(func() (string, error) { return activePath, nil }),
		NewClaudeCodeAdapter(func() (string, error) { return claudePath, nil }),
		NewCodexAdapter(func() (string, error) { return "", errors.New("codex 不可用") }),
		NewOpenCodeAdapter(func() (string, error) { return brokenPath, nil }, decodeJSONObject),
	)

	// 1. 迁移旧 .config/mcp.json 到动态目标路径
	migrated, err := svc.MigrateLegacy(legacyPath)
	require.NoError(t, err)
	require.True(t, migrated)

	_, err = os.Stat(legacyPath)
	require.True(t, os.IsNotExist(err), "旧文件应重命名为 .migrated")
	_, err = os.Stat(legacyPath + ".migrated")
	require.NoError(t, err)
	_, err = os.Stat(targetPath)
	require.NoError(t, err)

	// 2. 启动分发：claude-code 应成功写入，codex/opencode 失败隔离不影响 claude-code
	results, err := svc.SyncToAgents()
	require.NoError(t, err)
	require.Len(t, results, 3)
	require.True(t, results[0].OK)
	require.False(t, results[1].OK)
	require.False(t, results[2].OK)

	claudeContent, err := os.ReadFile(claudePath)
	require.NoError(t, err)
	require.Contains(t, string(claudeContent), "demo-mcp")

	// 3. 再次调用 SyncToAgents，内容未变时应跳过写入（不刷新 ModTime）
	oldTime := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(claudePath, oldTime, oldTime))
	results, err = svc.SyncToAgents()
	require.NoError(t, err)
	require.True(t, results[0].OK)
	info, err := os.Stat(claudePath)
	require.NoError(t, err)
	require.True(t, info.ModTime().Equal(oldTime), "内容未变化时不应重写智能体配置")

	// 4. 目标目录切换（模拟 Settings 变更）：Store 动态追踪新路径
	newProjectDir := filepath.Join(root, "custom-skills")
	newTarget := filepath.Join(newProjectDir, "mcp.json")
	require.NoError(t, os.MkdirAll(newProjectDir, 0700))
	require.NoError(t, os.Rename(targetPath, newTarget))
	activePath = newTarget

	state, err := svc.State()
	require.NoError(t, err)
	require.Len(t, state.Servers, 1)
	require.Equal(t, "demo", state.Servers[0].Name)
}

func TestRestartRetriesPersistedMCPDeletion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "claude.json")
	store := NewStore(filepath.Join(root, "mcp.json"))
	adapter := NewClaudeCodeAdapter(func() (string, error) { return path, nil })
	service := NewService(store, adapter)
	server := Server{Name: "demo", Transport: TransportStdio, Command: "example", Agents: []AgentKey{AgentClaudeCode}}
	result, err := service.Save("", []Server{server})
	require.NoError(t, err)
	require.True(t, result.Agents[0].OK)

	// 模拟列表已持久化删除、客户端文件尚未来得及分发便退出。
	raw, err := store.ReadRaw()
	require.NoError(t, err)
	file, err := decodeStoreFile(raw)
	require.NoError(t, err)
	file.Servers = []Server{}
	_, err = store.writeFile(&file)
	require.NoError(t, err)
	results, err := NewService(store, adapter).SyncToAgents()
	require.NoError(t, err)
	require.True(t, results[0].OK)
	current, err := os.ReadFile(path)
	require.NoError(t, err)
	entries, err := adapter.Read(current)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestStartupSyncRejectsLocallyModifiedMCP(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "claude.json")
	store := NewStore(filepath.Join(root, "mcp.json"))
	adapter := NewClaudeCodeAdapter(func() (string, error) { return path, nil })
	service := NewService(store, adapter)
	_, err := service.Save("", []Server{{Name: "demo", Transport: TransportStdio, Command: "example", Agents: []AgentKey{AgentClaudeCode}}})
	require.NoError(t, err)
	local := []byte(`{"mcpServers":{"demo":{"command":"local-edit"}}}`)
	require.NoError(t, os.WriteFile(path, local, 0600))
	results, err := NewService(store, adapter).SyncToAgents()
	require.NoError(t, err)
	require.False(t, results[0].OK)
	require.Contains(t, results[0].Error, "已在本地修改")
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, local, actual)
}
