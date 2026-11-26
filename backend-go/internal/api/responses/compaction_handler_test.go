package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/storage/metrics"
)

func TestCompactHandlerProtectsNativeModelAndOpaqueOutput(t *testing.T) {
	for _, tc := range []struct {
		name, model, response string
		crossChannel          bool
		status, calls         int
	}{
		{"已确认模型对", "main", `{"id":"compact-new","output":[{"type":"message","role":"user","content":"保留前缀"},{"type":"compaction","encrypted_content":"opaque"}]}`, false, 200, 1},
		{"未确认模型对", "other", ``, false, 409, 0},
		{"禁止跨渠道映射", "main", ``, true, 409, 0},
		{"无会话存储也拒绝坏密文", "main", `{"id":"compact-new","output":[{"type":"compaction","encrypted_content":null}]}`, false, 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || gjson.GetBytes(body, "model").String() != "cheap" || r.URL.Path != "/v1/responses/compact" || r.Header.Get("X-Api-Proxy-Task") != "" {
					t.Errorf("原生压缩发送错误: path=%s model=%s task=%s err=%v", r.URL.Path, gjson.GetBytes(body, "model").String(), r.Header.Get("X-Api-Proxy-Task"), err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}))
			t.Cleanup(server.Close)
			cfg, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cfg.Close()) })
			_, err = cfg.AddChannel("responses", config.UpstreamConfig{
				ID: "source", Name: "主渠道", BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: "responses",
				Compaction: config.CompactionConfig{NativeSourceModel: "main", NativeModel: "cheap", NativeCompatibilityConfirmed: true},
			})
			require.NoError(t, err)
			if tc.crossChannel {
				_, err = cfg.AddChannel("responses", config.UpstreamConfig{ID: "other", Name: "其他渠道", BaseURL: server.URL, APIKeys: []string{"other-key"}, ServiceType: "responses"})
				require.NoError(t, err)
				_, err = cfg.UpdateChannel("responses", 0, config.UpstreamUpdate{
					ModelMapping: map[string][]string{"main": {"cheap"}}, ModelMappingChannels: map[string]map[string]string{"main": {"cheap": "other"}},
				})
				require.NoError(t, err)
				// 新增渠道默认置顶；本用例必须让主渠道先被选择。
				require.NoError(t, cfg.ReorderChannels("responses", []int{0, 1}))
			}
			mm := metrics.NewMetricsManager()
			t.Cleanup(mm.Stop)
			sch := scheduler.NewChannelScheduler(cfg, map[scheduler.ChannelKind]*metrics.MetricsManager{scheduler.ChannelKindResponses: mm}, nil, nil)
			t.Cleanup(sch.Stop)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"`+tc.model+`","input":"历史"}`))
			c.Request.Header.Set("Authorization", "Bearer gateway-key")
			c.Request.Header.Set("X-Api-Proxy-Task", "compact")
			CompactHandler(&config.EnvConfig{ProxyAccessKey: "gateway-key", RequestTimeout: 5000, MaxRequestBodySize: 1 << 20}, cfg, nil, sch)(c)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, int64(tc.calls), calls.Load())
			require.Equal(t, "native", w.Header().Get("X-Api-Proxy-Compaction"))
			if tc.status == 200 {
				require.JSONEq(t, tc.response, w.Body.String())
				require.Equal(t, "native_model", w.Header().Get("X-Api-Proxy-Compaction-Route"))
			}
		})
	}
}
