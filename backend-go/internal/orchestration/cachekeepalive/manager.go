// Package cachekeepalive 按会话保留最新候选，并在空闲后有界地续期上游缓存。
package cachekeepalive

import (
	"container/heap"
	"context"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
)

const (
	MaxBodyBytes    = 2 << 20
	MaxMemoryBytes  = 64 << 20
	MaxSessions     = 512
	workerCount     = 2
	idleDelay       = time.Minute
	cleanupInterval = 30 * time.Second
	statusRetention = time.Hour
	requestTimeout  = 15 * time.Second
	expiryMargin    = 15 * time.Second
)

// Source.Open 只允许持有最终不可变正文，不能捕获请求、Gin Context 或安全管线。
// 协议构建器和请求前钩子的 GetBody 都满足这一所有权约束。
type Source struct {
	Kind           string
	Open           func() (io.ReadCloser, error)
	Size           int64
	Header         http.Header
	URL            string
	Upstream       *config.UpstreamConfig
	APIKey         string
	ConversationID string
	AttemptID      string
}

type Status struct {
	ID               string  `json:"id"`
	EntryProtocol    string  `json:"entryProtocol"`
	UpstreamProtocol string  `json:"upstreamProtocol"`
	CacheMode        string  `json:"cacheMode,omitempty"`
	SnapshotAt       int64   `json:"snapshotAt,omitempty"`
	NextHeartbeatAt  int64   `json:"nextHeartbeatAt,omitempty"`
	LastAt           int64   `json:"lastAt,omitempty"`
	ExpiresAt        int64   `json:"expiresAt,omitempty"`
	ConversationID   string  `json:"conversationId"`
	ChannelID        string  `json:"channelId"`
	Model            string  `json:"model"`
	State            string  `json:"state"`
	Reason           string  `json:"reason,omitempty"`
	Attempts         int     `json:"attempts"`
	InputTokens      int64   `json:"inputTokens"`
	CacheRead        int64   `json:"cacheReadTokens"`
	CacheCreation    int64   `json:"cacheCreationTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheHit         float64 `json:"cacheHitPercent"`
	BaselineCacheHit float64 `json:"baselineCacheHitPercent"`
	NextAt           int64   `json:"nextAt,omitempty"`
	UpdatedAt        int64   `json:"updatedAt"`
}

type snapshot struct {
	stream      bool
	mode        string
	model       string
	cacheURL    string
	cacheName   string
	deadline    time.Time
	source      Source
	body        []byte
	policy      config.CacheKeepaliveConfig
	baseline    int64
	baselineHit float64
	read        int64
	ttl         time.Duration
	lastSent    time.Time
	charge      int64
	refs        int
}

type entry struct {
	key        string
	generation uint64
	revision   uint64
	active     int
	success    bool
	snapshot   *snapshot
	deadline   time.Time
	updated    time.Time
	due        time.Time
	index      int
	cancel     context.CancelFunc
	status     Status
}

type work struct {
	entry      *entry
	generation uint64
	snapshot   *snapshot
	ctx        context.Context
	prepare    bool
}

type Manager struct {
	mu                 sync.Mutex
	entries            map[string]*entry
	queue              schedule
	bytes              int64
	sequence           uint64
	capacityRejections uint64
	stopped            bool
	ctx                context.Context
	cancel             context.CancelFunc
	workers            sync.WaitGroup
	jobs               chan work
	state              func() (bool, uint64)
	now                func() time.Time
	send               func(context.Context, *snapshot) sendResult
	checkPrompt        func(context.Context, string, string) error
}

func New(cfg *config.ConfigManager, checkPrompt func(context.Context, string, string) error, recorders ...func(tokenusage.Fact)) *Manager {
	m := newManager(cfg.CacheKeepaliveState, time.Now)
	m.send = newSender(cfg, recorders...)
	m.checkPrompt = checkPrompt
	changes := cfg.CacheKeepaliveChanges()
	m.workers.Add(workerCount + 1)
	go m.loop(changes)
	for i := 0; i < workerCount; i++ {
		go m.worker()
	}
	return m
}

// 构造与启动分开，时钟和发送可替换，测试不需要真实等待或网络。
func newManager(state func() (bool, uint64), now func() time.Time) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{entries: make(map[string]*entry), ctx: ctx, cancel: cancel,
		jobs: make(chan work), state: state, now: now}
}

// Turn 不持有正文。generation 按到达顺序递增，旧请求晚结束不能覆盖新候选。
type Turn struct {
	manager    *Manager
	entry      *entry
	generation uint64
	end        sync.Once
}

func (m *Manager) Begin(key string) *Turn {
	if m == nil || key == "" {
		return nil
	}
	enabled, revision := m.state()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil
	}
	e := m.entries[key]
	if !enabled {
		if e != nil && e.snapshot != nil {
			m.stopEntry(e, "配置已关闭保活")
		}
		return nil
	}
	if e == nil {
		if len(m.entries) >= MaxSessions {
			// 已停止的记录只用于展示，可先淘汰最旧记录，为新会话让出位置。
			var oldest *entry
			for _, candidate := range m.entries {
				if candidate.active == 0 && candidate.snapshot == nil && (oldest == nil || candidate.updated.Before(oldest.updated)) {
					oldest = candidate
				}
			}
			if oldest != nil {
				delete(m.entries, oldest.key)
			}
		}
		if len(m.entries) >= MaxSessions {
			m.capacityRejections++
			return nil
		}
		e = &entry{key: key, index: -1}
		m.entries[key] = e
	}
	m.clearSnapshot(e)
	m.sequence++
	e.generation, e.revision = m.sequence, revision
	e.active++
	e.success = false
	e.updated = m.now()
	e.status = Status{ID: key, State: "active", UpdatedAt: e.updated.UnixMilli()}
	return &Turn{manager: m, entry: e, generation: e.generation}
}

func (t *Turn) current() bool {
	return !t.manager.stopped && t.manager.entries[t.entry.key] == t.entry && t.entry.generation == t.generation
}

func (t *Turn) BindConversation(conversationID string) {
	if t == nil {
		return
	}
	t.manager.mu.Lock()
	defer t.manager.mu.Unlock()
	if t.current() {
		t.entry.status.ConversationID = conversationID
	}
}

// Capture 在实际上游发送时只登记正文引用，不读取、解析或序列化上下文。
func (t *Turn) Capture(source Source) {
	if t == nil || source.Upstream == nil {
		return
	}
	m := t.manager
	_, revision := m.state()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.current() || t.entry.revision != revision {
		return
	}
	e := t.entry
	m.clearSnapshot(e)
	e.success = false
	u := source.Upstream
	e.status.ConversationID, e.status.ChannelID = source.ConversationID, u.ID
	e.status.EntryProtocol, e.status.UpstreamProtocol = source.Kind, u.ServiceType
	if !u.CacheKeepalive.Enabled || !config.SupportsCacheKeepalive(source.Kind, u.ServiceType) || u.LowQuality || u.BuiltinProvider != "" {
		m.stopEntry(e, "实际目标未启用受支持的缓存保活")
		return
	}
	// 在任务入口再次校验，配置来源即使绕过保存接口，也不能产生无上限任务。
	if err := config.ValidateCacheKeepalive(source.Kind, u); err != nil {
		m.stopEntry(e, err.Error())
		return
	}
	if source.ConversationID == "" || source.AttemptID == "" {
		m.stopEntry(e, "缺少可关联的会话或上游尝试标识")
		return
	}
	if source.Open == nil || source.Size <= 0 || source.Size > MaxBodyBytes {
		m.stopEntry(e, "最终请求正文不可重放或超过 2 MiB 上限")
		return
	}
	headerBytes := int64(len(source.URL) + len(source.APIKey))
	for key, values := range source.Header {
		headerBytes += int64(len(key))
		for _, value := range values {
			headerBytes += int64(len(value))
		}
	}
	if headerBytes > 32<<10 {
		m.stopEntry(e, "请求头超过保活快照上限")
		return
	}
	// 为原始缓冲容量、延迟构造后的正文及元数据预留空间；工作线程另有固定临时预算。
	charge := source.Size*2 + headerBytes + 8192
	if m.bytes+charge > MaxMemoryBytes {
		m.stopEntry(e, "保活快照达到 64 MiB 内存预算")
		return
	}
	source.Header = source.Header.Clone()
	// 不保留包含模型映射和其他密钥的整份渠道配置。
	source.Upstream = &config.UpstreamConfig{ID: u.ID, Name: u.Name, BaseURL: u.BaseURL,
		UseFullURL: u.UseFullURL, ServiceType: u.ServiceType, InsecureSkipVerify: u.InsecureSkipVerify,
		ProxyMode: u.ProxyMode, ProxyURL: u.ProxyURL, ClientIdentityMode: u.ClientIdentityMode}
	if source.Kind != "messages" && source.Kind != "responses" {
		source.Upstream.ClientIdentityMode = "api"
	}
	e.snapshot = &snapshot{source: source, policy: u.CacheKeepalive, charge: charge, refs: 1, lastSent: m.now()}
	e.status.State, e.status.Reason = "active", ""
	m.bytes += charge
}

func (t *Turn) Result(attemptID string, success bool, usage *types.Usage) {
	if t == nil {
		return
	}
	m := t.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.current() || t.entry.snapshot == nil || t.entry.snapshot.source.AttemptID != attemptID {
		return
	}
	e := t.entry
	input, read, creation, _, err := usageNumbers(usage)
	if !success || err != nil || read+creation == 0 {
		m.stopEntry(e, "最新请求失败或未返回可信的缓存用量")
		return
	}
	e.success = true
	e.snapshot.baseline = read + creation
	e.snapshot.baselineHit = float64(read+creation) * 100 / float64(input+read+creation)
	e.status.BaselineCacheHit = e.snapshot.baselineHit
	e.snapshot.read = read
}

func (t *Turn) End() {
	if t == nil {
		return
	}
	t.end.Do(func() {
		m := t.manager
		m.mu.Lock()
		defer m.mu.Unlock()
		e := t.entry
		if m.entries[e.key] != e {
			return
		}
		e.active--
		if e.active != 0 {
			return
		}
		e.updated = m.now()
		if !e.success || e.snapshot == nil {
			if e.status.ChannelID == "" {
				delete(m.entries, e.key)
				return
			}
			if e.status.State != "stopped" {
				m.stopEntry(e, "没有可用于保活的最新成功请求")
			}
			return
		}
		p := e.snapshot.policy
		if p.MaxIdleMinutes > 0 {
			e.deadline = e.updated.Add(time.Duration(p.MaxIdleMinutes) * time.Minute)
		} else {
			e.deadline = time.Time{}
		}
		e.snapshot.deadline = e.deadline
		e.status.State = "waiting"
		m.schedule(e, e.updated.Add(idleDelay))
	})
}

func (m *Manager) schedule(e *entry, at time.Time) {
	e.due = at
	e.status.NextAt, e.status.UpdatedAt = at.UnixMilli(), m.now().UnixMilli()
	e.status.NextHeartbeatAt = 0
	if s := e.snapshot; s != nil {
		heartbeatAt := at
		if s.body == nil {
			heartbeatAt = e.updated.Add(time.Duration(s.policy.IntervalSeconds) * time.Second)
		}
		// 准备快照、达到期限和发送心跳是不同事件，不能把清理期限展示为心跳时间。
		if (e.deadline.IsZero() || heartbeatAt.Before(e.deadline)) &&
			(s.ttl == 0 || heartbeatAt.Before(s.lastSent.Add(s.ttl-expiryMargin))) {
			e.status.NextHeartbeatAt = heartbeatAt.UnixMilli()
		}
	}
	if e.index < 0 {
		heap.Push(&m.queue, e)
	} else {
		heap.Fix(&m.queue, e.index)
	}
}

func (m *Manager) release(s *snapshot) {
	if s == nil {
		return
	}
	s.refs--
	if s.refs == 0 {
		m.bytes -= s.charge
		s.source, s.body = Source{}, nil
	}
}

func (m *Manager) clearSnapshot(e *entry) {
	if e.index >= 0 {
		heap.Remove(&m.queue, e.index)
	}
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	m.release(e.snapshot)
	e.snapshot = nil
	e.status.NextAt = 0
	e.status.NextHeartbeatAt = 0
}

func (m *Manager) stopEntry(e *entry, reason string) {
	m.clearSnapshot(e)
	e.success = false
	e.updated = m.now()
	e.status.State, e.status.Reason, e.status.UpdatedAt = "stopped", reason, e.updated.UnixMilli()
	if e.status.ChannelID != "" {
		log.Printf("[CacheKeepalive-Stop] 会话=%s 渠道=%s 原因=%s 次数=%d 缓存读取=%d 新增输入=%d 缓存写入=%d 输出=%d",
			e.status.ConversationID, e.status.ChannelID, reason, e.status.Attempts,
			e.status.CacheRead, e.status.InputTokens, e.status.CacheCreation, e.status.OutputTokens)
	}
}

func (m *Manager) Forget(conversationID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, e := range m.entries {
		if conversationID == "" || e.status.ConversationID == conversationID {
			m.clearSnapshot(e)
			delete(m.entries, key)
		}
	}
}

// StopChannel 保留停止状态，并使尚在途的真实请求无法重新激活本轮。
func (m *Manager) StopChannel(channelID, id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, e := range m.entries {
		if e.status.ChannelID == channelID && (id == "" || e.key == id) && e.status.State != "stopped" {
			m.sequence++
			e.generation = m.sequence
			m.stopEntry(e, "已手动停止，下一次真实请求可建立新一轮保活")
			count++
		}
	}
	return count
}

func (m *Manager) Status(channelID string) []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Status, 0)
	for _, e := range m.entries {
		if e.status.ChannelID == channelID {
			result = append(result, e.status)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	return result
}

// ActiveChannels 供仪表盘一次读取所有渠道的保活会话数，不复制正文或逐渠道扫描。
func (m *Manager) ActiveChannels() map[string]int {
	if m == nil {
		return nil
	}
	enabled, revision := m.state()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !enabled || m.stopped {
		return nil
	}
	result := make(map[string]int)
	now := m.now()
	for _, e := range m.entries {
		if e.active != 0 || e.revision != revision || e.snapshot == nil || e.snapshot.body == nil ||
			!e.deadline.IsZero() && !now.Before(e.deadline) {
			continue
		}
		if e.status.State == "ready" && e.status.NextHeartbeatAt > 0 && stopReason(e, now) == "" ||
			e.status.State == "sending" {
			result[e.status.ChannelID]++
		}
	}
	return result
}

// CapacityRejections 统计因全局会话容量不足而未建立候选的请求数，不保留额外上下文。
func (m *Manager) CapacityRejections() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capacityRejections
}

func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.stopped {
		m.stopped = true
		m.cancel()
		for key, e := range m.entries {
			m.clearSnapshot(e)
			delete(m.entries, key)
		}
	}
	m.mu.Unlock()
	m.workers.Wait()
}

type schedule []*entry

func (s schedule) Len() int           { return len(s) }
func (s schedule) Less(i, j int) bool { return s[i].due.Before(s[j].due) }
func (s schedule) Swap(i, j int)      { s[i], s[j] = s[j], s[i]; s[i].index, s[j].index = i, j }
func (s *schedule) Push(value any)    { e := value.(*entry); e.index = len(*s); *s = append(*s, e) }
func (s *schedule) Pop() any {
	last := len(*s) - 1
	e := (*s)[last]
	(*s)[last] = nil
	*s = (*s)[:last]
	e.index = -1
	return e
}
