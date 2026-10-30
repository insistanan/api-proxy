package modelcatalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
)

func TestPublicModelEntriesFamilyOnly(t *testing.T) {
	t.Parallel()

	got := publicModelEntries(nil)
	wantIDs := []string{"opus", "sonnet", "gpt", "gemini", "chat"}
	if len(got) != len(wantIDs) {
		t.Fatalf("len=%d want %d; got=%v", len(got), len(wantIDs), modelIDs(got))
	}
	for index, want := range wantIDs {
		if got[index].ID != want {
			t.Fatalf("index %d = %q, want %q", index, got[index].ID, want)
		}
		if got[index].OwnedBy != "api-proxy" {
			t.Fatalf("owned_by for %s = %q", got[index].ID, got[index].OwnedBy)
		}
	}
}

func TestOpenAIModelsIncludesPoolMatchers(t *testing.T) {
	t.Parallel()

	cfgManager := newTestConfigManager(t, config.Config{
		MessagePools: []config.ChannelPool{
			{ID: "deepseek", Name: "DeepSeek", ModelMatcher: "deepseek", Priority: 1},
			{ID: config.DefaultChannelPoolID, Name: "默认子池", ModelMatcher: "*", Priority: 2},
		},
		ResponsesPools: []config.ChannelPool{
			{ID: "grok", Name: "Grok", ModelMatcher: "grok", Priority: 1},
			{ID: config.DefaultChannelPoolID, Name: "默认子池", ModelMatcher: "*", Priority: 2},
		},
		ChatPools: []config.ChannelPool{
			// same matcher as messages should still appear once
			{ID: "deepseek-chat", Name: "DeepSeek Chat", ModelMatcher: "deepseek", Priority: 1},
			{ID: config.DefaultChannelPoolID, Name: "默认子池", ModelMatcher: "*", Priority: 2},
		},
	})

	response := OpenAIModels(context.Background(), cfgManager)
	ids := modelIDs(response.Data)

	for _, family := range []string{"opus", "sonnet", "gpt", "gemini", "chat"} {
		if !containsString(ids, family) {
			t.Fatalf("missing family model %q in %v", family, ids)
		}
	}
	if !containsString(ids, "deepseek") {
		t.Fatalf("missing pool matcher deepseek in %v", ids)
	}
	if !containsString(ids, "grok") {
		t.Fatalf("missing pool matcher grok in %v", ids)
	}
	if containsString(ids, "*") || containsString(ids, "default") {
		t.Fatalf("should not expose default/wildcard pool as model id: %v", ids)
	}

	// no channel-style aliases
	for _, id := range ids {
		if looksLikeChatRouteAlias(id) {
			t.Fatalf("public list should not include chat route alias %q", id)
		}
	}

	deepseek, ok := ModelDetail(context.Background(), cfgManager, "deepseek")
	if !ok {
		t.Fatal("ModelDetail(deepseek) missing")
	}
	if deepseek.OwnedBy != "pool:chat,messages" {
		t.Fatalf("deepseek owned_by = %q, want pool:chat,messages", deepseek.OwnedBy)
	}
}

func TestOpenAIModelsPreservesFamilyPoolKinds(t *testing.T) {
	t.Parallel()

	// 覆盖截图中的五个分组及全部固定别名，确保同名去重不会丢失协议归属。
	matchers := []string{"opus", "sonnet", "gpt", "gemini", "chat", "claude", "deepseek", "glm", "grok", "kimi"}
	pools := make([]config.ChannelPool, 0, len(matchers))
	for index, matcher := range matchers {
		pools = append(pools, config.ChannelPool{ID: matcher, Name: matcher, ModelMatcher: matcher, Priority: index + 1})
	}
	cfgManager := newTestConfigManager(t, config.Config{
		MessagePools:   pools,
		ResponsesPools: []config.ChannelPool{{ID: "gpt", Name: "GPT", ModelMatcher: "gpt"}},
		ChatPools:      []config.ChannelPool{{ID: "sonnet", Name: "Sonnet", ModelMatcher: " SONNET "}},
		GeminiPools:    []config.ChannelPool{{ID: "gemini", Name: "Gemini", ModelMatcher: "gemini"}},
		ImagesPools:    []config.ChannelPool{{ID: "gpt", Name: "GPT", ModelMatcher: "gpt"}},
	})

	response := OpenAIModels(context.Background(), cfgManager)
	if len(response.Data) != len(matchers) {
		t.Fatalf("models = %v, want %v", modelIDs(response.Data), matchers)
	}
	wantKinds := map[string]string{
		"opus":     "pool:messages",
		"sonnet":   "pool:chat,messages",
		"gpt":      "pool:images,messages,responses",
		"gemini":   "pool:gemini,messages",
		"chat":     "pool:messages",
		"claude":   "pool:messages",
		"deepseek": "pool:messages",
		"glm":      "pool:messages",
		"grok":     "pool:messages",
		"kimi":     "pool:messages",
	}
	seen := make(map[string]bool)
	for _, model := range response.Data {
		if seen[model.ID] {
			t.Fatalf("duplicate model %q", model.ID)
		}
		seen[model.ID] = true
		if want, ok := wantKinds[model.ID]; !ok || model.OwnedBy != want {
			t.Fatalf("model %q owned_by = %q, want %q", model.ID, model.OwnedBy, want)
		}
		if detail, ok := ModelDetail(context.Background(), cfgManager, model.ID); !ok || detail != model {
			t.Fatalf("ModelDetail(%q) = %+v, %v; want %+v", model.ID, detail, ok, model)
		}
	}
}

func newTestConfigManager(t *testing.T, cfg config.Config) *config.ConfigManager {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	manager, err := config.NewConfigManager(path)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	t.Cleanup(func() {
		_ = manager.Close()
	})
	return manager
}

func modelIDs(models []ModelEntry) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
