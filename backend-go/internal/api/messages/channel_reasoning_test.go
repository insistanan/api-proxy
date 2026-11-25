package messages

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/orchestration/urlhealth"
	"insistanan/api-proxy/internal/storage/metrics"
)

func TestMessagesChannelReasoningOffSendsDisableAndForwardsUpstreamThinking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		protocol, disabledPath, disabledValue, response, termination string
	}{
		{
			"claude", "thinking.type", "disabled",
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"model\":\"mapped-model\",\"content\":[]}}\n\n" +
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"from upstream\"}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"test-signature\"}}\n\n" +
				"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
				"data: {\"type\":\"message_stop\"}\n\n",
			"test-signature",
		},
		{
			"openai", "reasoning_effort", "none",
			"data: {\"id\":\"chat_test\",\"model\":\"mapped-model\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"from upstream\"}}]}\n\n" +
				"data: {\"id\":\"chat_test\",\"model\":\"mapped-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n",
			"answer",
		},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			captured := make(chan []byte, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read upstream request: %v", err)
					http.Error(w, "could not read request", http.StatusBadRequest)
					return
				}
				select {
				case captured <- body:
				default:
					t.Error("unexpected retry")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := fmt.Fprint(w, tc.response); err != nil {
					t.Errorf("write upstream response: %v", err)
				}
			}))
			t.Cleanup(upstream.Close)

			manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, manager.Close()) })
			require.NoError(t, manager.AddUpstream(config.UpstreamConfig{
				Name: "reasoning-test", ServiceType: tc.protocol, BaseURL: upstream.URL,
				APIKeys: []string{"fixture"}, DefaultModel: "mapped-model",
			}))
			off := "none"
			_, err = manager.UpdateUpstream(0, config.UpstreamUpdate{ReasoningEffort: &off})
			require.NoError(t, err)
			channels := scheduler.NewChannelScheduler(manager, map[scheduler.ChannelKind]*metrics.MetricsManager{scheduler.ChannelKindMessages: metrics.NewMetricsManager()}, session.NewTraceAffinityManager(), urlhealth.NewURLManager(30*time.Second, 3))
			t.Cleanup(channels.Stop)

			env := config.DefaultEnvConfig()
			env.ProxyAccessKey = contentSafetyTestAccessKey
			router := gin.New()
			router.POST("/v1/messages", Handler(env, manager, channels))
			response := performContentSafetyMessagesRequest(t, router,
				`{"model":"client-model","stream":true,"max_tokens":4096,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hello"}]}`,
				"reasoning-test",
			)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			select {
			case body := <-captured:
				require.Equal(t, "mapped-model", gjson.GetBytes(body, "model").String())
				require.Equal(t, tc.disabledValue, gjson.GetBytes(body, tc.disabledPath).String())
				require.False(t, gjson.GetBytes(body, "output_config.effort").Exists())
			default:
				t.Fatal("upstream received no request")
			}
			require.Contains(t, response.Body.String(), "thinking_delta")
			require.Contains(t, response.Body.String(), "from upstream")
			require.Contains(t, response.Body.String(), tc.termination)
			require.Contains(t, response.Body.String(), "message_stop")
		})
	}
}
