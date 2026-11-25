package conversation

import (
	"context"
	"log"
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
