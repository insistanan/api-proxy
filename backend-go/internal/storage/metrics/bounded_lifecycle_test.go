package metrics

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMetricsQueue_BudgetIncludesAcceptedRecordsAndReportsLoss(t *testing.T) {
	store := &SQLiteStore{batchSize: maxPendingRecords + 1, flushWake: make(chan struct{}, 1)}
	for i := 0; i < maxPendingRecords+3; i++ {
		store.AddRecord(PersistentRecord{MetricsKey: "key"})
	}
	status := store.QueueStatus()
	require.Equal(t, maxPendingRecords, status.PendingRecords)
	require.EqualValues(t, 3, status.DroppedRecords)
	store.AddRecord(PersistentRecord{Model: strings.Repeat("x", maxPendingBytes)})
	require.EqualValues(t, 4, store.QueueStatus().DroppedRecords)
	require.LessOrEqual(t, store.QueueStatus().PendingBytes, maxPendingBytes)
}

func TestMetricsDelete_FailedFlushPreservesRecordsUntilExplicitRetry(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	_, err = store.db.Exec(`CREATE TRIGGER refuse_write BEFORE INSERT ON request_records BEGIN SELECT RAISE(FAIL, 'disk failure'); END`)
	require.NoError(t, err)
	store.AddRecord(PersistentRecord{MetricsKey: "key", APIType: "chat", Timestamp: time.Now()})
	_, err = store.DeleteRecordsByMetricsKeys([]string{"key"}, "chat")
	require.ErrorContains(t, err, "未执行删除")
	require.Equal(t, 1, store.QueueStatus().PendingRecords)
	_, err = store.db.Exec(`DROP TRIGGER refuse_write`)
	require.NoError(t, err)
	deleted, err := store.DeleteRecordsByMetricsKeys([]string{"key"}, "chat")
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	require.NoError(t, store.Close())
	require.Zero(t, store.QueueStatus().PendingRecords)
}

func TestMetricsClose_ReturnsFinalPersistenceFailure(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db")})
	require.NoError(t, err)
	_, err = store.db.Exec(`CREATE TRIGGER refuse_write BEFORE INSERT ON request_records BEGIN SELECT RAISE(FAIL, 'disk failure'); END`)
	require.NoError(t, err)
	store.AddRecord(PersistentRecord{MetricsKey: "key", Timestamp: time.Now()})
	require.ErrorContains(t, store.Close(), "未能落盘")
	require.Equal(t, 1, store.QueueStatus().PendingRecords)
	require.NotEmpty(t, store.QueueStatus().LastError)
}

func TestProfileIdentity_ProtocolIsolationAndActiveLease(t *testing.T) {
	pm := NewProfileManager()
	pm.Stop()
	keys := []string{"secret"}
	identity := ProfileIdentity{Kind: "messages", ID: "stable", Index: 0}
	pm.StartRequest("https://example", keys, "model", identity, 1)
	original := pm.GetProfile("https://example", keys, "model", identity)
	identity.Index = 9
	require.Same(t, original, pm.GetOrCreateProfile("https://example", keys, "model", identity))
	identity.Kind = "chat"
	require.NotSame(t, original, pm.GetOrCreateProfile("https://example", keys, "model", identity))
	pm.SetIdentityValidator(func(ProfileIdentity, string, string) bool { return false })
	pm.pruneLocked(time.Now(), false)
	require.Len(t, pm.profiles, 1, "配置删除不可丢弃仍在途的租约")
	pm.EndRequest(1, true, 10)
	pm.pruneLocked(time.Now(), false)
	require.Empty(t, pm.profiles)
	require.NotContains(t, original.credentialDigest, "secret")
	require.NotEqual(t, generateProfileKey("a|b", []string{"c"}, "d", identity), generateProfileKey("a", []string{"b|c"}, "d", identity))
}
