package logger

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPurgeReportsCommittedDeletionWhenCheckpointIsBusy(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "old", UnixMilli: time.Now().UnixMilli(), Phase: PhaseClientRequest, Body: "old"}))
	require.NoError(t, store.Flush())
	tx, err := store.readDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	var count int
	require.NoError(t, tx.QueryRow(`SELECT COUNT(*) FROM traffic_logs`).Scan(&count))
	_, err = store.db.Exec(`PRAGMA busy_timeout=1`)
	require.NoError(t, err)
	result, err := store.Purge(ctx, PurgeOptions{All: true})
	require.Error(t, err)
	require.True(t, result.Committed)
	require.EqualValues(t, 1, result.DeletedTrafficLogs)
	require.NotEmpty(t, result.CompactionError)
	require.NoError(t, tx.Rollback())
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "new", UnixMilli: time.Now().UnixMilli(), Phase: PhaseClientRequest, Body: "new"}))
	require.NoError(t, store.Flush())
	require.NoError(t, store.Compact(ctx))
	rows, err := store.QueryTrafficByRequestID(ctx, "new")
	require.NoError(t, err)
	require.Len(t, rows, 1, "仅重试压缩不能删除新增日志")
}
