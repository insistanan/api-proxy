package types

import "time"

// Session 会话数据结构（Responses API 多轮对话跟踪）。
// 历史上定义在 session 包；因其是纯数据体且被 converters（纯转换层）
// 按参数类型广泛引用，下沉到 types 以消除 converters → session 的
// 低层→高层反向依赖。session 包通过类型别名 `session.Session` 保持
// 既有引用兼容。
type Session struct {
	ID               string          // sess_xxxxx
	ConversationID   string          // 对话注册表中的 conv_xxxxx
	Messages         []ResponsesItem // 完整对话历史
	LastResponseID   string          // 最后一个 response ID
	CreatedAt        time.Time
	LastAccessAt     time.Time
	TotalTokens      int
	HasVisionContent bool // 会话历史是否包含图片内容
}
