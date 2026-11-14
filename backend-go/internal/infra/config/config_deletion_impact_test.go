package config

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelDeletionDetachesVisionReferencesAtomically(t *testing.T) {
	for _, operation := range []string{"channel", "pool"} {
		for _, failSave := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "/success", true: "/save-failure"}[failSave], func(t *testing.T) {
				cm := newTransactionTestManager(t, "messages")
				cm.config.Upstream[0].PoolID = "empty"
				cm.config.Upstream[0].VisionCapable = true
				cm.config.Upstream[0].ExcludeFromConversation = true
				cm.config.Upstream[1].VisionLayerEnabled = true
				cm.config.Upstream[1].VisionLayerChannelID = "first"
				cm.config.Upstream[1].VisionLayerModel = "selected-model"
				owner := cm.config.Upstream[1]
				owner.ID, owner.Name = "cross-protocol", "跨协议引用方"
				cm.config.ChatUpstream = []UpstreamConfig{owner}
				before := cloneConfig(cm.config)
				diskBefore, err := os.ReadFile(cm.configFile)
				require.NoError(t, err)
				index := 0
				var impact ChannelDeletionImpact
				if operation == "channel" {
					impact, err = cm.GetChannelDeletionImpact("messages", &index, "")
				} else {
					impact, err = cm.GetChannelDeletionImpact("messages", nil, "empty")
				}
				require.NoError(t, err)
				require.Equal(t, 1, impact.ChannelCount)
				require.Len(t, impact.Dependents, 2)
				require.True(t, reflect.DeepEqual(before, cm.config), "预览不能修改配置")
				if failSave {
					blockTransactionBackup(t, cm)
				}
				if operation == "channel" {
					_, err = cm.RemoveChannel("messages", index)
				} else {
					err = cm.DeleteChannelPool("messages", "empty")
				}
				if failSave {
					require.Error(t, err)
					require.True(t, reflect.DeepEqual(before, cm.config), "提交失败须保留引用与渠道")
					diskAfter, readErr := os.ReadFile(cm.configFile)
					require.NoError(t, readErr)
					require.Equal(t, diskBefore, diskAfter)
					return
				}
				require.NoError(t, err)
				require.Equal(t, ChannelStatusDeleted, cm.config.Upstream[0].Status)
				assertDetached := func(channel UpstreamConfig) {
					t.Helper()
					require.False(t, channel.VisionCapable)
					require.False(t, channel.VisionLayerEnabled)
					require.Empty(t, channel.VisionLayerChannelID)
					require.Empty(t, channel.VisionLayerModel)
					require.Equal(t, owner.BaseURL, channel.BaseURL)
					require.Equal(t, owner.APIKeys, channel.APIKeys)
				}
				assertDetached(cm.config.Upstream[1])
				assertDetached(cm.config.ChatUpstream[0])
				raw, err := os.ReadFile(cm.configFile)
				require.NoError(t, err)
				var persisted Config
				require.NoError(t, json.Unmarshal(raw, &persisted))
				assertDetached(persisted.ChatUpstream[0])
			})
		}
	}
}

func TestPoolDeletionImpactOmitsDeletedMembersAndInternalReferences(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	for i := range cm.config.Upstream {
		cm.config.Upstream[i].PoolID = "empty"
	}
	cm.config.Upstream[1].VisionLayerEnabled = true
	cm.config.Upstream[1].VisionLayerChannelID = "first"
	cm.config.Upstream = append(cm.config.Upstream, UpstreamConfig{ID: "deleted", PoolID: "empty", Status: ChannelStatusDeleted})
	impact, err := cm.GetChannelDeletionImpact("messages", nil, "empty")
	require.NoError(t, err)
	require.Equal(t, 2, impact.ChannelCount)
	require.Empty(t, impact.Dependents)
}
