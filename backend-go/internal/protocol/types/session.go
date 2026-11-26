package types

import "time"

// Session 是指定 Responses 历史边界的快照。
type Session struct {
	Version          int64           // 历史版本，仅成功提交递增；访问时间不改变版本。
	ID               string          // sess_xxxxx
	ConversationID   string          // 对话注册表中的 conv_xxxxx
	Messages         []ResponsesItem // 完整对话历史
	LastResponseID   string          // 最后一个 response ID
	CreatedAt        time.Time
	LastAccessAt     time.Time
	TotalTokens      int
	HasVisionContent bool // 会话历史是否包含图片内容
}
