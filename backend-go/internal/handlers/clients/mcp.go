package clients

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/admin/mcpsync"
	"github.com/insistanan/api-proxy/internal/admin/piagent"
)

// MCPAPI 提供 MCP 统一列表的读写接口，保存时把启用条目分发到各智能体的配置文件。
// 列表与分发逻辑在 internal/admin/mcpsync；本文件只负责路径解析与 HTTP 语义。
type MCPAPI struct {
	service *mcpsync.Service
}

// NewMCPAPI 创建 MCP 管理 API。listPath 为统一列表文件路径（默认 .config/mcp.json，含密钥）。
func NewMCPAPI(listPath string) *MCPAPI {
	return &MCPAPI{
		service: mcpsync.NewService(
			mcpsync.NewStore(listPath),
			mcpsync.NewClaudeCodeAdapter(resolveClaudeCodeMCPPath),
			mcpsync.NewCodexAdapter(resolveCodexMCPPath),
			mcpsync.NewOpenCodeAdapter(resolveOpenCodeMCPPath, decodeOpenCodeMCPConfig),
			mcpsync.NewPiAdapter(resolvePiMCPPath),
			mcpsync.NewDSHAdapter(resolveDSHMCPPath),
		),
	}
}

type mcpStateResponse struct {
	Revision string                   `json:"revision"`
	Servers  []mcpsync.Server         `json:"servers"`
	Agents   []mcpsync.AgentStatus    `json:"agents"`
	Detected []mcpsync.DetectedServer `json:"detected"`
}

type saveMCPRequest struct {
	Revision  *string            `json:"revision"`
	Servers   []mcpsync.Server   `json:"servers" binding:"required"`
	Adoptions []mcpsync.Adoption `json:"adoptions,omitempty"`
}

// Toggle 只切换一个应用中的一条 MCP，保留其它列表草稿和应用配置。
func (api *MCPAPI) Toggle() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Revision      *string          `json:"revision"`
			AgentRevision *string          `json:"agentRevision"`
			Agent         mcpsync.AgentKey `json:"agent"`
			Server        mcpsync.Server   `json:"server"`
			Enabled       *bool            `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Revision == nil || req.AgentRevision == nil || req.Enabled == nil {
			c.JSON(400, gin.H{"error": "MCP 开关请求缺少版本、应用、配置或启用状态"})
			return
		}
		result, err := api.service.Toggle(mcpsync.ToggleRequest{Revision: *req.Revision, AgentRevision: *req.AgentRevision, Agent: req.Agent, Server: req.Server, Enabled: *req.Enabled})
		if err != nil {
			switch {
			case errors.Is(err, mcpsync.ErrInvalidServers):
				c.JSON(400, gin.H{"error": err.Error()})
			case errors.Is(err, mcpsync.ErrRevisionConflict):
				c.JSON(409, gin.H{"error": err.Error(), "code": "revision_conflict"})
			case errors.Is(err, mcpsync.ErrAgentConflict):
				c.JSON(409, gin.H{"error": err.Error(), "code": "agent_config_conflict"})
			default:
				c.JSON(500, gin.H{"error": err.Error()})
			}
			return
		}
		c.JSON(200, gin.H{"success": true, "revision": result.Revision, "agents": result.Agents})
	}
}

// List 返回统一列表、版本号与各智能体配置文件的状态。
func (api *MCPAPI) List() gin.HandlerFunc {
	return func(c *gin.Context) {
		state, err := api.service.State()
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 MCP 列表失败: %v", err)})
			return
		}
		c.JSON(200, mcpStateResponse{Revision: state.Revision, Servers: state.Servers, Agents: state.Agents, Detected: state.Detected})
	}
}

// Save 校验并保存统一列表，然后分发到各智能体。
// 列表已保存但某个智能体写入失败时仍返回 200，由 agents[].ok 表达；
// 智能体配置无法解析时整体中止，不写入任何文件。
func (api *MCPAPI) Save() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req saveMCPRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "MCP 配置请求无效"})
			return
		}
		if req.Revision == nil {
			c.JSON(428, gin.H{"error": "保存需要列表版本，请先刷新", "code": "revision_required"})
			return
		}

		result, err := api.service.Save(*req.Revision, req.Servers, req.Adoptions...)
		if err != nil {
			switch {
			case errors.Is(err, mcpsync.ErrInvalidServers):
				c.JSON(400, gin.H{"error": err.Error()})
			case errors.Is(err, mcpsync.ErrRevisionConflict):
				c.JSON(409, gin.H{"error": err.Error(), "code": "revision_conflict"})
			case errors.Is(err, mcpsync.ErrAgentConflict):
				c.JSON(409, gin.H{"error": err.Error(), "code": "agent_config_conflict"})
			case errors.Is(err, mcpsync.ErrAgentPrepare):
				c.JSON(500, gin.H{"error": err.Error(), "code": "agent_config_invalid"})
			default:
				c.JSON(500, gin.H{"error": err.Error()})
			}
			return
		}
		c.JSON(200, gin.H{"success": true, "revision": result.Revision, "agents": result.Agents})
	}
}

// resolveClaudeCodeMCPPath 返回 Claude Code 用户级配置 .claude.json 的路径。
// MCP 条目位于该文件的 mcpServers 中，settings.json 不承载 MCP。CLAUDE_CONFIG_DIR 生效方式以第三方资料为准，待实机确认。
func resolveClaudeCodeMCPPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return absolutePath(filepath.Join(dir, ".claude.json")), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户目录失败: %w", err)
	}
	return filepath.Join(home, ".claude.json"), nil
}

// resolveCodexMCPPath 复用 Codex 的路径解析（CODEX_HOME 或 ~/.codex）。
func resolveCodexMCPPath() (string, error) {
	configPath, _, _, err := resolveCodexPaths()
	return configPath, err
}

// resolveOpenCodeMCPPath 复用 OpenCode 配置路径解析。
func resolveOpenCodeMCPPath() (string, error) {
	return resolveOpenCodeConfigPath(), nil
}

func resolvePiMCPPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return absolutePath(filepath.Join(dir, "mcp.json")), nil
	}
	dir, err := piagent.ResolveConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mcp.json"), nil
}

func resolveDSHMCPPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("DSH_HOME")); dir != "" {
		return absolutePath(filepath.Join(dir, "cordis.patch.yml")), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析 DSH 用户目录失败: %w", err)
	}
	return filepath.Join(home, ".dsh", "cordis.patch.yml"), nil
}

// decodeOpenCodeMCPConfig 解析 OpenCode 的 JSON/JSONC 配置，去注释规则与 opencode.go 一致。
func decodeOpenCodeMCPConfig(raw []byte) (map[string]any, error) {
	cleaned, err := normalizeJSONC(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(cleaned, &root); err != nil {
		return nil, fmt.Errorf("JSON/JSONC 格式无效: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("顶层必须是 JSON 对象")
	}
	return root, nil
}
