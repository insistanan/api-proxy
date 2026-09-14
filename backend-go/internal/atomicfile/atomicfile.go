// Package atomicfile 提供最小的原子文件写入原语：
// 同目录临时文件 → 完整写入 → 同步 → 平台替换 → 目录同步。
//
// 它只负责"文件内容以原子方式替换"这一件事；备份策略、revision、跨进程
// 锁、备份命名约定等业务语义留在各使用方（piagent / config）。
// 不复制出第三套平台替换代码，也不让 config 反向依赖 piagent。
//
// 提交语义（R3）：
//   - Write 返回 nil 之前发生的任何失败（临时文件创建/写入/同步/关闭、替换）
//     都保证目标文件保持原样，本次临时文件被清理；
//   - 替换（rename / MoveFileEx）是提交点；
//   - 替换成功后的目录同步失败以 CommittedError 返回：内存/调用方状态
//     与已替换的新文件一致，不得回滚伪装未提交。
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Phase 标识提交流程中失败的阶段。
type Phase string

const (
	PhaseTempCreate Phase = "temp_create"
	PhaseTempWrite  Phase = "temp_write"
	PhaseTempSync   Phase = "temp_sync"
	PhaseTempClose  Phase = "temp_close"
	PhaseReplace    Phase = "replace"
	PhaseDirSync    Phase = "dir_sync"
)

// CommittedError 表示提交点（文件替换）已发生之后的失败。
// Committed=true 时目标文件已经是新内容：调用方不得把该错误当作
// "未提交"重试（否则一次新增会被重试成两次）；应读取实际状态后
// 向用户表达"已提交但收尾失败"。
type CommittedError struct {
	Phase Phase
	Err   error
}

func (e *CommittedError) Error() string {
	return fmt.Sprintf("原子写入已提交但收尾失败（阶段 %s）: %v", e.Phase, e.Err)
}

func (e *CommittedError) Unwrap() error { return e.Err }

// IsCommitted 判断错误是否为"已提交"错误（可用 errors.As 提取详情）。
func IsCommitted(err error) bool {
	var committed *CommittedError
	return errors.As(err, &committed)
}

// Options 控制一次原子写入的行为。
type Options struct {
	// TempPrefix 临时文件名前缀（同目录，"." 开头避免被 watcher 当作
	// 正式文件）。默认 ".atomicfile-"。
	TempPrefix string
	// Mode 新文件权限；零值取 0600（配置文件默认含密钥，按受限权限）。
	Mode os.FileMode
}

// Write 以原子方式把 data 写入 path。
// 步骤：建目录（0700）→ 同目录临时文件 → 写入 → Sync → Close → 平台替换
// → 目录同步。替换前任何失败都清理临时文件并返回普通错误（未提交）；
// 替换后的目录同步失败返回 *CommittedError。
func Write(path string, data []byte, opts Options) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	prefix := opts.TempPrefix
	if prefix == "" {
		prefix = ".atomicfile-"
	}
	mode := opts.Mode
	if mode == 0 {
		mode = 0o600
	}

	temp, err := os.CreateTemp(dir, prefix+"*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath) // 替换成功后为 no-op

	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return fmt.Errorf("设置临时文件权限失败: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("同步临时文件失败: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}

	if err := replaceFile(tempPath, path); err != nil {
		return fmt.Errorf("替换目标文件失败: %w", err)
	}

	// 提交点已过：目录同步失败不再能撤销替换。
	if err := syncDir(dir); err != nil {
		return &CommittedError{Phase: PhaseDirSync, Err: err}
	}
	return nil
}
