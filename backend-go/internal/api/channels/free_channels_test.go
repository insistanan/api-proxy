package channels

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
)

func TestUpdateKiloFreeChannel_BranchLimitAndEnabledAreIndependent(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	require.NoError(t, manager.EnsureFreeChannels())
	update := func(body string, status int) {
		t.Helper()
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest("PUT", "/free-channels/kilo", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		UpdateKiloFreeChannel(manager)(c)
		require.Equal(t, status, response.Code, response.Body.String())
	}
	update(`{"enabled":false,"maxBranchConcurrency":5}`, 200)
	update(`{"maxBranchConcurrency":4}`, 200)
	for _, value := range []string{"null", "0", "-1", "1.5", `"3"`} {
		update(`{"enabled":true,"maxBranchConcurrency":`+value+`}`, 400)
	}
	snapshot, err := manager.GetFreeChannels()
	require.NoError(t, err)
	require.False(t, snapshot.Kilo.Enabled)
	require.Equal(t, 4, snapshot.Kilo.MaxBranchConcurrency)
	update(`{"enabled":true}`, 200)
	snapshot, err = manager.GetFreeChannels()
	require.NoError(t, err)
	require.True(t, snapshot.Kilo.Enabled)
	require.Equal(t, 4, snapshot.Kilo.MaxBranchConcurrency)
}

func TestParseKiloFreeModelsRequiresExplicitFreeFlag(t *testing.T) {
	models, err := parseKiloFreeModels([]byte(`{"data":[{"id":"paid/free","isFree":false},{"id":"no-price/free"},{"id":"b","isFree":true},{"id":"a","isFree":true},{"id":"b","isFree":true}]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, models)
	for _, body := range []string{`{`, `{"data":[]}`, `{"data":[{"id":"free"}]}`, `{"data":[{"id":" ","isFree":true}]}`, `{"data":[{"id":"x\ny","isFree":true}]}`} {
		_, err := parseKiloFreeModels([]byte(body))
		require.Error(t, err, body)
	}
}
