package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/safety/llmgate"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func automaticRemoteService(t *testing.T, protocol, baseURL string) *Service {
	t.Helper()
	s := intentTestService(t)
	var managers [7]*metrics.MetricsManager
	for i := range managers {
		managers[i] = metrics.NewMetricsManager()
		t.Cleanup(managers[i].Stop)
	}
	sch := scheduler.NewChannelScheduler(s.Cfg, managers[0], managers[1], managers[2], managers[3], managers[4], managers[5], managers[6], nil, nil)
	t.Cleanup(sch.Stop)
	up := config.UpstreamConfig{ID: "classifier", Name: "自动分类测试", BaseURL: baseURL, ServiceType: "openai", APIKeys: []string{"test-key"}, DefaultModel: "default-model"}
	if protocol == "llm" {
		require.NoError(t, s.Cfg.AddChatUpstream(up))
	} else {
		_, err := s.Cfg.AddDecisionsUpstreamWithResult(up)
		require.NoError(t, err)
	}
	s.Caller = &llmgate.Caller{EnvConfig: config.NewEnvConfig(), CfgManager: s.Cfg, ChannelScheduler: sch}
	return s
}

func TestAutomaticRemoteClassificationUsesTwoOrderedStages(t *testing.T) {
	for _, protocol := range []string{"llm", "decisions", "systemone"} {
		for _, difficulty := range []string{"complex", "unmatched", "invalid"} {
			t.Run(protocol+"/"+difficulty, func(t *testing.T) {
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if !assert.NoError(t, err) {
						http.Error(w, "读取测试请求失败", http.StatusBadRequest)
						return
					}
					assert.Equal(t, "classifier-model", gjson.GetBytes(body, "model").String())
					var payload string
					switch protocol {
					case "llm":
						assert.Equal(t, "/v1/chat/completions", r.URL.Path)
						payload = gjson.GetBytes(body, `messages.#(role=="user").content`).String()
					case "decisions", "systemone":
						assert.Equal(t, "/v1/decisions", r.URL.Path)
						payload = gjson.GetBytes(body, "state").Raw
						assert.True(t, gjson.GetBytes(body, "questions").IsObject())
						assert.False(t, gjson.GetBytes(body, "input").Exists())
					}
					assert.Equal(t, "实现一个普通函数", gjson.Get(payload, "text").String())
					stage := calls.Add(1)
					label := "code_generation"
					if stage == 1 {
						assert.True(t, gjson.Get(payload, "categories.code_generation").Exists())
						assert.False(t, gjson.Get(payload, "categories.complex").Exists())
					} else {
						assert.EqualValues(t, 2, stage)
						assert.True(t, gjson.Get(payload, "categories.complex").Exists())
						assert.False(t, gjson.Get(payload, "categories.code_generation").Exists())
						label = difficulty
					}
					w.Header().Set("Content-Type", "application/json")
					var response any
					switch protocol {
					case "llm":
						response = map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": label}, "finish_reason": "stop"}}}
					case "decisions", "systemone":
						response = map[string]any{"answers": map[string]any{"intent": map[string]string{"type": "choice", "choice": label}}}
					}
					assert.NoError(t, json.NewEncoder(w).Encode(response))
				}))
				t.Cleanup(upstream.Close)
				s := automaticRemoteService(t, protocol, upstream.URL)
				cfg := automaticConfig()
				cfg.Classifier, cfg.ClassifierChannel, cfg.ClassifierModel = protocol, "classifier", "classifier-model"
				d, err := s.Classify(context.Background(), cfg, Input{Text: "实现一个普通函数"}, "remote-test")
				if difficulty == "invalid" {
					require.Error(t, err)
					require.Empty(t, d.PoolID)
				} else {
					require.NoError(t, err)
					require.Equal(t, "code_generation", d.Task)
					if difficulty == "complex" {
						require.Equal(t, "strong-code", d.PoolID)
						require.Equal(t, 2, d.Tier)
					} else {
						require.Empty(t, d.PoolID)
						require.Equal(t, -1, d.Tier)
					}
				}
				require.EqualValues(t, 2, calls.Load())
			})
		}
	}
}

func TestAutomaticRemoteRejectsUnknownTaskBeforeDifficulty(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"undeclared_task"},"finish_reason":"stop"}]}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(upstream.Close)
	s := automaticRemoteService(t, "llm", upstream.URL)
	cfg := automaticConfig()
	cfg.Classifier, cfg.ClassifierChannel, cfg.ClassifierModel = "llm", "classifier", "classifier-model"
	d, err := s.Classify(context.Background(), cfg, Input{Text: "实现一个函数"}, "invalid-task")
	require.ErrorContains(t, err, "未声明的类别")
	require.Empty(t, d.PoolID)
	require.EqualValues(t, 1, calls.Load())
}
