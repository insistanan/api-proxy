package config

func (cm *ConfigManager) GetCurrentResponsesUpstreamWithIndexForModel(model string) (*UpstreamConfig, int, error) {
	return cm.CurrentChannelForModel("responses", model)
}

// GetNextResponsesAPIKey 获取下一个 API 密钥
func (cm *ConfigManager) GetNextResponsesAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Responses")
}
