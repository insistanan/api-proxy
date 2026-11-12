package sqlitedb_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/safety/sensitive"
	"github.com/insistanan/api-proxy/internal/storage/sqlitedb"
	"github.com/stretchr/testify/require"
)

func TestConversationStoresBorrowOneConnection(t *testing.T) {
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "conversations.db"))
	require.NoError(t, err)
	defer db.Close()
	sm, err := session.NewPersistentSessionManagerWithDB(db, 7*24*time.Hour, 0, 0)
	require.NoError(t, err)
	defer sm.Stop()
	r, err := conversation.NewPersistentRegistryWithDB(db)
	require.NoError(t, err)
	defer r.Stop()
	rec, err := r.ObserveRequestWithError(conversation.Observation{APIKind: "responses", ConversationID: "shared"})
	require.NoError(t, err)
	_, err = sm.GetOrCreateSessionForConversation("", rec.ID)
	require.NoError(t, err)
	r.MarkComplete(rec.ID, "responses")
	r.Stop()
	_, err = sm.GetOrCreateSessionForConversation("", rec.ID)
	require.NoError(t, err, "停止 registry 不能关闭 session 仍在使用的连接")
	sm.Stop()
	require.NoError(t, db.Ping())
	require.Equal(t, 1, db.Stats().MaxOpenConnections)
	var cacheSize int
	require.NoError(t, db.QueryRow("PRAGMA cache_size").Scan(&cacheSize))
	require.Equal(t, -512, cacheSize)
}

func TestSafetyStoresShareConnectionWithoutNestedQueryDeadlock(t *testing.T) {
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "blocked.db"))
	require.NoError(t, err)
	defer db.Close()
	blocked, err := sensitive.NewBlockedStoreWithDB(db)
	require.NoError(t, err)
	defer blocked.Close()
	gate, err := sensitive.NewGateStoreWithDB(db)
	require.NoError(t, err)
	defer gate.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if _, err := blocked.ListGrouped(ctx, sensitive.BlockedLogListOptions{}); err != nil {
				errors <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if err := gate.Purge(ctx); err != nil {
				errors <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.NoError(t, blocked.Close())
	_, err = gate.ListWaiting(ctx)
	require.NoError(t, err, "关闭拦截存储不能关闭闸门借用的连接")
	require.NoError(t, gate.Close())
	require.NoError(t, db.PingContext(ctx))
}
