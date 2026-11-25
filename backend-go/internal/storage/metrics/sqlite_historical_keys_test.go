package metrics

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHistoricalKeysStore_UninitializedStoreReportsErrors(t *testing.T) {
	for name, store := range map[string]*SQLiteStore{"nil": nil, "未打开": {}} {
		t.Run(name, func(t *testing.T) {
			_, err := store.LoadHistoricalKeys("messages")
			require.ErrorContains(t, err, "指标库未初始化")
			require.ErrorContains(t, store.SaveHistoricalKeys("messages", "channel", []string{"old"}), "指标库未初始化")
			require.ErrorContains(t, store.DeleteHistoricalKeys("messages", "channel"), "指标库未初始化")
		})
	}
}

// 历史 Key 注册表按 (api_type, channel_id) 保存，覆盖写入与删除都必须是整组语义。
func TestHistoricalKeysStoreRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{
		DBPath:        filepath.Join(t.TempDir(), "metrics.db"),
		RetentionDays: 7,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	require.NoError(t, store.SaveHistoricalKeys("messages", "ch_1", []string{"old-1", "old-2"}))
	require.NoError(t, store.SaveHistoricalKeys("messages", "ch_2", []string{"old-3"}))
	require.NoError(t, store.SaveHistoricalKeys("chat", "ch_1", []string{"chat-old"}))

	loaded, err := store.LoadHistoricalKeys("messages")
	require.NoError(t, err)
	require.Equal(t, map[string][]string{
		"ch_1": {"old-1", "old-2"},
		"ch_2": {"old-3"},
	}, loaded)

	// 覆盖保存：只保留传入的集合，不累积。
	require.NoError(t, store.SaveHistoricalKeys("messages", "ch_1", []string{"old-2"}))
	loaded, err = store.LoadHistoricalKeys("messages")
	require.NoError(t, err)
	require.Equal(t, []string{"old-2"}, loaded["ch_1"])

	// 空集合等价于清空该渠道。
	require.NoError(t, store.SaveHistoricalKeys("messages", "ch_2", nil))
	loaded, err = store.LoadHistoricalKeys("messages")
	require.NoError(t, err)
	_, exists := loaded["ch_2"]
	require.False(t, exists)

	require.NoError(t, store.DeleteHistoricalKeys("messages", "ch_1"))
	loaded, err = store.LoadHistoricalKeys("messages")
	require.NoError(t, err)
	require.Empty(t, loaded)

	// 协议隔离：删除 messages 不影响 chat。
	chat, err := store.LoadHistoricalKeys("chat")
	require.NoError(t, err)
	require.Equal(t, []string{"chat-old"}, chat["ch_1"])
}
