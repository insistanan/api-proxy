package httpclient

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTransportDialer_ConnectsToBothLoopbackFamilies(t *testing.T) {
	for _, tc := range []struct{ network, address string }{{"tcp4", "127.0.0.1:0"}, {"tcp6", "[::1]:0"}} {
		t.Run(tc.network, func(t *testing.T) {
			listener, err := net.Listen(tc.network, tc.address)
			if err != nil && tc.network == "tcp6" {
				t.Skipf("当前环境不能监听 IPv6 回环: %v", err)
			}
			require.NoError(t, err)
			defer listener.Close()
			accepted := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err == nil {
					err = conn.Close()
				}
				accepted <- err
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			transport, err := newTransport(ClientOptions{ResponseHeaderTimeout: time.Second})
			require.NoError(t, err)
			conn, err := transport.(*http.Transport).DialContext(ctx, "tcp", listener.Addr().String())
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			select {
			case err := <-accepted:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("回环连接未被接收")
			}
		})
	}
}
