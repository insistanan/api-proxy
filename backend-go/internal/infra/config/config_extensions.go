package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ExtensionsSettings 管理项目 Skills / MCP 统一目录与 Git 远程仓库地址。
// SkillsDir 保存时仅接受绝对路径或空值；空值使用 <workspace>/skills。
// 运行时仍兼容已保存的 "skills"、相对路径与 ~ 路径。
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
// 为兼容旧配置，仍解析 "skills"、相对路径与 ~ 路径；绝对路径限制仅用于保存校验。
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

// MigrateSkillsDir 将旧目录内容（含 .git、.metadata、.backups、skills-manifest.json、mcp.json）安全迁移到新目录。
// 返回 migrated 表示是否实际移动了文件，以及在配置保存失败时用于恢复的 rollback 回调。
func MigrateSkillsDir(oldDir, newDir string) (bool, func() error, error) {
	oldClean := filepath.Clean(oldDir)
	newClean := filepath.Clean(newDir)
	if SamePath(oldClean, newClean) {
		return false, nil, nil
	}
	if isNestedPath(oldClean, newClean) || isNestedPath(newClean, oldClean) {
		return false, nil, &ConfigError{Message: "新旧 Skills 目录不能存在包含关系"}
	}

	oldInfo, err := os.Stat(oldClean)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("检查原 Skills 目录失败: %w", err)
	}
	if !oldInfo.IsDir() {
		return false, nil, &ConfigError{Message: "原 Skills 路径不是目录"}
	}
	oldEntries, err := os.ReadDir(oldClean)
	if err != nil {
		return false, nil, fmt.Errorf("读取原 Skills 目录失败: %w", err)
	}
	if len(oldEntries) == 0 {
		return false, nil, nil
	}

	newInfo, err := os.Stat(newClean)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, nil, fmt.Errorf("检查目标 Skills 目录失败: %w", err)
	}
	if err == nil {
		if !newInfo.IsDir() {
			return false, nil, &ConfigError{Message: "目标 Skills 路径已存在且不是目录"}
		}
		newEntries, readErr := os.ReadDir(newClean)
		if readErr != nil {
			return false, nil, fmt.Errorf("读取目标 Skills 目录失败: %w", readErr)
		}
		if len(newEntries) > 0 {
			return false, nil, &ConfigError{Message: "目标 Skills 目录已存在且非空，请先清空或更换目录"}
		}
		// 移除已存在的空目标目录，确保同卷 Rename 与跨卷拷贝行为一致。
		if removeErr := os.Remove(newClean); removeErr != nil {
			return false, nil, fmt.Errorf("清理空目标目录失败: %w", removeErr)
		}
	}

	if err := moveDirSafe(oldClean, newClean); err != nil {
		return false, nil, err
	}
	rollback := func() error {
		return moveDirSafe(newClean, oldClean)
	}
	return true, rollback, nil
}

func isNestedPath(parent, child string) bool {
	if runtime.GOOS == "windows" {
		parent = strings.ToLower(parent)
		child = strings.ToLower(child)
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func moveDirSafe(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return fmt.Errorf("创建目标父目录失败: %w", err)
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyDirRecursive(src, dst); err != nil {
		_ = removeAllForce(dst)
		return fmt.Errorf("迁移 Skills 目录失败: %w", err)
	}
	if err := removeAllForce(src); err != nil {
		_ = removeAllForce(dst)
		return fmt.Errorf("清理原 Skills 目录失败: %w", err)
	}
	return nil
}

func copyDirRecursive(src, dst string) error {
	return filepath.WalkDir(src, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, current)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(current)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0700)
		}
		return copyFileWithMode(current, target, info.Mode().Perm())
	})
}

func copyFileWithMode(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if mode&0200 == 0 {
		_ = os.Chmod(dst, mode)
	}
	return nil
}

// removeAllForce 先清理只读属性（例如 Windows 下 .git/objects/pack/*），再递归删除目录。
func removeAllForce(root string) error {
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	_ = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			_ = os.Chmod(current, info.Mode().Perm()|0200)
		}
		return nil
	})
	return os.RemoveAll(root)
}
