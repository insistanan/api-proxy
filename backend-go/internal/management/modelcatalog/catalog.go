// Package modelcatalog 提供公开模型目录与显式上游模型发现。
package modelcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"insistanan/api-proxy/internal/infra/config"
)

const (
	modelsRequestTimeout = 30 * time.Second
	modelCreatedFallback = int64(0)
)

var (
	versionSuffixPattern    = regexp.MustCompile(`/v\d+[a-z]*$`)
	retiredChatAliasPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+__c[0-9]+__k[0-9a-f]{8}(?:__m[0-9a-f]{6}(?:_[0-9]+)?)?$`)

	// publicPoolKinds 决定从哪些协议收集分组并暴露为可选模型。
	publicPoolKinds = []string{
		"messages",
		"responses",
		"chat",
		"gemini",
		"images",
		"embeddings",
		"decisions",
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

func OpenAIModels(ctx context.Context, cfgManager *config.ConfigManager) ModelsResponse {
	_ = ctx
	return ModelsResponse{Object: "list", Data: publicModelEntries(cfgManager)}
}

// ModelsForKind 按客户端入口协议筛选目录，与渠道实际上游协议无关。空串表示全部入口。
func ModelsForKind(cfgManager *config.ConfigManager, kind string) (ModelsResponse, error) {
	if kind != "" {
		valid := false
		for _, supported := range publicPoolKinds {
			valid = valid || kind == supported
		}
		if !valid {
			return ModelsResponse{}, fmt.Errorf("无效的入口协议 %q", kind)
		}
	}
	return ModelsResponse{Object: "list", Data: publicModelEntriesForKind(cfgManager, kind)}, nil
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

// IsRetiredChatAlias 只识别已退役的完整语法，普通模型名中的相似片段不受影响。
func IsRetiredChatAlias(modelID string) bool {
	return retiredChatAliasPattern.MatchString(modelID)
}

// publicModelEntries 只根据已配置分组生成目录：普通分组使用匹配词，免费分组统一使用 free。
// 不暴露渠道级上游模型或 Chat 路由别名（如 model__c0__kxxxxxxxx）。
func publicModelEntries(cfgManager *config.ConfigManager) []ModelEntry {
	return publicModelEntriesForKind(cfgManager, "")
}

func publicModelEntriesForKind(cfgManager *config.ConfigManager, kind string) []ModelEntry {
	if cfgManager == nil {
		return []ModelEntry{}
	}
	// 分组与免费清单从同一配置快照读取，热重载时不会混入不同版本的数据。
	cfg := cfgManager.GetConfig()
	data := poolMatcherModels(cfg, kind)
	shared := cfg.FreeChannels
	if shared != nil && shared.Kilo.Enabled && (kind == "" || config.SupportsFreeChannels(kind)) {
		owner := "pool:chat,gemini,messages,responses"
		if kind != "" {
			owner = "pool:" + kind
		}
		data = append(data, ModelEntry{ID: config.FreeModelID, Object: "model", OwnedBy: owner})
	}
	sort.Slice(data, func(i, j int) bool { return data[i].ID < data[j].ID })
	return data
}

// poolMatcherModels 把非通配符分组的 modelMatcher 暴露为可选模型 ID。
// 客户端用该 ID 发请求时，调度器按 contains 最长匹配进入对应分组，再走渠道映射/故障转移。
func poolMatcherModels(cfg config.Config, kind string) []ModelEntry {
	groups := []struct {
		kind  string
		pools []config.ChannelPool
	}{
		{"messages", cfg.MessagePools}, {"responses", cfg.ResponsesPools},
		{"chat", cfg.ChatPools}, {"gemini", cfg.GeminiPools}, {"images", cfg.ImagesPools},
		{"embeddings", cfg.EmbeddingsPools}, {"decisions", cfg.DecisionsPools},
	}
	// matcher -> 出现过的协议种类（用于 owned_by）
	matcherKinds := make(map[string][]string)
	for _, group := range groups {
		if kind != "" && group.kind != kind {
			continue
		}
		for _, pool := range group.pools {
			if pool.ID == config.FreeChannelPoolID {
				continue
			}
			matcher := strings.TrimSpace(pool.ModelMatcher)
			if matcher == "" || matcher == "*" {
				continue
			}
			// 对外模型 ID 保持与调度匹配规则一致（小写 contains）
			matcher = strings.ToLower(matcher)
			kinds := matcherKinds[matcher]
			alreadyListed := false
			for _, existingKind := range kinds {
				if existingKind == group.kind {
					alreadyListed = true
					break
				}
			}
			if alreadyListed {
				continue
			}
			matcherKinds[matcher] = append(kinds, group.kind)
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
