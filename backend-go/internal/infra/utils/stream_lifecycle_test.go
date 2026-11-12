package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCheckStreamCompletion(t *testing.T) {
	resetErr := errors.New("read tcp: wsarecv: An existing connection was forcibly closed by the remote host.")
	for _, readErr := range []error{nil, io.EOF, fmt.Errorf("读取: %w", io.EOF)} {
		require.ErrorIs(t, CheckStreamCompletion(context.Background(), false, readErr), io.ErrUnexpectedEOF)
	}
	require.ErrorIs(t, CheckStreamCompletion(context.Background(), false, resetErr), resetErr)
	require.NoError(t, CheckStreamCompletion(context.Background(), true, resetErr))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, CheckStreamCompletion(ctx, true, resetErr), context.Canceled)
}

type lifecycleBlockingBody struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
	closes    atomic.Int32
}

func (b *lifecycleBlockingBody) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *lifecycleBlockingBody) Close() error {
	b.closes.Add(1)
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestBindStreamBodyCancelUnblocksRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &lifecycleBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
	release := BindStreamBody(ctx, body)
	defer release()
	done := make(chan error, 1)
	go func() { _, err := body.Read(make([]byte, 1)); done <- err }()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("读取未启动")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, io.ErrClosedPipe)
	case <-time.After(time.Second):
		t.Fatal("取消未解除读取阻塞")
	}
	release()
	release()
	require.EqualValues(t, 1, body.closes.Load())
}

func TestDecompressReadCloserCancelUnblocksRead(t *testing.T) {
	source := &lifecycleBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
	decoder := &lifecycleBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
	wrapper := &decompressReadCloser{reader: source, sourceCloser: source, readerCloser: decoder}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := BindStreamBody(ctx, wrapper)
	defer release()
	done := make(chan error, 1)
	go func() { _, err := wrapper.Read(make([]byte, 1)); done <- err }()
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("压缩流读取未启动")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, io.ErrClosedPipe)
	case <-time.After(time.Second):
		t.Fatal("压缩流取消后仍阻塞")
	}
	release()
	require.NoError(t, wrapper.Close())
	require.EqualValues(t, 1, source.closes.Load())
	require.EqualValues(t, 1, decoder.closes.Load())
}
