// Package handlers 提供 HTTP 处理器
package handlers

import (
	"net/http"
	"strconv"

	"github.com/BenedictKing/api-proxy/internal/assistant"
	"github.com/gin-gonic/gin"
)

// AssistantAPI 包装 AI 配置助手的管理端路由。
type AssistantAPI struct {
	service *assistant.Service
}

// NewAssistantAPI 创建助手 API。service 为 nil 时全部端点返回 503。
func NewAssistantAPI(service *assistant.Service) *AssistantAPI {
	return &AssistantAPI{service: service}
}

// Register 注册路由（apiGroup 已带统一鉴权）。
func (a *AssistantAPI) Register(apiGroup *gin.RouterGroup) {
	apiGroup.GET("/assistant/status", a.Status)
	apiGroup.POST("/assistant/sessions", a.CreateSession)
	apiGroup.GET("/assistant/sessions", a.ListSessions)
	apiGroup.DELETE("/assistant/sessions/:id", a.DeleteSession)
	apiGroup.GET("/assistant/sessions/:id/messages", a.GetMessages)
	apiGroup.POST("/assistant/sessions/:id/messages", a.SendMessage)
}

// Status 返回助手可用性与当前配置下实际会命中的渠道（分组 + 渠道）。
func (a *AssistantAPI) Status(c *gin.Context) {
	if a.service == nil {
		c.JSON(http.StatusOK, assistant.StatusReport{Available: false, Reason: "助手服务未装配"})
		return
	}
	c.JSON(http.StatusOK, a.service.Status(c.Request.Context()))
}

// CreateSession 创建会话。
func (a *AssistantAPI) CreateSession(c *gin.Context) {
	if a.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "助手服务未装配"})
		return
	}
	var req struct {
		Title string `json:"title"`
	}
	_ = c.ShouldBindJSON(&req) // 空 body 允许
	session, err := a.service.CreateSession(req.Title)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"session": session})
}

// ListSessions 列出会话。
func (a *AssistantAPI) ListSessions(c *gin.Context) {
	if a.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "助手服务未装配"})
		return
	}
	sessions, err := a.service.ListSessions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询助手会话失败"})
		return
	}
	if sessions == nil {
		sessions = []assistant.Session{}
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// DeleteSession 删除会话。
func (a *AssistantAPI) DeleteSession(c *gin.Context) {
	if a.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "助手服务未装配"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的会话 ID"})
		return
	}
	deleted, err := a.service.DeleteSession(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// GetMessages 返回会话全部消息。
func (a *AssistantAPI) GetMessages(c *gin.Context) {
	if a.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "助手服务未装配"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的会话 ID"})
		return
	}
	messages, err := a.service.GetSessionMessages(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if messages == nil {
		messages = []assistant.Message{}
	}
	c.JSON(http.StatusOK, gin.H{"messages": messages})
}

// SendMessage 发送用户消息并同步返回助手回复（非流式）。
func (a *AssistantAPI) SendMessage(c *gin.Context) {
	if a.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "助手服务未装配"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的会话 ID"})
		return
	}
	var req struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息内容不能为空"})
		return
	}
	outcome, err := a.service.Chat(c.Request.Context(), id, req.Content)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, outcome)
}
