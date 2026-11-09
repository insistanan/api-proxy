package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/types"
)

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
	if err := manager.AppendMessage(session.ID, types.ResponsesItem{Type: "message", Role: "user", Content: "hello"}, 12); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	if err := manager.UpdateLastResponseID(session.ID, "resp_123"); err != nil {
		t.Fatalf("UpdateLastResponseID() error = %v", err)
	}
	if err := manager.RecordResponseMapping("resp_123", session.ID); err != nil {
		t.Fatalf("RecordResponseMapping() error = %v", err)
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
