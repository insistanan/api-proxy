package mcpsync

import "fmt"

// piAdapter 管理 pi 原生的用户级 mcp.json，不触碰项目级覆盖配置。
type piAdapter struct{ path func() (string, error) }

func NewPiAdapter(path func() (string, error)) Adapter { return piAdapter{path: path} }
func (piAdapter) Key() AgentKey                        { return AgentPi }
func (piAdapter) Name() string                         { return "pi" }
func (a piAdapter) Path() (string, error)              { return a.path() }
func (piAdapter) Read(current []byte) ([]DetectedServer, error) {
	root, err := decodeJSONObject(current)
	if err != nil {
		return nil, fmt.Errorf("解析 pi mcp.json 失败: %w", err)
	}
	return readEntries(root, "mcpServers", AgentPi)
}
func (piAdapter) Render(current []byte, desired map[string]Server, managed map[string]struct{}) ([]byte, bool, error) {
	root, err := decodeJSONObject(current)
	if err != nil {
		return nil, false, err
	}
	entries, err := objectField(root, "mcpServers")
	if err != nil {
		return nil, false, err
	}
	if entries == nil {
		entries = map[string]any{}
	}
	if !applyManagedEntries(entries, desired, managed, AgentPi, piEntry) {
		return current, false, nil
	}
	if len(entries) == 0 {
		delete(root, "mcpServers")
	} else {
		root["mcpServers"] = entries
	}
	next, err := marshalJSON(root)
	return next, true, err
}
func piEntry(server Server) map[string]any {
	entry := claudeEntry(server)
	entry["enabled"] = true
	return entry
}
