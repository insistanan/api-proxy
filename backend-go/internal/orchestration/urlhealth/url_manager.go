// Package urlhealth 提供多端点渠道的 URL 健康管理和动态排序功能
package urlhealth

import (
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"log"
	"sort"
	"sync"
	"time"
)

// Candidate 是按优先级排列的地址，OriginalIdx 始终对应本次配置顺序。
type Candidate struct {
	URL         string
	OriginalIdx int
}

// InConfigOrder 构造配置顺序的候选，供不启用动态排序的调用方复用。
func InConfigOrder(urls []string) []Candidate {
	results := make([]Candidate, len(urls))
	for i, url := range urls {
		results[i] = Candidate{URL: url, OriginalIdx: i}
	}
	return results
}

type urlState struct {
	failCount    int
	lastFailTime time.Time
}

// URLManager URL 管理器（非阻塞，基于 failover 动态排序）
type URLManager struct {
	mu              sync.Mutex
	channelStates   map[int]map[string]*urlState
	failureCooldown time.Duration // 达到失败阈值后的降序时间
	maxFailCount    int           // 连续失败达到此值才降序
}

// NewURLManager 创建 URL 管理器；装配方必须提供正的冷却时间和失败阈值。
func NewURLManager(failureCooldown time.Duration, maxFailCount int) *URLManager {
	if failureCooldown <= 0 {
		panic("URL 健康管理的冷却时间必须大于 0")
	}
	if maxFailCount <= 0 {
		panic("URL 健康管理的失败阈值必须大于 0")
	}
	return &URLManager{
		channelStates:   make(map[int]map[string]*urlState),
		failureCooldown: failureCooldown,
		maxFailCount:    maxFailCount,
	}
}

// GetSortedURLs 获取排序后的 URL 列表（非阻塞，立即返回）
// 冷却只影响排序，不禁止尝试；调用方仍可遍历全部候选。
func (m *URLManager) GetSortedURLs(channelIndex int, urls []string) []Candidate {
	m.mu.Lock()
	defer m.mu.Unlock()

	states := m.syncChannelState(channelIndex, urls)
	results := InConfigOrder(urls)
	m.sortURLs(results, states, time.Now())
	return results
}

// MarkSuccess 标记 URL 成功
func (m *URLManager) MarkSuccess(channelIndex int, url string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if state := m.channelStates[channelIndex][url]; state != nil {
		state.failCount = 0
		state.lastFailTime = time.Time{}
	}
}

// MarkFailure 标记 URL 失败
func (m *URLManager) MarkFailure(channelIndex int, url string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 配置已移除的地址可能仍有在途请求，迟到回写不重新创建其状态。
	if state := m.channelStates[channelIndex][url]; state != nil {
		state.failCount++
		state.lastFailTime = time.Now()
		log.Printf("[URLManager] URL 失败: 渠道 [%d], URL: %s, 连续失败: %d", channelIndex, upstreamreq.RedactURL(url), state.failCount)
	}
}

// syncChannelState 按地址保留健康记录，顺序和原始索引由本次配置单独生成。
func (m *URLManager) syncChannelState(channelIndex int, urls []string) map[string]*urlState {
	previous := m.channelStates[channelIndex]
	current := make(map[string]*urlState, len(urls))
	for _, url := range urls {
		state := previous[url]
		if state == nil {
			state = &urlState{}
		}
		current[url] = state
	}
	if len(current) == 0 {
		delete(m.channelStates, channelIndex)
	} else {
		m.channelStates[channelIndex] = current
	}
	return current
}

// sortURLs 对 URL 列表排序
// 排序规则：
// 1. 未达到失败阈值的 URL 在最前（按当前配置索引排序）
// 2. 冷却期已过的失败 URL 次之（按失败次数升序）
// 3. 仍在冷却期的失败 URL 在最后（按冷却剩余时间升序）
func (m *URLManager) sortURLs(results []Candidate, states map[string]*urlState, now time.Time) {
	sort.SliceStable(results, func(i, j int) bool {
		ui, uj := states[results[i].URL], states[results[j].URL]
		iHealthy := ui.failCount < m.maxFailCount
		jHealthy := uj.failCount < m.maxFailCount
		if iHealthy != jHealthy {
			return iHealthy
		}
		if iHealthy {
			return results[i].OriginalIdx < results[j].OriginalIdx
		}

		iCooldownPassed := now.Sub(ui.lastFailTime) >= m.failureCooldown
		jCooldownPassed := now.Sub(uj.lastFailTime) >= m.failureCooldown

		if iCooldownPassed != jCooldownPassed {
			return iCooldownPassed
		}

		if iCooldownPassed && jCooldownPassed {
			if ui.failCount != uj.failCount {
				return ui.failCount < uj.failCount
			}
			return results[i].OriginalIdx < results[j].OriginalIdx
		}

		if !ui.lastFailTime.Equal(uj.lastFailTime) {
			return ui.lastFailTime.Before(uj.lastFailTime)
		}
		return results[i].OriginalIdx < results[j].OriginalIdx
	})
}
