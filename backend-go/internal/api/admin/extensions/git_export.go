package extensions

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"insistanan/api-proxy/internal/clients/skills"
)

type exportPlan struct {
	Files    []string
	Revision string
}

var legacyBackupDirectory = regexp.MustCompile(`^\d{8}-\d{6}\.\d+$`)

// 仅导出 Skill 资源及启用清单。本机凭据、托管状态、元数据与翻译备份不属于导出格式。
func shareableExtensionPath(path string) bool {
	if path == "skills-manifest.json" || path == ".gitignore" {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || !skills.ValidName(parts[0]) {
		return false
	}
	// 旧版备份位于 Skill 根目录；普通日期文件和其他资源名不按形状猜测。
	if len(parts) > 2 && legacyBackupDirectory.MatchString(parts[1]) {
		return false
	}
	for _, part := range parts[1:] {
		lower := strings.ToLower(part)
		if part == "" || strings.HasPrefix(part, ".") || strings.ContainsAny(part, "\\:") {
			return false
		}
		if lower == "mcp.json" || lower == "auth.json" || lower == "credentials.json" || lower == "credentials.yaml" || lower == "credentials.yml" {
			return false
		}
	}
	return true
}

func prepareExport(ctx context.Context, directory string) (exportPlan, error) {
	tracked, err := runGit(ctx, directory, "ls-files", "-z", "--cached")
	if err != nil {
		return exportPlan{}, err
	}
	for _, path := range strings.Split(tracked, "\x00") {
		if path != "" && !shareableExtensionPath(path) {
			return exportPlan{}, fmt.Errorf("已跟踪的文件不属于可分享范围: %s；请先在本地处理索引及相关历史", path)
		}
	}
	all, err := runGit(ctx, directory, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return exportPlan{}, err
	}
	unique := map[string]bool{}
	for _, path := range strings.Split(all, "\x00") {
		if path != "" && shareableExtensionPath(path) {
			unique[path] = true
		}
	}
	plan := exportPlan{}
	for path := range unique {
		plan.Files = append(plan.Files, path)
	}
	sort.Strings(plan.Files)
	hash := sha256.New()
	for _, path := range plan.Files {
		fmt.Fprintf(hash, "%d:%s\x00", len(path), path)
		info, err := os.Lstat(filepath.Join(directory, filepath.FromSlash(path)))
		if errors.Is(err, fs.ErrNotExist) {
			hash.Write([]byte("deleted\x00"))
			continue
		}
		if err != nil {
			return exportPlan{}, err
		}
		if !info.Mode().IsRegular() {
			return exportPlan{}, fmt.Errorf("导出不支持链接或特殊文件: %s", path)
		}
		raw, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(path)))
		if err != nil {
			return exportPlan{}, err
		}
		sum := sha256.Sum256(raw)
		hash.Write(sum[:])
		fmt.Fprintf(hash, ":%o\x00", info.Mode().Perm()&0111)
	}
	head, err := runGit(ctx, directory, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return exportPlan{}, err
		}
	} else {
		hash.Write([]byte(head))
	}
	plan.Revision = fmt.Sprintf("%x", hash.Sum(nil))
	return plan, nil
}

// 检查新推送的每一版树；仅检查当前索引会漏掉先提交凭据、再删除的历史。
func validateExportHistory(ctx context.Context, directory string) error {
	if _, err := runGit(ctx, directory, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}
	commits, err := runGit(ctx, directory, "rev-list", "HEAD", "--not", "--remotes=origin")
	if err != nil {
		return err
	}
	for _, commit := range strings.Fields(commits) {
		paths, err := runGit(ctx, directory, "ls-tree", "-r", "--name-only", "-z", commit)
		if err != nil {
			return err
		}
		for _, path := range strings.Split(paths, "\x00") {
			if path != "" && !shareableExtensionPath(path) {
				return fmt.Errorf("待推送历史 %s 包含不可导出的文件 %s，请先在本地清理历史", commit, path)
			}
		}
	}
	return nil
}

func stageExport(ctx context.Context, directory string, plan exportPlan) error {
	if len(plan.Files) == 0 {
		return nil
	}
	command := exec.CommandContext(ctx, "git", "--literal-pathspecs", "add", "-A", "--pathspec-from-file=-", "--pathspec-file-nul")
	command.Dir = directory
	command.Stdin = strings.NewReader(strings.Join(plan.Files, "\x00") + "\x00")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("暂存导出清单失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
