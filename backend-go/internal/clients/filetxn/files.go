// Package filetxn 协调客户端文件的版本检查、备份与提交。
package filetxn

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"insistanan/api-proxy/internal/storage/atomicfile"
)

var ErrConflict = errors.New("文件已被修改，请刷新后重试")
var ErrRevisionRequired = errors.New("必须提供读取时的文件 revision")

var fileLocks = struct {
	sync.Mutex
	entries map[string]*fileLock
}{entries: make(map[string]*fileLock)}

type fileLock struct {
	mu   sync.Mutex
	refs int
}

// Read 区分不存在（nil）与存在但为空的文件。
func Read(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

func Revision(raw []byte) string {
	if raw == nil {
		return "missing"
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// RevisionSet 将多个文件的存在性和内容一起纳入编辑快照。
func RevisionSet(files ...[]byte) string {
	hash := sha256.New()
	for _, raw := range files {
		fmt.Fprintf(hash, "%s\n", Revision(raw))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func CheckRevision(expected string, raw []byte) error {
	if expected == "" {
		return ErrRevisionRequired
	}
	if expected != Revision(raw) {
		return ErrConflict
	}
	return nil
}

func lock(path string) (func(), error) {
	identity, err := Identity(path)
	if err != nil {
		return nil, err
	}
	fileLocks.Lock()
	entry := fileLocks.entries[identity]
	if entry == nil {
		entry = &fileLock{}
		fileLocks.entries[identity] = entry
	}
	entry.refs++
	fileLocks.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		fileLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(fileLocks.entries, identity)
		}
		fileLocks.Unlock()
	}, nil
}

// Replace 的比较与替换共用进程内文件锁，防止不同客户端服务互相覆盖。
// 外部编辑不参与本锁，临时文件写完后再检查一次；不能把它当作跨进程事务。
func Replace(path string, previous, next []byte, backupDir string, mode os.FileMode) error {
	resolved, err := resolvePath(path)
	if err != nil {
		return err
	}
	// 读写使用同一真实目标，避免读取链接目标后却把链接本身替换成普通文件。
	path = resolved
	release, err := lock(path)
	if err != nil {
		return err
	}
	defer release()
	check := func() error {
		current, err := Read(path)
		if err != nil {
			return fmt.Errorf("检查文件版本失败: %w", err)
		}
		if (current == nil) != (previous == nil) || !bytes.Equal(current, previous) {
			return ErrConflict
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	if previous != nil && backupDir != "" {
		if _, err := Backup(backupDir, filepath.Base(path), previous); err != nil {
			return err
		}
	}
	return atomicfile.Write(path, next, atomicfile.Options{TempPrefix: ".client-", Mode: mode, BeforeReplace: check})
}

// Backup 用排他创建生成名称，避免同秒保存覆盖备份。
func Backup(dir, name string, raw []byte) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	file, err := os.CreateTemp(dir, name+".*.bak")
	if err != nil {
		return "", fmt.Errorf("创建备份失败: %w", err)
	}
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return "", fmt.Errorf("保存备份 %s 失败: %w", file.Name(), err)
	}
	return file.Name(), nil
}
