package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"insistanan/api-proxy/internal/infra/upstreamreq"
)

// GatewaySettings 将有部署差异的策略从 env 迁移到持久化设置。
type GatewaySettings struct {
	EnableRequestLogs         bool   `json:"enableRequestLogs"`
	EnableResponseLogs        bool   `json:"enableResponseLogs"`
	LogCaptureMode            string `json:"logCaptureMode"`
	LogBodyLimitKB            int    `json:"logBodyLimitKB"`
	LogRuntimeRetentionDays   int    `json:"logRuntimeRetentionDays"`
	LogCallRetentionDays      int    `json:"logCallRetentionDays"`
	LogBodyRetentionDays      int    `json:"logBodyRetentionDays"`
	LogBudgetMB               int    `json:"logBudgetMB"`
	RequestTimeout            int    `json:"requestTimeout"`
	ResponseHeaderTimeout     int    `json:"responseHeaderTimeout"`
	StreamIdleTimeout         int    `json:"streamIdleTimeout"`
	MaxRequestBodySizeMB      int64  `json:"maxRequestBodySizeMB"`
	MetricsRetentionDays      int    `json:"metricsRetentionDays"`
	MetricsPersistenceEnabled bool   `json:"metricsPersistenceEnabled"`
	LogDBPath                 string `json:"logDBPath"`
	EnableCORS                bool   `json:"enableCORS"`
	CORSOrigin                string `json:"corsOrigin"`
}

func DefaultGatewaySettings() GatewaySettings {
	return GatewaySettings{
		EnableRequestLogs:         true,
		EnableResponseLogs:        true,
		LogCaptureMode:            "bounded",
		LogBodyLimitKB:            1024,
		LogRuntimeRetentionDays:   2,
		LogCallRetentionDays:      2,
		LogBodyRetentionDays:      2,
		LogBudgetMB:               0,
		RequestTimeout:            300000,
		ResponseHeaderTimeout:     120,
		StreamIdleTimeout:         300,
		MaxRequestBodySizeMB:      50,
		MetricsRetentionDays:      7,
		MetricsPersistenceEnabled: true,
		LogDBPath:                 ".config/logs.db",
		EnableCORS:                true,
		CORSOrigin:                "*",
	}
}

func ValidateGatewaySettings(settings GatewaySettings) error {
	if settings.LogCaptureMode != "metadata" && settings.LogCaptureMode != "bounded" {
		return fmt.Errorf("日志采集档位必须为 metadata 或 bounded")
	}
	if settings.LogBodyLimitKB < 16 || settings.LogBodyLimitKB > 1024 {
		return fmt.Errorf("日志正文上限必须在 16 至 1024 KiB 之间")
	}
	if settings.LogRuntimeRetentionDays < 1 || settings.LogRuntimeRetentionDays > 365 || settings.LogCallRetentionDays < 1 || settings.LogCallRetentionDays > 365 {
		return fmt.Errorf("运行日志和调用保留期必须在 1 至 365 天之间")
	}
	if settings.LogBodyRetentionDays < 1 || settings.LogBodyRetentionDays > settings.LogCallRetentionDays {
		return fmt.Errorf("正文保留期必须在 1 天至调用保留期之间")
	}
	if settings.LogBudgetMB != 0 && (settings.LogBudgetMB < 64 || settings.LogBudgetMB > 1048576) {
		return fmt.Errorf("日志预算必须为 0（不限）或 64 至 1048576 MiB")
	}
	if settings.RequestTimeout <= 0 || settings.RequestTimeout > 86400000 {
		return fmt.Errorf("请求超时必须在 1 至 86400000 毫秒之间")
	}
	if settings.ResponseHeaderTimeout < 30 || settings.ResponseHeaderTimeout > 300 {
		return fmt.Errorf("首字节超时必须在 30 至 300 秒之间")
	}
	if settings.StreamIdleTimeout < 30 || settings.StreamIdleTimeout > 3600 {
		return fmt.Errorf("流式空闲超时必须在 30 至 3600 秒之间")
	}
	if settings.MaxRequestBodySizeMB <= 0 || settings.MaxRequestBodySizeMB > 1024 {
		return fmt.Errorf("请求体大小必须在 1 至 1024 MB 之间")
	}
	if settings.MetricsRetentionDays < 3 || settings.MetricsRetentionDays > 30 {
		return fmt.Errorf("指标保留期必须在 3 至 30 天之间")
	}
	if strings.TrimSpace(settings.LogDBPath) == "" || strings.ContainsAny(settings.LogDBPath, "\r\n\x00") {
		return fmt.Errorf("日志数据库路径不能为空或包含控制字符")
	}
	if strings.TrimSpace(settings.CORSOrigin) == "" || strings.ContainsAny(settings.CORSOrigin, "\r\n\x00") {
		return fmt.Errorf("跨域来源不能为空或包含控制字符")
	}
	return nil
}

func ValidateDisguiseSettings(settings DisguiseSettings) error {
	if err := upstreamreq.ValidateDisguiseVersion(settings.ClaudeCodeVersion); err != nil {
		return fmt.Errorf("Claude Code %w", err)
	}
	if err := upstreamreq.ValidateDisguiseVersion(settings.CodexVersion); err != nil {
		return fmt.Errorf("Codex %w", err)
	}
	return nil
}

// migrateManagedSettings 只为缺失字段补默认值或旧 env；显式 false、空版本和值始终保留。
// 错误交给加载路径返回，不把无效旧 env 静默替换成默认值。
func migrateManagedSettings(candidate *Config, rawJSON []byte) (bool, error) {
	var root struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(rawJSON, &root); err != nil {
		return false, err
	}
	defaults, err := json.Marshal(DefaultGatewaySettings())
	if err != nil {
		return false, err
	}
	var gateway, fallback map[string]json.RawMessage
	if err := json.Unmarshal(defaults, &fallback); err != nil {
		return false, err
	}
	gateway = make(map[string]json.RawMessage)
	if raw, exists := root.Settings["gateway"]; exists {
		if err := json.Unmarshal(raw, &gateway); err != nil || gateway == nil {
			return false, fmt.Errorf("gateway 必须为配置对象")
		}
	}
	legacy := map[string]string{
		"enableRequestLogs": "ENABLE_REQUEST_LOGS", "enableResponseLogs": "ENABLE_RESPONSE_LOGS",
		"requestTimeout": "REQUEST_TIMEOUT", "responseHeaderTimeout": "RESPONSE_HEADER_TIMEOUT",
		"streamIdleTimeout": "STREAM_IDLE_TIMEOUT", "maxRequestBodySizeMB": "MAX_REQUEST_BODY_SIZE_MB",
		"metricsRetentionDays": "METRICS_RETENTION_DAYS", "metricsPersistenceEnabled": "METRICS_PERSISTENCE_ENABLED",
		"logDBPath": "LOG_DB_PATH", "enableCORS": "ENABLE_CORS", "corsOrigin": "CORS_ORIGIN",
	}
	changed := false
	for field, value := range fallback {
		if _, exists := gateway[field]; exists {
			continue
		}
		if raw := os.Getenv(legacy[field]); raw != "" {
			var parsed any
			switch field {
			case "enableRequestLogs", "enableResponseLogs", "metricsPersistenceEnabled", "enableCORS":
				if raw != "true" && raw != "false" {
					return false, fmt.Errorf("旧配置 %s 必须为 true 或 false", legacy[field])
				}
				parsed = raw == "true"
			case "logDBPath", "corsOrigin":
				parsed = raw
			default:
				number, err := strconv.ParseInt(raw, 10, 64)
				if err != nil {
					return false, fmt.Errorf("旧配置 %s 必须为整数: %w", legacy[field], err)
				}
				parsed = number
			}
			encoded, err := json.Marshal(parsed)
			if err != nil {
				return false, err
			}
			value = encoded
		}
		gateway[field] = value
		changed = true
	}
	encoded, err := json.Marshal(gateway)
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(encoded, &candidate.Settings.Gateway); err != nil {
		return false, fmt.Errorf("网关配置格式无效: %w", err)
	}
	if err := ValidateGatewaySettings(candidate.Settings.Gateway); err != nil {
		return false, err
	}

	disguise := make(map[string]json.RawMessage)
	if raw, exists := root.Settings["disguise"]; exists {
		if err := json.Unmarshal(raw, &disguise); err != nil || disguise == nil {
			return false, fmt.Errorf("disguise 必须为配置对象")
		}
	}
	if _, exists := disguise["claudeCodeVersion"]; !exists {
		candidate.Settings.Disguise.ClaudeCodeVersion = strings.TrimSpace(os.Getenv("CLAUDE_CODE_DISGUISE_VERSION"))
		changed = true
	}
	if _, exists := disguise["codexVersion"]; !exists {
		candidate.Settings.Disguise.CodexVersion = strings.TrimSpace(os.Getenv("CODEX_DISGUISE_VERSION"))
		changed = true
	}
	if _, exists := disguise["autoUpdateFromRegistry"]; !exists {
		candidate.Settings.Disguise.AutoUpdateFromRegistry = true
		changed = true
	}
	if err := ValidateDisguiseSettings(candidate.Settings.Disguise); err != nil {
		return false, err
	}
	return changed, nil
}

// Current 读取完整策略快照，保留 EnvConfig 作为旧消费方的适配器；不原地修改共享字段。
func (c *EnvConfig) Current() *EnvConfig {
	if c.settingsProvider == nil {
		return c
	}
	current := *c
	current.applyGatewaySettings(c.settingsProvider())
	return &current
}

// Snapshot 固定当前完整策略，不再跟随 provider；用于单次请求的局部副本。
func (c *EnvConfig) Snapshot() *EnvConfig {
	current := *c.Current()
	current.settingsProvider = nil
	return &current
}

func (c *EnvConfig) applyGatewaySettings(settings GatewaySettings) {
	c.EnableRequestLogs = settings.EnableRequestLogs
	c.EnableResponseLogs = settings.EnableResponseLogs
	c.LogCaptureMode = settings.LogCaptureMode
	c.LogBodyLimitKB = settings.LogBodyLimitKB
	c.RequestTimeout = settings.RequestTimeout
	c.ResponseHeaderTimeout = settings.ResponseHeaderTimeout
	c.StreamIdleTimeout = settings.StreamIdleTimeout
	c.MaxRequestBodySize = settings.MaxRequestBodySizeMB * 1024 * 1024
	c.MetricsRetentionDays = settings.MetricsRetentionDays
	c.MetricsPersistenceEnabled = settings.MetricsPersistenceEnabled
	c.LogDBPath = settings.LogDBPath
	c.EnableCORS = settings.EnableCORS
	c.CORSOrigin = settings.CORSOrigin
}

func (cm *ConfigManager) GetGatewaySettings() GatewaySettings {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.Settings.Gateway
}

// WithSettings 仅在应用装配时绑定；手工构造的 EnvConfig 保持显式字段语义。
func (c *EnvConfig) WithSettings(cm *ConfigManager) *EnvConfig {
	current := *c
	current.settingsProvider = cm.GetGatewaySettings
	return current.Current()
}

// ReadGatewaySettings 供只读 CLI 读取存储路径，不启动 watcher 或回写迁移。
func ReadGatewaySettings(configFile string) (GatewaySettings, error) {
	data, err := os.ReadFile(configFile)
	if os.IsNotExist(err) {
		data = []byte(`{}`)
	} else if err != nil {
		return GatewaySettings{}, err
	}
	var candidate Config
	if err := json.Unmarshal(data, &candidate); err != nil {
		return GatewaySettings{}, err
	}
	if _, err := migrateManagedSettings(&candidate, data); err != nil {
		return GatewaySettings{}, err
	}
	return candidate.Settings.Gateway, nil
}
