// Package proxy 管理候选重试状态和错误分类，这些逻辑不依赖 HTTP 框架。
//
// proxycore 调用本包记录 BaseURL 和 API Key 候选的失败情况、同一候选的重试次数，
// 并判断请求是否由客户端取消。HTTP 请求、渠道许可和结果记录仍由 proxycore 负责。
//
// 依赖约束（go_test 静态断言）：不导入 gin、internal/api、SQLite 驱动。
package proxy

import (
	"context"
	"errors"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/types"
)

// CandidateRetryState 保存一个 BaseURL 下的失败 Key 和同候选重试次数。
// 每进入一个 BaseURL 都要创建新实例，避免上一轮失败状态影响后续候选。
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
	return errors.As(err, &target)
}

// IsUpstreamModelCapacityErrorBody 判断上游响应体是否为已知模型容量错误。
// 判据位于 internal/infra/upstreamreq。
func IsUpstreamModelCapacityErrorBody(body []byte) bool {
	return upstreamreq.IsUpstreamModelCapacityErrorBody(body)
}

// IsClientSideCancel 仅使用明确的取消或下游交付来源，不从网络错误文本猜测方向。
func IsClientSideCancel(err error) bool {
	switch wrapped := err.(type) {
	case *types.DownstreamWriteError:
		return true
	case interface{ Unwrap() []error }:
		// 上游失败后的通知也可能写失败，附加错误不能覆盖原始故障来源。
		causes := wrapped.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !IsClientSideCancel(cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return IsClientSideCancel(wrapped.Unwrap())
	default:
		return errors.Is(err, context.Canceled)
	}
}
