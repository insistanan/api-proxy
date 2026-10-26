package utils

import (
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	ContextKeyClaudeCodeDisguise = "client-disguise-claude-code"
	ContextKeyCodexDisguise      = "client-disguise-codex"
	// ContextKeyConversationUserID 由 RunProxyRequest 在会话观测后写入，
	// 供协议回调（如 HandleSuccess）取用注册表生成的会话记录 ID。
	ContextKeyConversationUserID = "conversation-user-id"
	// ContextKeyRequestLogID 由 RunProxyRequest 写入，把同一请求的
	// client/upstream/response 流量日志串到同一 requestId。
	ContextKeyRequestLogID = "logger-request-id"
	// ContextKeyResponsesPreviousIDDropped 表示转换器为避免服务端链与
	// 完整历史叠加而主动移除了 previous_response_id。响应出口不能再伪造
	// 旧 PreviousID 元数据。
	ContextKeyResponsesPreviousIDDropped = "responses-previous-id-dropped"
	// ContextKeyResponsesHistoryBoundary 表示当前 Responses input 已经成为
	// 新的本地历史根。非原生 Responses 转换器不能再把旧 session 追加到它
	// 前面，响应持久化也必须替换旧历史而不是按普通轮次合并。
	ContextKeyResponsesHistoryBoundary = "responses-history-boundary"

	// 伪装版本默认值：与真实客户端版本对齐；可被环境变量覆盖（见下方版本函数）。
	defaultClaudeCodeDisguiseVersion = "2.1.238"
	defaultCodexDisguiseVersion      = "0.150.1"

	// Anthropic SDK 版本 / Node 运行时版本（Claude Code 伪装的 X-Stainless-* 头）。
	claudeDisguiseSDKPackageVersion = "0.124.0"
	claudeDisguiseSDKRuntimeVersion = "v24.3.0"
)

const (
	envKeyClaudeCodeDisguiseVersion = "CLAUDE_CODE_DISGUISE_VERSION"
	envKeyCodexDisguiseVersion      = "CODEX_DISGUISE_VERSION"
)

// claudeCodeDisguiseVersion 返回 Claude Code 伪装版本号：
// 优先取环境变量 CLAUDE_CODE_DISGUISE_VERSION（.env 支持，重启生效），
// 未设置或为空白时使用内置默认值。影响 User-Agent、请求体计费块
// cc_version 与会话指纹（utils/claude_disguise.go）。
func claudeCodeDisguiseVersion() string {
	return disguiseVersionFromEnv(envKeyClaudeCodeDisguiseVersion, defaultClaudeCodeDisguiseVersion)
}

// codexDisguiseVersion 返回 Codex CLI 伪装版本号：
// 优先取环境变量 CODEX_DISGUISE_VERSION（.env 支持，重启生效），
// 未设置或为空白时使用内置默认值。影响 User-Agent 与 version 头。
func codexDisguiseVersion() string {
	return disguiseVersionFromEnv(envKeyCodexDisguiseVersion, defaultCodexDisguiseVersion)
}

// disguiseVersionFromEnv 每次实时读取环境变量：不缓存，避免包初始化早于
// main 的 config.LoadDotEnv 导致 .env 未加载；开销相对每次上游请求可忽略。
func disguiseVersionFromEnv(envKey, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return value
	}
	return fallback
}

// PrepareUpstreamHeaders 准备上游请求头（统一头部处理逻辑）
// 保留原始请求头，移除代理相关头部，设置认证头
func PrepareUpstreamHeaders(c *gin.Context, targetHost string) http.Header {
	return PrepareUpstreamHeadersFromHeader(c.Request.Header, targetHost)
}

// PrepareUpstreamHeadersFromHeader 以显式请求头准备上游请求头
// （R4-a：providers 的显式输入构造路径不经 gin context）。
// 未知请求头原样克隆，包括 x-claude-code-* 和客户端自己的 anthropic-beta。
// 只删除代理头、真实 IP 头和 Accept-Encoding。
func PrepareUpstreamHeadersFromHeader(source http.Header, targetHost string) http.Header {
	var headers http.Header
	if source != nil {
		headers = source.Clone()
	} else {
		headers = make(http.Header)
	}

	// 设置正确的Host头部
	headers.Set("Host", targetHost)

	// 移除代理相关头部
	headers.Del("x-proxy-key")
	headers.Del("X-Forwarded-Host")
	headers.Del("X-Forwarded-Proto")

	// 移除所有可能泄露客户端真实 IP 的头部，确保上游只能看到代理服务器的 IP
	headers.Del("X-Forwarded-For")
	headers.Del("X-Real-IP")
	headers.Del("X-Forwarded")
	headers.Del("Forwarded")
	headers.Del("CF-Connecting-IP")
	headers.Del("True-Client-IP")
	headers.Del("X-Client-IP")

	// 移除 Accept-Encoding，让 Go 的 http.Client 自动处理 gzip 压缩/解压缩
	// 这样可以避免在原始请求包含 Accept-Encoding 时 Go 不自动解压缩的问题
	headers.Del("Accept-Encoding")

	return headers
}

// SetAuthenticationHeader 设置认证头部（根据密钥格式智能选择）
func SetAuthenticationHeader(headers http.Header, apiKey string) {
	// 移除旧的认证头
	headers.Del("authorization")
	headers.Del("x-api-key")
	headers.Del("x-goog-api-key")

	// Claude 官方密钥格式（sk-ant-api03-xxx）使用 x-api-key
	// 符合 Claude API 官方推荐的认证方式
	if strings.HasPrefix(apiKey, "sk-ant-") {
		headers.Set("x-api-key", apiKey)
	} else {
		// 其他格式密钥使用 Authorization: Bearer
		// 适用于 OpenAI、自定义密钥等
		headers.Set("Authorization", "Bearer "+apiKey)
	}
}

// SetGeminiAuthenticationHeader 设置Gemini认证头部
func SetGeminiAuthenticationHeader(headers http.Header, apiKey string) {
	headers.Del("authorization")
	headers.Del("x-api-key")
	headers.Set("x-goog-api-key", apiKey)
}

// EnsureCompatibleUserAgent 确保兼容的User-Agent（仅在必要时设置）
func EnsureCompatibleUserAgent(headers http.Header, serviceType string) {
	userAgent := headers.Get("User-Agent")

	// 仅在Claude服务类型且用户未设置或设置不正确时才修改
	if serviceType == "claude" {
		if userAgent == "" || !strings.HasPrefix(strings.ToLower(userAgent), "claude-cli") {
			headers.Set("User-Agent", "claude-cli/2.0.34 (external, cli)")
		}
	}
}

// StripEntityHeadersForRebuiltBody 移除在解压或重建响应体后会失真的实体头部。
func StripEntityHeadersForRebuiltBody(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Del("Content-Encoding")
	headers.Del("Content-Length")
	headers.Del("Transfer-Encoding")
}

// ForwardResponseHeaders 转发上游响应头到客户端
// 默认响应体已被解压（或重建），剥除 content-encoding 等实体头
func ForwardResponseHeaders(upstreamHeaders http.Header, clientWriter http.ResponseWriter) {
	ForwardResponseHeadersEx(upstreamHeaders, clientWriter, true)
}

// ForwardResponseHeadersEx 转发上游响应头到客户端，可显式指定响应体是否已被解码。
// bodyDecoded 为 true 时剥除 content-encoding（因为 body 已经变为明文）；
// bodyDecoded 为 false 时保留 content-encoding（透传模式，由客户端自行解压）。
func ForwardResponseHeadersEx(upstreamHeaders http.Header, clientWriter http.ResponseWriter, bodyDecoded bool) {
	// 不应转发的头部列表（由框架或代理层自动处理）。
	// 上游 CORS 必须丢掉：网关自己的 CORS 中间件已经写好浏览器可见的来源，
	// 而部分图片上游会同时返回 Allow-Origin: * 与 Allow-Credentials: true。
	// Header.Add 会把两套头叠在一起，浏览器随即拒绝响应并断开连接；
	// 此时上游通常已经完成生图并扣费。
	skipHeaders := map[string]bool{
		"transfer-encoding":                    true,
		"content-length":                       true,
		"connection":                           true,
		"keep-alive":                           true,
		"proxy-authenticate":                   true,
		"proxy-authorization":                  true,
		"te":                                   true,
		"trailer":                              true,
		"upgrade":                              true,
		"access-control-allow-origin":          true,
		"access-control-allow-credentials":     true,
		"access-control-allow-headers":         true,
		"access-control-allow-methods":         true,
		"access-control-expose-headers":        true,
		"access-control-max-age":               true,
		"access-control-allow-private-network": true,
	}
	if bodyDecoded {
		skipHeaders["content-encoding"] = true // 如果已解压则不应转发
	}

	// 复制所有上游响应头到客户端
	for key, values := range upstreamHeaders {
		lowerKey := strings.ToLower(key)
		if skipHeaders[lowerKey] {
			continue
		}
		for _, value := range values {
			clientWriter.Header().Add(key, value)
		}
	}
}

// ApplyCodexDisguise 规范化发往上游的 Codex 身份头，并保留已有会话与追踪字段。
// 开启伪装时无条件用统一 User-Agent 覆盖，避免 Cursor 等客户端自带的旧版本
// UA（如 codex_cli_rs/0.133.0）被透传，导致上游按 UA 版本号误判"需要更新客户端"。
func ApplyCodexDisguise(headers http.Header, stream bool) {
	headers.Set("User-Agent", codexUserAgent())
	if headers.Get("originator") == "" {
		headers.Set("originator", "codex_cli_rs")
	}
	if headers.Get("version") == "" {
		headers.Set("version", codexDisguiseVersion())
	}

	if headers.Get("session_id") == "" {
		headers.Set("session_id", uuid.NewString())
	}

	// X-Codex-Installation-Id: 客户端安装 ID（如果没有则生成一个）
	if headers.Get("X-Codex-Installation-Id") == "" {
		headers.Set("X-Codex-Installation-Id", uuid.NewString())
	}

	// X-Codex-Window-Id: 编辑器窗口 ID（如果没有则使用默认值）
	if headers.Get("X-Codex-Window-Id") == "" {
		headers.Set("X-Codex-Window-Id", uuid.NewString())
	}

	// X-Request-Id: 请求追踪 ID（如果没有则生成）
	if headers.Get("X-Request-Id") == "" && headers.Get("X-Oai-Request-Id") == "" {
		headers.Set("X-Request-Id", uuid.NewString())
	}

	if stream && headers.Get("Accept") == "" {
		headers.Set("Accept", "text/event-stream")
	}
}

// ApplyClaudeCodeDisguise 为非 Claude Code 客户端规范化身份头，并保留已有会话与能力字段。
// anthropic-beta 与客户端已有值合并，不丢弃网关不认识的 beta。
func ApplyClaudeCodeDisguise(headers http.Header, stream bool) {
	isClaudeCodeClient := isClaudeCodeUserAgent(headers.Get("User-Agent"))
	if !isClaudeCodeClient {
		headers.Set("User-Agent", "claude-cli/"+claudeCodeDisguiseVersion()+" (external, cli)")
		headers.Set("X-Stainless-Lang", "js")
		headers.Set("X-Stainless-Package-Version", claudeDisguiseSDKPackageVersion)
		headers.Set("X-Stainless-Runtime", "node")
		headers.Set("X-Stainless-Runtime-Version", claudeDisguiseSDKRuntimeVersion)
		headers.Set("X-Stainless-Os", stainlessOS())
		headers.Set("X-Stainless-Arch", stainlessArch())
		headers.Set("X-Stainless-Retry-Count", "0")
		headers.Set("X-Stainless-Timeout", "600")
	}

	// X-App 和 Beta 集合是严格 Claude Code 网关识别客户端的必要信号。
	headers.Set("X-App", "cli")
	headers.Set("Anthropic-Beta", mergeClaudeCodeBetaHeaders(headers.Values("Anthropic-Beta")))
	headers.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")

	// X-Claude-Code-Session-Id: 会话 ID（如果没有则生成）
	if headers.Get("X-Claude-Code-Session-Id") == "" {
		headers.Set("X-Claude-Code-Session-Id", uuid.NewString())
	}

	// 真实 Claude Code 请求可能缺少部分可选字段，仅补缺失值。
	if headers.Get("X-App") == "" {
		headers.Set("X-App", "cli")
	}
	if headers.Get("X-Stainless-Lang") == "" {
		headers.Set("X-Stainless-Lang", "js")
	}
	if headers.Get("X-Stainless-Package-Version") == "" {
		headers.Set("X-Stainless-Package-Version", claudeDisguiseSDKPackageVersion)
	}
	if headers.Get("X-Stainless-Runtime") == "" {
		headers.Set("X-Stainless-Runtime", "node")
	}
	if headers.Get("X-Stainless-Runtime-Version") == "" {
		headers.Set("X-Stainless-Runtime-Version", claudeDisguiseSDKRuntimeVersion)
	}
	if headers.Get("X-Stainless-Os") == "" {
		headers.Set("X-Stainless-Os", stainlessOS())
	}
	if headers.Get("X-Stainless-Arch") == "" {
		headers.Set("X-Stainless-Arch", stainlessArch())
	}
	if headers.Get("X-Stainless-Retry-Count") == "" {
		headers.Set("X-Stainless-Retry-Count", "0")
	}
	if headers.Get("X-Stainless-Timeout") == "" {
		headers.Set("X-Stainless-Timeout", "600")
	}

	// anthropic-version: API 版本
	if headers.Get("anthropic-version") == "" {
		headers.Set("anthropic-version", "2023-06-01")
	}
	if headers.Get("Accept") == "" {
		if stream {
			headers.Set("Accept", "text/event-stream")
		} else {
			headers.Set("Accept", "application/json")
		}
	}
}

func codexUserAgent() string {
	switch runtime.GOOS {
	case "darwin":
		return "codex_cli_rs/" + codexDisguiseVersion() + " (Mac OS; " + runtime.GOARCH + ") " + codexTargetTriple()
	case "windows":
		return "codex_cli_rs/" + codexDisguiseVersion() + " (Windows; " + runtime.GOARCH + ") " + codexTargetTriple()
	default:
		return "codex_cli_rs/" + codexDisguiseVersion() + " (Linux; " + runtime.GOARCH + ") " + codexTargetTriple()
	}
}

func codexTargetTriple() string {
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	}
	switch runtime.GOOS {
	case "darwin":
		return arch + "-apple-darwin"
	case "windows":
		return arch + "-pc-windows-msvc"
	default:
		return arch + "-unknown-linux-gnu"
	}
}

func stainlessOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return "Unknown"
	}
}

func stainlessArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}
