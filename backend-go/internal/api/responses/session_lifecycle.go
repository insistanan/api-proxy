package responses

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	respitems "insistanan/api-proxy/internal/protocol/items"
	"insistanan/api-proxy/internal/protocol/providers"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/protocol/visionmedia"
)

const responsesTurnKey = "__responses_session_turn"

func beginResponsesTurn(c *gin.Context, manager *session.SessionManager, body []byte, conversationID string) error {
	if manager == nil || c.GetBool(upstreamreq.ContextKeyAuxiliaryTask) {
		return nil
	}
	if _, exists := c.Get(responsesTurnKey); exists {
		return nil
	}
	previous := gjson.GetBytes(body, "previous_response_id").String()
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if respitems.IsResponsesCompactionType(item.Get("type").String()) {
			previous = ""
			break
		}
	}
	turn, err := manager.BeginTurn(c.Request.Context(), previous, conversationID)
	if err != nil {
		return err
	}
	c.Set(responsesTurnKey, turn)
	providers.BindResponseSession(c, turn.Snapshot)
	return nil
}

func closeResponsesTurn(c *gin.Context) {
	if value, ok := c.Get(responsesTurnKey); ok {
		value.(*session.Turn).Close()
	}
}

type sessionCommitError struct{ Err error }

func (e *sessionCommitError) Error() string            { return e.Err.Error() }
func (e *sessionCommitError) Unwrap() error            { return e.Err }
func (e *sessionCommitError) LocalResponseError() bool { return true }

// commitResponsesTurn 共用普通、流式和 compact 的提交责任；已交付工具后的收尾有独立上限。
func commitResponsesTurn(c *gin.Context, manager *session.SessionManager, request *types.ResponsesRequest, body []byte, output []types.ResponsesItem, tokens int, responseID, owner string, compact, delivered bool) error {
	if c.GetBool(upstreamreq.ContextKeyAuxiliaryTask) {
		return nil
	}
	validateCompaction := compact || respitems.ResponsesItemsContainCompaction(output)
	var input []types.ResponsesItem
	if !compact {
		var err error
		input, err = parseInputToItems(request.Input)
		if err != nil {
			return &sessionCommitError{Err: err}
		}
		for _, item := range input {
			if item.Type == "compaction_trigger" {
				validateCompaction = true
				break
			}
		}
	}
	// 自动压缩同样会替换历史，必须在裁剪窗口和提交前验证完整性。
	if validateCompaction {
		if err := respitems.ValidateResponsesCompactionOutput(output); err != nil {
			return &sessionCommitError{Err: err}
		}
	}
	items := output
	if !compact {
		items = respitems.ResponsesTurnItems(input, output)
	}
	if manager == nil {
		return nil
	}
	if responseID == "" {
		return &sessionCommitError{Err: fmt.Errorf("Responses 响应缺少 ID，无法发布可恢复历史")}
	}
	value, ok := c.Get(responsesTurnKey)
	if !ok {
		return &sessionCommitError{Err: fmt.Errorf("Responses 请求缺少调用前历史快照")}
	}
	turn := value.(*session.Turn)
	ctx := c.Request.Context()
	if delivered {
		ctx = context.WithoutCancel(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	replace := compact || c.GetBool(upstreamreq.ContextKeyResponsesHistoryBoundary)
	if err := turn.Commit(ctx, items, tokens, visionmedia.DetectImageContent(body), responseID, owner, replace); err != nil {
		return &sessionCommitError{Err: fmt.Errorf("保存 Responses 历史失败: %w", err)}
	}
	if replace || respitems.ResponsesItemsContainCompaction(items) {
		session.DefaultResponseChainManager().Clear(turn.Snapshot.ConversationID)
	}
	return nil
}
