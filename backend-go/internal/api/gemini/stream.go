package gemini

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/api/common"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/proxycore"
	"insistanan/api-proxy/internal/safety/pipeline"
)

// handleStreamSuccess 处理流式响应
func handleStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	model string,
) (usage *types.Usage, resultErr error) {
	defer func() { resultErr = common.ReportStreamFailure(c, "gemini", resultErr) }()
	envCfg = proxycore.RequestLogEnv(c, envCfg)
	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("[Gemini-Stream] 警告: ResponseWriter 不支持 Flusher")
	}

	stopVeto := pipeline.BindAttachedStreamVeto(c, resp.Body)
	defer stopVeto()
	defer codec.BindStreamBody(c.Request.Context(), resp.Body)()
	// 检查流式响应是否含有 Content-Encoding。由于已强制设置 Accept-Encoding: identity，
	// 正常情况下上游不应压缩 SSE；若上游违规压缩，尝试包装流式解码 Reader 以防分块解析失败。
	if encoding := codec.GetContentEncoding(resp.Header); encoding != "" {
		log.Printf("[Gemini-Stream] 警告: 上游流式响应包含 Content-Encoding: %s，尝试流式解压", encoding)
		wrappedBody, wrapped, wrapErr := codec.WrapStreamReaderIfNeeded(resp)
		if wrapErr != nil {
			if err := c.Request.Context().Err(); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("包装 Gemini 流式解压 Reader 失败 (%s): %w", encoding, wrapErr)
		} else if wrapped {
			resp.Body = wrappedBody
			defer wrappedBody.Close()
			upstreamreq.StripEntityHeadersForRebuiltBody(resp.Header)
		}
	}

	var (
		totalUsage *types.Usage
		err        error
	)

	switch upstreamType {
	case "gemini":
		totalUsage, err = streamGeminiToGemini(c, resp, flusher, envCfg)
	case "claude", "openai", "responses":
		totalUsage, err = streamConvertedToGemini(c, resp, flusher, upstreamType)
	default:
		return nil, fmt.Errorf("Gemini 流式上游 serviceType %q 不支持", upstreamType)
	}

	if envCfg.Current().EnableResponseLogs {
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
	scanner := codec.NewSSEEventReader(c.Request.Context(), resp.Body)

	var totalUsage *types.Usage
	expectedCandidates := 1
	if raw, exists := c.Get("__gemini_req"); exists {
		if request, ok := raw.(*types.GeminiRequest); ok && request.GenerationConfig != nil && request.GenerationConfig.CandidateCount > 0 {
			expectedCandidates = request.GenerationConfig.CandidateCount
		}
	}
	finishedCandidates := make(map[int]bool)
	completed := false

	for scanner.Scan() {
		// 危险命令 AI 闸门的事后否决：非阻塞检查，用户在否决窗口内加黑名单即终止当前流。
		select {
		case <-pipeline.AttachedStreamVetoDone(c):
			return totalUsage, pipeline.HandleAttachedStreamVeto(c)
		default:
		}
		event := scanner.Event()
		line, err := event.DataLine()
		if err != nil {
			return totalUsage, err
		}
		if line == "" {
			if err := common.WriteStream(c.Writer, flusher, []byte(event.Raw)); err != nil {
				return totalUsage, err
			}
			continue
		}

		// 直接转发 SSE 数据
		if jsonData, isData := codec.ParseSSEDataLine(line); isData {
			if jsonData == codec.SSEDoneMarker {
				break
			}
			if jsonData == "" {
				continue
			}
			data, err := parseUpstreamStreamObject(jsonData)
			if err != nil {
				return totalUsage, err
			}

			if candidates, ok := data["candidates"].([]interface{}); ok {
				kept := make([]interface{}, 0, len(candidates))
				for _, raw := range candidates {
					candidate, ok := raw.(map[string]interface{})
					if !ok {
						return totalUsage, fmt.Errorf("Gemini candidate 必须是对象")
					}
					index, _ := candidate["index"].(float64)
					if !finishedCandidates[int(index)] {
						kept = append(kept, raw)
					}
				}
				if len(kept) != len(candidates) {
					data["candidates"] = kept
					encoded, err := json.Marshal(data)
					if err != nil {
						return totalUsage, err
					}
					jsonData, line = string(encoded), "data: "+string(encoded)
				}
			}
			var chunk types.GeminiStreamChunk
			if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
				return totalUsage, fmt.Errorf("解析 Gemini 流式数据失败: %w", err)
			} else {
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
						UnattributedCacheInput: tokenusage.UnattributedCacheInput(chunk.UsageMetadata.PromptTokenCount, chunk.UsageMetadata.CachedContentTokenCount),
						InputTokens:            actualInputTokens,
						OutputTokens:           chunk.UsageMetadata.CandidatesTokenCount + thoughtsTokens,
						CacheReadInputTokens:   chunk.UsageMetadata.CachedContentTokenCount,
					}
					if thoughtsTokens > 0 {
						totalUsage.OutputTokensDetails = &types.ClaudeOutputTokensDetails{ThinkingTokens: thoughtsTokens}
					}
				}
				if err := feedGeminiStreamChunk(c, &chunk); err != nil {
					return totalUsage, err
				}
				for _, candidate := range chunk.Candidates {
					i := candidate.Index
					finishedCandidates[i] = finishedCandidates[i] || candidate.FinishReason != ""
				}
				completed = len(finishedCandidates) >= expectedCandidates
				for _, finished := range finishedCandidates {
					completed = completed && finished
				}
			}

			restoredLine, err := pipeline.RestoreAttachedSSEEvent(c, line)
			if err != nil {
				return totalUsage, err
			}
			if restoredLine == "" {
				continue
			}
			proxycore.MarkRequestLogFirstToken(c)
			if err := common.WriteStream(c.Writer, flusher, []byte(restoredLine+"\n\n")); err != nil {
				return totalUsage, err
			}
		} else if line != "" {
			proxycore.MarkRequestLogFirstToken(c)
			if err := common.WriteStream(c.Writer, flusher, []byte(line+"\n")); err != nil {
				return totalUsage, err
			}
		} else {
			proxycore.MarkRequestLogFirstToken(c)
			if err := common.WriteStream(c.Writer, flusher, []byte("\n")); err != nil {
				return totalUsage, err
			}
		}

	}
	if err := pipeline.AttachedStreamVetoError(c); err != nil {
		return totalUsage, err
	}
	if err := codec.CheckStreamCompletion(c.Request.Context(), completed, scanner.Err()); err != nil {
		return totalUsage, err
	}
	if err := flushGeminiRestoration(c, flusher); err != nil {
		return totalUsage, err
	}
	if err := pipeline.FlushAttachedStreamHooks(c); err != nil {
		return totalUsage, err
	}
	return totalUsage, nil
}

// parseUpstreamStreamObject 统一拒绝无效 JSON 和上游错误信封，避免各转换路径静默跳过。
func parseUpstreamStreamObject(payload string) (map[string]interface{}, error) {
	var object map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &object); err != nil {
		return nil, fmt.Errorf("解析上游 SSE 数据失败: %w", err)
	}
	if object == nil {
		return nil, fmt.Errorf("上游 SSE 数据不是 JSON 对象")
	}
	if object["error"] != nil || object["type"] == "error" {
		return nil, fmt.Errorf("上游流返回错误: %s", payload)
	}
	return object, nil
}

func writeGeminiSSE(c *gin.Context, flusher http.Flusher, payload []byte) error {
	event, err := pipeline.RestoreAttachedSSEEvent(c, "data: "+string(payload)+"\n\n")
	if err != nil {
		return err
	}
	if event == "" {
		return nil
	}
	proxycore.MarkRequestLogFirstToken(c)
	if err := common.WriteStream(c.Writer, flusher, []byte(event)); err != nil {
		return err
	}
	return nil
}

func flushGeminiRestoration(c *gin.Context, flusher http.Flusher) error {
	events, err := pipeline.DrainAttachedStreamRestoration(c)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := common.WriteStream(c.Writer, flusher, []byte(event)); err != nil {
			return err
		}
	}
	return nil
}

func feedGeminiStreamChunk(c *gin.Context, chunk *types.GeminiStreamChunk) error {
	if chunk == nil {
		return nil
	}
	for _, candidate := range chunk.Candidates {
		candidateIndex := candidate.Index
		branch := fmt.Sprintf("gemini.candidate.%d.", candidateIndex)
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if err := pipeline.FeedAttachedStreamTextForKey(c, branch, part.Text); err != nil {
				return err
			}
			if part.FunctionCall != nil {
				arguments, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return fmt.Errorf("序列化 Gemini 工具参数失败: %w", err)
				}
				id := part.FunctionCall.ID
				if id == "" {
					sequence := c.GetInt("__gemini_tool_part_sequence") + 1
					c.Set("__gemini_tool_part_sequence", sequence)
					id = fmt.Sprintf("part-%d", sequence)
				}
				key := fmt.Sprintf("gemini.candidate.%d.call.%s", candidateIndex, id)
				if err := pipeline.FeedAttachedStreamToolArgumentsSnapshot(c, key, string(arguments), part.FunctionCall.Name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
