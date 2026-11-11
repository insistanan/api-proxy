package conversation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
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
	mu               sync.RWMutex
	records          map[string]*Record
	identityIndex    map[string]string
	frontierIndex    map[string]map[string]struct{}
	maxIdle          time.Duration
	stopCh           chan struct{}
	doneCh           chan struct{}
	stopOnce         sync.Once
	store            *SQLiteStore
	maxCachedRecords int
	maxCacheBytes    int
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
	}
	go r.cleanupLoop()
	return r
}

func (r *Registry) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		<-r.doneCh
		if r.store != nil {
			if err := r.store.Close(); err != nil {
				log.Printf("[Conversation] 关闭持久化存储失败: %v", err)
			}
		}
	})
}

// ObserveRequest 保留内部调用入口；代理请求使用带错误返回的 ObserveRequestWithError。
func (r *Registry) ObserveRequest(obs Observation) *Record {
	rec, err := r.ObserveRequestWithError(obs)
	if err != nil {
		log.Printf("[Conversation] 观测对话失败: %v", err)
	}
	return rec
}

func (r *Registry) ObserveRequestWithError(obs Observation) (*Record, error) {
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	if err := r.prepareObservationLocked(obs); err != nil {
		return nil, err
	}

	rec, identityKey, parentID, resolution := r.resolveObservationLocked(obs)
	if rec == nil {
		if identityKey == "" {
			identityKey = buildAnonymousIdentityKey(obs.APIKind)
		}
		rec = &Record{
			ID:            generateID("conv"),
			APIKind:       firstNonEmpty(obs.APIKind, "unknown"),
			LastModel:     obs.Model,
			FirstPrompt:   firstPromptFromObservation(obs),
			Stream:        obs.Stream,
			IsSending:     true,
			FirstSeenAt:   now,
			LastSeenAt:    now,
			LastRequestAt: now,
			identityKey:   identityKey,
		}
		rec.ParentConversationID = parentID
		appendPrompts(rec, promptsFromObservation(obs)...)
		r.records[rec.ID] = rec
		r.identityIndex[identityKey] = rec.ID
	}
	r.unindexLineageLocked(rec)
	r.applyObservationLineageLocked(rec, obs, resolution, parentID, now)

	rec.APIKind = firstNonEmpty(obs.APIKind, rec.APIKind)
	rec.LastModel = firstNonEmpty(obs.Model, rec.LastModel)
	if rec.FirstPrompt == "" {
		rec.FirstPrompt = firstPromptFromObservation(obs)
	}
	appendPrompts(rec, promptsFromObservation(obs)...)
	appendImageFingerprints(rec, obs.ImageFingerprints...)
	rec.Stream = obs.Stream
	if rec.ActiveRequests < 0 {
		rec.ActiveRequests = 0
	}
	rec.ActiveRequests++
	rec.IsSending = rec.ActiveRequests > 0
	rec.LastSeenAt = now
	rec.LastRequestAt = now
	rec.RequestCount++

	if err := r.persistLocked(rec); err != nil {
		rec.ActiveRequests--
		rec.IsSending = rec.ActiveRequests > 0
		return nil, err
	}
	return cloneRecord(rec), nil
}

func (r *Registry) MarkAttempt(recordID string, kind string, channelIndex int, channelName string, requestedModel string, resolvedModel string, stream bool) {
	if strings.TrimSpace(recordID) == "" {
		return
	}
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.records[recordID]
	if rec == nil {
		return
	}
	rec.APIKind = firstNonEmpty(kind, rec.APIKind)
	rec.LastModel = firstNonEmpty(requestedModel, rec.LastModel)
	rec.LastResolvedModel = firstNonEmpty(resolvedModel, rec.LastResolvedModel)
	rec.Stream = stream
	rec.IsSending = rec.ActiveRequests > 0
	rec.LastSeenAt = now
	rec.LastResolved = &ChannelRef{
		Kind:         kind,
		ChannelIndex: channelIndex,
		ChannelName:  channelName,
		UpdatedAt:    now,
	}
	r.persistLocked(rec)
}

func (r *Registry) MarkSuccess(recordID string, kind string, channelIndex int, channelName string) {
	if strings.TrimSpace(recordID) == "" {
		return
	}
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.records[recordID]
	if rec == nil {
		return
	}
	rec.APIKind = firstNonEmpty(kind, rec.APIKind)
	rec.LastSeenAt = now
	rec.LastCompletedAt = now
	rec.LastError = ""
	rec.LastResolved = &ChannelRef{
		Kind:         kind,
		ChannelIndex: channelIndex,
		ChannelName:  channelName,
		UpdatedAt:    now,
	}
	r.persistLocked(rec)
}

func (r *Registry) MarkFailure(recordID string, kind string, errorMessage string) {
	if strings.TrimSpace(recordID) == "" {
		return
	}
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.records[recordID]
	if rec == nil {
		return
	}
	rec.APIKind = firstNonEmpty(kind, rec.APIKind)
	rec.LastSeenAt = now
	rec.LastCompletedAt = now
	rec.ErrorCount++
	rec.LastError = truncate(errorMessage, 500)
	r.persistLocked(rec)
}

func (r *Registry) MarkComplete(recordID string, kind string) {
	if strings.TrimSpace(recordID) == "" {
		return
	}
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()

	rec := r.records[recordID]
	if rec == nil {
		return
	}
	rec.APIKind = firstNonEmpty(kind, rec.APIKind)
	if rec.ActiveRequests > 0 {
		rec.ActiveRequests--
	}
	rec.IsSending = rec.ActiveRequests > 0
	rec.LastSeenAt = now
	rec.LastCompletedAt = now
	r.persistLocked(rec)
}

func (r *Registry) List() []*Record {
	records, err := r.ListRecords()
	if err != nil {
		log.Printf("[Conversation] 查询对话列表失败: %v", err)
	}
	return records
}

func (r *Registry) ListRecords() ([]*Record, error) {
	r.mu.RLock()
	result := make([]*Record, 0, len(r.records))
	if r.store != nil {
		stored, err := r.store.loadRecords("")
		if err != nil {
			r.mu.RUnlock()
			return nil, err
		}
		for _, rec := range stored {
			if r.records[rec.ID] == nil {
				result = append(result, rec)
			}
		}
	}
	for _, rec := range r.records {
		result = append(result, cloneRecord(rec))
	}
	r.mu.RUnlock()
	sortRecords(result)
	return result, nil
}

func (r *Registry) Get(recordID string) (*Record, bool) {
	rec, err := r.Lookup(recordID)
	if err != nil {
		log.Printf("[Conversation] 读取对话失败: %v", err)
	}
	return rec, rec != nil
}

func (r *Registry) Lookup(recordID string) (*Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(recordID)
	if err != nil {
		return nil, err
	}
	return cloneRecord(rec), nil
}

// ResolveExistingRecordID 根据明确身份查找已有会话记录，但不会创建新记录或更新请求状态。
// 控制面请求（例如 Responses compact）只应复用主链路已经建立的会话，不能因为一次
// compact 请求凭空创建会话、污染请求计数，或把仅有 ScopeID 的并行客户端窗口强行合并。
func (r *Registry) ResolveExistingRecordID(apiKind string, identity Identity) string {
	id, err := r.ResolveExistingRecordIDWithError(apiKind, identity)
	if err != nil {
		log.Printf("[Conversation] 解析已有对话失败: %v", err)
	}
	return id
}

func (r *Registry) ResolveExistingRecordIDWithError(apiKind string, identity Identity) (string, error) {
	if r == nil {
		return "", nil
	}
	key := buildExplicitIdentityKey(Observation{
		APIKind:  apiKind,
		Identity: identity,
	})
	if key == "" {
		return "", nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	if err := r.prepareObservationLocked(Observation{APIKind: apiKind, Identity: identity}); err != nil {
		return "", err
	}

	if recordID, ok := r.identityIndex[key]; ok && r.records[recordID] != nil {
		return recordID, nil
	}
	if isPreviousResponseIdentity(identity) {
		baseKey := explicitIdentityKey(apiKind, identity.ExplicitID)
		if recordID, ok := r.identityIndex[baseKey]; ok && r.records[recordID] != nil {
			return recordID, nil
		}
	}
	return "", nil
}

func (r *Registry) SetRouteOverride(recordID string, kind string, channelIndex int, channelName string) (*Record, error) {
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()

	rec, err := r.loadRecordLocked(recordID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("conversation not found")
	}

	rec.RouteOverride = &RouteOverride{
		Kind:         strings.TrimSpace(kind),
		ChannelIndex: channelIndex,
		ChannelName:  strings.TrimSpace(channelName),
		UpdatedAt:    now,
	}
	rec.LastSeenAt = now

	if err := r.persistLocked(rec); err != nil {
		return nil, err
	}
	return cloneRecord(rec), nil
}

func (r *Registry) ClearRouteOverride(recordID string) (*Record, error) {
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()

	rec, err := r.loadRecordLocked(recordID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("conversation not found")
	}
	rec.RouteOverride = nil
	rec.LastSeenAt = now
	if err := r.persistLocked(rec); err != nil {
		return nil, err
	}
	return cloneRecord(rec), nil
}

func (r *Registry) SetName(recordID string, name string) (*Record, error) {
	name = truncate(name, 120)
	if name == "" {
		return nil, fmt.Errorf("conversation name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(recordID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("conversation not found")
	}
	rec.Name = name
	rec.LastSeenAt = time.Now()
	if err := r.persistLocked(rec); err != nil {
		return nil, err
	}
	return cloneRecord(rec), nil
}

func (r *Registry) Delete(recordID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(recordID)
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
	return nil
}

// AssociateExternalID 将客户端后续会携带的外部响应 ID 关联到已有对话。
// 目前主要用于 Responses API 的 previous_response_id 链。
func (r *Registry) AssociateExternalID(recordID string, apiKind string, externalID string) error {
	if strings.TrimSpace(recordID) == "" || strings.TrimSpace(externalID) == "" {
		return nil
	}
	alias := explicitIdentityKey(apiKind, externalID)

	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	if err := r.prepareObservationLocked(Observation{APIKind: apiKind, ConversationID: externalID}); err != nil {
		return err
	}
	rec, err := r.loadRecordLocked(recordID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("conversation not found")
	}
	if existingID, ok := r.identityIndex[alias]; ok && existingID != recordID {
		return fmt.Errorf("external conversation ID is already associated with another conversation")
	}
	if r.store != nil {
		if err := r.store.UpsertAlias(alias, recordID); err != nil {
			return err
		}
	}
	r.identityIndex[alias] = recordID
	return nil
}

func (r *Registry) GetRouteOverride(recordID string) (*RouteOverride, bool) {
	result, err := r.LookupRouteOverride(recordID)
	if err != nil {
		log.Printf("[Conversation] 读取固定渠道失败: %v", err)
	}
	return result, result != nil
}

func (r *Registry) LookupRouteOverride(recordID string) (*RouteOverride, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(recordID)
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.RouteOverride == nil {
		return nil, nil
	}
	result := *rec.RouteOverride
	return &result, nil
}

// GetLastResolved 返回最近一次命中的渠道，冷记录按需从持久层恢复。
func (r *Registry) GetLastResolved(recordID string) (*ChannelRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(recordID)
	if err != nil {
		log.Printf("[Conversation] 读取亲和渠道失败: %v", err)
		return nil, false
	}
	if rec == nil || rec.LastResolved == nil {
		return nil, false
	}
	result := *rec.LastResolved
	return &result, true
}

// LoadConversationImageUnderstanding 读取当前对话内的图片理解结果。
func (r *Registry) LoadConversationImageUnderstanding(conversationID string, cacheKey string) (string, bool, error) {
	if r == nil {
		return "", false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(conversationID)
	if err != nil {
		return "", false, err
	}
	if rec == nil {
		return "", false, fmt.Errorf("conversation not found")
	}
	if r.store == nil {
		return "", false, nil
	}
	return r.store.LoadConversationImageUnderstanding(conversationID, cacheKey)
}

func (r *Registry) SaveConversationImageUnderstanding(conversationID string, cacheKey string, result string) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(conversationID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("conversation not found")
	}
	if r.store == nil {
		return nil
	}
	return r.store.UpsertConversationImageUnderstanding(conversationID, cacheKey, result)
}

func (r *Registry) cleanupLoop() {
	defer close(r.doneCh)
	ticker := time.NewTicker(defaultCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.cleanup()
		case <-r.stopCh:
			return
		}
	}
}

func (r *Registry) cleanup() {
	cutoff := time.Now().Add(-r.maxIdle)
	r.mu.Lock()
	defer r.mu.Unlock()
	active := make([]string, 0)
	expired := make(map[string]bool)
	for id, rec := range r.records {
		if rec.ActiveRequests > 0 || rec.dirty {
			active = append(active, id)
		} else if rec.LastSeenAt.Before(cutoff) {
			expired[id] = true
		}
	}
	if r.store != nil {
		if err := r.store.purgeExpired(cutoff, active); err != nil {
			log.Printf("[Conversation] 清理过期对话失败: %v", err)
			return
		}
		// 父记录可能早已离开缓存，不能仅依赖当前内存中的 expired 集合。
		for _, rec := range r.records {
			if rec.ParentConversationID == "" {
				continue
			}
			var exists bool
			if err := r.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM expired_conversation_ids WHERE id = ?)`, rec.ParentConversationID).Scan(&exists); err != nil {
				log.Printf("[Conversation] 同步过期父对话失败: %v", err)
				return
			}
			if exists {
				rec.ParentConversationID = ""
				rec.lineage.ParentConversationID = ""
			}
		}
	}
	for id := range expired {
		r.unindexLineageLocked(r.records[id])
		delete(r.records, id)
	}
	for key, id := range r.identityIndex {
		if expired[id] {
			delete(r.identityIndex, key)
		}
	}
	for _, rec := range r.records {
		if expired[rec.ParentConversationID] {
			rec.ParentConversationID = ""
			rec.lineage.ParentConversationID = ""
		}
	}
	r.trimCacheLocked()
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

func buildIdentityKey(obs Observation) string {
	if key := buildExplicitIdentityKey(obs); key != "" {
		return key
	}
	return buildAnonymousIdentityKey(obs.APIKind)
}

func buildExplicitIdentityKey(obs Observation) string {
	value := strings.TrimSpace(obs.Identity.ExplicitID)
	if value == "" {
		value = strings.TrimSpace(obs.ConversationID)
	}
	agentID := strings.TrimSpace(obs.Identity.AgentID)
	if value == "" && agentID != "" {
		value = "agent:" + agentID
	} else if value != "" && agentID != "" {
		value += "|agent:" + agentID
	}
	if value == "" {
		return ""
	}
	return explicitIdentityKey(obs.APIKind, value)
}

func isPreviousResponseIdentity(identity Identity) bool {
	return strings.EqualFold(strings.TrimSpace(identity.Source), "previous_response")
}

func buildAnonymousIdentityKey(apiKind string) string {
	kind := firstNonEmpty(strings.TrimSpace(apiKind), "unknown")
	return strings.ToLower(strings.Join([]string{kind, generateID("anon")}, "|"))
}

func explicitIdentityKey(apiKind string, value string) string {
	kind := firstNonEmpty(strings.TrimSpace(apiKind), "unknown")
	return strings.ToLower(strings.Join([]string{kind, strings.TrimSpace(value)}, "|"))
}

func cloneRecord(src *Record) *Record {
	if src == nil {
		return nil
	}
	dst := *src
	if src.RouteOverride != nil {
		override := *src.RouteOverride
		dst.RouteOverride = &override
	}
	if src.LastResolved != nil {
		resolved := *src.LastResolved
		dst.LastResolved = &resolved
	}
	if src.Prompts != nil {
		dst.Prompts = make([]string, len(src.Prompts))
		copy(dst.Prompts, src.Prompts)
	}
	if src.ImageFingerprints != nil {
		dst.ImageFingerprints = append([]string{}, src.ImageFingerprints...)
	}
	return &dst
}

func sortRecords(records []*Record) {
	// 按 ID 固定顺序，避免 LastSeenAt/状态变化导致列表跳动
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPromptFromObservation(obs Observation) string {
	prompts := promptsFromObservation(obs)
	if len(prompts) > 0 {
		return prompts[0]
	}
	return ""
}

func promptsFromObservation(obs Observation) []string {
	values := make([]string, 0, len(obs.Prompts)+1)
	if obs.FirstPrompt != "" {
		values = append(values, obs.FirstPrompt)
	}
	values = append(values, obs.Prompts...)
	return cleanPromptList(values, 3)
}

func appendPrompts(rec *Record, prompts ...string) {
	if rec == nil {
		return
	}
	candidates := cleanPromptList(prompts, 3)
	// 旧版本按 300 字节截断；再次看到同一条完整历史输入时原位修复。
	for _, prompt := range candidates {
		for index, current := range rec.Prompts {
			if !strings.HasSuffix(current, "...") {
				continue
			}
			prefix := strings.ToValidUTF8(strings.TrimSuffix(current, "..."), "")
			if len(prefix) < 296 || !strings.HasPrefix(prompt, prefix) {
				continue
			}
			rec.Prompts[index] = prompt
			if index == 0 {
				rec.FirstPrompt = prompt
			}
		}
	}
	if len(rec.Prompts) >= 3 {
		return
	}
	for _, prompt := range candidates {
		if len(rec.Prompts) >= 3 {
			return
		}
		exists := false
		for _, current := range rec.Prompts {
			if current == prompt {
				exists = true
				break
			}
		}
		if !exists {
			rec.Prompts = append(rec.Prompts, prompt)
		}
	}
}

func appendImageFingerprints(rec *Record, fingerprints ...string) {
	if rec == nil {
		return
	}
	seen := make(map[string]bool, len(rec.ImageFingerprints))
	for _, current := range rec.ImageFingerprints {
		seen[current] = true
	}
	for _, fingerprint := range fingerprints {
		fingerprint = strings.TrimSpace(fingerprint)
		if fingerprint == "" || seen[fingerprint] {
			continue
		}
		rec.ImageFingerprints = append(rec.ImageFingerprints, fingerprint)
		seen[fingerprint] = true
	}
}

func (r *Registry) persistLocked(rec *Record) error {
	if r.store == nil || rec == nil {
		return nil
	}
	if err := r.store.Upsert(rec); err != nil {
		rec.dirty = true
		log.Printf("[Conversation] 保存对话失败: %v", err)
		return err
	}
	rec.dirty = false
	return nil
}

func cleanPromptList(values []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	result := make([]string, 0, limit)
	seen := make(map[string]bool)
	for _, value := range values {
		prompt := NormalizeUserPrompt(value)
		if prompt == "" || seen[prompt] {
			continue
		}
		seen[prompt] = true
		result = append(result, prompt)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func normalizeRecordPrompts(rec *Record) {
	if rec == nil {
		return
	}
	values := make([]string, 0, len(rec.Prompts)+1)
	values = append(values, rec.FirstPrompt)
	values = append(values, rec.Prompts...)
	rec.Prompts = cleanPromptList(values, 3)
	if len(rec.Prompts) > 0 {
		rec.FirstPrompt = rec.Prompts[0]
	} else {
		rec.FirstPrompt = ""
	}
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func generateID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buf))
}
