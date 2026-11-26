package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

const geminiSynthesizedIDPrefix = "gemini_synth_"

type GeminiShadowToolCall struct {
	ID               string
	Name             string
	Args             interface{}
	ThoughtSignature string
}

type GeminiShadowTurn struct {
	AssistantContent map[string]interface{}
	ToolCalls        []GeminiShadowToolCall
}

type GeminiShadowSnapshot struct {
	ProviderID string
	SessionID  string
	Turns      []GeminiShadowTurn
}

type geminiShadowKey struct {
	ProviderID string
	SessionID  string
}

type geminiShadowStore struct {
	mu          sync.Mutex
	maxSessions int
	maxBytes    int
	usedBytes   int
	order       []geminiShadowKey
	sessions    map[geminiShadowKey][]json.RawMessage
	persistence GeminiShadowPersistence
}

// GeminiShadowPersistence 由会话存储实现；删除和 TTL 通过对话外键统一清理。
type GeminiShadowPersistence interface {
	LoadGeminiShadows(context.Context, string, string) ([]json.RawMessage, error)
	AppendGeminiShadow(context.Context, string, string, json.RawMessage) error
}

var defaultGeminiShadowStore = newGeminiShadowStore(200, 32<<20)

func BindGeminiShadowPersistence(persistence GeminiShadowPersistence) {
	s := defaultGeminiShadowStore
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persistence = persistence
	s.sessions = make(map[geminiShadowKey][]json.RawMessage)
	s.order = nil
	s.usedBytes = 0
}

func newGeminiShadowStore(maxSessions, maxBytes int) *geminiShadowStore {
	return &geminiShadowStore{
		maxSessions: maxSessions, maxBytes: maxBytes,
		sessions: make(map[geminiShadowKey][]json.RawMessage),
	}
}

func (s *geminiShadowStore) Get(ctx context.Context, providerID string, sessionID string) (GeminiShadowSnapshot, error) {
	if s == nil || providerID == "" || sessionID == "" {
		return GeminiShadowSnapshot{}, nil
	}
	key := geminiShadowKey{ProviderID: providerID, SessionID: sessionID}
	s.mu.Lock()
	defer s.mu.Unlock()

	encoded, found := s.sessions[key]
	if !found && s.persistence != nil {
		var err error
		encoded, err = s.persistence.LoadGeminiShadows(ctx, providerID, sessionID)
		if err != nil {
			return GeminiShadowSnapshot{}, fmt.Errorf("恢复 Gemini 签名失败: %w", err)
		}
	}
	turns := make([]GeminiShadowTurn, len(encoded))
	for i, data := range encoded {
		if err := json.Unmarshal(data, &turns[i]); err != nil {
			return GeminiShadowSnapshot{}, fmt.Errorf("解析 Gemini 签名失败: %w", err)
		}
	}
	if len(encoded) > 0 {
		if !found {
			s.sessions[key] = encoded
			s.usedBytes += shadowBytes(key, encoded)
		}
		s.touchLocked(key)
		s.pruneLocked()
	}
	return GeminiShadowSnapshot{
		ProviderID: providerID,
		SessionID:  sessionID,
		Turns:      turns,
	}, nil
}

func (s *geminiShadowStore) Record(ctx context.Context, providerID string, sessionID string, turn GeminiShadowTurn) error {
	if s == nil || providerID == "" || sessionID == "" || len(turn.AssistantContent) == 0 {
		return nil
	}
	encoded, err := json.Marshal(turn)
	if err != nil {
		return fmt.Errorf("编码 Gemini 签名失败: %w", err)
	}
	key := geminiShadowKey{ProviderID: providerID, SessionID: sessionID}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.persistence != nil {
		if err := s.persistence.AppendGeminiShadow(ctx, providerID, sessionID, encoded); err != nil {
			return fmt.Errorf("保存 Gemini 签名失败: %w", err)
		}
	}
	turns, loaded := s.sessions[key]
	// 尚未加载的历史留到 Get 恢复，不能只缓存新一轮并覆盖历史视图。
	if !loaded && s.persistence != nil {
		return nil
	}
	if !loaded {
		s.usedBytes += len(key.ProviderID) + len(key.SessionID) + 64
	}
	s.sessions[key] = append(turns, encoded)
	s.usedBytes += len(encoded) + 24
	s.touchLocked(key)
	s.pruneLocked()
	return nil
}

func (s *geminiShadowStore) touchLocked(key geminiShadowKey) {
	for i, existing := range s.order {
		if existing == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.order = append(s.order, key)
}

func (s *geminiShadowStore) pruneLocked() {
	for len(s.order) > 0 && (len(s.order) > s.maxSessions || s.usedBytes > s.maxBytes) {
		oldest := s.order[0]
		s.order = s.order[1:]
		s.usedBytes -= shadowBytes(oldest, s.sessions[oldest])
		delete(s.sessions, oldest)
	}
}

func shadowBytes(key geminiShadowKey, turns []json.RawMessage) int {
	size := len(key.ProviderID) + len(key.SessionID) + 64
	for _, turn := range turns {
		size += len(turn) + 24
	}
	return size
}

func cloneMapStringInterface(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	if cloned, ok := cloneInterface(in).(map[string]interface{}); ok {
		return cloned
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = cloneInterface(v)
	}
	return out
}

func cloneInterface(in interface{}) interface{} {
	switch v := in.(type) {
	case nil:
		return nil
	case json.Number:
		return v
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, value := range v {
			out[key] = cloneInterface(value)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = cloneInterface(v[i])
		}
		return out
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return v
		}
		var out interface{}
		if err := json.Unmarshal(data, &out); err != nil {
			return v
		}
		return out
	}
}
