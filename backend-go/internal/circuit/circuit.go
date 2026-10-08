// Package circuit 实现渠道级三态熔断器（Closed / Open / HalfOpen）。
//
// 设计参照 cc-switch 的 circuit_breaker.rs：
//   - Closed：正常放行；连续失败达到阈值，或累计请求达到最小样本后错误率达到阈值，转 Open。
//   - Open：拒绝所有请求；冷却时间到期后惰性转 HalfOpen（无后台定时器，在被查询时转换）。
//   - HalfOpen：限量放行探测请求（默认同时 1 个）；探测连续成功达到阈值转 Closed，
//     任一探测失败立即回 Open 重新计时。
//
// 与 Key 级滑动窗口熔断（metrics.MetricsManager.ShouldSuspendKey）正交：
// 本包按 (kind, channelIndex) 组织，粒度是"渠道"；Key 级判断的是渠道内单个密钥。
// 状态仅存内存，进程重启即清零（与 cc-switch 一致——持久层只存展示用的健康度镜像）。
package circuit

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// State 熔断器状态。
type State string

const (
	StateClosed   State = "closed"
	StateOpen     State = "open"
	StateHalfOpen State = "half_open"
)

// Config 熔断器阈值配置。零值字段回落 DefaultConfig 对应项。
type Config struct {
	FailureThreshold   int           // 连续失败多少次转 Open
	ErrorRateThreshold float64       // 错误率阈值（0-1）
	MinRequests        int           // 计算错误率前的最小累计请求数
	Cooldown           time.Duration // Open 冷却时长，到期后转 HalfOpen
	SuccessThreshold   int           // HalfOpen 连续成功多少次转 Closed
}

// DefaultConfig 返回与 cc-switch 默认值一致的配置。
func DefaultConfig() Config {
	return Config{
		FailureThreshold:   4,
		ErrorRateThreshold: 0.6,
		MinRequests:        10,
		Cooldown:           60 * time.Second,
		SuccessThreshold:   2,
	}
}

func (c Config) withDefaults() Config {
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = 4
	}
	if c.ErrorRateThreshold <= 0 || c.ErrorRateThreshold > 1 {
		c.ErrorRateThreshold = 0.6
	}
	if c.MinRequests <= 0 {
		c.MinRequests = 10
	}
	if c.Cooldown <= 0 {
		c.Cooldown = 60 * time.Second
	}
	if c.SuccessThreshold <= 0 {
		c.SuccessThreshold = 2
	}
	return c
}

// Breaker 单个渠道的熔断器。所有方法并发安全。
type Breaker struct {
	mu sync.Mutex

	config Config

	state                State
	consecutiveFailures  int
	consecutiveSuccesses int
	totalRequests        int
	failedRequests       int
	lastOpenedAt         time.Time
	halfOpenInFlight     int // HalfOpen 状态下在途探测请求数

	// generation 状态代次：Reset / 重新 Open→HalfOpen 时递增。
	// Allow 占用探测名额时返回当时的代次；记账携带代次且不匹配时忽略，
	// 防止迟到结果释放新许可的探测名额（R2-b）。
	generation uint64
}

// NewBreaker 创建熔断器。config 零值字段回落默认值。
func NewBreaker(config Config) *Breaker {
	return &Breaker{
		config: config.withDefaults(),
		state:  StateClosed,
	}
}

// AllowResult 描述一次放行判定。
type AllowResult struct {
	// Allowed 是否允许本次请求通过。
	Allowed bool
	// Probe 表示本次请求是否占用 HalfOpen 探测名额。
	// 调用方必须在请求结束后调用 RecordSuccess / RecordFailure / RecordNeutral
	// 之一释放名额（Allowed && Probe 时）。
	Probe bool
	// State 判定时的熔断器状态。
	State State
	// Generation 判定时的状态代次。占用探测名额（Probe）时，记账必须携带
	// 同一代次；不匹配的记账被忽略，避免迟到结果释放新许可的名额。
	Generation uint64
}

// Allow 判定是否放行一次请求。Open 且冷却到期时惰性转 HalfOpen。
func (b *Breaker) Allow() AllowResult {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lazyTransitionLocked(time.Now())

	switch b.state {
	case StateOpen:
		return AllowResult{Allowed: false, State: b.state, Generation: b.generation}
	case StateHalfOpen:
		if b.halfOpenInFlight >= 1 {
			return AllowResult{Allowed: false, State: b.state, Generation: b.generation}
		}
		b.halfOpenInFlight++
		return AllowResult{Allowed: true, Probe: true, State: b.state, Generation: b.generation}
	default:
		return AllowResult{Allowed: true, State: b.state, Generation: b.generation}
	}
}

// Peek 预判是否放行，但不占用 HalfOpen 探测名额（选渠候选过滤用）。
// 选中渠道后仍必须以 Allow 正式占位；两次判定之间状态可能变化。
func (b *Breaker) Peek() AllowResult {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lazyTransitionLocked(time.Now())

	switch b.state {
	case StateOpen:
		return AllowResult{Allowed: false, State: b.state}
	case StateHalfOpen:
		if b.halfOpenInFlight >= 1 {
			return AllowResult{Allowed: false, State: b.state}
		}
		return AllowResult{Allowed: true, State: b.state}
	default:
		return AllowResult{Allowed: true, State: b.state}
	}
}

// RecordSuccess 记录一次成功请求。HalfOpen 下释放探测名额并累计连续成功。
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.recordSuccessLocked(time.Now())
}

// RecordSuccessGeneration 带状态代次的成功记账：代次不匹配时忽略
// （迟到结果不得释放新许可的探测名额，R2-b）。
func (b *Breaker) RecordSuccessGeneration(generation uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.generationMatchesLocked(generation) {
		return
	}
	b.recordSuccessLocked(time.Now())
}

// RecordFailure 记录一次失败请求。Closed 下累计连续失败与错误率；HalfOpen 下立即回 Open。
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.recordFailureLocked(time.Now())
}

// RecordFailureGeneration 带状态代次的失败记账：代次不匹配时忽略。
func (b *Breaker) RecordFailureGeneration(generation uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.generationMatchesLocked(generation) {
		return
	}
	b.recordFailureLocked(time.Now())
}

// RecordNeutral 记录一次"不应影响健康度"的请求结果（客户端取消、内容审核拦截等）。
// 只释放 HalfOpen 探测名额，不改变任何计数与状态。
func (b *Breaker) RecordNeutral() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.recordNeutralLocked()
}

// RecordNeutralGeneration 带状态代次的中性记账：代次不匹配时忽略。
func (b *Breaker) RecordNeutralGeneration(generation uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.generationMatchesLocked(generation) {
		return
	}
	b.recordNeutralLocked()
}

// generationMatchesLocked 判断记账代次与当前代次一致。
// 代次一致的迟到记账才作用于当前状态；Reset 后的旧请求结果被忽略。
func (b *Breaker) generationMatchesLocked(generation uint64) bool {
	return generation == b.generation
}

// Reset 手动重置到 Closed 并清零全部计数。
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.resetLocked()
}

// Snapshot 返回当前状态快照（用于 API 展示）。
type Snapshot struct {
	State                State  `json:"state"`
	ConsecutiveFailures  int    `json:"consecutiveFailures"`
	TotalRequests        int    `json:"totalRequests"`
	FailedRequests       int    `json:"failedRequests"`
	LastOpenedAt         string `json:"lastOpenedAt,omitempty"`
	HalfOpenInFlight     int    `json:"halfOpenInFlight"`
	CooldownRemainingSec int    `json:"cooldownRemainingSeconds,omitempty"`
	ChannelName          string `json:"channelName,omitempty"` // 仅 SnapshotEntry 填充
}

func (b *Breaker) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	snap := Snapshot{
		State:               b.state,
		ConsecutiveFailures: b.consecutiveFailures,
		TotalRequests:       b.totalRequests,
		FailedRequests:      b.failedRequests,
		HalfOpenInFlight:    b.halfOpenInFlight,
	}
	if !b.lastOpenedAt.IsZero() {
		snap.LastOpenedAt = b.lastOpenedAt.Format(time.RFC3339)
	}
	if b.state == StateOpen {
		remaining := b.config.Cooldown - time.Since(b.lastOpenedAt)
		if remaining < 0 {
			remaining = 0
		}
		snap.CooldownRemainingSec = int(remaining.Seconds())
	}
	return snap
}

// lazyTransitionLocked 在被查询时惰性推进 Open → HalfOpen。
// 注意与 cc-switch 的幂等守卫一致：已经是 HalfOpen 时不重置在途计数，
// 避免并发查询把探测名额误清空。
func (b *Breaker) lazyTransitionLocked(now time.Time) {
	if b.state != StateOpen {
		return
	}
	if now.Sub(b.lastOpenedAt) < b.config.Cooldown {
		return
	}
	b.state = StateHalfOpen
	b.consecutiveSuccesses = 0
	b.generation++ // 新一轮探测开始：上一轮迟到结果不再作用于本轮
	log.Printf("[Circuit-Probe] 熔断冷却到期，进入半开状态（探测限额: 1）")
}

func (b *Breaker) recordSuccessLocked(now time.Time) {
	releaseProbe := b.state == StateHalfOpen && b.halfOpenInFlight > 0
	if releaseProbe {
		b.halfOpenInFlight--
	}

	switch b.state {
	case StateHalfOpen:
		b.consecutiveFailures = 0
		b.consecutiveSuccesses++
		if b.consecutiveSuccesses >= b.config.SuccessThreshold {
			b.resetLocked()
			log.Printf("[Circuit-Close] 半开探测连续成功 %d 次，熔断恢复", b.config.SuccessThreshold)
		}
	case StateOpen:
		// 冷却期内到达的成功回报（竞态窗口）：仅释放名额，不改变状态。
	default:
		b.consecutiveFailures = 0
		b.totalRequests++
	}
}

func (b *Breaker) recordFailureLocked(now time.Time) {
	releaseProbe := b.state == StateHalfOpen && b.halfOpenInFlight > 0
	if releaseProbe {
		b.halfOpenInFlight--
	}

	switch b.state {
	case StateHalfOpen:
		// 探测失败立即回 Open 重新计时。
		b.tripOpenLocked(now)
		log.Printf("[Circuit-Open] 半开探测失败，重新熔断并重置冷却计时")
	case StateOpen:
		// 冷却期内的失败回报：忽略。
	default:
		b.totalRequests++
		b.failedRequests++
		b.consecutiveFailures++
		b.consecutiveSuccesses = 0

		if b.consecutiveFailures >= b.config.FailureThreshold {
			b.tripOpenLocked(now)
			log.Printf("[Circuit-Open] 连续失败 %d 次触发熔断", b.consecutiveFailures)
			return
		}
		if b.totalRequests >= b.config.MinRequests {
			errorRate := float64(b.failedRequests) / float64(b.totalRequests)
			if errorRate >= b.config.ErrorRateThreshold {
				b.tripOpenLocked(now)
				log.Printf("[Circuit-Open] 错误率 %.0f%%（%d/%d）触发熔断", errorRate*100, b.failedRequests, b.totalRequests)
			}
		}
	}
}

func (b *Breaker) recordNeutralLocked() {
	if b.state == StateHalfOpen && b.halfOpenInFlight > 0 {
		b.halfOpenInFlight--
	}
}

func (b *Breaker) tripOpenLocked(now time.Time) {
	b.state = StateOpen
	b.lastOpenedAt = now
	b.halfOpenInFlight = 0
	b.consecutiveSuccesses = 0
	b.generation++ // 重新熔断：在途探测的迟到结果不再作用于新一轮状态
}

func (b *Breaker) resetLocked() {
	b.state = StateClosed
	b.consecutiveFailures = 0
	b.consecutiveSuccesses = 0
	b.totalRequests = 0
	b.failedRequests = 0
	b.lastOpenedAt = time.Time{}
	b.halfOpenInFlight = 0
	b.generation++ // 手动重置：旧请求的迟到结果不得归还新探测的名额
}

// Manager 管理全部 (kind, channelIndex) 组合的熔断器。
type Manager struct {
	mu       sync.Mutex
	config   Config
	enabled  bool
	breakers map[string]*Breaker
	names    map[string]string // key -> 渠道名（API 展示用）
}

// NewManager 创建熔断器管理器。enabled=false 时所有 Allow 直接放行（一键回滚开关）。
func NewManager(config Config, enabled bool) *Manager {
	return &Manager{
		config:   config.withDefaults(),
		enabled:  enabled,
		breakers: make(map[string]*Breaker),
		names:    make(map[string]string),
	}
}

func managerKey(kind string, channelIndex int) string {
	return fmt.Sprintf("%s:%d", kind, channelIndex)
}

func (m *Manager) getOrCreateLocked(kind string, channelIndex int) *Breaker {
	key := managerKey(kind, channelIndex)
	breaker, exists := m.breakers[key]
	if !exists {
		breaker = NewBreaker(m.config)
		m.breakers[key] = breaker
	}
	return breaker
}

// Allow 判定某渠道是否放行。enabled=false 或未知渠道（首次出现）时放行。
func (m *Manager) Allow(kind string, channelIndex int) AllowResult {
	if !m.enabled {
		return AllowResult{Allowed: true, State: StateClosed}
	}
	m.mu.Lock()
	breaker := m.getOrCreateLocked(kind, channelIndex)
	m.mu.Unlock()
	return breaker.Allow()
}

// Peek 预判某渠道是否放行，但不占用 HalfOpen 探测名额（选渠候选过滤用）。
// 选中渠道后仍必须以 Allow 正式占位。
func (m *Manager) Peek(kind string, channelIndex int) AllowResult {
	if !m.enabled {
		return AllowResult{Allowed: true, State: StateClosed}
	}
	m.mu.Lock()
	breaker := m.getOrCreateLocked(kind, channelIndex)
	m.mu.Unlock()
	return breaker.Peek()
}

// RecordSuccess 记录渠道成功。
func (m *Manager) RecordSuccess(kind string, channelIndex int) {
	if !m.enabled {
		return
	}
	m.mu.Lock()
	breaker := m.getOrCreateLocked(kind, channelIndex)
	m.mu.Unlock()
	breaker.RecordSuccess()
}

// RecordSuccessGeneration 带状态代次的成功记账。
func (m *Manager) RecordSuccessGeneration(kind string, channelIndex int, generation uint64) {
	if !m.enabled {
		return
	}
	m.mu.Lock()
	breaker := m.getOrCreateLocked(kind, channelIndex)
	m.mu.Unlock()
	breaker.RecordSuccessGeneration(generation)
}

// RecordFailure 记录渠道失败。
func (m *Manager) RecordFailure(kind string, channelIndex int) {
	if !m.enabled {
		return
	}
	m.mu.Lock()
	breaker := m.getOrCreateLocked(kind, channelIndex)
	m.mu.Unlock()
	breaker.RecordFailure()
}

// RecordFailureGeneration 带状态代次的失败记账。
func (m *Manager) RecordFailureGeneration(kind string, channelIndex int, generation uint64) {
	if !m.enabled {
		return
	}
	m.mu.Lock()
	breaker := m.getOrCreateLocked(kind, channelIndex)
	m.mu.Unlock()
	breaker.RecordFailureGeneration(generation)
}

// RecordNeutral 记录不影响健康度的结果。
func (m *Manager) RecordNeutral(kind string, channelIndex int) {
	if !m.enabled {
		return
	}
	m.mu.Lock()
	breaker := m.getOrCreateLocked(kind, channelIndex)
	m.mu.Unlock()
	breaker.RecordNeutral()
}

// RecordNeutralGeneration 带状态代次的中性记账。
func (m *Manager) RecordNeutralGeneration(kind string, channelIndex int, generation uint64) {
	if !m.enabled {
		return
	}
	m.mu.Lock()
	breaker := m.getOrCreateLocked(kind, channelIndex)
	m.mu.Unlock()
	breaker.RecordNeutralGeneration(generation)
}

// Reset 手动重置指定渠道。
func (m *Manager) Reset(kind string, channelIndex int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := managerKey(kind, channelIndex)
	breaker, exists := m.breakers[key]
	if !exists {
		return false
	}
	breaker.Reset()
	return true
}

// SetChannelName 登记渠道名（API 展示用；重复设置以后者为准）。
func (m *Manager) SetChannelName(kind string, channelIndex int, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.names[managerKey(kind, channelIndex)] = name
}

// Entry Snapshot 的单条导出（附带 kind/channelIndex/channelName）。
type Entry struct {
	Kind         string   `json:"kind"`
	ChannelIndex int      `json:"channelIndex"`
	ChannelName  string   `json:"channelName,omitempty"`
	Snapshot     Snapshot `json:"snapshot"`
}

// Snapshot 导出全部熔断器状态。
func (m *Manager) Snapshot() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries := make([]Entry, 0, len(m.breakers))
	for key, breaker := range m.breakers {
		kind, index := parseManagerKey(key)
		entries = append(entries, Entry{
			Kind:         kind,
			ChannelIndex: index,
			ChannelName:  m.names[key],
			Snapshot:     breaker.Snapshot(),
		})
	}
	return entries
}

// SnapshotEntry 返回单个渠道的快照；渠道不存在时返回 Closed 零值快照。
// 用于日志等只需要单渠道状态的场景，避免为读一个状态导出全量。
func (m *Manager) SnapshotEntry(kind string, channelIndex int) Snapshot {
	m.mu.Lock()
	breaker, exists := m.breakers[managerKey(kind, channelIndex)]
	name := m.names[managerKey(kind, channelIndex)]
	m.mu.Unlock()
	if !exists {
		return Snapshot{State: StateClosed}
	}
	snap := breaker.Snapshot()
	snap.ChannelName = name
	return snap
}

// parseManagerKey 拆分 "kind:channelIndex"。冒号只按最后一个出现位置拆分，
// kind 本身不含冒号（ChannelKind 枚举值），channelIndex 为十进制整数。
func parseManagerKey(key string) (string, int) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == ':' {
			kind := key[:i]
			index := -1
			if _, err := fmt.Sscanf(key[i+1:], "%d", &index); err != nil {
				return key, -1
			}
			return kind, index
		}
	}
	return key, -1
}
