package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/orchestration/availability"
	"github.com/insistanan/api-proxy/internal/storage/logger"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
	"github.com/stretchr/testify/require"
)

type freePoolTransport func(*http.Request) (*http.Response, error)

func (f freePoolTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type freePoolCommitWriter struct {
	gin.ResponseWriter
	once      sync.Once
	committed chan struct{}
}

func (w *freePoolCommitWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.once.Do(func() { close(w.committed) })
	return n, err
}

// 四个真实协议 handler 走完整路由/转换/记账链；发送出口替换为内存响应，禁止连接真实 Kilo。
func TestFreePoolRoutesAllProtocolsThroughChat(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	for _, protocol := range []struct{ kind, path, body string }{
		{"messages", "/v1/messages", `{"model":"%s","max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`},
		{"responses", "/v1/responses", `{"model":"%s","input":"hi"}`},
		{"chat", "/v1/chat/completions", `{"model":"%s","messages":[{"role":"user","content":"hi"}]}`},
		{"gemini", "/v1beta/models/%s:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`},
	} {
		for _, mode := range []string{"health", "fallback", "explicit", "explicit-fallback", "limit", "invalid", "committed", "cooling", "health-unavailable"} {
			t.Run(protocol.kind+"/"+mode, func(t *testing.T) {
				r, app := newChannelAdminTestRouter(t)
				require.NoError(t, app.cfgManager.EnsureFreeChannels())
				require.NoError(t, app.cfgManager.SetFuzzyModeEnabled(false))
				require.NoError(t, app.cfgManager.SetKiloModels([]string{"bad", "good", "third", "fourth", config.KiloAutoModelID}, ""))
				store, err := logger.Open(filepath.Join(t.TempDir(), "logs.db"))
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, store.Close()) })
				app.channelScheduler.SetRequestLogStore(metrics.NewRequestLogStore(store))
				if mode == "health-unavailable" {
					app.channelScheduler.SetRequestLogStore(metrics.NewRequestLogStore(nil))
				}
				app.envCfg = &config.EnvConfig{ProxyAccessKey: "test-admin", LogLevel: "error", MaxRequestBodySize: 1024 * 1024, RequestTimeout: 5501, StreamIdleTimeout: 5}
				app.contentSafetyPipeline = hooks.NewContentSafetyPipeline(app.cfgManager)
				app.sessionManager, err = session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "sessions.db"), time.Hour, 0, 0)
				require.NoError(t, err)
				t.Cleanup(app.sessionManager.Stop)
				committed := make(chan struct{})
				r.Use(func(c *gin.Context) {
					c.Writer = &freePoolCommitWriter{ResponseWriter: c.Writer, committed: committed}
					c.Next()
				})
				app.setupProxyEndpoints(r)
				// 没有普通渠道，free 必须直接命中内置分组；不允许变成付费兜底请求。
				if mode == "health" {
					up := app.cfgManager.GetConfig().ChatUpstream[0]
					for i := 0; i < 6; i++ {
						require.NoError(t, store.RecordRequest(logger.RequestLogRecord{Payload: []byte(`{}`),
							Availability: &availability.Sample{Kind: protocol.kind, ChannelID: "builtin-kilo-" + protocol.kind,
								Model: "good", CandidateID: up.RequestIdentity("").Fingerprint(), Outcome: availability.Success}}))
					}
					require.NoError(t, store.Flush())
				}
				if mode == "cooling" {
					up := app.cfgManager.GetConfig().ChatUpstream[0]
					for _, model := range up.ModelMapping[config.FreeModelID] {
						require.NoError(t, store.RecordRequest(logger.RequestLogRecord{Payload: []byte(`{}`),
							Availability: &availability.Sample{Kind: protocol.kind, ChannelID: "builtin-kilo-" + protocol.kind,
								Model: model, CandidateID: up.RequestIdentity("").Fingerprint(), Outcome: availability.RateLimited,
								CooldownUntil: time.Now().Add(time.Minute).UnixMilli()}}))
					}
					require.NoError(t, store.Flush())
				}
				client, err := httpclient.GetManager().GetStandardClient(5501*time.Millisecond, false, "")
				require.NoError(t, err)
				if mode == "committed" {
					client, err = httpclient.GetManager().GetStreamClient(false, "")
					require.NoError(t, err)
				}
				original := client.Transport
				t.Cleanup(func() { client.Transport = original })
				var sentModels []string
				client.Transport = freePoolTransport(func(req *http.Request) (*http.Response, error) {
					require.Equal(t, config.KiloChatURL, req.URL.String())
					require.Empty(t, req.Header.Get("Authorization"))
					require.Empty(t, req.Header.Get("x-api-key"))
					require.Empty(t, req.Header.Get("x-goog-api-key"))
					var payload map[string]any
					require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
					require.NotEmpty(t, payload["messages"], "实际上游必须接收 Chat 消息")
					require.NotContains(t, payload, "input")
					require.NotContains(t, payload, "contents")
					model := payload["model"].(string)
					sentModels = append(sentModels, model)
					status := http.StatusOK
					body := fmt.Sprintf(`{"id":"reply","model":%q,"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"fixture reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`, model)
					contentType := "application/json"
					switch {
					case mode == "committed":
						contentType = "text/event-stream"
						reader, writer := io.Pipe()
						go func() {
							defer writer.Close()
							_, _ = io.WriteString(writer, "data: {\"id\":\"reply\",\"model\":\"bad\",\"choices\":[{\"delta\":{\"content\":\"partial reply.\\n\"}}]}\n\n")
							select {
							case <-committed:
								_, _ = io.WriteString(writer, "data: {\"error\":{\"message\":\"fixture failure\"}}\n\n")
							case <-req.Context().Done():
							}
						}()
						return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: reader, Request: req}, nil
					case mode == "invalid":
						status, body = http.StatusBadRequest, `{"error":{"code":"context_length_exceeded","message":"too long"}}`
					case mode == "limit" || mode == "fallback" && model == "bad" || mode == "explicit-fallback" && model == "third":
						status, body = http.StatusNotFound, `{"error":{"code":"model_not_found","message":"unavailable"}}`
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				})
				requested := "free"
				if mode == "explicit" || mode == "explicit-fallback" {
					requested = "free/third"
				}
				path, body := protocol.path, protocol.body
				if protocol.kind == "gemini" {
					path = fmt.Sprintf(path, requested)
				} else {
					body = fmt.Sprintf(body, requested)
				}
				if mode == "committed" {
					if protocol.kind == "gemini" {
						path = strings.Replace(path, ":generateContent", ":streamGenerateContent?alt=sse", 1)
					} else {
						body = strings.TrimSuffix(body, "}") + `,"stream":true}`
					}
				}
				wantStatus := http.StatusOK
				if mode == "limit" {
					wantStatus = http.StatusNotFound
				}
				if mode == "invalid" {
					wantStatus = http.StatusBadRequest
				}
				if mode == "cooling" || mode == "health-unavailable" {
					wantStatus = http.StatusServiceUnavailable
				}
				w := channelAdminRequest(t, r, http.MethodPost, path, body, wantStatus)
				switch mode {
				case "health":
					require.Equal(t, []string{"good"}, sentModels)
				case "fallback":
					require.Equal(t, []string{"bad", "good"}, sentModels)
				case "explicit":
					require.Equal(t, []string{"third"}, sentModels)
				case "explicit-fallback":
					require.Equal(t, []string{"third", "bad"}, sentModels)
				case "limit":
					require.Equal(t, []string{"bad", "good", "third"}, sentModels)
				case "invalid", "committed":
					require.Equal(t, []string{"bad"}, sentModels, "不可重试或已经输出内容后不能切换模型")
				case "cooling", "health-unavailable":
					require.Empty(t, sentModels, "无候选或健康读取失败时不发送请求")
					require.Contains(t, w.Body.String(), "FREE_MODELS_UNAVAILABLE")
				}
				if mode == "health" || mode == "fallback" || mode == "explicit" || mode == "explicit-fallback" {
					require.Contains(t, w.Body.String(), "fixture reply")
				}
				require.NoError(t, store.Flush())
				observations, err := store.QueryAvailability(context.Background(), protocol.kind, time.Now())
				require.NoError(t, err)
				if mode == "health-unavailable" {
					require.Empty(t, observations)
				} else {
					require.NotEmpty(t, observations)
				}
				for _, observation := range observations {
					require.NotEqual(t, "free", observation.Model, "按实际上游模型记录健康")
				}
			})
		}
	}
}
