// Package proxy 承载与 HTTP 框架无关的请求编排核心（R4-b 第一步提取）。
//
// 当前范围：单次上游尝试的候选（BaseURL/Key）重试状态与故障分类谓词——
// 这些纯逻辑在 proxycore 内部与 gin / scheduler 耦合混放，先收拢到本包，
// 由 proxycore 委托调用。后续切片继续把尝试顺序、许可收尾与观测调用
// 逐步迁入（方案 7.1 第 4 步）。
//
// 依赖约束（go_test 静态断言）：不导入 gin、internal/handlers、SQLite 驱动。
package proxy

import (
	"context"
	"errors"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/utils"
)

// CandidateRetryState 收拢单个候选（BaseURL×Key）生命周期的 Key 失败与
// 同候选重试状态。每进入一个 BaseURL 都必须创建新实例，避免状态跨候选泄漏。
type CandidateRetryState struct {
	failedKeys           map[string]bool
	sameCandidateRetries map[string]int
	maxSameCandidate     int
	maxRetries           int
}

// DefaultMaxSameCandidateRetries 同一候选（URL×Key）的最大就地重试次数。
const DefaultMaxSameCandidateRetries = 1

// NewCandidateRetryState 构造候选重试状态。
func NewCandidateRetryState(apiKeyCount int) *CandidateRetryState {
	return &CandidateRetryState{
		failedKeys:           make(map[string]bool),
		sameCandidateRetries: make(map[string]int),
		maxSameCandidate:     DefaultMaxSameCandidateRetries,
		maxRetries:           apiKeyCount * (DefaultMaxSameCandidateRetries + 1),
	}
}

// FailedKeys 返回已失败 Key 集合的快照（供 NextAPIKey 排除选取）。
// 每次调用拷贝一份，避免调用方直接修改内部状态。
func (s *CandidateRetryState) FailedKeys() map[string]bool {
	if s == nil {
		return nil
	}
	snapshot := make(map[string]bool, len(s.failedKeys))
	for key, failed := range s.failedKeys {
		snapshot[key] = failed
	}
	return snapshot
}

// MaxRetries 返回本候选集的总尝试上限。
func (s *CandidateRetryState) MaxRetries() int {
	if s == nil {
		return 0
	}
	return s.maxRetries
}

// MarkKeyFailed 标记 Key 失败（本候选集内不再选取）。
func (s *CandidateRetryState) MarkKeyFailed(apiKey string) {
	if s == nil {
		return
	}
	s.failedKeys[apiKey] = true
}

// CanRetryCandidate 判断同候选是否还可就地重试（含 context 取消检查）。
func (s *CandidateRetryState) CanRetryCandidate(candidate string, ctx context.Context) bool {
	if s == nil || s.sameCandidateRetries[candidate] >= s.maxSameCandidate {
		return false
	}
	return ctx != nil && ctx.Err() == nil
}

// RecordCandidateRetry 记录一次同候选重试并返回已重试次数。
func (s *CandidateRetryState) RecordCandidateRetry(candidate string) int {
	if s == nil {
		return 0
	}
	s.sameCandidateRetries[candidate]++
	return s.sameCandidateRetries[candidate]
}

// RetrySameCandidateError 表示响应已成功到达代理，但模型暂时无法产出内容。
// 这类错误属于模型容量状态，不应污染 API Key、BaseURL 或渠道的失败指标。
type RetrySameCandidateError struct {
	err error
}

func (e *RetrySameCandidateError) Error() string {
	if e == nil || e.err == nil {
		return "upstream model is temporarily unavailable"
	}
	return e.err.Error()
}

func (e *RetrySameCandidateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// NewRetrySameCandidateError 构造同候选重试错误。
func NewRetrySameCandidateError(err error) error {
	return &RetrySameCandidateError{err: err}
}

// IsRetrySameCandidateError 判断错误是否为同候选重试类。
func IsRetrySameCandidateError(err error) bool {
	var target *RetrySameCandidateError
	return asRetrySameCandidate(err, &target)
}

func asRetrySameCandidate(err error, target **RetrySameCandidateError) bool {
	for err != nil {
		if typed, ok := err.(*RetrySameCandidateError); ok {
			*target = typed
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// IsUpstreamModelCapacityErrorBody 判断上游响应体是否为已知模型容量错误。
// 判据唯一出处 utils（R4-a 收敛）。
func IsUpstreamModelCapacityErrorBody(body []byte) bool {
	return utils.IsUpstreamModelCapacityErrorBody(body)
}

// IsClientSideCancel 判断错误是否为客户端明确取消。
//
// 浏览器或本机隧道在响应头阶段断开时，Windows 的 net/http 可能把写回错误
// 表示为 wsasend，而不是 context.Canceled。10054 是 forcibly closed by the
// remote host，10053 是 aborted by the software in your host machine。
// 两者都发生在代理向客户端写响应的阶段，不应把已经返回 200 的上游渠道记为失败。
func IsClientSideCancel(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "wsasend") {
		return false
	}
	return strings.Contains(message, "forcibly closed by the remote host") ||
		strings.Contains(message, "aborted by the software in your host machine")
}
