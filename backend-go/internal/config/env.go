package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
)

type EnvConfig struct {
	Port                 int
	Env                  string
	EnableWebUI          bool
	ProxyAccessKey       string
	LogLevel             string
	EnableRequestLogs    bool
	EnableResponseLogs   bool
	QuietPollingLogs     bool   // 静默轮询端点日志
	RawLogOutput         bool   // 原始日志输出（不缩进、不截断、不重排序）
	SSEDebugLevel        string // SSE 调试级别: off, summary, full
	RewriteResponseModel bool   // 是否改写响应中的 model 字段为请求的 model（默认 false）
	// CorrectResponsesInputTokens 控制是否校正 Responses 透传分支中明显错报的 input_tokens。
	// 校正只作用于下发给客户端的 usage，计费与性能画像仍使用上游原值。
	CorrectResponsesInputTokens bool

	RequestTimeout     int
	MaxRequestBodySize int64 // 请求体最大大小 (字节)，由 MB 配置转换
	EnableCORS         bool
	CORSOrigin         string
	// 指标配置
	MetricsWindowSize       int     // 滑动窗口大小
	MetricsFailureThreshold float64 // 失败率阈值
	// 渠道级熔断配置（internal/circuit；与 Key 级滑动窗口正交）
	CircuitEnabled               bool    // 总开关，false 时全部放行（一键回滚）
	CircuitFailureThreshold      int     // 连续失败多少次转 Open
	CircuitErrorRateThreshold    float64 // 错误率阈值（0-1）
	CircuitMinRequests           int     // 计算错误率前的最小累计请求数
	CircuitCooldownSeconds       int     // Open 冷却秒数，到期后转 HalfOpen
	CircuitProbeSuccessThreshold int     // HalfOpen 连续成功多少次转 Closed
	// 指标持久化配置
	MetricsPersistenceEnabled bool // 是否启用 SQLite 持久化
	MetricsRetentionDays      int  // 数据保留天数（3-30）
	// HTTP 客户端配置
	ResponseHeaderTimeout int // 等待响应头超时时间（秒）
	StreamIdleTimeout     int // 流式响应空闲超时（秒），0 表示不启用；检测"上游挂起但 TCP 通"
	// 日志 sqlite 配置。正文与运行日志进 .config/logs.db，不再写 logs/ 文件。
	LogDBPath    string
	LogToConsole bool
}

// NewEnvConfig 创建环境配置
func NewEnvConfig() *EnvConfig {
	// ENV 优先，NODE_ENV 兼容旧配置。都未设置时默认 production：
	// 打包 exe / 便携目录不写 ENV 时不应落到 development（会放开 /admin/dev/info、Gin DebugMode）。
	// 本地开发在 .env 显式写 ENV=development。
	env := getEnv("ENV", "")
	if env == "" {
		env = getEnv("NODE_ENV", "production")
	}

	return &EnvConfig{
		Port:                        getEnvAsInt("PORT", 3000),
		Env:                         env,
		EnableWebUI:                 getEnv("ENABLE_WEB_UI", "true") != "false",
		ProxyAccessKey:              getEnv("PROXY_ACCESS_KEY", "your-proxy-access-key"),
		LogLevel:                    getEnv("LOG_LEVEL", "info"),
		EnableRequestLogs:           getEnv("ENABLE_REQUEST_LOGS", "true") != "false",
		EnableResponseLogs:          getEnv("ENABLE_RESPONSE_LOGS", "true") != "false",
		QuietPollingLogs:            getEnv("QUIET_POLLING_LOGS", "true") != "false",
		RawLogOutput:                getEnv("RAW_LOG_OUTPUT", "false") == "true",
		SSEDebugLevel:               getEnv("SSE_DEBUG_LEVEL", "off"),
		RewriteResponseModel:        getEnv("REWRITE_RESPONSE_MODEL", "false") == "true",
		CorrectResponsesInputTokens: getEnv("CORRECT_RESPONSES_INPUT_TOKENS", "true") != "false",

		RequestTimeout:     getEnvAsInt("REQUEST_TIMEOUT", 300000),
		MaxRequestBodySize: getEnvAsInt64("MAX_REQUEST_BODY_SIZE_MB", 50) * 1024 * 1024, // MB 转换为字节
		EnableCORS:         getEnv("ENABLE_CORS", "true") != "false",
		CORSOrigin:         getEnv("CORS_ORIGIN", "*"),
		// 指标配置
		MetricsWindowSize:       getEnvAsInt("METRICS_WINDOW_SIZE", 10),
		MetricsFailureThreshold: getEnvAsFloat("METRICS_FAILURE_THRESHOLD", 0.5),
		// 渠道级熔断配置（默认值与 cc-switch 一致）
		CircuitEnabled:               getEnv("CIRCUIT_ENABLED", "true") != "false",
		CircuitFailureThreshold:      clampInt(getEnvAsInt("CIRCUIT_FAILURE_THRESHOLD", 4), 1, 20),
		CircuitErrorRateThreshold:    getEnvAsFloat("CIRCUIT_ERROR_RATE", 0.6),
		CircuitMinRequests:           clampInt(getEnvAsInt("CIRCUIT_MIN_REQUESTS", 10), 2, 100),
		CircuitCooldownSeconds:       clampInt(getEnvAsInt("CIRCUIT_COOLDOWN_SECONDS", 60), 5, 600),
		CircuitProbeSuccessThreshold: clampInt(getEnvAsInt("CIRCUIT_SUCCESS_THRESHOLD", 2), 1, 10),
		// 指标持久化配置
		MetricsPersistenceEnabled: getEnv("METRICS_PERSISTENCE_ENABLED", "true") != "false",
		MetricsRetentionDays:      clampInt(getEnvAsInt("METRICS_RETENTION_DAYS", 7), 3, 30),
		// HTTP 客户端配置
		ResponseHeaderTimeout: clampInt(getEnvAsInt("RESPONSE_HEADER_TIMEOUT", 120), 30, 300), // 30-300 秒，默认 120
		StreamIdleTimeout:     clampInt(getEnvAsInt("STREAM_IDLE_TIMEOUT", 300), 30, 3600),    // 30-3600 秒，默认 300（5 分钟）
		LogDBPath:             getEnv("LOG_DB_PATH", ".config/logs.db"),
		LogToConsole:          getEnv("LOG_TO_CONSOLE", "false") == "true",
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

// getEnvAsInt64 获取环境变量并转换为 int64
func getEnvAsInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// getEnvAsFloat 获取环境变量并转换为浮点数
func getEnvAsFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

// clampInt 将整数限制在指定范围内
func clampInt(value, minVal, maxVal int) int {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}
