//go:build !windows

package atomicfile

import (
	"fmt"
	"os"
)

// replaceFile 用同目录 rename 原子替换目标文件。
func replaceFile(tempPath, targetPath string) error {
	return os.Rename(tempPath, targetPath)
}

// syncDir 同步目录项，使 rename 的元数据变更尽量持久化。
// Unix 支持目录 fsync：失败显式返回（调用方以已提交错误表达实际状态）。
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("打开目录同步失败: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("同步目录失败: %w", err)
	}
	return nil
}
