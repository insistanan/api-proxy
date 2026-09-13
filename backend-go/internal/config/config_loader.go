package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	maxBackups      = 10
	keyRecoveryTime = 3 * time.Minute
	maxFailureCount = 3
)

// NewConfigManager 创建配置管理器
func NewConfigManager(configFile string) (*ConfigManager, error) {
	cm := &ConfigManager{
		configFile:      configFile,
		failedKeysCache: make(map[string]*FailedKey),
		keyRecoveryTime: keyRecoveryTime,
		maxFailureCount: maxFailureCount,
		stopChan:        make(chan struct{}),
	}

	// 加载配置
	if err := cm.loadConfig(); err != nil {
		return nil, err
	}

	// 启动文件监听
	if err := cm.startWatcher(); err != nil {
		log.Printf("[Config-Watcher] 警告: 启动配置文件监听失败: %v", err)
	}

	// 启动定期清理
	go cm.cleanupExpiredFailures()
	go cm.startChannelLifecycleWorker()

	return cm, nil
}

// loadConfig 加载配置
func (cm *ConfigManager) loadConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 如果配置文件不存在，创建默认配置
	if _, err := os.Stat(cm.configFile); os.IsNotExist(err) {
		return cm.createDefaultConfig()
	}

	// 读取配置文件
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, &cm.config); err != nil {
		return err
	}

	// 兼容旧配置：检查 FuzzyModeEnabled 字段是否存在
	// 如果不存在，默认设为 true（新功能默认启用）
	needSaveDefaults := cm.applyConfigDefaults(data)

	// 兼容旧格式：检测是否需要迁移
	needMigration := cm.migrateOldFormat()

	// 为旧配置补齐稳定标识并归一化优先级，避免新增、删除和恢复渠道后出现错绑或重复排序。
	needNormalization := cm.normalizeChannelLists()

	if err := cm.validateProxyConfig(); err != nil {
		return err
	}
	if err := ValidateContentSafetyConfig(cm.config.Settings.ContentSafety); err != nil {
		return fmt.Errorf("内容安全配置无效: %w", err)
	}

	// 如果有默认值迁移或格式迁移，保存配置
	if needSaveDefaults || needMigration || needNormalization {
		if err := cm.saveConfigLocked(cm.config); err != nil {
			log.Printf("[Config-Migration] 警告: 保存迁移后的配置失败: %v", err)
			return err
		}
		if needMigration {
			log.Printf("[Config-Migration] 配置迁移完成")
		}
	}

	// 自检：没有配置 key 的渠道自动暂停
	if cm.validateChannelKeys() {
		if err := cm.saveConfigLocked(cm.config); err != nil {
			log.Printf("[Config-Validate] 警告: 保存自检后的配置失败: %v", err)
			return err
		}
	}

	return nil
}

// createDefaultConfig 创建默认配置
func (cm *ConfigManager) createDefaultConfig() error {
	defaultConfig := Config{
		Upstream:                  []UpstreamConfig{},
		MessagePools:              []ChannelPool{defaultChannelPool()},
		CurrentUpstream:           0,
		LoadBalance:               "failover",
		ResponsesUpstream:         []UpstreamConfig{},
		ResponsesPools:            []ChannelPool{defaultChannelPool()},
		CurrentResponsesUpstream:  0,
		ResponsesLoadBalance:      "failover",
		GeminiUpstream:            []UpstreamConfig{},
		GeminiPools:               []ChannelPool{defaultChannelPool()},
		GeminiLoadBalance:         "failover",
		ChatUpstream:              []UpstreamConfig{},
		ChatPools:                 []ChannelPool{defaultChannelPool()},
		ChatLoadBalance:           "failover",
		ImagesUpstream:            []UpstreamConfig{},
		ImagesPools:               []ChannelPool{defaultChannelPool()},
		ImagesLoadBalance:         "failover",
		FuzzyModeEnabled:          true, // 默认启用 Fuzzy 模式
		ClaudeCodeDisguiseEnabled: false,
		CodexDisguiseEnabled:      false,
		Settings: SettingsConfig{
			ContentSafety: DefaultContentSafetyConfig(),
		},
	}

	if err := os.MkdirAll(filepath.Dir(cm.configFile), 0755); err != nil {
		return err
	}

	return cm.saveConfigLocked(defaultConfig)
}

// applyConfigDefaults 应用配置默认值
// rawJSON: 原始 JSON 数据，用于检测字段是否存在
// 返回: 是否有字段需要迁移（需要保存配置）
func (cm *ConfigManager) applyConfigDefaults(rawJSON []byte) bool {
	needSave := false

	if cm.config.LoadBalance == "" {
		cm.config.LoadBalance = "failover"
		needSave = true
	} else if cm.config.LoadBalance != "failover" {
		cm.config.LoadBalance = "failover"
		needSave = true
	}
	if cm.config.ResponsesLoadBalance == "" {
		cm.config.ResponsesLoadBalance = "failover"
		needSave = true
	} else if cm.config.ResponsesLoadBalance != "failover" {
		cm.config.ResponsesLoadBalance = "failover"
		needSave = true
	}
	if cm.config.GeminiLoadBalance == "" {
		cm.config.GeminiLoadBalance = "failover"
		needSave = true
	} else if cm.config.GeminiLoadBalance != "failover" {
		cm.config.GeminiLoadBalance = "failover"
		needSave = true
	}
	if cm.config.ChatLoadBalance == "" {
		cm.config.ChatLoadBalance = "failover"
		needSave = true
	} else if cm.config.ChatLoadBalance != "failover" {
		cm.config.ChatLoadBalance = "failover"
		needSave = true
	}
	if cm.config.ImagesLoadBalance == "" {
		cm.config.ImagesLoadBalance = "failover"
		needSave = true
	} else if cm.config.ImagesLoadBalance != "failover" {
		cm.config.ImagesLoadBalance = "failover"
		needSave = true
	}

	// FuzzyModeEnabled 默认值处理：
	// 由于 bool 零值是 false，无法区分"用户设为 false"和"字段不存在"
	// 通过检查原始 JSON 是否包含该字段来判断
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &rawMap); err == nil {
		if _, exists := rawMap["fuzzyModeEnabled"]; !exists {
			// 字段不存在，设为默认值 true
			cm.config.FuzzyModeEnabled = true
			needSave = true
			log.Printf("[Config-Migration] FuzzyModeEnabled 字段不存在，设为默认值 true")
		}
		if rawResponses, exists := rawMap["responsesUpstream"]; exists {
			var responseItems []map[string]json.RawMessage
			if err := json.Unmarshal(rawResponses, &responseItems); err == nil {
				for index := range cm.config.ResponsesUpstream {
					if index >= len(responseItems) {
						break
					}
					_, capabilityConfigured := responseItems[index]["visionCapable"]
					_, layerConfigured := responseItems[index]["visionLayerEnabled"]
					if !capabilityConfigured && !layerConfigured {
						cm.config.ResponsesUpstream[index].VisionCapable = true
						cm.config.ResponsesUpstream[index].VisionLayerEnabled = false
						cm.config.ResponsesUpstream[index].VisionLayerChannelID = ""
						cm.config.ResponsesUpstream[index].VisionLayerModel = ""
						needSave = true
						log.Printf("[Config-Migration] Responses 渠道 [%d] 默认启用图片理解", index)
					}
				}
			}
		}
		if rawSettings, exists := rawMap["settings"]; !exists {
			cm.config.Settings.ContentSafety = DefaultContentSafetyConfig()
			needSave = true
			log.Printf("[Config-Migration] Settings.ContentSafety 字段不存在，已写入默认配置")
		} else {
			var settingsMap map[string]json.RawMessage
			if err := json.Unmarshal(rawSettings, &settingsMap); err == nil {
				if rawContentSafety, exists := settingsMap["contentSafety"]; !exists {
					cm.config.Settings.ContentSafety = DefaultContentSafetyConfig()
					needSave = true
					log.Printf("[Config-Migration] Settings.ContentSafety 字段不存在，已写入默认配置")
				} else if migrateContentSafetyConfig(&cm.config.Settings.ContentSafety, rawContentSafety) {
					needSave = true
					log.Printf("[Config-Migration] 内容安全配置已迁移到分离的 PII/凭据策略")
				}
				// 旧版全局代理配置只有代理地址没有启用开关（引入开关前的版本）：
				// 地址非空表示此前一直生效，按“已启用”迁移，保持旧行为不因开关默认 false 而中断。
				if rawNetwork, exists := settingsMap["network"]; exists {
					var networkMap map[string]json.RawMessage
					if err := json.Unmarshal(rawNetwork, &networkMap); err == nil {
						if _, hasEnabled := networkMap["upstreamProxyEnabled"]; !hasEnabled &&
							strings.TrimSpace(cm.config.Settings.Network.UpstreamProxyURL) != "" {
							cm.config.Settings.Network.UpstreamProxyEnabled = true
							needSave = true
							log.Printf("[Config-Migration] 旧版全局代理配置缺少启用开关，按已启用迁移")
						}
					}
				}
			}
		}
	}

	poolSets := []struct {
		pools     *[]ChannelPool
		upstreams []UpstreamConfig
		label     string
	}{
		{&cm.config.MessagePools, cm.config.Upstream, "Messages"},
		{&cm.config.ResponsesPools, cm.config.ResponsesUpstream, "Responses"},
		{&cm.config.GeminiPools, cm.config.GeminiUpstream, "Gemini"},
		{&cm.config.ChatPools, cm.config.ChatUpstream, "Chat"},
		{&cm.config.ImagesPools, cm.config.ImagesUpstream, "Images"},
	}
	for _, set := range poolSets {
		normalized, changed, err := ensurePoolsAndAssignments(*set.pools, set.upstreams)
		if err != nil {
			log.Printf("[Config-Migration] %s 分组配置验证失败，保留现有配置请手动修复: %v", set.label, err)
			// 验证失败时不重置分组也不迁移渠道，避免数据丢失
			// 保留现有配置，让用户通过管理界面手动修复
			changed = false
			normalized = *set.pools
		}
		*set.pools = normalized
		needSave = needSave || changed
	}

	return needSave
}

func migrateContentSafetyConfig(settings *ContentSafetyConfig, rawJSON []byte) bool {
	if settings == nil {
		return false
	}
	defaults := DefaultContentSafetyConfig()
	if bytes.Equal(bytes.TrimSpace(rawJSON), []byte("null")) {
		*settings = defaults
		return true
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		return false
	}
	changed := false
	if rawSensitiveData, exists := raw["sensitiveData"]; !exists {
		settings.SensitiveData = defaults.SensitiveData
		changed = true
	} else {
		var fields map[string]json.RawMessage
		if json.Unmarshal(rawSensitiveData, &fields) == nil {
			if _, exists := fields["mode"]; !exists || strings.TrimSpace(settings.SensitiveData.Mode) == "" {
				settings.SensitiveData = defaults.SensitiveData
				changed = true
			}
		}
	}
	if _, exists := raw["sensitiveWord"]; !exists {
		settings.SensitiveWord = defaults.SensitiveWord
		changed = true
	}
	if rawInfo, exists := raw["sensitiveInfo"]; !exists {
		settings.SensitiveInfo = defaults.SensitiveInfo
		changed = true
	} else {
		var fields map[string]json.RawMessage
		if json.Unmarshal(rawInfo, &fields) == nil {
			if _, exists := fields["mode"]; !exists || strings.TrimSpace(settings.SensitiveInfo.Mode) == "" {
				settings.SensitiveInfo.Mode = defaults.SensitiveInfo.Mode
				changed = true
			}
			if _, exists := fields["ipMaskScope"]; !exists || strings.TrimSpace(settings.SensitiveInfo.IPMaskScope) == "" {
				settings.SensitiveInfo.IPMaskScope = defaults.SensitiveInfo.IPMaskScope
				changed = true
			}
		}
	}

	_, credentialExists := raw["credential"]
	if !credentialExists {
		settings.Credential = defaults.Credential
		changed = true
	} else {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw["credential"], &fields) == nil {
			if _, exists := fields["userInputMode"]; !exists || strings.TrimSpace(settings.Credential.UserInputMode) == "" {
				settings.Credential.UserInputMode = defaults.Credential.UserInputMode
				changed = true
			}
			if _, exists := fields["toolResultMode"]; !exists || strings.TrimSpace(settings.Credential.ToolResultMode) == "" {
				settings.Credential.ToolResultMode = defaults.Credential.ToolResultMode
				changed = true
			}
			if _, exists := fields["toolArgumentMode"]; !exists || strings.TrimSpace(settings.Credential.ToolArgumentMode) == "" {
				settings.Credential.ToolArgumentMode = defaults.Credential.ToolArgumentMode
				changed = true
			}
		}
	}

	legacyAPIKey := false
	legacySensitiveInfoEnabled := settings.SensitiveInfo.Enabled
	filteredInfoRules := make([]string, 0, len(settings.SensitiveInfo.EnabledRules))
	for _, rule := range settings.SensitiveInfo.EnabledRules {
		if rule == CredentialRuleAPIKey {
			legacyAPIKey = true
			changed = true
			continue
		}
		filteredInfoRules = append(filteredInfoRules, rule)
	}
	settings.SensitiveInfo.EnabledRules = filteredInfoRules
	if legacyAPIKey && len(filteredInfoRules) == 0 && settings.SensitiveInfo.Enabled {
		settings.SensitiveInfo.Enabled = false
		changed = true
	}
	if legacyAPIKey && !containsString(settings.Credential.EnabledRules, CredentialRuleAPIKey) {
		settings.Credential.EnabledRules = append(settings.Credential.EnabledRules, CredentialRuleAPIKey)
		if !credentialExists {
			settings.Credential.Enabled = legacySensitiveInfoEnabled
			settings.Credential.UserInputMode = ContentSafetyModeMask
		}
	}

	if _, exists := raw["dangerousCmd"]; !exists {
		settings.DangerousCmd = defaults.DangerousCmd
		changed = true
	}
	// 旧配置的 dangerousCmd 没有 aiGate 子对象（Go 零值 WaitSeconds=0）：
	// 补默认等待秒数，保持"未启用但参数就绪"的形态。
	if settings.DangerousCmd.AIGate.WaitSeconds == 0 {
		settings.DangerousCmd.AIGate.WaitSeconds = defaults.DangerousCmd.AIGate.WaitSeconds
		changed = true
	}
	if settings.SensitiveWord.CustomWords == nil {
		settings.SensitiveWord.CustomWords = []string{}
		changed = true
	}
	if settings.SensitiveInfo.EnabledRules == nil {
		settings.SensitiveInfo.EnabledRules = []string{}
		changed = true
	}
	if settings.Credential.EnabledRules == nil {
		settings.Credential.EnabledRules = []string{}
		changed = true
	}
	if settings.DangerousCmd.EnabledRules == nil {
		settings.DangerousCmd.EnabledRules = []string{}
		changed = true
	}
	return changed
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// migrateOldFormat 迁移旧格式配置，返回是否有迁移
func (cm *ConfigManager) migrateOldFormat() bool {
	needMigration := false

	// 迁移 Messages 渠道
	if cm.migrateUpstreams(cm.config.Upstream, cm.config.CurrentUpstream, "Messages") {
		needMigration = true
	}

	// 迁移 Responses 渠道
	if cm.migrateUpstreams(cm.config.ResponsesUpstream, cm.config.CurrentResponsesUpstream, "Responses") {
		needMigration = true
	}
	if cm.migrateUpstreams(cm.config.GeminiUpstream, 0, "Gemini") {
		needMigration = true
	}
	if cm.migrateUpstreams(cm.config.ChatUpstream, 0, "Chat") {
		needMigration = true
	}
	if cm.migrateUpstreams(cm.config.ImagesUpstream, 0, "Images") {
		needMigration = true
	}

	if needMigration {
		log.Printf("[Config-Migration] 检测到旧格式配置，正在迁移到新格式...")
	}

	return needMigration
}

func (cm *ConfigManager) normalizeChannelLists() bool {
	changed := false
	lists := [][]UpstreamConfig{
		cm.config.Upstream,
		cm.config.ResponsesUpstream,
		cm.config.GeminiUpstream,
		cm.config.ChatUpstream,
		cm.config.ImagesUpstream,
	}
	for _, upstreams := range lists {
		changed = ensureUpstreamIDs(upstreams) || changed
		changed = normalizeUpstreamPriorities(upstreams) || changed
		for index := range upstreams {
			if upstreams[index].VisionCapable && upstreams[index].VisionLayerEnabled {
				upstreams[index].VisionCapable = false
				changed = true
			}
		}
	}
	return changed
}

// migrateUpstreams 迁移单个渠道列表
func (cm *ConfigManager) migrateUpstreams(upstreams []UpstreamConfig, currentIdx int, name string) bool {
	if len(upstreams) == 0 {
		return false
	}

	// 检查是否已有 status 字段
	for _, up := range upstreams {
		if up.Status != "" {
			return false
		}
	}

	// 需要迁移
	if currentIdx < 0 || currentIdx >= len(upstreams) {
		currentIdx = 0
	}

	for i := range upstreams {
		if i == currentIdx {
			upstreams[i].Status = "active"
		} else {
			upstreams[i].Status = "disabled"
		}
	}

	log.Printf("[Config-Migration] %s 渠道 [%d] %s 已设置为 active，其他 %d 个渠道已设为 disabled",
		name, currentIdx, upstreams[currentIdx].Name, len(upstreams)-1)

	return true
}

// validateChannelKeys 自检渠道密钥配置
// 没有配置 API key 的渠道，即使状态为 active 也应暂停
// 返回 true 表示有配置被修改，需要保存
func (cm *ConfigManager) validateChannelKeys() bool {
	modified := false
	modified = validateUpstreamKeys(cm.config.Upstream, "Messages") || modified
	modified = validateUpstreamKeys(cm.config.ResponsesUpstream, "Responses") || modified
	modified = validateUpstreamKeys(cm.config.GeminiUpstream, "Gemini") || modified
	modified = validateUpstreamKeys(cm.config.ChatUpstream, "Chat") || modified
	modified = validateUpstreamKeys(cm.config.ImagesUpstream, "Images") || modified

	return modified
}

func validateUpstreamKeys(upstreams []UpstreamConfig, label string) bool {
	changed := false
	for index := range upstreams {
		upstream := &upstreams[index]
		if GetChannelStatus(upstream) == ChannelStatusActive && len(upstream.APIKeys) == 0 {
			upstream.Status = ChannelStatusSuspended
			changed = true
			log.Printf("[Config-Validate] 警告: %s 渠道 [%d] %s 没有配置 API key，已自动暂停", label, index, upstream.Name)
		}
	}
	return changed
}

// saveConfigLocked 保存配置（已加锁）
func (cm *ConfigManager) saveConfigLocked(config Config) error {
	// 备份当前配置
	cm.backupConfig()

	// 清理已废弃字段，确保不会被序列化到 JSON
	config.CurrentUpstream = 0
	config.CurrentResponsesUpstream = 0

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	cm.config = config
	return os.WriteFile(cm.configFile, data, 0644)
}

// SaveConfig 保存配置
func (cm *ConfigManager) SaveConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.saveConfigLocked(cm.config)
}

// backupConfig 备份配置
func (cm *ConfigManager) backupConfig() {
	if _, err := os.Stat(cm.configFile); os.IsNotExist(err) {
		return
	}

	backupDir := filepath.Join(filepath.Dir(cm.configFile), "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		log.Printf("[Config-Backup] 警告: 创建备份目录失败: %v", err)
		return
	}

	// 读取当前配置
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		log.Printf("[Config-Backup] 警告: 读取配置文件失败: %v", err)
		return
	}

	// 创建备份文件
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	backupFile := filepath.Join(backupDir, fmt.Sprintf("config-%s.json", timestamp))
	if err := os.WriteFile(backupFile, data, 0644); err != nil {
		log.Printf("[Config-Backup] 警告: 写入备份文件失败: %v", err)
		return
	}

	// 清理旧备份
	cm.cleanupOldBackups(backupDir)
}

// cleanupOldBackups 清理旧备份
func (cm *ConfigManager) cleanupOldBackups(backupDir string) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}

	if len(entries) <= maxBackups {
		return
	}

	// 删除最旧的备份
	for i := 0; i < len(entries)-maxBackups; i++ {
		os.Remove(filepath.Join(backupDir, entries[i].Name()))
	}
}

// startWatcher 启动文件监听
func (cm *ConfigManager) startWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	cm.watcher = watcher

	go func() {
		for {
			select {
			case <-cm.stopChan:
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Printf("[Config-Watcher] 检测到配置文件变化，重载配置...")
					if err := cm.loadConfig(); err != nil {
						log.Printf("[Config-Watcher] 警告: 配置重载失败: %v", err)
					} else {
						log.Printf("[Config-Watcher] 配置已重载")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("[Config-Watcher] 警告: 文件监听错误: %v", err)
			}
		}
	}()

	return watcher.Add(cm.configFile)
}

// Close 关闭 ConfigManager 并释放资源（幂等，可安全多次调用）
func (cm *ConfigManager) Close() error {
	var closeErr error
	cm.closeOnce.Do(func() {
		// 通知所有 goroutine 停止
		if cm.stopChan != nil {
			close(cm.stopChan)
		}

		// 关闭文件监听器
		if cm.watcher != nil {
			closeErr = cm.watcher.Close()
		}
	})
	return closeErr
}
