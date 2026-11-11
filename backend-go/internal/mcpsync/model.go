// Package mcpsync 维护 MCP 服务器的统一列表，并把其中启用的条目分发到各智能体的用户级配置文件。
//
// 本包包含数据模型、配置发现、列表存储与分发，不解析配置路径，也不处理 HTTP；
// 路径解析与接口层位于 handlers/mcp.go。
//
// 每个应用的托管范围仅包含该应用在上次或本次被勾选的名称；
// 从未在该应用接管的其它条目保持原样，同名内容冲突时拒绝接管。
package mcpsync

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
)

// AgentKey 标识一个接收 MCP 分发的智能体。
type AgentKey string

const (
	AgentClaudeCode AgentKey = "claude-code"
	AgentCodex      AgentKey = "codex"
	AgentOpenCode   AgentKey = "opencode"
	AgentPi         AgentKey = "pi"
	AgentDSH        AgentKey = "dsh"
)

// Transport 是列表中统一使用的传输方式。stdio 启动本地进程，http 连接远程地址。
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
)

// Server 是列表中的一条 MCP 服务器。stdio 只使用 Command/Args/Env，http 只使用 URL/Headers。
type Server struct {
	Name         string                  `json:"name"`
	Transport    Transport               `json:"transport"`
	Command      string                  `json:"command,omitempty"`
	Args         []string                `json:"args,omitempty"`
	Env          map[string]string       `json:"env,omitempty"`
	URL          string                  `json:"url,omitempty"`
	Headers      map[string]string       `json:"headers,omitempty"`
	Agents       []AgentKey              `json:"agents"`
	AgentConfigs map[AgentKey]Connection `json:"agentConfigs,omitempty"`
}

// Connection 保存应用原有的连接方式，避免合并展示后覆盖各应用的启动包装。
type Connection struct {
	Transport Transport         `json:"transport"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// ConfigForAgent 返回实际写入应用的配置，不携带其它应用的连接设置。
func ConfigForAgent(server Server, agent AgentKey) Server {
	if config, ok := server.AgentConfigs[agent]; ok {
		return Server{Name: server.Name, Transport: config.Transport, Command: config.Command,
			Args: config.Args, Env: config.Env, URL: config.URL, Headers: config.Headers, Agents: server.Agents}
	}
	server.AgentConfigs = nil
	return server
}

// 名称会直接作为 Codex TOML 表名的一部分，因此只允许裸键字符。
var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// IsKnownAgent 判断智能体标识是否受支持。
func IsKnownAgent(agent AgentKey) bool {
	switch agent {
	case AgentClaudeCode, AgentCodex, AgentOpenCode, AgentPi, AgentDSH:
		return true
	default:
		return false
	}
}

// ValidateServers 检查名称唯一性，以及每条记录的字段组合是否与传输方式一致。
func ValidateServers(servers []Server) error {
	seen := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		if err := server.validate(); err != nil {
			return err
		}
		if _, dup := seen[server.Name]; dup {
			return fmt.Errorf("MCP 名称重复: %s", server.Name)
		}
		seen[server.Name] = struct{}{}
	}
	return nil
}

func (s Server) validate() error {
	if !namePattern.MatchString(s.Name) {
		return fmt.Errorf("MCP 名称无效（仅允许字母、数字、下划线和连字符，最长 64 位）: %q", s.Name)
	}
	switch s.Transport {
	case TransportStdio:
		if s.Command == "" {
			return fmt.Errorf("MCP %s 使用 stdio 传输，但缺少 command", s.Name)
		}
		if s.URL != "" || len(s.Headers) > 0 {
			return fmt.Errorf("MCP %s 使用 stdio 传输，不能设置 url 或 headers", s.Name)
		}
	case TransportHTTP:
		parsed, err := url.Parse(s.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("MCP %s 的 url 必须是带主机名的 http 或 https 地址", s.Name)
		}
		if s.Command != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return fmt.Errorf("MCP %s 使用 http 传输，不能设置 command、args 或 env", s.Name)
		}
	default:
		return fmt.Errorf("MCP %s 的传输方式无效: %q", s.Name, s.Transport)
	}
	for key := range s.Env {
		if key == "" {
			return fmt.Errorf("MCP %s 的环境变量名不能为空", s.Name)
		}
	}
	for key := range s.Headers {
		if key == "" {
			return fmt.Errorf("MCP %s 的请求头名称不能为空", s.Name)
		}
	}
	seenAgents := make(map[AgentKey]struct{}, len(s.Agents))
	for _, agent := range s.Agents {
		if !IsKnownAgent(agent) {
			return fmt.Errorf("MCP %s 指定了未知的智能体: %q", s.Name, agent)
		}
		if _, dup := seenAgents[agent]; dup {
			return fmt.Errorf("MCP %s 重复指定了智能体: %s", s.Name, agent)
		}
		seenAgents[agent] = struct{}{}
	}
	for agent := range s.AgentConfigs {
		if !IsKnownAgent(agent) {
			return fmt.Errorf("MCP %s 指定了未知的应用配置: %q", s.Name, agent)
		}
		config := ConfigForAgent(s, agent)
		config.Agents = nil
		if err := config.validate(); err != nil {
			return fmt.Errorf("MCP %s 的 %s 配置无效: %w", s.Name, agent, err)
		}
	}
	return nil
}

// DesiredFor 返回 agent 应当启用的条目，键为 MCP 名称。
func DesiredFor(servers []Server, agent AgentKey) map[string]Server {
	desired := make(map[string]Server)
	for _, server := range servers {
		if slices.Contains(server.Agents, agent) {
			desired[server.Name] = ConfigForAgent(server, agent)
		}
	}
	return desired
}
