// Package modelcatalog builds the externally visible model list and resolves
// Chat route aliases back to a pinned channel/key/upstream model.
package modelcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
)

const (
	cacheTTL             = 20 * time.Minute
	modelsRequestTimeout = 30 * time.Second
	modelCreatedFallback = int64(0)
)

var (
	versionSuffixPattern = regexp.MustCompile(`/v\d+[a-z]*$`)
	unsafeModelIDChars   = regexp.MustCompile(`[^A-Za-z0-9._:-]+`)
	globalCache          = &catalogCache{}

	// publicFamilyModelIDs 是对外稳定暴露的家族别名。
	// 实际上游模型由渠道 modelMapping / defaultModel + 分组路由决定。
	publicFamilyModelIDs = []string{
		"opus",
		"sonnet",
		"gpt",
		"gemini",
		"chat",
	}

	// publicPoolKinds 决定从哪些协议收集分组并暴露为可选模型。
	publicPoolKinds = []string{
		"messages",
		"responses",
		"chat",
		"gemini",
		"images",
	}
)

type ModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ModelsResponse struct {
	Object string       `json:"object"`
	Data   []ModelEntry `json:"data"`
}

type ChatRoute struct {
	Alias         string
	UpstreamModel string
	ChannelIndex  int
	ChannelName   string
	BaseURL       string
	APIKey        string
	KeyID         string
	ServiceType   string
}

type catalogSnapshot struct {
	expiresAt time.Time
	models    []ModelEntry
	routes    map[string]ChatRoute
}

type catalogCache struct {
	mu         sync.RWMutex
	snapshot   catalogSnapshot
	refreshing bool
}

func OpenAIModels(ctx context.Context, cfgManager *config.ConfigManager) ModelsResponse {
	_ = ctx
	return ModelsResponse{Object: "list", Data: publicModelEntries(cfgManager)}
}

func ModelDetail(ctx context.Context, cfgManager *config.ConfigManager, modelID string) (ModelEntry, bool) {
	_ = ctx
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ModelEntry{}, false
	}
	for _, model := range publicModelEntries(cfgManager) {
		if model.ID == modelID {
			return model, true
		}
	}
	return ModelEntry{}, false
}

func ResolveChatRoute(ctx context.Context, cfgManager *config.ConfigManager, modelID string) (ChatRoute, bool) {
	if !looksLikeChatRouteAlias(modelID) {
		return ChatRoute{}, false
	}

	_, routes, err := chatCatalog(ctx, cfgManager, false)
	if err == nil {
		if route, ok := routes[modelID]; ok {
			return route, true
		}
	}

	_, routes, err = chatCatalog(ctx, cfgManager, true)
	if err != nil {
		return ChatRoute{}, false
	}
	route, ok := routes[modelID]
	return route, ok
}

func looksLikeChatRouteAlias(modelID string) bool {
	return strings.Contains(modelID, "__c") && strings.Contains(modelID, "__k")
}

func staticFamilyModels() []ModelEntry {
	models := make([]ModelEntry, 0, len(publicFamilyModelIDs))
	for _, id := range publicFamilyModelIDs {
		models = append(models, ModelEntry{
			ID:      id,
			Object:  "model",
			Created: modelCreatedFallback,
			OwnedBy: "api-proxy",
		})
	}
	return models
}

// publicModelEntries 返回对外可见模型：固定家族别名 + 各协议分组捕获规则。
// 不再暴露渠道级上游模型或 Chat 路由别名（如 model__c0__kxxxxxxxx）。
func publicModelEntries(cfgManager *config.ConfigManager) []ModelEntry {
	familyModels := staticFamilyModels()
	familyIndexes := make(map[string]int, len(familyModels))
	for index, model := range familyModels {
		familyIndexes[model.ID] = index
	}

	poolModels := poolMatcherModels(cfgManager)
	data := make([]ModelEntry, 0, len(familyModels)+len(poolModels))
	data = append(data, familyModels...)
	for _, model := range poolModels {
		if index, ok := familyIndexes[model.ID]; ok {
			// 同名模型只保留一条，但必须保留真实分组归属，供客户端按协议导入。
			data[index].OwnedBy = model.OwnedBy
			continue
		}
		data = append(data, model)
	}
	return data
}

// poolMatcherModels 把非通配符分组的 modelMatcher 暴露为可选模型 ID。
// 客户端用该 ID 发请求时，调度器按 contains 最长匹配进入对应分组，再走渠道映射/故障转移。
func poolMatcherModels(cfgManager *config.ConfigManager) []ModelEntry {
	if cfgManager == nil {
		return nil
	}

	// matcher -> 出现过的协议种类（用于 owned_by）
	matcherKinds := make(map[string][]string)
	for _, kind := range publicPoolKinds {
		pools := cfgManager.GetChannelPools(kind)
		for _, pool := range pools {
			matcher := strings.TrimSpace(pool.ModelMatcher)
			if matcher == "" || matcher == "*" {
				continue
			}
			// 对外模型 ID 保持与调度匹配规则一致（小写 contains）
			matcher = strings.ToLower(matcher)
			kinds := matcherKinds[matcher]
			alreadyListed := false
			for _, existingKind := range kinds {
				if existingKind == kind {
					alreadyListed = true
					break
				}
			}
			if alreadyListed {
				continue
			}
			matcherKinds[matcher] = append(kinds, kind)
		}
	}

	matchers := make([]string, 0, len(matcherKinds))
	for matcher := range matcherKinds {
		matchers = append(matchers, matcher)
	}
	sort.Strings(matchers)

	models := make([]ModelEntry, 0, len(matchers))
	for _, matcher := range matchers {
		kinds := matcherKinds[matcher]
		sort.Strings(kinds)
		models = append(models, ModelEntry{
			ID:      matcher,
			Object:  "model",
			Created: modelCreatedFallback,
			OwnedBy: "pool:" + strings.Join(kinds, ","),
		})
	}
	return models
}

func chatCatalog(ctx context.Context, cfgManager *config.ConfigManager, forceRefresh bool) ([]ModelEntry, map[string]ChatRoute, error) {
	now := time.Now()
	if !forceRefresh {
		globalCache.mu.RLock()
		if now.Before(globalCache.snapshot.expiresAt) {
			models := cloneModels(globalCache.snapshot.models)
			routes := cloneRoutes(globalCache.snapshot.routes)
			globalCache.mu.RUnlock()
			return models, routes, nil
		}
		if len(globalCache.snapshot.models) > 0 {
			models := cloneModels(globalCache.snapshot.models)
			routes := cloneRoutes(globalCache.snapshot.routes)
			globalCache.mu.RUnlock()
			startBackgroundRefresh(cfgManager)
			return models, routes, nil
		}
		globalCache.mu.RUnlock()
	}

	return refreshChatCatalog(ctx, cfgManager)
}

func refreshChatCatalog(ctx context.Context, cfgManager *config.ConfigManager) ([]ModelEntry, map[string]ChatRoute, error) {
	models, routes, err := discoverChatModels(ctx, cfgManager)
	globalCache.mu.Lock()
	defer globalCache.mu.Unlock()
	if err != nil && len(globalCache.snapshot.models) > 0 {
		return cloneModels(globalCache.snapshot.models), cloneRoutes(globalCache.snapshot.routes), err
	}
	globalCache.snapshot = catalogSnapshot{
		expiresAt: time.Now().Add(cacheTTL),
		models:    cloneModels(models),
		routes:    cloneRoutes(routes),
	}
	return models, routes, err
}

func startBackgroundRefresh(cfgManager *config.ConfigManager) {
	globalCache.mu.Lock()
	if globalCache.refreshing {
		globalCache.mu.Unlock()
		return
	}
	globalCache.refreshing = true
	globalCache.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), modelsRequestTimeout)
		defer cancel()

		models, routes, err := discoverChatModels(ctx, cfgManager)

		globalCache.mu.Lock()
		defer globalCache.mu.Unlock()
		defer func() { globalCache.refreshing = false }()
		if err != nil {
			log.Printf("[Models-Chat] 后台刷新失败，继续使用旧缓存: %v", err)
			return
		}
		globalCache.snapshot = catalogSnapshot{
			expiresAt: time.Now().Add(cacheTTL),
			models:    cloneModels(models),
			routes:    cloneRoutes(routes),
		}
		log.Printf("[Models-Chat] 后台刷新完成: models=%d", len(models))
	}()
}

func discoverChatModels(ctx context.Context, cfgManager *config.ConfigManager) ([]ModelEntry, map[string]ChatRoute, error) {
	cfg := cfgManager.GetConfig()
	routes := make(map[string]ChatRoute)
	models := make([]ModelEntry, 0)
	usedAliases := make(map[string]bool)
	usedRouteTargets := make(map[string]bool)
	var errors []string

	for channelIndex, upstream := range cfg.ChatUpstream {
		if config.GetChannelStatus(&upstream) != config.ChannelStatusActive || !config.IsChannelSchedulable(&upstream) {
			continue
		}
		if len(upstream.APIKeys) == 0 {
			continue
		}

		baseURLs := upstream.GetAllBaseURLs()
		if len(baseURLs) == 0 {
			continue
		}

		for _, apiKey := range upstream.APIKeys {
			keyID := keyFingerprint(apiKey)
			for _, configured := range configuredChatModels(&upstream) {
				appendChatRoute(
					&models, routes, usedAliases, usedRouteTargets,
					configured.ClientModel, configured.UpstreamModel,
					channelIndex, &upstream, apiKey, keyID, baseURLs[0], "local",
				)
			}

			foundModels, baseURL, method, err := discoverModelsForKey(ctx, cfgManager, &upstream, apiKey, baseURLs)
			if err != nil {
				errors = append(errors, fmt.Sprintf("[%d]%s/%s: %v", channelIndex, upstream.Name, keyID, err))
				continue
			}

			for _, upstreamModel := range foundModels {
				appendChatRoute(
					&models, routes, usedAliases, usedRouteTargets,
					upstreamModel, upstreamModel,
					channelIndex, &upstream, apiKey, keyID, baseURL, method,
				)
			}
		}
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})

	if len(models) == 0 && len(errors) > 0 {
		return models, routes, fmt.Errorf("%s", strings.Join(errors, "; "))
	}
	return models, routes, nil
}

type configuredModel struct {
	ClientModel   string
	UpstreamModel string
}

func configuredChatModels(upstream *config.UpstreamConfig) []configuredModel {
	seen := make(map[string]bool)
	models := make([]configuredModel, 0, len(upstream.ModelMapping)+1)

	if model := strings.TrimSpace(upstream.DefaultModel); model != "" {
		models = append(models, configuredModel{ClientModel: model, UpstreamModel: model})
		seen[model] = true
	}

	for clientModel, upstreamModels := range upstream.ModelMapping {
		clientModel = strings.TrimSpace(clientModel)
		if clientModel == "" || clientModel == "*" || seen[clientModel] || len(upstreamModels) == 0 {
			continue
		}
		// 使用第一个目标模型作为默认展示
		upstreamModel := strings.TrimSpace(upstreamModels[0])
		if upstreamModel == "" {
			continue
		}
		models = append(models, configuredModel{ClientModel: clientModel, UpstreamModel: upstreamModel})
		seen[clientModel] = true
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].ClientModel < models[j].ClientModel
	})
	return models
}

func appendChatRoute(
	models *[]ModelEntry,
	routes map[string]ChatRoute,
	usedAliases map[string]bool,
	usedRouteTargets map[string]bool,
	clientModel string,
	upstreamModel string,
	channelIndex int,
	upstream *config.UpstreamConfig,
	apiKey string,
	keyID string,
	baseURL string,
	method string,
) {
	targetKey := fmt.Sprintf("%d|%s|%s|%s", channelIndex, keyID, clientModel, upstreamModel)
	if usedRouteTargets[targetKey] {
		return
	}
	usedRouteTargets[targetKey] = true

	alias := uniqueAlias(clientModel, channelIndex, keyID, usedAliases)
	route := ChatRoute{
		Alias:         alias,
		UpstreamModel: upstreamModel,
		ChannelIndex:  channelIndex,
		ChannelName:   upstream.Name,
		BaseURL:       baseURL,
		APIKey:        apiKey,
		KeyID:         keyID,
		ServiceType:   strings.TrimSpace(upstream.ServiceType),
	}
	routes[alias] = route
	*models = append(*models, ModelEntry{
		ID:      alias,
		Object:  "model",
		Created: modelCreatedFallback,
		OwnedBy: fmt.Sprintf("chat:%s:%s", upstream.Name, method),
	})
}

// DiscoverUpstreamModels 为管理界面的渠道模型发现提供统一的后端出站路径。
func DiscoverUpstreamModels(ctx context.Context, cfgManager *config.ConfigManager, upstream *config.UpstreamConfig, apiKey string) (ModelsResponse, error) {
	if cfgManager == nil || upstream == nil {
		return ModelsResponse{}, fmt.Errorf("缺少模型发现配置")
	}
	baseURLs := upstream.GetAllBaseURLs()
	if len(baseURLs) == 0 {
		return ModelsResponse{}, fmt.Errorf("缺少上游地址")
	}
	// API Key 允许为空：本地 Ollama 等上游通常无需密钥。
	// 需要鉴权的上游会在探测阶段返回明确错误。

	discoveryCtx, cancel := context.WithTimeout(ctx, modelsRequestTimeout)
	defer cancel()
	models, _, method, err := discoverModelsForKey(discoveryCtx, cfgManager, upstream, apiKey, baseURLs)
	if err != nil {
		return ModelsResponse{}, err
	}

	sort.Strings(models)
	entries := make([]ModelEntry, 0, len(models))
	for _, model := range models {
		entries = append(entries, ModelEntry{
			ID:      model,
			Object:  "model",
			Created: modelCreatedFallback,
			OwnedBy: method,
		})
	}
	return ModelsResponse{Object: "list", Data: entries}, nil
}

func uniqueAlias(model string, channelIndex int, keyID string, used map[string]bool) string {
	slug := sanitizeModelID(model)
	if slug == "" {
		slug = "model"
	}
	base := fmt.Sprintf("%s__c%d__k%s", slug, channelIndex, keyID)
	if !used[base] {
		used[base] = true
		return base
	}

	hash := shortHash(model, 6)
	alias := base + "__m" + hash
	for i := 2; used[alias]; i++ {
		alias = fmt.Sprintf("%s__m%s_%d", base, hash, i)
	}
	used[alias] = true
	return alias
}

func sanitizeModelID(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "models/")
	model = unsafeModelIDChars.ReplaceAllString(model, "-")
	model = strings.Trim(model, "-")
	for strings.Contains(model, "--") {
		model = strings.ReplaceAll(model, "--", "-")
	}
	return model
}

func keyFingerprint(apiKey string) string {
	return shortHash(apiKey, 8)
}

func shortHash(value string, n int) string {
	sum := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(sum[:])
	if n > len(encoded) {
		n = len(encoded)
	}
	return encoded[:n]
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func cloneModels(models []ModelEntry) []ModelEntry {
	if models == nil {
		return nil
	}
	cloned := make([]ModelEntry, len(models))
	copy(cloned, models)
	return cloned
}

func cloneRoutes(routes map[string]ChatRoute) map[string]ChatRoute {
	if routes == nil {
		return nil
	}
	cloned := make(map[string]ChatRoute, len(routes))
	for k, v := range routes {
		cloned[k] = v
	}
	return cloned
}
