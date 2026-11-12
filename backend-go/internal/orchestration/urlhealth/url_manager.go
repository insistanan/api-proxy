// Package urlhealth 提供多端点渠道的 URL 健康管理和动态排序功能
package urlhealth

import (
	"log"
	"sort"
	"sync"
	"time"
)

// URLLatencyResult 单个 URL 的结果
type URLLatencyResult struct {
	URL         string
	OriginalIdx int  // 原始索引（用于指标记录）
	Success     bool // 是否可用（未在冷却期内）
}

// URLState URL 状态信息
type URLState struct {
	URL             string
	OriginalIdx     int       // 原始索引（用于指标记录）
	FailCount       int       // 连续失败次数
	LastFailTime    time.Time // 最后失败时间
	LastSuccessTime time.Time // 最后成功时间
	TotalRequests   int64     // 总请求数
	TotalFailures   int64     // 总失败数
}

// ChannelURLState 渠道 URL 状态
type ChannelURLState struct {
	ChannelIndex int
	URLs         []*URLState
	UpdatedAt    time.Time
}

// URLManager URL 管理器（非阻塞，基于 failover 动态排序）
type URLManager struct {
	mu              sync.RWMutex
	channelStates   map[int]*ChannelURLState // key: channelIndex
	failureCooldown time.Duration            // 失败冷却时间（过后允许重试）
	maxFailCount    int                      // 最大连续失败次数（超过则移到末尾）
}

// NewURLManager 创建 URL 管理器
func NewURLManager(failureCooldown time.Duration, maxFailCount int) *URLManager {
	if failureCooldown <= 0 {
		failureCooldown = 30 * time.Second // 默认 30 秒冷却
	}
	if maxFailCount <= 0 {
		maxFailCount = 3 // 默认连续 3 次失败后移到末尾
	}
	return &URLManager{
		channelStates:   make(map[int]*ChannelURLState),
		failureCooldown: failureCooldown,
		maxFailCount:    maxFailCount,
	}
}

// GetSortedURLs 获取排序后的 URL 列表（非阻塞，立即返回）
// 排序规则：
// 1. 成功的 URL 优先
// 2. 冷却期过后的失败 URL 可重试
// 3. 仍在冷却期的失败 URL 放到最后
func (m *URLManager) GetSortedURLs(channelIndex int, urls []string) []URLLatencyResult {
	if len(urls) == 0 {
		return nil
	}
	if len(urls) == 1 {
		return []URLLatencyResult{{URL: urls[0], OriginalIdx: 0, Success: true}}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.ensureChannelState(channelIndex, urls)
	m.sortURLs(state)

	now := time.Now()
	results := make([]URLLatencyResult, len(state.URLs))

	for i, urlState := range state.URLs {
		results[i] = URLLatencyResult{
			URL:         urlState.URL,
			OriginalIdx: urlState.OriginalIdx,
			Success:     urlState.FailCount == 0 || now.Sub(urlState.LastFailTime) >= m.failureCooldown,
		}
	}

	return results
}

// MarkSuccess 标记 URL 成功
func (m *URLManager) MarkSuccess(channelIndex int, url string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.channelStates[channelIndex]
	if !ok {
		return
	}

	for _, urlState := range state.URLs {
		if urlState.URL == url {
			urlState.FailCount = 0
			urlState.LastSuccessTime = time.Now()
			urlState.TotalRequests++
			break
		}
	}

	m.sortURLs(state)
	state.UpdatedAt = time.Now()
}

// MarkFailure 标记 URL 失败
func (m *URLManager) MarkFailure(channelIndex int, url string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.channelStates[channelIndex]
	if !ok {
		return
	}

	now := time.Now()
	for _, urlState := range state.URLs {
		if urlState.URL == url {
			urlState.FailCount++
			urlState.LastFailTime = now
			urlState.TotalRequests++
			urlState.TotalFailures++
			log.Printf("[URLManager] URL 失败: 渠道 [%d], URL: %s, 连续失败: %d", channelIndex, url, urlState.FailCount)
			break
		}
	}

	m.sortURLs(state)
	state.UpdatedAt = time.Now()
}

func (m *URLManager) ensureChannelState(channelIndex int, urls []string) *ChannelURLState {
	state, ok := m.channelStates[channelIndex]

	if !ok {
		state = &ChannelURLState{
			ChannelIndex: channelIndex,
			URLs:         make([]*URLState, len(urls)),
			UpdatedAt:    time.Now(),
		}
		for i, url := range urls {
			state.URLs[i] = &URLState{
				URL:         url,
				OriginalIdx: i,
			}
		}
		m.channelStates[channelIndex] = state
		return state
	}

	if !m.urlsMatch(state.URLs, urls) {
		log.Printf("[URLManager] 检测到渠道 [%d] URL 配置变化，重置状态", channelIndex)
		state = &ChannelURLState{
			ChannelIndex: channelIndex,
			URLs:         make([]*URLState, len(urls)),
			UpdatedAt:    time.Now(),
		}
		for i, url := range urls {
			state.URLs[i] = &URLState{
				URL:         url,
				OriginalIdx: i,
			}
		}
		m.channelStates[channelIndex] = state
	}

	return state
}

func (m *URLManager) urlsMatch(states []*URLState, urls []string) bool {
	if len(states) != len(urls) {
		return false
	}
	urlSet := make(map[string]bool)
	for _, url := range urls {
		urlSet[url] = true
	}
	for _, state := range states {
		if !urlSet[state.URL] {
			return false
		}
	}
	return true
}

// sortURLs 对 URL 列表排序
// 排序规则：
// 1. 无失败记录的 URL 在最前（按原始索引排序）
// 2. 冷却期已过的失败 URL 次之（按失败次数升序）
// 3. 仍在冷却期的失败 URL 在最后（按冷却剩余时间升序）
func (m *URLManager) sortURLs(state *ChannelURLState) {
	now := time.Now()

	sort.SliceStable(state.URLs, func(i, j int) bool {
		ui, uj := state.URLs[i], state.URLs[j]

		iNoFail := ui.FailCount == 0
		jNoFail := uj.FailCount == 0
		if iNoFail != jNoFail {
			return iNoFail
		}
		if iNoFail && jNoFail {
			return ui.OriginalIdx < uj.OriginalIdx
		}

		iCooldownPassed := now.Sub(ui.LastFailTime) >= m.failureCooldown
		jCooldownPassed := now.Sub(uj.LastFailTime) >= m.failureCooldown

		if iCooldownPassed != jCooldownPassed {
			return iCooldownPassed
		}

		if iCooldownPassed && jCooldownPassed {
			if ui.FailCount != uj.FailCount {
				return ui.FailCount < uj.FailCount
			}
			return ui.OriginalIdx < uj.OriginalIdx
		}

		iRemaining := m.failureCooldown - now.Sub(ui.LastFailTime)
		jRemaining := m.failureCooldown - now.Sub(uj.LastFailTime)
		return iRemaining < jRemaining
	})
}

// InvalidateAll 清除所有状态
func (m *URLManager) InvalidateAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channelStates = make(map[int]*ChannelURLState)
	log.Printf("[URLManager] 所有渠道状态已清除")
}
