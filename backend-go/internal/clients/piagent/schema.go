package piagent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// pi-agent 配置文件的可编辑字段集合（保留其他未知字段）。
const (
	KeyProvider             = "providers"
	KeyDefaultModel         = "defaultModel"
	KeyDefaultThinkingLevel = "defaultThinkingLevel"
	KeyEnabledModels        = "enabledModels"
)

// ModelCost 是 pi-agent models.json 中模型的百万 token 费用（美元）。
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// ModelConfig 是 pi-agent models.json 中 provider 下单个模型的编辑视图。
type ModelConfig struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name,omitempty"`
	API              string                     `json:"api,omitempty"`
	BaseURL          string                     `json:"baseUrl,omitempty"`
	Reasoning        *bool                      `json:"reasoning,omitempty"`
	ThinkingLevelMap map[string]*string         `json:"thinkingLevelMap,omitempty"`
	Input            []string                   `json:"input,omitempty"`
	ContextWindow    *int64                     `json:"contextWindow,omitempty"`
	MaxTokens        *int64                     `json:"maxTokens,omitempty"`
	Cost             *ModelCost                 `json:"cost,omitempty"`
	Headers          map[string]string          `json:"headers,omitempty"`
	Compat           map[string]any             `json:"compat,omitempty"`
	Extra            map[string]json.RawMessage `json:"-"`
}

// ProviderConfig 是 pi-agent models.json 中单个 provider 的编辑视图。
type ProviderConfig struct {
	Name           string                     `json:"name,omitempty"`
	BaseURL        string                     `json:"baseUrl,omitempty"`
	API            string                     `json:"api,omitempty"`
	APIKey         string                     `json:"apiKey,omitempty"`
	AuthHeader     *bool                      `json:"authHeader,omitempty"`
	Headers        map[string]string          `json:"headers,omitempty"`
	Compat         map[string]any             `json:"compat,omitempty"`
	Models         []ModelConfig              `json:"models,omitempty"`
	ModelOverrides map[string]json.RawMessage `json:"modelOverrides,omitempty"`
	Extra          map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON 读取已知字段，同时保留 pi-agent 未来版本新增的字段。
func (m *ModelConfig) UnmarshalJSON(data []byte) error {
	type plain ModelConfig
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"id", "name", "api", "baseUrl", "reasoning", "thinkingLevelMap", "input", "contextWindow", "maxTokens", "cost", "headers", "compat"} {
		delete(fields, key)
	}
	*m = ModelConfig(decoded)
	m.Extra = fields
	return nil
}

// MarshalJSON 将已知字段与未识别字段合并，避免保存配置时破坏新字段。
func (m ModelConfig) MarshalJSON() ([]byte, error) {
	type plain ModelConfig
	data, err := json.Marshal(plain(m))
	if err != nil {
		return nil, err
	}
	return mergeJSONObjects(data, m.Extra)
}

// UnmarshalJSON 读取已知字段，同时保留 pi-agent 未来版本新增的字段。
func (p *ProviderConfig) UnmarshalJSON(data []byte) error {
	type plain ProviderConfig
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"name", "baseUrl", "api", "apiKey", "authHeader", "headers", "compat", "models", "modelOverrides"} {
		delete(fields, key)
	}
	*p = ProviderConfig(decoded)
	p.Extra = fields
	return nil
}

// MarshalJSON 将已知字段与未识别字段合并，避免保存配置时破坏新字段。
func (p ProviderConfig) MarshalJSON() ([]byte, error) {
	type plain ProviderConfig
	data, err := json.Marshal(plain(p))
	if err != nil {
		return nil, err
	}
	return mergeJSONObjects(data, p.Extra)
}

func mergeJSONObjects(known []byte, extra map[string]json.RawMessage) ([]byte, error) {
	if len(extra) == 0 {
		return known, nil
	}
	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal(known, &fields); err != nil {
		return nil, err
	}
	for key, value := range extra {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}

// decodeProvider 从原始 JSON 解码 provider，并保留未知字段。
func decodeProvider(raw json.RawMessage) (ProviderConfig, error) {
	var provider ProviderConfig
	if err := json.Unmarshal(raw, &provider); err != nil {
		return ProviderConfig{}, err
	}
	return provider, nil
}

// encodeProvider 将 provider 编码为原始 JSON。只写非零字段，避免空数组误落盘。
func encodeProvider(provider ProviderConfig) (json.RawMessage, error) {
	data, err := json.Marshal(provider)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// IsValidAPIProtocol 校验 pi-agent 支持的 API 协议名。
func IsValidAPIProtocol(api string) bool {
	switch api {
	case "anthropic-messages", "openai-completions", "openai-responses", "google-generative-ai":
		return true
	default:
		return false
	}
}

// IsValidThinkingLevel 校验 pi-agent 支持的思考等级。
// 完整档位：off / minimal / low / medium / high / xhigh / max。
func IsValidThinkingLevel(level string) bool {
	switch level {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

// NormalizeID 规范 provider ID：去空格、去首尾斜杠。
func NormalizeID(id string) string {
	return strings.Trim(strings.TrimSpace(id), "/")
}

// MergeProviderUpdate 将编辑结果合并到现有 provider，保留未知字段和未重新填写的密钥。
func MergeProviderUpdate(existing, updated ProviderConfig) ProviderConfig {
	updated.Extra = mergeRawFields(existing.Extra, updated.Extra)
	if strings.TrimSpace(updated.APIKey) == "" {
		updated.APIKey = existing.APIKey
	}
	oldModels := make(map[string]ModelConfig, len(existing.Models))
	for _, model := range existing.Models {
		oldModels[model.ID] = model
	}
	for i := range updated.Models {
		if old, ok := oldModels[updated.Models[i].ID]; ok {
			updated.Models[i].Extra = mergeRawFields(old.Extra, updated.Models[i].Extra)
		}
	}
	return updated
}

func mergeRawFields(existing, updated map[string]json.RawMessage) map[string]json.RawMessage {
	if len(existing) == 0 && len(updated) == 0 {
		return nil
	}
	merged := make(map[string]json.RawMessage, len(existing)+len(updated))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range updated {
		merged[key] = value
	}
	return merged
}

// ValidateProvider 校验 provider 配置完整性。
func ValidateProvider(id string, provider ProviderConfig) error {
	if NormalizeID(id) == "" {
		return fmt.Errorf("provider ID 不能为空")
	}
	if strings.ContainsAny(id, `/\\`) {
		return fmt.Errorf("provider ID 不能包含路径分隔符")
	}
	if provider.API != "" && !IsValidAPIProtocol(provider.API) {
		return fmt.Errorf("provider %s 的 API 协议无效: %s", id, provider.API)
	}
	seenModels := make(map[string]struct{}, len(provider.Models))
	for _, model := range provider.Models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			return fmt.Errorf("provider %s 存在空模型 ID", id)
		}
		if _, exists := seenModels[modelID]; exists {
			return fmt.Errorf("provider %s 的模型 ID 重复: %s", id, modelID)
		}
		if model.API != "" && !IsValidAPIProtocol(model.API) {
			return fmt.Errorf("provider %s 的模型 %s API 协议无效: %s", id, modelID, model.API)
		}
		if (model.ContextWindow != nil && *model.ContextWindow < 0) || (model.MaxTokens != nil && *model.MaxTokens < 0) {
			return fmt.Errorf("provider %s 的模型 %s token 限制不能为负数", id, modelID)
		}
		if model.Cost != nil && (model.Cost.Input < 0 || model.Cost.Output < 0 || model.Cost.CacheRead < 0 || model.Cost.CacheWrite < 0) {
			return fmt.Errorf("provider %s 的模型 %s 费用不能为负数", id, modelID)
		}
		seenModels[modelID] = struct{}{}
	}
	return nil
}
