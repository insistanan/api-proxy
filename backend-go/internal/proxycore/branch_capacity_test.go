package proxycore

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/stretchr/testify/require"
)

func TestRunProxyRequest_BranchCapacitySkipsFullChannels(t *testing.T) {
	for _, fuzzy := range []bool{false, true} {
		for _, scenario := range []string{"fallback", "all-full", "explicit"} {
			t.Run(fmt.Sprintf("fuzzy=%t/%s", fuzzy, scenario), func(t *testing.T) {
				env := newRunProxyTestEnv(t)
				require.NoError(t, env.cfgManager.SetFuzzyModeForProtocol("messages", fuzzy))
				registry := conversation.NewRegistry()
				t.Cleanup(registry.Stop)
				env.channelScheduler.SetConversationRegistry(registry)
				server := newRecordingUpstreamServer(t, nil)
				addTestUpstream(t, env, "first", server.URL(), "key-a")
				addTestUpstream(t, env, "second", server.URL(), "key-b")
				channels, _, err := env.cfgManager.GetChannelConfig("messages")
				require.NoError(t, err)
				for index, channel := range channels {
					if index == 1 && scenario != "all-full" {
						continue
					}
					for branch := range 3 {
						release, err := env.channelScheduler.AdmitChannelConversation(scheduler.ChannelKindMessages, channel.ID, "occupied", fmt.Sprint(branch), 3)
						require.NoError(t, err)
						t.Cleanup(release)
					}
				}
				rec := &specRecorder{}
				body := `{"model":"test-model","messages":[{"role":"user","content":"新分支"}]}`
				if scenario == "explicit" {
					body = strings.TrimSuffix(body, "}") + `,"metadata":{"channel_index":0}}`
				}
				response := performRunProxyRequest(t, env, rec.newSpec(), body, "test-access-key")
				if scenario == "fallback" {
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					require.Equal(t, []string{"key-b"}, server.receivedKeys(), "已满渠道不能发出上游请求")
				} else {
					require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
					require.Contains(t, response.Body.String(), "channel_branch_capacity_exceeded")
					require.Empty(t, server.receivedKeys())
				}
				for index := range channels {
					require.Zero(t, env.channelScheduler.GetChannelInFlight(scheduler.ChannelKindMessages, index))
				}
			})
		}
	}
}
