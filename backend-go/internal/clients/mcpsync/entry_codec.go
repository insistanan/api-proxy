package mcpsync

import (
	"fmt"
	"maps"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// EntryCodec 是开关、接管与所有权检查消费的条目能力。客户端格式由适配器提供。
type EntryCodec struct {
	Object  func([]byte) (map[string]any, string, error)
	Encode  func(string, map[string]any) ([]byte, error)
	Decode  func(string, []byte) (map[string]any, error)
	Replace func([]byte, string, map[string]any) ([]byte, bool, error)
	Build   func(Server) (map[string]any, error)
	Enable  func(map[string]any)
	Parse   func(string, map[string]any) (Server, bool, error)
	Merge   func(map[string]any, Server) (map[string]any, error)
}

type EntryAdapter interface {
	EntryCodec() EntryCodec
}

func codecFor(adapter Adapter) (EntryCodec, error) {
	provider, ok := adapter.(EntryAdapter)
	if !ok {
		return EntryCodec{}, fmt.Errorf("%s 未提供 MCP 条目编辑能力", adapter.Name())
	}
	codec := provider.EntryCodec()
	if codec.Object == nil || codec.Encode == nil || codec.Decode == nil || codec.Replace == nil || codec.Build == nil || codec.Enable == nil || codec.Parse == nil || codec.Merge == nil {
		return EntryCodec{}, fmt.Errorf("%s 的 MCP 条目编辑能力不完整", adapter.Name())
	}
	return codec, nil
}

func configObject(adapter Adapter, raw []byte) (map[string]any, string, error) {
	codec, err := codecFor(adapter)
	if err != nil {
		return nil, "", err
	}
	return codec.Object(raw)
}

func decodeEntrySnapshot(adapter Adapter, saved DisabledEntry) (map[string]any, error) {
	codec, err := codecFor(adapter)
	if err != nil {
		return nil, err
	}
	return codec.Decode(saved.Name, saved.Config)
}

func replaceEntry(adapter Adapter, current []byte, name string, entry map[string]any) ([]byte, bool, error) {
	codec, err := codecFor(adapter)
	if err != nil {
		return nil, false, err
	}
	return codec.Replace(current, name, entry)
}

func encodeEntry(adapter Adapter, name string, entry map[string]any) ([]byte, error) {
	codec, err := codecFor(adapter)
	if err != nil {
		return nil, err
	}
	return codec.Encode(name, entry)
}

func jsonEntryCodec(agent AgentKey, field string, decode func([]byte) (map[string]any, error), build func(Server) map[string]any, enabled bool) EntryCodec {
	codec := EntryCodec{
		Object: func(raw []byte) (map[string]any, string, error) {
			root, err := decode(raw)
			return root, field, err
		},
		Encode: func(_ string, entry map[string]any) ([]byte, error) { return marshalJSON(entry) },
		Decode: func(_ string, raw []byte) (map[string]any, error) { return decodeJSONObject(raw) },
		Build:  func(server Server) (map[string]any, error) { return build(server), nil },
		Parse: func(name string, entry map[string]any) (Server, bool, error) {
			return decodeEntry(name, entry, agent)
		},
		Merge: func(entry map[string]any, server Server) (map[string]any, error) {
			return mergeEntry(entry, server, agent, build), nil
		},
		Enable: func(entry map[string]any) {
			if enabled {
				entry["enabled"] = true
			}
		},
	}
	codec.Replace = func(raw []byte, name string, entry map[string]any) ([]byte, bool, error) {
		root, err := decode(raw)
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
		if len(entries) == 0 {
			delete(root, field)
		} else {
			root[field] = entries
		}
		next, err := marshalJSON(root)
		return next, true, err
	}
	return codec
}

func (claudeCodeAdapter) EntryCodec() EntryCodec {
	return jsonEntryCodec(AgentClaudeCode, "mcpServers", decodeJSONObject, claudeEntry, false)
}

func (piAdapter) EntryCodec() EntryCodec {
	return jsonEntryCodec(AgentPi, "mcpServers", decodeJSONObject, piEntry, true)
}

func (a openCodeAdapter) EntryCodec() EntryCodec {
	return jsonEntryCodec(AgentOpenCode, "mcp", func(raw []byte) (map[string]any, error) {
		if raw == nil {
			return map[string]any{}, nil
		}
		return a.decode(raw)
	}, openCodeEntry, true)
}

func (dshAdapter) EntryCodec() EntryCodec {
	codec := jsonEntryCodec(AgentDSH, "entries", func(raw []byte) (map[string]any, error) {
		entries, err := dshEntries(raw)
		return map[string]any{"entries": entries}, err
	}, dshEntry, true)
	codec.Replace = replaceDSHEntry
	return codec
}

func (codexAdapter) EntryCodec() EntryCodec {
	decode := func(raw []byte) (map[string]any, error) {
		root := map[string]any{}
		if err := toml.Unmarshal(raw, &root); err != nil {
			return nil, fmt.Errorf("解析 config.toml 失败: %w", err)
		}
		return root, nil
	}
	codec := jsonEntryCodec(AgentCodex, "mcp_servers", decode, nil, true)
	codec.Encode = func(name string, entry map[string]any) ([]byte, error) {
		return toml.Marshal(map[string]any{"mcp_servers": map[string]any{name: entry}})
	}
	codec.Decode = func(name string, raw []byte) (map[string]any, error) {
		root, err := decode(raw)
		if err != nil {
			return nil, err
		}
		entries, err := objectField(root, "mcp_servers")
		if err != nil {
			return nil, err
		}
		entry, ok := entries[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Codex MCP 快照缺少条目: %s", name)
		}
		return entry, nil
	}
	codec.Build = func(server Server) (map[string]any, error) {
		return codec.Decode(server.Name, []byte(strings.Join(codexBlock(server), "\n")))
	}
	codec.Merge = func(entry map[string]any, server Server) (map[string]any, error) {
		generated, err := codec.Build(server)
		if err != nil {
			return nil, err
		}
		return mergeEntry(entry, server, AgentCodex, func(Server) map[string]any { return generated }), nil
	}
	codec.Replace = func(raw []byte, name string, entry map[string]any) ([]byte, bool, error) {
		root, err := decode(raw)
		if err != nil {
			return nil, false, err
		}
		entries, err := objectField(root, "mcp_servers")
		if err != nil {
			return nil, false, err
		}
		entries = maps.Clone(entries)
		if entries == nil {
			entries = map[string]any{}
		}
		var blocks []string
		if entry == nil {
			delete(entries, name)
		} else {
			entries[name] = entry
			encoded, err := codec.Encode(name, entry)
			if err != nil {
				return nil, false, err
			}
			var lines []string
			for line := range strings.SplitSeq(string(encoded), "\n") {
				if strings.TrimSpace(line) != "[mcp_servers]" {
					lines = append(lines, line)
				}
			}
			blocks = append(blocks, strings.TrimSpace(strings.Join(lines, "\n")))
		}
		return patchCodexEntries(raw, root, entries, map[string]struct{}{name: {}}, blocks)
	}
	return codec
}
