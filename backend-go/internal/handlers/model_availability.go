package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/availability"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/logger"
)

// GetModelAvailability 使用配置快照过滤已移除的 Key/URL，旧日志不会按渠道序号重新归属。
func GetModelAvailability(store *logger.Store, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil || cfgManager == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "可用性观测存储或配置未初始化"})
			return
		}
		cfg := cfgManager.GetConfig()
		kind := c.Query("type")
		var upstreams []config.UpstreamConfig
		switch kind {
		case "messages":
			upstreams = cfg.Upstream
		case "responses":
			upstreams = cfg.ResponsesUpstream
		case "gemini":
			upstreams = cfg.GeminiUpstream
		case "chat":
			upstreams = cfg.ChatUpstream
		case "images":
			upstreams = cfg.ImagesUpstream
		case "embeddings":
			upstreams = cfg.EmbeddingsUpstream
		case "decisions":
			upstreams = cfg.DecisionsUpstream
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道协议类型"})
			return
		}
		now := time.Now()
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		aggregates, err := store.QueryAvailability(ctx, kind, now)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		rows := buildAvailabilityRows(upstreams, aggregates, now)
		c.JSON(http.StatusOK, gin.H{"rows": rows, "generatedAt": now.UnixMilli(), "minSamples": availability.MinSamples})
	}
}

func buildAvailabilityRows(upstreams []config.UpstreamConfig, aggregates []availability.Aggregate, now time.Time) []availability.Row {
	models := make(map[string]map[string]bool)
	byChannel := make(map[string]map[string][]availability.Aggregate)
	addModel := func(channelID, model string) {
		model = strings.TrimSpace(model)
		if channelID == "" || model == "" {
			return
		}
		if models[channelID] == nil {
			models[channelID] = make(map[string]bool)
		}
		models[channelID][model] = true
	}
	for _, upstream := range upstreams {
		addModel(upstream.ID, upstream.DefaultModel)
		if upstream.ServiceType == "responses" {
			addModel(upstream.ID, upstream.ResponsesModel)
		}
		for source, targets := range upstream.ModelMapping {
			for _, target := range targets {
				channelID := upstream.ID
				if override := upstream.ModelMappingChannels[source][target]; override != "" {
					channelID = override
				}
				addModel(channelID, target)
			}
		}
	}
	for _, a := range aggregates {
		addModel(a.ChannelID, a.Model)
		if byChannel[a.ChannelID] == nil {
			byChannel[a.ChannelID] = make(map[string][]availability.Aggregate)
		}
		byChannel[a.ChannelID][a.Model] = append(byChannel[a.ChannelID][a.Model], a)
	}
	rows := make([]availability.Row, 0)
	for _, upstream := range upstreams {
		if upstream.Status == "deleted" || upstream.ID == "" {
			continue
		}
		candidates := availabilityCandidates(upstream)
		for model := range models[upstream.ID] {
			rows = append(rows, availability.Summarize(upstream.ID, upstream.Name, model, candidates, byChannel[upstream.ID][model], now))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ChannelID == rows[j].ChannelID {
			return rows[i].Model < rows[j].Model
		}
		if rows[i].ChannelName != rows[j].ChannelName {
			return rows[i].ChannelName < rows[j].ChannelName
		}
		return rows[i].ChannelID < rows[j].ChannelID
	})
	return rows
}

func availabilityCandidates(upstream config.UpstreamConfig) []availability.Candidate {
	result := make([]availability.Candidate, 0)
	seen := make(map[string]bool)
	for _, baseURL := range upstream.GetAllBaseURLs() {
		upstream.BaseURL = baseURL
		for keyIndex, key := range upstream.APIKeys {
			identity := upstream.RequestIdentity(key)
			id := identity.Fingerprint()
			if seen[id] {
				continue
			}
			seen[id] = true
			endpoint := "地址格式异常"
			if parsed, err := url.Parse(baseURL); err == nil {
				// 只展示主机，避免把 URL 用户信息、路径凭据或查询密钥带到界面。
				endpoint = parsed.Scheme + "://" + parsed.Host
			}
			label := fmt.Sprintf("Key %d · %s", keyIndex+1, id[:8])
			if upstream.BuiltinProvider != "" {
				label = "免 Key · " + id[:8]
			}
			result = append(result, availability.Candidate{ID: id, Label: label, Endpoint: endpoint, Protocol: identity.Protocol})
		}
	}
	return result
}
