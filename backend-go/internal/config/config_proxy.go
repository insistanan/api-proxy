package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

func proxyConfigErrorf(format string, args ...interface{}) error {
	return &ConfigError{Message: fmt.Sprintf(format, args...)}
}

const (
	ProxyModeInherit = "inherit"
	ProxyModeDirect  = "direct"
	ProxyModeCustom  = "custom"
)

// ValidateProxyURL 验证 HTTP Transport 支持的代理地址。
func ValidateProxyURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return proxyConfigErrorf("代理地址格式无效")
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return proxyConfigErrorf("代理地址必须包含协议和主机，例如 http://127.0.0.1:7897")
	}
	if parsed.Hostname() == "" {
		return proxyConfigErrorf("代理地址必须包含有效主机")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return proxyConfigErrorf("不支持的代理协议 %q，仅支持 http、https、socks5 和 socks5h", parsed.Scheme)
	}
	if parsed.Fragment != "" || parsed.RawQuery != "" {
		return proxyConfigErrorf("代理地址不能包含查询参数或片段")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return proxyConfigErrorf("代理地址不能包含路径")
	}
	return nil
}

func normalizeProxyMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return ProxyModeInherit
	}
	return mode
}

func normalizeUpstreamProxyConfig(upstream *UpstreamConfig) {
	if upstream == nil {
		return
	}
	upstream.ProxyMode = normalizeProxyMode(upstream.ProxyMode)
	upstream.ProxyURL = strings.TrimSpace(upstream.ProxyURL)
	if upstream.ProxyMode != ProxyModeCustom {
		upstream.ProxyURL = ""
	}
}

func validateUpstreamProxyConfig(upstream *UpstreamConfig) error {
	if upstream == nil {
		return proxyConfigErrorf("渠道配置不能为空")
	}
	switch normalizeProxyMode(upstream.ProxyMode) {
	case ProxyModeInherit, ProxyModeDirect:
		return nil
	case ProxyModeCustom:
		if strings.TrimSpace(upstream.ProxyURL) == "" {
			return proxyConfigErrorf("使用独立代理时必须填写代理地址")
		}
		if err := ValidateProxyURL(upstream.ProxyURL); err != nil {
			return fmt.Errorf("渠道代理配置无效: %w", err)
		}
		return nil
	default:
		return proxyConfigErrorf("不支持的渠道代理模式 %q", upstream.ProxyMode)
	}
}

// ResolveUpstreamProxyURL 解析渠道最终使用的代理地址；空字符串表示直连。
func (cm *ConfigManager) ResolveUpstreamProxyURL(upstream *UpstreamConfig) (string, error) {
	if upstream == nil {
		return "", proxyConfigErrorf("无法解析空渠道的代理配置")
	}

	switch normalizeProxyMode(upstream.ProxyMode) {
	case ProxyModeDirect:
		return "", nil
	case ProxyModeCustom:
		if err := validateUpstreamProxyConfig(upstream); err != nil {
			return "", err
		}
		return strings.TrimSpace(upstream.ProxyURL), nil
	case ProxyModeInherit:
		cm.mu.RLock()
		network := cm.config.Settings.Network
		cm.mu.RUnlock()
		// 全局代理开关未启用时按直连处理，避免残留的代理地址强制走代理
		if !network.UpstreamProxyEnabled {
			return "", nil
		}
		proxyURL := strings.TrimSpace(network.UpstreamProxyURL)
		if err := ValidateProxyURL(proxyURL); err != nil {
			return "", fmt.Errorf("全局上游代理配置无效: %w", err)
		}
		return proxyURL, nil
	default:
		return "", proxyConfigErrorf("不支持的渠道代理模式 %q", upstream.ProxyMode)
	}
}

func (cm *ConfigManager) GetSettings() SettingsConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cloneSettingsConfig(cm.config.Settings)
}

// UpdateSettings 原子更新可由管理界面维护的全部全局设置。
func (cm *ConfigManager) UpdateSettings(settings SettingsConfig) error {
	settings.Network.UpstreamProxyURL = strings.TrimSpace(settings.Network.UpstreamProxyURL)
	if err := ValidateProxyURL(settings.Network.UpstreamProxyURL); err != nil {
		return err
	}
	if err := ValidateContentSafetyConfig(settings.ContentSafety); err != nil {
		return err
	}
	settings = cloneSettingsConfig(settings)

	cm.mu.Lock()
	defer cm.mu.Unlock()
	previous := cloneSettingsConfig(cm.config.Settings)
	cm.config.Settings = settings
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.config.Settings = previous
		return err
	}
	return nil
}

// UpdateNetworkSettings 全量设置网络分组。
// 传入空 UpstreamProxyURL 表示清空全局代理地址；是否实际生效由
// ResolveUpstreamProxyURL 依据 UpstreamProxyEnabled 开关统一判定。
func (cm *ConfigManager) UpdateNetworkSettings(settings NetworkSettings) error {
	settings.UpstreamProxyURL = strings.TrimSpace(settings.UpstreamProxyURL)
	if err := ValidateProxyURL(settings.UpstreamProxyURL); err != nil {
		return err
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()
	previous := cm.config.Settings.Network
	cm.config.Settings.Network = settings
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.config.Settings.Network = previous
		return err
	}
	return nil
}

// ValidateContentSafetyConfig 校验内容安全规则选择，拒绝未知、空白或重复配置。
func ValidateContentSafetyConfig(settings ContentSafetyConfig) error {
	if err := validateSensitiveDataMode(settings.SensitiveData.Mode); err != nil {
		return err
	}
	if settings.SensitiveWord.Enabled &&
		!settings.SensitiveWord.PornographyEnabled &&
		!settings.SensitiveWord.GamblingEnabled &&
		!settings.SensitiveWord.DrugsEnabled &&
		!settings.SensitiveWord.ViolenceTerrorEnabled &&
		!settings.SensitiveWord.PoliticalEnabled &&
		!settings.SensitiveWord.IllegalCrimeEnabled &&
		len(settings.SensitiveWord.CustomWords) == 0 {
		return proxyConfigErrorf("敏感词检测已启用，但未选择任何分类或自定义词")
	}
	if err := validateEnabledRuleSelection("敏感信息", settings.SensitiveInfo.Enabled, settings.SensitiveInfo.EnabledRules); err != nil {
		return err
	}
	if err := validateSafetyMode("敏感信息", settings.SensitiveInfo.Mode, true); err != nil {
		return err
	}
	if err := validateRuleSelection(
		"敏感信息",
		settings.SensitiveInfo.EnabledRules,
		map[string]struct{}{
			SensitiveInfoRulePhone:     {},
			SensitiveInfoRuleIDCard:    {},
			SensitiveInfoRuleEmail:     {},
			SensitiveInfoRuleIPAddress: {},
			SensitiveInfoRuleBankCard:  {},
		},
	); err != nil {
		return err
	}
	if err := validateIPMaskScope(settings.SensitiveInfo.IPMaskScope); err != nil {
		return err
	}
	if err := validateEnabledRuleSelection("凭据", settings.Credential.Enabled, settings.Credential.EnabledRules); err != nil {
		return err
	}
	if err := validateSafetyMode("凭据用户输入", settings.Credential.UserInputMode, true); err != nil {
		return err
	}
	if err := validateSafetyMode("凭据工具结果", settings.Credential.ToolResultMode, false); err != nil {
		return err
	}
	if err := validateSafetyMode("凭据工具参数", settings.Credential.ToolArgumentMode, false); err != nil {
		return err
	}
	if err := validateRuleSelection(
		"凭据",
		settings.Credential.EnabledRules,
		map[string]struct{}{
			CredentialRuleAPIKey:           {},
			CredentialRuleNamedSecret:      {},
			CredentialRulePrivateKey:       {},
			CredentialRuleConnectionString: {},
			CredentialRuleHighEntropy:      {},
		},
	); err != nil {
		return err
	}
	if err := validateEnabledRuleSelection("危险命令", settings.DangerousCmd.Enabled, settings.DangerousCmd.EnabledRules); err != nil {
		return err
	}
	if err := validateRuleSelection(
		"危险命令",
		settings.DangerousCmd.EnabledRules,
		map[string]struct{}{
			DangerousCmdRuleDestructive:          {},
			DangerousCmdRuleDownloadExecute:      {},
			DangerousCmdRuleReverseShell:         {},
			DangerousCmdRulePrivilegeEscalation:  {},
			DangerousCmdRuleEnvironmentTampering: {},
		},
	); err != nil {
		return err
	}
	if err := validateDangerousCmdAIGate(settings.DangerousCmd); err != nil {
		return err
	}
	if err := validateAllowlistConfig(settings.Allowlist); err != nil {
		return err
	}
	if err := validateCustomRulesConfig(settings.CustomRules, settings.SensitiveData.Mode); err != nil {
		return err
	}

	seenWords := make(map[string]struct{}, len(settings.SensitiveWord.CustomWords))
	for _, word := range settings.SensitiveWord.CustomWords {
		if strings.TrimSpace(word) == "" {
			return proxyConfigErrorf("自定义敏感词不能为空")
		}
		if _, exists := seenWords[word]; exists {
			return proxyConfigErrorf("自定义敏感词 %q 重复", word)
		}
		seenWords[word] = struct{}{}
	}
	if err := validateWhitelistConfig(settings.Whitelist); err != nil {
		return err
	}
	return nil
}

func validateSensitiveDataMode(mode string) error {
	switch mode {
	case ContentSafetyModeBlock, ContentSafetyModeMask:
		return nil
	default:
		return proxyConfigErrorf("敏感数据处置模式必须是 block 或 mask，当前为 %q", mode)
	}
}

// validateDangerousCmdAIGate 校验危险命令 AI 闸门设置。
// 闸门依赖危险命令检测本体：本体关闭时闸门一并拒绝，避免"闸门开了却永远不触发"的假配置。
func validateDangerousCmdAIGate(cmd DangerousCmdConfig) error {
	if !cmd.AIGate.Enabled {
		return nil
	}
	if !cmd.Enabled {
		return proxyConfigErrorf("危险命令 AI 闸门依赖危险命令检测：请先启用危险命令检测")
	}
	if cmd.AIGate.WaitSeconds == 0 {
		return nil // 0 表示使用默认值
	}
	if cmd.AIGate.WaitSeconds < 3 || cmd.AIGate.WaitSeconds > 120 {
		return proxyConfigErrorf("危险命令 AI 闸门等待秒数必须在 3 到 120 之间，当前为 %d", cmd.AIGate.WaitSeconds)
	}
	return nil
}

// validateAllowlistConfig 校验字符串级放行字面量。
func validateAllowlistConfig(allowlist AllowlistConfig) error {
	if !allowlist.Enabled {
		return nil
	}
	seen := make(map[string]struct{}, len(allowlist.Entries))
	for _, entry := range allowlist.Entries {
		if strings.TrimSpace(entry) == "" {
			return proxyConfigErrorf("放行字面量不能为空或纯空白")
		}
		if len([]rune(entry)) > 256 {
			return proxyConfigErrorf("单条放行字面量不能超过 256 个字符")
		}
		if _, exists := seen[entry]; exists {
			return proxyConfigErrorf("放行字面量 %q 重复", entry)
		}
		seen[entry] = struct{}{}
	}
	if len(allowlist.Entries) > 1000 {
		return proxyConfigErrorf("放行字面量最多 1000 条，当前 %d 条", len(allowlist.Entries))
	}
	return nil
}

// validateCustomRulesConfig 校验自定义检测规则。
// 自定义规则只有可逆脱敏一种处置，sensitiveData.mode=block 下启用属于假配置，直接拒绝。
func validateCustomRulesConfig(rules CustomRulesConfig, sensitiveDataMode string) error {
	if !rules.Enabled {
		return nil
	}
	if sensitiveDataMode != ContentSafetyModeMask {
		return proxyConfigErrorf("自定义检测规则依赖可逆脱敏：请将敏感数据处置切换为可逆脱敏（当前 %q）", sensitiveDataMode)
	}
	if len(rules.Rules) == 0 {
		return proxyConfigErrorf("自定义检测规则已启用，但没有配置任何规则")
	}
	if len(rules.Rules) > 50 {
		return proxyConfigErrorf("自定义检测规则最多 50 条，当前 %d 条", len(rules.Rules))
	}
	seenNames := make(map[string]struct{}, len(rules.Rules))
	seenTypes := make(map[string]struct{}, len(rules.Rules))
	for _, rule := range rules.Rules {
		if strings.TrimSpace(rule.Name) == "" {
			return proxyConfigErrorf("自定义规则名不能为空")
		}
		if len([]rune(rule.Name)) > 64 {
			return proxyConfigErrorf("自定义规则名不能超过 64 个字符")
		}
		if _, exists := seenNames[rule.Name]; exists {
			return proxyConfigErrorf("自定义规则名 %q 重复", rule.Name)
		}
		seenNames[rule.Name] = struct{}{}
		typeName := strings.TrimSpace(rule.Type)
		if typeName == "" {
			return proxyConfigErrorf("自定义规则 %q 缺少占位符类型前缀", rule.Name)
		}
		if !validCustomRuleType(typeName) {
			return proxyConfigErrorf("自定义规则 %q 的类型前缀 %q 只能包含大写字母、数字和下划线（1-16 位）", rule.Name, typeName)
		}
		if _, exists := seenTypes[typeName]; exists {
			return proxyConfigErrorf("自定义规则类型前缀 %q 重复", typeName)
		}
		seenTypes[typeName] = struct{}{}
		if strings.TrimSpace(rule.Pattern) == "" {
			return proxyConfigErrorf("自定义规则 %q 缺少正则表达式", rule.Name)
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return proxyConfigErrorf("自定义规则 %q 的正则无法编译: %v", rule.Name, err)
		}
	}
	return nil
}

func validCustomRuleType(value string) bool {
	if len(value) < 1 || len(value) > 16 {
		return false
	}
	for _, current := range value {
		if current >= 'A' && current <= 'Z' || current >= '0' && current <= '9' || current == '_' {
			continue
		}
		return false
	}
	return true
}

func validateWhitelistConfig(settings WhitelistConfig) error {
	if !settings.Enabled {
		return nil
	}
	seen := make(map[string]struct{}, len(settings.ToolNames))
	for _, name := range settings.ToolNames {
		name = strings.TrimSpace(name)
		if name == "" {
			return proxyConfigErrorf("白名单工具名不能为空")
		}
		if _, exists := seen[name]; exists {
			return proxyConfigErrorf("白名单工具名 %q 重复", name)
		}
		seen[name] = struct{}{}
	}
	if len(seen) == 0 {
		return proxyConfigErrorf("白名单已启用，但未配置任何工具名")
	}
	return nil
}

func validateEnabledRuleSelection(label string, enabled bool, rules []string) error {
	if enabled && len(rules) == 0 {
		return proxyConfigErrorf("%s已启用，但未选择任何规则", label)
	}
	return nil
}

func validateSafetyMode(label, mode string, allowMask bool) error {
	switch mode {
	case ContentSafetyModeAudit, ContentSafetyModeBlock:
		return nil
	case ContentSafetyModeMask:
		if allowMask {
			return nil
		}
	}
	return proxyConfigErrorf("%s处理模式 %q 无效", label, mode)
}

func validateIPMaskScope(scope string) error {
	switch strings.TrimSpace(scope) {
	case SensitiveInfoIPMaskScopePublic, SensitiveInfoIPMaskScopeAll:
		return nil
	case "":
		return proxyConfigErrorf("IP 掩码范围不能为空")
	default:
		return proxyConfigErrorf("不支持的 IP 掩码范围 %q", scope)
	}
}

func validateRuleSelection(label string, rules []string, allowed map[string]struct{}) error {
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule) == "" {
			return proxyConfigErrorf("%s规则名不能为空", label)
		}
		if _, exists := allowed[rule]; !exists {
			return proxyConfigErrorf("不支持的%s规则 %q", label, rule)
		}
		if _, exists := seen[rule]; exists {
			return proxyConfigErrorf("%s规则 %q 重复", label, rule)
		}
		seen[rule] = struct{}{}
	}
	return nil
}

func cloneSettingsConfig(settings SettingsConfig) SettingsConfig {
	settings.ContentSafety.SensitiveWord.CustomWords = append([]string{}, settings.ContentSafety.SensitiveWord.CustomWords...)
	settings.ContentSafety.SensitiveInfo.EnabledRules = append([]string{}, settings.ContentSafety.SensitiveInfo.EnabledRules...)
	settings.ContentSafety.Credential.EnabledRules = append([]string{}, settings.ContentSafety.Credential.EnabledRules...)
	settings.ContentSafety.DangerousCmd.EnabledRules = append([]string{}, settings.ContentSafety.DangerousCmd.EnabledRules...)
	settings.ContentSafety.Whitelist.ToolNames = append([]string{}, settings.ContentSafety.Whitelist.ToolNames...)
	return settings
}

func (cm *ConfigManager) validateProxyConfig() error {
	if err := ValidateProxyURL(cm.config.Settings.Network.UpstreamProxyURL); err != nil {
		return fmt.Errorf("全局上游代理配置无效: %w", err)
	}
	groups := [][]UpstreamConfig{
		cm.config.Upstream,
		cm.config.ResponsesUpstream,
		cm.config.GeminiUpstream,
		cm.config.ChatUpstream,
		cm.config.ImagesUpstream,
	}
	for _, upstreams := range groups {
		for index := range upstreams {
			if err := validateUpstreamProxyConfig(&upstreams[index]); err != nil {
				return fmt.Errorf("渠道 %q 的代理配置无效: %w", upstreams[index].Name, err)
			}
		}
	}
	return nil
}
