package cachekeepalive

import "testing"

func TestParseResponse_RequiresTrustedUsageAndPreservesCreationBreakdown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		status   int
		creation int64
		invalid  bool
	}{
		{"原生分档", `{"type":"message","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":1000,"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":30}}}`, 200, 50, false},
		{"兼容分档", `{"type":"message","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":1000,"cache_creation_5m_input_tokens":20,"cache_creation_1h_input_tokens":30}}`, 200, 50, false},
		{"完整命中", `{"type":"message","stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":1000}}`, 200, 0, false},
		{"缺少输出", `{"type":"message","stop_reason":"end_turn","usage":{"input_tokens":0,"cache_read_input_tokens":1000}}`, 200, 0, true},
		{"缺少缓存读取", `{"type":"message","stop_reason":"end_turn","usage":{"input_tokens":1000,"output_tokens":1}}`, 200, 0, true},
		{"负用量", `{"type":"message","stop_reason":"end_turn","usage":{"input_tokens":-1,"output_tokens":1,"cache_read_input_tokens":1000}}`, 200, 0, true},
		{"缺少用量", `{"type":"message"}`, 200, 0, true},
		{"错误信封", `{"type":"error","error":{"type":"overloaded_error"}}`, 200, 0, true},
		{"上游拒绝", `{"type":"error","error":{"type":"rate_limit_error"}}`, 429, 0, true},
		{"截断 JSON", `{"usage":`, 200, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, err := parseResponse([]byte(tc.body), tc.status)
			if (err != nil) != tc.invalid {
				t.Fatalf("响应判定不符: %v", err)
			}
			if !tc.invalid {
				_, read, creation, _, err := usageNumbers(usage)
				if err != nil || read != 1000 || creation != tc.creation {
					t.Fatalf("丢失用量: read=%d creation=%d err=%v", read, creation, err)
				}
			}
		})
	}
}
