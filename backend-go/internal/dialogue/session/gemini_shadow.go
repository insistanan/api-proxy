package session

import (
	"context"
	"encoding/json"
	"errors"
)

func (sm *SessionManager) LoadGeminiShadows(ctx context.Context, providerID, conversationID string) ([]json.RawMessage, error) {
	rows, err := sm.store.db.QueryContext(ctx, `SELECT turn_json FROM gemini_shadow_turns WHERE conversation_id=? AND provider_id=? ORDER BY id`, conversationID, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var turns []json.RawMessage
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		turns = append(turns, json.RawMessage(data))
	}
	return turns, errors.Join(rows.Err(), rows.Close())
}

// 只追加本轮，不重写完整历史。外键使删除对话和签名清理共用事务。
func (sm *SessionManager) AppendGeminiShadow(ctx context.Context, providerID, conversationID string, turn json.RawMessage) error {
	_, err := sm.store.db.ExecContext(ctx, `INSERT INTO gemini_shadow_turns(conversation_id,provider_id,turn_json) VALUES (?,?,?)`, conversationID, providerID, string(turn))
	return err
}
