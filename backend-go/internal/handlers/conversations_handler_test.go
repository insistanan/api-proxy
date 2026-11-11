package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/conversation"
	"github.com/insistanan/api-proxy/internal/sqlitedb"
	"github.com/stretchr/testify/require"
)

func TestDeleteConversationReadFailureIsNotNotFound(t *testing.T) {
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "conversations.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	registry, err := conversation.NewPersistentRegistryWithDB(db)
	require.NoError(t, err)
	t.Cleanup(registry.Stop)
	router := gin.New()
	router.DELETE("/conversations/:id", DeleteConversation(registry, nil))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/conversations/missing", nil))
	require.Equal(t, http.StatusNotFound, response.Code)

	require.NoError(t, db.Close())
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/conversations/missing", nil))
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), "读取持久化对话失败")
}
