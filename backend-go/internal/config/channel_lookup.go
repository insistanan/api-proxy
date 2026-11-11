package config

import "strings"

// LocatedChannel 是按稳定 UUID 跨五类切片只读查到的渠道。
// Kind 使用调度层的渠道类型字符串：messages / responses / gemini / chat / images。
// Index 仍是该切片内的数组下标，仅供熔断查询等仍以 index 为键的旧接口使用。
type LocatedChannel struct {
	Kind     string
	Index    int
	Upstream UpstreamConfig
}

// FindChannelByID 按 UpstreamConfig.ID 跨五类切片只读查找。
// 返回的 Upstream 是深拷贝，调用方可以安全持有。ID 为空视为未找到。
func (cm *ConfigManager) FindChannelByID(id string) (LocatedChannel, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return LocatedChannel{}, false
	}

	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if located, ok := findChannelInSlice(cm.config.Upstream, "messages", id); ok {
		return located, true
	}
	if located, ok := findChannelInSlice(cm.config.ResponsesUpstream, "responses", id); ok {
		return located, true
	}
	if located, ok := findChannelInSlice(cm.config.GeminiUpstream, "gemini", id); ok {
		return located, true
	}
	if located, ok := findChannelInSlice(cm.config.ChatUpstream, "chat", id); ok {
		return located, true
	}
	if located, ok := findChannelInSlice(cm.config.ImagesUpstream, "images", id); ok {
		return located, true
	}
	if located, ok := findChannelInSlice(cm.config.EmbeddingsUpstream, "embeddings", id); ok {
		return located, true
	}
	if located, ok := findChannelInSlice(cm.config.DecisionsUpstream, "decisions", id); ok {
		return located, true
	}
	return LocatedChannel{}, false
}

func findChannelInSlice(upstreams []UpstreamConfig, kind, id string) (LocatedChannel, bool) {
	for index := range upstreams {
		if strings.TrimSpace(upstreams[index].ID) != id {
			continue
		}
		cloned := upstreams[index].Clone()
		if cloned == nil {
			return LocatedChannel{}, false
		}
		return LocatedChannel{
			Kind:     kind,
			Index:    index,
			Upstream: *cloned,
		}, true
	}
	return LocatedChannel{}, false
}
