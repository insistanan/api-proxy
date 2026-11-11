package mcpsync

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/atomicfile"
)

// 调用方可通过 errors.Is 区分失败类别，以映射到不同的 HTTP 状态码。
var (
	ErrInvalidServers   = errors.New("MCP 列表无效")
	ErrRevisionConflict = errors.New("MCP 列表已被修改，请刷新后重试")
	ErrAgentPrepare     = errors.New("智能体配置无法解析，未写入任何文件")
	ErrAgentConflict    = errors.New("应用中存在不同的同名 MCP 配置，未写入任何文件")
)

// Adapter 把统一列表渲染进某个智能体的配置文件。
// 实现只负责内容变换，不做文件 IO 之外的副作用；文件读写由 Service 统一执行。
type Adapter interface {
	Key() AgentKey
	Name() string
	// Path 解析配置文件路径。失败时该智能体在列表中标记为不可用，保存时整体中止。
	Path() (string, error)
	// Read 读取现有条目，单条不兼容配置通过 DetectedServer.Error 单独报告。
	Read(current []byte) ([]DetectedServer, error)
	// Render 基于 current（文件当前字节，文件不存在时为 nil）计算新内容。
	// managed 中的名称由本次分发接管：desired 中存在则写入，不存在则从智能体配置中删除。
	// managed 之外的条目不得改动。changed=false 表示无需写入。
	Render(current []byte, desired map[string]Server, managed map[string]struct{}) (next []byte, changed bool, err error)
}

// Service 串联列表存储与各智能体适配器，负责两阶段分发。
type Service struct {
	mu       sync.Mutex
	store    *Store
	adapters []Adapter
}

// NewService 创建服务。adapters 决定分发目标与列表中可用的智能体。
func NewService(store *Store, adapters ...Adapter) *Service {
	return &Service{store: store, adapters: adapters}
}

// AgentStatus 描述智能体配置文件的当前状态，用于列表页展示。
type AgentStatus struct {
	Key      AgentKey `json:"key"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Exists   bool     `json:"exists"`
	Count    int      `json:"count"`
	Active   int      `json:"active"`
	Error    string   `json:"error,omitempty"`
	Revision string   `json:"revision"`
}

// State 是列表页需要的完整快照。
type State struct {
	Revision string
	Servers  []Server
	Agents   []AgentStatus
	Detected []DetectedServer
}

// AgentResult 描述一次保存中某个智能体的写入结果。
// OK=false 时统一列表已保存，但该智能体的配置文件未更新。
type AgentResult struct {
	Key   AgentKey `json:"key"`
	Name  string   `json:"name"`
	Path  string   `json:"path"`
	OK    bool     `json:"ok"`
	Error string   `json:"error,omitempty"`
}

// SaveResult 是保存成功后的结果。
type SaveResult struct {
	Revision string
	Agents   []AgentResult
}

// State 返回统一列表与各智能体配置文件状态。
func (s *Service) State() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := s.store.ReadRaw()
	if err != nil {
		return State{}, err
	}
	file, err := decodeStoreFile(raw)
	if err != nil {
		return State{}, err
	}
	servers := file.Servers

	agents := make([]AgentStatus, 0, len(s.adapters))
	detected := make([]DetectedServer, 0)
	for _, adapter := range s.adapters {
		status := AgentStatus{Key: adapter.Key(), Name: adapter.Name()}
		path, err := adapter.Path()
		if err != nil {
			status.Error = err.Error()
			agents = append(agents, status)
			continue
		}
		status.Path = path
		if _, err := os.Stat(path); err == nil {
			status.Exists = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			status.Error = fmt.Sprintf("检查配置文件失败: %v", err)
		}
		if status.Exists {
			current, err := os.ReadFile(path)
			if err != nil {
				status.Error = fmt.Sprintf("读取配置文件失败: %v", err)
			} else if entries, err := adapter.Read(current); err != nil {
				status.Error = err.Error()
			} else {
				status.Revision = Revision(current)
				status.Count = len(entries)
				for _, entry := range entries {
					if entry.Enabled {
						status.Active++
					}
				}
				detected = append(detected, entries...)
			}
		}
		// 停用快照只在目标应用中已不存在同名条目时展示；写入失败时以实际文件为准。
		if status.Error == "" {
			for _, saved := range file.Disabled {
				if saved.Agent != adapter.Key() {
					continue
				}
				present := false
				for _, entry := range detected {
					if entry.Agent == saved.Agent && entry.Name == saved.Name {
						present = true
						break
					}
				}
				if present {
					continue
				}
				entry, err := decodeEntrySnapshot(adapter, saved)
				if err != nil {
					return State{}, fmt.Errorf("读取停用的 MCP %s 失败: %w", saved.Name, err)
				}
				entries, err := readEntries(map[string]any{"saved": map[string]any{saved.Name: entry}}, "saved", saved.Agent)
				if err != nil {
					return State{}, err
				}
				for index := range entries {
					entries[index].Enabled = false
					if entries[index].Server != nil {
						entries[index].Server.Agents = []AgentKey{}
					}
				}
				detected = append(detected, entries...)
				status.Count += len(entries)
			}
		}
		agents = append(agents, status)
	}
	return State{Revision: Revision(raw), Servers: servers, Agents: agents, Detected: detected}, nil
}

// Save 校验并保存统一列表，然后把启用的条目分发到各智能体。
//
// 流程分两阶段：
//  1. 准备阶段：读取所有智能体文件并计算新内容。任一智能体无法解析或路径失败时直接返回，不写入任何文件。
//  2. 提交阶段：先替换统一列表，再逐个写入智能体。单个智能体写入失败只记录在结果中，不回滚其它文件。
//
// revision 必须与当前列表字节的版本号一致，否则返回 ErrRevisionConflict。
func (s *Service) Save(revision string, servers []Server, adoptions ...Adoption) (SaveResult, error) {
	if err := ValidateServers(servers); err != nil {
		return SaveResult{}, fmt.Errorf("%w: %w", ErrInvalidServers, err)
	}
	for _, change := range adoptions {
		if !IsKnownAgent(change.Agent) {
			return SaveResult{}, fmt.Errorf("%w: 未知的应用", ErrInvalidServers)
		}
		if err := change.Server.validate(); err != nil {
			return SaveResult{}, fmt.Errorf("%w: %w", ErrInvalidServers, err)
		}
		if !slices.ContainsFunc(s.adapters, func(adapter Adapter) bool { return adapter.Key() == change.Agent }) {
			return SaveResult{}, fmt.Errorf("%w: 应用适配器不可用", ErrInvalidServers)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := s.store.ReadRaw()
	if err != nil {
		return SaveResult{}, err
	}
	if revision != Revision(raw) {
		return SaveResult{}, ErrRevisionConflict
	}
	file, err := decodeStoreFile(raw)
	if err != nil {
		return SaveResult{}, err
	}
	previous := file.Servers

	type plan struct {
		adapter Adapter
		path    string
		current []byte
		next    []byte
		changed bool
	}
	plans := make([]plan, 0, len(s.adapters))
	for _, adapter := range s.adapters {
		path, err := adapter.Path()
		if err != nil {
			return SaveResult{}, fmt.Errorf("%w: %s: %w", ErrAgentPrepare, adapter.Name(), err)
		}
		current, err := readOptional(path)
		if err != nil {
			return SaveResult{}, fmt.Errorf("%w: 读取 %s 配置失败: %w", ErrAgentPrepare, adapter.Name(), err)
		}
		desired := DesiredFor(servers, adapter.Key())
		prior := DesiredFor(previous, adapter.Key())
		base, adopted, err := prepareAdoptions(adapter, current, &file, adoptions, servers)
		if err != nil {
			return SaveResult{}, fmt.Errorf("处理 %s 编辑失败: %w", adapter.Name(), err)
		}
		for name, original := range adopted {
			prior[name] = original
		}
		// 每个应用独立计算托管范围，不能删除从未在该应用中接管过的同名条目。
		managed := make(map[string]struct{}, len(prior)+len(desired))
		for name := range prior {
			managed[name] = struct{}{}
		}
		for name := range desired {
			managed[name] = struct{}{}
		}
		entries, err := adapter.Read(current)
		if err != nil {
			return SaveResult{}, fmt.Errorf("%w: %s: %w", ErrAgentPrepare, adapter.Name(), err)
		}
		for _, entry := range entries {
			server, wanted := desired[entry.Name]
			_, owned := prior[entry.Name]
			if wanted && !owned && (entry.Server == nil || !sameConfig(*entry.Server, server)) {
				return SaveResult{}, fmt.Errorf("%w: %s / %s，请更名或先在原应用中处理冲突", ErrAgentConflict, adapter.Name(), entry.Name)
			}
		}
		// 编辑列表后重新启用停用项时，同样恢复原应用附加设置。
		for _, saved := range file.Disabled {
			if saved.Agent != adapter.Key() {
				continue
			}
			if _, wanted := desired[saved.Name]; !wanted {
				continue
			}
			present := false
			for _, entry := range entries {
				if entry.Name == saved.Name {
					present = true
					break
				}
			}
			if present {
				continue
			}
			entry, err := decodeEntrySnapshot(adapter, saved)
			if err != nil {
				return SaveResult{}, fmt.Errorf("%w: %w", ErrAgentPrepare, err)
			}
			base, _, err = replaceEntry(adapter, base, saved.Name, entry)
			if err != nil {
				return SaveResult{}, fmt.Errorf("%w: %w", ErrAgentPrepare, err)
			}
		}
		next, _, err := adapter.Render(base, desired, managed)
		if err != nil {
			return SaveResult{}, fmt.Errorf("%w: %s: %w", ErrAgentPrepare, adapter.Name(), err)
		}
		plans = append(plans, plan{adapter: adapter, path: path, current: current, next: next, changed: !bytes.Equal(current, next)})
	}

	file.Servers = servers
	newRaw, err := s.store.writeFile(file)
	if err != nil {
		return SaveResult{}, err
	}

	results := make([]AgentResult, 0, len(plans))
	for _, p := range plans {
		result := AgentResult{Key: p.adapter.Key(), Name: p.adapter.Name(), Path: p.path, OK: true}
		if p.changed {
			if err := writeAgentFile(p.path, p.current, p.next); err != nil {
				log.Printf("[MCP-Sync] 写入 %s 配置失败: %v", p.adapter.Name(), err)
				result.OK = false
				result.Error = err.Error()
			}
		}
		results = append(results, result)
	}
	return SaveResult{Revision: Revision(newRaw), Agents: results}, nil
}

// readOptional 读取文件；不存在时返回 nil 而不是错误，便于适配器判断"从零创建"。
func readOptional(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

// writeAgentFile 写入智能体配置。
// 外部工具（尤其是 Claude Code 自身）可能在准备与提交之间修改文件，因此替换前再次比较原始字节；
// 已有内容时先保存带时间戳的备份，与 OpenCode 配置写入保持一致。
func writeAgentFile(path string, previous, next []byte) error {
	current, err := readOptional(path)
	if err != nil {
		return fmt.Errorf("检查配置版本失败: %w", err)
	}
	if !bytes.Equal(current, previous) {
		return fmt.Errorf("配置文件已被外部修改，请刷新后重试")
	}
	if len(previous) > 0 {
		backup := path + "." + time.Now().Format("20060102-150405") + ".bak"
		if err := os.WriteFile(backup, previous, 0600); err != nil {
			return fmt.Errorf("创建备份失败: %w", err)
		}
	}
	if err := atomicfile.Write(path, next, atomicfile.Options{TempPrefix: ".mcp-"}); err != nil {
		if atomicfile.IsCommitted(err) {
			log.Printf("[MCP-Sync] %s 已写入但收尾失败: %v", path, err)
			return nil
		}
		return err
	}
	return nil
}
