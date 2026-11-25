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

func (r *Registry) SetRouteOverride(ctx context.Context, recordID string, kind string, channelIndex int, channelName string) (*Record, error) {
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
	if err := r.prepareObservationLocked(context.Background(), Observation{APIKind: apiKind, ConversationID: externalID}); err != nil {
		return err
	}
	rec, err := r.loadRecordLocked(context.Background(), recordID)
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
