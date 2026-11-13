package mcpsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToggleDiscoveredEntryRestoresCompleteConfig(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		create func(func() (string, error)) Adapter
	}{
		{"claude", `{"mcpServers":{"demo":{"command":"npx","args":["", " example "],"timeout":30000},"other":{"command":"other"}},"unrelated":true}`, NewClaudeCodeAdapter},
		{"codex", "# 保留其它表\nmodel = 'example'\n[mcp_servers.demo]\ncommand = 'npx'\nstartup_timeout_sec = 30\nenv_vars = ['EXAMPLE']\n[mcp_servers.other]\ncommand = 'other'\n", NewCodexAdapter},
		{"opencode", `{"mcp":{"demo":{"type":"local","command":["npx"],"enabled":true,"timeout":30000},"other":{"type":"remote","url":"https://example.com/mcp"}},"theme":"system"}`, func(path func() (string, error)) Adapter { return NewOpenCodeAdapter(path, decodeJSONObject) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "application-config")
			require.NoError(t, os.WriteFile(path, []byte(tc.raw), 0600))
			adapter := tc.create(func() (string, error) { return path, nil })
			service := NewService(NewStore(filepath.Join(dir, "mcp.json")), adapter)
			state, err := service.State()
			require.NoError(t, err)
			require.Empty(t, state.Servers, "切换外部条目不要求先导入")
			require.Len(t, state.Detected, 2)
			require.NotNil(t, state.Detected[0].Server)
			server := *state.Detected[0].Server
			result, err := service.Toggle(ToggleRequest{Revision: state.Revision, AgentRevision: state.Agents[0].Revision, Agent: adapter.Key(), Server: server, Enabled: false})
			require.NoError(t, err)
			require.True(t, result.Agents[0].OK)
			afterDisable, err := os.ReadFile(path)
			require.NoError(t, err)
			entries, err := adapter.Read(afterDisable)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, "other", entries[0].Name)
			state, err = service.State()
			require.NoError(t, err)
			require.Len(t, state.Detected, 2, "停用后仍能展示条目并重新启用")
			for _, entry := range state.Detected {
				if entry.Name == "demo" {
					require.False(t, entry.Enabled)
				}
			}
			result, err = service.Toggle(ToggleRequest{Revision: state.Revision, AgentRevision: state.Agents[0].Revision, Agent: adapter.Key(), Server: server, Enabled: true})
			require.NoError(t, err)
			require.True(t, result.Agents[0].OK)
			afterEnable, err := os.ReadFile(path)
			require.NoError(t, err)
			original, field, err := configObject(adapter, []byte(tc.raw))
			require.NoError(t, err)
			restored, _, err := configObject(adapter, afterEnable)
			require.NoError(t, err)
			// Codex 原先省略 enabled，恢复时会明确设为 true。
			if adapter.Key() == AgentCodex {
				original[field].(map[string]any)["demo"].(map[string]any)["enabled"] = true
			}
			require.Equal(t, original, restored, "切换不能丢失附加字段或修改其它配置")
		})
	}
}

func TestToggleSameNameVariantsAreIndependent(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "claude.json")
	codexPath := filepath.Join(dir, "codex.toml")
	claudeRaw := []byte(`{"mcpServers":{"demo":{"command":"claude-command"}}}`)
	codexRaw := []byte("[mcp_servers.demo]\ncommand = 'codex-command'\n")
	require.NoError(t, os.WriteFile(claudePath, claudeRaw, 0600))
	require.NoError(t, os.WriteFile(codexPath, codexRaw, 0600))
	service := NewService(NewStore(filepath.Join(dir, "mcp.json")), NewClaudeCodeAdapter(func() (string, error) { return claudePath, nil }), NewCodexAdapter(func() (string, error) { return codexPath, nil }))
	server := Server{Name: "demo", Transport: TransportStdio, Command: "claude-command", Agents: []AgentKey{}}
	_, err := service.Toggle(ToggleRequest{Agent: AgentCodex, AgentRevision: Revision(codexRaw), Server: server, Enabled: true})
	require.ErrorIs(t, err, ErrAgentConflict)
	result, err := service.Toggle(ToggleRequest{Agent: AgentClaudeCode, AgentRevision: Revision(claudeRaw), Server: server, Enabled: false})
	require.NoError(t, err)
	require.True(t, result.Agents[0].OK)
	actual, err := os.ReadFile(codexPath)
	require.NoError(t, err)
	require.Equal(t, codexRaw, actual)
}

func TestToggleRejectsStaleApplicationRevision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	listPath := filepath.Join(dir, "mcp.json")
	raw := []byte("[mcp_servers.demo]\ncommand = 'npx'\n")
	require.NoError(t, os.WriteFile(path, raw, 0600))
	service := NewService(NewStore(listPath), NewCodexAdapter(func() (string, error) { return path, nil }))
	_, err := service.Toggle(ToggleRequest{Agent: AgentCodex, AgentRevision: "stale", Server: Server{Name: "demo", Transport: TransportStdio, Command: "npx"}, Enabled: false})
	require.ErrorIs(t, err, ErrRevisionConflict)
	_, err = os.Stat(listPath)
	require.True(t, os.IsNotExist(err))
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, raw, actual)
}
