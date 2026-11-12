package proxycore

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/intent"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/urlhealth"
)

func prepareIntentRoute(c *gin.Context, cfg *config.ConfigManager, sched *scheduler.ChannelScheduler, kind scheduler.ChannelKind, body []byte, conversation, requestID string, explicit *int) (func(), error) {
	service := intent.FromContext(c.Request.Context())
	if service == nil || !cfg.GetIntentRouting(string(kind)).Enabled {
		return func() {}, nil
	}
	manualPool := ""
	manualChannel := ""
	if explicit != nil {
		up, _, err := ResolveRequestedUpstream(cfg, kind, *explicit)
		if err != nil {
			return func() {}, err
		}
		manualPool = up.PoolID
	} else if registry := sched.GetConversationRegistry(); registry != nil {
		override, err := registry.LookupRouteOverride(conversation)
		if err != nil {
			return func() {}, err
		}
		if override != nil {
			if override.Kind != string(kind) {
				return func() {}, fmt.Errorf("人工固定渠道的协议与当前请求不一致")
			}
			up, _, err := ResolveRequestedUpstream(cfg, kind, override.ChannelIndex)
			if err != nil {
				return func() {}, err
			}
			manualPool = up.PoolID
			manualChannel = up.ID
		}
	}
	ctx, release, err := service.Begin(c.Request.Context(), string(kind), conversation, requestID, body, manualPool)
	if err != nil {
		return release, err
	}
	if manualChannel != "" {
		route := scheduler.PoolConstraintFrom(ctx)
		route.ChannelID = manualChannel
		ctx = scheduler.WithPoolConstraint(ctx, route)
	}
	c.Request = c.Request.WithContext(ctx)
	return release, nil
}

// pinIntentCredentials 复用上一轮凭据组合，避免轮转 Key 破坏不透明历史归属。
func (a UpstreamAttempt) pinIntentCredentials() UpstreamAttempt {
	turn := intent.TurnFrom(a.Context.Request.Context())
	if turn == nil || a.Upstream == nil {
		return a
	}
	identity := turn.PinnedIdentity(a.Upstream.ID)
	if identity == "" {
		return a
	}
	for urlIndex, candidate := range a.URLResults {
		for keyIndex, key := range a.Upstream.APIKeys {
			up := a.Upstream.Clone()
			up.BaseURL = candidate.URL
			if intent.MakeTarget(up, key, "").Identity != identity {
				continue
			}
			if turn.Strict() {
				up.APIKeys = []string{key}
				a.URLResults = []urlhealth.URLLatencyResult{candidate}
			} else {
				up.APIKeys = append([]string{key}, a.Upstream.APIKeys[:keyIndex]...)
				up.APIKeys = append(up.APIKeys, a.Upstream.APIKeys[keyIndex+1:]...)
				remaining := a.URLResults[urlIndex+1:]
				a.URLResults = append([]urlhealth.URLLatencyResult{candidate}, a.URLResults[:urlIndex]...)
				a.URLResults = append(a.URLResults, remaining...)
			}
			a.Upstream = up
			return a
		}
	}
	return a
}

func (a UpstreamAttempt) checkIntentCandidate(up *config.UpstreamConfig, key string) error {
	turn := intent.TurnFrom(a.Context.Request.Context())
	if turn == nil {
		return nil
	}
	if turn.HasOpaqueHistory() {
		native := map[scheduler.ChannelKind]string{scheduler.ChannelKindMessages: "claude", scheduler.ChannelKindResponses: "responses", scheduler.ChannelKindChat: "openai", scheduler.ChannelKindGemini: "gemini"}
		if up.ServiceType != native[a.Kind] || up.StripThoughtSignature {
			return &intent.HistoryError{Message: "含加密历史的请求需要原生协议且保留签名，已阻止有损协议转换"}
		}
	}
	return turn.CheckTarget(up, key, config.ResolveUpstreamModel(a.RequestedModel, up))
}

func intentRouteError(c *gin.Context, err error) {
	status, code := http.StatusConflict, "INTENT_ROUTE_BLOCKED"
	var routingErr *intent.RoutingError
	if errors.As(err, &routingErr) {
		code = routingErr.Code
		switch code {
		case "INTENT_CLASSIFIER_FAILED":
			status = http.StatusBadGateway
		case "INTENT_STATE_FAILED":
			status = http.StatusInternalServerError
		default:
			status = http.StatusUnprocessableEntity
		}
	}
	log.Printf("[Intent-Blocked] requestId=%s code=%s error=%v", BindRequestLogID(c), code, err)
	c.JSON(status, gin.H{"error": gin.H{"message": err.Error(), "type": "intent_route_error", "code": code}})
}

func intentHistoryProtected(c *gin.Context) bool { return intent.TurnFrom(c.Request.Context()) != nil }
