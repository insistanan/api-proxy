package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// 每个响应记录其在正文中的边界；普通续轮仅新增边界元数据，不额外保存整份历史快照。
type responseBoundary struct {
	MessageCount     int   `json:"messageCount"`
	Version          int64 `json:"version"`
	TotalTokens      int   `json:"totalTokens"`
	HasVisionContent bool  `json:"hasVisionContent"`
}

type responseReference struct {
	SessionID string
	Owner     string
	Boundary  *responseBoundary
}

func boundaryOf(current *Session) *responseBoundary {
	return &responseBoundary{MessageCount: len(current.Messages), Version: current.Version, TotalTokens: current.TotalTokens, HasVisionContent: current.HasVisionContent}
}

func responseSnapshot(current *Session, responseID string, boundary *responseBoundary) (*Session, error) {
	if boundary == nil {
		// 未记录边界时，只有正文标记的最后一个响应可以被准确恢复。
		if current.LastResponseID != responseID {
			return nil, &HistoryError{Message: "该 response ID 缺少可恢复的历史边界，请提供完整对话"}
		}
		boundary = boundaryOf(current)
	}
	if boundary.MessageCount < 0 || boundary.MessageCount > len(current.Messages) || boundary.Version > current.Version {
		return nil, fmt.Errorf("Responses 历史边界与持久化正文不一致")
	}
	result := *current
	result.Messages = current.Messages[:boundary.MessageCount]
	result.Version = boundary.Version
	result.TotalTokens = boundary.TotalTokens
	result.HasVisionContent = boundary.HasVisionContent
	result.LastResponseID = responseID
	return &result, nil
}

func (s *sqliteStore) loadResponseReference(ctx context.Context, responseID string) (responseReference, bool, error) {
	var ref responseReference
	var boundary sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT session_id, upstream_identity, boundary_json FROM response_session_mappings WHERE response_id=?`, responseID).Scan(&ref.SessionID, &ref.Owner, &boundary)
	if errors.Is(err, sql.ErrNoRows) {
		return ref, false, nil
	}
	if err != nil {
		return ref, false, err
	}
	if boundary.Valid {
		if err := json.Unmarshal([]byte(boundary.String), &ref.Boundary); err != nil {
			return ref, false, fmt.Errorf("解析 Responses 历史边界失败: %w", err)
		}
	}
	return ref, true, nil
}
