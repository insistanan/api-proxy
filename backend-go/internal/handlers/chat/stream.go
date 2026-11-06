package chat

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

func handleStreamSuccess(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, startTime time.Time) (*types.Usage, error) {
	stopVeto := hooks.BindAttachedStreamVeto(c, resp.Body)
	defer stopVeto()
	defer utils.BindStreamBody(c.Request.Context(), resp.Body)()
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
			if err := c.Request.Context().Err(); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("包装 Chat 流式解压 Reader 失败 (%s): %w", encoding, wrapErr)
		} else if wrapped {
			resp.Body = wrappedBody
			defer wrappedBody.Close()
			utils.StripEntityHeadersForRebuiltBody(resp.Header)
		}
	}

	flusher, _ := c.Writer.(http.Flusher)
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var streamUsage *types.Usage
	completion := chatStreamCompletion{choices: make(map[int]bool)}
	var streamReadErr error
	var trailingEventOpen, trailingLineTerminated bool

	for {
		// 危险命令 AI 闸门的事后否决：非阻塞检查，用户在否决窗口内加黑名单即终止当前流。
		select {
		case <-hooks.AttachedStreamVetoDone(c):
			return nil, hooks.HandleAttachedStreamVeto(c)
		default:
		}
		rawLine, readErr := reader.ReadBytes('\n')
		if err := hooks.AttachedStreamVetoError(c); err != nil {
			return nil, err
		}
		if len(rawLine) == 0 && readErr != nil {
			streamReadErr = readErr
			break
		}

		line := strings.TrimSuffix(strings.TrimSuffix(string(rawLine), "\n"), "\r")
		trailingEventOpen = line != ""
		trailingLineTerminated = strings.HasSuffix(string(rawLine), "\n")
		if err := completion.consume(line); err != nil {
			return nil, err
		}
		if usage := extractChatUsageFromSSELine(line); usage != nil {
			streamUsage = mergeChatUsage(streamUsage, usage)
		}
		texts, toolArguments := extractChatStreamSafetyFragments(line)
		for _, text := range texts {
			if err := hooks.FeedAttachedStreamTextForKey(c, text.key, text.fragment); err != nil {
				return nil, err
			}
		}
		for _, toolArgument := range toolArguments {
			if err := hooks.FeedAttachedStreamToolArgumentsForKey(c, toolArgument.key, toolArgument.fragment, toolArgument.name); err != nil {
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
		if completion.done {
			// 不再读取 [DONE] 后的连接；补齐事件分隔符供 SSE 客户端分派。
			separator := "\n"
			if !strings.HasSuffix(string(rawLine), "\n") {
				separator = "\n\n"
			}
			if _, err := c.Writer.Write([]byte(separator)); err != nil {
				return nil, err
			}
			if flusher != nil {
				flusher.Flush()
			}
			break
		}

		if readErr != nil {
			streamReadErr = readErr
			break
		}
	}
	if err := utils.CheckStreamCompletion(c.Request.Context(), completion.completed(), streamReadErr); err != nil {
		return nil, err
	}
	if trailingEventOpen && !completion.done {
		separator := "\n"
		if !trailingLineTerminated {
			separator = "\n\n"
		}
		if _, err := c.Writer.Write([]byte(separator)); err != nil {
			return nil, err
		}
		if flusher != nil {
			flusher.Flush()
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

// chatStreamCompletion 跟踪透传 Chat 流的各个 choice，保留定稿后的 usage 尾包。
type chatStreamCompletion struct {
	choices map[int]bool
	done    bool
}

func (s *chatStreamCompletion) consume(line string) error {
	payload, isData := utils.ParseSSEDataLine(line)
	if !isData || payload == "" {
		return nil
	}
	if payload == utils.SSEDoneMarker {
		s.done = true
		return nil
	}
	var chunk struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Index        int    `json:"index"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return fmt.Errorf("解析 Chat SSE 数据失败: %w", err)
	}
	if strings.TrimSpace(payload) == "null" {
		return fmt.Errorf("上游 Chat SSE 数据不是 JSON 对象")
	}
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return fmt.Errorf("上游 Chat 流返回错误: %s", payload)
	}
	for _, choice := range chunk.Choices {
		finished := choice.FinishReason != "" && choice.FinishReason != "none" && choice.FinishReason != "null"
		s.choices[choice.Index] = s.choices[choice.Index] || finished
	}
	return nil
}

func (s *chatStreamCompletion) completed() bool {
	if len(s.choices) == 0 {
		return false
	}
	if s.done {
		return true
	}
	for _, finished := range s.choices {
		if !finished {
			return false
		}
	}
	return true
}

type chatStreamToolArgumentFragment struct {
	key      string
	fragment string
	name     string
}

func extractChatStreamSafetyFragments(line string) ([]chatStreamToolArgumentFragment, []chatStreamToolArgumentFragment) {
	payload, ok := utils.SSEDataJSON(strings.TrimSpace(line))
	if !ok {
		return nil, nil
	}
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return nil, nil
	}
	choices, _ := event["choices"].([]interface{})
	texts := make([]chatStreamToolArgumentFragment, 0)
	toolArguments := make([]chatStreamToolArgumentFragment, 0)
	for choiceOffset, choice := range choices {
		choiceMap, _ := choice.(map[string]interface{})
		choiceKey := streamFragmentKeyPart(choiceMap["index"], choiceOffset)
		delta, _ := choiceMap["delta"].(map[string]interface{})
		if content, _ := delta["content"].(string); content != "" {
			texts = append(texts, chatStreamToolArgumentFragment{key: "chat.choice." + choiceKey + ".", fragment: content})
		}
		toolCalls, _ := delta["tool_calls"].([]interface{})
		for toolOffset, toolCall := range toolCalls {
			callMap, _ := toolCall.(map[string]interface{})
			function, _ := callMap["function"].(map[string]interface{})
			arguments, _ := function["arguments"].(string)
			name, _ := function["name"].(string)
			if arguments != "" || name != "" {
				toolKey := streamFragmentKeyPart(callMap["index"], toolOffset)
				toolArguments = append(toolArguments, chatStreamToolArgumentFragment{
					key:      fmt.Sprintf("chat.choice.%s.tool.%s", choiceKey, toolKey),
					fragment: arguments,
					name:     name,
				})
			}
		}
		if functionCall, ok := delta["function_call"].(map[string]interface{}); ok {
			arguments, _ := functionCall["arguments"].(string)
			name, _ := functionCall["name"].(string)
			if arguments != "" || name != "" {
				toolArguments = append(toolArguments, chatStreamToolArgumentFragment{
					key:      fmt.Sprintf("chat.choice.%s.function_call", choiceKey),
					fragment: arguments,
					name:     name,
				})
			}
		}
	}
	return texts, toolArguments
}

func streamFragmentKeyPart(value interface{}, fallback int) string {
	if value != nil {
		if key := strings.TrimSpace(fmt.Sprint(value)); key != "" {
			return key
		}
	}
	return fmt.Sprintf("%d", fallback)
}
