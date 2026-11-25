package cachekeepalive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"github.com/stretchr/testify/require"
)

func senderFixture(t *testing.T, protocol string, handler http.HandlerFunc) (*config.ConfigManager, *snapshot) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cfg.Close()) })
	up := &config.UpstreamConfig{ID: "test", Name: "test", BaseURL: server.URL, ServiceType: protocol, APIKeys: []string{"fixture"}, MaxConcurrency: 5, CacheKeepalive: samplePolicy()}
	_, err = cfg.AddChannel("messages", *up)
	require.NoError(t, err)
	return cfg, &snapshot{source: Source{Kind: "messages", URL: server.URL + "/v1/responses", Upstream: up, APIKey: "fixture", Header: http.Header{"Authorization": []string{"Bearer fixture"}}},
		policy: samplePolicy(), model: "mapped-model", body: []byte(`{"model":"mapped-model","input":"heartbeat","stream":true,"store":false}`),
		mode: "implicit", stream: true, ttl: 5 * time.Minute, lastSent: time.Now(), deadline: time.Now().Add(30 * time.Minute)}
}

func TestKeepaliveSender_StreamsUsageIntoLedgerWithoutConversationWriteback(t *testing.T) {
	var gotBody string
	cfg, s := senderFixture(t, "responses", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		if r.Header.Get("Authorization") != "Bearer fixture" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata:{\"type\":\"response.completed\",\"response\":{\"id\":\"heartbeat-only\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1010,\"output_tokens\":3,\"input_tokens_details\":{\"cached_tokens\":1000}}}}\n\n")
	})
	var facts []tokenusage.Fact
	send := newSender(cfg, func(fact tokenusage.Fact) { facts = append(facts, fact) })
	result := send(context.Background(), s)
	require.NoError(t, result.err)
	require.True(t, result.sent)
	require.Equal(t, string(s.body), gotBody)
	require.Len(t, facts, 1)
	require.Equal(t, "cache_keepalive/responses", facts[0].Protocol)
	require.EqualValues(t, 10, facts[0].Input)
	require.EqualValues(t, 1000, facts[0].CacheRead)
	require.Equal(t, "mapped-model", facts[0].Model)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = send(ctx, s)
	require.Error(t, result.err)
	require.False(t, result.sent)
	require.Len(t, facts, 1)
}

func TestGeminiExplicitCache_ExtendsOnlyMatchingResourceAndVerifiesExpiry(t *testing.T) {
	for _, scenario := range []string{"extend", "already-retained", "expired", "wrong-resource", "ignored-update"} {
		t.Run(scenario, func(t *testing.T) {
			patches := 0
			expiry := time.Now().Add(2 * time.Minute)
			if scenario == "expired" {
				expiry = time.Now().Add(-time.Minute)
			}
			if scenario == "already-retained" {
				expiry = time.Now().Add(time.Hour)
			}
			cfg, s := senderFixture(t, "gemini", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1beta/cachedContents/test-cache" {
					w.WriteHeader(404)
					return
				}
				if r.URL.Query().Get("fields") != "name,expireTime" {
					w.WriteHeader(400)
					return
				}
				name := "cachedContents/test-cache"
				if scenario == "wrong-resource" {
					name = "cachedContents/other"
				}
				if r.Method == http.MethodPatch {
					patches++
					if r.URL.Query().Get("updateMask") != "expireTime" {
						w.WriteHeader(400)
						return
					}
					var body struct {
						ExpireTime time.Time `json:"expireTime"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						w.WriteHeader(400)
						return
					}
					if scenario != "ignored-update" {
						expiry = body.ExpireTime
					}
				}
				_ = json.NewEncoder(w).Encode(geminiCacheResource{Name: name, ExpireTime: expiry})
			})
			s.mode = "explicit"
			s.cacheName = "cachedContents/test-cache"
			s.cacheURL = strings.TrimSuffix(s.source.URL, "/v1/responses") + "/v1beta/" + s.cacheName
			result := newSender(cfg)(context.Background(), s)
			if scenario == "expired" || scenario == "wrong-resource" || scenario == "ignored-update" {
				require.Error(t, result.err)
			} else {
				require.NoError(t, result.err)
				require.True(t, expiry.Equal(result.expiresAt))
			}
			if scenario == "extend" || scenario == "ignored-update" {
				require.Equal(t, 1, patches)
			} else {
				require.Zero(t, patches)
			}
			if scenario == "extend" {
				require.False(t, expiry.After(s.deadline), "续期不能超过本轮时限")
			}
		})
	}
}

func TestGeminiExplicitCache_DeduplicatesConcurrentRenewals(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var gets atomic.Int32
	cfg, s := senderFixture(t, "gemini", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if gets.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
		}
		_ = json.NewEncoder(w).Encode(geminiCacheResource{Name: "cachedContents/shared", ExpireTime: time.Now().Add(time.Hour)})
	})
	s.mode, s.cacheName = "explicit", "cachedContents/shared"
	s.cacheURL = strings.TrimSuffix(s.source.URL, "/v1/responses") + "/v1beta/" + s.cacheName
	send := newSender(cfg)
	first := make(chan sendResult, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { first <- send(ctx, s) }()
	select {
	case <-entered:
	case result := <-first:
		t.Fatalf("首次读取没有进入上游: %v", result.err)
	}
	duplicate := send(context.Background(), s)
	close(release)
	require.ErrorContains(t, duplicate.err, "同一 Gemini 缓存资源")
	require.False(t, duplicate.sent)
	require.NoError(t, (<-first).err)
	require.EqualValues(t, 1, gets.Load())
}
