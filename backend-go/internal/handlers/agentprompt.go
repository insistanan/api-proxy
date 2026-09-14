// agentprompt.go 智能体客户端全局提示词文件读写：
// 为各客户端配置页（Claude Code / GPT(Codex) / OpenCode / pi / DSH）提供
// "读取 + 编辑保存"各自全局提示词文件的统一入口。
//
// 设计要点：
//   - 路径解析复用各客户端已有的环境变量约定（CLAUDE_CONFIG_DIR / CODEX_HOME /
//     OPENCODE_CONFIG(_DIR) / XDG_CONFIG_HOME / PI_AGENT_CONFIG_DIR / DSH_HOME），
//     与各配置 handler 的目录解析保持同一来源，禁止客户端传入任意路径；
//   - 文件不存在不算错误（exists=false），首次保存时创建；
//   - 保存前对原文件做时间戳 .bak 备份（同 writeClaudeCodeSettings 惯例），
//     经 internal/atomicfile 原子替换；
//   - 写入失败（目录只读、权限不足、磁盘满等）显式返回 500 + error/detail，
//     不静默兜底；detail 保留底层错误（含 Windows 只读属性 / ACL 拒绝信息）。
package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/atomicfile"
	"github.com/BenedictKing/api-proxy/internal/piagent"
	"github.com/gin-gonic/gin"
)

// agentPromptContentMaxBytes 单次提交的提示词内容上限（1 MiB），
// 与 config.maxPromptContentByte 口径一致，防止误提交拖垮请求体解析。
const agentPromptContentMaxBytes = 1 << 20

// agentPromptSpec 描述一个智能体客户端的全局提示词文件。
type agentPromptSpec struct {
	// displayName 展示名（卡片标题）。
	displayName string
	// fileName 提示词文件名（各客户端约定不同：CLAUDE.md / AGENTS.md）。
	fileName string
	// resolvePath 解析提示词文件绝对路径；失败返回错误（不静默回退临时目录）。
	resolvePath func() (string, error)
}

// agentPromptSpecs 受支持的智能体清单。key 即路由参数 agent 的取值。
var agentPromptSpecs = map[string]agentPromptSpec{
	"claude-code": {
		displayName: "Claude Code",
		fileName:    "CLAUDE.md",
		resolvePath: resolveClaudeCodePromptPath,
	},
	"codex": {
		displayName: "Codex",
		fileName:    "AGENTS.md",
		resolvePath: resolveCodexPromptPath,
	},
	"opencode": {
		displayName: "OpenCode",
		fileName:    "AGENTS.md",
		resolvePath: resolveOpenCodePromptPath,
	},
	"pi": {
		displayName: "pi",
		fileName:    "AGENTS.md",
		resolvePath: resolvePiPromptPath,
	},
	"dsh": {
		displayName: "DSH",
		fileName:    "AGENTS.md",
		resolvePath: resolveDSHPromptPath,
	},
}

// resolveClaudeCodePromptPath 解析 Claude Code 全局提示词：
// CLAUDE_CONFIG_DIR 指定时取该目录，否则 ~/.claude/CLAUDE.md。
func resolveClaudeCodePromptPath() (string, error) {
	if configDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configDir != "" {
		return absolutePath(filepath.Join(configDir, "CLAUDE.md")), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法解析用户主目录: %w", err)
	}
	return filepath.Join(home, ".claude", "CLAUDE.md"), nil
}

// resolveCodexPromptPath 解析 Codex 全局提示词：CODEX_HOME 指定时取该目录，
// 否则 ~/.codex/AGENTS.md。
func resolveCodexPromptPath() (string, error) {
	configDir := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法解析用户主目录: %w", err)
		}
		configDir = filepath.Join(home, ".codex")
	}
	abs, err := filepath.Abs(configDir)
	if err != nil {
		return "", fmt.Errorf("解析 CODEX_HOME 失败: %w", err)
	}
	return filepath.Join(abs, "AGENTS.md"), nil
}

// resolveOpenCodePromptPath 解析 OpenCode 全局提示词：
// OPENCODE_CONFIG 指向配置文件本体时取其同目录；OPENCODE_CONFIG_DIR /
// XDG_CONFIG_HOME 依次接管；默认 ~/.config/opencode/AGENTS.md。
// 与 resolveOpenCodeConfigPath 的目录推导逻辑保持一致。
func resolveOpenCodePromptPath() (string, error) {
	if configuredPath := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); configuredPath != "" {
		return absolutePath(filepath.Join(filepath.Dir(absolutePath(configuredPath)), "AGENTS.md")), nil
	}
	if configDir := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); configDir != "" {
		return absolutePath(filepath.Join(configDir, "AGENTS.md")), nil
	}
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return absolutePath(filepath.Join(configHome, "opencode", "AGENTS.md")), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法解析用户主目录: %w", err)
	}
	return filepath.Join(home, ".config", "opencode", "AGENTS.md"), nil
}

// resolvePiPromptPath 解析 pi 全局提示词，复用 piagent 的目录解析
// （PI_AGENT_CONFIG_DIR 或 ~/.pi/agent/AGENTS.md，官方文档约定）。
func resolvePiPromptPath() (string, error) {
	dir, err := piagent.ResolveConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "AGENTS.md"), nil
}

// resolveDSHPromptPath 解析 DSH 全局提示词：DSH_HOME 指定时取该目录，
// 否则 ~/.dsh/AGENTS.md（与 resolveDSHHome 同一约定）。
func resolveDSHPromptPath() (string, error) {
	if home := strings.TrimSpace(os.Getenv("DSH_HOME")); home != "" {
		abs, err := filepath.Abs(home)
		if err != nil {
			return "", fmt.Errorf("解析 DSH_HOME 失败: %w", err)
		}
		return filepath.Join(abs, "AGENTS.md"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法解析用户主目录: %w", err)
	}
	return filepath.Join(home, ".dsh", "AGENTS.md"), nil
}

// agentPromptFileResponse GET 的响应体。
type agentPromptFileResponse struct {
	Agent       string `json:"agent"`
	DisplayName string `json:"displayName"`
	Path        string `json:"path"`
	Exists      bool   `json:"exists"`
	// Writable 目标可写性探测结果：目录可建临时文件且（文件存在时）文件本身可写。
	Writable  bool   `json:"writable"`
	Content   string `json:"content"`
	SizeBytes int64  `json:"sizeBytes"`
	// UpdatedAt 文件修改时间（Unix 毫秒，不存在为 0）。
	UpdatedAt int64  `json:"updatedAt"`
	ReadError string `json:"readError,omitempty"`
}

// saveAgentPromptRequest PUT 的请求体。
type saveAgentPromptRequest struct {
	Content string `json:"content"`
}

// GetAgentPrompt 返回指定智能体的全局提示词文件内容与状态。
// 文件不存在返回 exists=false 而非错误；读取失败（如权限不足）显式返回
// readError 字段并同时以 200 透出——页面仍可展示路径与状态，错误文案就地可见。
func GetAgentPrompt() gin.HandlerFunc {
	return func(c *gin.Context) {
		spec, ok := agentPromptSpecs[c.Param("agent")]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("不支持的智能体: %s", c.Param("agent"))})
			return
		}
		path, err := spec.resolvePath()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("解析 %s 提示词文件路径失败: %v", spec.displayName, err)})
			return
		}

		response := agentPromptFileResponse{
			Agent:       c.Param("agent"),
			DisplayName: spec.displayName,
			Path:        path,
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			if !os.IsNotExist(readErr) {
				// 权限不足等真实读取失败：不静默当作"未创建"，透出错误详情。
				response.ReadError = fmt.Sprintf("读取 %s 失败: %v", path, readErr)
			}
		} else {
			response.Exists = true
			response.Content = string(raw)
			response.SizeBytes = int64(len(raw))
			if info, statErr := os.Stat(path); statErr == nil {
				response.UpdatedAt = info.ModTime().UnixMilli()
			}
		}
		response.Writable = checkAgentPromptWritable(path)
		c.JSON(http.StatusOK, response)
	}
}

// SaveAgentPrompt 整体替换指定智能体的全局提示词文件。
// 写入前对已存在的原文件做时间戳 .bak 备份；内容经 atomicfile 原子替换。
func SaveAgentPrompt() gin.HandlerFunc {
	return func(c *gin.Context) {
		spec, ok := agentPromptSpecs[c.Param("agent")]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("不支持的智能体: %s", c.Param("agent"))})
			return
		}
		var req saveAgentPromptRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "提示词保存请求无效"})
			return
		}
		if len(req.Content) > agentPromptContentMaxBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("提示词内容不能超过 %d 字节", agentPromptContentMaxBytes)})
			return
		}

		path, err := spec.resolvePath()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("解析 %s 提示词文件路径失败: %v", spec.displayName, err)})
			return
		}

		// 只读 / 权限不足在写入前先给出可读的错误，避免走到一半留下 .bak。
		if !checkAgentPromptWritable(path) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":  fmt.Sprintf("%s 提示词文件不可写（可能只读或无写入权限）: %s", spec.displayName, path),
				"detail": probeAgentPromptWriteError(path),
			})
			return
		}

		if previous, readErr := os.ReadFile(path); readErr == nil && len(previous) > 0 {
			backup := path + "." + time.Now().Format("20060102-150405.000000000") + ".bak"
			if err := os.WriteFile(backup, previous, 0600); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":  fmt.Sprintf("备份 %s 原提示词失败: %v", spec.displayName, err),
					"detail": err.Error(),
				})
				return
			}
		}

		data := []byte(req.Content)
		if err := atomicfile.Write(path, data, atomicfile.Options{TempPrefix: ".agent-prompt-"}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":  fmt.Sprintf("保存 %s 提示词失败: %v", spec.displayName, err),
				"detail": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "path": path, "sizeBytes": len(data)})
	}
}

// checkAgentPromptWritable 探测提示词文件是否可写。
// 保存经 atomicfile（同目录临时文件 → rename 替换），因此目录必须可建临时文件；
// 文件已存在时还需文件本身可写（Windows 只读属性会让 rename 替换失败）。
// 探测失败一律按不可写处理，由调用方决定如何向用户表达。
func checkAgentPromptWritable(path string) bool {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	probe, err := os.CreateTemp(dir, ".agent-prompt-writable-test-*")
	if err != nil {
		return false
	}
	probe.Close()
	os.Remove(probe.Name())
	if file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600); err == nil {
		file.Close()
		return true
	} else {
		// 文件存在但打不开（只读 / 权限不足）→ 不可写；不存在 → 首次创建，目录可写即可
		return os.IsNotExist(err)
	}
}

// probeAgentPromptWriteError 生成不可写原因的可读描述，用于错误详情展示。
func probeAgentPromptWriteError(path string) string {
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil {
		return fmt.Sprintf("目录不存在或不可访问: %s（%v）", dir, err)
	} else if !info.IsDir() {
		return fmt.Sprintf("路径不是目录: %s", dir)
	}
	if _, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600); err != nil && !os.IsNotExist(err) {
		return fmt.Sprintf("打开文件 %s 失败: %v", path, err)
	}
	if probe, err := os.CreateTemp(dir, ".agent-prompt-writable-test-*"); err != nil {
		return fmt.Sprintf("目录 %s 无法创建临时文件: %v", dir, err)
	} else {
		probe.Close()
		os.Remove(probe.Name())
	}
	return "未知原因，请检查文件属性与运行用户权限"
}
