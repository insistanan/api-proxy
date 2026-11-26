package filetxn

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"

	"insistanan/api-proxy/internal/storage/atomicfile"
)

type Change struct {
	Path     string
	Previous []byte
	Next     []byte
	NoBackup bool
}

// CommitError 明确列出已替换文件，调用方不能将多文件部分成功当作未提交。
type CommitError struct {
	Files []string
	Err   error
}

func (e *CommitError) Error() string {
	return fmt.Sprintf("文件操作部分提交（%v）: %v", e.Files, e.Err)
}
func (e *CommitError) Unwrap() error { return e.Err }

func Committed(err error) bool {
	var partial *CommitError
	return atomicfile.IsCommitted(err) || errors.As(err, &partial)
}

// Commit 先检查全部文件，再依调用方指定的依赖顺序发布。
// 它不承诺跨文件原子性：失败保留实际已提交清单，禁止用旧内容盲目回滚外部修改。
func Commit(changes ...Change) error {
	for _, change := range changes {
		current, err := Read(change.Path)
		if err != nil {
			return err
		}
		if (current == nil) != (change.Previous == nil) || !bytes.Equal(current, change.Previous) {
			return fmt.Errorf("%s: %w", change.Path, ErrConflict)
		}
	}
	var completed []string
	for _, change := range changes {
		if (change.Previous == nil) == (change.Next == nil) && bytes.Equal(change.Previous, change.Next) {
			continue
		}
		backupDir := filepath.Dir(change.Path)
		if change.NoBackup {
			backupDir = ""
		}
		err := Replace(change.Path, change.Previous, change.Next, backupDir, 0600)
		if err == nil || atomicfile.IsCommitted(err) {
			completed = append(completed, change.Path)
		}
		if err != nil {
			if len(completed) > 0 {
				return &CommitError{Files: completed, Err: err}
			}
			return err
		}
	}
	return nil
}
