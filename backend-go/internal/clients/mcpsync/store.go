package mcpsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"

	"github.com/insistanan/api-proxy/internal/storage/atomicfile"
)

// storeVersion 是统一列表文件的格式版本。读取版本不匹配的文件时直接报错，不尝试猜测结构。
const storeVersion = 1

// Store 持久化统一的 MCP 列表（默认 .config/mcp.json）。
// 文件包含 MCP 的 env 与 headers，可能含密钥，权限按 0600 写入。
type Store struct {
	path string
}

type storeFile struct {
	Version  int             `json:"version"`
	Servers  []Server        `json:"servers"`
	Disabled []DisabledEntry `json:"disabled,omitempty"`
}

// DisabledEntry 保存停用前的完整客户端条目，重新启用时恢复附加设置。
type DisabledEntry struct {
	Agent  AgentKey `json:"agent"`
	Name   string   `json:"name"`
	Config []byte   `json:"config"`
}

// NewStore 创建列表存储，路径由调用方决定。
func NewStore(path string) *Store {
	return &Store{path: path}
}

// ReadRaw 读取列表原始字节。文件不存在时返回 nil 字节与 nil 错误。
func (s *Store) ReadRaw() ([]byte, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 MCP 列表失败: %w", err)
	}
	return raw, nil
}

// decodeStore 解析并校验列表内容。空字节视为尚未创建列表，返回空列表。
func decodeStore(raw []byte) ([]Server, error) {
	file, err := decodeStoreFile(raw)
	return file.Servers, err
}

func decodeStoreFile(raw []byte) (storeFile, error) {
	if len(raw) == 0 {
		return storeFile{Version: storeVersion, Servers: []Server{}}, nil
	}
	var file storeFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return storeFile{}, fmt.Errorf("MCP 列表文件格式无效: %w", err)
	}
	if file.Version != storeVersion {
		return storeFile{}, fmt.Errorf("MCP 列表文件版本不受支持: %d", file.Version)
	}
	if err := ValidateServers(file.Servers); err != nil {
		return storeFile{}, fmt.Errorf("MCP 列表文件内容无效: %w", err)
	}
	if file.Servers == nil {
		file.Servers = []Server{}
	}
	seen := map[string]bool{}
	for _, entry := range file.Disabled {
		key := string(entry.Agent) + ":" + entry.Name
		if !IsKnownAgent(entry.Agent) || !namePattern.MatchString(entry.Name) || len(entry.Config) == 0 || seen[key] {
			return storeFile{}, fmt.Errorf("MCP 停用记录无效或重复: %s", key)
		}
		seen[key] = true
	}
	return file, nil
}

// write 以原子方式替换列表文件，返回写入的原始字节。
// 替换完成后的目录同步失败视为已提交：文件已是新内容，只记录日志，不返回错误。
func (s *Store) write(servers []Server) ([]byte, error) {
	raw, err := s.ReadRaw()
	if err != nil {
		return nil, err
	}
	file, err := decodeStoreFile(raw)
	if err != nil {
		return nil, err
	}
	file.Servers = servers
	return s.writeFile(file)
}

func (s *Store) writeFile(file storeFile) ([]byte, error) {
	if file.Servers == nil {
		file.Servers = []Server{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化 MCP 列表失败: %w", err)
	}
	data = append(data, '\n')
	if err := atomicfile.Write(s.path, data, atomicfile.Options{TempPrefix: ".mcp-"}); err != nil {
		if atomicfile.IsCommitted(err) {
			log.Printf("[MCP-Store] 列表已写入但收尾失败: %v", err)
			return data, nil
		}
		return nil, fmt.Errorf("保存 MCP 列表失败: %w", err)
	}
	return data, nil
}

// Revision 返回列表原始字节的版本号。空字节（文件不存在）的版本号为空字符串。
func Revision(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
