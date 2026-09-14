package piagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BenedictKing/api-proxy/internal/atomicfile"
)

// ErrLocked 表示 pi-agent（proper-lockfile）正持有目标文件锁。
var ErrLocked = errors.New("pi-agent 正持有配置锁")

// ErrRevisionMismatch 表示磁盘文件已被外部修改，revision 校验失败。
var ErrRevisionMismatch = errors.New("配置文件已被外部修改")

// ErrRevisionRequired 表示修改已存在文件时必须携带读取时的 revision。
var ErrRevisionRequired = errors.New("修改已存在的配置必须提供 revision")

// LockDuration 表示 manager 锁在多大时间内视为有效（超过视为陈旧）。
const lockStaleAfter = 60 * time.Second

// managerLockName 是 api-proxy 写入 pi-agent 配置时使用的互斥锁文件名，
// 放在 pi-agent 配置目录下，避免与 pi-agent 的 per-file proper-lockfile 冲突。
const managerLockName = ".pi-agent-manager.lock"

// acquireManagerLock 以排他方式获取管理锁。若锁已存在且陈旧则回收。
// 返回的 release 函数必须在写入完成后调用。
func acquireManagerLock(configDir string) (release func(), err error) {
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("创建配置目录失败: %w", err)
	}
	lockPath := filepath.Join(configDir, managerLockName)
	deadline := time.Now().Add(5 * time.Second)
	for {
		file, openErr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if openErr == nil {
			fmt.Fprintf(file, "%d\n", time.Now().UnixNano())
			file.Close()
			return func() {
				_ = os.Remove(lockPath)
			}, nil
		}
		if !os.IsExist(openErr) {
			return nil, fmt.Errorf("获取管理锁失败: %w", openErr)
		}
		info, statErr := os.Stat(lockPath)
		if statErr == nil && time.Since(info.ModTime()) > lockStaleAfter {
			// 陈旧锁直接回收，避免死锁
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, ErrLocked
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// checkPiAgentLocks 检测 pi-agent 自身的 proper-lockfile 是否正持有
// models.json / auth.json / settings.json 任一文件锁（锁以 <file>.lock 目录存在表达）。
func checkPiAgentLocks(configDir string) error {
	for _, kind := range allFileKinds {
		lockPath := filepath.Join(configDir, string(kind)+".lock")
		if _, err := os.Stat(lockPath); err == nil {
			return ErrLocked
		}
	}
	return nil
}

// RevisionOf 计算文件的 SHA-256 十六进制摘要。
func RevisionOf(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return SHA256Hex(raw), nil
}

// SHA256Hex 计算字节流的 SHA-256 十六进制摘要。
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReadRaw 读取文件原始内容。文件不存在时返回 (nil, false, nil)。
func ReadRaw(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	return raw, true, nil
}

// ReadJSON 读取并解析 JSON 文件。文件不存在时返回空对象。
// pi-agent 配置不允许注释，因此这里只做标准 JSON 解析。
func ReadJSON(path string) (map[string]json.RawMessage, []byte, bool, error) {
	raw, exists, err := ReadRaw(path)
	if err != nil {
		return nil, raw, exists, err
	}
	if !exists {
		return make(map[string]json.RawMessage), nil, false, nil
	}
	root := make(map[string]json.RawMessage)
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, raw, true, fmt.Errorf("JSON 格式无效: %w", err)
	}
	return root, raw, true, nil
}

// WriteResult 是一次安全写入的结果。
type WriteResult struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
	Backup   string `json:"backup"`
}

// WriteFileAtomic 以"备份 → 同目录临时文件 → 原子替换"的方式写入文件。
// 返回备份路径与新内容 revision。备份写入失败时中止写入。
// 文件替换原语委托 internal/atomicfile（唯一平台实现出处）。
func WriteFileAtomic(path string, data []byte, sensitive bool, backupDir string) (WriteResult, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return WriteResult{}, fmt.Errorf("创建目录失败: %w", err)
	}

	previous, exists, err := ReadRaw(path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("读取现有文件失败: %w", err)
	}

	var backupPath string
	if exists && len(previous) > 0 {
		backupPath, err = writeBackup(path, previous, sensitive, backupDir)
		if err != nil {
			return WriteResult{}, err
		}
	}

	mode := os.FileMode(0644)
	if sensitive {
		mode = 0600
	}
	if err := atomicfile.Write(path, data, atomicfile.Options{
		TempPrefix: ".piagent-",
		Mode:       mode,
	}); err != nil {
		return WriteResult{}, err
	}

	return WriteResult{
		Path:     path,
		Revision: SHA256Hex(data),
		Backup:   backupPath,
	}, nil
}

// writeBackup 将文件旧内容写入备份目录。sensitive 文件使用 0600 权限。
func writeBackup(path string, content []byte, sensitive bool, backupDir string) (string, error) {
	if backupDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	name := fmt.Sprintf("%s.%s.json", filepath.Base(path), time.Now().Format("20060102-150405.000000000"))
	backupPath := filepath.Join(backupDir, name)
	mode := os.FileMode(0600)
	if !sensitive {
		mode = 0644
	}
	if err := os.WriteFile(backupPath, content, mode); err != nil {
		return "", fmt.Errorf("创建备份失败: %w", err)
	}
	return backupPath, nil
}

// MarshalIndent 以两空格缩进序列化 JSON，并保证以换行结尾。
func MarshalIndent(root map[string]json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
