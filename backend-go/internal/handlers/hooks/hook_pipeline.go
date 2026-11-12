package hooks

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"

	"github.com/insistanan/api-proxy/internal/dialogue/semantic"
	"github.com/insistanan/api-proxy/internal/safety/sensitive"
)

// HookStage 表示代理请求生命周期中的 Hook 执行阶段。
type HookStage string

const (
	HookStagePreRequest   HookStage = "pre_request"
	HookStagePostResponse HookStage = "post_response"
)

// HookContext 是一次管道执行期间保持不变的请求元数据。
type HookContext struct {
	APIType            string
	PayloadProtocol    string
	Model              string
	Stream             bool
	RequestID          string
	ConversationID     string
	ChannelName        string
	eventDeduper       *safetyEventDeduper
	redactionVault     *sensitive.Vault
	preRequestSnapshot *contentSafetySnapshot
}

// 独立执行处理器时可不提供请求级去重器；AttachHookPipeline 会为代理请求创建共享实例。
type safetyEventDeduper struct {
	mu         sync.Mutex
	seen       map[[sha256.Size]byte]struct{}
	unassigned bool
}

func newSafetyEventDeduper() *safetyEventDeduper {
	return &safetyEventDeduper{seen: make(map[[sha256.Size]byte]struct{})}
}

func (d *safetyEventDeduper) contains(key string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, exists := d.seen[sha256.Sum256([]byte(key))]
	return exists
}

func (d *safetyEventDeduper) mark(key string, unassigned bool) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	hash := sha256.Sum256([]byte(key))
	if _, exists := d.seen[hash]; !exists && len(d.seen) >= 8192 {
		return fmt.Errorf("单次请求的内容安全审计事件超过 8192 条上限")
	}
	d.seen[hash] = struct{}{}
	if unassigned {
		d.unassigned = true
	}
	return nil
}

func (d *safetyEventDeduper) hasUnassigned() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.unassigned
}

func (d *safetyEventDeduper) markAssigned() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.unassigned = false
	d.mu.Unlock()
}

func (d *safetyEventDeduper) markUnassigned() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.unassigned = true
	d.mu.Unlock()
}

// HookResult 承载 Hook 可变更并传递给后续 Hook 的数据。
// 调用方应为每次请求创建独立实例，不应在并发请求之间复用。
type HookResult struct {
	RequestBody      []byte
	Prompts          []string
	UpstreamRequest  *http.Request
	UpstreamResponse *http.Response
	ResponseBody     []byte
	StreamChunk      []byte
	StreamFinal      bool
}

// BlockedLogRecorder 是内容安全管道写入拦截事件所需的最小存储接口。
// sensitive.BlockedStore 满足该接口，测试可注入轻量实现验证错误路径。
type BlockedLogRecorder interface {
	Record(context.Context, sensitive.BlockedLog) (sensitive.BlockedLog, error)
}

type blockedLogConversationAssigner interface {
	AssignConversation(context.Context, string, string) error
}

// HookExecutionError 标识具体阶段和 Hook 的执行失败。
type HookExecutionError struct {
	Stage HookStage
	Hook  string
	Err   error
}

func (e *HookExecutionError) Error() string {
	return fmt.Sprintf("%s 阶段 Hook %q 执行失败: %v", e.Stage, e.Hook, e.Err)
}

func (e *HookExecutionError) Unwrap() error {
	return e.Err
}

// Pipeline 在构造时装配请求前和响应后两个内置处理器，供并发请求共享。
// 处理器自行管理配置快照，管道不提供动态注册或优先级编排。
type Pipeline struct {
	preRequest         *contentSafetyPreRequestHook
	postResponse       *contentSafetyPostResponseHook
	blockedLogRecorder BlockedLogRecorder
	cmdGate            *CmdGate
	semantic           *semantic.Service
}

func NewPipeline() *Pipeline {
	return &Pipeline{}
}

// Run 执行指定阶段的内置处理器；失败时保留输入并返回带阶段信息的错误。
func (p *Pipeline) Run(ctx context.Context, stage HookStage, metadata HookContext, initial HookResult) (HookResult, error) {
	if p == nil {
		return initial, fmt.Errorf("Hook 管道不能为空")
	}
	if ctx == nil {
		return initial, fmt.Errorf("Hook 执行上下文不能为空")
	}
	var (
		result HookResult
		name   string
		err    error
	)
	switch stage {
	case HookStagePreRequest:
		if p.preRequest == nil {
			return initial, nil
		}
		name = "content-safety"
		result, err = p.preRequest.Run(ctx, metadata, initial)
	case HookStagePostResponse:
		if p.postResponse == nil {
			return initial, nil
		}
		name = "response-content-safety"
		result, err = p.postResponse.Run(ctx, metadata, initial)
	default:
		return initial, fmt.Errorf("无效 Hook 阶段 %q", stage)
	}
	if err != nil {
		return initial, &HookExecutionError{Stage: stage, Hook: name, Err: err}
	}
	return result, nil
}

func (p *Pipeline) RunPreRequest(ctx context.Context, metadata HookContext, initial HookResult) (HookResult, error) {
	return p.Run(ctx, HookStagePreRequest, metadata, initial)
}

func (p *Pipeline) RunPostResponse(ctx context.Context, metadata HookContext, initial HookResult) (HookResult, error) {
	return p.Run(ctx, HookStagePostResponse, metadata, initial)
}
