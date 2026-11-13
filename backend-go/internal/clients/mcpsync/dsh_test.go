package mcpsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const dshExample = `# 用户补丁
- insert:
    - id: mcp-demo
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: demo
        transport: stdio
        command: npx
        args: ['old']
        toolCallTimeoutMs: 45000
    - id: unrelated
      name: example-plugin
      config:
        expression: !!js process.env.EXAMPLE
`

func TestDSHReadEditAndToggleKeepPluginMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cordis.patch.yml")
	require.NoError(t, os.WriteFile(path, []byte(dshExample), 0600))
	adapter := NewDSHAdapter(func() (string, error) { return path, nil })
	service := NewService(NewStore(filepath.Join(dir, "list.json")), adapter)
	state, err := service.State()
	require.NoError(t, err)
	require.Len(t, state.Detected, 1)
	require.NotNil(t, state.Detected[0].Server)
	source := *state.Detected[0].Server
	next, changed, err := adapter.Render([]byte(dshExample), map[string]Server{"demo": source}, map[string]struct{}{"demo": {}})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, dshExample, string(next))
	replacement := source
	replacement.Args = []string{"new"}
	result, err := service.Save(state.Revision, []Server{replacement}, Adoption{Agent: AgentDSH, AgentRevision: Revision([]byte(dshExample)), Server: source, Replacement: source.Name})
	require.NoError(t, err)
	require.True(t, result.Agents[0].OK)
	current, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(current), "toolCallTimeoutMs: 45000")
	require.Contains(t, string(current), "!!js process.env.EXAMPLE")
	state, err = service.State()
	require.NoError(t, err)
	_, err = service.Toggle(ToggleRequest{Revision: state.Revision, AgentRevision: Revision(current), Agent: AgentDSH, Server: replacement, Enabled: false})
	require.NoError(t, err)
	state, err = service.State()
	require.NoError(t, err)
	require.False(t, state.Detected[0].Enabled)
	current, err = os.ReadFile(path)
	require.NoError(t, err)
	_, err = service.Toggle(ToggleRequest{Revision: state.Revision, AgentRevision: Revision(current), Agent: AgentDSH, Server: replacement, Enabled: true})
	require.NoError(t, err)
	current, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(current), "toolCallTimeoutMs: 45000")
	require.Contains(t, string(current), "!!js process.env.EXAMPLE")
}

func TestDSHExpressionInConnectionIsReportedWithoutExecution(t *testing.T) {
	raw := []byte("- insert:\n    - id: mcp-demo\n      name: '@deepseek-ai/dsh-mcp-client'\n      config:\n        serverName: demo\n        transport: stdio\n        command: !!js process.env.MCP_COMMAND\n")
	entries, err := NewDSHAdapter(func() (string, error) { return "unused", nil }).Read(raw)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Nil(t, entries[0].Server)
	require.Contains(t, entries[0].Error, "表达式")
}

func TestDSHCreatesStreamableHTTPEntry(t *testing.T) {
	adapter := NewDSHAdapter(func() (string, error) { return "unused", nil })
	server := Server{Name: "web", Transport: TransportHTTP, URL: "https://example.com/mcp", Headers: map[string]string{"X-Example": "value"}, Agents: []AgentKey{AgentDSH}}
	next, changed, err := adapter.Render(nil, map[string]Server{"web": server}, map[string]struct{}{"web": {}})
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(next), "transport: streamable-http")
	entries, err := adapter.Read(next)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].Server)
	require.True(t, sameConfig(server, *entries[0].Server))
}
