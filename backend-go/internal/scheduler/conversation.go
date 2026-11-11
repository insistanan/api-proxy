package scheduler

import (
	"fmt"

	"github.com/insistanan/api-proxy/internal/conversation"
	"github.com/insistanan/api-proxy/internal/urlhealth"
)

func (s *ChannelScheduler) SetTraceAffinityForKind(kind ChannelKind, conversationID string, channelIndex int) {
	if s != nil && s.traceAffinity != nil && conversationID != "" {
		s.traceAffinity.SetPreferredChannelForKind(string(kind), conversationID, channelIndex)
	}
}

// GetConversationLastResolved 返回对话最近成功/尝试命中的渠道（对话级粘滞依据）。
// 调度选渠时若命中该渠道且其负载未过载，则沿用；否则放行给负载均衡处理。
func (s *ChannelScheduler) GetConversationLastResolved(conversationID string) (*conversation.ChannelRef, bool) {
	if conversationID == "" || s == nil {
		return nil, false
	}
	s.mu.RLock()
	registry := s.conversationRegistry
	s.mu.RUnlock()
	if registry == nil {
		return nil, false
	}
	return registry.GetLastResolved(conversationID)
}

// GetPreferredBaseURL 获取会话粘滞的 BaseURL（用于 prompt cache 亲和）。
func (s *ChannelScheduler) GetPreferredBaseURL(sessionKey string) (string, bool) {
	if s == nil || s.baseURLAffinity == nil {
		return "", false
	}
	return s.baseURLAffinity.GetPreferredBaseURL(sessionKey)
}

// SetPreferredBaseURL 记录会话最近成功的 BaseURL。
func (s *ChannelScheduler) SetPreferredBaseURL(sessionKey string, baseURL string) {
	if s == nil || s.baseURLAffinity == nil {
		return
	}
	s.baseURLAffinity.SetPreferredBaseURL(sessionKey, baseURL)
}

// PreferBaseURLInResults 将会话偏好的 BaseURL 排到候选列表最前（保持其余相对顺序）。
func PreferBaseURLInResults(results []urlhealth.URLLatencyResult, preferred string) []urlhealth.URLLatencyResult {
	if preferred == "" || len(results) <= 1 {
		return results
	}
	preferredIdx := -1
	for i, result := range results {
		if result.URL == preferred {
			preferredIdx = i
			break
		}
	}
	if preferredIdx <= 0 {
		return results
	}
	reordered := make([]urlhealth.URLLatencyResult, 0, len(results))
	reordered = append(reordered, results[preferredIdx])
	reordered = append(reordered, results[:preferredIdx]...)
	reordered = append(reordered, results[preferredIdx+1:]...)
	return reordered
}

// ConsumePromotionCount 消费促销请求次数
// 在请求成功后调用，递减促销计数，到 0 时自动清除促销状态
func (s *ChannelScheduler) ConsumePromotionCount(channelIndex int, kind ChannelKind) {
	channelType := "messages"
	switch kind {
	case ChannelKindResponses:
		channelType = "responses"
	case ChannelKindGemini:
		channelType = "gemini"
	case ChannelKindChat:
		channelType = "chat"
	case ChannelKindImages:
		channelType = "images"
	case ChannelKindEmbeddings:
		channelType = "embeddings"
	case ChannelKindDecisions:
		channelType = "decisions"
	}
	s.configManager.ConsumePromotionCount(channelIndex, channelType)
}

func (s *ChannelScheduler) ValidateFixedChannel(conversationID string, kind ChannelKind, channelIndex int) error {
	if conversationID == "" || s == nil {
		return nil
	}
	s.mu.RLock()
	registry := s.conversationRegistry
	s.mu.RUnlock()
	if registry == nil {
		return nil
	}
	override, ok := registry.GetRouteOverride(conversationID)
	if !ok {
		return nil
	}
	if override.Kind != string(kind) {
		return fmt.Errorf("该对话已固定到 %s 渠道池，当前请求为 %s", override.Kind, kind)
	}
	if override.ChannelIndex != channelIndex {
		return fmt.Errorf("该对话已固定到渠道 [%d]，当前命中的渠道为 [%d]", override.ChannelIndex, channelIndex)
	}
	return nil
}

func (s *ChannelScheduler) MarkConversationSuccess(conversationID string, kind ChannelKind, channelIndex int, channelName string) {
	if conversationID == "" || s == nil {
		return
	}
	s.mu.RLock()
	registry := s.conversationRegistry
	s.mu.RUnlock()
	if registry == nil {
		return
	}
	registry.MarkSuccess(conversationID, string(kind), channelIndex, channelName)
}

func (s *ChannelScheduler) MarkConversationAttempt(conversationID string, kind ChannelKind, channelIndex int, channelName string, requestedModel string, resolvedModel string, stream bool) {
	if conversationID == "" || s == nil {
		return
	}
	s.mu.RLock()
	registry := s.conversationRegistry
	s.mu.RUnlock()
	if registry == nil {
		return
	}
	registry.MarkAttempt(conversationID, string(kind), channelIndex, channelName, requestedModel, resolvedModel, stream)
}

func (s *ChannelScheduler) MarkConversationFailure(conversationID string, kind ChannelKind, errorMessage string) {
	if conversationID == "" || s == nil {
		return
	}
	s.mu.RLock()
	registry := s.conversationRegistry
	s.mu.RUnlock()
	if registry == nil {
		return
	}
	registry.MarkFailure(conversationID, string(kind), errorMessage)
}

func (s *ChannelScheduler) MarkConversationComplete(conversationID string, kind ChannelKind) {
	if conversationID == "" || s == nil {
		return
	}
	s.mu.RLock()
	registry := s.conversationRegistry
	s.mu.RUnlock()
	if registry == nil {
		return
	}
	registry.MarkComplete(conversationID, string(kind))
}
