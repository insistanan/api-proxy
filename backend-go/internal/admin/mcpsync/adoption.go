package mcpsync

import (
	"fmt"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Adoption 记录用户直接编辑或删除的应用条目及其原始版本。
// Replacement 为空表示删除，否则指向本次列表中替代它的名称。
type Adoption struct {
	Agent         AgentKey `json:"agent"`
	AgentRevision string   `json:"agentRevision"`
	Server        Server   `json:"server"`
	Replacement   string   `json:"replacement"`
}

func generatedEntry(server Server, agent AgentKey) (map[string]any, error) {
	switch agent {
	case AgentClaudeCode:
		return claudeEntry(server), nil
	case AgentOpenCode:
		return openCodeEntry(server), nil
	case AgentCodex:
		var root map[string]any
		if err := toml.Unmarshal([]byte(strings.Join(codexBlock(server), "\n")), &root); err != nil {
			return nil, fmt.Errorf("生成 Codex MCP 条目失败: %w", err)
		}
		return root["mcp_servers"].(map[string]any)[server.Name].(map[string]any), nil
	case AgentPi:
		return piEntry(server), nil
	case AgentDSH:
		return dshEntry(server), nil
	default:
		return nil, fmt.Errorf("未知的 MCP 应用: %s", agent)
	}
}

// prepareAdoptions 在内存中校验原始条目并保留附加设置，不写入用户文件。
func prepareAdoptions(adapter Adapter, current []byte, file *storeFile, changes []Adoption, servers []Server) ([]byte, map[string]Server, error) {
	base := current
	adopted := map[string]Server{}
	root, field, err := configObject(adapter, current)
	if err != nil {
		return nil, nil, err
	}
	entries, err := objectField(root, field)
	if err != nil {
		return nil, nil, err
	}
	for _, change := range changes {
		if change.Agent != adapter.Key() {
			continue
		}
		if change.AgentRevision != Revision(current) {
			return nil, nil, fmt.Errorf("%w: %s 配置已变化", ErrRevisionConflict, adapter.Name())
		}
		name := change.Server.Name
		if _, duplicate := adopted[name]; duplicate {
			return nil, nil, fmt.Errorf("%w: 重复编辑 %s / %s", ErrInvalidServers, adapter.Name(), name)
		}
		entry, present := entries[name].(map[string]any)
		if _, exists := entries[name]; exists && !present {
			return nil, nil, fmt.Errorf("%w: %s 配置无法识别", ErrAgentConflict, name)
		}
		if !present {
			for _, saved := range file.Disabled {
				if saved.Agent == change.Agent && saved.Name == name {
					entry, err = decodeEntrySnapshot(adapter, saved)
					if err != nil {
						return nil, nil, err
					}
					break
				}
			}
		}
		if entry == nil {
			return nil, nil, fmt.Errorf("%w: %s 已不存在，请刷新", ErrAgentConflict, name)
		}
		original, _, err := decodeEntry(name, entry, change.Agent)
		if err != nil || !sameConfig(original, change.Server) {
			return nil, nil, fmt.Errorf("%w: %s / %s", ErrAgentConflict, adapter.Name(), name)
		}
		adopted[name] = original
		file.Disabled = slices.DeleteFunc(file.Disabled, func(saved DisabledEntry) bool { return saved.Agent == change.Agent && saved.Name == name })
		if change.Replacement == "" {
			continue
		}
		index := slices.IndexFunc(servers, func(server Server) bool { return server.Name == change.Replacement })
		if index < 0 {
			return nil, nil, fmt.Errorf("%w: 缺少编辑后的 MCP %s", ErrInvalidServers, change.Replacement)
		}
		replacement := ConfigForAgent(servers[index], change.Agent)
		// 更名不能覆盖另一条尚未接管的配置，包括停用快照。
		if replacement.Name != name {
			if _, exists := entries[replacement.Name]; exists {
				return nil, nil, fmt.Errorf("%w: %s 已存在", ErrAgentConflict, replacement.Name)
			}
			for _, saved := range file.Disabled {
				if saved.Agent == change.Agent && saved.Name == replacement.Name {
					return nil, nil, fmt.Errorf("%w: %s 已有停用配置", ErrAgentConflict, replacement.Name)
				}
			}
		}
		generated, err := generatedEntry(replacement, change.Agent)
		if err != nil {
			return nil, nil, err
		}
		merged := mergeEntry(entry, replacement, change.Agent, func(Server) map[string]any { return generated })
		if slices.Contains(servers[index].Agents, change.Agent) {
			base, _, err = replaceEntry(adapter, base, replacement.Name, merged)
			if err != nil {
				return nil, nil, err
			}
		} else {
			snapshot, err := encodeEntry(change.Agent, replacement.Name, merged)
			if err != nil {
				return nil, nil, err
			}
			file.Disabled = append(file.Disabled, DisabledEntry{Agent: change.Agent, Name: replacement.Name, Config: snapshot})
		}
	}
	return base, adopted, nil
}
