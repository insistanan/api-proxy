package logger

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestStructuredRequestPreservesTailAndOmitsMedia(t *testing.T) {
	messages := make([]any, 0, 40)
	for index := 0; index < 40; index++ {
		messages = append(messages, map[string]any{"role": "user", "content": fmt.Sprintf("消息%d", index)})
	}
	messages[39] = map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "新增输入 MASKED_SECRET_7K4M2P7QAB2CD_PRESERVE_EXACTLY"},
		map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": strings.Repeat("A", 2*MaxBodyBytes)}},
	}}
	raw, err := json.Marshal(map[string]any{"model": "test", "instructions": strings.Repeat("公共配置", 100000), "input": messages})
	require.NoError(t, err)
	original, _, truncated := PrepareBody(raw)
	require.True(t, truncated)
	require.Contains(t, original, "正文已省略")
	body, err := PrepareStructuredRequest(raw)
	require.NoError(t, err)
	require.LessOrEqual(t, len(body), MaxStructuredBodyBytes)
	require.True(t, utf8.ValidString(body))
	require.Contains(t, body, "新增输入")
	require.NotContains(t, body, "7K4M2P7QAB2CD")
	require.NotContains(t, body, strings.Repeat("A", 64))
	var snapshot struct {
		Input []any              `json:"input"`
		Meta  structuredBodyMeta `json:"_log"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &snapshot))
	require.Len(t, snapshot.Input, maxSnapshotMessages)
	require.Equal(t, 24, snapshot.Meta.MessageOffset)
	require.Equal(t, 40, snapshot.Meta.OriginalMessages)
	require.Len(t, snapshot.Meta.MessageHashes, maxSnapshotMessages)
}

func TestStructuredRequestKeepsOrdinaryDataAndControlsEscapedSize(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"messages": []any{
		map[string]any{"role": "tool", "content": map[string]any{"data": "业务结果"}},
		map[string]any{"role": "user", "content": strings.Repeat("\x01中文", MaxBodyBytes)},
	}})
	require.NoError(t, err)
	body, err := PrepareStructuredRequest(raw)
	require.NoError(t, err)
	require.LessOrEqual(t, len(body), MaxStructuredBodyBytes)
	require.Contains(t, body, "业务结果")
	require.True(t, json.Valid([]byte(body)))
	var snapshot struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &snapshot))
	require.Equal(t, "user", snapshot.Messages[1].Role, "长内容在前也不能耗尽角色字段的预算")
}

func TestStructuredRequestRejectsInvalidInput(t *testing.T) {
	for _, raw := range []string{"{", "[]", "{\"model\":\"test\"}"} {
		_, err := PrepareStructuredRequest([]byte(raw))
		require.Error(t, err)
	}
}
