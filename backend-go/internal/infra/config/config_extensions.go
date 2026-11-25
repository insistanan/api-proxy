package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ExtensionsSettings 管理项目 Skills / MCP 统一目录与 Git 远程仓库地址。
// SkillsDir 保存时仅接受绝对路径或空值；空值使用 <workspace>/skills。
// 加载阶段将旧的相对路径与 ~ 路径转换为绝对路径后回写。
// 该目录同时存储包含 env/headers 密钥的 mcp.json，配置远程仓库时应使用私有仓库。
type ExtensionsSettings struct {
	SkillsDir     string `json:"skillsDir"`
	SkillsRepoURL string `json:"skillsRepoUrl"`
}

// WorkspaceRoot 返回工作区根目录；在 backend-go/ 子目录启动时回退到上一级目录。
func WorkspaceRoot() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("获取工作目录失败: %w", err)
	}
	workingDir = filepath.Clean(workingDir)
	if filepath.Base(workingDir) == "backend-go" {
		workingDir = filepath.Dir(workingDir)
	}
	return workingDir, nil
}

// NormalizeExtensionsSettings 清理扩展设置首尾空白。
func NormalizeExtensionsSettings(settings ExtensionsSettings) ExtensionsSettings {
	settings.SkillsDir = strings.TrimSpace(settings.SkillsDir)
	settings.SkillsRepoURL = strings.TrimSpace(settings.SkillsRepoURL)
	return settings
}

// ValidateExtensionsSettings 校验待保存的项目 Skills 绝对路径（允许留空）与 Git 仓库地址。
func ValidateExtensionsSettings(settings ExtensionsSettings) error {
	settings = NormalizeExtensionsSettings(settings)
	if strings.ContainsAny(settings.SkillsDir, "\r\n\x00") {
		return &ConfigError{Message: "Skills 目录路径不能包含控制字符"}
	}
	if settings.SkillsDir != "" {
		if !filepath.IsAbs(settings.SkillsDir) {
			return &ConfigError{Message: "Skills 目录必须填写绝对路径；留空使用工作区下的 skills/"}
		}
		resolved, err := ResolveSkillsDir(settings.SkillsDir)
		if err != nil {
			return err
		}
		if resolved == filepath.Dir(resolved) {
			return &ConfigError{Message: "Skills 目录不能设为文件系统根目录"}
		}
	}
	if settings.SkillsRepoURL != "" {
		if err := ValidateGitRepoURL(settings.SkillsRepoURL); err != nil {
			return err
		}
	}
	return nil
}

// ValidateGitRepoURL 校验 Git 远程仓库地址，拒绝空白、控制字符与以 "-" 开头的参数注入。
func ValidateGitRepoURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.ContainsAny(raw, " \t\r\n\x00") {
		return &ConfigError{Message: "Git 仓库地址不能包含空白或控制字符"}
	}
	if strings.HasPrefix(raw, "-") {
		return &ConfigError{Message: "Git 仓库地址不能以 '-' 开头"}
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return &ConfigError{Message: "Git 仓库地址格式无效"}
		}
		switch strings.ToLower(parsed.Scheme) {
		case "https", "http", "ssh", "git":
			if parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
				return &ConfigError{Message: "Git 仓库地址必须包含主机与仓库路径"}
			}
			return nil
		case "file":
			if parsed.Path == "" {
				return &ConfigError{Message: "file:// Git 仓库地址缺少路径"}
			}
			return nil
		default:
			return &ConfigError{Message: fmt.Sprintf("不支持的 Git 仓库协议 %q", parsed.Scheme)}
		}
	}
	// 支持 SCP 风格 SSH 地址（如 git@github.com:owner/repo.git）
	if at := strings.IndexByte(raw, '@'); at > 0 {
		rest := raw[at+1:]
		if colon := strings.IndexByte(rest, ':'); colon > 0 && colon < len(rest)-1 {
			return nil
		}
	}
	return &ConfigError{Message: "Git 仓库地址格式无效，请使用 https://、ssh:// 或 git@host:owner/repo.git"}
}

// ResolveSkillsDir 在运行时将 SkillsDir 解析为干净的绝对路径；空值使用 <workspaceRoot>/skills。
// 同时供加载阶段转换旧的相对路径与 ~ 路径；非空配置保存时必须是绝对路径。
func ResolveSkillsDir(skillsDir string) (string, error) {
	trimmed := strings.TrimSpace(skillsDir)
	if trimmed == "" {
		trimmed = "skills"
	}
	if strings.ContainsAny(trimmed, "\r\n\x00") {
		return "", &ConfigError{Message: "Skills 目录路径不能包含控制字符"}
	}
	if strings.HasPrefix(trimmed, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("解析用户主目录失败: %w", err)
		}
		if trimmed == "~" {
			trimmed = home
		} else if strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, `~\`) {
			trimmed = filepath.Join(home, trimmed[2:])
		}
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed), nil
	}
	root, err := WorkspaceRoot()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(root, trimmed)), nil
}

// SamePath 判断两个已解析路径在当前平台上是否指向同一位置。
func SamePath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}
