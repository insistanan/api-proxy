package conversation

import (
	"path/filepath"
	"testing"
)

func TestBuildIdentityKey(t *testing.T) {
	tests := []struct {
		name     string
		obs      Observation
		expected string
		sameAs   string // 用于测试两个 observation 是否生成相同 key
	}{
		{
			name: "with explicit conversation ID",
			obs: Observation{
				APIKind:        "messages",
				ConversationID: "conv_123",
				Model:          "claude-opus",
				FirstPrompt:    "Hello",
			},
			expected: "messages|conv_123",
		},
		{
			name: "without ID or fallback - should generate unique ID",
			obs: Observation{
				APIKind:     "messages",
				Model:       "claude-opus",
				FirstPrompt: "Test",
			},
			// 不验证具体值，因为包含随机部分
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildIdentityKey(tt.obs)

			if tt.expected != "" {
				if result != tt.expected {
					t.Errorf("buildIdentityKey() = %v, want %v", result, tt.expected)
				}
			}

			if tt.sameAs != "" && result != tt.sameAs {
				t.Errorf("buildIdentityKey() = %v, want %v", result, tt.sameAs)
			}

			// 确保生成的 key 不为空
			if result == "" {
				t.Error("buildIdentityKey() returned empty string")
			}
		})
	}
}

func TestBuildIdentityKey_ExplicitIDTakesPrecedence(t *testing.T) {
	obs := Observation{
		APIKind:        "messages",
		ConversationID: "explicit_conv_id",
		Model:          "claude-opus",
		FirstPrompt:    "Test",
	}

	result := buildIdentityKey(obs)
	expected := "messages|explicit_conv_id"

	if result != expected {
		t.Errorf("Explicit conversation ID should take precedence, got %v want %v", result, expected)
	}

}

func TestBuildIdentityKey_WithoutExplicitIDDoesNotMerge(t *testing.T) {
	obs := Observation{APIKind: "messages", Model: "claude-opus", FirstPrompt: "What is Go?"}
	if first, second := buildIdentityKey(obs), buildIdentityKey(obs); first == second {
		t.Fatalf("requests without an explicit conversation ID must not be merged: %q", first)
	}
}

func TestResolveExistingRecordIDDoesNotCreateRecord(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()

	identity := Identity{ExplicitID: "response-thread-1", Source: "previous_response"}
	if got := registry.ResolveExistingRecordID("responses", identity); got != "" {
		t.Fatalf("missing identity resolved to %q", got)
	}
	if got := len(registry.List()); got != 0 {
		t.Fatalf("lookup created %d records", got)
	}

	record := registry.ObserveRequest(Observation{
		APIKind:  "responses",
		Identity: identity,
	})
	registry.MarkComplete(record.ID, "responses")
	if got := registry.ResolveExistingRecordID("responses", identity); got != record.ID {
		t.Fatalf("resolved record ID = %q, want %q", got, record.ID)
	}
}

func TestRegistryContinuesUniqueCursorHistory(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()

	first := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"},
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"第一轮"},
	})
	registry.MarkComplete(first.ID, "messages")

	continued := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"},
		Transcript: Transcript{PrefixHashes: []string{"h1", "h2", "h3"}},
		Prompts:    []string{"第二轮"},
	})
	if continued.ID != first.ID {
		t.Fatalf("strict history extension created a new conversation: got %s want %s", continued.ID, first.ID)
	}
	if continued.IdentitySource != "unique_history_extension" {
		t.Fatalf("identity source = %q", continued.IdentitySource)
	}
}

func TestRegistryDoesNotMergeIdenticalRequestsOrAmbiguousHistory(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", LaneHash: "lane-main"}

	first := registry.ObserveRequest(Observation{APIKind: "messages", Identity: identity, Transcript: Transcript{PrefixHashes: []string{"h1"}}})
	registry.MarkComplete(first.ID, "messages")
	duplicate := registry.ObserveRequest(Observation{APIKind: "messages", Identity: identity, Transcript: Transcript{PrefixHashes: []string{"h1"}}})
	registry.MarkComplete(duplicate.ID, "messages")
	if duplicate.ID == first.ID {
		t.Fatal("identical requests without an explicit ID were merged")
	}

	ambiguous := registry.ObserveRequest(Observation{APIKind: "messages", Identity: identity, Transcript: Transcript{PrefixHashes: []string{"h1", "h2"}}})
	if ambiguous.ID == first.ID || ambiguous.ID == duplicate.ID {
		t.Fatal("ambiguous history was merged into an existing conversation")
	}
	if ambiguous.IdentitySource != "ambiguous_history" {
		t.Fatalf("identity source = %q", ambiguous.IdentitySource)
	}
}

func TestRegistrySeparatesCursorAgentLanesWithSameRootID(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()

	mainConversation := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   Identity{ClientFamily: "cursor", ExplicitID: "composer-1", LaneHash: "lane-main", Source: "request_conversation"},
		Transcript: Transcript{PrefixHashes: []string{"main-h1"}},
	})
	registry.MarkComplete(mainConversation.ID, "messages")

	subagent := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   Identity{ClientFamily: "cursor", ExplicitID: "composer-1", LaneHash: "lane-sub", Source: "request_conversation"},
		Transcript: Transcript{PrefixHashes: []string{"sub-h1"}},
	})
	registry.MarkComplete(subagent.ID, "messages")
	if subagent.ID == mainConversation.ID {
		t.Fatal("different Cursor agent lanes were merged")
	}
	if subagent.ParentConversationID != mainConversation.ID {
		t.Fatalf("parent conversation = %q, want %q", subagent.ParentConversationID, mainConversation.ID)
	}

	continuedSubagent := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   Identity{ClientFamily: "cursor", ExplicitID: "composer-1", LaneHash: "lane-sub", Source: "request_conversation"},
		Transcript: Transcript{PrefixHashes: []string{"sub-h1", "sub-h2"}},
	})
	if continuedSubagent.ID != subagent.ID {
		t.Fatalf("subagent lane did not remain stable: got %s want %s", continuedSubagent.ID, subagent.ID)
	}
}

func TestPersistentRegistryRestoresCursorHistoryFrontier(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversations.db")
	registry, err := NewPersistentRegistry(dbPath)
	if err != nil {
		t.Fatalf("NewPersistentRegistry() error = %v", err)
	}
	first := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   Identity{ClientFamily: "cursor", LaneHash: "lane-main"},
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
	})
	registry.MarkComplete(first.ID, "messages")
	registry.Stop()

	restored, err := NewPersistentRegistry(dbPath)
	if err != nil {
		t.Fatalf("restore registry error = %v", err)
	}
	defer restored.Stop()
	continued := restored.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   Identity{ClientFamily: "cursor", LaneHash: "lane-main"},
		Transcript: Transcript{PrefixHashes: []string{"h1", "h2"}},
	})
	if continued.ID != first.ID {
		t.Fatalf("persisted frontier did not continue: got %s want %s", continued.ID, first.ID)
	}
}

func TestPersistentRegistryRestoresNameImageFingerprintsAndResponseAlias(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversations.db")
	registry, err := NewPersistentRegistry(dbPath)
	if err != nil {
		t.Fatalf("NewPersistentRegistry() error = %v", err)
	}

	created := registry.ObserveRequest(Observation{
		APIKind:           "responses",
		ConversationID:    "thread_123",
		Model:             "gpt-5",
		Prompts:           []string{"请分析这张图片"},
		ImageFingerprints: []string{"sha256:image-a"},
	})
	if created == nil {
		t.Fatal("ObserveRequest() returned nil")
	}
	if _, err := registry.SetName(created.ID, "图片分析"); err != nil {
		t.Fatalf("SetName() error = %v", err)
	}
	if err := registry.AssociateExternalID(created.ID, "responses", "resp_123"); err != nil {
		t.Fatalf("AssociateExternalID() error = %v", err)
	}
	if err := registry.SaveConversationImageUnderstanding(created.ID, "image-cache-key", "第一张图片的描述"); err != nil {
		t.Fatalf("SaveConversationImageUnderstanding() error = %v", err)
	}
	registry.Stop()
	legacyStore, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open legacy store error = %v", err)
	}
	if _, err := legacyStore.db.Exec(`UPDATE conversations
		SET route_override_json = '{"kind":"","channelIndex":0}', last_resolved_json = 'null'
		WHERE id = ?`, created.ID); err != nil {
		t.Fatalf("prepare legacy optional JSON error = %v", err)
	}
	if err := legacyStore.Close(); err != nil {
		t.Fatalf("close legacy store error = %v", err)
	}

	restored, err := NewPersistentRegistry(dbPath)
	if err != nil {
		t.Fatalf("restore NewPersistentRegistry() error = %v", err)
	}
	defer restored.Stop()

	continued := restored.ObserveRequest(Observation{
		APIKind:        "responses",
		ConversationID: "resp_123",
		Prompts:        []string{"继续"},
	})
	if continued.ID != created.ID {
		t.Fatalf("response alias should keep the same conversation: got %s want %s", continued.ID, created.ID)
	}
	if continued.Name != "图片分析" {
		t.Fatalf("name = %q, want %q", continued.Name, "图片分析")
	}
	if continued.RouteOverride != nil {
		t.Fatalf("RouteOverride = %#v, want nil", continued.RouteOverride)
	}
	if continued.LastResolved != nil {
		t.Fatalf("LastResolved = %#v, want nil", continued.LastResolved)
	}
	if len(continued.ImageFingerprints) != 1 || continued.ImageFingerprints[0] != "sha256:image-a" {
		t.Fatalf("image fingerprints = %#v", continued.ImageFingerprints)
	}
	if result, ok, err := restored.LoadConversationImageUnderstanding(created.ID, "image-cache-key"); err != nil || !ok || result != "第一张图片的描述" {
		t.Fatalf("LoadConversationImageUnderstanding() = (%q, %v, %v)", result, ok, err)
	}
	if err := restored.Delete(created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, ok := restored.Get(created.ID); ok {
		t.Fatal("deleted conversation is still available")
	}
	if err := restored.SaveConversationImageUnderstanding(created.ID, "image-cache-key", "不应写回"); err == nil {
		t.Fatal("saving image result after delete should fail")
	}
	var imageRows int
	if err := restored.store.db.QueryRow("SELECT COUNT(*) FROM conversation_image_understandings WHERE conversation_id = ?", created.ID).Scan(&imageRows); err != nil {
		t.Fatalf("count image rows error = %v", err)
	}
	if imageRows != 0 {
		t.Fatalf("image rows after delete = %d", imageRows)
	}
}

func TestPersistentRegistryDeleteAllClearsRecordsAliasesAndImageResults(t *testing.T) {
	registry, err := NewPersistentRegistry(filepath.Join(t.TempDir(), "conversations.db"))
	if err != nil {
		t.Fatalf("NewPersistentRegistry() error = %v", err)
	}
	defer registry.Stop()

	for index, externalID := range []string{"resp_a", "resp_b"} {
		record := registry.ObserveRequest(Observation{APIKind: "responses", ConversationID: externalID, FirstPrompt: externalID})
		if record == nil {
			t.Fatalf("record %d is nil", index)
		}
		if err := registry.AssociateExternalID(record.ID, "responses", externalID+"_alias"); err != nil {
			t.Fatalf("AssociateExternalID() error = %v", err)
		}
		if err := registry.SaveConversationImageUnderstanding(record.ID, "cache", "result"); err != nil {
			t.Fatalf("SaveConversationImageUnderstanding() error = %v", err)
		}
	}

	if err := registry.DeleteAll(); err != nil {
		t.Fatalf("DeleteAll() error = %v", err)
	}
	if got := len(registry.List()); got != 0 {
		t.Fatalf("records after DeleteAll() = %d", got)
	}
	for _, table := range []string{"conversations", "conversation_aliases", "conversation_lineages", "conversation_image_understandings"} {
		var count int
		if err := registry.store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s error = %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows after DeleteAll() = %d", table, count)
		}
	}
}
