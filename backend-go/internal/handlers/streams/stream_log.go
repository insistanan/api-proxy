// 本文件负责流式响应结束时的收尾：logStreamCompletion 输出完成/事件统计日志、
// 触发隐式缓存推断，并把上游原始 usage 组装成 *types.Usage 返回；
// logPartialResponse 处理上游中断时的部分内容日志；logSynthesizedContent 打印
// StreamSynthesizer 的合成文本（合成不可用时回退打印原始 LogBuffer）。
package streams

import (
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/logger"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

// logStreamCompletion 记录流完成日志
func logStreamCompletion(c *gin.Context, ctx *Context, envCfg *config.EnvConfig, startTime time.Time) *types.Usage {
	if envCfg.Current().EnableResponseLogs {
		log.Printf("[Messages-Stream] 流式响应完成: %dms", time.Since(startTime).Milliseconds())
	}

	// SSE 事件统计日志
	if envCfg.SSEDebugLevel == "full" || envCfg.SSEDebugLevel == "summary" {
		blockTypeSummary := make(map[string]int)
		for _, bt := range ctx.ContentBlockTypes {
			blockTypeSummary[bt]++
		}
		log.Printf("[Messages-Stream-Summary] 总事件数=%d, content_blocks=%d, 类型分布=%v",
			ctx.EventCount, ctx.ContentBlockCount, blockTypeSummary)
	}

	if envCfg.Current().EnableResponseLogs {
		logSynthesizedContent(c, ctx, envCfg.ShouldLog("debug"))
	}

	// 推断隐式缓存读取
	inferImplicitCacheRead(ctx, envCfg.Current().EnableResponseLogs && envCfg.ShouldLog("debug"))

	// 指标使用上游原始快照。CollectedUsage 可能已经包含客户端补全或隐式缓存推断，
	// 只能用于客户端事件处理，不能污染计费、熔断和性能画像。
	var usage *types.Usage
	hasUsageData := ctx.UpstreamUsage.InputTokens > 0 ||
		ctx.UpstreamUsage.OutputTokens > 0 ||
		ctx.UpstreamUsage.CacheCreationInputTokens > 0 ||
		ctx.UpstreamUsage.CacheReadInputTokens > 0 ||
		ctx.UpstreamUsage.CacheCreation5mInputTokens > 0 ||
		ctx.UpstreamUsage.CacheCreation1hInputTokens > 0
	if hasUsageData {
		usage = &types.Usage{
			InputTokens:                ctx.UpstreamUsage.InputTokens,
			OutputTokens:               ctx.UpstreamUsage.OutputTokens,
			CacheCreationInputTokens:   ctx.UpstreamUsage.CacheCreationInputTokens,
			CacheReadInputTokens:       ctx.UpstreamUsage.CacheReadInputTokens,
			CacheCreation5mInputTokens: ctx.UpstreamUsage.CacheCreation5mInputTokens,
			CacheCreation1hInputTokens: ctx.UpstreamUsage.CacheCreation1hInputTokens,
			CacheTTL:                   ctx.UpstreamUsage.CacheTTL,
		}
	}
	return usage
}

// logPartialResponse 记录部分响应日志
func logPartialResponse(c *gin.Context, ctx *Context, envCfg *config.EnvConfig) {
	if envCfg.Current().EnableResponseLogs {
		logSynthesizedContent(c, ctx, envCfg.ShouldLog("debug"))
	}
}

// logSynthesizedContent 记录合成内容
func logSynthesizedContent(c *gin.Context, ctx *Context, debug bool) {
	if ctx.Synthesizer != nil {
		content := ctx.Synthesizer.GetSynthesizedContent()
		if content != "" && !ctx.Synthesizer.IsParseFailed() {
			trimmed := strings.TrimSpace(content)

			// 仅在“明显是 JSON 续写”的情况下拼接预置前缀，避免出现 "{OK" 这类误导日志
			if ctx.LogPrefillText == "{" && !strings.HasPrefix(strings.TrimLeft(trimmed, " \t\r\n"), "{") {
				left := strings.TrimLeft(trimmed, " \t\r\n")
				if strings.HasPrefix(left, "\"") {
					trimmed = ctx.LogPrefillText + trimmed
				}
			}

			if debug {
				log.Printf("[Messages-Stream] 上游流式响应合成内容:\n%s", utils.RedactSensitivePlaceholdersForLog(strings.TrimSpace(trimmed)))
			}
			if c != nil {
				logger.RecordStreamSynth(c.Request.Context(), "Messages", strings.TrimSpace(trimmed))
			}
			return
		}
	}
	if ctx.LogBuffer.Len() > 0 {
		raw := ctx.LogBuffer.String()
		if debug {
			log.Printf("[Messages-Stream] 上游流式响应原始内容:\n%s", utils.RedactSensitivePlaceholdersForLog(raw))
		}
		if c != nil {
			logger.RecordStreamSynth(c.Request.Context(), "Messages", raw)
		}
	}
}
