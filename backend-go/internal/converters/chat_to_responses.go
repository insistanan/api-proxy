package converters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// chatToResponsesState 流式转换状态
type chatToResponsesState struct {
	Seq          int
	ResponseID   string
	CreatedAt    int64
	CurrentMsgID string
	CurrentFCID  string
	InTextBlock  bool
	InFuncBlock  bool
	FuncArgsBuf  map[int]*strings.Builder // index -> args
	FuncNames    map[int]string           // index -> function name
	FuncCallIDs  map[int]string           // index -> call id
	// ToolIsCustom index -> 是否为自定义工具（custom_tool_call）。
	// 事件类型一经首个事件锁定不再变化，added/delta/done/completed 必须对同一
	// 调用发同类型事件，否则内容安全流式检查会按"参数类型变化"中断整个响应。
	ToolIsCustom map[int]bool
	TextBuf      strings.Builder
	// reasoning state
	ReasoningActive    bool
	ReasoningItemID    string
	ReasoningBuf       strings.Builder
	ReasoningPartAdded bool
	ReasoningIndex     int
	// usage（完整支持详细字段，参考 claude-code-hub）
	InputTokens     int64
	OutputTokens    int64
	CachedTokens    int64 // input_tokens_details.cached_tokens / cache_read_input_tokens
	ReasoningTokens int64 // output_tokens_details.reasoning_tokens
	UsageSeen       bool
	// Claude 缓存 TTL 细分
	CacheCreationTokens   int64  // cache_creation_input_tokens
	CacheCreation5mTokens int64  // cache_creation_5m_input_tokens
	CacheCreation1hTokens int64  // cache_creation_1h_input_tokens
	CacheTTL              string // "5m" | "1h" | "mixed"
	// 首次消息标记
	FirstChunk    bool
	FinishPending bool
	Completed     bool
	Failed        bool
	CustomTools   map[string]customToolSpec
}

func emitResponsesEvent(event string, payload string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload)
}

// lockChatToolIsCustom 锁定该 tool_calls 下标是否为自定义工具（custom_tool_call）。
// 分类只依赖工具名与请求携带的工具上下文；首个事件（output_item.added 或首个
// 参数增量）到达时锁定，此后即使名称后到也不改变类型——added/delta/done/
// completed 必须对同一调用发同类型事件，否则内容安全流式检查会因
// "同一工具调用的参数类型发生变化"中断整个响应。
// 锁定为 function 后名称才到的退化场景，全链路按 function_call 收尾：
// 宁可客户端把 JSON 参数当函数参数解析，也不混发事件类型打断流。
func (st *chatToResponsesState) lockChatToolIsCustom(idx int, name string) bool {
	if st.ToolIsCustom == nil {
		st.ToolIsCustom = make(map[int]bool)
	}
	if locked, exists := st.ToolIsCustom[idx]; exists {
		return locked
	}
	isCustom := false
	if name != "" {
		if spec, ok := st.CustomTools[name]; ok && spec.Kind == responseToolKindCustom {
			isCustom = true
		}
	}
	st.ToolIsCustom[idx] = isCustom
	return isCustom
}

// ConvertOpenAIChatToResponses 将 OpenAI Chat Completions SSE 转换为 Responses SSE 事件
// ctx: 上下文
// modelName: 模型名称
// originalRequestRawJSON: 原始的 Responses API 请求 JSON（用于回显字段）
// requestRawJSON: 转换后的 Chat Completions 请求 JSON
// rawJSON: OpenAI Chat Completions SSE 行
// param: 状态指针（在多次调用间保持状态）
func ConvertOpenAIChatToResponses(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []string {
	if *param == nil {
		*param = &chatToResponsesState{
			FuncArgsBuf: make(map[int]*strings.Builder),
			FuncNames:   make(map[int]string),
			FuncCallIDs: make(map[int]string),
			FirstChunk:  true,
		}
	}
	st := (*param).(*chatToResponsesState)
	if st.Completed || st.Failed {
		return nil
	}

	// 期望 `data: {..}` 格式（冒号后空格可选，判定走 utils 单一出处）
	payload, isData := utils.ParseSSEDataLineBytes(rawJSON)
	if !isData {
		return []string{}
	}
	rawJSON = payload

	// 检查 [DONE] 标记
	if string(rawJSON) == utils.SSEDoneMarker {
		// 生成完成事件
		return st.generateCompletedEvents(originalRequestRawJSON)
	}

	root := gjson.ParseBytes(rawJSON)
	var out []string

	nextSeq := func() int { st.Seq++; return st.Seq }

	// 上游错误 chunk（带 error 字段、无 choices）：转成 response.failed 交给
	// 消费端（responsesStreamEventError 按该 type 上报并终止流），不再静默丢弃
	// 后于 [DONE] 伪造 response.completed。model at capacity 类容量错误的识别
	// 由消费端按事件载荷统一判定。
	if errObj := root.Get("error"); errObj.Exists() && errObj.Type != gjson.Null {
		st.Failed = true
		failed := `{"type":"response.failed","sequence_number":0,"response":{"id":"","object":"response","created_at":0,"status":"failed","error":null}}`
		failed, _ = sjson.Set(failed, "sequence_number", nextSeq())
		failed, _ = sjson.Set(failed, "response.id", st.ResponseID)
		failed, _ = sjson.Set(failed, "response.created_at", st.CreatedAt)
		failed, _ = sjson.SetRaw(failed, "response.error", errObj.Raw)
		out = append(out, emitResponsesEvent("response.failed", failed))
		return out
	}

	// 处理首次 chunk - 初始化并生成 response.created 和 response.in_progress
	if st.FirstChunk {
		st.FirstChunk = false
		st.CustomTools = extractCustomToolContextFromRawRequest(originalRequestRawJSON)
		// 从 chunk 中提取 id
		if id := root.Get("id"); id.Exists() {
			st.ResponseID = id.String()
		} else {
			st.ResponseID = fmt.Sprintf("resp_%d", time.Now().UnixNano())
		}
		st.CreatedAt = time.Now().Unix()

		// 重置状态
		st.TextBuf.Reset()
		st.ReasoningBuf.Reset()
		st.ReasoningActive = false
		st.InTextBlock = false
		st.InFuncBlock = false
		st.CurrentMsgID = ""
		st.CurrentFCID = ""
		st.ReasoningItemID = ""
		st.ReasoningIndex = 0
		st.ReasoningPartAdded = false
		st.FuncArgsBuf = make(map[int]*strings.Builder)
		st.FuncNames = make(map[int]string)
		st.FuncCallIDs = make(map[int]string)
		st.ToolIsCustom = make(map[int]bool)
		st.InputTokens = 0
		st.OutputTokens = 0
		st.CachedTokens = 0
		st.ReasoningTokens = 0
		st.CacheCreationTokens = 0
		st.CacheCreation5mTokens = 0
		st.CacheCreation1hTokens = 0
		st.CacheTTL = ""
		st.UsageSeen = false

		// 发送 response.created
		created := `{"type":"response.created","sequence_number":0,"response":{"id":"","object":"response","created_at":0,"status":"in_progress","background":false,"error":null,"instructions":""}}`
		created, _ = sjson.Set(created, "sequence_number", nextSeq())
		created, _ = sjson.Set(created, "response.id", st.ResponseID)
		created, _ = sjson.Set(created, "response.created_at", st.CreatedAt)
		out = append(out, emitResponsesEvent("response.created", created))

		// 发送 response.in_progress
		inprog := `{"type":"response.in_progress","sequence_number":0,"response":{"id":"","object":"response","created_at":0,"status":"in_progress"}}`
		inprog, _ = sjson.Set(inprog, "sequence_number", nextSeq())
		inprog, _ = sjson.Set(inprog, "response.id", st.ResponseID)
		inprog, _ = sjson.Set(inprog, "response.created_at", st.CreatedAt)
		out = append(out, emitResponsesEvent("response.in_progress", inprog))
	}

	// 解析 choices
	choices := root.Get("choices")
	if !choices.Exists() || !choices.IsArray() {
		return out
	}

	for _, choice := range choices.Array() {
		if st.FinishPending {
			break
		}
		delta := choice.Get("delta")

		finishReason := choice.Get("finish_reason").String()

		// 处理 reasoning_content（OpenAI o1 模型的 reasoning）
		if reasoning := delta.Get("reasoning_content"); reasoning.Exists() && reasoning.String() != "" {
			reasoningText := reasoning.String()

			// 开始 reasoning block
			if !st.ReasoningActive {
				st.ReasoningActive = true
				st.ReasoningIndex = 0
				st.ReasoningBuf.Reset()
				st.ReasoningItemID = fmt.Sprintf("rs_%s_0", st.ResponseID)

				// response.output_item.added for reasoning
				item := `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"id":"","type":"reasoning","status":"in_progress","summary":[]}}`
				item, _ = sjson.Set(item, "sequence_number", nextSeq())
				item, _ = sjson.Set(item, "output_index", st.ReasoningIndex)
				item, _ = sjson.Set(item, "item.id", st.ReasoningItemID)
				out = append(out, emitResponsesEvent("response.output_item.added", item))

				// response.reasoning_summary_part.added
				part := `{"type":"response.reasoning_summary_part.added","sequence_number":0,"item_id":"","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`
				part, _ = sjson.Set(part, "sequence_number", nextSeq())
				part, _ = sjson.Set(part, "item_id", st.ReasoningItemID)
				part, _ = sjson.Set(part, "output_index", st.ReasoningIndex)
				out = append(out, emitResponsesEvent("response.reasoning_summary_part.added", part))
				st.ReasoningPartAdded = true
			}

			// 发送 reasoning delta
			st.ReasoningBuf.WriteString(reasoningText)
			msg := `{"type":"response.reasoning_summary_text.delta","sequence_number":0,"item_id":"","output_index":0,"summary_index":0,"delta":""}`
			msg, _ = sjson.Set(msg, "sequence_number", nextSeq())
			msg, _ = sjson.Set(msg, "item_id", st.ReasoningItemID)
			msg, _ = sjson.Set(msg, "output_index", st.ReasoningIndex)
			msg, _ = sjson.Set(msg, "delta", reasoningText)
			out = append(out, emitResponsesEvent("response.reasoning_summary_text.delta", msg))
		}

		// 处理 content（文本内容）
		if content := delta.Get("content"); content.Exists() && content.String() != "" {
			contentText := content.String()

			// 如果 reasoning 还在活跃状态，先关闭它
			if st.ReasoningActive {
				out = append(out, st.closeReasoningBlock(nextSeq)...)
			}

			// 开始 text block
			if !st.InTextBlock {
				st.InTextBlock = true
				// 计算 output_index：如果有 reasoning 则为 1，否则为 0
				outputIndex := 0
				if st.ReasoningPartAdded {
					outputIndex = 1
				}
				st.CurrentMsgID = fmt.Sprintf("msg_%s_%d", st.ResponseID, outputIndex)

				// response.output_item.added for message
				item := `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"id":"","type":"message","status":"in_progress","content":[],"role":"assistant"}}`
				item, _ = sjson.Set(item, "sequence_number", nextSeq())
				item, _ = sjson.Set(item, "output_index", outputIndex)
				item, _ = sjson.Set(item, "item.id", st.CurrentMsgID)
				out = append(out, emitResponsesEvent("response.output_item.added", item))

				// response.content_part.added
				part := `{"type":"response.content_part.added","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"part":{"type":"output_text","annotations":[],"logprobs":[],"text":""}}`
				part, _ = sjson.Set(part, "sequence_number", nextSeq())
				part, _ = sjson.Set(part, "item_id", st.CurrentMsgID)
				part, _ = sjson.Set(part, "output_index", outputIndex)
				out = append(out, emitResponsesEvent("response.content_part.added", part))
			}

			// 发送 text delta
			st.TextBuf.WriteString(contentText)
			outputIndex := 0
			if st.ReasoningPartAdded {
				outputIndex = 1
			}
			msg := `{"type":"response.output_text.delta","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"delta":"","logprobs":[]}`
			msg, _ = sjson.Set(msg, "sequence_number", nextSeq())
			msg, _ = sjson.Set(msg, "item_id", st.CurrentMsgID)
			msg, _ = sjson.Set(msg, "output_index", outputIndex)
			msg, _ = sjson.Set(msg, "delta", contentText)
			out = append(out, emitResponsesEvent("response.output_text.delta", msg))
		}

		// 处理 tool_calls
		if toolCalls := delta.Get("tool_calls"); toolCalls.Exists() && toolCalls.IsArray() {
			for _, tc := range toolCalls.Array() {
				idx := int(tc.Get("index").Int())

				// 如果 reasoning 还在活跃状态，先关闭它
				if st.ReasoningActive {
					out = append(out, st.closeReasoningBlock(nextSeq)...)
				}

				// 如果 text block 还在活跃状态，先关闭它
				if st.InTextBlock {
					out = append(out, st.closeTextBlock(nextSeq)...)
				}

				// 初始化 tool call 状态
				if st.FuncArgsBuf[idx] == nil {
					st.FuncArgsBuf[idx] = &strings.Builder{}
				}
				if st.FuncCallIDs[idx] == "" {
					st.FuncCallIDs[idx] = fmt.Sprintf("call_%d", idx)
				}

				// 处理 tool call ID
				if tcID := tc.Get("id"); tcID.Exists() && tcID.String() != "" {
					st.FuncCallIDs[idx] = tcID.String()
					st.CurrentFCID = tcID.String()

					// 开始新的 tool call item
					st.InFuncBlock = true

					// 计算 output_index
					outputIndex := idx
					if st.ReasoningPartAdded {
						outputIndex += 1
					}
					if st.CurrentMsgID != "" {
						outputIndex += 1
					}

					// 提前提取 function.name，确保 output_item.added 事件携带 name 字段
					addedName := ""
					if fn := tc.Get("function"); fn.Exists() {
						if name := fn.Get("name"); name.Exists() && name.String() != "" {
							st.FuncNames[idx] = name.String()
							addedName = name.String()
						}
					} else if st.FuncNames[idx] != "" {
						addedName = st.FuncNames[idx]
					}

					// 自定义工具按 Responses 规范发 custom_tool_call 事件（类型按锁定
					// 结果，与后续 delta/done/completed 保持一致），不能与 function_call
					// 事件混发，否则内容安全流式检查按"参数类型变化"中断响应。
					var item string
					if st.lockChatToolIsCustom(idx, addedName) {
						item = `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"id":"","type":"custom_tool_call","status":"in_progress","input":"","call_id":"","name":""}}`
						item, _ = sjson.Set(item, "item.id", fmt.Sprintf("ctc_%s", st.CurrentFCID))
					} else {
						item = `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"id":"","type":"function_call","status":"in_progress","arguments":"","call_id":"","name":""}}`
						item, _ = sjson.Set(item, "item.id", fmt.Sprintf("fc_%s", st.CurrentFCID))
					}
					item, _ = sjson.Set(item, "sequence_number", nextSeq())
					item, _ = sjson.Set(item, "output_index", outputIndex)
					item, _ = sjson.Set(item, "item.call_id", st.CurrentFCID)
					if addedName != "" {
						item, _ = sjson.Set(item, "item.name", addedName)
					}

					out = append(out, emitResponsesEvent("response.output_item.added", item))
				}

				// 处理 function
				if function := tc.Get("function"); function.Exists() {
					// 处理函数名（若尚未存储则补存）
					if name := function.Get("name"); name.Exists() && name.String() != "" {
						if st.FuncNames[idx] == "" {
							st.FuncNames[idx] = name.String()
						}
					}

					// 处理参数
					if args := function.Get("arguments"); args.Exists() && args.String() != "" {
						st.FuncArgsBuf[idx].WriteString(args.String())

						// 自定义工具不发 function_call_arguments.delta：chat 上游的
						// arguments 是 JSON 分片，不是自由文本输入，逐段转发与
						// custom_tool_call 的 done/completed 事件类型冲突（内容安全
						// 会按"参数类型变化"中断流）；参数审核推迟到 done 事件的
						// 完整文本（extractCustomToolInput 还原）。
						if st.lockChatToolIsCustom(idx, st.FuncNames[idx]) {
							continue
						}

						// 计算 output_index
						outputIndex := idx
						if st.ReasoningPartAdded {
							outputIndex += 1
						}
						if st.CurrentMsgID != "" {
							outputIndex += 1
						}

						msg := `{"type":"response.function_call_arguments.delta","sequence_number":0,"item_id":"","output_index":0,"delta":""}`
						msg, _ = sjson.Set(msg, "sequence_number", nextSeq())
						msg, _ = sjson.Set(msg, "item_id", fmt.Sprintf("fc_%s", st.FuncCallIDs[idx]))
						msg, _ = sjson.Set(msg, "output_index", outputIndex)
						msg, _ = sjson.Set(msg, "delta", args.String())
						out = append(out, emitResponsesEvent("response.function_call_arguments.delta", msg))
					}
				}
			}
		}

		// 处理 finish_reason
		if finishReason != "" && finishReason != "null" && finishReason != "none" {
			st.FinishPending = true
			// 关闭所有打开的 blocks
			if st.ReasoningActive {
				out = append(out, st.closeReasoningBlock(nextSeq)...)
			}
			if st.InTextBlock {
				out = append(out, st.closeTextBlock(nextSeq)...)
			}
			if st.InFuncBlock {
				out = append(out, st.closeFuncBlocks(nextSeq)...)
			}
		}
	}

	// 处理 usage（完整支持多格式详细字段，参考 claude-code-hub）
	if usage := root.Get("usage"); usage.Exists() {
		st.UsageSeen = true

		// OpenAI 格式基础字段
		if v := usage.Get("prompt_tokens"); v.Exists() {
			st.InputTokens = v.Int()
		}
		if v := usage.Get("completion_tokens"); v.Exists() {
			st.OutputTokens = v.Int()
		}

		// OpenAI 格式详细字段
		if v := usage.Get("prompt_tokens_details.cached_tokens"); v.Exists() {
			st.CachedTokens = v.Int()
		}
		if v := usage.Get("completion_tokens_details.reasoning_tokens"); v.Exists() {
			st.ReasoningTokens = v.Int()
		}

		// Claude 格式基础字段（优先级高于 OpenAI）
		if v := usage.Get("input_tokens"); v.Exists() {
			st.InputTokens = v.Int()
		}
		if v := usage.Get("output_tokens"); v.Exists() {
			st.OutputTokens = v.Int()
		}

		// Claude 格式缓存字段
		if v := usage.Get("cache_read_input_tokens"); v.Exists() {
			st.CachedTokens = v.Int()
		}
		if v := usage.Get("cache_creation_input_tokens"); v.Exists() {
			st.CacheCreationTokens = v.Int()
		}
		if v := usage.Get("cache_creation_5m_input_tokens"); v.Exists() {
			st.CacheCreation5mTokens = v.Int()
		}
		if v := usage.Get("cache_creation_1h_input_tokens"); v.Exists() {
			st.CacheCreation1hTokens = v.Int()
		}

		// 设置缓存 TTL 标识
		has5m := st.CacheCreation5mTokens > 0
		has1h := st.CacheCreation1hTokens > 0
		if has5m && has1h {
			st.CacheTTL = "mixed"
		} else if has1h {
			st.CacheTTL = "1h"
		} else if has5m {
			st.CacheTTL = "5m"
		}

		// Gemini 格式（自动去重）
		if v := usage.Get("promptTokenCount"); v.Exists() {
			promptTokens := v.Int()
			cachedTokens := usage.Get("cachedContentTokenCount").Int()
			// Gemini 的 promptTokenCount 已包含 cachedContentTokenCount，需要扣除
			actualInput := promptTokens - cachedTokens
			if actualInput < 0 {
				actualInput = 0
			}
			st.InputTokens = actualInput
			st.CachedTokens = cachedTokens
		}
		if v := usage.Get("candidatesTokenCount"); v.Exists() {
			st.OutputTokens = v.Int()
		}
	}

	return out
}
