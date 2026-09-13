// Package handlers 提供 HTTP 处理器
package handlers

import (
	"net/http"
	"strconv"

	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/gin-gonic/gin"
)

// GetGatePending 返回当前等待用户决定的危险命令待确认列表。
// 前端全局轮询此端点驱动通知卡片。
func GetGatePending(gate *hooks.CmdGate) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gate == nil {
			c.JSON(http.StatusOK, gin.H{"pending": []sensitive.GatePending{}})
			return
		}
		pending, err := gate.ListPending()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询待确认列表失败"})
			return
		}
		if pending == nil {
			pending = []sensitive.GatePending{}
		}
		c.JSON(http.StatusOK, gin.H{"pending": pending})
	}
}

// DecideGatePending 处理用户对一条待确认记录的放行/拦截决定。
// 幂等：记录已结束（不存在或已 resolved）返回 404 语义的 ok=false。
func DecideGatePending(gate *hooks.CmdGate) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gate == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 闸门未装配"})
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的待确认 ID"})
			return
		}
		var req struct {
			Decision string `json:"decision" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
			return
		}
		pending, ok, decideErr := gate.Decide(id, req.Decision)
		if decideErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": decideErr.Error()})
			return
		}
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "待确认记录不存在或已结束"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"pending": pending})
	}
}

// ListGateDecisions 返回全部会话级白/黑名单决定。
func ListGateDecisions(gate *hooks.CmdGate) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gate == nil {
			c.JSON(http.StatusOK, gin.H{"decisions": []sensitive.GateDecision{}})
			return
		}
		decisions, err := gate.ListDecisions()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询会话名单失败"})
			return
		}
		if decisions == nil {
			decisions = []sensitive.GateDecision{}
		}
		c.JSON(http.StatusOK, gin.H{"decisions": decisions})
	}
}

// RemoveGateDecisions 解除指定会话的全部名单决定。
func RemoveGateDecisions(gate *hooks.CmdGate) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gate == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 闸门未装配"})
			return
		}
		conversationID := c.Param("conversationId")
		if conversationID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "会话 ID 不能为空"})
			return
		}
		var commandHashes []string
		if hash := c.Query("commandHash"); hash != "" {
			commandHashes = append(commandHashes, hash)
		}
		removed, err := gate.RemoveDecisions(conversationID, commandHashes...)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"removed": removed})
	}
}

// AddGateDecision 手动写入会话级白/黑名单决定（拦截记录页的「移入名单」操作）。
func AddGateDecision(gate *hooks.CmdGate) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gate == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 闸门未装配"})
			return
		}
		var req struct {
			ConversationID string `json:"conversationId" binding:"required"`
			Decision       string `json:"decision" binding:"required"`
			Rule           string `json:"rule"`
			Command        string `json:"command"`
			CommandHash    string `json:"commandHash" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
			return
		}
		if req.Decision != sensitive.GateDecisionAllow && req.Decision != sensitive.GateDecisionBlock {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的名单决定 " + req.Decision})
			return
		}
		decision, err := gate.AddDecision(req.ConversationID, req.Decision, req.Rule, req.Command, req.CommandHash)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"decision": decision})
	}
}
