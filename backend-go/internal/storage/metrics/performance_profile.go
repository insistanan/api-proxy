package metrics

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// PerformanceProfile 渠道性能画像（模型级别）
type PerformanceProfile struct {
	mu sync.RWMutex

	// 基础标识
	BaseURL    string
	APIKeys    []string
	Model      string // 模型名（已规范化，去除后缀）
	ChannelIdx int

	// 实时负载
	ActiveRequests int64 `json:"activeRequests"` // 当前活跃请求数

	// TPS 指标
	RecentTPS     float64   `json:"recentTps"`     // 最近1分钟TPS
	Peak1MinTPS   float64   `json:"peak1MinTps"`   // 1分钟峰值TPS
	Peak5MinTPS   float64   `json:"peak5MinTps"`   // 5分钟峰值TPS
	LastTPSUpdate time.Time `json:"lastTpsUpdate"` // 上次TPS更新时间

	// 延迟指标（毫秒）
	AvgTTFB         float64 `json:"avgTtfb"`         // 平均首字节时间
	AvgResponseTime float64 `json:"avgResponseTime"` // 平均总响应时间
	P50Latency      float64 `json:"p50Latency"`      // P50 延迟
	P95Latency      float64 `json:"p95Latency"`      // P95 延迟
	P99Latency      float64 `json:"p99Latency"`      // P99 延迟

	// Token 生成速度（tokens/秒）
	AvgTokenSpeed float64 `json:"avgTokenSpeed"` // 平均 token 生成速度
	MinTokenSpeed float64 `json:"minTokenSpeed"` // 最小速度
	MaxTokenSpeed float64 `json:"maxTokenSpeed"` // 最大速度

	// 可靠性指标
	SuccessRate        float64 `json:"successRate"`        // 成功率 0-100
	RecentSuccessRate  float64 `json:"recentSuccessRate"`  // 最近100次成功率
	AvgErrorRecoveryMs float64 `json:"avgErrorRecoveryMs"` // 平均故障恢复时间

	// 动态评分（综合健康度）
	HealthScore     float64   `json:"healthScore"`     // 0-100，越高越好
	LastScoreUpdate time.Time `json:"lastScoreUpdate"` // 上次评分更新时间

	// 性能等级（自动分级）
	PerformanceTier string `json:"performanceTier"` // S/A/B/C/D

	// 原始采样数据（用于计算百分位）
	ttfbSamples         []float64   // TTFB 采样（保留最近1000个）
	responseTimeSamples []float64   // 响应时间采样
	tokenSpeedSamples   []float64   // Token 速度采样
	tpsTimestamps       []time.Time // TPS 计算用的时间戳（最近1分钟）
	recentResults       []bool      // 最近100次请求结果（用于计算最近成功率）

	// 统计窗口
	sampleWindowSize int // 采样窗口大小（默认1000）
}

// PerformanceSnapshotView 是调度器和 API 报表读取画像时使用的只读快照。
type PerformanceSnapshotView struct {
	BaseURL           string
	APIKeys           []string
	Model             string
	ChannelIdx        int
	ActiveRequests    int64
	RecentTPS         float64
	Peak1MinTPS       float64
	Peak5MinTPS       float64
	LastTPSUpdate     time.Time
	AvgTTFB           float64
	AvgResponseTime   float64
	P50Latency        float64
	P95Latency        float64
	P99Latency        float64
	AvgTokenSpeed     float64
	MinTokenSpeed     float64
	MaxTokenSpeed     float64
	SuccessRate       float64
	RecentSuccessRate float64
	HealthScore       float64
	LastScoreUpdate   time.Time
	PerformanceTier   string
}

// PerformanceSnapshot 性能快照（用于请求追踪）
type PerformanceSnapshot struct {
	RequestID       uint64
	StartTime       time.Time
	FirstByteTime   *time.Time // 首字节时间
	EndTime         *time.Time // 结束时间
	Success         bool
	OutputTokens    int64
	TotalDurationMs float64 // 总耗时
	TTFBMs          float64 // 首字节耗时
	TokenSpeed      float64 // token/s
}

// ProfileManager 性能画像管理器
type ProfileManager struct {
	mu       sync.RWMutex
	profiles map[string]*PerformanceProfile // key: hash(baseURL + sorted(apiKeys) + model)

	// 追踪进行中的请求
	activeSnapshots map[uint64]*PerformanceSnapshot

	// 后台更新任务
	stopCh         chan struct{}
	doneCh         chan struct{}
	stopOnce       sync.Once
	updateInterval time.Duration // 评分更新间隔（默认30秒）
}

// NewProfileManager 创建性能画像管理器
func NewProfileManager() *ProfileManager {
	pm := &ProfileManager{
		profiles:        make(map[string]*PerformanceProfile),
		activeSnapshots: make(map[uint64]*PerformanceSnapshot),
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
		updateInterval:  30 * time.Second,
	}

	// 启动后台评分更新任务
	go pm.backgroundScoreUpdater()

	return pm
}

// normalizeModel 规范化模型名，是性能画像 key 的唯一归一化入口。
// 目前只去首尾空白：模型名按客户端发来的原样参与画像归并，不做后缀截断。
func normalizeModel(model string) string {
	return strings.TrimSpace(model)
}

// generateProfileKey 生成画像键（包含模型与渠道维度）
func generateProfileKey(baseURL string, apiKeys []string, model string, channelIdx int) string {
	// 排序 keys 确保一致性
	sorted := make([]string, len(apiKeys))
	copy(sorted, apiKeys)
	sort.Strings(sorted)

	// 规范化模型名
	normalizedModel := normalizeModel(model)

	var combined string
	combined = baseURL
	for _, k := range sorted {
		combined += "|" + k
	}
	combined += "|" + normalizedModel

	return generateMetricsKey(baseURL, combined, channelIdx)
}

// GetOrCreateProfile 获取或创建性能画像（模型级别）
func (pm *ProfileManager) GetOrCreateProfile(baseURL string, apiKeys []string, model string, channelIdx int) *PerformanceProfile {
	key := generateProfileKey(baseURL, apiKeys, model, channelIdx)

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if profile, exists := pm.profiles[key]; exists {
		return profile
	}

	normalizedModel := normalizeModel(model)

	profile := &PerformanceProfile{
		BaseURL:             baseURL,
		APIKeys:             apiKeys,
		Model:               normalizedModel,
		ChannelIdx:          channelIdx,
		sampleWindowSize:    1000,
		HealthScore:         80.0, // 初始80分（新模型给中等分数）
		PerformanceTier:     "A",
		ttfbSamples:         make([]float64, 0, 1000),
		responseTimeSamples: make([]float64, 0, 1000),
		tokenSpeedSamples:   make([]float64, 0, 1000),
		tpsTimestamps:       make([]time.Time, 0, 100),
		recentResults:       make([]bool, 0, 100),
		SuccessRate:         100.0,
		RecentSuccessRate:   100.0,
	}

	pm.profiles[key] = profile
	return profile
}

// GetProfile 获取已存在的性能画像（不创建）
func (pm *ProfileManager) GetProfile(baseURL string, apiKeys []string, model string, channelIdx int) *PerformanceProfile {
	key := generateProfileKey(baseURL, apiKeys, model, channelIdx)

	pm.mu.RLock()
	defer pm.mu.RUnlock()

	return pm.profiles[key]
}

// RequestOutcome 表示请求结束时对画像的统计口径。
type RequestOutcome int

const (
	RequestOutcomeFailure RequestOutcome = iota
	RequestOutcomeSuccess
	RequestOutcomeNeutral
)

// GetProfileSnapshot 获取已存在画像的只读快照。
func (pm *ProfileManager) GetProfileSnapshot(baseURL string, apiKeys []string, model string, channelIdx int) (PerformanceSnapshotView, bool) {
	profile := pm.GetProfile(baseURL, apiKeys, model, channelIdx)
	if profile == nil {
		return PerformanceSnapshotView{}, false
	}
	return profile.Snapshot(), true
}

// GetAllProfileSnapshots 获取所有画像的只读快照。
func (pm *ProfileManager) GetAllProfileSnapshots() []PerformanceSnapshotView {
	pm.mu.RLock()
	profiles := make([]*PerformanceProfile, 0, len(pm.profiles))
	for _, p := range pm.profiles {
		profiles = append(profiles, p)
	}
	pm.mu.RUnlock()

	snapshots := make([]PerformanceSnapshotView, 0, len(profiles))
	for _, p := range profiles {
		snapshots = append(snapshots, p.Snapshot())
	}
	return snapshots
}

// Snapshot 返回当前画像的线程安全只读副本。
func (p *PerformanceProfile) Snapshot() PerformanceSnapshotView {
	if p == nil {
		return PerformanceSnapshotView{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	return PerformanceSnapshotView{
		BaseURL:           p.BaseURL,
		APIKeys:           append([]string(nil), p.APIKeys...),
		Model:             p.Model,
		ChannelIdx:        p.ChannelIdx,
		ActiveRequests:    p.ActiveRequests,
		RecentTPS:         p.RecentTPS,
		Peak1MinTPS:       p.Peak1MinTPS,
		Peak5MinTPS:       p.Peak5MinTPS,
		LastTPSUpdate:     p.LastTPSUpdate,
		AvgTTFB:           p.AvgTTFB,
		AvgResponseTime:   p.AvgResponseTime,
		P50Latency:        p.P50Latency,
		P95Latency:        p.P95Latency,
		P99Latency:        p.P99Latency,
		AvgTokenSpeed:     p.AvgTokenSpeed,
		MinTokenSpeed:     p.MinTokenSpeed,
		MaxTokenSpeed:     p.MaxTokenSpeed,
		SuccessRate:       p.SuccessRate,
		RecentSuccessRate: p.RecentSuccessRate,
		HealthScore:       p.HealthScore,
		LastScoreUpdate:   p.LastScoreUpdate,
		PerformanceTier:   p.PerformanceTier,
	}
}

// StartRequest 开始追踪请求
func (pm *ProfileManager) StartRequest(baseURL string, apiKeys []string, model string, channelIdx int, requestID uint64) {
	profile := pm.GetOrCreateProfile(baseURL, apiKeys, model, channelIdx)

	pm.mu.Lock()
	defer pm.mu.Unlock()

	// 增加活跃计数
	profile.mu.Lock()
	profile.ActiveRequests++
	profile.mu.Unlock()

	// 创建快照
	pm.activeSnapshots[requestID] = &PerformanceSnapshot{
		RequestID: requestID,
		StartTime: time.Now(),
	}
}

// RecordFirstByte 记录首字节时间
func (pm *ProfileManager) RecordFirstByte(requestID uint64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	snapshot, exists := pm.activeSnapshots[requestID]
	if !exists {
		return
	}

	now := time.Now()
	snapshot.FirstByteTime = &now
	snapshot.TTFBMs = float64(now.Sub(snapshot.StartTime).Milliseconds())
}

// EndRequest 结束请求追踪
func (pm *ProfileManager) EndRequest(baseURL string, apiKeys []string, model string, channelIdx int, requestID uint64, success bool, outputTokens int64) {
	outcome := RequestOutcomeFailure
	if success {
		outcome = RequestOutcomeSuccess
	}
	pm.EndRequestWithOutcome(baseURL, apiKeys, model, channelIdx, requestID, outcome, outputTokens)
}

// EndRequestNeutral 结束请求但不计入成功率，适用于客户端取消等非上游质量事件。
func (pm *ProfileManager) EndRequestNeutral(baseURL string, apiKeys []string, model string, channelIdx int, requestID uint64) {
	pm.EndRequestWithOutcome(baseURL, apiKeys, model, channelIdx, requestID, RequestOutcomeNeutral, 0)
}

// EndRequestWithOutcome 结束请求追踪并按指定口径更新画像。
func (pm *ProfileManager) EndRequestWithOutcome(baseURL string, apiKeys []string, model string, channelIdx int, requestID uint64, outcome RequestOutcome, outputTokens int64) {
	profile := pm.GetOrCreateProfile(baseURL, apiKeys, model, channelIdx)

	pm.mu.Lock()
	snapshot, exists := pm.activeSnapshots[requestID]
	if exists {
		delete(pm.activeSnapshots, requestID)
	}
	pm.mu.Unlock()

	if !exists {
		// 降级处理：没有开始追踪的请求
		profile.mu.Lock()
		if profile.ActiveRequests > 0 {
			profile.ActiveRequests--
		}
		profile.mu.Unlock()
		return
	}

	// 减少活跃计数
	profile.mu.Lock()
	if profile.ActiveRequests > 0 {
		profile.ActiveRequests--
	}
	profile.mu.Unlock()

	// 完善快照数据
	now := time.Now()
	snapshot.EndTime = &now
	snapshot.Success = outcome == RequestOutcomeSuccess
	snapshot.OutputTokens = outputTokens
	snapshot.TotalDurationMs = float64(now.Sub(snapshot.StartTime).Milliseconds())

	// 计算 token 生成速度
	if outcome == RequestOutcomeSuccess && outputTokens > 0 && snapshot.TotalDurationMs > 0 {
		durationSec := snapshot.TotalDurationMs / 1000.0
		snapshot.TokenSpeed = float64(outputTokens) / durationSec
	}

	// 更新性能画像
	pm.updateProfileWithSnapshot(profile, snapshot, outcome)
}

// updateProfileWithSnapshot 用快照数据更新画像
func (pm *ProfileManager) updateProfileWithSnapshot(profile *PerformanceProfile, snapshot *PerformanceSnapshot, outcome RequestOutcome) {
	profile.mu.Lock()
	defer profile.mu.Unlock()

	now := time.Now()

	// 更新 TPS
	profile.tpsTimestamps = append(profile.tpsTimestamps, now)
	// 只保留最近1分钟的时间戳
	cutoff := now.Add(-1 * time.Minute)
	validIdx := 0
	for i, ts := range profile.tpsTimestamps {
		if ts.After(cutoff) {
			validIdx = i
			break
		}
	}
	profile.tpsTimestamps = profile.tpsTimestamps[validIdx:]

	// 计算 TPS
	if len(profile.tpsTimestamps) > 0 {
		duration := now.Sub(profile.tpsTimestamps[0]).Seconds()
		if duration > 0 {
			tps := float64(len(profile.tpsTimestamps)) / duration
			profile.RecentTPS = tps
			profile.LastTPSUpdate = now

			// 更新峰值
			if tps > profile.Peak1MinTPS {
				profile.Peak1MinTPS = tps
			}
			if tps > profile.Peak5MinTPS {
				profile.Peak5MinTPS = tps
			}
		}
	}

	if outcome != RequestOutcomeNeutral {
		// 更新成功率（最近100次）
		profile.recentResults = append(profile.recentResults, snapshot.Success)
		if len(profile.recentResults) > 100 {
			profile.recentResults = profile.recentResults[1:]
		}
		recentSuccessCount := 0
		for _, r := range profile.recentResults {
			if r {
				recentSuccessCount++
			}
		}
		profile.RecentSuccessRate = float64(recentSuccessCount) / float64(len(profile.recentResults)) * 100
	}

	// 更新延迟指标（只记录成功的请求）
	if outcome == RequestOutcomeSuccess {
		// TTFB
		if snapshot.TTFBMs > 0 {
			profile.ttfbSamples = append(profile.ttfbSamples, snapshot.TTFBMs)
			if len(profile.ttfbSamples) > profile.sampleWindowSize {
				profile.ttfbSamples = profile.ttfbSamples[1:]
			}
			profile.AvgTTFB = calculateAverage(profile.ttfbSamples)
		}

		// 响应时间
		if snapshot.TotalDurationMs > 0 {
			profile.responseTimeSamples = append(profile.responseTimeSamples, snapshot.TotalDurationMs)
			if len(profile.responseTimeSamples) > profile.sampleWindowSize {
				profile.responseTimeSamples = profile.responseTimeSamples[1:]
			}
			profile.AvgResponseTime = calculateAverage(profile.responseTimeSamples)

			// 计算百分位
			profile.P50Latency = calculatePercentile(profile.responseTimeSamples, 0.50)
			profile.P95Latency = calculatePercentile(profile.responseTimeSamples, 0.95)
			profile.P99Latency = calculatePercentile(profile.responseTimeSamples, 0.99)
		}

		// Token 生成速度
		if snapshot.TokenSpeed > 0 {
			profile.tokenSpeedSamples = append(profile.tokenSpeedSamples, snapshot.TokenSpeed)
			if len(profile.tokenSpeedSamples) > profile.sampleWindowSize {
				profile.tokenSpeedSamples = profile.tokenSpeedSamples[1:]
			}
			profile.AvgTokenSpeed = calculateAverage(profile.tokenSpeedSamples)
			profile.MinTokenSpeed = calculateMin(profile.tokenSpeedSamples)
			profile.MaxTokenSpeed = calculateMax(profile.tokenSpeedSamples)
		}
	}
}

// backgroundScoreUpdater 后台定期更新健康评分
func (pm *ProfileManager) backgroundScoreUpdater() {
	defer close(pm.doneCh)
	ticker := time.NewTicker(pm.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pm.stopCh:
			return
		case <-ticker.C:
			pm.updateAllScores()
		}
	}
}

// updateAllScores 更新所有渠道的健康评分
func (pm *ProfileManager) updateAllScores() {
	pm.mu.RLock()
	profiles := make([]*PerformanceProfile, 0, len(pm.profiles))
	for _, p := range pm.profiles {
		profiles = append(profiles, p)
	}
	pm.mu.RUnlock()

	for _, profile := range profiles {
		pm.calculateHealthScore(profile)
	}
}

// calculateHealthScore 计算健康评分（0-100）
func (pm *ProfileManager) calculateHealthScore(profile *PerformanceProfile) {
	profile.mu.Lock()
	defer profile.mu.Unlock()

	now := time.Now()

	// 评分组成（权重可调）:
	// 1. 成功率 (40%)
	// 2. 响应速度 (30%): TTFB + P95延迟
	// 3. Token生成速度 (15%)
	// 4. TPS能力 (15%)

	var score float64 = 0

	// 1. 成功率评分 (0-40分)
	successScore := profile.RecentSuccessRate * 0.4
	score += successScore

	// 2. 响应速度评分 (0-30分)，无样本时给中性分，避免冷启动过早降权
	// TTFB: <500ms=15分, 500-1000ms=10-15分, 1000-2000ms=5-10分, >2000ms=0-5分
	ttfbScore := 10.0
	if profile.AvgTTFB > 0 {
		switch {
		case profile.AvgTTFB < 500:
			ttfbScore = 15.0
		case profile.AvgTTFB < 1000:
			ttfbScore = 15.0 - (profile.AvgTTFB-500)/500*5
		case profile.AvgTTFB < 2000:
			ttfbScore = 10.0 - (profile.AvgTTFB-1000)/1000*5
		default:
			ttfbScore = math.Max(0, 5.0-(profile.AvgTTFB-2000)/1000)
		}
	}

	// P95延迟: <2000ms=15分, 2000-5000ms=10-15分, 5000-10000ms=5-10分, >10000ms=0-5分
	p95Score := 10.0
	if profile.P95Latency > 0 {
		switch {
		case profile.P95Latency < 2000:
			p95Score = 15.0
		case profile.P95Latency < 5000:
			p95Score = 15.0 - (profile.P95Latency-2000)/3000*5
		case profile.P95Latency < 10000:
			p95Score = 10.0 - (profile.P95Latency-5000)/5000*5
		default:
			p95Score = math.Max(0, 5.0-(profile.P95Latency-10000)/5000)
		}
	}

	score += ttfbScore + p95Score

	// 3. Token生成速度评分 (0-15分)
	// >50 tokens/s=15分, 30-50=10-15分, 10-30=5-10分, <10=0-5分
	tokenSpeedScore := 8.0
	if profile.AvgTokenSpeed > 0 {
		switch {
		case profile.AvgTokenSpeed >= 50:
			tokenSpeedScore = 15.0
		case profile.AvgTokenSpeed >= 30:
			tokenSpeedScore = 10.0 + (profile.AvgTokenSpeed-30)/20*5
		case profile.AvgTokenSpeed >= 10:
			tokenSpeedScore = 5.0 + (profile.AvgTokenSpeed-10)/20*5
		default:
			tokenSpeedScore = profile.AvgTokenSpeed / 10 * 5
		}
	}
	score += tokenSpeedScore

	// 4. TPS能力评分 (0-15分)
	// >10 TPS=15分, 5-10=10-15分, 1-5=5-10分, <1=0-5分
	tpsScore := 8.0
	if profile.RecentTPS > 0 {
		switch {
		case profile.RecentTPS >= 10:
			tpsScore = 15.0
		case profile.RecentTPS >= 5:
			tpsScore = 10.0 + (profile.RecentTPS-5)/5*5
		case profile.RecentTPS >= 1:
			tpsScore = 5.0 + (profile.RecentTPS-1)/4*5
		default:
			tpsScore = profile.RecentTPS * 5
		}
	}
	score += tpsScore

	// 惩罚因子：活跃请求过多时降分
	if profile.ActiveRequests > 20 {
		overloadPenalty := math.Min(20.0, float64(profile.ActiveRequests-20)*0.5)
		score -= overloadPenalty
	}

	// 确保分数在 0-100 范围内
	score = math.Max(0, math.Min(100, score))

	profile.HealthScore = score
	profile.LastScoreUpdate = now

	// 自动分级
	profile.PerformanceTier = pm.calculateTier(score)
}

// calculateTier 根据评分计算性能等级
func (pm *ProfileManager) calculateTier(score float64) string {
	return calculateTierFromScore(score)
}

func calculateTierFromScore(score float64) string {
	switch {
	case score >= 90:
		return "S"
	case score >= 75:
		return "A"
	case score >= 60:
		return "B"
	case score >= 40:
		return "C"
	default:
		return "D"
	}
}

// Stop 停止并等待后台评分退出；并发或重复调用共享同一次关闭。
func (pm *ProfileManager) Stop() {
	pm.stopOnce.Do(func() { close(pm.stopCh) })
	<-pm.doneCh
}

// ============== 统计辅助函数 ==============

func calculateAverage(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range samples {
		sum += v
	}
	return sum / float64(len(samples))
}

func calculateMin(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	min := samples[0]
	for _, v := range samples {
		if v < min {
			min = v
		}
	}
	return min
}

func calculateMax(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	max := samples[0]
	for _, v := range samples {
		if v > max {
			max = v
		}
	}
	return max
}

func calculatePercentile(samples []float64, percentile float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)

	idx := int(float64(len(sorted)) * percentile)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
