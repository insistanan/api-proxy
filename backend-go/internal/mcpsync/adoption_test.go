package mcpsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirectEditAndDeletePreserveUnrelatedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	original := []byte(`{"mcpServers":{"demo":{"command":"npx","args":["old"],"cwd":"/work","timeout":30000},"other":{"command":"other"}},"autoEnableCodemode":false}`)
	require.NoError(t, os.WriteFile(path, original, 0600))
	service := NewService(NewStore(filepath.Join(dir, "list.json")), NewPiAdapter(func() (string, error) { return path, nil }))
	state, err := service.State()
	require.NoError(t, err)
	source := *state.Detected[0].Server
	replacement := source
	replacement.Name, replacement.Args = "renamed", []string{"new"}
	result, err := service.Save(state.Revision, []Server{replacement}, Adoption{Agent: AgentPi, AgentRevision: Revision(original), Server: source, Replacement: "renamed"})
	require.NoError(t, err)
	require.True(t, result.Agents[0].OK)
	current, err := os.ReadFile(path)
	require.NoError(t, err)
	root, err := decodeJSONObject(current)
	require.NoError(t, err)
	entries, err := objectField(root, "mcpServers")
	require.NoError(t, err)
	require.NotContains(t, entries, "demo")
	require.Contains(t, entries, "other")
	require.Equal(t, "/work", entries["renamed"].(map[string]any)["cwd"])
	require.Equal(t, false, root["autoEnableCodemode"])
	state, err = service.State()
	require.NoError(t, err)
	_, err = service.Toggle(ToggleRequest{Revision: state.Revision, AgentRevision: Revision(current), Agent: AgentPi, Server: replacement, Enabled: false})
	require.NoError(t, err)
	state, err = service.State()
	require.NoError(t, err)
	current, err = os.ReadFile(path)
	require.NoError(t, err)
	_, err = service.Save(state.Revision, []Server{}, Adoption{Agent: AgentPi, AgentRevision: Revision(current), Server: replacement, Replacement: ""})
	require.NoError(t, err)
	state, err = service.State()
	require.NoError(t, err)
	require.Len(t, state.Detected, 1, "删除必须同时清理停用记录")
	require.Equal(t, "other", state.Detected[0].Name)
}

func TestDirectEditRejectsChangedApplicationBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	path, listPath := filepath.Join(dir, "mcp.json"), filepath.Join(dir, "list.json")
	raw := []byte(`{"mcpServers":{"demo":{"command":"original"}}}`)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	service := NewService(NewStore(listPath), NewPiAdapter(func() (string, error) { return path, nil }))
	source := Server{Name: "demo", Transport: TransportStdio, Command: "original", Agents: []AgentKey{AgentPi}}
	_, err := service.Save("", []Server{}, Adoption{Agent: AgentPi, AgentRevision: "stale", Server: source})
	require.ErrorIs(t, err, ErrRevisionConflict)
	_, err = os.Stat(listPath)
	require.True(t, os.IsNotExist(err))
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, raw, actual)
}
