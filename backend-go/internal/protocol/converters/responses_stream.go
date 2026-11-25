package converters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
)

type responsesStreamState struct {
	Output            map[int]interface{}
	Tools             map[string]responseToolSpec
	Completion        contract.Completion
	Err               error
	Candidate         *int
	Seq               int
	Started           bool
	Completed         bool
	FinishPending     bool
	ResponseID        string
	CreatedAt         int64
	Model             string
	NextOutputIndex   int
	ActiveGeminiIndex int
	ActiveGeminiKind  string
	Blocks            map[int]*responsesStreamBlock
	Usage             types.ResponsesUsage
}

type responsesStreamBlock struct {
	InitialArgs string
	ItemID      string
	Type        string
	Name        string
	CallID      string
	Signature   string
	Text        strings.Builder
	Args        strings.Builder
}

func ConvertClaudeStreamToResponses(_ context.Context, model string, originalRequestJSON []byte, line []byte, state *any, toolContexts ...*ResponseToolContext) ([]string, error) {
	st := ensureResponsesStreamState(state, model)
	if st.Tools == nil {
		st.Tools = responseTools(originalRequestJSON, toolContexts...)
	}
	if st.Completed {
		return nil, nil
	}
	data, isData := codec.SSEDataJSON(strings.TrimSpace(string(line)))
	if !isData {
		return nil, nil
	}

	root := gjson.Parse(data)
	if !gjson.Valid(data) || !root.IsObject() {
		return nil, fmt.Errorf("上游 Claude SSE 数据不是有效 JSON 对象")
	}
	eventType := root.Get("type").String()
	var out []string

	switch eventType {
	case "message_start":
		id := root.Get("message.id").String()
		if id == "" {
			id = fmt.Sprintf("resp_%d", time.Now().UnixNano())
		}
		out = append(out, st.start(id, originalRequestJSON)...)
		if usage := root.Get("message.usage"); usage.Exists() {
			st.captureUsage(usage)
		}
	case "content_block_start":
		out = append(out, st.ensureStarted(originalRequestJSON)...)
		idx := int(root.Get("index").Int())
		block := root.Get("content_block")
		out = append(out, st.startClaudeBlock(idx, block)...)
	case "content_block_delta":
		out = append(out, st.ensureStarted(originalRequestJSON)...)
		idx := int(root.Get("index").Int())
		delta := root.Get("delta")
		out = append(out, st.deltaClaudeBlock(idx, delta)...)
	case "content_block_stop":
		idx := int(root.Get("index").Int())
		if block := st.Blocks[idx]; block == nil || block.Type != "function_call" {
			out = append(out, st.stopBlock(idx)...)
		}
	case "message_delta":
		st.Completion, st.Err = contract.Finish("claude", root.Get("delta.stop_reason").String())
		if st.Err != nil {
			return nil, st.Err
		}
		out = append(out, st.closeAllBlocks()...)
		if usage := root.Get("usage"); usage.Exists() {
			st.captureUsage(usage)
		}
	case "message_stop":
		out = append(out, st.closeAllBlocks()...)
		out = append(out, st.completed(originalRequestJSON)...)
	}
	if st.Err != nil {
		return nil, st.Err
	}
	return out, nil
}

func ConvertGeminiStreamToResponses(_ context.Context, model string, originalRequestJSON []byte, line []byte, state *any, toolContexts ...*ResponseToolContext) ([]string, error) {
	st := ensureResponsesStreamState(state, model)
	if st.Tools == nil {
		st.Tools = responseTools(originalRequestJSON, toolContexts...)
	}
	if st.Completed {
		return nil, nil
	}
	data, isData := codec.SSEDataJSON(strings.TrimSpace(string(line)))
	if !isData {
		return nil, nil
	}
	var chunk types.GeminiStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, fmt.Errorf("解析 Gemini 流式响应失败: %w", err)
	}
	// usage 必须在 candidates 循环之前无条件吸收，并且不能拿"有没有 usage"当定稿信号。
	// Gemini 流的每个 chunk 都可能带 usageMetadata（累积值），所以"见到 usage 就定稿"
	// 会在第一个 chunk 就发出 response.completed，把后续全部内容甩到定稿之后。
	// candidate.finishReason 只登记定稿状态；读取结束时再发送完成事件以吸收 usage 尾包。
	// last-wins 覆盖，不能改成"首个非空生效"：thinking 模型的早期 chunk 会给出只含
	// promptTokenCount/thoughtsTokenCount 的 usageMetadata（没有 candidatesTokenCount），
	// first-wins 会把 output_tokens 永久锁在 0。
	if chunk.UsageMetadata != nil {
		st.Usage.InputTokens = chunk.UsageMetadata.PromptTokenCount - chunk.UsageMetadata.CachedContentTokenCount
		if st.Usage.InputTokens < 0 {
			st.Usage.InputTokens = 0
		}
		// candidatesTokenCount 不含 thoughtsTokenCount，而 Responses 语义下 output_tokens
		// 含 reasoning，所以必须相加（三家语义对照见 types.ClaudeOutputTokensDetails）。
		// 与非流式 parseGeminiUsage 同口径，两条路径不允许分叉。
		st.Usage.OutputTokens = chunk.UsageMetadata.CandidatesTokenCount + chunk.UsageMetadata.ThoughtsTokenCount
		st.Usage.TotalTokens = st.Usage.InputTokens + st.Usage.OutputTokens
		if chunk.UsageMetadata.CachedContentTokenCount > 0 {
			st.Usage.CacheReadInputTokens = chunk.UsageMetadata.CachedContentTokenCount
			st.Usage.InputTokensDetails = &types.InputTokensDetails{CachedTokens: chunk.UsageMetadata.CachedContentTokenCount}
		}
		// 已计入 output_tokens，这里只作拆分展示。
		if chunk.UsageMetadata.ThoughtsTokenCount > 0 {
			st.Usage.OutputTokensDetails = &types.OutputTokensDetails{ReasoningTokens: chunk.UsageMetadata.ThoughtsTokenCount}
		}
	}
	if len(chunk.Candidates) > 1 {
		return nil, fmt.Errorf("Gemini → Responses 不支持多个候选")
	}
	for _, candidate := range chunk.Candidates {
		if st.Candidate != nil && *st.Candidate != candidate.Index {
			return nil, fmt.Errorf("Gemini 流中出现第二个候选")
		}
		index := candidate.Index
		st.Candidate = &index
	}
	if st.FinishPending {
		return nil, nil
	}
	out := st.ensureStarted(originalRequestJSON)

	for _, candidate := range chunk.Candidates {
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Thought && part.Text != "" {
					out = append(out, st.emitReasoningDelta(part.Text)...)
					continue
				}
				if part.Text != "" {
					out = append(out, st.emitTextDelta(part.Text)...)
				}
				if part.FunctionCall != nil {
					args, _ := json.Marshal(part.FunctionCall.Args)
					out = append(out, st.emitFunctionCall(part.FunctionCall.ID, part.FunctionCall.Name, string(args))...)
				}
			}
		}
		if candidate.FinishReason != "" {
			st.Completion, st.Err = contract.Finish("gemini", candidate.FinishReason)
			if st.Err != nil {
				return nil, st.Err
			}
			out = append(out, st.closeAllBlocks()...)
			st.FinishPending = true
			break
		}
	}
	if st.Err != nil {
		return nil, st.Err
	}
	return out, nil
}

func ensureResponsesStreamState(state *any, model string) *responsesStreamState {
	if *state == nil {
		*state = &responsesStreamState{Model: model, Blocks: map[int]*responsesStreamBlock{}, Output: map[int]interface{}{}, Completion: contract.Completion{Status: "completed"}}
	}
	return (*state).(*responsesStreamState)
}
