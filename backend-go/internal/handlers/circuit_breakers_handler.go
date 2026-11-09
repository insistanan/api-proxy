// 渠道级熔断器查询与手动重置 API（internal/circuit 的 HTTP 出口）。
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/scheduler"
)

// GetCircuitBreakers 返回全部渠道熔断器快照。
// 可选查询参数 kind 过滤协议类型（messages/responses/gemini/chat/images）。
func GetCircuitBreakers(sch *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		kindFilter := c.Query("kind")
		entries := sch.CircuitManagerSnapshot()
		if kindFilter != "" {
			filtered := make([]interface{}, 0)
			for _, entry := range entries {
				if entry.Kind == kindFilter {
					filtered = append(filtered, entry)
				}
			}
			c.JSON(http.StatusOK, gin.H{"entries": filtered})
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries})
	}
}

// ResetCircuitBreaker 手动重置指定渠道的熔断器。
// body: {"kind": "messages", "channelIndex": 0}
func ResetCircuitBreaker(sch *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Kind         string `json:"kind"`
			ChannelIndex *int   `json:"channelIndex"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体必须是 {kind, channelIndex} JSON", "detail": err.Error()})
			return
		}
		if body.ChannelIndex == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "channelIndex 必须提供"})
			return
		}
		if body.Kind == "" || *body.ChannelIndex < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "kind 不能为空且 channelIndex 不能小于 0"})
			return
		}
		if !sch.ResetCircuitBreaker(body.Kind, *body.ChannelIndex) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":        "该渠道没有熔断记录（未熔断或不存在）",
				"kind":         body.Kind,
				"channelIndex": *body.ChannelIndex,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"reset":        true,
			"kind":         body.Kind,
			"channelIndex": *body.ChannelIndex,
		})
	}
}
