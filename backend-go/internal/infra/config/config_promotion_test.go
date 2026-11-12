package config

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPromotionLimitsPersistAndEitherLimitEndsPromotion(t *testing.T) {
	for _, kind := range []string{"messages", "responses", "gemini", "chat", "images"} {
		t.Run(kind, func(t *testing.T) {
			cm := newTransactionTestManager(t, kind)
			require.NoError(t, cm.SetChannelPromotionFor(kind, 0, time.Hour, 2))
			var persisted Config
			raw, err := os.ReadFile(cm.configFile)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &persisted))
			cs, err := persisted.channelSetFor(kind)
			require.NoError(t, err)
			require.NotNil(t, (*cs.upstreams)[0].PromotionUntil)
			require.Equal(t, 2, (*cs.upstreams)[0].PromotionCount)

			cm.ConsumePromotionCount(0, kind)
			snapshot := cm.GetConfig()
			cs, err = snapshot.channelSetFor(kind)
			require.NoError(t, err)
			require.Equal(t, 1, (*cs.upstreams)[0].PromotionCount)
			require.True(t, IsChannelInPromotion(&(*cs.upstreams)[0]))
			cm.ConsumePromotionCount(0, kind)
			snapshot = cm.GetConfig()
			cs, err = snapshot.channelSetFor(kind)
			require.NoError(t, err)
			require.Nil(t, (*cs.upstreams)[0].PromotionUntil)
			require.False(t, IsChannelInPromotion(&(*cs.upstreams)[0]))

			require.NoError(t, cm.SetChannelPromotionFor(kind, 0, time.Hour, 2))
			require.NoError(t, cm.runChannelLifecycle(time.Now().Add(2*time.Hour), false))
			snapshot = cm.GetConfig()
			cs, err = snapshot.channelSetFor(kind)
			require.NoError(t, err)
			require.Nil(t, (*cs.upstreams)[0].PromotionUntil)
			require.Zero(t, (*cs.upstreams)[0].PromotionCount)
		})
	}
}

func TestExpiredPromotionDoesNotContinueByRemainingCount(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	channels := []UpstreamConfig{{PromotionUntil: &expired, PromotionCount: 3}}
	require.False(t, IsChannelInPromotion(&channels[0]))
	require.False(t, consumePromotionOp(channels, 0, "test"))
	require.Equal(t, 3, channels[0].PromotionCount)
}

func TestInvalidPromotionLimitsKeepPublishedConfig(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	before := cm.GetConfig()
	require.Error(t, cm.SetChannelPromotionFor("messages", 0, -time.Second, 1))
	require.Error(t, cm.SetChannelPromotionFor("messages", 0, time.Second, -1))
	require.Equal(t, before, cm.GetConfig())
}
