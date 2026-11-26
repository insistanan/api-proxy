package session

import (
	"context"
	"encoding/json"
	"fmt"
)

// commit 将正文、版本、response 归属及 registry 别名放入同一事务。
func (s *sqliteStore) commit(ctx context.Context, next *Session, messages []byte, expected int64, replace bool, responseID, owner, alias string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE response_sessions SET conversation_id=?,messages_json=?,last_response_id=?,last_access_at=?,total_tokens=?,has_vision_content=?,version=? WHERE id=? AND version=?`, next.ConversationID, string(messages), next.LastResponseID, next.LastAccessAt.Unix(), next.TotalTokens, boolToInt(next.HasVisionContent), next.Version, next.ID, expected)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	committed := *next
	if changed != 1 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM response_sessions WHERE id=?)`, next.ID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("会话已删除，不能保存晚到响应")
		}
		// 同一父边界允许多个成功响应，各自保存分支，不能合入已推进的正文。
		id, err := generateID("sess")
		if err != nil {
			return err
		}
		committed.ID = id
		_, err = tx.ExecContext(ctx, `INSERT INTO response_sessions(id,conversation_id,messages_json,last_response_id,created_at,last_access_at,total_tokens,has_vision_content,version) VALUES (?,?,?,?,?,?,?,?,?)`, id, next.ConversationID, string(messages), next.LastResponseID, next.CreatedAt.Unix(), next.LastAccessAt.Unix(), next.TotalTokens, boolToInt(next.HasVisionContent), next.Version)
		if err != nil {
			return err
		}
	}
	boundary, err := json.Marshal(boundaryOf(&committed))
	if err != nil {
		return err
	}
	if responseID != "" {
		result, err = tx.ExecContext(ctx, `INSERT INTO response_session_mappings (response_id,session_id,upstream_identity,alias_key,boundary_json) VALUES (?,?,?,?,?) ON CONFLICT(response_id) DO NOTHING`, responseID, committed.ID, owner, alias, string(boundary))
		if err != nil {
			return err
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return fmt.Errorf("response ID 已存在，拒绝覆盖历史归属")
		}
	}
	if replace {
		if alias != "" {
			if _, err = tx.ExecContext(ctx, `DELETE FROM conversation_aliases WHERE alias_key IN (SELECT alias_key FROM response_session_mappings WHERE session_id=? AND response_id!=?)`, committed.ID, responseID); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM response_session_mappings WHERE session_id=? AND response_id!=?`, committed.ID, responseID); err != nil {
			return err
		}
	}
	if alias != "" {
		result, err = tx.ExecContext(ctx, `INSERT INTO conversation_aliases(alias_key,conversation_id) VALUES (?,?) ON CONFLICT(alias_key) DO UPDATE SET conversation_id=excluded.conversation_id WHERE conversation_aliases.conversation_id=excluded.conversation_id`, alias, next.ConversationID)
		if err != nil {
			return err
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return fmt.Errorf("response ID 别名属于其他对话")
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO response_session_access(session_id,last_access_at) VALUES (?,?) ON CONFLICT(session_id) DO UPDATE SET last_access_at=excluded.last_access_at`, committed.ID, next.LastAccessAt.Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	next.ID = committed.ID
	return nil
}
