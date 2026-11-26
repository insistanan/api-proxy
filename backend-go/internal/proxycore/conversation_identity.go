package proxycore

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/infra/utils"
	"insistanan/api-proxy/internal/protocol/visionmedia"
)

const conversationBranchContextKey = "__conversation_branch_key"

// conversationBranchKey 只使用稳定的执行身份，不使用轮次、请求、缓存键或父节点标识。
// 分字段编码避免不同维度或包含分隔符的标识相撞；空键表示客户端未提供分支身份。
func conversationBranchKey(identity conversation.Identity) string {
	if identity.ThreadID == "" && identity.BranchID == "" && identity.AgentID == "" {
		return ""
	}
	hash := sha256.New()
	for _, field := range []string{identity.ThreadID, identity.BranchID, identity.AgentID} {
		_, _ = hash.Write([]byte(strconv.Itoa(len(field)) + ":" + field))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// RedactionScope 只接受已认证调用方和明确、稳定的会话身份。
// previous_response_id 会逐轮改变，窗口/缓存键也不是会话授权依据。
func RedactionScope(identity conversation.Identity, accessKey string) string {
	if accessKey == "" || identity.ExplicitID == "" || identity.Source == "previous_response" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(accessKey))
	for _, field := range []string{"redaction-scope-v1", identity.ClientFamily, identity.ExplicitID} {
		_, _ = mac.Write([]byte(strconv.Itoa(len(field)) + ":" + field))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// ResolveConversationIdentity 只把语义明确的 conversation/thread/composer 标识
// 作为强身份。缓存键、普通用户 ID 和 Cursor session 仅作为历史匹配范围。
func ResolveConversationIdentity(c *gin.Context, bodyBytes []byte) conversation.Identity {
	var root map[string]interface{}
	_ = json.Unmarshal(bodyBytes, &root)
	return resolveConversationIdentity(c, bodyBytes, root)
}

// 会话身份、历史指纹和图片指纹共享一次解码；只用于当前观测阶段，不缓存可变请求正文。
func parseConversationObservation(c *gin.Context, kind string, body []byte) (conversation.Identity, conversation.Transcript, []string, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return conversation.Identity{}, conversation.Transcript{}, nil, err
	}
	return resolveConversationIdentity(c, body, root), buildConversationTranscript(kind, root), visionmedia.ExtractImageFingerprintsFromValue(root), nil
}

func resolveConversationIdentity(c *gin.Context, bodyBytes []byte, root map[string]any) conversation.Identity {
	identity := conversation.Identity{ClientFamily: detectConversationClient(c, bodyBytes)}
	metadata, _ := root["metadata"].(map[string]interface{})

	if c != nil {
		codexThreadID, codexSessionID := extractCodexTurnIdentity(c.GetHeader("X-Codex-Turn-Metadata"))
		identity.ThreadID = firstConversationValue(firstHeader(c,
			"X-Thread-Id", "Thread-Id", "Thread_id"), codexThreadID)
		identity.BranchID = firstHeader(c, "X-Branch-Id", "Branch-Id", "Branch_id")
		identity.AgentID = firstHeader(c,
			"X-Cursor-Agent-Id", "X-Agent-Id", "Agent-Id", "Agent_id")
		identity.ParentAgentID = firstHeader(c,
			"X-Cursor-Parent-Agent-Id", "X-Parent-Agent-Id", "Parent-Agent-Id")
		if value := firstHeader(c,
			"X-Cursor-Conversation-Id", "X-Cursor-Composer-Id", "X-Composer-Id",
			"X-Conversation-Id", "Conversation_id", "Conversation-Id"); value != "" {
			identity.ExplicitID = value
			identity.Source = "conversation_header"
		}
		if identity.ExplicitID == "" {
			if value := firstHeader(c, "X-Claude-Code-Session-Id"); value != "" {
				if identity.ClientFamily == "cursor" {
					identity.ScopeID = value
				} else {
					identity.ExplicitID = value
					identity.Source = "claude_code_session"
				}
			}
		}
		if identity.ExplicitID == "" {
			if value := strings.TrimSpace(c.Query("conversation_id")); value != "" {
				identity.ExplicitID = value
				identity.Source = "conversation_query"
			}
		}
		if identity.ExplicitID == "" {
			if value := firstHeader(c, "Session_id", "Session-Id", "X-Session-Id"); value != "" {
				if identity.ClientFamily == "cursor" {
					identity.ScopeID = value
				} else {
					identity.ExplicitID = value
					identity.Source = "session_header"
				}
			}
		}
		if identity.ExplicitID == "" {
			if codexThreadID != "" {
				identity.ExplicitID = codexThreadID
				identity.Source = "codex_thread"
			} else if codexSessionID != "" {
				identity.ScopeID = firstConversationValue(identity.ScopeID, codexSessionID)
			}
		}
		identity.ScopeID = firstConversationValue(identity.ScopeID, firstHeader(c,
			"X-Cursor-Session-Id", "X-Cursor-Window-Id", "X-Codex-Window-Id", "X-Codex-Installation-Id"))
	}

	if identity.ExplicitID == "" {
		if value := firstMapString(root, "conversation_id", "conversationId", "thread_id", "threadId", "composer_id", "composerId"); value != "" {
			identity.ExplicitID = value
			identity.Source = "request_conversation"
		} else if value := firstMapString(metadata, "conversation_id", "conversationId", "thread_id", "threadId", "composer_id", "composerId"); value != "" {
			identity.ExplicitID = value
			identity.Source = "metadata_conversation"
		} else if value := firstMapString(root, "previous_response_id"); value != "" {
			identity.ExplicitID = value
			identity.Source = "previous_response"
		}
	}
	if identity.ExplicitID == "" {
		if value := firstConversationValue(firstMapString(metadata, "session_id", "sessionId"), firstMapString(root, "session_id", "sessionId")); value != "" {
			if identity.ClientFamily == "cursor" {
				identity.ScopeID = firstConversationValue(identity.ScopeID, value)
			} else {
				identity.ExplicitID = value
				identity.Source = "request_session"
			}
		}
	}

	identity.ThreadID = firstConversationValue(identity.ThreadID,
		firstMapString(root, "thread_id", "threadId"),
		firstMapString(metadata, "thread_id", "threadId"))
	identity.BranchID = firstConversationValue(identity.BranchID,
		firstMapString(root, "branch_id", "branchId"),
		firstMapString(metadata, "branch_id", "branchId"))
	identity.AgentID = firstConversationValue(identity.AgentID,
		firstMapString(root, "agent_id", "agentId"),
		firstMapString(metadata, "agent_id", "agentId"))
	identity.ParentAgentID = firstConversationValue(identity.ParentAgentID,
		firstMapString(root, "parent_agent_id", "parentAgentId"),
		firstMapString(metadata, "parent_agent_id", "parentAgentId"))

	if userID := firstMapString(metadata, "user_id", "userId"); userID != "" {
		var embedded map[string]interface{}
		if strings.HasPrefix(strings.TrimSpace(userID), "{") && json.Unmarshal([]byte(userID), &embedded) == nil {
			if identity.ExplicitID == "" {
				if value := firstMapString(embedded, "conversation_id", "conversationId", "thread_id", "threadId", "composer_id", "composerId"); value != "" {
					identity.ExplicitID = value
					identity.Source = "structured_user_session"
				} else if value := firstMapString(embedded, "session_id", "sessionId"); value != "" {
					if identity.ClientFamily == "cursor" {
						identity.ScopeID = firstConversationValue(identity.ScopeID, value)
					} else {
						identity.ExplicitID = value
						identity.Source = "structured_user_session"
					}
				}
			}
			identity.ThreadID = firstConversationValue(identity.ThreadID, firstMapString(embedded, "thread_id", "threadId"))
			identity.BranchID = firstConversationValue(identity.BranchID, firstMapString(embedded, "branch_id", "branchId"))
			identity.AgentID = firstConversationValue(identity.AgentID, firstMapString(embedded, "agent_id", "agentId"))
			identity.ParentAgentID = firstConversationValue(identity.ParentAgentID, firstMapString(embedded, "parent_agent_id", "parentAgentId"))
		} else if identity.ScopeID == "" {
			identity.ScopeID = userID
		}
	}
	// 仅提供通用线程请求头的客户端也具有稳定身份，避免并行请求走匿名历史猜测。
	if identity.ExplicitID == "" && identity.ThreadID != "" {
		identity.ExplicitID = identity.ThreadID
		identity.Source = "thread_header"
	}
	identity.ScopeID = firstConversationValue(identity.ScopeID,
		firstMapString(root, "prompt_cache_key", "promptCacheKey", "user"),
		firstMapString(metadata, "scope_id", "scopeId"))
	identity.LaneHash = buildConversationLaneHash(root)
	return identity
}

// BuildConversationTranscript 把各协议的历史消息规整为累计哈希前缀。
// 只有严格扩展的请求才可能命中已有会话。
func BuildConversationTranscript(apiKind string, bodyBytes []byte) conversation.Transcript {
	var root map[string]interface{}
	if json.Unmarshal(bodyBytes, &root) != nil {
		return conversation.Transcript{}
	}
	return buildConversationTranscript(apiKind, root)
}

func buildConversationTranscript(apiKind string, root map[string]any) conversation.Transcript {
	items := conversationItems(strings.ToLower(strings.TrimSpace(apiKind)), root)
	prefixes := make([]string, 0, len(items))
	state := []byte("conversation-transcript-v1")
	for _, item := range items {
		normalized := normalizeConversationItem(item)
		if normalized == nil || !hasConversationSemanticContent(normalized) {
			continue
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			continue
		}
		payload := make([]byte, 0, len(state)+len(encoded)+1)
		payload = append(payload, state...)
		payload = append(payload, 0)
		payload = append(payload, encoded...)
		sum := sha256.Sum256(payload)
		state = append(state[:0], sum[:]...)
		prefixes = append(prefixes, hex.EncodeToString(sum[:]))
	}
	return conversation.Transcript{PrefixHashes: prefixes}
}

func conversationItems(apiKind string, root map[string]interface{}) []interface{} {
	switch apiKind {
	case "messages", "chat":
		items, _ := root["messages"].([]interface{})
		return items
	case "responses":
		if text, ok := root["input"].(string); ok && strings.TrimSpace(text) != "" {
			return []interface{}{map[string]interface{}{"role": "user", "content": text}}
		}
		items, _ := root["input"].([]interface{})
		return items
	case "gemini":
		items, _ := root["contents"].([]interface{})
		return items
	default:
		return nil
	}
}

func normalizeConversationItem(value interface{}) interface{} {
	stripInjected := false
	if item, ok := value.(map[string]interface{}); ok {
		role, _ := item["role"].(string)
		stripInjected = strings.EqualFold(role, "user")
	}
	return normalizeConversationValueMode(value, stripInjected)
}

func normalizeConversationLaneValue(value interface{}) interface{} {
	return normalizeConversationValueMode(value, false)
}

func normalizeConversationValueMode(value interface{}, stripInjected bool) interface{} {
	switch current := value.(type) {
	case nil:
		return nil
	case string:
		text := strings.TrimSpace(strings.ReplaceAll(current, "\r\n", "\n"))
		if stripInjected {
			text = cleanAndExtractRealPrompt(text)
		}
		if text == "" {
			return nil
		}
		if len(text) > 4096 {
			sum := sha256.Sum256([]byte(text))
			return "sha256:" + hex.EncodeToString(sum[:])
		}
		return text
	case []interface{}:
		result := make([]interface{}, 0, len(current))
		for _, item := range current {
			if normalized := normalizeConversationValueMode(item, stripInjected); normalized != nil {
				result = append(result, normalized)
			}
		}
		return result
	case map[string]interface{}:
		if blockType, _ := current["type"].(string); strings.EqualFold(blockType, "thinking") || strings.EqualFold(blockType, "redacted_thinking") {
			return nil
		}
		if fingerprint := visionmedia.ImageFingerprintForBlock(current); fingerprint != "" {
			return map[string]interface{}{"type": "image", "fingerprint": fingerprint}
		}
		result := make(map[string]interface{}, len(current))
		for key, item := range current {
			switch strings.ToLower(key) {
			case "cache_control", "signature", "thought_signature", "reasoning", "reasoning_content":
				continue
			}
			if normalized := normalizeConversationValueMode(item, stripInjected); normalized != nil {
				result[key] = normalized
			}
		}
		return result
	default:
		return current
	}
}

func hasConversationSemanticContent(value interface{}) bool {
	switch current := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(current) != ""
	case []interface{}:
		for _, item := range current {
			if hasConversationSemanticContent(item) {
				return true
			}
		}
		return false
	case map[string]interface{}:
		for key, item := range current {
			switch strings.ToLower(key) {
			case "role", "type", "status", "id":
				continue
			}
			if hasConversationSemanticContent(item) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func buildConversationLaneHash(root map[string]interface{}) string {
	lane := make(map[string]interface{})
	for _, key := range []string{"system", "instructions", "systemInstruction"} {
		if value, ok := root[key]; ok {
			lane[key] = normalizeConversationLaneValue(value)
		}
	}
	if tools, ok := root["tools"]; ok {
		names := make([]string, 0, 16)
		collectToolNames(tools, &names)
		lane["tools"] = utils.DedupeSortedStrings(names)
	}
	if len(lane) == 0 {
		return ""
	}
	encoded, err := json.Marshal(lane)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func collectToolNames(value interface{}, names *[]string) {
	switch current := value.(type) {
	case []interface{}:
		for _, item := range current {
			collectToolNames(item, names)
		}
	case map[string]interface{}:
		if name, ok := current["name"].(string); ok && strings.TrimSpace(name) != "" {
			*names = append(*names, strings.TrimSpace(name))
		}
		for _, key := range []string{"function", "functionDeclarations", "tools"} {
			collectToolNames(current[key], names)
		}
	}
}

func detectConversationClient(c *gin.Context, bodyBytes []byte) string {
	bodyLooksLikeCursor := bytes.Contains(bodyBytes, []byte("<agent_transcripts>")) || bytes.Contains(bodyBytes, []byte("<user_info>"))
	if c != nil {
		all := strings.ToLower(strings.Join([]string{
			c.GetHeader("User-Agent"),
			c.GetHeader("X-Client-Name"),
			c.GetHeader("X-App"),
		}, " "))
		switch {
		case strings.Contains(all, "cursor") || hasHeaderPrefix(c, "x-cursor-") || bodyLooksLikeCursor:
			return "cursor"
		case strings.Contains(all, "claude-code") || c.GetHeader("X-Claude-Code-Session-Id") != "":
			return "claude-code"
		case strings.Contains(all, "codex") || c.GetHeader("X-Codex-Turn-Metadata") != "":
			return "codex"
		}
	}
	if bodyLooksLikeCursor {
		return "cursor"
	}
	return "unknown"
}

func hasHeaderPrefix(c *gin.Context, prefix string) bool {
	if c == nil || c.Request == nil {
		return false
	}
	prefix = strings.ToLower(prefix)
	for key := range c.Request.Header {
		if strings.HasPrefix(strings.ToLower(key), prefix) {
			return true
		}
	}
	return false
}

func firstHeader(c *gin.Context, names ...string) string {
	if c == nil {
		return ""
	}
	for _, name := range names {
		if value := strings.TrimSpace(c.GetHeader(name)); value != "" {
			return value
		}
	}
	return ""
}

func firstMapString(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstConversationValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func extractCodexTurnIdentity(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	var metadata struct {
		ThreadID  string `json:"thread_id"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(value), &metadata) != nil {
		return "", ""
	}
	return strings.TrimSpace(metadata.ThreadID), strings.TrimSpace(metadata.SessionID)
}
