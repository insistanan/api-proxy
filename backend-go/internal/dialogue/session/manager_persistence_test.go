package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func TestPersistentCommitCompactionPreservesCompleteWindow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "responses.db")
	manager, err := NewPersistentSessionManager(dbPath, time.Hour, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := manager.GetOrCreateSessionForConversation("", "compact-window")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CommitTurn(sess.ID, []types.ResponsesItem{{Type: "message", Role: "user", Content: "旧历史"}}, 10, false, "old-response", "old-owner"); err != nil {
		t.Fatal(err)
	}
	window := []types.ResponsesItem{
		{Type: "message", Role: "user", Content: "compact 保留的用户消息"},
		{Type: "compaction", EncryptedContent: "opaque"},
		{Type: "message", Role: "user", Content: "新问"},
		{Type: "message", Role: "assistant", Content: "回答"},
	}
	if err := manager.CommitTurn(sess.ID, window, 20, false, "new-response", "new-owner"); err != nil {
		t.Fatal(err)
	}
	manager.Stop()
	restored, err := NewPersistentSessionManager(dbPath, time.Hour, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Stop()
	continued, err := restored.GetOrCreateSessionForConversation("new-response", "compact-window")
	if err != nil {
		t.Fatal(err)
	}
	if len(continued.Messages) != 4 || continued.Messages[0].Content != window[0].Content || continued.Messages[1].EncryptedContent != "opaque" {
		t.Fatalf("完整压缩窗口必须落盘并恢复: %+v", continued.Messages)
	}
	if _, found, err := restored.ResponseOwner("old-response"); err != nil || found {
		t.Fatalf("旧映射应原子失效: found=%v err=%v", found, err)
	}
}

func TestPersistentSessionManagerRestoresPreviousResponseChain(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversations.db")
	manager, err := NewPersistentSessionManager(dbPath, 7*24*time.Hour, 100, 100000)
	if err != nil {
		t.Fatalf("NewPersistentSessionManager() error = %v", err)
	}
	session, err := manager.GetOrCreateSessionForConversation("", "conv_123")
	if err != nil {
		t.Fatalf("GetOrCreateSession() error = %v", err)
	}
	if err := manager.CommitTurn(session.ID, []types.ResponsesItem{{Type: "message", Role: "user", Content: "hello"}}, 12, false, "resp_123", "fixture-owner"); err != nil {
		t.Fatalf("CommitTurn() error = %v", err)
	}
	manager.Stop()

	restored, err := NewPersistentSessionManager(dbPath, 7*24*time.Hour, 100, 100000)
	if err != nil {
		t.Fatalf("restore NewPersistentSessionManager() error = %v", err)
	}
	defer restored.Stop()
	continued, err := restored.GetOrCreateSessionForConversation("resp_123", "conv_123")
	if err != nil {
		t.Fatalf("restored GetOrCreateSession() error = %v", err)
	}
	if continued.ID != session.ID {
		t.Fatalf("session ID = %q, want %q", continued.ID, session.ID)
	}
	if len(continued.Messages) != 1 || continued.TotalTokens != 12 {
		t.Fatalf("restored session = %#v", continued)
	}
	if continued.ConversationID != "conv_123" {
		t.Fatalf("ConversationID = %q", continued.ConversationID)
	}
	if owner, known, err := restored.ResponseOwner("resp_123"); err != nil || !known || owner != "fixture-owner" {
		t.Fatalf("重启后凭据归属 = (%q, %v, %v)", owner, known, err)
	}
}

func TestPersistentSessionManagerDeletesConversationChain(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversations.db")
	manager, err := NewPersistentSessionManager(dbPath, 7*24*time.Hour, 100, 100000)
	if err != nil {
		t.Fatalf("NewPersistentSessionManager() error = %v", err)
	}
	sess, err := manager.GetOrCreateSessionForConversation("", "conv_delete")
	if err != nil {
		t.Fatalf("GetOrCreateSessionForConversation() error = %v", err)
	}
	if err := manager.RecordResponseMapping("resp_delete", sess.ID); err != nil {
		t.Fatalf("RecordResponseMapping() error = %v", err)
	}
	if err := manager.DeleteConversation("conv_delete"); err != nil {
		t.Fatalf("DeleteConversation() error = %v", err)
	}
	if _, err := manager.GetSessionByResponseID("resp_delete"); err == nil {
		t.Fatal("deleted response mapping is still available")
	}
	manager.Stop()

	restored, err := NewPersistentSessionManager(dbPath, 7*24*time.Hour, 100, 100000)
	if err != nil {
		t.Fatalf("restore NewPersistentSessionManager() error = %v", err)
	}
	defer restored.Stop()
	if _, err := restored.GetSessionByResponseID("resp_delete"); err == nil {
		t.Fatal("deleted response mapping was restored")
	}
}

func TestPersistentSessionManagerRecoversUnknownPreviousResponseID(t *testing.T) {
	manager, err := NewPersistentSessionManager(filepath.Join(t.TempDir(), "conversations.db"), 7*24*time.Hour, 100, 100000)
	if err != nil {
		t.Fatalf("NewPersistentSessionManager() error = %v", err)
	}
	defer manager.Stop()

	recovered, err := manager.GetOrCreateSession("resp_from_old_version")
	if err != nil {
		t.Fatalf("GetOrCreateSession() error = %v", err)
	}
	if recovered.LastResponseID != "resp_from_old_version" {
		t.Fatalf("LastResponseID = %q", recovered.LastResponseID)
	}
	if _, known, err := manager.ResponseOwner("resp_from_old_version"); err != nil || known {
		t.Fatalf("旧记录不能被当成已知凭据归属: known=%v err=%v", known, err)
	}
	if mapped, err := manager.GetSessionByResponseID("resp_from_old_version"); err != nil || mapped.ID != recovered.ID {
		t.Fatalf("recovered mapping = (%#v, %v)", mapped, err)
	}
}

func TestPersistentSessionManagerDeleteAll(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversations.db")
	manager, err := NewPersistentSessionManager(dbPath, 7*24*time.Hour, 100, 100000)
	if err != nil {
		t.Fatalf("NewPersistentSessionManager() error = %v", err)
	}
	for _, conversationID := range []string{"conv_a", "conv_b"} {
		sess, err := manager.GetOrCreateSessionForConversation("", conversationID)
		if err != nil {
			t.Fatalf("GetOrCreateSessionForConversation() error = %v", err)
		}
		if err := manager.RecordResponseMapping("resp_"+conversationID, sess.ID); err != nil {
			t.Fatalf("RecordResponseMapping() error = %v", err)
		}
	}
	if err := manager.DeleteAll(); err != nil {
		t.Fatalf("DeleteAll() error = %v", err)
	}
	if len(manager.sessions) != 0 || len(manager.responseMapping) != 0 {
		t.Fatalf("in-memory state after DeleteAll(): sessions=%d mappings=%d", len(manager.sessions), len(manager.responseMapping))
	}
	manager.Stop()

	restored, err := NewPersistentSessionManager(dbPath, 7*24*time.Hour, 100, 100000)
	if err != nil {
		t.Fatalf("restore NewPersistentSessionManager() error = %v", err)
	}
	defer restored.Stop()
	for _, responseID := range []string{"resp_conv_a", "resp_conv_b"} {
		if _, err := restored.GetSessionByResponseID(responseID); err == nil {
			t.Fatalf("deleted response mapping %q was restored", responseID)
		}
	}
}

func TestSessionCleanupWithZeroMessageAndTokenLimitsKeepsActiveConversation(t *testing.T) {
	manager := newSessionManager(7*24*time.Hour, 0, 0, nil)
	now := time.Now()
	messages := make([]types.ResponsesItem, 150)
	manager.sessions["sess_long"] = &Session{
		ID:           "sess_long",
		Messages:     messages,
		CreatedAt:    now,
		LastAccessAt: now,
		TotalTokens:  200000,
	}

	manager.cleanup()
	if _, ok := manager.sessions["sess_long"]; !ok {
		t.Fatal("active long conversation was removed even though count limits are disabled")
	}
}
