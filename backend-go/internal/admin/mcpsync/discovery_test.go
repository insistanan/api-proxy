package mcpsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergedPlaywrightPreservesPerAgentLaunchers(t *testing.T) {
	dir := t.TempDir()
	claudePath, codexPath := filepath.Join(dir, "claude.json"), filepath.Join(dir, "codex.toml")
	claudeRaw := []byte(`{"mcpServers":{"playwright":{"command":"cmd","args":["/c","npx","@playwright/mcp@latest","--extension"],"timeout":30000}}}`)
	codexRaw := []byte("[mcp_servers.playwright]\ncommand = 'npx'\nargs = ['@playwright/mcp@latest', '--extension']\nstartup_timeout_sec = 30\n")
	require.NoError(t, os.WriteFile(claudePath, claudeRaw, 0600))
	require.NoError(t, os.WriteFile(codexPath, codexRaw, 0600))
	store := NewStore(filepath.Join(dir, "mcp.json"))
	service := NewService(store, NewClaudeCodeAdapter(func() (string, error) { return claudePath, nil }), NewCodexAdapter(func() (string, error) { return codexPath, nil }))
	server := Server{Name: "playwright", Transport: TransportStdio, Command: "cmd", Args: []string{"/c", "npx", "@playwright/mcp@latest", "--extension"}, Agents: []AgentKey{AgentClaudeCode, AgentCodex},
		AgentConfigs: map[AgentKey]Connection{AgentCodex: {Transport: TransportStdio, Command: "npx", Args: []string{"@playwright/mcp@latest", "--extension"}}}}
	result, err := service.Save("", []Server{server})
	require.NoError(t, err)
	for _, outcome := range result.Agents {
		require.True(t, outcome.OK)
	}
	actualClaude, err := os.ReadFile(claudePath)
	require.NoError(t, err)
	require.Equal(t, claudeRaw, actualClaude, "导入合并不能改写 Claude 启动包装或附加设置")
	actualCodex, err := os.ReadFile(codexPath)
	require.NoError(t, err)
	require.Equal(t, codexRaw, actualCodex, "Codex 应保留直接启动及原始文本")
	state, err := service.State()
	require.NoError(t, err)
	result, err = service.Toggle(ToggleRequest{Revision: state.Revision, AgentRevision: Revision(codexRaw), Agent: AgentCodex, Server: ConfigForAgent(server, AgentCodex), Enabled: false})
	require.NoError(t, err)
	require.True(t, result.Agents[0].OK)
	state, err = service.State()
	require.NoError(t, err)
	require.Equal(t, []AgentKey{AgentClaudeCode}, state.Servers[0].Agents)
	current, err := os.ReadFile(codexPath)
	require.NoError(t, err)
	result, err = service.Toggle(ToggleRequest{Revision: state.Revision, AgentRevision: Revision(current), Agent: AgentCodex, Server: ConfigForAgent(state.Servers[0], AgentCodex), Enabled: true})
	require.NoError(t, err)
	require.True(t, result.Agents[0].OK)
	current, err = os.ReadFile(codexPath)
	require.NoError(t, err)
	require.Contains(t, string(current), "startup_timeout_sec = 30")
	entries, err := service.adapters[1].Read(current)
	require.NoError(t, err)
	require.True(t, sameConfig(ConfigForAgent(server, AgentCodex), *entries[0].Server))
}

func TestNpxIdentityDoesNotFoldShellLogic(t *testing.T) {
	direct := Server{Name: "playwright", Transport: TransportStdio, Command: "npx", Args: []string{"@playwright/mcp@latest", "--extension"}}
	wrapped := direct
	wrapped.Command, wrapped.Args = "cmd.exe", append([]string{"/c", "npx"}, direct.Args...)
	require.True(t, sameIdentity(direct, wrapped))
	require.False(t, sameConfig(direct, wrapped), "写入前仍须严格比较")
	wrapped.Args = append(wrapped.Args, "&&", "other")
	require.False(t, sameIdentity(direct, wrapped))
	wrapped.Args = []string{"/c", "npx", "@playwright/mcp@latest", "%EXTRA%"}
	require.False(t, sameIdentity(direct, wrapped))
}

func TestImportKeepsClientSettings(t *testing.T) {
	path := func() (string, error) { return "unused", nil }
	cases := []struct {
		name    string
		adapter Adapter
		raw     string
		extra   string
	}{
		{
			name:    "claude",
			adapter: NewClaudeCodeAdapter(path),
			raw:     `{"mcpServers":{"demo":{"command":"npx","args":["-y","example"],"timeout":30000}},"unrelated":true}`,
			extra:   `"timeout": 30000`,
		},
		{
			name:    "codex",
			adapter: NewCodexAdapter(path),
			raw:     "# 保留注释\nmodel = 'example'\n[mcp_servers.demo]\ncommand = 'npx'\nargs = ['-y', 'example']\nstartup_timeout_sec = 30\n",
			extra:   "startup_timeout_sec = 30",
		},
		{
			name:    "opencode",
			adapter: NewOpenCodeAdapter(path, decodeJSONObject),
			raw:     `{"mcp":{"demo":{"type":"local","command":["npx","-y","example"],"enabled":true,"timeout":30000}},"theme":"system"}`,
			extra:   `"timeout": 30000`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := tc.adapter.Read([]byte(tc.raw))
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.NotNil(t, entries[0].Server)
			require.True(t, entries[0].Enabled)
			require.NotEmpty(t, entries[0].Warning)
			server := *entries[0].Server
			managed := map[string]struct{}{"demo": {}}
			next, changed, err := tc.adapter.Render([]byte(tc.raw), map[string]Server{"demo": server}, managed)
			require.NoError(t, err)
			require.False(t, changed, "仅导入不能重写原配置")
			require.Equal(t, tc.raw, string(next))

			server.Command = "updated-command"
			next, changed, err = tc.adapter.Render([]byte(tc.raw), map[string]Server{"demo": server}, managed)
			require.NoError(t, err)
			require.True(t, changed)
			require.Contains(t, string(next), tc.extra, "编辑通用字段不能丢失客户端设置")
			updated, err := tc.adapter.Read(next)
			require.NoError(t, err)
			require.Len(t, updated, 1)
			require.NotNil(t, updated[0].Server)
			require.Equal(t, "updated-command", updated[0].Server.Command)
		})
	}
}

func TestDiscoveryReportsUnsupportedEntriesAndDisabledState(t *testing.T) {
	adapter := NewCodexAdapter(func() (string, error) { return "unused", nil })
	entries, err := adapter.Read([]byte("[mcp_servers.disabled]\ncommand = 'npx'\nenabled = false\n[mcp_servers.remote]\nurl = 'https://example.com/mcp'\n[mcp_servers.remote.http_headers]\nAuthorization = 'example'\n[mcp_servers.invalid]\ncommand = 42\n"))
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Equal(t, "disabled", entries[0].Name)
	require.False(t, entries[0].Enabled)
	require.NotNil(t, entries[0].Server)
	require.Empty(t, entries[0].Server.Agents)
	require.Equal(t, "invalid", entries[1].Name)
	require.Nil(t, entries[1].Server)
	require.NotEmpty(t, entries[1].Error)
	require.NotNil(t, entries[2].Server)
	require.Equal(t, TransportHTTP, entries[2].Server.Transport)
	require.Equal(t, "example", entries[2].Server.Headers["Authorization"])

	claude := NewClaudeCodeAdapter(func() (string, error) { return "unused", nil })
	entries, err = claude.Read([]byte(`{"mcpServers":{"legacy":{"type":"sse","url":"https://example.com/sse"},"valid":{"command":"npx"}}}`))
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.NotEmpty(t, entries[0].Error)
	require.NotNil(t, entries[1].Server)
}

func TestCodexUpdatesMultipleServersAndKeepsOtherTables(t *testing.T) {
	adapter := NewCodexAdapter(func() (string, error) { return "unused", nil })
	raw := []byte("model = 'example'\n[mcp_servers]\n[mcp_servers.first]\ncommand = 'old'\nstartup_timeout_sec = 30\n[profiles.work]\nmodel = 'work-model'\n")
	desired := map[string]Server{
		"first":  {Name: "first", Transport: TransportStdio, Command: "new"},
		"second": {Name: "second", Transport: TransportHTTP, URL: "https://example.com/mcp", Headers: map[string]string{"X-Example": "value"}},
	}
	next, changed, err := adapter.Render(raw, desired, map[string]struct{}{"first": {}, "second": {}})
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(next), "startup_timeout_sec = 30")
	require.Contains(t, string(next), "[profiles.work]\nmodel = 'work-model'")
	entries, err := adapter.Read(next)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.NotNil(t, entries[0].Server)
	require.Equal(t, "new", entries[0].Server.Command)
	require.NotNil(t, entries[1].Server)
	require.Equal(t, TransportHTTP, entries[1].Server.Transport)
}

func TestSaveDoesNotDeleteUnselectedAppEntry(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "claude.json")
	codexPath := filepath.Join(dir, "config.toml")
	claudeRaw := []byte(`{"mcpServers":{"demo":{"command":"claude-command"}}}`)
	codexRaw := []byte("[mcp_servers.demo]\ncommand = 'codex-command'\n")
	require.NoError(t, os.WriteFile(claudePath, claudeRaw, 0600))
	require.NoError(t, os.WriteFile(codexPath, codexRaw, 0600))
	service := NewService(NewStore(filepath.Join(dir, "mcp.json")),
		NewClaudeCodeAdapter(func() (string, error) { return claudePath, nil }),
		NewCodexAdapter(func() (string, error) { return codexPath, nil }))
	server := Server{Name: "demo", Transport: TransportStdio, Command: "claude-command", Agents: []AgentKey{AgentClaudeCode}}
	result, err := service.Save("", []Server{server})
	require.NoError(t, err)
	for _, agent := range result.Agents {
		require.True(t, agent.OK)
	}
	actual, err := os.ReadFile(codexPath)
	require.NoError(t, err)
	require.Equal(t, codexRaw, actual)

	// 删除托管条目只能清理原来勾选的应用。
	_, err = service.Save(result.Revision, []Server{})
	require.NoError(t, err)
	actual, err = os.ReadFile(codexPath)
	require.NoError(t, err)
	require.Equal(t, codexRaw, actual)
}

func TestSaveRejectsExternalNameConflictBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	listPath := filepath.Join(dir, "mcp.json")
	raw := []byte("[mcp_servers.demo]\ncommand = 'original'\n")
	require.NoError(t, os.WriteFile(configPath, raw, 0600))
	service := NewService(NewStore(listPath), NewCodexAdapter(func() (string, error) { return configPath, nil }))
	_, err := service.Save("", []Server{{Name: "demo", Transport: TransportStdio, Command: "different", Agents: []AgentKey{AgentCodex}}})
	require.ErrorIs(t, err, ErrAgentConflict)
	_, err = os.Stat(listPath)
	require.True(t, os.IsNotExist(err))
	actual, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Equal(t, raw, actual)
}

func TestStateIsReadOnlyAndIsolatesBrokenAppConfig(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "claude.json")
	invalidPath := filepath.Join(dir, "config.toml")
	listPath := filepath.Join(dir, "mcp.json")
	require.NoError(t, os.WriteFile(validPath, []byte(`{"mcpServers":{"demo":{"command":"npx"}}}`), 0600))
	require.NoError(t, os.WriteFile(invalidPath, []byte("[invalid"), 0600))
	service := NewService(NewStore(listPath),
		NewClaudeCodeAdapter(func() (string, error) { return validPath, nil }),
		NewCodexAdapter(func() (string, error) { return invalidPath, nil }))
	state, err := service.State()
	require.NoError(t, err)
	require.Empty(t, state.Servers)
	require.Len(t, state.Detected, 1)
	require.Equal(t, 1, state.Agents[0].Count)
	require.Equal(t, 1, state.Agents[0].Active)
	require.NotEmpty(t, state.Agents[1].Error)
	_, err = os.Stat(listPath)
	require.True(t, os.IsNotExist(err), "读取页面不能自动接管或创建统一列表")
}
