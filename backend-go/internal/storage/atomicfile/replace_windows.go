//go:build windows

package atomicfile

import "golang.org/x/sys/windows"

// replaceFile 复用与 piagent 相同的 Windows 替换方式：
// MoveFileEx(REPLACE_EXISTING | WRITE_THROUGH)。
func replaceFile(tempPath, targetPath string) error {
	return windows.MoveFileEx(
		windows.StringToUTF16Ptr(tempPath),
		windows.StringToUTF16Ptr(targetPath),
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

// syncDir 在 Windows 上为显式 no-op：NTFS 不提供目录 fsync（返回
// Access denied），替换的持久性由 MoveFileEx 的 WRITE_THROUGH 承担。
// 这是"平台不支持"的显式处理，不是吞掉任意同步错误；Unix 实现仍显式
// 返回失败（见 replace_unix.go）。
func syncDir(dir string) error {
	return nil
}
