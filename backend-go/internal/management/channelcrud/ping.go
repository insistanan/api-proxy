package channelcrud

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/infra/config"
)

// Ping 只验证模型目录访问，不能据此保证推理能力或模型权限。
func (h *Handlers) pingChannel(ctx context.Context, channel *config.UpstreamConfig) gin.H {
	key := ""
	if len(channel.APIKeys) > 0 {
		key = channel.APIKeys[0]
	}
	start := time.Now()
	_, err := h.discover(ctx, channel, key)
	result := gin.H{"success": err == nil, "latency": time.Since(start).Milliseconds(), "scope": "model_catalog", "status": "healthy"}
	if err != nil {
		result["status"], result["error"] = "error", err.Error()
	}
	return result
}

func (h *Handlers) PingChannel(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道 ID"})
		return
	}
	list, _, err := h.cfg.GetChannelConfig(string(h.kind))
	if err != nil {
		respondConfigError(c, err)
		return
	}
	if index < 0 || index >= len(list) || config.GetChannelStatus(&list[index]) == config.ChannelStatusDeleted {
		c.JSON(http.StatusNotFound, gin.H{"error": "渠道不存在"})
		return
	}
	c.JSON(http.StatusOK, h.pingChannel(c.Request.Context(), &list[index]))
}

func (h *Handlers) PingAllChannels(c *gin.Context) {
	list, _, err := h.cfg.GetChannelConfig(string(h.kind))
	if err != nil {
		respondConfigError(c, err)
		return
	}
	// 一次任务最多八个探测；URL 回退在协议发现器内串行执行，不再嵌套启动协程。
	ctx := c.Request.Context()
	jobs := make(chan int)
	results := make([]gin.H, len(list))
	var workers sync.WaitGroup
	for i := 0; i < min(8, len(list)); i++ {
		workers.Go(func() {
			for index := range jobs {
				if ctx.Err() != nil {
					continue
				}
				result := h.pingChannel(ctx, &list[index])
				result["id"], result["name"] = index, list[index].Name
				results[index] = result
			}
		})
	}
schedule:
	for i := range list {
		if config.GetChannelStatus(&list[i]) == config.ChannelStatusDeleted {
			continue
		}
		select {
		case jobs <- i:
		case <-ctx.Done():
			break schedule
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		c.JSON(http.StatusRequestTimeout, gin.H{"error": "渠道探测已取消"})
		return
	}
	visible := make([]gin.H, 0, len(results))
	for _, result := range results {
		if result != nil {
			visible = append(visible, result)
		}
	}
	c.JSON(http.StatusOK, visible)
}
