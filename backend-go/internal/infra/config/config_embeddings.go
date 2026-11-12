package config

import (
	"strings"
)

// ValidateEmbeddingsUpstream 校验向量嵌入渠道。该协议按 OpenAI Embeddings API
// 原样透传到 {baseUrl}/v1/embeddings，上游协议固定为 openai。
func ValidateEmbeddingsUpstream(upstream *UpstreamConfig) error {
	if upstream == nil {
		return &ConfigError{Message: "Embeddings 渠道配置不能为空"}
	}
	if upstream.EmbeddingsDimensions < 0 {
		return &ConfigError{Message: "Embeddings 输出维度不能为负数，零表示不覆盖请求维度"}
	}
	upstream.ServiceType = strings.TrimSpace(upstream.ServiceType)
	switch upstream.ServiceType {
	case "", "openai":
		upstream.ServiceType = "openai"
		return nil
	default:
		return &ConfigError{Message: "Embeddings 渠道的上游协议仅支持 OpenAI Embeddings API（openai）"}
	}
}
