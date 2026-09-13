package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/BenedictKing/api-proxy/internal/utils"

	"github.com/fsnotify/fsnotify"
)

// ============== 核心类型定义 ==============

// UpstreamConfig 上游配置
type UpstreamConfig struct {
	ID                 string              `json:"id,omitempty"`     // 稳定渠道标识，避免数组位置变化影响关联状态
	PoolID             string              `json:"poolId,omitempty"` // 所属渠道分组；空值迁移到兜底分组
	BaseURL            string              `json:"baseUrl"`
	BaseURLs           []string            `json:"baseUrls,omitempty"` // 多 BaseURL 支持（failover 模式）
	APIKeys            []string            `json:"apiKeys"`
	HistoricalAPIKeys  []string            `json:"historicalApiKeys,omitempty"` // 历史 API Key（用于统计聚合，换 Key 后保留旧 Key 的统计数据）
	ServiceType        string              `json:"serviceType"`                 // gemini, openai, claude
	Name               string              `json:"name,omitempty"`
	Description        string              `json:"description,omitempty"`
	Website            string              `json:"website,omitempty"`
	InsecureSkipVerify bool                `json:"insecureSkipVerify,omitempty"`
	ProxyMode          string              `json:"proxyMode,omitempty"` // inherit（继承全局）、direct（直连）、custom（独立代理）
	ProxyURL           string              `json:"proxyUrl,omitempty"`
	ModelMapping       map[string][]string `json:"modelMapping,omitempty"` // 模型重定向：源模型 -> 目标模型列表（支持多个备选）
	DefaultModel       string              `json:"defaultModel,omitempty"`
	// 多渠道调度相关字段
	Priority                int        `json:"priority"`                          // 渠道优先级（数字越小优先级越高，默认按索引）
	Status                  string     `json:"status"`                            // 渠道状态：active（正常）, suspended（暂停）, disabled（备用池）
	PromotionUntil          *time.Time `json:"promotionUntil,omitempty"`          // 促销期截止时间，在此期间内优先使用此渠道（忽略trace亲和）
	PromotionCount          int        `json:"promotionCount,omitempty"`          // 促销期剩余请求次数，每次成功请求递减，到0自动清除
	LowQuality              bool       `json:"lowQuality,omitempty"`              // 低质量渠道标记：启用后强制本地估算 token，偏差>5%时使用本地值
	VisionCapable           bool       `json:"visionCapable"`                     // 渠道是否原生支持图片理解，可直接接收图片，也可被图片理解层调用
	ExcludeFromConversation bool       `json:"excludeFromConversation,omitempty"` // 不参与常规对话调度，仅可作为图片理解渠道使用
	VisionLayerEnabled      bool       `json:"visionLayerEnabled,omitempty"`      // 是否为不支持图片的当前渠道指定优先图片理解渠道
	VisionLayerChannelID    string     `json:"visionLayerChannelId,omitempty"`    // 优先调用的稳定渠道标识；未启用时自动使用公共图片理解池
	VisionLayerModel        string     `json:"visionLayerModel,omitempty"`        // 可选：覆盖透传给图片理解渠道的模型名
	Temporary               bool       `json:"temporary,omitempty"`               // 临时渠道：到期后自动移入弃用池
	TemporaryUntil          *time.Time `json:"temporaryUntil,omitempty"`          // 临时渠道到期时间
	DeprecatedAt            *time.Time `json:"deprecatedAt,omitempty"`            // 移入弃用池时间
	// Gemini 特定配置
	InjectDummyThoughtSignature bool `json:"injectDummyThoughtSignature,omitempty"` // 给空 thought_signature 注入 dummy 值（兼容 x666.me 等要求必须有该字段的 API）
	StripThoughtSignature       bool `json:"stripThoughtSignature,omitempty"`       // 移除 thought_signature 字段（兼容旧版 Gemini API）
	IncludeHistoryThinking      bool `json:"includeHistoryThinking,omitempty"`      // 是否将历史 reasoning/thinking 回灌上游
	DisablePromptCacheInjection bool `json:"disablePromptCacheInjection,omitempty"` // 是否禁用 Claude Prompt Cache 自动断点注入
	DisablePromptCacheKey       bool `json:"disablePromptCacheKey,omitempty"`       // 是否禁用兼容上游的 prompt_cache_key
	RequireReasoningContent     bool `json:"requireReasoningContent,omitempty"`     // 严格渠道要求所有缺失的 assistant reasoning_content 使用兼容续接值
	EnablePreviousResponseID    bool `json:"enablePreviousResponseID,omitempty"`    // Messages->Responses 是否启用 previous_response_id 链
}

// UnmarshalJSON 兼容旧配置中 modelMapping 的一对一字符串格式：
//
//	"modelMapping": { "opus": "gpt-4o" }
//
// 当前内部统一使用一对多格式：
//
//	"modelMapping": { "opus": ["gpt-4o"] }
func (u *UpstreamConfig) UnmarshalJSON(data []byte) error {
	type upstreamConfigAlias UpstreamConfig

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var modelMapping map[string][]string
	if rawModelMapping, ok := raw["modelMapping"]; ok {
		delete(raw, "modelMapping")
		parsed, err := decodeModelMapping(rawModelMapping)
		if err != nil {
			return fmt.Errorf("modelMapping: %w", err)
		}
		modelMapping = parsed
	}

	withoutModelMapping, err := json.Marshal(raw)
	if err != nil {
		return err
	}

	var alias upstreamConfigAlias
	if err := json.Unmarshal(withoutModelMapping, &alias); err != nil {
		return err
	}

	*u = UpstreamConfig(alias)
	if modelMapping != nil {
		u.ModelMapping = modelMapping
	}
	return nil
}

// UpstreamUpdate 用于部分更新 UpstreamConfig
type UpstreamUpdate struct {
	Name               *string             `json:"name"`
	PoolID             *string             `json:"poolId"`
	ServiceType        *string             `json:"serviceType"`
	BaseURL            *string             `json:"baseUrl"`
	BaseURLs           []string            `json:"baseUrls"`
	APIKeys            []string            `json:"apiKeys"`
	Description        *string             `json:"description"`
	Website            *string             `json:"website"`
	InsecureSkipVerify *bool               `json:"insecureSkipVerify"`
	ProxyMode          *string             `json:"proxyMode"`
	ProxyURL           *string             `json:"proxyUrl"`
	ModelMapping       map[string][]string `json:"modelMapping"` // 支持一对多映射
	DefaultModel       *string             `json:"defaultModel"`
	// 多渠道调度相关字段
	Priority                *int       `json:"priority"`
	Status                  *string    `json:"status"`
	PromotionUntil          *time.Time `json:"promotionUntil"`
	PromotionCount          *int       `json:"promotionCount"`
	LowQuality              *bool      `json:"lowQuality"`
	VisionCapable           *bool      `json:"visionCapable"`
	ExcludeFromConversation *bool      `json:"excludeFromConversation"`
	VisionLayerEnabled      *bool      `json:"visionLayerEnabled"`
	VisionLayerChannelID    *string    `json:"visionLayerChannelId"`
	VisionLayerModel        *string    `json:"visionLayerModel"`
	Temporary               *bool      `json:"temporary"`
	TemporaryUntil          *time.Time `json:"temporaryUntil"`
	DeprecatedAt            *time.Time `json:"deprecatedAt"`
	// Gemini 特定配置
	InjectDummyThoughtSignature *bool `json:"injectDummyThoughtSignature"`
	StripThoughtSignature       *bool `json:"stripThoughtSignature"`
	IncludeHistoryThinking      *bool `json:"includeHistoryThinking"`
	DisablePromptCacheInjection *bool `json:"disablePromptCacheInjection"`
	DisablePromptCacheKey       *bool `json:"disablePromptCacheKey"`
	RequireReasoningContent     *bool `json:"requireReasoningContent"`
	EnablePreviousResponseID    *bool `json:"enablePreviousResponseID"`
}

func (u *UpstreamUpdate) UnmarshalJSON(data []byte) error {
	type upstreamUpdateAlias UpstreamUpdate

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var modelMapping map[string][]string
	if rawModelMapping, ok := raw["modelMapping"]; ok {
		delete(raw, "modelMapping")
		parsed, err := decodeModelMapping(rawModelMapping)
		if err != nil {
			return fmt.Errorf("modelMapping: %w", err)
		}
		modelMapping = parsed
	}

	withoutModelMapping, err := json.Marshal(raw)
	if err != nil {
		return err
	}

	var alias upstreamUpdateAlias
	if err := json.Unmarshal(withoutModelMapping, &alias); err != nil {
		return err
	}

	*u = UpstreamUpdate(alias)
	if modelMapping != nil {
		u.ModelMapping = modelMapping
	}
	return nil
}

func decodeModelMapping(raw json.RawMessage) (map[string][]string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}

	var items map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}

	modelMapping := make(map[string][]string, len(items))
	for source, targetRaw := range items {
		targetRaw = bytes.TrimSpace(targetRaw)
		if len(targetRaw) == 0 || bytes.Equal(targetRaw, []byte("null")) {
			continue
		}

		switch targetRaw[0] {
		case '"':
			var target string
			if err := json.Unmarshal(targetRaw, &target); err != nil {
				return nil, err
			}
			modelMapping[source] = []string{target}
		case '[':
			var targets []string
			if err := json.Unmarshal(targetRaw, &targets); err != nil {
				return nil, err
			}
			modelMapping[source] = targets
		default:
			return nil, fmt.Errorf("映射值必须是字符串或字符串数组")
		}
	}

	return normalizeModelMapping(modelMapping), nil
}

// SettingsConfig 集中承载可由管理界面维护的全局设置，便于后续扩展其他设置分类。
type SettingsConfig struct {
	Network       NetworkSettings     `json:"network"`
	ContentSafety ContentSafetyConfig `json:"contentSafety"`
	Integration   IntegrationSettings `json:"integration"`
	// Assistant AI 配置助手的模型路由设置。
	Assistant AssistantSettings `json:"assistant"`
}

// AssistantSettings AI 配置助手设置。
// Model 是内部调用的模型 ID（取渠道分组的匹配词），由主链路编排按分组匹配
// 选渠；为空表示自动——与主链路对未匹配模型的行为一致，走各协议兜底分组。
// ChannelID 显式指定渠道（稳定渠道 ID）；为空表示自动——按
// messages → responses → chat → gemini 优先级编排选渠。显式指定时
// 不回退不换渠道（显式指定代表用户意图）。
type AssistantSettings struct {
	Model     string `json:"model"`
	ChannelID string `json:"channelId"`
}

// IntegrationSettings 外部工具集成设置。
type IntegrationSettings struct {
	// CCSwitchPath CC Switch 可执行文件路径；为空表示未配置，
	// 渠道菜单的"导入到 CCS"退化为"复制 CCS 链接"。
	CCSwitchPath string `json:"ccSwitchPath"`
}

// NetworkSettings 网络相关设置。
type NetworkSettings struct {
	UpstreamProxyURL     string `json:"upstreamProxyUrl"`
	UpstreamProxyEnabled bool   `json:"upstreamProxyEnabled"`
}

// ContentSafetyConfig 内容安全相关设置。
type ContentSafetyConfig struct {
	SensitiveData SensitiveDataConfig `json:"sensitiveData"`
	SensitiveWord SensitiveWordConfig `json:"sensitiveWord"`
	SensitiveInfo SensitiveInfoConfig `json:"sensitiveInfo"`
	Credential    CredentialConfig    `json:"credential"`
	DangerousCmd  DangerousCmdConfig  `json:"dangerousCmd"`
	Whitelist     WhitelistConfig     `json:"whitelist"`
	// Allowlist 字符串级放行字面量：条目与命中片段完全相等时跳过全部检测维度
	// （整段相等则跳过该 segment；子串相等则丢弃该命中）。与 Whitelist（按工具名
	// 放行 tool_result/tool_argument）语义不同，勿混用。
	Allowlist   AllowlistConfig   `json:"allowlist"`
	CustomRules CustomRulesConfig `json:"customRules"`
}

// AllowlistConfig 字符串级放行字面量设置。
type AllowlistConfig struct {
	Enabled bool     `json:"enabled"`
	Entries []string `json:"entries"`
}

// CustomRulesConfig 自定义检测规则。命中走可逆脱敏（占位符 + 回答还原），
// 仅在 sensitiveData.mode=mask 时参与；block 模式下无意义（没有拦截语义）。
type CustomRulesConfig struct {
	Enabled bool             `json:"enabled"`
	Rules   []CustomRuleItem `json:"rules"`
}

// CustomRuleItem 是一条用户自定义的正则检测规则。
type CustomRuleItem struct {
	Name    string `json:"name"`
	Type    string `json:"type"`    // 占位符类型前缀，[A-Z0-9_]{1,16}，如 CUSTOM / CODENAME
	Pattern string `json:"pattern"` // Go RE2 正则（结构免疫灾难性回溯）
}

// SensitiveDataConfig 统一控制敏感信息与凭据命中后的处置方式。
// 各检测分组仍负责决定检测哪些内置规则。
type SensitiveDataConfig struct {
	Mode string `json:"mode"`
}

// SensitiveWordConfig 敏感词检测设置。
// CustomWords 仅预留给配置文件和后续管理能力，本轮不提供独立 CRUD 界面。
type SensitiveWordConfig struct {
	Enabled               bool     `json:"enabled"`
	PornographyEnabled    bool     `json:"pornographyEnabled"`
	GamblingEnabled       bool     `json:"gamblingEnabled"`
	DrugsEnabled          bool     `json:"drugsEnabled"`
	ViolenceTerrorEnabled bool     `json:"violenceTerrorEnabled"`
	PoliticalEnabled      bool     `json:"politicalEnabled"`
	IllegalCrimeEnabled   bool     `json:"illegalCrimeEnabled"`
	CustomWords           []string `json:"customWords"`
}

// SensitiveInfoConfig 请求方向的个人信息检测设置。
type SensitiveInfoConfig struct {
	Enabled      bool     `json:"enabled"`
	Mode         string   `json:"mode"`
	EnabledRules []string `json:"enabledRules"`
	// IPMaskScope 决定 IP 地址规则的生效范围：
	//   - "public"：仅掩码公网单播地址（默认）
	//   - "all"：所有合法形态的地址都掩码（包含私网/保留/文档段）
	// 空字符串等价于 "public"。
	IPMaskScope string `json:"ipMaskScope,omitempty"`
}

// CredentialConfig 凭据检测设置。
// 注意：UserInputMode / ToolResultMode / ToolArgumentMode 是历史遗留的按来源细分
// 处置模式，运行时已不再读取——凭据处置统一由 SensitiveData.Mode 决定（与个人信息
// 同一个切换器）。字段保留仅为兼容旧配置文件的加载与校验，勿在新代码中消费。
type CredentialConfig struct {
	Enabled          bool     `json:"enabled"`
	UserInputMode    string   `json:"userInputMode"`
	ToolResultMode   string   `json:"toolResultMode"`
	ToolArgumentMode string   `json:"toolArgumentMode"`
	EnabledRules     []string `json:"enabledRules"`
}

// DangerousCmdConfig 响应方向的危险命令检测设置。
type DangerousCmdConfig struct {
	Enabled      bool                     `json:"enabled"`
	EnabledRules []string                 `json:"enabledRules"`
	AIGate       DangerousCmdAIGateConfig `json:"aiGate"`
}

// DangerousCmdAIGateConfig 危险命令 AI 闸门设置。启用后命中危险命令不再直接拦截，
// 而是先查会话级白/黑名单，再由代理调用一个大模型渠道二次判断；
// AI 判定危险时挂起请求等待用户白名单（WaitSeconds 秒，超时拦截），
// AI 判定安全时立即放行并保留同长度的否决窗口。AI 判断失败一律拦截。
type DangerousCmdAIGateConfig struct {
	Enabled bool `json:"enabled"`
	// ChannelID 指定判断渠道的稳定 ID；为空时按 messages → responses → chat → gemini
	// 自动挑选第一个可用渠道。
	ChannelID string `json:"channelId"`
	// WaitSeconds 用户决定窗口秒数，范围 3-120，默认 10。
	WaitSeconds int `json:"waitSeconds"`
}

// DefaultAIGateWaitSeconds 是用户决定窗口的默认值。
const DefaultAIGateWaitSeconds = 10

// WhitelistConfig 内容安全白名单设置。命中白名单的片段跳过拦截但记录审计事件，
// 让拦截记录页能看见"已放行"的条目。当前只支持按工具名放行：tool_result 与
// tool_argument 来源的片段若关联到白名单中的工具名，则跳过全部检测维度。
type WhitelistConfig struct {
	Enabled   bool     `json:"enabled"`
	ToolNames []string `json:"toolNames"`
}

const (
	SensitiveInfoRulePhone     = "phone"
	SensitiveInfoRuleIDCard    = "id_card"
	SensitiveInfoRuleEmail     = "email"
	SensitiveInfoRuleIPAddress = "ip_address"
	SensitiveInfoRuleBankCard  = "bank_card"

	// SensitiveInfoIPMaskScopePublic 仅掩码公网单播地址。
	SensitiveInfoIPMaskScopePublic = "public"
	// SensitiveInfoIPMaskScopeAll 掩码全部合法形态的地址，含私网/保留/文档段。
	SensitiveInfoIPMaskScopeAll = "all"

	ContentSafetyModeAudit = "audit"
	ContentSafetyModeBlock = "block"
	ContentSafetyModeMask  = "mask"

	CredentialRuleAPIKey           = "api_key"
	CredentialRuleNamedSecret      = "named_secret"
	CredentialRulePrivateKey       = "private_key"
	CredentialRuleConnectionString = "connection_string"
	CredentialRuleHighEntropy      = "high_entropy"

	DangerousCmdRuleDestructive          = "destructive"
	DangerousCmdRuleDownloadExecute      = "download_execute"
	DangerousCmdRuleReverseShell         = "reverse_shell"
	DangerousCmdRulePrivilegeEscalation  = "privilege_escalation"
	DangerousCmdRuleEnvironmentTampering = "environment_tampering"
)

// DefaultContentSafetyConfig 返回开箱即用的内容安全默认设置。
func DefaultContentSafetyConfig() ContentSafetyConfig {
	return ContentSafetyConfig{
		SensitiveData: SensitiveDataConfig{
			Mode: ContentSafetyModeBlock,
		},
		SensitiveWord: SensitiveWordConfig{
			Enabled:               false,
			PornographyEnabled:    false,
			GamblingEnabled:       false,
			DrugsEnabled:          false,
			ViolenceTerrorEnabled: false,
			PoliticalEnabled:      false,
			IllegalCrimeEnabled:   false,
			CustomWords:           []string{},
		},
		SensitiveInfo: SensitiveInfoConfig{
			Enabled:      false,
			Mode:         ContentSafetyModeMask,
			EnabledRules: []string{},
			IPMaskScope:  SensitiveInfoIPMaskScopeAll,
		},
		Credential: CredentialConfig{
			Enabled:          false,
			UserInputMode:    ContentSafetyModeBlock,
			ToolResultMode:   ContentSafetyModeBlock,
			ToolArgumentMode: ContentSafetyModeBlock,
			EnabledRules:     []string{},
		},
		Whitelist: WhitelistConfig{
			Enabled:   false,
			ToolNames: []string{},
		},
		DangerousCmd: DangerousCmdConfig{
			Enabled:      false,
			EnabledRules: []string{},
			AIGate: DangerousCmdAIGateConfig{
				Enabled:     false,
				ChannelID:   "",
				WaitSeconds: DefaultAIGateWaitSeconds,
			},
		},
		Allowlist: AllowlistConfig{
			Enabled: true,
			Entries: []string{"127.0.0.1", "localhost", "0.0.0.0", "::1"},
		},
		CustomRules: CustomRulesConfig{
			Enabled: false,
			Rules:   []CustomRuleItem{},
		},
	}
}

// Config 配置结构
type Config struct {
	Settings SettingsConfig `json:"settings"`

	Upstream        []UpstreamConfig `json:"upstream"`
	MessagePools    []ChannelPool    `json:"messagePools,omitempty"`
	CurrentUpstream int              `json:"currentUpstream,omitempty"` // 已废弃：旧格式兼容用
	LoadBalance     string           `json:"loadBalance"`               // 当前仅支持 failover

	// Responses 接口专用配置（独立于 /v1/messages）
	ResponsesUpstream        []UpstreamConfig `json:"responsesUpstream"`
	ResponsesPools           []ChannelPool    `json:"responsesPools,omitempty"`
	CurrentResponsesUpstream int              `json:"currentResponsesUpstream,omitempty"` // 已废弃：旧格式兼容用
	ResponsesLoadBalance     string           `json:"responsesLoadBalance"`

	// Gemini 接口专用配置（独立于 /v1/messages 和 /v1/responses）
	GeminiUpstream    []UpstreamConfig `json:"geminiUpstream"`
	GeminiPools       []ChannelPool    `json:"geminiPools,omitempty"`
	GeminiLoadBalance string           `json:"geminiLoadBalance"`

	// Chat Completions 接口专用配置（独立于 Claude/Responses/Gemini）
	ChatUpstream    []UpstreamConfig `json:"chatUpstream"`
	ChatPools       []ChannelPool    `json:"chatPools,omitempty"`
	ChatLoadBalance string           `json:"chatLoadBalance"`

	// Images 接口专用配置（OpenAI Images API，独立渠道池）
	ImagesUpstream    []UpstreamConfig `json:"imagesUpstream"`
	ImagesPools       []ChannelPool    `json:"imagesPools,omitempty"`
	ImagesLoadBalance string           `json:"imagesLoadBalance"`

	// Fuzzy 模式：启用时模糊处理错误，所有非 2xx 错误都尝试 failover
	FuzzyModeEnabled bool `json:"fuzzyModeEnabled"`

	// 客户端伪装：仅规范化发往对应原生协议上游的客户端身份请求头
	ClaudeCodeDisguiseEnabled bool `json:"claudeCodeDisguiseEnabled"`
	CodexDisguiseEnabled      bool `json:"codexDisguiseEnabled"`
}

// FailedKey 失败密钥记录
type FailedKey struct {
	Timestamp    time.Time
	FailureCount int
}

// ConfigManager 配置管理器
type ConfigManager struct {
	mu              sync.RWMutex
	config          Config
	configFile      string
	watcher         *fsnotify.Watcher
	failedKeysCache map[string]*FailedKey
	keyRecoveryTime time.Duration
	maxFailureCount int
	stopChan        chan struct{} // 用于通知 goroutine 停止
	closeOnce       sync.Once     // 确保 Close 只执行一次
}

// ============== 核心共享方法 ==============

// GetConfig 获取配置（返回深拷贝，确保并发安全）
func (cm *ConfigManager) GetConfig() Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// 深拷贝整个 Config 结构体
	cloned := cm.config

	// 深拷贝 Upstream slice
	if cm.config.Upstream != nil {
		cloned.Upstream = make([]UpstreamConfig, len(cm.config.Upstream))
		for i := range cm.config.Upstream {
			cloned.Upstream[i] = *cm.config.Upstream[i].Clone()
		}
	}

	// 深拷贝 ResponsesUpstream slice
	if cm.config.ResponsesUpstream != nil {
		cloned.ResponsesUpstream = make([]UpstreamConfig, len(cm.config.ResponsesUpstream))
		for i := range cm.config.ResponsesUpstream {
			cloned.ResponsesUpstream[i] = *cm.config.ResponsesUpstream[i].Clone()
		}
	}

	// 深拷贝 GeminiUpstream slice
	if cm.config.GeminiUpstream != nil {
		cloned.GeminiUpstream = make([]UpstreamConfig, len(cm.config.GeminiUpstream))
		for i := range cm.config.GeminiUpstream {
			cloned.GeminiUpstream[i] = *cm.config.GeminiUpstream[i].Clone()
		}
	}

	if cm.config.ChatUpstream != nil {
		cloned.ChatUpstream = make([]UpstreamConfig, len(cm.config.ChatUpstream))
		for i := range cm.config.ChatUpstream {
			cloned.ChatUpstream[i] = *cm.config.ChatUpstream[i].Clone()
		}
	}

	if cm.config.ImagesUpstream != nil {
		cloned.ImagesUpstream = make([]UpstreamConfig, len(cm.config.ImagesUpstream))
		for i := range cm.config.ImagesUpstream {
			cloned.ImagesUpstream[i] = *cm.config.ImagesUpstream[i].Clone()
		}
	}
	cloned.MessagePools = append([]ChannelPool(nil), cm.config.MessagePools...)
	cloned.ResponsesPools = append([]ChannelPool(nil), cm.config.ResponsesPools...)
	cloned.GeminiPools = append([]ChannelPool(nil), cm.config.GeminiPools...)
	cloned.ChatPools = append([]ChannelPool(nil), cm.config.ChatPools...)
	cloned.ImagesPools = append([]ChannelPool(nil), cm.config.ImagesPools...)

	return cloned
}

// GetNextAPIKey 获取下一个 API 密钥（纯 failover 模式）
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
func (cm *ConfigManager) GetNextAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool, apiType string) (string, error) {
	if len(upstream.APIKeys) == 0 {
		return "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
	}

	// 单 Key 直接返回
	if len(upstream.APIKeys) == 1 {
		return upstream.APIKeys[0], nil
	}

	// 筛选可用密钥：排除临时失败密钥和内存中的失败密钥
	availableKeys := []string{}
	for _, key := range upstream.APIKeys {
		if !failedKeys[key] && !cm.isKeyFailed(key) {
			availableKeys = append(availableKeys, key)
		}
	}

	if len(availableKeys) == 0 {
		// 如果所有密钥都失效，尝试选择失败时间最早的密钥（恢复尝试）
		var oldestFailedKey string
		oldestTime := time.Now()

		cm.mu.RLock()
		for _, key := range upstream.APIKeys {
			if !failedKeys[key] { // 排除本次请求已经尝试过的密钥
				if failure, exists := cm.failedKeysCache[key]; exists {
					if failure.Timestamp.Before(oldestTime) {
						oldestTime = failure.Timestamp
						oldestFailedKey = key
					}
				}
			}
		}
		cm.mu.RUnlock()

		if oldestFailedKey != "" {
			log.Printf("[%s-Key] 警告: 所有密钥都失效，尝试最早失败的密钥: %s", apiType, utils.MaskAPIKey(oldestFailedKey))
			return oldestFailedKey, nil
		}

		return "", fmt.Errorf("上游 %s 的所有API密钥都暂时不可用", upstream.Name)
	}

	// 纯 failover：按优先级顺序选择第一个可用密钥
	selectedKey := availableKeys[0]
	// 获取该密钥在原始列表中的索引
	keyIndex := 0
	for i, key := range upstream.APIKeys {
		if key == selectedKey {
			keyIndex = i + 1
			break
		}
	}
	log.Printf("[%s-Key] 故障转移选择密钥 %s (%d/%d)", apiType, utils.MaskAPIKey(selectedKey), keyIndex, len(upstream.APIKeys))
	return selectedKey, nil
}

// MarkKeyAsFailed 标记密钥失败
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
func (cm *ConfigManager) MarkKeyAsFailed(apiKey string, apiType string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if failure, exists := cm.failedKeysCache[apiKey]; exists {
		failure.FailureCount++
		failure.Timestamp = time.Now()
	} else {
		cm.failedKeysCache[apiKey] = &FailedKey{
			Timestamp:    time.Now(),
			FailureCount: 1,
		}
	}

	failure := cm.failedKeysCache[apiKey]
	recoveryTime := cm.keyRecoveryTime
	if failure.FailureCount > cm.maxFailureCount {
		recoveryTime = cm.keyRecoveryTime * 2
	}

	log.Printf("[%s-Key] 标记API密钥失败: %s (失败次数: %d, 恢复时间: %v)",
		apiType, utils.MaskAPIKey(apiKey), failure.FailureCount, recoveryTime)
}

// isKeyFailed 检查密钥是否失败
func (cm *ConfigManager) isKeyFailed(apiKey string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	failure, exists := cm.failedKeysCache[apiKey]
	if !exists {
		return false
	}

	recoveryTime := cm.keyRecoveryTime
	if failure.FailureCount > cm.maxFailureCount {
		recoveryTime = cm.keyRecoveryTime * 2
	}

	return time.Since(failure.Timestamp) < recoveryTime
}

// IsKeyFailed 检查 Key 是否在冷却期（公开方法）
func (cm *ConfigManager) IsKeyFailed(apiKey string) bool {
	return cm.isKeyFailed(apiKey)
}

// clearFailedKeysForUpstream 清理指定渠道的所有失败 key 记录
// 当渠道被删除时调用，避免内存泄漏和冷却状态残留
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
func (cm *ConfigManager) clearFailedKeysForUpstream(upstream *UpstreamConfig, apiType string) {
	for _, key := range upstream.APIKeys {
		if _, exists := cm.failedKeysCache[key]; exists {
			delete(cm.failedKeysCache, key)
			log.Printf("[%s-Key] 已清理被删除渠道 %s 的失败密钥记录: %s", apiType, upstream.Name, utils.MaskAPIKey(key))
		}
	}
}

// cleanupExpiredFailures 清理过期的失败记录
func (cm *ConfigManager) cleanupExpiredFailures() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-cm.stopChan:
			return
		case <-ticker.C:
			cm.mu.Lock()
			now := time.Now()
			for key, failure := range cm.failedKeysCache {
				recoveryTime := cm.keyRecoveryTime
				if failure.FailureCount > cm.maxFailureCount {
					recoveryTime = cm.keyRecoveryTime * 2
				}

				if now.Sub(failure.Timestamp) > recoveryTime {
					delete(cm.failedKeysCache, key)
					log.Printf("[Config-Key] API密钥 %s 已从失败列表中恢复", utils.MaskAPIKey(key))
				}
			}
			cm.mu.Unlock()
		}
	}
}

// ============== Fuzzy 模式相关方法 ==============

// GetFuzzyModeEnabled 获取 Fuzzy 模式状态
func (cm *ConfigManager) GetFuzzyModeEnabled() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.FuzzyModeEnabled
}

// SetFuzzyModeEnabled 设置 Fuzzy 模式状态
func (cm *ConfigManager) SetFuzzyModeEnabled(enabled bool) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.config.FuzzyModeEnabled = enabled

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	status := "关闭"
	if enabled {
		status = "启用"
	}
	log.Printf("[Config-FuzzyMode] Fuzzy 模式已%s", status)
	return nil
}

// GetClaudeCodeDisguiseEnabled 获取 Claude Code 客户端伪装状态。
func (cm *ConfigManager) GetClaudeCodeDisguiseEnabled() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.ClaudeCodeDisguiseEnabled
}

// GetCodexDisguiseEnabled 获取 Codex 客户端伪装状态。
func (cm *ConfigManager) GetCodexDisguiseEnabled() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.CodexDisguiseEnabled
}

// SetClientDisguiseEnabled 设置指定协议的客户端伪装状态。
func (cm *ConfigManager) SetClientDisguiseEnabled(protocol string, enabled bool) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	switch protocol {
	case "messages":
		cm.config.ClaudeCodeDisguiseEnabled = enabled
	case "responses":
		cm.config.CodexDisguiseEnabled = enabled
	default:
		return fmt.Errorf("不支持的伪装协议: %s", protocol)
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-ClientDisguise] %s 客户端伪装已设置为 %v", protocol, enabled)
	return nil
}
