package mcpsync

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
)

// DetectedServer 描述客户端文件中的现有条目。无法转换的配置仍展示名称和原因，不能导入。
// Enabled 仅表示文件中的启用设置，不代表 MCP 进程已经连接。
type DetectedServer struct {
	Name    string   `json:"name"`
	Agent   AgentKey `json:"agent"`
	Enabled bool     `json:"enabled"`
	Server  *Server  `json:"server,omitempty"`
	Error   string   `json:"error,omitempty"`
	Warning string   `json:"warning,omitempty"`
}

// readEntries 只转换统一模型能表达的字段。其余字段留在原客户端，分发时由适配器保留。
func readEntries(root map[string]any, field string, agent AgentKey) ([]DetectedServer, error) {
	entries, err := objectField(root, field)
	if err != nil {
		return nil, err
	}
	result := make([]DetectedServer, 0, len(entries))
	for _, name := range sortedKeys(entries) {
		detected := DetectedServer{Name: name, Agent: agent}
		entry, ok := entries[name].(map[string]any)
		if !ok {
			detected.Error = "MCP 条目必须是对象"
			result = append(result, detected)
			continue
		}
		server, enabled, err := decodeEntry(name, entry, agent)
		detected.Enabled = enabled
		if err != nil {
			detected.Error = err.Error()
		} else {
			detected.Server = &server
			for key := range entry {
				if !slices.Contains(entryFields(agent), key) {
					detected.Warning = "含客户端附加设置：保存时保留在原应用，不随条目分发到其他应用"
					break
				}
			}
		}
		result = append(result, detected)
	}
	return result, nil
}

func decodeEntry(name string, entry map[string]any, agent AgentKey) (Server, bool, error) {
	if agent == AgentDSH {
		if reason, ok := entry["_dsh_error"].(string); ok {
			return Server{}, false, fmt.Errorf("%s", reason)
		}
		if len(name) > 32 {
			return Server{}, false, fmt.Errorf("DSH MCP 名称不能超过 32 个字符")
		}
	}
	// 通过带类型的中间结构检查字段类型，拒绝把错误字段静默转为空值。
	var config struct {
		Type        string            `json:"type"`
		Command     json.RawMessage   `json:"command"`
		Args        []string          `json:"args"`
		Env         map[string]string `json:"env"`
		Environment map[string]string `json:"environment"`
		URL         string            `json:"url"`
		Headers     map[string]string `json:"headers"`
		HTTPHeaders map[string]string `json:"http_headers"`
		Enabled     *bool             `json:"enabled"`
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return Server{}, false, fmt.Errorf("编码 MCP 条目失败: %w", err)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return Server{}, false, fmt.Errorf("MCP 字段类型无效: %w", err)
	}
	enabled := config.Enabled == nil || *config.Enabled
	server := Server{Name: name, Agents: []AgentKey{}}
	if enabled {
		server.Agents = []AgentKey{agent}
	}
	if agent == AgentOpenCode {
		switch config.Type {
		case "local":
			var command []string
			if err := json.Unmarshal(config.Command, &command); err != nil || len(command) == 0 {
				return Server{}, enabled, fmt.Errorf("本地 MCP 的 command 必须是非空字符串数组")
			}
			server.Transport, server.Command, server.Args, server.Env = TransportStdio, command[0], command[1:], config.Environment
		case "remote":
			server.Transport, server.URL, server.Headers = TransportHTTP, config.URL, config.Headers
		default:
			return Server{}, enabled, fmt.Errorf("暂不支持该 OpenCode MCP 类型")
		}
	} else {
		if config.Type != "" && config.Type != "stdio" && config.Type != "http" {
			return Server{}, enabled, fmt.Errorf("暂不支持该 MCP 传输类型（仅支持 stdio / http）")
		}
		if config.URL != "" {
			if config.Type == "stdio" || len(config.Command) > 0 || len(config.Args) > 0 || len(config.Env) > 0 {
				return Server{}, enabled, fmt.Errorf("MCP 同时包含本地命令和远程连接设置")
			}
			server.Transport, server.URL, server.Headers = TransportHTTP, config.URL, config.Headers
			if agent == AgentCodex {
				server.Headers = config.HTTPHeaders
			}
		} else {
			if config.Type == "http" || len(config.Headers) > 0 || len(config.HTTPHeaders) > 0 {
				return Server{}, enabled, fmt.Errorf("远程 MCP 缺少 URL")
			}
			if err := json.Unmarshal(config.Command, &server.Command); err != nil {
				return Server{}, enabled, fmt.Errorf("本地 MCP 的 command 必须是字符串")
			}
			server.Transport, server.Args, server.Env = TransportStdio, config.Args, config.Env
		}
	}
	if err := server.validate(); err != nil {
		return Server{}, enabled, err
	}
	return server, enabled, nil
}

// sameIdentity 仅用于合并来源；实际覆盖客户端配置仍须通过 sameConfig 严格比较。
func sameIdentity(a, b Server) bool {
	return sameConfig(normalizeNpx(a), normalizeNpx(b))
}

func normalizeNpx(server Server) Server {
	if server.Transport != TransportStdio {
		return server
	}
	isNpx := func(command string) bool {
		return strings.EqualFold(command, "npx") || strings.EqualFold(command, "npx.cmd")
	}
	if (strings.EqualFold(server.Command, "cmd") || strings.EqualFold(server.Command, "cmd.exe")) &&
		len(server.Args) >= 2 && strings.EqualFold(server.Args[0], "/c") && isNpx(server.Args[1]) {
		for _, arg := range server.Args[2:] {
			if arg == "" || strings.ContainsAny(arg, "&|<>^%!\"'()") || strings.ContainsFunc(arg, unicode.IsSpace) {
				return server
			}
		}
		server.Command, server.Args = "npx", server.Args[2:]
	} else if isNpx(server.Command) {
		server.Command = "npx"
	}
	return server
}

// sameConfig 忽略来源与启用选择；空集合和省略的可选字段视为相同。
func sameConfig(a, b Server) bool {
	return a.Name == b.Name && a.Transport == b.Transport && a.Command == b.Command &&
		a.URL == b.URL && slices.Equal(a.Args, b.Args) && maps.Equal(a.Env, b.Env) && maps.Equal(a.Headers, b.Headers)
}

func entryFields(agent AgentKey) []string {
	switch agent {
	case AgentOpenCode:
		return []string{"type", "command", "environment", "url", "headers", "enabled"}
	case AgentCodex:
		return []string{"command", "args", "env", "url", "http_headers", "enabled"}
	case AgentPi, AgentDSH:
		return []string{"type", "command", "args", "env", "url", "headers", "enabled"}
	default:
		return []string{"type", "command", "args", "env", "url", "headers"}
	}
}

// mergeEntry 只替换统一模型负责的字段，保留超时、OAuth、环境变量引用等客户端专有设置。
func mergeEntry(current map[string]any, server Server, agent AgentKey, build func(Server) map[string]any) map[string]any {
	merged := maps.Clone(current)
	if merged == nil {
		merged = map[string]any{}
	}
	for _, field := range entryFields(agent) {
		delete(merged, field)
	}
	for key, value := range build(server) {
		merged[key] = value
	}
	return merged
}
