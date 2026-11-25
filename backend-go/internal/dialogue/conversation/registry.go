package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/insistanan/api-proxy/internal/dialogue/internal/syncstate"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxIdle         = 7 * 24 * time.Hour
	defaultCleanupInterval = 10 * time.Minute
	retryMatchWindow       = 5 * time.Minute
)

type Observation struct {
	APIKind           string
	Model             string
	Stream            bool
	ConversationID    string
	Identity          Identity
	Transcript        Transcript
	FirstPrompt       string
	Prompts           []string
	ImageFingerprints []string
}

type RouteOverride struct {
	Kind         string    `json:"kind"`
	ChannelIndex int       `json:"channelIndex"`
	ChannelName  string    `json:"channelName,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type ChannelRef struct {
	Kind         string    `json:"kind"`
	ChannelIndex int       `json:"channelIndex"`
	ChannelName  string    `json:"channelName,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Record struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name,omitempty"`
	APIKind              string         `json:"apiKind"`
	LastModel            string         `json:"lastModel,omitempty"`
	LastResolvedModel    string         `json:"lastResolvedModel,omitempty"`
	FirstPrompt          string         `json:"firstPrompt,omitempty"`
	Prompts              []string       `json:"prompts,omitempty"`
	Stream               bool           `json:"stream"`
	IsSending            bool           `json:"isSending"`
	ActiveRequests       int64          `json:"activeRequests,omitempty"`
	FirstSeenAt          time.Time      `json:"firstSeenAt"`
	LastSeenAt           time.Time      `json:"lastSeenAt"`
	LastRequestAt        time.Time      `json:"lastRequestAt"`
	LastCompletedAt      time.Time      `json:"lastCompletedAt,omitempty"`
	RequestCount         int64          `json:"requestCount"`
	ErrorCount           int64          `json:"errorCount"`
	LastError            string         `json:"lastError,omitempty"`
	RouteOverride        *RouteOverride `json:"routeOverride,omitempty"`
	LastResolved         *ChannelRef    `json:"lastResolved,omitempty"`
	ImageFingerprints    []string       `json:"imageFingerprints,omitempty"`
	ClientFamily         string         `json:"clientFamily,omitempty"`
	IdentitySource       string         `json:"identitySource,omitempty"`
	ParentConversationID string         `json:"parentConversationId,omitempty"`
	PersistenceError     string         `json:"persistenceError,omitempty"`

	dirty       bool
	identityKey string
	lineage     lineageState
}

func (r *Record) RouteOverrideString() string {
	if r == nil || r.RouteOverride == nil {
		return ""
	}
	return strings.ToLower(strings.Join([]string{
		r.RouteOverride.Kind,
		fmt.Sprintf("%d", r.RouteOverride.ChannelIndex),
		r.RouteOverride.ChannelName,
	}, " "))
}

type Registry struct {
	mu               syncstate.Mutex
	records          map[string]*Record
	identityIndex    map[string]string
	frontierIndex    map[string]map[string]struct{}
	maxIdle          time.Duration
	stopCh           chan struct{}
	doneCh           chan struct{}
	stopOnce         sync.Once
	stopErr          error
	stopped          bool
	store            *SQLiteStore
	maxCachedRecords int
	maxCacheBytes    int
	invalidators     map[string]func(string)
	history          HistoryPruner
}

func NewRegistry() *Registry {
	return newRegistry(nil)
}

func NewPersistentRegistry(dbPath string) (*Registry, error) {
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		return nil, err
	}
	registry, err := newPersistentRegistry(store)
	if err != nil {
		return nil, errors.Join(err, store.Close())
	}
	return registry, nil
}

func newRegistry(store *SQLiteStore) *Registry {
	r := &Registry{
		records:          make(map[string]*Record),
		identityIndex:    make(map[string]string),
		frontierIndex:    make(map[string]map[string]struct{}),
		maxIdle:          defaultMaxIdle,
		stopCh:           make(chan struct{}),
		doneCh:           make(chan struct{}),
		store:            store,
		maxCachedRecords: defaultCachedRecords,
		maxCacheBytes:    defaultCacheBytes,
		invalidators:     make(map[string]func(string)),
	}
	go r.cleanupLoop()
	return r
}

func (r *Registry) Stop() error {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		<-r.doneCh
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.mu.Lock()
		r.stopped = true
		r.stopErr = r.retryDirtyLocked(ctx)
		r.invalidateCachesLocked("")
		r.mu.Unlock()
		if r.store != nil {
			r.stopErr = errors.Join(r.stopErr, r.store.Close())
		}
	})
	return r.stopErr
}

func (r *Registry) Delete(recordID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(context.Background(), recordID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("conversation not found")
	}
	if r.store != nil {
		if err := r.store.Delete(recordID); err != nil {
			return err
		}
	}
	r.clearParentReferencesLocked(recordID)
	delete(r.records, recordID)
	r.unindexLineageLocked(rec)
	r.removeIdentityKeysLocked(recordID)
	r.invalidateCachesLocked(recordID)
	return nil
}

// DeleteAll 删除全部持久化对话及其专属图片理解结果。
func (r *Registry) DeleteAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.store != nil {
		if err := r.store.DeleteAll(); err != nil {
			return err
		}
	}
	r.records = make(map[string]*Record)
	r.identityIndex = make(map[string]string)
	r.frontierIndex = make(map[string]map[string]struct{})
	r.invalidateCachesLocked("")
	return nil
}

func (r *Registry) clearParentReferencesLocked(parentID string) {
	for _, child := range r.records {
		if child != nil && child.lineage.ParentConversationID == parentID {
			child.lineage.ParentConversationID = ""
			child.ParentConversationID = ""
		}
	}
}

func (r *Registry) removeIdentityKeysLocked(recordID string) {
	for identityKey, currentRecordID := range r.identityIndex {
		if currentRecordID == recordID {
			delete(r.identityIndex, identityKey)
		}
	}
}

func buildExplicitIdentityKey(obs Observation) string {
	value := strings.TrimSpace(obs.Identity.ExplicitID)
	if value == "" {
		value = strings.TrimSpace(obs.ConversationID)
	}
	agentID := strings.TrimSpace(obs.Identity.AgentID)
	if value == "" && agentID == "" {
		return ""
	}
	return encodeExplicitIdentity(obs.APIKind, value, agentID)
}

func isPreviousResponseIdentity(identity Identity) bool {
	return strings.EqualFold(strings.TrimSpace(identity.Source), "previous_response")
}

func explicitIdentityKey(apiKind string, value string) string {
	return encodeExplicitIdentity(apiKind, strings.TrimSpace(value), "")
}

// 分量分别编码；只规范协议名，客户端不透明 ID 保留大小写及分隔符。
func encodeExplicitIdentity(apiKind, value, agentID string) string {
	key, _ := json.Marshal([3]string{strings.ToLower(firstNonEmpty(strings.TrimSpace(apiKind), "unknown")), value, agentID})
	return string(key)
}
