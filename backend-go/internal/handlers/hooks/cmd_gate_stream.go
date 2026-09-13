package hooks

import (
	"context"
	"fmt"
	"sync"

	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/gin-gonic/gin"
)

// StreamVetoGuard 是"AI 放行后的事后否决"信号：用户在否决窗口内加黑名单时
// Trigger 一次，各协议的流式转发循环通过 Done() 感知并终止当前流。
// 幂等：重复 Trigger 无副作用；未 arm 的 guard 永不触发（nil channel 语义）。
type StreamVetoGuard struct {
	ch   chan struct{}
	once sync.Once
}

func newStreamVetoGuard() *StreamVetoGuard {
	return &StreamVetoGuard{ch: make(chan struct{})}
}

// Trigger 触发否决信号（幂等）。
func (g *StreamVetoGuard) Trigger() {
	if g == nil {
		return
	}
	g.once.Do(func() { close(g.ch) })
}

// Done 返回否决信号通道；未 arm 时返回 nil（select 分支永不命中，安全）。
func (g *StreamVetoGuard) Done() <-chan struct{} {
	if g == nil {
		return nil
	}
	return g.ch
}

// TriggerAttachedStreamVeto 触发当前请求的事后否决（CmdGate 的 onVeto 回调使用）。
func TriggerAttachedStreamVeto(c requestContextGetter) {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return
	}
	attached.vetoGuard.Trigger()
}

// AttachedStreamVetoDone 返回当前请求的否决信号通道。
// 供各协议流式转发循环的 select 分支监听；无绑定管道时返回 nil channel。
func AttachedStreamVetoDone(c requestContextGetter) <-chan struct{} {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return nil
	}
	return attached.vetoGuard.Done()
}

// NewStreamVetoError 构造"事后否决"的内容安全错误。
// 各协议流式循环收到否决信号后用 HandleAttachedStreamVeto 写出并返回它。
func NewStreamVetoError() *ContentSafetyError {
	return &ContentSafetyError{
		BlockType: sensitive.BlockTypeDangerousCmdAI,
		RuleName:  "stream_veto",
		Snippet:   "mode=ai_gate source=assistant\nAI 判定放行的命令被用户否决",
	}
}

// HandleAttachedStreamVeto 处理流式转发中的事后否决：写出当前协议的
// 内容安全错误 SSE 事件与流终止序列，返回内容安全错误供调用方结束流循环。
func HandleAttachedStreamVeto(c *gin.Context) error {
	vetoErr := NewStreamVetoError()
	if writeErr := WriteAttachedStreamError(c, vetoErr); writeErr != nil {
		return fmt.Errorf("写出否决流错误失败: %w", writeErr)
	}
	return vetoErr
}

// gateReviewDangerousCommand 是危险命令命中的统一审查入口。
// 闸门启用时走 CmdGate.Review（名单 → AI 二次判断 → 用户决定窗口），
// 未启用或基础设施缺失时退化为既有"命中即拦截"。
// 返回 nil 表示放行；返回 ContentSafetyError 表示拦截。
// 该函数会阻塞（AI 调用 + 用户决定窗口），调用点位于流式转发循环内时
// 即达成"暂停 SSE 转发、对客户端表现为响应变慢"的挂起效果。
// 调用方持有 attached.streamMu：同一命令在同一请求内放行一次后不再送审
// （StreamCmdScanner 命中后每个后续 chunk 都重复返回同一命中）。
func gateReviewDangerousCommand(ctx context.Context, c requestContextGetter, attached *attachedHookPipeline, metadata HookContext, match sensitive.CommandMatch) *ContentSafetyError {
	if attached == nil || attached.pipeline == nil || attached.pipeline.cmdGate == nil || !attached.pipeline.cmdGate.Enabled() {
		return dangerousCommandError(match)
	}
	if _, allowed := attached.allowedCommands[match.Command]; allowed {
		return nil
	}
	onVeto := func() { TriggerAttachedStreamVeto(c) }
	verdict := attached.pipeline.cmdGate.Review(ctx, metadata, match, onVeto)
	if verdict.Allow {
		if attached.allowedCommands == nil {
			attached.allowedCommands = make(map[string]struct{})
		}
		attached.allowedCommands[match.Command] = struct{}{}
		return nil
	}
	blockType := verdict.BlockType
	if blockType == "" {
		blockType = sensitive.BlockTypeDangerousCmdAI
	}
	return &ContentSafetyError{
		BlockType: blockType,
		RuleName:  match.Rule,
		Snippet:   fmt.Sprintf("mode=ai_gate source=assistant context=%s\n%s\n裁决: %s", match.Context, match.Command, verdict.Reason),
	}
}
