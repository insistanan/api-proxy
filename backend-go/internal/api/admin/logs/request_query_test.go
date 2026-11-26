package logs

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestQueriesRejectInvalidFiltersBeforeStorage(t *testing.T) {
	for _, query := range []string{"type=typo", "status=typo", "compare=maybe", "dimensionSort=typo", "duration=-1h", "interval=-1m"} {
		t.Run(query, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/api/request-stats?"+query, nil)
			GetRequestStats(nil)(c)
			require.Equal(t, 400, w.Code)
		})
	}
	for _, status := range []string{"completed", "failed", "cancelled", "interrupted", "blocked", "unknown"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/api/request-stats?status="+status+"&dimensionSort=volume", nil)
		require.True(t, validateLogQuery(c, true))
	}
}
