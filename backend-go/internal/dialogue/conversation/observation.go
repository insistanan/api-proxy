package conversation

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (r *Registry) ObserveRequestWithError(ctx context.Context, obs Observation) (*Record, error) {
	now := time.Now()

	if err := r.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	if r.stopped {
		return nil, fmt.Errorf("对话注册表已停止")
	}
	if err := r.prepareObservationLocked(ctx, obs); err != nil {
		return nil, err
	}
	// 已失败的观测写入有界保留；磁盘长期故障时停止接纳新观测，避免无限驻留。
	pendingCount := 0
	for _, record := range r.records {
		if record.dirty || record.ActiveRequests > 0 {
			pendingCount++
		}
	}
	if pendingCount >= defaultCachedRecords {
		return nil, fmt.Errorf("会话观测待保存或在途数量已达上限，请等待请求完成或数据库恢复")
	}

	rec, identityKey, parentID, resolution := r.resolveObservationLocked(obs)
	previous := cloneRecord(rec)
	if rec == nil {
		id, err := generateID("conv")
		if err != nil {
			return nil, err
		}
		if identityKey == "" {
			identityKey = "anonymous:" + id
		}
		rec = &Record{
			ID:            id,
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

	if err := r.persistLocked(ctx, rec); err != nil {
		r.unindexLineageLocked(rec)
		if previous == nil {
			delete(r.records, rec.ID)
			r.removeIdentityKeysLocked(rec.ID)
		} else {
			r.records[rec.ID] = previous
			r.indexLineageLocked(previous)
		}
		return nil, err
	}
	return cloneRecord(rec), nil
}

func (r *Registry) MarkAttempt(recordID string, kind string, channelIndex int, channelName string, requestedModel string, resolvedModel string, stream bool) {
	if strings.TrimSpace(recordID) == "" {
		return
	}
	now := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.records[recordID]
	if rec == nil || r.stopped {
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
	r.persistLocked(ctx, rec)
}

func (r *Registry) MarkSuccess(recordID string, kind string, channelIndex int, channelName string) {
	if strings.TrimSpace(recordID) == "" {
		return
	}
	now := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.records[recordID]
	if rec == nil || r.stopped {
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
	r.persistLocked(ctx, rec)
}

func (r *Registry) MarkFailure(recordID string, kind string, errorMessage string) {
	if strings.TrimSpace(recordID) == "" {
		return
	}
	now := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.records[recordID]
	if rec == nil || r.stopped {
		return
	}
	rec.APIKind = firstNonEmpty(kind, rec.APIKind)
	rec.LastSeenAt = now
	rec.LastCompletedAt = now
	rec.ErrorCount++
	rec.LastError = truncate(errorMessage, 500)
	r.persistLocked(ctx, rec)
}

func (r *Registry) MarkComplete(recordID string, kind string) {
	if strings.TrimSpace(recordID) == "" {
		return
	}
	now := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()

	rec := r.records[recordID]
	if rec == nil || r.stopped {
		return
	}
	rec.APIKind = firstNonEmpty(kind, rec.APIKind)
	if rec.ActiveRequests > 0 {
		rec.ActiveRequests--
	}
	rec.IsSending = rec.ActiveRequests > 0
	rec.LastSeenAt = now
	rec.LastCompletedAt = now
	r.persistLocked(ctx, rec)
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
