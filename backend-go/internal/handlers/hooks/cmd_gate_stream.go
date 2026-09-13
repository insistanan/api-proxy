package hooks

import (
	"context"
	"fmt"
	"io"

	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/gin-gonic/gin"
)

// StreamVetoGuard 是"AI 放行后的事后否决"信号：用户在否决窗口内加黑名单时
// Trigger 一次，各协议的流式转发循环通过 Done() 感知并终止当前流。
// 幂等：重复 Trigger 无副作用；未 arm 的 guard 永不触发（nil channel 语义）。
type StreamVetoGuard struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func newStreamVetoGuard() *StreamVetoGuard {
	ctx, cancel := context.WithCancel(context.Background())
	return &StreamVetoGuard{ctx: ctx, cancel: cancel}
}

// Trigger 触发否决信号（幂等）。
func (g *StreamVetoGuard) Trigger() {
	if g == nil {
		return
	}
	g.cancel()
}

// Done 返回否决信号通道；未 arm 时返回 nil（select 分支永不命中，安全）。
func (g *StreamVetoGuard) Done() <-chan struct{} {
	if g == nil {
		return nil
	}
	return g.ctx.Done()
}

// BindAttachedStreamVeto 关闭原上游响应体，打断正在阻塞的 Read；回调不持有 Gin 上下文。
func BindAttachedStreamVeto(c requestContextGetter, reader io.Closer) func() {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil || attached.vetoGuard == nil || reader == nil {
		return func() {}
	}
	stop := context.AfterFunc(attached.vetoGuard.ctx, func() { _ = reader.Close() })
	return func() { stop() }
}

func AttachedStreamVetoError(c requestContextGetter) error {
	select {
	case <-AttachedStreamVetoDone(c):
		return NewStreamVetoError()
	default:
		return nil
	}
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
// 各协议流式循环返回它，由协议上层统一写出错误响应。
func NewStreamVetoError() *ContentSafetyError {
	return &ContentSafetyError{
		BlockType: sensitive.BlockTypeDangerousCmdAI,
		RuleName:  "stream_veto",
		Snippet:   "mode=ai_gate source=assistant\nAI 判定放行的命令被用户否决",
	}
}

// HandleAttachedStreamVeto 返回内容安全错误，由协议上层唯一一次写出错误和终止序列。
func HandleAttachedStreamVeto(_ *gin.Context) error {
	return NewStreamVetoError()
}

// gateReviewDangerousCommand 是危险命令命中的统一审查入口。
// 闸门启用时走 CmdGate.Review（名单 → AI 二次判断 → 用户决定窗口），
// 未启用或基础设施缺失时退化为既有"命中即拦截"。
// 返回 nil 表示放行；返回 ContentSafetyError 表示拦截。
// 该函数会阻塞（AI 调用 + 用户决定窗口），调用点位于流式转发循环内时
// 即达成"暂停 SSE 转发、对客户端表现为响应变慢"的挂起效果。
// 调用方持有 attached.streamMu：完整脚本命中多条规则时只审核一次，
// 同一请求内复用完全相同的命令身份，事后否决仍优先终止整个请求。
func gateReviewDangerousCommand(ctx context.Context, c requestContextGetter, attached *attachedHookPipeline, metadata HookContext, match sensitive.CommandMatch) *ContentSafetyError {
	if attached == nil || attached.pipeline == nil || attached.pipeline.cmdGate == nil || !attached.pipeline.cmdGate.Enabled() {
		return dangerousCommandError(match)
	}
	if err := AttachedStreamVetoError(c); err != nil {
		return NewStreamVetoError()
	}
	commandHash := sensitive.CommandIdentity(match)
	if _, allowed := attached.allowedCommands[commandHash]; allowed {
		return nil
	}
	if len(attached.allowedCommands) >= 256 {
		return &ContentSafetyError{
			BlockType: sensitive.BlockTypeDangerousCmdAI,
			RuleName:  "command_count_limit",
			Snippet:   "单次响应的危险命令超过 256 条检查上限",
		}
	}
	// 只捕获本请求独立 guard；请求结束后 Gin Context 会被池复用。
	guard := attached.vetoGuard
	if attached.lifecycleCtx != nil {
		ctx = attached.lifecycleCtx
	}
	cancel := attached.lifecycleCancel
	onVeto := func() {
		guard.Trigger()
		if cancel != nil {
			cancel()
		}
	}
	verdict := attached.pipeline.cmdGate.Review(ctx, metadata, match, onVeto)
	if err := AttachedStreamVetoError(c); err != nil {
		return NewStreamVetoError()
	}
	if verdict.Allow {
		if attached.allowedCommands == nil {
			attached.allowedCommands = make(map[string]struct{})
		}
		attached.allowedCommands[commandHash] = struct{}{}
		return nil
	}
	blockType := verdict.BlockType
	if blockType == "" {
		blockType = sensitive.BlockTypeDangerousCmdAI
	}
	return &ContentSafetyError{
		BlockType:   blockType,
		RuleName:    match.Rule,
		Snippet:     fmt.Sprintf("mode=ai_gate source=assistant context=%s\n%s\n裁决: %s", match.Context, match.Command, verdict.Reason),
		Command:     match.Command,
		CommandHash: commandHash,
		AIReview:    verdict.AIReview,
		audited:     verdict.audited,
	}
}
