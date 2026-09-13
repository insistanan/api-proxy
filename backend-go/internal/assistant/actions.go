package assistant

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
)

// 行动协议：模型在回复中嵌入 ```assistant-action 代码块，块内是一个 JSON 对象。
// 后端按白名单解析并执行；未登记的行动一律拒绝（显式报错，不静默忽略）。

// actionJSON 是模型输出的行动结构。字段用指针区分"未提供"与"零值"。
type actionJSON struct {
	Action        string              `json:"action"`
	Kind          string              `json:"kind"` // messages / responses / chat / gemini
	Name          string              `json:"name"`
	BaseURL       string              `json:"baseUrl"`
	ServiceType   string              `json:"serviceType"`
	DefaultModel  string              `json:"defaultModel"`
	PoolName      string              `json:"poolName"`
	ModelMatcher  string              `json:"modelMatcher"`
	ChannelID     string              `json:"channelId"`
	ModelMapping  map[string][]string `json:"modelMapping"`
	VisionCapable *bool               `json:"visionCapable"`
	// 图片理解层（渠道级）
	VisionLayerEnabled   *bool  `json:"visionLayerEnabled"`
	VisionLayerChannelID string `json:"visionLayerChannelId"`
	VisionLayerModel     string `json:"visionLayerModel"`
}

const assistantActionFence = "assistant-action"

// extractActions 从模型回复中提取全部行动块，并返回剥掉行动块后的正文。
// 没有行动块时返回空列表与原文。
func extractActions(reply string) ([]actionJSON, string, error) {
	actions := make([]actionJSON, 0)
	var body strings.Builder
	rest := reply
	for {
		start := strings.Index(rest, "```"+assistantActionFence)
		if start < 0 {
			body.WriteString(rest)
			break
		}
		body.WriteString(rest[:start])
		contentStart := start + len("```"+assistantActionFence)
		end := strings.Index(rest[contentStart:], "```")
		if end < 0 {
			// 围栏未闭合：按无行动处理，正文原样保留。
			body.WriteString(rest[start:])
			break
		}
		payload := strings.TrimSpace(rest[contentStart : contentStart+end])
		rest = rest[contentStart+end+len("```"):]
		var action actionJSON
		if err := json.Unmarshal([]byte(payload), &action); err != nil {
			return nil, "", fmt.Errorf("行动块 JSON 无效: %w", err)
		}
		actions = append(actions, action)
	}
	return actions, strings.TrimSpace(body.String()), nil
}

// executor 通过 config.ConfigManager 执行白名单行动。
type executor struct {
	cfgManager *config.ConfigManager
}

var validKinds = map[string]struct{}{
	"messages": {}, "responses": {}, "chat": {}, "gemini": {},
}

func validServiceTypes(kind string) map[string]struct{} {
	// serviceType 描述渠道的上游协议；gemini 渠道下 gemini 原生，其余三者常见组合如下。
	switch kind {
	case "gemini":
		return map[string]struct{}{"gemini": {}}
	case "responses":
		return map[string]struct{}{"responses": {}, "openai": {}, "claude": {}, "gemini": {}}
	case "messages":
		return map[string]struct{}{"claude": {}, "openai": {}, "gemini": {}, "responses": {}}
	case "chat":
		return map[string]struct{}{"openai": {}, "gemini": {}, "claude": {}}
	default:
		return map[string]struct{}{}
	}
}

// execute 执行一条行动，返回结果记录。所有写操作走 ConfigManager 的
// 既有校验（addValidatedUpstreamOp / pool 校验），绝不绕过。
// 任何行动都不携带 API Key：新建渠道无 Key（提示用户去渠道页填写），
// 更新渠道永不触碰 APIKeys 字段。
func (e *executor) execute(action actionJSON) ActionRecord {
	switch action.Action {
	case "create_channel":
		return e.createChannel(action)
	case "create_pool":
		return e.createPool(action)
	case "set_model_mapping":
		return e.setModelMapping(action)
	case "update_channel":
		return e.updateChannel(action)
	case "explain":
		// explain 是纯回复，没有副作用；出现显式行动块视为模型格式错误。
		return ActionRecord{Action: action.Action, Success: false, Detail: "explain 不需要行动块，直接文字回复即可"}
	default:
		return ActionRecord{Action: action.Action, Success: false, Detail: fmt.Sprintf("不支持的行动 %q（白名单：create_channel / create_pool / set_model_mapping / update_channel）", action.Action)}
	}
}

func (e *executor) createChannel(action actionJSON) ActionRecord {
	kind := strings.ToLower(strings.TrimSpace(action.Kind))
	if _, ok := validKinds[kind]; !ok {
		return ActionRecord{Action: action.Action, Success: false, Detail: "kind 必须是 messages / responses / chat / gemini"}
	}
	name := strings.TrimSpace(action.Name)
	baseURL := strings.TrimSpace(action.BaseURL)
	if name == "" || baseURL == "" {
		return ActionRecord{Action: action.Action, Success: false, Detail: "name 与 baseUrl 不能为空"}
	}
	serviceType := strings.ToLower(strings.TrimSpace(action.ServiceType))
	if serviceType == "" {
		serviceType = defaultServiceTypeFor(kind)
	}
	if _, ok := validServiceTypes(kind)[serviceType]; !ok {
		return ActionRecord{Action: action.Action, Success: false, Detail: fmt.Sprintf("kind=%s 不支持 serviceType=%s", kind, serviceType)}
	}
	// 分组按名称解析为 pool ID；找不到时留在兜底分组。
	poolID := ""
	if poolName := strings.TrimSpace(action.PoolName); poolName != "" {
		resolved, ok := e.resolvePoolID(kind, poolName)
		if !ok {
			return ActionRecord{Action: action.Action, Success: false, Detail: fmt.Sprintf("分组 %q 不存在，请先创建分组", poolName)}
		}
		poolID = resolved
	}
	upstream := config.UpstreamConfig{
		Name:         name,
		BaseURL:      baseURL,
		ServiceType:  serviceType,
		PoolID:       poolID,
		DefaultModel: strings.TrimSpace(action.DefaultModel),
		// 故意不设置 APIKeys：新建渠道无 Key，由用户在渠道页填写，
		// Key 绝不经过大模型。
	}
	if action.VisionCapable != nil {
		upstream.VisionCapable = *action.VisionCapable
	}
	if action.VisionLayerEnabled != nil {
		upstream.VisionLayerEnabled = *action.VisionLayerEnabled
	}
	upstream.VisionLayerChannelID = strings.TrimSpace(action.VisionLayerChannelID)
	upstream.VisionLayerModel = strings.TrimSpace(action.VisionLayerModel)

	var result config.AddedUpstream
	var err error
	switch kind {
	case "messages":
		result, err = e.cfgManager.AddUpstreamWithResult(upstream)
	case "responses":
		result, err = e.cfgManager.AddResponsesUpstreamWithResult(upstream)
	case "chat":
		result, err = e.cfgManager.AddChatUpstreamWithResult(upstream)
	case "gemini":
		result, err = e.cfgManager.AddGeminiUpstreamWithResult(upstream)
	}
	if err != nil {
		return ActionRecord{Action: action.Action, Success: false, Detail: "创建渠道失败: " + err.Error()}
	}
	detail := fmt.Sprintf("已在 %s 创建渠道 %q（serviceType=%s，稳定 ID=%s）。注意：渠道尚无 API Key，请在渠道页填写。",
		kind, name, serviceType, result.ID)
	if poolID != "" {
		detail += fmt.Sprintf("已加入分组 %q。", strings.TrimSpace(action.PoolName))
	}
	return ActionRecord{Action: action.Action, Success: true, Detail: detail}
}

func (e *executor) createPool(action actionJSON) ActionRecord {
	kind := strings.ToLower(strings.TrimSpace(action.Kind))
	if _, ok := validKinds[kind]; !ok {
		return ActionRecord{Action: action.Action, Success: false, Detail: "kind 必须是 messages / responses / chat / gemini"}
	}
	name := strings.TrimSpace(action.Name)
	if name == "" {
		return ActionRecord{Action: action.Action, Success: false, Detail: "分组名不能为空"}
	}
	pool, err := e.cfgManager.CreateChannelPool(kind, config.ChannelPool{
		Name:         name,
		ModelMatcher: strings.TrimSpace(action.ModelMatcher),
	})
	if err != nil {
		return ActionRecord{Action: action.Action, Success: false, Detail: "创建分组失败: " + err.Error()}
	}
	detail := fmt.Sprintf("已在 %s 创建分组 %q（ID=%s）", kind, name, pool.ID)
	if pool.ModelMatcher != "" {
		detail += fmt.Sprintf("，模型匹配规则：%s", pool.ModelMatcher)
	}
	return ActionRecord{Action: action.Action, Success: true, Detail: detail}
}

func (e *executor) setModelMapping(action actionJSON) ActionRecord {
	kind := strings.ToLower(strings.TrimSpace(action.Kind))
	if _, ok := validKinds[kind]; !ok {
		return ActionRecord{Action: action.Action, Success: false, Detail: "kind 必须是 messages / responses / chat / gemini"}
	}
	channelID := strings.TrimSpace(action.ChannelID)
	if channelID == "" {
		return ActionRecord{Action: action.Action, Success: false, Detail: "channelId 不能为空"}
	}
	if len(action.ModelMapping) == 0 {
		return ActionRecord{Action: action.Action, Success: false, Detail: "modelMapping 不能为空"}
	}
	index, err := e.channelIndexByID(kind, channelID)
	if err != nil {
		return ActionRecord{Action: action.Action, Success: false, Detail: err.Error()}
	}
	updates := config.UpstreamUpdate{ModelMapping: action.ModelMapping}
	if err := e.updateChannelByKind(kind, index, updates); err != nil {
		return ActionRecord{Action: action.Action, Success: false, Detail: "更新模型映射失败: " + err.Error()}
	}
	return ActionRecord{Action: action.Action, Success: true, Detail: fmt.Sprintf("已更新渠道（ID=%s）的模型映射，共 %d 条规则", channelID, len(action.ModelMapping))}
}

func (e *executor) updateChannel(action actionJSON) ActionRecord {
	kind := strings.ToLower(strings.TrimSpace(action.Kind))
	if _, ok := validKinds[kind]; !ok {
		return ActionRecord{Action: action.Action, Success: false, Detail: "kind 必须是 messages / responses / chat / gemini"}
	}
	channelID := strings.TrimSpace(action.ChannelID)
	if channelID == "" {
		return ActionRecord{Action: action.Action, Success: false, Detail: "channelId 不能为空"}
	}
	index, err := e.channelIndexByID(kind, channelID)
	if err != nil {
		return ActionRecord{Action: action.Action, Success: false, Detail: err.Error()}
	}
	updates := config.UpstreamUpdate{}
	changed := false
	if value := strings.TrimSpace(action.BaseURL); value != "" {
		updates.BaseURL = &value
		changed = true
	}
	if value := strings.TrimSpace(action.DefaultModel); value != "" {
		updates.DefaultModel = &value
		changed = true
	}
	if poolName := strings.TrimSpace(action.PoolName); poolName != "" {
		poolID, ok := e.resolvePoolID(kind, poolName)
		if !ok {
			return ActionRecord{Action: action.Action, Success: false, Detail: fmt.Sprintf("分组 %q 不存在", poolName)}
		}
		updates.PoolID = &poolID
		changed = true
	}
	if action.VisionCapable != nil {
		updates.VisionCapable = action.VisionCapable
		changed = true
	}
	if action.VisionLayerEnabled != nil {
		updates.VisionLayerEnabled = action.VisionLayerEnabled
		changed = true
	}
	if value := strings.TrimSpace(action.VisionLayerChannelID); value != "" {
		updates.VisionLayerChannelID = &value
		changed = true
	}
	if value := strings.TrimSpace(action.VisionLayerModel); value != "" {
		updates.VisionLayerModel = &value
		changed = true
	}
	if !changed {
		return ActionRecord{Action: action.Action, Success: false, Detail: "没有提供任何可更新字段（baseUrl / defaultModel / poolName / vision 系列）"}
	}
	// 故意不映射 APIKeys / APIKey：渠道 Key 永不被助手触碰。
	if err := e.updateChannelByKind(kind, index, updates); err != nil {
		return ActionRecord{Action: action.Action, Success: false, Detail: "更新渠道失败: " + err.Error()}
	}
	return ActionRecord{Action: action.Action, Success: true, Detail: fmt.Sprintf("已更新渠道（ID=%s）", channelID)}
}

func (e *executor) resolvePoolID(kind, poolName string) (string, bool) {
	for _, pool := range e.cfgManager.GetChannelPools(kind) {
		if pool.Name == poolName {
			return pool.ID, true
		}
	}
	return "", false
}

func (e *executor) channelIndexByID(kind, channelID string) (int, error) {
	cfg := e.cfgManager.GetConfig()
	var upstreams []config.UpstreamConfig
	switch kind {
	case "messages":
		upstreams = cfg.Upstream
	case "responses":
		upstreams = cfg.ResponsesUpstream
	case "chat":
		upstreams = cfg.ChatUpstream
	case "gemini":
		upstreams = cfg.GeminiUpstream
	}
	for index := range upstreams {
		if upstreams[index].ID == channelID {
			return index, nil
		}
	}
	return -1, fmt.Errorf("%s 下不存在渠道 ID=%s", kind, channelID)
}

func (e *executor) updateChannelByKind(kind string, index int, updates config.UpstreamUpdate) error {
	var err error
	switch kind {
	case "messages":
		_, err = e.cfgManager.UpdateUpstream(index, updates)
	case "responses":
		_, err = e.cfgManager.UpdateResponsesUpstream(index, updates)
	case "chat":
		_, err = e.cfgManager.UpdateChatUpstream(index, updates)
	case "gemini":
		_, err = e.cfgManager.UpdateGeminiUpstream(index, updates)
	}
	return err
}

func defaultServiceTypeFor(kind string) string {
	switch kind {
	case "gemini":
		return "gemini"
	case "responses":
		return "responses"
	case "chat":
		return "openai"
	default:
		return "claude"
	}
}

func marshalActions(actions []ActionRecord) (string, error) {
	encoded, err := json.Marshal(actions)
	if err != nil {
		return "", fmt.Errorf("序列化行动结果失败: %w", err)
	}
	return string(encoded), nil
}

func unmarshalActions(payload string) ([]ActionRecord, error) {
	var actions []ActionRecord
	if strings.TrimSpace(payload) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(payload), &actions); err != nil {
		return nil, fmt.Errorf("解析行动结果失败: %w", err)
	}
	return actions, nil
}
