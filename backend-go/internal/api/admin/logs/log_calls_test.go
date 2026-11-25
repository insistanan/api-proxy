package logs

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/storage/logger"
)

func TestLogCallsRejectInvalidFiltersAndMissingRequests(t *testing.T) {
	store, err := logger.Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	router := gin.New()
	router.GET("/log-calls", GetLogCalls(store))
	router.GET("/log-call-attempts", GetLogCallAttempts(store))
	for _, query := range []string{"limit=101", "limit=bad", "status=unknown", "from=bad", "from=2026-11-12T00:00:00Z&to=2026-11-01T00:00:00Z", "cursor=bad!"} {
		t.Run(query, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/log-calls?"+query, nil))
			require.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/log-call-attempts", nil))
	require.Equal(t, http.StatusBadRequest, response.Code)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/log-call-attempts?requestId=missing", nil))
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestSystemLogReportsDetailBudgetError(t *testing.T) {
	store, err := logger.Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	for range 101 {
		require.NoError(t, store.RecordRequest(logger.RequestLogRecord{RequestID: "many", Payload: []byte(`{"status":"failed"}`)}))
	}
	require.NoError(t, store.Flush())
	router := gin.New()
	router.GET("/logs/:requestId", GetSystemLog(store))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/logs/many", nil))
	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	require.Contains(t, response.Body.String(), "查询上限")
}
