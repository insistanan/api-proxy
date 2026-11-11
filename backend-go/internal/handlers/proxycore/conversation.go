package proxycore

import (
	"log"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/conversation"
	"github.com/insistanan/api-proxy/internal/scheduler"
)

// ExtractConversationID 从请求中提取明确的对话标识。
// 无明确标识时返回空，调用方会创建独立会话，避免错误合并不同 agent 的对话。
func ExtractConversationID(c *gin.Context, bodyBytes []byte) string {
	return ResolveConversationIdentity(c, bodyBytes).ExplicitID
}

// ResolveExistingConversationID 将控制面请求中的明确身份解析为已存在的内部会话记录 ID。
// 主请求经过 ObserveConversationRequest 后，调度器使用的是内部记录 ID；compact 等控制面
// 请求不能直接把外部 ID 当作内部 ID，否则亲和性会分裂。未找到已有记录时返回空，避免
// 控制面请求创建孤儿亲和记录或误合并并行客户端窗口。
func ResolveExistingConversationID(
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	identity conversation.Identity,
) (string, error) {
	if channelScheduler == nil {
		return identity.ExplicitID, nil
	}
	registry := channelScheduler.GetConversationRegistry()
	if registry == nil {
		return identity.ExplicitID, nil
	}
	return registry.ResolveExistingRecordIDWithError(string(kind), identity)
}

func ObserveConversationRequest(
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	identity conversation.Identity,
	transcript conversation.Transcript,
	model string,
	prompts []string,
	imageFingerprints []string,
	stream bool,
) (string, error) {
	if channelScheduler == nil {
		return identity.ExplicitID, nil
	}
	registry := channelScheduler.GetConversationRegistry()
	if registry == nil {
		return identity.ExplicitID, nil
	}

	firstPrompt := ""
	if len(prompts) > 0 {
		firstPrompt = prompts[0]
	}
	record, err := registry.ObserveRequestWithError(conversation.Observation{
		APIKind:           string(kind),
		Model:             model,
		Stream:            stream,
		ConversationID:    identity.ExplicitID,
		Identity:          identity,
		Transcript:        transcript,
		FirstPrompt:       firstPrompt,
		Prompts:           prompts,
		ImageFingerprints: imageFingerprints,
	})
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

func MarkConversationSuccess(channelScheduler *scheduler.ChannelScheduler, conversationID string, kind scheduler.ChannelKind, channelIndex int, channelName string) {
	if channelScheduler == nil || conversationID == "" {
		return
	}
	channelScheduler.MarkConversationSuccess(conversationID, kind, channelIndex, channelName)
}

func MarkConversationComplete(channelScheduler *scheduler.ChannelScheduler, conversationID string, kind scheduler.ChannelKind) {
	if channelScheduler == nil || conversationID == "" {
		return
	}
	channelScheduler.MarkConversationComplete(conversationID, kind)
}

func MarkConversationFailure(channelScheduler *scheduler.ChannelScheduler, conversationID string, kind scheduler.ChannelKind, err error) {
	if channelScheduler == nil || conversationID == "" || err == nil {
		return
	}
	channelScheduler.MarkConversationFailure(conversationID, kind, err.Error())
}

func AssociateConversationExternalID(channelScheduler *scheduler.ChannelScheduler, conversationID string, kind scheduler.ChannelKind, externalID string) {
	if channelScheduler == nil || conversationID == "" || externalID == "" {
		return
	}
	registry := channelScheduler.GetConversationRegistry()
	if registry == nil {
		return
	}
	if err := registry.AssociateExternalID(conversationID, string(kind), externalID); err != nil {
		log.Printf("[Conversation] 关联外部对话 ID 失败: %v", err)
	}
}
