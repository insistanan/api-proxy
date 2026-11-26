package mcpsync

import (
	"fmt"
	"maps"
	"slices"

	"insistanan/api-proxy/internal/clients/filetxn"
)

// ToggleRequest 使用列表和目标应用的版本共同防止并发覆盖。Server 是页面选中的配置版本。
type ToggleRequest struct {
	Revision      string   `json:"revision"`
	AgentRevision string   `json:"agentRevision"`
	Agent         AgentKey `json:"agent"`
	Server        Server   `json:"server"`
	Enabled       bool     `json:"enabled"`
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
	codec, err := codecFor(adapter)
	if err != nil {
		return SaveResult{}, err
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
		existing, _, err := codec.Parse(name, entry)
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
			existing, _, err := codec.Parse(name, entry)
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
		entry, err = codec.Build(req.Server)
		if err != nil {
			return SaveResult{}, err
		}
	}
	var nextEntry map[string]any
	if req.Enabled {
		nextEntry = maps.Clone(entry)
		codec.Enable(nextEntry)
	} else {
		snapshot, err := encodeEntry(adapter, name, entry)
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
	if !slices.ContainsFunc(file.Servers, func(server Server) bool { return server.Name == name }) {
		server := req.Server
		server.Agents = []AgentKey{}
		file.Servers = append(file.Servers, server)
	}
	for index, server := range file.Servers {
		if server.Name != name {
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
	if err := trackOwnership(&file, adapter, path, current, next, map[string]struct{}{name: {}}); err != nil {
		return SaveResult{}, err
	}
	newRaw, err := s.store.writeFile(&file)
	if err != nil {
		return SaveResult{}, err
	}
	result := AgentResult{Key: req.Agent, Name: adapter.Name(), Path: path, OK: true}
	if changed {
		if err := writeAgentFile(path, current, next); err != nil {
			result.OK, result.Error = false, err.Error()
			result.Committed = filetxn.Committed(err)
		}
	}
	if result.OK {
		confirmOwnership(&file, req.Agent, name)
		newRaw, err = s.store.writeFile(&file)
		if err != nil {
			return SaveResult{}, &filetxn.CommitError{Files: []string{path}, Err: err}
		}
	}
	return SaveResult{Revision: Revision(newRaw), Agents: []AgentResult{result}}, nil
}
