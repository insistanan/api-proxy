package urlhealth

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSortedURLs_ConfigOrderChangesPreserveHealth(t *testing.T) {
	manager := NewURLManager(time.Minute, 3)
	manager.GetSortedURLs(0, []string{"a", "b"})
	require.Equal(t, []Candidate{{"b", 0}, {"a", 1}}, manager.GetSortedURLs(0, []string{"b", "a"}))
	for range 3 {
		manager.MarkFailure(0, "b")
	}
	// 重排和增加地址均不得清除已有的失败；索引必须对应新配置。
	require.Equal(t, []Candidate{{"c", 1}, {"a", 2}, {"b", 0}}, manager.GetSortedURLs(0, []string{"b", "c", "a"}))
	manager.MarkSuccess(0, "b")
	require.Equal(t, []Candidate{{"b", 0}, {"c", 1}, {"a", 2}}, manager.GetSortedURLs(0, []string{"b", "c", "a"}))
}

func TestSortedURLs_FailureThresholdControlsDemotion(t *testing.T) {
	for _, threshold := range []int{1, 3, 5} {
		manager := NewURLManager(time.Minute, threshold)
		urls := []string{"a", "b"}
		manager.GetSortedURLs(0, urls)
		for count := 1; count < threshold; count++ {
			manager.MarkFailure(0, "a")
			require.Equal(t, InConfigOrder(urls), manager.GetSortedURLs(0, urls), "阈值=%d，失败=%d", threshold, count)
		}
		manager.MarkFailure(0, "a")
		require.Equal(t, []Candidate{{"b", 1}, {"a", 0}}, manager.GetSortedURLs(0, urls))
		manager.MarkSuccess(0, "a")
		require.Equal(t, InConfigOrder(urls), manager.GetSortedURLs(0, urls))
	}
}

func TestSortedURLs_CooldownExpiryPromotesRetryCandidate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := NewURLManager(time.Minute, 1)
		urls := []string{"a", "b", "c"}
		manager.GetSortedURLs(0, urls)
		manager.MarkFailure(0, "a")
		time.Sleep(30 * time.Second)
		manager.MarkFailure(0, "b")
		time.Sleep(30 * time.Second)
		// c 健康优先；a 刚好到期，在仍冷却的 b 之前。
		require.Equal(t, []Candidate{{"c", 2}, {"a", 0}, {"b", 1}}, manager.GetSortedURLs(0, urls))
		manager.MarkFailure(0, "a")
		require.Equal(t, []Candidate{{"c", 2}, {"b", 1}, {"a", 0}}, manager.GetSortedURLs(0, urls))
		time.Sleep(time.Minute)
		// 都已到期时，失败较少的地址优先。
		require.Equal(t, []Candidate{{"c", 2}, {"b", 1}, {"a", 0}}, manager.GetSortedURLs(0, urls))
	})
}

func TestSortedURLs_ShrinkingConfigDiscardsRemovedHealth(t *testing.T) {
	manager := NewURLManager(time.Minute, 1)
	urls := []string{"a", "b"}
	manager.GetSortedURLs(0, urls)
	manager.MarkFailure(0, "a")
	manager.GetSortedURLs(0, []string{"b"})
	manager.MarkFailure(0, "a") // 已移除地址的迟到回写不得重建状态。
	require.Equal(t, InConfigOrder(urls), manager.GetSortedURLs(0, urls))
	manager.MarkFailure(0, "a")
	require.Empty(t, manager.GetSortedURLs(0, nil))
	require.Equal(t, InConfigOrder(urls), manager.GetSortedURLs(0, urls))
}

func TestSortedURLs_ChannelStatesAreIndependent(t *testing.T) {
	manager := NewURLManager(time.Minute, 1)
	urls := []string{"a", "b"}
	manager.GetSortedURLs(0, urls)
	manager.GetSortedURLs(1, urls)
	manager.MarkFailure(0, "a")
	require.Equal(t, InConfigOrder(urls), manager.GetSortedURLs(1, urls))
	require.Equal(t, []Candidate{{"b", 1}, {"a", 0}}, manager.GetSortedURLs(0, urls))
}

func TestURLManager_RejectsInvalidPolicy(t *testing.T) {
	require.Panics(t, func() { NewURLManager(0, 3) })
	require.Panics(t, func() { NewURLManager(time.Second, 0) })
}
