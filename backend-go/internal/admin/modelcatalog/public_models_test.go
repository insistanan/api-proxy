package modelcatalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
)

func TestFreeCatalogUsesSharedListWithoutDiscovery(t *testing.T) {
	cm := newTestConfigManager(t, config.Config{})
	require.NoError(t, cm.EnsureFreeChannels())
	ids := modelIDs(OpenAIModels(context.Background(), cm).Data)
	require.Equal(t, []string{"free"}, ids)
	require.NoError(t, cm.SetKiloModels([]string{"vendor/a", "vendor/b", config.KiloAutoModelID}, ""))
	for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
		models, err := ModelsForKind(cm, kind)
		require.NoError(t, err)
		require.Equal(t, []string{"free"}, modelIDs(models.Data), "目录同步后仍只导入一个免费分组")
	}
	// 只有内置免费渠道时，旧发现链路应立即返回空结果，不访问其真实地址。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	models, routes, err := discoverChatModels(ctx, cm)
	require.NoError(t, err)
	require.Empty(t, models)
	require.Empty(t, routes)
	require.NoError(t, cm.SetKiloEnabled(false))
	require.Empty(t, OpenAIModels(context.Background(), cm).Data)
}

func TestPublicModelsEmptyWithoutConfiguredGroups(t *testing.T) {
	t.Parallel()

	for _, manager := range []*config.ConfigManager{nil, newTestConfigManager(t, config.Config{})} {
		response := OpenAIModels(context.Background(), manager)
		encoded, err := json.Marshal(response)
		require.NoError(t, err)
		require.JSONEq(t, `{"object":"list","data":[]}`, string(encoded))
		_, found := ModelDetail(context.Background(), manager, "opus")
		require.False(t, found)
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

	require.Equal(t, []string{"deepseek", "grok"}, ids)

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

func TestOpenAIModelsPreservesConfiguredPoolKinds(t *testing.T) {
	t.Parallel()

	// 名为 opus、sonnet 等的分组仍正常暴露，同名规则去重时保留所有协议归属。
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
	if cfg.Settings.Gateway == (config.GatewaySettings{}) {
		cfg.Settings.Gateway = config.DefaultGatewaySettings()
	}
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

func TestModelsForKindUsesIngressProtocolAndFreeEligibility(t *testing.T) {
	pools := func(kind string) []config.ChannelPool {
		return []config.ChannelPool{{ID: kind, Name: kind, ModelMatcher: kind}}
	}
	cm := newTestConfigManager(t, config.Config{
		MessagePools: pools("messages"), ResponsesPools: pools("responses"),
		ChatPools: pools("chat"), GeminiPools: pools("gemini"), ImagesPools: pools("images"),
		EmbeddingsPools: pools("embeddings"), DecisionsPools: pools("decisions"),
		// Messages 入口使用 Chat 上游，不改变公开目录的协议归属。
		Upstream:     []config.UpstreamConfig{{PoolID: "messages", Name: "Chat 上游", ServiceType: "openai", BaseURL: "https://upstream.invalid", APIKeys: []string{"fixture"}, MaxConcurrency: 5}},
		FreeChannels: &config.FreeChannelsConfig{Kilo: config.KiloFreeConfig{Enabled: true, Models: []string{"vendor/model"}}},
	})
	for _, kind := range []string{"messages", "responses", "chat", "gemini", "images", "embeddings", "decisions"} {
		t.Run(kind, func(t *testing.T) {
			result, err := ModelsForKind(cm, kind)
			require.NoError(t, err)
			want := []string{kind}
			if kind == "messages" || kind == "responses" || kind == "chat" || kind == "gemini" {
				want = append(want, "free")
			}
			require.ElementsMatch(t, want, modelIDs(result.Data))
			for _, model := range result.Data {
				require.Equal(t, "pool:"+kind, model.OwnedBy)
			}
		})
	}
	all, err := ModelsForKind(cm, "")
	require.NoError(t, err)
	require.Len(t, all.Data, 8)
	require.NoError(t, cm.SetKiloEnabled(false))
	result, err := ModelsForKind(cm, "messages")
	require.NoError(t, err)
	require.Equal(t, []string{"messages"}, modelIDs(result.Data))
	_, err = ModelsForKind(cm, "claude")
	require.ErrorContains(t, err, "无效的入口协议")
}
