package session

import (
	"sync"
	"testing"
	"time"
)

func TestAffinityStopConcurrentWaitsForLoop(t *testing.T) {
	for _, tc := range []struct {
		name   string
		create func() (func(), <-chan struct{})
	}{
		{"Trace", func() (func(), <-chan struct{}) { m := NewTraceAffinityManager(); return m.Stop, m.doneCh }},
		{"BaseURL", func() (func(), <-chan struct{}) { m := NewBaseURLAffinityManager(); return m.Stop, m.doneCh }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stop, done := tc.create()
			t.Cleanup(stop)
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); stop() }()
			}
			completed := make(chan struct{})
			go func() { wg.Wait(); close(completed) }()
			select {
			case <-completed:
			case <-time.After(5 * time.Second):
				t.Fatal("并发停止未结束")
			}
			select {
			case <-done:
			default:
				t.Fatal("Stop 返回后后台循环尚未退出")
			}
		})
	}
}
