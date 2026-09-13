package chat

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func handleStreamSuccess(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, startTime time.Time) (*types.Usage, error) {
	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Chat-Stream] Chat 流式响应开始: %dms, 状态: %d", responseTime, resp.StatusCode)
	}

	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(resp.StatusCode)

	// 检查流式响应是否含有 Content-Encoding。由于已强制设置 Accept-Encoding: identity，
	// 正常情况下上游不应压缩 SSE；若上游违规压缩，尝试包装流式解码 Reader 以防分块解析失败。
	if encoding := utils.GetContentEncoding(resp.Header); encoding != "" {
		log.Printf("[Chat-Stream] 警告: 上游流式响应包含 Content-Encoding: %s，尝试流式解压", encoding)
		wrappedBody, wrapped, wrapErr := utils.WrapStreamReaderIfNeeded(resp)
		if wrapErr != nil {
			log.Printf("[Chat-Stream] 警告: 包装流式解压 Reader 失败 (%s): %v", encoding, wrapErr)
		} else if wrapped {
			resp.Body = wrappedBody
			utils.StripEntityHeadersForRebuiltBody(resp.Header)
		}
	}

	flusher, _ := c.Writer.(http.Flusher)
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var streamUsage *types.Usage

	for {
		// 危险命令 AI 闸门的事后否决：非阻塞检查，用户在否决窗口内加黑名单即终止当前流。
		select {
		case <-hooks.AttachedStreamVetoDone(c):
			return nil, hooks.HandleAttachedStreamVeto(c)
		default:
		}
		rawLine, readErr := reader.ReadBytes('\n')
		if len(rawLine) == 0 && readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, readErr
		}

		line := strings.TrimSuffix(strings.TrimSuffix(string(rawLine), "\n"), "\r")
		if usage := extractChatUsageFromSSELine(line); usage != nil {
			streamUsage = mergeChatUsage(streamUsage, usage)
		}
		text, toolArguments := extractChatStreamSafetyFragments(line)
		if err := hooks.FeedAttachedStreamText(c, text); err != nil {
			return nil, err
		}
		for _, toolArgument := range toolArguments {
			if err := hooks.FeedAttachedStreamToolArgumentsForKey(c, toolArgument.key, toolArgument.fragment); err != nil {
				return nil, err
			}
		}
		restoredLine, err := hooks.RestoreAttachedSSEEvent(c, string(rawLine))
		if err != nil {
			return nil, err
		}
		proxycore.MarkRequestLogFirstToken(c)
		if _, err := c.Writer.Write([]byte(restoredLine)); err != nil {
			return nil, err
		}
		if flusher != nil {
			flusher.Flush()
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, readErr
		}
	}
	remaining, err := hooks.DrainAttachedStreamRestoration(c)
	if err != nil {
		return nil, err
	}
	for _, event := range remaining {
		if _, err := c.Writer.Write([]byte(event)); err != nil {
			return nil, err
		}
	}
	if err := hooks.FlushAttachedStreamHooks(c); err != nil {
		return nil, err
	}
	return streamUsage, nil
}

type chatStreamToolArgumentFragment struct {
	key      string
	fragment string
}

func extractChatStreamSafetyFragments(line string) (string, []chatStreamToolArgumentFragment) {
	payload, ok := utils.SSEDataJSON(strings.TrimSpace(line))
	if !ok {
		return "", nil
	}
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return "", nil
	}
	choices, _ := event["choices"].([]interface{})
	var text strings.Builder
	toolArguments := make([]chatStreamToolArgumentFragment, 0)
	for choiceOffset, choice := range choices {
		choiceMap, _ := choice.(map[string]interface{})
		delta, _ := choiceMap["delta"].(map[string]interface{})
		if content, _ := delta["content"].(string); content != "" {
			text.WriteString(content)
		}
		toolCalls, _ := delta["tool_calls"].([]interface{})
		for toolOffset, toolCall := range toolCalls {
			callMap, _ := toolCall.(map[string]interface{})
			function, _ := callMap["function"].(map[string]interface{})
			if arguments, _ := function["arguments"].(string); arguments != "" {
				choiceKey := streamFragmentKeyPart(choiceMap["index"], choiceOffset)
				toolKey := streamFragmentKeyPart(callMap["index"], toolOffset)
				toolArguments = append(toolArguments, chatStreamToolArgumentFragment{
					key:      fmt.Sprintf("chat.choice.%s.tool.%s", choiceKey, toolKey),
					fragment: arguments,
				})
			}
		}
		if functionCall, ok := delta["function_call"].(map[string]interface{}); ok {
			if arguments, _ := functionCall["arguments"].(string); arguments != "" {
				choiceKey := streamFragmentKeyPart(choiceMap["index"], choiceOffset)
				toolArguments = append(toolArguments, chatStreamToolArgumentFragment{
					key:      fmt.Sprintf("chat.choice.%s.function_call", choiceKey),
					fragment: arguments,
				})
			}
		}
	}
	return text.String(), toolArguments
}

func streamFragmentKeyPart(value interface{}, fallback int) string {
	if value != nil {
		if key := strings.TrimSpace(fmt.Sprint(value)); key != "" {
			return key
		}
	}
	return fmt.Sprintf("%d", fallback)
}
