package config

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"github.com/stretchr/testify/require"
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
