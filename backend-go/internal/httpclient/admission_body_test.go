package httpclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/stretchr/testify/require"
)

func TestStreamHoldsSlotUntilBodyClose(t *testing.T) {
	id := upstreamcontrol.NewIdentity("https://example.com", "responses", "fixture")
	controller := upstreamcontrol.NewController()
	controller.Reconcile(map[upstreamcontrol.Identity]int{id: 1})
	ctx := upstreamcontrol.WithCandidate(upstreamcontrol.WithScope(context.Background()), controller, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com/v1/responses", nil)
	require.NoError(t, err)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: event\n\n")), Request: request}, nil
	})}
	response, err := sendWithAdmission(client, req)
	require.NoError(t, err)
	waitCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	acquired := make(chan *upstreamcontrol.Lease, 1)
	go func() {
		lease, _, _ := controller.Acquire(waitCtx, id)
		acquired <- lease
	}()
	select {
	case lease := <-acquired:
		lease.Release()
		t.Fatal("收到响应头就提前释放了流式名额")
	case <-time.After(20 * time.Millisecond):
	}
	require.NoError(t, response.Body.Close())
	select {
	case lease := <-acquired:
		require.NotNil(t, lease)
		lease.Release()
	case <-time.After(time.Second):
		t.Fatal("关闭响应后没有归还名额")
	}
}

func TestCancelledBodyReturnsSlotEvenWithoutConsumerRead(t *testing.T) {
	id := upstreamcontrol.NewIdentity("https://example.com", "responses", "fixture")
	controller := upstreamcontrol.NewController()
	controller.Reconcile(map[upstreamcontrol.Identity]int{id: 1})
	ctx, cancel := context.WithCancel(context.Background())
	lease, _, err := controller.Acquire(ctx, id)
	require.NoError(t, err)
	body := newAdmissionBody(ctx, io.NopCloser(strings.NewReader("stream")), lease)
	cancel()
	waitCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	next, _, err := controller.Acquire(waitCtx, id)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	next.Release()
}
