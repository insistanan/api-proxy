package config

import "strings"

// 校验器由应用服务装配；配置层不依赖数据集存储。回调在配置锁内执行，不得重入 ConfigManager。
func (cm *ConfigManager) SetResourceValidator(validate func(Config, Config) error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.validateResources = validate
}

// 资源删除持有配置读锁直到完成，与全部配置保存及 watcher 发布互斥。
func (cm *ConfigManager) WithResourceSnapshot(fn func(Config) error) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return fn(cloneConfig(cm.config))
}

type DatasetReference struct {
	Name    string
	Kind    string
	IDs     []string
	Profile SemanticEmbeddingConfig
}

func DatasetReferences(cfg Config) []DatasetReference {
	var refs []DatasetReference
	for _, kind := range conversationChannelKinds {
		value := cfg.IntentRouting[kind]
		if !value.Enabled || !IntentUsesVector(value) {
			continue
		}
		ids := value.DatasetIDs
		if len(ids) == 0 && value.RecognitionMode == "" {
			ids = []string{"inline-" + kind}
		}
		refs = append(refs, DatasetReference{Name: kind + " 意图匹配", Kind: "intent", IDs: ids,
			Profile: SemanticEmbeddingConfig{Source: value.EmbeddingSource, Channel: value.EmbeddingChannel, Model: value.EmbeddingModel, ModelVersion: value.EmbeddingVersion, QueryPrefix: value.QueryPrefix, ExamplePrefix: value.ExamplePrefix, TimeoutMS: value.TimeoutMS}})
	}
	safety := cfg.Settings.ContentSafety
	if cmd := safety.DangerousCmd; safety.Enabled && cmd.Enabled && cmd.Semantic != nil && cmd.Semantic.Enabled {
		refs = append(refs, DatasetReference{Name: "工具语义检测", Kind: "safety", IDs: cmd.Semantic.DatasetIDs, Profile: cmd.Semantic.Embedding})
	}
	return refs
}

// 返回不能随删除自动解除的业务引用，供预览与维护策略解释阻塞原因。
func (cfg *Config) blockingChannelDependents(ids map[string]struct{}) []VisionLayerDependent {
	var result []VisionLayerDependent
	add := func(id, kind, name, feature string, index int) {
		if _, found := ids[id]; found {
			result = append(result, VisionLayerDependent{Kind: kind, Index: index, Name: name, Feature: feature, Blocking: true})
		}
	}
	for _, set := range cfg.channelSets() {
		for index, channel := range *set.upstreams {
			if _, deleting := ids[channel.ID]; deleting || GetChannelStatus(&channel) == ChannelStatusDeleted {
				continue
			}
			for _, targets := range channel.ModelMappingChannels {
				for _, id := range targets {
					add(id, set.kind, channel.Name, "模型映射", index)
				}
			}
		}
	}
	for _, kind := range conversationChannelKinds {
		value := cfg.IntentRouting[kind]
		add(value.EmbeddingChannel, kind, "意图匹配", "向量计算", -1)
		add(value.ClassifierChannel, kind, "意图匹配", "辅助分类", -1)
	}
	safety := cfg.Settings.ContentSafety
	if safety.Enabled && safety.DangerousCmd.Enabled {
		cmd := safety.DangerousCmd
		if cmd.AIGate.Enabled {
			add(cmd.AIGate.ChannelID, "chat", "内容安全", "命令审核", -1)
		}
		if cmd.Semantic != nil && cmd.Semantic.Enabled && cmd.Semantic.Embedding.Source == "remote" {
			add(cmd.Semantic.Embedding.Channel, "embeddings", "内容安全", "语义检测", -1)
		}
	}
	return result
}

func validateSafetyChannelReferences(cfg *Config) error {
	safety := cfg.Settings.ContentSafety
	if !safety.Enabled || !safety.DangerousCmd.Enabled {
		return nil
	}
	check := func(id string, kinds []string, feature string) error {
		if strings.TrimSpace(id) == "" {
			return nil // AI 闸门允许从对应协议池自动选择。
		}
		for _, kind := range kinds {
			set, _ := cfg.channelSetFor(kind)
			for _, channel := range *set.upstreams {
				if channel.ID == id && IsChannelSchedulable(&channel) && len(channel.APIKeys) > 0 {
					return nil
				}
			}
		}
		return proxyConfigErrorf("%s 引用的渠道 %q 不存在或不可用，请先解除引用", feature, id)
	}
	cmd := safety.DangerousCmd
	if cmd.AIGate.Enabled {
		kinds := conversationChannelKinds
		if cmd.AIGate.Protocol == "decisions" || cmd.AIGate.Protocol == "systemone" {
			kinds = []string{"decisions"}
		}
		if err := check(cmd.AIGate.ChannelID, kinds, "命令审核"); err != nil {
			return err
		}
	}
	if cmd.Semantic != nil && cmd.Semantic.Enabled && cmd.Semantic.Embedding.Source == "remote" {
		return check(cmd.Semantic.Embedding.Channel, []string{"embeddings"}, "语义检测")
	}
	return nil
}
