package conversation

import (
	"context"
	"fmt"
)

func (r *Registry) LoadConversationImageUnderstanding(conversationID string, cacheKey string) (string, bool, error) {
	if r == nil {
		return "", false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.trimCacheLocked()
	rec, err := r.loadRecordLocked(context.Background(), conversationID)
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
	rec, err := r.loadRecordLocked(context.Background(), conversationID)
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
