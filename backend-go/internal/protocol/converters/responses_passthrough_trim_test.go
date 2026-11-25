package converters

import (
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 在真实转换入口验证：角色、条数和重复正文都不是放弃旧链的声明。
func TestResponsesHistoryBoundaryRequiresProtocolEvidence(t *testing.T) {
	history := []types.ResponsesItem{
		{Type: "message", Role: "user", Content: "hi"},
		{Type: "message", Role: "assistant", Content: "hello"},
	}
	for _, tc := range []struct {
		name, input string
		history     []types.ResponsesItem
		chain       string
		count       int
	}{
		{"无本地历史的角色序列", `[{"role":"system","content":"规则"},{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]`, nil, "parent", 3},
		{"新系统消息也是增量", `[{"role":"system","content":"新增规则"}]`, history, "parent", 1},
		{"改写正文不猜换根", `[{"role":"assistant","content":"摘要"},{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]`, history, "parent", 3},
		{"完整前缀只裁重复", `[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"next"}]`, history, "parent", 1},
		{"工具结果重试保留", `[{"type":"function_call_output","call_id":"tool","output":"ok"}]`, history, "parent", 1},
		{"明确压缩根", `[{"type":"compaction","encrypted_content":"opaque"},{"role":"user","content":"next"}]`, history, "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"m","previous_response_id":"parent","input":` + tc.input + `}`)
			var req types.ResponsesRequest
			require.NoError(t, json.Unmarshal(body, &req))
			got, err := convertResponsesPassthroughRequestWithSession("", body, nil, &types.Session{Messages: tc.history}, &req)
			require.NoError(t, err)
			require.Equal(t, tc.chain, gjson.GetBytes(got, "previous_response_id").String())
			require.Len(t, gjson.GetBytes(got, "input").Array(), tc.count)
		})
	}
}
