package utils

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// 未配置真实安装标识时只使用进程级标识，不每个请求随机生成。
// 有真实会话 ID 的请求使用会话派生值；这些值不冒充账号 UUID。
var disguiseInstallationID = uuid.NewString()
var fallbackDisguiseSessionID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("api-proxy:"+disguiseInstallationID+":untracked-session")).String()

func SeedDisguiseSession(headers http.Header, conversationID, protocol string) {
	if strings.TrimSpace(conversationID) == "" {
		return
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("api-proxy:"+disguiseInstallationID+":"+conversationID)).String()
	if protocol == "responses" && headers.Get("session_id") == "" {
		headers.Set("session_id", id)
	}
	if protocol == "claude" && headers.Get("X-Claude-Code-Session-Id") == "" {
		headers.Set("X-Claude-Code-Session-Id", id)
	}
}

// PrepareTargetIdentity 清除与目标协议不匹配的能力、身份和账号头。
func PrepareTargetIdentity(headers http.Header, protocol string, compatibility bool) {
	for name := range headers {
		lower := strings.ToLower(name)
		claudeHeader := strings.HasPrefix(lower, "x-claude-code-") || strings.HasPrefix(lower, "x-stainless-") || strings.HasPrefix(lower, "anthropic-") || lower == "x-app"
		codexHeader := strings.HasPrefix(lower, "x-codex-") || lower == "originator" || lower == "version" || lower == "session_id" || lower == "openai-beta"
		// Account/组织身份必须来自上游凭据，不能透传下游客户端的账号。
		accountHeader := lower == "chatgpt-account-id" || lower == "openai-organization" || lower == "openai-project"
		if accountHeader || (claudeHeader && protocol != "claude") || (codexHeader && protocol != "responses") {
			headers.Del(name)
			continue
		}
		if !compatibility && ((claudeHeader && !strings.HasPrefix(lower, "anthropic-")) || (codexHeader && lower != "openai-beta")) {
			headers.Del(name)
		}
	}
	if !compatibility {
		headers.Set("User-Agent", "api-proxy")
		headers.Del("Anthropic-Dangerous-Direct-Browser-Access")
	}
	// 协议必需字段与客户端伪装无关，Responses 转 Messages 的普通 API 模式也必须携带。
	if protocol == "claude" && headers.Get("Anthropic-Version") == "" {
		headers.Set("Anthropic-Version", "2023-06-01")
	}
}
