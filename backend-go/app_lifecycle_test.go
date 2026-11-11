package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAppShutdownReversesDependenciesAndKeepsAllErrors(t *testing.T) {
	a := &app{}
	storeErr, workerErr := errors.New("存储关闭失败"), errors.New("维护关闭失败")
	var events []string
	a.registerCleanup("存储", func() error { events = append(events, "store"); return storeErr })
	a.registerCleanup("调度器", func() error { events = append(events, "scheduler"); return nil })
	a.registerCleanup("维护", func() error { events = append(events, "worker"); return workerErr })
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() { results <- a.shutdown() }()
	}
	for i := 0; i < 16; i++ {
		err := <-results
		require.ErrorIs(t, err, storeErr)
		require.ErrorIs(t, err, workerErr)
	}
	require.Equal(t, []string{"worker", "scheduler", "store"}, events)
}

func TestHTTPListenFailureReturns(t *testing.T) {
	err := serveUntilStopped(context.Background(), &http.Server{Addr: "127.0.0.1:-1"}, time.Second)
	require.ErrorContains(t, err, "服务器监听失败")
}

func TestHTTPShutdownWaitsForRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	address := make(chan string, 1)
	srv := &http.Server{Addr: "127.0.0.1:0", BaseContext: func(l net.Listener) context.Context { address <- l.Addr().String(); return context.Background() },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusNoContent)
		})}
	t.Cleanup(func() { releaseRequest(); cancel(); require.NoError(t, srv.Close()) })
	stopped := make(chan error, 1)
	go func() { stopped <- serveUntilStopped(ctx, srv, time.Second) }()
	clientResult := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + <-address)
		if err == nil {
			err = resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				err = errors.New("响应状态错误")
			}
		}
		clientResult <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("请求未开始")
	}
	cancel()
	select {
	case err := <-stopped:
		t.Fatalf("请求结束前服务器已返回: %v", err)
	default:
	}
	releaseRequest()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("服务器未关闭")
	}
	require.NoError(t, <-clientResult)
}

func TestHTTPTimeoutCancelsThenDrainsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	address := make(chan string, 1)
	srv := &http.Server{Addr: "127.0.0.1:0", BaseContext: func(l net.Listener) context.Context { address <- l.Addr().String(); return context.Background() },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-r.Context().Done()
			close(cancelled)
			<-release
		})}
	t.Cleanup(func() { releaseRequest(); cancel(); require.NoError(t, srv.Close()) })
	stopped := make(chan error, 1)
	go func() { stopped <- serveUntilStopped(ctx, srv, 50*time.Millisecond) }()
	clientResult := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + <-address)
		if resp != nil {
			_ = resp.Body.Close()
		}
		clientResult <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("请求未开始")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("关闭超时后未取消请求")
	}
	select {
	case err := <-stopped:
		t.Fatalf("请求退出前服务器已返回: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseRequest()
	select {
	case err := <-stopped:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("服务器未关闭")
	}
	require.Error(t, <-clientResult)
}
