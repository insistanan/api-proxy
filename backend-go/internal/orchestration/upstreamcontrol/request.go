package upstreamcontrol

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 全请求预算覆盖主请求、兼容重试和辅助推理，不作为成功流的总超时。
const MaxAttempts = 12
const MaxRetryWait = 30 * time.Second

var ErrBudget = errors.New("请求已耗尽上游尝试预算（最多 12 次发送、累计等待 30 秒）")
var ErrSkipped = errors.New("本请求已放弃此上游组合，跳过重复队列")

type scopeKey struct{}
type candidateKey struct{}

type Scope struct {
	mu       sync.Mutex
	attempts int
	waited   time.Duration
	skipped  map[Identity]bool
}

type Candidate struct {
	Controller    *Controller
	Identity      Identity
	Compatibility bool
}

func WithScope(ctx context.Context) context.Context {
	if ScopeFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, scopeKey{}, &Scope{skipped: make(map[Identity]bool)})
}

func ScopeFromContext(ctx context.Context) *Scope {
	scope, _ := ctx.Value(scopeKey{}).(*Scope)
	return scope
}

func WithCandidate(ctx context.Context, c *Controller, id Identity, compatibility ...bool) context.Context {
	bound := Candidate{Controller: c, Identity: id}
	if len(compatibility) > 0 {
		bound.Compatibility = compatibility[0]
	}
	return context.WithValue(ctx, candidateKey{}, bound)
}

func CandidateFromContext(ctx context.Context) (Candidate, bool) {
	candidate, ok := ctx.Value(candidateKey{}).(Candidate)
	return candidate, ok
}

func IsLocalError(err error) bool {
	return IsAdmissionError(err) || errors.Is(err, ErrSkipped) || errors.Is(err, ErrBudget)
}

func (s *Scope) acquire(ctx context.Context, candidate Candidate) (*Lease, time.Duration, error) {
	s.mu.Lock()
	if s.skipped[candidate.Identity] {
		s.mu.Unlock()
		return nil, 0, ErrSkipped
	}
	remaining := MaxRetryWait - s.waited
	exhausted := s.attempts >= MaxAttempts || remaining <= 0
	s.mu.Unlock()
	if exhausted {
		return nil, 0, ErrBudget
	}
	queueCtx, cancel := context.WithTimeout(ctx, remaining)
	lease, waited, err := candidate.Controller.Acquire(queueCtx, candidate.Identity)
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waited += waited
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		err = ErrBudget
	}
	if IsAdmissionError(err) {
		s.skipped[candidate.Identity] = true
	}
	if err == nil {
		if s.attempts >= MaxAttempts || s.waited >= MaxRetryWait {
			lease.Release()
			return nil, waited, ErrBudget
		}
		s.attempts++
	}
	return lease, waited, err
}

func AcquireRequest(ctx context.Context) (*Lease, time.Duration, int, error) {
	candidate, bound := CandidateFromContext(ctx)
	if !bound {
		// 非推理的 HTTP 操作不参与 LLM 准入。
		return nil, 0, 0, nil
	}
	if candidate.Controller == nil {
		return nil, 0, 0, fmt.Errorf("上游共享准入控制器未初始化")
	}
	scope := ScopeFromContext(ctx)
	if scope == nil {
		return nil, 0, 0, fmt.Errorf("上游请求未绑定统一尝试预算")
	}
	lease, waited, err := scope.acquire(ctx, candidate)
	scope.mu.Lock()
	attempts := scope.attempts
	scope.mu.Unlock()
	return lease, waited, attempts, err
}

// Record429 支持 Retry-After 秒数和 HTTP 日期，无效时指数退避并添加抖动。
func Record429(ctx context.Context, header http.Header) {
	candidate, ok := CandidateFromContext(ctx)
	if !ok || candidate.Controller == nil {
		return
	}
	now := time.Now()
	value := strings.TrimSpace(header.Get("Retry-After"))
	until := time.Time{}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/int64(time.Second)) {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(value); err == nil && date.After(now) {
		until = date
	}
	if until.IsZero() {
		attempts := 1
		if scope := ScopeFromContext(ctx); scope != nil {
			scope.mu.Lock()
			attempts = scope.attempts
			scope.mu.Unlock()
		}
		exponent := min(max(attempts-1, 0), 5)
		base := time.Second * time.Duration(1<<exponent)
		until = now.Add(base + time.Duration(rand.Int64N(int64(base/2)+1)))
	}
	candidate.Controller.CoolDown(candidate.Identity, until)
}
