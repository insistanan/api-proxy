// Package channelcrud 提供按渠道类型复用的渠道管理 HTTP handler。
package channelcrud

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/management/modelcatalog"
	"insistanan/api-proxy/internal/orchestration/scheduler"
)

type channelConfig interface {
	GetChannelConfig(string) ([]config.UpstreamConfig, string, error)
	AddChannel(string, config.UpstreamConfig) (config.AddedUpstream, error)
	UpdateChannel(string, int, config.UpstreamUpdate) (bool, error)
	RemoveChannel(string, int, ...string) (*config.UpstreamConfig, error)
	AddChannelKey(string, int, string, ...string) error
	RemoveChannelKey(string, int, string, ...string) error
	MoveChannelKeyTop(string, int, string, ...string) error
	MoveChannelKeyBottom(string, int, string, ...string) error
	ReorderChannels(string, []int) error
	SetChannelStatusFor(string, int, string, ...string) error
	SetChannelPromotionFor(string, int, time.Duration, int, ...string) error
	SetLoadBalanceFor(string, string) error
}

// Handlers 渠道管理 HTTP handler 集合，与协议无关。
type Handlers struct {
	cfg      channelConfig
	kind     scheduler.ChannelKind
	discover func(context.Context, *config.UpstreamConfig, string) (modelcatalog.ModelsResponse, error)
	sch      *scheduler.ChannelScheduler // 更新/删除后同步调度器状态用（Ping 不需要）
}

// New 创建渠道管理 handler 集合。
func New(cfg *config.ConfigManager, sch *scheduler.ChannelScheduler, kind scheduler.ChannelKind) *Handlers {
	return &Handlers{cfg: cfg, sch: sch, kind: kind, discover: func(ctx context.Context, ch *config.UpstreamConfig, key string) (modelcatalog.ModelsResponse, error) {
		return modelcatalog.DiscoverUpstreamModels(ctx, cfg, ch, key)
	}}
}

// resetMetrics 更新渠道后，如配置判定需重置熔断指标，则同步重置。
func (h *Handlers) resetMetrics(index int) {
	if h.sch == nil {
		return
	}
	h.sch.ResetChannelMetrics(index, h.kind)
}

// respondConfigError 响应配置写操作失败：配置错误返回 400，其余返回 500。
// 已过提交点的错误额外携带 code=CONFIG_COMMITTED 与 committed=true，
// 前端不得将其当作未提交而自动重试。
func respondConfigError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if config.IsConfigError(err) {
		status = http.StatusBadRequest
	} else if errors.Is(err, config.ErrChannelNotFound) {
		status = http.StatusNotFound
	} else if errors.Is(err, config.ErrChannelIdentityConflict) || errors.Is(err, config.ErrConfigConflict) {
		status = http.StatusConflict
	}
	body := gin.H{"error": err.Error()}
	if config.IsCommittedError(err) {
		status = http.StatusInternalServerError
		body["code"] = "CONFIG_COMMITTED"
		body["committed"] = true
	}
	c.JSON(status, body)
}

// GetUpstreams 获取上游列表（兼容前端 channels 字段名）
func (h *Handlers) GetUpstreams(c *gin.Context) {
	list, strategy, err := h.cfg.GetChannelConfig(string(h.kind))
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.JSON(200, gin.H{
		"channels":    config.ChannelListToDTO(list),
		"loadBalance": strategy,
	})
}

// AddUpstream 添加上游
func (h *Handlers) AddUpstream(c *gin.Context) {
	var upstream config.UpstreamConfig
	if err := c.ShouldBindJSON(&upstream); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	// 普通新增由服务端分配身份；导入必须另行校验完整候选。
	upstream.ID = ""
	created, err := h.cfg.AddChannel(string(h.kind), upstream)
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

	shouldResetMetrics, err := h.cfg.UpdateChannel(string(h.kind), id, updates)
	if errors.Is(err, config.ErrChannelIdentityConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "channel_identity_conflict"})
		return
	}
	if shouldResetMetrics && (err == nil || config.IsCommittedError(err)) {
		h.resetMetrics(id)
	}
	if err != nil {
		respondConfigError(c, err)
		return
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

	removed, err := h.cfg.RemoveChannel(string(h.kind), id, c.GetHeader("X-Channel-ID"))
	// 已提交错误表示删除已经生效，指标与亲和性也必须同步清理。
	if h.sch != nil && (err == nil || config.IsCommittedError(err)) {
		h.sch.DeleteChannelMetrics(removed, id, h.kind)
		h.sch.GetTraceAffinityManager().RemoveByChannelForKind(string(h.kind), id)
	}
	if err != nil {
		respondConfigError(c, err)
		return
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
		APIKey string `json:"apiKey" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	if err := h.cfg.AddChannelKey(string(h.kind), id, req.APIKey, c.GetHeader("X-Channel-ID")); err != nil {
		respondConfigError(c, err)
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

	var req struct {
		APIKey string `json:"apiKey" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "必须提供 API 密钥"})
		return
	}
	apiKey := req.APIKey

	if err := h.cfg.RemoveChannelKey(string(h.kind), id, apiKey, c.GetHeader("X-Channel-ID")); err != nil {
		respondConfigError(c, err)
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

	var req struct {
		APIKey string `json:"apiKey" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "必须提供 API 密钥"})
		return
	}
	apiKey := req.APIKey

	if err := h.cfg.MoveChannelKeyTop(string(h.kind), id, apiKey, c.GetHeader("X-Channel-ID")); err != nil {
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

	var req struct {
		APIKey string `json:"apiKey" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "必须提供 API 密钥"})
		return
	}
	apiKey := req.APIKey

	if err := h.cfg.MoveChannelKeyBottom(string(h.kind), id, apiKey, c.GetHeader("X-Channel-ID")); err != nil {
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

	if err := h.cfg.SetLoadBalanceFor(string(h.kind), req.Strategy); err != nil {
		respondConfigError(c, err)
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

	if err := h.cfg.ReorderChannels(string(h.kind), req.Order); err != nil {
		respondConfigError(c, err)
		return
	}

	c.JSON(200, gin.H{"success": true, "message": "渠道顺序已更新"})
}

// SetChannelStatus 设置渠道状态；手动激活同时重置运行时熔断。
func (h *Handlers) SetChannelStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid channel ID"})
		return
	}

	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return
	}

	err = h.cfg.SetChannelStatusFor(string(h.kind), id, req.Status, c.GetHeader("X-Channel-ID"))
	if (err == nil || config.IsCommittedError(err)) &&
		strings.EqualFold(strings.TrimSpace(req.Status), config.ChannelStatusActive) {
		h.resetMetrics(id)
	}
	if err != nil {
		respondConfigError(c, err)
		return
	}

	c.JSON(200, gin.H{"success": true, "message": "渠道状态已更新", "status": req.Status})
}

// SetChannelPromotion 设置渠道促销期
// 促销只在同组内优先，会话亲和性仍优先于促销。
func (h *Handlers) SetChannelPromotion(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "无效的渠道 ID"})
		return
	}

	var req struct {
		Duration *int `json:"duration"` // 促销期时长（秒），0 表示不设时间限制
		Count    *int `json:"count"`    // 促销请求次数，0 表示不设次数限制
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求参数"})
		return
	}
	if req.Duration == nil && req.Count == nil {
		c.JSON(400, gin.H{"error": "必须提供 duration 或 count；清除促销请显式传 0"})
		return
	}
	duration, count := 0, 0
	if req.Duration != nil {
		duration = *req.Duration
	}
	if req.Count != nil {
		count = *req.Count
	}
	if duration < 0 || count < 0 || int64(duration) > int64((1<<63-1)/time.Second) {
		c.JSON(400, gin.H{"error": "促销时长和次数必须是有效的非负整数"})
		return
	}

	if err := h.cfg.SetChannelPromotionFor(string(h.kind), id, time.Duration(duration)*time.Second, count, c.GetHeader("X-Channel-ID")); err != nil {
		respondConfigError(c, err)
		return
	}

	// duration 和 count 都为 0 时，视为清除促销
	message := "渠道促销期已设置"
	if duration == 0 && count == 0 {
		message = "渠道促销期已清除"
	}
	c.JSON(200, gin.H{
		"success":  true,
		"message":  message,
		"duration": duration,
		"count":    count,
	})
}
