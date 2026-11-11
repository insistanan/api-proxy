package proxycore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/tidwall/gjson"
)

// IsQuickTestRequest 检查请求是否来自快捷测试（通过请求头或 metadata.purpose 判断）。
func IsQuickTestRequest(c *gin.Context, bodyBytes []byte) bool {
	if c != nil {
		if purpose := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Proxy-Purpose"))); purpose == "quick_test" {
			return true
		}
		if purpose := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Purpose"))); purpose == "quick_test" {
			return true
		}
	}
	if len(bodyBytes) == 0 {
		return false
	}

	metadata, err := extractRequestMetadata(bodyBytes)
	if err != nil {
		return false
	}

	var purpose string
	if err := json.Unmarshal(metadata.Purpose, &purpose); err != nil {
		return false
	}
	return strings.ToLower(strings.TrimSpace(purpose)) == "quick_test"
}

// requestMetadata 只保存路由所需的字段；RawMessage 保留原始数字精度和字段类型。
type requestMetadata struct {
	Purpose      json.RawMessage
	ChannelIndex json.RawMessage
}

func extractRequestMetadata(bodyBytes []byte) (requestMetadata, error) {
	if !json.Valid(bodyBytes) {
		return requestMetadata{}, nil // 非法 JSON 由协议解析阶段报错。
	}
	if trimmed := bytes.TrimSpace(bodyBytes); len(trimmed) == 0 || trimmed[0] != '{' {
		return requestMetadata{}, nil
	}
	// 只收集根字段名，不复制文本和 base64；字段名保持大小写敏感。
	metadataCount := 0
	gjson.GetBytes(bodyBytes, "@keys").ForEach(func(_, key gjson.Result) bool {
		if key.String() == "metadata" {
			metadataCount++
		}
		return true
	})
	if metadataCount == 0 {
		return requestMetadata{}, nil
	}
	var rawMetadata []byte
	if metadataCount == 1 {
		rawMetadata = []byte(gjson.GetBytes(bodyBytes, "metadata").Raw)
	} else {
		// gjson 路径查询取第一个匹配；仅重复根字段时使用 RawMessage 保留最后值语义。
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			return requestMetadata{}, fmt.Errorf("解析请求元数据失败: %w", err)
		}
		rawMetadata = payload["metadata"]
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(rawMetadata, &metadata); err != nil {
		return requestMetadata{}, fmt.Errorf("metadata 必须是对象")
	}
	return requestMetadata{Purpose: metadata["purpose"], ChannelIndex: metadata["channel_index"]}, nil
}

// AreAllKeysSuspended 检查渠道的所有 Key 是否都处于熔断状态
// 用于判断是否需要启用强制探测模式
func AreAllKeysSuspended(metricsManager *metrics.MetricsManager, baseURL string, apiKeys []string, channelIndex int) bool {
	if len(apiKeys) == 0 {
		return false
	}

	for _, apiKey := range apiKeys {
		if !metricsManager.ShouldSuspendKey(baseURL, apiKey, channelIndex) {
			return false
		}
	}
	return true
}

// ExtractRequestedChannelIndex 从请求体 metadata.channel_index 中提取显式指定的渠道索引。
// 仅当字段显式存在时才返回 ok=true；若字段类型非法则返回错误，避免静默回退到默认调度。
func ExtractRequestedChannelIndex(bodyBytes []byte) (int, bool, error) {
	if len(bodyBytes) == 0 {
		return 0, false, nil
	}

	metadata, err := extractRequestMetadata(bodyBytes)
	if err != nil {
		return 0, false, err
	}
	rawChannelIndex := bytes.TrimSpace(metadata.ChannelIndex)
	if len(rawChannelIndex) == 0 || bytes.Equal(rawChannelIndex, []byte("null")) {
		return 0, false, nil
	}

	channelIndex, err := strconv.ParseInt(string(rawChannelIndex), 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("metadata.channel_index 必须是整数")
	}
	if channelIndex < 0 {
		return 0, true, fmt.Errorf("metadata.channel_index 不能小于 0")
	}

	return int(channelIndex), true, nil
}

// ResolveRequestedUpstream 解析显式指定的渠道。
// 这里允许演练/调试显式命中非默认渠道，但会拒绝 deleted 渠道和未配置 BaseURL 的渠道。
func ResolveRequestedUpstream(
	cfgManager *config.ConfigManager,
	kind scheduler.ChannelKind,
	channelIndex int,
) (*config.UpstreamConfig, int, error) {
	cfg := cfgManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case scheduler.ChannelKindMessages:
		upstreams = cfg.Upstream
	case scheduler.ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case scheduler.ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case scheduler.ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case scheduler.ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	case scheduler.ChannelKindEmbeddings:
		upstreams = cfg.EmbeddingsUpstream
	case scheduler.ChannelKindDecisions:
		upstreams = cfg.DecisionsUpstream
	default:
		return nil, -1, fmt.Errorf("不支持的渠道类型: %s", kind)
	}

	if len(upstreams) == 0 {
		return nil, -1, fmt.Errorf("当前未配置任何 %s 渠道", kind)
	}
	if channelIndex < 0 || channelIndex >= len(upstreams) {
		return nil, -1, fmt.Errorf("metadata.channel_index [%d] 超出 %s 渠道范围", channelIndex, kind)
	}

	upstream := upstreams[channelIndex].Clone()
	if upstream == nil {
		return nil, -1, fmt.Errorf("无法解析 %s 渠道 [%d]", kind, channelIndex)
	}
	if config.GetChannelStatus(upstream) == config.ChannelStatusDeleted {
		return nil, -1, fmt.Errorf("%s 渠道 [%d] 已删除", kind, channelIndex)
	}
	if kind != scheduler.ChannelKindImages && kind != scheduler.ChannelKindEmbeddings && kind != scheduler.ChannelKindDecisions && upstream.ExcludeFromConversation {
		return nil, -1, fmt.Errorf("%s 渠道 [%d] 已设置为不参与对话", kind, channelIndex)
	}
	if strings.TrimSpace(upstream.GetEffectiveBaseURL()) == "" {
		return nil, -1, fmt.Errorf("%s 渠道 [%d] 未配置 BaseURL", kind, channelIndex)
	}

	return upstream, channelIndex, nil
}
