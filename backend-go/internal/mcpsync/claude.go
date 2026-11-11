package mcpsync

import (
	"fmt"
)

// claudeCodeAdapter 写入 Claude Code 的用户级 MCP 配置：$CLAUDE_CONFIG_DIR/.claude.json（未设置时为 ~/.claude.json）顶层的 mcpServers。
// 注意 settings.json 不承载 MCP 条目。文件中的其它字段（会话、项目信息等）由 Claude Code 自己维护，这里原样保留。
type claudeCodeAdapter struct {
	path func() (string, error)
}

// NewClaudeCodeAdapter 创建 Claude Code 适配器。path 由调用方解析配置文件路径。
func NewClaudeCodeAdapter(path func() (string, error)) Adapter {
	return claudeCodeAdapter{path: path}
}

func (claudeCodeAdapter) Key() AgentKey { return AgentClaudeCode }

func (claudeCodeAdapter) Name() string { return "Claude Code" }

func (a claudeCodeAdapter) Path() (string, error) { return a.path() }

func (claudeCodeAdapter) Read(current []byte) ([]DetectedServer, error) {
	root, err := decodeJSONObject(current)
	if err != nil {
		return nil, fmt.Errorf("解析 .claude.json 失败: %w", err)
	}
	return readEntries(root, "mcpServers", AgentClaudeCode)
}

func (claudeCodeAdapter) Render(current []byte, desired map[string]Server, managed map[string]struct{}) ([]byte, bool, error) {
	root, err := decodeJSONObject(current)
	if err != nil {
		return nil, false, fmt.Errorf("解析 .claude.json 失败: %w", err)
	}
	servers, err := objectField(root, "mcpServers")
	if err != nil {
		return nil, false, fmt.Errorf("解析 .claude.json 失败: %w", err)
	}
	if servers == nil {
		servers = map[string]any{}
	}

	changed := applyManagedEntries(servers, desired, managed, AgentClaudeCode, claudeEntry)
	if !changed {
		return current, false, nil
	}

	// 所有条目被移除后不留下空的 mcpServers 对象。
	if len(servers) == 0 {
		delete(root, "mcpServers")
	} else {
		root["mcpServers"] = servers
	}
	data, err := marshalJSON(root)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// claudeEntry 生成 Claude Code 的 mcpServers 条目。stdio 与 http 的字段名遵循 Claude Code 的 .claude.json 格式。
// 空的 args、env、headers 不写出，保持文件简洁。
func claudeEntry(server Server) map[string]any {
	if server.Transport == TransportHTTP {
		entry := map[string]any{"type": "http", "url": server.URL}
		if len(server.Headers) > 0 {
			entry["headers"] = anyStringMap(server.Headers)
		}
		return entry
	}
	entry := map[string]any{"type": "stdio", "command": server.Command}
	if len(server.Args) > 0 {
		entry["args"] = anyStringList(server.Args)
	}
	if len(server.Env) > 0 {
		entry["env"] = anyStringMap(server.Env)
	}
	return entry
}
