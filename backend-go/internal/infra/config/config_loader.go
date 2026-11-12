package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/insistanan/api-proxy/internal/storage/atomicfile"
)

const (
	maxBackups      = 10
	keyRecoveryTime = 3 * time.Minute
	maxFailureCount = 3

	// reloadDebounce 合并 watcher 短时间事件后重读目标文件的等待窗口：
	// 一次外部保存可能触发 Write+Create+Rename 多个事件，原子替换至少
	// 触发 Create+Rename；等待窗口合并为一次重载。有上限，不会无限推迟。
	reloadDebounce = 100 * time.Millisecond
)

// NewConfigManager 创建配置管理器
func NewConfigManager(configFile string) (*ConfigManager, error) {
	cm := &ConfigManager{
		configFile:      configFile,
		failedKeysCache: make(map[string]*FailedKey),
		keyRecoveryTime: keyRecoveryTime,
		maxFailureCount: maxFailureCount,
		stopChan:        make(chan struct{}),
		settingsChanged: make(chan struct{}, 1),
	}

	// 加载配置
	if err := cm.loadConfig(); err != nil {
		return nil, err
	}

	// 启动文件监听（失败显式返回：配置热重载是部署行为的一部分，
	// 静默降级会让"改了配置不生效"难以排查）
	if err := cm.startWatcher(); err != nil {
		_ = cm.Close()
		return nil, fmt.Errorf("启动配置文件监听失败: %w", err)
	}

	// 启动定期清理
	cm.workers.Add(2)
	go cm.cleanupExpiredFailures()
	go cm.startChannelLifecycleWorker()

	return cm, nil
}

// configDigest 计算配置内容的摘要（仅用于版本比较，不输出配置原文）。
func configDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// loadConfig 启动加载：文件不存在时创建默认配置；存在时在临时候选中
// 解码、迁移、校验，全部通过才发布为当前快照。
func (cm *ConfigManager) loadConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	data, err := os.ReadFile(cm.configFile)
	if os.IsNotExist(err) {
		return cm.createDefaultConfig()
	}
	if err != nil {
		return err
	}
	return cm.applyConfigDataLocked(data)
}

// applyConfigDataLocked 统一启动和热重载的校验、迁移回写与快照发布。
// 摘要去重避免自身提交触发重复迁移；回写失败时保留旧快照。
func (cm *ConfigManager) applyConfigDataLocked(data []byte) error {
	digest := configDigest(data)
	if digest == cm.persistedDigest {
		return nil
	}
	candidate, needSave, err := cm.prepareCandidate(data)
	if err != nil {
		return err
	}
	if needSave {
		return cm.saveConfigLocked(candidate)
	}
	cm.publishConfigLocked(candidate)
	cm.persistedDigest = digest
	return nil
}

// prepareCandidate 在临时候选中完成解码、默认值/旧格式迁移、归一化与
// 校验（R3：读者只能拿到完整旧快照或完整新快照，不存在边改边检查）。
// 返回 (候选配置, 是否需要回写, 错误)。错误时候选不发布。
func (cm *ConfigManager) prepareCandidate(data []byte) (Config, bool, error) {
	var candidate Config
	if err := json.Unmarshal(data, &candidate); err != nil {
		return Config{}, false, fmt.Errorf("配置 JSON 解析失败: %w", err)
	}
	if err := validateFuzzyModeProtocols(&candidate); err != nil {
		return Config{}, false, err
	}
	if err := materializeFreeChannels(&candidate); err != nil {
		return Config{}, false, err
	}

	needSaveDefaults, err := applyConfigDefaultsTo(&candidate, data)
	if err != nil {
		return Config{}, false, err
	}
	needManagedMigration, err := migrateManagedSettings(&candidate, data)
	if err != nil {
		return Config{}, false, fmt.Errorf("迁移全局设置失败: %w", err)
	}
	needMigration := migrateOldFormatIn(&candidate)
	if needMigration {
		log.Printf("[Config-Migration] 检测到旧格式配置，正在迁移到新格式...")
	}
	needNormalization := normalizeChannelListsIn(&candidate)
	if clearUnsupportedClientIdentityIn(&candidate) {
		needNormalization = true
	}
	if err := validateConcurrency(&candidate); err != nil {
		return Config{}, false, err
	}

	if err := validateProxyConfigOf(&candidate); err != nil {
		return Config{}, false, err
	}
	if err := ValidateContentSafetyConfig(candidate.Settings.ContentSafety); err != nil {
		return Config{}, false, fmt.Errorf("内容安全配置无效: %w", err)
	}

	// 自检：没有配置 key 的渠道自动暂停
	needKeyCheck := validateChannelKeysIn(&candidate)

	return candidate, needSaveDefaults || needManagedMigration || needMigration || needNormalization || needKeyCheck, nil
}

// reloadFromDisk 热重载（watcher 触发）：在临时候选中解码与校验，
// 成功才发布；任何失败保留最后一次有效快照并记录明确错误。
// 运行期间目标文件短暂缺失或内容不完整时同样保留有效快照；
// 后续有效文件出现时自然恢复（事件会再次触发重载）。
func (cm *ConfigManager) reloadFromDisk() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	data, err := os.ReadFile(cm.configFile)
	if os.IsNotExist(err) {
		log.Printf("[Config-Watcher] 警告: 配置文件暂时缺失，保留当前有效快照: %v", err)
		return nil
	}
	if err != nil {
		return err
	}
	if err := cm.applyConfigDataLocked(data); err != nil {
		return err
	}
	log.Printf("[Config-Watcher] 配置已重载")
	return nil
}

// createDefaultConfig 创建默认配置
func (cm *ConfigManager) createDefaultConfig() error {
	defaultConfig := Config{
		FuzzyModeEnabled: true,
		Settings: SettingsConfig{
			ContentSafety: DefaultContentSafetyConfig(),
			Disguise:      DisguiseSettings{AutoUpdateFromRegistry: true},
		},
	}
	for _, set := range defaultConfig.channelSets() {
		*set.upstreams = []UpstreamConfig{}
		*set.pools = []ChannelPool{defaultChannelPool()}
		*set.loadBalance = "failover"
	}

	if _, err := migrateManagedSettings(&defaultConfig, []byte(`{}`)); err != nil {
		return fmt.Errorf("迁移旧环境配置失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cm.configFile), 0755); err != nil {
		return err
	}

	return cm.saveConfigLocked(defaultConfig)
}

// applyConfigDefaultsTo 应用配置默认值到候选（在候选上进行，不触碰当前快照）。
// rawJSON: 原始 JSON 数据，用于检测字段是否存在
// 返回是否需要保存迁移结果；格式或分组校验失败时不发布候选。
func applyConfigDefaultsTo(candidate *Config, rawJSON []byte) (bool, error) {
	needSave := false

	// 七组渠道都按配置顺序故障转移，统一迁移历史上的其他模式。
	for _, set := range candidate.channelSets() {
		if *set.loadBalance != "failover" {
			*set.loadBalance = "failover"
			needSave = true
		}
	}

	// 只解码需要区分“缺失”和“零值”的字段，避免逐层重复解析同一份配置。
	var fields struct {
		FuzzyModeEnabled  json.RawMessage              `json:"fuzzyModeEnabled"`
		ResponsesUpstream []map[string]json.RawMessage `json:"responsesUpstream"`
		Settings          struct {
			ContentSafety json.RawMessage            `json:"contentSafety"`
			Network       map[string]json.RawMessage `json:"network"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(rawJSON, &fields); err != nil {
		return false, fmt.Errorf("解析配置迁移字段失败: %w", err)
	}
	if len(fields.FuzzyModeEnabled) == 0 {
		candidate.FuzzyModeEnabled = true
		needSave = true
		log.Printf("[Config-Migration] FuzzyModeEnabled 字段不存在，设为默认值 true")
	}
	for index := range candidate.ResponsesUpstream {
		if index >= len(fields.ResponsesUpstream) {
			break
		}
		_, capabilityConfigured := fields.ResponsesUpstream[index]["visionCapable"]
		_, layerConfigured := fields.ResponsesUpstream[index]["visionLayerEnabled"]
		if !capabilityConfigured && !layerConfigured {
			candidate.ResponsesUpstream[index].VisionCapable = true
			candidate.ResponsesUpstream[index].VisionLayerEnabled = false
			candidate.ResponsesUpstream[index].VisionLayerChannelID = ""
			candidate.ResponsesUpstream[index].VisionLayerModel = ""
			needSave = true
			log.Printf("[Config-Migration] Responses 渠道 [%d] 默认启用图片理解", index)
		}
	}
	if len(fields.Settings.ContentSafety) == 0 {
		candidate.Settings.ContentSafety = DefaultContentSafetyConfig()
		needSave = true
		log.Printf("[Config-Migration] Settings.ContentSafety 字段不存在，已写入默认配置")
	} else if migrateContentSafetyConfig(&candidate.Settings.ContentSafety, fields.Settings.ContentSafety) {
		needSave = true
		log.Printf("[Config-Migration] 内容安全配置已迁移到分离的 PII/凭据策略")
	}
	// 旧版代理地址非空即生效，仅在缺少开关时保持这个行为。
	if _, hasEnabled := fields.Settings.Network["upstreamProxyEnabled"]; !hasEnabled &&
		strings.TrimSpace(candidate.Settings.Network.UpstreamProxyURL) != "" {
		candidate.Settings.Network.UpstreamProxyEnabled = true
		needSave = true
		log.Printf("[Config-Migration] 旧版全局代理配置缺少启用开关，按已启用迁移")
	}

	for _, set := range candidate.channelSets() {
		normalized, changed, err := ensurePoolsAndAssignments(*set.pools, *set.upstreams)
		if err != nil {
			return false, fmt.Errorf("%s 分组配置无效: %w", set.name, err)
		}
		*set.pools = normalized
		needSave = needSave || changed
	}

	return needSave, nil
}

func migrateContentSafetyConfig(settings *ContentSafetyConfig, rawJSON []byte) bool {
	if settings == nil {
		return false
	}
	defaults := DefaultContentSafetyConfig()
	if isNullJSON(rawJSON) {
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

func isNullJSON(raw []byte) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// migrateOldFormatIn 在候选上迁移旧格式配置，返回是否有迁移。
func migrateOldFormatIn(candidate *Config) bool {
	changed := false
	for _, set := range candidate.channelSets() {
		current := 0
		switch set.kind {
		case "messages":
			current = candidate.CurrentUpstream
		case "responses":
			current = candidate.CurrentResponsesUpstream
		}
		changed = migrateUpstreamsList(*set.upstreams, current, set.name) || changed
	}
	return changed
}

func normalizeChannelListsIn(candidate *Config) bool {
	changed := false
	lists := upstreamLists(candidate)
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

// clearUnsupportedClientIdentityIn 清除非 Messages / Responses 渠道上残留的客户端身份模式。
// 这些协议没有伪装能力，设置不会生效；清除后配置文件、界面与运行时行为保持一致。
func clearUnsupportedClientIdentityIn(candidate *Config) bool {
	changed := false
	for _, set := range candidate.channelSets() {
		if set.kind == "messages" || set.kind == "responses" {
			continue
		}
		for index := range *set.upstreams {
			upstream := &(*set.upstreams)[index]
			if upstream.ClientIdentityMode != "" {
				log.Printf("[Config-Migration] 渠道 %q 所属协议不支持客户端伪装，已清除身份模式设置", upstream.Name)
				upstream.ClientIdentityMode = ""
				changed = true
			}
		}
	}
	return changed
}

// migrateUpstreamsList 迁移单个渠道列表
func migrateUpstreamsList(upstreams []UpstreamConfig, currentIdx int, name string) bool {
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

// validateChannelKeysIn 在候选上自检渠道密钥配置：
// 没有配置 API key 的渠道，即使状态为 active 也应暂停。
// 返回 true 表示有配置被修改，需要保存。
func validateChannelKeysIn(candidate *Config) bool {
	changed := false
	for _, set := range candidate.channelSets() {
		changed = validateUpstreamKeys(*set.upstreams, set.name) || changed
	}
	return changed
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

// validateProxyConfigOf 校验候选配置的代理设置（不触碰当前快照）。
func validateProxyConfigOf(candidate *Config) error {
	if err := validateIntentRouting(candidate); err != nil {
		return err
	}
	if err := ValidateProxyURL(candidate.Settings.Network.UpstreamProxyURL); err != nil {
		return fmt.Errorf("全局上游代理配置无效: %w", err)
	}
	groups := upstreamLists(candidate)
	for _, upstreams := range groups {
		for index := range upstreams {
			if err := validateUpstreamProxyConfig(&upstreams[index]); err != nil {
				return fmt.Errorf("渠道 %q 的代理配置无效: %w", upstreams[index].Name, err)
			}
		}
	}
	return nil
}

// saveConfigLocked 提交配置变更（R3 唯一提交入口，调用方必须已持锁）。
// 顺序：序列化候选 → 备份旧文件 → 原子替换（提交点）→ 发布新快照。
//   - 提交点前失败：原文件保持原样，当前快照不变，返回普通错误（未提交）；
//   - 提交点后失败（目录同步等）：文件已是新内容，快照同样发布，
//     返回 *atomicfile.CommittedError（调用方不得按未提交重试）。
func (cm *ConfigManager) saveConfigLocked(config Config) error {
	if err := validateIntentRouting(&config); err != nil {
		return err
	}
	if err := validateFuzzyModeProtocols(&config); err != nil {
		return err
	}
	if err := materializeFreeChannels(&config); err != nil {
		return err
	}
	// 保存前清除不支持伪装的协议池上的身份模式，保证落盘内容与运行时一致。
	clearUnsupportedClientIdentityIn(&config)
	if err := validateConcurrency(&config); err != nil {
		return err
	}
	// 清理已废弃字段，确保不会被序列化到 JSON
	config.CurrentUpstream = 0
	config.CurrentResponsesUpstream = 0

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// 备份旧文件（备份失败按未提交处理，不写新内容）
	if err := cm.backupConfig(); err != nil {
		return fmt.Errorf("备份当前配置失败: %w", err)
	}

	// 原子替换（唯一提交点）
	err = atomicfile.Write(cm.configFile, data, atomicfile.Options{
		TempPrefix: ".config-",
		Mode:       0600, // 配置含密钥，限制文件权限。
	})
	if err == nil || atomicfile.IsCommitted(err) {
		cm.publishConfigLocked(config)
		cm.persistedDigest = configDigest(data)
	}
	return err
}

// SaveConfig 保存配置
func (cm *ConfigManager) SaveConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.saveConfigLocked(cm.config)
}

// backupConfig 备份配置。失败显式返回（提交前失败语义），
// 不再静默吞掉——备份失败时中止本次写入。
func (cm *ConfigManager) backupConfig() error {
	if _, err := os.Stat(cm.configFile); os.IsNotExist(err) {
		return nil
	}

	backupDir := filepath.Join(filepath.Dir(cm.configFile), "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("创建备份目录失败: %w", err)
	}

	// 读取当前配置
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	// 创建备份文件
	timestamp := time.Now().Format("2006-01-02T15-04-05.000000000")
	backup, err := os.CreateTemp(backupDir, "config-"+timestamp+"-*.json")
	if err != nil {
		return fmt.Errorf("创建配置备份失败: %w", err)
	}
	_, writeErr := backup.Write(data)
	if err := errors.Join(writeErr, backup.Close()); err != nil {
		return fmt.Errorf("写入备份文件失败: %w", err)
	}

	// 清理旧备份
	return cm.cleanupOldBackups(backupDir)
}

// cleanupOldBackups 清理旧备份
func (cm *ConfigManager) cleanupOldBackups(backupDir string) error {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return fmt.Errorf("读取配置备份目录失败: %w", err)
	}

	backups := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "config-") && strings.HasSuffix(entry.Name(), ".json") {
			backups = append(backups, entry.Name())
		}
	}

	// 删除最旧的备份
	for _, name := range backups[:max(0, len(backups)-maxBackups)] {
		if err := os.Remove(filepath.Join(backupDir, name)); err != nil {
			return fmt.Errorf("清理旧配置备份失败: %w", err)
		}
	}
	return nil
}

// watcherTargetPath 目标配置文件的规范化绝对路径（事件过滤基准）。
func (cm *ConfigManager) watcherTargetPath() string {
	abs, err := filepath.Abs(cm.configFile)
	if err != nil {
		return filepath.Clean(cm.configFile)
	}
	return filepath.Clean(abs)
}

// isRelevantWatcherEvent 判断事件是否指向目标配置文件：
// 按规范化后的路径比较（Windows 大小写不敏感），忽略临时文件与备份。
// 原子替换会以 Create + Rename 组合出现，外部编辑器以 Write/Create 出现，
// 短暂删除以 Remove 出现（后续文件恢复时 Create 再触发）。
func (cm *ConfigManager) isRelevantWatcherEvent(eventPath string) bool {
	abs, err := filepath.Abs(eventPath)
	if err != nil {
		abs = eventPath
	}
	abs = filepath.Clean(abs)
	target := cm.watcherTargetPath()
	if strings.EqualFold(filepath.ToSlash(abs), filepath.ToSlash(target)) {
		return true
	}
	return false
}

// startWatcher 监听配置所在目录（而非目标文件本身——原子替换会改变文件
// 对象，监听文件的句柄会失效），按目标路径过滤事件，合并短时间事件后重载。
func (cm *ConfigManager) startWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	cm.watcher = watcher

	if err := watcher.Add(filepath.Dir(cm.watcherTargetPath())); err != nil {
		_ = watcher.Close()
		cm.watcher = nil
		return err
	}

	cm.workers.Add(1)
	go cm.watchLoop()
	return nil
}

// watchLoop 是 watcher 事件主循环：事件合并（防抖）后重载目标文件。
func (cm *ConfigManager) watchLoop() {
	defer cm.workers.Done()
	var debounce *time.Timer
	var debounceC <-chan time.Time
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()

	for {
		select {
		case <-cm.stopChan:
			return
		case <-debounceC:
			debounceC = nil
			// 防抖重载归 watcher 自己执行，Close 能等待它完成，不遗留 AfterFunc 回调。
			log.Printf("[Config-Watcher] 检测到配置文件变化，重载配置...")
			if err := cm.reloadFromDisk(); err != nil {
				log.Printf("[Config-Watcher] 警告: 配置重载失败: %v", err)
			}
		case event, ok := <-cm.watcher.Events:
			if !ok {
				return
			}
			// 只关心目标文件的变化（Write/Create/Rename/Remove）。
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			if !cm.isRelevantWatcherEvent(event.Name) {
				continue
			}
			// 合并短时间事件：重置防抖定时器。
			if debounce == nil {
				debounce = time.NewTimer(reloadDebounce)
			} else {
				debounce.Reset(reloadDebounce)
			}
			debounceC = debounce.C
		case err, ok := <-cm.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[Config-Watcher] 警告: 文件监听错误: %v", err)
		}
	}
}

// Close 关闭 ConfigManager 并释放资源（幂等，可安全多次调用）。
// watcher 与生命周期工作先于下游资源退出。
func (cm *ConfigManager) Close() error {
	cm.closeOnce.Do(func() {
		// 通知所有 goroutine 停止
		if cm.stopChan != nil {
			close(cm.stopChan)
		}

		// 关闭文件监听器
		if cm.watcher != nil {
			cm.closeErr = cm.watcher.Close()
		}
		cm.workers.Wait()
	})
	return cm.closeErr
}
