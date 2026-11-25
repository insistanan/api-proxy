package proxycore

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/dialogue/conversation"
)

func TestConversationBranchesUseStableClientIdentity(t *testing.T) {
	for _, test := range []struct {
		name    string
		headers map[string]string
		body    string
		thread  string
		branch  string
		agent   string
	}{
		{name: "通用线程请求头", headers: map[string]string{"Thread-Id": "thread-a"}, body: `{}`, thread: "thread-a"},
		{name: "通用 X 请求头", headers: map[string]string{"X-Thread-Id": "thread-a", "X-Branch-Id": "branch-a", "X-Agent-Id": "agent-a"}, body: `{}`, thread: "thread-a", branch: "branch-a", agent: "agent-a"},
		{name: "下划线请求头", headers: map[string]string{"Thread_id": "thread-a", "Branch_id": "branch-a", "Agent_id": "agent-a"}, body: `{}`, thread: "thread-a", branch: "branch-a", agent: "agent-a"},
		{name: "根字段 snake_case", body: `{"thread_id":"thread-a","branch_id":"branch-a","agent_id":"agent-a"}`, thread: "thread-a", branch: "branch-a", agent: "agent-a"},
		{name: "metadata camelCase", body: `{"metadata":{"threadId":"thread-a","branchId":"branch-a","agentId":"agent-a"}}`, thread: "thread-a", branch: "branch-a", agent: "agent-a"},
		{name: "结构化 user_id", body: `{"metadata":{"user_id":"{\"thread_id\":\"thread-a\",\"branch_id\":\"branch-a\",\"agent_id\":\"agent-a\"}"}}`, thread: "thread-a", branch: "branch-a", agent: "agent-a"},
		{name: "Cursor agent", headers: map[string]string{"X-Cursor-Agent-Id": "agent-a"}, body: `{}`, agent: "agent-a"},
		{name: "Codex 根会话不遮蔽线程元数据", headers: map[string]string{"X-Codex-Turn-Metadata": `{"thread_id":"side","session_id":"side","forked_from_thread_id":"root","turn_id":"turn-a"}`}, body: `{}`, thread: "side"},
		{name: "请求头优先于元数据", headers: map[string]string{"Thread-Id": "header", "X-Codex-Turn-Metadata": `{"thread_id":"metadata"}`}, body: `{"thread_id":"body"}`, thread: "header"},
		{name: "临时身份和父节点不能构成分支", headers: map[string]string{"X-Client-Request-Id": "request-a", "X-Codex-Turn-Metadata": `{"turn_id":"turn-a","root_turn_id":"root-turn","parent_thread_id":"parent","forked_from_thread_id":"parent","agent_name":"/root"}`}, body: `{"previous_response_id":"response-a","prompt_cache_key":"cache-a","parent_thread_id":"parent","parent_agent_id":"parent-agent","run_id":"run-a"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			ctx.Request.Header.Set("Session-Id", "root")
			for name, value := range test.headers {
				ctx.Request.Header.Set(name, value)
			}
			identity := ResolveConversationIdentity(ctx, []byte(test.body))
			// Cursor 仍沿用既有的 session 匹配范围规则。
			if identity.ClientFamily != "cursor" && (identity.ExplicitID != "root" || identity.Source != "session_header") {
				t.Fatalf("分支身份改变了已有会话归组: %+v", identity)
			}
			if identity.ThreadID != test.thread || identity.BranchID != test.branch || identity.AgentID != test.agent {
				t.Fatalf("分支身份提取错误: %+v", identity)
			}
			wantKey := conversationBranchKey(conversation.Identity{ThreadID: test.thread, BranchID: test.branch, AgentID: test.agent})
			if conversationBranchKey(identity) != wantKey {
				t.Fatal("分支键包含了无关请求字段")
			}
		})
	}
}

func TestConversationBranchKeysDoNotCollideAcrossDimensions(t *testing.T) {
	seen := make(map[string]bool)
	for _, identity := range []conversation.Identity{
		{ThreadID: "same"}, {BranchID: "same"}, {AgentID: "same"},
		{ThreadID: "a:b", BranchID: "c"}, {ThreadID: "a", BranchID: "b:c"},
	} {
		key := conversationBranchKey(identity)
		if key == "" || seen[key] {
			t.Fatalf("不同分支身份发生碰撞: %+v", identity)
		}
		seen[key] = true
	}
}

func TestGenericThreadHeaderProvidesIdentityWithoutSession(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	ctx.Request.Header.Set("Thread-Id", "standalone-thread")
	identity := ResolveConversationIdentity(ctx, []byte(`{}`))
	if identity.ExplicitID != "standalone-thread" || identity.Source != "thread_header" {
		t.Fatalf("单独线程标识未形成稳定会话: %+v", identity)
	}
}

func TestExtractConversationID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		header string
		body   string
		want   string
	}{
		{
			name: "structured Claude Code metadata user ID",
			body: `{"metadata":{"user_id":"{\"session_id\":\"claude-session\"}"}}`,
			want: "claude-session",
		},
		{
			name: "ordinary user ID is not a conversation",
			body: `{"metadata":{"user_id":"user-123"}}`,
			want: "",
		},
		{
			name:   "Codex turn metadata thread ID",
			header: `{"session_id":"installation-123","thread_id":"thread-123"}`,
			body:   `{}`,
			want:   "thread-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			request := httptest.NewRequest("POST", "/v1/responses", nil)
			if tt.header != "" {
				request.Header.Set("X-Codex-Turn-Metadata", tt.header)
			}
			ctx.Request = request
			if got := ExtractConversationID(ctx, []byte(tt.body)); got != tt.want {
				t.Fatalf("ExtractConversationID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveConversationIdentitySeparatesCursorRootAndAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	request := httptest.NewRequest("POST", "/v1/messages", nil)
	request.Header.Set("User-Agent", "cursor/1.0")
	request.Header.Set("X-Cursor-Composer-Id", "composer-1")
	request.Header.Set("X-Cursor-Agent-Id", "agent-2")
	request.Header.Set("X-Cursor-Parent-Agent-Id", "agent-1")
	ctx.Request = request

	identity := ResolveConversationIdentity(ctx, []byte(`{"messages":[],"prompt_cache_key":"cache-scope"}`))
	if identity.ClientFamily != "cursor" || identity.ExplicitID != "composer-1" || identity.AgentID != "agent-2" || identity.ParentAgentID != "agent-1" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
	if identity.ScopeID != "cache-scope" {
		t.Fatalf("scope ID = %q", identity.ScopeID)
	}
}

func TestPromptCacheKeyIsScopeNotExplicitConversation(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	identity := ResolveConversationIdentity(ctx, []byte(`{"prompt_cache_key":"shared-cache","messages":[]}`))
	if identity.ExplicitID != "" {
		t.Fatalf("prompt cache key became explicit conversation ID: %#v", identity)
	}
	if identity.ScopeID != "shared-cache" {
		t.Fatalf("scope ID = %q", identity.ScopeID)
	}
}

func TestCursorSessionIDIsOnlyAMatchingScope(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	request := httptest.NewRequest("POST", "/v1/messages", nil)
	request.Header.Set("User-Agent", "cursor/1.0")
	request.Header.Set("Session-Id", "cursor-window-session")
	ctx.Request = request
	identity := ResolveConversationIdentity(ctx, []byte(`{"messages":[]}`))
	if identity.ExplicitID != "" {
		t.Fatalf("Cursor session ID became an explicit conversation: %#v", identity)
	}
	if identity.ScopeID != "cursor-window-session" {
		t.Fatalf("scope ID = %q", identity.ScopeID)
	}
}

func TestCursorMasqueradingAsClaudeCodeDoesNotMergeBySessionHeader(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	request := httptest.NewRequest("POST", "/v1/messages", nil)
	request.Header.Set("X-Claude-Code-Session-Id", "shared-editor-session")
	ctx.Request = request
	identity := ResolveConversationIdentity(ctx, []byte(`{"messages":[{"role":"user","content":"<user_info>cursor</user_info><user_query>hello</user_query>"}]}`))
	if identity.ClientFamily != "cursor" || identity.ExplicitID != "" || identity.ScopeID != "shared-editor-session" {
		t.Fatalf("unexpected masquerading Cursor identity: %#v", identity)
	}
}

func TestBuildConversationTranscriptProducesStableStrictPrefixes(t *testing.T) {
	first := BuildConversationTranscript("messages", []byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	continued := BuildConversationTranscript("messages", []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"continue"}]}`))
	if first.Depth() != 1 || continued.Depth() != 3 {
		t.Fatalf("unexpected transcript depths: %d and %d", first.Depth(), continued.Depth())
	}
	if first.FrontierHash() != continued.PrefixHashes[0] {
		t.Fatal("continued request does not preserve the original transcript prefix")
	}
}

func TestBuildConversationTranscriptIgnoresThinkingSignatures(t *testing.T) {
	left := BuildConversationTranscript("messages", []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"secret","signature":"a"},{"type":"text","text":"answer"}]}]}`))
	right := BuildConversationTranscript("messages", []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"changed","signature":"b"},{"type":"text","text":"answer"}]}]}`))
	if left.FrontierHash() == "" || left.FrontierHash() != right.FrontierHash() {
		t.Fatal("thinking-only changes affected transcript identity")
	}
}

func TestBuildConversationTranscriptPreservesUserSuppliedTranscriptMarkup(t *testing.T) {
	first := BuildConversationTranscript("messages", []byte(`{"messages":[{"role":"user","content":"<agent_transcripts>old internal transcript</agent_transcripts>"},{"role":"user","content":"review this code"}]}`))
	continued := BuildConversationTranscript("messages", []byte(`{"messages":[{"role":"user","content":"<agent_transcripts>new internal transcript</agent_transcripts>"},{"role":"user","content":"review this code"},{"role":"assistant","content":"done"},{"role":"user","content":"continue"}]}`))
	if first.Depth() != 2 || continued.Depth() != 4 {
		t.Fatalf("unexpected transcript depths: %d and %d", first.Depth(), continued.Depth())
	}
	if first.FrontierHash() == continued.PrefixHashes[1] {
		t.Fatal("different user text must not share a transcript identity")
	}
}
