package httpclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

func TestHeaderTimeout_CancelsBlockedSendAndReleasesAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		id := upstreamcontrol.NewIdentity("https://example.com", "responses", "key")
		controller := upstreamcontrol.NewController()
		controller.Reconcile(map[upstreamcontrol.Identity]int{id: 1})
		ctx := upstreamcontrol.WithCandidate(upstreamcontrol.WithScope(context.Background()), controller, id, false)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com", strings.NewReader("large upload"))
		require.NoError(t, err)
		client := &http.Client{Transport: &responseHeaderTransport{timeout: time.Second, inner: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			// 模拟传输尚未写完请求体；单靠 http.Transport.ResponseHeaderTimeout 无法覆盖此阶段。
			defer req.Body.Close()
			<-req.Context().Done()
			return nil, req.Context().Err()
		})}}
		response, err := sendWithAdmission(client, request)
		require.Nil(t, response)
		require.ErrorIs(t, err, ErrResponseHeaderTimeout)
		var timeout net.Error
		require.ErrorAs(t, err, &timeout)
		require.True(t, timeout.Timeout())
		require.NoError(t, ctx.Err(), "阶段超时不能取消入站请求")
		lease, waited, err := controller.Acquire(ctx, id)
		require.NoError(t, err)
		require.Zero(t, waited, "失败发送必须归还名额")
		lease.Release()
	})
}

func TestHeaderTimeout_StopsAtHeadersAndKeepsStreamContextAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var upstreamContext context.Context
		transport := &responseHeaderTransport{timeout: time.Second, inner: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamContext = req.Context()
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: result\n\n")), Request: req}, nil
		})}
		request, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)
		response, err := transport.RoundTrip(request)
		require.NoError(t, err)
		time.Sleep(2 * time.Second) // synctest 虚拟时间，不等待真实时钟。
		require.NoError(t, upstreamContext.Err())
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, "data: result\n\n", string(body))
		require.ErrorIs(t, upstreamContext.Err(), context.Canceled, "EOF 后应释放子 context")
		require.NoError(t, response.Body.Close())
	})
}

func TestHeaderTimeout_CloseWithoutReadingReleasesStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var upstreamContext context.Context
		reader, writer := io.Pipe()
		defer writer.Close()
		transport := &responseHeaderTransport{timeout: time.Second, inner: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamContext = req.Context()
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: reader, Request: req}, nil
		})}
		request, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)
		response, err := transport.RoundTrip(request)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.NoError(t, response.Body.Close(), "重复关闭不应重复释放资源")
		require.ErrorIs(t, upstreamContext.Err(), context.Canceled)
		_, err = writer.Write([]byte("late data"))
		require.ErrorIs(t, err, io.ErrClosedPipe, "未读取正文也必须关闭底层流")
		require.NoError(t, request.Context().Err(), "关闭正文不能取消入站请求")
	})
}

func TestHeaderTimeout_PropagatesCallerCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		transport := &responseHeaderTransport{timeout: time.Minute, inner: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			cancel()
			<-req.Context().Done()
			return nil, req.Context().Err()
		})}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)
		_, err = transport.RoundTrip(request)
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ErrResponseHeaderTimeout)
	})
}
