package proxycore

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

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

func TestBuildConversationTranscriptIgnoresChangingCursorAgentTranscriptEnvelope(t *testing.T) {
	first := BuildConversationTranscript("messages", []byte(`{"messages":[{"role":"user","content":"<agent_transcripts>old internal transcript</agent_transcripts>"},{"role":"user","content":"review this code"}]}`))
	continued := BuildConversationTranscript("messages", []byte(`{"messages":[{"role":"user","content":"<agent_transcripts>new internal transcript</agent_transcripts>"},{"role":"user","content":"review this code"},{"role":"assistant","content":"done"},{"role":"user","content":"continue"}]}`))
	if first.Depth() != 1 || continued.Depth() != 3 {
		t.Fatalf("unexpected transcript depths: %d and %d", first.Depth(), continued.Depth())
	}
	if first.FrontierHash() != continued.PrefixHashes[0] {
		t.Fatal("changing agent transcript envelope broke Cursor history continuity")
	}
}
