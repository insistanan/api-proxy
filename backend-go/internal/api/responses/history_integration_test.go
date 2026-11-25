package responses

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/storage/metrics"
	"insistanan/api-proxy/internal/storage/sqlitedb"
)

type historyRouteFixture struct {
	router   *gin.Engine
	db       *sql.DB
	manager  *session.SessionManager
	registry *conversation.Registry
	metrics  *metrics.MetricsManager
	close    func()
}

func newHistoryRouteFixture(t *testing.T, path, url, key string) *historyRouteFixture {
	t.Helper()
	db, err := sqlitedb.Open(path)
	require.NoError(t, err)
	registry, err := conversation.NewPersistentRegistryWithDB(db)
	require.NoError(t, err)
	manager, err := session.NewPersistentSessionManagerWithDB(db, time.Hour, 0, 0)
	require.NoError(t, err)
	cfg, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	require.NoError(t, cfg.SetFuzzyModeEnabled(false))
	_, err = cfg.AddChannel("responses", config.UpstreamConfig{Name: "upstream", BaseURL: url, ServiceType: "responses", APIKeys: []string{key, "reserve-key"}})
	require.NoError(t, err)
	mm := metrics.NewMetricsManager()
	sch := scheduler.NewChannelScheduler(cfg, map[scheduler.ChannelKind]*metrics.MetricsManager{scheduler.ChannelKindResponses: mm}, nil, nil)
	sch.SetConversationRegistry(registry)
	router := gin.New()
	router.POST("/v1/responses", Handler(&config.EnvConfig{ProxyAccessKey: "test-access", RequestTimeout: 5000, ResponseHeaderTimeout: 5, MaxRequestBodySize: 10 << 20}, cfg, manager, sch))
	once := &sync.Once{}
	close := func() {
		once.Do(func() {
			sch.Stop()
			manager.Stop()
			require.NoError(t, registry.Stop())
			mm.Stop()
			require.NoError(t, cfg.Close())
			require.NoError(t, db.Close())
		})
	}
	t.Cleanup(close)
	return &historyRouteFixture{router: router, db: db, manager: manager, registry: registry, metrics: mm, close: close}
}
func sendHistoryRequest(router http.Handler, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "test-access")
	router.ServeHTTP(recorder, req)
	return recorder
}
func TestImageToolHistoryRestoresAfterRestartAndCredentialChange(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, image := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/image=%t", stream, image), func(t *testing.T) {
				var calls atomic.Int64
				var secondBody []byte
				output := `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"仅返回文字"}]}]`
				if image {
					output = `[{"id":"img","type":"image_generation_call","status":"completed","result":"AQID","revised_prompt":"中文图片","future":{"mode":"keep","large":9007199254740993}}]`
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					call := calls.Add(1)
					if call == 2 {
						secondBody = body
					}
					response := fmt.Sprintf(`{"id":"response-%d","object":"response","model":"m","status":"completed","output":%s,"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`, call, output)
					if gjson.GetBytes(body, "stream").Bool() {
						w.Header().Set("Content-Type", "text/event-stream")
						// 两个 data 行构成一个完整 JSON 事件，覆盖 HTTP 交付入口。
						fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\ndata: \"response\":%s}\n\n", response)
					} else {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, response)
					}
				}))
				defer server.Close()
				path := filepath.Join(t.TempDir(), "dialogue.db")
				first := newHistoryRouteFixture(t, path, server.URL, "first-key")
				response := sendHistoryRequest(first.router, fmt.Sprintf(`{"model":"m","input":[{"role":"user","content":"画图","future_input":9007199254740993}],"stream":%t,"tools":[{"type":"image_generation"}]}`, stream))
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "response-1")
				if image {
					require.Contains(t, response.Body.String(), "AQID")
				}
				first.close()
				second := newHistoryRouteFixture(t, path, server.URL, "second-key")
				response = sendHistoryRequest(second.router, `{"model":"m","previous_response_id":"response-1","input":"继续"}`)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, int64(2), calls.Load())
				require.False(t, gjson.GetBytes(secondBody, "previous_response_id").Exists())
				require.Contains(t, string(secondBody), "画图")
				require.Equal(t, "9007199254740993", gjson.GetBytes(secondBody, "input.0.future_input").Raw)
				require.Contains(t, string(secondBody), "继续")
				if image {
					require.Contains(t, string(secondBody), "AQID")
					require.Contains(t, string(secondBody), "future")
					require.Contains(t, string(secondBody), "9007199254740993")
				} else {
					require.Contains(t, string(secondBody), "仅返回文字")
				}
			})
		}
	}
}
func TestResponsesLocalCommitFailureDoesNotRetryOrPublishTerminal(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var calls atomic.Int64
			var fixture *historyRouteFixture
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, err := fixture.db.Exec(`CREATE TRIGGER fail_commit BEFORE UPDATE ON response_sessions BEGIN SELECT RAISE(ABORT,'injected'); END`)
				if err != nil {
					t.Error(err)
				}
				response := `{"id":"not-published","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":11,"output_tokens":7}}`
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
				} else {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, response)
				}
			}))
			defer server.Close()
			fixture = newHistoryRouteFixture(t, filepath.Join(t.TempDir(), "dialogue.db"), server.URL, "first-key")
			response := sendHistoryRequest(fixture.router, fmt.Sprintf(`{"model":"m","input":"hello","stream":%t}`, stream))
			require.Equal(t, int64(1), calls.Load(), response.Body.String())
			require.NotContains(t, response.Body.String(), `"type":"response.completed"`)
			require.Contains(t, response.Body.String(), "保存 Responses 历史失败")
			var count int
			require.NoError(t, fixture.db.QueryRow(`SELECT COUNT(*) FROM response_session_mappings`).Scan(&count))
			require.Zero(t, count)
			_, known, err := fixture.manager.ResponseOwner(context.Background(), "not-published")
			require.NoError(t, err)
			require.False(t, known)
			for _, key := range []string{"first-key", "reserve-key"} {
				if metric := fixture.metrics.GetKeyMetrics(server.URL, key, 0); metric != nil {
					require.Zero(t, metric.FailureCount)
				}
			}
		})
	}
}

func TestIncompleteResponsesDoNotPersistPartialTools(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := `{"id":"incomplete","model":"m","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","status":"incomplete","role":"assistant","content":[{"type":"output_text","text":"已交付文字"}]},{"type":"function_call","status":"incomplete","call_id":"partial","name":"exec","arguments":"{"}],"usage":{"input_tokens":11,"output_tokens":7}}`
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.incomplete\",\"response\":%s}\n\n", response)
				} else {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, response)
				}
			}))
			defer server.Close()
			fixture := newHistoryRouteFixture(t, filepath.Join(t.TempDir(), "dialogue.db"), server.URL, "first-key")
			response := sendHistoryRequest(fixture.router, fmt.Sprintf(`{"model":"m","input":"hello","stream":%t}`, stream))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "partial", "客户端保留上游实际截断结果")
			history, err := fixture.manager.GetOrCreateSessionForConversation(context.Background(), "incomplete", "")
			require.NoError(t, err, response.Body.String())
			require.Len(t, history.Messages, 2)
			require.Equal(t, "message", history.Messages[1].Type)
		})
	}
}

func TestHTTPTruncationDoesNotPublishRecoverableHistory(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"truncated\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"item_id\":\"message\",\"content_index\":0,\"delta\":\"已交付\"}\n\n"
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)+100))
		io.WriteString(w, body)
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	fixture := newHistoryRouteFixture(t, filepath.Join(t.TempDir(), "dialogue.db"), server.URL, "first-key")
	response := sendHistoryRequest(fixture.router, `{"model":"m","input":"hello","stream":true}`)
	require.Contains(t, response.Body.String(), "已交付")
	require.NotContains(t, response.Body.String(), `"type":"response.completed"`)
	require.Equal(t, int64(1), calls.Load())
	_, known, err := fixture.manager.ResponseOwner(context.Background(), "truncated")
	require.NoError(t, err)
	require.False(t, known)
}
