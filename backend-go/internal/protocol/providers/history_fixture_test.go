package providers

import (
	"context"
	"fmt"
	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func commitFixtureTurn(manager *session.SessionManager, snapshot *session.Session, items []types.ResponsesItem, tokens int, vision bool, responseID string, owners ...string) error {
	turn, err := manager.BeginTurn(context.Background(), "", snapshot.ConversationID)
	if err != nil {
		return err
	}
	defer turn.Close()
	if turn.Snapshot.ID != snapshot.ID {
		return fmt.Errorf("测试历史 ID 不一致")
	}
	owner := ""
	if len(owners) > 0 {
		owner = owners[0]
	}
	return turn.Commit(context.Background(), items, tokens, vision, responseID, owner, false)
}
