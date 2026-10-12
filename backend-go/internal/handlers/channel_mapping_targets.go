package handlers

import (
	"net/http"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/circuit"
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/modelcatalog"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// MappingTargetChannelDTO 模型映射"目标渠道"下拉的候选项。
type MappingTargetChannelDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Index        int    `json:"index"`
	PoolID       string `json:"poolId"`
	ServiceType  string `json:"serviceType"`
	Status       string `json:"status"`
	CircuitState string `json:"circuitState"`
}

// ListMappingTargetChannels 返回同协议下可作为模型映射目标渠道的候选：
// 启用状态、配置了 API Key、熔断未处于 Open（HalfOpen 保留，选中时由许可正式占位）。
// 任意分组均可选；excludeId 用于排除源渠道自身。
//
// GET /api/{kind}/mapping-targets?excludeId=<渠道ID>
func ListMappingTargetChannels(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		excludeID := strings.TrimSpace(c.Query("excludeId"))
		cfg := cfgManager.GetConfig()
		upstreams := scheduler.UpstreamsOfKind(cfg, kind)
		circuits := sch.GetCircuitSnapshotForKind(kind)

		items := make([]MappingTargetChannelDTO, 0, len(upstreams))
		for index := range upstreams {
			up := &upstreams[index]
			if up.ID == "" || up.ID == excludeID {
				continue
			}
			if config.GetChannelStatus(up) != config.ChannelStatusActive || len(up.APIKeys) == 0 {
				continue
			}
			circuitState := circuit.StateClosed
			if snap, ok := circuits[index]; ok {
				circuitState = snap.State
			}
			if circuitState == circuit.StateOpen {
				continue
			}
			items = append(items, MappingTargetChannelDTO{
				ID:           up.ID,
				Name:         up.Name,
				Index:        index,
				PoolID:       up.PoolID,
				ServiceType:  up.ServiceType,
				Status:       config.GetChannelStatus(up),
				CircuitState: string(circuitState),
			})
		}
		c.JSON(http.StatusOK, gin.H{"channels": items})
	}
}

// ListMappingTargetChannelModels 使用目标渠道自身的配置（服务端持有密钥，不回传前端）获取其模型列表。
// 取该渠道第一个 API Key 作为探测凭据。
//
// GET /api/{kind}/mapping-targets/:channelId/models
func ListMappingTargetChannelModels(cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		channelID := strings.TrimSpace(c.Param("channelId"))
		if channelID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少目标渠道 ID"})
			return
		}
		cfg := cfgManager.GetConfig()
		upstreams := scheduler.UpstreamsOfKind(cfg, kind)

		var target *config.UpstreamConfig
		for index := range upstreams {
			if upstreams[index].ID == channelID {
				target = upstreams[index].Clone()
				break
			}
		}
		if target == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "目标渠道不存在"})
			return
		}
		if len(target.APIKeys) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "目标渠道没有可用 API 密钥"})
			return
		}

		response, err := modelcatalog.DiscoverUpstreamModels(c.Request.Context(), cfgManager, target, target.APIKeys[0])
		if err != nil {
			status := http.StatusBadGateway
			if config.IsConfigError(err) {
				status = http.StatusBadRequest
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, response)
	}
}
