package mcpsync

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// ToggleRequest 使用列表和目标应用的版本共同防止并发覆盖。Server 是页面选中的配置版本。
type ToggleRequest struct {
	Revision      string   `json:"revision"`
	AgentRevision string   `json:"agentRevision"`
	Agent         AgentKey `json:"agent"`
	Server        Server   `json:"server"`
	Enabled       bool     `json:"enabled"`
}

// configObject 复用各适配器的解析规则，返回根对象和 MCP 字段名。
func configObject(adapter Adapter, raw []byte) (map[string]any, string, error) {
	switch a := adapter.(type) {
	case codexAdapter:
		root := map[string]any{}
		if err := toml.Unmarshal(raw, &root); err != nil {
			return nil, "", fmt.Errorf("解析 config.toml 失败: %w", err)
		}
		return root, "mcp_servers", nil
	case openCodeAdapter:
		if len(raw) == 0 {
			return map[string]any{}, "mcp", nil
		}
		root, err := a.decode(raw)
		return root, "mcp", err
	case claudeCodeAdapter:
		root, err := decodeJSONObject(raw)
		return root, "mcpServers", err
	case piAdapter:
		root, err := decodeJSONObject(raw)
		return root, "mcpServers", err
	case dshAdapter:
		entries, err := dshEntries(raw)
		return map[string]any{"entries": entries}, "entries", err
	default:
		return nil, "", fmt.Errorf("不支持该应用的 MCP 开关")
	}
}

// encodeEntry 保存完整原始条目；Codex 使用 TOML，以保留数字、日期等附加字段类型。
func encodeEntry(agent AgentKey, name string, entry map[string]any) ([]byte, error) {
	if agent == AgentCodex {
		return toml.Marshal(map[string]any{"mcp_servers": map[string]any{name: entry}})
	}
	return marshalJSON(entry)
}

func decodeEntrySnapshot(adapter Adapter, saved DisabledEntry) (map[string]any, error) {
	if saved.Agent != AgentCodex {
		return decodeJSONObject(saved.Config)
	}
	root, field, err := configObject(adapter, saved.Config)
	if err != nil {
		return nil, err
	}
	entries, err := objectField(root, field)
	if err != nil {
		return nil, err
	}
	entry, ok := entries[saved.Name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("停用的 MCP 配置缺少原始条目: %s", saved.Name)
	}
	return entry, nil
}

func replaceEntry(adapter Adapter, current []byte, name string, entry map[string]any) ([]byte, bool, error) {
	if adapter.Key() == AgentDSH {
		return replaceDSHEntry(current, name, entry)
	}
	root, field, err := configObject(adapter, current)
	if err != nil {
		return nil, false, err
	}
	entries, err := objectField(root, field)
	if err != nil {
		return nil, false, err
	}
	entries = maps.Clone(entries)
	if entries == nil {
		entries = map[string]any{}
	}
	if entry == nil {
		delete(entries, name)
	} else {
		entries[name] = entry
	}
	if adapter.Key() == AgentCodex {
		var blocks []string
		if entry != nil {
			raw, err := encodeEntry(AgentCodex, name, entry)
			if err != nil {
				return nil, false, err
			}
			var lines []string
			for line := range strings.SplitSeq(string(raw), "\n") {
				if strings.TrimSpace(line) != "[mcp_servers]" {
					lines = append(lines, line)
				}
			}
			blocks = append(blocks, strings.TrimSpace(strings.Join(lines, "\n")))
		}
		return patchCodexEntries(current, root, entries, map[string]struct{}{name: {}}, blocks)
	}
	if len(entries) == 0 {
		delete(root, field)
	} else {
		root[field] = entries
	}
	next, err := marshalJSON(root)
	return next, true, err
}

// Toggle 只写入目标应用和列表中的相应启用选择，不分发其它 MCP 或应用。
// 停用时从客户端移除条目，但先把完整配置保存到统一列表，重新启用时可恢复。
func (s *Service) Toggle(req ToggleRequest) (SaveResult, error) {
	if !IsKnownAgent(req.Agent) {
		return SaveResult{}, fmt.Errorf("%w: 未知的应用", ErrInvalidServers)
	}
	if err := req.Server.validate(); err != nil {
		return SaveResult{}, fmt.Errorf("%w: %w", ErrInvalidServers, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.store.ReadRaw()
	if err != nil {
		return SaveResult{}, err
	}
	if Revision(raw) != req.Revision {
		return SaveResult{}, ErrRevisionConflict
	}
	file, err := decodeStoreFile(raw)
	if err != nil {
		return SaveResult{}, err
	}
	var adapter Adapter
	for _, candidate := range s.adapters {
		if candidate.Key() == req.Agent {
			adapter = candidate
			break
		}
	}
	if adapter == nil {
		return SaveResult{}, fmt.Errorf("%w: 应用适配器不可用", ErrInvalidServers)
	}
	path, err := adapter.Path()
	if err != nil {
		return SaveResult{}, fmt.Errorf("%w: %w", ErrAgentPrepare, err)
	}
	current, err := readOptional(path)
	if err != nil {
		return SaveResult{}, err
	}
	if Revision(current) != req.AgentRevision {
		return SaveResult{}, fmt.Errorf("%w: 目标应用配置已变化", ErrRevisionConflict)
	}
	root, field, err := configObject(adapter, current)
	if err != nil {
		return SaveResult{}, fmt.Errorf("%w: %w", ErrAgentPrepare, err)
	}
	entries, err := objectField(root, field)
	if err != nil {
		return SaveResult{}, fmt.Errorf("%w: %w", ErrAgentPrepare, err)
	}
	name := req.Server.Name
	entry, exists := entries[name].(map[string]any)
	if _, present := entries[name]; present && !exists {
		return SaveResult{}, fmt.Errorf("%w: %s 的现有配置不是对象", ErrAgentConflict, name)
	}
	if exists {
		existing, _, err := decodeEntry(name, entry, req.Agent)
		if err != nil || !sameConfig(existing, req.Server) {
			return SaveResult{}, fmt.Errorf("%w: %s / %s", ErrAgentConflict, adapter.Name(), name)
		}
	}
	if !exists {
		for _, saved := range file.Disabled {
			if saved.Agent != req.Agent || saved.Name != name {
				continue
			}
			entry, err = decodeEntrySnapshot(adapter, saved)
			if err != nil {
				return SaveResult{}, fmt.Errorf("%w: %w", ErrAgentPrepare, err)
			}
			existing, _, err := decodeEntry(name, entry, req.Agent)
			if err != nil || !sameConfig(existing, req.Server) {
				return SaveResult{}, fmt.Errorf("%w: %s 中保存了不同的停用配置", ErrAgentConflict, name)
			}
			break
		}
	}
	if entry == nil {
		if !req.Enabled {
			return SaveResult{}, fmt.Errorf("%w: 目标应用中不存在该 MCP", ErrAgentConflict)
		}
		switch req.Agent {
		case AgentClaudeCode:
			entry = claudeEntry(req.Server)
		case AgentOpenCode:
			entry = openCodeEntry(req.Server)
		case AgentCodex:
			var generated map[string]any
			if err := toml.Unmarshal([]byte(strings.Join(codexBlock(req.Server), "\n")), &generated); err != nil {
				return SaveResult{}, err
			}
			entry = generated["mcp_servers"].(map[string]any)[name].(map[string]any)
		case AgentPi:
			entry = piEntry(req.Server)
		case AgentDSH:
			entry = dshEntry(req.Server)
		}
	}
	var nextEntry map[string]any
	if req.Enabled {
		nextEntry = maps.Clone(entry)
		if req.Agent != AgentClaudeCode {
			nextEntry["enabled"] = true
		}
	} else {
		snapshot, err := encodeEntry(req.Agent, name, entry)
		if err != nil {
			return SaveResult{}, err
		}
		file.Disabled = slices.DeleteFunc(file.Disabled, func(saved DisabledEntry) bool { return saved.Agent == req.Agent && saved.Name == name })
		file.Disabled = append(file.Disabled, DisabledEntry{Agent: req.Agent, Name: name, Config: snapshot})
	}
	next, changed, err := replaceEntry(adapter, current, name, nextEntry)
	if err != nil {
		return SaveResult{}, fmt.Errorf("%w: %w", ErrAgentPrepare, err)
	}
	for index, server := range file.Servers {
		if !sameIdentity(ConfigForAgent(server, req.Agent), req.Server) {
			continue
		}
		if file.Servers[index].AgentConfigs == nil {
			file.Servers[index].AgentConfigs = make(map[AgentKey]Connection)
		}
		file.Servers[index].AgentConfigs[req.Agent] = Connection{Transport: req.Server.Transport,
			Command: req.Server.Command, Args: req.Server.Args, Env: req.Server.Env, URL: req.Server.URL, Headers: req.Server.Headers}
		agents := slices.DeleteFunc(slices.Clone(server.Agents), func(agent AgentKey) bool { return agent == req.Agent })
		if req.Enabled {
			agents = append(agents, req.Agent)
		}
		file.Servers[index].Agents = agents
	}
	newRaw, err := s.store.writeFile(file)
	if err != nil {
		return SaveResult{}, err
	}
	result := AgentResult{Key: req.Agent, Name: adapter.Name(), Path: path, OK: true}
	if changed {
		if err := writeAgentFile(path, current, next); err != nil {
			result.OK, result.Error = false, err.Error()
		}
	}
	return SaveResult{Revision: Revision(newRaw), Agents: []AgentResult{result}}, nil
}
