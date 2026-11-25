package config

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

func TestConcurrencyDefaultsAndInvalidExplicitValue(t *testing.T) {
	var upstream UpstreamConfig
	require.NoError(t, json.Unmarshal([]byte(`{"baseUrl":"https://example.com","apiKeys":["fixture"],"serviceType":"openai"}`), &upstream))
	require.Equal(t, 5, upstream.MaxConcurrency)
	require.Error(t, json.Unmarshal([]byte(`{"maxConcurrency":0}`), &upstream))
	require.Error(t, json.Unmarshal([]byte(`{"maxConcurrency":-1}`), &upstream))
}

func TestConcurrencyMergesAcrossEntrypointsAndUsesMinimum(t *testing.T) {
	first := UpstreamConfig{BaseURL: "https://EXAMPLE.com/", APIKeys: []string{"fixture"}, ServiceType: "responses", MaxConcurrency: 5}
	second := first
	second.BaseURL = "https://example.com"
	second.MaxConcurrency = 2
	manager := &ConfigManager{}
	manager.publishConfigLocked(Config{Upstream: []UpstreamConfig{first}, ResponsesUpstream: []UpstreamConfig{second}})
	current := manager.GetConfig()
	require.Equal(t, 2, current.Upstream[0].EffectiveConcurrency[0].MaxConcurrency)
	require.Equal(t, 2, current.ResponsesUpstream[0].EffectiveConcurrency[0].MaxConcurrency)
	ctx := upstreamcontrol.WithScope(context.Background())
	firstContext := manager.UpstreamRequestContext(ctx, &current.Upstream[0], "fixture")
	secondContext := manager.UpstreamRequestContext(ctx, &current.ResponsesUpstream[0], "fixture")
	firstCandidate, _ := upstreamcontrol.CandidateFromContext(firstContext)
	secondCandidate, _ := upstreamcontrol.CandidateFromContext(secondContext)
	require.Equal(t, firstCandidate.Identity, secondCandidate.Identity)
	require.Same(t, firstCandidate.Controller, secondCandidate.Controller)
	current.Upstream[0].EffectiveConcurrency[0].MaxConcurrency = 999
	require.Equal(t, 2, manager.GetConfig().Upstream[0].EffectiveConcurrency[0].MaxConcurrency)
}

func TestImagesAndChatHaveDifferentActualProtocols(t *testing.T) {
	upstream := UpstreamConfig{BaseURL: "https://example.com", APIKeys: []string{"fixture"}, ServiceType: "openai", MaxConcurrency: 5}
	manager := &ConfigManager{}
	manager.publishConfigLocked(Config{ChatUpstream: []UpstreamConfig{upstream}, ImagesUpstream: []UpstreamConfig{upstream}})
	current := manager.GetConfig()
	require.Equal(t, "openai", current.ChatUpstream[0].EffectiveConcurrency[0].Protocol)
	require.Equal(t, "images", current.ImagesUpstream[0].EffectiveConcurrency[0].Protocol)
}

func TestDeletedTombstoneDoesNotBlockConfigSaveValidation(t *testing.T) {
	require.NoError(t, validateConcurrency(&Config{Upstream: []UpstreamConfig{{ID: "deleted", Status: ChannelStatusDeleted}}}))
}

func TestRequestIdentity_UsesResolvedServiceAddress(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, first, second string
		firstFull, secondFull         bool
		equal                         bool
	}{
		{"默认版本", "responses", "https://example.com", "https://example.com/v1", false, false, true},
		{"完整端点", "responses", "https://example.com", "https://example.com/v1/responses", false, true, true},
		{"默认端口", "openai", "https://EXAMPLE.com:443/", "https://example.com/v1", false, false, true},
		{"HTTP 默认端口", "claude", "http://example.com:80", "http://example.com/v1/messages", false, true, true},
		{"IPv6 默认端口", "responses", "https://[::1]:443", "https://[::1]/v1", false, false, true},
		{"自定义前缀", "responses", "https://example.com/proxy", "https://example.com/proxy/v1/responses", false, true, true},
		{"跳过版本", "responses", "https://example.com#", "https://example.com/responses", false, true, true},
		{"跳过版本不等价", "responses", "https://example.com#", "https://example.com", false, false, false},
		{"相同文本不同模式", "responses", "https://example.com/api", "https://example.com/api", false, true, false},
		{"自定义路径不同", "responses", "https://example.com/a", "https://example.com/b", false, false, false},
		{"非默认端口", "responses", "https://example.com:8443", "https://example.com", false, false, false},
		{"保留完整路径尾斜杠", "responses", "https://example.com/render/", "https://example.com/render", true, true, false},
		{"保留查询参数", "responses", "https://example.com/render?route=a", "https://example.com/render?route=b", true, true, false},
		{"保留路径编码", "responses", "https://example.com/a%2Fb/responses", "https://example.com/a/b/responses", true, true, false},
		{"压缩共用额度", "responses", "https://example.com", "https://example.com/v1/responses/compact", false, true, true},
		{"计数共用额度", "claude", "https://example.com", "https://example.com/v1/messages/count_tokens", false, true, true},
		{"图片编辑共用额度", "images", "https://example.com", "https://example.com/v1/images/edits", false, true, true},
		{"向量端点", "embeddings", "https://example.com", "https://example.com/v1/embeddings", false, true, true},
		{"决策端点", "decisions", "https://example.com", "https://example.com/v1/decisions", false, true, true},
		{"Gemini 默认版本", "gemini", "https://example.com", "https://example.com/v1beta", false, false, true},
		{"Gemini 完整流式地址", "gemini", "https://example.com", "https://example.com/v1beta/models/m:streamGenerateContent?alt=sse", false, true, true},
		{"Gemini 模型共用额度", "gemini", "https://example.com/v1beta/models/a:generateContent", "https://example.com/v1beta/models/b:streamGenerateContent?alt=sse", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := &UpstreamConfig{BaseURL: tc.first, ServiceType: tc.protocol, UseFullURL: tc.firstFull}
			second := &UpstreamConfig{BaseURL: tc.second, ServiceType: tc.protocol, UseFullURL: tc.secondFull}
			firstID, secondID := first.RequestIdentity("key"), second.RequestIdentity("key")
			require.Equal(t, tc.equal, firstID == secondID)
			require.Equal(t, tc.equal, firstID.Fingerprint() == secondID.Fingerprint())
			require.NotEqual(t, firstID, second.RequestIdentity("another-key"))
		})
	}
}

func TestEquivalentAddresses_ShareAdmissionAndCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := UpstreamConfig{BaseURL: "https://example.com", ServiceType: "responses", APIKeys: []string{"key"}, MaxConcurrency: 1}
		second := first
		second.BaseURL, second.UseFullURL, second.MaxConcurrency = "https://example.com/v1/responses", true, 3
		manager := &ConfigManager{}
		manager.publishConfigLocked(Config{Upstream: []UpstreamConfig{first}, ResponsesUpstream: []UpstreamConfig{second}})
		current := manager.GetConfig()
		require.Equal(t, 1, current.ResponsesUpstream[0].EffectiveConcurrency[0].MaxConcurrency)
		firstCtx := manager.UpstreamRequestContext(upstreamcontrol.WithScope(context.Background()), &current.Upstream[0], "key")
		secondCtx := manager.UpstreamRequestContext(upstreamcontrol.WithScope(context.Background()), &current.ResponsesUpstream[0], "key")
		lease, _, _, err := upstreamcontrol.AcquireRequest(firstCtx)
		require.NoError(t, err)
		defer lease.Release()
		type result struct {
			lease *upstreamcontrol.Lease
			err   error
		}
		queued := make(chan result, 1)
		go func() {
			lease, _, _, err := upstreamcontrol.AcquireRequest(secondCtx)
			queued <- result{lease, err}
		}()
		synctest.Wait()
		select {
		case acquired := <-queued:
			acquired.lease.Release()
			t.Fatal("等价地址绕过了共享并发上限")
		default:
		}
		lease.Release()
		acquired := <-queued
		require.NoError(t, acquired.err)
		acquired.lease.Release()
		upstreamcontrol.Record429(firstCtx, http.Header{"Retry-After": {"60"}})
		cooldowns := manager.UpstreamCooldowns()
		require.Equal(t, time.Now().Add(time.Minute).UnixMilli(), cooldowns[current.ResponsesUpstream[0].RequestIdentity("key").Fingerprint()])
		_, waited, _, err := upstreamcontrol.AcquireRequest(secondCtx)
		require.ErrorIs(t, err, upstreamcontrol.ErrQueueTimeout)
		require.Equal(t, 10*time.Second, waited, "另一种地址写法必须等待同一冷却")
	})
}
