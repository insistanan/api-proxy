package hooks

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/BenedictKing/api-proxy/internal/sensitive"
)

// HookStage 表示代理请求生命周期中的 Hook 执行阶段。
type HookStage string

const (
	HookStagePreRequest   HookStage = "pre_request"
	HookStageRequestTime  HookStage = "request_time"
	HookStagePostResponse HookStage = "post_response"
)

// HookContext 是一次管道执行期间保持不变的请求元数据。
type HookContext struct {
	APIType         string
	PayloadProtocol string
	Model           string
	Stream          bool
	RequestID       string
	ConversationID  string
	ChannelName     string
	eventDeduper    *safetyEventDeduper
	redactionVault  *sensitive.Vault
}

type safetyEventDeduper struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newSafetyEventDeduper() *safetyEventDeduper {
	return &safetyEventDeduper{seen: make(map[string]struct{})}
}

func (d *safetyEventDeduper) contains(key string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, exists := d.seen[key]
	return exists
}

func (d *safetyEventDeduper) mark(key string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.seen[key] = struct{}{}
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

// Hook 是三阶段管道中的一个处理单元。Priority 数值越小越先执行；
// 优先级相同时保持注册顺序。
type Hook interface {
	Name() string
	Stage() HookStage
	Priority() int
	Run(context.Context, HookContext, HookResult) (HookResult, error)
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

type registeredHook struct {
	name string
	hook Hook
}

// Pipeline 管理三阶段 Hook。注册使用写时复制，使已开始执行的请求
// 始终使用稳定快照，且不阻塞 Hook 的实际执行。
type Pipeline struct {
	mu                 sync.RWMutex
	hooks              map[HookStage][]registeredHook
	blockedLogRecorder BlockedLogRecorder
	cmdGate            *CmdGate
}

func NewPipeline() *Pipeline {
	return &Pipeline{hooks: make(map[HookStage][]registeredHook, 3)}
}

// Register 注册一个 Hook。相同阶段内的 Hook 名必须唯一。
func (p *Pipeline) Register(hook Hook) error {
	if p == nil {
		return fmt.Errorf("Hook 管道不能为空")
	}
	if hook == nil {
		return fmt.Errorf("Hook 不能为空")
	}

	name := strings.TrimSpace(hook.Name())
	if name == "" {
		return fmt.Errorf("Hook 名称不能为空")
	}
	stage := hook.Stage()
	if !stage.valid() {
		return fmt.Errorf("Hook %q 使用了无效阶段 %q", name, stage)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	current := p.hooks[stage]
	for _, entry := range current {
		if entry.name == name {
			return fmt.Errorf("%s 阶段已注册 Hook %q", stage, name)
		}
	}

	next := make([]registeredHook, len(current), len(current)+1)
	copy(next, current)
	next = append(next, registeredHook{name: name, hook: hook})
	sort.SliceStable(next, func(i, j int) bool {
		return next[i].hook.Priority() < next[j].hook.Priority()
	})
	p.hooks[stage] = next
	return nil
}

// Run 执行指定阶段的 Hook。发生错误时立即停止，并返回错误前已经产生的变更。
func (p *Pipeline) Run(ctx context.Context, stage HookStage, metadata HookContext, initial HookResult) (HookResult, error) {
	if p == nil {
		return initial, fmt.Errorf("Hook 管道不能为空")
	}
	if ctx == nil {
		return initial, fmt.Errorf("Hook 执行上下文不能为空")
	}
	if !stage.valid() {
		return initial, fmt.Errorf("无效 Hook 阶段 %q", stage)
	}

	p.mu.RLock()
	snapshot := p.hooks[stage]
	p.mu.RUnlock()

	result := initial
	for _, entry := range snapshot {
		next, err := entry.hook.Run(ctx, metadata, result)
		if err != nil {
			return result, &HookExecutionError{Stage: stage, Hook: entry.name, Err: err}
		}
		result = next
	}
	return result, nil
}

func (p *Pipeline) RunPreRequest(ctx context.Context, metadata HookContext, initial HookResult) (HookResult, error) {
	return p.Run(ctx, HookStagePreRequest, metadata, initial)
}

func (p *Pipeline) RunRequestTime(ctx context.Context, metadata HookContext, initial HookResult) (HookResult, error) {
	return p.Run(ctx, HookStageRequestTime, metadata, initial)
}

func (p *Pipeline) RunPostResponse(ctx context.Context, metadata HookContext, initial HookResult) (HookResult, error) {
	return p.Run(ctx, HookStagePostResponse, metadata, initial)
}

func (p *Pipeline) snapshot(stage HookStage) []registeredHook {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	snapshot := p.hooks[stage]
	p.mu.RUnlock()
	return snapshot
}

func (s HookStage) valid() bool {
	switch s {
	case HookStagePreRequest, HookStageRequestTime, HookStagePostResponse:
		return true
	default:
		return false
	}
}
