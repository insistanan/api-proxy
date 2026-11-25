package proxycore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/cachekeepalive"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/safety/pipeline"
	"github.com/insistanan/api-proxy/internal/storage/logger"
	"github.com/tidwall/gjson"
)

const keepaliveTurnKey = "__cache_keepalive_turn"

func beginCacheKeepalive(c *gin.Context, sch *scheduler.ChannelScheduler, kind scheduler.ChannelKind, identity conversation.Identity, accessKey string, quickTest bool) *cachekeepalive.Turn {
	m := sch.CacheKeepalive()
	if m == nil || quickTest || !keepaliveConversationKind(kind) || identity.ExplicitID == "" || identity.Source == "previous_response" {
		return nil
	}
	turn := m.Begin(cacheKeepaliveKey(identity, accessKey))
	c.Set(keepaliveTurnKey, turn)
	return turn
}

func keepaliveConversationKind(kind scheduler.ChannelKind) bool {
	return kind == scheduler.ChannelKindMessages || kind == scheduler.ChannelKindResponses ||
		kind == scheduler.ChannelKindChat || kind == scheduler.ChannelKindGemini
}

func cacheKeepaliveKey(identity conversation.Identity, accessKey string) string {
	// 已有明确会话 ID 时，不再用可能变化的窗口/缓存键拆分它，确保新请求取消旧任务。
	digest := sha256.Sum256([]byte(codec.CanonicalJSON([]string{RedactionScope(identity, accessKey), identity.ClientFamily, identity.ExplicitID, identity.AgentID})))
	return hex.EncodeToString(digest[:])
}

// CheckCacheKeepalivePrompt 只在空闲准备阶段检查新增心跳内容，不重跑整段已检查的历史。
// 需要脱敏的自定义心跳明确拒绝，避免把新映射或改写混入冻结的缓存前缀。
func CheckCacheKeepalivePrompt(hooks *pipeline.Pipeline) func(context.Context, string, string) error {
	return func(ctx context.Context, prompt, conversationID string) error {
		body, err := json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": prompt}}})
		if err != nil {
			return errors.New("编码心跳内容检查请求失败")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/v1/messages", bytes.NewReader(body))
		if err != nil {
			return errors.New("创建心跳内容检查请求失败")
		}
		req.Header.Set("Content-Type", "application/json")
		c := &gin.Context{Request: req}
		pipeline.AttachHookPipeline(c, hooks, pipeline.HookContext{APIType: "messages", ConversationID: conversationID, RequestID: logger.NewRequestID()})
		defer pipeline.ClearAttachedSensitiveData(c)
		defer func() { _ = req.Body.Close() }()
		if err := pipeline.RunAttachedPreRequestHooks(ctx, c, req, "缓存保活", "messages"); err != nil {
			return errors.New("自定义心跳内容未通过内容安全检查，请查看拦截记录")
		}
		if checked, ok := pipeline.PreparedRequestBody(req); ok && gjson.GetBytes(checked, "messages.0.content").String() != prompt {
			return errors.New("自定义心跳内容需要脱敏，请修改后重试")
		}
		return nil
	}
}

func currentCacheKeepalive(c *gin.Context) *cachekeepalive.Turn {
	if c == nil {
		return nil
	}
	value, _ := c.Get(keepaliveTurnKey)
	turn, _ := value.(*cachekeepalive.Turn)
	return turn
}

func (a UpstreamAttempt) captureCacheKeepalive(req *http.Request, observation *availabilityObservation) {
	turn := currentCacheKeepalive(a.Context)
	if turn == nil || !config.SupportsCacheKeepalive(string(a.Kind), observation.upstream.ServiceType) {
		return
	}
	turn.Capture(cachekeepalive.Source{Kind: string(a.Kind), Open: req.GetBody, Size: req.ContentLength,
		Header: req.Header, URL: req.URL.String(), Upstream: observation.upstream,
		APIKey: observation.apiKey, ConversationID: a.LogContext.ConversationID,
		AttemptID: logger.AttemptIDFromContext(req.Context())})
}
