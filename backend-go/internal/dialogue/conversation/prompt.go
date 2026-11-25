package conversation

import "strings"

// NormalizeUserPrompt 仅规范换行与首尾空白。正文内容不能证明其来源，
// 标签、命令和文档均可能是用户正在讨论的真实输入。
func NormalizeUserPrompt(prompt string) string {
	return strings.TrimSpace(strings.ReplaceAll(prompt, "\r\n", "\n"))
}
