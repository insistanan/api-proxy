// Package channelcrud 提供与协议无关的渠道管理 HTTP handler。
//
// 背景：messages/responses/gemini/chat/images 五个协议包各自复制了一份几乎相同的
// channels.go（约 2900 行），仅配置方法名与 upstream 切片不同。本包把这些收敛为
// 一份实现：具体协议包只需提供一组配置操作（Ops，闭包绑定自己的切片与 ChannelKind），
// 其余逻辑（CRUD、key 管理、reorder/status/promotion、负载均衡、Ping）全部复用本包。
//
// 行为基准：统一采用五份实现中"最完整"的版本——
//   - Ping 按 ServiceType 注入对应的客户端请求头（Codex / Claude CLI），
//     避免简化版漏掉请求头导致上游误判；
//   - 错误处理取各实现中最友好的分支（无效索引→404、密钥冲突→400 等）；
//   - 成功文案统一为中文。
package channelcrud

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// Ops 描述某一种渠道类型所需的全部配置操作。
// 由具体协议包构造（闭包绑定自己的 upstream 切片与 ChannelKind），
// 使本包的实现完全不感知渠道类型的存在。
type Ops struct {
	// Kind 渠道类型，用于指标清理、trace 亲和性移除等调度器交互。
	Kind scheduler.ChannelKind

	// List 返回该渠道类型的上游列表（读取用）。
	List func() []config.UpstreamConfig
	// LoadBalance 返回当前负载均衡策略。
	LoadBalance func() string
	// GetClient 返回带超时/代理配置的标准 HTTP client（Ping 用）。
	GetClient func(ch *config.UpstreamConfig, timeout time.Duration) (*http.Client, error)

	// Add 添加上游，返回创建结果（含新渠道 ID 与索引）。
	Add func(config.UpstreamConfig) (config.AddedUpstream, error)
	// Update 部分更新上游，返回是否需要重置熔断指标。
	Update func(int, config.UpstreamUpdate) (bool, error)
	// Remove 删除上游，返回被删除的渠道（用于清理指标）。
	Remove func(int) (*config.UpstreamConfig, error)
	// AddKey 为指定渠道添加 API 密钥。
	AddKey func(int, string) error
	// RemoveKey 从指定渠道移除 API 密钥。
	RemoveKey func(int, string) error
	// MoveKeyTop 将 API 密钥移到列表顶部。
	MoveKeyTop func(int, string) error
	// MoveKeyBottom 将 API 密钥移到列表底部。
	MoveKeyBottom func(int, string) error
	// Reorder 按给定顺序重排渠道。
	Reorder func([]int) error
	// SetStatus 设置渠道状态。
	SetStatus func(int, string) error
	// SetPromotion 设置渠道促销期。
	SetPromotion func(int, time.Duration, int) error
	// SetLoadBalance 设置负载均衡策略。
	SetLoadBalance func(string) error
}

// Handlers 渠道管理 HTTP handler 集合，与协议无关。
type Handlers struct {
	ops *Ops
	sch *scheduler.ChannelScheduler // 更新/删除后同步调度器状态用（Ping 不需要）
}

// New 创建渠道管理 handler 集合。
func New(ops Ops, sch *scheduler.ChannelScheduler) *Handlers {
	return &Handlers{ops: &ops, sch: sch}
}

// resetMetrics 更新渠道后，如配置判定需重置熔断指标，则同步重置。
func (h *Handlers) resetMetrics(index int) {
	if h.sch == nil {
		return
	}
	h.sch.ResetChannelMetrics(index, h.ops.Kind)
}

// respondConfigError 统一响应配置写操作失败（R3-c）：
//   - 配置校验/操作错误（ConfigError）返回 400；
//   - 其余（持久化失败等）返回 500；
//   - 已过提交点的错误（文件已原子替换、内存与磁盘一致）额外携带
//     code=CONFIG_COMMITTED 与 committed=true：前端不得把该失败按"未提交"
//     自动重试——一次新增会被重试成两次；应提示用户刷新查看实际状态。
func respondConfigError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if config.IsConfigError(err) {
		status = http.StatusBadRequest
	}
	body := gin.H{"error": err.Error()}
	if config.IsCommittedError(err) {
		body["code"] = "CONFIG_COMMITTED"
		body["committed"] = true
	}
	c.JSON(status, body)
}

// GetUpstreams 获取上游列表（兼容前端 channels 字段名）
func (h *Handlers) GetUpstreams(c *gin.Context) {
	c.JSON(200, gin.H{
		"channels":    config.ChannelListToDTO(h.ops.List()),
		"loadBalance": h.ops.LoadBalance(),
	})
}

// AddUpstream 添加上游
func (h *Handlers) AddUpstream(c *gin.Context) {
	var upstream config.UpstreamConfig
	if err := c.ShouldBindJSON(&upstream); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	created, err := h.ops.Add(upstream)
	if err != nil {
		respondConfigError(c, err)
		return
	}

	c.JSON(200, gin.H{
		"message":   "上游已添加",
		"channel":   gin.H{"id": created.ID, "index": created.Index},
		"channelId": created.ID,
	})
}

// UpdateUpstream 更新上游
func (h *Handlers) UpdateUpstream(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid upstream ID"})
		return
	}

	var updates config.UpstreamUpdate
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	shouldResetMetrics, err := h.ops.Update(id, updates)
	if err != nil {
		if strings.Contains(err.Error(), "无效的上游索引") {
			c.JSON(404, gin.H{"error": "Upstream not found"})
			return
		}
		respondConfigError(c, err)
		return
	}

	if shouldResetMetrics {
		h.resetMetrics(id)
	}

	c.JSON(200, gin.H{"message": "上游已更新"})
}

// DeleteUpstream 删除上游
func (h *Handlers) DeleteUpstream(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid upstream ID"})
		return
	}

	removed, err := h.ops.Remove(id)
	if err != nil {
		if strings.Contains(err.Error(), "无效的上游索引") {
			c.JSON(404, gin.H{"error": "Upstream not found"})
			return
		}
		respondConfigError(c, err)
		return
	}

	// 删除成功后清理指标数据与 trace 亲和性绑定
	if h.sch != nil {
		h.sch.DeleteChannelMetrics(removed, id, h.ops.Kind)
		h.sch.GetTraceAffinityManager().RemoveByChannelForKind(string(h.ops.Kind), id)
	}

	c.JSON(200, gin.H{"message": "上游已删除", "removed": removed})
}

// AddApiKey 添加 API 密钥
func (h *Handlers) AddApiKey(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid upstream ID"})
		return
	}

	var req struct {
		APIKey string `json:"apiKey"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	if err := h.ops.AddKey(id, req.APIKey); err != nil {
		switch {
		case strings.Contains(err.Error(), "无效的上游索引"):
			c.JSON(404, gin.H{"error": "Upstream not found"})
		case strings.Contains(err.Error(), "API密钥已存在"):
			c.JSON(400, gin.H{"error": "API密钥已存在"})
		default:
			respondConfigError(c, err)
		}
		return
	}

	c.JSON(200, gin.H{"message": "API密钥已添加", "success": true})
}

// DeleteApiKey 删除 API 密钥
func (h *Handlers) DeleteApiKey(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid upstream ID"})
		return
	}

	apiKey := c.Param("apiKey")
	if apiKey == "" {
		c.JSON(400, gin.H{"error": "API key is required"})
		return
	}

	if err := h.ops.RemoveKey(id, apiKey); err != nil {
		switch {
		case strings.Contains(err.Error(), "无效的上游索引"):
			c.JSON(404, gin.H{"error": "Upstream not found"})
		case strings.Contains(err.Error(), "API密钥不存在"):
			c.JSON(404, gin.H{"error": "API key not found"})
		default:
			respondConfigError(c, err)
		}
		return
	}

	c.JSON(200, gin.H{"message": "API密钥已删除"})
}

// MoveApiKeyToTop 将 API 密钥移到顶部
func (h *Handlers) MoveApiKeyToTop(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid upstream ID"})
		return
	}

	apiKey := c.Param("apiKey")
	if apiKey == "" {
		c.JSON(400, gin.H{"error": "API key is required"})
		return
	}

	if err := h.ops.MoveKeyTop(id, apiKey); err != nil {
		respondConfigError(c, err)
		return
	}

	c.JSON(200, gin.H{"message": "API密钥已移到顶部"})
}

// MoveApiKeyToBottom 将 API 密钥移到底部
func (h *Handlers) MoveApiKeyToBottom(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid upstream ID"})
		return
	}

	apiKey := c.Param("apiKey")
	if apiKey == "" {
		c.JSON(400, gin.H{"error": "API key is required"})
		return
	}

	if err := h.ops.MoveKeyBottom(id, apiKey); err != nil {
		respondConfigError(c, err)
		return
	}

	c.JSON(200, gin.H{"message": "API密钥已移到底部"})
}

// UpdateLoadBalance 更新负载均衡策略
func (h *Handlers) UpdateLoadBalance(c *gin.Context) {
	var req struct {
		Strategy string `json:"strategy"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	if err := h.ops.SetLoadBalance(req.Strategy); err != nil {
		if strings.Contains(err.Error(), "无效的负载均衡策略") {
			respondConfigError(c, err)
			return
		}
		c.JSON(500, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(200, gin.H{"message": "负载均衡策略已更新", "strategy": req.Strategy})
}

// ReorderChannels 重新排序渠道
func (h *Handlers) ReorderChannels(c *gin.Context) {
	var req struct {
		Order []int `json:"order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	if err := h.ops.Reorder(req.Order); err != nil {
		respondConfigError(c, err)
		return
	}

	c.JSON(200, gin.H{"success": true, "message": "渠道顺序已更新"})
}

// SetChannelStatus 设置渠道状态
func (h *Handlers) SetChannelStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid channel ID"})
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	if err := h.ops.SetStatus(id, req.Status); err != nil {
		if strings.Contains(err.Error(), "无效的上游索引") {
			c.JSON(404, gin.H{"error": "Channel not found"})
			return
		}
		respondConfigError(c, err)
		return
	}

	c.JSON(200, gin.H{"success": true, "message": "渠道状态已更新", "status": req.Status})
}

// SetChannelPromotion 设置渠道促销期
// 促销期内的渠道会被优先选择，忽略 trace 亲和性
func (h *Handlers) SetChannelPromotion(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "无效的渠道 ID"})
		return
	}

	var req struct {
		Duration int `json:"duration"` // 促销期时长（秒），0 表示不设时间限制
		Count    int `json:"count"`    // 促销请求次数，0 表示不设次数限制
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求参数"})
		return
	}

	if err := h.ops.SetPromotion(id, time.Duration(req.Duration)*time.Second, req.Count); err != nil {
		respondConfigError(c, err)
		return
	}

	// duration 和 count 都为 0 时，视为清除促销
	message := "渠道促销期已设置"
	if req.Duration <= 0 && req.Count <= 0 {
		message = "渠道促销期已清除"
	}
	c.JSON(200, gin.H{
		"success":  true,
		"message":  message,
		"duration": req.Duration,
		"count":    req.Count,
	})
}

// PingChannel Ping 单个渠道
func (h *Handlers) PingChannel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
		return
	}

	list := h.ops.List()
	if id < 0 || id >= len(list) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
		return
	}

	channel := list[id]
	c.JSON(http.StatusOK, h.pingChannelWithAPIKey(&channel))
}

// PingAllChannels Ping 所有渠道
func (h *Handlers) PingAllChannels(c *gin.Context) {
	list := h.ops.List()
	results := make(chan gin.H)
	var wg sync.WaitGroup

	for i := range list {
		ch := list[i]
		wg.Add(1)
		go func(id int, channel config.UpstreamConfig) {
			defer wg.Done()
			result := h.pingChannelWithAPIKey(&channel)
			result["id"] = id
			result["name"] = channel.Name
			results <- result
		}(i, ch)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var finalResults []gin.H
	for res := range results {
		finalResults = append(finalResults, res)
	}

	c.JSON(http.StatusOK, finalResults)
}

// pingChannelWithAPIKey 使用真实 API 请求测试渠道（验证 URL + API Key）
// 按 ServiceType 注入对应的客户端请求头，确保各上游都能正确识别客户端。
func (h *Handlers) pingChannelWithAPIKey(ch *config.UpstreamConfig) gin.H {
	urls := ch.GetAllBaseURLs()
	if len(urls) == 0 {
		return gin.H{"success": false, "latency": 0, "status": "error", "error": "no_base_url"}
	}

	// 如果没有 API Key，回退到简单的连通性测试
	if len(ch.APIKeys) == 0 {
		return h.pingChannelURLs(ch)
	}

	// 使用第一个 API Key 测试（多 URL 并发，选最快的）
	apiKey := ch.APIKeys[0]

	type pingResult struct {
		url     string
		latency int64
		success bool
		err     string
	}

	results := make(chan pingResult, len(urls))
	for _, baseURL := range urls {
		go func(testURL string) {
			startTime := time.Now()
			testURL = strings.TrimSuffix(testURL, "/")

			// 所有上游都支持 /v1/models 作为连通性验证端点
			endpoint := testURL + "/v1/models"

			client, err := h.ops.GetClient(ch, 10*time.Second)
			if err != nil {
				results <- pingResult{url: testURL, latency: 0, success: false, err: err.Error()}
				return
			}
			req, err := http.NewRequest("GET", endpoint, nil)
			if err != nil {
				results <- pingResult{url: testURL, latency: 0, success: false, err: "req_creation_failed"}
				return
			}

			// 设置认证头
			if strings.HasPrefix(apiKey, "sk-ant-") {
				req.Header.Set("x-api-key", apiKey)
			} else {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}

			// 根据 ServiceType 补充对应客户端的请求头
			switch ch.ServiceType {
			case "responses":
				// Codex CLI 请求头（如果是 Responses API）
				req.Header.Set("X-Codex-Installation-Id", "proxy-ping-installation")
				req.Header.Set("X-Codex-Window-Id", "proxy-ping-window")
				req.Header.Set("X-Request-Id", fmt.Sprintf("ping-%d", time.Now().UnixNano()))
				req.Header.Set("User-Agent", "codex-cli")
			case "claude":
				// Claude Code CLI 请求头
				req.Header.Set("X-Claude-Code-Session-Id", fmt.Sprintf("ping-session-%d", time.Now().UnixNano()))
				req.Header.Set("X-App", "cli")
				req.Header.Set("X-Stainless-Lang", "js")
				req.Header.Set("X-Stainless-Package-Version", "0.74.0")
				req.Header.Set("X-Stainless-Runtime", "node")
				req.Header.Set("X-Stainless-Runtime-Version", "v22.12.0")
				req.Header.Set("X-Stainless-Os", "Linux")
				req.Header.Set("X-Stainless-Arch", "x64")
				req.Header.Set("X-Stainless-Retry-Count", "0")
				req.Header.Set("X-Stainless-Timeout", "600")
				req.Header.Set("User-Agent", "claude-cli/2.1.92 (external, cli)")
				req.Header.Set("anthropic-version", "2023-06-01")
			default:
				// 其他类型使用通用的 User-Agent
				req.Header.Set("User-Agent", "claude-cli/2.0.34 (external, cli)")
			}

			resp, err := client.Do(req)
			latency := time.Since(startTime).Milliseconds()
			if err != nil {
				results <- pingResult{url: testURL, latency: latency, success: false, err: err.Error()}
				return
			}
			defer resp.Body.Close()

			// 检查状态码（2xx 或 3xx 视为成功）
			success := resp.StatusCode >= 200 && resp.StatusCode < 400
			if !success {
				results <- pingResult{url: testURL, latency: latency, success: false, err: fmt.Sprintf("status_%d", resp.StatusCode)}
				return
			}
			results <- pingResult{url: testURL, latency: latency, success: true}
		}(baseURL)
	}

	// 收集结果，找最快的成功响应
	var bestResult *pingResult
	for i := 0; i < len(urls); i++ {
		r := <-results
		if r.success {
			if bestResult == nil || !bestResult.success || r.latency < bestResult.latency {
				bestResult = &r
			}
		} else if bestResult == nil || !bestResult.success {
			bestResult = &r
		}
	}

	if bestResult == nil {
		return gin.H{"success": false, "latency": 0, "status": "error", "error": "all_urls_failed"}
	}

	if bestResult.success {
		return gin.H{"success": true, "latency": bestResult.latency, "status": "healthy"}
	}
	return gin.H{"success": false, "latency": bestResult.latency, "status": "error", "error": bestResult.err}
}

// pingChannelURLs 简单的 URL 连通性测试（不验证 API Key）
func (h *Handlers) pingChannelURLs(ch *config.UpstreamConfig) gin.H {
	urls := ch.GetAllBaseURLs()
	if len(urls) == 0 {
		return gin.H{"success": false, "latency": 0, "status": "error", "error": "no_base_url"}
	}

	type pingResult struct {
		url     string
		latency int64
		success bool
		err     string
	}

	results := make(chan pingResult, len(urls))
	for _, url := range urls {
		go func(testURL string) {
			startTime := time.Now()
			testURL = strings.TrimSuffix(testURL, "/")

			client, err := h.ops.GetClient(ch, 5*time.Second)
			if err != nil {
				results <- pingResult{url: testURL, latency: 0, success: false, err: err.Error()}
				return
			}
			req, err := http.NewRequest("HEAD", testURL, nil)
			if err != nil {
				results <- pingResult{url: testURL, latency: 0, success: false, err: "req_creation_failed"}
				return
			}

			resp, err := client.Do(req)
			latency := time.Since(startTime).Milliseconds()
			if err != nil {
				results <- pingResult{url: testURL, latency: latency, success: false, err: err.Error()}
				return
			}
			resp.Body.Close()
			results <- pingResult{url: testURL, latency: latency, success: true}
		}(url)
	}

	var bestResult *pingResult
	for i := 0; i < len(urls); i++ {
		r := <-results
		if r.success {
			if bestResult == nil || !bestResult.success || r.latency < bestResult.latency {
				bestResult = &r
			}
		} else if bestResult == nil || !bestResult.success {
			bestResult = &r
		}
	}

	if bestResult == nil {
		return gin.H{"success": false, "latency": 0, "status": "error", "error": "all_urls_failed"}
	}

	if bestResult.success {
		return gin.H{"success": true, "latency": bestResult.latency, "status": "healthy"}
	}
	return gin.H{"success": false, "latency": bestResult.latency, "status": "error", "error": bestResult.err}
}
