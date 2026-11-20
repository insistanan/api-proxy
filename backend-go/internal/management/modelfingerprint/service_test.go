package modelfingerprint

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type referenceFixture struct {
	outputs []lmfpd.Output
}

func (f *referenceFixture) BuiltAt() string    { return "fixture-version" }
func (f *referenceFixture) DataSHA256() string { return "fixture-data-digest" }

func (f *referenceFixture) Analyze(outputs []lmfpd.Output, _ bool) (*lmfpd.Analysis, error) {
	f.outputs = outputs
	return &lmfpd.Analysis{Decision: "scored", UsedOutputs: len(outputs)}, nil
}

func newFixtureService(t *testing.T, upstream config.UpstreamConfig) (*Service, string) {
	t.Helper()
	upstream.ID = "d37fdd4b-e928-4410-9dba-796ba0613e72"
	upstream.Name = "指纹测试渠道"
	upstream.MaxConcurrency = 2
	upstream.ModelMapping = map[string][]string{"requested": {"wrong-model"}}
	upstream.DefaultModel = "wrong-default"
	upstream.ReasoningEffort = "high"
	data, err := json.Marshal(config.Config{
		Settings: config.SettingsConfig{Gateway: config.DefaultGatewaySettings()},
		Upstream: []config.UpstreamConfig{upstream}, ClaudeCodeDisguiseEnabled: true, CodexDisguiseEnabled: true,
	})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	cm, err := config.NewConfigManager(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cm.Close()) })
	service := New(cm)
	service.detector = &referenceFixture{}
	return service, upstream.ID
}

func TestSamplingPinsTargetAndKeepsDisguise(t *testing.T) {
	for _, protocol := range []string{"responses", "claude"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, "Bearer selected-key", r.Header.Get("Authorization"))
				require.Equal(t, "requested", gjson.GetBytes(body, "model").String())
				require.True(t, gjson.GetBytes(body, "stream").Bool())
				for _, field := range []string{"tools", "previous_response_id", "reasoning", "thinking", "output_config"} {
					require.False(t, gjson.GetBytes(body, field).Exists(), field)
				}
				w.Header().Set("Content-Type", "application/json")
				text := strings.Repeat("247,83,319,", 111)
				if protocol == "responses" {
					require.Equal(t, "/v1/responses", r.URL.Path)
					require.Contains(t, r.UserAgent(), "codex_cli_rs/")
					require.Equal(t, "codex_cli_rs", r.Header.Get("originator"))
					require.False(t, gjson.GetBytes(body, "store").Bool())
					require.Len(t, gjson.GetBytes(body, "input").Array(), 1)
					_, err = fmt.Fprintf(w, `{"status":"completed","model":"reported","output":[{"type":"message","content":[{"type":"output_text","text":%q}]}]}`, text)
				} else {
					require.Equal(t, "/v1/messages", r.URL.Path)
					require.Contains(t, r.UserAgent(), "claude-cli/")
					require.Equal(t, "cli", r.Header.Get("X-App"))
					require.NotEmpty(t, r.Header.Get("Anthropic-Beta"))
					require.Contains(t, gjson.GetBytes(body, "system.0.text").String(), "x-anthropic-billing-header:")
					require.NotEmpty(t, gjson.GetBytes(body, "metadata.user_id").String())
					require.Len(t, gjson.GetBytes(body, "messages").Array(), 1)
					_, err = fmt.Fprintf(w, `{"stop_reason":"end_turn","model":"reported","content":[{"type":"text","text":%q}]}`, text)
				}
				require.NoError(t, err)
			}))
			defer server.Close()
			service, id := newFixtureService(t, config.UpstreamConfig{
				ServiceType: protocol, BaseURLs: []string{"http://127.0.0.1:1", server.URL},
				APIKeys: []string{"unused-key", "selected-key"}, ProxyMode: "direct",
			})
			target, err := service.Prepare(id, Request{Model: "requested", URLIndex: 1, KeyIndex: 1})
			require.NoError(t, err)
			var events []Event
			service.Run(context.Background(), target, func(event Event) { events = append(events, event) })
			require.Equal(t, 3, calls)
			require.Equal(t, "result", events[len(events)-1].Type)
			require.Equal(t, "waiting", events[2].Sample.State, "进度事件必须保留当时的快照")
			for _, output := range service.detector.(*referenceFixture).outputs {
				require.NotEmpty(t, output.Text)
			}
			located, ok := service.cfg.FindChannelByID(id)
			require.True(t, ok)
			require.Equal(t, "wrong-default", located.Upstream.DefaultModel)
			require.Equal(t, "high", located.Upstream.ReasoningEffort)
			require.Len(t, located.Upstream.APIKeys, 2)
		})
	}
}

func TestSamplingDoesNotFollowRedirectOrSwitchKeys(t *testing.T) {
	followed := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed++ }))
	defer destination.Close()
	calls := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "Bearer selected-key", r.Header.Get("Authorization"))
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	service, id := newFixtureService(t, config.UpstreamConfig{
		ServiceType: "openai", BaseURLs: []string{source.URL, destination.URL}, APIKeys: []string{"selected-key", "other-key"},
	})
	target, err := service.Prepare(id, Request{Model: "requested"})
	require.NoError(t, err)
	var settled []Sample
	service.Run(context.Background(), target, func(event Event) {
		if event.Sample != nil && event.Sample.State != "waiting" {
			settled = append(settled, *event.Sample)
		}
	})
	require.Equal(t, 3, calls)
	require.Zero(t, followed)
	require.Len(t, settled, 3)
	for _, sample := range settled {
		require.Equal(t, "failed", sample.State)
		require.Contains(t, sample.Error, "307")
	}
}

func TestSamplingUsesSelectedEffortWithoutChangingChallenge(t *testing.T) {
	for _, protocol := range []string{"responses", "claude", "openai", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			service, id := newFixtureService(t, config.UpstreamConfig{ServiceType: protocol, BaseURL: "https://example.invalid", APIKeys: []string{"key"}})
			target, err := service.Prepare(id, Request{Model: "requested", Effort: "low"})
			require.NoError(t, err)
			req, err := buildRequest(context.Background(), target, "原始挑战")
			require.NoError(t, err)
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.NoError(t, req.Body.Close())
			paths := map[string]string{"responses": "reasoning.effort", "claude": "output_config.effort", "openai": "reasoning_effort", "gemini": "generationConfig.thinkingConfig.thinkingLevel"}
			require.Equal(t, "low", gjson.GetBytes(body, paths[protocol]).String())
			contentPaths := map[string]string{"responses": "input.0.content", "claude": "messages.0.content", "openai": "messages.0.content", "gemini": "contents.0.parts.0.text"}
			require.Equal(t, "原始挑战", gjson.GetBytes(body, contentPaths[protocol]).String())
		})
	}
}

func TestCancelledDetectionDoesNotSampleOrScore(t *testing.T) {
	service, id := newFixtureService(t, config.UpstreamConfig{ServiceType: "openai", BaseURL: "http://127.0.0.1:1", APIKeys: []string{"key"}})
	target, err := service.Prepare(id, Request{Model: "requested"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service.Run(ctx, target, func(event Event) { require.NotEqual(t, "sample", event.Type) })
	require.Empty(t, service.detector.(*referenceFixture).outputs)
}

func TestRetryOnlyRequestsFailedSampleAndScoresWithSavedReplies(t *testing.T) {
	calls := 0
	var actualPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取采样请求失败：%v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		actualPrompt = gjson.GetBytes(body, "messages.0.content").String()
		w.Header().Set("Content-Type", "application/json")
		_, err = fmt.Fprintf(w, `{"model":"reported","choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, strings.Repeat("247,83,319,", 110))
		if err != nil {
			t.Errorf("写入采样回答失败：%v", err)
		}
	}))
	defer server.Close()
	service, id := newFixtureService(t, config.UpstreamConfig{ServiceType: "openai", BaseURL: server.URL, APIKeys: []string{"key"}, ProxyMode: "direct"})
	target, err := service.Prepare(id, Request{Model: "requested", Effort: "low"})
	require.NoError(t, err)
	previous := Task{Target: target, DataBuilt: "fixture-version", Samples: []Sample{
		{Index: 0, State: "complete", Prompt: "第一道", Text: "保留第一条", ExpectedCount: 302},
		{Index: 1, State: "failed", Prompt: "原始第二道挑战", ExpectedCount: 322, Error: "TLS handshake timeout"},
		{Index: 2, State: "complete", Prompt: "第三道", Text: "保留第三条", ExpectedCount: 330},
	}}
	var events []Event
	service.Retry(context.Background(), target, previous, 1, func(event Event) { events = append(events, event) })
	require.Equal(t, 1, calls)
	require.Equal(t, "原始第二道挑战", actualPrompt)
	require.Equal(t, "result", events[len(events)-1].Type)
	outputs := service.detector.(*referenceFixture).outputs
	require.Len(t, outputs, 3)
	require.Equal(t, "保留第一条", outputs[0].Text)
	require.NotEmpty(t, outputs[1].Text)
	require.Equal(t, 322, outputs[1].ExpectedCount)
	require.Equal(t, "保留第三条", outputs[2].Text)
}

func TestRetryRejectsChangedReferenceBeforeSendingRequest(t *testing.T) {
	service, id := newFixtureService(t, config.UpstreamConfig{ServiceType: "openai", BaseURL: "https://example.invalid", APIKeys: []string{"key"}})
	target, err := service.Prepare(id, Request{Model: "requested"})
	require.NoError(t, err)
	previous := Task{Target: target, DataBuilt: "different-version", Samples: []Sample{{Index: 0, State: "failed", Prompt: "原挑战", ExpectedCount: 302}}}
	var events []Event
	service.Retry(context.Background(), target, previous, 0, func(event Event) { events = append(events, event) })
	require.Len(t, events, 1)
	require.Equal(t, "error", events[0].Type)
	require.Contains(t, events[0].Error, "参考库版本已变化")
	require.Empty(t, service.detector.(*referenceFixture).outputs)
}

func TestRetrySamples_ContinuesAfterFailureAndKeepsSuccessfulReply(t *testing.T) {
	var prompts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取补测请求失败：%v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		prompt := gjson.GetBytes(body, "messages.0.content").String()
		prompts = append(prompts, prompt)
		if prompt == "仍然失败" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, strings.Repeat("247,83,319,", 110)); err != nil {
			t.Errorf("写入补测回答失败：%v", err)
		}
	}))
	defer server.Close()
	service, id := newFixtureService(t, config.UpstreamConfig{ServiceType: "openai", BaseURL: server.URL, APIKeys: []string{"key"}, ProxyMode: "direct"})
	target, err := service.Prepare(id, Request{Model: "requested"})
	require.NoError(t, err)
	previous := Task{Target: target, DataBuilt: "fixture-version", Samples: []Sample{
		{Index: 0, State: "complete", Text: "保留成功回答", ExpectedCount: 302},
		{Index: 1, State: "failed", Prompt: "仍然失败", ExpectedCount: 322},
		{Index: 2, State: "cancelled", Prompt: "补测第三条", ExpectedCount: 330},
	}}
	var events []Event
	service.RetrySamples(context.Background(), target, previous, []int{1, 2}, func(event Event) { events = append(events, event) })
	require.Equal(t, []string{"仍然失败", "补测第三条"}, prompts)
	results := 0
	for _, event := range events {
		if event.Type == "result" {
			results++
		}
	}
	require.Equal(t, 1, results, "所有失败项补测后只应生成一次最终评分")
	outputs := service.detector.(*referenceFixture).outputs
	require.Equal(t, "保留成功回答", outputs[0].Text)
	require.Empty(t, outputs[1].Text)
	require.NotEmpty(t, outputs[2].Text)
}

func TestRestoreRetryTargetRequiresUnchangedURLAndCredentials(t *testing.T) {
	service, id := newFixtureService(t, config.UpstreamConfig{ServiceType: "openai", BaseURL: "https://example.invalid", APIKeys: []string{"key"}})
	target, err := service.Prepare(id, Request{Model: "requested"})
	require.NoError(t, err)
	data, err := json.Marshal(target)
	require.NoError(t, err)
	var saved Target
	require.NoError(t, json.Unmarshal(data, &saved))
	require.Nil(t, saved.upstream)
	restored, err := service.restoreTarget(&saved)
	require.NoError(t, err)
	require.Equal(t, target.ConfigDigest, restored.ConfigDigest)
	saved.ConfigDigest = "old-target-digest"
	_, err = service.restoreTarget(&saved)
	require.ErrorContains(t, err, "无法确认原采样配置未变")
}

func TestRetryRejectsChangedDataEvenWithSameBuildDate(t *testing.T) {
	service, id := newFixtureService(t, config.UpstreamConfig{ServiceType: "openai", BaseURL: "https://example.invalid", APIKeys: []string{"key"}})
	target, err := service.Prepare(id, Request{Model: "requested"})
	require.NoError(t, err)
	previous := Task{Target: target, DataBuilt: "fixture-version", DataSHA256: "old-data-digest",
		Samples: []Sample{{Index: 0, State: "failed", Prompt: "原挑战", ExpectedCount: 302}}}
	var events []Event
	service.Retry(context.Background(), target, previous, 0, func(event Event) { events = append(events, event) })
	require.Len(t, events, 1)
	require.Equal(t, "error", events[0].Type)
	require.Contains(t, events[0].Error, "数据摘要已变化")
	require.Empty(t, service.detector.(*referenceFixture).outputs)
}
