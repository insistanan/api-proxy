package utils

import "testing"

// TestParseSSEDataLine 固定 SSE data 行判定的行为契约：
// 收敛前散落各处的严格 "data: " 与宽松 "data:" 两派判定规则在此统一锁定。
func TestParseSSEDataLine(t *testing.T) {
	cases := []struct {
		name        string
		line        string
		wantPayload string
		wantIsData  bool
	}{
		{"带空格的规范写法", "data: {\"type\":\"ping\"}", `{"type":"ping"}`, true},
		{"不带空格同样是 data 行", "data:{\"type\":\"ping\"}", `{"type":"ping"}`, true},
		{"多个前导空格一并清理", "data:   {\"a\":1}", `{"a":1}`, true},
		{"尾部空白一并清理", "data: {\"a\":1}  ", `{"a":1}`, true},
		{"终止标记带空格", "data: [DONE]", SSEDoneMarker, true},
		{"终止标记不带空格", "data:[DONE]", SSEDoneMarker, true},
		{"终止标记带尾空白", "data: [DONE] ", SSEDoneMarker, true},
		{"空载荷仍是 data 行", "data:", "", true},
		{"仅空格的载荷仍是 data 行", "data: ", "", true},
		{"event 行不是 data 行", "event: response.completed", "", false},
		{"id 行不是 data 行", "id: 42", "", false},
		{"注释行不是 data 行", ": keep-alive", "", false},
		{"空行不是 data 行", "", "", false},
		{"行首缩进不视为 data 行", " data: {}", "", false},
		{"字段名前缀相近不误判", "database: x", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, isData := ParseSSEDataLine(tc.line)
			if isData != tc.wantIsData {
				t.Fatalf("isData = %v, want %v", isData, tc.wantIsData)
			}
			if payload != tc.wantPayload {
				t.Fatalf("payload = %q, want %q", payload, tc.wantPayload)
			}

			gotBytes, isDataBytes := ParseSSEDataLineBytes([]byte(tc.line))
			if isDataBytes != tc.wantIsData {
				t.Fatalf("bytes 版 isData = %v, want %v", isDataBytes, tc.wantIsData)
			}
			if string(gotBytes) != tc.wantPayload {
				t.Fatalf("bytes 版 payload = %q, want %q", gotBytes, tc.wantPayload)
			}
		})
	}
}

// TestSSEDataJSON 锁定 JSON 载荷过滤层：终止标记与空载荷一律视为无 JSON 可处理。
func TestSSEDataJSON(t *testing.T) {
	cases := []struct {
		name        string
		line        string
		wantPayload string
		wantOK      bool
	}{
		{"正常 JSON 载荷", "data: {\"a\":1}", `{"a":1}`, true},
		{"不带空格的 JSON 载荷", "data:{\"a\":1}", `{"a":1}`, true},
		{"非 JSON 文本也原样返回", "data: hello", "hello", true},
		{"终止标记被滤掉", "data: [DONE]", "", false},
		{"空载荷被滤掉", "data:", "", false},
		{"仅空格载荷被滤掉", "data:  ", "", false},
		{"非 data 行被滤掉", "event: ping", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, ok := SSEDataJSON(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if payload != tc.wantPayload {
				t.Fatalf("payload = %q, want %q", payload, tc.wantPayload)
			}
		})
	}
}

// TestRewriteSSEDataLinesFidelity 锁定重建保真契约：
// 无任何行被改写时，返回值必须与入参逐字节相同——尤其不能给以 "\n\n"
// 结尾的事件多补一个换行（收敛前各调用点逐行 WriteString("\n") 的老毛病）。
func TestRewriteSSEDataLinesFidelity(t *testing.T) {
	keepAll := func(string) (string, bool) { return "", false }

	cases := []struct {
		name  string
		event string
	}{
		{"标准事件带空行结尾", "event: message_delta\ndata: {\"a\":1}\n\n"},
		{"无空格 data 行", "event: message_delta\ndata:{\"a\":1}\n\n"},
		{"无尾换行", "data: {\"a\":1}"},
		{"单个尾换行", "data: {\"a\":1}\n"},
		{"多个尾空行", "data: {\"a\":1}\n\n\n\n"},
		{"多条 data 行", "event: x\ndata: {\"a\":1}\ndata: {\"b\":2}\n\n"},
		{"无 data 行", "event: ping\nid: 42\n\n"},
		{"空事件", ""},
		{"仅换行", "\n"},
		{"[DONE] 事件", "data: [DONE]\n\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := RewriteSSEDataLines(tc.event, keepAll)
			if changed {
				t.Fatal("changed = true, want false（回调从未改写）")
			}
			if got != tc.event {
				t.Fatalf("重建结果与原事件不一致:\n got  %q\n want %q", got, tc.event)
			}
		})
	}
}

// TestRewriteSSEDataLinesRewrites 断言改写路径：只替换 data 行载荷、
// 统一写规范前缀，其余行（含非 data 行与尾部空行）结构不变。
func TestRewriteSSEDataLinesRewrites(t *testing.T) {
	t.Run("改写单条 data 行", func(t *testing.T) {
		event := "event: message_delta\ndata:{\"a\":1}\n\n"
		got, changed := RewriteSSEDataLines(event, func(payload string) (string, bool) {
			if payload != `{"a":1}` {
				t.Fatalf("payload = %q, want %q", payload, `{"a":1}`)
			}
			return `{"a":2}`, true
		})
		if !changed {
			t.Fatal("changed = false, want true")
		}
		want := "event: message_delta\ndata: {\"a\":2}\n\n"
		if got != want {
			t.Fatalf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("只改写首条 data 行时另一条原样保留", func(t *testing.T) {
		event := "data:{\"a\":1}\ndata: {\"b\":2}\n\n"
		first := true
		got, changed := RewriteSSEDataLines(event, func(string) (string, bool) {
			if !first {
				return "", false
			}
			first = false
			return `{"a":9}`, true
		})
		if !changed {
			t.Fatal("changed = false, want true")
		}
		// 首行前缀被规范化，次行保留上游原始写法
		want := "data: {\"a\":9}\ndata: {\"b\":2}\n\n"
		if got != want {
			t.Fatalf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("回调对 DONE 载荷同样被调用", func(t *testing.T) {
		var seen []string
		RewriteSSEDataLines("data: [DONE]\n\n", func(payload string) (string, bool) {
			seen = append(seen, payload)
			return "", false
		})
		if len(seen) != 1 || seen[0] != SSEDoneMarker {
			t.Fatalf("回调收到的载荷 = %#v, want [%q]", seen, SSEDoneMarker)
		}
	})
}
