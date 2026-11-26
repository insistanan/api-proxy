package piagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"insistanan/api-proxy/internal/clients/filetxn"
)

// ErrLocked 表示 pi-agent（proper-lockfile）正持有目标文件锁。
var ErrLocked = errors.New("pi-agent 正持有配置锁")

// ErrRevisionMismatch 表示磁盘文件已被外部修改，revision 校验失败。
var ErrRevisionMismatch = errors.New("配置文件已被外部修改")

// ErrRevisionRequired 表示修改已存在文件时必须携带读取时的 revision。
var ErrRevisionRequired = errors.New("修改已存在的配置必须提供 revision")

// 管理锁文件保持原位，由操作系统绑定句柄所有权；进程退出自动释放，禁止按时间删锁。
const managerLockName = ".pi-agent-manager.lock"

func acquireManagerLock(ctx context.Context, configDir string) (func(), error) {
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(configDir, managerLockName), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("打开管理锁失败: %w", err)
	}
	closeFile := func() {
		if err := file.Close(); err != nil {
			log.Printf("[PiAgent-Lock] 关闭管理锁失败: %v", err)
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := waitCtx.Err(); err != nil {
			closeFile()
			return nil, err
		}
		locked, err := tryManagerFileLock(file)
		if err != nil {
			closeFile()
			return nil, fmt.Errorf("获取管理锁失败: %w", err)
		}
		if locked {
			return closeFile, nil
		}
		select {
		case <-waitCtx.Done():
			closeFile()
			return nil, errors.Join(ErrLocked, waitCtx.Err())
		case <-tick.C:
		}
	}
}

// checkPiAgentLocks 检测 pi-agent 自身的 proper-lockfile 是否正持有
// models.json / auth.json / settings.json 任一文件锁（锁以 <file>.lock 目录存在表达）。
// 这是取管理锁前后及提交前的冲突检测；管理锁并未参与官方锁协议，外部交错仍由内容校验拒绝。
func checkPiAgentLocks(configDir string) error {
	for _, kind := range allFileKinds {
		lockPath := filepath.Join(configDir, string(kind)+".lock")
		if _, err := os.Stat(lockPath); err == nil {
			return ErrLocked
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("检查 pi-agent 锁失败: %w", err)
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
	if root == nil {
		return nil, raw, true, fmt.Errorf("配置根节点必须是 JSON 对象，不能是 null")
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
// 文件替换原语委托 internal/storage/atomicfile（唯一平台实现出处）。
func WriteFileAtomic(path string, data []byte, sensitive bool, backupDir string, expected []byte) (WriteResult, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return WriteResult{}, fmt.Errorf("创建目录失败: %w", err)
	}

	previous, exists, err := ReadRaw(path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("读取现有文件失败: %w", err)
	}

	if (previous == nil) != (expected == nil) || !bytes.Equal(previous, expected) {
		return WriteResult{}, ErrRevisionMismatch
	}
	if err := checkPiAgentLocks(dir); err != nil {
		return WriteResult{}, err
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
	if err := filetxn.Replace(path, previous, data, "", mode); err != nil {
		return WriteResult{Path: path, Revision: SHA256Hex(data), Backup: backupPath}, err
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
	return filetxn.Backup(backupDir, filepath.Base(path), content)
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
