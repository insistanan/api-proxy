package scheduler

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBranchAdmission_DeduplicatesAndReleasesAtLastReference(t *testing.T) {
	s := &ChannelScheduler{}
	admit := func(conversationID, branch string, limit int) func() {
		t.Helper()
		release, err := s.AdmitChannelConversation(ChannelKindResponses, "channel", conversationID, branch, limit)
		require.NoError(t, err)
		return release
	}
	main := admit("conversation", "main", 3)
	parallel := admit("conversation", "main", 3)
	side := admit("conversation", "side", 3)
	child := admit("conversation", "side-child", 3)
	release, err := s.AdmitChannelConversation(ChannelKindResponses, "channel", "other", "main", 3)
	require.ErrorIs(t, err, ErrBranchCapacity)
	require.Nil(t, release)
	require.Equal(t, 3, s.ActiveChannelActivity(ChannelKindResponses, "channel").Branches)
	// 热调小只阻止新增分支，不驱逐已在处理的分支。
	continued := admit("conversation", "side", 1)
	continued()
	main()
	main()
	_, err = s.AdmitChannelConversation(ChannelKindResponses, "channel", "other", "main", 3)
	require.ErrorIs(t, err, ErrBranchCapacity, "同分支最后一个引用未结束前不能腾出额度")
	parallel()
	other := admit("other", "main", 3)
	other()
	side()
	child()
	require.Equal(t, ChannelActivity{}, s.ActiveChannelActivity(ChannelKindResponses, "channel"))
	require.Empty(t, s.activeConversations)
}

func TestBranchAdmission_UnknownIdentityAndChannelIsolation(t *testing.T) {
	s := &ChannelScheduler{}
	unknown, err := s.AdmitChannelConversation(ChannelKindMessages, "channel", "conversation", "", 1)
	require.NoError(t, err)
	main, err := s.AdmitChannelConversation(ChannelKindMessages, "channel", "conversation", "main", 1)
	require.NoError(t, err, "补齐分支身份不能被误认为额外占用")
	_, err = s.AdmitChannelConversation(ChannelKindMessages, "channel", "conversation", "side", 1)
	require.ErrorIs(t, err, ErrBranchCapacity)
	for _, candidate := range []channelIdentity{{ChannelKindChat, "channel"}, {ChannelKindMessages, "other"}} {
		release, err := s.AdmitChannelConversation(candidate.kind, candidate.id, "conversation", "side", 1)
		require.NoError(t, err)
		release()
	}
	main()
	require.Equal(t, 1, s.ActiveChannelActivity(ChannelKindMessages, "channel").Branches)
	unknown()
	require.Empty(t, s.activeConversations)
}

func TestBranchAdmission_ConcurrentArrivalsCannotOverbook(t *testing.T) {
	s := &ChannelScheduler{}
	type outcome struct {
		release func()
		err     error
	}
	start := make(chan struct{})
	results := make(chan outcome, 20)
	var arrivals sync.WaitGroup
	for i := range 20 {
		arrivals.Go(func() {
			<-start
			release, err := s.AdmitChannelConversation(ChannelKindMessages, "channel", "conversation", fmt.Sprint(i), 3)
			results <- outcome{release, err}
		})
	}
	close(start)
	arrivals.Wait()
	close(results)
	admitted := 0
	for result := range results {
		if result.err != nil {
			require.ErrorIs(t, result.err, ErrBranchCapacity)
			continue
		}
		admitted++
		result.release()
	}
	require.Equal(t, 3, admitted)
	require.Empty(t, s.activeConversations)
}
