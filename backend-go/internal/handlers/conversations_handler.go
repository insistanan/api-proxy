package handlers

import (
	"net/http"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/conversation"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/BenedictKing/api-proxy/internal/visionlayer"
	"github.com/gin-gonic/gin"
)

type routeOverrideRequest struct {
	Kind         string `json:"kind"`
	ChannelIndex int    `json:"channelIndex"`
}

type conversationNameRequest struct {
	Name string `json:"name"`
}

type routeOptionChannel struct {
	Kind         string              `json:"kind"`
	ChannelIndex int                 `json:"channelIndex"`
	ChannelName  string              `json:"channelName"`
	ServiceType  string              `json:"serviceType"`
	Status       string              `json:"status"`
	DefaultModel string              `json:"defaultModel,omitempty"`
	ModelMapping map[string][]string `json:"modelMapping,omitempty"`
}

type routeOptionGroup struct {
	Kind     string               `json:"kind"`
	Label    string               `json:"label"`
	Channels []routeOptionChannel `json:"channels"`
}

func ListConversations(registry *conversation.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			c.JSON(http.StatusOK, gin.H{"conversations": []*conversation.Record{}})
			return
		}

		query := strings.ToLower(strings.TrimSpace(c.Query("q")))
		apiKind := strings.ToLower(strings.TrimSpace(c.Query("kind")))

		items := registry.List()
		filtered := make([]*conversation.Record, 0, len(items))
		for _, item := range items {
			if apiKind != "" && strings.ToLower(item.APIKind) != apiKind {
				continue
			}
			if query != "" && !matchConversationQuery(item, query) {
				continue
			}
			filtered = append(filtered, item)
		}

		c.JSON(http.StatusOK, gin.H{"conversations": filtered})
	}
}

func GetConversation(registry *conversation.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
			return
		}

		item, ok := registry.Get(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func SetConversationName(registry *conversation.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "conversation registry is not initialized"})
			return
		}
		var req conversationNameRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		item, err := registry.SetName(c.Param("id"), req.Name)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func DeleteConversation(registry *conversation.Registry, sessionManager *session.SessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "conversation registry is not initialized"})
			return
		}
		conversationID := c.Param("id")
		if _, ok := registry.Get(conversationID); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
			return
		}
		if sessionManager == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "response session manager is not initialized"})
			return
		}
		// 先删除 Responses 会话链；若持久化删除失败，保留对话记录供用户重试。
		if err := sessionManager.DeleteConversation(conversationID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := registry.Delete(conversationID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		session.DefaultResponseChainManager().Clear(conversationID)
		visionlayer.ClearConversationCache(conversationID)
		c.Status(http.StatusNoContent)
	}
}

// DeleteAllConversations 删除全部对话、图片理解结果和 Responses 会话链。
func DeleteAllConversations(registry *conversation.Registry, sessionManager *session.SessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "conversation registry is not initialized"})
			return
		}
		if sessionManager == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "response session manager is not initialized"})
			return
		}
		if err := sessionManager.DeleteAll(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := registry.DeleteAll(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		session.DefaultResponseChainManager().ClearAll()
		visionlayer.ClearCache()
		c.Status(http.StatusNoContent)
	}
}

func SetConversationRouteOverride(registry *conversation.Registry, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "conversation registry is not initialized"})
			return
		}

		var req routeOverrideRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		item, ok := registry.Get(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
			return
		}
		req.Kind = strings.TrimSpace(strings.ToLower(req.Kind))
		if req.Kind == "" {
			req.Kind = strings.ToLower(item.APIKind)
		}
		if !isValidConversationKind(req.Kind) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel kind"})
			return
		}
		if req.Kind != strings.ToLower(item.APIKind) {
			c.JSON(http.StatusConflict, gin.H{"error": "route override kind must match conversation kind"})
			return
		}
		channelName, ok := lookupChannelName(cfgManager, scheduler.ChannelKind(req.Kind), req.ChannelIndex, item.LastModel)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
			return
		}

		item, err := registry.SetRouteOverride(c.Param("id"), req.Kind, req.ChannelIndex, channelName)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func ClearConversationRouteOverride(registry *conversation.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "conversation registry is not initialized"})
			return
		}

		item, err := registry.ClearRouteOverride(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func GetConversationRouteOptions(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"kinds": []routeOptionGroup{
				buildRouteOptionGroup(cfgManager, scheduler.ChannelKindMessages, "Claude"),
				buildRouteOptionGroup(cfgManager, scheduler.ChannelKindResponses, "Codex"),
				buildRouteOptionGroup(cfgManager, scheduler.ChannelKindGemini, "Gemini"),
				buildRouteOptionGroup(cfgManager, scheduler.ChannelKindChat, "Chat"),
				buildRouteOptionGroup(cfgManager, scheduler.ChannelKindImages, "Images"),
			},
		})
	}
}

func buildRouteOptionGroup(cfgManager *config.ConfigManager, kind scheduler.ChannelKind, label string) routeOptionGroup {
	cfg := cfgManager.GetConfig()
	upstreams := getConfigUpstreams(cfg, kind)
	channels := make([]routeOptionChannel, 0, len(upstreams))
	for index, upstream := range upstreams {
		if config.GetChannelStatus(&upstream) == config.ChannelStatusDeleted || upstream.ExcludeFromConversation {
			continue
		}
		channels = append(channels, routeOptionChannel{
			Kind:         string(kind),
			ChannelIndex: index,
			ChannelName:  upstream.Name,
			ServiceType:  upstream.ServiceType,
			Status:       upstream.Status,
			DefaultModel: upstream.DefaultModel,
			ModelMapping: upstream.ModelMapping,
		})
	}
	return routeOptionGroup{
		Kind:     string(kind),
		Label:    label,
		Channels: channels,
	}
}

func lookupChannelName(cfgManager *config.ConfigManager, kind scheduler.ChannelKind, channelIndex int, model string) (string, bool) {
	cfg := cfgManager.GetConfig()
	upstreams := getConfigUpstreams(cfg, kind)
	if channelIndex < 0 || channelIndex >= len(upstreams) {
		return "", false
	}
	upstream := &upstreams[channelIndex]
	if upstream.ExcludeFromConversation || config.GetChannelStatus(upstream) == config.ChannelStatusDeleted {
		return "", false
	}
	poolRoute, err := config.SelectChannelPoolRoute(getConfigPools(cfg, kind), model)
	if err != nil {
		return "", false
	}
	poolID := strings.TrimSpace(upstream.PoolID)
	if poolID == "" {
		poolID = config.DefaultChannelPoolID
	}
	for _, pool := range poolRoute {
		if poolID == pool.ID {
			return upstream.Name, true
		}
	}
	return "", false
}

func getConfigUpstreams(cfg config.Config, kind scheduler.ChannelKind) []config.UpstreamConfig {
	switch kind {
	case scheduler.ChannelKindMessages:
		return cfg.Upstream
	case scheduler.ChannelKindResponses:
		return cfg.ResponsesUpstream
	case scheduler.ChannelKindGemini:
		return cfg.GeminiUpstream
	case scheduler.ChannelKindChat:
		return cfg.ChatUpstream
	case scheduler.ChannelKindImages:
		return cfg.ImagesUpstream
	default:
		return nil
	}
}

func getConfigPools(cfg config.Config, kind scheduler.ChannelKind) []config.ChannelPool {
	switch kind {
	case scheduler.ChannelKindMessages:
		return cfg.MessagePools
	case scheduler.ChannelKindResponses:
		return cfg.ResponsesPools
	case scheduler.ChannelKindGemini:
		return cfg.GeminiPools
	case scheduler.ChannelKindChat:
		return cfg.ChatPools
	case scheduler.ChannelKindImages:
		return cfg.ImagesPools
	default:
		return nil
	}
}

func isValidConversationKind(kind string) bool {
	switch kind {
	case string(scheduler.ChannelKindMessages), string(scheduler.ChannelKindResponses), string(scheduler.ChannelKindGemini), string(scheduler.ChannelKindChat), string(scheduler.ChannelKindImages):
		return true
	default:
		return false
	}
}

func matchConversationQuery(item *conversation.Record, query string) bool {
	values := []string{
		item.ID,
		item.Name,
		item.APIKind,
		item.LastModel,
		item.FirstPrompt,
		item.RouteOverrideString(),
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
