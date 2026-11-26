package conversation

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (r *Registry) Lookup(ctx context.Context, recordID string) (*Record, error) {
	if err := r.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(ctx, recordID)
	if err != nil {
		return nil, err
	}
	return cloneRecord(rec), nil
}

// ResolveExistingRecordIDWithError 根据明确身份查找已有会话记录，但不会创建新记录或更新请求状态。
// 控制面请求（例如 Responses compact）只应复用主链路已经建立的会话，不能因为一次
// compact 请求凭空创建会话、污染请求计数，或把仅有 ScopeID 的并行客户端窗口强行合并。

func (r *Registry) ResolveExistingRecordIDWithError(ctx context.Context, apiKind string, identity Identity) (string, error) {
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

	if err := r.mu.LockContext(ctx); err != nil {
		return "", err
	}
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	if err := r.prepareObservationLocked(ctx, Observation{APIKind: apiKind, Identity: identity}); err != nil {
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

func (r *Registry) SetRouteOverride(ctx context.Context, recordID string, kind string, channelIndex int, channelName string, channelID string) (*Record, error) {
	if strings.TrimSpace(channelID) == "" {
		return nil, fmt.Errorf("固定渠道缺少稳定 ID，请重新选择渠道")
	}
	now := time.Now()

	if err := r.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer r.mu.Unlock()
	defer r.trimCacheLocked()

	rec, err := r.loadRecordLocked(ctx, recordID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("conversation not found")
	}

	rec = cloneRecord(rec)
	rec.RouteOverride = &RouteOverride{
		ChannelID:    channelID,
		Kind:         strings.TrimSpace(kind),
		ChannelIndex: channelIndex,
		ChannelName:  strings.TrimSpace(channelName),
		UpdatedAt:    now,
	}
	rec.LastSeenAt = now

	if err := r.persistLocked(ctx, rec); err != nil {
		return nil, err
	}
	r.records[rec.ID] = rec
	return cloneRecord(rec), nil
}

func (r *Registry) ClearRouteOverride(ctx context.Context, recordID string) (*Record, error) {
	now := time.Now()

	if err := r.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer r.mu.Unlock()
	defer r.trimCacheLocked()

	rec, err := r.loadRecordLocked(ctx, recordID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("conversation not found")
	}
	rec = cloneRecord(rec)
	rec.RouteOverride = nil
	rec.LastSeenAt = now
	if err := r.persistLocked(ctx, rec); err != nil {
		return nil, err
	}
	r.records[rec.ID] = rec
	return cloneRecord(rec), nil
}

func (r *Registry) SetName(ctx context.Context, recordID string, name string) (*Record, error) {
	name = truncate(name, 120)
	if name == "" {
		return nil, fmt.Errorf("conversation name is required")
	}
	if err := r.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(ctx, recordID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("conversation not found")
	}
	rec = cloneRecord(rec)
	rec.Name = name
	rec.LastSeenAt = time.Now()
	if err := r.persistLocked(ctx, rec); err != nil {
		return nil, err
	}
	r.records[rec.ID] = rec
	return cloneRecord(rec), nil
}

func (r *Registry) LookupRouteOverride(ctx context.Context, recordID string) (*RouteOverride, error) {
	if err := r.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(ctx, recordID)
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.RouteOverride == nil {
		return nil, nil
	}
	result := *rec.RouteOverride
	return &result, nil
}
