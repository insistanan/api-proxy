package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// EnvConfig 集中承载环境变量或 .env 中的启动级配置。
// 请求与日志策略由 Current 从持久化设置读取；其余内部策略使用默认值。
type EnvConfig struct {
	Port           int
	Env            string
	ProxyAccessKey string
	EnableWebUI    bool
	LogLevel       string
	LogToConsole   bool

	// 以下字段保留内部默认值；请求与日志策略由 Current 从设置快照覆盖，不再逐次读取旧 env。
	EnableRequestLogs           bool
	EnableResponseLogs          bool
	LogCaptureMode              string
	LogBodyLimitKB              int
	QuietPollingLogs            bool
	RawLogOutput                bool
	SSEDebugLevel               string
	RewriteResponseModel        bool
	CorrectResponsesInputTokens bool

	RequestTimeout        int
	MaxRequestBodySize    int64
	EnableCORS            bool
	CORSOrigin            string
	ResponseHeaderTimeout int
	StreamIdleTimeout     int

	MetricsWindowSize       int
	MetricsFailureThreshold float64

	MetricsPersistenceEnabled bool
	MetricsRetentionDays      int
	LogDBPath                 string
	settingsProvider          func() GatewaySettings
}

// DefaultEnvConfig 返回独立默认配置，不读取进程环境；运行时策略和测试夹具均可复用。
func DefaultEnvConfig() *EnvConfig {
	// 网关策略默认值以 DefaultGatewaySettings 为唯一来源，避免与持久化设置各写一份。
	defaults := DefaultGatewaySettings()

	return &EnvConfig{
		// 6 项核心外部环境变量
		Port:           3000,
		Env:            "production",
		ProxyAccessKey: "your-proxy-access-key",
		EnableWebUI:    true,
		LogLevel:       "info",
		LogToConsole:   false,

		// 日志与调试：根据 LogLevel 或稳态默认值自动决定
		EnableRequestLogs:           defaults.EnableRequestLogs,
		EnableResponseLogs:          defaults.EnableResponseLogs,
		LogCaptureMode:              defaults.LogCaptureMode,
		LogBodyLimitKB:              defaults.LogBodyLimitKB,
		QuietPollingLogs:            true, // 非 debug 模式下静默轮询日志
		RawLogOutput:                false,
		SSEDebugLevel:               "off",
		RewriteResponseModel:        false,
		CorrectResponsesInputTokens: true, // 核心 input_tokens 校正始终开启

		// 网络与请求体限制常量
		RequestTimeout:        defaults.RequestTimeout,
		MaxRequestBodySize:    defaults.MaxRequestBodySizeMB * 1024 * 1024,
		EnableCORS:            defaults.EnableCORS,
		CORSOrigin:            defaults.CORSOrigin,
		ResponseHeaderTimeout: defaults.ResponseHeaderTimeout,
		StreamIdleTimeout:     defaults.StreamIdleTimeout,

		// 指标稳态常量
		MetricsWindowSize:       10,
		MetricsFailureThreshold: 0.5,

		// 持久化存储常量
		MetricsPersistenceEnabled: defaults.MetricsPersistenceEnabled,
		MetricsRetentionDays:      defaults.MetricsRetentionDays,
		LogDBPath:                 defaults.LogDBPath,
	}
}

// LoadEnvConfig 只在启动时读取环境变量。缺失采用默认值，显式无效值必须报错。
func LoadEnvConfig() (*EnvConfig, error) {
	current := DefaultEnvConfig()
	if value, exists := os.LookupEnv("PORT"); exists {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("PORT 必须是 1 至 65535 之间的整数")
		}
		current.Port = port
	}
	for key, target := range map[string]*bool{"ENABLE_WEB_UI": &current.EnableWebUI, "LOG_TO_CONSOLE": &current.LogToConsole} {
		if value, exists := os.LookupEnv(key); exists {
			if value != "true" && value != "false" {
				return nil, fmt.Errorf("%s 必须是 true 或 false", key)
			}
			*target = value == "true"
		}
	}
	if value, exists := os.LookupEnv("ENV"); exists {
		if value != "production" && value != "development" {
			return nil, fmt.Errorf("ENV 必须是 production 或 development")
		}
		current.Env = value
	}
	if value, exists := os.LookupEnv("LOG_LEVEL"); exists {
		switch value {
		case "error", "warn", "info", "debug":
			current.LogLevel = value
		default:
			return nil, fmt.Errorf("LOG_LEVEL 必须是 error、warn、info 或 debug")
		}
	}
	if value, exists := os.LookupEnv("PROXY_ACCESS_KEY"); exists {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("PROXY_ACCESS_KEY 不能为空")
		}
		current.ProxyAccessKey = value
	}
	if current.IsProduction() && current.ProxyAccessKey == "your-proxy-access-key" {
		return nil, fmt.Errorf("生产模式必须显式设置 PROXY_ACCESS_KEY，不能使用公开示例值")
	}
	current.QuietPollingLogs = current.LogLevel != "debug"
	return current, nil
}

// ListenAddress 将显式开发模式限制在本机；生产模式启动前已校验访问密钥。
func (c *EnvConfig) ListenAddress() string {
	if c.IsDevelopment() {
		return fmt.Sprintf("127.0.0.1:%d", c.Port)
	}
	return fmt.Sprintf(":%d", c.Port)
}

// IsDevelopment 是否为开发环境
func (c *EnvConfig) IsDevelopment() bool {
	return c.Env == "development"
}

// IsProduction 是否为生产环境
func (c *EnvConfig) IsProduction() bool {
	return c.Env == "production"
}

// LoadDotEnv 加载 .env。先读可执行文件同目录，再读进程工作目录；
// 已存在的环境变量不覆盖。两条路径相同则只加载一次。
// 返回实际加载成功的文件路径（可能为空）。
func LoadDotEnv() ([]string, error) {
	loaded := make([]string, 0, 2)
	paths, err := candidateDotEnvPaths()
	if err != nil {
		return nil, err
	}
	for _, envPath := range paths {
		if _, err := os.Stat(envPath); err != nil {
			if !os.IsNotExist(err) {
				return loaded, fmt.Errorf("无法访问 %s: %w", envPath, err)
			}
			continue
		}
		if err := godotenv.Load(envPath); err != nil {
			return loaded, fmt.Errorf("加载 %s 失败: %w", envPath, err)
		}
		loaded = append(loaded, envPath)
	}
	return loaded, nil
}

func candidateDotEnvPaths() ([]string, error) {
	paths := make([]string, 0, 2)
	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("解析可执行文件路径失败: %w", err)
	}
	workingPath, err := filepath.Abs(".env")
	if err != nil {
		return nil, fmt.Errorf("解析工作目录环境文件路径失败: %w", err)
	}
	executableEnv := filepath.Join(filepath.Dir(executablePath), ".env")
	paths = append(paths, executableEnv)
	if !SamePath(executableEnv, workingPath) {
		paths = append(paths, workingPath)
	}
	return paths, nil
}

// ShouldLog 是否应该记录日志
func (c *EnvConfig) ShouldLog(level string) bool {
	levels := map[string]int{
		"error": 0,
		"warn":  1,
		"info":  2,
		"debug": 3,
	}

	currentLevel, ok := levels[c.LogLevel]
	if !ok {
		currentLevel = 2 // 默认 info
	}

	requestLevel, ok := levels[level]
	if !ok {
		return false
	}

	return requestLevel <= currentLevel
}
