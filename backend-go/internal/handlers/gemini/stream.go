package gemini

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/handlers/proxycore"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// handleStreamSuccess 处理流式响应
func handleStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	model string,
) (*types.Usage, error) {
	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("[Gemini-Stream] 警告: ResponseWriter 不支持 Flusher")
	}

	// 检查流式响应是否含有 Content-Encoding。由于已强制设置 Accept-Encoding: identity，
	// 正常情况下上游不应压缩 SSE；若上游违规压缩，尝试包装流式解码 Reader 以防分块解析失败。
	if encoding := utils.GetContentEncoding(resp.Header); encoding != "" {
		log.Printf("[Gemini-Stream] 警告: 上游流式响应包含 Content-Encoding: %s，尝试流式解压", encoding)
		wrappedBody, wrapped, wrapErr := utils.WrapStreamReaderIfNeeded(resp)
		if wrapErr != nil {
			log.Printf("[Gemini-Stream] 警告: 包装流式解压 Reader 失败 (%s): %v", encoding, wrapErr)
		} else if wrapped {
			resp.Body = wrappedBody
			utils.StripEntityHeadersForRebuiltBody(resp.Header)
		}
	}

	var (
		totalUsage *types.Usage
		err        error
	)

	switch upstreamType {
	case "gemini":
		totalUsage, err = streamGeminiToGemini(c, resp, flusher, envCfg)
	case "claude":
		totalUsage, err = streamClaudeToGemini(c, resp, flusher, envCfg, model)
	case "openai":
		totalUsage, err = streamOpenAIToGemini(c, resp, flusher, envCfg, model)
	default:
		// 默认透传
		totalUsage, err = streamGeminiToGemini(c, resp, flusher, envCfg)
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Gemini-Stream-Timing] 流式响应完成: %dms", responseTime)
	}

	return totalUsage, err
}

// streamGeminiToGemini Gemini 上游直接透传
func streamGeminiToGemini(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	envCfg *config.EnvConfig,
) (*types.Usage, error) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	var totalUsage *types.Usage

	for scanner.Scan() {
		// 危险命令 AI 闸门的事后否决：非阻塞检查，用户在否决窗口内加黑名单即终止当前流。
		select {
		case <-hooks.AttachedStreamVetoDone(c):
			return nil, hooks.HandleAttachedStreamVeto(c)
		default:
		}
		line := scanner.Text()

		// 直接转发 SSE 数据
		if jsonData, isData := utils.ParseSSEDataLine(line); isData {

			// 尝试解析 usage
			var chunk types.GeminiStreamChunk
			if err := json.Unmarshal([]byte(jsonData), &chunk); err == nil {
				if err := feedGeminiStreamChunk(c, &chunk); err != nil {
					return nil, err
				}
				if chunk.UsageMetadata != nil {
					// promptTokenCount 含 cachedContentTokenCount，扣除后才是新增输入。
					// 与非流式路径（handler.go 的同名扣减）保持同一口径，含 <0 钳制：
					// 上游异常回包（cached > prompt）不能让负数流进指标与 sqlite 日志。
					actualInputTokens := chunk.UsageMetadata.PromptTokenCount - chunk.UsageMetadata.CachedContentTokenCount
					if actualInputTokens < 0 {
						actualInputTokens = 0
					}
					// candidatesTokenCount 不含 thoughtsTokenCount，指标按两者之和记，
					// 与非流式 handler.go 同口径（理由与官方证据见
					// types.ClaudeOutputTokensDetails）。透传给客户端的原生 SSE 不改。
					thoughtsTokens := chunk.UsageMetadata.ThoughtsTokenCount
					totalUsage = &types.Usage{
						InputTokens:          actualInputTokens,
						OutputTokens:         chunk.UsageMetadata.CandidatesTokenCount + thoughtsTokens,
						CacheReadInputTokens: chunk.UsageMetadata.CachedContentTokenCount,
					}
					if thoughtsTokens > 0 {
						totalUsage.OutputTokensDetails = &types.ClaudeOutputTokensDetails{ThinkingTokens: thoughtsTokens}
					}
				}
			}

			restoredLine, err := hooks.RestoreAttachedSSEEvent(c, line)
			if err != nil {
				return nil, err
			}
			proxycore.MarkRequestLogFirstToken(c)
			fmt.Fprintf(c.Writer, "%s\n", restoredLine)
		} else if line != "" {
			proxycore.MarkRequestLogFirstToken(c)
			fmt.Fprintf(c.Writer, "%s\n", line)
		} else {
			proxycore.MarkRequestLogFirstToken(c)
			fmt.Fprintf(c.Writer, "\n")
		}

		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := flushGeminiRestoration(c, flusher); err != nil {
		return nil, err
	}
	if err := hooks.FlushAttachedStreamHooks(c); err != nil {
		return nil, err
	}
	return totalUsage, nil
}

// streamClaudeToGemini Claude 流式响应转换为 Gemini 格式
func streamClaudeToGemini(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	envCfg *config.EnvConfig,
	model string,
) (*types.Usage, error) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var totalUsage *types.Usage
	var currentText strings.Builder

	for scanner.Scan() {
		// 危险命令 AI 闸门的事后否决：非阻塞检查，用户在否决窗口内加黑名单即终止当前流。
		select {
		case <-hooks.AttachedStreamVetoDone(c):
			return nil, hooks.HandleAttachedStreamVeto(c)
		default:
		}
		line := scanner.Text()

		jsonData, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}
		if jsonData == utils.SSEDoneMarker {
			break
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "content_block_delta":
			// 文本增量
			delta, ok := event["delta"].(map[string]interface{})
			if !ok {
				continue
			}
			deltaType, _ := delta["type"].(string)
			if deltaType == "text_delta" {
				text, _ := delta["text"].(string)
				if err := hooks.FeedAttachedStreamText(c, text); err != nil {
					return nil, err
				}
				currentText.WriteString(text)

				// 转换为 Gemini 格式
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							Content: &types.GeminiContent{
								Parts: []types.GeminiPart{
									{Text: text},
								},
								Role: "model",
							},
						},
					},
				}

				chunkBytes, _ := json.Marshal(geminiChunk)
				proxycore.MarkRequestLogFirstToken(c)
				if err := writeGeminiSSE(c, flusher, chunkBytes); err != nil {
					return nil, err
				}
			}

		case "message_delta":
			// 消息完成，包含 usage
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				inputTokens := 0
				outputTokens := 0
				cacheCreationTokens := 0
				cacheReadTokens := 0
				cacheCreation5mTokens := 0
				cacheCreation1hTokens := 0
				if v, ok := usage["input_tokens"].(float64); ok {
					inputTokens = int(v)
				}
				if v, ok := usage["output_tokens"].(float64); ok {
					outputTokens = int(v)
				}
				if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
					cacheCreationTokens = int(v)
				}
				if v, ok := usage["cache_read_input_tokens"].(float64); ok {
					cacheReadTokens = int(v)
				}
				if v, ok := usage["cache_creation_5m_input_tokens"].(float64); ok {
					cacheCreation5mTokens = int(v)
				}
				if v, ok := usage["cache_creation_1h_input_tokens"].(float64); ok {
					cacheCreation1hTokens = int(v)
				}
				totalUsage = &types.Usage{
					InputTokens:                inputTokens,
					OutputTokens:               outputTokens,
					CacheCreationInputTokens:   cacheCreationTokens,
					CacheReadInputTokens:       cacheReadTokens,
					CacheCreation5mInputTokens: cacheCreation5mTokens,
					CacheCreation1hInputTokens: cacheCreation1hTokens,
				}

				// 发送带 finishReason 和 usage 的最终块
				// Gemini 契约要求 promptTokenCount 含缓存内容，缓存量另由
				// cachedContentTokenCount 单列（客户端不再求和）。Claude 上游给的
				// input_tokens 已是 uncached，所以这里必须把 cacheRead 加回去，
				// 与非流式路径 converters.ClaudeResponseToGemini 保持同一口径。
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							FinishReason: "STOP",
						},
					},
					UsageMetadata: &types.GeminiUsageMetadata{
						PromptTokenCount:        inputTokens + cacheReadTokens,
						CandidatesTokenCount:    outputTokens,
						CachedContentTokenCount: cacheReadTokens,
						TotalTokenCount:         inputTokens + cacheReadTokens + outputTokens,
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				proxycore.MarkRequestLogFirstToken(c)
				if err := writeGeminiSSE(c, flusher, chunkBytes); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := flushGeminiRestoration(c, flusher); err != nil {
		return nil, err
	}
	if err := hooks.FlushAttachedStreamHooks(c); err != nil {
		return nil, err
	}
	return totalUsage, nil
}

// streamOpenAIToGemini OpenAI 流式响应转换为 Gemini 格式
func streamOpenAIToGemini(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	envCfg *config.EnvConfig,
	model string,
) (*types.Usage, error) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var totalUsage *types.Usage
	var currentText strings.Builder

	for scanner.Scan() {
		// 危险命令 AI 闸门的事后否决：非阻塞检查，用户在否决窗口内加黑名单即终止当前流。
		select {
		case <-hooks.AttachedStreamVetoDone(c):
			return nil, hooks.HandleAttachedStreamVeto(c)
		default:
		}
		line := scanner.Text()

		jsonData, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}
		if jsonData == utils.SSEDoneMarker {
			break
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			continue
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			// 检查是否有 usage（某些 OpenAI 兼容 API 在最后发送）
			if usage, ok := chunk["usage"].(map[string]interface{}); ok {
				promptTokens := 0
				completionTokens := 0
				if v, ok := usage["prompt_tokens"].(float64); ok {
					promptTokens = int(v)
				}
				if v, ok := usage["completion_tokens"].(float64); ok {
					completionTokens = int(v)
				}
				totalUsage = &types.Usage{
					InputTokens:  promptTokens,
					OutputTokens: completionTokens,
				}

				// 发送带 usage 的最终块
				geminiChunk := types.GeminiStreamChunk{
					UsageMetadata: &types.GeminiUsageMetadata{
						PromptTokenCount:     promptTokens,
						CandidatesTokenCount: completionTokens,
						TotalTokenCount:      promptTokens + completionTokens,
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				proxycore.MarkRequestLogFirstToken(c)
				if err := writeGeminiSSE(c, flusher, chunkBytes); err != nil {
					return nil, err
				}
			}
			continue
		}

		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}

		// 检查 finish_reason
		finishReason, hasFinish := choice["finish_reason"].(string)

		// 获取 delta
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			if hasFinish && finishReason != "" {
				// 发送 finishReason
				geminiFinishReason := openaiFinishReasonToGemini(finishReason)
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							FinishReason: geminiFinishReason,
						},
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				proxycore.MarkRequestLogFirstToken(c)
				if err := writeGeminiSSE(c, flusher, chunkBytes); err != nil {
					return nil, err
				}
			}
			continue
		}

		// 提取文本内容
		content, _ := delta["content"].(string)
		if content != "" {
			if err := hooks.FeedAttachedStreamText(c, content); err != nil {
				return nil, err
			}
			currentText.WriteString(content)

			geminiChunk := types.GeminiStreamChunk{
				Candidates: []types.GeminiCandidate{
					{
						Content: &types.GeminiContent{
							Parts: []types.GeminiPart{
								{Text: content},
							},
							Role: "model",
						},
					},
				},
			}

			chunkBytes, _ := json.Marshal(geminiChunk)
			proxycore.MarkRequestLogFirstToken(c)
			if err := writeGeminiSSE(c, flusher, chunkBytes); err != nil {
				return nil, err
			}
		}

		// 如果有 finish_reason，发送
		if hasFinish && finishReason != "" {
			geminiFinishReason := openaiFinishReasonToGemini(finishReason)
			geminiChunk := types.GeminiStreamChunk{
				Candidates: []types.GeminiCandidate{
					{
						FinishReason: geminiFinishReason,
					},
				},
			}
			chunkBytes, _ := json.Marshal(geminiChunk)
			proxycore.MarkRequestLogFirstToken(c)
			if err := writeGeminiSSE(c, flusher, chunkBytes); err != nil {
				return nil, err
			}
		}
	}
	if err := flushGeminiRestoration(c, flusher); err != nil {
		return nil, err
	}
	if err := hooks.FlushAttachedStreamHooks(c); err != nil {
		return nil, err
	}
	return totalUsage, nil
}

func writeGeminiSSE(c *gin.Context, flusher http.Flusher, payload []byte) error {
	event, err := hooks.RestoreAttachedSSEEvent(c, "data: "+string(payload)+"\n\n")
	if err != nil {
		return err
	}
	proxycore.MarkRequestLogFirstToken(c)
	if _, err := fmt.Fprint(c.Writer, event); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

func flushGeminiRestoration(c *gin.Context, flusher http.Flusher) error {
	events, err := hooks.DrainAttachedStreamRestoration(c)
	if err != nil {
		return err
	}
	for _, event := range events {
		if _, err := fmt.Fprint(c.Writer, event); err != nil {
			return err
		}
	}
	if len(events) > 0 && flusher != nil {
		flusher.Flush()
	}
	return nil
}

func feedGeminiStreamChunk(c *gin.Context, chunk *types.GeminiStreamChunk) error {
	if chunk == nil {
		return nil
	}
	for candidateIndex, candidate := range chunk.Candidates {
		if candidate.Content == nil {
			continue
		}
		for partIndex, part := range candidate.Content.Parts {
			if err := hooks.FeedAttachedStreamText(c, part.Text); err != nil {
				return err
			}
			if part.FunctionCall != nil && len(part.FunctionCall.Args) > 0 {
				arguments, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return fmt.Errorf("序列化 Gemini 工具参数失败: %w", err)
				}
				key := fmt.Sprintf("gemini.candidate.%d.part.%d.%s", candidateIndex, partIndex, part.FunctionCall.Name)
				if err := hooks.FeedAttachedStreamToolArgumentsForKey(c, key, string(arguments)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// openaiFinishReasonToGemini 将 OpenAI 停止原因转换为 Gemini 格式
func openaiFinishReasonToGemini(finishReason string) string {
	switch finishReason {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "tool_calls":
		return "STOP"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}
