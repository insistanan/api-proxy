package config

func (cm *ConfigManager) AddGeminiUpstream(upstream UpstreamConfig) error {
	_, err := cm.AddGeminiUpstreamWithResult(upstream)
	return err
}

func (cm *ConfigManager) AddGeminiUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.AddChannel("gemini", upstream)
}

func (cm *ConfigManager) UpdateGeminiUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.UpdateChannel("gemini", index, updates)
}

// GetNextGeminiAPIKey 获取下一个 API 密钥
func (cm *ConfigManager) GetNextGeminiAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Gemini")
}
