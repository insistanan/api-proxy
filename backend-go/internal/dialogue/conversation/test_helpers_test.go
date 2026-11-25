package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"testing"
)

func mustObserve(t *testing.T, r *Registry, observation Observation) *Record {
	t.Helper()
	record, err := r.ObserveRequestWithError(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
func mustResolve(t *testing.T, r *Registry, kind string, identity Identity) string {
	t.Helper()
	id, err := r.ResolveExistingRecordIDWithError(context.Background(), kind, identity)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustList(t *testing.T, r *Registry) []*Record {
	t.Helper()
	records, err := r.ListRecords()
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func (r *Registry) ListRecords() ([]*Record, error) {
	r.mu.RLock()
	result := make([]*Record, 0, len(r.records))
	if r.store != nil {
		stored, err := r.store.loadRecords(context.Background(), "")
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
	rec, err := r.Lookup(context.Background(), recordID)
	if err != nil {
		log.Printf("[Conversation] 读取对话失败: %v", err)
	}
	return rec, rec != nil
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
		if _, err := r.store.db.Exec(`INSERT INTO conversation_aliases (alias_key, conversation_id) VALUES (?, ?)`, alias, recordID); err != nil {
			return err
		}
	}
	r.identityIndex[alias] = recordID
	return nil
}

// 测试独立 Registry 时仅提供事务，不复制生产删除 SQL，也不伪造历史数据。
type recordOnlyHistory struct{}

func (recordOnlyHistory) DeleteWithRecords(ctx context.Context, _ string, db *sql.DB, remove func(*sql.Tx) error) error {
	if db == nil {
		return remove(nil)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := remove(tx); err != nil {
		return err
	}
	return tx.Commit()
}
