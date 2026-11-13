package logger

import (
	"context"
	"fmt"
	"time"
)

// StoragePolicy 的保留天数按本地日历日计算，包含今天；预算使用 SQLite 已占用页。
type StoragePolicy struct {
	RuntimeDays int `json:"runtimeDays"`
	CallDays    int `json:"callDays"`
	BodyDays    int `json:"bodyDays"`
	BudgetMB    int `json:"budgetMB"`
}

func DefaultStoragePolicy() StoragePolicy {
	return StoragePolicy{RuntimeDays: 2, CallDays: 2, BodyDays: 2}
}

func (s *Store) storagePolicy() StoragePolicy {
	if s.policyProvider != nil {
		return s.policyProvider()
	}
	return DefaultStoragePolicy()
}

// CapturePolicy 在入口绑定到 context，整个请求（包括重试和流式收尾）复用此值。
type CapturePolicy struct {
	Request   bool
	Response  bool
	Mode      string
	BodyLimit int
	Paused    bool
}

type capturePolicyKey struct{}

func ContextWithCapturePolicy(ctx context.Context, policy CapturePolicy) context.Context {
	return context.WithValue(ctx, capturePolicyKey{}, policy)
}

func CapturePolicyFromContext(ctx context.Context) (CapturePolicy, bool) {
	if ctx == nil {
		return CapturePolicy{}, false
	}
	policy, ok := ctx.Value(capturePolicyKey{}).(CapturePolicy)
	return policy, ok
}

func (s *Store) BodyCapturePaused() bool {
	if s == nil {
		return false
	}
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	return s.budgetPaused
}

func (policy CapturePolicy) BodyState(response bool) string {
	if (!response && !policy.Request) || (response && !policy.Response) {
		return "disabled"
	}
	if policy.Mode == "metadata" {
		return "metadata"
	}
	if policy.Paused {
		return "budget"
	}
	return ""
}

func contentPhase(phase string) bool {
	switch phase {
	case PhaseClientRequest, PhaseClientInput, PhaseUpstreamRequest, PhaseUpstreamResponse, PhaseStreamSynth:
		return true
	default:
		return false
	}
}

func (entry TrafficLog) WithCapturePolicy(policy CapturePolicy) TrafficLog {
	entry.capture = &policy
	return entry
}

func RecordTrafficContext(ctx context.Context, entry TrafficLog) {
	if policy, ok := CapturePolicyFromContext(ctx); ok {
		entry = entry.WithCapturePolicy(policy)
	}
	RecordTraffic(entry)
}

func applyCapture(entry TrafficLog) TrafficLog {
	if !contentPhase(entry.Phase) || entry.capture == nil {
		return entry
	}
	response := entry.Phase == PhaseUpstreamResponse || entry.Phase == PhaseStreamSynth
	state := entry.capture.BodyState(response)
	if state != "" {
		entry.Body, entry.HeadersJSON = "", ""
		entry.BodyState, entry.Truncated = state, false
		return entry
	}
	if !entry.bodyPrepared {
		body, original, truncated := PrepareBodyWithLimit([]byte(entry.Body), entry.capture.BodyLimit)
		entry.Body = body
		if entry.OriginalBytes == 0 {
			entry.OriginalBytes = original
		}
		entry.Truncated = entry.Truncated || truncated
		entry.bodyPrepared = true
	} else if len(entry.Body) > entry.capture.BodyLimit {
		entry.Body = OmittedBodyWithLimit(entry.OriginalBytes, entry.capture.BodyLimit)
		entry.Truncated = true
	}
	if entry.BodyState == "" && entry.Truncated {
		entry.BodyState = "truncated"
	}
	return entry
}

func retentionCutoffDays(now time.Time, days int) time.Time {
	local := now.In(time.Local)
	year, month, day := local.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local).AddDate(0, 0, 1-days)
}

func validateStoragePolicy(policy StoragePolicy) error {
	if policy.RuntimeDays < 1 || policy.RuntimeDays > 365 || policy.CallDays < 1 || policy.CallDays > 365 || policy.BodyDays < 1 || policy.BodyDays > policy.CallDays {
		return fmt.Errorf("日志保留策略无效")
	}
	if policy.BudgetMB != 0 && (policy.BudgetMB < 64 || policy.BudgetMB > 1048576) {
		return fmt.Errorf("日志预算无效")
	}
	return nil
}
