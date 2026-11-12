package providers

import (
	"encoding/json"
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
	mu                 sync.Mutex
	maxSessions        int
	maxTurnsPerSession int
	order              []geminiShadowKey
	sessions           map[geminiShadowKey][]GeminiShadowTurn
}

var defaultGeminiShadowStore = newGeminiShadowStore(200, 64)

func newGeminiShadowStore(maxSessions int, maxTurnsPerSession int) *geminiShadowStore {
	if maxSessions < 1 {
		maxSessions = 1
	}
	if maxTurnsPerSession < 1 {
		maxTurnsPerSession = 1
	}
	return &geminiShadowStore{
		maxSessions:        maxSessions,
		maxTurnsPerSession: maxTurnsPerSession,
		sessions:           make(map[geminiShadowKey][]GeminiShadowTurn),
	}
}

func (s *geminiShadowStore) Get(providerID string, sessionID string) GeminiShadowSnapshot {
	if s == nil || providerID == "" || sessionID == "" {
		return GeminiShadowSnapshot{}
	}
	key := geminiShadowKey{ProviderID: providerID, SessionID: sessionID}
	s.mu.Lock()
	defer s.mu.Unlock()

	turns := cloneGeminiShadowTurns(s.sessions[key])
	if turns != nil {
		s.touchLocked(key)
	}
	return GeminiShadowSnapshot{
		ProviderID: providerID,
		SessionID:  sessionID,
		Turns:      turns,
	}
}

func (s *geminiShadowStore) Record(providerID string, sessionID string, turn GeminiShadowTurn) GeminiShadowSnapshot {
	if s == nil || providerID == "" || sessionID == "" || len(turn.AssistantContent) == 0 {
		return GeminiShadowSnapshot{}
	}
	key := geminiShadowKey{ProviderID: providerID, SessionID: sessionID}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.touchLocked(key)
	turns := append(s.sessions[key], cloneGeminiShadowTurn(turn))
	for len(turns) > s.maxTurnsPerSession {
		turns = turns[1:]
	}
	s.sessions[key] = turns
	s.pruneLocked()

	return GeminiShadowSnapshot{
		ProviderID: providerID,
		SessionID:  sessionID,
		Turns:      cloneGeminiShadowTurns(turns),
	}
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
	for len(s.order) > s.maxSessions {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.sessions, oldest)
	}
}

func cloneGeminiShadowTurns(turns []GeminiShadowTurn) []GeminiShadowTurn {
	if len(turns) == 0 {
		return nil
	}
	out := make([]GeminiShadowTurn, len(turns))
	for i := range turns {
		out[i] = cloneGeminiShadowTurn(turns[i])
	}
	return out
}

func cloneGeminiShadowTurn(turn GeminiShadowTurn) GeminiShadowTurn {
	toolCalls := make([]GeminiShadowToolCall, len(turn.ToolCalls))
	for i := range turn.ToolCalls {
		toolCalls[i] = turn.ToolCalls[i]
		toolCalls[i].Args = cloneInterface(turn.ToolCalls[i].Args)
	}
	return GeminiShadowTurn{
		AssistantContent: cloneMapStringInterface(turn.AssistantContent),
		ToolCalls:        toolCalls,
	}
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
