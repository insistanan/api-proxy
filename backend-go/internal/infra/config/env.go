package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"

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

	MetricsWindowSize            int
	MetricsFailureThreshold      float64
	CircuitEnabled               bool
	CircuitFailureThreshold      int
	CircuitErrorRateThreshold    float64
	CircuitMinRequests           int
	CircuitCooldownSeconds       int
	CircuitProbeSuccessThreshold int

	MetricsPersistenceEnabled bool
	MetricsRetentionDays      int
	LogDBPath                 string
	settingsProvider          func() GatewaySettings
}

// NewEnvConfig 创建环境配置。
// .env 仅解析 6 项核心配置，其余字段初始化为稳态最佳默认值。
func NewEnvConfig() *EnvConfig {
	env := getEnv("ENV", "production")
	logLevel := getEnv("LOG_LEVEL", "info")
	isDebug := logLevel == "debug"

	return &EnvConfig{
		// 6 项核心外部环境变量
		Port:           getEnvAsInt("PORT", 3000),
		Env:            env,
		ProxyAccessKey: getEnv("PROXY_ACCESS_KEY", "your-proxy-access-key"),
		EnableWebUI:    getEnv("ENABLE_WEB_UI", "true") != "false",
		LogLevel:       logLevel,
		LogToConsole:   getEnv("LOG_TO_CONSOLE", "false") == "true",

		// 日志与调试：根据 LogLevel 或稳态默认值自动决定
		EnableRequestLogs:           true,
		EnableResponseLogs:          true,
		QuietPollingLogs:            !isDebug, // 非 debug 模式下静默轮询日志
		RawLogOutput:                false,
		SSEDebugLevel:               "off",
		RewriteResponseModel:        false,
		CorrectResponsesInputTokens: true, // 核心 input_tokens 校正始终开启

		// 网络与请求体限制常量
		RequestTimeout:        300000,           // 5 分钟
		MaxRequestBodySize:    50 * 1024 * 1024, // 50 MB
		EnableCORS:            true,             // 跨域支持始终启用
		CORSOrigin:            "*",
		ResponseHeaderTimeout: 120, // 120 秒
		StreamIdleTimeout:     300, // 300 秒（5 分钟）

		// 指标与熔断稳态常量
		MetricsWindowSize:            10,
		MetricsFailureThreshold:      0.5,
		CircuitEnabled:               true, // 熔断器始终启用
		CircuitFailureThreshold:      4,    // 连续失败 4 次转 Open
		CircuitErrorRateThreshold:    0.6,  // 错误率 60%
		CircuitMinRequests:           10,   // 最小 10 次请求计算错误率
		CircuitCooldownSeconds:       60,   // 冷却 60 秒
		CircuitProbeSuccessThreshold: 2,    // 探测 2 次成功转 Closed

		// 持久化存储常量
		MetricsPersistenceEnabled: true,
		MetricsRetentionDays:      7,
		LogDBPath:                 ".config/logs.db",
	}
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
func LoadDotEnv() []string {
	loaded := make([]string, 0, 2)
	for _, envPath := range candidateDotEnvPaths() {
		if _, err := os.Stat(envPath); err != nil {
			if !os.IsNotExist(err) {
				log.Printf("[Env-Load] 无法访问 %s: %v", envPath, err)
			}
			continue
		}
		if err := godotenv.Load(envPath); err != nil {
			log.Printf("[Env-Load] 加载失败 %s: %v", envPath, err)
			continue
		}
		loaded = append(loaded, envPath)
	}
	return loaded
}

func candidateDotEnvPaths() []string {
	paths := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	add := func(envPath string) {
		if envPath == "" {
			return
		}
		absolutePath, err := filepath.Abs(envPath)
		if err != nil {
			return
		}
		if _, exists := seen[absolutePath]; exists {
			return
		}
		seen[absolutePath] = struct{}{}
		paths = append(paths, absolutePath)
	}
	if executablePath, err := os.Executable(); err == nil {
		add(filepath.Join(filepath.Dir(executablePath), ".env"))
	}
	add(".env")
	return paths
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

// getEnv 获取环境变量，如果不存在则返回默认值
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt 获取环境变量并转换为整数
func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
