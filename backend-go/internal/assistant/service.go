package assistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/llmgate"
)

// Service 是 AI 配置助手的核心服务。
// main.go 组装注入（ConfigManager / llmgate.Caller / Store）。
type Service struct {
	cfgManager *config.ConfigManager
	caller     *llmgate.Caller
	store      *Store
}

// NewService 创建助手服务。
func NewService(cfgManager *config.ConfigManager, caller *llmgate.Caller, store *Store) (*Service, error) {
	if cfgManager == nil {
		return nil, fmt.Errorf("助手服务未绑定配置管理器")
	}
	if caller == nil {
		return nil, fmt.Errorf("助手服务未绑定内部调用器")
	}
	if store == nil {
		return nil, fmt.Errorf("助手服务未绑定会话存储")
	}
	return &Service{cfgManager: cfgManager, caller: caller, store: store}, nil
}

// Close 关闭会话存储。
func (s *Service) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Close()
}

// ChatOutcome 是一次对话的结果。
type ChatOutcome struct {
	UserMessage      Message        `json:"userMessage"`
	AssistantMessage Message        `json:"assistantMessage"`
	Actions          []ActionRecord `json:"actions"`
}

// Chat 处理一条用户消息：调用大模型（带会话历史）→ 解析行动块 → 执行 → 持久化。
// 渠道选择：settings.assistant.channelId 非空时显式指定（不回退不换渠道）；
// 为空时自动——模型渠道由主链路编排选择（分组匹配 → 优先级 → 熔断/健康
// → 兜底分组），模型 ID 取 settings.assistant.model（空 = 自动，走兜底分组）。
func (s *Service) Chat(ctx context.Context, sessionID int64, userContent string) (ChatOutcome, error) {
	userContent = strings.TrimSpace(userContent)
	if userContent == "" {
		return ChatOutcome{}, fmt.Errorf("消息内容不能为空")
	}
	if _, err := s.store.GetSession(sessionID); err != nil {
		return ChatOutcome{}, err
	}
	userMessage, err := s.store.AppendMessage(Message{SessionID: sessionID, Role: "user", Content: userContent})
	if err != nil {
		return ChatOutcome{}, err
	}

	history, err := s.store.ListMessages(sessionID)
	if err != nil {
		return ChatOutcome{}, err
	}
	prompt := s.buildPrompt(history)

	result, err := s.caller.Call(ctx, s.assistantChannelID(), prompt)
	if err != nil {
		// 模型调用失败：写入一条说明性 assistant 消息（会话可续），
		// 同时把错误返回给调用方展示。
		failure := "AI 助手暂时不可用：" + err.Error()
		_, _ = s.store.AppendMessage(Message{SessionID: sessionID, Role: "assistant", Content: failure})
		return ChatOutcome{}, fmt.Errorf("AI 助手调用失败: %w", err)
	}

	actions, replyBody, err := extractActions(result.Text)
	if err != nil {
		// 行动块解析失败：保留原始回复（正文仍有解释价值），错误作为一条行动记录。
		replyBody = result.Text
		return s.handleChatOutcome(sessionID, userMessage, replyBody, []ActionRecord{{Action: "parse", Success: false, Detail: err.Error()}})
	}
	// 执行行动并持久化。
	executor := &executor{cfgManager: s.cfgManager}
	executed := make([]ActionRecord, 0, len(actions))
	for _, action := range actions {
		executed = append(executed, executor.execute(action))
	}
	return s.handleChatOutcome(sessionID, userMessage, replyBody, executed)
}

// handleChatOutcome 落库 assistant 消息并组装返回值。
func (s *Service) handleChatOutcome(sessionID int64, userMessage Message, replyBody string, executed []ActionRecord) (ChatOutcome, error) {
	if replyBody == "" && len(executed) == 0 {
		replyBody = "（模型没有返回内容）"
	}
	assistantMessage, err := s.store.AppendMessage(Message{
		SessionID: sessionID,
		Role:      "assistant",
		Content:   replyBody,
		Actions:   executed,
	})
	if err != nil {
		return ChatOutcome{}, err
	}
	return ChatOutcome{UserMessage: userMessage, AssistantMessage: assistantMessage, Actions: executed}, nil
}

// buildPrompt 把会话历史转换为 llmgate 提示词。历史中 assistant 消息的
// 行动执行结果以简短附录随消息携带，让模型知道上一次操作是否成功。
// Model 取配置的分组匹配词，由主链路编排按分组匹配选渠；空表示自动（兜底分组）。
func (s *Service) buildPrompt(history []Message) llmgate.Prompt {
	if len(history) > maxHistoryMessages {
		history = history[len(history)-maxHistoryMessages:]
	}
	messages := make([]llmgate.PromptMessage, 0, len(history))
	for _, message := range history {
		content := message.Content
		if len(message.Actions) > 0 {
			var builder strings.Builder
			builder.WriteString(content)
			builder.WriteString("\n[行动执行结果]")
			for _, action := range message.Actions {
				status := "成功"
				if !action.Success {
					status = "失败"
				}
				builder.WriteString(fmt.Sprintf("\n- %s（%s）: %s", action.Action, status, action.Detail))
			}
			content = builder.String()
		}
		messages = append(messages, llmgate.PromptMessage{Role: message.Role, Content: content})
	}
	return llmgate.Prompt{
		System:    buildSystemPrompt(s.cfgManager),
		Messages:  messages,
		Model:     s.assistantModel(),
		MaxTokens: 4096,
	}
}

// CreateSession 创建会话（标题留空，首条消息后由前端更新）。
func (s *Service) CreateSession(title string) (Session, error) {
	return s.store.CreateSession(title)
}

// ListSessions 列出会话。
func (s *Service) ListSessions() ([]Session, error) {
	return s.store.ListSessions()
}

// GetSessionMessages 返回会话全部消息。
func (s *Service) GetSessionMessages(sessionID int64) ([]Message, error) {
	if _, err := s.store.GetSession(sessionID); err != nil {
		return nil, err
	}
	return s.store.ListMessages(sessionID)
}

// DeleteSession 删除会话。
func (s *Service) DeleteSession(sessionID int64) (bool, error) {
	return s.store.DeleteSession(sessionID)
}

// assistantModel 返回配置的助手模型 ID（分组匹配词）；空表示自动（兜底分组）。
func (s *Service) assistantModel() string {
	if s == nil || s.cfgManager == nil {
		return ""
	}
	return strings.TrimSpace(s.cfgManager.GetSettings().Assistant.Model)
}

// assistantChannelID 返回配置的显式渠道 ID；空表示自动（按四协议优先级编排）。
func (s *Service) assistantChannelID() string {
	if s == nil || s.cfgManager == nil {
		return ""
	}
	return strings.TrimSpace(s.cfgManager.GetSettings().Assistant.ChannelID)
}

// ChannelInfo 描述助手当前会命中的渠道（status 展示用）。
type ChannelInfo struct {
	Kind string `json:"kind"` // messages / responses / chat / gemini
	Name string `json:"name"`
	ID   string `json:"id"`
}

// ChannelOption 是助手渠道选择器的一个候选项（按 messages → responses →
// chat → gemini 顺序排列，images 不参与）。
type ChannelOption struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// StatusReport 是 /assistant/status 的响应体。
type StatusReport struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
	// Model 是配置的模型 ID（分组匹配词），空 = 自动（兜底分组）。
	Model string `json:"model"`
	// ChannelID 是配置的显式渠道 ID，空 = 自动（按四协议优先级编排）。
	ChannelID string       `json:"channelId"`
	Channel   *ChannelInfo `json:"channel"`
	// Channels 是渠道选择器的全部候选（按四协议优先级顺序）。
	Channels []ChannelOption `json:"channels"`
}

// Status 探测当前配置下助手实际会命中的渠道（不发送请求）。
func (s *Service) Status(ctx context.Context) StatusReport {
	if s == nil || s.cfgManager == nil || s.caller == nil {
		return StatusReport{Available: false, Reason: "助手服务未装配"}
	}
	model := s.assistantModel()
	channelID := s.assistantChannelID()
	report := StatusReport{Model: model, ChannelID: channelID, Channels: s.channelOptions()}
	selection, err := s.caller.PreviewChannel(ctx, channelID, model)
	if err != nil {
		report.Available = false
		report.Reason = err.Error()
		return report
	}
	report.Available = true
	report.Channel = &ChannelInfo{
		Kind: string(selection.Kind),
		Name: selection.Upstream.Name,
		ID:   selection.Upstream.ID,
	}
	return report
}

// channelOptions 列出四协议（messages → responses → chat → gemini）的全部
// 渠道作为选择器候选；渠道按配置顺序返回。
func (s *Service) channelOptions() []ChannelOption {
	if s == nil || s.cfgManager == nil {
		return []ChannelOption{}
	}
	cfg := s.cfgManager.GetConfig()
	kinds := []struct {
		kind      string
		upstreams []config.UpstreamConfig
	}{
		{"messages", cfg.Upstream},
		{"responses", cfg.ResponsesUpstream},
		{"chat", cfg.ChatUpstream},
		{"gemini", cfg.GeminiUpstream},
	}
	options := make([]ChannelOption, 0, len(cfg.Upstream)+len(cfg.ResponsesUpstream)+len(cfg.ChatUpstream)+len(cfg.GeminiUpstream))
	for _, entry := range kinds {
		for index := range entry.upstreams {
			upstream := entry.upstreams[index]
			if config.GetChannelStatus(&upstream) == config.ChannelStatusDeleted {
				continue
			}
			options = append(options, ChannelOption{Kind: entry.kind, ID: upstream.ID, Name: upstream.Name})
		}
	}
	return options
}
