package visionlayer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/protocol/visionmedia"
	"github.com/insistanan/api-proxy/internal/storage/sqlitedb"
	"github.com/stretchr/testify/require"
)

func TestVisionCache_ResolvedModelAndQuestionControlReuse(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		calls.Add(1)
		_, _ = w.Write([]byte(strings.ReplaceAll(visionSuccessBody, "图片中有一只猫", request.Model)))
	}))
	t.Cleanup(server.Close)
	cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: server.URL, APIKeys: []string{"key"}, ModelMapping: map[string][]string{
			"alias": {"model-a"}, "same-model": {"model-a"},
		}},
	})
	conversationID := t.Name()
	t.Cleanup(func() { ClearConversationCache(conversationID) })
	call := func(inputModel, question string) string {
		t.Helper()
		result, err := describeImages(ctx, config.DefaultEnvConfig(), cfg, sch, scheduler.ChannelKindMessages, config.DefaultChannelPoolID, "vision", inputModel, conversationID, resolveVisionAnalysisProfile(question), images)
		require.NoError(t, err)
		require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
		return result["image_1"]
	}
	require.Equal(t, "model-a", call("alias", "看颜色"))
	require.Equal(t, "model-a", call("same-model", "看颜色"))
	require.EqualValues(t, 1, calls.Load(), "不同输入别名映射到同一实际模型时可复用")
	_, err := cfg.UpdateChannel("messages", 0, config.UpstreamUpdate{ModelMapping: map[string][]string{"alias": {"model-b"}}})
	require.NoError(t, err)
	require.Equal(t, "model-b", call("alias", "看颜色"))
	require.EqualValues(t, 2, calls.Load(), "修改模型映射后不得命中旧结果")
	require.Equal(t, "model-b", call("alias", "读取文字"))
	require.EqualValues(t, 3, calls.Load(), "不同分析问题必须重新调用")
	require.Equal(t, "model-b", call("alias", "读取文字"))
	require.EqualValues(t, 3, calls.Load())
}

func TestVisionCache_PartialHitPreservesFullComparisonContext(t *testing.T) {
	var calls atomic.Int32
	received := make(chan []string, 2)
	responseText, err := json.Marshal(`{"images":[{"id":"image_1","description":"第一张观察"},{"id":"image_2","description":"第二张观察"}]}`)
	require.NoError(t, err)
	responseBody := strings.Replace(visionSuccessBody, `"图片中有一只猫"`, string(responseText), 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		blocks, err := collectImagesForUpstream(payload, "claude")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fingerprints := make([]string, 0, len(blocks))
		for _, block := range blocks {
			fingerprints = append(fingerprints, visionmedia.ImageFingerprintForBlock(block))
		}
		calls.Add(1)
		received <- fingerprints
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: server.URL, APIKeys: []string{"key"}},
	})
	secondBlock := map[string]interface{}{
		"type":   "image",
		"source": map[string]interface{}{"type": "base64", "media_type": "image/png", "data": "c2Vjb25k"},
	}
	images = append(images, visionImage{id: "image_2", block: secondBlock, fingerprint: visionmedia.ImageFingerprintForBlock(secondBlock)})
	conversationID := t.Name()
	t.Cleanup(func() { ClearConversationCache(conversationID) })
	profile := resolveVisionAnalysisProfile("比较两张图片")
	selection, err := sch.SelectVisionChannel(ctx.Request.Context(), scheduler.ChannelKindMessages, "vision", config.DefaultChannelPoolID)
	require.NoError(t, err)
	selection.Permit.Release()
	first := images[0]
	first.cacheKey = buildImageCacheKey(first.fingerprint, selection.Upstream, "test", profile, images)
	first.memoryKey = memoryCacheKey(conversationID, first.cacheKey)
	_, _, first.call, _ = claimAnalysis(first.memoryKey)
	defer finishAnalysis(first.memoryKey, first.call, visionAnalysisResult{result: "已有比较结果"})
	require.NoError(t, storeCache(nil, conversationID, first, "已有比较结果"))
	finishAnalysis(first.memoryKey, first.call, visionAnalysisResult{result: "已有比较结果"})
	call := func(batch []visionImage) map[string]string {
		t.Helper()
		result, err := describeImages(ctx, config.DefaultEnvConfig(), cfg, sch, scheduler.ChannelKindMessages, config.DefaultChannelPoolID, "vision", "test", conversationID, profile, batch)
		require.NoError(t, err)
		require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
		return result
	}
	result := call(images)
	require.Equal(t, "已有比较结果", result["image_1"])
	require.Equal(t, "第二张观察", result["image_2"])
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, []string{images[0].fingerprint, images[1].fingerprint}, <-received, "缺失一张结果也必须发送完整比较上下文")
	require.Equal(t, result, call(images))
	require.EqualValues(t, 1, calls.Load(), "完整批次再次请求应全部命中缓存")
	changed := append([]visionImage(nil), images...)
	changed[1].block = map[string]interface{}{
		"type":   "image",
		"source": map[string]interface{}{"type": "base64", "media_type": "image/png", "data": "dGhpcmQ="},
	}
	changed[1].fingerprint = visionmedia.ImageFingerprintForBlock(changed[1].block)
	changedResult := call(changed)
	require.EqualValues(t, 2, calls.Load())
	require.Equal(t, "第一张观察", changedResult["image_1"], "更换另一张图片后不得复用第一张的旧比较结果")
	require.Equal(t, []string{changed[0].fingerprint, changed[1].fingerprint}, <-received)
	require.Equal(t, result, call(images), "不同批次的比较结果应分别缓存")
	require.EqualValues(t, 2, calls.Load())
}

func TestPrepareRequest_KnownImageFailureIsReturned(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_request"}}`))
	}))
	t.Cleanup(server.Close)
	cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: server.URL, APIKeys: []string{"key"}},
	})
	registry := conversation.NewRegistry()
	t.Cleanup(registry.Stop)
	sch.SetConversationRegistry(registry)
	record, err := registry.ObserveRequestWithError(conversation.Observation{
		APIKind: "messages", ConversationID: t.Name(), ImageFingerprints: []string{images[0].fingerprint},
	})
	require.NoError(t, err)
	t.Cleanup(func() { ClearConversationCache(record.ID) })
	body := `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}]}`
	target := &config.UpstreamConfig{ServiceType: "claude", VisionLayerEnabled: true, VisionLayerChannelID: "vision"}
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		err := PrepareRequest(ctx, config.DefaultEnvConfig(), cfg, sch, scheduler.ChannelKindMessages, target, "test", record.ID, request)
		require.Error(t, err)
		status, code := ErrorResponse(err)
		require.Equal(t, http.StatusBadGateway, status)
		require.Equal(t, "VISION_LAYER_UPSTREAM_ERROR", code)
		remaining, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		require.JSONEq(t, body, string(remaining), "失败不能把图片改成占位文本")
		require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
	}
	require.EqualValues(t, 2, calls.Load(), "失败不能被缓存成成功")
}

func TestVisionCache_AbnormalStopReasonIsNotCached(t *testing.T) {
	for _, stopReason := range []string{"max_tokens", "tool_use", "pause_turn", "refusal"} {
		t.Run(stopReason, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := visionSuccessBody
				if calls.Add(1) == 1 {
					body = strings.Replace(body, `"stop_reason":"end_turn"`, `"stop_reason":"`+stopReason+`"`, 1)
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
				{ID: "vision", Name: "vision", BaseURL: server.URL, APIKeys: []string{"key"}},
			})
			conversationID := t.Name()
			t.Cleanup(func() { ClearConversationCache(conversationID) })
			call := func() (map[string]string, error) {
				return describeImages(ctx, config.DefaultEnvConfig(), cfg, sch, scheduler.ChannelKindMessages, config.DefaultChannelPoolID, "vision", "test", conversationID, resolveVisionAnalysisProfile("观察"), images)
			}
			result, err := call()
			require.ErrorContains(t, err, stopReason)
			require.Nil(t, result)
			require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
			result, err = call()
			require.NoError(t, err)
			require.Equal(t, "图片中有一只猫", result["image_1"])
			require.EqualValues(t, 2, calls.Load(), "截断或未完成的内容不能缓存为成功")
			_, err = call()
			require.NoError(t, err)
			require.EqualValues(t, 2, calls.Load(), "完整结果才可复用")
		})
	}
}

func TestVisionCache_ReadFailureReleasesEarlierClaims(t *testing.T) {
	cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: "https://vision.example", APIKeys: []string{"key"}},
	})
	db, err := sqlitedb.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	registry, err := conversation.NewPersistentRegistryWithDB(db)
	require.NoError(t, err)
	t.Cleanup(registry.Stop)
	sch.SetConversationRegistry(registry)
	record, err := registry.ObserveRequestWithError(conversation.Observation{APIKind: "messages", ConversationID: t.Name()})
	require.NoError(t, err)
	t.Cleanup(func() { ClearConversationCache(record.ID) })
	selection, err := sch.SelectVisionChannel(ctx.Request.Context(), scheduler.ChannelKindMessages, "vision", config.DefaultChannelPoolID)
	require.NoError(t, err)
	profile := resolveVisionAnalysisProfile("观察")
	second := images[0]
	second.id = "image_2"
	second.fingerprint = "second-image"
	images = append(images, second)
	secondKey := buildImageCacheKey(second.fingerprint, selection.Upstream, "test", profile, images)
	// 第一张缓存未命中并取得占位；第二张刷新持久化缓存访问时间时显式报错。
	require.NoError(t, registry.SaveConversationImageUnderstanding(record.ID, secondKey, "已有描述"))
	_, err = db.Exec(`CREATE TRIGGER reject_vision_cache_touch BEFORE UPDATE OF last_used_at ON conversation_image_understandings BEGIN SELECT RAISE(ABORT, 'cache unavailable'); END`)
	require.NoError(t, err)
	result := describeImagesOnChannel(ctx, config.DefaultEnvConfig(), cfg, sch, selection, "test", record.ID, profile, images, 2)
	require.ErrorContains(t, result.err, "cache unavailable")
	require.False(t, result.retryable)
	require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
	key := memoryCacheKey(record.ID, buildImageCacheKey(images[0].fingerprint, selection.Upstream, "test", profile, images))
	_, cached, call, owner := claimAnalysis(key)
	t.Cleanup(func() { finishAnalysis(key, call, visionAnalysisResult{err: result.err}) })
	require.False(t, cached)
	require.True(t, owner, "收集失败后下一请求必须能重新取得占位")
}

func TestVisionCache_WaiterPreservesRetryDecisionWithoutHealthFailure(t *testing.T) {
	cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: "https://vision.example", APIKeys: []string{"key"}},
	})
	for _, failure := range []error{errors.New("上游暂时不可用"), context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			selection, err := sch.SelectVisionChannel(ctx.Request.Context(), scheduler.ChannelKindMessages, "vision", config.DefaultChannelPoolID)
			require.NoError(t, err)
			conversationID := t.Name()
			profile := resolveVisionAnalysisProfile("观察")
			key := memoryCacheKey(conversationID, buildImageCacheKey(images[0].fingerprint, selection.Upstream, "test", profile, images))
			synctest.Test(t, func(t *testing.T) {
				_, _, call, owner := claimAnalysis(key)
				require.True(t, owner)
				defer finishAnalysis(key, call, visionAnalysisResult{err: failure, retryable: true})
				finished := make(chan visionAttemptResult, 1)
				go func() {
					finished <- describeImagesOnChannel(ctx, config.DefaultEnvConfig(), cfg, sch, selection, "test", conversationID, profile, images, 2)
				}()
				synctest.Wait()
				// 上游自身超时不等于发起者取消，不能在此处重新调用。
				finishAnalysis(key, call, visionAnalysisResult{err: failure, retryable: true})
				result := <-finished
				require.ErrorIs(t, result.err, failure)
				require.True(t, result.retryable)
				require.Equal(t, visionHealthNeutral, result.health, "等待者未发送上游请求，不应重复记失败")
				require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
			})
		})
	}
}

func TestVisionCache_ActiveWaitersTakeOverCanceledOwner(t *testing.T) {
	for _, ownerErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(ownerErr.Error(), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(visionSuccessBody))
			}))
			t.Cleanup(server.Close)
			cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
				{ID: "vision", Name: "vision", BaseURL: server.URL, APIKeys: []string{"key"}},
			})
			conversationID := t.Name()
			t.Cleanup(func() { ClearConversationCache(conversationID) })
			profile := resolveVisionAnalysisProfile("观察")
			first, err := sch.SelectVisionChannel(ctx.Request.Context(), scheduler.ChannelKindMessages, "vision", config.DefaultChannelPoolID)
			require.NoError(t, err)
			second, err := sch.SelectVisionChannel(ctx.Request.Context(), scheduler.ChannelKindMessages, "vision", config.DefaultChannelPoolID)
			require.NoError(t, err)
			key := memoryCacheKey(conversationID, buildImageCacheKey(images[0].fingerprint, first.Upstream, "test", profile, images))
			synctest.Test(t, func(t *testing.T) {
				// 真实 HTTP 的空闲连接协程不能留到虚拟时钟作用域退出之后。
				defer server.CloseClientConnections()
				_, _, call, owner := claimAnalysis(key)
				require.True(t, owner)
				defer finishAnalysis(key, call, visionAnalysisResult{err: ownerErr, ownerCanceled: true})
				finished := make(chan visionAttemptResult, 2)
				for _, selection := range []*scheduler.SelectionResult{first, second} {
					waiterContext, _ := gin.CreateTestContext(httptest.NewRecorder())
					waiterContext.Request = ctx.Request.Clone(ctx.Request.Context())
					go func() {
						finished <- describeImagesOnChannel(waiterContext, config.DefaultEnvConfig(), cfg, sch, selection, "test", conversationID, profile, images, 2)
					}()
				}
				synctest.Wait()
				require.Zero(t, calls.Load(), "等待期间不重复调用上游")
				finishAnalysis(key, call, visionAnalysisResult{err: ownerErr, ownerCanceled: true})
				for range 2 {
					result := <-finished
					require.NoError(t, result.err)
					require.Equal(t, "图片中有一只猫", result.descriptions["image_1"])
				}
				require.EqualValues(t, 1, calls.Load(), "多个等待者只应产生一次接手调用")
				require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
				keyMetrics := sch.MetricsManager(scheduler.ChannelKindMessages).GetKeyMetrics(server.URL, "key", 0)
				require.NotNil(t, keyMetrics)
				require.Zero(t, keyMetrics.FailureCount)
			})
		})
	}
}

func TestVisionCache_OwnerCancellationPublishesTakeover(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		close(started)
		if err != nil {
			t.Errorf("读取测试请求失败: %v", err)
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: server.URL, APIKeys: []string{"key"}},
	})
	conversationID := t.Name()
	t.Cleanup(func() { ClearConversationCache(conversationID) })
	ownerContext, cancel := context.WithCancel(ctx.Request.Context())
	defer cancel()
	ctx.Request = ctx.Request.WithContext(ownerContext)
	selection, err := sch.SelectVisionChannel(ownerContext, scheduler.ChannelKindMessages, "vision", config.DefaultChannelPoolID)
	require.NoError(t, err)
	profile := resolveVisionAnalysisProfile("观察")
	key := memoryCacheKey(conversationID, buildImageCacheKey(images[0].fingerprint, selection.Upstream, "test", profile, images))
	finished := make(chan visionAttemptResult, 1)
	go func() {
		finished <- describeImagesOnChannel(ctx, config.DefaultEnvConfig(), cfg, sch, selection, "test", conversationID, profile, images, 2)
	}()
	<-started
	_, cached, call, owner := claimAnalysis(key)
	require.False(t, cached)
	require.False(t, owner)
	cancel()
	result := <-finished
	require.ErrorIs(t, result.err, context.Canceled)
	require.False(t, result.retryable)
	<-call.done
	require.True(t, call.ownerCanceled, "取消只终止发起请求，等待者可以接手")
	require.ErrorIs(t, call.err, context.Canceled)
	require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
	keyMetrics := sch.MetricsManager(scheduler.ChannelKindMessages).GetKeyMetrics(server.URL, "key", 0)
	require.NotNil(t, keyMetrics)
	require.Zero(t, keyMetrics.FailureCount)
}

func TestVisionCache_CancelingWaiterLeavesOwnerRunning(t *testing.T) {
	cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: "https://vision.example", APIKeys: []string{"key"}},
	})
	conversationID := t.Name()
	t.Cleanup(func() { ClearConversationCache(conversationID) })
	profile := resolveVisionAnalysisProfile("观察")
	synctest.Test(t, func(t *testing.T) {
		waiterContext, cancel := context.WithCancel(ctx.Request.Context())
		defer cancel()
		ctx.Request = ctx.Request.WithContext(waiterContext)
		selection, err := sch.SelectVisionChannel(waiterContext, scheduler.ChannelKindMessages, "vision", config.DefaultChannelPoolID)
		require.NoError(t, err)
		key := memoryCacheKey(conversationID, buildImageCacheKey(images[0].fingerprint, selection.Upstream, "test", profile, images))
		_, _, call, owner := claimAnalysis(key)
		require.True(t, owner)
		defer finishAnalysis(key, call, visionAnalysisResult{result: "有效描述"})
		finished := make(chan visionAttemptResult, 1)
		go func() {
			finished <- describeImagesOnChannel(ctx, config.DefaultEnvConfig(), cfg, sch, selection, "test", conversationID, profile, images, 2)
		}()
		synctest.Wait()
		cancel()
		result := <-finished
		require.ErrorIs(t, result.err, context.Canceled)
		_, cached, current, owner := claimAnalysis(key)
		require.False(t, cached)
		require.False(t, owner)
		require.Same(t, call, current)
		select {
		case <-call.done:
			t.Fatal("等待者取消不能结束共享分析")
		default:
		}
		storeMemoryCache(key, call, "有效描述")
		finishAnalysis(key, call, visionAnalysisResult{result: "有效描述"})
		text, cached, _, _ := claimAnalysis(key)
		require.True(t, cached)
		require.Equal(t, "有效描述", text)
		require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
	})
}

type visionCacheStoreStub struct {
	load func(string, string) (string, bool, error)
	save func(string, string, string) error
}

func (s visionCacheStoreStub) LoadConversationImageUnderstanding(conversationID, key string) (string, bool, error) {
	return s.load(conversationID, key)
}

func (s visionCacheStoreStub) SaveConversationImageUnderstanding(conversationID, key, result string) error {
	return s.save(conversationID, key, result)
}

func TestVisionCache_ClearPreventsInflightRefill(t *testing.T) {
	for _, scope := range []string{"conversation", "all"} {
		for _, operation := range []string{"load", "save"} {
			for _, replacement := range []string{"none", "pending", "cached"} {
				t.Run(scope+"/"+operation+"/"+replacement, func(t *testing.T) {
					conversationID := t.Name()
					t.Cleanup(func() { ClearConversationCache(conversationID) })
					key := memoryCacheKey(conversationID, "image")
					_, _, oldCall, owner := claimAnalysis(key)
					require.True(t, owner)
					defer finishAnalysis(key, oldCall, visionAnalysisResult{result: "旧描述"})
					image := visionImage{cacheKey: "image", memoryKey: key, call: oldCall}
					var newCall *visionInflightCall
					invalidate := func() {
						// 确定性插入“持久层操作已完成、内存尚未回填”的清理时序。
						if scope == "all" {
							ClearCache()
						} else {
							ClearConversationCache(conversationID)
						}
						if replacement == "none" {
							return
						}
						_, cached, call, claimed := claimAnalysis(key)
						require.False(t, cached)
						require.True(t, claimed, "清理后的请求不能等待旧分析")
						require.NotSame(t, oldCall, call)
						newCall = call
						t.Cleanup(func() { finishAnalysis(key, call, visionAnalysisResult{result: "新描述"}) })
						if replacement == "cached" {
							storeMemoryCache(key, call, "新描述")
							finishAnalysis(key, call, visionAnalysisResult{result: "新描述"})
						}
					}
					store := visionCacheStoreStub{
						load: func(string, string) (string, bool, error) {
							invalidate()
							return "旧描述", true, nil
						},
						save: func(string, string, string) error {
							invalidate()
							return nil
						},
					}
					if operation == "load" {
						text, ok, err := loadCache(store, conversationID, image)
						require.NoError(t, err)
						require.True(t, ok)
						require.Equal(t, "旧描述", text, "清理前已开始的读取仍可正常收尾")
					} else {
						require.NoError(t, storeCache(store, conversationID, image, "旧描述"))
					}
					finishAnalysis(key, oldCall, visionAnalysisResult{result: "旧描述"})
					<-oldCall.done
					text, cached, current, claimed := claimAnalysis(key)
					switch replacement {
					case "none":
						require.False(t, cached, "旧结果不能重新填回已清理缓存")
						require.True(t, claimed)
						t.Cleanup(func() { finishAnalysis(key, current, visionAnalysisResult{}) })
					case "pending":
						require.False(t, cached)
						require.False(t, claimed)
						require.Same(t, newCall, current, "旧调用收尾不能删除新占位")
					case "cached":
						require.True(t, cached)
						require.Equal(t, "新描述", text, "旧调用不能覆盖清理后的新结果")
					}
				})
			}
		}
	}
}

func TestVisionCache_ClearConversationPreservesOtherConversations(t *testing.T) {
	conversationID := t.Name()
	otherID := conversationID + "-other"
	t.Cleanup(func() { ClearConversationCache(conversationID) })
	t.Cleanup(func() { ClearConversationCache(otherID) })
	for _, id := range []string{conversationID, otherID} {
		key := memoryCacheKey(id, "cached")
		_, _, call, _ := claimAnalysis(key)
		storeMemoryCache(key, call, "已有描述")
		finishAnalysis(key, call, visionAnalysisResult{result: "已有描述"})
	}
	pendingKey := memoryCacheKey(otherID, "pending")
	_, _, pending, _ := claimAnalysis(pendingKey)
	defer finishAnalysis(pendingKey, pending, visionAnalysisResult{result: "在途描述"})
	ClearConversationCache(conversationID)
	_, cached, newCall, owner := claimAnalysis(memoryCacheKey(conversationID, "cached"))
	require.False(t, cached)
	require.True(t, owner)
	finishAnalysis(memoryCacheKey(conversationID, "cached"), newCall, visionAnalysisResult{})
	text, cached, _, _ := claimAnalysis(memoryCacheKey(otherID, "cached"))
	require.True(t, cached)
	require.Equal(t, "已有描述", text)
	_, cached, current, owner := claimAnalysis(pendingKey)
	require.False(t, cached)
	require.False(t, owner)
	require.Same(t, pending, current)
	storeMemoryCache(pendingKey, pending, "在途描述")
	finishAnalysis(pendingKey, pending, visionAnalysisResult{result: "在途描述"})
	text, cached, _, _ = claimAnalysis(pendingKey)
	require.True(t, cached)
	require.Equal(t, "在途描述", text)
}
