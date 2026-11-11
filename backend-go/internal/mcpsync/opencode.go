package mcpsync

import (
	"fmt"
)

// openCodeAdapter 写入 OpenCode 配置（opencode.json / .jsonc）顶层的 mcp 对象。
// 解析（JSONC 去注释）由调用方注入，与 handlers/opencode.go 的 normalizeJSONC 保持同一套规则，避免在本包重复实现。
type openCodeAdapter struct {
	path   func() (string, error)
	decode func(raw []byte) (map[string]any, error)
}

// NewOpenCodeAdapter 创建 OpenCode 适配器。decode 负责把 JSON/JSONC 字节解析为对象。
func NewOpenCodeAdapter(path func() (string, error), decode func(raw []byte) (map[string]any, error)) Adapter {
	return openCodeAdapter{path: path, decode: decode}
}

func (openCodeAdapter) Key() AgentKey { return AgentOpenCode }

func (openCodeAdapter) Name() string { return "OpenCode" }

func (a openCodeAdapter) Path() (string, error) { return a.path() }

func (a openCodeAdapter) Read(current []byte) ([]DetectedServer, error) {
	if len(current) == 0 {
		return []DetectedServer{}, nil
	}
	root, err := a.decode(current)
	if err != nil {
		return nil, fmt.Errorf("解析 OpenCode 配置失败: %w", err)
	}
	return readEntries(root, "mcp", AgentOpenCode)
}

func (a openCodeAdapter) Render(current []byte, desired map[string]Server, managed map[string]struct{}) ([]byte, bool, error) {
	root := map[string]any{}
	if len(current) > 0 {
		parsed, err := a.decode(current)
		if err != nil {
			return nil, false, fmt.Errorf("解析 OpenCode 配置失败: %w", err)
		}
		root = parsed
	}
	mcp, err := objectField(root, "mcp")
	if err != nil {
		return nil, false, fmt.Errorf("解析 OpenCode 配置失败: %w", err)
	}
	if mcp == nil {
		mcp = map[string]any{}
	}

	if !applyManagedEntries(mcp, desired, managed, AgentOpenCode, openCodeEntry) {
		return current, false, nil
	}

	if len(mcp) == 0 {
		delete(root, "mcp")
	} else {
		root["mcp"] = mcp
	}
	data, err := marshalJSON(root)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// openCodeEntry 生成 OpenCode 的 mcp 条目。本地进程的 command 是字符串数组（命令与参数合并），环境变量字段名为 environment。
func openCodeEntry(server Server) map[string]any {
	if server.Transport == TransportHTTP {
		entry := map[string]any{"type": "remote", "url": server.URL, "enabled": true}
		if len(server.Headers) > 0 {
			entry["headers"] = anyStringMap(server.Headers)
		}
		return entry
	}
	command := append([]string{server.Command}, server.Args...)
	entry := map[string]any{"type": "local", "command": anyStringList(command), "enabled": true}
	if len(server.Env) > 0 {
		entry["environment"] = anyStringMap(server.Env)
	}
	return entry
}
