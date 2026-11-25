// Package extensions 管理 Skills / MCP 扩展目录的文件迁移，不依赖 HTTP。
package extensions

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

// MigrateSkillsDir 将旧目录内容（含 .git、.metadata、.backups、skills-manifest.json、mcp.json）安全迁移到新目录。
// 返回 migrated 表示是否实际移动了文件，以及在配置保存失败时用于恢复的 rollback 回调。
func MigrateSkillsDir(oldDir, newDir string) (bool, func() error, error) {
	oldClean := filepath.Clean(oldDir)
	newClean := filepath.Clean(newDir)
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
	} else if !isCrossDeviceError(err) {
		return fmt.Errorf("重命名 Skills 目录失败: %w", err)
	}
	return copyThenRemove(src, dst, removeAllForce)
}

func isCrossDeviceError(err error) bool {
	if runtime.GOOS == "windows" {
		return errors.Is(err, syscall.Errno(17)) // ERROR_NOT_SAME_DEVICE
	}
	return errors.Is(err, syscall.EXDEV)
}

// copyThenRemove 的删除依赖可替换，用于确定性验证“源目录部分清理失败”。
func copyThenRemove(src, dst string, removeSource func(string) error) error {
	if err := copyDirRecursive(src, dst); err != nil {
		return errors.Join(fmt.Errorf("复制 Skills 目录失败: %w", err), removeAllForce(dst))
	}
	if err := removeSource(src); err != nil {
		// 删除可能已经部分成功，此时目标是唯一完整副本，绝不能删除目标来“回滚”。
		return fmt.Errorf("清理原 Skills 目录 %s 失败，完整副本保留在 %s，请恢复目录后再操作: %w", src, dst, err)
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
		return errors.Join(err, out.Close())
	}
	if err := out.Close(); err != nil {
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
