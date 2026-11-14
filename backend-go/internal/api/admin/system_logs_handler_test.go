package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/storage/logger"
	"github.com/insistanan/api-proxy/internal/storage/sqlitedb"
	"github.com/stretchr/testify/require"
)

func recordConversationLog(t *testing.T, store *logger.Store, id, kind, conversationID, preview string, stamp int64) {
	t.Helper()
	body, err := json.Marshal(logger.RequestLifecycle{
		ConversationID: conversationID, StartedAt: stamp, Status: "completed", PromptPreview: preview,
	})
	require.NoError(t, err)
	require.NoError(t, store.RecordTraffic(logger.TrafficLog{
		RequestID: id, APIType: kind, Phase: logger.PhaseRequestEnd, UnixMilli: stamp, Body: string(body),
	}))
}

func TestLogConversationsNamesAreReadLiveAndPreserveFirstPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := logger.Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	registry := conversation.NewRegistry()
	t.Cleanup(registry.Stop)
	record, err := registry.ObserveRequestWithError(conversation.Observation{APIKind: "responses", FirstPrompt: "注册表提示词"})
	require.NoError(t, err)
	now := time.Now().UnixMilli()
	recordConversationLog(t, store, "first", "Responses", record.ID, "首条提示词", now)
	recordConversationLog(t, store, "latest", "Responses", record.ID, "最新提示词", now+1)
	recordConversationLog(t, store, "deleted", "Messages", "missing-record", "已删除会话的提示词", now+2)
	recordConversationLog(t, store, "no-identity", "Messages", "", "无会话标识", now+3)
	// 相同 ID 属于另一协议时，不能误用自定义名称。
	recordConversationLog(t, store, "other-kind", "Chat", record.ID, "另一协议", now+4)
	require.NoError(t, store.Flush())

	query := func(reg *conversation.Registry) map[string]logger.LogConversationSummary {
		t.Helper()
		router := gin.New()
		router.GET("/log-conversations", GetLogConversations(store, reg))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/log-conversations", nil))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var page logger.LogConversationsPage
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
		require.Equal(t, int64(4), page.Total)
		groups := make(map[string]logger.LogConversationSummary)
		for _, group := range page.Conversations {
			groups[group.Key] = group
		}
		return groups
	}

	key := "responses:" + record.ID
	groups := query(registry)
	require.Empty(t, groups[key].Name)
	require.Equal(t, "首条提示词", groups[key].Title)
	_, err = registry.SetName(record.ID, "自定义会话")
	require.NoError(t, err)
	groups = query(registry)
	require.Equal(t, "自定义会话", groups[key].Name)
	require.Equal(t, "首条提示词", groups[key].Title)
	require.Equal(t, "最新提示词", groups[key].Latest.PromptPreview)
	require.Equal(t, int64(2), groups[key].RequestCount)
	require.Empty(t, groups["chat:"+record.ID].Name)
	require.Empty(t, groups["messages:missing-record"].Name)
	require.Equal(t, "已删除会话的提示词", groups["messages:missing-record"].Title)
	require.Empty(t, groups["request:no-identity"].Name)
	_, err = registry.SetName(record.ID, "再次改名")
	require.NoError(t, err)
	require.Equal(t, "再次改名", query(registry)[key].Name)
	require.Empty(t, query(nil)[key].Name)
}

func TestLogConversationsRegistryReadFailureIsReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := logger.Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	recordConversationLog(t, store, "request", "Messages", "uncached", "提示词", time.Now().UnixMilli())
	require.NoError(t, store.Flush())
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "conversations.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	registry, err := conversation.NewPersistentRegistryWithDB(db)
	require.NoError(t, err)
	t.Cleanup(registry.Stop)
	require.NoError(t, db.Close())
	router := gin.New()
	router.GET("/log-conversations", GetLogConversations(store, registry))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/log-conversations", nil))
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), "读取持久化对话失败")
}
