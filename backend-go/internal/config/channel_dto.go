package config

// ChannelToDTO 是渠道对象序列化到管理 API 的**唯一**真相源。
//
// 收敛前：channelcrud.GetUpstreams、handlers.GetChannelDashboard、
// gemini/chat/images 三份 dashboard 各手写一份字段表，字段集已经漂移
// （proxyMode/defaultModel/promotionCount/thoughtSignature 各缺各的），
// 导致同一个渠道从不同端点取出来长得不一样。
//
// 新增渠道字段时**只改这里**。
func ChannelToDTO(up *UpstreamConfig, index int) map[string]interface{} {
	return map[string]interface{}{
		"id":                          up.ID,
		"poolId":                      up.PoolID,
		"index":                       index,
		"name":                        up.Name,
		"serviceType":                 up.ServiceType,
		"baseUrl":                     up.BaseURL,
		"baseUrls":                    up.BaseURLs,
		"apiKeys":                     up.APIKeys,
		"description":                 up.Description,
		"website":                     up.Website,
		"insecureSkipVerify":          up.InsecureSkipVerify,
		"proxyMode":                   up.ProxyMode,
		"proxyUrl":                    up.ProxyURL,
		"modelMapping":                up.ModelMapping,
		"modelMappingChannels":        up.ModelMappingChannels,
		"defaultModel":                up.DefaultModel,
		"responsesModel":              up.ResponsesModel,
		"imagesExtraParams":           up.ImagesExtraParams,
		"imagesForceNonStream":        up.ImagesForceNonStream,
		"latency":                     nil,
		"status":                      GetChannelStatus(up),
		"priority":                    GetChannelPriority(up, index),
		"promotionUntil":              up.PromotionUntil,
		"promotionCount":              up.PromotionCount,
		"lowQuality":                  up.LowQuality,
		"visionCapable":               up.VisionCapable,
		"excludeFromConversation":     up.ExcludeFromConversation,
		"disablePromptCacheInjection": up.DisablePromptCacheInjection,
		"disablePromptCacheKey":       up.DisablePromptCacheKey,
		"visionLayerEnabled":          up.VisionLayerEnabled,
		"visionLayerChannelId":        up.VisionLayerChannelID,
		"visionLayerModel":            up.VisionLayerModel,
		"injectDummyThoughtSignature": up.InjectDummyThoughtSignature,
		"stripThoughtSignature":       up.StripThoughtSignature,
	}
}

// ChannelListToDTO 批量序列化，自动跳过 status == deleted 的渠道。
// 返回的 index 是**原切片下标**（前端和调度器都按它定位渠道，不能压缩）。
func ChannelListToDTO(list []UpstreamConfig) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(list))
	for i := range list {
		if GetChannelStatus(&list[i]) == ChannelStatusDeleted {
			continue
		}
		out = append(out, ChannelToDTO(&list[i], i))
	}
	return out
}
