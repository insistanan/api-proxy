package converters

import (
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
)

// TestResponsesRawItemsCarryOwnHistory 锁定“input 是否自带完整历史”的判据。
// 判据必须保守：漏判只是保留 previous_response_id（等于修复前的行为），
// 误判会删掉服务端链而 input 其实只是后缀，导致整段上下文丢失。
func TestResponsesRawItemsCarryOwnHistory(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "developer 会话根",
			input: `[{"type":"message","role":"developer","content":"sys"},{"type":"message","role":"user","content":"hi"}]`,
			want:  true,
		},
		{
			name:  "system 会话根",
			input: `[{"type":"message","role":"system","content":"sys"},{"type":"message","role":"user","content":"hi"}]`,
			want:  true,
		},
		{
			name:  "user 开头且带 assistant 产出",
			input: `[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","summary":[]},{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"ls"}]`,
			want:  true,
		},
		{
			name:  "只有工具结果后缀",
			input: `[{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]`,
			want:  false,
		},
		{
			name:  "只有一条新 user 输入",
			input: `[{"type":"message","role":"user","content":"下一步"}]`,
			want:  false,
		},
		{
			name:  "reasoning 加工具结果但无会话根",
			input: `[{"type":"reasoning","summary":[]},{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]`,
			want:  false,
		},
		{
			name:  "空数组",
			input: `[]`,
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rawItems []json.RawMessage
			if err := json.Unmarshal([]byte(tt.input), &rawItems); err != nil {
				t.Fatalf("构造 input 失败: %v", err)
			}
			if got := responsesRawItemsCarryOwnHistory(rawItems); got != tt.want {
				t.Errorf("responsesRawItemsCarryOwnHistory() = %v, 期望 %v", got, tt.want)
			}
		})
	}
}

// TestTrimResponsesPassthroughInputWithoutLocalSession 覆盖本地 session 为空的场景：
// 进程重启、TTL 过期、LRU 驱逐或首次代理该会话都会走到这里。此前该分支无条件原样
// 转发，previous_response_id 与完整历史会同时到达上游，同一段历史被算两次。
func TestTrimResponsesPassthroughInputWithoutLocalSession(t *testing.T) {
	const fullHistory = `[{"type":"message","role":"developer","content":"sys"},{"type":"message","role":"user","content":"hi"},{"type":"message","role":"assistant","content":"yo"},{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]`
	const toolSuffix = `[{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]`

	tests := []struct {
		name         string
		body         string
		sess         *types.Session
		wantChain    string
		wantInputLen int
	}{
		{
			name:         "session 为 nil 且 input 自带完整历史时切断旧链",
			body:         `{"model":"gpt-5","previous_response_id":"resp_123","input":` + fullHistory + `}`,
			sess:         nil,
			wantChain:    "",
			wantInputLen: 4,
		},
		{
			name:         "session 存在但历史为空时同样切断旧链",
			body:         `{"model":"gpt-5","previous_response_id":"resp_123","input":` + fullHistory + `}`,
			sess:         &types.Session{ID: "sess_empty"},
			wantChain:    "",
			wantInputLen: 4,
		},
		{
			name:         "增量工具结果后缀保留旧链",
			body:         `{"model":"gpt-5","previous_response_id":"resp_123","input":` + toolSuffix + `}`,
			sess:         nil,
			wantChain:    "resp_123",
			wantInputLen: 1,
		},
		{
			name:         "无 previous_response_id 的完整历史不受影响",
			body:         `{"model":"gpt-5","input":` + fullHistory + `}`,
			sess:         nil,
			wantChain:    "",
			wantInputLen: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req types.ResponsesRequest
			if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
				t.Fatalf("解析测试请求失败: %v", err)
			}
			got, err := convertResponsesPassthroughRequestWithSession("", []byte(tt.body), nil, tt.sess, &req)
			if err != nil {
				t.Fatalf("convertResponsesPassthroughRequestWithSession() 返回错误: %v", err)
			}
			if chain := gjson.GetBytes(got, "previous_response_id").String(); chain != tt.wantChain {
				t.Errorf("previous_response_id = %q, 期望 %q", chain, tt.wantChain)
			}
			// 切断旧链时只能删链，绝不能同时裁剪 input——否则上下文两头都丢。
			if inputLen := len(gjson.GetBytes(got, "input").Array()); inputLen != tt.wantInputLen {
				t.Errorf("input 长度 = %d, 期望 %d", inputLen, tt.wantInputLen)
			}
			if model := gjson.GetBytes(got, "model").String(); model != "gpt-5" {
				t.Errorf("model 被意外改写为 %q", model)
			}
		})
	}
}

// responsesTrimTestHistory 造一段常规本地会话历史：developer 根 + 三轮 user/assistant。
func responsesTrimTestHistory() []types.ResponsesItem {
	return []types.ResponsesItem{
		{Type: "message", Role: "developer", Content: "sys"},
		{Type: "message", Role: "user", Content: "u1"},
		{Type: "message", Role: "assistant", Content: "a1"},
		{Type: "message", Role: "user", Content: "u2"},
		{Type: "message", Role: "assistant", Content: "a2"},
		{Type: "message", Role: "user", Content: "u3"},
		{Type: "message", Role: "assistant", Content: "a3"},
	}
}

// TestTrimResponsesPassthroughInputWithRewrittenHistory 覆盖客户端自己压缩上下文的场景：
// Cursor 不发 compaction item，而是把中间若干轮换成一条摘要、再原样重发最近几轮。这类
// input 比本地历史更短且不以 user/developer 开头，条数比较（looksLikeResponsesFullReplay）
// 与形态比较（responsesRawItemsCarryOwnHistory）都看不见它，只有与本地历史的重叠能认出来。
// 漏判会让旧链与新历史根同时发往上游，压缩前的服务端历史被整段前置，客户端读到的 usage
// 不降反升，压缩后立刻又要压缩。
func TestTrimResponsesPassthroughInputWithRewrittenHistory(t *testing.T) {
	toolHistory := []types.ResponsesItem{
		{Type: "message", Role: "developer", Content: "sys"},
		{Type: "message", Role: "user", Content: "u1"},
		{Type: "custom_tool_call", CallID: "c1", Name: "exec", Arguments: "{}"},
		{Type: "custom_tool_call_output", CallID: "c1", Content: "ok"},
	}

	tests := []struct {
		name         string
		input        string
		history      []types.ResponsesItem
		wantChain    string
		wantInputLen int
	}{
		{
			name:         "摘要成 assistant 新根并重发最近两轮时切断旧链",
			input:        `[{"type":"message","role":"assistant","content":"前两轮摘要"},{"type":"message","role":"user","content":"u3"},{"type":"message","role":"assistant","content":"a3"},{"type":"message","role":"user","content":"u4"}]`,
			history:      responsesTrimTestHistory(),
			wantChain:    "",
			wantInputLen: 4,
		},
		{
			name:         "单条重放的 assistant 产出即可判定重放",
			input:        `[{"type":"message","role":"assistant","content":"a2"},{"type":"message","role":"user","content":"u4"}]`,
			history:      responsesTrimTestHistory(),
			wantChain:    "",
			wantInputLen: 2,
		},
		{
			name:         "内容重复的单条 user 输入保留旧链",
			input:        `[{"type":"message","role":"user","content":"u3"}]`,
			history:      responsesTrimTestHistory(),
			wantChain:    "resp_123",
			wantInputLen: 1,
		},
		{
			// 上游已成功但客户端没收到响应时的重试会原样重发同一条工具结果，
			// 此时必须保留链，否则上游只收到一条孤立的工具结果。
			name:         "重试重发同一条工具结果时保留旧链",
			input:        `[{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]`,
			history:      toolHistory,
			wantChain:    "resp_123",
			wantInputLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"model":"gpt-5","previous_response_id":"resp_123","input":` + tt.input + `}`
			var req types.ResponsesRequest
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				t.Fatalf("解析测试请求失败: %v", err)
			}
			sess := &types.Session{ID: "sess_1", Messages: tt.history}
			got, err := convertResponsesPassthroughRequestWithSession("", []byte(body), nil, sess, &req)
			if err != nil {
				t.Fatalf("convertResponsesPassthroughRequestWithSession() 返回错误: %v", err)
			}
			if chain := gjson.GetBytes(got, "previous_response_id").String(); chain != tt.wantChain {
				t.Errorf("previous_response_id = %q, 期望 %q", chain, tt.wantChain)
			}
			// 切断旧链时只能删链，绝不能同时裁剪 input——否则上下文两头都丢。
			if inputLen := len(gjson.GetBytes(got, "input").Array()); inputLen != tt.wantInputLen {
				t.Errorf("input 长度 = %d, 期望 %d", inputLen, tt.wantInputLen)
			}
		})
	}
}

// TestResponsesInputRerootsSession 锁定非透传上游（openai / claude / gemini）的换根判据。
// 这些链路不走 input 裁剪，而是靠 MergeResponsesItemsDedup 把本地历史前置到 input 之前；
// 换根没被识别时，压缩前的整段历史会被重新拼回压缩后的新根之前。
func TestResponsesInputRerootsSession(t *testing.T) {
	history := responsesTrimTestHistory()

	tests := []struct {
		name  string
		input string
		sess  *types.Session
		want  bool
	}{
		{
			name:  "摘要成 assistant 新根并重发最近两轮",
			input: `[{"type":"message","role":"assistant","content":"前两轮摘要"},{"type":"message","role":"user","content":"u3"},{"type":"message","role":"assistant","content":"a3"},{"type":"message","role":"user","content":"u4"}]`,
			sess:  &types.Session{ID: "sess_1", Messages: history},
			want:  true,
		},
		{
			// 前缀能对上说明这是正常增量，必须让既有的裁剪/合并路径继续生效。
			name:  "完整历史加新增输入仍按前缀处理",
			input: `[{"type":"message","role":"developer","content":"sys"},{"type":"message","role":"user","content":"u1"},{"type":"message","role":"assistant","content":"a1"},{"type":"message","role":"user","content":"u2"},{"type":"message","role":"assistant","content":"a2"},{"type":"message","role":"user","content":"u3"},{"type":"message","role":"assistant","content":"a3"},{"type":"message","role":"user","content":"u4"}]`,
			sess:  &types.Session{ID: "sess_1", Messages: history},
			want:  false,
		},
		{
			name:  "只有一条新 user 输入",
			input: `[{"type":"message","role":"user","content":"u4"}]`,
			sess:  &types.Session{ID: "sess_1", Messages: history},
			want:  false,
		},
		{
			name:  "session 为 nil",
			input: `[{"type":"message","role":"assistant","content":"a2"},{"type":"message","role":"user","content":"u4"}]`,
			sess:  nil,
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var input interface{}
			if err := json.Unmarshal([]byte(tt.input), &input); err != nil {
				t.Fatalf("构造 input 失败: %v", err)
			}
			if got := ResponsesInputRerootsSession(tt.sess, input); got != tt.want {
				t.Errorf("ResponsesInputRerootsSession() = %v, 期望 %v", got, tt.want)
			}
		})
	}
}
