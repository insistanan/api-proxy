// Package extensions 管理 Skills / MCP 扩展目录的文件迁移，不依赖 HTTP。
package extensions

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"insistanan/api-proxy/internal/clients/filetxn"
	"insistanan/api-proxy/internal/infra/config"
)

// MigrateSkillsDir 将旧目录内容（含 .git、.metadata、.backups、skills-manifest.json、mcp.json）完整复制到新目录，配置提交前始终保留源目录。
// 返回 migrated 表示是否已发布目标副本，以及在配置保存失败时用于恢复的 rollback 回调。
func MigrateSkillsDir(oldDir, newDir string) (bool, func() error, error) {
	oldClean, err := filetxn.Identity(oldDir)
	if err != nil {
		return false, nil, err
	}
	newClean, err := filetxn.Identity(newDir)
	if err != nil {
		return false, nil, err
	}
	if config.SamePath(oldClean, newClean) {
		return false, nil, nil
	}
	if isNestedPath(oldClean, newClean) || isNestedPath(newClean, oldClean) {
		return false, nil, &config.ConfigError{Message: "新旧 Skills 目录不能存在包含关系"}
	}

	oldInfo, err := os.Stat(oldClean)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("检查原 Skills 目录失败: %w", err)
	}
	if !oldInfo.IsDir() {
		return false, nil, &config.ConfigError{Message: "原 Skills 路径不是目录"}
	}
	oldEntries, err := os.ReadDir(oldClean)
	if err != nil {
		return false, nil, fmt.Errorf("读取原 Skills 目录失败: %w", err)
	}
	if len(oldEntries) == 0 {
		return false, nil, nil
	}
	if resumed, rollback, err := resumePreparedMigration(oldClean, newClean); resumed || err != nil {
		return resumed, rollback, err
	}

	newInfo, err := os.Stat(newClean)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, nil, fmt.Errorf("检查目标 Skills 目录失败: %w", err)
	}
	if err == nil {
		if !newInfo.IsDir() {
			return false, nil, &config.ConfigError{Message: "目标 Skills 路径已存在且不是目录"}
		}
		newEntries, readErr := os.ReadDir(newClean)
		if readErr != nil {
			return false, nil, fmt.Errorf("读取目标 Skills 目录失败: %w", readErr)
		}
		if len(newEntries) > 0 {
			return false, nil, &config.ConfigError{Message: "目标 Skills 目录已存在且非空，请先清空或更换目录"}
		}
		// 移除已存在的空目标目录，确保同卷 Rename 与跨卷拷贝行为一致。
		if removeErr := os.Remove(newClean); removeErr != nil {
			return false, nil, fmt.Errorf("清理空目标目录失败: %w", removeErr)
		}
	}

	return prepareMigration(oldClean, newClean)
}

func isNestedPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
			return fmt.Errorf("迁移不支持符号链接: %s", current)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("迁移不支持特殊文件: %s", current)
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
		return errors.Join(err, out.Close())
	}
	if err := errors.Join(out.Sync(), out.Close()); err != nil {
		return err
	}
	if mode&0200 == 0 {
		return os.Chmod(dst, mode)
	}
	return nil
}

// removeAllForce 先清理只读属性（例如 Windows 下 .git/objects/pack/*），再递归删除目录。
func removeAllForce(root string) error {
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(current, info.Mode().Perm()|0200)
	}); err != nil {
		return fmt.Errorf("清理目录只读属性失败: %w", err)
	}
	return os.RemoveAll(root)
}
