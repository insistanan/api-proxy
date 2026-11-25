package session

import (
	"github.com/stretchr/testify/require"
	"testing"
	"testing/synctest"
	"time"
)

func TestResponseChainExpiresWithoutAnyReadAndPreservesReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := NewResponseChainManager(time.Minute)
		defer manager.ClearAll()
		manager.Set("one", ResponseChainState{ResponseID: "old"})
		time.Sleep(30 * time.Second)
		manager.Set("one", ResponseChainState{ResponseID: "new"})
		time.Sleep(31 * time.Second)
		synctest.Wait()
		got, ok := manager.Get("one")
		require.True(t, ok)
		require.Equal(t, "new", got.ResponseID)
		time.Sleep(30 * time.Second)
		synctest.Wait()
		manager.mu.RLock()
		defer manager.mu.RUnlock()
		require.Empty(t, manager.items)
		require.Empty(t, manager.timers)
	})
}
