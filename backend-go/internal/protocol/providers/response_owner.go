package providers

import (
	"encoding/json"
	"fmt"

	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

type ResponseHistoryError struct{ Message string }

func (e *ResponseHistoryError) Error() string            { return e.Message }
func (e *ResponseHistoryError) ClientRequestError() bool { return true }

// 本地只存会话最新正文，无法按旧 response ID 截断，也不能把旧分支追加到最新会话。
func validateResponseHistoryBoundary(sess *session.Session, previousID string) error {
	if previousID != "" && sess != nil && len(sess.Messages) > 0 && sess.LastResponseID != previousID {
		return &ResponseHistoryError{Message: "previous_response_id 不是本地最新历史边界，无法准确恢复该分支；请发送完整对话重新建立会话"}
	}
	return nil
}

// PrepareNativeResponseHistory 供原生 Responses 的辅助端点复用凭据归属检查。
func PrepareNativeResponseHistory(manager *session.SessionManager, conversationID string, body []byte, owner string) ([]byte, error) {
	var req types.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, &ResponseHistoryError{Message: "无法解析 Responses 请求"}
	}
	var sess *session.Session
	if manager != nil && req.PreviousResponseID != "" {
		var err error
		sess, err = manager.GetOrCreateSessionForConversation(req.PreviousResponseID, conversationID)
		if err != nil {
			return nil, fmt.Errorf("获取 Responses 历史失败: %w", err)
		}
	}
	result, _, err := restoreNativeResponseHistory(manager, sess, &req, body, owner)
	return result, err
}

// restoreNativeResponseHistory 换凭据后从本地完整会话恢复输入，不把旧 ID 发给新 Key。
func restoreNativeResponseHistory(manager *session.SessionManager, sess *session.Session, req *types.ResponsesRequest, body []byte, owner string) ([]byte, bool, error) {
	previousID := req.PreviousResponseID
	if previousID == "" {
		return body, false, nil
	}
	if manager == nil {
		return nil, false, &ResponseHistoryError{Message: "携带 previous_response_id 的请求缺少本地会话管理器，无法核实凭据归属"}
	}
	if err := validateResponseHistoryBoundary(sess, previousID); err != nil {
		return nil, false, err
	}
	previousOwner, known, err := manager.ResponseOwner(previousID)
	if err != nil {
		return nil, false, err
	}
	if known && previousOwner == owner {
		return body, false, nil
	}
	if sess == nil || len(sess.Messages) == 0 {
		return nil, false, &ResponseHistoryError{Message: "previous_response_id 的上游凭据已变化或归属未知，且没有可恢复的完整历史；请发送完整对话重新建立会话"}
	}
	var current []types.ResponsesItem
	if text, ok := req.Input.(string); ok {
		current = []types.ResponsesItem{{Type: "message", Role: "user", Content: text}}
	} else {
		encoded, err := json.Marshal(req.Input)
		if err != nil {
			return nil, false, fmt.Errorf("序列化 Responses 当前输入失败: %w", err)
		}
		if err := json.Unmarshal(encoded, &current); err != nil {
			return nil, false, &ResponseHistoryError{Message: "Responses 当前输入格式不支持恢复完整历史"}
		}
	}
	full := utils.MergeResponsesItemsDedup(sess.Messages, current)
	for i := range full {
		// 本地存储把字符串 input 记为 text；原生 API 重放需要 message 结构。
		if full[i].Type == "text" && full[i].Role == "" {
			full[i].Type = "message"
			full[i].Role = "user"
			full[i].Content = []interface{}{map[string]interface{}{"type": "input_text", "text": full[i].Content}}
		}
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false, fmt.Errorf("解析 Responses 恢复载荷失败: %w", err)
	}
	input, err := json.Marshal(full)
	if err != nil {
		return nil, false, fmt.Errorf("序列化 Responses 恢复历史失败: %w", err)
	}
	payload["input"] = input
	delete(payload, "previous_response_id")
	result, err := json.Marshal(payload)
	if err != nil {
		return nil, false, fmt.Errorf("序列化 Responses 恢复请求失败: %w", err)
	}
	req.Input = full
	req.PreviousResponseID = ""
	return result, true, nil
}
