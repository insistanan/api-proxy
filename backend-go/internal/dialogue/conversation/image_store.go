package conversation

import (
	"context"
	"fmt"
)

func (r *Registry) LoadConversationImageUnderstanding(ctx context.Context, conversationID string, cacheKey string) (string, bool, error) {
	if r == nil {
		return "", false, nil
	}
	if err := r.mu.LockContext(ctx); err != nil {
		return "", false, err
	}
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(ctx, conversationID)
	if err != nil {
		return "", false, err
	}
	if rec == nil {
		return "", false, fmt.Errorf("conversation not found")
	}
	if r.store == nil {
		return "", false, nil
	}
	return r.store.LoadConversationImageUnderstanding(ctx, conversationID, cacheKey)
}

func (r *Registry) SaveConversationImageUnderstanding(ctx context.Context, conversationID string, cacheKey string, result string) error {
	if r == nil {
		return nil
	}
	if err := r.mu.LockContext(ctx); err != nil {
		return err
	}
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(ctx, conversationID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("conversation not found")
	}
	if r.store == nil {
		return nil
	}
	return r.store.UpsertConversationImageUnderstanding(ctx, conversationID, cacheKey, result)
}
