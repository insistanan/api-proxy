// 本文件是上游尝试的观测侧：把每次尝试写入 ChannelLogStore / RequestLogStore 两份日志
// （含首 token 计时与 utils.MaskAPIKey 脱敏）、向调度器登记对话尝试，
// 以及从 types.Usage 读取各类 token 字段的取值口径。
// 这些函数只做记录与读数，不参与故障转移决策。
package proxycore

import (
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/logger"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

var attemptLogCounter uint64

var profileRequestCounter uint64

const requestLogFirstTokenAtKey = "__request_log_first_token_at"

// nextProfileRequestID 生成唯一请求 ID 用于性能画像追踪
func nextProfileRequestID() uint64 {
	return atomic.AddUint64(&profileRequestCounter, 1)
}

func ResetRequestLogFirstToken(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(requestLogFirstTokenAtKey, time.Time{})
}

func MarkRequestLogFirstToken(c *gin.Context) {
	if c == nil {
		return
	}
	if value, ok := c.Get(requestLogFirstTokenAtKey); ok {
		if markedAt, ok := value.(time.Time); ok && !markedAt.IsZero() {
			return
		}
	}
	c.Set(requestLogFirstTokenAtKey, time.Now())
}

func requestLogFirstTokenMs(c *gin.Context, start time.Time) int64 {
	if c == nil {
		return 0
	}
	value, ok := c.Get(requestLogFirstTokenAtKey)
	if !ok {
		return 0
	}
	markedAt, ok := value.(time.Time)
	if !ok || markedAt.IsZero() || markedAt.Before(start) {
		return 0
	}
	return markedAt.Sub(start).Milliseconds()
}

func recordConversationAttempt(channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, upstream *config.UpstreamConfig, logCtx AttemptLogContext, isStream bool) {
	if channelScheduler == nil || strings.TrimSpace(logCtx.ConversationID) == "" || upstream == nil {
		return
	}
	channelScheduler.MarkConversationAttempt(
		logCtx.ConversationID,
		kind,
		logCtx.ChannelIndex,
		upstream.Name,
		logCtx.Model,
		config.ResolveUpstreamModel(logCtx.Model, upstream),
		isStream,
	)
}

func recordAttemptLog(
	c *gin.Context,
	logCtx AttemptLogContext,
	upstream *config.UpstreamConfig,
	apiType string,
	requestID string,
	baseURL string,
	apiKey string,
	status string,
	statusCode int,
	success bool,
	start time.Time,
	errorType string,
	errorMessage string,
	retried bool,
	isStream bool,
	usage *types.Usage,
) {
	if state := requestLifecycle(c); state != nil {
		state.Status = status
		state.StatusCode = statusCode
		state.ErrorType = errorType
		state.ErrorMessage = truncateLogMessage(errorMessage, 500)
	}
	if logCtx.LogStore == nil && logCtx.RequestLogStore == nil {
		return
	}

	channelName := ""
	resolvedModel := logCtx.Model
	if upstream != nil {
		channelName = upstream.Name
		resolvedModel = config.ResolveUpstreamModel(logCtx.Model, upstream)
	}
	transform := ""
	if strings.TrimSpace(logCtx.Model) != "" && strings.TrimSpace(resolvedModel) != "" && logCtx.Model != resolvedModel {
		transform = logCtx.Model + " -> " + resolvedModel
	}
	timestamp := time.Now().Format(time.RFC3339Nano)
	attemptID := ""
	if c != nil && c.Request != nil {
		attemptID = logger.AttemptIDFromContext(c.Request.Context())
	}
	if attemptID == "" {
		attemptID = nextAttemptLogID("attempt")
	}
	durationMs := time.Since(start).Milliseconds()
	firstTokenMs := requestLogFirstTokenMs(c, start)
	errorMessage = truncateLogMessage(errorMessage, 500)

	if logCtx.LogStore != nil {
		logCtx.LogStore.Record(&metrics.ChannelLog{
			RequestID:             requestID,
			AttemptID:             attemptID,
			Timestamp:             timestamp,
			Status:                status,
			StatusCode:            statusCode,
			Success:               success,
			DurationMs:            durationMs,
			APIType:               apiType,
			Model:                 logCtx.Model,
			InputTokens:           usageInputTokens(usage),
			OutputTokens:          usageOutputTokens(usage),
			CacheCreationTokens:   usageCacheCreationTokens(usage),
			CacheReadTokens:       usageCacheReadTokens(usage),
			CacheCreation5mTokens: usageCacheCreation5mTokens(usage),
			CacheCreation1hTokens: usageCacheCreation1hTokens(usage),
			ChannelIndex:          logCtx.ChannelIndex,
			ChannelName:           channelName,
			BaseURL:               baseURL,
			KeyMask:               utils.MaskAPIKey(apiKey),
			ErrorType:             errorType,
			ErrorMessage:          errorMessage,
			Retried:               retried,
			Stream:                isStream,
		})
	}
	if logCtx.RequestLogStore != nil {
		logCtx.RequestLogStore.Record(metrics.RequestLogEntry{
			RequestID:             requestID,
			AttemptID:             attemptID,
			Timestamp:             timestamp,
			APIType:               apiType,
			Status:                status,
			StatusCode:            statusCode,
			Success:               success,
			DurationMs:            durationMs,
			FirstTokenMs:          firstTokenMs,
			TPS:                   calculateRequestTPS(usageOutputTokens(usage), durationMs, firstTokenMs, isStream),
			Model:                 logCtx.Model,
			ResolvedModel:         resolvedModel,
			Transform:             transform,
			InputTokens:           usageInputTokens(usage),
			OutputTokens:          usageOutputTokens(usage),
			CacheCreationTokens:   usageCacheCreationTokens(usage),
			CacheReadTokens:       usageCacheReadTokens(usage),
			CacheCreation5mTokens: usageCacheCreation5mTokens(usage),
			CacheCreation1hTokens: usageCacheCreation1hTokens(usage),
			CacheTTL:              usageCacheTTL(usage),
			ChannelIndex:          logCtx.ChannelIndex,
			ChannelName:           channelName,
			BaseURL:               baseURL,
			KeyMask:               utils.MaskAPIKey(apiKey),
			ErrorType:             errorType,
			ErrorMessage:          errorMessage,
			Retried:               retried,
			Stream:                isStream,
			ConversationID:        logCtx.ConversationID,
		})
	}
}

func nextAttemptLogID(prefix string) string {
	seq := atomic.AddUint64(&attemptLogCounter, 1)
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), seq)
}

func truncateLogMessage(message string, limit int) string {
	message = strings.TrimSpace(utils.RedactSensitivePlaceholdersForLog(message))
	if limit <= 0 || len(message) <= limit {
		return message
	}
	return message[:limit] + "..."
}

func calculateRequestTPS(outputTokens int, durationMs int64, firstTokenMs int64, isStream bool) float64 {
	if outputTokens <= 0 || durationMs <= 0 {
		return 0
	}
	generationMs := durationMs
	if isStream && firstTokenMs > 0 && durationMs > firstTokenMs {
		generationMs = durationMs - firstTokenMs
	}
	if generationMs <= 0 {
		return 0
	}
	tps := float64(outputTokens) / (float64(generationMs) / 1000.0)
	return math.Round(tps*100) / 100
}

func usageInputTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.InputTokens > 0 {
		return usage.InputTokens
	}
	return usage.PromptTokens
}

func usageOutputTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.OutputTokens > 0 {
		return usage.OutputTokens
	}
	return usage.CompletionTokens
}

func usageCacheCreationTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.CacheCreationInputTokens > 0 {
		return usage.CacheCreationInputTokens
	}
	return usage.CacheCreation5mInputTokens + usage.CacheCreation1hInputTokens
}

func usageCacheReadTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	return usage.CacheReadInputTokens
}

func usageCacheCreation5mTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	return usage.CacheCreation5mInputTokens
}

func usageCacheCreation1hTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	return usage.CacheCreation1hInputTokens
}

func usageCacheTTL(usage *types.Usage) string {
	if usage == nil {
		return ""
	}
	return usage.CacheTTL
}
