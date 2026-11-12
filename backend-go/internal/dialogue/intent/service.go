package intent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/dialogue/semantic"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/safety/llmgate"
)

type Target struct {
	PoolID    string `json:"poolId"`
	ChannelID string `json:"channelId"`
	Model     string `json:"model"`
	Identity  string `json:"identity"`
}

type sessionState struct {
	Task    string    `json:"task,omitempty"`
	Epoch   uint64    `json:"epoch"`
	Target  Target    `json:"target"`
	Since   time.Time `json:"since"`
	Updated time.Time `json:"updated"`
	Turns   int       `json:"turns"`
	Tier    int       `json:"tier"`
	Digest  string    `json:"digest"`
}

type Event struct {
	Control        bool      `json:"control,omitempty"`
	RequestID      string    `json:"requestId"`
	Protocol       string    `json:"protocol"`
	ConversationID string    `json:"conversationId"`
	Decision       Decision  `json:"decision"`
	Target         Target    `json:"target"`
	At             time.Time `json:"at"`
	Success        bool      `json:"success"`
	Feedback       *bool     `json:"feedback,omitempty"`
}

type quality struct {
	Good float64 `json:"good"`
	Bad  float64 `json:"bad"`
}
type persistedState struct {
	Sessions map[string]sessionState `json:"sessions"`
	Quality  map[string]quality      `json:"quality"`
	Events   []Event                 `json:"events"`
}

type Service struct {
	Cfg      *config.ConfigManager
	Caller   *llmgate.Caller
	Local    *LocalRuntime
	Datasets *semantic.Service
	mu       sync.Mutex
	state    persistedState
	busy     map[string]bool
	path     string
	store    *stateStore
}

func New(cfg *config.ConfigManager, caller *llmgate.Caller, path string, local *LocalRuntime) (*Service, error) {
	s := &Service{Cfg: cfg, Caller: caller, Local: local, path: path, busy: make(map[string]bool)}
	var err error
	s.store, s.state, err = openStateStore(path)
	if err != nil {
		return nil, fmt.Errorf("打开意图会话存储失败: %w", err)
	}
	s.Datasets, err = semantic.New(filepath.Join(filepath.Dir(path), "datasets.db"), s)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("打开数据集存储失败: %w", err), s.store.db.Close())
	}
	return s, nil
}

func (s *Service) Close() error { return errors.Join(s.Datasets.Close(), s.store.db.Close()) }

type serviceKey struct{}
type turnKey struct{}
type protocolKey struct{}

func WithProtocol(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, protocolKey{}, kind)
}
func protocolFrom(ctx context.Context) string {
	kind, _ := ctx.Value(protocolKey{}).(string)
	return kind
}
func WithService(ctx context.Context, service *Service) context.Context {
	return context.WithValue(ctx, serviceKey{}, service)
}
func FromContext(ctx context.Context) *Service {
	service, _ := ctx.Value(serviceKey{}).(*Service)
	return service
}
func TurnFrom(ctx context.Context) *Turn { turn, _ := ctx.Value(turnKey{}).(*Turn); return turn }

type Turn struct {
	manual    bool
	epoch     uint64
	service   *Service
	key       string
	previous  sessionState
	input     Input
	digest    string
	event     Event
	strict    bool
	committed bool
	accepted  Target
}

type HistoryError struct{ Message string }

func (e *HistoryError) Error() string            { return e.Message }
func (e *HistoryError) ClientRequestError() bool { return true }

type RoutingError struct {
	Code string
	Err  error
}

func (e *RoutingError) Error() string { return e.Err.Error() }
func (e *RoutingError) Unwrap() error { return e.Err }

func (t *Turn) PinnedIdentity(channelID string) string {
	if t.previous.Target.ChannelID == channelID {
		return t.previous.Target.Identity
	}
	return ""
}
func (t *Turn) HasOpaqueHistory() bool { return t.input.Opaque }
func (t *Turn) Strict() bool           { return t.strict }
func (t *Turn) PinnedModel(channelID string) string {
	if t.manual && !t.strict {
		return ""
	}
	if t.previous.Target.ChannelID == channelID {
		return t.previous.Target.Model
	}
	return ""
}

// Begin 序列化同一会话的路由决定。返回的关闭函数必须覆盖失败和取消路径。
func (s *Service) Begin(ctx context.Context, kind, conversation, requestID string, body []byte, manualPool string) (context.Context, func(), error) {
	return s.begin(ctx, kind, conversation, requestID, body, manualPool, false)
}

// BeginControl 让上下文压缩沿用会话来源，不触发分类或增加用户轮次。
func (s *Service) BeginControl(ctx context.Context, conversation, requestID string, body []byte) (context.Context, func(), error) {
	return s.begin(ctx, "responses", conversation, requestID, body, "", true)
}

func (s *Service) begin(ctx context.Context, kind, conversation, requestID string, body []byte, manualPool string, control bool) (context.Context, func(), error) {
	ctx = WithProtocol(ctx, kind)
	cfg := s.Cfg.GetIntentRouting(kind)
	if !config.SupportsIntentRouting(kind) || !cfg.Enabled {
		return ctx, func() {}, nil
	}
	input, err := ParseInput(body)
	if err != nil {
		return ctx, func() {}, err
	}
	if control {
		input.ToolContinuation = true
	}
	key := digest(kind + "\x00" + conversation)
	s.mu.Lock()
	if s.busy[key] {
		s.mu.Unlock()
		return ctx, func() {}, fmt.Errorf("该会话还有请求执行中，请等待完成后再发送")
	}
	previous := s.state.Sessions[key]
	// 关闭期间的请求未经过保护，重新启用后不能继续信任旧来源。
	if previous.Epoch != cfg.SessionEpoch {
		previous = sessionState{}
	}
	s.busy[key] = true
	s.mu.Unlock()
	release := func() { s.mu.Lock(); delete(s.busy, key); s.mu.Unlock() }
	t := &Turn{service: s, key: key, previous: previous, input: input, digest: digest(input.Text), strict: input.Opaque || input.ToolContinuation, epoch: cfg.SessionEpoch, manual: manualPool != "",
		event: Event{RequestID: requestID, Protocol: kind, ConversationID: conversation, At: time.Now(), Control: control}}
	if t.strict && previous.Target.ChannelID == "" {
		release()
		return ctx, func() {}, &HistoryError{"无法确认工具或加密历史的来源，已阻止自动路由；请在原渠道完成当前任务，或以完整可读上下文新建会话"}
	}
	decision := Decision{Tier: -1, Reason: "沿用原分组规则"}
	if manualPool != "" {
		decision.PoolID = manualPool
		decision.Reason = "显式渠道或人工会话固定优先"
	} else if previous.Target.PoolID != "" && (t.strict || previous.Digest == t.digest || time.Since(previous.Since) < time.Duration(cfg.MinResidenceSeconds)*time.Second || previous.Turns < cfg.MinTurns || cfg.Strategy == "adaptive") {
		decision.PoolID = previous.Target.PoolID
		decision.Reason = "会话驻留或连续性保护"
	} else if !input.History || previous.Target.PoolID != "" {
		decision, err = s.Classify(ctx, cfg, input, requestID)
		if err != nil {
			if cfg.FailurePolicy != "keep" {
				release()
				return ctx, func() {}, &RoutingError{Code: "INTENT_CLASSIFIER_FAILED", Err: err}
			}
			decision = Decision{PoolID: previous.Target.PoolID, Tier: -1, Reason: "分类失败，按显式配置保留原路由: " + err.Error()}
		} else if decision.PoolID == "" {
			switch cfg.Unmatched {
			case "error":
				release()
				return ctx, func() {}, &RoutingError{Code: "INTENT_UNMATCHED", Err: fmt.Errorf("意图未命中: %s", decision.Reason)}
			case "fallback":
				decision.PoolID = cfg.FallbackPoolID
			}
		}
		if previous.Target.PoolID != "" && decision.PoolID != previous.Target.PoolID {
			// 复杂度只自动升档；语义匹配比较新旧组分数，避免近似例句来回跳。
			allow := decision.Score >= cfg.Threshold && decision.Tier > previous.Tier
			if cfg.Strategy == "semantic" {
				allow = decision.Score >= cfg.Threshold && decision.Score-decision.PoolScores[previous.Target.PoolID] >= cfg.SwitchMargin
			}
			if cfg.Strategy == "auto" {
				allow = decision.PoolID != "" && decision.Task != "" && decision.Score >= cfg.Threshold && (decision.Tier > previous.Tier || decision.Task != previous.Task || !groupCanHandle(cfg, previous.Target.PoolID, decision.Task, decision.Tier))
			}
			if !allow {
				decision.PoolID = previous.Target.PoolID
				decision.Reason += "；收益不足，保持原组"
			}
		}
		if cfg.Strategy == "adaptive" && cfg.ObserveOnly {
			decision.Reason = "观察建议组 " + decision.PoolID + "；" + decision.Reason
			decision.PoolID = previous.Target.PoolID
		}
	}
	if t.strict {
		decision.PoolID = previous.Target.PoolID
		decision.Reason = "工具或加密历史固定来源"
	}
	if control {
		decision.Reason = "上下文压缩固定会话来源"
	}
	if decision.PoolID == previous.Target.PoolID && previous.Target.PoolID != "" {
		if cfg.Strategy != "auto" {
			decision.Tier = previous.Tier
		} else if decision.Task == "" && (input.ToolContinuation || previous.Digest == t.digest) {
			decision.Task, decision.Tier = previous.Task, previous.Tier
		}
	}
	t.event.Decision = decision
	route := scheduler.PoolConstraint{PoolID: decision.PoolID}
	if previous.Target.PoolID != "" && previous.Target.PoolID == decision.PoolID && manualPool == "" {
		if t.strict {
			route.ChannelID = previous.Target.ChannelID
		} else {
			route.PreferredChannelID = previous.Target.ChannelID
		}
	}
	ctx = scheduler.WithPoolConstraint(ctx, route)
	return context.WithValue(ctx, turnKey{}, t), release, nil
}

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func MakeTarget(up *config.UpstreamConfig, key, model string) Target {
	return Target{PoolID: up.PoolID, ChannelID: up.ID, Model: model, Identity: digest(up.ServiceType + "\x00" + up.BaseURL + "\x00" + key)}
}

// CheckTarget 在任何实际发送前执行，覆盖跨组、模型映射、URL 和 Key 轮转。
func (t *Turn) CheckTarget(up *config.UpstreamConfig, key, model string) error {
	target := MakeTarget(up, key, model)
	if t.committed && target != t.accepted {
		return &HistoryError{"本次响应已选定执行目标，不能在交付过程中切换模型"}
	}
	if pool := t.event.Decision.PoolID; pool != "" && target.PoolID != pool {
		return &HistoryError{"模型映射目标超出了意图选择的分组，已阻止发送"}
	}
	if t.strict && target != t.previous.Target {
		return &HistoryError{"工具或加密历史与候选渠道、模型或凭据不兼容，已阻止切换；不会删除思考块重试"}
	}
	return nil
}

// CommitTarget 在响应交付前保存真实来源；持久化失败时不开始向客户端输出。
func (t *Turn) CommitTarget(up *config.UpstreamConfig, key, model string) error {
	if err := t.CheckTarget(up, key, model); err != nil {
		return err
	}
	s := t.service
	s.mu.Lock()
	defer s.mu.Unlock()
	next := t.previous
	next.Epoch = t.epoch
	target := MakeTarget(up, key, model)
	if next.Target != target {
		next.Since = time.Now()
		next.Turns = 0
		next.Tier = t.event.Decision.Tier
	}
	if !t.input.ToolContinuation && next.Digest != t.digest {
		next.Turns++
	}
	next.Target, next.Updated = target, time.Now()
	// 驻留期中的新文本可能未分类，不能把上一任务的标签借给本轮反馈。
	next.Task = t.event.Decision.Task
	if next.Task != "" || t.event.Decision.Tier < 0 {
		next.Tier = t.event.Decision.Tier
	}
	if !t.input.ToolContinuation {
		next.Digest = t.digest
	}
	candidate := s.cloneStateLocked()
	if len(candidate.Sessions) >= 10000 {
		for id, state := range candidate.Sessions {
			if !s.busy[id] && time.Since(state.Updated) > 24*time.Hour {
				delete(candidate.Sessions, id)
			}
		}
		if _, exists := candidate.Sessions[t.key]; !exists && len(candidate.Sessions) >= 10000 {
			return fmt.Errorf("意图会话存储已满，请清理过期会话")
		}
	}
	candidate.Sessions[t.key] = next
	t.event.Target = target
	t.event.Target.Identity = "" // 管理界面不暴露凭据指纹。
	candidate.putEvent(t.event)
	if err := s.saveLocked(candidate); err != nil {
		return err
	}
	t.committed = true
	t.accepted = target
	log.Printf("[Intent-Route] requestId=%s protocol=%s pool=%s channel=%s model=%s reason=%s durationMs=%d", t.event.RequestID, t.event.Protocol, target.PoolID, target.ChannelID, model, t.event.Decision.Reason, t.event.Decision.DurationMS)
	return nil
}

func (t *Turn) Complete(success bool) error {
	if !t.committed {
		return nil
	}
	s := t.service
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := s.cloneStateLocked()
	event := t.event
	event.Success = success
	candidate.putEvent(event)
	return s.saveLocked(candidate)
}

// AbortUndelivered 仅供尚未向客户端输出的失败调用，恢复来源后才允许换候选。
// 保存失败时保留已提交标记，调用方必须终止重试，避免来源与持久化记录分离。
func (t *Turn) AbortUndelivered() error {
	if !t.committed {
		return nil
	}
	s := t.service
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := s.cloneStateLocked()
	if t.previous.Target.ChannelID == "" {
		delete(candidate.Sessions, t.key)
	} else {
		candidate.Sessions[t.key] = t.previous
	}
	candidate.putEvent(t.event)
	if err := s.saveLocked(candidate); err != nil {
		return err
	}
	t.committed, t.accepted = false, Target{}
	return nil
}

func (state *persistedState) putEvent(event Event) {
	for i := range state.Events {
		if state.Events[i].RequestID == event.RequestID {
			state.Events[i] = event
			return
		}
	}
	state.Events = append(state.Events, event)
	if len(state.Events) > 200 {
		state.Events = state.Events[len(state.Events)-200:]
	}
}

func (s *Service) cloneStateLocked() persistedState {
	return persistedState{Sessions: maps.Clone(s.state.Sessions), Quality: maps.Clone(s.state.Quality), Events: slices.Clone(s.state.Events)}
}

func (s *Service) saveLocked(candidate persistedState) error {
	if err := s.store.save(s.state, candidate); err != nil {
		return &RoutingError{Code: "INTENT_STATE_FAILED", Err: fmt.Errorf("保存意图会话失败: %w", err)}
	}
	s.state = candidate
	return nil
}

func (s *Service) Events(kind string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Event{}
	for i := len(s.state.Events) - 1; i >= 0; i-- {
		if s.state.Events[i].Protocol == kind {
			result = append(result, s.state.Events[i])
		}
	}
	return result
}

func (s *Service) Feedback(kind, requestID string, good bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := s.cloneStateLocked()
	for i := range candidate.Events {
		event := &candidate.Events[i]
		if event.RequestID != requestID || event.Protocol != kind {
			continue
		}
		if !event.Success {
			return fmt.Errorf("只有已成功交付的请求可以记录任务质量反馈")
		}
		if event.Control {
			return fmt.Errorf("上下文压缩不是任务回答，不记录任务质量反馈")
		}
		if event.Feedback != nil {
			return fmt.Errorf("该请求已记录反馈")
		}
		event.Feedback = &good
		if event.Decision.Tier < 0 {
			// 保留人工评价，但未分类的新任务不能计入某种任务或难度的质量统计。
			return s.saveLocked(candidate)
		}
		key := qualityPrefix(event.Protocol, event.Decision.Task, event.Decision.Tier, event.Target.PoolID) + event.Target.ChannelID + "\x00" + event.Target.Model
		q := candidate.Quality[key]
		if good {
			q.Good++
		} else {
			q.Bad++
		}
		candidate.Quality[key] = q
		return s.saveLocked(candidate)
	}
	return fmt.Errorf("请求记录不存在或已过期")
}

// qualityPrefix 隔离各任务的质量反馈；无任务标签时保留旧难度策略的存储键。
func qualityPrefix(kind, task string, tier int, pool string) string {
	if task != "" {
		kind += "\x00task\x00" + task
	}
	return kind + "\x00" + fmt.Sprint(tier) + "\x00" + pool + "\x00"
}

func (s *Service) adaptiveChoice(kind, task string, routes []config.IntentRoute) config.IntentRoute {
	s.mu.Lock()
	defer s.mu.Unlock()
	chosen, best := routes[0], math.Inf(-1)
	maxCost := 1.0
	for _, route := range routes {
		maxCost = math.Max(maxCost, route.Cost)
	}
	for _, route := range routes {
		q := quality{}
		prefix := qualityPrefix(kind, task, route.Tier, route.PoolID)
		for key, sample := range s.state.Quality {
			if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
				q.Good += sample.Good
				q.Bad += sample.Bad
			}
		}
		// 平滑质量估计 + 有界探索奖励 − 归一化成本。已有会话仍受切换保护。
		score := (q.Good+1)/(q.Good+q.Bad+2) + 0.1/math.Sqrt(q.Good+q.Bad+1) - 0.1*route.Cost/maxCost
		if score > best {
			chosen, best = route, score
		}
	}
	return chosen
}
