package pipeline

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestCmdGateClose_CancelsChildrenAndWaitsForTheirCleanup(t *testing.T) {
	gate := NewCmdGate(nil, nil, nil, nil)
	ctx, finish, err := gate.beginTask(context.Background())
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- gate.Close() }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("派生任务没有收到取消")
	}
	select {
	case <-done:
		t.Fatal("关闭没有等待派生任务收尾")
	default:
	}
	_, _, err = gate.beginTask(context.Background())
	require.Error(t, err)
	finish()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("收尾结束后关闭没有返回")
	}
}
